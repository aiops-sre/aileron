package rca

import (
	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
)

// ── Save Gate ─────────────────────────────────────────────────────────────────
//
// The SaveGate is the final quality checkpoint before any narrative is written
// to the investigation.  It evaluates nine scoring principles and blocks
// narrative generation when critical principles fail.
//
// Only if score >= 7 AND P2 (PrimarySourcePresent) AND P7 (ToolCoverage) are
// both true is the narrative approved.

// SaveGateResult is the verdict returned by EvaluateSaveGate.
type SaveGateResult struct {
	// Approved is true when all hard-fail conditions are met and score >= 7.
	Approved bool

	// Score is the count of passed principles out of 9.
	Score int

	// DepthScoreTable records whether each principle passed.
	DepthScoreTable map[string]bool

	// FailReasons contains human-readable explanations for each failed principle.
	FailReasons []string
}

// SaveGateInput carries all data required by the nine principles.
type SaveGateInput struct {
	// Evidence is the full evidence set for this investigation.
	Evidence []*domain_ev.Evidence

	// HypothesesGenerated is the total number of hypotheses produced.
	HypothesesGenerated int

	// HypothesesDisproven is the count with status DISPROVEN / REJECTED.
	HypothesesDisproven int

	// CausalChain is the constructed why-chain (may be nil if Phase 3 blocked).
	CausalChain *CausalChainResult

	// MandatoryFetcherAttempted maps each mandatory fetcher ID to whether it
	// was run during this investigation (any FetchStatus, even FetchMissing).
	MandatoryFetcherAttempted map[string]bool

	// PrimaryEntityName is the name of the root entity under investigation.
	// Must be non-empty and non-generic for P6.
	PrimaryEntityName string
}

// principle keys used in DepthScoreTable.
const (
	principleP1MultiSourceEvidence   = "P1.MultiSourceEvidence"
	principleP2PrimarySourcePresent  = "P2.PrimarySourcePresent"
	principleP3HypothesisCount       = "P3.HypothesisCount"
	principleP4AlternativesDisproven = "P4.AlternativesDisproven"
	principleP5TemporalEvidence      = "P5.TemporalEvidence"
	principleP6EntitySpecificity     = "P6.EntitySpecificity"
	principleP7ToolCoverage          = "P7.ToolCoverage"
	principleP8ContributingFactors   = "P8.ContributingFactors"
	principleP9AttributionCoverage   = "P9.AttributionCoverage"
)

// genericEntityNames is the set of entity names considered too vague for P6.
var genericEntityNames = map[string]bool{
	"":        true,
	"unknown": true,
	"n/a":     true,
	"none":    true,
	"entity":  true,
	"pod":     true,
	"node":    true,
}

