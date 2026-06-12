package hypothesis

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines all persistence operations for the Hypothesis aggregate.
type Repository interface {
	// BulkCreate inserts all scored hypotheses for an investigation atomically.
	BulkCreate(ctx context.Context, hypotheses []*Hypothesis) error

	// GetByInvestigation returns all hypotheses for an investigation, ordered by rank.
	GetByInvestigation(ctx context.Context, investigationID uuid.UUID) ([]*Hypothesis, error)

	// GetByID returns a single hypothesis.
	GetByID(ctx context.Context, id uuid.UUID) (*Hypothesis, error)

	// UpdateScores bulk-updates confidence, band, rank, and status for a set of hypotheses.
	// Used when late evidence triggers re-scoring.
	UpdateScores(ctx context.Context, hypotheses []*Hypothesis) error

	// GetWinner returns the highest-ranked non-rejected hypothesis.
	// Returns (nil, nil) if no winner exists (all rejected or insufficient evidence).
	GetWinner(ctx context.Context, investigationID uuid.UUID) (*Hypothesis, error)
}
