-- Migration: Intelligent Correlation & RCA Integration
-- Adds columns to support end-to-end: alert → correlation → incident → RCA → update

-- ─── Incident enrichment columns ──────────────────────────────────────────────
ALTER TABLE incidents
  ADD COLUMN IF NOT EXISTS rca_investigation_id TEXT,
  ADD COLUMN IF NOT EXISTS rca_status           TEXT NOT NULL DEFAULT 'none',
  ADD COLUMN IF NOT EXISTS rca_confidence        DECIMAL(5,2),
  ADD COLUMN IF NOT EXISTS blast_radius          JSONB DEFAULT '[]',
  ADD COLUMN IF NOT EXISTS affected_services_names TEXT[],
  ADD COLUMN IF NOT EXISTS topology_path         TEXT,
  ADD COLUMN IF NOT EXISTS correlation_method    TEXT,
  ADD COLUMN IF NOT EXISTS dominant_strategy     TEXT;

CREATE INDEX IF NOT EXISTS idx_incidents_rca_investigation ON incidents(rca_investigation_id);
CREATE INDEX IF NOT EXISTS idx_incidents_rca_status        ON incidents(rca_status);
CREATE INDEX IF NOT EXISTS idx_incidents_auto_created      ON incidents(auto_created);

-- ─── Pipeline correlation results (AIOps dashboard) ────────────────────────
CREATE TABLE IF NOT EXISTS pipeline_correlation_results (
  id                UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  alert_id          UUID        NOT NULL,
  incident_id       UUID,
  alert_title       TEXT,
  alert_source      TEXT,
  alert_severity    TEXT,
  decision          TEXT,
  final_score       DECIMAL(5,4),
  dominant_strategy TEXT,
  semantic_score    DECIMAL(5,4),
  temporal_score    DECIMAL(5,4),
  topology_score    DECIMAL(5,4),
  rules_score       DECIMAL(5,4),
  reasoning         TEXT,
  ai_root_cause     TEXT,
  matched_node_label TEXT,
  root_cause_label  TEXT,
  elapsed_ms        BIGINT,
  processed_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pcr_alert_id    ON pipeline_correlation_results(alert_id);
CREATE INDEX IF NOT EXISTS idx_pcr_incident_id ON pipeline_correlation_results(incident_id);
CREATE INDEX IF NOT EXISTS idx_pcr_processed   ON pipeline_correlation_results(processed_at DESC);

-- ─── RCA investigations (mirror for quick joins) ───────────────────────────
-- rca_investigations table already exists (created by rca-orchestrator).
-- If not (first run), create it here too so backend can write back.
CREATE TABLE IF NOT EXISTS rca_investigations (
  id         TEXT PRIMARY KEY,
  incident_id TEXT,
  data       JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rca_incident ON rca_investigations(incident_id);
