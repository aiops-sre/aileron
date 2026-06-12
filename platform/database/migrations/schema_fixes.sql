-- Migration: Fix foreign key relationships and schema consistency
-- Version: schema_fixes_v1.0.0

-- Ensure users table exists (required for foreign keys)
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    full_name VARCHAR(255),
    password_hash VARCHAR(255),
    is_active BOOLEAN DEFAULT TRUE,
    is_admin BOOLEAN DEFAULT FALSE,
    last_login TIMESTAMP WITH TIME ZONE,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Ensure incidents table exists (required for foreign keys)
CREATE TABLE IF NOT EXISTS incidents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    description TEXT,
    severity VARCHAR(50) NOT NULL DEFAULT 'medium',
    status VARCHAR(50) NOT NULL DEFAULT 'open',
    priority VARCHAR(50) NOT NULL DEFAULT 'medium',
    incident_type VARCHAR(100) DEFAULT 'alert_based',
    
    -- Assignment and ownership
    assigned_to UUID REFERENCES users(id) ON DELETE SET NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    
    -- Correlation and alerts
    alert_ids JSONB DEFAULT '[]', -- Array of alert IDs that caused this incident
    correlation_id VARCHAR(255),
    root_cause_alert_id UUID REFERENCES alerts(id) ON DELETE SET NULL,
    
    -- Lifecycle
    acknowledged_at TIMESTAMP WITH TIME ZONE,
    acknowledged_by UUID REFERENCES users(id) ON DELETE SET NULL,
    resolved_at TIMESTAMP WITH TIME ZONE,
    resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    resolution_notes TEXT,
    
    -- SLA tracking
    sla_target_minutes INTEGER DEFAULT 240, -- 4 hours default
    response_time_minutes INTEGER,
    resolution_time_minutes INTEGER,
    sla_met BOOLEAN,
    
    -- Business impact
    impact_level VARCHAR(50) DEFAULT 'low', -- low, medium, high, critical
    affected_services JSONB DEFAULT '[]',
    affected_users_count INTEGER DEFAULT 0,
    estimated_cost DECIMAL(10,2) DEFAULT 0.0,
    
    -- Communication
    communication_channels JSONB DEFAULT '{}', -- Slack channels, war room details, etc.
    status_page_updated BOOLEAN DEFAULT FALSE,
    external_ticket_id VARCHAR(255), -- ServiceNow, Jira, etc.
    
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (severity IN ('critical', 'high', 'medium', 'low')),
    CHECK (status IN ('open', 'acknowledged', 'investigating', 'resolved', 'closed')),
    CHECK (priority IN ('critical', 'high', 'medium', 'low')),
    CHECK (impact_level IN ('critical', 'high', 'medium', 'low'))
);

-- Ensure alerts table has all required columns for unified model
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS fingerprint VARCHAR(64);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS count INTEGER DEFAULT 1;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMP WITH TIME ZONE DEFAULT NOW();
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP WITH TIME ZONE DEFAULT NOW();
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ai_analysis JSONB;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ai_classification VARCHAR(255);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ai_confidence DECIMAL(5,4) DEFAULT 0.0;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS is_alert_active BOOLEAN DEFAULT TRUE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS linked_alerts JSONB DEFAULT '[]';
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS linked_to JSONB;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_status INTEGER DEFAULT 0;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_start_time TIMESTAMP WITH TIME ZONE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_end_time TIMESTAMP WITH TIME ZONE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS sla_met_responsetime BOOLEAN DEFAULT FALSE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS sla_met_resolutiontime BOOLEAN DEFAULT FALSE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS recent_email_timestamp TIMESTAMP WITH TIME ZONE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS first_email_timestamp TIMESTAMP WITH TIME ZONE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolution_type VARCHAR(50) DEFAULT 'manual';
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_by_name VARCHAR(255);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_by_name VARCHAR(255);

-- Add missing foreign key references
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams(id) ON DELETE SET NULL;

