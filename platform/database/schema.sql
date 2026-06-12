-- Enterprise AlertHub Database Schema
-- PostgreSQL 14+

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================================
-- RBAC Tables
-- ============================================================================

-- Roles Table
CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    is_system_role BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_roles_name ON roles(name);

-- Permissions Table
CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) UNIQUE NOT NULL,
    resource VARCHAR(100) NOT NULL,
    action VARCHAR(50) NOT NULL,
    description TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_permissions_resource ON permissions(resource);
CREATE INDEX idx_permissions_action ON permissions(action);

-- Role Permissions Junction Table
CREATE TABLE role_permissions (
    role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID REFERENCES permissions(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id)
);

-- ============================================================================
-- User Management Tables
-- ============================================================================

-- Users Table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username VARCHAR(255) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    full_name VARCHAR(255),
    role_id UUID REFERENCES roles(id),
    is_active BOOLEAN DEFAULT true,
    is_verified BOOLEAN DEFAULT false,
    mfa_enabled BOOLEAN DEFAULT false,
    mfa_secret VARCHAR(255),
    last_login TIMESTAMP,
    login_count INTEGER DEFAULT 0,
    failed_login_attempts INTEGER DEFAULT 0,
    locked_until TIMESTAMP,
    avatar_url TEXT,
    phone VARCHAR(50),
    timezone VARCHAR(100) DEFAULT 'UTC',
    preferences JSONB DEFAULT '{}',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_role_id ON users(role_id);
CREATE INDEX idx_users_is_active ON users(is_active);

-- User Sessions Table
CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) NOT NULL,
    refresh_token_hash VARCHAR(255),
    ip_address VARCHAR(45),
    user_agent TEXT,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_token_hash ON user_sessions(token_hash);
CREATE INDEX idx_user_sessions_expires_at ON user_sessions(expires_at);

-- ============================================================================
-- Alert Management Tables
-- ============================================================================

-- Alerts Table
CREATE TABLE alerts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title VARCHAR(500) NOT NULL,
    description TEXT,
    severity VARCHAR(50) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info')),
    status VARCHAR(50) NOT NULL CHECK (status IN ('open', 'acknowledged', 'investigating', 'resolved', 'closed')),
    source VARCHAR(100),
    source_id VARCHAR(255),
    source_url TEXT,
    tags JSONB DEFAULT '[]',
    labels JSONB DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    assigned_to UUID REFERENCES users(id),
    created_by UUID REFERENCES users(id),
    team_id UUID,
    ai_analysis JSONB,
    ai_classification VARCHAR(100),
    ai_confidence DECIMAL(5,2),
    fingerprint VARCHAR(255),
    count INTEGER DEFAULT 1,
    first_seen_at TIMESTAMP DEFAULT NOW(),
    last_seen_at TIMESTAMP DEFAULT NOW(),
    acknowledged_at TIMESTAMP,
    acknowledged_by UUID REFERENCES users(id),
    resolved_at TIMESTAMP,
    resolved_by UUID REFERENCES users(id),
    resolution_notes TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    
    -- AUTONOMOUS AIOPS ENHANCEMENTS
    entity_type VARCHAR(50),
    entity_id VARCHAR(255),
    region VARCHAR(50),
    topology_path VARCHAR(500),
    blast_radius_score DECIMAL(5,2) DEFAULT 0.0,
    autonomous_analysis JSONB,
    rca_confidence DECIMAL(5,2) DEFAULT 0.0,
    root_cause_entity VARCHAR(255),
    agent_processed BOOLEAN DEFAULT FALSE,
    agent_decision VARCHAR(100)
   );

-- Create indexes
CREATE INDEX idx_alerts_severity ON alerts(severity);
CREATE INDEX idx_alerts_status ON alerts(status);
CREATE INDEX idx_alerts_source ON alerts(source);
CREATE INDEX idx_alerts_assigned_to ON alerts(assigned_to);
CREATE INDEX idx_alerts_created_at ON alerts(created_at DESC);
CREATE INDEX idx_alerts_fingerprint ON alerts(fingerprint);
CREATE INDEX idx_alerts_tags ON alerts USING GIN(tags);
-- Indexes for autonomous AIOps fields
CREATE INDEX idx_alerts_entity_type ON alerts(entity_type);
CREATE INDEX idx_alerts_entity_id ON alerts(entity_id);
CREATE INDEX idx_alerts_region ON alerts(region);
CREATE INDEX idx_alerts_agent_processed ON alerts(agent_processed);
CREATE INDEX idx_alerts_agent_decision ON alerts(agent_decision);

