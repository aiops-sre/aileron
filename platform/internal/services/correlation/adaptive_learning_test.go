package correlation

import (
	"context"
	"testing"
)

// TestDefaultWeights_MatchGoLiveConfig verifies defaultWeights() matches the
// go-live weights in parallel_correlation_engine.go (Topo 45%, Rules 25%,
// Sem 20%, Temp 10%). A mismatch causes adaptive learning to return wrong
// weights before 10 samples are collected.
func TestDefaultWeights_MatchGoLiveConfig(t *testing.T) {
	w := defaultWeights()
	if w.Topology != 0.45 {
		t.Errorf("Topology weight = %.2f, want 0.45 (go-live weight)", w.Topology)
	}
	if w.Rules != 0.25 {
		t.Errorf("Rules weight = %.2f, want 0.25", w.Rules)
	}
	if w.Semantic != 0.20 {
		t.Errorf("Semantic weight = %.2f, want 0.20", w.Semantic)
	}
	if w.Temporal != 0.10 {
		t.Errorf("Temporal weight = %.2f, want 0.10", w.Temporal)
	}
	sum := w.Topology + w.Rules + w.Semantic + w.Temporal
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("weights sum = %.4f, want 1.00 (± 0.01)", sum)
	}
}

// TestAdaptiveLearning_DisabledReturnDefaults verifies that when learning is
// disabled, GetWeightsForContext always returns the default weights.
func TestAdaptiveLearning_DisabledReturnDefaults(t *testing.T) {
	engine := NewAdaptiveLearningEngine(nil, nil, nil)
	engine.SetDisabled(true)

	ctx := context.Background()
	for i := 0; i < 20; i++ {
		engine.OnFeedback(ctx, LearningEvent{
			Domain:    DomainKubernetes,
			Source:    "dynatrace",
			IsCorrect: true,
			StrategyScores: map[string]float64{
				"semantic": 0.9, "topology": 0.1,
			},
		})
	}

	want := defaultWeights()
	got := engine.GetWeightsForContext(&Alert{Source: "dynatrace"})
	if got.Topology != want.Topology || got.Semantic != want.Semantic {
		t.Errorf("disabled engine returned learned weights, want defaults: got=%+v want=%+v", got, want)
	}
}

// TestAdaptiveLearning_OnFeedback_NoopWhenDisabled verifies that OnFeedback
// does not update internal state when the engine is disabled.
func TestAdaptiveLearning_OnFeedback_NoopWhenDisabled(t *testing.T) {
	engine := NewAdaptiveLearningEngine(nil, nil, nil)
	engine.SetDisabled(true)

	ctx := context.Background()
	for i := 0; i < 15; i++ {
		engine.OnFeedback(ctx, LearningEvent{
			Domain:    DomainStorage,
			Source:    "prometheus",
			IsCorrect: true,
		})
	}

	if len(engine.features) != 0 {
		t.Errorf("expected empty features map when disabled, got %d entries", len(engine.features))
	}
}

// TestAdaptiveLearning_EnabledAcceptsFeedback verifies that when enabled,
// OnFeedback populates the features map.
func TestAdaptiveLearning_EnabledAcceptsFeedback(t *testing.T) {
	engine := NewAdaptiveLearningEngine(nil, nil, nil)
	// disabled=false by default

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		engine.OnFeedback(ctx, LearningEvent{
			Domain:    DomainKubernetes,
			Source:    "dynatrace",
			IsCorrect: true,
		})
	}

	if len(engine.features) == 0 {
		t.Error("expected features map to be populated after OnFeedback calls, got empty")
	}
}

// TestAdaptiveLearning_WeightsSumToOne verifies that all returned weight sets
// always sum to 1.0.
func TestAdaptiveLearning_WeightsSumToOne(t *testing.T) {
	checkSum := func(name string, w StrategyWeights) {
		sum := w.Topology + w.Rules + w.Semantic + w.Temporal
		if sum < 0.98 || sum > 1.02 {
			t.Errorf("%s: weights sum = %.4f, want 1.0 ± 0.02", name, sum)
		}
	}

	checkSum("defaultWeights()", defaultWeights())

	e1 := NewAdaptiveLearningEngine(nil, nil, nil)
	e1.SetDisabled(false)
	checkSum("fresh enabled engine", e1.GetWeightsForContext(&Alert{}))

	e2 := NewAdaptiveLearningEngine(nil, nil, nil)
	e2.SetDisabled(true)
	checkSum("disabled engine", e2.GetWeightsForContext(&Alert{}))
}
