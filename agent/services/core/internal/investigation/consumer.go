// Package investigation wires the kubesense.investigations.requests Kafka topic
// to the RCA engine and publishes results on kubesense.investigations.results.
//
// Flow:
//   AlertHub → kubesense.investigations.requests
//     → Consumer.processRequest()
//       → rca.Engine.Investigate()
//       → Producer.publishResult()
//     → kubesense.investigations.results
//   AlertHub → merges into incident rca_confidence
package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"github.com/aileron-platform/aileron/agent/services/core/internal/rca"
)

const (
	TopicRequests = "kubesense.investigations.requests"
	TopicResults  = "kubesense.investigations.results"
	ConsumerGroup = "kubesense-investigation-consumer"
)

// Request is the investigation request payload from AlertHub.
// Must match internal/services/kubesense/types.go InvestigationRequest in AlertHub.
type Request struct {
	ID            string    `json:"id"`
	RequestedAt   time.Time `json:"requested_at"`
	IncidentID    string    `json:"incident_id"`
	ClusterID     string    `json:"cluster_id"`
	Namespace     string    `json:"namespace,omitempty"`
	ResourceKind  string    `json:"resource_kind,omitempty"`
	ResourceName  string    `json:"resource_name,omitempty"`
	Severity      string    `json:"severity"`
	AlertTitle    string    `json:"alert_title"`
	CallbackTopic string    `json:"callback_topic"`
}

// Result is the investigation result payload published back to AlertHub.
// Must match internal/services/kubesense/types.go InvestigationResult in AlertHub.
type Result struct {
	ID            string      `json:"id"`
	CompletedAt   time.Time   `json:"completed_at"`
	IncidentID    string      `json:"incident_id"`
	ClusterID     string      `json:"cluster_id"`
	Grade         string      `json:"grade"`          // A/B/C/D/F
	Confidence    float64     `json:"confidence"`     // 0.0–1.0
	RootCause     string      `json:"root_cause"`
	Summary       string      `json:"summary"`
	Hypotheses    []Hypothesis `json:"hypotheses,omitempty"`
	EvidenceCount int         `json:"evidence_count"`
	RCADurationMs int64       `json:"rca_duration_ms"`
}

// Hypothesis is a single ranked hypothesis in the result.
type Hypothesis struct {
	EntityID      string  `json:"entity_id"`
	EntityKind    string  `json:"entity_kind"`
	EntityName    string  `json:"entity_name"`
	Namespace     string  `json:"namespace"`
	FailureMode   string  `json:"failure_mode"`
	Confidence    float64 `json:"confidence"`
	SupportingEvid int    `json:"supporting_evidence"`
	RefutingEvid  int     `json:"refuting_evidence"`
}

// Consumer consumes investigation requests from AlertHub and drives the RCA engine.
type Consumer struct {
	engine   *rca.Engine
	producer sarama.SyncProducer
	brokers  []string
}

// NewConsumer creates the investigation consumer + producer pair.
func NewConsumer(brokers []string, engine *rca.Engine) (*Consumer, error) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.Producer.RequiredAcks = sarama.WaitForLocal
	cfg.Producer.Return.Successes = true
	cfg.Producer.Retry.Max = 5
	cfg.Net.DialTimeout = 5 * time.Second

	producer, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("investigation producer: %w", err)
	}
	return &Consumer{engine: engine, producer: producer, brokers: brokers}, nil
}

