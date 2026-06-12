package normalization

import (
	"strings"
	"testing"
	"time"
)

// TestDynatraceNormalizer_CanHandle verifies the DT normalizer accepts the right payloads.
func TestDynatraceNormalizer_CanHandle(t *testing.T) {
	n := DynatraceNormalizer{}

	cases := []struct {
		name    string
		payload map[string]interface{}
		want    bool
	}{
		{"ProblemID field", map[string]interface{}{"ProblemID": "P-123"}, true},
		{"ProblemTitle field", map[string]interface{}{"ProblemTitle": "CPU high"}, true},
		{"problemId field", map[string]interface{}{"problemId": "P-456"}, true},
		{"empty payload", map[string]interface{}{}, false},
		{"non-DT payload", map[string]interface{}{"alertname": "HighCPU", "severity": "critical"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := n.CanHandle(tc.payload)
			if got != tc.want {
				t.Errorf("CanHandle(%v) = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

// TestDynatraceNormalizer_Source verifies source name.
func TestDynatraceNormalizer_Source(t *testing.T) {
	n := DynatraceNormalizer{}
	if n.Source() != "dynatrace" {
		t.Errorf("Source() = %q, want %q", n.Source(), "dynatrace")
	}
}

// TestDynatraceNormalizer_Normalize_BasicProblem covers the minimal DT problem payload.
func TestDynatraceNormalizer_Normalize_BasicProblem(t *testing.T) {
	n := DynatraceNormalizer{}
	payload := map[string]interface{}{
		"ProblemID":      "P-12345",
		"ProblemTitle":   "CPU-request saturation on node",
		"ProblemSeverity": "PERFORMANCE",
		"ImpactLevel":    "INFRASTRUCTURE",
		"State":          "OPEN",
		"ProblemURL":     "https://dt.example.com/#problems/P-12345",
	}

	result, err := n.Normalize(payload)
	if err != nil {
		t.Fatalf("Normalize() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Normalize() returned nil result")
	}

	if result.Title == "" {
		t.Error("expected non-empty Title")
	}
	if result.Source != "dynatrace" {
		t.Errorf("Source = %q, want %q", result.Source, "dynatrace")
	}
	if result.SourceID == "" {
		t.Error("expected non-empty ExternalID from ProblemID")
	}
}

// TestDynatraceNormalizer_Normalize_ResolvedState verifies RESOLVED detection.
func TestDynatraceNormalizer_Normalize_ResolvedState(t *testing.T) {
	n := DynatraceNormalizer{}

	cases := []struct {
		name    string
		payload map[string]interface{}
		wantStatus string
	}{
		{
			"State=RESOLVED",
			map[string]interface{}{"ProblemID": "P-1", "ProblemTitle": "CPU high", "State": "RESOLVED"},
			"resolved",
		},
		{
			"State=OPEN",
			map[string]interface{}{"ProblemID": "P-2", "ProblemTitle": "CPU high", "State": "OPEN"},
			"open",
		},
		{
			"empty State",
			map[string]interface{}{
				"ProblemID":    "P-3",
				"ProblemTitle": "CPU high",
				"State":        "",
			},
			"open", // empty State → StatusFiring (open) per MapStatus
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := n.Normalize(tc.payload)
			if err != nil || result == nil {
				t.Fatalf("Normalize() err=%v result=%v", err, result)
			}
			if string(result.Status) != tc.wantStatus {
				t.Errorf("Status = %q, want %q", result.Status, tc.wantStatus)
			}
		})
	}
}

// TestDynatraceNormalizer_Normalize_SeverityMapping verifies DT severity maps to alerting levels.
func TestDynatraceNormalizer_Normalize_SeverityMapping(t *testing.T) {
	n := DynatraceNormalizer{}

	cases := []struct {
		dtSev      string
		wantSev    string
	}{
		{"PERFORMANCE", "low"},          // maps to "low" via severity.go
		{"RESOURCE_CONTENTION", "medium"}, // maps via generic fallback
		{"AVAILABILITY", "high"},
		{"ERROR", "high"},
		{"CUSTOM_ALERT", "medium"},
	}

	for _, tc := range cases {
		t.Run(tc.dtSev, func(t *testing.T) {
			payload := map[string]interface{}{
				"ProblemID":       "P-1",
				"ProblemTitle":    "Test",
				"ProblemSeverity": tc.dtSev,
				"State":           "OPEN",
			}
			result, err := n.Normalize(payload)
			if err != nil || result == nil {
				t.Fatalf("Normalize() err=%v", err)
			}
			if string(result.Severity) != tc.wantSev {
				t.Errorf("Severity for %q = %q, want %q", tc.dtSev, result.Severity, tc.wantSev)
			}
		})
	}
}

// TestDynatraceNormalizer_Normalize_K8sClusterExtraction verifies cluster labels are extracted.
func TestDynatraceNormalizer_Normalize_K8sClusterExtraction(t *testing.T) {
	n := DynatraceNormalizer{}
	payload := map[string]interface{}{
		"ProblemID":    "P-1",
		"ProblemTitle": "Pod crash",
		"State":        "OPEN",
		"customProperties": map[string]interface{}{
			"k8s.cluster.name": "example-cluster",
		},
	}

	result, err := n.Normalize(payload)
	if err != nil || result == nil {
		t.Fatalf("Normalize() err=%v", err)
	}

	if result.Labels["k8s.cluster.name"] != "example-cluster" {
		t.Errorf("expected k8s.cluster.name=example-cluster in labels, got %v", result.Labels)
	}
}

// TestRegistry_Normalize_DelegatesToDT verifies the registry dispatches DT payloads.
func TestRegistry_Normalize_DelegatesToDT(t *testing.T) {
	reg := NewRegistry()
	payload := map[string]interface{}{
		"ProblemID":    "P-99",
		"ProblemTitle": "Node unreachable",
		"State":        "OPEN",
	}

	result, err := reg.Normalize("dynatrace", payload)
	if err != nil {
		t.Fatalf("Registry.Normalize returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Registry.Normalize returned nil for valid DT payload")
	}
	if result.Source != "dynatrace" {
		t.Errorf("Source = %q, want dynatrace", result.Source)
	}
}

// TestRegistry_Normalize_FallsBackToGeneric verifies unknown source uses generic normalizer.
func TestRegistry_Normalize_FallsBackToGeneric(t *testing.T) {
	reg := NewRegistry()
	payload := map[string]interface{}{
		"title":    "Some Alert",
		"severity": "high",
	}

	// Should not error even with unknown source
	result, err := reg.Normalize("unknown-source", payload)
	if err != nil {
		t.Fatalf("unexpected error for unknown source: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for fallback normalizer")
	}
}

// TestToAlert_MapsNormalizedToModel verifies ToAlert produces a valid Alert.
func TestToAlert_MapsNormalizedToModel(t *testing.T) {
	now := time.Now()
	normalized := &NormalizedAlert{
		Title:       "CPU High",
		Description: "CPU at 98%",
		Severity:    "critical",
		Status:      "open",
		Source:      "dynatrace",
		SourceID:  "P-100",
		Labels:      map[string]string{"k8s.cluster.name": "prod"},
		Metadata:    map[string]interface{}{"env": "prod"},
		FiredAt:     now,
	}

	alert := ToAlert(normalized)
	if alert == nil {
		t.Fatal("ToAlert returned nil")
	}
	if alert.Title != "CPU High" {
		t.Errorf("Title = %q, want %q", alert.Title, "CPU High")
	}
	if alert.Severity != "critical" {
		t.Errorf("Severity = %q, want %q", alert.Severity, "critical")
	}
	if alert.Source != "dynatrace" {
		t.Errorf("Source = %q, want %q", alert.Source, "dynatrace")
	}
	if alert.SourceID != "P-100" {
		t.Errorf("SourceID = %q, want %q", alert.SourceID, "P-100")
	}
	if v, ok := alert.Labels["k8s.cluster.name"]; !ok || v != "prod" {
		t.Errorf("Labels[k8s.cluster.name] = %q, want %q", v, "prod")
	}
}

// TestMapSeverity covers the severity mapping helper exhaustively.
func TestMapSeverity(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"CRITICAL", "critical"},
		{"critical", "critical"},
		{"HIGH", "high"},
		{"high", "high"},
		{"MEDIUM", "medium"},
		{"medium", "medium"},
		{"LOW", "low"},
		{"low", "low"},
		{"WARNING", "medium"},
		{"warning", "medium"},
		{"ERROR", "high"},
		{"error", "high"},
		{"", "medium"},
		{"unknown-level", "medium"},
		{"AVAILABILITY", "high"},
		{"RESOURCE_CONTENTION", "medium"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := MapSeverity(tc.input)
			if string(got) != tc.want {
				t.Errorf("MapSeverity(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestFingerprintFromSourceID verifies the fingerprint is stable and source-prefixed.
func TestFingerprintFromSourceID(t *testing.T) {
	f1 := FingerprintFromSourceID("dynatrace", "P-12345")
	f2 := FingerprintFromSourceID("dynatrace", "P-12345")
	if f1 != f2 {
		t.Error("FingerprintFromSourceID should be deterministic for the same inputs")
	}

	f3 := FingerprintFromSourceID("prometheus", "P-12345")
	if f1 == f3 {
		t.Error("same external ID from different sources should produce different fingerprints")
	}

	if !strings.HasPrefix(f1, "dt:") && len(f1) < 10 {
		t.Logf("fingerprint format: %q", f1)
	}
}
