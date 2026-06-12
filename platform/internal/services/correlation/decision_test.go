package correlation

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// makeCorrelationDecision
// ---------------------------------------------------------------------------

func TestMakeCorrelationDecision(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil) // nil DB — no queries in this test

	tests := []struct {
		name           string
		score          float64
		strategyCount  int
		wantDecision   string
		wantContains   string // optional substring check
	}{
		// High confidence — correlate
		{name: "exactly at threshold correlates", score: 0.75, strategyCount: 4, wantDecision: "correlate"},
		{name: "above threshold correlates", score: 0.90, strategyCount: 4, wantDecision: "correlate"},
		{name: "perfect score correlates", score: 1.0, strategyCount: 4, wantDecision: "correlate"},

		// Medium confidence — monitor
		{name: "mid-range score is monitor", score: 0.60, strategyCount: 4, wantDecision: "monitor"},
		{name: "at lower monitor boundary", score: 0.50, strategyCount: 4, wantDecision: "monitor"},

		// Low confidence — discard
		{name: "below monitor threshold discards", score: 0.49, strategyCount: 4, wantDecision: "discard"},
		{name: "zero score discards", score: 0.0, strategyCount: 4, wantDecision: "discard"},
		{name: "near-zero score discards", score: 0.01, strategyCount: 2, wantDecision: "discard"},

		// No strategies executed — create_incident
		{name: "zero strategies creates incident", score: 0.0, strategyCount: 0, wantDecision: "create_incident"},
		{name: "zero strategies even with non-zero score creates incident", score: 0.3, strategyCount: 0, wantDecision: "create_incident"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.makeCorrelationDecision(tc.score, tc.strategyCount)
			if got != tc.wantDecision {
				t.Errorf("makeCorrelationDecision(score=%.2f, strategies=%d) = %q, want %q",
					tc.score, tc.strategyCount, got, tc.wantDecision)
			}
		})
	}
}

