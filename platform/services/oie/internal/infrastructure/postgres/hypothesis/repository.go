package hypothesis

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
)

// Repository is the Postgres implementation of domain/hypothesis.Repository.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	if db == nil {
		panic("hypothesis postgres repository: db is required")
	}
	return &Repository{db: db}
}

// BulkCreate inserts all hypotheses for an investigation in a single round-trip.
func (r *Repository) BulkCreate(ctx context.Context, hypotheses []*domain.Hypothesis) error {
	if len(hypotheses) == 0 {
		return nil
	}

	ids := make([]string, len(hypotheses))
	invIDs := make([]string, len(hypotheses))
	types := make([]string, len(hypotheses))
	titles := make([]string, len(hypotheses))
	descriptions := make([]string, len(hypotheses))
	statuses := make([]string, len(hypotheses))
	confidences := make([]float64, len(hypotheses))
	bands := make([]string, len(hypotheses))
	baseScores := make([]float64, len(hypotheses))
	evidenceBoosts := make([]float64, len(hypotheses))
	contPenalties := make([]float64, len(hypotheses))
	topoAdjs := make([]float64, len(hypotheses))
	histAdjs := make([]float64, len(hypotheses))
	suppCounts := make([]int, len(hypotheses))
	contCounts := make([]int, len(hypotheses))
	missingRequired := make([]string, len(hypotheses))
	createdAts := make([]time.Time, len(hypotheses))

	for i, h := range hypotheses {
		ids[i] = h.ID.String()
		invIDs[i] = h.InvestigationID.String()
		types[i] = h.Type
		titles[i] = h.Title
		descriptions[i] = h.Description
		statuses[i] = string(h.Status)
		confidences[i] = h.Confidence
		bands[i] = string(h.Band)
		baseScores[i] = h.BaseScore
		evidenceBoosts[i] = h.EvidenceBoost
		contPenalties[i] = h.ContradictionPenalty
		topoAdjs[i] = h.TopologyAdjustment
		histAdjs[i] = h.HistoricalAdjustment
		suppCounts[i] = h.SupportingEvidenceCount
		contCounts[i] = h.ContradictingEvidenceCount
		missingRequired[i] = strings.Join(h.MissingRequiredEvidence, ",")
		createdAts[i] = h.CreatedAt
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investigation_hypotheses (
			id, investigation_id, type, title, description,
			status, confidence, band,
			base_score, evidence_boost, contradiction_penalty,
			topology_adjustment, historical_adjustment,
			supporting_evidence_count, contradicting_evidence_count,
			missing_required_evidence, created_at, updated_at
		)
		SELECT
			unnest($1::uuid[]), unnest($2::uuid[]),
			unnest($3::text[]), unnest($4::text[]), unnest($5::text[]),
			unnest($6::text[]), unnest($7::float[]), unnest($8::text[]),
			unnest($9::float[]), unnest($10::float[]), unnest($11::float[]),
			unnest($12::float[]), unnest($13::float[]),
			unnest($14::int[]), unnest($15::int[]),
			unnest($16::text[]), unnest($17::timestamptz[]), unnest($17::timestamptz[])
		ON CONFLICT (investigation_id, type) DO UPDATE SET
			status = EXCLUDED.status,
			confidence = EXCLUDED.confidence,
			band = EXCLUDED.band,
			evidence_boost = EXCLUDED.evidence_boost,
			contradiction_penalty = EXCLUDED.contradiction_penalty,
			supporting_evidence_count = EXCLUDED.supporting_evidence_count,
			contradicting_evidence_count = EXCLUDED.contradicting_evidence_count,
			missing_required_evidence = EXCLUDED.missing_required_evidence,
			updated_at = NOW()`,
		pq.Array(ids), pq.Array(invIDs),
		pq.Array(types), pq.Array(titles), pq.Array(descriptions),
		pq.Array(statuses), pq.Array(confidences), pq.Array(bands),
		pq.Array(baseScores), pq.Array(evidenceBoosts), pq.Array(contPenalties),
		pq.Array(topoAdjs), pq.Array(histAdjs),
		pq.Array(suppCounts), pq.Array(contCounts),
		pq.Array(missingRequired), pq.Array(createdAts),
	)
	if err != nil {
		return fmt.Errorf("bulk creating hypotheses: %w", err)
	}
	return nil
}

// GetByInvestigation returns all hypotheses ordered by rank ASC (best first).
func (r *Repository) GetByInvestigation(ctx context.Context, investigationID uuid.UUID) ([]*domain.Hypothesis, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, investigation_id, type, title, description,
		       status, confidence, band, rank,
		       base_score, evidence_boost, contradiction_penalty,
		       topology_adjustment, historical_adjustment,
		       supporting_evidence_count, contradicting_evidence_count,
		       missing_required_evidence, rejection_reason,
		       created_at, updated_at
		FROM investigation_hypotheses
		WHERE investigation_id = $1
		ORDER BY rank ASC NULLS LAST, confidence DESC`,
		investigationID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting hypotheses: %w", err)
	}
	defer rows.Close()

	var result []*domain.Hypothesis
	for rows.Next() {
		h, err := scanHypothesis(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, rows.Err()
}

// GetByID returns a single hypothesis.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Hypothesis, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, investigation_id, type, title, description,
		       status, confidence, band, rank,
		       base_score, evidence_boost, contradiction_penalty,
		       topology_adjustment, historical_adjustment,
		       supporting_evidence_count, contradicting_evidence_count,
		       missing_required_evidence, rejection_reason,
		       created_at, updated_at
		FROM investigation_hypotheses WHERE id = $1`, id)
	return scanHypothesis(row)
}

// UpdateScores bulk-updates confidence, band, rank, and status.
func (r *Repository) UpdateScores(ctx context.Context, hypotheses []*domain.Hypothesis) error {
	for _, h := range hypotheses {
		_, err := r.db.ExecContext(ctx, `
			UPDATE investigation_hypotheses
			SET confidence=$1, band=$2, status=$3, rank=$4,
			    evidence_boost=$5, contradiction_penalty=$6,
			    supporting_evidence_count=$7, contradicting_evidence_count=$8,
			    updated_at=NOW()
			WHERE id=$9`,
			h.Confidence, string(h.Band), string(h.Status), h.Rank,
			h.EvidenceBoost, h.ContradictionPenalty,
			h.SupportingEvidenceCount, h.ContradictingEvidenceCount,
			h.ID,
		)
		if err != nil {
			return fmt.Errorf("updating hypothesis %s: %w", h.ID, err)
		}
	}
	return nil
}

// GetWinner returns the highest-ranked non-rejected hypothesis.
func (r *Repository) GetWinner(ctx context.Context, investigationID uuid.UUID) (*domain.Hypothesis, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, investigation_id, type, title, description,
		       status, confidence, band, rank,
		       base_score, evidence_boost, contradiction_penalty,
		       topology_adjustment, historical_adjustment,
		       supporting_evidence_count, contradicting_evidence_count,
		       missing_required_evidence, rejection_reason,
		       created_at, updated_at
		FROM investigation_hypotheses
		WHERE investigation_id = $1
		  AND status NOT IN ('REJECTED')
		  AND confidence >= 0.25
		ORDER BY rank ASC NULLS LAST, confidence DESC
		LIMIT 1`,
		investigationID,
	)
	h, err := scanHypothesis(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return h, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHypothesis(row rowScanner) (*domain.Hypothesis, error) {
	h := &domain.Hypothesis{}
	var (
		statusStr       string
		bandStr         string
		missingRawStr   string
		rejectionReason sql.NullString
	)
	err := row.Scan(
		&h.ID, &h.InvestigationID, &h.Type, &h.Title, &h.Description,
		&statusStr, &h.Confidence, &bandStr, &h.Rank,
		&h.BaseScore, &h.EvidenceBoost, &h.ContradictionPenalty,
		&h.TopologyAdjustment, &h.HistoricalAdjustment,
		&h.SupportingEvidenceCount, &h.ContradictingEvidenceCount,
		&missingRawStr, &rejectionReason,
		&h.CreatedAt, &h.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, domain.ErrHypothesisNotFound{}
	}
	if err != nil {
		return nil, fmt.Errorf("scanning hypothesis: %w", err)
	}

	h.Status = domain.HypothesisStatus(statusStr)
	h.Band = domain.ConfidenceBand(bandStr)

	if missingRawStr != "" {
		h.MissingRequiredEvidence = strings.Split(missingRawStr, ",")
	}
	if rejectionReason.Valid {
		h.RejectionReason = &rejectionReason.String
	}

	return h, nil
}
