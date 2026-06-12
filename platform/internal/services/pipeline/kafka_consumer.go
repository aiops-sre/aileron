package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/alerts"
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

// universalAlertMsg matches the JSON format published by universal-alert-gateway
// and by the webhook handler's Kafka-first path.
type universalAlertMsg struct {
	ID          string                 `json:"id"`
	AlertID     string                 `json:"alert_id"` // webhook handler uses this field
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity"`
	Status      string                 `json:"status"`
	Source      string                 `json:"source"`
	SourceID    string                 `json:"source_id"`
	Labels      map[string]string      `json:"labels,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
}

// AlertKafkaConsumer reads from alerts.raw, persists each alert, and enqueues
// it to the parallel correlation pipeline.
type AlertKafkaConsumer struct {
	brokers     []string
	topic       string
	alertSvc    *alerts.AlertService
	pipelineSvc *AlertPipelineService
}

func NewAlertKafkaConsumer(
	brokers []string,
	topic string,
	alertSvc *alerts.AlertService,
	pipelineSvc *AlertPipelineService,
) *AlertKafkaConsumer {
	return &AlertKafkaConsumer{
		brokers:     brokers,
		topic:       topic,
		alertSvc:    alertSvc,
		pipelineSvc: pipelineSvc,
	}
}

// Start blocks until ctx is cancelled. Errors connecting to Kafka are logged but
// do not crash the process — the rest of the pipeline still works.
func (c *AlertKafkaConsumer) Start(ctx context.Context) {
	cfg := sarama.NewConfig()
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cfg.Version = sarama.V2_8_0_0

	group, err := sarama.NewConsumerGroup(c.brokers, "alerthub-pipeline-consumer", cfg)
	if err != nil {
		log.Printf("Kafka consumer unavailable (brokers=%s): %v — pipeline running without Kafka consumer",
			strings.Join(c.brokers, ","), err)
		return
	}
	defer group.Close()

	log.Printf("Kafka consumer ready: topic=%s brokers=%s", c.topic, strings.Join(c.brokers, ","))

	handler := &alertCGHandler{consumer: c}
	for {
		if err := group.Consume(ctx, []string{c.topic}, handler); err != nil {
			log.Printf("Kafka consume error: %v", err)
		}
		if ctx.Err() != nil {
			log.Printf("Kafka consumer stopped")
			return
		}
	}
}

// alertCGHandler implements sarama.ConsumerGroupHandler
type alertCGHandler struct {
	consumer *AlertKafkaConsumer
}

func (h *alertCGHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *alertCGHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *alertCGHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		// Only commit the offset when processing succeeded. On transient failures
		// (e.g. pipeline queue full) the offset is left uncommitted so Kafka
		// re-delivers the message after a rebalance or restart.
		if h.consumer.processMessage(sess.Context(), msg) {
			sess.MarkMessage(msg, "")
		}
	}
	return nil
}

// processMessage returns true when the message was successfully handled (commit offset)
// and false when the pipeline is backlogged and the message should be re-delivered.
func (c *AlertKafkaConsumer) processMessage(ctx context.Context, msg *sarama.ConsumerMessage) (commit bool) {
	var raw universalAlertMsg
	if err := json.Unmarshal(msg.Value, &raw); err != nil {
		// Permanent failure: malformed JSON cannot be reprocessed. Log as dead-letter
		// and commit the offset so the consumer does not stall indefinitely.
		log.Printf("Kafka DLQ: malformed message topic=%s partition=%d offset=%d err=%v value=%.200s",
			msg.Topic, msg.Partition, msg.Offset, err, string(msg.Value))
		return true
	}

	// Handle resolved/closed alerts separately — close the linked incident rather than
	// creating a new alert record.
	if raw.Status == "resolved" || raw.Status == "closed" {
		c.handleResolvedAlert(ctx, raw)
		return true
	}

	// Dedup: if an alert with this source_id already exists, skip creation to prevent
	// duplicates from Dynatrace retries or at-least-once Kafka delivery.
	// If the alert exists but has never been through the pipeline (no correlation result),
	// still enqueue it so AIOps gets an entry.
	if raw.Source != "" && raw.SourceID != "" {
		if existing, err := c.alertSvc.GetAlertBySourceID(ctx, raw.Source, raw.SourceID); err == nil && existing != nil {
			// Check if a pipeline result already exists for this alert.
			var hasResult bool
			if c.pipelineSvc != nil && c.pipelineSvc.db != nil {
				var cnt int
				if err := c.pipelineSvc.db.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM pipeline_correlation_results WHERE alert_id = $1`, existing.ID,
				).Scan(&cnt); err != nil {
					log.Printf("Kafka: failed to check pipeline result for alert %s: %v", existing.ID, err)
				} else {
					hasResult = cnt > 0
				}
			}
			if hasResult {
				log.Printf("Kafka: skipping duplicate source_id=%s (alert %s already correlated)", raw.SourceID, existing.ID)
				return true
			}
			// Alert exists but hasn't been correlated. If this is an OPEN message but
			// the existing DB record is resolved (race: RESOLVED webhook arrived before
			// Kafka processed the OPEN message and created a resolved record), reopen
			// the alert so the pipeline processes it as OPEN — not as a no-op resolved.
			if (raw.Status == "open" || raw.Status == "") && existing.Status == "resolved" {
				existing.Status = "open"
				if c.pipelineSvc != nil && c.pipelineSvc.db != nil {
					if _, err := c.pipelineSvc.db.ExecContext(ctx,
						`UPDATE alerts SET status='open', resolved_at=NULL WHERE id=$1`, existing.ID,
					); err != nil {
						log.Printf("Kafka: failed to reopen alert %s in DB: %v", existing.ID, err)
						existing.Status = "resolved"
						return false
					}
				}
				log.Printf("Kafka: reopening alert %s (was resolved, incoming OPEN for source_id=%s)", existing.ID, raw.SourceID)
			}
			log.Printf("Kafka: alert %s exists but not yet correlated, enqueuing to pipeline", existing.ID)
			return c.pipelineSvc.TryEnqueueAlert(existing)
		}
	}

	// Support both "id" and "alert_id" field names used by different publishers
	idStr := raw.ID
	if idStr == "" {
		idStr = raw.AlertID
	}

	alertID := uuid.New()
	if parsed, err := uuid.Parse(idStr); err == nil {
		alertID = parsed
	}

	alert := &models.Alert{
		ID:          alertID,
		Title:       raw.Title,
		Description: raw.Description,
		Severity:    raw.Severity,
		Status:      raw.Status,
		Source:      raw.Source,
		SourceID:    raw.SourceID,
		Labels:      raw.Labels,
		Metadata:    raw.Metadata,
	}
	if alert.Status == "" {
		alert.Status = "open"
	}
	// Reject unknown status values at the ingestion boundary.
	switch alert.Status {
	case "open", "acknowledged", "resolved", "suppressed", "closed":
		// valid
	default:
		log.Printf("Kafka: unknown status %q for alert %s — defaulting to open", alert.Status, alert.ID)
		alert.Status = "open"
	}
	if alert.Severity == "" {
		alert.Severity = "medium"
	}

	// Enqueue FIRST — if the pipeline is backlogged we return false so Kafka
	// re-delivers the message. This prevents a DB write for an alert that never
	// gets processed, and avoids duplicate rows for source-id-less alerts on retry.
	if !c.pipelineSvc.TryEnqueueAlert(alert) {
		return false // pipeline backlogged — do not commit offset; Kafka will re-deliver
	}

	// Persist to DB after successful enqueue — use uuid.Nil as the system user.
	if err := c.alertSvc.CreateAlert(ctx, alert, uuid.Nil); err != nil {
		log.Printf("Kafka: failed to store alert %s: %v", alert.ID, err)
		// Alert already queued; pipeline DB operations will proceed using the in-memory struct.
	}
	log.Printf("Kafka: ingested alert %s from source=%s source_id=%s", alert.ID, raw.Source, raw.SourceID)
	return true
}

