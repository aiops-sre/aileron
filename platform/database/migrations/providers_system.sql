-- Migration: Add provider integrations system tables
-- Version: providers_v1.0.0

-- Provider configurations table
CREATE TABLE IF NOT EXISTS provider_configurations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    provider_type VARCHAR(100) NOT NULL,
    provider_id VARCHAR(100) NOT NULL,
    configuration JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN DEFAULT FALSE,
    status VARCHAR(50) DEFAULT 'inactive',
    last_sync TIMESTAMP WITH TIME ZONE,
    last_error TEXT,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name),
    CHECK (status IN ('active', 'inactive', 'error', 'configuring'))
);

-- Provider specifications table (registry of available providers)
CREATE TABLE IF NOT EXISTS provider_specs (
    id VARCHAR(100) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(100) NOT NULL,
    version VARCHAR(50) NOT NULL,
    description TEXT,
    capabilities JSONB NOT NULL DEFAULT '[]',
    config_schema JSONB NOT NULL DEFAULT '{}',
    documentation TEXT,
    examples JSONB DEFAULT '{}',
    tags JSONB DEFAULT '[]',
    vendor VARCHAR(255),
    license VARCHAR(100),
    support_level VARCHAR(50) DEFAULT 'community',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (support_level IN ('official', 'community', 'experimental'))
);

-- Provider sync logs table
CREATE TABLE IF NOT EXISTS provider_sync_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_config_id UUID NOT NULL REFERENCES provider_configurations(id) ON DELETE CASCADE,
    sync_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    records_processed INTEGER DEFAULT 0,
    records_failed INTEGER DEFAULT 0,
    error_message TEXT,
    metadata JSONB DEFAULT '{}',
    
    CHECK (sync_type IN ('pull', 'push', 'query', 'webhook')),
    CHECK (status IN ('running', 'completed', 'failed', 'cancelled'))
);

-- Provider metrics table
CREATE TABLE IF NOT EXISTS provider_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_config_id UUID NOT NULL REFERENCES provider_configurations(id) ON DELETE CASCADE,
    metric_name VARCHAR(100) NOT NULL,
    metric_value DECIMAL(15,4) NOT NULL,
    metric_type VARCHAR(50) NOT NULL,
    labels JSONB DEFAULT '{}',
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (metric_type IN ('counter', 'gauge', 'histogram', 'summary'))
);

-- Provider webhooks table
CREATE TABLE IF NOT EXISTS provider_webhooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_config_id UUID NOT NULL REFERENCES provider_configurations(id) ON DELETE CASCADE,
    webhook_path VARCHAR(255) NOT NULL,
    webhook_secret VARCHAR(255),
    event_types JSONB DEFAULT '[]',
    enabled BOOLEAN DEFAULT TRUE,
    last_triggered TIMESTAMP WITH TIME ZONE,
    trigger_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(webhook_path)
);

-- Provider data mappings table (for field transformations)
CREATE TABLE IF NOT EXISTS provider_data_mappings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_config_id UUID NOT NULL REFERENCES provider_configurations(id) ON DELETE CASCADE,
    source_field VARCHAR(255) NOT NULL,
    target_field VARCHAR(255) NOT NULL,
    transformation VARCHAR(255),
    transformation_params JSONB DEFAULT '{}',
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(provider_config_id, source_field, target_field)
);

-- Insert provider specifications for Keep's supported integrations
INSERT INTO provider_specs (id, name, type, version, description, capabilities, config_schema, tags, vendor, support_level) VALUES

-- Monitoring Tools
('prometheus', 'Prometheus', 'monitoring', '1.0.0', 'Prometheus monitoring system', 
 '["pull", "query", "webhook"]', 
 '{"base_url": {"type": "string", "required": true, "description": "Prometheus server URL"}, "alertmanager_url": {"type": "string", "required": false, "description": "AlertManager URL"}}',
 '["monitoring", "metrics", "alerts"]', 'Prometheus', 'official'),

