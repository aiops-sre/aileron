-- Davis AI Correlation Engine Database Schema
-- This migration adds support for Davis AI-like correlation with automatic incident creation

-- Table for storing Davis AI correlation results
CREATE TABLE IF NOT EXISTS davis_ai_correlations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    correlation_id VARCHAR(255) NOT NULL,
    correlation_type VARCHAR(100) NOT NULL,
    confidence_score DECIMAL(5,4) NOT NULL,
    correlation_result JSONB NOT NULL DEFAULT '{}',
    processing_time_ms BIGINT DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(alert_id)
);

-- Table for storing historical patterns (learning engine)
CREATE TABLE IF NOT EXISTS historical_patterns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern_id VARCHAR(255) NOT NULL,
    pattern_type VARCHAR(100) NOT NULL, -- 'recurring', 'seasonal', 'cascade', etc.
    alert_source VARCHAR(100) NOT NULL,
    alert_title TEXT,
    alert_description TEXT,
    frequency INTEGER DEFAULT 1,
    confidence DECIMAL(5,4) DEFAULT 0.0,
    typical_resolution TEXT,
    avg_resolution_time_seconds BIGINT DEFAULT 0,
    last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(pattern_id, alert_source)
);

-- Table for Ollama service configuration and status
CREATE TABLE IF NOT EXISTS ollama_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_url VARCHAR(255) NOT NULL DEFAULT 'http://localhost:11434',
    model_name VARCHAR(100) NOT NULL DEFAULT 'llama2',
    enabled BOOLEAN DEFAULT TRUE,
    last_health_check TIMESTAMP WITH TIME ZONE,
    health_status VARCHAR(50) DEFAULT 'unknown',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Add correlation metadata to existing alerts table if not exists
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS davis_correlation_id VARCHAR(255);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS auto_created_incident_id UUID REFERENCES incidents(id) ON DELETE SET NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS correlation_confidence DECIMAL(5,4) DEFAULT 0.0;

-- Add Davis AI fields to incidents table if not exists
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS auto_created BOOLEAN DEFAULT FALSE;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS correlation_type VARCHAR(100);
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS davis_ai_analysis JSONB DEFAULT '{}';

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_davis_correlations_alert_id ON davis_ai_correlations(alert_id);
CREATE INDEX IF NOT EXISTS idx_davis_correlations_correlation_id ON davis_ai_correlations(correlation_id);
CREATE INDEX IF NOT EXISTS idx_davis_correlations_confidence ON davis_ai_correlations(confidence_score DESC);
CREATE INDEX IF NOT EXISTS idx_davis_correlations_created_at ON davis_ai_correlations(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_historical_patterns_source ON historical_patterns(alert_source);
CREATE INDEX IF NOT EXISTS idx_historical_patterns_type ON historical_patterns(pattern_type);
CREATE INDEX IF NOT EXISTS idx_historical_patterns_confidence ON historical_patterns(confidence DESC);
CREATE INDEX IF NOT EXISTS idx_historical_patterns_frequency ON historical_patterns(frequency DESC);
CREATE INDEX IF NOT EXISTS idx_historical_patterns_title ON historical_patterns USING gin(to_tsvector('english', alert_title));

CREATE INDEX IF NOT EXISTS idx_alerts_davis_correlation ON alerts(davis_correlation_id) WHERE davis_correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_auto_incident ON alerts(auto_created_incident_id) WHERE auto_created_incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_correlation_confidence ON alerts(correlation_confidence DESC);

CREATE INDEX IF NOT EXISTS idx_incidents_auto_created ON incidents(auto_created) WHERE auto_created = TRUE;
CREATE INDEX IF NOT EXISTS idx_incidents_correlation_type ON incidents(correlation_type) WHERE correlation_type IS NOT NULL;

-- Insert default Ollama configuration
INSERT INTO ollama_config (service_url, model_name, enabled) 
VALUES ('http://localhost:11434', 'llama2', TRUE)
ON CONFLICT DO NOTHING;

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_davis_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_davis_ai_correlations_updated_at 
    BEFORE UPDATE ON davis_ai_correlations
    FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();

CREATE TRIGGER update_historical_patterns_updated_at 
    BEFORE UPDATE ON historical_patterns
    FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();

CREATE TRIGGER update_ollama_config_updated_at 
    BEFORE UPDATE ON ollama_config
    FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();

-- Sample historical patterns for testing
INSERT INTO historical_patterns (pattern_id, pattern_type, alert_source, alert_title, frequency, confidence, typical_resolution, avg_resolution_time_seconds) VALUES
('db-connection-pattern', 'recurring', 'dynatrace', 'Database connection timeout', 15, 0.85, 'Restart database connection pool', 900),
('memory-leak-pattern', 'cascade', 'prometheus', 'High memory usage', 8, 0.78, 'Restart application service', 1800),
('network-timeout-pattern', 'seasonal', 'grafana', 'Network timeout detected', 12, 0.72, 'Check network infrastructure', 600),
('disk-space-pattern', 'recurring', 'zabbix', 'Disk space critical', 20, 0.90, 'Clean up temporary files', 300)
ON CONFLICT (pattern_id, alert_source) DO NOTHING;