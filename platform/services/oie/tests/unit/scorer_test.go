package unit_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	hyp_app "github.com/aileron-platform/aileron/platform/services/oie/internal/application/hypothesis"
	hyp_templates "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/hypothesis_templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Scorer tests ───────────────────────────────────────────────────────────────

func TestScorer_BasePriorOnNoEvidence(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	result := scorer.Score(tmpl, nil)

	assert.Equal(t, tmpl.BasePrior, result.Confidence)
	assert.Equal(t, domain_hyp.StatusInsufficientEvidence, result.Status)
}

func TestScorer_SupportingEvidenceBoostsConfidence(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	// VM stopped + node NotReady should make this highly confident.
	evidence := []*domain_ev.Evidence{
		makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleSupports, 0.90, 0.95, domain_ev.TemporalCurrent, 30),
		makeEvidence(domain_ev.TypeK8sNodeCondition, domain_ev.RoleSupports, 0.88, 0.98, domain_ev.TemporalHistorical, 0),
	}

	result := scorer.Score(tmpl, evidence)

	assert.Greater(t, result.Confidence, 0.70, "two supporting evidence pieces should boost above 0.70")
	assert.Equal(t, domain_hyp.StatusActive, result.Status)
	assert.Greater(t, result.EvidenceBoost, 0.0)
}

func TestScorer_CorrelatedEvidenceGroupNotInflated(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "crashloop_oom_kill")

	oomGroup := "oom_signals"
	// Two OOM signals in the same group — should NOT inflate confidence via independence.
	ev1 := makeEvidence(domain_ev.TypeK8sOOMKill, domain_ev.RoleSupports, 0.92, 0.97, domain_ev.TemporalHistorical, 0)
	ev1.EvidenceGroup = &oomGroup
	ev2 := makeEvidence(domain_ev.TypeK8sPodExitCode, domain_ev.RoleSupports, 0.88, 0.97, domain_ev.TemporalHistorical, 0)
	ev2.EvidenceGroup = &oomGroup

	result := scorer.Score(tmpl, []*domain_ev.Evidence{ev1, ev2})

	// With independence-complement (broken): 1 - (1-0.92)(1-0.88) = 0.99
	// With group max-weight (correct):       base + 0.92*(1-base) ≈ 0.94
	assert.Less(t, result.Confidence, 0.97,
		"correlated OOM evidence should not inflate confidence above 0.97")
}

func TestScorer_ContradictionReducesConfidence(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	evidence := []*domain_ev.Evidence{
		makeEvidence(domain_ev.TypeK8sNodeCondition, domain_ev.RoleSupports, 0.88, 0.98, domain_ev.TemporalHistorical, 0),
		// VM is Running — contradicts this hypothesis.
		makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleContradicts, 0.88, 0.95, domain_ev.TemporalCurrent, 30),
	}

	withoutContradiction := scorer.Score(tmpl, []*domain_ev.Evidence{evidence[0]})
	withContradiction := scorer.Score(tmpl, evidence)

	assert.Less(t, withContradiction.Confidence, withoutContradiction.Confidence,
		"contradiction must reduce confidence")
	assert.Greater(t, withContradiction.ContradictionPenalty, 0.0)
}

func TestScorer_TemporalDampeningOnStaleContradiction(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	// Base evidence: K8s node is NotReady.
	base := makeEvidence(domain_ev.TypeK8sNodeCondition, domain_ev.RoleSupports, 0.88, 0.98, domain_ev.TemporalHistorical, 0)

	// Fresh contradiction (30s gap): full weight.
	freshContra := makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleContradicts, 0.88, 0.95, domain_ev.TemporalCurrent, 30)
	// Stale contradiction (240s gap): heavily dampened.
	staleContra := makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleContradicts, 0.88, 0.95, domain_ev.TemporalCurrent, 240)
	staleContra.Source = "cloudstack"

	resultFresh := scorer.Score(tmpl, []*domain_ev.Evidence{base, freshContra})
	resultStale := scorer.Score(tmpl, []*domain_ev.Evidence{base, staleContra})

	assert.Greater(t, resultStale.Confidence, resultFresh.Confidence,
		"stale contradiction must be dampened (higher confidence than fresh)")
}

