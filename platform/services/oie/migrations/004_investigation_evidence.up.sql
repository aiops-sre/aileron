-- ============================================================
-- OIE: investigation_evidence table
-- Stores all evidence gathered during investigations.
-- Range-partitioned by created_at for long-term performance.
-- UPSERT on (investigation_id, idempotency_key) prevents
-- duplicate inserts from crash recovery.
-- ============================================================

CREATE TABLE IF NOT EXISTS investigation_evidence (
    id                  UUID        NOT NULL,
    investigation_id    UUID        NOT NULL,
    -- run_id groups evidence from one investigation run.
    -- On crash recovery, a new run_id is assigned.
    -- Scoring uses only the latest run_id.
    run_id              UUID        NOT NULL,
    hypothesis_id       UUID,

    fetcher_id          VARCHAR(80) NOT NULL,
    idempotency_key     VARCHAR(255) NOT NULL,

    evidence_type       VARCHAR(80)  NOT NULL,
    source              VARCHAR(50)  NOT NULL,
    role                VARCHAR(20)  NOT NULL DEFAULT 'CONTEXT',
    weight              FLOAT        NOT NULL DEFAULT 0.0,
    evidence_confidence FLOAT        NOT NULL DEFAULT 1.0,
    evidence_group      VARCHAR(50),

    temporal_mode       VARCHAR(20)  NOT NULL DEFAULT 'CURRENT',
    as_of_time          TIMESTAMPTZ,
    temporal_gap_secs   INTEGER,

    description         TEXT         NOT NULL DEFAULT '',
    payload             JSONB        NOT NULL DEFAULT '{}'::jsonb,

    occurred_at         TIMESTAMPTZ,
    gathered_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    fetch_duration_ms   BIGINT       NOT NULL DEFAULT 0,
    fetch_status        VARCHAR(20)  NOT NULL DEFAULT 'SUCCESS',
    fetch_error         TEXT,

    applied_to_scoring  BOOLEAN      NOT NULL DEFAULT FALSE,

    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Monthly partitions.
DO $$
DECLARE
    start_date DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_date   DATE := DATE_TRUNC('month', CURRENT_DATE) + INTERVAL '3 months';
    part_name  TEXT;
    part_start TEXT;
    part_end   TEXT;
BEGIN
    WHILE start_date < end_date LOOP
        part_name  := 'investigation_evidence_' || TO_CHAR(start_date, 'YYYY_MM');
        part_start := TO_CHAR(start_date, 'YYYY-MM-DD');
        part_end   := TO_CHAR(start_date + INTERVAL '1 month', 'YYYY-MM-DD');
        EXECUTE FORMAT(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF investigation_evidence FOR VALUES FROM (%L) TO (%L)',
            part_name, part_start, part_end
        );
        start_date := start_date + INTERVAL '1 month';
    END LOOP;
END;
$$;

-- Idempotency: prevents duplicate evidence on crash recovery.
CREATE UNIQUE INDEX IF NOT EXISTS uq_investigation_evidence_idempotency
    ON investigation_evidence (investigation_id, idempotency_key);

-- Fast lookup by investigation + run_id (scoring query).
CREATE INDEX IF NOT EXISTS idx_investigation_evidence_inv_run
    ON investigation_evidence (investigation_id, run_id);

-- Fast lookup by hypothesis (hypothesis evaluation query).
CREATE INDEX IF NOT EXISTS idx_investigation_evidence_hypothesis
    ON investigation_evidence (hypothesis_id)
    WHERE hypothesis_id IS NOT NULL;

-- Scoring filter: unapplied evidence for late re-scoring.
CREATE INDEX IF NOT EXISTS idx_investigation_evidence_unapplied
    ON investigation_evidence (investigation_id)
    WHERE applied_to_scoring = FALSE;

ALTER TABLE investigation_evidence SET (
    autovacuum_vacuum_scale_factor  = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);
