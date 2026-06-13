package rca

import (
	"fmt"
	"math"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
)

// ── Multi-hop Causality (Extended 5-Whys) ────────────────────────────────────
//
// BuildCausalChain constructs a structured why-chain that traces a failure from
// its immediate technical manifestation down to the organisational or
// architectural condition that allowed it to occur.
//
// The chain has two sections:
//
//   - Technical layers 1–5: symptom → root technical cause → validation gap →
//     detection gap → temporal trigger.
//   - Organisational layers 6–10: provisioning gap → validation gap → detection
//     gap → temporal trigger → architectural masking.
//
// Every layer carries an EvidenceTag that records whether the answer is a
// DIRECT observation, an INFERENCE, or a HYPOTHESIS awaiting validation.
//
// AttributionCoverage tracks the sum of ContributionPct across all layers.
// When it falls below 0.90 a synthetic GAP layer is appended and Phase 4
// synthesis is blocked until the gap is investigated.

// CausalCategory classifies a causal layer as technical or organisational.
type CausalCategory string

const (
	CausalCategoryTechnical       CausalCategory = "technical"
	CausalCategoryOrganisational  CausalCategory = "organizational"
	CausalCategoryGap             CausalCategory = "gap" // injected when attribution < 0.90
)

// CausalLayer is one node in the why-chain.
type CausalLayer struct {
	// Layer number: 1 = immediate symptom, 10 = deepest organisational factor.
	Layer int `json:"layer"`

	// Category distinguishes technical (1-5) from organisational (6-10) layers.
	Category CausalCategory `json:"category"`

	// Question is the "Why did X happen?" phrasing for this level.
	Question string `json:"question"`

	// Answer is the conclusion reached at this level.
	Answer string `json:"answer"`

	// EvidenceTag records the epistemic status of the Answer.
	EvidenceTag domain_ev.EvidenceTag `json:"evidence_tag"`

	// Source is the fetcher or derivation rule that produced this answer.
	Source string `json:"source"`

	// ContributionPct is the measured fraction of the total problem explained
	// by this layer (0.0–1.0).  Summed across all layers to produce
	// AttributionCoverage.
	ContributionPct float64 `json:"contribution_pct"`
}

// CausalChainResult is the full causal chain for one investigation.
type CausalChainResult struct {
	// Layers is the ordered list from Layer 1 (symptom) to the deepest root.
	Layers []CausalLayer `json:"layers"`

	// RootCause points to the deepest confirmed layer (last non-GAP layer).
	RootCause *CausalLayer `json:"root_cause,omitempty"`

	// AttributionCoverage is the sum of all ContributionPct values.
	// A value >= 0.90 means the chain explains at least 90 % of the problem.
	AttributionCoverage float64 `json:"attribution_coverage"`

	// BlockSynthesis is true when AttributionCoverage < 0.90; Phase 4 must
	// not proceed until the caller resolves the gap layers.
	BlockSynthesis bool `json:"block_synthesis"`
}

// attributionCoverageThreshold is the minimum sum of ContributionPct values
// required before Phase 4 synthesis is allowed.
const attributionCoverageThreshold = 0.90

// ── Layer question templates ──────────────────────────────────────────────────

// technicalLayerQuestions maps layer number → default question template.
// The template uses %s as a placeholder for the entity/metric name.
var technicalLayerQuestions = map[int]string{
	1: "Why did the service/workload fail?",
	2: "Why did the underlying resource fail?",
	3: "Why was the failure not caught before it impacted production?",
	4: "Why was the failure not detected earlier?",
	5: "What temporal condition allowed the failure to occur at this moment?",
}

var organisationalLayerQuestions = map[int]string{
	6:  "Why was the resource not provisioned correctly?",
	7:  "Why was there no automated validation preventing this state?",
	8:  "Why did alerting not fire before the incident was opened?",
	9:  "What process or schedule change created the window for this failure?",
	10: "What architectural property masked the risk until this failure?",
}

// ── Builder ───────────────────────────────────────────────────────────────────