func TestScorer_HardRejectionOnHistoricalEvidence(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "crashloop_bad_deployment")

	// OOM kill is a hard rejection for crashloop_bad_deployment.
	oomKillEv := makeEvidence(domain_ev.TypeK8sOOMKill, domain_ev.RoleSupports, 0.92, 0.97, domain_ev.TemporalHistorical, 0)

	result := scorer.Score(tmpl, []*domain_ev.Evidence{oomKillEv})

	assert.Equal(t, domain_hyp.StatusRejected, result.Status)
	assert.Equal(t, 0.0, result.Confidence)
	assert.NotNil(t, result.RejectionReason)
}

func TestScorer_HardRejectionNotAppliedToStaleCurrentEvidence(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	// CloudStack showing VM Running 3 minutes after incident.
	// Should NOT hard-reject because it's stale current-state evidence.
	staleRunning := makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleContradicts, 0.88, 0.95, domain_ev.TemporalCurrent, 180)
	staleRunning.Source = "cloudstack"

	result := scorer.Score(tmpl, []*domain_ev.Evidence{staleRunning})

	// Should not be hard-rejected — VM may have auto-recovered.
	assert.NotEqual(t, domain_hyp.StatusRejected, result.Status,
		"stale current-state contradiction should not hard-reject")
}

func TestScorer_MissingRequiredEvidencePenalty(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	// CloudStack VM state required but entity not in source (FetchMissing).
	missingEv := makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleContext, 0, 0, domain_ev.TemporalCurrent, 0)
	missingEv.FetchStatus = domain_ev.FetchMissing
	missingEv.Weight = 0

	// Without missing evidence.
	resultClean := scorer.Score(tmpl, nil)
	// With missing required evidence.
	resultMissing := scorer.Score(tmpl, []*domain_ev.Evidence{missingEv})

	assert.Less(t, resultMissing.Confidence, resultClean.Confidence,
		"missing required evidence (FetchMissing) must penalise confidence")
	assert.Contains(t, resultMissing.MissingRequired, string(domain_ev.TypeCloudStackVMState))
}

func TestScorer_MissingEvidenceNopenaltyOnTimeout(t *testing.T) {
	scorer := &hyp_app.Scorer{}
	tmpl := templateByType(t, "node_cloudstack_vm_failure")

	// CloudStack timed out — our problem, not the hypothesis' fault.
	timedOutEv := makeEvidence(domain_ev.TypeCloudStackVMState, domain_ev.RoleContext, 0, 0, domain_ev.TemporalCurrent, 0)
	timedOutEv.FetchStatus = domain_ev.FetchTimeout
	timedOutEv.Weight = 0

	resultClean := scorer.Score(tmpl, nil)
	resultTimeout := scorer.Score(tmpl, []*domain_ev.Evidence{timedOutEv})

	// Timeout should NOT add the evidence type to MissingRequired.
	assert.NotContains(t, resultTimeout.MissingRequired, string(domain_ev.TypeCloudStackVMState),
		"timeout should not be treated as missing evidence")
	// Confidence unchanged from timeout (no penalty).
	assert.Equal(t, resultClean.Confidence, resultTimeout.Confidence)
}

// ── RankHypotheses tests ───────────────────────────────────────────────────────

func TestRankHypotheses_ActiveBeforeInsufficient(t *testing.T) {
	h1 := &domain_hyp.Hypothesis{Type: "a", Status: domain_hyp.StatusInsufficientEvidence, Confidence: 0.30}
	h2 := &domain_hyp.Hypothesis{Type: "b", Status: domain_hyp.StatusActive, Confidence: 0.65}
	h3 := &domain_hyp.Hypothesis{Type: "c", Status: domain_hyp.StatusRejected, Confidence: 0.0}

	hyp_app.RankHypotheses([]*domain_hyp.Hypothesis{h1, h2, h3})

	require.NotNil(t, h2.Rank)
	require.NotNil(t, h1.Rank)
	require.NotNil(t, h3.Rank)

	assert.Equal(t, 1, *h2.Rank, "active hypothesis must rank first")
	assert.Equal(t, 2, *h1.Rank, "insufficient must rank before rejected")
	assert.Equal(t, 3, *h3.Rank, "rejected must rank last")
}

func TestRankHypotheses_ConfidenceOrdering(t *testing.T) {
	h1 := &domain_hyp.Hypothesis{Type: "a", Status: domain_hyp.StatusActive, Confidence: 0.50}
	h2 := &domain_hyp.Hypothesis{Type: "b", Status: domain_hyp.StatusActive, Confidence: 0.80}
	h3 := &domain_hyp.Hypothesis{Type: "c", Status: domain_hyp.StatusActive, Confidence: 0.65}

	hyp_app.RankHypotheses([]*domain_hyp.Hypothesis{h1, h2, h3})

	assert.Equal(t, 1, *h2.Rank) // highest confidence first
	assert.Equal(t, 2, *h3.Rank)
	assert.Equal(t, 3, *h1.Rank)
}

