-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ============================================================================
-- TIME-SERIES METRICS TABLE
-- ============================================================================

CREATE TABLE IF NOT EXISTS metrics (
    time TIMESTAMPTZ NOT NULL,
    metric_type TEXT NOT NULL,
    metric_name TEXT NOT NULL,
    value DOUBLE PRECISION,
    labels JSONB,
    integration_id UUID,
    source TEXT
);

-- Convert to hypertable (automatic time-based partitioning)
SELECT create_hypertable('metrics', 'time', if_not_exists => TRUE);

-- Add retention policy (keep 90 days)
SELECT add_retention_policy('metrics', INTERVAL '90 days', if_not_exists => TRUE);

-- Create indexes for fast queries
CREATE INDEX IF NOT EXISTS idx_metrics_integration ON metrics (integration_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_metrics_type_name ON metrics (metric_type, metric_name, time DESC);
CREATE INDEX IF NOT EXISTS idx_metrics_labels_gin ON metrics USING GIN (labels);

-- ============================================================================
-- CONTINUOUS AGGREGATES (Pre-computed for dashboards)
-- ============================================================================

-- Hourly aggregates
CREATE MATERIALIZED VIEW IF NOT EXISTS metrics_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    metric_type,
    metric_name,
    integration_id,
    avg(value) as avg_value,
    max(value) as max_value,
    min(value) as min_value,
    count(*) as count
FROM metrics
GROUP BY bucket, metric_type, metric_name, integration_id
WITH NO DATA;

-- Refresh policy (auto-refresh every hour)
SELECT add_continuous_aggregate_policy('metrics_hourly',
    start_offset => INTERVAL '3 hours',
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 hour',
    if_not_exists => TRUE);

-- Daily aggregates
CREATE MATERIALIZED VIEW IF NOT EXISTS metrics_daily
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 day', time) AS bucket,
    metric_type,
    metric_name,
    integration_id,
    avg(value) as avg_value,
    max(value) as max_value,
    min(value) as min_value,
    count(*) as count
FROM metrics
GROUP BY bucket, metric_type, metric_name, integration_id
WITH NO DATA;

-- Refresh daily aggregates once per day
SELECT add_continuous_aggregate_policy('metrics_daily',
    start_offset => INTERVAL '3 days',
    end_offset => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

-- ============================================================================
-- INTEGRATION PERFORMANCE METRICS
-- ============================================================================

CREATE TABLE IF NOT EXISTS integration_performance (
    time TIMESTAMPTZ NOT NULL,
    integration_id UUID NOT NULL,
    integration_type TEXT NOT NULL,
    request_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    error_count INTEGER DEFAULT 0,
    avg_latency_ms DOUBLE PRECISION,
    p95_latency_ms DOUBLE PRECISION,
    p99_latency_ms DOUBLE PRECISION,
    data_volume_bytes BIGINT DEFAULT 0,
    cost DOUBLE PRECISION DEFAULT 0.0
);

SELECT create_hypertable('integration_performance', 'time', if_not_exists => TRUE);

-- Compression policy (compress data older than 7 days)
ALTER TABLE integration_performance SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'integration_id, integration_type',
    timescaledb.compress_orderby = 'time DESC'
);

SELECT add_compression_policy('integration_performance', INTERVAL '7 days', if_not_exists => TRUE);

-- Retention policy
SELECT add_retention_policy('integration_performance', INTERVAL '180 days', if_not_exists => TRUE);

-- ============================================================================
-- ALERT METRICS FOR ANALYTICS
-- ============================================================================

CREATE TABLE IF NOT EXISTS alert_metrics (
    time TIMESTAMPTZ NOT NULL,
    alert_id UUID,
    severity TEXT NOT NULL,
    status TEXT NOT NULL,
    source TEXT,
    assigned_to UUID,
    resolution_time_seconds INTEGER,
    acknowledged_time_seconds INTEGER,
    escalated BOOLEAN DEFAULT FALSE,
    sla_met BOOLEAN DEFAULT TRUE
);

SELECT create_hypertable('alert_metrics', 'time', if_not_exists => TRUE);

-- Continuous aggregate for MTTR dashboard
CREATE MATERIALIZED VIEW IF NOT EXISTS alert_mttr_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    severity,
    avg(resolution_time_seconds) as avg_mttr_seconds,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY resolution_time_seconds) as p95_mttr,
    count(*) as alert_count,
    count(*) FILTER (WHERE sla_met = TRUE) as sla_met_count
