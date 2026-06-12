# Database Migration Plan: schema v1 → schema v2

**Target:** Replace the fragmented feature-migration schema with the unified domain-driven `schema_v2.sql`.  
**Risk level:** High — production data. Follow every step in order. Do not skip checkpoints.

**Production coordinates:**
- Cluster: `example-cluster`
- Namespace: `aileron`
- PostgreSQL service: `postgres-primary.aileron.svc.cluster.local:5432`
- Database: `alerthub`
- DB user: `alerthub`

---

## Cluster Context Setup

```bash
# Ensure you are pointed at the right cluster before running any command
kubectl config use-context example-cluster

# Confirm namespace and postgres pod are visible
kubectl get pods -n aileron -l app=postgres
# or, if deployed as a StatefulSet:
kubectl get statefulset -n aileron | grep postgres
```

---

## Prerequisites

```bash
# Open a psql session into the production database (run this in a separate terminal,
# keep it open throughout the migration)
kubectl run -it --rm pg-client \
  --image=postgres:14-alpine \
  --namespace=aileron \
  --restart=Never \
  --env="PGPASSWORD=$(kubectl get secret alerthub-secrets -n aileron \
        -o jsonpath='{.data.postgres-password}' | base64 -d)" \
  -- psql -h postgres-primary.aileron.svc.cluster.local \
          -U alerthub -d alerthub
```

Once inside psql, run verification:

```sql
-- Verify PostgreSQL version (requires 14+)
SELECT version();

-- Verify available disk space (need 2× current DB size for safe migration)
SELECT pg_size_pretty(pg_database_size(current_database()));

-- Verify extensions
SELECT extname FROM pg_extension WHERE extname IN ('uuid-ossp','pg_trgm','btree_gin');
-- If pg_trgm is missing: CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- If btree_gin is missing: CREATE EXTENSION IF NOT EXISTS btree_gin;
```

---

## Phase 0 — Freeze and Backup (REQUIRED BEFORE ANY CHANGES)

```bash
# 0a. Find the postgres pod name
PGPOD=$(kubectl get pod -n aileron -l app=postgres \
        -o jsonpath='{.items[0].metadata.name}')
echo "Postgres pod: $PGPOD"

# 0b. Scale down the application to stop writes
kubectl scale deployment alerthub-enterprise-main --replicas=0 -n aileron
kubectl rollout status deployment/alerthub-enterprise-main -n aileron --timeout=60s

# 0c. Full logical backup inside the pod
BACKUP_FILE="alerthub_premigration_$(date +%Y%m%d_%H%M%S).dump"
kubectl exec -n aileron "$PGPOD" -- \
  pg_dump -U alerthub alerthub \
    --format=custom --compress=9 \
    -f "/tmp/$BACKUP_FILE"

# 0d. Copy backup out of the cluster to your local machine
mkdir -p ./backups
kubectl cp "aileron/$PGPOD:/tmp/$BACKUP_FILE" "./backups/$BACKUP_FILE"
echo "Backup saved: ./backups/$BACKUP_FILE"

# 0e. Verify the backup is complete and readable
pg_restore --list "./backups/$BACKUP_FILE" | wc -l
# Should print several hundred lines (one per database object)
```

---

## Phase 1 — Schema Extensions and New Infrastructure

Apply in a single transaction to keep schema consistent on rollback.

```sql
BEGIN;

-- Required extensions
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Create partition helper function (used by all phases)
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
        SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
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

COMMIT;
```

---

## Phase 2 — New Tables (Non-Breaking Additions)

These tables do not exist yet. Safe to create while the old schema is live.

