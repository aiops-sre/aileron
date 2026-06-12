// kubesense-llm — Incident narrator service.
//
// Subscribes to: kubesense.correlation.incident-context (Kafka)
// Publishes to:  kubesense.llm.narratives (Kafka)
// Persists to:   kubesense_narratives (PostgreSQL) for API query
//
// LLM backend priority:
//   1. Claude API (CLAUDE_API_KEY set)            — highest quality
//   2. Ollama    (OLLAMA_BASE_URL set)             — on-prem, no internet
//   3. Deterministic fallback                     — always available
//
// Role: NARRATOR ONLY. Never makes remediation decisions.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	_ "github.com/lib/pq"

	"github.com/aileron-platform/aileron/agent/services/llm/internal/narrator"
)

const (
	consumerTopic = "kubesense.correlation.incident-context"
	producerTopic = "kubesense.llm.narratives"
	consumerGroup = "kubesense-llm-narrator"
)

func main() {
	kafkaBrokers := envOrDefault("KAFKA_BROKERS",
		"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092")
	claudeKey   := envOrDefault("CLAUDE_API_KEY", "")
	claudeModel := envOrDefault("CLAUDE_MODEL", "")
	ollamaURL   := envOrDefault("OLLAMA_BASE_URL", "http://ollama.aileron.svc.cluster.local:11434")
	ollamaModel := envOrDefault("OLLAMA_MODEL", "qwen2.5:7b")
	dbURL       := envOrDefault("DATABASE_URL", "")

	backend := "fallback"
	switch {
	case claudeKey != "":  backend = "claude/" + claudeModel
	case ollamaURL != "":  backend = "ollama/" + ollamaModel
	}
	log.Printf("kubesense-llm starting: kafka=%s backend=%s", kafkaBrokers, backend)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// PostgreSQL for narrative persistence (optional)
	var db *sql.DB
	if dbURL != "" {
		var err error
		db, err = sql.Open("postgres", dbURL)
		if err != nil {
			log.Printf("kubesense-llm: postgres unavailable: %v — narratives Kafka-only", err)
			db = nil
		} else {
			db.SetMaxOpenConns(5)
			ensureNarrativeSchema(ctx, db)
		}
	}

	brokers := strings.Split(kafkaBrokers, ",")
	client := narrator.NewClient(claudeKey, claudeModel, ollamaURL, ollamaModel)

	prod, err := newProducer(brokers)
	if err != nil {
		log.Fatalf("kubesense-llm: Kafka producer: %v", err)
	}
	defer prod.Close()

	consumer, err := newConsumerGroup(brokers)
	if err != nil {
		log.Fatalf("kubesense-llm: Kafka consumer: %v", err)
	}
	defer consumer.Close()

	handler := &narratorHandler{client: client, producer: prod, db: db}

	log.Printf("kubesense-llm: %s → %s", consumerTopic, producerTopic)
	for {
		if err := consumer.Consume(ctx, []string{consumerTopic}, handler); err != nil {
			if ctx.Err() != nil { break }
			log.Printf("kubesense-llm: consumer error: %v — retrying in 5s", err)
			select {
			case <-ctx.Done(): return
			case <-time.After(5 * time.Second):
			}
		}
	}
	log.Println("kubesense-llm: shutdown")
}

// ─── Kafka handler ────────────────────────────────────────────────────────────

type narratorHandler struct {
	client   *narrator.Client
	producer sarama.SyncProducer
	db       *sql.DB
}

func (h *narratorHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *narratorHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *narratorHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-session.Context().Done(): return nil
		case msg, ok := <-claim.Messages():
			if !ok { return nil }
			if err := h.handleMessage(session.Context(), msg); err != nil {
				log.Printf("kubesense-llm: error offset=%d: %v", msg.Offset, err)
			}
			session.MarkMessage(msg, "")
		}
	}
}

func (h *narratorHandler) handleMessage(ctx context.Context, msg *sarama.ConsumerMessage) error {
	var ic incidentContext
	if err := json.Unmarshal(msg.Value, &ic); err != nil {
		return nil
	}

	log.Printf("kubesense-llm: narrating incident=%s grade=%s signals=%d",
		ic.IncidentID, ic.EvidenceGrade, len(ic.Signals))

	narr, err := h.client.Generate(ctx, incidentContextToRequest(ic))
	if err != nil {
		return err
	}

	out := narrativeOutput{
		IncidentID:    ic.IncidentID,
		ClusterID:     ic.ClusterID,
		Narrative:     narr.Text,
		Model:         narr.Model,
		EvidenceGrade: ic.EvidenceGrade,
		Confidence:    ic.Confidence,
		GeneratedAt:   narr.GeneratedAt,
		InputTokens:   narr.InputTokens,
		OutputTokens:  narr.OutputTokens,
	}

	// Publish to Kafka
	payload, _ := json.Marshal(out)
	if _, _, err := h.producer.SendMessage(&sarama.ProducerMessage{
		Topic: producerTopic,
		Key:   sarama.StringEncoder(ic.IncidentID),
		Value: sarama.ByteEncoder(payload),
	}); err != nil {
		log.Printf("kubesense-llm: publish failed for %s: %v", ic.IncidentID, err)
	}

	// Persist to PostgreSQL so the API can return narratives without Kafka
	if h.db != nil {
		persistNarrative(ctx, h.db, out)
	}

	log.Printf("kubesense-llm: done incident=%s model=%s tokens_out=%d",
		ic.IncidentID, narr.Model, narr.OutputTokens)
	return nil
}

// ─── PostgreSQL persistence ───────────────────────────────────────────────────