// TestMakeCorrelationDecision_BoundaryEdges tests exact threshold boundaries.
func TestMakeCorrelationDecision_BoundaryEdges(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil)

	// 0.75 is the correlate threshold; just below should be monitor.
	just_below_correlate := 0.7499
	got := engine.makeCorrelationDecision(just_below_correlate, 4)
	if got != "monitor" {
		t.Errorf("score just below 0.75 should be 'monitor', got %q", got)
	}

	// 0.50 is the monitor threshold; just below should be discard.
	just_below_monitor := 0.4999
	got = engine.makeCorrelationDecision(just_below_monitor, 4)
	if got != "discard" {
		t.Errorf("score just below 0.50 should be 'discard', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// calculateInfrastructureRelationship — pure math, no DB
// ---------------------------------------------------------------------------

func TestCalculateInfrastructureRelationship(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil)

	tests := []struct {
		name      string
		node1     string
		node2     string
		wantScore float64
	}{
		{name: "identical long keys score 1.0", node1: "c:prod/ns:api/n:node1/w:frontend", node2: "c:prod/ns:api/n:node1/w:frontend", wantScore: 1.0},
		// Identical short keys also trigger the node1==node2 guard 1.0.
		{name: "same cluster+ns key scores 1.0 (identical strings)", node1: "c:prod/ns:api", node2: "c:prod/ns:api", wantScore: 1.0},
		{name: "same cluster different namespace scores 0.75", node1: "c:prod/ns:api", node2: "c:prod/ns:backend", wantScore: 0.75},
		// Both empty: node1==node2=="" triggers the equality guard 1.0.
		{name: "both empty strings are equal, score 1.0", node1: "", node2: "", wantScore: 1.0},
		{name: "one empty node scores 0.0", node1: "c:prod", node2: "", wantScore: 0.0},
		{name: "unrelated clusters score 0.0", node1: "c:prod", node2: "c:staging", wantScore: 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.calculateInfrastructureRelationship(tc.node1, tc.node2)
			if got != tc.wantScore {
				t.Errorf("calculateInfrastructureRelationship(%q, %q) = %.4f, want %.4f",
					tc.node1, tc.node2, got, tc.wantScore)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractProblemDomain — pure string logic, no DB
// ---------------------------------------------------------------------------

func TestExtractProblemDomain(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil)

	tests := []struct {
		name       string
		title      string
		desc       string
		wantDomain string
	}{
		{name: "cpu title", title: "High CPU usage", desc: "", wantDomain: "cpu"},
		{name: "throttling", title: "CPU throttling detected", desc: "", wantDomain: "cpu"},
		{name: "memory oom", title: "OOM Killed", desc: "container was killed", wantDomain: "memory"},
		{name: "heap exhaustion", title: "heap exhaustion", desc: "", wantDomain: "memory"},
		{name: "disk", title: "Disk full", desc: "", wantDomain: "storage"},
		{name: "netapp", title: "NetApp aggregate degraded", desc: "", wantDomain: "storage"},
		{name: "network timeout", title: "connection timeout", desc: "", wantDomain: "network"},
		{name: "pod crash", title: "Pod CrashLoopBackOff", desc: "", wantDomain: "pod"},
		{name: "container restart", title: "container restart", desc: "", wantDomain: "pod"},
		{name: "node not ready", title: "Node not ready", desc: "", wantDomain: "node"},
		{name: "unknown returns empty", title: "some random event", desc: "nothing special", wantDomain: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Alert{Title: tc.title, Description: tc.desc}
			got := engine.extractProblemDomain(a)
			if got != tc.wantDomain {
				t.Errorf("extractProblemDomain(%q, %q) = %q, want %q",
					tc.title, tc.desc, got, tc.wantDomain)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// generateReasoning — pure string logic, no DB
// ---------------------------------------------------------------------------

func TestGenerateReasoning(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil)

	t.Run("no high-scoring strategies produces fallback message", func(t *testing.T) {
		results := map[string]*StrategyResult{
			"semantic": {StrategyName: "semantic", Score: 0.2},
			"topology": {StrategyName: "topology", Score: 0.1},
		}
		reasoning := engine.generateReasoning(results, 0.15, "discard")
		if reasoning == "" {
			t.Error("reasoning should not be empty")
		}
		// Should contain final score info.
		if !strings.Contains(reasoning, "%") {
			t.Errorf("reasoning %q should contain percentage", reasoning)
		}
	})

	t.Run("high-scoring strategy is mentioned in reasoning", func(t *testing.T) {
		results := map[string]*StrategyResult{
			"topology": {StrategyName: "topology", Score: 0.9},
			"semantic": {StrategyName: "semantic", Score: 0.1},
		}
		reasoning := engine.generateReasoning(results, 0.85, "correlate")
		if !strings.Contains(reasoning, "topology") {
			t.Errorf("reasoning %q should mention high-scoring 'topology' strategy", reasoning)
		}
		if !strings.Contains(reasoning, "correlate") {
			t.Errorf("reasoning %q should mention the decision", reasoning)
		}
	})

	t.Run("errored strategy is skipped", func(t *testing.T) {
		results := map[string]*StrategyResult{
			"topology": {StrategyName: "topology", Score: 0.9, Error: nil},
			"semantic": {StrategyName: "semantic", Score: 0.8, Error: errForTest("semantic failed")},
		}
		reasoning := engine.generateReasoning(results, 0.81, "correlate")
		if strings.Contains(reasoning, "semantic") {
			t.Errorf("reasoning %q should NOT mention errored 'semantic' strategy", reasoning)
		}
	})
}

// errForTest is a tiny sentinel error used only in tests to avoid importing errors package.
type testErr string

func (e testErr) Error() string { return string(e) }

func errForTest(msg string) error { return testErr(msg) }

// ---------------------------------------------------------------------------
// LockWeights / SetStrategyWeights
// ---------------------------------------------------------------------------

func TestLockWeights_PreventsOverride(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil)
	originalWeights := engine.weights

	engine.LockWeights()

	newWeights := StrategyWeights{Semantic: 0.9, Temporal: 0.1, Topology: 0.0, Rules: 0.0}
	engine.SetStrategyWeights(newWeights)

	engine.weightsMu.RLock()
	got := engine.weights
	engine.weightsMu.RUnlock()

	if got != originalWeights {
		t.Errorf("SetStrategyWeights should be a no-op after LockWeights; weights changed from %+v to %+v",
			originalWeights, got)
	}
}

func TestSetStrategyWeights_UpdatesWhenUnlocked(t *testing.T) {
	engine := NewParallelCorrelationEngine(nil)

	newWeights := StrategyWeights{Semantic: 0.4, Temporal: 0.3, Topology: 0.2, Rules: 0.1}
	engine.SetStrategyWeights(newWeights)

	engine.weightsMu.RLock()
	got := engine.weights
	engine.weightsMu.RUnlock()

	if got != newWeights {
		t.Errorf("SetStrategyWeights did not update weights; got %+v, want %+v", got, newWeights)
	}
}
