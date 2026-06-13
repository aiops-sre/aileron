package rca

import (
	"math"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
)

// ── Confidence Scoring ────────────────────────────────────────────────────────
//
// The production-grade confidence formula used in Phase 4 (Root Cause
// Determination).  It replaces the simple prior-plus-boost approach with a
// three-axis model that is resistant to hallucination and data gaps.
//
// Formula:
//   Confidence = (DataQuality × 0.30) + (EvidenceStrength × 0.30) + (HypothesisValidation × 0.40)
//
// After the weighted sum, a deduction matrix is applied for known quality
// problems, and hard blocks (zero grounding facts) short-circuit to 0.

// ConfidenceThresholdHigh is the minimum score to attach a LLM narrative without
// caveats.
const ConfidenceThresholdHigh = 0.80

// ConfidenceThresholdMedium is the minimum score to attach a narrative with
// caveats.  Below this value only a deterministic template is returned.
const ConfidenceThresholdMedium = 0.60

// ConfidenceTier classifies the computed confidence score for downstream routing.
type ConfidenceTier string

const (
	ConfidenceTierHigh   ConfidenceTier = "HIGH"   // >= 0.80: attach narrative
	ConfidenceTierMedium ConfidenceTier = "MEDIUM" // 0.60-0.79: attach with caveats
	ConfidenceTierLow    ConfidenceTier = "LOW"    // < 0.60: template only, no LLM
)

// EvidenceTier classifies the reliability of an evidence source for the
// DataQuality axis.
type EvidenceTier int

const (
	// EvidenceTierPrimary (Tier1): live metric API calls, real-time telemetry.
	// Highest reliability — authoritative at investigation time.
	EvidenceTierPrimary EvidenceTier = 1

	// EvidenceTierSecondary (Tier2): structured state APIs (K8s events, audit logs).
	// High reliability but may be slightly delayed.
	EvidenceTierSecondary EvidenceTier = 2

	// EvidenceTierConfig (Tier3): configuration objects and snapshots.
	// Moderate reliability — reflects desired state not actual state.
	EvidenceTierConfig EvidenceTier = 3

	// EvidenceTierArchive (Tier4): historical snapshots, point-in-time queries.
	// Lowest reliability for capacity decisions.
	EvidenceTierArchive EvidenceTier = 4
)

// EvidenceTierFor maps an EvidenceType to its reliability tier.
// Tier1 = live metrics, Tier2 = structured events, Tier3 = configs, Tier4 = snapshots.
func EvidenceTierFor(evType domain_ev.EvidenceType) EvidenceTier {
	switch evType {
	// Live metric / telemetry sources → Tier1
	case domain_ev.TypeKubeSenseAPMRegression,
		domain_ev.TypeKubeSenseForecast,
		domain_ev.TypeKubeSenseAnomaly,
		domain_ev.TypeKubeSenseDrift:
		return EvidenceTierPrimary

	// Structured real-time state sources → Tier2
	case domain_ev.TypeK8sNodeCondition,
		domain_ev.TypeK8sNodeEvent,
		domain_ev.TypeK8sNodeNotReady,
		domain_ev.TypeK8sNodeEvicted,
		domain_ev.TypeK8sPodExitCode,
		domain_ev.TypeK8sPodExitCodeZero,
		domain_ev.TypeK8sPodExitCodeOOM,
		domain_ev.TypeK8sPodExitCodeSegfault,
		domain_ev.TypeK8sPodEvent,
		domain_ev.TypeK8sPodEventCrashLoop,
		domain_ev.TypeK8sPodEventStorage,
		domain_ev.TypeK8sPodEventImage,
		domain_ev.TypeK8sPodEventFailed,
		domain_ev.TypeK8sPodEventEviction,
		domain_ev.TypeK8sPodEventPending,
		domain_ev.TypeK8sOOMKill,
		domain_ev.TypeK8sResourcePressure,
		domain_ev.TypeKubeSenseHealthEvent,
		domain_ev.TypeNetAppAggregateState,
		domain_ev.TypeNetAppSVMState,
		domain_ev.TypeNetAppNodeState,
		domain_ev.TypeNetAppVolumeState,
		domain_ev.TypeNetAppVolumeFull,
		domain_ev.TypeCloudStackVMState,
		domain_ev.TypeCloudStackHostState:
		return EvidenceTierSecondary

	// Change and config sources → Tier3
	case domain_ev.TypeChangeDeployment,
		domain_ev.TypeChangeConfig,
		domain_ev.TypeOKGCausalityScore,
		domain_ev.TypeKubeSenseConfigViolation,
		domain_ev.TypeKubeSenseChaosScore,
		domain_ev.TypeK8sCPURequestSaturation,
		domain_ev.TypeEntityContext,
		domain_ev.TypeTopologyParent:
		return EvidenceTierConfig

	// Historical / archive sources → Tier4
	case domain_ev.TypeSimilarIncident:
		return EvidenceTierArchive

	default:
		return EvidenceTierSecondary // conservative default
	}
}