// handleResolvedAlert closes the open alert and, if all correlated alerts are resolved,
// auto-resolves the linked incident.
func (c *AlertKafkaConsumer) handleResolvedAlert(ctx context.Context, raw universalAlertMsg) {
	if c.pipelineSvc == nil || c.pipelineSvc.db == nil {
		log.Printf("Kafka: handleResolvedAlert called but pipeline/db not available")
		return
	}
	db := c.pipelineSvc.db

	// Determine source_id: prefer source_id field, fall back to alert_id / id.
	sourceID := raw.SourceID
	if sourceID == "" {
		sourceID = raw.AlertID
	}
	if sourceID == "" {
		sourceID = raw.ID
	}
	// Never query with an empty source_id — that would match all HC/synthetic alerts
	// with source_id='' for this source, bulk-resolving unrelated alerts.
	if sourceID == "" {
		log.Printf("Kafka: handleResolvedAlert skipped — no source_id/alert_id/id in resolved event (source=%s)", raw.Source)
		return
	}

	// Find the open alert by source + source_id. ORDER BY created_at DESC so that
	// when Dynatrace reuses a Problem ID across clusters, the most recent alert wins.
	var foundAlertID uuid.UUID
	var incidentIDStr sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT id, incident_id::text
		FROM alerts
		WHERE source = $1 AND source_id = $2 AND status != 'resolved'
		ORDER BY created_at DESC
		LIMIT 1
	`, raw.Source, sourceID).Scan(&foundAlertID, &incidentIDStr)
	if err != nil {
		// Fallback: the original OPEN alert may have been ingested before the source_id
		// was set (empty source_id race). Search for it via its description text which
		// Dynatrace always stamps with "OPEN Problem <sourceID> in environment …".
		descPattern := "%Problem " + sourceID + "%"
		err2 := db.QueryRowContext(ctx, `
			SELECT id, incident_id::text
			FROM alerts
			WHERE source = $1
			  AND (source_id IS NULL OR source_id = '')
			  AND status != 'resolved'
			  AND description ILIKE $2
			ORDER BY created_at DESC
			LIMIT 1
		`, raw.Source, descPattern).Scan(&foundAlertID, &incidentIDStr)
		if err2 != nil {
			log.Printf("Kafka: no open alert found for source=%s source_id=%s (resolved event ignored)", raw.Source, sourceID)
			return
		}
		// Backfill the source_id so future events can find this alert directly.
		_, _ = db.ExecContext(ctx, `UPDATE alerts SET source_id = $1 WHERE id = $2`, sourceID, foundAlertID)
		log.Printf("Kafka: resolved orphaned alert %s via description fallback for problem %s", foundAlertID, sourceID)
	}

	// Mark the alert resolved.
	_, _ = db.ExecContext(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1
	`, foundAlertID)
	log.Printf("Kafka: alert %s marked resolved (source=%s source_id=%s)", foundAlertID, raw.Source, sourceID)

	// If the alert was linked to an incident, let the pipeline decide whether to close it.
	if incidentIDStr.Valid && incidentIDStr.String != "" {
		incidentID, err := uuid.Parse(incidentIDStr.String)
		if err == nil && incidentID != uuid.Nil {
			c.pipelineSvc.HandleAlertResolved(ctx, foundAlertID, incidentID)
		}
		return
	}

	// incident_id was NULL — the pipeline may still be in-flight linking this alert.
	// Search open incidents that contain alerts with the same source+source_id so we
	// don't leave an open incident with a resolved alert inside.
	var fallbackIncidentID uuid.UUID
	_ = db.QueryRowContext(ctx, `
		SELECT i.id FROM incidents i
		JOIN alerts a ON a.incident_id = i.id
		WHERE i.status IN ('open','investigating','acknowledged')
		  AND a.source = $1 AND a.source_id = $2
		ORDER BY i.created_at DESC LIMIT 1
	`, raw.Source, sourceID).Scan(&fallbackIncidentID)
	if fallbackIncidentID != uuid.Nil {
		log.Printf("Kafka: found incident %s via source_id fallback for resolved alert %s", fallbackIncidentID, foundAlertID)
		c.pipelineSvc.HandleAlertResolved(ctx, foundAlertID, fallbackIncidentID)
	} else {
		log.Printf("Kafka: alert %s resolved, no linked incident found yet (stale sweep will close if needed)", foundAlertID)
	}
}
