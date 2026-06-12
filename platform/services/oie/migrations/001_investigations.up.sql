-- ============================================================
-- OIE: investigations table
-- ============================================================

CREATE TABLE IF NOT EXISTS investigations (
    id                              UUID            NOT NULL,
    incident_id                     UUID            NOT NULL,
    incident_number                 VARCHAR(50)     NOT NULL DEFAULT '',
    idempotency_key                 VARCHAR(255)    NOT NULL,
    source_message_key              VARCHAR(255)    NOT NULL DEFAULT '',
    status                          VARCHAR(30)     NOT NULL DEFAULT 'PENDING',
    status_reason                   TEXT            NOT NULL DEFAULT '',
    root_entity_id                  UUID,
    root_entity_type                VARCHAR(100),
    failure_class                   VARCHAR(100),
    domain                          VARCHAR(50),
    severity                        VARCHAR(20)     NOT NULL,
    playbook_id                     VARCHAR(100)    NOT NULL DEFAULT '',
    incident_started_at             TIMESTAMPTZ     NOT NULL,
    triggered_at                    TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    started_at                      TIMESTAMPTZ,
    evidence_complete_at            TIMESTAMPTZ,
    completed_at                    TIMESTAMPTZ,
    elapsed_ms                      INTEGER,
    time_budget_ms                  INTEGER         NOT NULL DEFAULT 45000,
    locked_by                       VARCHAR(255),
    lock_acquired_at                TIMESTAMPTZ,
    lock_expires_at                 TIMESTAMPTZ,
    recovery_attempt_count          INTEGER         NOT NULL DEFAULT 0,
    last_recovery_at                TIMESTAMPTZ,
    hypotheses_generated            INTEGER         NOT NULL DEFAULT 0,
    hypotheses_rejected             INTEGER         NOT NULL DEFAULT 0,
    evidence_gathered               INTEGER         NOT NULL DEFAULT 0,
    evidence_sources                JSONB           NOT NULL DEFAULT '[]'::jsonb,
    root_cause_hypothesis_id        UUID,
    confidence                      FLOAT,
    confidence_band                 VARCHAR(30),
    root_cause_summary              TEXT,
    causal_chain                    JSONB,
    blast_radius_summary            JSONB,
    narrative                       TEXT,
    narrative_model                 VARCHAR(50),
    narrative_generated_at          TIMESTAMPTZ,
    report_version                  INTEGER         NOT NULL DEFAULT 1,
    late_evidence_window_closes_at  TIMESTAMPTZ,
    feedback_status                 VARCHAR(20)     NOT NULL DEFAULT 'no_feedback',
    feedback_by                     UUID,
    feedback_at                     TIMESTAMPTZ,
    feedback_notes                  TEXT,
    actual_root_cause               TEXT,
    actual_hypothesis_type          VARCHAR(100),
    deleted_at                      TIMESTAMPTZ,
    created_at                      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at                      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id)
) PARTITION BY RANGE (triggered_at);

-- Create initial monthly partitions
DO $$
DECLARE
    start_date DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_date   DATE := DATE_TRUNC('month', CURRENT_DATE) + INTERVAL '3 months';
    part_name  TEXT;
    part_start TEXT;
    part_end   TEXT;
BEGIN
    WHILE start_date < end_date LOOP
        part_name  := 'investigations_' || TO_CHAR(start_date, 'YYYY_MM');
        part_start := TO_CHAR(start_date, 'YYYY-MM-DD');
        part_end   := TO_CHAR(start_date + INTERVAL '1 month', 'YYYY-MM-DD');
        EXECUTE FORMAT(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF investigations FOR VALUES FROM (%L) TO (%L)',
            part_name, part_start, part_end
        );
        start_date := start_date + INTERVAL '1 month';
    END LOOP;
END;
$$;

-- Prevent duplicate active investigations for the same incident.
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_investigation
    ON investigations (incident_id)
    WHERE status NOT IN ('COMPLETED','FAILED','CANCELLED') AND deleted_at IS NULL;

-- Prevent Kafka at-least-once duplicate processing.
CREATE UNIQUE INDEX IF NOT EXISTS uq_investigation_idempotency
    ON investigations (idempotency_key)
    WHERE status NOT IN ('COMPLETED','FAILED','CANCELLED') AND deleted_at IS NULL;

-- Crash recovery: find orphaned investigations.
CREATE INDEX IF NOT EXISTS idx_investigations_recovery
    ON investigations (status, triggered_at, lock_expires_at)
    WHERE status NOT IN ('COMPLETED','FAILED','CANCELLED') AND deleted_at IS NULL;

-- Incident lookup.
CREATE INDEX IF NOT EXISTS idx_investigations_incident
    ON investigations (incident_id, triggered_at DESC)
    WHERE deleted_at IS NULL;

-- Lock holder lookup.
CREATE INDEX IF NOT EXISTS idx_investigations_locked_by
    ON investigations (locked_by, lock_expires_at)
    WHERE locked_by IS NOT NULL AND deleted_at IS NULL;

-- updated_at trigger
CREATE OR REPLACE FUNCTION investigations_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_investigations_updated_at
    BEFORE UPDATE ON investigations
    FOR EACH ROW EXECUTE FUNCTION investigations_set_updated_at();

ALTER TABLE investigations SET (
    autovacuum_vacuum_scale_factor  = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);
