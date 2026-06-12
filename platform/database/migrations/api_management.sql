-- =============================================================================
-- Enterprise API Management Schema
-- API Keys, Rate Limit Tiers, Webhook Subscriptions, Delivery Queue
-- =============================================================================

-- ---------------------------------------------------------------------------
-- Rate Limit Tiers — defines per-tier quotas for all endpoint categories
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rate_limit_tiers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(50)  NOT NULL UNIQUE,   -- free, standard, enterprise, internal
    display_name        VARCHAR(100) NOT NULL,
    requests_per_minute INTEGER      NOT NULL DEFAULT 60,
    requests_per_hour   INTEGER      NOT NULL DEFAULT 1000,
    requests_per_day    INTEGER      NOT NULL DEFAULT 10000,
    burst_limit         INTEGER      NOT NULL DEFAULT 20,
    -- Per-category overrides (AI endpoints are more expensive)
    ai_requests_per_min INTEGER      NOT NULL DEFAULT 10,
    webhook_ingress_per_min INTEGER  NOT NULL DEFAULT 500,
    -- Soft limit before warnings; hard limit returns 429
    soft_limit_pct      INTEGER      NOT NULL DEFAULT 80,
    is_active           BOOLEAN      NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

INSERT INTO rate_limit_tiers (name, display_name, requests_per_minute, requests_per_hour, requests_per_day, burst_limit, ai_requests_per_min, webhook_ingress_per_min) VALUES
    ('free',       'Free',       60,   1000,   10000,  10,  5,   100),
    ('standard',   'Standard',   300,  10000,  100000, 50,  20,  500),
    ('enterprise', 'Enterprise', 2000, 100000, 1000000,200, 100, 5000),
    ('internal',   'Internal',   99999,999999, 9999999,9999,9999,9999)
