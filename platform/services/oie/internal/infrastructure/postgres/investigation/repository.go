package investigation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	"github.com/lib/pq"
)

// Repository is the Postgres implementation of domain.Repository.
type Repository struct {
	db *sql.DB
}

// NewRepository constructs a Repository with the given DB connection pool.
func NewRepository(db *sql.DB) *Repository {
	if db == nil {
		panic("investigation postgres repository: db is required")
	}
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, inv *domain.Investigation) error {
	evidenceSources, _ := json.Marshal(inv.EvidenceSources)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investigations (
			id, incident_id, incident_number, idempotency_key, source_message_key,
			status, status_reason, root_entity_id, root_entity_type, failure_class,
			domain, severity, playbook_id, topology_path, correlation_id,
			incident_started_at, triggered_at,
			time_budget_ms, hypotheses_generated, hypotheses_rejected,
			evidence_gathered, evidence_sources, report_version, feedback_status,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
			$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26
		)`,
		inv.ID, inv.IncidentID, inv.IncidentNumber, inv.IdempotencyKey, inv.SourceMessageKey,
		string(inv.Status), inv.StatusReason, inv.RootEntityID, inv.RootEntityType, inv.FailureClass,
		inv.Domain, inv.Severity, inv.PlaybookID, inv.TopologyPath, inv.CorrelationID,
		inv.IncidentStartedAt, inv.TriggeredAt,
		inv.TimeBudgetMs, inv.HypothesesGenerated, inv.HypothesesRejected,
		inv.EvidenceGathered, evidenceSources,
		inv.ReportVersion, inv.FeedbackStatus,
		inv.CreatedAt, inv.UpdatedAt,
	)
	if err != nil {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrDuplicateInvestigation{IdempotencyKey: inv.IdempotencyKey}
		}
		return fmt.Errorf("inserting investigation: %w", err)
	}
	return nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Investigation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+investigationColumns+` FROM investigations WHERE id=$1 AND deleted_at IS NULL`, id)
	return scanInvestigation(row)
}

func (r *Repository) GetByIncidentID(ctx context.Context, incidentID uuid.UUID) (*domain.Investigation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+investigationColumns+`
		 FROM investigations
		 WHERE incident_id=$1 AND status NOT IN ('CANCELLED') AND deleted_at IS NULL
		 ORDER BY triggered_at DESC LIMIT 1`, incidentID)
	return scanInvestigation(row)
}

func (r *Repository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Investigation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+investigationColumns+`
		 FROM investigations
		 WHERE idempotency_key=$1
		   AND status NOT IN ('COMPLETED','FAILED','CANCELLED')
		   AND deleted_at IS NULL
		 ORDER BY triggered_at DESC LIMIT 1`, key)
	return scanInvestigation(row)
}

func (r *Repository) UpdateStatus(
	ctx context.Context,
	id uuid.UUID,
	expectedCurrent domain.Status,
	next domain.Status,
	reason string,
) error {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `
		UPDATE investigations
		SET status        = $1::varchar,
		    status_reason = $2,
		    updated_at    = $3,
		    started_at           = CASE WHEN $1::varchar='RUNNING'                            THEN $3 ELSE started_at END,
		    evidence_complete_at = CASE WHEN $1::varchar='RCA_GENERATION'                    THEN $3 ELSE evidence_complete_at END,
		    completed_at         = CASE WHEN $1::varchar IN ('COMPLETED','FAILED','CANCELLED') THEN $3 ELSE completed_at END,
		    late_evidence_window_closes_at = CASE WHEN $1::varchar='COMPLETED' THEN $3+'5 minutes'::interval ELSE late_evidence_window_closes_at END
		WHERE id=$4 AND status=$5::varchar AND deleted_at IS NULL`,
		string(next), reason, now, id, string(expectedCurrent),
	)
	if err != nil {
		return fmt.Errorf("updating investigation status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrInvalidTransition{
			From:   expectedCurrent,
			To:     next,
			Reason: "optimistic lock failed — status changed by another process",
		}
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, inv *domain.Investigation) error {
	evidenceSources, _ := json.Marshal(inv.EvidenceSources)
	now := time.Now().UTC()
	inv.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, `
		UPDATE investigations SET
			status=$1, status_reason=$2, root_entity_id=$3, root_entity_type=$4,
			failure_class=$5, domain=$6, playbook_id=$7,
			started_at=$8, evidence_complete_at=$9, completed_at=$10, elapsed_ms=$11,
			hypotheses_generated=$12, hypotheses_rejected=$13,
			evidence_gathered=$14, evidence_sources=$15,
			root_cause_hypothesis_id=$16, confidence=$17, confidence_band=$18,
			root_cause_summary=$19, causal_chain=$20, blast_radius_summary=$21,
			narrative=$22, narrative_model=$23, narrative_generated_at=$24,
			report_version=$25, late_evidence_window_closes_at=$26,
			feedback_status=$27, feedback_by=$28, feedback_at=$29,
			feedback_notes=$30, actual_root_cause=$31, actual_hypothesis_type=$32,
			citations_json=$33,
			updated_at=$34
		WHERE id=$35 AND deleted_at IS NULL`,
		string(inv.Status), inv.StatusReason, inv.RootEntityID, inv.RootEntityType,
		inv.FailureClass, inv.Domain, inv.PlaybookID,
		inv.StartedAt, inv.EvidenceCompleteAt, inv.CompletedAt, inv.ElapsedMs,
		inv.HypothesesGenerated, inv.HypothesesRejected,
		inv.EvidenceGathered, evidenceSources,
		inv.RootCauseHypothesisID, inv.Confidence, inv.ConfidenceBand,
		inv.RootCauseSummary, inv.CausalChain, inv.BlastRadiusSummary,
		inv.Narrative, inv.NarrativeModel, inv.NarrativeGeneratedAt,
		inv.ReportVersion, inv.LateEvidenceWindowClosesAt,
		inv.FeedbackStatus, inv.FeedbackBy, inv.FeedbackAt,
		inv.FeedbackNotes, inv.ActualRootCause, inv.ActualHypothesisType,
		inv.CitationsJSON,
		now, inv.ID,
	)
	if err != nil {
		return fmt.Errorf("updating investigation: %w", err)
	}
	return nil
}

func (r *Repository) ListActive(ctx context.Context) ([]*domain.Investigation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+investigationColumns+`
		 FROM investigations
		 WHERE status NOT IN ('COMPLETED','FAILED','CANCELLED') AND deleted_at IS NULL
		 ORDER BY triggered_at DESC LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("listing active investigations: %w", err)
	}
	defer rows.Close()
	return scanInvestigations(rows)
}