('grafana', 'Grafana', 'monitoring', '1.0.0', 'Grafana dashboards and alerting',
 '["pull", "push", "query"]',
 '{"base_url": {"type": "string", "required": true}, "api_key": {"type": "string", "required": true, "sensitive": true}}',
 '["monitoring", "dashboards", "visualization"]', 'Grafana Labs', 'official'),

('datadog', 'Datadog', 'monitoring', '1.0.0', 'Datadog monitoring and APM',
 '["pull", "push", "query"]',
 '{"api_key": {"type": "string", "required": true, "sensitive": true}, "app_key": {"type": "string", "required": true, "sensitive": true}, "site": {"type": "string", "default": "datadoghq.com"}}',
 '["monitoring", "apm", "logs"]', 'Datadog', 'official'),

('dynatrace', 'Dynatrace', 'monitoring', '1.0.0', 'Dynatrace APM and infrastructure monitoring',
 '["pull", "push", "webhook"]',
 '{"environment_url": {"type": "string", "required": true}, "api_token": {"type": "string", "required": true, "sensitive": true}}',
 '["monitoring", "apm", "infrastructure"]', 'Dynatrace', 'official'),

('newrelic', 'New Relic', 'monitoring', '1.0.0', 'New Relic monitoring and observability',
 '["pull", "query"]',
 '{"api_key": {"type": "string", "required": true, "sensitive": true}, "account_id": {"type": "string", "required": true}}',
 '["monitoring", "apm", "infrastructure"]', 'New Relic', 'official'),

('splunk', 'Splunk', 'monitoring', '1.0.0', 'Splunk log monitoring and SIEM',
 '["query", "pull"]',
 '{"host": {"type": "string", "required": true}, "port": {"type": "number", "default": 8089}, "username": {"type": "string", "required": true}, "password": {"type": "string", "required": true, "sensitive": true}}',
 '["monitoring", "logs", "siem"]', 'Splunk', 'official'),

-- Communication Platforms
('slack', 'Slack', 'communication', '1.0.0', 'Slack team communication',
 '["push", "notify", "webhook"]',
 '{"webhook_url": {"type": "string", "required": true, "sensitive": true}, "bot_token": {"type": "string", "required": false, "sensitive": true}}',
 '["communication", "notifications"]', 'Slack', 'official'),

('teams', 'Microsoft Teams', 'communication', '1.0.0', 'Microsoft Teams communication',
 '["push", "notify", "webhook"]',
 '{"webhook_url": {"type": "string", "required": true, "sensitive": true}}',
 '["communication", "notifications", "microsoft"]', 'Microsoft', 'official'),

('discord', 'Discord', 'communication', '1.0.0', 'Discord communication platform',
 '["push", "notify", "webhook"]',
 '{"webhook_url": {"type": "string", "required": true, "sensitive": true}}',
 '["communication", "notifications", "gaming"]', 'Discord', 'community'),

-- Incident Management
('pagerduty', 'PagerDuty', 'incident_management', '1.0.0', 'PagerDuty incident management and on-call',
 '["push", "pull", "bidirection"]',
 '{"integration_key": {"type": "string", "required": true, "sensitive": true}, "api_token": {"type": "string", "required": false, "sensitive": true}}',
 '["incident_management", "oncall", "escalation"]', 'PagerDuty', 'official'),

('opsgenie', 'Opsgenie', 'incident_management', '1.0.0', 'Opsgenie incident management',
 '["push", "pull", "bidirection"]',
 '{"api_key": {"type": "string", "required": true, "sensitive": true}, "region": {"type": "string", "default": "us", "options": ["us", "eu"]}}',
 '["incident_management", "oncall"]', 'Atlassian', 'official'),

-- Databases
('postgresql', 'PostgreSQL', 'database', '1.0.0', 'PostgreSQL database',
 '["query", "pull", "push"]',
 '{"host": {"type": "string", "required": true}, "port": {"type": "number", "default": 5432}, "database": {"type": "string", "required": true}, "username": {"type": "string", "required": true}, "password": {"type": "string", "required": true, "sensitive": true}}',
 '["database", "sql"]', 'PostgreSQL', 'official'),