```sql
BEGIN;

-- Platform domain additions
CREATE TABLE IF NOT EXISTS teams (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    parent_team_id UUID REFERENCES teams(id),
    manager_id  UUID,  -- FK added after users migration
    metadata    JSONB NOT NULL DEFAULT '{}',
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_expertise (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expertise_area TEXT NOT NULL,
    proficiency    TEXT NOT NULL DEFAULT 'intermediate'
                   CHECK (proficiency IN ('beginner','intermediate','expert')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Correlation domain additions
CREATE TABLE IF NOT EXISTS correlation_results (
    id                UUID        NOT NULL DEFAULT gen_random_uuid(),
    alert_id          UUID        NOT NULL,
    correlation_id    UUID,
    correlation_type  TEXT        NOT NULL DEFAULT 'rule_based'
                      CHECK (correlation_type IN
                             ('rule_based','ai_semantic','davis_ai','topology',
                              'temporal','pipeline','manual')),
    confidence_score  NUMERIC(5,4) CHECK (confidence_score BETWEEN 0 AND 1),
    is_duplicate      BOOLEAN     NOT NULL DEFAULT false,
    duplicate_of      UUID,
    group_id          UUID,
    cluster_key       TEXT,
    incident_id       UUID,
    engine_name       TEXT,
    strategy_weights  JSONB       NOT NULL DEFAULT '{}',
    metadata          JSONB       NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMPTZ,
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Pre-create 2025–2027 monthly partitions for correlation_results
DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series('2025-01-01'::date, '2027-12-01'::date, '1 month'::interval)
    LOOP
        PERFORM create_monthly_partition('correlation_results', d);
    END LOOP;
END $$;

CREATE TABLE IF NOT EXISTS alert_similarity_cache (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id_a     UUID NOT NULL,
    alert_id_b     UUID NOT NULL,
    similarity     NUMERIC(5,4) CHECK (similarity BETWEEN 0 AND 1),
    method         TEXT,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '2 hours',
    UNIQUE (alert_id_a, alert_id_b)
);

CREATE TABLE IF NOT EXISTS historical_patterns (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern_type   TEXT NOT NULL,
    alert_source   TEXT,
    pattern_data   JSONB NOT NULL DEFAULT '{}',
    frequency      INTEGER NOT NULL DEFAULT 0,
    confidence     NUMERIC(5,4),
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Topology domain additions
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

CREATE TABLE IF NOT EXISTS topology_sync_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id    UUID NOT NULL REFERENCES topology_sources(id),
    status       TEXT NOT NULL,
    entities_synced INTEGER NOT NULL DEFAULT 0,
    errors       JSONB NOT NULL DEFAULT '[]',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Config domain additions
CREATE TABLE IF NOT EXISTS auth_providers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_type TEXT NOT NULL CHECK (provider_type IN ('ldap','saml','oidc','local','mas')),
    name         TEXT NOT NULL UNIQUE,
    config       JSONB NOT NULL DEFAULT '{}',
    is_active    BOOLEAN NOT NULL DEFAULT true,
    priority     INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS llm_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    provider        TEXT NOT NULL,
    model_name      TEXT NOT NULL,
    endpoint_url    TEXT,
    api_key_secret  TEXT,
    max_tokens      INTEGER NOT NULL DEFAULT 4096,
    temperature     NUMERIC(3,2) NOT NULL DEFAULT 0.1,
    is_default      BOOLEAN NOT NULL DEFAULT false,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    capabilities    JSONB NOT NULL DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS llm_feedback (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id    UUID,
    config_id   UUID REFERENCES llm_configs(id),
    prompt_type TEXT NOT NULL,
    rating      INTEGER CHECK (rating BETWEEN 1 AND 5),
    feedback    TEXT,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS correlation_engine_config (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engine_name    TEXT NOT NULL UNIQUE,
    is_active      BOOLEAN NOT NULL DEFAULT true,
    weight         NUMERIC(5,4) NOT NULL DEFAULT 1.0,
    config         JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Platform: SLO / SLI
CREATE TABLE IF NOT EXISTS slo_definitions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    service_name    TEXT NOT NULL,
    sli_type        TEXT NOT NULL,
    target          NUMERIC(6,4) NOT NULL,
    window_days     INTEGER NOT NULL DEFAULT 30,
    error_budget    NUMERIC(6,4) NOT NULL,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    config          JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS slo_violations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slo_id          UUID NOT NULL REFERENCES slo_definitions(id),
    violation_type  TEXT NOT NULL,
    severity        TEXT NOT NULL,
    current_value   NUMERIC(10,6),
    threshold       NUMERIC(10,6),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    metadata        JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS service_health_checks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_name TEXT NOT NULL,
    endpoint_url TEXT NOT NULL,
    status       TEXT NOT NULL,
    response_ms  INTEGER,
    status_code  INTEGER,
    error_msg    TEXT,
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata     JSONB NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (checked_at);

DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series('2025-01-01'::date, '2027-12-01'::date, '1 month'::interval)
    LOOP
        PERFORM create_monthly_partition('service_health_checks', d);
    END LOOP;
END $$;

CREATE TABLE IF NOT EXISTS security_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type  TEXT NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'medium',
    source_ip   INET,
    user_id     UUID,
    details     JSONB NOT NULL DEFAULT '{}',
    resolved    BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMIT;
```

