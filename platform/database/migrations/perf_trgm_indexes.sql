-- pg_trgm GIN indexes for ILIKE text-search queries on incidents and alerts.
-- Without these, every ILIKE '%…%' is a full table scan.
--
-- Apply once:
--   kubectl exec -n alert-engine-poc postgres-primary-0 -- \
--     psql -U alerthub -d alerthub -f /path/to/perf_trgm_indexes.sql

-- Enable pg_trgm if not already installed (idempotent)
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ─── incidents ──────────────────────────────────────────────────────────────
-- These columns appear in ILIKE queries in root_cause_engine.go and alert_pipeline.go
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_topology_path_trgm
    ON incidents USING GIN (topology_path gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_title_trgm
    ON incidents USING GIN (title gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_description_trgm
    ON incidents USING GIN (description gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_correlation_id_trgm
    ON incidents USING GIN (correlation_id gin_trgm_ops);

-- B-tree index on status + created_at (used in every RCE/pipeline WHERE clause)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_status_created_at
    ON incidents (status, created_at DESC)
    WHERE auto_created = TRUE;

-- ─── alerts ─────────────────────────────────────────────────────────────────
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_title_trgm
    ON alerts USING GIN (title gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_source_id
    ON alerts (source, source_id)
    WHERE source_id IS NOT NULL;
