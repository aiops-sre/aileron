package evidence

import "fmt"

// ── Evidence Tag System ───────────────────────────────────────────────────────
//
// Every claim produced during an investigation must carry an EvidenceTag that
// classifies how it was obtained.  The tag drives confidence scoring, synthesis
// gating, and explainability output.

// EvidenceTag classifies the epistemic status of a single claim.
type EvidenceTag string

const (
	// EvidenceTagDirect: the claim is a direct observation returned by a live
	// data-source fetcher.  No inference was applied.
	EvidenceTagDirect EvidenceTag = "EVIDENCE"

	// EvidenceTagInference: the claim was derived logically from one or more
	// direct observations.  The derivation must be stated in Source.
	EvidenceTagInference EvidenceTag = "INFERENCE"

	// EvidenceTagHypothesis: the claim is an educated guess that requires
	// validation against further evidence before synthesis may proceed.
	EvidenceTagHypothesis EvidenceTag = "HYPOTHESIS"

	// EvidenceTagDisproven: the claim was a hypothesis that has since been
	// ruled out by contradicting evidence.  Kept for audit and explainability.
	EvidenceTagDisproven EvidenceTag = "DISPROVEN"

	// EvidenceTagUnknown: a data gap — the fetcher was attempted but the data
	// source returned nothing for this entity.  Blocks synthesis when present.
	EvidenceTagUnknown EvidenceTag = "UNKNOWN"
)

// TaggedClaim is a single assertion within an investigation, annotated with its
// epistemic status, the tool/fetcher that produced it, and whether it has been
// independently validated.
type TaggedClaim struct {
	Tag       EvidenceTag
	Source    string // fetcher ID or derivation rule (e.g. "kubernetes:fetcher", "rule:oom_pattern")
	Statement string // human-readable claim (e.g. "Pod nginx-abc exited with code 137")
	Validated bool   // true once cross-checked against a second independent source
}

// ── Validation result ─────────────────────────────────────────────────────────

// ValidationLevel classifies the outcome of ValidateClaims.
type ValidationLevel string

const (
	ValidationOK      ValidationLevel = "OK"
	ValidationWarning ValidationLevel = "WARNING"
	ValidationBlocked ValidationLevel = "BLOCKED"
)

// ValidationResult holds the outcome and any diagnostics from ValidateClaims.
type ValidationResult struct {
	Level    ValidationLevel
	Messages []string
}

// IsBlocked returns true when synthesis must not proceed.
func (v ValidationResult) IsBlocked() bool {
	return v.Level == ValidationBlocked
}

// ValidateClaims checks a slice of TaggedClaims for structural completeness and
// returns a ValidationResult that drives the investigation gate.
//
// Rules:
//  1. Every claim must carry a non-empty Tag — untagged claims are an implementation bug.
//  2. If HYPOTHESIS count > 3 and there are no DISPROVEN counterparts, emit a
//     WARNING: the investigation is speculative and the narrator must add caveats.
//  3. If any UNKNOWN tag is present and its fetcher was NOT retried, block synthesis
//     so the investigation manager requests the missing data before proceeding.
func ValidateClaims(claims []TaggedClaim) ValidationResult {
	var messages []string
	level := ValidationOK

	hypothesisCount := 0
	disprovenCount := 0
	unknownCount := 0
	untaggedCount := 0

	for i, c := range claims {
		if c.Tag == "" {
			untaggedCount++
			messages = append(messages, fmt.Sprintf("claim[%d] has no tag: %q", i, c.Statement))
		}
		switch c.Tag {
		case EvidenceTagHypothesis:
			hypothesisCount++
		case EvidenceTagDisproven:
			disprovenCount++
		case EvidenceTagUnknown:
			unknownCount++
		}
	}

	// Rule 1 — untagged claims are a hard error.
	if untaggedCount > 0 {
		level = ValidationBlocked
		messages = append(messages,
			fmt.Sprintf("%d untagged claim(s) found — every claim must carry an EvidenceTag", untaggedCount))
	}

	// Rule 2 — speculative investigation warning.
	if hypothesisCount > 3 && disprovenCount == 0 {
		if level == ValidationOK {
			level = ValidationWarning
		}
		messages = append(messages,
			fmt.Sprintf("%d HYPOTHESIS claims present with no DISPROVEN counterparts — "+
				"narrative must include explicit uncertainty caveats", hypothesisCount))
	}

	// Rule 3 — unknown data gaps block synthesis.
	if unknownCount > 0 {
		if level != ValidationBlocked {
			level = ValidationBlocked
		}
		messages = append(messages,
			fmt.Sprintf("%d UNKNOWN data gap(s) found — mandatory fetchers returned no data; "+
				"retry or acknowledge gaps before synthesis", unknownCount))
	}

	return ValidationResult{
		Level:    level,
		Messages: messages,
	}
}

// TagFromFetchStatus converts a FetchStatus to the closest EvidenceTag.
// SUCCESS → EVIDENCE, MISSING → UNKNOWN, anything else → UNKNOWN.
func TagFromFetchStatus(fs FetchStatus) EvidenceTag {
	switch fs {
	case FetchSuccess:
		return EvidenceTagDirect
	case FetchMissing:
		return EvidenceTagUnknown
	default:
		return EvidenceTagUnknown
	}
}