// ── Deduction codes ───────────────────────────────────────────────────────────

// DeductionCode identifies a known quality defect that reduces the final score.
type DeductionCode string

const (
	DeductionMissingCriticalData       DeductionCode = "MISSING_CRITICAL_DATA"
	DeductionUnverifiedInference       DeductionCode = "UNVERIFIED_INFERENCE"
	DeductionAlternativeNotDisproven   DeductionCode = "ALTERNATIVE_NOT_DISPROVEN"
	DeductionMandatoryFetcherSkipped   DeductionCode = "MANDATORY_FETCHER_SKIPPED"
	DeductionZeroGroundingFacts        DeductionCode = "ZERO_GROUNDING_FACTS"
)

// Deduction is one applied penalty entry in the score breakdown.
type Deduction struct {
	Code   DeductionCode
	Count  int     // how many instances triggered this deduction
	Amount float64 // total points deducted
}

// ── Input / output types ──────────────────────────────────────────────────────

// ConfidenceInput carries all signals required by ComputeConfidence.
type ConfidenceInput struct {
	// Evidence is the complete set gathered for this investigation.
	Evidence []*domain_ev.Evidence

	// ConfirmedHypotheses is the number of hypotheses that were validated
	// (Tag = EVIDENCE or Validated == true after cross-checking).
	ConfirmedHypotheses int

	// TotalHypotheses is the total hypotheses generated before filtering.
	TotalHypotheses int

	// DisprovenHypotheses is the count of hypotheses with tag DISPROVEN.
	DisprovenHypotheses int

	// MandatoryFetchersMissing is the list of fetcher IDs that were required
	// but returned FetchMissing without a successful retry.
	MandatoryFetchersMissing []string
}

// ConfidenceOutput is the full scoring breakdown returned to callers.
type ConfidenceOutput struct {
	// Final is the computed confidence ∈ [0, 1].
	Final float64

	// Tier is the routing classification for the final score.
	Tier ConfidenceTier

	// Axis scores (before deductions).
	DataQuality          float64
	EvidenceStrength     float64
	HypothesisValidation float64

	// Deductions applied.
	Deductions []Deduction

	// ZeroGroundingFacts is true when the hard-block condition was triggered.
	ZeroGroundingFacts bool
}

// ── Core formula ──────────────────────────────────────────────────────────────

