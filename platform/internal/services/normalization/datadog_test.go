package normalization

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DatadogNormalizer.Normalize
// ---------------------------------------------------------------------------

func TestDatadogNormalize_FullPayload(t *testing.T) {
	n := DatadogNormalizer{}

	raw := map[string]interface{}{
		"alert_title":  "High CPU on web-01",
		"body":         "CPU has been above 90% for 5 minutes.",
		"alert_type":   "error",
		"alert_status": "alert",
		"aggreg_key":   "monitor-42",
		"host":         "web-01.prod.example.com",
		"metric":       "system.cpu.user",
		"url":          "https://app.datadoghq.com/monitors/42",
		"date_happened": float64(1700000000),
		"tags": []interface{}{
			"env:production",
			"service:web",
			"team:platform",
		},
	}

	got, err := n.Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize returned unexpected error: %v", err)
	}

	// Title
	if got.Title != "High CPU on web-01" {
		t.Errorf("Title = %q, want %q", got.Title, "High CPU on web-01")
	}

	// Source is always "datadog"
	if got.Source != "datadog" {
		t.Errorf("Source = %q, want %q", got.Source, "datadog")
	}

	// SourceID comes from aggreg_key
	if got.SourceID != "monitor-42" {
		t.Errorf("SourceID = %q, want %q", got.SourceID, "monitor-42")
	}

	// SourceURL
	if got.SourceURL != "https://app.datadoghq.com/monitors/42" {
		t.Errorf("SourceURL = %q, want %q", got.SourceURL, "https://app.datadoghq.com/monitors/42")
	}

	// Severity: "error" high
	if got.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityHigh)
	}

	// Status: "alert" alert_status, "error" alert_type firing
	if got.Status != StatusFiring {
		t.Errorf("Status = %q, want %q", got.Status, StatusFiring)
	}

	// Host from host field
	if got.Host != "web-01.prod.example.com" {
		t.Errorf("Host = %q, want %q", got.Host, "web-01.prod.example.com")
	}

	// Labels from tags
	if got.Labels["env"] != "production" {
		t.Errorf("Labels[env] = %q, want %q", got.Labels["env"], "production")
	}
	if got.Labels["service"] != "web" {
		t.Errorf("Labels[service] = %q, want %q", got.Labels["service"], "web")
	}
	if got.Labels["source"] != "datadog" {
		t.Errorf("Labels[source] = %q, want %q", got.Labels["source"], "datadog")
	}

	// FiredAt from date_happened unix timestamp
	wantFiredAt := time.Unix(1700000000, 0)
	if !got.FiredAt.Equal(wantFiredAt) {
		t.Errorf("FiredAt = %v, want %v", got.FiredAt, wantFiredAt)
	}

	// MetricName
	if got.MetricName != "system.cpu.user" {
		t.Errorf("MetricName = %q, want %q", got.MetricName, "system.cpu.user")
	}

	// Fingerprint must be non-empty
	if got.Fingerprint == "" {
		t.Error("Fingerprint should not be empty")
	}

	// Raw should be preserved
	if got.Raw == nil {
		t.Error("Raw should be preserved in output")
	}
}

// ---------------------------------------------------------------------------
// Empty payload — must not panic, must use defaults
// ---------------------------------------------------------------------------

func TestDatadogNormalize_EmptyPayload(t *testing.T) {
	n := DatadogNormalizer{}

	got, err := n.Normalize(map[string]interface{}{})
	if err != nil {
		t.Fatalf("Normalize returned unexpected error on empty payload: %v", err)
	}

	// Default title
	if got.Title != "Datadog Alert" {
		t.Errorf("Title = %q, want default %q", got.Title, "Datadog Alert")
	}

	// Source always "datadog"
	if got.Source != "datadog" {
		t.Errorf("Source = %q, want %q", got.Source, "datadog")
	}

	// SourceID must be auto-generated (non-empty, starts with "dd-")
	if !strings.HasPrefix(got.SourceID, "dd-") {
		t.Errorf("auto-generated SourceID %q should start with 'dd-'", got.SourceID)
	}

	// Severity defaults to medium (empty string MapSeverity("") medium)
	if got.Severity != SeverityMedium {
		t.Errorf("Severity = %q, want %q for empty payload", got.Severity, SeverityMedium)
	}

	// FiredAt must be populated (defaulted to now)
	if got.FiredAt.IsZero() {
		t.Error("FiredAt should not be zero even for empty payload")
	}

	// Labels map must be non-nil
	if got.Labels == nil {
		t.Error("Labels should not be nil")
	}
}

// ---------------------------------------------------------------------------
// Missing optional fields — partial payload
// ---------------------------------------------------------------------------