-- Alert History Table
CREATE TABLE alert_history (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),
    action VARCHAR(100) NOT NULL,
    old_value JSONB,
    new_value JSONB,
    comment TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_alert_history_alert_id ON alert_history(alert_id);
CREATE INDEX idx_alert_history_created_at ON alert_history(created_at DESC);

-- ============================================================================
-- Incident Management Tables
-- ============================================================================

-- Incidents Table
CREATE TABLE incidents (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    incident_number SERIAL UNIQUE,
    title VARCHAR(500) NOT NULL,
    description TEXT,
    severity VARCHAR(50) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low')),
    status VARCHAR(50) NOT NULL CHECK (status IN ('open', 'investigating', 'identified', 'monitoring', 'resolved', 'closed')),
    priority VARCHAR(50) CHECK (priority IN ('p1', 'p2', 'p3', 'p4')),
    impact VARCHAR(50),
    urgency VARCHAR(50),
    assigned_to UUID REFERENCES users(id),
    created_by UUID REFERENCES users(id),
    team_id UUID,
    alert_ids UUID[],
    related_incident_ids UUID[],
    timeline JSONB DEFAULT '[]',
    ai_insights JSONB,
    ai_root_cause TEXT,
    ai_recommendations JSONB,
    resolution_notes TEXT,
    post_mortem_url TEXT,
    started_at TIMESTAMP DEFAULT NOW(),
    detected_at TIMESTAMP,
    acknowledged_at TIMESTAMP,
    resolved_at TIMESTAMP,
    closed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    
    -- Correlation and auto-creation fields
    correlation_id VARCHAR(255),
    auto_created BOOLEAN DEFAULT FALSE,
    source VARCHAR(100) DEFAULT 'manual',
    correlation_type VARCHAR(100),
    correlation_confidence DECIMAL(5,2),
    auto_creation_metadata JSONB DEFAULT '{}'
);

-- Create indexes
CREATE INDEX idx_incidents_severity ON incidents(severity);
CREATE INDEX idx_incidents_status ON incidents(status);
CREATE INDEX idx_incidents_assigned_to ON incidents(assigned_to);
CREATE INDEX idx_incidents_created_at ON incidents(created_at DESC);
CREATE INDEX idx_incidents_incident_number ON incidents(incident_number);

-- Incident Timeline Table
CREATE TABLE incident_timeline (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    incident_id UUID REFERENCES incidents(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),
    event_type VARCHAR(100) NOT NULL,
    title VARCHAR(500),
    description TEXT,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_incident_timeline_incident_id ON incident_timeline(incident_id);
CREATE INDEX idx_incident_timeline_created_at ON incident_timeline(created_at DESC);

-- ============================================================================
-- Escalation & On-Call Tables
-- ============================================================================

-- Escalation Policies Table
CREATE TABLE escalation_policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    rules JSONB NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- On-Call Schedules Table
CREATE TABLE oncall_schedules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    timezone VARCHAR(100) DEFAULT 'UTC',
    rotation_type VARCHAR(50),
    rotation_config JSONB,
    users UUID[],
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- On-Call Shifts Table
CREATE TABLE oncall_shifts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    schedule_id UUID REFERENCES oncall_schedules(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP NOT NULL,
    is_override BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_oncall_shifts_schedule_id ON oncall_shifts(schedule_id);
CREATE INDEX idx_oncall_shifts_user_id ON oncall_shifts(user_id);
CREATE INDEX idx_oncall_shifts_time_range ON oncall_shifts(start_time, end_time);

-- ============================================================================
-- Notification Tables
-- ============================================================================

-- Notification Channels Table
CREATE TABLE notification_channels (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('email', 'slack', 'pagerduty', 'webhook', 'sms')),
    config JSONB NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Notification Rules Table
CREATE TABLE notification_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    conditions JSONB NOT NULL,
    channels UUID[],
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Notification Log Table
CREATE TABLE notification_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID REFERENCES notification_channels(id),
    alert_id UUID REFERENCES alerts(id),
    incident_id UUID REFERENCES incidents(id),
    recipient VARCHAR(255),
    status VARCHAR(50),
    error_message TEXT,
    sent_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_notification_log_alert_id ON notification_log(alert_id);
CREATE INDEX idx_notification_log_incident_id ON notification_log(incident_id);
CREATE INDEX idx_notification_log_sent_at ON notification_log(sent_at DESC);

-- ============================================================================
-- AI & Analytics Tables
-- ============================================================================

-- AI Models Table
CREATE TABLE ai_models (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(100) NOT NULL,
    version VARCHAR(50),
    config JSONB,
    metrics JSONB,
    is_active BOOLEAN DEFAULT true,
    trained_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- AI Predictions Table
CREATE TABLE ai_predictions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    model_id UUID REFERENCES ai_models(id),
    input_data JSONB NOT NULL,
    prediction JSONB NOT NULL,
    confidence DECIMAL(5,2),
    actual_outcome JSONB,
    feedback_score INTEGER,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_ai_predictions_model_id ON ai_predictions(model_id);
CREATE INDEX idx_ai_predictions_created_at ON ai_predictions(created_at DESC);

-- Analytics Metrics Table
CREATE TABLE analytics_metrics (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    metric_name VARCHAR(255) NOT NULL,
    metric_type VARCHAR(100),
    value DECIMAL(15,2),
    dimensions JSONB,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_analytics_metrics_name ON analytics_metrics(metric_name);
CREATE INDEX idx_analytics_metrics_timestamp ON analytics_metrics(timestamp DESC);

-- ============================================================================
-- Audit & Compliance Tables
-- ============================================================================

-- Audit Logs Table
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id),
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100),
    resource_id UUID,
    old_value JSONB,
    new_value JSONB,
    details JSONB,
    ip_address VARCHAR(45),
    user_agent TEXT,
    status VARCHAR(50),
    error_message TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_resource_type ON audit_logs(resource_type);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at DESC);

-- Compliance Reports Table
CREATE TABLE compliance_reports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_type VARCHAR(100) NOT NULL,
    period_start TIMESTAMP NOT NULL,
    period_end TIMESTAMP NOT NULL,
    data JSONB NOT NULL,
    generated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW()
);