ON CONFLICT (name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- API Keys — scoped, expiring, rotatable keys for programmatic access
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_keys (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    -- key_prefix stored in plain (first 8 chars of sk-xxx, shown in UI)
    key_prefix      VARCHAR(20)  NOT NULL,
    -- SHA256(plaintext_key) — never store plaintext after creation
    key_hash        VARCHAR(64)  NOT NULL UNIQUE,
    -- Comma-separated scopes: alerts:read,alerts:write,incidents:read,webhooks:ingest,admin
    scopes          TEXT[]       NOT NULL DEFAULT '{"alerts:read"}',
    tier_id         UUID         REFERENCES rate_limit_tiers(id),
    tier_name       VARCHAR(50)  NOT NULL DEFAULT 'standard',
    is_active       BOOLEAN      NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,                         -- NULL = never expires
    last_used_at    TIMESTAMPTZ,
    last_used_ip    INET,
    total_requests  BIGINT       NOT NULL DEFAULT 0,
    -- Rotation: when rotated, old key is soft-revoked and new key inherits metadata
    rotated_from_id UUID         REFERENCES api_keys(id) ON DELETE SET NULL,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  VARCHAR(255),
    description     TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user_id   ON api_keys(user_id) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_api_keys_hash       ON api_keys(key_hash) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_api_keys_expires    ON api_keys(expires_at) WHERE expires_at IS NOT NULL AND is_active = true;

-- ---------------------------------------------------------------------------
-- API Key Usage Log — per-request tracking for quotas and analytics
-- Partitioned by day to control row count
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_key_usage (
    id           BIGSERIAL,
    api_key_id   UUID         NOT NULL,
    endpoint     VARCHAR(255) NOT NULL,
    method       VARCHAR(10)  NOT NULL,
    status_code  INTEGER      NOT NULL,
    latency_ms   INTEGER,
    ip_address   INET,
    user_agent   TEXT,
    request_id   VARCHAR(64),
    used_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (used_at);

CREATE TABLE IF NOT EXISTS api_key_usage_today    PARTITION OF api_key_usage FOR VALUES FROM (CURRENT_DATE)           TO (CURRENT_DATE + INTERVAL '1 day');
CREATE TABLE IF NOT EXISTS api_key_usage_yesterday PARTITION OF api_key_usage FOR VALUES FROM (CURRENT_DATE - INTERVAL '1 day') TO (CURRENT_DATE);
CREATE TABLE IF NOT EXISTS api_key_usage_week     PARTITION OF api_key_usage FOR VALUES FROM (CURRENT_DATE - INTERVAL '7 days') TO (CURRENT_DATE - INTERVAL '1 day');

CREATE INDEX IF NOT EXISTS idx_api_key_usage_key_used  ON api_key_usage(api_key_id, used_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_endpoint  ON api_key_usage(endpoint, used_at DESC);

-- ---------------------------------------------------------------------------
-- Per-user rate limit tier assignment
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_rate_limit_tiers (
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tier_id     UUID        NOT NULL REFERENCES rate_limit_tiers(id),
    assigned_by UUID        REFERENCES users(id),
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    notes       TEXT,
    PRIMARY KEY (user_id)
);

-- ---------------------------------------------------------------------------
-- Outbound Webhook Subscriptions — users register URLs to receive events
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    target_url      TEXT         NOT NULL,
    -- Events to deliver: alert.created, alert.resolved, incident.created, incident.closed, etc.
    event_types     TEXT[]       NOT NULL DEFAULT '{}',
    -- HMAC-SHA256 secret for signing deliveries (stored hashed)
    signing_secret  VARCHAR(64)  NOT NULL,  -- hex(SHA256(plaintext_secret))
    -- Only deliver for these severities (empty = all)
    severity_filter TEXT[]       NOT NULL DEFAULT '{}',
    -- Only deliver for these sources (empty = all)
    source_filter   TEXT[]       NOT NULL DEFAULT '{}',
    is_active       BOOLEAN      NOT NULL DEFAULT true,
    -- TLS settings
    verify_ssl      BOOLEAN      NOT NULL DEFAULT true,
    -- Delivery tracking
    total_deliveries  BIGINT     NOT NULL DEFAULT 0,
    failed_deliveries BIGINT     NOT NULL DEFAULT 0,
    last_delivery_at  TIMESTAMPTZ,
    last_success_at   TIMESTAMPTZ,
    last_failure_at   TIMESTAMPTZ,
    last_response_code INTEGER,
    -- Circuit breaker: pause delivery if too many consecutive failures
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    paused_until    TIMESTAMPTZ,
    description     TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_subs_user     ON webhook_subscriptions(user_id) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_webhook_subs_events   ON webhook_subscriptions USING GIN(event_types);

-- ---------------------------------------------------------------------------
-- Webhook Deliveries — per-delivery audit trail with retry state
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id     UUID        NOT NULL REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    event_type          VARCHAR(100) NOT NULL,
    event_id            UUID        NOT NULL,   -- alert_id or incident_id that triggered this
    idempotency_key     VARCHAR(255) UNIQUE,    -- prevents duplicate delivery on retry
    -- Payload snapshot at delivery time
    payload             JSONB       NOT NULL,
    -- Delivery state machine: pending → delivering → delivered | failed | dead_lettered
    status              VARCHAR(30) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','delivering','delivered','failed','dead_lettered','skipped')),
    attempt_count       INTEGER     NOT NULL DEFAULT 0,
    max_attempts        INTEGER     NOT NULL DEFAULT 3,
    -- Retry schedule (exponential backoff)
    next_attempt_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_attempt_at     TIMESTAMPTZ,
    -- Response from target
    response_status     INTEGER,
    response_body       TEXT,
    response_latency_ms INTEGER,
    -- Error details for debugging
    error_message       TEXT,
    -- Signature sent (for debugging)
    signature_header    VARCHAR(100),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_pending  ON webhook_deliveries(next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_sub      ON webhook_deliveries(subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_event    ON webhook_deliveries(event_id, event_type);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_idem     ON webhook_deliveries(idempotency_key) WHERE idempotency_key IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Event Type Catalog — defines all events AlertHub can emit
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS event_catalog (
    event_type      VARCHAR(100) PRIMARY KEY,
    category        VARCHAR(50)  NOT NULL,   -- alert, incident, correlation, system
    display_name    VARCHAR(255) NOT NULL,
    description     TEXT         NOT NULL,
    payload_schema  JSONB,                   -- JSON Schema for the event payload
    example_payload JSONB,
    is_active       BOOLEAN      NOT NULL DEFAULT true,
    since_version   VARCHAR(20)  NOT NULL DEFAULT '1.0.0',
    deprecated      BOOLEAN      NOT NULL DEFAULT false,
    deprecated_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

INSERT INTO event_catalog (event_type, category, display_name, description) VALUES
    ('alert.created',           'alert',       'Alert Created',           'Fired when a new alert is ingested from any source'),
    ('alert.updated',           'alert',       'Alert Updated',           'Fired when an alert field changes (status, severity, assignee)'),
    ('alert.resolved',          'alert',       'Alert Resolved',          'Fired when an alert transitions to resolved status'),
    ('alert.acknowledged',      'alert',       'Alert Acknowledged',      'Fired when an operator acknowledges an alert'),
    ('alert.suppressed',        'alert',       'Alert Suppressed',        'Fired when an alert is suppressed by a rule or maintenance window'),
    ('incident.created',        'incident',    'Incident Created',        'Fired when a new incident is created from correlated alerts'),
    ('incident.updated',        'incident',    'Incident Updated',        'Fired when incident title, severity, or assignee changes'),
    ('incident.closed',         'incident',    'Incident Closed',         'Fired when all correlated alerts are resolved and incident closes'),
    ('incident.escalated',      'incident',    'Incident Escalated',      'Fired when incident severity is upgraded'),
    ('correlation.completed',   'correlation', 'Correlation Completed',   'Fired when the pipeline finishes correlating an alert group'),
    ('rca.completed',           'rca',         'RCA Completed',           'Fired when an RCA investigation finishes with a root cause'),
    ('rca.failed',              'rca',         'RCA Failed',              'Fired when an RCA investigation fails to identify a root cause'),
    ('maintenance.started',     'system',      'Maintenance Started',     'Fired when a maintenance window becomes active'),
    ('maintenance.ended',       'system',      'Maintenance Ended',       'Fired when a maintenance window expires'),
    ('webhook.delivery_failed', 'system',      'Webhook Delivery Failed', 'Meta-event: fired when a webhook delivery exhausts retries')
ON CONFLICT (event_type) DO NOTHING;

-- ---------------------------------------------------------------------------
-- API Request Audit Log — structured log of every authenticated API request
-- Partitioned daily; retain 90 days
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_request_log (
    id          BIGSERIAL,
    request_id  VARCHAR(64)  NOT NULL,
    trace_id    VARCHAR(64),
    user_id     UUID,
    api_key_id  UUID,
    method      VARCHAR(10)  NOT NULL,
    path        VARCHAR(500) NOT NULL,
    query       TEXT,
    status_code INTEGER      NOT NULL,
    latency_ms  INTEGER      NOT NULL,
    ip_address  INET,
    user_agent  TEXT,
    error_msg   TEXT,
    logged_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (logged_at);

CREATE TABLE IF NOT EXISTS api_request_log_today     PARTITION OF api_request_log FOR VALUES FROM (CURRENT_DATE)             TO (CURRENT_DATE + INTERVAL '1 day');
CREATE TABLE IF NOT EXISTS api_request_log_yesterday PARTITION OF api_request_log FOR VALUES FROM (CURRENT_DATE - INTERVAL '1 day') TO (CURRENT_DATE);

CREATE INDEX IF NOT EXISTS idx_api_req_log_user    ON api_request_log(user_id, logged_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_req_log_key     ON api_request_log(api_key_id, logged_at DESC) WHERE api_key_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_req_log_path    ON api_request_log(path, logged_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_req_log_request ON api_request_log(request_id);
