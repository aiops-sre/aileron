# Self-Service Configuration Management System

## 🎯 Overview

AlertHub Enterprise now includes a **comprehensive self-service configuration management system** that enables complete UI-driven customization of all intelligent AIOps features without requiring backend code changes or system restarts.

---

## 🏗️ System Architecture

### Core Components

```
┌──────────────────────────────────────────────────────────────┐
│                    Configuration UI Layer                     │
│  ┌─────────────┬──────────────┬──────────────┬─────────────┐ │
│  │  Wizards &  │  Form        │  Visual      │  Drag-Drop  │ │
│  │  Templates  │  Builders    │  Editors     │  Composers  │ │
│  └─────────────┴──────────────┴──────────────┴─────────────┘ │
└────────────────────────────┬─────────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────────┐
│              Configuration Management Backend                 │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │  Schema Validation │ Versioning │ Audit Trail │ RBAC   │ │
│  └─────────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │  Real-time Sync │ Rollback │ Export/Import │ Templates │ │
│  └─────────────────────────────────────────────────────────┘ │
└────────────────────────────┬─────────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────────┐
│                 Configuration Database                        │
│  (PostgreSQL with JSONB for flexible schema-less configs)   │
└───────────────────────────────────────────────────────────────┘
```

---

## 📊 Database Schema

**Location**: [`database/migrations/configuration_management.sql`](../database/migrations/configuration_management.sql)

### Core Tables:

1. **`config_namespaces`** - Organizational structure
   - Pre-populated with 14 system namespaces
   - Supports team/user/global access levels

2. **`config_schemas`** - Schema definitions with validation
   - JSON Schema validation
   - UI rendering hints
   - Version management

3. **`config_entries`** - Actual configuration values
   - Environment-specific (dev/staging/production)
   - Version tracking
   - Encryption support

4. **`config_history`** - Complete audit trail
   - Every change tracked
   - Rollback capability
   - Change attribution

5. **`config_templates`** - Reusable patterns
   - Public template library
   - Usage tracking
   - Rating system

6. **`config_deployments`** - Deployment management
   - Canary deployments
   - Blue-green deployments
   - Rollback support

7. **`config_approvals`** - Approval workflows
   - Multi-approver support
   - Expiration handling

8. **`config_locks`** - Concurrent modification prevention

9. **`config_dependencies`** - Inter-config dependencies

10. **`config_notifications`** - Change notifications

11. **`config_cost_tracking`** - LLM usage cost tracking

---

## 🎛️ Configurable Features

### 1. **Burst Pattern Detection Parameters**

**Namespace**: `aiops.burst_detection`

```json
{
  "burst_threshold": {
    "type": "integer",
    "default": 10,
    "min": 5,
    "max": 100,
    "description": "Number of alerts in time window to trigger burst",
    "ui_widget": "slider"
  },
  "storm_threshold": {
    "type": "integer",
    "default": 50,
    "min": 20,
    "max": 500,
    "description": "Number of alerts to escalate to storm",
    "ui_widget": "slider"
  },
  "time_window": {
    "type": "duration",
    "default": "5m",
    "options": ["1m", "5m", "10m", "15m", "30m"],
    "ui_widget": "dropdown"
  },
  "sensitivity_level": {
    "type": "string",
    "default": "medium",
    "enum": ["low", "medium", "high", "critical"],
    "ui_widget": "radio_buttons"
  },
  "auto_suppression": {
    "type": "boolean",
    "default": true,
    "description": "Automatically suppress duplicate alerts during storms",
    "ui_widget": "toggle"
  },
  "notification_channels": {
    "type": "array",
    "items": {"type": "string"},
    "default": ["slack", "email"],
    "ui_widget": "multiselect"
  }
}
```

### 2. **Multi-Agent Framework Settings**

**Namespace**: `aiops.agents`

```json
{
  "enabled_agents": {
    "type": "array",
    "items": {"type": "string"},
    "default": ["detection", "correlation", "analysis", "remediation"],
    "options": ["detection", "correlation", "analysis", "remediation", "learning", "topology"],
    "ui_widget": "checkbox_group"
  },
  "orchestrator": {
    "type": "object",
    "properties": {
      "task_queue_size": {"type": "integer", "default": 1000},
      "parallel_execution": {"type": "boolean", "default": true},
      "retry_policy": {
        "max_retries": 3,
        "backoff": "exponential"
      }
    }
  },
  "llm_provider": {
    "type": "string",
    "enum": ["openai", "anthropic", "google", "azure", "ollama", "deepseek", "grok"],
    "default": "openai",
    "ui_widget": "dropdown"
  },
  "llm_model": {
    "type": "string",
    "default": "gpt-4",
    "dynamic_options_from": "llm_provider",
    "ui_widget": "searchable_dropdown"
  },
  "agent_coordination_rules": {
    "type": "array",
    "items": {
      "type": "object",
      "properties": {
        "trigger_agent": "string",
        "next_agent": "string",
        "condition": "string"
      }
    },
    "ui_widget": "visual_workflow_editor"
  }
}
```

