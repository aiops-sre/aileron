-- Migration: Add workflow automation system tables
-- Version: workflows_v1.0.0

-- Workflows table
CREATE TABLE IF NOT EXISTS workflows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    triggers JSONB NOT NULL DEFAULT '[]',
    steps JSONB NOT NULL DEFAULT '[]',
    enabled BOOLEAN DEFAULT TRUE,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    version INTEGER DEFAULT 1,
    tags JSONB DEFAULT '[]',
    
    UNIQUE(name)
);

-- Workflow executions table
CREATE TABLE IF NOT EXISTS workflow_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'running',
    trigger_event JSONB NOT NULL DEFAULT '{}',
    context JSONB DEFAULT '{}',
    step_results JSONB DEFAULT '{}',
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    error TEXT,
    logs JSONB DEFAULT '[]',
    
    CHECK (status IN ('running', 'completed', 'failed', 'cancelled'))
);

-- Workflow schedules table for scheduled workflows
CREATE TABLE IF NOT EXISTS workflow_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    schedule_type VARCHAR(50) NOT NULL,
    cron_expression VARCHAR(255),
    interval_seconds INTEGER,
    timezone VARCHAR(100) DEFAULT 'UTC',
    enabled BOOLEAN DEFAULT TRUE,
    last_run_at TIMESTAMP WITH TIME ZONE,
    next_run_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (schedule_type IN ('cron', 'interval', 'once'))
);

-- Workflow triggers table for event-based triggers
CREATE TABLE IF NOT EXISTS workflow_triggers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    trigger_type VARCHAR(50) NOT NULL,
    event_type VARCHAR(100) NOT NULL,
    conditions JSONB DEFAULT '[]',
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (trigger_type IN ('alert', 'incident', 'webhook', 'manual', 'schedule'))
);

-- Workflow templates table for reusable workflow templates
CREATE TABLE IF NOT EXISTS workflow_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(100),
    template_data JSONB NOT NULL,
    parameters JSONB DEFAULT '[]',
    tags JSONB DEFAULT '[]',
    public BOOLEAN DEFAULT FALSE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    usage_count INTEGER DEFAULT 0,
    
    UNIQUE(name)
);

-- Workflow execution steps table for detailed step tracking
CREATE TABLE IF NOT EXISTS workflow_execution_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id UUID NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
    step_id VARCHAR(255) NOT NULL,
    step_name VARCHAR(255) NOT NULL,
    step_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_ms INTEGER,
    attempts INTEGER DEFAULT 0,
    output JSONB DEFAULT '{}',
    error TEXT,
    metadata JSONB DEFAULT '{}',
    
    CHECK (status IN ('pending', 'running', 'success', 'failure', 'skipped', 'cancelled'))
);