// EvaluateSaveGate runs all nine principles and returns the gate verdict.
func EvaluateSaveGate(in SaveGateInput) SaveGateResult {
	table := make(map[string]bool, 9)
	var failReasons []string

	// ── P1: MultiSourceEvidence ──────────────────────────────────────────────
	// At least 3 distinct sources contributed SUCCESS evidence.
	sources := make(map[string]bool)
	for _, ev := range in.Evidence {
		if ev.FetchStatus == domain_ev.FetchSuccess {
			sources[ev.Source] = true
		}
	}
	p1 := len(sources) >= 3
	table[principleP1MultiSourceEvidence] = p1
	if !p1 {
		failReasons = append(failReasons,
			"P1 failed: evidence from only %d source(s); need >= 3 distinct sources for cross-validation")
	}

	// ── P2: PrimarySourcePresent (hard-fail) ──────────────────────────────────
	// At least one Tier1 (live-metrics) evidence item with SUCCESS status.
	p2 := false
	for _, ev := range in.Evidence {
		if ev.FetchStatus == domain_ev.FetchSuccess &&
			EvidenceTierFor(ev.EvidenceType) == EvidenceTierPrimary {
			p2 = true
			break
		}
	}
	table[principleP2PrimarySourcePresent] = p2
	if !p2 {
		failReasons = append(failReasons,
			"P2 HARD-FAIL: no Tier1 (live-metrics) evidence present; "+
				"narrative generation blocked — gather primary source data first")
	}

	// ── P3: HypothesisCount ───────────────────────────────────────────────────
	// At least 2 hypotheses were generated.
	p3 := in.HypothesesGenerated >= 2
	table[principleP3HypothesisCount] = p3
	if !p3 {
		failReasons = append(failReasons,
			"P3 failed: fewer than 2 hypotheses generated; investigation may be too narrow")
	}

	// ── P4: AlternativesDisproven ─────────────────────────────────────────────
	// At least 1 hypothesis was marked DISPROVEN/REJECTED.
	p4 := in.HypothesesDisproven >= 1
	table[principleP4AlternativesDisproven] = p4
	if !p4 {
		failReasons = append(failReasons,
			"P4 failed: no hypothesis was disproven; cannot confirm the winning hypothesis is the only explanation")
	}

	// ── P5: TemporalEvidence ──────────────────────────────────────────────────
	// At least one evidence item has OccurredAt set and is not just "current state".
	p5 := false
	for _, ev := range in.Evidence {
		if ev.OccurredAt != nil && ev.FetchStatus == domain_ev.FetchSuccess {
			p5 = true
			break
		}
	}
	table[principleP5TemporalEvidence] = p5
	if !p5 {
		failReasons = append(failReasons,
			"P5 failed: no timestamped evidence; causal ordering cannot be established")
	}

	// ── P6: EntitySpecificity ─────────────────────────────────────────────────
	// PrimaryEntityName is specific (not empty or generic).
	lower := toLower(in.PrimaryEntityName)
	p6 := !genericEntityNames[lower]
	table[principleP6EntitySpecificity] = p6
	if !p6 {
		failReasons = append(failReasons,
			"P6 failed: entity name is empty or generic ('"+in.PrimaryEntityName+"'); "+
				"investigation must identify a specific named entity")
	}

	// ── P7: ToolCoverage (hard-fail) ──────────────────────────────────────────
	// All mandatory fetchers were attempted (at least once, regardless of status).
	p7 := true
	for fid, attempted := range in.MandatoryFetcherAttempted {
		if !attempted {
			p7 = false
			failReasons = append(failReasons,
				"P7 HARD-FAIL: mandatory fetcher '"+fid+"' was never attempted; "+
					"blocked until this fetcher is executed")
		}
	}
	table[principleP7ToolCoverage] = p7

	// ── P8: ContributingFactors ───────────────────────────────────────────────
	// CausalChain has at least 2 non-GAP layers.
	p8 := false
	if in.CausalChain != nil {
		layerCount := 0
		for _, l := range in.CausalChain.Layers {
			if l.Category != CausalCategoryGap {
				layerCount++
			}
		}
		p8 = layerCount >= 2
	}
	table[principleP8ContributingFactors] = p8
	if !p8 {
		failReasons = append(failReasons,
			"P8 failed: causal chain has fewer than 2 layers; root cause may be under-explained")
	}

	// ── P9: AttributionCoverage ───────────────────────────────────────────────
	// CausalChain.AttributionCoverage >= 0.70.
	p9 := false
	if in.CausalChain != nil {
		p9 = in.CausalChain.AttributionCoverage >= 0.70
	}
	table[principleP9AttributionCoverage] = p9
	if !p9 {
		failReasons = append(failReasons,
			"P9 failed: attribution coverage below 70%; investigate causal chain gaps before narrative generation")
	}

	// ── Scoring ───────────────────────────────────────────────────────────────
	score := 0
	for _, passed := range table {
		if passed {
			score++
		}
	}

	// Approved only if: score >= 7 AND P2 == true AND P7 == true.
	approved := score >= 7 && p2 && p7

	return SaveGateResult{
		Approved:        approved,
		Score:           score,
		DepthScoreTable: table,
		FailReasons:     failReasons,
	}
}

// toLower returns a lowercase copy of s without importing strings in an otherwise
// zero-import file (we want to avoid import cycles).
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