-- Add missing indexes for performance
CREATE INDEX IF NOT EXISTS idx_alerts_fingerprint ON alerts(fingerprint) WHERE fingerprint IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_first_seen_at ON alerts(first_seen_at);
CREATE INDEX IF NOT EXISTS idx_alerts_last_seen_at ON alerts(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_alerts_ai_classification ON alerts(ai_classification) WHERE ai_classification IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_ai_confidence ON alerts(ai_confidence) WHERE ai_confidence > 0;
CREATE INDEX IF NOT EXISTS idx_alerts_is_active ON alerts(is_alert_active) WHERE is_alert_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_alerts_maintenance_status ON alerts(maintenance_status);
CREATE INDEX IF NOT EXISTS idx_alerts_resolution_type ON alerts(resolution_type);
CREATE INDEX IF NOT EXISTS idx_alerts_team_id ON alerts(team_id) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_count ON alerts(count) WHERE count > 1;

-- Incident indexes
CREATE INDEX IF NOT EXISTS idx_incidents_severity ON incidents(severity);
CREATE INDEX IF NOT EXISTS idx_incidents_status ON incidents(status);
CREATE INDEX IF NOT EXISTS idx_incidents_priority ON incidents(priority);
CREATE INDEX IF NOT EXISTS idx_incidents_assigned_to ON incidents(assigned_to) WHERE assigned_to IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_team_id ON incidents(team_id) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_correlation_id ON incidents(correlation_id) WHERE correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_created_at ON incidents(created_at);
CREATE INDEX IF NOT EXISTS idx_incidents_resolved_at ON incidents(resolved_at) WHERE resolved_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incidents_sla_met ON incidents(sla_met);
CREATE INDEX IF NOT EXISTS idx_incidents_impact_level ON incidents(impact_level);

-- User indexes
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_is_active ON users(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_users_is_admin ON users(is_admin) WHERE is_admin = TRUE;
CREATE INDEX IF NOT EXISTS idx_users_last_login ON users(last_login);

-- Alert history table for audit trail
CREATE TABLE IF NOT EXISTS alert_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL, -- created, updated, assigned, acknowledged, resolved, escalated
    old_value JSONB DEFAULT '{}',
    new_value JSONB DEFAULT '{}',
    notes TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_history_alert_id ON alert_history(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_user_id ON alert_history(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_history_action ON alert_history(action);
CREATE INDEX IF NOT EXISTS idx_alert_history_created_at ON alert_history(created_at);

-- Incident history table for audit trail
CREATE TABLE IF NOT EXISTS incident_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL, -- created, updated, assigned, acknowledged, resolved, escalated
    old_value JSONB DEFAULT '{}',
    new_value JSONB DEFAULT '{}',
    notes TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_incident_history_incident_id ON incident_history(incident_id);
CREATE INDEX IF NOT EXISTS idx_incident_history_user_id ON incident_history(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_incident_history_action ON incident_history(action);
CREATE INDEX IF NOT EXISTS idx_incident_history_created_at ON incident_history(created_at);

-- Service catalog table for service-based correlation
CREATE TABLE IF NOT EXISTS service_catalog (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_name VARCHAR(255) NOT NULL UNIQUE,
    display_name VARCHAR(255) NOT NULL,
    description TEXT,
    service_type VARCHAR(100) DEFAULT 'application', -- application, database, infrastructure, external
    
    -- Ownership
    owner_team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    primary_contact_id UUID REFERENCES users(id) ON DELETE SET NULL,
    secondary_contacts JSONB DEFAULT '[]',
    
    -- Technical details
    repository_url VARCHAR(500),
    documentation_url VARCHAR(500),
    monitoring_dashboards JSONB DEFAULT '[]',
    health_check_url VARCHAR(500),
    
    -- Dependencies
    dependencies JSONB DEFAULT '[]', -- Services this service depends on
    dependents JSONB DEFAULT '[]', -- Services that depend on this service
    
    -- SLA and business context
    sla_targets JSONB DEFAULT '{}', -- Response time, availability targets
    business_criticality VARCHAR(50) DEFAULT 'medium', -- critical, high, medium, low
    business_impact TEXT,
    
    -- Runtime information
    environments JSONB DEFAULT '[]', -- prod, staging, dev environments
    deployment_info JSONB DEFAULT '{}',
    infrastructure_tags JSONB DEFAULT '{}',
    
    -- Status
    operational_status VARCHAR(50) DEFAULT 'operational', -- operational, degraded, outage, maintenance
    last_deployment_at TIMESTAMP WITH TIME ZONE,
    last_incident_at TIMESTAMP WITH TIME ZONE,
    
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (business_criticality IN ('critical', 'high', 'medium', 'low')),
    CHECK (operational_status IN ('operational', 'degraded', 'outage', 'maintenance'))
);

CREATE INDEX IF NOT EXISTS idx_service_catalog_name ON service_catalog(service_name);
CREATE INDEX IF NOT EXISTS idx_service_catalog_type ON service_catalog(service_type);
CREATE INDEX IF NOT EXISTS idx_service_catalog_owner_team ON service_catalog(owner_team_id);
CREATE INDEX IF NOT EXISTS idx_service_catalog_criticality ON service_catalog(business_criticality);
CREATE INDEX IF NOT EXISTS idx_service_catalog_status ON service_catalog(operational_status);
CREATE INDEX IF NOT EXISTS idx_service_catalog_dependencies ON service_catalog USING gin(dependencies);

-- Add service reference to alerts table
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS service_id UUID REFERENCES service_catalog(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_service_id ON alerts(service_id) WHERE service_id IS NOT NULL;

-- Notification channels table
CREATE TABLE IF NOT EXISTS notification_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    channel_type VARCHAR(100) NOT NULL, -- email, slack, teams, pagerduty, webhook, sms
    configuration JSONB NOT NULL DEFAULT '{}',
    
    -- Routing rules
    alert_filters JSONB DEFAULT '[]', -- Conditions for when to use this channel
    severity_filters JSONB DEFAULT '[]', -- Which severities to notify
    service_filters JSONB DEFAULT '[]', -- Which services to notify for
    team_filters JSONB DEFAULT '[]', -- Which teams to notify
    
    -- Rate limiting and scheduling
    rate_limit_config JSONB DEFAULT '{}',
    quiet_hours JSONB DEFAULT '{}', -- When not to send notifications
    escalation_delay_minutes INTEGER DEFAULT 15,
    
    -- Status and performance
    is_active BOOLEAN DEFAULT TRUE,
    last_notification_at TIMESTAMP WITH TIME ZONE,
    success_rate DECIMAL(5,4) DEFAULT 1.0,
    failure_count_last_24h INTEGER DEFAULT 0,
    
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notification_channels_type ON notification_channels(channel_type);
CREATE INDEX IF NOT EXISTS idx_notification_channels_active ON notification_channels(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_notification_channels_filters ON notification_channels USING gin(alert_filters);

-- Notification log table for tracking
CREATE TABLE IF NOT EXISTS notification_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID REFERENCES alerts(id) ON DELETE SET NULL,
    incident_id UUID REFERENCES incidents(id) ON DELETE SET NULL,
    channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    notification_type VARCHAR(100) NOT NULL, -- alert_created, alert_escalated, incident_created, etc.
    
    recipient_type VARCHAR(50) NOT NULL, -- user, team, channel
    recipient_id UUID, -- user_id, team_id, or channel identifier
    recipient_address VARCHAR(500), -- email, phone, slack channel, etc.
    
    status VARCHAR(50) DEFAULT 'pending', -- pending, sent, delivered, failed, bounced
    sent_at TIMESTAMP WITH TIME ZONE,
    delivered_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    
    message_content TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notification_log_alert_id ON notification_log(alert_id) WHERE alert_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notification_log_incident_id ON notification_log(incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notification_log_channel_id ON notification_log(channel_id);
CREATE INDEX IF NOT EXISTS idx_notification_log_status ON notification_log(status);
CREATE INDEX IF NOT EXISTS idx_notification_log_created_at ON notification_log(created_at);
CREATE INDEX IF NOT EXISTS idx_notification_log_recipient ON notification_log(recipient_type, recipient_id);

-- Add trigger for incidents
CREATE TRIGGER update_incidents_updated_at 
    BEFORE UPDATE ON incidents
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_service_catalog_updated_at 
    BEFORE UPDATE ON service_catalog
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_notification_channels_updated_at 
    BEFORE UPDATE ON notification_channels
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Default users are not seeded in production. Users are managed via IDMS/MAS SSO.
-- See internal/db/migrations.go for the runtime bootstrap logic.

-- Insert default notification channels
INSERT INTO notification_channels (name, channel_type, configuration) VALUES
('Default Email', 'email', '{"smtp_host": "localhost", "from": "alerts@alerthub.local"}'),
('Slack General', 'slack', '{"webhook_url": "", "channel": "#alerts"}'),
('PagerDuty Critical', 'pagerduty', '{"service_key": "", "severity_filter": ["critical", "high"]}')
ON CONFLICT (name) DO NOTHING;