---

## Phase 3 — Alter Existing Tables (Additive Only)

Add new columns to tables that already exist. All new columns are nullable or have defaults, so this is non-breaking.

```sql
BEGIN;

-- users: add team, oauth, profile columns
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS team_id          UUID,
    ADD COLUMN IF NOT EXISTS display_name     TEXT,
    ADD COLUMN IF NOT EXISTS avatar_url       TEXT,
    ADD COLUMN IF NOT EXISTS timezone         TEXT NOT NULL DEFAULT 'UTC',
    ADD COLUMN IF NOT EXISTS preferences      JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS expertise_areas  TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS oauth_source     TEXT,
    ADD COLUMN IF NOT EXISTS oauth_subject    TEXT,
    ADD COLUMN IF NOT EXISTS mas_dsid         TEXT,
    ADD COLUMN IF NOT EXISTS last_login_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS failed_logins    INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS locked_until     TIMESTAMPTZ;

-- alerts: add team, enrichment, AI, dedup columns
ALTER TABLE alerts
    ADD COLUMN IF NOT EXISTS team_id          UUID,
    ADD COLUMN IF NOT EXISTS correlation_id   UUID,
    ADD COLUMN IF NOT EXISTS incident_id      UUID,
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
    ADD COLUMN IF NOT EXISTS first_seen_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_seen_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resolved_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resolved_by      UUID,
    ADD COLUMN IF NOT EXISTS resolution_note  TEXT,
    ADD COLUMN IF NOT EXISTS count            INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS labels           JSONB NOT NULL DEFAULT '{}';

-- incidents: add team, correlation, closure columns
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS team_id         UUID,
    ADD COLUMN IF NOT EXISTS correlation_id  UUID,
    ADD COLUMN IF NOT EXISTS closed_at       TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resolved_by     UUID;

-- oncall_shifts → align to new schema column names (additive)
ALTER TABLE oncall_shifts
    ADD COLUMN IF NOT EXISTS shift_type     TEXT NOT NULL DEFAULT 'primary'
                             CHECK (shift_type IN ('primary','secondary','shadow'));

-- alert_history → store previous value
ALTER TABLE alert_history
    ADD COLUMN IF NOT EXISTS previous_value JSONB;

COMMIT;
```

---

## Phase 4 — Partition Existing High-Volume Tables

**Critical:** `alerts`, `alert_history`, `audit_logs`, `notification_log`, and `incident_timeline` must be converted to partitioned tables. This requires a table swap — done with minimal downtime using the following pattern.

