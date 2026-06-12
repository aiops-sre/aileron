-- Migration: Add missing enterprise tables for production readiness
-- Version: enterprise_tables_v1.0.0

-- Enhanced correlation rules table for advanced enterprise correlation
CREATE TABLE IF NOT EXISTS enhanced_correlation_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    rule_type VARCHAR(100) NOT NULL DEFAULT 'ml_enhanced', -- ml_enhanced, infrastructure, service_based, ai_driven
    conditions JSONB NOT NULL DEFAULT '[]',
    actions JSONB NOT NULL DEFAULT '[]',
    ml_model_config JSONB DEFAULT '{}', -- Configuration for ML models
    infrastructure_mapping JSONB DEFAULT '{}', -- Infrastructure topology mappings
    priority INTEGER DEFAULT 0,
    confidence_threshold DECIMAL(3,2) DEFAULT 0.70,
    enabled BOOLEAN DEFAULT TRUE,
    auto_learning BOOLEAN DEFAULT FALSE, -- Whether rule can learn and adapt
    performance_metrics JSONB DEFAULT '{}', -- Rule performance tracking
    last_trained_at TIMESTAMP WITH TIME ZONE,
    training_data_size INTEGER DEFAULT 0,
    success_rate DECIMAL(5,4) DEFAULT 0.0,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name),
    CHECK (confidence_threshold >= 0.0 AND confidence_threshold <= 1.0),
    CHECK (success_rate >= 0.0 AND success_rate <= 1.0)
);

-- Infrastructure correlations table for topology-aware correlation
CREATE TABLE IF NOT EXISTS infrastructure_correlations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_entity_id VARCHAR(255) NOT NULL, -- K8s pod, service, node, host, etc.
    target_entity_id VARCHAR(255) NOT NULL,
    entity_type VARCHAR(100) NOT NULL, -- pod, service, node, host, container, application
    correlation_type VARCHAR(100) NOT NULL, -- dependency, topology, communication, resource_shared
    relationship_strength DECIMAL(3,2) DEFAULT 0.0, -- 0.0 to 1.0
    dependency_direction VARCHAR(20) DEFAULT 'bidirectional', -- upstream, downstream, bidirectional
    infrastructure_layer VARCHAR(50) NOT NULL, -- application, service, pod, node, cluster, network
    cluster_id VARCHAR(255),
    namespace_name VARCHAR(255),
    service_name VARCHAR(255),
    correlation_data JSONB DEFAULT '{}', -- Additional correlation context
    discovered_method VARCHAR(100) NOT NULL, -- dynatrace_smartscape, k8s_discovery, prometheus_service_discovery
    confidence_score DECIMAL(3,2) DEFAULT 0.0,
    last_validated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    validation_count INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (relationship_strength >= 0.0 AND relationship_strength <= 1.0),
    CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0),
    CHECK (dependency_direction IN ('upstream', 'downstream', 'bidirectional'))
);

-- User expertise table for intelligent alert routing
CREATE TABLE IF NOT EXISTS user_expertise (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expertise_type VARCHAR(100) NOT NULL, -- technology, service, domain, infrastructure
    expertise_area VARCHAR(255) NOT NULL, -- kubernetes, database, networking, security, etc.
    expertise_level VARCHAR(50) NOT NULL DEFAULT 'intermediate', -- beginner, intermediate, advanced, expert
    confidence_score DECIMAL(3,2) DEFAULT 0.5,
    
    -- Technology specific expertise
    technologies JSONB DEFAULT '[]', -- ["kubernetes", "postgres", "redis", "prometheus"]
    services JSONB DEFAULT '[]', -- Services this user is expert in
    infrastructure_components JSONB DEFAULT '[]', -- ["networking", "storage", "compute"]
    
    -- Performance metrics
    resolution_count INTEGER DEFAULT 0, -- Number of alerts resolved in this area
    avg_resolution_time_minutes INTEGER DEFAULT 0,
    success_rate DECIMAL(5,4) DEFAULT 0.0,
    escalation_rate DECIMAL(5,4) DEFAULT 0.0,
    
    -- Learning and adaptation
    auto_detected BOOLEAN DEFAULT FALSE, -- Whether expertise was auto-detected from activity
    last_activity_date TIMESTAMP WITH TIME ZONE,
    skill_validation_date TIMESTAMP WITH TIME ZONE,
    validated_by UUID REFERENCES users(id) ON DELETE SET NULL, -- Manager/admin who validated
    
    -- Availability and preferences
    preferred_alert_types JSONB DEFAULT '[]',
    availability_hours JSONB DEFAULT '{}', -- Weekly availability schedule
    max_concurrent_alerts INTEGER DEFAULT 5,
    notification_preferences JSONB DEFAULT '{}',
    
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(user_id, expertise_area, expertise_type),
    CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0),
    CHECK (success_rate >= 0.0 AND success_rate <= 1.0),
    CHECK (escalation_rate >= 0.0 AND escalation_rate <= 1.0),
    CHECK (expertise_level IN ('beginner', 'intermediate', 'advanced', 'expert'))
);