// BuildCausalChain constructs the why-chain from the winning hypothesis and the
// gathered evidence.
//
// The algorithm:
//  1. Walk the evidence slice and populate layers 1–5 from technical signals
//     (OOMKilled, eviction, resource pressure, change events, temporal signals).
//  2. Attempt to populate layers 6–10 from config/change evidence.
//  3. For each layer that has no evidence, emit a HYPOTHESIS layer that blocks
//     synthesis.
//  4. Compute AttributionCoverage; if < 0.90 append a GAP layer.
func BuildCausalChain(
	winner *domain_hyp.Hypothesis,
	evidence []*domain_ev.Evidence,
) *CausalChainResult {
	layers := make([]CausalLayer, 0, 10)

	// ── Layer 1: Immediate technical cause ───────────────────────────────────
	layers = append(layers, buildLayer1(winner, evidence))

	// ── Layer 2: Underlying resource failure ────────────────────────────────
	layers = append(layers, buildLayer2(evidence))

	// ── Layer 3: Validation gap ──────────────────────────────────────────────
	layers = append(layers, buildLayer3(evidence))

	// ── Layer 4: Detection gap ───────────────────────────────────────────────
	layers = append(layers, buildLayer4(evidence))

	// ── Layer 5: Temporal trigger ────────────────────────────────────────────
	layers = append(layers, buildLayer5(evidence))

	// ── Layers 6–10: Organisational factors ─────────────────────────────────
	layers = append(layers, buildOrganisationalLayers(evidence)...)

	// ── Attribution coverage ─────────────────────────────────────────────────
	coverage := 0.0
	for _, l := range layers {
		coverage += l.ContributionPct
	}
	coverage = math.Min(1.0, coverage)

	result := &CausalChainResult{
		Layers:              layers,
		AttributionCoverage: coverage,
	}

	// Find deepest non-GAP confirmed layer as the root cause.
	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].Category != CausalCategoryGap &&
			layers[i].EvidenceTag != domain_ev.EvidenceTagUnknown {
			copy := layers[i]
			result.RootCause = &copy
			break
		}
	}

	// Inject a GAP layer and block synthesis if coverage is insufficient.
	if coverage < attributionCoverageThreshold {
		remaining := attributionCoverageThreshold - coverage
		gapLayer := CausalLayer{
			Layer:    len(layers) + 1,
			Category: CausalCategoryGap,
			Question: "What additional contributing factors have not yet been identified?",
			Answer: fmt.Sprintf(
				"Attribution gap: %.0f%% of the problem is unexplained. "+
					"Investigate metric trends and change history to close this gap before synthesis.",
				remaining*100,
			),
			EvidenceTag:     domain_ev.EvidenceTagUnknown,
			Source:          "attribution_coverage_check",
			ContributionPct: 0.0,
		}
		result.Layers = append(result.Layers, gapLayer)
		result.BlockSynthesis = true
	}

	return result
}

// ── Layer constructors ────────────────────────────────────────────────────────

func buildLayer1(winner *domain_hyp.Hypothesis, evidence []*domain_ev.Evidence) CausalLayer {
	layer := CausalLayer{
		Layer:    1,
		Category: CausalCategoryTechnical,
		Question: technicalLayerQuestions[1],
	}

	// Look for the highest-weight direct evidence of the failure manifestation.
	var best *domain_ev.Evidence
	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleSupports || ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		if best == nil || ev.EffectiveWeight() > best.EffectiveWeight() {
			best = ev
		}
	}

	if best != nil {
		layer.Answer = best.Description
		layer.EvidenceTag = domain_ev.EvidenceTagDirect
		layer.Source = best.FetcherID
		layer.ContributionPct = math.Min(0.30, best.EffectiveWeight()*0.30)
	} else {
		layer.Answer = fmt.Sprintf("Hypothesis: %s — awaiting direct evidence confirmation.", winner.Title)
		layer.EvidenceTag = domain_ev.EvidenceTagHypothesis
		layer.Source = "hypothesis_engine"
		layer.ContributionPct = 0.0
	}
	return layer
}

