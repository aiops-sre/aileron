-- =============================================================================
-- AlertHub Enterprise — Domain-Driven Schema v2.0
-- PostgreSQL 14+  |  Production-Grade  |  Backward-Compatible
--
-- Domains:  platform · alerts · incidents · correlation · topology · config
--           workflow · observability
--
-- Design principles:
--   1. TIMESTAMPTZ everywhere (UTC stored, TZ-aware queries)
--   2. gen_random_uuid() (no uuid-ossp overhead)
--   3. Range partitioning on high-volume append tables
--   4. Partial + composite indexes for hot query paths
--   5. JSONB with default '{}' / '[]', not nullable
--   6. CHECK constraints instead of application-level enum guarding
--   7. Backward-compat views so existing Go code needs zero changes
-- =============================================================================

BEGIN;

-- ---------------------------------------------------------------------------
-- Extensions
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";   -- legacy compat
CREATE EXTENSION IF NOT EXISTS pg_trgm;        -- trigram search on title/desc
CREATE EXTENSION IF NOT EXISTS btree_gin;      -- multi-column GIN

-- Preferred UUID generator (no extension, faster)
-- Use gen_random_uuid() in this file, uuid_generate_v4() aliases kept for compat

-- =============================================================================
-- DOMAIN: platform
-- Users · Roles · Teams · On-Call · Escalation · Sessions
-- =============================================================================

-- ---------------------------------------------------------------------------
-- roles
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    is_system_role BOOLEAN  DEFAULT false,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_roles_name ON roles(name);

-- ---------------------------------------------------------------------------
-- permissions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS permissions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) UNIQUE NOT NULL,
    resource    VARCHAR(100) NOT NULL,
    action      VARCHAR(50)  NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_permissions_resource_action ON permissions(resource, action);

-- ---------------------------------------------------------------------------
-- role_permissions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS role_permissions (
    role_id       UUID REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID REFERENCES permissions(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id)
);

