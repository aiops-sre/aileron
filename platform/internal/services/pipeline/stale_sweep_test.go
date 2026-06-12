package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/shared/models"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// alertDedupKey
// ---------------------------------------------------------------------------

func TestAlertDedupKey(t *testing.T) {
	t.Run("consistent key for same alert", func(t *testing.T) {
		alert := &models.Alert{
			ID:       uuid.New(),
			Title:    "High CPU Usage",
			Source:   "prometheus",
			Severity: "critical",
			Labels:   map[string]string{"cluster": "prod-k8s-01"},
		}
		k1 := alertDedupKey(alert)
		k2 := alertDedupKey(alert)
		if k1 != k2 {
			t.Errorf("expected identical keys, got %q and %q", k1, k2)
		}
	})

	t.Run("key includes source", func(t *testing.T) {
		alert := &models.Alert{
			ID:       uuid.New(),
			Title:    "Disk Full",
			Source:   "datadog",
			Severity: "high",
			Labels:   map[string]string{},
		}
		key := alertDedupKey(alert)
		if !strings.Contains(key, "datadog") {
			t.Errorf("dedup key %q should contain source 'datadog'", key)
		}
	})

	t.Run("key includes severity", func(t *testing.T) {
		alert := &models.Alert{
			ID:       uuid.New(),
			Title:    "Service Down",
			Source:   "pagerduty",
			Severity: "critical",
			Labels:   map[string]string{},
		}
		key := alertDedupKey(alert)
		if !strings.Contains(key, "critical") {
			t.Errorf("dedup key %q should contain severity 'critical'", key)
		}
	})

	t.Run("different clusters produce different keys", func(t *testing.T) {
		base := &models.Alert{
			ID:       uuid.New(),
			Title:    "Pod Not Ready",
			Source:   "dynatrace",
			Severity: "high",
		}
		alertA := *base
		alertA.Labels = map[string]string{"cluster": "cluster-a"}
		alertB := *base
		alertB.Labels = map[string]string{"cluster": "cluster-b"}

		if alertDedupKey(&alertA) == alertDedupKey(&alertB) {
			t.Error("alerts with different clusters should produce different dedup keys")
		}
	})

	t.Run("nil labels does not panic", func(t *testing.T) {
		alert := &models.Alert{
			ID:       uuid.New(),
			Title:    "Test Alert",
			Source:   "prometheus",
			Severity: "medium",
			Labels:   nil,
		}
		// Must not panic.
		_ = alertDedupKey(alert)
	})

	t.Run("separator character present", func(t *testing.T) {
		alert := &models.Alert{
			ID:       uuid.New(),
			Title:    "Some Alert",
			Source:   "prometheus",
			Severity: "low",
			Labels:   map[string]string{},
		}
		key := alertDedupKey(alert)
		if !strings.Contains(key, "|") {
			t.Errorf("dedup key %q should use pipe separator", key)
		}
	})
}

// ---------------------------------------------------------------------------
// extractTitleKey
// ---------------------------------------------------------------------------

func TestExtractTitleKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantSubs []string // substrings that must be present
		wantNot  []string // substrings that must NOT be present
	}{
		{
			name:     "strips [ALERT] prefix",
			input:    "[ALERT] High CPU",
			wantSubs: []string{"high cpu"},
			wantNot:  []string{"[alert]"},
		},
		{
			name:     "strips [CRITICAL] prefix",
			input:    "[CRITICAL] Disk Full",
			wantSubs: []string{"disk full"},
			wantNot:  []string{"[critical]"},
		},
		{
			name:     "strips alert: prefix",
			input:    "alert: service degraded",
			wantSubs: []string{"service degraded"},
			wantNot:  []string{"alert:"},
		},
		{
			name:     "normalises to lowercase",
			input:    "High CPU on Node",
			wantSubs: []string{"high cpu on node"},
		},
		{
			name:     "truncates long titles at 50 runes",
			input:    strings.Repeat("a", 60),
			wantSubs: []string{strings.Repeat("a", 50)},
		},
		{
			name:     "short title unchanged",
			input:    "pod crash",
			wantSubs: []string{"pod crash"},
		},
		{
			name:     "empty title returns empty string",
			input:    "",
			wantSubs: []string{""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTitleKey(tc.input)
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("extractTitleKey(%q) = %q, want it to contain %q", tc.input, got, sub)
				}
			}
			for _, sub := range tc.wantNot {
				if strings.Contains(got, sub) {
					t.Errorf("extractTitleKey(%q) = %q, should NOT contain %q", tc.input, got, sub)
				}
			}
			// Length constraint: must not exceed 50 runes.
			if len([]rune(got)) > 50 {
				t.Errorf("extractTitleKey(%q) = %q exceeds 50 runes (got %d)", tc.input, got, len([]rune(got)))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// processAlertResolvedStage — pure logic, no DB
// ---------------------------------------------------------------------------

func TestProcessAlertResolvedStage(t *testing.T) {
	// Build a minimal service with no DB so resolved-path stops at the early-return
	// inside handleResolvedAlert (db == nil guard).  We only care about the bool return
	// value of processAlertResolvedStage itself.
	svc := &AlertPipelineService{}

	t.Run("resolved status returns terminal=true", func(t *testing.T) {
		alert := &models.Alert{
			ID:     uuid.New(),
			Status: "resolved",
			Source: "prometheus",
		}
		terminal := svc.processAlertResolvedStage(nil, alert) //nolint:staticcheck
		if !terminal {
			t.Error("expected terminal=true for resolved alert")
		}
	})

	t.Run("RESOLVED upper-case is also terminal", func(t *testing.T) {
		alert := &models.Alert{
			ID:     uuid.New(),
			Status: "RESOLVED",
			Source: "datadog",
		}
		terminal := svc.processAlertResolvedStage(nil, alert)
		if !terminal {
			t.Error("expected terminal=true for RESOLVED (upper-case) alert")
		}
	})

	t.Run("open status returns terminal=false", func(t *testing.T) {
		alert := &models.Alert{
			ID:     uuid.New(),
			Status: "open",
			Source: "prometheus",
		}
		terminal := svc.processAlertResolvedStage(nil, alert)
		if terminal {
			t.Error("expected terminal=false for open alert")
		}
	})

	t.Run("empty status returns terminal=false", func(t *testing.T) {
		alert := &models.Alert{
			ID:     uuid.New(),
			Status: "",
			Source: "prometheus",
		}
		terminal := svc.processAlertResolvedStage(nil, alert)
		if terminal {
			t.Error("expected terminal=false for alert with empty status")
		}
	})

	t.Run("acknowledged status returns terminal=false", func(t *testing.T) {
		alert := &models.Alert{
			ID:     uuid.New(),
			Status: "acknowledged",
			Source: "dynatrace",
		}
		terminal := svc.processAlertResolvedStage(nil, alert)
		if terminal {
			t.Error("expected terminal=false for acknowledged alert")
		}
	})
}

// ---------------------------------------------------------------------------
// runStaleSweep — db==nil early-return guard (only on the ticker wrapper)
// ---------------------------------------------------------------------------
//
// runStaleSweep (the ticker loop) checks `s.db == nil` and returns immediately
// before starting the 1-hour ticker.  sweepStaleAlerts / sweepStaleIncidents do
// NOT have that guard — they call s.db.QueryContext directly and must only be
// called when db != nil.

func TestRunStaleSweep_NilDBExitsImmediately(t *testing.T) {
	svc := &AlertPipelineService{} // db is nil

	done := make(chan struct{})
	go func() {
		defer close(done)
		// runStaleSweep returns immediately when s.db == nil.
		svc.runStaleSweep(nil) //nolint:staticcheck
	}()

	// Should return well within a millisecond; 100ms is generous.
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
		// Good: exited without blocking.
	case <-timer.C:
		t.Fatal("runStaleSweep did not return promptly with nil db — expected immediate exit")
	}
}
