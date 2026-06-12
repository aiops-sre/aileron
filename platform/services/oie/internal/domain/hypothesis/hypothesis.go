package hypothesis

import (
	"time"

	"github.com/google/uuid"
)

// HypothesisStatus represents the outcome of scoring a hypothesis.
type HypothesisStatus string

const (
	StatusActive              HypothesisStatus = "ACTIVE"
	StatusRejected            HypothesisStatus = "REJECTED"
	StatusInsufficientEvidence HypothesisStatus = "INSUFFICIENT_EVIDENCE"
)

// ConfidenceBand is the human-readable classification of a confidence score.
type ConfidenceBand string

const (
	BandConfirmed    ConfidenceBand = "CONFIRMED"     // >= 0.85
	BandLikely       ConfidenceBand = "LIKELY"         // >= 0.65
	BandPossible     ConfidenceBand = "POSSIBLE"       // >= 0.45
	BandSpeculative  ConfidenceBand = "SPECULATIVE"    // >= 0.25
	BandInsufficient ConfidenceBand = "INSUFFICIENT_EVIDENCE" // < 0.25
)

// BandFromConfidence classifies a raw confidence score.
func BandFromConfidence(c float64) ConfidenceBand {
	switch {
	case c >= 0.85:
		return BandConfirmed
	case c >= 0.65:
		return BandLikely
	case c >= 0.45:
		return BandPossible
	case c >= 0.25:
		return BandSpeculative
	default:
		return BandInsufficient
	}
}

// Hypothesis is the scored result of evaluating one hypothesis template
// against the evidence gathered for an investigation.
type Hypothesis struct {
	ID              uuid.UUID
	InvestigationID uuid.UUID

	// Type maps to a HypothesisTemplate.ID.
	Type  string
	Title string
	// Human-readable explanation of what this hypothesis claims.
	Description string

	Status     HypothesisStatus
	Confidence float64
	Band       ConfidenceBand
	// Rank is set after all hypotheses are scored; 1 = most likely.
	Rank *int

	// Score decomposition (for full explainability in the UI).
	BaseScore              float64
	EvidenceBoost          float64
	ContradictionPenalty   float64
	// Reserved for topology graph analysis (Module 4 wires this).
	TopologyAdjustment     float64
	// Calibration adjustment from confidence_calibration table.
	HistoricalAdjustment   float64

	// Evidence accounting.
	SupportingEvidenceCount    int
	ContradictingEvidenceCount int
	// MissingRequiredEvidence lists evidence types that were required but not gathered.
	// Distinguishes FetchMissing (entity not in source — penalised) from
	// FetchTimeout/FetchError (source unreachable — not penalised).
	MissingRequiredEvidence []string

	// Rejection details.
	RejectionReason    *string
	RejectionEvidenceID *uuid.UUID

	// Implication context.
	ImplicatedEntityIDs      []uuid.UUID
	ImplicatedChangeID       *uuid.UUID
	ImplicatedChangeSummary  *string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsTerminallyRejected returns true when the hypothesis was hard-rejected
// and cannot be reinstated by additional evidence.
func (h *Hypothesis) IsTerminallyRejected() bool {
	return h.Status == StatusRejected && h.Confidence == 0.0
}
