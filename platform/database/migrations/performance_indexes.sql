-- Performance Optimization Indexes for AlertHub
-- This migration adds composite indexes for common query patterns

-- ============================================================================
-- Alert Performance Indexes
-- ============================================================================

-- Composite index for list queries (status + created_at)
CREATE INDEX IF NOT EXISTS idx_alerts_status_created_at 
ON alerts(status, created_at DESC);

-- Composite index for severity filtering with time
CREATE INDEX IF NOT EXISTS idx_alerts_severity_created_at 
ON alerts(severity, created_at DESC);

-- Composite index for source filtering with time
CREATE INDEX IF NOT EXISTS idx_alerts_source_created_at 
ON alerts(source, created_at DESC);

-- Composite index for assigned alerts
CREATE INDEX IF NOT EXISTS idx_alerts_assigned_to_status 
ON alerts(assigned_to, status) 
WHERE assigned_to IS NOT NULL;

-- Index for acknowledged alerts with user
CREATE INDEX IF NOT EXISTS idx_alerts_acknowledged_by 
ON alerts(acknowledged_by, acknowledged_at DESC) 
WHERE acknowledged_by IS NOT NULL;

-- Index for resolved alerts with user
CREATE INDEX IF NOT EXISTS idx_alerts_resolved_by 
ON alerts(resolved_by, resolved_at DESC) 
WHERE resolved_by IS NOT NULL;

-- Index for unresolved alerts (most common query)
CREATE INDEX IF NOT EXISTS idx_alerts_unresolved 
ON alerts(created_at DESC) 
WHERE status IN ('open', 'acknowledged', 'investigating');

-- JSONB indexes for metadata queries
CREATE INDEX IF NOT EXISTS idx_alerts_metadata_cluster 
ON alerts USING GIN ((metadata->'cluster'));

CREATE INDEX IF NOT EXISTS idx_alerts_metadata_namespace 
ON alerts USING GIN ((metadata->'namespace'));

-- ============================================================================
-- User Session Performance
-- ============================================================================

-- Composite index for active sessions
CREATE INDEX IF NOT EXISTS idx_user_sessions_user_expires 
ON user_sessions(user_id, expires_at DESC) 
WHERE expires_at > NOW();

-- ============================================================================
-- Alert History Performance
-- ============================================================================

-- Composite index for alert history queries
CREATE INDEX IF NOT EXISTS idx_alert_history_alert_created 
ON alert_history(alert_id, created_at DESC);

-- Index for recent actions by user
CREATE INDEX IF NOT EXISTS idx_alert_history_user_action 
ON alert_history(user_id, action, created_at DESC) 
WHERE user_id IS NOT NULL;

-- ============================================================================
-- Incident Performance
-- ============================================================================

-- Composite index for incident queries
CREATE INDEX IF NOT EXISTS idx_incidents_status_started 
ON incidents(status, started_at DESC);

-- Index for assigned incidents
CREATE INDEX IF NOT EXISTS idx_incidents_assigned_to_status 
ON incidents(assigned_to, status) 
WHERE assigned_to IS NOT NULL;

-- ============================================================================
-- Materialized View for Dashboard Metrics (Optional - Uncomment if needed)
-- ============================================================================

-- CREATE MATERIALIZED VIEW IF NOT EXISTS alert_metrics_hourly AS
-- SELECT 
--     date_trunc('hour', created_at) as hour,
--     severity,
--     status,
--     source,
--     COUNT(*) as count,
--     AVG(EXTRACT(EPOCH FROM (COALESCE(resolved_at, NOW()) - created_at))/60) as avg_resolution_minutes
-- FROM alerts
-- WHERE created_at > NOW() - INTERVAL '7 days'
-- GROUP BY date_trunc('hour', created_at), severity, status, source;

-- CREATE INDEX IF NOT EXISTS idx_alert_metrics_hourly_hour 
-- ON alert_metrics_hourly(hour DESC);

-- -- Refresh materialized view every 5 minutes (setup cron job)
-- -- REFRESH MATERIALIZED VIEW CONCURRENTLY alert_metrics_hourly;

-- ============================================================================
-- Analyze Tables for Query Planner
-- ============================================================================

ANALYZE alerts;
ANALYZE users;
ANALYZE user_sessions;
ANALYZE alert_history;
ANALYZE incidents;

-- ============================================================================
-- Comments
-- ============================================================================

COMMENT ON INDEX idx_alerts_status_created_at IS 'Optimizes listing alerts by status and time';
COMMENT ON INDEX idx_alerts_unresolved IS 'Partial index for unresolved alerts (most common query)';
COMMENT ON INDEX idx_alerts_metadata_cluster IS 'GIN index for cluster metadata queries';
