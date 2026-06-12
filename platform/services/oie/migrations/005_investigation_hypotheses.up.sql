-- ============================================================
-- OIE: investigation_hypotheses table
-- Stores one row per (investigation, hypothesis_type).
-- ON CONFLICT allows re-scoring with updated evidence (late evidence).
-- ============================================================

CREATE TABLE IF NOT EXISTS investigation_hypotheses (
    id                          UUID        NOT NULL DEFAULT gen_random_uuid(),
    investigation_id            UUID        NOT NULL,
    -- type maps to a HypothesisTemplate.Type (e.g. "node_cloudstack_vm_failure").
    type                        VARCHAR(80) NOT NULL,
    title                       TEXT        NOT NULL DEFAULT '',
    description                 TEXT        NOT NULL DEFAULT '',

    -- Scoring result
    status                      VARCHAR(30) NOT NULL DEFAULT 'ACTIVE',
    confidence                  FLOAT       NOT NULL DEFAULT 0.0,
    band                        VARCHAR(30) NOT NULL DEFAULT 'INSUFFICIENT_EVIDENCE',
    rank                        INTEGER,

    -- Score decomposition (for UI explainability)
    base_score                  FLOAT       NOT NULL DEFAULT 0.0,
    evidence_boost              FLOAT       NOT NULL DEFAULT 0.0,
    contradiction_penalty       FLOAT       NOT NULL DEFAULT 0.0,
    topology_adjustment         FLOAT       NOT NULL DEFAULT 0.0,
    historical_adjustment       FLOAT       NOT NULL DEFAULT 0.0,

    -- Evidence accounting
    supporting_evidence_count   INTEGER     NOT NULL DEFAULT 0,
    contradicting_evidence_count INTEGER    NOT NULL DEFAULT 0,
    -- Comma-separated list of missing required evidence types
    missing_required_evidence   TEXT        NOT NULL DEFAULT '',

    -- Rejection
    rejection_reason            TEXT,
    rejection_evidence_id       UUID,

    -- Implication context
    implicated_entity_ids       UUID[]      NOT NULL DEFAULT '{}',
    implicated_change_id        UUID,
    implicated_change_summary   TEXT,

    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id),
    -- Each (investigation, hypothesis_type) pair is unique.
    -- ON CONFLICT DO UPDATE enables re-scoring without duplicate rows.
    UNIQUE (investigation_id, type)
);

CREATE INDEX IF NOT EXISTS idx_investigation_hypotheses_inv
    ON investigation_hypotheses (investigation_id, rank ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS idx_investigation_hypotheses_winner
    ON investigation_hypotheses (investigation_id, confidence DESC)
    WHERE status NOT IN ('REJECTED') AND confidence >= 0.25;

CREATE TRIGGER trg_hypotheses_updated_at
    BEFORE UPDATE ON investigation_hypotheses
    FOR EACH ROW EXECUTE FUNCTION investigations_set_updated_at();