func ensureNarrativeSchema(ctx context.Context, db *sql.DB) {
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS kubesense_narratives (
			incident_id    VARCHAR(64)  PRIMARY KEY,
			cluster_id     VARCHAR(128),
			narrative      TEXT         NOT NULL,
			model          VARCHAR(100),
			evidence_grade VARCHAR(5),
			confidence     FLOAT,
			input_tokens   INTEGER      DEFAULT 0,
			output_tokens  INTEGER      DEFAULT 0,
			generated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_ks_narr_cluster ON kubesense_narratives(cluster_id);
	`)
}

func persistNarrative(ctx context.Context, db *sql.DB, n narrativeOutput) {
	_, _ = db.ExecContext(ctx, `
		INSERT INTO kubesense_narratives
		    (incident_id, cluster_id, narrative, model, evidence_grade, confidence,
		     input_tokens, output_tokens, generated_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
		ON CONFLICT (incident_id) DO UPDATE SET
		    narrative      = EXCLUDED.narrative,
		    model          = EXCLUDED.model,
		    evidence_grade = EXCLUDED.evidence_grade,
		    confidence     = EXCLUDED.confidence,
		    input_tokens   = EXCLUDED.input_tokens,
		    output_tokens  = EXCLUDED.output_tokens,
		    updated_at     = NOW()
	`, n.IncidentID, n.ClusterID, n.Narrative, n.Model, n.EvidenceGrade,
		n.Confidence, n.InputTokens, n.OutputTokens, n.GeneratedAt)
}

// ─── Data shapes ──────────────────────────────────────────────────────────────

type incidentContext struct {
	IncidentID         string            `json:"incident_id"`
	ClusterID          string            `json:"cluster_id"`
	Title              string            `json:"title"`
	DetectedAt         time.Time         `json:"detected_at"`
	Signals            []rawSignal          `json:"signals"`
	EvidenceGrade      string            `json:"evidence_grade"`
	Confidence         float64           `json:"confidence"`
	RootCause          *rootCauseSummary `json:"root_cause,omitempty"`
	SLOImpact          *sloImpactSummary `json:"slo_impact,omitempty"`
	SecurityFindings   []securitySummary `json:"security_findings,omitempty"`
	RecommendedActions []action          `json:"recommended_actions"`
}

type rawSignal struct {
	Source, Type, Description string
	Strength                  float64
	ResourceKind, ResourceName, Namespace string
	Timestamp                 time.Time
}
type rootCauseSummary struct {
	EntityKind, EntityLabel string
	Confidence              float64
	ConfidenceBand          string
}
type sloImpactSummary struct {
	Service, SLOName           string
	BurnRate, BudgetRemainingPct float64
}
type securitySummary struct{ RuleID, Severity, Title string }
type action struct {
	Type, Target, Command, Rationale string
	Confidence                       float64
}
type narrativeOutput struct {
	IncidentID, ClusterID, Narrative, Model, EvidenceGrade string
	Confidence                                              float64
	GeneratedAt                                             time.Time
	InputTokens, OutputTokens                               int
}

func incidentContextToRequest(ic incidentContext) narrator.NarrativeRequest {
	req := narrator.NarrativeRequest{
		IncidentID: ic.IncidentID, ClusterID: ic.ClusterID,
		Title: ic.Title, DetectedAt: ic.DetectedAt,
		EvidenceGrade: ic.EvidenceGrade, Confidence: ic.Confidence,
	}
	for _, s := range ic.Signals {
		req.Signals = append(req.Signals, narrator.SignalSummary{
			Source: s.Source, Type: s.Type, Description: s.Description,
			Strength: s.Strength, ResourceKind: s.ResourceKind,
			ResourceName: s.ResourceName, Namespace: s.Namespace,
			OccurredAt: s.Timestamp,
		})
	}
	if rc := ic.RootCause; rc != nil {
		parts := strings.SplitN(rc.EntityLabel, "/", 3)
		name, ns := rc.EntityLabel, ""
		if len(parts) >= 3 { ns, name = parts[1], parts[2] }
		req.RootCause = &narrator.RootCauseSummary{
			EntityKind: rc.EntityKind, EntityName: name, EntityNS: ns,
			Confidence: rc.Confidence, ConfidenceBand: rc.ConfidenceBand,
		}
	}
	if slo := ic.SLOImpact; slo != nil {
		req.SLOImpact = &narrator.SLOSummary{
			Service: slo.Service, SLOName: slo.SLOName,
			BurnRate: slo.BurnRate, BudgetPct: slo.BudgetRemainingPct,
		}
	}
	for _, sf := range ic.SecurityFindings {
		req.SecurityFindings = append(req.SecurityFindings, narrator.SecurityFinding{
			RuleID: sf.RuleID, Severity: sf.Severity, Title: sf.Title,
		})
	}
	for _, a := range ic.RecommendedActions {
		req.Actions = append(req.Actions, narrator.ActionSummary{
			Type: a.Type, Target: a.Target, Command: a.Command,
			Confidence: a.Confidence, Rationale: a.Rationale,
		})
	}
	return req
}

// ─── Kafka setup ──────────────────────────────────────────────────────────────

func newProducer(brokers []string) (sarama.SyncProducer, error) {
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 5
	cfg.Producer.Compression = sarama.CompressionSnappy
	return sarama.NewSyncProducer(brokers, cfg)
}

func newConsumerGroup(brokers []string) (sarama.ConsumerGroup, error) {
	cfg := sarama.NewConfig()
	cfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	return sarama.NewConsumerGroup(brokers, consumerGroup, cfg)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" { return v }
	return def
}