func (r *Repository) ListOrphaned(ctx context.Context, staleAfter time.Duration) ([]*domain.Investigation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+investigationColumns+`
		 FROM investigations
		 WHERE status NOT IN ('COMPLETED','FAILED','CANCELLED')
		   AND triggered_at < $1
		   AND (lock_expires_at IS NULL OR lock_expires_at < NOW())
		   AND deleted_at IS NULL
		 ORDER BY triggered_at ASC LIMIT 50`,
		time.Now().UTC().Add(-staleAfter),
	)
	if err != nil {
		return nil, fmt.Errorf("listing orphaned investigations: %w", err)
	}
	defer rows.Close()
	return scanInvestigations(rows)
}

func (r *Repository) AcquireLock(ctx context.Context, id uuid.UUID, holderID string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `
		UPDATE investigations
		SET locked_by=
		$1, lock_acquired_at=$2, lock_expires_at=$3,
		    recovery_attempt_count=recovery_attempt_count+1, updated_at=$2
		WHERE id=$4 AND (lock_expires_at IS NULL OR lock_expires_at<$2) AND deleted_at IS NULL`,
		holderID, now, now.Add(ttl), id,
	)
	if err != nil {
		return false, fmt.Errorf("acquiring investigation lock: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking lock rows: %w", err)
	}
	return affected > 0, nil
}

func (r *Repository) RenewLock(ctx context.Context, id uuid.UUID, holderID string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx,
		`UPDATE investigations SET lock_expires_at=$1, updated_at=$2
		 WHERE id=$3 AND locked_by=$4 AND deleted_at IS NULL`,
		now.Add(ttl), now, id, holderID,
	)
	if err != nil {
		return false, fmt.Errorf("renewing investigation lock: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking renewal rows: %w", err)
	}
	return affected > 0, nil
}

func (r *Repository) ReleaseLock(ctx context.Context, id uuid.UUID, holderID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE investigations SET locked_by=NULL, lock_acquired_at=NULL, lock_expires_at=NULL, updated_at=$1
		 WHERE id=$2 AND locked_by=$3 AND deleted_at IS NULL`,
		time.Now().UTC(), id, holderID,
	)
	if err != nil {
		return fmt.Errorf("releasing investigation lock: %w", err)
	}
	return nil
}

func (r *Repository) SaveEvent(ctx context.Context, event *domain.PersistedEvent) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO investigation_events (id, investigation_id, event_type, payload, created_at)
		 VALUES ($1,$2,$3,$4,$5)`,
		event.ID, event.InvestigationID, event.EventType, event.Payload, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("saving investigation event: %w", err)
	}
	return nil
}

func (r *Repository) GetEvents(ctx context.Context, investigationID uuid.UUID, afterSeq int64) ([]*domain.PersistedEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, investigation_id, sequence_num, event_type, payload, created_at
		 FROM investigation_events
		 WHERE investigation_id=$1 AND sequence_num>$2
		 ORDER BY sequence_num ASC LIMIT 500`,
		investigationID, afterSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("getting investigation events: %w", err)
	}
	defer rows.Close()

	var events []*domain.PersistedEvent
	for rows.Next() {
		ev := &domain.PersistedEvent{}
		if err := rows.Scan(&ev.ID, &ev.InvestigationID, &ev.SequenceNum,
			&ev.EventType, &ev.Payload, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning event row: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (r *Repository) UpsertRetryCount(ctx context.Context, messageKey string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO kafka_message_retries (message_key, retry_count, updated_at)
		VALUES ($1,1,NOW())
		ON CONFLICT (message_key) DO UPDATE
			SET retry_count=kafka_message_retries.retry_count+1, updated_at=NOW()
		RETURNING retry_count`, messageKey,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("upserting retry count: %w", err)
	}
	return count, nil
}

