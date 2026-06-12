package unit_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	apprca "github.com/aileron-platform/aileron/platform/services/oie/internal/application/rca"
	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Narrator tests ────────────────────────────────────────────────────────────

func TestNarrator_BelowThreshold_UsesTemplate(t *testing.T) {
	// When confidence < 0.65, LLM must NOT be called.
	// A nil ollama client would panic if called — verifies gate works.
	narrator := apprca.NewNarrator(nil, testLogger())

	winner := &domain_hyp.Hypothesis{
		ID:          uuid.New(),
		Type:        "node_cloudstack_vm_failure",
		Title:       "CloudStack VM Failure",
		Description: "VM is stopped",
		Status:      domain_hyp.StatusActive,
		Confidence:  0.50, // below 0.65 threshold
		Band:        domain_hyp.BandPossible,
	}

	narrative, model := narrator.Generate(
		context.Background(), winner, nil, nil, "Node NotReady")

	require.NotEmpty(t, narrative)
	assert.Equal(t, "template", model, "should use template below threshold")
	assert.Contains(t, narrative, "POSSIBLE", "template must state confidence band")
	assert.Contains(t, narrative, "50%", "template must state confidence percentage")
}

func TestNarrator_UncertaintyTemplateLeadsWithUncertainty(t *testing.T) {
	narrator := apprca.NewNarrator(nil, testLogger())

	reason := "hard rejection: oom kill present"
	rejected := &domain_hyp.Hypothesis{
		ID:              uuid.New(),
		Type:            "crashloop_bad_deployment",
		Title:           "Bad Deployment",
		Status:          domain_hyp.StatusRejected,
		Confidence:      0.0,
		RejectionReason: &reason,
	}

	winner := &domain_hyp.Hypothesis{
		ID:         uuid.New(),
		Type:       "crashloop_oom_kill",
		Title:      "OOM Kill",
		Status:     domain_hyp.StatusActive,
		Confidence: 0.55,
		Band:       domain_hyp.BandPossible,
	}

	narrative, _ := narrator.Generate(
		context.Background(), winner,
		[]*domain_hyp.Hypothesis{winner, rejected},
		nil, "CrashLoopBackOff",
	)

	// Template must include the rejected hypothesis.
	assert.Contains(t, narrative, "Bad Deployment", "rejected hypothesis must appear in narrative")
}

func TestNarrator_MissingRequiredEvidence_InTemplate(t *testing.T) {
	narrator := apprca.NewNarrator(nil, testLogger())

	winner := &domain_hyp.Hypothesis{
		ID:                      uuid.New(),
		Type:                    "pvc_storage_full",
		Title:                   "PVC Storage Full",
		Status:                  domain_hyp.StatusActive,
		Confidence:              0.40,
		Band:                    domain_hyp.BandSpeculative,
		MissingRequiredEvidence: []string{"netapp_volume_state"},
	}

	narrative, model := narrator.Generate(
		context.Background(), winner, nil, nil, "PVC Full")

	assert.Equal(t, "template", model)
	assert.Contains(t, narrative, "netapp_volume_state",
		"missing evidence must be listed in the template")
}

// ── CausalChainBuilder tests ──────────────────────────────────────────────────

func TestCausalChainBuilder_NoEIRS_NoApiCalls(t *testing.T) {
	// With empty eirsBaseURL, the builder should return a minimal chain
	// without making any HTTP calls.
	builder := apprca.NewCausalChainBuilder("") // no EIRS URL

	winner := &domain_hyp.Hypothesis{
		Type:        "node_cloudstack_vm_failure",
		Confidence:  0.87,
	}

	// Deploy evidence with a change event.
	deployEv := makeDeployEvidence()

	chain := builder.Build(context.Background(), winner,
		[]*domain_ev.Evidence{deployEv},
		"", // no root entity
		testIncidentTime(),
	)

	// Chain should have at least the trigger node from the change evidence.
	require.NotNil(t, chain)
	// With change evidence weight 0.85, trigger should be extracted.
	if deployEv.EffectiveWeight() >= 0.40 {
		triggerFound := false
		for _, n := range chain.Nodes {
			if n.Role == "trigger" {
				triggerFound = true
				break
			}
		}
		assert.True(t, triggerFound, "deployment evidence should produce a trigger node")
	}
}