-- On-call schedules table for intelligent escalation
CREATE TABLE IF NOT EXISTS oncall_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    team_id UUID REFERENCES teams(id) ON DELETE CASCADE,
    schedule_type VARCHAR(50) NOT NULL DEFAULT 'rotation', -- rotation, fixed, flexible, ai_optimized
    
    -- Schedule configuration
    rotation_config JSONB DEFAULT '{}', -- Rotation rules and patterns
    timezone VARCHAR(100) DEFAULT 'UTC',
    effective_start_date TIMESTAMP WITH TIME ZONE NOT NULL,
    effective_end_date TIMESTAMP WITH TIME ZONE,
    
    -- AI-driven optimization
    ai_optimized BOOLEAN DEFAULT FALSE,
    optimization_criteria JSONB DEFAULT '{}', -- Skills, workload, performance factors
    last_optimized_at TIMESTAMP WITH TIME ZONE,
    
    -- Alert routing rules
    alert_routing_rules JSONB DEFAULT '[]', -- Rules for routing alerts to on-call
    escalation_policy JSONB DEFAULT '{}', -- Multi-level escalation policy
    override_policies JSONB DEFAULT '[]', -- Emergency override policies
    
    -- Performance tracking
    avg_response_time_minutes INTEGER DEFAULT 0,
    coverage_percentage DECIMAL(5,2) DEFAULT 100.0,
    alert_volume_last_30d INTEGER DEFAULT 0,
    resolution_rate DECIMAL(5,4) DEFAULT 0.0,
    
    -- Status and metadata
    is_active BOOLEAN DEFAULT TRUE,
    auto_generated BOOLEAN DEFAULT FALSE, -- Whether schedule was AI-generated
    approval_required BOOLEAN DEFAULT TRUE,
    approved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_at TIMESTAMP WITH TIME ZONE,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name),
    CHECK (coverage_percentage >= 0.0 AND coverage_percentage <= 100.0),
    CHECK (resolution_rate >= 0.0 AND resolution_rate <= 1.0)
);

-- On-call schedule entries table
CREATE TABLE IF NOT EXISTS oncall_schedule_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id UUID NOT NULL REFERENCES oncall_schedules(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE NOT NULL,
    entry_type VARCHAR(50) DEFAULT 'regular', -- regular, override, emergency, backup
    is_primary BOOLEAN DEFAULT TRUE, -- Primary vs backup on-call
    
    -- Context and constraints
    shift_notes TEXT,
    coverage_areas JSONB DEFAULT '[]', -- Services/areas covered during this shift
    escalation_level INTEGER DEFAULT 1, -- 1=primary, 2=secondary, etc.
    contact_methods JSONB DEFAULT '[]', -- Override contact preferences for this shift
    
    -- Performance during this shift
    alerts_received INTEGER DEFAULT 0,
    alerts_resolved INTEGER DEFAULT 0,
    avg_response_time_minutes INTEGER DEFAULT 0,
    handoff_notes TEXT,
    
    -- Status
    status VARCHAR(50) DEFAULT 'scheduled', -- scheduled, active, completed, cancelled, no_show
    actual_start_time TIMESTAMP WITH TIME ZONE,
    actual_end_time TIMESTAMP WITH TIME ZONE,
    
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (end_time > start_time),
    CHECK (escalation_level >= 1),
    CHECK (status IN ('scheduled', 'active', 'completed', 'cancelled', 'no_show'))
);