-- ============================================================================
-- Integration Tables
-- ============================================================================

-- Integrations Table
CREATE TABLE integrations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(100) NOT NULL,
    config JSONB NOT NULL,
    credentials JSONB,
    is_active BOOLEAN DEFAULT true,
    last_sync_at TIMESTAMP,
    sync_status VARCHAR(50),
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Webhooks Table
CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    url TEXT NOT NULL,
    secret VARCHAR(255),
    events VARCHAR(100)[],
    headers JSONB,
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Webhook Deliveries Table
CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    webhook_id UUID REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type VARCHAR(100),
    payload JSONB,
    response_status INTEGER,
    response_body TEXT,
    error_message TEXT,
    delivered_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_webhook_deliveries_webhook_id ON webhook_deliveries(webhook_id);
CREATE INDEX idx_webhook_deliveries_delivered_at ON webhook_deliveries(delivered_at DESC);

-- ============================================================================
-- Alert Correlation Tables
-- ============================================================================

-- Alert correlations table
CREATE TABLE IF NOT EXISTS alert_correlations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
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

-- Correlation rules table
CREATE TABLE IF NOT EXISTS correlation_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    conditions JSONB NOT NULL DEFAULT '[]',
    actions JSONB NOT NULL DEFAULT '[]',
    priority INTEGER DEFAULT 0,
    enabled BOOLEAN DEFAULT TRUE,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Correlation clusters table
