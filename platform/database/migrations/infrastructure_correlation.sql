-- Advanced Infrastructure-Aware Alert Correlation
-- Identifies root causes like KVM restart → VM alerts

-- Table for infrastructure correlations
CREATE TABLE IF NOT EXISTS infrastructure_correlations (
    id TEXT PRIMARY KEY,
    correlation_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Add root cause tracking to alerts
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS root_cause JSONB DEFAULT NULL;

-- Index for fast correlation lookups
CREATE INDEX IF NOT EXISTS idx_alerts_correlation ON alerts(correlation_id) WHERE correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_root_cause ON alerts USING GIN (root_cause) WHERE root_cause IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_infra_corr_created ON infrastructure_correlations(created_at);

-- Comments
COMMENT ON TABLE infrastructure_correlations IS 'Stores infrastructure-aware alert correlations with root cause analysis';
COMMENT ON COLUMN alerts.root_cause IS 'Identified root cause for this alert (KVM restart, network failure, etc.)';
