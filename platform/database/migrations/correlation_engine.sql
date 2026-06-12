-- Migration: Add alert correlation and deduplication tables
-- Version: correlation_v1.0.0

-- Alert correlations table
CREATE TABLE IF NOT EXISTS alert_correlations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    correlation_id VARCHAR(255) NOT NULL,
    similar_alerts JSONB DEFAULT '[]',
    confidence_score DECIMAL(3,2) DEFAULT 0.0,
    correlation_type VARCHAR(50) NOT NULL DEFAULT 'ml_similarity',
    recommended_action VARCHAR(100) NOT NULL DEFAULT 'correlate',
    is_duplicate BOOLEAN DEFAULT FALSE,
    duplicate_of UUID REFERENCES alerts(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(alert_id)
);

-- Correlation rules table for user-defined correlation rules
CREATE TABLE IF NOT EXISTS correlation_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    conditions JSONB NOT NULL DEFAULT '[]',
    actions JSONB NOT NULL DEFAULT '[]',
    priority INTEGER DEFAULT 0,
    enabled BOOLEAN DEFAULT TRUE,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name)
);

-- Alert similarity cache for performance
CREATE TABLE IF NOT EXISTS alert_similarity_cache (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert1_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    alert2_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    title_similarity DECIMAL(3,2) DEFAULT 0.0,
    description_similarity DECIMAL(3,2) DEFAULT 0.0,
    source_similarity DECIMAL(3,2) DEFAULT 0.0,
    tag_similarity DECIMAL(3,2) DEFAULT 0.0,
    label_similarity DECIMAL(3,2) DEFAULT 0.0,
    time_similarity DECIMAL(3,2) DEFAULT 0.0,
    overall_similarity DECIMAL(3,2) DEFAULT 0.0,
    calculated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(alert1_id, alert2_id)
);

-- Correlation clusters table for grouping correlated alerts
CREATE TABLE IF NOT EXISTS correlation_clusters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    description TEXT,
    alert_ids JSONB NOT NULL DEFAULT '[]',
    cluster_type VARCHAR(50) DEFAULT 'similarity',
    confidence_score DECIMAL(3,2) DEFAULT 0.0,
    status VARCHAR(50) DEFAULT 'active',
    root_alert_id UUID REFERENCES alerts(id) ON DELETE SET NULL,
    incident_id UUID REFERENCES incidents(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(cluster_id)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_alert_correlations_alert_id ON alert_correlations(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_correlations_correlation_id ON alert_correlations(correlation_id);
CREATE INDEX IF NOT EXISTS idx_alert_correlations_duplicate_of ON alert_correlations(duplicate_of) WHERE duplicate_of IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_correlations_created_at ON alert_correlations(created_at);

CREATE INDEX IF NOT EXISTS idx_correlation_rules_enabled ON correlation_rules(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_correlation_rules_priority ON correlation_rules(priority DESC);

CREATE INDEX IF NOT EXISTS idx_alert_similarity_cache_alert1 ON alert_similarity_cache(alert1_id);
CREATE INDEX IF NOT EXISTS idx_alert_similarity_cache_alert2 ON alert_similarity_cache(alert2_id);
CREATE INDEX IF NOT EXISTS idx_alert_similarity_cache_similarity ON alert_similarity_cache(overall_similarity DESC);

CREATE INDEX IF NOT EXISTS idx_correlation_clusters_cluster_id ON correlation_clusters(cluster_id);
CREATE INDEX IF NOT EXISTS idx_correlation_clusters_status ON correlation_clusters(status);
CREATE INDEX IF NOT EXISTS idx_correlation_clusters_incident_id ON correlation_clusters(incident_id) WHERE incident_id IS NOT NULL;

-- Add enhanced fingerprint column to alerts table if not exists
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS enhanced_fingerprint VARCHAR(32);
CREATE INDEX IF NOT EXISTS idx_alerts_enhanced_fingerprint ON alerts(enhanced_fingerprint) WHERE enhanced_fingerprint IS NOT NULL;

-- Add correlation metadata to alerts table
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS is_correlated BOOLEAN DEFAULT FALSE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS correlation_parent_id UUID REFERENCES alerts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_alerts_correlation_id ON alerts(correlation_id) WHERE correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_is_correlated ON alerts(is_correlated) WHERE is_correlated = TRUE;
CREATE INDEX IF NOT EXISTS idx_alerts_correlation_parent ON alerts(correlation_parent_id) WHERE correlation_parent_id IS NOT NULL;

-- Insert default correlation rules
INSERT INTO correlation_rules (name, description, conditions, actions, priority, enabled) VALUES
(
    'High Severity Auto-Escalate',
    'Automatically escalate high severity alerts from critical services',
    '[
        {"field": "severity", "operator": "equals", "value": "critical", "weight": 1.0},
        {"field": "source", "operator": "in", "value": ["dynatrace", "prometheus"], "weight": 0.8}
    ]',
    '[
        {"type": "escalate", "parameters": {"priority": "high"}},
        {"type": "notify", "parameters": {"channels": ["slack", "pagerduty"]}}
    ]',
    100,
    TRUE
),
(
    'Database Alert Correlation',
    'Correlate database-related alerts from the same service',
    '[
        {"field": "tags", "operator": "contains", "value": "database", "weight": 0.9},
        {"field": "labels.service", "operator": "equals", "value": "database", "weight": 0.8}
    ]',
    '[
        {"type": "correlate", "parameters": {"group_by": "service"}},
        {"type": "create_incident", "parameters": {"title": "Database Service Issue"}}
    ]',
    80,
    TRUE
),
(
    'Infrastructure Alert Grouping',
    'Group infrastructure alerts by host/node',
    '[
        {"field": "labels.component_type", "operator": "equals", "value": "infrastructure", "weight": 0.9},
        {"field": "labels.host", "operator": "exists", "value": true, "weight": 0.7}
    ]',
    '[
        {"type": "group", "parameters": {"group_by": "host"}},
        {"type": "suppress_duplicates", "parameters": {"time_window": 1800}}
    ]',
    60,
    TRUE
) ON CONFLICT (name) DO NOTHING;

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_alert_correlations_updated_at 
    BEFORE UPDATE ON alert_correlations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_correlation_rules_updated_at 
    BEFORE UPDATE ON correlation_rules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_correlation_clusters_updated_at 
    BEFORE UPDATE ON correlation_clusters
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();