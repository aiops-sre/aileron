package hypothesis

import (
	"fmt"
	"math"
	"sort"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
)

// Scorer computes the confidence score for a single hypothesis given a set of evidence.
// The algorithm:
//  1. Start with base_prior from the template.
//  2. Apply supporting evidence using group-aware scoring:
//     - Evidence within the same group: use max-weight (prevents correlated evidence inflation).
//     - Ungrouped evidence: apply independence-complement formula.
//  3. Apply contradicting evidence with temporal gap dampening.
//  4. Check hard rejection: if any hard-rejection evidence type present (with correct temporal mode), confidence = 0.
//  5. Apply missing required evidence penalty (only for FetchMissing, not timeouts/errors).
//  6. Classify the final confidence into a confidence band.
type Scorer struct{}

// ScoreResult holds the full scoring breakdown for one hypothesis.
type ScoreResult struct {
	Confidence             float64
	Band                   domain_hyp.ConfidenceBand
	Status                 domain_hyp.HypothesisStatus
	RejectionReason        *string
	RejectionEvidenceType  string

	// Decomposition (for UI explainability).
	BaseScore            float64
	EvidenceBoost        float64
	ContradictionPenalty float64

	// Accounting.
	SupportingCount    int
	ContradictingCount int
	MissingRequired    []string
}

// Score evaluates a hypothesis template against a flat evidence slice.
func (s *Scorer) Score(
	template *domain_hyp.HypothesisTemplate,
	evidence []*domain_ev.Evidence,
) *ScoreResult {
	result := &ScoreResult{
		BaseScore:  template.BasePrior,
	}
	confidence := template.BasePrior

	// ── Step 1: Hard rejection check ──────────────────────────────────────────
	// Hard rejection is only valid for logically necessary exclusions.
	// Current-state evidence with large temporal gap does NOT hard-reject (temporal dampening applies instead).
	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleSupports && ev.Role != domain_ev.RoleContext {
			continue
		}
		for _, hardType := range template.HardRejectionEvidenceTypes {
			if string(ev.EvidenceType) == hardType {
				// Only hard-reject if evidence is historically timestamped or very fresh.
				if ev.TemporalMode == domain_ev.TemporalHistorical ||
					(ev.TemporalGapSecs != nil && *ev.TemporalGapSecs < 60) {
					reason := fmt.Sprintf("hard rejection: %s evidence present (%s)", hardType, ev.Description)
					result.Status = domain_hyp.StatusRejected
					result.RejectionReason = &reason
					result.RejectionEvidenceType = hardType
					result.Confidence = 0.0
					result.Band = domain_hyp.BandInsufficient
					return result
				}
			}
		}
	}

	// ── Step 2: Apply supporting evidence (group-aware) ────────────────────────
	// Group evidence: within each group, use max-weight only (prevents correlated inflation).
	// Ungrouped evidence: apply independence-complement formula.
	groupMaxWeights := make(map[string]float64)

	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleSupports {
			continue
		}

		// CRITICAL: Only apply evidence that is explicitly listed in this template's
		// SupportingEvidence spec. Evidence not in the spec is not relevant to this
		// hypothesis and must not boost its confidence.
		// Without this filter, a K8s BackOff event boosts deployment regression
		// hypotheses — a logical impossibility.
		if !templateContainsEvidenceType(template.SupportingEvidence, string(ev.EvidenceType)) {
			continue
		}

		effectiveWeight := ev.EffectiveWeight()
		if effectiveWeight == 0 {
			continue
		}

		// Match evidence to template's supporting specs for group tagging.
		group := templateGroupForEvidence(template, string(ev.EvidenceType), ev.EvidenceGroup)

		if group != "" {
			if effectiveWeight > groupMaxWeights[group] {
				groupMaxWeights[group] = effectiveWeight
			}
		} else {
			// Ungrouped: independence-complement.
			confidence += effectiveWeight * (1.0 - confidence)
			result.EvidenceBoost += effectiveWeight * (1.0 - confidence + effectiveWeight*(1.0-confidence))
		}
		result.SupportingCount++
	}

	// Apply max-weight for each group.
	for _, maxW := range groupMaxWeights {
		boost := maxW * (1.0 - confidence)
		confidence += boost
		result.EvidenceBoost += boost
	}

	// ── Step 3: Apply contradicting evidence (with temporal dampening) ─────────
	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleContradicts {
			continue
		}
		// Only apply contradictions that are in this template's ContradictingEvidence spec.
		if !templateContainsEvidenceType(template.ContradictingEvidence, string(ev.EvidenceType)) {
			continue
		}

		effectiveWeight := ev.EffectiveWeight()
		// Apply additional temporal dampening for contradictions based on source.
		effectiveWeight = dampContradictionBySource(effectiveWeight, ev)

		if effectiveWeight == 0 {
			continue
		}

		before := confidence
		confidence *= (1.0 - effectiveWeight)
		penalty := before - confidence
		result.ContradictionPenalty += penalty
		result.ContradictingCount++
	}

	// ── Step 4: Missing required evidence penalty ──────────────────────────────
	// Only penalise if evidence was MISSING (entity not in source).
	// Do NOT penalise for TIMEOUT or ERROR (source unreachable — our problem, not theirs).
	for _, reqSpec := range template.RequiredEvidence {
		if !reqSpec.Required {
			continue
		}
		found := false
		missingButFetched := false // was attempted but returned MISSING

		for _, ev := range evidence {
			if string(ev.EvidenceType) == reqSpec.EvidenceType {
				found = true
				if ev.FetchStatus == domain_ev.FetchMissing {
					missingButFetched = true
					found = false // treat as not found for penalty purposes
				}
				break
			}
		}

		if !found {
			result.MissingRequired = append(result.MissingRequired, reqSpec.EvidenceType)
			if missingButFetched {
				// Entity confirmed to not exist in this source — significant doubt.
				confidence *= 0.70
			}
			// If not fetched at all (timeout/error): no penalty — don't blame the hypothesis.
		}
	}

	// ── Step 5: Threshold classification ──────────────────────────────────────
	confidence = math.Max(0.0, math.Min(1.0, confidence))
	result.Confidence = confidence
	result.Band = domain_hyp.BandFromConfidence(confidence)

	if confidence < 0.25 || (result.SupportingCount < template.MinSupportingEvidence && confidence < 0.45) {
		result.Status = domain_hyp.StatusInsufficientEvidence
	} else {
		result.Status = domain_hyp.StatusActive
	}

	return result
}