```sql
-- Pattern: rename original → _old, create partitioned version, copy data, create view for compatibility

BEGIN;

-- 4a: alerts
ALTER TABLE alerts RENAME TO alerts_old;

CREATE TABLE alerts (
    -- (full definition from schema_v2.sql — abbreviated here for readability)
    id              UUID        NOT NULL DEFAULT gen_random_uuid(),
    source          TEXT        NOT NULL,
    source_id       TEXT,
    title           TEXT        NOT NULL,
    description     TEXT,
    severity        TEXT        NOT NULL DEFAULT 'medium',
    status          TEXT        NOT NULL DEFAULT 'open',
    fingerprint     TEXT,
    entity_type     TEXT,
    entity_id       TEXT,
    service_name    TEXT,
    environment     TEXT,
    region          TEXT,
    tags            TEXT[]      NOT NULL DEFAULT '{}',
    labels          JSONB       NOT NULL DEFAULT '{}',
    metadata        JSONB       NOT NULL DEFAULT '{}',
    enrichment_data JSONB       NOT NULL DEFAULT '{}',
    ai_summary      TEXT,
    ai_confidence   NUMERIC(5,4),
    runbook_url     TEXT,
    is_duplicate    BOOLEAN     NOT NULL DEFAULT false,
    duplicate_of    UUID,
    dedup_key       TEXT,
    cluster_id      UUID,
    correlation_id  UUID,
    incident_id     UUID,
    assigned_to     UUID,
    team_id         UUID,
    count           INTEGER     NOT NULL DEFAULT 1,
    first_seen_at   TIMESTAMPTZ,
    last_seen_at    TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    resolved_by     UUID,
    resolution_note TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- legacy columns retained for compatibility
    agent_processed  BOOLEAN,
    agent_decision   TEXT,
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Create monthly partitions
DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series('2024-01-01'::date, '2027-12-01'::date, '1 month'::interval)
    LOOP
        PERFORM create_monthly_partition('alerts', d);
    END LOOP;
END $$;

-- Copy existing data
INSERT INTO alerts SELECT
    id, source, source_id, title, description, severity, status,
    fingerprint, entity_type, entity_id, NULL, NULL, region,
    COALESCE(tags, '{}'), COALESCE(labels, '{}'),
    COALESCE(metadata, '{}'), '{}', NULL, NULL, NULL,
    COALESCE(is_duplicate, false), duplicate_of, NULL, NULL, NULL, NULL,
    assigned_to, NULL,
    COALESCE(count, 1),
    first_seen_at, last_seen_at, NULL, NULL, NULL,
    created_at, updated_at,
    agent_processed, agent_decision
FROM alerts_old;

COMMIT;
```

```sql
BEGIN;

-- 4b: audit_logs
ALTER TABLE audit_logs RENAME TO audit_logs_old;

CREATE TABLE audit_logs (
    id            UUID        NOT NULL DEFAULT gen_random_uuid(),
    user_id       UUID,
    action        TEXT        NOT NULL,
    resource_type TEXT,
    resource_id   UUID,
    old_value     JSONB,
    new_value     JSONB,
    ip_address    INET,
    user_agent    TEXT,
    request_id    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series('2024-01-01'::date, '2027-12-01'::date, '1 month'::interval)
    LOOP
        PERFORM create_monthly_partition('audit_logs', d);
    END LOOP;
END $$;

INSERT INTO audit_logs
    SELECT id, user_id, action, resource_type, resource_id,
           old_value, new_value, ip_address::INET, user_agent, NULL, created_at
    FROM audit_logs_old;

COMMIT;
```

```sql
BEGIN;

-- 4c: notification_log
ALTER TABLE notification_log RENAME TO notification_log_old;

CREATE TABLE notification_log (
    id           UUID        NOT NULL DEFAULT gen_random_uuid(),
    channel_id   UUID        REFERENCES notification_channels(id),
    rule_id      UUID        REFERENCES notification_rules(id),
    alert_id     UUID,
    incident_id  UUID,
    channel_type TEXT        NOT NULL,
    recipient    TEXT        NOT NULL,
    subject      TEXT,
    body         TEXT,
    status       TEXT        NOT NULL DEFAULT 'sent',
    error_msg    TEXT,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, sent_at)
) PARTITION BY RANGE (sent_at);

DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series('2024-01-01'::date, '2027-12-01'::date, '1 month'::interval)
    LOOP
        PERFORM create_monthly_partition('notification_log', d);
    END LOOP;
END $$;

INSERT INTO notification_log
    SELECT id, channel_id, rule_id, alert_id, incident_id,
           channel_type, recipient, subject, body,
           COALESCE(status,'sent'), error_message, sent_at
    FROM notification_log_old;

COMMIT;
```