### 3. **ML Model Parameters**

**Namespace**: `aiops.ml_models`

```json
{
  "anomaly_detection": {
    "sensitivity": {
      "type": "float",
      "default": 0.85,
      "min": 0.5,
      "max": 0.99,
      "ui_widget": "slider_with_value"
    },
    "algorithm": {
      "type": "string",
      "enum": ["isolation_forest", "lstm", "statistical", "ensemble"],
      "default": "isolation_forest",
      "ui_widget": "radio_cards"
    },
    "training_schedule": {
      "type": "object",
      "properties": {
        "frequency": {"enum": ["hourly", "daily", "weekly", "monthly"]},
        "time_of_day": "time",
        "auto_retrain": "boolean"
      },
      "ui_widget": "schedule_picker"
    }
  },
  "feature_engineering": {
    "enabled_features": {
      "type": "array",
      "items": "string",
      "default": ["alert_frequency", "severity_distribution", "time_patterns"],
      "ui_widget": "feature_selector"
    },
    "custom_features": {
      "type": "array",
      "items": {
        "name": "string",
        "formula": "string",
        "type": "string"
      },
      "ui_widget": "code_editor"
    }
  }
}
```

### 4. **Alert Routing & Escalation Policies**

**Namespace**: `escalation_policies`

```json
{
  "routing_rules": {
    "type": "array",
    "items": {
      "type": "object",
      "properties": {
        "name": "string",
        "conditions": {
          "severity": ["critical", "high", "medium", "low"],
          "source": "array",
          "tags": "array",
          "custom_expression": "string"
        },
        "actions": {
          "assign_to_team": "uuid",
          "notify_channels": "array",
          "create_incident": "boolean",
          "run_workflow": "uuid"
        },
        "priority": "integer"
      }
    },
    "ui_widget": "rule_composer"
  },
  "escalation_levels": {
    "type": "array",
    "items": {
      "level": "integer",
      "delay": "duration",
      "notify": "array",
      "condition": "string"
    },
    "ui_widget": "escalation_ladder"
  },
  "business_hours": {
    "timezone": "string",
    "working_hours": {
      "monday": {"start": "09:00", "end": "17:00"},
      "tuesday": {"start": "09:00", "end": "17:00"}
      // ... etc
    },
    "holidays": "array",
    "ui_widget": "business_hours_editor"
  }
}
```

### 5. **Notification Channels & Templates**

**Namespace**: `notifications`

```json
{
  "channels": {
    "type": "array",
    "items": {
      "type": "object",
      "properties": {
        "channel_type": {"enum": ["slack", "email", "webhook", "pagerduty", "teams"]},
        "name": "string",
        "config": "object",
        "enabled": "boolean",
        "tenant_id": "uuid"
      }
    },
    "ui_widget": "channel_manager"
  },
  "templates": {
    "type": "object",
    "properties": {
      "email": {
        "subject": "template_string",
        "body_html": "template_string",
        "body_text": "template_string"
      },
      "slack": {
        "message": "template_string",
        "blocks": "array"
      }
    },
    "ui_widget": "template_editor"
  },
  "multi_tenancy": {
    "enabled": "boolean",
    "tenant_isolation": "strict|flexible",
    "shared_channels": "array"
  }
}
```

### 6. **Integration Connectors**

**Namespace**: `integrations.dynatrace`

```json
{
  "connection": {
    "url": {
      "type": "string",
      "format": "url",
      "required": true,
      "ui_widget": "url_input"
    },
    "api_token": {
      "type": "string",
      "encrypted": true,
      "required": true,
      "ui_widget": "password_input"
    },
    "verify_ssl": {
      "type": "boolean",
      "default": true
    }
  },
  "sync_settings": {
    "sync_interval": {
      "type": "duration",
      "default": "5m",
      "ui_widget": "duration_picker"
    },
    "entity_types": {
      "type": "array",
      "items": "string",
      "default": ["host", "process", "service", "application"],
      "ui_widget": "multiselect"
    },
    "tag_mapping": {
      "type": "object",
      "additionalProperties": "string",
      "ui_widget": "key_value_editor"
    }
  },
  "alert_ingestion": {
    "enabled": true,
    "filters": {
      "severity": "array",
      "entity_types": "array"
    }
  }
}
```

