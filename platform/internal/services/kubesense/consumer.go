package kubesense

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

// IncidentUpdater is a narrow interface so the Consumer can merge KubeSense
// investigation results into AlertHub's incident table without a full import cycle.
type IncidentUpdater interface {
	UpdateFromKubeSense(ctx context.Context, incidentID, grade, rootCause string, confidence float64) error
}

// Consumer is a multi-topic Kafka consumer that handles all kubesense.* topics.
// Writes events to the kubesense_* tables so the OIE evidence bus and CACIE can
// query them as pre-computed intelligence signals.
type Consumer struct {
	brokers         []string
	db              *sql.DB
	incidentUpdater IncidentUpdater
}

// NewConsumer creates a multi-topic consumer for all kubesense.* topics.
// db may be nil (consumer still starts but won't persist events).
func NewConsumer(brokers []string, db *sql.DB, updater IncidentUpdater) *Consumer {
	return &Consumer{
		brokers:         brokers,
		db:              db,
		incidentUpdater: updater,
	}
}

// Start blocks until ctx is cancelled. Connects to Kafka with OffsetOldest so
// recent events are replayed on restart (events have short retention — 7 days).
func (c *Consumer) Start(ctx context.Context) {
	cfg := sarama.NewConfig()
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cfg.Version = sarama.V3_3_1_0
	cfg.Consumer.Group.Session.Timeout = 30 * time.Second
	cfg.Consumer.Group.Heartbeat.Interval = 10 * time.Second

	topics := []string{
		TopicEventsHealth,
		TopicEventsWorkloads,
		TopicEventsConfig,
		TopicEventsStorage,
		TopicEventsNetwork,
		TopicInvResults,
		TopicForecasts,
		TopicConfigViolations,
		TopicAPMGoldenSignals,
		TopicAPMRegressions,
		// New SRE intelligence topics (from KubeSense v2 capabilities)
		TopicChaosScores,
		TopicDriftDetected,
		TopicPostmortems,
		TopicToilEvents,
		TopicAnomalies,
		TopicNoiseBudgetSuppressions,
	}

	group, err := sarama.NewConsumerGroup(c.brokers, "alerthub-kubesense-consumer", cfg)
	if err != nil {
		log.Printf("KubeSense consumer unavailable (brokers=%s): %v — KubeSense intelligence disabled",
			strings.Join(c.brokers, ","), err)
		return
	}
	defer group.Close()

	log.Printf("KubeSense consumer ready: %d topics brokers=%s", len(topics), strings.Join(c.brokers, ","))

	handler := &ksHandler{consumer: c}
	for {
		if err := group.Consume(ctx, topics, handler); err != nil {
			log.Printf("KubeSense consume error: %v", err)
		}
		if ctx.Err() != nil {
			log.Printf("KubeSense consumer stopped")
			return
		}
	}
}

// ksHandler implements sarama.ConsumerGroupHandler.
type ksHandler struct{ consumer *Consumer }

func (h *ksHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *ksHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *ksHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		h.consumer.handle(sess.Context(), msg)
		sess.MarkMessage(msg, "")
	}
	return nil
}

func (c *Consumer) handle(ctx context.Context, msg *sarama.ConsumerMessage) {
	switch msg.Topic {
	case TopicEventsHealth, TopicEventsWorkloads, TopicEventsConfig, TopicEventsStorage, TopicEventsNetwork:
		c.handleIntelligenceEvent(ctx, msg)
	case TopicInvResults:
		c.handleInvResult(ctx, msg)
	case TopicForecasts:
		c.handleForecast(ctx, msg)
	case TopicConfigViolations:
		c.handleConfigViolation(ctx, msg)
	case TopicAPMGoldenSignals:
		c.handleAPMSignal(ctx, msg)
	case TopicAPMRegressions:
		c.handleAPMRegression(ctx, msg)
	// SRE intelligence topics — store and surface as OIE evidence
	case TopicChaosScores:
		c.handleChaosScore(ctx, msg)
	case TopicDriftDetected:
		c.handleDrift(ctx, msg)
	case TopicPostmortems:
		c.handleKubeSensePostmortem(ctx, msg)
	case TopicToilEvents, TopicAnomalies, TopicNoiseBudgetSuppressions:
		c.handleSREEvent(ctx, msg)
	}
}