-- Teams table (if not exists) for organizational structure
CREATE TABLE IF NOT EXISTS teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    team_type VARCHAR(100) DEFAULT 'engineering', -- engineering, sre, devops, security, etc.
    parent_team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    manager_id UUID REFERENCES users(id) ON DELETE SET NULL,
    
    -- Team capabilities and responsibilities
    primary_services JSONB DEFAULT '[]', -- Services this team owns
    secondary_services JSONB DEFAULT '[]', -- Services this team supports
    expertise_areas JSONB DEFAULT '[]', -- Technology/domain expertise
    escalation_contacts JSONB DEFAULT '[]',
    
    -- Team metrics
    avg_response_time_minutes INTEGER DEFAULT 0,
    resolution_rate DECIMAL(5,4) DEFAULT 0.0,
    alert_volume_last_30d INTEGER DEFAULT 0,
    team_size INTEGER DEFAULT 0,
    
    -- Configuration
    notification_channels JSONB DEFAULT '{}', -- Slack, email, etc.
    working_hours JSONB DEFAULT '{}', -- Team working hours by timezone
    sla_targets JSONB DEFAULT '{}', -- Response and resolution SLA targets
    
    is_active BOOLEAN DEFAULT TRUE,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Alert assignments table for tracking alert ownership
CREATE TABLE IF NOT EXISTS alert_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    assignment_type VARCHAR(50) NOT NULL, -- auto_assigned, manual_assigned, escalated, inherited
    assignment_reason TEXT, -- Why this assignment was made
    assignment_confidence DECIMAL(3,2) DEFAULT 0.0, -- AI confidence in assignment
    
    -- Assignment context
    assigned_by UUID REFERENCES users(id) ON DELETE SET NULL, -- Who made the assignment
    assignment_method VARCHAR(100), -- oncall_schedule, expertise_match, manual, escalation
    assignment_criteria JSONB DEFAULT '{}', -- Factors that led to assignment
    
    -- Timing
    assigned_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    accepted_at TIMESTAMP WITH TIME ZONE,
    started_work_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    
    -- Status and outcome
    status VARCHAR(50) DEFAULT 'assigned', -- assigned, accepted, in_progress, completed, reassigned, declined
    outcome VARCHAR(100), -- resolved, escalated, transferred, timeout
    notes TEXT,
    
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (assignment_confidence >= 0.0 AND assignment_confidence <= 1.0),
    CHECK ((user_id IS NOT NULL) OR (team_id IS NOT NULL)) -- Must assign to either user or team
);