('mysql', 'MySQL', 'database', '1.0.0', 'MySQL database',
 '["query", "pull", "push"]',
 '{"host": {"type": "string", "required": true}, "port": {"type": "number", "default": 3306}, "database": {"type": "string", "required": true}, "username": {"type": "string", "required": true}, "password": {"type": "string", "required": true, "sensitive": true}}',
 '["database", "sql"]', 'Oracle', 'official'),

('mongodb', 'MongoDB', 'database', '1.0.0', 'MongoDB document database',
 '["query", "pull", "push"]',
 '{"connection_string": {"type": "string", "required": true, "sensitive": true}, "database": {"type": "string", "required": true}}',
 '["database", "nosql", "document"]', 'MongoDB', 'official'),

-- Data Warehouses
('bigquery', 'BigQuery', 'data_warehouse', '1.0.0', 'Google BigQuery data warehouse',
 '["query", "pull", "push"]',
 '{"project_id": {"type": "string", "required": true}, "dataset_id": {"type": "string", "required": true}, "credentials": {"type": "object", "required": true, "sensitive": true}}',
 '["data_warehouse", "analytics", "google"]', 'Google', 'official'),

('snowflake', 'Snowflake', 'data_warehouse', '1.0.0', 'Snowflake cloud data platform',
 '["query", "pull", "push"]',
 '{"account": {"type": "string", "required": true}, "username": {"type": "string", "required": true}, "password": {"type": "string", "required": true, "sensitive": true}, "warehouse": {"type": "string", "required": true}, "database": {"type": "string", "required": true}}',
 '["data_warehouse", "analytics", "cloud"]', 'Snowflake', 'official'),

('clickhouse', 'ClickHouse', 'data_warehouse', '1.0.0', 'ClickHouse analytical database',
 '["query", "pull", "push"]',
 '{"host": {"type": "string", "required": true}, "port": {"type": "number", "default": 9000}, "database": {"type": "string", "required": true}, "username": {"type": "string", "required": true}, "password": {"type": "string", "required": true, "sensitive": true}}',
 '["data_warehouse", "analytics", "olap"]', 'ClickHouse', 'official'),

-- Cloud Providers
('aws', 'Amazon Web Services', 'cloud_provider', '1.0.0', 'AWS cloud services integration',
 '["pull", "query", "push"]',
 '{"access_key_id": {"type": "string", "required": true, "sensitive": true}, "secret_access_key": {"type": "string", "required": true, "sensitive": true}, "region": {"type": "string", "required": true}}',
 '["cloud", "aws", "infrastructure"]', 'Amazon', 'official'),

('azure', 'Microsoft Azure', 'cloud_provider', '1.0.0', 'Azure cloud services integration',
 '["pull", "query", "push"]',
 '{"subscription_id": {"type": "string", "required": true}, "client_id": {"type": "string", "required": true}, "client_secret": {"type": "string", "required": true, "sensitive": true}, "tenant_id": {"type": "string", "required": true}}',
 '["cloud", "azure", "microsoft"]', 'Microsoft', 'official'),

('gcp', 'Google Cloud Platform', 'cloud_provider', '1.0.0', 'GCP cloud services integration',
 '["pull", "query", "push"]',
 '{"project_id": {"type": "string", "required": true}, "credentials": {"type": "object", "required": true, "sensitive": true}}',
 '["cloud", "gcp", "google"]', 'Google', 'official'),

-- Ticketing Systems
('jira', 'Atlassian Jira', 'ticketing', '1.0.0', 'Jira issue tracking and project management',
 '["push", "pull", "bidirection"]',
 '{"base_url": {"type": "string", "required": true}, "username": {"type": "string", "required": true}, "api_token": {"type": "string", "required": true, "sensitive": true}, "project_key": {"type": "string", "required": true}}',
 '["ticketing", "project_management", "atlassian"]', 'Atlassian', 'official'),

('servicenow', 'ServiceNow', 'ticketing', '1.0.0', 'ServiceNow IT service management',
 '["push", "pull", "bidirection"]',
 '{"instance_url": {"type": "string", "required": true}, "username": {"type": "string", "required": true}, "password": {"type": "string", "required": true, "sensitive": true}}',
 '["ticketing", "itsm", "enterprise"]', 'ServiceNow', 'official'),

