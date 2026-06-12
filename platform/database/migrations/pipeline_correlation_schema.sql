-- pipeline_correlation_schema.sql
-- Creates final_correlation_results (missing table) and adds missing columns to
-- correlation_results so storeAggregationResult never fails with pq: column not found.

-- ── final_correlation_results ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS final_correlation_results (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id            UUID NOT NULL,
    correlation_id      TEXT NOT NULL,
    decision            TEXT NOT NULL,
    final_score         DOUBLE PRECISION NOT NULL DEFAULT 0,
    confidence          DOUBLE PRECISION NOT NULL DEFAULT 0,
    best_match_id       UUID,
    strategy_results    JSONB,
    weights_used        JSONB,
    processing_time_ms  BIGINT,
    reasoning           TEXT,
    dominant_strategy   TEXT,
    recommended_action  TEXT,
    metadata            JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_fcr_alert_id    ON final_correlation_results (alert_id);
CREATE INDEX IF NOT EXISTS idx_fcr_created_at  ON final_correlation_results (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_fcr_decision    ON final_correlation_results (decision);

-- ── correlation_results: add any missing columns ──────────────────────────────
ALTER TABLE correlation_results
    ADD COLUMN IF NOT EXISTS final_score         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS decision            TEXT,
    ADD COLUMN IF NOT EXISTS dominant_strategy   TEXT,
    ADD COLUMN IF NOT EXISTS topology_score      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS semantic_score      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS temporal_score      DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS rules_score         DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS reasoning           TEXT;
