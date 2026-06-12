-- =============================================================================
-- AlertHub v2 Safe Migration
-- Applied to: alert-engine-poc / postgres-primary-0
-- Strategy: additive only, no drops, IF NOT EXISTS throughout
-- alert_correlations TABLE → renamed bak + VIEW created for backward compat
-- =============================================================================

BEGIN;

-- Required extensions
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Partition helper (idempotent)
CREATE OR REPLACE FUNCTION create_monthly_partition(
    parent_table  TEXT,
    partition_date DATE
) RETURNS TEXT AS $$
DECLARE
    partition_name TEXT;
    start_date     DATE;
    end_date       DATE;
BEGIN
    start_date     := DATE_TRUNC('month', partition_date);
    end_date       := start_date + INTERVAL '1 month';
    partition_name := parent_table || '_' || TO_CHAR(start_date, 'YYYY_MM');
    IF NOT EXISTS (
        SELECT 1 FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = partition_name AND n.nspname = 'public'
    ) THEN
        EXECUTE FORMAT(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
            partition_name, parent_table, start_date, end_date
        );
    END IF;
    RETURN partition_name;
END;
$$ LANGUAGE plpgsql;

-- =============================================================================
-- PLATFORM: teams (users.team_id already exists but table is missing)
-- =============================================================================
CREATE TABLE IF NOT EXISTS teams (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL UNIQUE,
    description    TEXT,
    parent_team_id UUID REFERENCES teams(id),
    manager_id     UUID,
    metadata       JSONB NOT NULL DEFAULT '{}',
    is_active      BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_teams_name    ON teams(name);
CREATE INDEX IF NOT EXISTS idx_teams_active  ON teams(is_active) WHERE is_active = true;

-- =============================================================================
-- USERS: missing columns
-- =============================================================================
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS oauth_subject    TEXT,
    ADD COLUMN IF NOT EXISTS mas_dsid         TEXT,
    ADD COLUMN IF NOT EXISTS display_name     TEXT,
    ADD COLUMN IF NOT EXISTS avatar_url       TEXT,
    ADD COLUMN IF NOT EXISTS timezone         TEXT NOT NULL DEFAULT 'UTC',
    ADD COLUMN IF NOT EXISTS preferences      JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS expertise_areas  TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS last_login_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS failed_logins    INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS locked_until     TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_users_oauth ON users(oauth_source, oauth_subject)
    WHERE oauth_source IS NOT NULL;

-- =============================================================================
-- ALERTS: missing columns
-- =============================================================================
ALTER TABLE alerts
    ADD COLUMN IF NOT EXISTS is_duplicate     BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS duplicate_of     UUID,
    ADD COLUMN IF NOT EXISTS dedup_key        TEXT,
    ADD COLUMN IF NOT EXISTS cluster_id       UUID,
    ADD COLUMN IF NOT EXISTS enrichment_data  JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS ai_summary       TEXT,
    ADD COLUMN IF NOT EXISTS ai_confidence    NUMERIC(5,4),
    ADD COLUMN IF NOT EXISTS runbook_url      TEXT,
    ADD COLUMN IF NOT EXISTS service_name     TEXT,
    ADD COLUMN IF NOT EXISTS environment      TEXT,
    ADD COLUMN IF NOT EXISTS resolved_by      UUID,
    ADD COLUMN IF NOT EXISTS resolution_note  TEXT;

-- =============================================================================
-- INCIDENTS: missing columns
-- =============================================================================
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS resolved_by UUID;

-- =============================================================================
-- CORRELATION DOMAIN: unified correlation_results (partitioned)
-- =============================================================================
CREATE TABLE IF NOT EXISTS correlation_results (
    id               UUID        NOT NULL DEFAULT gen_random_uuid(),
    alert_id         UUID        NOT NULL,
    correlation_id   UUID,
    correlation_type TEXT        NOT NULL DEFAULT 'rule_based'
                     CHECK (correlation_type IN (
                         'rule_based','ai_semantic','davis_ai','topology',
                         'temporal','pipeline','manual')),
    confidence_score NUMERIC(5,4) CHECK (confidence_score BETWEEN 0 AND 1),
    is_duplicate     BOOLEAN     NOT NULL DEFAULT false,
    duplicate_of     UUID,
    group_id         UUID,
    cluster_key      TEXT,
    incident_id      UUID,
    engine_name      TEXT,
    strategy_weights JSONB       NOT NULL DEFAULT '{}',
    metadata         JSONB       NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ,
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Pre-create 2024–2028 monthly partitions
DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series(
        '2024-01-01'::date, '2028-12-01'::date, '1 month'::interval
    ) LOOP
        PERFORM create_monthly_partition('correlation_results', d);
    END LOOP;
END $$;

-- Migrate from pipeline_correlation_results (519 rows, actual schema)
INSERT INTO correlation_results
    (id, alert_id, correlation_id, correlation_type,
     confidence_score, is_duplicate,
     incident_id, engine_name, strategy_weights, metadata, created_at)
SELECT
    id,
    alert_id,
    NULL,  -- pipeline_correlation_results has no correlation_id column
    'pipeline',
    CASE WHEN final_score IS NOT NULL
         THEN LEAST(GREATEST(final_score::NUMERIC, 0), 1) ELSE NULL END,
    false,
    incident_id,
    COALESCE(dominant_strategy, 'parallel_pipeline'),
    jsonb_build_object(
        'semantic_score',    semantic_score,
        'temporal_score',    temporal_score,
        'topology_score',    topology_score,
        'rules_score',       rules_score,
        'decision',          decision
    ),
    jsonb_build_object(
        'reasoning',         reasoning,
        'ai_root_cause',     ai_root_cause,
        'matched_node_label',matched_node_label,
        'root_cause_label',  root_cause_label,
        'elapsed_ms',        elapsed_ms,
        'alert_title',       alert_title,
        'alert_source',      alert_source,
        'alert_severity',    alert_severity
    ),
    COALESCE(processed_at, NOW())
FROM pipeline_correlation_results
WHERE NOT EXISTS (
    SELECT 1 FROM correlation_results cr
    WHERE cr.id = pipeline_correlation_results.id
      AND cr.created_at = COALESCE(pipeline_correlation_results.processed_at, NOW())
);

-- =============================================================================
-- alert_correlations: TABLE → VIEW swap (backward compat for Go code)
-- alert_correlations currently has 0 rows, so rename is instant
-- =============================================================================
DO $$
BEGIN
    -- Only rename if it's still a table (not already a view)
    IF EXISTS (
        SELECT 1 FROM pg_tables
        WHERE schemaname = 'public' AND tablename = 'alert_correlations'
    ) THEN
        ALTER TABLE alert_correlations RENAME TO alert_correlations_bak;
    END IF;
END $$;

CREATE OR REPLACE VIEW alert_correlations AS
    SELECT DISTINCT ON (alert_id)
        id,
        alert_id,
        correlation_id,
        correlation_type   AS engine_name,
        confidence_score,
        is_duplicate,
        duplicate_of,
        group_id,
        incident_id,
        metadata,
        created_at,
        expires_at
    FROM correlation_results
    ORDER BY alert_id, created_at DESC;

-- =============================================================================
-- CORRELATION: alert_similarity_cache
-- =============================================================================
CREATE TABLE IF NOT EXISTS alert_similarity_cache (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id_a  UUID NOT NULL,
    alert_id_b  UUID NOT NULL,
    similarity  NUMERIC(5,4) CHECK (similarity BETWEEN 0 AND 1),
    method      TEXT,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '2 hours',
    UNIQUE (alert_id_a, alert_id_b)
);

CREATE INDEX IF NOT EXISTS idx_similarity_pair    ON alert_similarity_cache(alert_id_a, alert_id_b);
CREATE INDEX IF NOT EXISTS idx_similarity_expires ON alert_similarity_cache(expires_at);

-- =============================================================================
-- TOPOLOGY: new tables
-- =============================================================================
CREATE TABLE IF NOT EXISTS topology_entities (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id     TEXT NOT NULL,
    entity_type     TEXT NOT NULL,
    name            TEXT NOT NULL,
    source          TEXT NOT NULL DEFAULT 'dynatrace',
    management_zone TEXT,
    cluster_name    TEXT,
    labels          JSONB NOT NULL DEFAULT '{}',
    properties      JSONB NOT NULL DEFAULT '{}',
    last_synced_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, external_id)
);

CREATE INDEX IF NOT EXISTS idx_topo_entities_source  ON topology_entities(source, entity_type);
CREATE INDEX IF NOT EXISTS idx_topo_entities_extid   ON topology_entities(external_id);
CREATE INDEX IF NOT EXISTS idx_topo_entities_zone    ON topology_entities(management_zone) WHERE management_zone IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_topo_entities_cluster ON topology_entities(cluster_name)    WHERE cluster_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_topo_entities_labels  ON topology_entities USING GIN(labels);

CREATE TABLE IF NOT EXISTS topology_management_zones (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    zone_name   TEXT NOT NULL UNIQUE,
    zone_id     TEXT,
    source      TEXT NOT NULL DEFAULT 'dynatrace',
    properties  JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS topology_sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_type  TEXT NOT NULL,
    name         TEXT NOT NULL UNIQUE,
    config       JSONB NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT true,
    last_sync_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_topo_sources_enabled ON topology_sources(enabled) WHERE enabled = true;

CREATE TABLE IF NOT EXISTS topology_sync_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id       UUID NOT NULL REFERENCES topology_sources(id),
    status          TEXT NOT NULL,
    entities_synced INTEGER NOT NULL DEFAULT 0,
    errors          JSONB NOT NULL DEFAULT '[]',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_topo_sync_source ON topology_sync_log(source_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_topo_sync_status ON topology_sync_log(status, created_at DESC);

-- =============================================================================
-- SLO / SLI
-- =============================================================================
CREATE TABLE IF NOT EXISTS slo_definitions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    service_name TEXT NOT NULL,
    sli_type     TEXT NOT NULL,
    target       NUMERIC(6,4) NOT NULL,
    window_days  INTEGER NOT NULL DEFAULT 30,
    error_budget NUMERIC(6,4) NOT NULL,
    is_active    BOOLEAN NOT NULL DEFAULT true,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS slo_violations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slo_id         UUID NOT NULL REFERENCES slo_definitions(id),
    violation_type TEXT NOT NULL,
    severity       TEXT NOT NULL,
    current_value  NUMERIC(10,6),
    threshold      NUMERIC(10,6),
    started_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at    TIMESTAMPTZ,
    metadata       JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_slo_violations_slo        ON slo_violations(slo_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_slo_violations_unresolved ON slo_violations(slo_id) WHERE resolved_at IS NULL;

-- =============================================================================
-- SERVICE HEALTH CHECKS (partitioned)
-- =============================================================================
CREATE TABLE IF NOT EXISTS service_health_checks (
    id           UUID        NOT NULL DEFAULT gen_random_uuid(),
    service_name TEXT        NOT NULL,
    endpoint_url TEXT        NOT NULL,
    status       TEXT        NOT NULL,
    response_ms  INTEGER,
    status_code  INTEGER,
    error_msg    TEXT,
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata     JSONB       NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, checked_at)
) PARTITION BY RANGE (checked_at);

DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series(
        '2025-01-01'::date, '2028-12-01'::date, '1 month'::interval
    ) LOOP
        PERFORM create_monthly_partition('service_health_checks', d);
    END LOOP;
END $$;

CREATE INDEX IF NOT EXISTS idx_svc_health_service ON service_health_checks(service_name, checked_at DESC);

-- =============================================================================
-- SECURITY EVENTS
-- =============================================================================
CREATE TABLE IF NOT EXISTS security_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    severity   TEXT NOT NULL DEFAULT 'medium',
    source_ip  INET,
    user_id    UUID,
    details    JSONB NOT NULL DEFAULT '{}',
    resolved   BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sec_events_type       ON security_events(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sec_events_unresolved ON security_events(created_at DESC) WHERE resolved = false;

-- =============================================================================
-- INDEXES: alerts (missing composite/partial indexes)
-- Note: CONCURRENTLY cannot run inside a transaction — applied after COMMIT
-- =============================================================================

COMMIT;

-- Now apply CONCURRENTLY indexes (must be outside a transaction block)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_status_sev_created
    ON alerts(status, severity, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_active
    ON alerts(created_at DESC, severity)
    WHERE status IN ('open','acknowledged');

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_fingerprint
    ON alerts(fingerprint) WHERE fingerprint IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_title_trgm
    ON alerts USING GIN(title gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_labels
    ON alerts USING GIN(labels jsonb_path_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_corr_results_alert_id
    ON correlation_results(alert_id, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_corr_results_correlation_id
    ON correlation_results(correlation_id) WHERE correlation_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_corr_results_incident
    ON correlation_results(incident_id) WHERE incident_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_active
    ON incidents(created_at DESC, severity)
    WHERE status NOT IN ('resolved','closed');

-- =============================================================================
-- TRIGGERS: updated_at maintenance
-- =============================================================================
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE tbl TEXT;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'teams','topology_entities','topology_management_zones',
        'topology_sources','slo_definitions','llm_configs','auth_providers',
        'correlation_engine_config'
    ] LOOP
        EXECUTE FORMAT(
            'DROP TRIGGER IF EXISTS trg_%s_updated_at ON %I;
             CREATE TRIGGER trg_%s_updated_at
             BEFORE UPDATE ON %I
             FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();',
            tbl, tbl, tbl, tbl
        );
    END LOOP;
END $$;

-- =============================================================================
-- ENSURE NEXT MONTH PARTITIONS (maintenance function)
-- =============================================================================
CREATE OR REPLACE FUNCTION ensure_next_month_partitions() RETURNS VOID AS $$
DECLARE
    tbl  TEXT;
    next DATE := DATE_TRUNC('month', NOW() + INTERVAL '1 month');
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'correlation_results', 'service_health_checks'
    ] LOOP
        PERFORM create_monthly_partition(tbl, next);
        PERFORM create_monthly_partition(tbl, next + INTERVAL '1 month');
    END LOOP;
END;
$$ LANGUAGE plpgsql;