// ComputeConfidence computes the production-grade three-axis confidence score.
//
// Formula:
//
//	Confidence = (DataQuality×0.30) + (EvidenceStrength×0.30) + (HypothesisValidation×0.40)
//
// Followed by a deduction matrix.  Hard block when zero grounding facts.
func ComputeConfidence(in ConfidenceInput) ConfidenceOutput {
	out := ConfidenceOutput{}

	// ── Hard block: zero grounding facts ──────────────────────────────────────
	groundingFacts := countGroundingFactsForConfidence(in.Evidence)
	if groundingFacts == 0 {
		out.ZeroGroundingFacts = true
		out.Final = 0.0
		out.Tier = ConfidenceTierLow
		out.Deductions = []Deduction{{Code: DeductionZeroGroundingFacts, Count: 1, Amount: 1.0}}
		return out
	}

	// ── Axis 1: DataQuality ───────────────────────────────────────────────────
	// DataQuality = primary source count / total claims count, capped at 1.0.
	// "Primary" = Tier1 evidence (live metrics).
	totalClaims := 0
	primarySourceCount := 0
	for _, ev := range in.Evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		totalClaims++
		if EvidenceTierFor(ev.EvidenceType) == EvidenceTierPrimary {
			primarySourceCount++
		}
	}
	if totalClaims > 0 {
		out.DataQuality = math.Min(1.0, float64(primarySourceCount)/float64(totalClaims))
	}

	// ── Axis 2: EvidenceStrength ──────────────────────────────────────────────
	// EvidenceStrength = (tagged-claims / total-claims) × (1 - unvalidated-inference-ratio)
	// "Tagged" means evidence with Tag = EvidenceTagDirect or EvidenceTagInference.
	taggedCount := 0
	inferenceCount := 0
	unvalidatedInferenceCount := 0
	for _, ev := range in.Evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		tag := ev.EvidenceTag
		if tag == domain_ev.EvidenceTagDirect || tag == domain_ev.EvidenceTagInference {
			taggedCount++
		}
		if tag == domain_ev.EvidenceTagInference {
			inferenceCount++
			if !ev.ClaimValidated {
				unvalidatedInferenceCount++
			}
		}
	}
	if totalClaims > 0 {
		taggedRatio := float64(taggedCount) / float64(totalClaims)
		unvalidatedRatio := 0.0
		if inferenceCount > 0 {
			unvalidatedRatio = float64(unvalidatedInferenceCount) / float64(inferenceCount)
		}
		out.EvidenceStrength = taggedRatio * (1.0 - unvalidatedRatio)
	}

	// ── Axis 3: HypothesisValidation ─────────────────────────────────────────
	// HypothesisValidation = confirmed / total, capped at 1.0.
	if in.TotalHypotheses > 0 {
		out.HypothesisValidation = math.Min(1.0,
			float64(in.ConfirmedHypotheses)/float64(in.TotalHypotheses))
	}

	// ── Weighted sum ──────────────────────────────────────────────────────────
	raw := (out.DataQuality * 0.30) + (out.EvidenceStrength * 0.30) + (out.HypothesisValidation * 0.40)
	raw = math.Max(0.0, math.Min(1.0, raw))

	// ── Deduction matrix ──────────────────────────────────────────────────────
	var deductions []Deduction

	// Missing critical data: −0.10 per gap (UNKNOWN-tagged evidence where fetch was attempted).
	unknownCount := 0
	for _, ev := range in.Evidence {
		if ev.EvidenceTag == domain_ev.EvidenceTagUnknown {
			unknownCount++
		}
	}
	if unknownCount > 0 {
		amount := float64(unknownCount) * 0.10
		raw -= amount
		deductions = append(deductions, Deduction{
			Code: DeductionMissingCriticalData, Count: unknownCount, Amount: amount,
		})
	}

	// Unverified inferences: −0.05 per unverified inference claim.
	if unvalidatedInferenceCount > 0 {
		amount := float64(unvalidatedInferenceCount) * 0.05
		raw -= amount
		deductions = append(deductions, Deduction{
			Code: DeductionUnverifiedInference, Count: unvalidatedInferenceCount, Amount: amount,
		})
	}

	// Alternative not disproven: −0.10 per hypothesis that could not be ruled out.
	// Represents uncertainty that the winner is truly the only explanation.
	undisprovedAlternatives := 0
	if in.TotalHypotheses > 1 {
		undisprovedAlternatives = in.TotalHypotheses - in.ConfirmedHypotheses - in.DisprovenHypotheses
		if undisprovedAlternatives < 0 {
			undisprovedAlternatives = 0
		}
	}
	if undisprovedAlternatives > 0 {
		amount := float64(undisprovedAlternatives) * 0.10
		raw -= amount
		deductions = append(deductions, Deduction{
			Code: DeductionAlternativeNotDisproven, Count: undisprovedAlternatives, Amount: amount,
		})
	}

	// Mandatory fetcher skipped: −0.20 per fetcher that returned FetchMissing without retry.
	if len(in.MandatoryFetchersMissing) > 0 {
		amount := float64(len(in.MandatoryFetchersMissing)) * 0.20
		raw -= amount
		deductions = append(deductions, Deduction{
			Code: DeductionMandatoryFetcherSkipped, Count: len(in.MandatoryFetchersMissing), Amount: amount,
		})
	}

	raw = math.Max(0.0, math.Min(1.0, raw))
	out.Final = raw
	out.Deductions = deductions
	out.Tier = TierFromConfidence(raw)
	return out
}

// TierFromConfidence classifies a confidence score into a routing tier.
func TierFromConfidence(c float64) ConfidenceTier {
	switch {
	case c >= ConfidenceThresholdHigh:
		return ConfidenceTierHigh
	case c >= ConfidenceThresholdMedium:
		return ConfidenceTierMedium
	default:
		return ConfidenceTierLow
	}
}

// countGroundingFactsForConfidence returns the number of evidence items usable
// as LLM grounding facts for the confidence axis: SUCCESS status, SUPPORTS role,
// non-empty description.  Named distinctly from narrator.go's countGroundingFacts
// to avoid a redeclaration error within the same package.
func countGroundingFactsForConfidence(evidence []*domain_ev.Evidence) int {
	n := 0
	for _, ev := range evidence {
		if ev.Role == domain_ev.RoleSupports &&
			ev.FetchStatus == domain_ev.FetchSuccess &&
			ev.Description != "" {
			n++
		}
	}
	return n
}