// Run starts the consumer group and blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.BalanceStrategyRoundRobin}
	cfg.Consumer.Group.Session.Timeout = 30 * time.Second
	cfg.Consumer.Group.Heartbeat.Interval = 10 * time.Second

	group, err := sarama.NewConsumerGroup(c.brokers, ConsumerGroup, cfg)
	if err != nil {
		log.Printf("investigation consumer: failed to create group (brokers=%s): %v — investigations disabled",
			strings.Join(c.brokers, ","), err)
		return
	}
	defer group.Close()

	log.Printf("investigation consumer: ready on topic=%s group=%s", TopicRequests, ConsumerGroup)
	handler := &cgHandler{consumer: c}
	for {
		if err := group.Consume(ctx, []string{TopicRequests}, handler); err != nil {
			log.Printf("investigation consumer: group error: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// Close shuts down the Kafka producer.
func (c *Consumer) Close() error { return c.producer.Close() }

// cgHandler implements sarama.ConsumerGroupHandler.
type cgHandler struct{ consumer *Consumer }

func (h *cgHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *cgHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *cgHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		h.consumer.processMessage(sess.Context(), msg)
		sess.MarkMessage(msg, "")
	}
	return nil
}

func (c *Consumer) processMessage(ctx context.Context, msg *sarama.ConsumerMessage) {
	var req Request
	if err := json.Unmarshal(msg.Value, &req); err != nil {
		log.Printf("investigation consumer: malformed request partition=%d offset=%d: %v",
			msg.Partition, msg.Offset, err)
		return
	}
	if req.IncidentID == "" || req.ClusterID == "" {
		log.Printf("investigation consumer: skipping request missing incident_id or cluster_id")
		return
	}

	log.Printf("investigation consumer: processing request id=%s incident=%s cluster=%s severity=%s",
		req.ID, req.IncidentID, req.ClusterID, req.Severity)

	// Build the RCA engine request from the AlertHub investigation request.
	rcaReq := rca.Request{
		ClusterID:    req.ClusterID,
		IncidentTime: req.RequestedAt,
		AlertContext: map[string]string{
			"incident_id": req.IncidentID,
			"severity":    req.Severity,
			"title":       req.AlertTitle,
		},
	}
	// If namespace + kind + name are provided, use them as the affected resource.
	// Otherwise fall back to an empty chain — RCA will use cluster-wide event scan.
	if req.ResourceKind != "" && req.ResourceName != "" {
		rcaReq.AffectedResources = []rca.ResourceRef{{
			Kind:      req.ResourceKind,
			Namespace: req.Namespace,
			Name:      req.ResourceName,
		}}
	}
	// Use RequestedAt as incident time; fall back to now if zero.
	if rcaReq.IncidentTime.IsZero() {
		rcaReq.IncidentTime = time.Now().UTC().Add(-5 * time.Minute)
	}

	result, err := c.engine.Investigate(ctx, rcaReq)
	if err != nil {
		log.Printf("investigation consumer: RCA engine failed incident=%s: %v", req.IncidentID, err)
		c.publishResult(req.IncidentID, req.ClusterID, &rca.Result{})
		return
	}

	log.Printf("investigation consumer: RCA complete incident=%s confidence=%.2f hypotheses=%d evidence=%d duration=%s",
		req.IncidentID, result.Confidence, len(result.AllHypotheses), len(result.Evidence), result.Duration)
	c.publishResult(req.IncidentID, req.ClusterID, result)

	// Publish IncidentContext to kubesense.correlation.incident-context so the
	// LLM narrator (kubesense-llm service) generates a human-readable narrative.
	if c.producer != nil {
		c.publishIncidentContext(req, result)
	}
}

func (c *Consumer) publishResult(incidentID, clusterID string, result *rca.Result) {
	grade := confidenceToGrade(result.Confidence)
	rootCause := ""
	summary := ""
	if result.RootCause != nil {
		rootCause = fmt.Sprintf("%s/%s/%s: %s",
			result.RootCause.EntityKind, result.RootCause.EntityNS, result.RootCause.EntityName,
			result.RootCause.FailureMode)
		if rootCause == "//: " {
			// No entity info — use best hypothesis description if available
			rootCause = result.RootCause.EntityID
		}
		summary = buildSummary(result)
	} else {
		rootCause = "Investigation completed with insufficient evidence for a conclusive root cause"
		summary = rootCause
	}

	hypotheses := make([]Hypothesis, 0, len(result.AllHypotheses))
	for _, h := range result.AllHypotheses {
		hypotheses = append(hypotheses, Hypothesis{
			EntityID:      h.EntityID,
			EntityKind:    h.EntityKind,
			EntityName:    h.EntityName,
			Namespace:     h.EntityNS,
			FailureMode:   h.FailureMode,
			Confidence:    h.Confidence,
			SupportingEvid: len(h.SupportingEvidence),
			RefutingEvid:   len(h.RefutingEvidence),
		})
	}

	out := Result{
		ID:            uuid.New().String(),
		CompletedAt:   time.Now().UTC(),
		IncidentID:    incidentID,
		ClusterID:     clusterID,
		Grade:         grade,
		Confidence:    result.Confidence,
		RootCause:     rootCause,
		Summary:       summary,
		Hypotheses:    hypotheses,
		EvidenceCount: len(result.Evidence),
		RCADurationMs: result.Duration.Milliseconds(),
	}

	data, err := json.Marshal(out)
	if err != nil {
		log.Printf("investigation: marshal result failed incident=%s: %v", incidentID, err)
		return
	}
	_, _, err = c.producer.SendMessage(&sarama.ProducerMessage{
		Topic: TopicResults,
		Key:   sarama.StringEncoder(incidentID), // partition by incident
		Value: sarama.ByteEncoder(data),
	})
	if err != nil {
		log.Printf("investigation: publish result failed incident=%s: %v", incidentID, err)
		return
	}
	log.Printf("investigation: result published incident=%s grade=%s confidence=%.2f",
		incidentID, grade, result.Confidence)
}

func confidenceToGrade(c float64) string {
	switch {
	case c >= 0.88:
		return "A"
	case c >= 0.72:
		return "B"
	case c >= 0.55:
		return "C"
	case c >= 0.35:
		return "D"
	default:
		return "F"
	}
}

func buildSummary(result *rca.Result) string {
	if result.RootCause == nil {
		return "No conclusive root cause identified."
	}
	rc := result.RootCause
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Root cause: %s/%s", rc.EntityKind, rc.EntityName))
	if rc.EntityNS != "" {
		sb.WriteString(fmt.Sprintf(" in namespace %s", rc.EntityNS))
	}
	if rc.FailureMode != "" {
		sb.WriteString(fmt.Sprintf(" (%s)", rc.FailureMode))
	}
	sb.WriteString(fmt.Sprintf(". Confidence: %.0f%%.", result.Confidence*100))
	if len(result.RecentChanges) > 0 {
		ch := result.RecentChanges[0]
		if ch.SecondsBeforeIncident > 0 && ch.SecondsBeforeIncident < 3600 {
			sb.WriteString(fmt.Sprintf(" A %s on %s/%s was deployed %ds before the incident.",
				ch.ChangeType, ch.ResourceKind, ch.ResourceName, ch.SecondsBeforeIncident))
		}
	}
	if len(result.AllHypotheses) > 1 {
		sb.WriteString(fmt.Sprintf(" %d alternative hypotheses evaluated.", len(result.AllHypotheses)-1))
	}
	s := sb.String()
	if len(s) > 400 {
		s = s[:400]
	}
	return s
}

const topicIncidentContext = "kubesense.correlation.incident-context"

// publishIncidentContext publishes an IncidentContext to the LLM narrator topic.
// The LLM narrator (kubesense-llm) consumes this and generates a human-readable
// narrative, then publishes to kubesense.llm.narratives.
func (c *Consumer) publishIncidentContext(req Request, result *rca.Result) {
	type signal struct {
		Source       string    `json:"source"`
		Type         string    `json:"type"`
		Description  string    `json:"description"`
		Strength     float64   `json:"strength"`
		ResourceKind string    `json:"resource_kind"`
		ResourceName string    `json:"resource_name"`
		Namespace    string    `json:"namespace"`
		Timestamp    time.Time `json:"timestamp"`
	}
	type rootCauseSummary struct {
		EntityKind     string  `json:"entity_kind"`
		EntityLabel    string  `json:"entity_label"`
		Confidence     float64 `json:"confidence"`
		ConfidenceBand string  `json:"confidence_band"`
	}
	type incidentContext struct {
		IncidentID         string            `json:"incident_id"`
		ClusterID          string            `json:"cluster_id"`
		Title              string            `json:"title"`
		DetectedAt         time.Time         `json:"detected_at"`
		Signals            []signal          `json:"signals"`
		EvidenceGrade      string            `json:"evidence_grade"`
		Confidence         float64           `json:"confidence"`
		RootCause          *rootCauseSummary `json:"root_cause,omitempty"`
		RecommendedActions []string          `json:"recommended_actions"`
	}

	var signals []signal
	for _, ev := range result.Evidence {
		sig := signal{
			Source:       ev.Source,
			Type:         ev.Type,
			Description:  ev.Description,
			Strength:     ev.Strength,
			ResourceKind: ev.ResourceRef.Kind,
			ResourceName: ev.ResourceRef.Name,
			Namespace:    ev.ResourceRef.Namespace,
			Timestamp:    ev.OccurredAt,
		}
		signals = append(signals, sig)
	}

	var rc *rootCauseSummary
	if result.RootCause != nil {
		band := "LOW"
		switch {
		case result.Confidence >= 0.85:
			band = "CONFIRMED"
		case result.Confidence >= 0.65:
			band = "LIKELY"
		case result.Confidence >= 0.45:
			band = "POSSIBLE"
		}
		rc = &rootCauseSummary{
			EntityKind:     result.RootCause.EntityKind,
			EntityLabel:    result.RootCause.EntityName + "/" + result.RootCause.EntityNS,
			Confidence:     result.Confidence,
			ConfidenceBand: band,
		}
	}

	ic := incidentContext{
		IncidentID:    req.IncidentID,
		ClusterID:     req.ClusterID,
		Title:         req.AlertTitle,
		DetectedAt:    req.RequestedAt,
		Signals:       signals,
		EvidenceGrade: confidenceToGrade(result.Confidence),
		Confidence:    result.Confidence,
		RootCause:     rc,
	}

	data, err := json.Marshal(ic)
	if err != nil {
		log.Printf("investigation: marshal incidentContext failed: %v", err)
		return
	}
	_, _, err = c.producer.SendMessage(&sarama.ProducerMessage{
		Topic: topicIncidentContext,
		Key:   sarama.StringEncoder(req.IncidentID),
		Value: sarama.ByteEncoder(data),
	})
	if err != nil {
		log.Printf("investigation: publish incidentContext failed incident=%s: %v", req.IncidentID, err)
		return
	}
	log.Printf("investigation: incidentContext published incident=%s → LLM narrator", req.IncidentID)
}