func buildLayer2(evidence []*domain_ev.Evidence) CausalLayer {
	layer := CausalLayer{
		Layer:    2,
		Category: CausalCategoryTechnical,
		Question: technicalLayerQuestions[2],
	}

	// Resource-level signals: node pressure, storage failure, host failure.
	resourceTypes := map[domain_ev.EvidenceType]bool{
		domain_ev.TypeK8sNodeNotReady:       true,
		domain_ev.TypeK8sResourcePressure:   true,
		domain_ev.TypeK8sNodeEvicted:        true,
		domain_ev.TypeNetAppAggregateState:  true,
		domain_ev.TypeNetAppVolumeState:     true,
		domain_ev.TypeNetAppVolumeFull:      true,
		domain_ev.TypeCloudStackHostState:   true,
		domain_ev.TypeK8sPodExitCodeOOM:     true,
		domain_ev.TypeK8sPodEventEviction:   true,
		domain_ev.TypeK8sPodEventStorage:    true,
	}

	for _, ev := range evidence {
		if !resourceTypes[ev.EvidenceType] {
			continue
		}
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		layer.Answer = ev.Description
		layer.EvidenceTag = domain_ev.EvidenceTagDirect
		layer.Source = ev.FetcherID
		layer.ContributionPct = math.Min(0.25, ev.EffectiveWeight()*0.25)
		return layer
	}

	layer.Answer = "Underlying resource failure not yet identified — further investigation required."
	layer.EvidenceTag = domain_ev.EvidenceTagUnknown
	layer.Source = "attribution_engine"
	layer.ContributionPct = 0.0
	return layer
}

func buildLayer3(evidence []*domain_ev.Evidence) CausalLayer {
	layer := CausalLayer{
		Layer:    3,
		Category: CausalCategoryTechnical,
		Question: technicalLayerQuestions[3],
	}

	// Validation gap: config violations, missing probes, missing limits.
	validationTypes := map[domain_ev.EvidenceType]bool{
		domain_ev.TypeKubeSenseConfigViolation: true,
		domain_ev.TypeKubeSenseChaosScore:      true,
		domain_ev.TypeKubeSenseDrift:           true,
	}

	for _, ev := range evidence {
		if !validationTypes[ev.EvidenceType] {
			continue
		}
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		layer.Answer = fmt.Sprintf("Config/process gap: %s", ev.Description)
		layer.EvidenceTag = domain_ev.EvidenceTagDirect
		layer.Source = ev.FetcherID
		layer.ContributionPct = 0.10
		return layer
	}

	layer.Answer = "No automated validation gap evidence found — may be a configuration drift or process omission."
	layer.EvidenceTag = domain_ev.EvidenceTagHypothesis
	layer.Source = "gap_analysis"
	layer.ContributionPct = 0.05
	return layer
}

func buildLayer4(evidence []*domain_ev.Evidence) CausalLayer {
	layer := CausalLayer{
		Layer:    4,
		Category: CausalCategoryTechnical,
		Question: technicalLayerQuestions[4],
	}

	// Detection gap: no anomaly or forecast evidence present.
	detectionTypes := map[domain_ev.EvidenceType]bool{
		domain_ev.TypeKubeSenseAPMRegression: true,
		domain_ev.TypeKubeSenseForecast:      true,
		domain_ev.TypeKubeSenseAnomaly:       true,
	}

	for _, ev := range evidence {
		if !detectionTypes[ev.EvidenceType] {
			continue
		}
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		layer.Answer = fmt.Sprintf("Detection signal present: %s — alert thresholds may need tuning.", ev.Description)
		layer.EvidenceTag = domain_ev.EvidenceTagDirect
		layer.Source = ev.FetcherID
		layer.ContributionPct = 0.10
		return layer
	}

	layer.Answer = "No pre-incident anomaly or forecast signals found — detection gap likely."
	layer.EvidenceTag = domain_ev.EvidenceTagInference
	layer.Source = "detection_gap_analysis"
	layer.ContributionPct = 0.05
	return layer
}

func buildLayer5(evidence []*domain_ev.Evidence) CausalLayer {
	layer := CausalLayer{
		Layer:    5,
		Category: CausalCategoryTechnical,
		Question: technicalLayerQuestions[5],
	}

	// Temporal trigger: recent deployment or config change.
	changeTypes := map[domain_ev.EvidenceType]bool{
		domain_ev.TypeChangeDeployment:  true,
		domain_ev.TypeChangeConfig:      true,
		domain_ev.TypeOKGCausalityScore: true,
	}

	for _, ev := range evidence {
		if !changeTypes[ev.EvidenceType] {
			continue
		}
		if ev.Role != domain_ev.RoleSupports || ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		layer.Answer = fmt.Sprintf("Recent change: %s", ev.Description)
		layer.EvidenceTag = domain_ev.EvidenceTagDirect
		layer.Source = ev.FetcherID
		layer.ContributionPct = math.Min(0.20, ev.EffectiveWeight()*0.20)
		return layer
	}

	layer.Answer = "No deployment or config change found within the investigation window."
	layer.EvidenceTag = domain_ev.EvidenceTagInference
	layer.Source = "temporal_analysis"
	layer.ContributionPct = 0.03
	return layer
}