```sql
BEGIN;

-- 4d: incident_timeline
ALTER TABLE incident_timeline RENAME TO incident_timeline_old;

CREATE TABLE incident_timeline (
    id          UUID        NOT NULL DEFAULT gen_random_uuid(),
    incident_id UUID        NOT NULL,
    event_type  TEXT        NOT NULL,
    description TEXT        NOT NULL,
    user_id     UUID,
    metadata    JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

DO $$
DECLARE d DATE;
BEGIN
    FOR d IN SELECT generate_series('2024-01-01'::date, '2027-12-01'::date, '1 month'::interval)
    LOOP
        PERFORM create_monthly_partition('incident_timeline', d);
    END LOOP;
END $$;

INSERT INTO incident_timeline
    SELECT id, incident_id, event_type, description,
           user_id, COALESCE(metadata,'{}'), created_at
    FROM incident_timeline_old;

COMMIT;
```

---

## Phase 5 — Migrate Correlation Data to Unified Table

This is the most critical phase. Data from 4 tables flows into `correlation_results`.

```sql
BEGIN;

-- From alert_correlations (rule-based and general)
INSERT INTO correlation_results
    (id, alert_id, correlation_id, correlation_type,
     confidence_score, is_duplicate, duplicate_of,
     group_id, incident_id, metadata, created_at)
SELECT
    id,
    alert_id,
    correlation_id,
    COALESCE(engine_name, 'rule_based'),
    confidence_score,
    COALESCE(is_duplicate, false),
    duplicate_of,
    group_id,
    incident_id,
    COALESCE(metadata, '{}'),
    created_at
FROM alert_correlations
ON CONFLICT DO NOTHING;

-- From ai_correlation_results
INSERT INTO correlation_results
    (alert_id, correlation_id, correlation_type,
     confidence_score, is_duplicate, duplicate_of,
     engine_name, metadata, created_at)
SELECT
    alert_id,
    correlation_id,
    'ai_semantic',
    confidence_score,
    false,
    NULL,
    'ai_correlation_engine',
    jsonb_build_object(
        'model', model_used,
        'reasoning', reasoning,
        'similar_alerts', similar_alerts
    ),
    created_at
FROM ai_correlation_results
ON CONFLICT DO NOTHING;

-- From davis_ai_correlations
INSERT INTO correlation_results
    (alert_id, correlation_id, correlation_type,
     confidence_score, is_duplicate, cluster_key,
     engine_name, metadata, created_at)
SELECT
    alert_id,
    problem_id::UUID,
    'davis_ai',
    confidence,
    false,
    problem_id,
    'davis_ai',
    jsonb_build_object(
        'problem_title',   problem_title,
        'impact_level',    impact_level,
        'root_cause_id',   root_cause_entity_id,
        'management_zone', management_zone
    ),
    created_at
FROM davis_ai_correlations
ON CONFLICT DO NOTHING;

-- From pipeline_correlation_results
INSERT INTO correlation_results
    (alert_id, correlation_id, correlation_type,
     confidence_score, is_duplicate, duplicate_of,
     engine_name, strategy_weights, metadata, created_at)
SELECT
    alert_id,
    correlation_id,
    'pipeline',
    final_confidence,
    COALESCE(is_duplicate, false),
    duplicate_of,
    'parallel_pipeline',
    COALESCE(strategy_scores, '{}'),
    COALESCE(metadata, '{}'),
    created_at
FROM pipeline_correlation_results
ON CONFLICT DO NOTHING;

COMMIT;

-- Verify counts match
SELECT
    (SELECT COUNT(*) FROM alert_correlations)         AS src_rule_based,
    (SELECT COUNT(*) FROM ai_correlation_results)     AS src_ai,
    (SELECT COUNT(*) FROM davis_ai_correlations)      AS src_davis,
    (SELECT COUNT(*) FROM pipeline_correlation_results) AS src_pipeline,
    (SELECT COUNT(*) FROM correlation_results)        AS dst_total;
```