-- Workflow permissions table
CREATE TABLE IF NOT EXISTS workflow_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
    permission_type VARCHAR(50) NOT NULL,
    granted_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (permission_type IN ('view', 'execute', 'edit', 'delete', 'manage')),
    CONSTRAINT workflow_permissions_user_or_role CHECK (
        (user_id IS NOT NULL AND role_id IS NULL) OR 
        (user_id IS NULL AND role_id IS NOT NULL)
    )
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_workflows_enabled ON workflows(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_workflows_created_by ON workflows(created_by);
CREATE INDEX IF NOT EXISTS idx_workflows_tags ON workflows USING GIN(tags);

CREATE INDEX IF NOT EXISTS idx_workflow_executions_workflow_id ON workflow_executions(workflow_id);
CREATE INDEX IF NOT EXISTS idx_workflow_executions_status ON workflow_executions(status);
CREATE INDEX IF NOT EXISTS idx_workflow_executions_started_at ON workflow_executions(started_at DESC);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_workflow_id ON workflow_schedules(workflow_id);
CREATE INDEX IF NOT EXISTS idx_workflow_schedules_next_run ON workflow_schedules(next_run_at) WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_workflow_triggers_workflow_id ON workflow_triggers(workflow_id);
CREATE INDEX IF NOT EXISTS idx_workflow_triggers_event_type ON workflow_triggers(event_type) WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_workflow_templates_category ON workflow_templates(category);
CREATE INDEX IF NOT EXISTS idx_workflow_templates_public ON workflow_templates(public) WHERE public = TRUE;
CREATE INDEX IF NOT EXISTS idx_workflow_templates_tags ON workflow_templates USING GIN(tags);

CREATE INDEX IF NOT EXISTS idx_workflow_execution_steps_execution_id ON workflow_execution_steps(execution_id);
CREATE INDEX IF NOT EXISTS idx_workflow_execution_steps_status ON workflow_execution_steps(status);

CREATE INDEX IF NOT EXISTS idx_workflow_permissions_workflow_id ON workflow_permissions(workflow_id);
CREATE INDEX IF NOT EXISTS idx_workflow_permissions_user_id ON workflow_permissions(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_workflow_permissions_role_id ON workflow_permissions(role_id) WHERE role_id IS NOT NULL;

-- Insert sample workflow templates
INSERT INTO workflow_templates (name, description, category, template_data, parameters, tags, public) VALUES
(
    'Alert Escalation Workflow',
    'Escalates critical alerts to on-call team after a specified timeout',
    'alerting',
    '{
        "triggers": [
            {
                "type": "alert",
                "conditions": [
                    {"field": "severity", "operator": "equals", "value": "critical"}
                ]
            }
        ],
        "steps": [
            {
                "id": "wait",
                "name": "Wait for acknowledgment",
                "type": "action",
                "action": {
                    "type": "wait",
                    "parameters": {"duration": "{{escalation_timeout}}"}
                }
            },
            {
                "id": "check_status",
                "name": "Check alert status",
                "type": "condition",
                "condition": {
                    "expression": "trigger.status == \"open\""
                }
            },
            {
                "id": "page_oncall",
                "name": "Page on-call team",
                "type": "action",
                "action": {
                    "type": "pagerduty",
                    "parameters": {
                        "integration_key": "{{pagerduty_key}}",
                        "summary": "Critical alert requires attention: {{trigger.title}}",
                        "severity": "critical"
                    }
                }
            }
        ]
    }',
    '[
        {"name": "escalation_timeout", "type": "duration", "default": "5m", "description": "Time to wait before escalation"},
        {"name": "pagerduty_key", "type": "string", "required": true, "description": "PagerDuty integration key"}
    ]',
    '["alerting", "escalation", "pagerduty"]',
    TRUE
),
(
    'Incident Creation Workflow',
    'Automatically creates incidents for high-severity alerts from critical services',
    'incident-management',
    '{
        "triggers": [
            {
                "type": "alert",
                "conditions": [
                    {"field": "severity", "operator": "in", "value": ["critical", "high"]},
                    {"field": "labels.service_tier", "operator": "equals", "value": "critical"}
                ]
            }
        ],
        "steps": [
            {
                "id": "create_incident",
                "name": "Create incident",
                "type": "action",
                "action": {
                    "type": "create_incident",
                    "parameters": {
                        "title": "{{trigger.title}}",
                        "description": "Automatically created incident for alert: {{trigger.description}}",
                        "severity": "{{trigger.severity}}"
                    }
                }
            },
            {
                "id": "notify_team",
                "name": "Notify incident response team",
                "type": "action",
                "action": {
                    "type": "slack",
                    "parameters": {
                        "webhook_url": "{{slack_webhook}}",
                        "message": "🚨 New incident created: {{step_create_incident.title}} - Severity: {{trigger.severity}}",
                        "channel": "{{incident_channel}}"
                    }
                }
            }
        ]
    }',
    '[
        {"name": "slack_webhook", "type": "string", "required": true, "description": "Slack webhook URL for notifications"},
        {"name": "incident_channel", "type": "string", "default": "#incidents", "description": "Slack channel for incident notifications"}
    ]',
    '["incident-management", "automation", "slack"]',
    TRUE
),
(
    'Alert Correlation Workflow',
    'Groups similar alerts and suppresses duplicates',
    'correlation',
    '{
        "triggers": [
            {
                "type": "alert",
                "conditions": []
            }
        ],
        "steps": [
            {
                "id": "check_correlation",
                "name": "Check for similar alerts",
                "type": "action",
                "action": {
                    "type": "http",
                    "parameters": {
                        "url": "/api/v1/correlation/alert/{{trigger.alert_id}}",
                        "method": "GET"
                    }
                }
            },
            {
                "id": "handle_duplicate",
                "name": "Handle duplicate alert",
                "type": "condition",
                "condition": {
                    "expression": "step_check_correlation.is_duplicate == true"
                },
                "on_success": "suppress_alert"
            },
            {
                "id": "suppress_alert",
                "name": "Suppress duplicate alert",
                "type": "action",
                "action": {
                    "type": "update_alert",
                    "parameters": {
                        "alert_id": "{{trigger.alert_id}}",
                        "status": "suppressed"
                    }
                }
            }
        ]
    }',
    '[]',
    '["correlation", "deduplication", "automation"]',
    TRUE
) ON CONFLICT (name) DO NOTHING;

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_workflow_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_workflows_updated_at 
    BEFORE UPDATE ON workflows
    FOR EACH ROW EXECUTE FUNCTION update_workflow_updated_at();

CREATE TRIGGER update_workflow_schedules_updated_at 
    BEFORE UPDATE ON workflow_schedules
    FOR EACH ROW EXECUTE FUNCTION update_workflow_updated_at();

CREATE TRIGGER update_workflow_triggers_updated_at 
    BEFORE UPDATE ON workflow_triggers
    FOR EACH ROW EXECUTE FUNCTION update_workflow_updated_at();

CREATE TRIGGER update_workflow_templates_updated_at 
    BEFORE UPDATE ON workflow_templates
    FOR EACH ROW EXECUTE FUNCTION update_workflow_updated_at();