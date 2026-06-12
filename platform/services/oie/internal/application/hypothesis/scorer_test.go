package hypothesis

import (
	"testing"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/hypothesis_templates"
)

func findTemplate(id string) *domain_hyp.HypothesisTemplate {
	for _, t := range hypothesis_templates.All() {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func TestScorerBackOffDoesNotScorePVCFull(t *testing.T) {
	tmpl := findTemplate("HT-K8S-012")
	if tmpl == nil {
		t.Fatal("HT-K8S-012 not found in library")
	}

	backOffEv := &domain_ev.Evidence{
		EvidenceType:       domain_ev.TypeK8sPodEventCrashLoop,
		Role:               domain_ev.RoleSupports,
		Weight:             0.75,
		TemporalMode:       domain_ev.TemporalHistorical,
		EvidenceConfidence: 0.95,
	}

	scorer := &Scorer{}
	result := scorer.Score(tmpl, []*domain_ev.Evidence{backOffEv})

	if result.Confidence > tmpl.BasePrior {
		t.Errorf("BackOff evidence should NOT score pvc_storage_full: got %.2f, base prior %.2f", result.Confidence, tmpl.BasePrior)
	}
	if result.SupportingCount != 0 {
		t.Errorf("expected 0 supporting evidence counted, got %d", result.SupportingCount)
	}
}

func TestScorerStorageEventScoresPVCFull(t *testing.T) {
	tmpl := findTemplate("HT-K8S-012")
	if tmpl == nil {
		t.Fatal("HT-K8S-012 not found in library")
	}

	storageEv := &domain_ev.Evidence{
		EvidenceType:       domain_ev.TypeK8sPodEventStorage,
		Role:               domain_ev.RoleSupports,
		Weight:             0.85,
		TemporalMode:       domain_ev.TemporalHistorical,
		EvidenceConfidence: 0.95,
	}

	scorer := &Scorer{}
	result := scorer.Score(tmpl, []*domain_ev.Evidence{storageEv})

	if result.Confidence <= tmpl.BasePrior {
		t.Errorf("storage event should boost pvc_storage_full: got %.2f, base prior %.2f", result.Confidence, tmpl.BasePrior)
	}
	if result.SupportingCount == 0 {
		t.Errorf("expected at least 1 supporting evidence counted, got 0")
	}
}

func TestScorerBackOffScoresDependencyUnavailable(t *testing.T) {
	tmpl := findTemplate("HT-K8S-009")
	if tmpl == nil {
		t.Fatal("HT-K8S-009 not found in library")
	}

	backOffEv := &domain_ev.Evidence{
		EvidenceType:       domain_ev.TypeK8sPodEventCrashLoop,
		Role:               domain_ev.RoleSupports,
		Weight:             0.75,
		EvidenceGroup:      strPtr("k8s_pod_crash_signals"),
		TemporalMode:       domain_ev.TemporalHistorical,
		EvidenceConfidence: 0.95,
	}
	exitCodeEv := &domain_ev.Evidence{
		EvidenceType:       domain_ev.TypeK8sPodExitCode,
		Role:               domain_ev.RoleSupports,
		Weight:             0.65,
		EvidenceGroup:      strPtr("k8s_pod_crash_signals"),
		TemporalMode:       domain_ev.TemporalHistorical,
		EvidenceConfidence: 0.97,
	}

	scorer := &Scorer{}
	result := scorer.Score(tmpl, []*domain_ev.Evidence{backOffEv, exitCodeEv})

	if result.Confidence <= tmpl.BasePrior {
		t.Errorf("BackOff + exit-code should boost dependency_unavailable: got %.2f, base prior %.2f", result.Confidence, tmpl.BasePrior)
	}
	if result.Status == domain_hyp.StatusRejected {
		t.Errorf("dependency_unavailable should not be rejected with crash evidence")
	}
}

func strPtr(s string) *string { return &s }