### 7. **Correlation Rules & Pattern Matching**

**Namespace**: `aiops.correlation`

```json
{
  "correlation_rules": {
    "type": "array",
    "items": {
      "name": "string",
      "description": "string",
      "pattern_type": {"enum": ["time_based", "source_based", "tag_based", "ml_similarity"]},
      "conditions": "object",
      "actions": "array",
      "confidence_threshold": "float",
      "enabled": "boolean"
    },
    "ui_widget": "rule_builder"
  },
  "similarity_algorithm": {
    "type": "string",
    "enum": ["levenshtein", "jaccard", "cosine", "ml_embedding"],
    "default": "levenshtein"
  },
  "time_window": {
    "type": "duration",
    "default": "30m"
  }
}
```

### 8. **Auto-Remediation Workflows**

**Namespace**: `aiops.remediation`

```json
{
  "runbooks": {
    "type": "array",
    "items": {
      "name": "string",
      "description": "string",
      "trigger_conditions": "object",
      "steps": {
        "type": "array",
        "items": {
          "step_name": "string",
          "action_type": {"enum": ["api_call", "script", "workflow", "manual"]},
          "config": "object",
          "timeout": "duration",
          "rollback_on_failure": "boolean"
        }
      },
      "auto_execute": "boolean",
      "requires_approval": "boolean",
      "approval_count": "integer"
    },
    "ui_widget": "workflow_builder"
  },
  "auto_remediation": {
    "enabled": "boolean",
    "safe_mode": "boolean",
    "max_concurrent": "integer",
    "allowed_hours": "business_hours"
  }
}
```

### 9. **SLO/SLA Definitions**

**Namespace**: `slo_sla`

```json
{
  "slos": {
    "type": "array",
    "items": {
      "name": "string",
      "description": "string",
      "metric": "string",
      "target": "float",
      "window": "duration",
      "breach_actions": "array",
      "severity": {"enum": ["critical", "high", "medium", "low"]}
    },
    "ui_widget": "slo_editor"
  },
  "sla_tracking": {
    "response_time_target": "duration",
    "resolution_time_target": "duration",
    "availability_target": "float"
  }
}
```

### 10. **Dashboard Layouts & Visualizations**

**Namespace**: `ui.dashboards`

```json
{
  "layouts": {
    "type": "array",
    "items": {
      "name": "string",
      "is_default": "boolean",
      "widgets": {
        "type": "array",
        "items": {
          "widget_type": "string",
          "position": {"x": "int", "y": "int", "w": "int", "h": "int"},
          "config": "object"
        }
      }
    },
    "ui_widget": "drag_drop_editor"
  },
  "visualization_preferences": {
    "theme": {"enum": ["light", "dark", "auto"]},
    "chart_library": {"enum": ["recharts", "d3", "chartjs"]},
    "refresh_interval": "duration"
  }
}
```

### 11. **RBAC Policies**

**Namespace**: `rbac`

```json
{
  "roles": {
    "type": "array",
    "items": {
      "role_name": "string",
      "description": "string",
      "permissions": "array",
      "inherits_from": "array"
    },
    "ui_widget": "role_editor"
  },
  "team_access_policies": {
    "type": "array",
    "items": {
      "team_id": "uuid",
      "namespaces": "array",
      "read": "boolean",
      "write": "boolean",
      "admin": "boolean"
    }
  }
}
```

### 12. **Data Retention & Archival**

**Namespace**: `data_retention`

```json
{
  "retention_policies": {
    "alerts": {
      "active_retention": "90d",
      "resolved_retention": "180d",
      "archive_after": "365d"
    },
    "incidents": {
      "retention": "2y",
      "archive_after": "3y"
    },
    "metrics": {
      "high_resolution": "30d",
      "aggregated": "2y"
    }
  },
  "archival": {
    "enabled": "boolean",
    "storage_type": {"enum": ["s3", "gcs", "azure_blob"]},
    "compression": "boolean",
    "encryption": "boolean"
  }
}
```

### 13. **Cost Optimization Settings**

**Namespace**: `cost_optimization`

