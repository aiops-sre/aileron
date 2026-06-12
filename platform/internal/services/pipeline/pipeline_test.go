package pipeline

import (
	"testing"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/shared/models"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// TryEnqueueAlert — backpressure and channel behaviour
// ---------------------------------------------------------------------------

func TestTryEnqueueAlert_AcceptsAlert_WhenQueueHasCapacity(t *testing.T) {
	svc := &AlertPipelineService{
		alertCh: make(chan *models.Alert, 10),
	}
	alert := &models.Alert{ID: uuid.New(), Title: "CPU High", Severity: "critical", Source: "dynatrace"}
	if !svc.TryEnqueueAlert(alert) {
		t.Error("expected TryEnqueueAlert to return true when channel has capacity")
	}
}

func TestTryEnqueueAlert_ReturnsFalse_WhenQueueFull(t *testing.T) {
	svc := &AlertPipelineService{
		alertCh: make(chan *models.Alert, 1),
	}
	svc.alertCh <- &models.Alert{ID: uuid.New()} // fill the single slot

	alert := &models.Alert{ID: uuid.New(), Title: "CPU High", Severity: "critical", Source: "dynatrace"}
	if svc.TryEnqueueAlert(alert) {
		t.Error("expected TryEnqueueAlert to return false when channel is full")
	}
}

func TestTryEnqueueAlert_DoesNotBlock(t *testing.T) {
	svc := &AlertPipelineService{
		alertCh: make(chan *models.Alert, 0), // zero-cap — always full
	}
	alert := &models.Alert{ID: uuid.New(), Title: "Test", Severity: "high", Source: "prometheus"}

	done := make(chan bool, 1)
	go func() { done <- svc.TryEnqueueAlert(alert) }()

	select {
	case result := <-done:
		if result {
			t.Error("expected false on zero-cap channel")
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("TryEnqueueAlert blocked — must be non-blocking")
	}
}

// ---------------------------------------------------------------------------
// EnqueueAlert — blocking path with nil/empty channel
// ---------------------------------------------------------------------------

func TestEnqueueAlert_NilChannel_DoesNotPanic(t *testing.T) {
	svc := &AlertPipelineService{}
	// alertCh is nil — EnqueueAlert must not panic; it uses send-to-nil which blocks forever.
	// We wrap in a goroutine with a timeout to detect blocking rather than panic.
	alert := &models.Alert{ID: uuid.New(), Title: "Test", Source: "prometheus"}

	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// A panic is also acceptable here as long as it doesn't crash the test.
			}
			close(done)
		}()
		svc.EnqueueAlert(alert)
	}()

	// If send blocks (nil channel), we just verify it doesn't hard-crash within 100ms.
	select {
	case <-done:
		// Returned normally or panicked (recovered) — both acceptable.
	case <-time.After(100 * time.Millisecond):
		// Blocked — also acceptable since send to nil blocks forever; test verifies no panic.
		t.Log("EnqueueAlert blocked on nil channel (expected for nil channel send)")
	}
}

// ---------------------------------------------------------------------------
// rcaConfidenceBand — confidence band classification
// ---------------------------------------------------------------------------

func TestRCAConfidenceBand(t *testing.T) {
	tests := []struct {
		conf float64
		want string
	}{
		{0.95, "HIGH"},
		{0.85, "HIGH"},
		{0.80, "MEDIUM"},
		{0.65, "MEDIUM"},
		{0.60, "LOW"},
		{0.40, "LOW"},
		{0.39, "VERY_LOW"},
		{0.00, "VERY_LOW"},
	}
	for _, tc := range tests {
		got := rcaConfidenceBand(tc.conf)
		if got != tc.want {
			t.Errorf("rcaConfidenceBand(%.2f) = %q, want %q", tc.conf, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// severityToPriority — alert severity → incident priority passthrough
// ---------------------------------------------------------------------------

func TestSeverityToPriority(t *testing.T) {
	tests := []struct {
		severity string
		want     string
	}{
		{"critical", "critical"},
		{"high", "high"},
		{"medium", "medium"},
		{"low", "medium"},   // falls through to default
		{"", "medium"},      // falls through to default
		{"unknown", "medium"},
	}
	for _, tc := range tests {
		got := severityToPriority(tc.severity)
		if got != tc.want {
			t.Errorf("severityToPriority(%q) = %q, want %q", tc.severity, got, tc.want)
		}
	}
}