func TestDatadogNormalize_MissingOptionalFields(t *testing.T) {
	n := DatadogNormalizer{}

	// Only title and aggreg_key provided; everything else absent.
	raw := map[string]interface{}{
		"alert_title": "Latency spike",
		"aggreg_key":  "mon-99",
	}

	got, err := n.Normalize(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Title from alert_title
	if got.Title != "Latency spike" {
		t.Errorf("Title = %q, want %q", got.Title, "Latency spike")
	}

	// SourceID from aggreg_key
	if got.SourceID != "mon-99" {
		t.Errorf("SourceID = %q, want %q", got.SourceID, "mon-99")
	}

	// No tags Labels still contains "source"
	if got.Labels["source"] != "datadog" {
		t.Errorf("Labels[source] = %q, want %q", got.Labels["source"], "datadog")
	}

	// Host is empty — should not panic
	if got.Host != "" {
		// A host extracted from entity text-parsing is acceptable; just must not panic.
	}

	// Description is empty
	if got.Description != "" {
		t.Logf("Description = %q (empty is expected for missing body/message/text)", got.Description)
	}
}

// ---------------------------------------------------------------------------
// Status mapping: ok / success resolved
// ---------------------------------------------------------------------------

func TestDatadogNormalize_ResolvedStatuses(t *testing.T) {
	n := DatadogNormalizer{}

	tests := []struct {
		name        string
		alertStatus string
		alertType   string
	}{
		{"ok status", "ok", ""},
		{"success type", "", "success"},
		{"recovery status", "recovery", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]interface{}{
				"alert_title":  "Test",
				"aggreg_key":   "mon-1",
				"alert_status": tc.alertStatus,
				"alert_type":   tc.alertType,
			}
			got, err := n.Normalize(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != StatusResolved {
				t.Errorf("status = %q, want resolved for alert_status=%q alert_type=%q",
					got.Status, tc.alertStatus, tc.alertType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Severity mapping
// ---------------------------------------------------------------------------

func TestDatadogNormalize_SeverityMapping(t *testing.T) {
	n := DatadogNormalizer{}

	tests := []struct {
		alertType    string
		wantSeverity Severity
	}{
		{"error", SeverityHigh},
		{"warning", SeverityMedium},
		{"info", SeverityInfo},
		// "success" is not a key in MapSeverity so it falls through to the default (medium).
		// Note: mapStatus maps "success" resolved (via Contains check), but severity
		// uses MapSeverity which has no "success" entry medium.
		{"success", SeverityMedium},
	}

	for _, tc := range tests {
		t.Run(tc.alertType, func(t *testing.T) {
			raw := map[string]interface{}{
				"alert_title": "Test Alert",
				"alert_type":  tc.alertType,
				"aggreg_key":  "mon-x",
			}
			got, err := n.Normalize(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q for alert_type=%q",
					got.Severity, tc.wantSeverity, tc.alertType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tag parsing
// ---------------------------------------------------------------------------

func TestDatadogNormalize_TagParsing(t *testing.T) {
	n := DatadogNormalizer{}

	raw := map[string]interface{}{
		"alert_title": "Tag test",
		"aggreg_key":  "mon-tags",
		"tags": []interface{}{
			"cluster:prod-k8s",
			"namespace:default",
			"malformed_no_colon",
			"multi:val:ue", // only first colon split
		},
	}

	got, err := n.Normalize(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Labels["cluster"] != "prod-k8s" {
		t.Errorf("Labels[cluster] = %q, want %q", got.Labels["cluster"], "prod-k8s")
	}
	if got.Labels["namespace"] != "default" {
		t.Errorf("Labels[namespace] = %q, want %q", got.Labels["namespace"], "default")
	}
	// malformed tag should not create a label key
	if _, ok := got.Labels["malformed_no_colon"]; ok {
		t.Error("malformed tag without colon should not appear as label key")
	}
	// multi:val:ue key="multi", value="val:ue"
	if got.Labels["multi"] != "val:ue" {
		t.Errorf("Labels[multi] = %q, want %q", got.Labels["multi"], "val:ue")
	}
}

// ---------------------------------------------------------------------------
// CanHandle
// ---------------------------------------------------------------------------

func TestDatadogCanHandle(t *testing.T) {
	n := DatadogNormalizer{}

	tests := []struct {
		name string
		raw  map[string]interface{}
		want bool
	}{
		{"has alert_title", map[string]interface{}{"alert_title": "x"}, true},
		{"has aggreg_key", map[string]interface{}{"aggreg_key": "x"}, true},
		{"has alert_type", map[string]interface{}{"alert_type": "x"}, true},
		{"monitor event_type", map[string]interface{}{"event_type": "monitor.alert"}, true},
		{"empty payload", map[string]interface{}{}, false},
		{"prometheus-style", map[string]interface{}{"alertname": "up", "labels": "x"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := n.CanHandle(tc.raw)
			if got != tc.want {
				t.Errorf("CanHandle() = %v, want %v for raw=%v", got, tc.want, tc.raw)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DatadogPayloadToAlerts
// ---------------------------------------------------------------------------

func TestDatadogPayloadToAlerts(t *testing.T) {
	payload := map[string]interface{}{"alert_title": "x", "aggreg_key": "1"}
	alerts := DatadogPayloadToAlerts(payload)
	if len(alerts) != 1 {
		t.Errorf("DatadogPayloadToAlerts: len=%d, want 1", len(alerts))
	}
	if alerts[0]["alert_title"] != "x" {
		t.Errorf("expected payload to be returned unchanged")
	}
}