func TestWinnerFrom_ReturnsHighestNonRejected(t *testing.T) {
	rank1 := 1
	rank2 := 2
	rank3 := 3

	h1 := &domain_hyp.Hypothesis{Type: "winner", Status: domain_hyp.StatusActive, Confidence: 0.75, Rank: &rank1}
	h2 := &domain_hyp.Hypothesis{Type: "second", Status: domain_hyp.StatusActive, Confidence: 0.55, Rank: &rank2}
	h3 := &domain_hyp.Hypothesis{Type: "rejected", Status: domain_hyp.StatusRejected, Confidence: 0.0, Rank: &rank3}

	winner := hyp_app.WinnerFrom([]*domain_hyp.Hypothesis{h1, h2, h3})

	require.NotNil(t, winner)
	assert.Equal(t, "winner", winner.Type)
}

func TestWinnerFrom_NilWhenAllRejected(t *testing.T) {
	h1 := &domain_hyp.Hypothesis{Type: "a", Status: domain_hyp.StatusRejected, Confidence: 0.0}
	h2 := &domain_hyp.Hypothesis{Type: "b", Status: domain_hyp.StatusInsufficientEvidence, Confidence: 0.15}

	winner := hyp_app.WinnerFrom([]*domain_hyp.Hypothesis{h1, h2})
	assert.Nil(t, winner)
}

// ── Template library tests ─────────────────────────────────────────────────────

func TestTemplateLibrary_AllTemplatesValid(t *testing.T) {
	templates := hyp_templates.All()
	require.Greater(t, len(templates), 15, "library must have at least 15 built-in templates")

	for _, tmpl := range templates {
		t.Run(tmpl.ID, func(t *testing.T) {
			assert.NotEmpty(t, tmpl.ID, "template must have ID")
			assert.NotEmpty(t, tmpl.Type, "template must have Type")
			assert.NotEmpty(t, tmpl.Name, "template must have Name")
			assert.Greater(t, tmpl.BasePrior, 0.0, "base prior must be positive")
			assert.Less(t, tmpl.BasePrior, 1.0, "base prior must be < 1.0")
			assert.NotEmpty(t, tmpl.ApplicableEntityTypes, "template must specify entity types")
		})
	}
}

func TestByPlaybook_AllPlaybooksResolvable(t *testing.T) {
	allTemplates := hyp_templates.All()
	typeSet := make(map[string]struct{}, len(allTemplates))
	for _, t := range allTemplates {
		typeSet[t.Type] = struct{}{}
	}

	for playbookID, types := range hyp_templates.ByPlaybook {
		for _, hypType := range types {
			_, ok := typeSet[hypType]
			assert.True(t, ok,
				"playbook %s references unknown hypothesis type %s", playbookID, hypType)
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func makeEvidence(evType domain_ev.EvidenceType, role domain_ev.EvidenceRole, weight float64, confidence float64, temporal domain_ev.TemporalMode, gapSecs int) *domain_ev.Evidence {
	ev := &domain_ev.Evidence{
		ID:                 uuid.New(),
		InvestigationID:    uuid.New(),
		RunID:              uuid.New(),
		EvidenceType:       evType,
		Source:             sourceForType(evType),
		Role:               role,
		Weight:             weight,
		EvidenceConfidence: confidence,
		TemporalMode:       temporal,
		FetchStatus:        domain_ev.FetchSuccess,
		Payload:            json.RawMessage("{}"),
	}
	if gapSecs > 0 {
		ev.TemporalGapSecs = &gapSecs
	}
	return ev
}

func templateByType(t *testing.T, hypType string) *domain_hyp.HypothesisTemplate {
	t.Helper()
	for _, tmpl := range hyp_templates.All() {
		if tmpl.Type == hypType {
			return tmpl
		}
	}
	t.Fatalf("hypothesis template %q not found in library", hypType)
	return nil
}

func sourceForType(evType domain_ev.EvidenceType) string {
	switch {
	case startsWith(string(evType), "k8s_"):
		return "kubernetes"
	case startsWith(string(evType), "cloudstack_"):
		return "cloudstack"
	case startsWith(string(evType), "netapp_"):
		return "netapp"
	case startsWith(string(evType), "okg_") || startsWith(string(evType), "change_") || startsWith(string(evType), "similar_"):
		return "okg"
	default:
		return "unknown"
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
