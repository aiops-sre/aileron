-- Performance Optimization Indexes for Alerts Table
-- This migration adds indexes to significantly speed up alert queries

-- Index on created_at for sorting and time-based filtering (most common sort)
CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at DESC);

-- Index on status for filtering by alert status
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);

-- Index on severity for filtering by severity level
CREATE INDEX IF NOT EXISTS idx_alerts_severity ON alerts(severity);

-- Composite index for status + created_at (very common query pattern)
CREATE INDEX IF NOT EXISTS idx_alerts_status_created_at ON alerts(status, created_at DESC);

-- Composite index for severity + created_at (common for critical alert queries)
CREATE INDEX IF NOT EXISTS idx_alerts_severity_created_at ON alerts(severity, created_at DESC);

-- Index on source for filtering by alert source
CREATE INDEX IF NOT EXISTS idx_alerts_source ON alerts(source);

-- Index on source_id for webhook lookups (deduplication)
CREATE INDEX IF NOT EXISTS idx_alerts_source_id ON alerts(source, source_id);

-- Index on assigned_to for filtering user's assigned alerts
CREATE INDEX IF NOT EXISTS idx_alerts_assigned_to ON alerts(assigned_to) WHERE assigned_to IS NOT NULL;

-- Index on acknowledged_by for tracking acknowledgments
CREATE INDEX IF NOT EXISTS idx_alerts_acknowledged_by ON alerts(acknowledged_by) WHERE acknowledged_by IS NOT NULL;

-- Index on resolved_by for resolution tracking
CREATE INDEX IF NOT EXISTS idx_alerts_resolved_by ON alerts(resolved_by) WHERE resolved_by IS NOT NULL;

-- Composite index for open/unresolved alerts (very common query)
CREATE INDEX IF NOT EXISTS idx_alerts_open ON alerts(status, severity, created_at DESC) 
WHERE status IN ('open', 'acknowledged', 'investigating');

-- GIN index on metadata for JSON queries (if using PostgreSQL JSONB queries)
CREATE INDEX IF NOT EXISTS idx_alerts_metadata_gin ON alerts USING GIN(metadata) 
WHERE metadata IS NOT NULL;

-- Index on fingerprint for deduplication lookups
CREATE INDEX IF NOT EXISTS idx_alerts_fingerprint ON alerts(fingerprint);

-- Analyze table to update query planner statistics
ANALYZE alerts;

-- Display index information
SELECT 
    schemaname,
    tablename,
    indexname,
    pg_size_pretty(pg_relation_size(indexrelid)) as index_size
FROM pg_indexes
LEFT JOIN pg_stat_user_indexes USING (schemaname, tablename, indexname)
WHERE tablename = 'alerts'
ORDER BY indexname;
