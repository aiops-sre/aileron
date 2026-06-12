package evidence

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository defines all persistence operations for Evidence.
type Repository interface {
	// BulkUpsert inserts multiple evidence pieces, skipping duplicates on idempotency key.
	// This is the hot path — called after each fetcher completes.
	BulkUpsert(ctx context.Context, evidence []*Evidence) error

	// GetByInvestigation returns all evidence for an investigation, ordered by created_at.
	// If runID is not nil, returns only evidence for that run.
	GetByInvestigation(ctx context.Context, investigationID uuid.UUID, runID *uuid.UUID) ([]*Evidence, error)

	// GetLatestRunID returns the most recent run_id for an investigation.
	// Returns uuid.Nil if no evidence exists.
	GetLatestRunID(ctx context.Context, investigationID uuid.UUID) (uuid.UUID, error)

	// MarkApplied marks evidence as applied to scoring (idempotency for late evidence).
	MarkApplied(ctx context.Context, evidenceIDs []uuid.UUID) error

	// DeleteOldRuns removes evidence from all runs except the latest one.
	// Called after recovery to clean up stale duplicate evidence.
	DeleteOldRuns(ctx context.Context, investigationID uuid.UUID, keepRunID uuid.UUID) error

	// CountByInvestigation returns the total evidence count for an investigation.
	CountByInvestigation(ctx context.Context, investigationID uuid.UUID) (int, error)
}

// SourceHealth tracks the per-source fetch success rate for circuit breaker decisions.
type SourceHealth struct {
	SourceName        string
	CircuitState      string // "closed" | "open" | "half_open"
	LastSuccessAt     *time.Time
	LastFailureAt     *time.Time
	ConsecutiveFails  int
	P99LatencyMs      int
	UpdatedAt         time.Time
}