```json
{
  "llm_usage": {
    "budget_limit": {
      "type": "float",
      "default": 1000.0,
      "currency": "USD"
    },
    "cost_alerts": {
      "threshold_percentage": 80,
      "notify": "array"
    },
    "usage_optimization": {
      "cache_responses": "boolean",
      "fallback_to_cheaper_model": "boolean",
      "rate_limiting": {
        "max_requests_per_minute": "integer",
        "max_tokens_per_day": "integer"
      }
    }
  },
  "model_selection_strategy": {
    "type": "string",
    "enum": ["always_premium", "cost_optimized", "balanced"],
    "default": "balanced"
  }
}
```

### 14. **Deployment Configurations**

**Namespace**: `deployment`

```json
{
  "components": {
    "backend": {
      "replicas": "integer",
      "resources": {
        "cpu": "string",
        "memory": "string"
      },
      "auto_scaling": {
        "enabled": "boolean",
        "min_replicas": "integer",
        "max_replicas": "integer",
        "target_cpu": "integer"
      }
    },
    "agents": {
      "detection_agent": {"enabled": true, "workers": 2},
      "correlation_agent": {"enabled": true, "workers": 2},
      "analysis_agent": {"enabled": true, "workers": 1},
      "remediation_agent": {"enabled": true, "workers": 1}
    }
  }
}
```

---

## 🎨 UI Components (Frontend)

### Configuration Wizards

**Location**: `sre-command-center/src/components/config/ConfigWizards.tsx`

```typescript
// Burst Detection Configuration Wizard
export const BurstDetectionWizard = () => {
  const steps = [
    {
      title: "Thresholds",
      component: <ThresholdConfig />
    },
    {
      title: "Sensitivity",
      component: <SensitivityConfig />
    },
    {
      title: "Notifications",
      component: <NotificationConfig />
    },
    {
      title: "Review & Save",
      component: <ReviewConfig />
    }
  ];
  
  return <WizardStepper steps={steps} />;
};
```

### Form Builders

**Dynamic Form Generation from JSON Schema**:

```typescript
export const DynamicConfigForm = ({ namespace, schema }) => {
  const formFields = generateFormFields(schema);
  
  return (
    <Form onSubmit={handleSave}>
      {formFields.map(field => (
        <DynamicField key={field.name} {...field} />
      ))}
      <Button type="submit">Save Configuration</Button>
    </Form>
  );
};
```

### Visual Workflow Editor

**Drag-and-Drop Workflow Builder**:

```typescript
export const VisualWorkflowEditor = () => {
  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
    >
      <Controls />
      <MiniMap />
      <Background />
    </ReactFlow>
  );
};
```

### Rule Composer

**Drag-and-Drop Rule Builder**:

```typescript
export const RuleComposer = () => {
  return (
    <RuleBuilder
      fields={availableFields}
      operators={operators}
      onRuleChange={handleRuleChange}
    />
  );
};
```

---

## 🔄 Real-Time Updates

### WebSocket-Based Configuration Sync

```typescript
// Frontend subscription
const useConfigSync = (namespace) => {
  useEffect(() => {
    const ws = new WebSocket(`ws://api/config/subscribe/${namespace}`);
    
    ws.onmessage = (event) => {
      const update = JSON.parse(event.data);
      // Update local state
      updateLocalConfig(update);
    };
    
    return () => ws.close();
  }, [namespace]);
};
```

### Backend Broadcasting

```go
// Broadcast configuration changes
func (cm *ConfigManager) BroadcastChange(namespace string, config *ConfigEntry) {
    message := ConfigChangeMessage{
        Namespace: namespace,
        Key:       config.Key,
        Value:     config.Value,
        Timestamp: time.Now(),
    }
    
    // Broadcast to all connected clients
    cm.broadcaster.Publish(namespace, message)
}
```

---

## 📦 Export/Import Functionality

### Export Configuration

```bash
# Export all configurations
GET /api/v1/config/export?namespace=aiops.agents&format=json

# Export as Terraform
GET /api/v1/config/export?namespace=aiops.agents&format=terraform

# Export as YAML
GET /api/v1/config/export?namespace=aiops.agents&format=yaml
```

### Import Configuration

```bash
# Import from file
POST /api/v1/config/import
Content-Type: multipart/form-data