FROM alert_metrics
WHERE resolution_time_seconds IS NOT NULL
GROUP BY bucket, severity
WITH NO DATA;

SELECT add_continuous_aggregate_policy('alert_mttr_hourly',
    start_offset => INTERVAL '3 hours',
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes',
    if_not_exists => TRUE);

-- ============================================================================
-- SLA TRACKING
-- ============================================================================

CREATE TABLE IF NOT EXISTS sla_metrics (
    time TIMESTAMPTZ NOT NULL,
    entity_type TEXT NOT NULL, -- alert, incident, integration
    entity_id UUID NOT NULL,
    sla_name TEXT NOT NULL,
    target_value DOUBLE PRECISION,
    actual_value DOUBLE PRECISION,
    met BOOLEAN,
    breach_reason TEXT
);

SELECT create_hypertable('sla_metrics', 'time', if_not_exists => TRUE);

-- Index for fast SLA dashboard queries
CREATE INDEX IF NOT EXISTS idx_sla_entity ON sla_metrics (entity_type, entity_id, time DESC);

-- ============================================================================
-- AI MODEL PERFORMANCE TRACKING
-- ============================================================================

CREATE TABLE IF NOT EXISTS ai_model_performance (
    time TIMESTAMPTZ NOT NULL,
    provider TEXT NOT NULL, -- chatgpt, claude, gemini
    model TEXT NOT NULL,
    query_type TEXT,
    latency_ms INTEGER,
    tokens_used INTEGER,
    cost DOUBLE PRECISION,
    confidence DOUBLE PRECISION,
    user_rating INTEGER, -- 1-5 stars from user feedback
    success BOOLEAN
);

SELECT create_hypertable('ai_model_performance', 'time', if_not_exists => TRUE);

-- Continuous aggregate for AI cost dashboard
CREATE MATERIALIZED VIEW IF NOT EXISTS ai_cost_daily
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 day', time) AS bucket,
    provider,
    model,
    sum(cost) as total_cost,
    avg(latency_ms) as avg_latency,
    sum(tokens_used) as total_tokens,
    avg(confidence) as avg_confidence,
    count(*) as query_count,
    count(*) FILTER (WHERE success = TRUE) as success_count
FROM ai_model_performance
GROUP BY bucket, provider, model
WITH NO DATA;

SELECT add_continuous_aggregate_policy('ai_cost_daily',
    start_offset => INTERVAL '3 days',
    end_offset => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

-- ============================================================================
-- COMMENTS & DOCUMENTATION
-- ============================================================================

COMMENT ON TABLE metrics IS 'TimescaleDB hypertable for all time-series metrics';
COMMENT ON TABLE integration_performance IS 'Integration performance metrics with automatic compression after 7 days';
COMMENT ON TABLE alert_metrics IS 'Alert lifecycle metrics for MTTR and SLA tracking';
COMMENT ON TABLE sla_metrics IS 'SLA compliance tracking across all entities';
COMMENT ON TABLE ai_model_performance IS 'AI model performance and cost tracking';

COMMENT ON VIEW metrics_hourly IS 'Pre-computed hourly metrics for fast dashboard queries';
COMMENT ON VIEW metrics_daily IS 'Pre-computed daily metrics for trend analysis';
COMMENT ON VIEW alert_mttr_hourly IS 'Hourly MTTR calculations for SLA dashboard';
COMMENT ON VIEW ai_cost_daily IS 'Daily AI cost and performance aggregates';
