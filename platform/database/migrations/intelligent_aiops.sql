-- ============================================================================
-- Intelligent AIOps Platform - Database Schema
-- ============================================================================

-- Alert Bursts Table - Tracks burst patterns and alert storms
CREATE TABLE IF NOT EXISTS alert_bursts (
    id UUID PRIMARY KEY,
    pattern VARCHAR(50) NOT NULL,
    pattern_value VARCHAR(255) NOT NULL,
    alert_count INTEGER NOT NULL DEFAULT 0,
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP,
    duration INTERVAL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    severity VARCHAR(30) NOT NULL,
    affected_sources TEXT[],
    affected_severities TEXT[],
    alert_ids JSONB,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_alert_bursts_status ON alert_bursts(status);
CREATE INDEX idx_alert_bursts_pattern ON alert_bursts(pattern, pattern_value);
CREATE INDEX idx_alert_bursts_start_time ON alert_bursts(start_time);

-- Alert Storms Table - Tracks severe alert storms
CREATE TABLE IF NOT EXISTS alert_storms (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    alerts_per_minute FLOAT NOT NULL,
    total_alerts INTEGER NOT NULL DEFAULT 0,
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP,
    duration INTERVAL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    impact_level VARCHAR(20) NOT NULL,
    affected_services TEXT[],
    root_cause_hypothesis TEXT,
    mitigation_actions JSONB,
    related_bursts JSONB,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_alert_storms_status ON alert_storms(status);
CREATE INDEX idx_alert_storms_impact ON alert_storms(impact_level);
CREATE INDEX idx_alert_storms_start_time ON alert_storms(start_time);

-- Agentic Tasks Table - Tracks tasks for the multi-agent system
CREATE TABLE IF NOT EXISTS agentic_tasks (
    id UUID PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    priority INTEGER NOT NULL DEFAULT 5,
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    incident_id UUID,
    payload JSONB,
    assigned_agent VARCHAR(100),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    result JSONB,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    dependencies JSONB,
    parent_task_id UUID REFERENCES agentic_tasks(id),
    child_tasks JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    started_at TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX idx_agentic_tasks_status ON agentic_tasks(status);
CREATE INDEX idx_agentic_tasks_type ON agentic_tasks(type);
CREATE INDEX idx_agentic_tasks_agent ON agentic_tasks(assigned_agent);
CREATE INDEX idx_agentic_tasks_alert ON agentic_tasks(alert_id);
CREATE INDEX idx_agentic_tasks_priority ON agentic_tasks(priority DESC);

-- Agent Messages Table - Tracks inter-agent communication
CREATE TABLE IF NOT EXISTS agent_messages (
    id UUID PRIMARY KEY,
    from_agent VARCHAR(100) NOT NULL,
    to_agent VARCHAR(100) NOT NULL,
    message_type VARCHAR(50) NOT NULL,
    content JSONB,
    task_id UUID REFERENCES agentic_tasks(id),
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_agent_messages_to ON agent_messages(to_agent);
CREATE INDEX idx_agent_messages_from ON agent_messages(from_agent);
CREATE INDEX idx_agent_messages_task ON agent_messages(task_id);

-- Anomaly Detection Table - Tracks detected anomalies
CREATE TABLE IF NOT EXISTS anomaly_detections (
    id UUID PRIMARY KEY,
    anomaly_type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    metric_name VARCHAR(255) NOT NULL,
    metric_value FLOAT NOT NULL,
    expected_value FLOAT,
    deviation_score FLOAT NOT NULL,
    confidence FLOAT NOT NULL,
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    time_window INTERVAL,
    context JSONB,
    detected_at TIMESTAMP DEFAULT NOW(),
    resolved_at TIMESTAMP
);

CREATE INDEX idx_anomaly_type ON anomaly_detections(anomaly_type);
CREATE INDEX idx_anomaly_severity ON anomaly_detections(severity);
CREATE INDEX idx_anomaly_detected_at ON anomaly_detections(detected_at);
CREATE INDEX idx_anomaly_alert ON anomaly_detections(alert_id);

-- ML Models Table - Tracks machine learning models
CREATE TABLE IF NOT EXISTS ml_models (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    model_type VARCHAR(100) NOT NULL,
    version VARCHAR(50) NOT NULL,
    description TEXT,
    accuracy FLOAT,
    precision_score FLOAT,
    recall_score FLOAT,
    f1_score FLOAT,
    training_data_size INTEGER,
    features JSONB,
    hyperparameters JSONB,
    model_path VARCHAR(500),
    status VARCHAR(20) NOT NULL DEFAULT 'trained',
    trained_at TIMESTAMP,
    deployed_at TIMESTAMP,
    last_prediction_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_ml_models_type ON ml_models(model_type);
CREATE INDEX idx_ml_models_status ON ml_models(status);
CREATE INDEX idx_ml_models_version ON ml_models(version);

-- Predictions Table - Stores ML model predictions
CREATE TABLE IF NOT EXISTS predictions (
    id UUID PRIMARY KEY,
    model_id UUID REFERENCES ml_models(id),
    prediction_type VARCHAR(100) NOT NULL,
    input_data JSONB,
    prediction_value JSONB,
    confidence FLOAT,
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    incident_id UUID,
    actual_outcome JSONB,
    accuracy_score FLOAT,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_predictions_model ON predictions(model_id);
CREATE INDEX idx_predictions_type ON predictions(prediction_type);
CREATE INDEX idx_predictions_alert ON predictions(alert_id);
CREATE INDEX idx_predictions_created ON predictions(created_at);

-- NLP Analysis Table - Stores natural language processing results
CREATE TABLE IF NOT EXISTS nlp_analysis (
    id UUID PRIMARY KEY,
    text_source VARCHAR(100) NOT NULL,
    original_text TEXT NOT NULL,
    language VARCHAR(10) DEFAULT 'en',
    entities JSONB,
    keywords JSONB,
    sentiment VARCHAR(20),
    sentiment_score FLOAT,
    categories JSONB,
    topics JSONB,
    intent VARCHAR(100),
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    analyzed_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_nlp_source ON nlp_analysis(text_source);
CREATE INDEX idx_nlp_alert ON nlp_analysis(alert_id);
CREATE INDEX idx_nlp_sentiment ON nlp_analysis(sentiment);

-- Feedback Loop Table - Tracks user feedback for self-learning
CREATE TABLE IF NOT EXISTS feedback_loops (
    id UUID PRIMARY KEY,
    entity_type VARCHAR(50) NOT NULL,
    entity_id UUID NOT NULL,
    feedback_type VARCHAR(50) NOT NULL,
    feedback_value INTEGER,
    feedback_text TEXT,
    user_id UUID REFERENCES users(id),
    context JSONB,
    applied BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    applied_at TIMESTAMP
);

CREATE INDEX idx_feedback_entity ON feedback_loops(entity_type, entity_id);
CREATE INDEX idx_feedback_type ON feedback_loops(feedback_type);
CREATE INDEX idx_feedback_applied ON feedback_loops(applied);
CREATE INDEX idx_feedback_user ON feedback_loops(user_id);

-- Time Series Metrics Table - Stores time-series data for analysis
CREATE TABLE IF NOT EXISTS time_series_metrics (
    id UUID PRIMARY KEY,
    metric_name VARCHAR(255) NOT NULL,
    metric_value FLOAT NOT NULL,
    metric_type VARCHAR(50) NOT NULL,
    source VARCHAR(100) NOT NULL,
    labels JSONB,
    timestamp TIMESTAMP NOT NULL,
    aggregation_window INTERVAL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_ts_metrics_name ON time_series_metrics(metric_name);
CREATE INDEX idx_ts_metrics_timestamp ON time_series_metrics(timestamp);
CREATE INDEX idx_ts_metrics_source ON time_series_metrics(source);

-- Runbooks Table - Stores automated remediation runbooks
CREATE TABLE IF NOT EXISTS runbooks (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    trigger_conditions JSONB NOT NULL,
    steps JSONB NOT NULL,
    required_approvals INTEGER DEFAULT 0,
    auto_execute BOOLEAN DEFAULT FALSE,
    success_rate FLOAT,
    execution_count INTEGER DEFAULT 0,
    average_duration INTERVAL,
    tags TEXT[],
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_runbooks_name ON runbooks(name);
CREATE INDEX idx_runbooks_auto_execute ON runbooks(auto_execute);

-- Runbook Executions Table - Tracks runbook executions
CREATE TABLE IF NOT EXISTS runbook_executions (
    id UUID PRIMARY KEY,
    runbook_id UUID REFERENCES runbooks(id),
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    incident_id UUID,
    status VARCHAR(20) NOT NULL DEFAULT 'running',
    current_step INTEGER DEFAULT 0,
    total_steps INTEGER NOT NULL,
    step_results JSONB,
    error_message TEXT,
    started_by UUID REFERENCES users(id),
    started_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP,
    duration INTERVAL
);

CREATE INDEX idx_runbook_exec_runbook ON runbook_executions(runbook_id);
CREATE INDEX idx_runbook_exec_status ON runbook_executions(status);
CREATE INDEX idx_runbook_exec_alert ON runbook_executions(alert_id);

-- Pattern Library Table - Stores learned patterns
CREATE TABLE IF NOT EXISTS pattern_library (
    id UUID PRIMARY KEY,
    pattern_type VARCHAR(100) NOT NULL,
    pattern_name VARCHAR(255) NOT NULL,
    pattern_definition JSONB NOT NULL,
    occurrence_count INTEGER DEFAULT 1,
    confidence FLOAT NOT NULL,
    last_seen TIMESTAMP NOT NULL,
    first_seen TIMESTAMP NOT NULL,
    tags TEXT[],
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_pattern_type ON pattern_library(pattern_type);
CREATE INDEX idx_pattern_confidence ON pattern_library(confidence);
CREATE INDEX idx_pattern_last_seen ON pattern_library(last_seen);

-- Alert Streaming Events Table - For real-time processing
CREATE TABLE IF NOT EXISTS alert_stream_events (
    id UUID PRIMARY KEY,
    event_type VARCHAR(50) NOT NULL,
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    event_data JSONB NOT NULL,
    processed BOOLEAN DEFAULT FALSE,
    processing_errors TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    processed_at TIMESTAMP
);

CREATE INDEX idx_stream_processed ON alert_stream_events(processed);
CREATE INDEX idx_stream_type ON alert_stream_events(event_type);
CREATE INDEX idx_stream_created ON alert_stream_events(created_at);

-- Comments and Notes
COMMENT ON TABLE alert_bursts IS 'Tracks alert burst patterns and storms for intelligent suppression';
COMMENT ON TABLE agentic_tasks IS 'Tasks for the multi-agent system to process';
COMMENT ON TABLE anomaly_detections IS 'Anomalies detected by ML models';
COMMENT ON TABLE ml_models IS 'Trained machine learning models for various predictions';
COMMENT ON TABLE predictions IS 'Predictions made by ML models';
COMMENT ON TABLE nlp_analysis IS 'Natural language processing results for alert text';
COMMENT ON TABLE feedback_loops IS 'User feedback for continuous learning and improvement';
COMMENT ON TABLE runbooks IS 'Automated remediation runbooks';
COMMENT ON TABLE pattern_library IS 'Learned patterns from historical data';
COMMENT ON TABLE alert_stream_events IS 'Real-time alert processing events';

-- Grant permissions (adjust as needed)
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO alerthub_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO alerthub_user;