{
  "file": <config_file>,
  "environment": "production",
  "merge_strategy": "overwrite|merge|skip"
}
```

---

## ✅ Validation & Schema Enforcement

### JSON Schema Validation

```go
func (cm *ConfigManager) ValidateConfig(config *ConfigEntry) error {
    schema := cm.getSchema(config.NamespaceID)
    
    validator := jsonschema.NewValidator(schema.SchemaDefinition)
    if err := validator.Validate(config.Value); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    
    // Custom validators
    for _, validator := range schema.CustomValidators {
        if err := cm.executeValidator(validator, config); err != nil {
            return err
        }
    }
    
    return nil
}
```

---

## 🔒 Permission-Based Access

### Configuration Access Control

```go
func (cm *ConfigManager) CheckAccess(userID uuid.UUID, namespace string, operation string) error {
    ns := cm.getNamespace(namespace)
    
    switch ns.AccessLevel {
    case "global":
        return nil // Everyone can read
    case "team":
        return cm.checkTeamAccess(userID, ns.OwnerTeamID, operation)
    case "user":
        return cm.checkUserAccess(userID, ns.CreatedBy, operation)
    }
    
    return ErrUnauthorized
}
```

---

## 🔔 Change Notifications

### Configuration Change Alerts

```go
func (cm *ConfigManager) NotifyChange(config *ConfigEntry) {
    subscriptions := cm.getSubscriptions(config.NamespaceID)
    
    for _, sub := range subscriptions {
        switch sub.NotificationType {
        case "email":
            cm.sendEmail(sub.NotificationTarget, config)
        case "slack":
            cm.sendSlack(sub.NotificationTarget, config)
        case "webhook":
            cm.callWebhook(sub.NotificationTarget, config)
        }
    }
}
```

---

## 🔄 Rollback Capabilities

### Configuration Rollback

```bash
# List configuration history
GET /api/v1/config/history/:namespace/:key

# Rollback to specific version
POST /api/v1/config/rollback
{
  "namespace": "aiops.agents",
  "key": "llm_provider",
  "version": 5
}

# Rollback entire namespace
POST /api/v1/config/rollback/namespace
{
  "namespace": "aiops.agents",
  "timestamp": "2024-01-01T00:00:00Z"
}
```

---

## 📊 Configuration Analytics

### Usage Tracking & Cost Analysis

```bash
# Get configuration usage metrics
GET /api/v1/config/analytics/usage?namespace=aiops.ml_models

# Get cost breakdown
GET /api/v1/config/analytics/costs?period=last_30_days

# Get most changed configurations
GET /api/v1/config/analytics/changes?limit=10
```

---

## 🚀 Deployment

### Apply Database Migration

```bash
psql -U postgres -d alerthub -f database/migrations/configuration_management.sql
```

### Initialize System Configurations

All system namespaces are automatically created with default values during migration.

---

## 📝 API Summary

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/config/namespaces` | GET | List all namespaces |
| `/api/v1/config/:namespace` | GET | Get all configs in namespace |
| `/api/v1/config/:namespace/:key` | GET | Get specific config |
| `/api/v1/config/:namespace/:key` | PUT | Update configuration |
| `/api/v1/config/:namespace/:key` | DELETE | Delete configuration |
| `/api/v1/config/templates` | GET | List templates |
| `/api/v1/config/apply-template` | POST | Apply template |
| `/api/v1/config/export` | GET | Export configurations |
| `/api/v1/config/import` | POST | Import configurations |
| `/api/v1/config/history/:id` | GET | Get config history |
| `/api/v1/config/rollback` | POST | Rollback configuration |
| `/api/v1/config/approve/:id` | POST | Approve configuration change |
| `/api/v1/config/deploy` | POST | Deploy configuration |

---

## 🎉 Summary

The **Self-Service Configuration Management System** provides:

✅ **Complete UI-Driven Customization** - No code changes needed
✅ **14 Pre-Configured Namespaces** - All AIOps features configurable
✅ **Schema Validation** - Prevent invalid configurations
✅ **Version Control & Audit Trail** - Full history tracking
✅ **Real-Time Synchronization** - Changes propagate immediately
✅ **Rollback Support** - Revert to any previous version
✅ **Multi-Environment Support** - Dev/Staging/Production isolation
✅ **Template Library** - Reusable best practices
✅ **Approval Workflows** - Governance for critical changes
✅ **Cost Tracking** - Monitor LLM and resource usage
✅ **Export/Import** - Backup and migration support
✅ **RBAC Integration** - Permission-based access control
✅ **Change Notifications** - Stay informed of updates

This enables **complete platform customization through the UI** without requiring backend deployments or code modifications.
