-- ============================================================================
-- Configuration Management System - Database Schema
-- ============================================================================

-- Configuration Namespaces - Top-level organizational structure
CREATE TABLE IF NOT EXISTS config_namespaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    owner_team_id UUID,
    access_level VARCHAR(20) DEFAULT 'team', -- 'global', 'team', 'user'
    is_system BOOLEAN DEFAULT FALSE,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Schemas - Define structure and validation rules
CREATE TABLE IF NOT EXISTS config_schemas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID REFERENCES config_namespaces(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    version VARCHAR(20) NOT NULL,
    schema_type VARCHAR(50) NOT NULL, -- 'json_schema', 'avro', 'protobuf'
    schema_definition JSONB NOT NULL,
    validation_rules JSONB,
    default_values JSONB,
    ui_schema JSONB, -- UI rendering hints
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT NOW(),
    deprecated_at TIMESTAMP,
    UNIQUE(namespace_id, name, version)
);

-- Configuration Entries - Actual configuration data
CREATE TABLE IF NOT EXISTS config_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID REFERENCES config_namespaces(id) ON DELETE CASCADE,
    schema_id UUID REFERENCES config_schemas(id),
    key VARCHAR(255) NOT NULL,
    value JSONB NOT NULL,
    environment VARCHAR(50) DEFAULT 'production', -- 'dev', 'staging', 'production'
    version INTEGER DEFAULT 1,
    is_encrypted BOOLEAN DEFAULT FALSE,
    is_active BOOLEAN DEFAULT TRUE,
    tags TEXT[],
    metadata JSONB,
    created_by UUID REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(namespace_id, key, environment)
);

-- Configuration History - Version tracking and audit trail
CREATE TABLE IF NOT EXISTS config_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    config_entry_id UUID REFERENCES config_entries(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    previous_value JSONB,
    new_value JSONB,
    change_type VARCHAR(20) NOT NULL, -- 'create', 'update', 'delete', 'rollback'
    changed_by UUID REFERENCES users(id),
    change_reason TEXT,
    change_metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Templates - Reusable configuration patterns
CREATE TABLE IF NOT EXISTS config_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(100),
    namespace_id UUID REFERENCES config_namespaces(id),
    template_data JSONB NOT NULL,
    variables JSONB, -- Template variables for customization
    tags TEXT[],
    is_public BOOLEAN DEFAULT FALSE,
    usage_count INTEGER DEFAULT 0,
    rating FLOAT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Deployments - Track configuration rollouts
CREATE TABLE IF NOT EXISTS config_deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    environment VARCHAR(50) NOT NULL,
    config_snapshot JSONB NOT NULL, -- Complete config state
    deployment_strategy VARCHAR(50) DEFAULT 'immediate', -- 'immediate', 'canary', 'blue_green'
    rollout_percentage INTEGER DEFAULT 100,
    status VARCHAR(20) DEFAULT 'pending', -- 'pending', 'in_progress', 'completed', 'failed', 'rolled_back'
    deployed_by UUID REFERENCES users(id),
    deployed_at TIMESTAMP,
    completed_at TIMESTAMP,
    rollback_deployment_id UUID REFERENCES config_deployments(id),
    error_message TEXT,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Approvals - Approval workflow for changes
CREATE TABLE IF NOT EXISTS config_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    config_entry_id UUID REFERENCES config_entries(id) ON DELETE CASCADE,
    deployment_id UUID REFERENCES config_deployments(id),
    proposed_change JSONB NOT NULL,
    approval_required_count INTEGER DEFAULT 1,
    approval_received_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'pending', -- 'pending', 'approved', 'rejected', 'expired'
    requested_by UUID REFERENCES users(id),
    requested_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP,
    completed_at TIMESTAMP
);

-- Configuration Approval Votes
CREATE TABLE IF NOT EXISTS config_approval_votes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_id UUID REFERENCES config_approvals(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),
    vote VARCHAR(10) NOT NULL, -- 'approve', 'reject'
    comment TEXT,
    voted_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(approval_id, user_id)
);

-- Configuration Locks - Prevent concurrent modifications
CREATE TABLE IF NOT EXISTS config_locks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    config_entry_id UUID REFERENCES config_entries(id) ON DELETE CASCADE,
    locked_by UUID REFERENCES users(id),
    lock_reason TEXT,
    locked_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL,
    is_active BOOLEAN DEFAULT TRUE
);

-- Configuration Dependencies - Track inter-configuration dependencies
CREATE TABLE IF NOT EXISTS config_dependencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_config_id UUID REFERENCES config_entries(id) ON DELETE CASCADE,
    dependent_config_id UUID REFERENCES config_entries(id) ON DELETE CASCADE,
    dependency_type VARCHAR(50), -- 'required', 'optional', 'recommended'
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(source_config_id, dependent_config_id)
);

