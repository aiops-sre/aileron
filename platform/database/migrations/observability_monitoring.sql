-- ============================================================================
-- SRE-Grade Application Monitoring & Observability Schema
-- Implements Google's Four Golden Signals, RED, and USE Metrics
-- ============================================================================

-- Observability Metrics - Time-series metrics storage
CREATE TABLE IF NOT EXISTS observability_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    metric_type VARCHAR(50) NOT NULL, -- 'counter', 'gauge', 'histogram', 'summary'
    metric_name VARCHAR(255) NOT NULL,
    metric_value FLOAT NOT NULL,
    labels JSONB, -- Multi-dimensional tags
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_obs_metrics_name ON observability_metrics(metric_name);
CREATE INDEX idx_obs_metrics_timestamp ON observability_metrics(timestamp);
CREATE INDEX idx_obs_metrics_labels ON observability_metrics USING GIN (labels);

-- Distributed Traces - Trace and span storage
CREATE TABLE IF NOT EXISTS distributed_traces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id VARCHAR(64) NOT NULL,
    span_id VARCHAR(32) NOT NULL,
    parent_span_id VARCHAR(32),
    operation VARCHAR(255) NOT NULL,
    service_name VARCHAR(100) NOT NULL,
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP,
    duration_ms FLOAT,
    tags JSONB,
    logs JSONB,
    status VARCHAR(20), -- 'ok', 'error', 'timeout'
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(trace_id, span_id)
);

CREATE INDEX idx_traces_trace_id ON distributed_traces(trace_id);
CREATE INDEX idx_traces_service ON distributed_traces(service_name);
CREATE INDEX idx_traces_start_time ON distributed_traces(start_time);
CREATE INDEX idx_traces_duration ON distributed_traces(duration_ms);

-- Observability Logs - Structured log aggregation
CREATE TABLE IF NOT EXISTS observability_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    level VARCHAR(20) NOT NULL, -- 'debug', 'info', 'warn', 'error', 'fatal'
    message TEXT NOT NULL,
    service VARCHAR(100) NOT NULL,
    trace_id VARCHAR(64),
    span_id VARCHAR(32),
    fields JSONB,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_obs_logs_level ON observability_logs(level);
CREATE INDEX idx_obs_logs_service ON observability_logs(service);
CREATE INDEX idx_obs_logs_trace ON observability_logs(trace_id);
CREATE INDEX idx_obs_logs_timestamp ON observability_logs(timestamp);
CREATE INDEX idx_obs_logs_fields ON observability_logs USING GIN (fields);

-- Health Checks - Component health status
CREATE TABLE IF NOT EXISTS health_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    check_name VARCHAR(100) NOT NULL,
    check_type VARCHAR(50) NOT NULL, -- 'liveness', 'readiness', 'startup'
    status VARCHAR(20) NOT NULL, -- 'healthy', 'degraded', 'unhealthy'
    latency_ms FLOAT,
    message TEXT,
    metadata JSONB,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_health_check_name ON health_checks(check_name);
CREATE INDEX idx_health_status ON health_checks(status);
CREATE INDEX idx_health_timestamp ON health_checks(timestamp);

-- Service Level Indicators (SLIs)
CREATE TABLE IF NOT EXISTS sli_measurements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sli_name VARCHAR(255) NOT NULL,
    metric_name VARCHAR(255) NOT NULL,
    metric_value FLOAT NOT NULL,
    aggregation VARCHAR(20), -- 'avg', 'p50', 'p95', 'p99', 'count'
    window_duration INTERVAL NOT NULL,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_sli_name ON sli_measurements(sli_name);
CREATE INDEX idx_sli_timestamp ON sli_measurements(timestamp);

-- Service Level Objectives (SLOs)
CREATE TABLE IF NOT EXISTS slo_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    sli_name VARCHAR(255) NOT NULL,
    target FLOAT NOT NULL,
    window INTERVAL NOT NULL,
    error_budget FLOAT NOT NULL,
    remaining_budget FLOAT,
    budget_used_percent FLOAT,
    alert_threshold FLOAT DEFAULT 80.0,
    status VARCHAR(20) DEFAULT 'healthy',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_slo_name ON slo_definitions(name);