-- Performance indexes for enterprise scale (150+ servers)
CREATE INDEX IF NOT EXISTS idx_enhanced_correlation_rules_type ON enhanced_correlation_rules(rule_type);
CREATE INDEX IF NOT EXISTS idx_enhanced_correlation_rules_enabled ON enhanced_correlation_rules(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_enhanced_correlation_rules_priority ON enhanced_correlation_rules(priority DESC);
CREATE INDEX IF NOT EXISTS idx_enhanced_correlation_rules_confidence ON enhanced_correlation_rules(confidence_threshold);

CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_source ON infrastructure_correlations(source_entity_id);
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_target ON infrastructure_correlations(target_entity_id);
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_type ON infrastructure_correlations(entity_type);
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_layer ON infrastructure_correlations(infrastructure_layer);
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_cluster ON infrastructure_correlations(cluster_id);
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_namespace ON infrastructure_correlations(namespace_name);
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_active ON infrastructure_correlations(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_confidence ON infrastructure_correlations(confidence_score DESC);

CREATE INDEX IF NOT EXISTS idx_user_expertise_user_id ON user_expertise(user_id);
CREATE INDEX IF NOT EXISTS idx_user_expertise_area ON user_expertise(expertise_area);
CREATE INDEX IF NOT EXISTS idx_user_expertise_level ON user_expertise(expertise_level);
CREATE INDEX IF NOT EXISTS idx_user_expertise_technologies ON user_expertise USING gin(technologies);
CREATE INDEX IF NOT EXISTS idx_user_expertise_services ON user_expertise USING gin(services);
CREATE INDEX IF NOT EXISTS idx_user_expertise_confidence ON user_expertise(confidence_score DESC);

CREATE INDEX IF NOT EXISTS idx_oncall_schedules_team ON oncall_schedules(team_id);
CREATE INDEX IF NOT EXISTS idx_oncall_schedules_active ON oncall_schedules(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_oncall_schedules_dates ON oncall_schedules(effective_start_date, effective_end_date);
CREATE INDEX IF NOT EXISTS idx_oncall_schedules_type ON oncall_schedules(schedule_type);

CREATE INDEX IF NOT EXISTS idx_oncall_schedule_entries_schedule ON oncall_schedule_entries(schedule_id);
CREATE INDEX IF NOT EXISTS idx_oncall_schedule_entries_user ON oncall_schedule_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_oncall_schedule_entries_time_range ON oncall_schedule_entries(start_time, end_time);
CREATE INDEX IF NOT EXISTS idx_oncall_schedule_entries_current ON oncall_schedule_entries(schedule_id, start_time, end_time) 
    WHERE status = 'active' AND start_time <= NOW() AND end_time >= NOW();

CREATE INDEX IF NOT EXISTS idx_teams_parent ON teams(parent_team_id) WHERE parent_team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_teams_manager ON teams(manager_id) WHERE manager_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_teams_active ON teams(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_teams_services ON teams USING gin(primary_services);

CREATE INDEX IF NOT EXISTS idx_alert_assignments_alert ON alert_assignments(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_assignments_user ON alert_assignments(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_assignments_team ON alert_assignments(team_id) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alert_assignments_status ON alert_assignments(status);
CREATE INDEX IF NOT EXISTS idx_alert_assignments_assigned_at ON alert_assignments(assigned_at);

-- Triggers for updating timestamps
CREATE TRIGGER update_enhanced_correlation_rules_updated_at 
    BEFORE UPDATE ON enhanced_correlation_rules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_infrastructure_correlations_updated_at 
    BEFORE UPDATE ON infrastructure_correlations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_user_expertise_updated_at 
    BEFORE UPDATE ON user_expertise
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_oncall_schedules_updated_at 
    BEFORE UPDATE ON oncall_schedules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_oncall_schedule_entries_updated_at 
    BEFORE UPDATE ON oncall_schedule_entries
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_teams_updated_at 
    BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_alert_assignments_updated_at 
    BEFORE UPDATE ON alert_assignments
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Insert default team for testing/demo
INSERT INTO teams (name, description, team_type, expertise_areas, working_hours) VALUES
(
    'SRE Team',
    'Site Reliability Engineering team responsible for production infrastructure',
    'sre',
    '["kubernetes", "prometheus", "grafana", "infrastructure", "monitoring"]',
    '{"timezone": "UTC", "days": ["monday", "tuesday", "wednesday", "thursday", "friday"], "hours": {"start": 9, "end": 17}}'
),
(
    'DevOps Team', 
    'Development Operations team handling CI/CD and deployment automation',
    'devops',
    '["docker", "kubernetes", "jenkins", "terraform", "aws"]',
    '{"timezone": "UTC", "days": ["monday", "tuesday", "wednesday", "thursday", "friday"], "hours": {"start": 8, "end": 18}}'
) ON CONFLICT (name) DO NOTHING;

-- Add foreign key constraints to alerts table for better referential integrity
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS primary_team_id UUID REFERENCES teams(id) ON DELETE SET NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS assigned_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_alerts_primary_team ON alerts(primary_team_id) WHERE primary_team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_assigned_user ON alerts(assigned_user_id) WHERE assigned_user_id IS NOT NULL;