CREATE TABLE IF NOT EXISTS correlation_clusters (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    cluster_id VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255),
    description TEXT,
    alert_ids JSONB NOT NULL DEFAULT '[]',
    cluster_type VARCHAR(50) DEFAULT 'similarity',
    confidence_score DECIMAL(3,2) DEFAULT 0.0,
    status VARCHAR(50) DEFAULT 'active',
    root_alert_id UUID REFERENCES alerts(id) ON DELETE SET NULL,
    incident_id UUID REFERENCES incidents(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for alert correlations
CREATE INDEX IF NOT EXISTS idx_alert_correlations_alert_id ON alert_correlations(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_correlations_correlation_id ON alert_correlations(correlation_id);
CREATE INDEX IF NOT EXISTS idx_alert_correlations_duplicate_of ON alert_correlations(duplicate_of) WHERE duplicate_of IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_correlations_created_at ON alert_correlations(created_at DESC);

-- Indexes for correlation rules
CREATE INDEX IF NOT EXISTS idx_correlation_rules_enabled ON correlation_rules(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_correlation_rules_priority ON correlation_rules(priority DESC);

-- Indexes for correlation clusters
CREATE INDEX IF NOT EXISTS idx_correlation_clusters_cluster_id ON correlation_clusters(cluster_id);
CREATE INDEX IF NOT EXISTS idx_correlation_clusters_status ON correlation_clusters(status);
CREATE INDEX IF NOT EXISTS idx_correlation_clusters_incident_id ON correlation_clusters(incident_id) WHERE incident_id IS NOT NULL;

-- Alert Comments Table (referenced in alerts.go but missing)
CREATE TABLE alert_comments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),
    username VARCHAR(255),
    comment TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes for alert comments
CREATE INDEX idx_alert_comments_alert_id ON alert_comments(alert_id);
CREATE INDEX idx_alert_comments_created_at ON alert_comments(created_at DESC);

-- ============================================================================
-- Insert Default Data
-- ============================================================================

-- Insert default roles
INSERT INTO roles (name, description, is_system_role) VALUES
('admin', 'Full system access with all permissions', true),
('manager', 'Manage alerts, incidents, and team members', true),
('engineer', 'Handle alerts and incidents', true),
('viewer', 'Read-only access to alerts and incidents', true);

-- Insert default permissions
INSERT INTO permissions (name, resource, action, description) VALUES
-- User permissions
('users.view', 'users', 'view', 'View users'),
('users.create', 'users', 'create', 'Create users'),
('users.update', 'users', 'update', 'Update users'),
('users.delete', 'users', 'delete', 'Delete users'),

-- Role permissions
('roles.view', 'roles', 'view', 'View roles'),
('roles.create', 'roles', 'create', 'Create roles'),
('roles.update', 'roles', 'update', 'Update roles'),
('roles.delete', 'roles', 'delete', 'Delete roles'),

-- Alert permissions
('alerts.view', 'alerts', 'view', 'View alerts'),
('alerts.create', 'alerts', 'create', 'Create alerts'),
('alerts.update', 'alerts', 'update', 'Update alerts'),
('alerts.delete', 'alerts', 'delete', 'Delete alerts'),
('alerts.assign', 'alerts', 'assign', 'Assign alerts'),
('alerts.resolve', 'alerts', 'resolve', 'Resolve alerts'),

-- Incident permissions
('incidents.view', 'incidents', 'view', 'View incidents'),
('incidents.create', 'incidents', 'create', 'Create incidents'),
('incidents.update', 'incidents', 'update', 'Update incidents'),
('incidents.delete', 'incidents', 'delete', 'Delete incidents'),
('incidents.assign', 'incidents', 'assign', 'Assign incidents'),
('incidents.resolve', 'incidents', 'resolve', 'Resolve incidents'),

-- Analytics permissions
('analytics.view', 'analytics', 'view', 'View analytics'),
('analytics.export', 'analytics', 'export', 'Export analytics'),

-- AI permissions
('ai.use', 'ai', 'use', 'Use AI features'),
('ai.train', 'ai', 'train', 'Train AI models'),

-- Audit permissions
('audit.view', 'audit', 'view', 'View audit logs'),

-- System permissions
('system.configure', 'system', 'configure', 'Configure system settings');

-- Assign permissions to admin role
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'admin';

-- Assign permissions to manager role
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'manager'
AND p.name IN (
    'users.view', 'alerts.view', 'alerts.create', 'alerts.update', 'alerts.assign', 'alerts.resolve',
    'incidents.view', 'incidents.create', 'incidents.update', 'incidents.assign', 'incidents.resolve',
    'analytics.view', 'analytics.export', 'ai.use'
);

-- Assign permissions to engineer role
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'engineer'
AND p.name IN (
    'alerts.view', 'alerts.update', 'alerts.resolve',
    'incidents.view', 'incidents.create', 'incidents.update',
    'analytics.view', 'ai.use'
);

-- Assign permissions to viewer role
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'viewer'
AND p.name IN ('alerts.view', 'incidents.view', 'analytics.view');

-- ============================================================================
-- Functions and Triggers
-- ============================================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply updated_at trigger to relevant tables
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_roles_updated_at BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_alerts_updated_at BEFORE UPDATE ON alerts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_incidents_updated_at BEFORE UPDATE ON incidents
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ============================================================================
-- Views for Common Queries
-- ============================================================================

-- Active alerts view
CREATE VIEW active_alerts AS
SELECT 
    a.*,
    u.full_name as assigned_to_name,
    u.email as assigned_to_email
FROM alerts a
LEFT JOIN users u ON a.assigned_to = u.id
WHERE a.status IN ('open', 'acknowledged', 'investigating');

-- Active incidents view
CREATE VIEW active_incidents AS
SELECT 
    i.*,
    u.full_name as assigned_to_name,
    u.email as assigned_to_email
FROM incidents i
LEFT JOIN users u ON i.assigned_to = u.id
WHERE i.status IN ('open', 'investigating', 'identified', 'monitoring');

-- User permissions view
CREATE VIEW user_permissions AS
SELECT 
    u.id as user_id,
    u.username,
    u.email,
    r.name as role_name,
    p.name as permission_name,
    p.resource,
    p.action
FROM users u
JOIN roles r ON u.role_id = r.id
JOIN role_permissions rp ON r.id = rp.role_id
JOIN permissions p ON rp.permission_id = p.id;

-- ============================================================================
-- Comments
-- ============================================================================

COMMENT ON TABLE users IS 'User accounts with authentication and profile information';
COMMENT ON TABLE roles IS 'User roles for RBAC';
COMMENT ON TABLE permissions IS 'Granular permissions for resources and actions';
COMMENT ON TABLE alerts IS 'Alert records from various sources';
COMMENT ON TABLE incidents IS 'Incident records for tracking and resolution';
COMMENT ON TABLE audit_logs IS 'Audit trail of all system actions';
COMMENT ON TABLE ai_predictions IS 'AI model predictions and outcomes';

-- ============================================================================
-- Kubernetes Topology Configuration Tables
-- ============================================================================

-- K8s Cluster Configurations Table
CREATE TABLE IF NOT EXISTS k8s_cluster_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) UNIQUE NOT NULL,
    environment VARCHAR(100) NOT NULL,
    region VARCHAR(100) NOT NULL,
    api_server_url VARCHAR(500) NOT NULL,
    service_account_token TEXT NOT NULL,
    ca_cert_data TEXT,
    namespace VARCHAR(255) DEFAULT 'default',
    enabled BOOLEAN DEFAULT TRUE,
    last_sync TIMESTAMP WITH TIME ZONE,
    sync_status VARCHAR(50) DEFAULT 'pending',
    sync_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for K8s cluster configs
CREATE INDEX idx_k8s_cluster_configs_name ON k8s_cluster_configs(name);
CREATE INDEX idx_k8s_cluster_configs_enabled ON k8s_cluster_configs(enabled) WHERE enabled = TRUE;
CREATE INDEX idx_k8s_cluster_configs_environment ON k8s_cluster_configs(environment);
CREATE INDEX idx_k8s_cluster_configs_region ON k8s_cluster_configs(region);
CREATE INDEX idx_k8s_cluster_configs_sync_status ON k8s_cluster_configs(sync_status);
CREATE INDEX idx_k8s_cluster_configs_last_sync ON k8s_cluster_configs(last_sync DESC);

-- Create trigger to update updated_at timestamp for K8s configs
CREATE TRIGGER update_k8s_cluster_configs_updated_at BEFORE UPDATE ON k8s_cluster_configs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Comments for K8s cluster configs
COMMENT ON TABLE k8s_cluster_configs IS 'Configuration for Kubernetes clusters for topology discovery';
COMMENT ON COLUMN k8s_cluster_configs.service_account_token IS 'Service account JWT token for read-only access to cluster';
COMMENT ON COLUMN k8s_cluster_configs.ca_cert_data IS 'Base64 encoded CA certificate for cluster verification';
COMMENT ON COLUMN k8s_cluster_configs.sync_status IS 'Status of last sync: pending, success, error';

-- ============================================================================
-- Grant Permissions (adjust based on your database users)
-- ============================================================================

-- Grant permissions to application user (create this user first)
-- GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO alerthub_app;
-- GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO alerthub_app;

-- ============================================================================
-- LLM Configuration Tables
-- ============================================================================

CREATE TABLE IF NOT EXISTS llm_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(100) NOT NULL DEFAULT 'ollama',
    model_name VARCHAR(255) NOT NULL DEFAULT 'phi3:mini',
    endpoint_url VARCHAR(500) NOT NULL DEFAULT 'http://ollama:11434',
    api_key TEXT,
    max_tokens INTEGER DEFAULT 2048,
    temperature FLOAT DEFAULT 0.1,
    enabled BOOLEAN DEFAULT true,
    use_for_rca BOOLEAN DEFAULT true,
    use_for_correlation BOOLEAN DEFAULT true,
    use_for_remediation BOOLEAN DEFAULT true,
    use_for_summarization BOOLEAN DEFAULT true,
    system_prompt TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS llm_feedback (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    alert_id UUID,
    prediction_type VARCHAR(100),
    prediction_value TEXT,
    actual_value TEXT,
    feedback_type VARCHAR(50),
    user_id UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW()
);

-- ============================================================================
-- Alert Sources Configuration
-- ============================================================================

CREATE TABLE IF NOT EXISTS alert_sources (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) UNIQUE NOT NULL,
    source_type VARCHAR(100) NOT NULL,
    display_name VARCHAR(255),
    endpoint_url VARCHAR(500),
    api_key TEXT,
    username TEXT,
    password TEXT,
    webhook_secret TEXT,
    extra_config JSONB DEFAULT '{}',
    enabled BOOLEAN DEFAULT true,
    polling_interval_seconds INTEGER DEFAULT 60,
    last_poll_at TIMESTAMP,
    last_poll_status VARCHAR(50) DEFAULT 'pending',
    alerts_received_total INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_sources_type ON alert_sources(source_type);
CREATE INDEX IF NOT EXISTS idx_alert_sources_enabled ON alert_sources(enabled) WHERE enabled = true;

-- ============================================================================
-- Infrastructure Resources (CloudStack / KVM / NetApp)
-- ============================================================================

CREATE TABLE IF NOT EXISTS infra_regions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) UNIQUE NOT NULL,
    display_name VARCHAR(255),
    location VARCHAR(255),
    region_type VARCHAR(100) DEFAULT 'datacenter',
    bm_count INTEGER DEFAULT 0,
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cloudstack_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    region_id UUID REFERENCES infra_regions(id),
    name VARCHAR(255) UNIQUE NOT NULL,
    api_url VARCHAR(500) NOT NULL,
    api_key TEXT NOT NULL,
    secret_key TEXT NOT NULL,
    zone_id VARCHAR(255),
    enabled BOOLEAN DEFAULT true,
    last_sync TIMESTAMP,
    sync_status VARCHAR(50) DEFAULT 'pending',
    vm_count INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS netapp_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    region_id UUID REFERENCES infra_regions(id),
    name VARCHAR(255) UNIQUE NOT NULL,
    management_url VARCHAR(500) NOT NULL,
    username TEXT NOT NULL,
    password TEXT NOT NULL,
    cluster_name VARCHAR(255),
    enabled BOOLEAN DEFAULT true,
    last_sync TIMESTAMP,
    total_capacity_tb FLOAT DEFAULT 0,
    used_capacity_tb FLOAT DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- ============================================================================