func (r *Repository) GetRetryCount(ctx context.Context, messageKey string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT retry_count FROM kafka_message_retries WHERE message_key=$1`, messageKey,
	).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("getting retry count: %w", err)
	}
	return count, nil
}

func (r *Repository) DeleteRetryCount(ctx context.Context, messageKey string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM kafka_message_retries WHERE message_key=$1`, messageKey)
	if err != nil {
		return fmt.Errorf("deleting retry count: %w", err)
	}
	return nil
}

// ── Column list and scan helpers ──────────────────────────────────────────────

const investigationColumns = `
	id, incident_id, incident_number, idempotency_key, source_message_key,
	status, status_reason, root_entity_id, root_entity_type, failure_class,
	domain, severity, playbook_id, topology_path, correlation_id,
	incident_started_at, triggered_at,
	started_at, evidence_complete_at, completed_at, elapsed_ms, time_budget_ms,
	locked_by, lock_acquired_at, lock_expires_at,
	recovery_attempt_count, last_recovery_at,
	hypotheses_generated, hypotheses_rejected, evidence_gathered, evidence_sources,
	root_cause_hypothesis_id, confidence, confidence_band, root_cause_summary,
	causal_chain, blast_radius_summary,
	narrative, narrative_model, narrative_generated_at,
	report_version, late_evidence_window_closes_at,
	feedback_status, feedback_by, feedback_at, feedback_notes,
	actual_root_cause, actual_hypothesis_type,
	COALESCE(citations_json, 'null'::jsonb),
	deleted_at, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanInvestigation(row scanner) (*domain.Investigation, error) {
	var inv domain.Investigation
	var statusStr string
	var evidenceSourcesJSON []byte
	// JSONB columns that can be NULL — scan into []byte, then assign to json.RawMessage
	var causalChainBytes, blastRadiusBytes, citationsBytes []byte

	err := row.Scan(
		&inv.ID, &inv.IncidentID, &inv.IncidentNumber,
		&inv.IdempotencyKey, &inv.SourceMessageKey,
		&statusStr, &inv.StatusReason,
		&inv.RootEntityID, &inv.RootEntityType, &inv.FailureClass,
		&inv.Domain, &inv.Severity, &inv.PlaybookID, &inv.TopologyPath, &inv.CorrelationID,
		&inv.IncidentStartedAt, &inv.TriggeredAt,
		&inv.StartedAt, &inv.EvidenceCompleteAt, &inv.CompletedAt,
		&inv.ElapsedMs, &inv.TimeBudgetMs,
		&inv.LockedBy, &inv.LockAcquiredAt, &inv.LockExpiresAt,
		&inv.RecoveryAttemptCount, &inv.LastRecoveryAt,
		&inv.HypothesesGenerated, &inv.HypothesesRejected,
		&inv.EvidenceGathered, &evidenceSourcesJSON,
		&inv.RootCauseHypothesisID, &inv.Confidence,
		&inv.ConfidenceBand, &inv.RootCauseSummary,
		&causalChainBytes, &blastRadiusBytes,
		&inv.Narrative, &inv.NarrativeModel, &inv.NarrativeGeneratedAt,
		&inv.ReportVersion, &inv.LateEvidenceWindowClosesAt,
		&inv.FeedbackStatus, &inv.FeedbackBy, &inv.FeedbackAt,
		&inv.FeedbackNotes, &inv.ActualRootCause, &inv.ActualHypothesisType,
		&citationsBytes,
		&inv.DeletedAt, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrInvestigationNotFound{}
	}
	if err != nil {
		return nil, fmt.Errorf("scanning investigation: %w", err)
	}

	inv.Status = domain.Status(statusStr)
	if len(evidenceSourcesJSON) > 0 {
		_ = json.Unmarshal(evidenceSourcesJSON, &inv.EvidenceSources)
	}
	// Assign nullable JSONB bytes — nil stays nil (json.RawMessage(nil) is valid).
	if len(causalChainBytes) > 0 {
		inv.CausalChain = json.RawMessage(causalChainBytes)
	}
	if len(blastRadiusBytes) > 0 {
		inv.BlastRadiusSummary = json.RawMessage(blastRadiusBytes)
	}
	if len(citationsBytes) > 0 && string(citationsBytes) != "null" {
		inv.CitationsJSON = json.RawMessage(citationsBytes)
	}
	return &inv, nil
}

func scanInvestigations(rows *sql.Rows) ([]*domain.Investigation, error) {
	var result []*domain.Investigation
	for rows.Next() {
		inv, err := scanInvestigation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating investigation rows: %w", err)
	}
	return result, nil
}