---

## Phase 6 — Create Backward-Compatible View

The Go application queries `alert_correlations` by name. The view makes zero application changes necessary.

```sql
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
```

---

## Phase 7 — Indexes

Apply all indexes from `schema_v2.sql`. Run `CONCURRENTLY` so they do not block reads/writes during creation.

```sql
-- Core query patterns (run each as a separate statement outside a transaction block)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_status_sev_created
    ON alerts(status, severity, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_fingerprint
    ON alerts(fingerprint) WHERE fingerprint IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_active
    ON alerts(created_at DESC, severity)
    WHERE status IN ('open','acknowledged');

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_title_trgm
    ON alerts USING GIN(title gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_corr_results_alert_id
    ON correlation_results(alert_id, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_corr_results_correlation_id
    ON correlation_results(correlation_id) WHERE correlation_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_active
    ON incidents(created_at DESC, severity)
    WHERE status NOT IN ('resolved','closed');

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_user
    ON audit_logs(user_id, created_at DESC);

-- (Full list in index_strategy.md)
```

---

## Phase 8 — Constraints and Foreign Keys

After data is migrated, add FK constraints that could not be added during schema creation.

```sql
BEGIN;

-- FK: teams.manager_id → users.id
ALTER TABLE teams
    ADD CONSTRAINT fk_teams_manager
    FOREIGN KEY (manager_id) REFERENCES users(id);

-- FK: users.team_id → teams.id
ALTER TABLE users
    ADD CONSTRAINT fk_users_team
    FOREIGN KEY (team_id) REFERENCES teams(id);

-- alerts.incident_id FK (NOT VALID avoids full table scan on partitioned parent)
ALTER TABLE alerts
    ADD CONSTRAINT fk_alerts_incident
    FOREIGN KEY (incident_id) REFERENCES incidents(id)
    NOT VALID;

-- Validate in a separate step when load allows
-- ALTER TABLE alerts VALIDATE CONSTRAINT fk_alerts_incident;

COMMIT;
```

---

## Phase 9 — Functions and Triggers

Copy `schema_v2.sql` into the cluster then apply it (triggers and functions only):

```bash
# Copy schema file into the postgres pod
PGPOD=$(kubectl get pod -n aileron -l app=postgres \
        -o jsonpath='{.items[0].metadata.name}')

kubectl cp \
  ./database/schema_v2.sql \
  "aileron/$PGPOD:/tmp/schema_v2.sql"

# Apply only the function/trigger section (lines 1444–1565 in schema_v2.sql)
kubectl exec -n aileron "$PGPOD" -- \
  bash -c "PGPASSWORD=\$(cat /run/secrets/postgres-password 2>/dev/null || echo \$POSTGRES_PASSWORD) \
  psql -U alerthub -d alerthub -f /tmp/schema_v2.sql 2>&1" | grep -E "CREATE|ERROR|WARN"
```

Or apply just the functions interactively in the open psql session:

```sql
-- Copy verbatim from schema_v2.sql lines 1444–1565:
-- update_updated_at_column(), alert_dedup_touch(),
-- sync_alert_correlation_fields(), sync_cluster_alert_count(),
-- ensure_next_month_partitions()
-- Then apply triggers for each table.
```

---

## Phase 10 — Verify and Bring Up Application

Run inside the psql session:

