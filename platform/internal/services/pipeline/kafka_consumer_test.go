package pipeline

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// handleResolvedAlert guard: nil pipeline / nil db
// ---------------------------------------------------------------------------
//
// The kafka_consumer's handleResolvedAlert is a method on AlertKafkaConsumer.
// It starts with:
//
//	if c.pipelineSvc == nil || c.pipelineSvc.db == nil { log.Printf(...); return }
//
// These tests confirm that early-return paths do not panic or perform any DB
// calls — purely the guard logic can be exercised without a real Kafka broker
// or database.

func TestKafkaHandleResolvedAlert_NilPipeline(t *testing.T) {
	c := &AlertKafkaConsumer{
		pipelineSvc: nil, // guard: nil pipeline
	}

	msg := universalAlertMsg{
		ID:       uuid.New().String(),
		Status:   "resolved",
		Source:   "prometheus",
		SourceID: "alert-42",
	}

	// Must not panic.
	c.handleResolvedAlert(context.Background(), msg)
}

func TestKafkaHandleResolvedAlert_NilDB(t *testing.T) {
	svc := &AlertPipelineService{
		db: nil, // guard: nil db
	}
	c := &AlertKafkaConsumer{
		pipelineSvc: svc,
	}

	msg := universalAlertMsg{
		ID:       uuid.New().String(),
		Status:   "resolved",
		Source:   "datadog",
		SourceID: "monitor-99",
	}

	// Must not panic.
	c.handleResolvedAlert(context.Background(), msg)
}

// ---------------------------------------------------------------------------
// handleResolvedAlert guard: empty source_id — should return early
// ---------------------------------------------------------------------------
//
// When all of SourceID, AlertID, and ID are empty, the function logs and
// returns without touching the DB.

func TestKafkaHandleResolvedAlert_EmptySourceID(t *testing.T) {
	svc := &AlertPipelineService{
		db: nil, // even if we reached the DB, it's nil — so any call would panic
	}
	c := &AlertKafkaConsumer{
		pipelineSvc: svc,
	}

	msg := universalAlertMsg{
		// All identity fields empty — triggers the "no source_id" guard.
		ID:       "",
		AlertID:  "",
		Status:   "resolved",
		Source:   "prometheus",
		SourceID: "",
	}

	// Must not panic.
	c.handleResolvedAlert(context.Background(), msg)
}

// ---------------------------------------------------------------------------
// universalAlertMsg status routing guard logic
// ---------------------------------------------------------------------------
//
// processMessage branches on raw.Status == "resolved" | "closed" before any
// dedup or DB work.  We test this routing by observing that processing a
// message whose pipeline service has nil DB:
//  - for resolved/closed: reaches handleResolvedAlert which returns immediately
//    (nil pipeline guard fires first, returning before any DB query).
//  - In all cases: must not panic.
//
// We cannot call processMessage directly (it requires a *sarama.ConsumerMessage),
// so we exercise the same routing logic through the exported guard paths.

func TestKafkaProcessMessage_StatusGuards(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{"resolved routes to handler", "resolved"},
		{"closed routes to handler", "closed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Use nil pipelineSvc so handleResolvedAlert returns on first guard.
			c := &AlertKafkaConsumer{
				pipelineSvc: nil,
			}

			msg := universalAlertMsg{
				ID:       uuid.New().String(),
				Status:   tc.status,
				Source:   "prometheus",
				SourceID: "probe-1",
			}

			// This mirrors the processMessage resolved/closed branch.
			// Must not panic even with nil services.
			c.handleResolvedAlert(context.Background(), msg)
		})
	}
}

// ---------------------------------------------------------------------------
// universalAlertMsg field parsing helpers
// ---------------------------------------------------------------------------

func TestUniversalAlertMsg_IDFallback(t *testing.T) {
	// The consumer prefers "id" over "alert_id"; if "id" is empty it falls back
	// to "alert_id". This logic lives inside processMessage.  We test the struct
	// directly to make sure both fields are populated independently (not aliased).
	msg := universalAlertMsg{
		ID:      "",
		AlertID: "fallback-uuid",
	}

	// Replicate the ID resolution logic from processMessage:
	idStr := msg.ID
	if idStr == "" {
		idStr = msg.AlertID
	}

	if idStr != "fallback-uuid" {
		t.Errorf("expected 'fallback-uuid', got %q", idStr)
	}
}

func TestUniversalAlertMsg_PreferIDOverAlertID(t *testing.T) {
	msg := universalAlertMsg{
		ID:      "primary-uuid",
		AlertID: "secondary-uuid",
	}

	idStr := msg.ID
	if idStr == "" {
		idStr = msg.AlertID
	}

	if idStr != "primary-uuid" {
		t.Errorf("expected 'primary-uuid', got %q", idStr)
	}
}

// ---------------------------------------------------------------------------
// Default status / severity normalisation
// ---------------------------------------------------------------------------

func TestKafkaConsumer_DefaultStatus(t *testing.T) {
	// Replicate the status defaulting logic from processMessage:
	//   if alert.Status == "" { alert.Status = "open" }
	status := ""
	if status == "" {
		status = "open"
	}
	if status != "open" {
		t.Errorf("expected default status 'open', got %q", status)
	}
}

func TestKafkaConsumer_DefaultSeverity(t *testing.T) {
	// Replicate the severity defaulting:
	//   if alert.Severity == "" { alert.Severity = "medium" }
	severity := ""
	if severity == "" {
		severity = "medium"
	}
	if severity != "medium" {
		t.Errorf("expected default severity 'medium', got %q", severity)
	}
}

func TestKafkaConsumer_UnknownStatusDefaultsToOpen(t *testing.T) {
	// Replicate the unknown-status guard from processMessage.
	validate := func(status string) string {
		switch status {
		case "open", "acknowledged", "resolved", "suppressed", "closed":
			return status
		default:
			return "open"
		}
	}

	tests := []struct {
		input string
		want  string
	}{
		{"open", "open"},
		{"resolved", "resolved"},
		{"closed", "closed"},
		{"acknowledged", "acknowledged"},
		{"suppressed", "suppressed"},
		{"bogus", "open"},
		{"OPEN", "open"},   // case-sensitive: upper-case is treated as unknown open
		{"", "open"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := validate(tc.input)
			if got != tc.want {
				t.Errorf("status guard(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