// RankHypotheses assigns 1-based ranks to hypotheses, highest confidence first.
// Hypotheses with StatusRejected are ranked last and excluded from the winner set.
func RankHypotheses(hypotheses []*domain_hyp.Hypothesis) {
	// Sort: active > insufficient_evidence > rejected, then by confidence DESC.
	sort.Slice(hypotheses, func(i, j int) bool {
		si, sj := statusPriority(hypotheses[i].Status), statusPriority(hypotheses[j].Status)
		if si != sj {
			return si > sj
		}
		return hypotheses[i].Confidence > hypotheses[j].Confidence
	})

	for i, h := range hypotheses {
		rank := i + 1
		h.Rank = &rank
	}
}

// WinnerFrom returns the top-ranked non-rejected hypothesis, or nil if none qualifies.
// A hypothesis must have at least 1 supporting evidence piece OR confidence >= 0.50
// to win. This prevents a BasePrior-only hypothesis from winning when all evidence
// fetchers fail — the prior behaviour caused the highest-BasePrior hypothesis to win
// unconditionally, leading to hallucinated LLM narratives with zero factual grounding.
func WinnerFrom(hypotheses []*domain_hyp.Hypothesis) *domain_hyp.Hypothesis {
	for _, h := range hypotheses {
		if h.Status == domain_hyp.StatusRejected {
			continue
		}
		if h.Confidence < 0.25 {
			continue
		}
		// Require supporting evidence OR high enough confidence that it's evidence-driven.
		// BasePrior values top out at ~0.50, so confidence > 0.50 implies evidence boosted it.
		if h.SupportingEvidenceCount == 0 && h.Confidence < 0.50 {
			continue // BasePrior-only — skip until actual evidence arrives
		}
		return h
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// dampContradictionBySource applies source-specific temporal dampening to contradictions.
// CloudStack polls every 60-300s; K8s conditions are near-real-time.
func dampContradictionBySource(weight float64, ev *domain_ev.Evidence) float64 {
	if ev.TemporalMode == domain_ev.TemporalHistorical {
		return weight // Historical evidence: no dampening.
	}
	if ev.TemporalGapSecs == nil {
		return weight
	}
	gap := *ev.TemporalGapSecs

	switch ev.Source {
	case "cloudstack":
		// CloudStack polls every 1-5 minutes: stale after 120s.
		if gap < 120 {
			return weight
		}
		// Linear decay from 120s to 300s: weight → weight * 0.30.
		if gap >= 300 {
			return weight * 0.30
		}
		decay := float64(gap-120) / float64(180)
		return weight * (1.0 - decay*0.70)

	case "kubernetes":
		// K8s conditions are near-real-time: stale after 60s.
		if gap < 60 {
			return weight
		}
		if gap >= 180 {
			return weight * 0.40
		}
		decay := float64(gap-60) / float64(120)
		return weight * (1.0 - decay*0.60)

	default:
		if gap < 60 {
			return weight
		}
		if gap >= 180 {
			return weight * 0.50
		}
		decay := float64(gap-60) / float64(120)
		return weight * (1.0 - decay*0.50)
	}
}

// templateGroupForEvidence finds the evidence group for a given evidence type
// by matching against the template's supporting evidence specs.
func templateGroupForEvidence(template *domain_hyp.HypothesisTemplate, evType string, evidenceGroup *string) string {
	// Priority: use the evidence's own group annotation if present.
	if evidenceGroup != nil && *evidenceGroup != "" {
		return *evidenceGroup
	}
	// Fallback: check template's supporting specs.
	for _, spec := range template.SupportingEvidence {
		if spec.EvidenceType == evType && spec.EvidenceGroup != "" {
			return spec.EvidenceGroup
		}
	}
	return ""
}

func statusPriority(s domain_hyp.HypothesisStatus) int {
	switch s {
	case domain_hyp.StatusActive:
		return 2
	case domain_hyp.StatusInsufficientEvidence:
		return 1
	case domain_hyp.StatusRejected:
		return 0
	default:
		return 0
	}
}

// templateContainsEvidenceType returns true if any spec in the list matches evType.
// Used to filter evidence to only hypothesis-relevant types before scoring.
func templateContainsEvidenceType(specs []domain_hyp.EvidenceSpec, evType string) bool {
	for _, spec := range specs {
		if spec.EvidenceType == evType {
			return true
		}
	}
	return false
}
