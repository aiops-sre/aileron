package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
)

// Repository is the Postgres implementation of domain/evidence.Repository.
type Repository struct {
	db *sql.DB
}

// NewRepository constructs a Repository with the given DB connection pool.
func NewRepository(db *sql.DB) *Repository {
	if db == nil {
		panic("evidence postgres repository: db is required")
	}
	return &Repository{db: db}
}

// BulkUpsert inserts multiple evidence pieces using a single batched INSERT.
// ON CONFLICT on idempotency_key means duplicate inserts from crash recovery
// are safely ignored.
func (r *Repository) BulkUpsert(ctx context.Context, evidence []*domain.Evidence) error {
	if len(evidence) == 0 {
		return nil
	}

	// Use UNNEST for a single-round-trip batch insert.
	ids := make([]string, len(evidence))
	invIDs := make([]string, len(evidence))
	runIDs := make([]string, len(evidence))
	fetcherIDs := make([]string, len(evidence))
	idempotencyKeys := make([]string, len(evidence))
	evTypes := make([]string, len(evidence))
	sources := make([]string, len(evidence))
	roles := make([]string, len(evidence))
	weights := make([]float64, len(evidence))
	confidences := make([]float64, len(evidence))
	temporalModes := make([]string, len(evidence))
	descriptions := make([]string, len(evidence))
	payloads := make([][]byte, len(evidence))
	fetchStatuses := make([]string, len(evidence))
	fetchDurations := make([]int64, len(evidence))
	gatheredAts := make([]time.Time, len(evidence))
	createdAts := make([]time.Time, len(evidence))

	for i, ev := range evidence {
		ids[i] = ev.ID.String()
		invIDs[i] = ev.InvestigationID.String()
		runIDs[i] = ev.RunID.String()
		fetcherIDs[i] = ev.FetcherID
		idempotencyKeys[i] = ev.IdempotencyKey
		// If the idempotency key is empty (evidence built by fetchers without explicit key),
		// generate one from the evidence ID so the UNIQUE constraint never sees a collision.
		if idempotencyKeys[i] == "" {
			idempotencyKeys[i] = ev.ID.String() + "::" + string(ev.EvidenceType)
		}
		evTypes[i] = string(ev.EvidenceType)
		sources[i] = ev.Source
		roles[i] = string(ev.Role)
		weights[i] = ev.Weight
		confidences[i] = ev.EvidenceConfidence
		temporalModes[i] = string(ev.TemporalMode)
		descriptions[i] = ev.Description
		payloads[i] = ev.Payload
		// Ensure payload is valid JSON — malformed payloads from K8s API responses
		// or nil slices will cause Postgres JSONB parse errors.
		if len(payloads[i]) == 0 || !json.Valid(payloads[i]) {
			payloads[i] = []byte("{}")
		}
		fetchStatuses[i] = string(ev.FetchStatus)
		fetchDurations[i] = int64(ev.FetchDurationMs)
		gatheredAts[i] = ev.GatheredAt
		createdAts[i] = ev.CreatedAt
	}

	// payloads must be string slice for Postgres JSONB casting via UNNEST.
	// pq.Array([][]byte) sends bytea[], which Postgres cannot cast to jsonb[].
	payloadStrs := make([]string, len(evidence))
	for i, p := range payloads {
		payloadStrs[i] = string(p)
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investigation_evidence (
			id, investigation_id, run_id, fetcher_id, idempotency_key,
			evidence_type, source, role, weight, evidence_confidence,
			temporal_mode, description, payload, fetch_status,
			fetch_duration_ms, gathered_at, created_at
		)
		SELECT
			unnest($1::uuid[]), unnest($2::uuid[]), unnest($3::uuid[]),
			unnest($4::text[]), unnest($5::text[]),
			unnest($6::text[]), unnest($7::text[]), unnest($8::text[]),
			unnest($9::float[]), unnest($10::float[]),
			unnest($11::text[]), unnest($12::text[]),
			unnest($13::text[])::jsonb, unnest($14::text[]),
			unnest($15::bigint[]), unnest($16::timestamptz[]), unnest($17::timestamptz[])
		ON CONFLICT DO NOTHING`,
		pq.Array(ids), pq.Array(invIDs), pq.Array(runIDs),
		pq.Array(fetcherIDs), pq.Array(idempotencyKeys),
		pq.Array(evTypes), pq.Array(sources), pq.Array(roles),
		pq.Array(weights), pq.Array(confidences),
		pq.Array(temporalModes), pq.Array(descriptions),
		pq.Array(payloadStrs), pq.Array(fetchStatuses),
		pq.Array(fetchDurations), pq.Array(gatheredAts), pq.Array(createdAts),
	)
	if err != nil {
		return fmt.Errorf("bulk upsert evidence: %w", err)
	}
	return nil
}

// GetByInvestigation returns all evidence for an investigation.
func (r *Repository) GetByInvestigation(ctx context.Context, investigationID uuid.UUID, runID *uuid.UUID) ([]*domain.Evidence, error) {
	query := `
		SELECT id, investigation_id, run_id, fetcher_id, idempotency_key,
		       evidence_type, source, role, weight, evidence_confidence,
		       temporal_mode, description, payload, fetch_status,
		       fetch_duration_ms, gathered_at, applied_to_scoring, created_at
		FROM investigation_evidence
		WHERE investigation_id = $1`
	args := []any{investigationID}

	if runID != nil {
		query += " AND run_id = $2"
		args = append(args, *runID)
	}
	query += " ORDER BY created_at ASC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("getting evidence: %w", err)
	}
	defer rows.Close()

	var result []*domain.Evidence
	for rows.Next() {
		ev := &domain.Evidence{}
		var (
			evTypeStr    string
			roleStr      string
			temporalStr  string
			fetchStatus  string
			payloadBytes []byte
		)
		err := rows.Scan(
			&ev.ID, &ev.InvestigationID, &ev.RunID, &ev.FetcherID, &ev.IdempotencyKey,
			&evTypeStr, &ev.Source, &roleStr, &ev.Weight, &ev.EvidenceConfidence,
			&temporalStr, &ev.Description, &payloadBytes, &fetchStatus,
			&ev.FetchDurationMs, &ev.GatheredAt, &ev.AppliedToScoring, &ev.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning evidence row: %w", err)
		}
		ev.EvidenceType = domain.EvidenceType(evTypeStr)
		ev.Role = domain.EvidenceRole(roleStr)
		ev.TemporalMode = domain.TemporalMode(temporalStr)
		ev.FetchStatus = domain.FetchStatus(fetchStatus)
		ev.Payload = json.RawMessage(payloadBytes)
		result = append(result, ev)
	}
	return result, rows.Err()
}

// GetLatestRunID returns the most recent run_id for an investigation.
func (r *Repository) GetLatestRunID(ctx context.Context, investigationID uuid.UUID) (uuid.UUID, error) {
	var runID uuid.UUID
	err := r.db.QueryRowContext(ctx,
		`SELECT run_id FROM investigation_evidence
		 WHERE investigation_id = $1
		 ORDER BY created_at DESC LIMIT 1`,
		investigationID,
	).Scan(&runID)
	if err == sql.ErrNoRows {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("getting latest run id: %w", err)
	}
	return runID, nil
}

// MarkApplied sets applied_to_scoring=true for the given evidence IDs.
func (r *Repository) MarkApplied(ctx context.Context, evidenceIDs []uuid.UUID) error {
	if len(evidenceIDs) == 0 {
		return nil
	}
	idStrs := make([]string, len(evidenceIDs))
	for i, id := range evidenceIDs {
		idStrs[i] = id.String()
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE investigation_evidence SET applied_to_scoring=true WHERE id = ANY($1::uuid[])`,
		pq.Array(idStrs),
	)
	if err != nil {
		return fmt.Errorf("marking evidence applied: %w", err)
	}
	return nil
}

// DeleteOldRuns removes evidence from stale runs.
func (r *Repository) DeleteOldRuns(ctx context.Context, investigationID uuid.UUID, keepRunID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM investigation_evidence
		 WHERE investigation_id=$1 AND run_id != $2`,
		investigationID, keepRunID,
	)
	if err != nil {
		return fmt.Errorf("deleting old evidence runs: %w", err)
	}
	return nil
}

// CountByInvestigation returns total evidence count.
func (r *Repository) CountByInvestigation(ctx context.Context, investigationID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM investigation_evidence WHERE investigation_id=$1`,
		investigationID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting evidence: %w", err)
	}
	return count, nil
}