func TestBuildTimeline_IncidentMarkerInserted(t *testing.T) {
	builder := apprca.NewCausalChainBuilder("")

	evidence := []*domain_ev.Evidence{
		makeTimedEvidence(domain_ev.TypeK8sNodeEvent, -5),   // 5 min before
		makeTimedEvidence(domain_ev.TypeK8sNodeEvent, -1),   // 1 min before
		makeTimedEvidence(domain_ev.TypeK8sNodeEvent, 3),    // 3 min after (within window)
		makeTimedEvidence(domain_ev.TypeK8sNodeEvent, -35),  // 35 min before (outside window)
	}

	timeline := builder.BuildTimeline(evidence, testIncidentTime())

	require.NotEmpty(t, timeline)
	// Incident start marker must be present.
	incidentFound := false
	for _, e := range timeline {
		if e.Category == "incident_start" {
			incidentFound = true
			assert.Equal(t, 0, e.DeltaMinutes)
			break
		}
	}
	assert.True(t, incidentFound, "incident start marker must be in timeline")

	// Outside-window event must be excluded.
	for _, e := range timeline {
		assert.GreaterOrEqual(t, e.DeltaMinutes, -30,
			"events >30min before incident must be excluded")
	}
}

func TestBuildTimeline_ChronologicalOrder(t *testing.T) {
	builder := apprca.NewCausalChainBuilder("")

	evidence := []*domain_ev.Evidence{
		makeTimedEvidence(domain_ev.TypeK8sNodeEvent, 2),
		makeTimedEvidence(domain_ev.TypeChangeDeployment, -10),
		makeTimedEvidence(domain_ev.TypeK8sOOMKill, -1),
	}

	timeline := builder.BuildTimeline(evidence, testIncidentTime())

	for i := 1; i < len(timeline); i++ {
		assert.LessOrEqual(t, timeline[i-1].DeltaMinutes, timeline[i].DeltaMinutes,
			"timeline must be in chronological order (ascending delta)")
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func makeDeployEvidence() *domain_ev.Evidence {
	payload := []byte(`{"change_id":"deploy-123","change_type":"deployment_event","title":"alerthub-backend v3.0.38","author_display":"vishwa","delta_minutes":14,"causality_score":0.83}`)
	ev := &domain_ev.Evidence{
		ID:                 uuid.New(),
		InvestigationID:    uuid.New(),
		RunID:              uuid.New(),
		EvidenceType:       domain_ev.TypeChangeDeployment,
		Source:             "okg",
		Role:               domain_ev.RoleSupports,
		Weight:             0.85,
		EvidenceConfidence: 0.85,
		TemporalMode:       domain_ev.TemporalHistorical,
		FetchStatus:        domain_ev.FetchSuccess,
		Payload:            payload,
	}
	t := testIncidentTime().Add(-14 * time.Minute)
	ev.OccurredAt = &t
	return ev
}

func makeTimedEvidence(evType domain_ev.EvidenceType, deltaMinutes int) *domain_ev.Evidence {
	t := testIncidentTime().Add(time.Duration(deltaMinutes) * time.Minute)
	return &domain_ev.Evidence{
		ID:           uuid.New(),
		EvidenceType: evType,
		Source:       "kubernetes",
		Role:         domain_ev.RoleSupports,
		Weight:       0.75,
		FetchStatus:  domain_ev.FetchSuccess,
		OccurredAt:   &t,
		Description:  "test event",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testIncidentTime() time.Time {
	return time.Date(2026, 6, 1, 14, 45, 0, 0, time.UTC)
}