-- Configuration Notifications - Subscribe to config changes
CREATE TABLE IF NOT EXISTS config_notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID REFERENCES config_namespaces(id),
    config_entry_id UUID REFERENCES config_entries(id),
    user_id UUID REFERENCES users(id),
    notification_type VARCHAR(50), -- 'email', 'slack', 'webhook'
    notification_target VARCHAR(255), -- email/webhook URL
    events TEXT[], -- ['create', 'update', 'delete']
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Validators - Custom validation functions
CREATE TABLE IF NOT EXISTS config_validators (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID REFERENCES config_namespaces(id),
    name VARCHAR(100) NOT NULL,
    validator_type VARCHAR(50), -- 'regex', 'range', 'custom_function', 'api_call'
    validator_config JSONB NOT NULL,
    error_message TEXT,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Cost Tracking - Track LLM and resource costs
CREATE TABLE IF NOT EXISTS config_cost_tracking (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID REFERENCES config_namespaces(id),
    config_entry_id UUID REFERENCES config_entries(id),
    resource_type VARCHAR(50), -- 'llm_api', 'compute', 'storage'
    cost_amount DECIMAL(10, 4),
    cost_currency VARCHAR(3) DEFAULT 'USD',
    usage_quantity FLOAT,
    usage_unit VARCHAR(50),
    period_start TIMESTAMP NOT NULL,
    period_end TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Configuration Exports - Track configuration exports
CREATE TABLE IF NOT EXISTS config_exports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    export_name VARCHAR(255) NOT NULL,
    namespace_id UUID REFERENCES config_namespaces(id),
    export_format VARCHAR(50), -- 'json', 'yaml', 'toml', 'terraform'
    export_data JSONB,
    export_path VARCHAR(500),
    exported_by UUID REFERENCES users(id),
    exported_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP
);

-- Indexes for performance
CREATE INDEX idx_config_entries_namespace ON config_entries(namespace_id);
CREATE INDEX idx_config_entries_key ON config_entries(key);
CREATE INDEX idx_config_entries_environment ON config_entries(environment);
CREATE INDEX idx_config_entries_active ON config_entries(is_active);
CREATE INDEX idx_config_history_entry ON config_history(config_entry_id);
CREATE INDEX idx_config_history_created ON config_history(created_at);
CREATE INDEX idx_config_approvals_status ON config_approvals(status);
CREATE INDEX idx_config_deployments_env ON config_deployments(environment);
CREATE INDEX idx_config_deployments_status ON config_deployments(status);
CREATE INDEX idx_config_locks_active ON config_locks(is_active, expires_at);

-- System configuration namespaces (pre-populated)
INSERT INTO config_namespaces (name, description, is_system) VALUES
('aiops.burst_detection', 'Alert burst and storm detection configuration', TRUE),
('aiops.agents', 'Multi-agent framework configuration', TRUE),
('aiops.ml_models', 'Machine learning model parameters', TRUE),
('aiops.anomaly_detection', 'Anomaly detection settings', TRUE),
('aiops.nlp', 'Natural language processing configuration', TRUE),
('integrations.dynatrace', 'Dynatrace integration settings', TRUE),
('integrations.cloudstack', 'CloudStack integration settings', TRUE),
('notifications', 'Notification channel configuration', TRUE),
('escalation_policies', 'Alert escalation and routing', TRUE),
('slo_sla', 'SLO/SLA definitions and tracking', TRUE),
('data_retention', 'Data retention and archival policies', TRUE),
('rbac', 'Role-based access control policies', TRUE),
('cost_optimization', 'Cost optimization settings', TRUE),
('deployment', 'Deployment and runtime configuration', TRUE)
ON CONFLICT (name) DO NOTHING;

-- Comments
COMMENT ON TABLE config_namespaces IS 'Top-level organizational structure for configurations';
COMMENT ON TABLE config_schemas IS 'Schema definitions with validation rules for configurations';
COMMENT ON TABLE config_entries IS 'Actual configuration values with versioning';
COMMENT ON TABLE config_history IS 'Complete audit trail of all configuration changes';
COMMENT ON TABLE config_templates IS 'Reusable configuration templates and patterns';
COMMENT ON TABLE config_deployments IS 'Configuration deployment tracking and rollout management';
COMMENT ON TABLE config_approvals IS 'Approval workflow for configuration changes';
COMMENT ON TABLE config_locks IS 'Prevent concurrent modifications to configurations';
COMMENT ON TABLE config_dependencies IS 'Track dependencies between configurations';
COMMENT ON TABLE config_cost_tracking IS 'Track costs associated with configurations (LLM usage, etc)';

-- Grants
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO alerthub_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO alerthub_user;