-- ---------------------------------------------------------------------------
-- teams
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS teams (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(255) UNIQUE NOT NULL,
    description             TEXT,
    team_type               VARCHAR(50)  DEFAULT 'engineering',
    parent_team_id          UUID REFERENCES teams(id) ON DELETE SET NULL,
    manager_id              UUID,        -- FK → users (set after users table)
    primary_services        JSONB        DEFAULT '[]',
    expertise_areas         JSONB        DEFAULT '[]',
    escalation_contacts     JSONB        DEFAULT '[]',
    sla_targets             JSONB        DEFAULT '{}',
    notification_channels   JSONB        DEFAULT '[]',
    working_hours           JSONB        DEFAULT '{}',
    is_active               BOOLEAN      DEFAULT true,
    metadata                JSONB        DEFAULT '{}',
    created_by              UUID,
    created_at              TIMESTAMPTZ  DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_teams_name        ON teams(name);
CREATE INDEX IF NOT EXISTS idx_teams_parent      ON teams(parent_team_id) WHERE parent_team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_teams_manager     ON teams(manager_id)     WHERE manager_id     IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_teams_active      ON teams(is_active)      WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- users
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    username               VARCHAR(255) UNIQUE NOT NULL,
    email                  VARCHAR(255) UNIQUE NOT NULL,
    password_hash          VARCHAR(255) NOT NULL DEFAULT '',
    full_name              VARCHAR(255),
    role_id                UUID         REFERENCES roles(id),
    team_id                UUID         REFERENCES teams(id) ON DELETE SET NULL,
    is_active              BOOLEAN      DEFAULT true,
    is_verified            BOOLEAN      DEFAULT false,
    mfa_enabled            BOOLEAN      DEFAULT false,
    mfa_secret             VARCHAR(255),
    last_login             TIMESTAMPTZ,
    login_count            INTEGER      DEFAULT 0,
    failed_login_attempts  INTEGER      DEFAULT 0,
    locked_until           TIMESTAMPTZ,
    avatar_url             TEXT,
    phone                  VARCHAR(50),
    timezone               VARCHAR(100) DEFAULT 'UTC',
    preferences            JSONB        DEFAULT '{}',
    -- OAuth / SSO
    oauth_source           VARCHAR(100),        -- 'mas'|'saml'|'ldap'|'local'
    oauth_subject          VARCHAR(500),        -- external user identifier
    vpn_ip                 VARCHAR(45),
    created_at             TIMESTAMPTZ  DEFAULT NOW(),
    updated_at             TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_users_email       ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_username    ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_role_id     ON users(role_id);
CREATE INDEX IF NOT EXISTS idx_users_team_id     ON users(team_id)    WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_active      ON users(is_active)  WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_users_oauth       ON users(oauth_source, oauth_subject)
    WHERE oauth_source IS NOT NULL;

-- Wire deferred FKs on teams now that users exists
ALTER TABLE teams ADD CONSTRAINT fk_teams_manager
    FOREIGN KEY (manager_id) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE teams ADD CONSTRAINT fk_teams_created_by
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- user_sessions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_sessions (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash          VARCHAR(64)  NOT NULL,    -- SHA-256 hex (64 chars)
    refresh_token_hash  VARCHAR(64),
    ip_address          INET,
    user_agent          TEXT,
    expires_at          TIMESTAMPTZ  NOT NULL,
    created_at          TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id    ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_token      ON user_sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires    ON user_sessions(expires_at);
-- Partial: only non-expired sessions
CREATE INDEX IF NOT EXISTS idx_user_sessions_active
    ON user_sessions(user_id, expires_at)
    WHERE expires_at > NOW();

-- ---------------------------------------------------------------------------
-- user_expertise
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_expertise (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expertise_type           VARCHAR(50) NOT NULL DEFAULT 'technical',
    expertise_area           VARCHAR(255) NOT NULL,
    expertise_level          VARCHAR(20)  DEFAULT 'intermediate'
                             CHECK (expertise_level IN ('beginner','intermediate','expert','lead')),
    technologies             JSONB        DEFAULT '[]',
    services                 JSONB        DEFAULT '[]',
    preferred_alert_types    JSONB        DEFAULT '[]',
    avg_resolution_time_mins INTEGER      DEFAULT 0,
    resolution_count         INTEGER      DEFAULT 0,
    success_rate             DECIMAL(5,4) DEFAULT 0.0,
    max_concurrent_alerts    INTEGER      DEFAULT 5,
    availability_hours       JSONB        DEFAULT '{}',
    auto_detected            BOOLEAN      DEFAULT false,
    last_activity_date       TIMESTAMPTZ,
    created_at               TIMESTAMPTZ  DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_expertise_user_id ON user_expertise(user_id);
CREATE INDEX IF NOT EXISTS idx_user_expertise_area    ON user_expertise(expertise_area);

-- ---------------------------------------------------------------------------
-- escalation_policies
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS escalation_policies (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    rules       JSONB        NOT NULL DEFAULT '[]',
    is_active   BOOLEAN      DEFAULT true,
    created_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ  DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- oncall_schedules
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS oncall_schedules (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  VARCHAR(255) UNIQUE NOT NULL,
    description           TEXT,
    team_id               UUID         REFERENCES teams(id) ON DELETE SET NULL,
    timezone              VARCHAR(100) DEFAULT 'UTC',
    rotation_type         VARCHAR(50)  DEFAULT 'weekly',
    rotation_config       JSONB        DEFAULT '{}',
    effective_start_date  TIMESTAMPTZ,
    effective_end_date    TIMESTAMPTZ,
    alert_routing_rules   JSONB        DEFAULT '[]',
    escalation_policy_id  UUID         REFERENCES escalation_policies(id) ON DELETE SET NULL,
    is_active             BOOLEAN      DEFAULT true,
    approved_by           UUID         REFERENCES users(id) ON DELETE SET NULL,
    approved_at           TIMESTAMPTZ,
    created_by            UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ  DEFAULT NOW(),
    updated_at            TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_oncall_schedules_team   ON oncall_schedules(team_id)   WHERE team_id   IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_oncall_schedules_active ON oncall_schedules(is_active) WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- oncall_shifts  (was oncall_shifts + oncall_schedule_entries, merged)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS oncall_shifts (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id            UUID         NOT NULL REFERENCES oncall_schedules(id) ON DELETE CASCADE,
    user_id                UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_time             TIMESTAMPTZ  NOT NULL,
    end_time               TIMESTAMPTZ  NOT NULL,
    is_override            BOOLEAN      DEFAULT false,
    is_primary             BOOLEAN      DEFAULT true,
    escalation_level       INTEGER      DEFAULT 1,
    contact_methods        JSONB        DEFAULT '[]',
    coverage_areas         JSONB        DEFAULT '[]',
    alerts_received        INTEGER      DEFAULT 0,
    alerts_resolved        INTEGER      DEFAULT 0,
    avg_response_time_mins INTEGER      DEFAULT 0,
    handoff_notes          TEXT,
    status                 VARCHAR(20)  DEFAULT 'scheduled'
                           CHECK (status IN ('scheduled','active','completed','cancelled')),
    metadata               JSONB        DEFAULT '{}',
    created_at             TIMESTAMPTZ  DEFAULT NOW(),
    CONSTRAINT chk_shift_times CHECK (end_time > start_time)
);
CREATE INDEX IF NOT EXISTS idx_oncall_shifts_schedule  ON oncall_shifts(schedule_id);
CREATE INDEX IF NOT EXISTS idx_oncall_shifts_user      ON oncall_shifts(user_id);
CREATE INDEX IF NOT EXISTS idx_oncall_shifts_time      ON oncall_shifts(start_time, end_time);
-- For "who is on-call right now?"
CREATE INDEX IF NOT EXISTS idx_oncall_shifts_current
    ON oncall_shifts(start_time, end_time, user_id)
    WHERE status = 'active';

-- =============================================================================
-- DOMAIN: alerts   (PARTITIONED — high ingestion path)
-- =============================================================================

-- ---------------------------------------------------------------------------
-- alerts  — partitioned monthly by created_at
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alerts (
    id                      UUID         NOT NULL DEFAULT gen_random_uuid(),
    title                   VARCHAR(500) NOT NULL,
    description             TEXT,
    severity                VARCHAR(20)  NOT NULL
                            CHECK (severity IN ('critical','high','medium','low','info')),
    status                  VARCHAR(30)  NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open','acknowledged','investigating',
                                              'resolved','closed','suppressed')),

    -- Source
    source                  VARCHAR(100),
    source_id               VARCHAR(255),
    source_url              TEXT,

    -- Classification
    tags                    JSONB        NOT NULL DEFAULT '[]',
    labels                  JSONB        NOT NULL DEFAULT '{}',
    metadata                JSONB        NOT NULL DEFAULT '{}',
    fingerprint             VARCHAR(255),

    -- Deduplication / occurrence tracking
    count                   INTEGER      NOT NULL DEFAULT 1,
    first_seen_at           TIMESTAMPTZ  DEFAULT NOW(),
    last_seen_at            TIMESTAMPTZ  DEFAULT NOW(),

    -- Ownership
    assigned_to             UUID         REFERENCES users(id) ON DELETE SET NULL,
    team_id                 UUID         REFERENCES teams(id) ON DELETE SET NULL,
    created_by              UUID         REFERENCES users(id) ON DELETE SET NULL,

    -- Lifecycle timestamps
    acknowledged_at         TIMESTAMPTZ,
    acknowledged_by         UUID         REFERENCES users(id) ON DELETE SET NULL,
    resolved_at             TIMESTAMPTZ,
    resolved_by             UUID         REFERENCES users(id) ON DELETE SET NULL,
    resolution_notes        TEXT,
    resolution_type         VARCHAR(50),  -- 'manual'|'auto'|'suppressed'

    -- SLA
    sla_met_responsetime    BOOLEAN      DEFAULT false,
    sla_met_resolutiontime  BOOLEAN      DEFAULT false,

    -- Maintenance
    is_alert_active         BOOLEAN      DEFAULT true,
    maintenance_status      INTEGER      DEFAULT 0,

    -- Linked data (denormalized for fast list queries)
    linked_alerts           JSONB        DEFAULT '[]',
    linked_to               JSONB        DEFAULT '{}',
    incident_id             UUID,        -- FK → incidents (added after incidents table)

    -- Correlation (denormalized for fast list queries — authoritative in correlation_results)
    correlation_id          VARCHAR(255),
    correlation_confidence  DECIMAL(5,4) DEFAULT 0.0,

    -- AI / AIOps
    ai_classification       VARCHAR(100),
    ai_confidence           DECIMAL(5,4) DEFAULT 0.0,
    ai_analysis             JSONB,
    autonomous_analysis     JSONB,
    rca_confidence          DECIMAL(5,4) DEFAULT 0.0,
    root_cause_entity       VARCHAR(255),
    blast_radius_score      DECIMAL(5,4) DEFAULT 0.0,
    agent_processed         BOOLEAN      DEFAULT false,
    agent_decision          VARCHAR(100),

    -- Infrastructure context
    entity_type             VARCHAR(50),
    entity_id               VARCHAR(255),
    region                  VARCHAR(50),
    topology_path           VARCHAR(500),
    topology_context        JSONB,
    root_cause              JSONB,

    -- Dynatrace-specific (backward compat)
    dynatrace_entity_id     VARCHAR(255),
    management_zone         VARCHAR(255),

    created_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Partition creation function (call monthly or pre-create)
-- Current + rolling 12-month partitions created below in seed section

-- Indexes on parent table (inherited by partitions)
CREATE INDEX IF NOT EXISTS idx_alerts_status_sev_created
    ON alerts(status, severity, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_source_sourceid
    ON alerts(source, source_id) WHERE source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_fingerprint
    ON alerts(fingerprint)       WHERE fingerprint IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_assigned_to
    ON alerts(assigned_to)       WHERE assigned_to IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_team_id
    ON alerts(team_id)           WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_incident_id
    ON alerts(incident_id)       WHERE incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_correlation_id
    ON alerts(correlation_id)    WHERE correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_entity
    ON alerts(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_alerts_region
    ON alerts(region)            WHERE region IS NOT NULL;
-- GIN for JSONB filter on tags/labels
CREATE INDEX IF NOT EXISTS idx_alerts_tags
    ON alerts USING GIN(tags);
CREATE INDEX IF NOT EXISTS idx_alerts_labels
    ON alerts USING GIN(labels);
-- Partial: only active/open alerts (hot path)
CREATE INDEX IF NOT EXISTS idx_alerts_active
    ON alerts(created_at DESC, severity)
    WHERE status IN ('open','acknowledged','investigating');
-- Trigram on title for search
CREATE INDEX IF NOT EXISTS idx_alerts_title_trgm
    ON alerts USING GIN(title gin_trgm_ops);

-- ---------------------------------------------------------------------------
-- alert_history — partitioned monthly
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_history (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id   UUID         NOT NULL,      -- no FK on partition key workaround
    user_id    UUID         REFERENCES users(id) ON DELETE SET NULL,
    action     VARCHAR(100) NOT NULL,
    old_value  JSONB,
    new_value  JSONB,
    comment    TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_alert_history_alert_id   ON alert_history(alert_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alert_history_created_at ON alert_history(created_at DESC);

-- ---------------------------------------------------------------------------
-- alert_comments
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_comments (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id   UUID         NOT NULL,
    user_id    UUID         REFERENCES users(id) ON DELETE SET NULL,
    username   VARCHAR(255),
    comment    TEXT         NOT NULL
                            CHECK (length(comment) BETWEEN 1 AND 10000),
    created_at TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_alert_comments_alert_id   ON alert_comments(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_comments_created_at ON alert_comments(created_at DESC);

-- ---------------------------------------------------------------------------
-- maintenance_windows
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS maintenance_windows (
    id                 UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id           UUID,        -- nullable: window can pre-date alert
    alertname          TEXT,
    start_time         TIMESTAMPTZ  NOT NULL,
    end_time           TIMESTAMPTZ,
    is_active          BOOLEAN      GENERATED ALWAYS AS (
                           end_time IS NULL OR end_time > NOW()
                       ) STORED,
    maintenance_status INTEGER      DEFAULT 1,
    comment            TEXT,
    created_by         UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ  DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  DEFAULT NOW(),
    CONSTRAINT chk_mw_times CHECK (end_time IS NULL OR end_time > start_time)
);
CREATE INDEX IF NOT EXISTS idx_mw_alert_id  ON maintenance_windows(alert_id)  WHERE alert_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_mw_active    ON maintenance_windows(start_time, end_time) WHERE end_time IS NULL OR end_time > NOW();

-- ---------------------------------------------------------------------------
-- alert_assignments
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_assignments (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id            UUID         NOT NULL,
    user_id             UUID         REFERENCES users(id) ON DELETE SET NULL,
    team_id             UUID         REFERENCES teams(id) ON DELETE SET NULL,
    assignment_type     VARCHAR(50)  DEFAULT 'manual'
                        CHECK (assignment_type IN ('manual','auto','escalation','ai_recommended')),
    assignment_reason   TEXT,
    assignment_confidence DECIMAL(5,4) DEFAULT 0.0,
    assigned_by         UUID         REFERENCES users(id) ON DELETE SET NULL,
    assignment_method   VARCHAR(50),
    assigned_at         TIMESTAMPTZ  DEFAULT NOW(),
    accepted_at         TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    status              VARCHAR(20)  DEFAULT 'assigned'
                        CHECK (status IN ('assigned','accepted','declined','completed','reassigned')),
    notes               TEXT,
    metadata            JSONB        DEFAULT '{}',
    created_at          TIMESTAMPTZ  DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_alert_assignments_alert  ON alert_assignments(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_assignments_user   ON alert_assignments(user_id)  WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_assignments_team   ON alert_assignments(team_id)  WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_assignments_active
    ON alert_assignments(alert_id, status)
    WHERE status IN ('assigned','accepted');

-- =============================================================================
-- DOMAIN: incidents
-- =============================================================================

CREATE TABLE IF NOT EXISTS incidents (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_number        BIGINT       GENERATED ALWAYS AS IDENTITY UNIQUE,
    title                  VARCHAR(500) NOT NULL,
    description            TEXT,
    severity               VARCHAR(20)  NOT NULL
                           CHECK (severity IN ('critical','high','medium','low')),
    status                 VARCHAR(30)  NOT NULL DEFAULT 'open'
                           CHECK (status IN ('open','investigating','identified',
                                             'monitoring','resolved','closed')),
    priority               VARCHAR(10)  CHECK (priority IN ('p1','p2','p3','p4')),
    impact                 VARCHAR(50),
    urgency                VARCHAR(50),

    -- Ownership
    assigned_to            UUID         REFERENCES users(id) ON DELETE SET NULL,
    team_id                UUID         REFERENCES teams(id) ON DELETE SET NULL,
    created_by             UUID         REFERENCES users(id) ON DELETE SET NULL,

    -- Linked alerts (array for fast incident→alerts lookup)
    alert_ids              UUID[]       DEFAULT '{}',
    related_incident_ids   UUID[]       DEFAULT '{}',

    -- Correlation metadata
    correlation_id         VARCHAR(255),
    auto_created           BOOLEAN      DEFAULT false,
    source                 VARCHAR(100) DEFAULT 'manual',
    correlation_type       VARCHAR(100),
    correlation_confidence DECIMAL(5,4) DEFAULT 0.0,
    auto_creation_metadata JSONB        DEFAULT '{}',

    -- AI
    ai_root_cause          TEXT,
    ai_insights            JSONB,
    ai_recommendations     JSONB,

    -- Post-mortem
    resolution_notes       TEXT,
    post_mortem_url        TEXT,

    -- Lifecycle
    started_at             TIMESTAMPTZ  DEFAULT NOW(),
    detected_at            TIMESTAMPTZ,
    acknowledged_at        TIMESTAMPTZ,
    resolved_at            TIMESTAMPTZ,
    closed_at              TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_incidents_status_sev  ON incidents(status, severity, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_assigned    ON incidents(assigned_to)   WHERE assigned_to IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_team        ON incidents(team_id)       WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_number      ON incidents(incident_number);
CREATE INDEX IF NOT EXISTS idx_incidents_correlation ON incidents(correlation_id) WHERE correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_created_at  ON incidents(created_at DESC);
-- GIN for alert_ids array lookups
CREATE INDEX IF NOT EXISTS idx_incidents_alert_ids   ON incidents USING GIN(alert_ids);
-- Partial: active incidents
CREATE INDEX IF NOT EXISTS idx_incidents_active
    ON incidents(created_at DESC, severity)
    WHERE status IN ('open','investigating','identified','monitoring');

-- Wire the deferred FK from alerts to incidents
ALTER TABLE alerts ADD CONSTRAINT fk_alerts_incident
    FOREIGN KEY (incident_id) REFERENCES incidents(id) ON DELETE SET NULL
    NOT VALID;   -- non-blocking, validate separately

-- ---------------------------------------------------------------------------
-- incident_timeline — partitioned monthly
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS incident_timeline (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID         NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    user_id     UUID         REFERENCES users(id) ON DELETE SET NULL,
    event_type  VARCHAR(100) NOT NULL,
    title       VARCHAR(500),
    description TEXT,
    metadata    JSONB        DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_incident_timeline_incident ON incident_timeline(incident_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_incident_timeline_created  ON incident_timeline(created_at DESC);

-- ---------------------------------------------------------------------------
-- incident_history  (audit trail for incident field changes)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS incident_history (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID         NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    user_id     UUID         REFERENCES users(id) ON DELETE SET NULL,
    action      VARCHAR(100) NOT NULL,
    old_value   JSONB,
    new_value   JSONB,
    comment     TEXT,
    created_at  TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_incident_history_incident ON incident_history(incident_id, created_at DESC);

-- =============================================================================
-- DOMAIN: correlation
--
-- Unified  correlation_results  replaces:
--   • alert_correlations
--   • ai_correlation_results
--   • davis_ai_correlations
--   • pipeline_correlation_results
--   • ai_correlation_feedback (merged as feedback columns)
-- =============================================================================

-- ---------------------------------------------------------------------------
-- correlation_clusters  (logical group — referenced by correlation_results)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS correlation_clusters (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_key     VARCHAR(255) UNIQUE NOT NULL,   -- stable external ID / fingerprint
    name            VARCHAR(255),
    description     TEXT,
    cluster_type    VARCHAR(50)  DEFAULT 'similarity'
                    CHECK (cluster_type IN ('similarity','temporal','topology',
                                            'semantic','rule_based','burst','storm')),
    status          VARCHAR(20)  DEFAULT 'active'
                    CHECK (status IN ('active','merged','resolved','suppressed')),
    alert_ids       UUID[]       DEFAULT '{}',      -- denormalized fast lookup
    root_alert_id   UUID,        -- FK → alerts wired below
    incident_id     UUID         REFERENCES incidents(id) ON DELETE SET NULL,
    confidence_score DECIMAL(5,4) DEFAULT 0.0,
    alert_count     INTEGER      DEFAULT 0,
    metadata        JSONB        DEFAULT '{}',
    created_at      TIMESTAMPTZ  DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_corr_clusters_key        ON correlation_clusters(cluster_key);
CREATE INDEX IF NOT EXISTS idx_corr_clusters_status     ON correlation_clusters(status) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_corr_clusters_incident   ON correlation_clusters(incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_corr_clusters_alert_ids  ON correlation_clusters USING GIN(alert_ids);

-- ---------------------------------------------------------------------------
-- correlation_results  — UNIFIED single correlation table  (partitioned monthly)
-- Replaces: alert_correlations + ai_correlation_results + davis_ai_correlations
--           + pipeline_correlation_results
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS correlation_results (
    id                  UUID         NOT NULL DEFAULT gen_random_uuid(),

    -- What was correlated
    alert_id            UUID         NOT NULL,   -- logical FK → alerts
    cluster_id          UUID         REFERENCES correlation_clusters(id) ON DELETE SET NULL,
    incident_id         UUID         REFERENCES incidents(id)            ON DELETE SET NULL,

    -- Correlation identity
    correlation_group_id VARCHAR(255) NOT NULL,   -- stable group key (= cluster_key in most cases)

    -- Strategy
    correlation_type    VARCHAR(50)  NOT NULL DEFAULT 'ml_similarity'
                        CHECK (correlation_type IN (
                            'ml_similarity','temporal','topology','semantic',
                            'davis','rule_based','burst','duplicate'
                        )),
    dominant_strategy   VARCHAR(50),
    strategy_scores     JSONB        NOT NULL DEFAULT '{}',
    -- e.g. {"ml": 0.92, "topology": 0.85, "temporal": 0.70, "semantic": 0.88}

    -- Result
    overall_confidence  DECIMAL(5,4) NOT NULL DEFAULT 0.0,
    recommended_action  VARCHAR(50)  NOT NULL DEFAULT 'correlate'
                        CHECK (recommended_action IN (
                            'correlate','create_incident','merge','escalate',
                            'suppress','investigate','ignore'
                        )),

    -- Duplicate detection
    is_duplicate        BOOLEAN      DEFAULT false,
    duplicate_of        UUID,        -- logical FK → alerts

    -- Similar alerts
    similar_alert_ids   UUID[]       DEFAULT '{}',

    -- AI enrichment
    ai_reasoning        TEXT,
    ai_enrichment       JSONB        DEFAULT '{}',

    -- Processing metadata
    processing_time_ms  INTEGER      DEFAULT 0,
    engine_version      VARCHAR(50),
    engine_name         VARCHAR(50)  DEFAULT 'parallel',

    -- Feedback loop
    feedback_correct    BOOLEAN,
    feedback_notes      TEXT,
    feedback_by         UUID         REFERENCES users(id) ON DELETE SET NULL,
    feedback_at         TIMESTAMPTZ,

    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_corrres_alert_id      ON correlation_results(alert_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_corrres_group_id      ON correlation_results(correlation_group_id);
CREATE INDEX IF NOT EXISTS idx_corrres_cluster_id    ON correlation_results(cluster_id)   WHERE cluster_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_corrres_incident_id   ON correlation_results(incident_id)  WHERE incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_corrres_type          ON correlation_results(correlation_type, overall_confidence DESC);
CREATE INDEX IF NOT EXISTS idx_corrres_created_at    ON correlation_results(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_corrres_duplicate     ON correlation_results(duplicate_of) WHERE is_duplicate = true;
-- GIN on similar_alert_ids for array containment queries
CREATE INDEX IF NOT EXISTS idx_corrres_similar       ON correlation_results USING GIN(similar_alert_ids);
-- High-confidence correlations (most queried)
CREATE INDEX IF NOT EXISTS idx_corrres_high_conf
    ON correlation_results(alert_id, overall_confidence DESC)
    WHERE overall_confidence >= 0.7;

-- ---------------------------------------------------------------------------
-- correlation_rules  (unified — merges correlation_rules + enhanced_correlation_rules)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS correlation_rules (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  VARCHAR(255) UNIQUE NOT NULL,
    description           TEXT,
    rule_type             VARCHAR(50)  DEFAULT 'standard'
                          CHECK (rule_type IN ('standard','ml','topology','temporal','composite')),
    conditions            JSONB        NOT NULL DEFAULT '[]',
    actions               JSONB        NOT NULL DEFAULT '[]',
    ml_model_config       JSONB        DEFAULT '{}',
    infrastructure_mapping JSONB       DEFAULT '{}',
    priority              INTEGER      DEFAULT 0,
    confidence_threshold  DECIMAL(5,4) DEFAULT 0.7,
    enabled               BOOLEAN      DEFAULT true,
    auto_learning         BOOLEAN      DEFAULT false,
    -- Performance metrics
    performance_metrics   JSONB        DEFAULT '{}',
    success_rate          DECIMAL(5,4) DEFAULT 0.0,
    total_matches         INTEGER      DEFAULT 0,
    last_matched_at       TIMESTAMPTZ,
    last_trained_at       TIMESTAMPTZ,
    training_data_size    INTEGER      DEFAULT 0,
    metadata              JSONB        DEFAULT '{}',
    created_by            UUID         REFERENCES users(id) ON DELETE SET NULL,
    updated_by            UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ  DEFAULT NOW(),
    updated_at            TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_corrules_enabled   ON correlation_rules(enabled, priority DESC) WHERE enabled = true;
CREATE INDEX IF NOT EXISTS idx_corrules_type      ON correlation_rules(rule_type);

-- ---------------------------------------------------------------------------
-- alert_similarity_cache  (pairwise similarity for fast lookup)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_similarity_cache (
    id                   UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    alert1_id            UUID         NOT NULL,
    alert2_id            UUID         NOT NULL,
    title_similarity     DECIMAL(5,4) DEFAULT 0.0,
    description_similarity DECIMAL(5,4) DEFAULT 0.0,
    source_similarity    DECIMAL(5,4) DEFAULT 0.0,
    tag_similarity       DECIMAL(5,4) DEFAULT 0.0,
    label_similarity     DECIMAL(5,4) DEFAULT 0.0,
    time_similarity      DECIMAL(5,4) DEFAULT 0.0,
    topology_similarity  DECIMAL(5,4) DEFAULT 0.0,
    overall_similarity   DECIMAL(5,4) NOT NULL DEFAULT 0.0,
    calculated_at        TIMESTAMPTZ  DEFAULT NOW(),
    expires_at           TIMESTAMPTZ  DEFAULT NOW() + INTERVAL '2 hours',
    UNIQUE (alert1_id, alert2_id),
    CONSTRAINT chk_similarity_order CHECK (alert1_id < alert2_id)
);
CREATE INDEX IF NOT EXISTS idx_similarity_alert1     ON alert_similarity_cache(alert1_id, overall_similarity DESC);
CREATE INDEX IF NOT EXISTS idx_similarity_alert2     ON alert_similarity_cache(alert2_id, overall_similarity DESC);
CREATE INDEX IF NOT EXISTS idx_similarity_overall    ON alert_similarity_cache(overall_similarity DESC) WHERE overall_similarity >= 0.7;
CREATE INDEX IF NOT EXISTS idx_similarity_expires    ON alert_similarity_cache(expires_at);

-- ---------------------------------------------------------------------------
-- historical_patterns  (learned patterns for future correlation)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS historical_patterns (
    id                        UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern_key               VARCHAR(255) UNIQUE NOT NULL,
    pattern_type              VARCHAR(50)  NOT NULL,
    alert_source              VARCHAR(100),
    alert_title               TEXT,
    alert_description         TEXT,
    frequency                 INTEGER      DEFAULT 1,
    confidence                DECIMAL(5,4) DEFAULT 0.0,
    typical_resolution        TEXT,
    avg_resolution_time_secs  BIGINT       DEFAULT 0,
    last_seen                 TIMESTAMPTZ,
    metadata                  JSONB        DEFAULT '{}',
    created_at                TIMESTAMPTZ  DEFAULT NOW(),
    updated_at                TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_patterns_type     ON historical_patterns(pattern_type);
CREATE INDEX IF NOT EXISTS idx_patterns_source   ON historical_patterns(alert_source);
CREATE INDEX IF NOT EXISTS idx_patterns_freq     ON historical_patterns(frequency DESC, confidence DESC);

-- =============================================================================
-- DOMAIN: topology
-- PostgreSQL stores entity METADATA + sync config.
-- Relationships (edges) belong in Neo4j.
-- =============================================================================

-- ---------------------------------------------------------------------------
-- topology_entities  (replaces dynatrace_topology + service_catalog parts)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS topology_entities (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id     VARCHAR(255) NOT NULL,   -- DT entity ID, K8s UID, etc.
    source          VARCHAR(50)  NOT NULL,   -- 'dynatrace'|'kubernetes'|'cloudstack'|'manual'
    entity_type     VARCHAR(100) NOT NULL,   -- SERVICE, HOST, POD, PROCESS_GROUP, etc.
    display_name    VARCHAR(500),
    -- Contextual grouping
    management_zone VARCHAR(255),
    cluster_name    VARCHAR(255),
    namespace_name  VARCHAR(255),
    region          VARCHAR(100),
    environment     VARCHAR(50),
    -- Health
    health_status   VARCHAR(50)  DEFAULT 'unknown',
    -- Full topology data as ingested
    raw_data        JSONB        NOT NULL DEFAULT '{}',
    labels          JSONB        NOT NULL DEFAULT '{}',
    -- Sync tracking
    last_updated    TIMESTAMPTZ  DEFAULT NOW(),
    created_at      TIMESTAMPTZ  DEFAULT NOW(),
    UNIQUE (external_id, source)
);
CREATE INDEX IF NOT EXISTS idx_topo_entities_source   ON topology_entities(source, entity_type);
CREATE INDEX IF NOT EXISTS idx_topo_entities_extid    ON topology_entities(external_id);
CREATE INDEX IF NOT EXISTS idx_topo_entities_zone     ON topology_entities(management_zone) WHERE management_zone IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_topo_entities_cluster  ON topology_entities(cluster_name)    WHERE cluster_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_topo_entities_labels   ON topology_entities USING GIN(labels);

-- ---------------------------------------------------------------------------
-- topology_management_zones
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS topology_management_zones (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    zone_id      VARCHAR(255) UNIQUE NOT NULL,
    zone_name    VARCHAR(255) NOT NULL,
    source       VARCHAR(50)  NOT NULL DEFAULT 'dynatrace',
    description  TEXT,
    entity_count INTEGER      DEFAULT 0,
    metadata     JSONB        DEFAULT '{}',
    last_updated TIMESTAMPTZ  DEFAULT NOW(),
    created_at   TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_topo_zones_name ON topology_management_zones(zone_name);

-- ---------------------------------------------------------------------------
-- topology_sources  (config per source system)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS topology_sources (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   VARCHAR(255) UNIQUE NOT NULL,
    source_type            VARCHAR(50)  NOT NULL
                           CHECK (source_type IN ('dynatrace','kubernetes','cloudstack',
                                                   'netapp','manual','prometheus')),
    display_name           VARCHAR(255),
    -- Connection
    endpoint_url           VARCHAR(500),
    api_token_encrypted    TEXT,
    credentials_encrypted  JSONB        DEFAULT '{}',
    -- Sync
    sync_enabled           BOOLEAN      DEFAULT true,
    sync_interval_mins     INTEGER      DEFAULT 15,
    last_sync_at           TIMESTAMPTZ,
    last_sync_status       VARCHAR(20)  DEFAULT 'pending'
                           CHECK (last_sync_status IN ('pending','running','success','error')),
    last_sync_error        TEXT,
    entity_types_filter    TEXT[]       DEFAULT '{}',
    -- Extra config
    config                 JSONB        DEFAULT '{}',
    enabled                BOOLEAN      DEFAULT true,
    created_at             TIMESTAMPTZ  DEFAULT NOW(),
    updated_at             TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_topo_sources_type    ON topology_sources(source_type);
CREATE INDEX IF NOT EXISTS idx_topo_sources_enabled ON topology_sources(enabled) WHERE enabled = true;

-- ---------------------------------------------------------------------------
-- topology_sync_log  (replaces k8s_topology_snapshots + topology_snapshots)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS topology_sync_log (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id       UUID         NOT NULL REFERENCES topology_sources(id) ON DELETE CASCADE,
    status          VARCHAR(20)  NOT NULL,
    entities_synced INTEGER      DEFAULT 0,
    errors          INTEGER      DEFAULT 0,
    error_details   JSONB        DEFAULT '[]',
    duration_ms     INTEGER      DEFAULT 0,
    snapshot_ref    TEXT,        -- path/key if snapshot stored externally
    created_at      TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_topo_sync_source  ON topology_sync_log(source_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_topo_sync_status  ON topology_sync_log(status, created_at DESC);

-- =============================================================================
-- DOMAIN: config
-- Integrations · Sources · LLM · Infra · Webhooks · Notifications · Auth
-- =============================================================================

-- ---------------------------------------------------------------------------
-- infra_regions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS infra_regions (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(255) UNIQUE NOT NULL,
    display_name VARCHAR(255),
    location     VARCHAR(255),
    region_type  VARCHAR(100) DEFAULT 'datacenter',
    bm_count     INTEGER      DEFAULT 0,
    enabled      BOOLEAN      DEFAULT true,
    created_at   TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- k8s_cluster_configs  (UNIFIED — merges schema.sql + enhanced_topology_config.sql)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS k8s_cluster_configs (
    id                      UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Identity
    name                    VARCHAR(255) UNIQUE NOT NULL,
    display_name            VARCHAR(255),
    environment             VARCHAR(100),
    region                  VARCHAR(100),
    -- Connection
    api_server_url          VARCHAR(500) NOT NULL,
    service_account_token   TEXT,        -- stored encrypted in production
    ca_cert_data            TEXT,
    namespace               VARCHAR(255) DEFAULT 'default',
    -- Status
    enabled                 BOOLEAN      DEFAULT true,
    discovery_enabled       BOOLEAN      DEFAULT true,
    readonly_mode           BOOLEAN      DEFAULT true,
    -- Topology info
    version                 VARCHAR(50),
    node_count              INTEGER      DEFAULT 0,
    namespace_count         INTEGER      DEFAULT 0,
    pod_count               INTEGER      DEFAULT 0,
    -- Sync
    last_sync               TIMESTAMPTZ,
    sync_status             VARCHAR(20)  DEFAULT 'pending',
    sync_error              TEXT,
    labels                  JSONB        DEFAULT '{}',
    metadata                JSONB        DEFAULT '{}',
    created_at              TIMESTAMPTZ  DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_k8s_configs_enabled ON k8s_cluster_configs(enabled)     WHERE enabled = true;
CREATE INDEX IF NOT EXISTS idx_k8s_configs_env     ON k8s_cluster_configs(environment);
CREATE INDEX IF NOT EXISTS idx_k8s_configs_region  ON k8s_cluster_configs(region);

-- ---------------------------------------------------------------------------
-- cloudstack_configs  (UNIFIED)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cloudstack_configs (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    region_id        UUID         REFERENCES infra_regions(id) ON DELETE SET NULL,
    name             VARCHAR(255) UNIQUE NOT NULL,
    display_name     VARCHAR(255),
    api_url          VARCHAR(500) NOT NULL,
    api_key          TEXT         NOT NULL,
    secret_key       TEXT         NOT NULL,
    zone_id          VARCHAR(255),
    region           VARCHAR(100),
    environment      VARCHAR(100),
    enabled          BOOLEAN      DEFAULT true,
    discovery_enabled BOOLEAN     DEFAULT true,
    vm_count         INTEGER      DEFAULT 0,
    host_count       INTEGER      DEFAULT 0,
    last_sync        TIMESTAMPTZ,
    sync_status      VARCHAR(20)  DEFAULT 'pending',
    labels           JSONB        DEFAULT '{}',
    metadata         JSONB        DEFAULT '{}',
    created_at       TIMESTAMPTZ  DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- netapp_configs
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS netapp_configs (
    id                 UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    region_id          UUID         REFERENCES infra_regions(id) ON DELETE SET NULL,
    name               VARCHAR(255) UNIQUE NOT NULL,
    management_url     VARCHAR(500) NOT NULL,
    username           TEXT         NOT NULL,
    password           TEXT         NOT NULL,
    cluster_name       VARCHAR(255),
    enabled            BOOLEAN      DEFAULT true,
    last_sync          TIMESTAMPTZ,
    total_capacity_tb  FLOAT        DEFAULT 0,
    used_capacity_tb   FLOAT        DEFAULT 0,
    created_at         TIMESTAMPTZ  DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- dynatrace_config
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dynatrace_config (
    id                      UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(255) UNIQUE NOT NULL DEFAULT 'default',
    tenant_url              VARCHAR(500),
    api_token_encrypted     TEXT,
    environment_name        VARCHAR(255),
    sync_enabled            BOOLEAN      DEFAULT true,
    sync_interval_mins      INTEGER      DEFAULT 15,
    last_sync_time          TIMESTAMPTZ,
    last_sync_status        VARCHAR(20)  DEFAULT 'pending',
    entity_types_filter     TEXT[]       DEFAULT '{}',
    created_at              TIMESTAMPTZ  DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- alert_sources
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_sources (
    id                      UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(255) UNIQUE NOT NULL,
    source_type             VARCHAR(100) NOT NULL,
    display_name            VARCHAR(255),
    endpoint_url            VARCHAR(500),
    api_key                 TEXT,
    webhook_secret          TEXT,
    extra_config            JSONB        DEFAULT '{}',
    enabled                 BOOLEAN      DEFAULT true,
    polling_interval_secs   INTEGER      DEFAULT 60,
    last_poll_at            TIMESTAMPTZ,
    last_poll_status        VARCHAR(20)  DEFAULT 'pending',
    alerts_received_total   INTEGER      DEFAULT 0,
    created_at              TIMESTAMPTZ  DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_alert_sources_type    ON alert_sources(source_type);
CREATE INDEX IF NOT EXISTS idx_alert_sources_enabled ON alert_sources(enabled) WHERE enabled = true;

-- ---------------------------------------------------------------------------
-- integrations  (UNIFIED — merges schema.sql + integrations.sql)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS integrations (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    type            VARCHAR(100) NOT NULL,
    provider        VARCHAR(100),
    config          JSONB        NOT NULL DEFAULT '{}',
    credentials     JSONB        DEFAULT '{}',   -- store encrypted in app layer
    is_active       BOOLEAN      DEFAULT true,
    status          VARCHAR(50)  DEFAULT 'active',
    last_sync_at    TIMESTAMPTZ,
    sync_status     VARCHAR(50),
    sync_error      TEXT,
    created_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_integrations_type   ON integrations(type);
CREATE INDEX IF NOT EXISTS idx_integrations_active ON integrations(is_active) WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- auth_providers  (merges auth_providers + oauth2_providers)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS auth_providers (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) UNIQUE NOT NULL,
    type        VARCHAR(50)  NOT NULL
                CHECK (type IN ('oauth2','saml','ldap','oidc','mas')),
    provider    VARCHAR(100),
    enabled     BOOLEAN      DEFAULT true,
    config      JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- llm_configs  (merges llm_configs + ollama_config)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS llm_configs (
    id                      UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(255) NOT NULL,
    provider                VARCHAR(100) NOT NULL DEFAULT 'ollama',
    model_name              VARCHAR(255) NOT NULL,
    endpoint_url            VARCHAR(500) NOT NULL,
    api_key                 TEXT,
    max_tokens              INTEGER      DEFAULT 2048,
    temperature             FLOAT        DEFAULT 0.1,
    enabled                 BOOLEAN      DEFAULT true,
    use_for_rca             BOOLEAN      DEFAULT true,
    use_for_correlation     BOOLEAN      DEFAULT true,
    use_for_remediation     BOOLEAN      DEFAULT true,
    use_for_summarization   BOOLEAN      DEFAULT true,
    system_prompt           TEXT,
    last_health_check       TIMESTAMPTZ,
    health_status           VARCHAR(20)  DEFAULT 'unknown',
    created_at              TIMESTAMPTZ  DEFAULT NOW(),
    updated_at              TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- llm_feedback
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS llm_feedback (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id         UUID,
    prediction_type  VARCHAR(100),
    prediction_value TEXT,
    actual_value     TEXT,
    feedback_type    VARCHAR(50),
    user_id          UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_llm_feedback_alert ON llm_feedback(alert_id) WHERE alert_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- correlation_engine_config
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS correlation_engine_config (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    config_key   VARCHAR(255) UNIQUE NOT NULL,
    config_value JSONB        NOT NULL DEFAULT '{}',
    description  TEXT,
    updated_by   UUID         REFERENCES users(id) ON DELETE SET NULL,
    updated_at   TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- webhooks
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhooks (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    url         TEXT         NOT NULL,
    secret      VARCHAR(255),
    events      TEXT[]       DEFAULT '{}',
    headers     JSONB        DEFAULT '{}',
    is_active   BOOLEAN      DEFAULT true,
    created_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ  DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- webhook_deliveries  — partitioned monthly
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id      UUID         NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type      VARCHAR(100),
    payload         JSONB,
    response_status INTEGER,
    response_body   TEXT,
    error_message   TEXT,
    delivered_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id, delivered_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status  ON webhook_deliveries(response_status, delivered_at DESC);

-- ---------------------------------------------------------------------------
-- notification_channels
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notification_channels (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    type        VARCHAR(50)  NOT NULL
                CHECK (type IN ('email','slack','pagerduty','webhook','sms','teams','opsgenie')),
    config      JSONB        NOT NULL DEFAULT '{}',
    is_active   BOOLEAN      DEFAULT true,
    created_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ  DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notif_channels_type   ON notification_channels(type);
CREATE INDEX IF NOT EXISTS idx_notif_channels_active ON notification_channels(is_active) WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- notification_rules
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notification_rules (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    conditions  JSONB        NOT NULL DEFAULT '[]',
    channels    UUID[]       DEFAULT '{}',
    priority    INTEGER      DEFAULT 0,
    is_active   BOOLEAN      DEFAULT true,
    created_at  TIMESTAMPTZ  DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notif_rules_active ON notification_rules(is_active, priority DESC) WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- notification_log  — partitioned monthly
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notification_log (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id   UUID         REFERENCES notification_channels(id) ON DELETE SET NULL,
    alert_id     UUID,
    incident_id  UUID,
    recipient    VARCHAR(255),
    status       VARCHAR(50),
    error_message TEXT,
    sent_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (sent_at);

CREATE INDEX IF NOT EXISTS idx_notif_log_alert    ON notification_log(alert_id)   WHERE alert_id   IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notif_log_incident ON notification_log(incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notif_log_sent_at  ON notification_log(sent_at DESC);

-- =============================================================================
-- DOMAIN: workflow
-- =============================================================================

CREATE TABLE IF NOT EXISTS workflows (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(255) UNIQUE NOT NULL,
    description  TEXT,
    trigger_type VARCHAR(100) NOT NULL,
    trigger_config JSONB      NOT NULL DEFAULT '{}',
    steps        JSONB        NOT NULL DEFAULT '[]',
    is_active    BOOLEAN      DEFAULT true,
    version      INTEGER      DEFAULT 1,
    created_by   UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ  DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_workflows_trigger ON workflows(trigger_type, is_active);

CREATE TABLE IF NOT EXISTS workflow_executions (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id    UUID         NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    trigger_data   JSONB        DEFAULT '{}',
    status         VARCHAR(20)  DEFAULT 'pending'
                   CHECK (status IN ('pending','running','completed','failed','cancelled')),
    result         JSONB        DEFAULT '{}',
    error_message  TEXT,
    started_at     TIMESTAMPTZ  DEFAULT NOW(),
    completed_at   TIMESTAMPTZ,
    duration_ms    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_workflow_exec_workflow ON workflow_executions(workflow_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_exec_status   ON workflow_executions(status, started_at DESC);

CREATE TABLE IF NOT EXISTS workflow_execution_steps (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id     UUID         NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
    step_name        VARCHAR(255) NOT NULL,
    step_type        VARCHAR(100) NOT NULL,
    status           VARCHAR(20)  DEFAULT 'pending',
    input            JSONB        DEFAULT '{}',
    output           JSONB        DEFAULT '{}',
    error_message    TEXT,
    started_at       TIMESTAMPTZ  DEFAULT NOW(),
    completed_at     TIMESTAMPTZ,
    duration_ms      INTEGER
);
CREATE INDEX IF NOT EXISTS idx_wf_steps_execution ON workflow_execution_steps(execution_id);

CREATE TABLE IF NOT EXISTS runbooks (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(255) UNIQUE NOT NULL,
    description  TEXT,
    content      TEXT,
    format       VARCHAR(20)  DEFAULT 'markdown',
    tags         TEXT[]       DEFAULT '{}',
    alert_types  TEXT[]       DEFAULT '{}',
    is_active    BOOLEAN      DEFAULT true,
    version      INTEGER      DEFAULT 1,
    created_by   UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ  DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS runbook_executions (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    runbook_id    UUID         NOT NULL REFERENCES runbooks(id) ON DELETE CASCADE,
    alert_id      UUID,
    incident_id   UUID,
    triggered_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    status        VARCHAR(20)  DEFAULT 'running',
    steps_output  JSONB        DEFAULT '[]',
    started_at    TIMESTAMPTZ  DEFAULT NOW(),
    completed_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_runbook_exec_runbook  ON runbook_executions(runbook_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runbook_exec_alert    ON runbook_executions(alert_id)   WHERE alert_id   IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_runbook_exec_incident ON runbook_executions(incident_id) WHERE incident_id IS NOT NULL;

-- =============================================================================
-- DOMAIN: observability
-- SLO/SLI tracking — time-series data stays in Prometheus/TimescaleDB
-- =============================================================================

CREATE TABLE IF NOT EXISTS slo_definitions (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) UNIQUE NOT NULL,
    description         TEXT,
    service_name        VARCHAR(255),
    slo_type            VARCHAR(50)  DEFAULT 'availability',
    target_percentage   DECIMAL(6,4) NOT NULL,   -- e.g. 99.9
    measurement_window  VARCHAR(20)  DEFAULT '30d',
    error_budget_policy JSONB        DEFAULT '{}',
    is_active           BOOLEAN      DEFAULT true,
    created_by          UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ  DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS slo_violations (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    slo_id           UUID         NOT NULL REFERENCES slo_definitions(id) ON DELETE CASCADE,
    alert_id         UUID,
    incident_id      UUID,
    violation_type   VARCHAR(50),
    severity         VARCHAR(20),
    error_budget_burn DECIMAL(8,4),
    details          JSONB        DEFAULT '{}',
    started_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    resolved_at      TIMESTAMPTZ,
    duration_secs    INTEGER      GENERATED ALWAYS AS (
                         EXTRACT(EPOCH FROM (COALESCE(resolved_at, NOW()) - started_at))::INTEGER
                     ) STORED
);
CREATE INDEX IF NOT EXISTS idx_slo_violations_slo        ON slo_violations(slo_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_slo_violations_unresolved ON slo_violations(slo_id) WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS service_health_checks (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    service_name  VARCHAR(255) NOT NULL,
    check_type    VARCHAR(50)  DEFAULT 'http',
    endpoint_url  VARCHAR(500),
    is_healthy    BOOLEAN      DEFAULT true,
    response_time_ms INTEGER   DEFAULT 0,
    status_code   INTEGER,
    error_message TEXT,
    checked_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (checked_at);
CREATE INDEX IF NOT EXISTS idx_svc_health_service ON service_health_checks(service_name, checked_at DESC);

-- =============================================================================
-- DOMAIN: platform.audit
-- =============================================================================

CREATE TABLE IF NOT EXISTS audit_logs (
    id            UUID         NOT NULL DEFAULT gen_random_uuid(),
    user_id       UUID         REFERENCES users(id) ON DELETE SET NULL,
    action        VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100),
    resource_id   UUID,
    old_value     JSONB,
    new_value     JSONB,
    details       JSONB        DEFAULT '{}',
    ip_address    INET,
    user_agent    TEXT,
    status        VARCHAR(20)  DEFAULT 'success',
    error_message TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_audit_user     ON audit_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action   ON audit_logs(action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_resource ON audit_logs(resource_type, resource_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_created  ON audit_logs(created_at DESC);

-- ---------------------------------------------------------------------------
-- security_events
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS security_events (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type     VARCHAR(100) NOT NULL,
    severity       VARCHAR(20)  DEFAULT 'medium',
    actor_id       UUID         REFERENCES users(id) ON DELETE SET NULL,
    actor_ip       INET,
    resource_type  VARCHAR(100),
    resource_id    UUID,
    details        JSONB        DEFAULT '{}',
    resolved       BOOLEAN      DEFAULT false,
    resolved_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ  DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sec_events_type      ON security_events(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sec_events_unresolved ON security_events(created_at DESC) WHERE resolved = false;

-- ---------------------------------------------------------------------------
-- ai_models  (track ML models used for correlation/classification)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS ai_models (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    type        VARCHAR(100) NOT NULL,
    version     VARCHAR(50),
    config      JSONB        DEFAULT '{}',
    metrics     JSONB        DEFAULT '{}',
    is_active   BOOLEAN      DEFAULT true,
    trained_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- compliance_reports
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS compliance_reports (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    report_type   VARCHAR(100) NOT NULL,
    period_start  TIMESTAMPTZ  NOT NULL,
    period_end    TIMESTAMPTZ  NOT NULL,
    data          JSONB        NOT NULL DEFAULT '{}',
    generated_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ  DEFAULT NOW()
);

-- =============================================================================
-- PARTITION MANAGEMENT
-- Create concrete partitions for alerts, correlation, audit, history tables.
-- Pattern: YYYY_MM naming, 13 months pre-created (current + 12 ahead)
-- Production: use pg_partman for automatic creation.
-- =============================================================================

-- Helper: creates next N monthly partitions. Called from application at startup.
CREATE OR REPLACE FUNCTION create_monthly_partition(
    p_table_name TEXT,
    p_year       INTEGER,
    p_month      INTEGER
) RETURNS VOID AS $$
DECLARE
    part_name   TEXT;
    start_date  DATE;
    end_date    DATE;
BEGIN
    part_name  := p_table_name || '_' || LPAD(p_year::TEXT, 4, '0') || '_' || LPAD(p_month::TEXT, 2, '0');
    start_date := DATE(p_year || '-' || LPAD(p_month::TEXT, 2, '0') || '-01');
    end_date   := start_date + INTERVAL '1 month';

    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I
         FOR VALUES FROM (%L) TO (%L)',
        part_name, p_table_name, start_date, end_date
    );
END;
$$ LANGUAGE plpgsql;

-- Pre-create partitions: 2025-01 through 2027-12
DO $$
DECLARE
    tables TEXT[] := ARRAY[
        'alerts','alert_history','correlation_results','incident_timeline',
        'audit_logs','notification_log','service_health_checks','webhook_deliveries'
    ];
    t      TEXT;
    yr     INTEGER;
    mo     INTEGER;
BEGIN
    FOREACH t IN ARRAY tables LOOP
        FOR yr IN 2025..2027 LOOP
            FOR mo IN 1..12 LOOP
                PERFORM create_monthly_partition(t, yr, mo);
            END LOOP;
        END LOOP;
    END LOOP;
END $$;

-- =============================================================================
-- FUNCTIONS & TRIGGERS
-- =============================================================================

-- ---------------------------------------------------------------------------
-- updated_at auto-stamp
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply to all mutable tables
DO $$
DECLARE
    tbl TEXT;
    tbls TEXT[] := ARRAY[
        'roles','permissions','users','teams','user_expertise',
        'escalation_policies','oncall_schedules','oncall_shifts',
        'incidents',
        'correlation_clusters','correlation_rules','historical_patterns',
        'topology_entities','topology_sources','topology_management_zones',
        'k8s_cluster_configs','cloudstack_configs','netapp_configs',
        'dynatrace_config','alert_sources','integrations','auth_providers',
        'llm_configs','webhooks','notification_channels','notification_rules',
        'workflows','runbooks','slo_definitions','ai_models',
        'maintenance_windows','alert_assignments'
    ];
BEGIN
    FOREACH tbl IN ARRAY tbls LOOP
        EXECUTE format(
            'CREATE TRIGGER trg_%s_updated_at
             BEFORE UPDATE ON %I
             FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()',
            tbl, tbl
        );
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- alerts: auto-set first_seen_at / last_seen_at on dedup increment
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION alert_dedup_touch()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.count > OLD.count THEN
        NEW.last_seen_at := NOW();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_alerts_dedup_touch
    BEFORE UPDATE OF count ON alerts
    FOR EACH ROW EXECUTE FUNCTION alert_dedup_touch();

-- ---------------------------------------------------------------------------
-- correlation_results: sync denormalized fields back to alerts
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION sync_alert_correlation_fields()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE alerts
    SET correlation_id         = NEW.correlation_group_id,
        correlation_confidence = NEW.overall_confidence,
        updated_at             = NOW()
    WHERE id = NEW.alert_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_corrres_sync_alerts
    AFTER INSERT OR UPDATE ON correlation_results
    FOR EACH ROW EXECUTE FUNCTION sync_alert_correlation_fields();

-- ---------------------------------------------------------------------------
-- correlation_clusters: keep alert_count in sync
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION sync_cluster_alert_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE correlation_clusters
    SET alert_count = array_length(alert_ids, 1),
        updated_at  = NOW()
    WHERE id = NEW.cluster_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_corrres_cluster_count
    AFTER INSERT OR UPDATE OF cluster_id ON correlation_results
    FOR EACH ROW
    WHEN (NEW.cluster_id IS NOT NULL)
    EXECUTE FUNCTION sync_cluster_alert_count();

-- ---------------------------------------------------------------------------
-- Partition maintenance: auto-create next month on last day of current
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION ensure_next_month_partitions()
RETURNS VOID AS $$
DECLARE
    tables TEXT[] := ARRAY[
        'alerts','alert_history','correlation_results','incident_timeline',
        'audit_logs','notification_log','service_health_checks','webhook_deliveries'
    ];
    t TEXT;
    next_month DATE := DATE_TRUNC('month', NOW() + INTERVAL '1 month');
BEGIN
    FOREACH t IN ARRAY tables LOOP
        PERFORM create_monthly_partition(
            t,
            EXTRACT(YEAR  FROM next_month)::INTEGER,
            EXTRACT(MONTH FROM next_month)::INTEGER
        );
    END LOOP;
END;
$$ LANGUAGE plpgsql;

-- =============================================================================
-- BACKWARD-COMPATIBLE VIEWS
-- Go code that queries these views requires ZERO changes.
-- =============================================================================

-- ---------------------------------------------------------------------------
-- alert_correlations  (drop + recreate as view on correlation_results)
-- Supports: SELECT COUNT(*) WHERE confidence_score > 0.8
-- ---------------------------------------------------------------------------
CREATE VIEW alert_correlations AS
SELECT
    cr.id,
    cr.alert_id,
    cr.correlation_group_id    AS correlation_id,
    cr.similar_alert_ids       AS similar_alerts,
    cr.overall_confidence      AS confidence_score,
    cr.correlation_type,
    cr.recommended_action,
    cr.is_duplicate,
    cr.duplicate_of,
    -- Backward-compat from dynatrace migration
    (cr.strategy_scores->>'topology')::DECIMAL(5,4)  AS topology_correlation_score,
    cr.ai_enrichment->'related_entities'              AS related_entities,
    (cr.ai_enrichment->>'management_zone_match')::BOOLEAN AS management_zone_match,
    cr.created_at,
    cr.updated_at
FROM (
    -- Latest correlation result per alert
    SELECT DISTINCT ON (alert_id) *
    FROM correlation_results
    ORDER BY alert_id, created_at DESC
) cr;

-- ---------------------------------------------------------------------------
-- active_alerts
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW active_alerts AS
SELECT
    a.*,
    u.full_name  AS assigned_to_name,
    u.email      AS assigned_to_email
FROM alerts a
LEFT JOIN users u ON a.assigned_to = u.id
WHERE a.status IN ('open','acknowledged','investigating');

-- ---------------------------------------------------------------------------
-- active_incidents
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW active_incidents AS
SELECT
    i.*,
    u.full_name  AS assigned_to_name,
    u.email      AS assigned_to_email
FROM incidents i
LEFT JOIN users u ON i.assigned_to = u.id
WHERE i.status IN ('open','investigating','identified','monitoring');

-- ---------------------------------------------------------------------------
-- user_permissions
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW user_permissions AS
SELECT
    u.id            AS user_id,
    u.username,
    u.email,
    r.name          AS role_name,
    p.name          AS permission_name,
    p.resource,
    p.action
FROM users u
JOIN roles            r  ON u.role_id       = r.id
JOIN role_permissions rp ON r.id            = rp.role_id
JOIN permissions      p  ON rp.permission_id = p.id;

-- ---------------------------------------------------------------------------
-- ai_correlation_analytics  (backward compat for dashboard queries)
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW ai_correlation_analytics AS
SELECT
    DATE_TRUNC('hour', cr.created_at)     AS hour_bucket,
    cr.correlation_type                   AS strategy,
    COUNT(*)                              AS total_correlations,
    AVG(cr.overall_confidence)            AS avg_confidence,
    SUM(CASE WHEN cr.feedback_correct = true  THEN 1 ELSE 0 END) AS correct_count,
    SUM(CASE WHEN cr.feedback_correct = false THEN 1 ELSE 0 END) AS incorrect_count
FROM correlation_results cr
WHERE cr.created_at >= NOW() - INTERVAL '7 days'
GROUP BY 1, 2;

-- =============================================================================
-- SEED DATA
-- =============================================================================

-- ---------------------------------------------------------------------------
-- Roles
-- ---------------------------------------------------------------------------
INSERT INTO roles (name, description, is_system_role) VALUES
('admin',    'Full system access',                        true),
('manager',  'Manage alerts, incidents, and team members', true),
('engineer', 'Handle alerts and incidents',                true),
('viewer',   'Read-only access',                           true),
('operator', 'Operate alerts with limited config access',  true)
ON CONFLICT (name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------
INSERT INTO permissions (name, resource, action, description) VALUES
('users.view',        'users',     'view',      'View users'),
('users.create',      'users',     'create',    'Create users'),
('users.update',      'users',     'update',    'Update users'),
('users.delete',      'users',     'delete',    'Delete users'),
('roles.view',        'roles',     'view',      'View roles'),
('roles.manage',      'roles',     'manage',    'Manage roles'),
('alerts.view',       'alerts',    'view',      'View alerts'),
('alerts.create',     'alerts',    'create',    'Create alerts'),
('alerts.update',     'alerts',    'update',    'Update alerts'),
('alerts.delete',     'alerts',    'delete',    'Delete alerts'),
('alerts.assign',     'alerts',    'assign',    'Assign alerts'),
('alerts.resolve',    'alerts',    'resolve',   'Resolve alerts'),
('incidents.view',    'incidents', 'view',      'View incidents'),
('incidents.create',  'incidents', 'create',    'Create incidents'),
('incidents.update',  'incidents', 'update',    'Update incidents'),
('incidents.delete',  'incidents', 'delete',    'Delete incidents'),
('incidents.assign',  'incidents', 'assign',    'Assign incidents'),
('incidents.resolve', 'incidents', 'resolve',   'Resolve incidents'),
('analytics.view',    'analytics', 'view',      'View analytics'),
('analytics.export',  'analytics', 'export',    'Export analytics'),
('ai.use',            'ai',        'use',       'Use AI features'),
('ai.train',          'ai',        'train',     'Train AI models'),
('audit.view',        'audit',     'view',      'View audit logs'),
('system.configure',  'system',    'configure', 'Configure system'),
('correlation.manage','correlation','manage',   'Manage correlation rules')
ON CONFLICT (name) DO NOTHING;

-- Admin gets all permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p WHERE r.name = 'admin'
ON CONFLICT DO NOTHING;

-- Manager permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'manager'
  AND p.name IN (
    'users.view','alerts.view','alerts.create','alerts.update','alerts.assign','alerts.resolve',
    'incidents.view','incidents.create','incidents.update','incidents.assign','incidents.resolve',
    'analytics.view','analytics.export','ai.use','correlation.manage'
  )
ON CONFLICT DO NOTHING;

-- Engineer permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'engineer'
  AND p.name IN (
    'alerts.view','alerts.update','alerts.assign','alerts.resolve',
    'incidents.view','incidents.create','incidents.update',
    'analytics.view','ai.use'
  )
ON CONFLICT DO NOTHING;

-- Viewer permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'viewer'
  AND p.name IN ('alerts.view','incidents.view','analytics.view')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Default correlation engine config
-- ---------------------------------------------------------------------------
INSERT INTO correlation_engine_config (config_key, config_value, description) VALUES
('deduplication',   '{"enabled":true,"window_minutes":60,"similarity_threshold":0.85,"fingerprint_fields":["source","severity","title","hostname"]}', 'Deduplication settings'),
('auto_correlation','{"enabled":true,"window_minutes":30,"min_cluster_size":2,"confidence_threshold":0.7,"use_semantic":true,"use_topology":true}',   'Auto-correlation settings'),
('topology_weights','{"parent_child":0.9,"same_host":0.8,"same_service":0.7,"same_cluster":0.6,"same_namespace":0.65}',                              'Topology correlation weights'),
('llm_enrichment',  '{"enabled":true,"min_severity":"high","auto_rca":true,"auto_remediation_suggestions":true}',                                    'LLM enrichment settings'),
('rate_limits',     '{"max_alerts_per_minute":5000,"max_correlations_per_minute":2000,"burst_window_seconds":10}',                                   'Processing rate limits')
ON CONFLICT (config_key) DO NOTHING;

-- Default LLM config
INSERT INTO llm_configs (name, provider, model_name, endpoint_url, enabled) VALUES
('Ollama phi3:mini', 'ollama', 'phi3:mini', 'http://ollama.alert-engine-poc.svc.cluster.local:11434', true)
ON CONFLICT DO NOTHING;

-- Default infra regions
INSERT INTO infra_regions (name, display_name, location, bm_count) VALUES
('reno',   'Reno (RNO)',   'Reno, NV',   75),
('maiden', 'Maiden (MDN)', 'Maiden, NC', 75)
ON CONFLICT (name) DO NOTHING;

-- =============================================================================
-- TABLE COMMENTS (documentation)
-- =============================================================================
COMMENT ON TABLE roles                   IS 'RBAC roles';
COMMENT ON TABLE users                   IS 'User accounts — local, LDAP, SAML, MAS';
COMMENT ON TABLE teams                   IS 'Organizational teams for alert routing and ownership';
COMMENT ON TABLE alerts                  IS 'PARTITIONED: alert records from all sources';
COMMENT ON TABLE alert_history           IS 'PARTITIONED: append-only audit trail of alert field changes';
COMMENT ON TABLE incidents               IS 'Incident records — manual and auto-created from correlations';
COMMENT ON TABLE incident_timeline       IS 'PARTITIONED: chronological events for an incident';
COMMENT ON TABLE correlation_results     IS 'PARTITIONED: UNIFIED correlation table (replaces alert_correlations, ai_correlation_results, davis_ai_correlations, pipeline_correlation_results)';
COMMENT ON TABLE correlation_clusters    IS 'Logical groups of correlated alerts, optionally linked to an incident';
COMMENT ON TABLE correlation_rules       IS 'Unified correlation rules (replaces correlation_rules + enhanced_correlation_rules)';
COMMENT ON TABLE alert_similarity_cache  IS 'Pairwise alert similarity — evicting cache with TTL for fast correlation';
COMMENT ON TABLE topology_entities       IS 'Topology entity METADATA from DT, K8s, CloudStack (relationships live in Neo4j)';
COMMENT ON TABLE topology_sources        IS 'Configuration for topology source systems';
COMMENT ON TABLE audit_logs              IS 'PARTITIONED: compliance-grade audit trail';
COMMENT ON VIEW  alert_correlations      IS 'BACKWARD-COMPAT view on correlation_results — keeps existing Go code working';

COMMIT;