```sql
-- Verify critical row counts
SELECT 'alerts'           AS tbl, COUNT(*) FROM alerts
UNION ALL SELECT 'incidents',              COUNT(*) FROM incidents
UNION ALL SELECT 'correlation_results',    COUNT(*) FROM correlation_results
UNION ALL SELECT 'audit_logs',             COUNT(*) FROM audit_logs
UNION ALL SELECT 'users',                  COUNT(*) FROM users;

-- Verify backward-compat view works
SELECT COUNT(*) FROM alert_correlations;

-- Quick smoke test: latest 5 open alerts visible
SELECT id, title, severity, status, created_at
FROM alerts WHERE status = 'open'
ORDER BY created_at DESC LIMIT 5;
```

Then bring the application back up:

```bash
kubectl scale deployment alerthub-enterprise-main \
  --replicas=3 -n aileron

kubectl rollout status deployment/alerthub-enterprise-main \
  -n aileron --timeout=120s

# Watch logs for SQL errors
kubectl logs -n aileron \
  -l app=alerthub-enterprise,component=main-application \
  --tail=100 -f
```

---

## Phase 11 — Deferred Cleanup (After 7-Day Soak Period)

Only drop old tables after 7 days of clean production operation.

See `tables_to_delete.md` for the complete drop list.

```sql
-- Sample: drop after validation
DROP TABLE IF EXISTS alert_correlations_old CASCADE;
DROP TABLE IF EXISTS ai_correlation_results CASCADE;
DROP TABLE IF EXISTS davis_ai_correlations CASCADE;
DROP TABLE IF EXISTS pipeline_correlation_results CASCADE;
-- ... (full list in tables_to_delete.md)
```

---

## Rollback Plan

If any phase fails **before Phase 10** (application not yet brought up):

```bash
# Get the postgres pod name
PGPOD=$(kubectl get pod -n aileron -l app=postgres \
        -o jsonpath='{.items[0].metadata.name}')

# Copy backup back into pod and restore
kubectl cp "./backups/$BACKUP_FILE" \
  "aileron/$PGPOD:/tmp/$BACKUP_FILE"

kubectl exec -n aileron "$PGPOD" -- \
  bash -c "PGPASSWORD=\$POSTGRES_PASSWORD \
  pg_restore -U alerthub -d alerthub --clean --if-exists \
  /tmp/$BACKUP_FILE"

# Bring application back up after restore
kubectl scale deployment alerthub-enterprise-main \
  --replicas=3 -n aileron
```

If failure is discovered **after Phase 10** (application already running):

```bash
# 1. Scale down to stop writes
kubectl scale deployment alerthub-enterprise-main \
  --replicas=0 -n aileron
```

```sql
-- 2. Swap partitioned tables back to originals
-- (the _old tables are still present)
DROP TABLE alerts CASCADE;
ALTER TABLE alerts_old RENAME TO alerts;

DROP TABLE audit_logs CASCADE;
ALTER TABLE audit_logs_old RENAME TO audit_logs;

-- Drop the backward-compat view (old alert_correlations table is back)
DROP VIEW IF EXISTS alert_correlations;
```

```bash
# 3. Bring application back up
kubectl scale deployment alerthub-enterprise-main \
  --replicas=3 -n aileron
```

---

## Estimated Time per Phase

| Phase | Operation                            | Estimated Time   |
|-------|--------------------------------------|-----------------|
| 0     | Backup                               | 5–15 min         |
| 1     | Extensions + helper function         | < 1 min          |
| 2     | New tables                           | < 1 min          |
| 3     | ALTER TABLE (additive columns)       | 1–3 min          |
| 4a    | alerts partition + data copy         | 5–30 min (data dependent) |
| 4b–4d | Other partition conversions          | 2–10 min each    |
| 5     | Correlation data migration           | 2–15 min         |
| 6     | Backward-compat view                 | < 1 min          |
| 7     | Concurrent indexes                   | 10–60 min        |
| 8     | FK constraints                       | 1–3 min          |
| 9     | Triggers                             | < 1 min          |
| 10    | Verify + app startup                 | 5 min            |
| 11    | Cleanup (deferred)                   | Day +7           |

Total downtime window needed: **~30–90 minutes** depending on data volume.