-- AI Providers
('openai', 'OpenAI', 'ai', '1.0.0', 'OpenAI GPT models for analysis',
 '["query", "execute"]',
 '{"api_key": {"type": "string", "required": true, "sensitive": true}, "model": {"type": "string", "default": "gpt-4", "options": ["gpt-4", "gpt-3.5-turbo"]}, "base_url": {"type": "string", "default": "https://api.openai.com"}}',
 '["ai", "nlp", "analysis"]', 'OpenAI', 'official'),

('anthropic', 'Anthropic', 'ai', '1.0.0', 'Anthropic Claude models for analysis',
 '["query", "execute"]',
 '{"api_key": {"type": "string", "required": true, "sensitive": true}, "model": {"type": "string", "default": "claude-3-sonnet-20240229"}, "base_url": {"type": "string", "default": "https://api.anthropic.com"}}',
 '["ai", "nlp", "analysis"]', 'Anthropic', 'official'),

('google_ai', 'Google AI', 'ai', '1.0.0', 'Google Gemini models for analysis',
 '["query", "execute"]',
 '{"api_key": {"type": "string", "required": true, "sensitive": true}, "model": {"type": "string", "default": "gemini-pro"}, "base_url": {"type": "string", "default": "https://generativelanguage.googleapis.com"}}',
 '["ai", "nlp", "analysis"]', 'Google', 'official'),

('ollama', 'Ollama', 'ai', '1.0.0', 'Local Ollama AI models',
 '["query", "execute"]',
 '{"base_url": {"type": "string", "required": true, "default": "http://localhost:11434"}, "model": {"type": "string", "required": true}}',
 '["ai", "nlp", "local"]', 'Ollama', 'community')

ON CONFLICT (id) DO NOTHING;

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_provider_configurations_provider_id ON provider_configurations(provider_id);
CREATE INDEX IF NOT EXISTS idx_provider_configurations_enabled ON provider_configurations(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_provider_configurations_status ON provider_configurations(status);
CREATE INDEX IF NOT EXISTS idx_provider_configurations_created_by ON provider_configurations(created_by);

CREATE INDEX IF NOT EXISTS idx_provider_specs_type ON provider_specs(type);
CREATE INDEX IF NOT EXISTS idx_provider_specs_tags ON provider_specs USING GIN(tags);
CREATE INDEX IF NOT EXISTS idx_provider_specs_support_level ON provider_specs(support_level);

CREATE INDEX IF NOT EXISTS idx_provider_sync_logs_provider_id ON provider_sync_logs(provider_config_id);
CREATE INDEX IF NOT EXISTS idx_provider_sync_logs_status ON provider_sync_logs(status);
CREATE INDEX IF NOT EXISTS idx_provider_sync_logs_started_at ON provider_sync_logs(started_at DESC);

CREATE INDEX IF NOT EXISTS idx_provider_metrics_provider_id ON provider_metrics(provider_config_id);
CREATE INDEX IF NOT EXISTS idx_provider_metrics_timestamp ON provider_metrics(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_provider_metrics_name ON provider_metrics(metric_name);

CREATE INDEX IF NOT EXISTS idx_provider_webhooks_provider_id ON provider_webhooks(provider_config_id);
CREATE INDEX IF NOT EXISTS idx_provider_webhooks_path ON provider_webhooks(webhook_path);
CREATE INDEX IF NOT EXISTS idx_provider_webhooks_enabled ON provider_webhooks(enabled) WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_provider_data_mappings_provider_id ON provider_data_mappings(provider_config_id);
CREATE INDEX IF NOT EXISTS idx_provider_data_mappings_enabled ON provider_data_mappings(enabled) WHERE enabled = TRUE;

-- Create trigger to update updated_at timestamp
CREATE TRIGGER update_provider_configurations_updated_at 
    BEFORE UPDATE ON provider_configurations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_provider_specs_updated_at 
    BEFORE UPDATE ON provider_specs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_provider_webhooks_updated_at 
    BEFORE UPDATE ON provider_webhooks
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();