func buildOrganisationalLayers(evidence []*domain_ev.Evidence) []CausalLayer {
	layers := make([]CausalLayer, 0, 5)

	// For now the first three organisational layers are inferred from the
	// presence / absence of certain evidence types.  They are tagged
	// INFERENCE because no direct source directly answers these questions.

	// Layer 6: Provisioning gap.
	l6 := CausalLayer{
		Layer:    6,
		Category: CausalCategoryOrganisational,
		Question: organisationalLayerQuestions[6],
	}
	hasDrift := hasEvidenceOfType(evidence, domain_ev.TypeKubeSenseDrift)
	if hasDrift {
		l6.Answer = "GitOps drift detected — resource spec diverged from the desired state in the Git repository."
		l6.EvidenceTag = domain_ev.EvidenceTagInference
		l6.Source = "kubesense:drift"
		l6.ContributionPct = 0.05
	} else {
		l6.Answer = "Provisioning gap not confirmed — resource limits or quotas may not reflect actual workload growth."
		l6.EvidenceTag = domain_ev.EvidenceTagHypothesis
		l6.Source = "gap_analysis"
		l6.ContributionPct = 0.02
	}
	layers = append(layers, l6)

	// Layer 7: Automated validation gap.
	l7 := CausalLayer{
		Layer:    7,
		Category: CausalCategoryOrganisational,
		Question: organisationalLayerQuestions[7],
	}
	hasConfigViolation := hasEvidenceOfType(evidence, domain_ev.TypeKubeSenseConfigViolation)
	if hasConfigViolation {
		l7.Answer = "Policy-as-code violation detected — admission controller or OPA policy was absent or disabled."
		l7.EvidenceTag = domain_ev.EvidenceTagInference
		l7.Source = "kubesense:config_violation"
		l7.ContributionPct = 0.05
	} else {
		l7.Answer = "Validation gap not confirmed — CI/CD pre-deployment checks may not cover this failure class."
		l7.EvidenceTag = domain_ev.EvidenceTagHypothesis
		l7.Source = "gap_analysis"
		l7.ContributionPct = 0.02
	}
	layers = append(layers, l7)

	// Layer 8: Detection gap.
	l8 := CausalLayer{
		Layer:    8,
		Category: CausalCategoryOrganisational,
		Question: organisationalLayerQuestions[8],
	}
	hasAnomaly := hasEvidenceOfType(evidence, domain_ev.TypeKubeSenseAnomaly) ||
		hasEvidenceOfType(evidence, domain_ev.TypeKubeSenseAPMRegression)
	if hasAnomaly {
		l8.Answer = "Pre-incident anomaly signals were available — alert rules did not fire or thresholds were too high."
		l8.EvidenceTag = domain_ev.EvidenceTagInference
		l8.Source = "kubesense:anomaly"
		l8.ContributionPct = 0.04
	} else {
		l8.Answer = "Alert detection gap — no pre-incident anomaly signal was present or configured."
		l8.EvidenceTag = domain_ev.EvidenceTagHypothesis
		l8.Source = "gap_analysis"
		l8.ContributionPct = 0.02
	}
	layers = append(layers, l8)

	// Layers 9–10 are architectural / process level — always hypothesis.
	layers = append(layers, CausalLayer{
		Layer:           9,
		Category:        CausalCategoryOrganisational,
		Question:        organisationalLayerQuestions[9],
		Answer:          "Process or schedule change created the window — requires postmortem review.",
		EvidenceTag:     domain_ev.EvidenceTagHypothesis,
		Source:          "postmortem_required",
		ContributionPct: 0.01,
	})
	layers = append(layers, CausalLayer{
		Layer:           10,
		Category:        CausalCategoryOrganisational,
		Question:        organisationalLayerQuestions[10],
		Answer:          "Architectural masking — single point of failure or insufficient redundancy.",
		EvidenceTag:     domain_ev.EvidenceTagHypothesis,
		Source:          "architecture_review",
		ContributionPct: 0.01,
	})

	return layers
}

// hasEvidenceOfType returns true if evidence contains at least one item of the
// given type with FetchStatus SUCCESS.
func hasEvidenceOfType(evidence []*domain_ev.Evidence, evType domain_ev.EvidenceType) bool {
	for _, ev := range evidence {
		if ev.EvidenceType == evType && ev.FetchStatus == domain_ev.FetchSuccess {
			return true
		}
	}
	return false
}
