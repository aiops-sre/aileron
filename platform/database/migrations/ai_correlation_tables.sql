-- AI Correlation Engine Database Schema
-- Create tables for AI correlation results, feedback, and analytics

-- AI correlation results table
CREATE TABLE IF NOT EXISTS ai_correlation_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL,
    incident_id UUID,
    confidence FLOAT NOT NULL DEFAULT 0.0,
    strategies_used JSONB DEFAULT '[]',
    strategy_scores JSONB DEFAULT '{}',
    dominant_strategy VARCHAR(100),
    reasoning TEXT,
    confidence_details JSONB DEFAULT '{}',
    processing_time_ms FLOAT DEFAULT 0.0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT unique_alert_correlation UNIQUE (alert_id)
);

-- Correlation feedback table for ML learning
CREATE TABLE IF NOT EXISTS ai_correlation_feedback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    correlation_id VARCHAR(255) NOT NULL,
    alert_id VARCHAR(255) NOT NULL,
    incident_id VARCHAR(255),
    correct_correlation BOOLEAN NOT NULL,
    expected_incident_id VARCHAR(255),
    confidence_rating FLOAT NOT NULL DEFAULT 0.0,
    feedback_notes TEXT,
    submitted_by VARCHAR(255) NOT NULL,
    submitted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT valid_confidence_rating CHECK (confidence_rating >= 0 AND confidence_rating <= 1)
);

-- Enhanced correlation rules table (if not exists from previous migrations)
CREATE TABLE IF NOT EXISTS correlation_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    conditions JSONB NOT NULL DEFAULT '[]',
    actions JSONB DEFAULT '[]',
    priority INTEGER DEFAULT 1,
    enabled BOOLEAN DEFAULT TRUE,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Alert correlations table (enhanced)
CREATE TABLE IF NOT EXISTS alert_correlations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL UNIQUE,
    correlation_id VARCHAR(255) NOT NULL,
    similar_alerts JSONB DEFAULT '[]',
    confidence_score FLOAT DEFAULT 0.0,
    correlation_type VARCHAR(100) DEFAULT 'traditional',
    recommended_action VARCHAR(100),
    is_duplicate BOOLEAN DEFAULT FALSE,
    duplicate_of UUID,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Performance indexes for AI correlation
CREATE INDEX IF NOT EXISTS idx_ai_correlation_results_alert_id ON ai_correlation_results(alert_id);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_results_incident_id ON ai_correlation_results(incident_id);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_results_created_at ON ai_correlation_results(created_at);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_results_confidence ON ai_correlation_results(confidence);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_results_dominant_strategy ON ai_correlation_results(dominant_strategy);

CREATE INDEX IF NOT EXISTS idx_ai_correlation_feedback_correlation_id ON ai_correlation_feedback(correlation_id);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_feedback_alert_id ON ai_correlation_feedback(alert_id);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_feedback_submitted_at ON ai_correlation_feedback(submitted_at);
CREATE INDEX IF NOT EXISTS idx_ai_correlation_feedback_correct ON ai_correlation_feedback(correct_correlation);

CREATE INDEX IF NOT EXISTS idx_correlation_rules_enabled ON correlation_rules(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_correlation_rules_priority ON correlation_rules(priority DESC);

CREATE INDEX IF NOT EXISTS idx_alert_correlations_alert_id ON alert_correlations(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_correlations_correlation_id ON alert_correlations(correlation_id);
CREATE INDEX IF NOT EXISTS idx_alert_correlations_confidence ON alert_correlations(confidence_score);

-- AI correlation analytics view
CREATE OR REPLACE VIEW ai_correlation_analytics AS
SELECT 
    COUNT(*) as total_correlations,
    COUNT(CASE WHEN incident_id IS NOT NULL THEN 1 END) as successful_correlations,
    AVG(confidence) as avg_confidence,
    AVG(processing_time_ms) as avg_processing_time,
    dominant_strategy,
    DATE_TRUNC('hour', created_at) as hour_bucket,
    COUNT(*) as correlations_per_hour
FROM ai_correlation_results
GROUP BY dominant_strategy, DATE_TRUNC('hour', created_at)
ORDER BY hour_bucket DESC;

-- Feedback analytics view  
CREATE OR REPLACE VIEW ai_correlation_feedback_analytics AS
SELECT
    COUNT(*) as total_feedback,
    COUNT(CASE WHEN correct_correlation = true THEN 1 END) as correct_correlations,
    COUNT(CASE WHEN correct_correlation = false THEN 1 END) as incorrect_correlations,
    AVG(confidence_rating) as avg_human_confidence,
    DATE_TRUNC('day', submitted_at) as day_bucket
FROM ai_correlation_feedback
GROUP BY DATE_TRUNC('day', submitted_at)
ORDER BY day_bucket DESC;

-- Strategy performance view
CREATE OR REPLACE VIEW ai_strategy_performance AS
SELECT 
    dominant_strategy,
    COUNT(*) as usage_count,
    AVG(confidence) as avg_confidence,
    AVG(processing_time_ms) as avg_processing_time,
    COUNT(CASE WHEN acf.correct_correlation = true THEN 1 END) as correct_predictions,
    COUNT(CASE WHEN acf.correct_correlation = false THEN 1 END) as incorrect_predictions,
    CASE 
        WHEN COUNT(acf.correct_correlation) > 0 THEN
            COUNT(CASE WHEN acf.correct_correlation = true THEN 1 END)::float / COUNT(acf.correct_correlation)::float
        ELSE NULL
    END as accuracy_rate
FROM ai_correlation_results acr
LEFT JOIN ai_correlation_feedback acf ON acr.alert_id::text = acf.alert_id
WHERE dominant_strategy IS NOT NULL
GROUP BY dominant_strategy
ORDER BY usage_count DESC;

-- Alert correlation trends view
CREATE OR REPLACE VIEW alert_correlation_trends AS
SELECT 
    DATE_TRUNC('day', created_at) as day,
    COUNT(*) as total_alerts,
    COUNT(CASE WHEN incident_id IS NOT NULL THEN 1 END) as correlated_alerts,
    AVG(confidence) as avg_confidence,
    COUNT(DISTINCT incident_id) as unique_incidents,
    AVG(processing_time_ms) as avg_processing_time
FROM ai_correlation_results
GROUP BY DATE_TRUNC('day', created_at)
ORDER BY day DESC;

COMMENT ON TABLE ai_correlation_results IS 'Stores results from AI Correlation Engine for analytics and auditing';
COMMENT ON TABLE ai_correlation_feedback IS 'Human feedback on AI correlation accuracy for machine learning improvement';
COMMENT ON TABLE correlation_rules IS 'User-defined correlation rules for rule-based strategy';
COMMENT ON VIEW ai_correlation_analytics IS 'Real-time analytics for AI correlation performance monitoring';
COMMENT ON VIEW ai_strategy_performance IS 'Performance comparison between different correlation strategies';
COMMENT ON VIEW alert_correlation_trends IS 'Daily trends in alert correlation patterns and performance';