-- Correlation Engine Configuration
-- ============================================================================

CREATE TABLE IF NOT EXISTS correlation_engine_config (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    config_key VARCHAR(255) UNIQUE NOT NULL,
    config_value JSONB NOT NULL,
    description TEXT,
    updated_by UUID REFERENCES users(id),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Default correlation config
INSERT INTO correlation_engine_config (config_key, config_value, description) VALUES
('deduplication', '{"enabled": true, "window_minutes": 60, "fingerprint_fields": ["source", "severity", "title", "hostname"], "similarity_threshold": 0.85}', 'Deduplication settings'),
('auto_correlation', '{"enabled": true, "window_minutes": 30, "min_cluster_size": 2, "confidence_threshold": 0.7, "use_semantic": true, "use_topology": true}', 'Auto-correlation settings'),
('topology_correlation', '{"enabled": true, "parent_child_weight": 0.9, "same_host_weight": 0.8, "same_service_weight": 0.7, "same_cluster_weight": 0.6}', 'Topology-aware correlation weights'),
('llm_enrichment', '{"enabled": true, "min_severity": "high", "auto_rca": true, "auto_remediation_suggestions": true}', 'LLM enrichment settings')
ON CONFLICT (config_key) DO NOTHING;

-- Seed default LLM config
INSERT INTO llm_configs (name, provider, model_name, endpoint_url, enabled) VALUES
('Ollama phi3:mini', 'ollama', 'phi3:mini', 'http://ollama.alert-engine-poc.svc.cluster.local:11434', true)
ON CONFLICT DO NOTHING;

-- Seed default infra regions
INSERT INTO infra_regions (name, display_name, location, bm_count) VALUES
('reno', 'Reno (RNO)', 'Reno, NV', 75),
('maiden', 'Maiden (MDN)', 'Maiden, NC', 75)
ON CONFLICT (name) DO NOTHING;