CREATE INDEX idx_slo_status ON slo_definitions(status);

-- SLO Violations - Track SLO breaches
CREATE TABLE IF NOT EXISTS slo_violations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slo_id UUID REFERENCES slo_definitions(id),
    violation_type VARCHAR(50), -- 'breach', 'warning', 'budget_exhausted'
    actual_value FLOAT,
    target_value FLOAT,
    budget_consumed FLOAT,
    duration INTERVAL,
    started_at TIMESTAMP NOT NULL,
    ended_at TIMESTAMP,
    incident_created BOOLEAN DEFAULT FALSE,
    incident_id UUID,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_slo_violations_slo ON slo_violations(slo_id);
CREATE INDEX idx_slo_violations_started ON slo_violations(started_at);

-- Error Budget - Track error budget consumption
CREATE TABLE IF NOT EXISTS error_budget_tracking (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slo_id UUID REFERENCES slo_definitions(id),
    period_start TIMESTAMP NOT NULL,
    period_end TIMESTAMP NOT NULL,
    total_budget FLOAT NOT NULL,
    consumed_budget FLOAT NOT NULL,
    remaining_budget FLOAT NOT NULL,
    burn_rate FLOAT, -- Rate of budget consumption
    projection JSONB, -- Projected budget exhaustion
    status VARCHAR(20), -- 'healthy', 'warning', 'critical'
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_error_budget_slo ON error_budget_tracking(slo_id);
CREATE INDEX idx_error_budget_period ON error_budget_tracking(period_start, period_end);

-- Synthetic Monitoring - Synthetic checks and tests
CREATE TABLE IF NOT EXISTS synthetic_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    check_name VARCHAR(255) NOT NULL,
    check_type VARCHAR(50), -- 'http', 'api', 'browser', 'script'
    url VARCHAR(500),
    method VARCHAR(10),
    expected_status INTEGER,
    interval INTERVAL NOT NULL,
    timeout INTERVAL NOT NULL,
    enabled BOOLEAN DEFAULT TRUE,
    config JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Synthetic Check Results
CREATE TABLE IF NOT EXISTS synthetic_check_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    check_id UUID REFERENCES synthetic_checks(id),
    status VARCHAR(20), -- 'success', 'failure', 'timeout'
    latency_ms FLOAT,
    response_code INTEGER,
    error_message TEXT,
    metadata JSONB,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_synthetic_results_check ON synthetic_check_results(check_id);
CREATE INDEX idx_synthetic_results_timestamp ON synthetic_check_results(timestamp);
CREATE INDEX idx_synthetic_results_status ON synthetic_check_results(status);

-- Capacity Planning Metrics
CREATE TABLE IF NOT EXISTS capacity_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type VARCHAR(100) NOT NULL, -- 'cpu', 'memory', 'disk', 'network', 'database'
    resource_name VARCHAR(255) NOT NULL,
    current_usage FLOAT NOT NULL,
    capacity FLOAT NOT NULL,
    utilization_percent FLOAT NOT NULL,
    growth_rate FLOAT, -- Percentage growth per day
    projected_full_date TIMESTAMP,
    threshold_warning FLOAT DEFAULT 75.0,
    threshold_critical FLOAT DEFAULT 90.0,
    status VARCHAR(20), -- 'ok', 'warning', 'critical'
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_capacity_resource ON capacity_metrics(resource_type, resource_name);
CREATE INDEX idx_capacity_status ON capacity_metrics(status);
CREATE INDEX idx_capacity_timestamp ON capacity_metrics(timestamp);

-- Anomaly Detections (from observability)
CREATE TABLE IF NOT EXISTS observability_anomalies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    metric_name VARCHAR(255) NOT NULL,
    anomaly_type VARCHAR(50), -- 'spike', 'drop', 'trend_change', 'seasonal'
    severity VARCHAR(20),
    baseline_value FLOAT,
    actual_value FLOAT,
    deviation_score FLOAT,
    confidence FLOAT,
    detection_algorithm VARCHAR(50),
    context JSONB,
    detected_at TIMESTAMP NOT NULL,
    resolved_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_obs_anomaly_metric ON observability_anomalies(metric_name);
CREATE INDEX idx_obs_anomaly_severity ON observability_anomalies(severity);
CREATE INDEX idx_obs_anomaly_detected ON observability_anomalies(detected_at);

-- Dependency Health Tracking
CREATE TABLE IF NOT EXISTS dependency_health (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dependency_name VARCHAR(255) NOT NULL,
    dependency_type VARCHAR(50), -- 'database', 'cache', 'api', 'service'
    status VARCHAR(20), -- 'healthy', 'degraded', 'unhealthy', 'unavailable'
    latency_ms FLOAT,
    error_rate FLOAT,
    last_check TIMESTAMP,
    consecutive_failures INTEGER DEFAULT 0,
    metadata JSONB,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_dependency_name ON dependency_health(dependency_name);
CREATE INDEX idx_dependency_status ON dependency_health(status);

-- Performance Baselines - Store normal performance patterns
CREATE TABLE IF NOT EXISTS performance_baselines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    metric_name VARCHAR(255) NOT NULL,
    time_period VARCHAR(50), -- 'hourly', 'daily', 'weekly'
    period_identifier VARCHAR(50), -- 'hour_9', 'monday', 'week_1'
    avg_value FLOAT,
    p50_value FLOAT,
    p95_value FLOAT,
    p99_value FLOAT,
    std_dev FLOAT,
    sample_count INTEGER,
    last_updated TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(metric_name, time_period, period_identifier)
);

CREATE INDEX idx_baseline_metric ON performance_baselines(metric_name);
CREATE INDEX idx_baseline_period ON performance_baselines(time_period, period_identifier);

-- Incident Detection from Observability
CREATE TABLE IF NOT EXISTS observability_incidents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_type VARCHAR(100),
    severity VARCHAR(20),
    title VARCHAR(500) NOT NULL,
    description TEXT,
    affected_services TEXT[],
    related_metrics JSONB,
    related_traces JSONB,
    related_logs JSONB,
    root_cause_analysis TEXT,
    status VARCHAR(20) DEFAULT 'detected',
    detected_at TIMESTAMP NOT NULL,
    acknowledged_at TIMESTAMP,
    resolved_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_obs_incidents_status ON observability_incidents(status);
CREATE INDEX idx_obs_incidents_severity ON observability_incidents(severity);
CREATE INDEX idx_obs_incidents_detected ON observability_incidents(detected_at);

-- Metric Exporters Configuration
CREATE TABLE IF NOT EXISTS metric_exporters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    exporter_name VARCHAR(100) NOT NULL UNIQUE,
    exporter_type VARCHAR(50), -- 'prometheus', 'grafana', 'datadog', 'newrelic'
    endpoint_url VARCHAR(500),
    auth_config JSONB,
    enabled BOOLEAN DEFAULT TRUE,
    export_interval INTERVAL DEFAULT '10 seconds',
    last_export TIMESTAMP,
    error_count INTEGER DEFAULT 0,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Metric Retention Policies
CREATE TABLE IF NOT EXISTS metric_retention_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    metric_pattern VARCHAR(255) NOT NULL, -- Regex or glob pattern
    retention_period INTERVAL NOT NULL,
    aggregation_rules JSONB, -- Downsampling rules
    archive_enabled BOOLEAN DEFAULT FALSE,
    archive_location VARCHAR(500),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Comments
COMMENT ON TABLE observability_metrics IS 'Time-series metrics with multi-dimensional labels';
COMMENT ON TABLE distributed_traces IS 'Distributed tracing data for request flows';
COMMENT ON TABLE observability_logs IS 'Aggregated structured logs with trace correlation';
COMMENT ON TABLE health_checks IS 'Component health check results';
COMMENT ON TABLE sli_measurements IS 'Service Level Indicator measurements';
COMMENT ON TABLE slo_definitions IS 'Service Level Objective definitions with error budgets';
COMMENT ON TABLE slo_violations IS 'SLO breach tracking';
COMMENT ON TABLE error_budget_tracking IS 'Error budget consumption tracking';
COMMENT ON TABLE synthetic_checks IS 'Synthetic monitoring check definitions';
COMMENT ON TABLE synthetic_check_results IS 'Synthetic check execution results';
COMMENT ON TABLE capacity_metrics IS 'Resource capacity and utilization for capacity planning';
COMMENT ON TABLE observability_anomalies IS 'Detected anomalies in metrics/traces/logs';
COMMENT ON TABLE dependency_health IS 'External dependency health tracking';
COMMENT ON TABLE performance_baselines IS 'Historical performance baselines for anomaly detection';
COMMENT ON TABLE observability_incidents IS 'Auto-detected incidents from observability data';
COMMENT ON TABLE metric_exporters IS 'Configuration for metric exporters (Prometheus, Grafana, etc)';

-- Create Hypertable for time-series data (if TimescaleDB is available)
-- This enables better performance for time-series queries
-- Uncomment if using TimescaleDB:
-- SELECT create_hypertable('observability_metrics', 'timestamp', if_not_exists => TRUE);
-- SELECT create_hypertable('distributed_traces', 'start_time', if_not_exists => TRUE);
-- SELECT create_hypertable('observability_logs', 'timestamp', if_not_exists => TRUE);
-- SELECT create_hypertable('sli_measurements', 'timestamp', if_not_exists => TRUE);

-- Default SLO definitions
INSERT INTO slo_definitions (name, description, sli_name, target, window, error_budget, alert_threshold) VALUES
('api_availability', 'API endpoint availability must be > 99.9%', 'http_success_rate', 99.9, '30 days', 0.1, 80.0),
('api_latency_p99', '99th percentile latency must be < 500ms', 'http_latency_p99', 500.0, '1 day', 100.0, 75.0),
('alert_processing_time', 'Alerts processed within 10 seconds', 'alert_process_duration', 10.0, '1 day', 5.0, 80.0),
('database_availability', 'Database availability > 99.99%', 'db_success_rate', 99.99, '30 days', 0.01, 90.0),
('incident_detection_time', 'Incidents detected within 60 seconds', 'incident_detection_duration', 60.0, '1 day', 30.0, 70.0)
ON CONFLICT (name) DO NOTHING;

-- Default synthetic checks
INSERT INTO synthetic_checks (check_name, check_type, url, method, expected_status, interval, timeout) VALUES
('api_health_check', 'http', 'http://localhost:3000/health', 'GET', 200, '30 seconds', '5 seconds'),
('api_metrics_endpoint', 'http', 'http://localhost:3000/metrics', 'GET', 200, '60 seconds', '10 seconds'),
('database_connectivity', 'api', 'http://localhost:3000/api/v1/health/db', 'GET', 200, '30 seconds', '5 seconds'),
('redis_connectivity', 'api', 'http://localhost:3000/api/v1/health/redis', 'GET', 200, '30 seconds', '5 seconds')
ON CONFLICT DO NOTHING;

-- Metric retention policies
INSERT INTO metric_retention_policies (metric_pattern, retention_period, aggregation_rules) VALUES
('alerthub_http_*', '90 days', '{"1h": "7 days", "1d": "90 days"}'),
('alerthub_service_*', '180 days', '{"1h": "30 days", "1d": "180 days"}'),
('alerthub_system_*', '365 days', '{"1h": "90 days", "1d": "365 days"}'),
('alerthub_database_*', '90 days', '{"1h": "7 days", "1d": "90 days"}')
ON CONFLICT DO NOTHING;

-- Grant permissions
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO alerthub_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO alerthub_user;