func (c *Consumer) handleIntelligenceEvent(ctx context.Context, msg *sarama.ConsumerMessage) {
	var ev IntelligenceEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		log.Printf("KubeSense: malformed IntelligenceEvent partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	if c.db == nil {
		return
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO kubesense_health_events
			(id, cluster_id, event_type, severity, resource_kind, namespace, resource_name,
			 resource_uid, payload, occurred_at, received_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
		ON CONFLICT (id) DO NOTHING
	`, ev.ID, ev.ClusterID, string(ev.Type), string(ev.Severity),
		ev.Resource.Kind, ev.Resource.Namespace, ev.Resource.Name, ev.Resource.UID,
		msg.Value, ev.Timestamp)
	if err != nil {
		log.Printf("KubeSense: store health event %s: %v", ev.ID, err)
	}
}

func (c *Consumer) handleInvResult(ctx context.Context, msg *sarama.ConsumerMessage) {
	var result InvestigationResult
	if err := json.Unmarshal(msg.Value, &result); err != nil {
		log.Printf("KubeSense: malformed InvestigationResult partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	log.Printf("KubeSense: investigation result incident=%s grade=%s confidence=%.2f evidence=%d",
		result.IncidentID, result.Grade, result.Confidence, result.EvidenceCount)

	if c.db != nil {
		_, _ = c.db.ExecContext(ctx, `
			INSERT INTO kubesense_investigation_results
				(id, incident_id, cluster_id, grade, confidence, root_cause, summary,
				 evidence_count, payload, completed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO NOTHING
		`, result.ID, result.IncidentID, result.ClusterID, result.Grade, result.Confidence,
			result.RootCause, result.Summary, result.EvidenceCount, msg.Value, result.CompletedAt)
	}

	// Merge KubeSense RCA into the AlertHub incident via IncidentUpdater.
	if c.incidentUpdater != nil && result.IncidentID != "" {
		if err := c.incidentUpdater.UpdateFromKubeSense(
			ctx, result.IncidentID, result.Grade, result.RootCause, result.Confidence,
		); err != nil {
			log.Printf("KubeSense: update incident %s from result: %v", result.IncidentID, err)
		}
	}
}

func (c *Consumer) handleForecast(ctx context.Context, msg *sarama.ConsumerMessage) {
	var f ForecastEvent
	if err := json.Unmarshal(msg.Value, &f); err != nil {
		log.Printf("KubeSense: malformed ForecastEvent partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	if c.db == nil {
		return
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO kubesense_forecasts
			(id, cluster_id, target, resource_kind, namespace, resource_name, current_value,
			 threshold, predicted_breach, trend_per_day, model_confidence, payload, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id) DO NOTHING
	`, f.ID, f.ClusterID, f.Target, f.ResourceKind, f.Namespace, f.ResourceName,
		f.CurrentValue, f.Threshold, f.PredictedBreach, f.TrendPerDay, f.ModelConfidence,
		msg.Value, f.Timestamp)
	if err != nil {
		log.Printf("KubeSense: store forecast %s: %v", f.ID, err)
	}
}

func (c *Consumer) handleConfigViolation(ctx context.Context, msg *sarama.ConsumerMessage) {
	var v ConfigViolationEvent
	if err := json.Unmarshal(msg.Value, &v); err != nil {
		log.Printf("KubeSense: malformed ConfigViolation partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	if c.db == nil {
		return
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO kubesense_config_violations
			(id, cluster_id, rule_id, severity, resource_kind, namespace, resource_name,
			 message, remediation, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO NOTHING
	`, v.ID, v.ClusterID, v.RuleID, string(v.Severity), v.ResourceKind, v.Namespace, v.ResourceName,
		v.Message, v.Remediation, v.Timestamp)
	if err != nil {
		log.Printf("KubeSense: store config violation %s: %v", v.ID, err)
	}
}

func (c *Consumer) handleAPMSignal(ctx context.Context, msg *sarama.ConsumerMessage) {
	var s APMGoldenSignal
	if err := json.Unmarshal(msg.Value, &s); err != nil {
		log.Printf("KubeSense: malformed APMGoldenSignal partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	if c.db == nil {
		return
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO kubesense_apm_signals
			(id, cluster_id, namespace, service_name, request_rate, error_rate,
			 latency_p99_ms, saturation, sampled_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING
	`, s.ID, s.ClusterID, s.Namespace, s.ServiceName,
		s.RequestRate, s.ErrorRate, s.Latency, s.Saturation, s.Timestamp)
	if err != nil {
		log.Printf("KubeSense: store APM signal %s: %v", s.ID, err)
	}
}

func (c *Consumer) handleAPMRegression(ctx context.Context, msg *sarama.ConsumerMessage) {
	var r APMRegression
	if err := json.Unmarshal(msg.Value, &r); err != nil {
		log.Printf("KubeSense: malformed APMRegression partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	log.Printf("KubeSense APM regression: %s/%s dimension=%s deviation=%.1fσ severity=%s",
		r.Namespace, r.ServiceName, r.Dimension, r.Deviation, r.Severity)
	if c.db == nil {
		return
	}
	// APM regressions are persisted as health events (regression is a performance health signal).
	_, _ = c.db.ExecContext(ctx, `
		INSERT INTO kubesense_health_events
			(id, cluster_id, event_type, severity, resource_kind, namespace, resource_name,
			 resource_uid, payload, occurred_at, received_at)
		VALUES ($1,$2,$3,$4,'Service',$5,$6,'',$7,$8,NOW())
		ON CONFLICT (id) DO NOTHING
	`, r.ID, r.ClusterID, "apm.regression."+r.Dimension, string(r.Severity),
		r.Namespace, r.ServiceName, msg.Value, r.Timestamp)
}

func (c *Consumer) handleChaosScore(ctx context.Context, msg *sarama.ConsumerMessage) {
	var payload struct {
		ClusterID    string  `json:"cluster_id"`
		ClusterScore float64 `json:"cluster_score"`
		Grade        string  `json:"grade"`
		TotalWorkloads int   `json:"total_workloads"`
	}
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		log.Printf("KubeSense: malformed ChaosScore partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	log.Printf("KubeSense chaos score: cluster=%s score=%.0f/100 grade=%s workloads=%d",
		payload.ClusterID, payload.ClusterScore, payload.Grade, payload.TotalWorkloads)
	if c.db == nil {
		return
	}
	_, _ = c.db.ExecContext(ctx, `
		INSERT INTO kubesense_health_events
			(id, cluster_id, event_type, severity, resource_kind, namespace, resource_name,
			 resource_uid, payload, occurred_at, received_at)
		VALUES (uuid_generate_v4()::text, $1, 'chaos.score', $2, 'Cluster', '', $1, '', $3, NOW(), NOW())
		ON CONFLICT DO NOTHING
	`, payload.ClusterID,
		func() string {
			if payload.ClusterScore < 40 {
				return "critical"
			} else if payload.ClusterScore < 60 {
				return "high"
			} else if payload.ClusterScore < 80 {
				return "medium"
			}
			return "info"
		}(),
		msg.Value)
}

func (c *Consumer) handleDrift(ctx context.Context, msg *sarama.ConsumerMessage) {
	var payload struct {
		ClusterID    string    `json:"cluster_id"`
		Namespace    string    `json:"namespace"`
		ResourceKind string    `json:"resource_kind"`
		ResourceName string    `json:"resource_name"`
		DriftType    string    `json:"drift_type"`
		Severity     string    `json:"severity"`
		Actor        string    `json:"actor"`
		OccurredAt   string    `json:"occurred_at"`
	}
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		log.Printf("KubeSense: malformed DriftDetected partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		return
	}
	log.Printf("KubeSense drift: %s/%s/%s drift_type=%s severity=%s actor=%s",
		payload.Namespace, payload.ResourceKind, payload.ResourceName, payload.DriftType, payload.Severity, payload.Actor)
	if c.db == nil {
		return
	}
	_, _ = c.db.ExecContext(ctx, `
		INSERT INTO kubesense_health_events
			(id, cluster_id, event_type, severity, resource_kind, namespace, resource_name,
			 resource_uid, payload, occurred_at, received_at)
		VALUES (uuid_generate_v4()::text, $1, $2, $3, $4, $5, $6, '', $7, NOW(), NOW())
		ON CONFLICT DO NOTHING
	`, payload.ClusterID, "drift."+payload.DriftType, payload.Severity,
		payload.ResourceKind, payload.Namespace, payload.ResourceName, msg.Value)
}

func (c *Consumer) handleKubeSensePostmortem(ctx context.Context, msg *sarama.ConsumerMessage) {
	// KubeSense postmortems are informational — log and store for cross-referencing
	// when AlertHub generates its own postmortem for the same incident.
	var payload struct {
		IncidentID string `json:"incident_id"`
		ClusterID  string `json:"cluster_id"`
		Title      string `json:"title"`
	}
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		return
	}
	log.Printf("KubeSense postmortem received: incident=%s cluster=%s", payload.IncidentID, payload.ClusterID)
}

func (c *Consumer) handleSREEvent(ctx context.Context, msg *sarama.ConsumerMessage) {
	// Toil events, anomalies, noise budget suppressions — store as health events
	// for tracing / audit; not directly surfaced as OIE evidence yet.
	if c.db == nil {
		return
	}
	var payload struct {
		ClusterID string `json:"cluster_id"`
		Severity  string `json:"severity"`
	}
	_ = json.Unmarshal(msg.Value, &payload)
	clusterID := payload.ClusterID
	if clusterID == "" {
		clusterID = "unknown"
	}
	sev := payload.Severity
	if sev == "" {
		sev = "info"
	}
	_, _ = c.db.ExecContext(ctx, `
		INSERT INTO kubesense_health_events
			(id, cluster_id, event_type, severity, resource_kind, namespace, resource_name,
			 resource_uid, payload, occurred_at, received_at)
		VALUES (uuid_generate_v4()::text, $1, $2, $3, 'SRE', '', '', '', $4, NOW(), NOW())
		ON CONFLICT DO NOTHING
	`, clusterID, "sre."+msg.Topic, sev, msg.Value)
}
