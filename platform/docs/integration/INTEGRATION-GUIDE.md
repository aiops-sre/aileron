# AlertHub Integration Guide

## Quick Start - Send Alerts to AlertHub

AlertHub provides multiple easy ways for developers to send alerts from their monitoring stacks.

## 1. REST API (Simplest)

### Send Alert via cURL
```bash
curl -X POST https://alerthub.example.com/api/v1/alerts/ingest \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key-here" \
  -d '{
    "title": "High CPU Usage on prod-server-01",
    "description": "CPU usage exceeded 90% for 5 minutes",
    "severity": "high",
    "source": "prometheus",
    "tags": ["cpu", "production", "server-01"],
    "metadata": {
      "hostname": "prod-server-01",
      "cpu_percent": 95.2,
      "threshold": 90
    }
  }'
```

### Response
```json
{
  "success": true,
  "data": {
    "alert_id": "123e4567-e89b-12d3-a456-426614174000",
    "status": "created",
    "ai_analysis": {
      "classification": "resource_exhaustion",
      "confidence": 0.92,
      "recommendations": ["Scale horizontally", "Check for memory leaks"]
    }
  }
}
```

## 2. Webhook Integration

### Configure Webhook in Your Monitoring Tool

**Prometheus Alertmanager**:
```yaml
receivers:
  - name: 'alerthub'
    webhook_configs:
      - url: 'https://alerthub.example.com/api/v1/webhooks/prometheus'
        send_resolved: true
        http_config:
          bearer_token: 'your-api-key-here'
```

**Grafana**:
```json
{
  "name": "AlertHub",
  "type": "webhook",
  "settings": {
    "url": "https://alerthub.example.com/api/v1/webhooks/grafana",
    "httpMethod": "POST",
    "authorization": "Bearer your-api-key-here"
  }
}
```

**Dynatrace**:
```json
{
  "type": "WEBHOOK",
  "name": "AlertHub Integration",
  "webhookUrl": "https://alerthub.example.com/api/v1/webhooks/dynatrace",
  "headers": [
    {
      "name": "X-API-Key",
      "value": "your-api-key-here"
    }
  ]
}
```

**PagerDuty**:
```json
{
  "webhook": {
    "endpoint_url": "https://alerthub.example.com/api/v1/webhooks/pagerduty",
    "type": "generic_v2_webhook",
    "headers": [
      {
        "name": "X-API-Key",
        "value": "your-api-key-here"
      }
    ]
  }
}
```

## 3. Python SDK

### Installation
```bash
pip install alerthub-sdk
```

### Usage
```python
from alerthub import AlertHubClient

# Initialize client
client = AlertHubClient(
    api_url="https://alerthub.example.com",
    api_key="your-api-key-here"
)

# Send alert
alert = client.create_alert(
    title="Database Connection Pool Exhausted",
    description="Connection pool reached maximum capacity",
    severity="critical",
    source="application",
    tags=["database", "connection-pool", "production"],
    metadata={
        "service": "user-service",
        "pool_size": 100,
        "active_connections": 100
    }
)

print(f"Alert created: {alert.id}")
print(f"AI Classification: {alert.ai_classification}")
print(f"Recommendations: {alert.ai_analysis.recommendations}")
```

## 4. Go SDK

### Installation
```bash
go get github.com/apple/alerthub-go-sdk
```

### Usage
```go
package main

import (
    "context"
    "github.com/apple/alerthub-go-sdk"
)

func main() {
    // Initialize client
    client := alerthub.NewClient("https://alerthub.example.com", "your-api-key-here")
    
    // Send alert
    alert, err := client.CreateAlert(context.Background(), &alerthub.Alert{
        Title:       "Memory Leak Detected",
        Description: "Memory usage increasing steadily",
        Severity:    "high",
        Source:      "monitoring",
        Tags:        []string{"memory", "leak", "production"},
        Metadata: map[string]interface{}{
            "service":      "api-gateway",
            "memory_mb":    2048,
            "threshold_mb": 1024,
        },
    })
    
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Alert ID: %s\n", alert.ID)
    fmt.Printf("AI Analysis: %+v\n", alert.AIAnalysis)
}
```

## 5. JavaScript/Node.js SDK

### Installation
```bash
npm install @apple/alerthub-sdk
```

### Usage
```javascript
const AlertHub = require('@apple/alerthub-sdk');

// Initialize client
const client = new AlertHub({
  apiUrl: 'https://alerthub.example.com',
  apiKey: 'your-api-key-here'
});

// Send alert
async function sendAlert() {
  const alert = await client.createAlert({
    title: 'API Response Time Degraded',
    description: 'P95 latency exceeded 500ms',
    severity: 'medium',
    source: 'apm',
    tags: ['api', 'performance', 'latency'],
    metadata: {
      endpoint: '/api/users',
      p95_latency_ms: 650,
      threshold_ms: 500
    }
  });
  
  console.log('Alert ID:', alert.id);
  console.log('AI Recommendations:', alert.aiAnalysis.recommendations);
}

sendAlert();
```

## 6. Prometheus Integration (Native)

### AlertHub Exporter
```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'alerthub'
    static_configs:
      - targets: ['alerthub.example.com:9090']
    
# alertmanager.yml
route:
  receiver: 'alerthub'
  
receivers:
  - name: 'alerthub'
    webhook_configs:
      - url: 'https://alerthub.example.com/api/v1/webhooks/prometheus'
        http_config:
          bearer_token: 'your-api-key-here'
```

## 7. Email Integration

### Send Alerts via Email
```
To: alerts@alerthub.example.com
Subject: [CRITICAL] Production Database Down
Body:
---
Title: Production Database Down
Severity: critical
Source: monitoring
Tags: database, production, outage
---
The production database cluster is unreachable.
Connection timeout after 30 seconds.
```

## 8. Slack Integration

### Slack Command
```
/alerthub create
Title: Disk Space Low
Severity: high
Description: /var partition at 95% capacity
Tags: disk, storage, production
```

### Slack App
```javascript
// In your Slack app
const response = await fetch('https://alerthub.example.com/api/v1/alerts/ingest', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'X-API-Key': process.env.ALERTHUB_API_KEY
  },
  body: JSON.stringify({
    title: 'Deployment Failed',
    description: `Deployment to ${environment} failed`,
    severity: 'high',
    source: 'slack',
    tags: ['deployment', environment]
  })
});
```

## 9. CLI Tool

### Installation
```bash
# macOS
brew install alerthub-cli

# Linux
curl -L https://alerthub.example.com/cli/install.sh | bash

# Configure
alerthub config set api-url https://alerthub.example.com
alerthub config set api-key your-api-key-here
```

### Usage
```bash
# Send alert
alerthub alert create \
  --title "Service Unavailable" \
  --severity critical \
  --description "User service returning 503" \
  --tags "service,503,production" \
  --source "healthcheck"

# List alerts
alerthub alert list --status open --severity critical

# Resolve alert
alerthub alert resolve <alert-id> --notes "Restarted service"

# Get AI analysis
alerthub alert analyze <alert-id>
```

## 10. Terraform Provider

### Installation
```hcl
terraform {
  required_providers {
    alerthub = {
      source  = "apple/alerthub"
      version = "~> 1.0"
    }
  }
}

provider "alerthub" {
  api_url = "https://alerthub.example.com"
  api_key = var.alerthub_api_key
}
```

### Usage
```hcl
resource "alerthub_alert_rule" "high_cpu" {
  name        = "High CPU Alert"
  description = "Alert when CPU exceeds 90%"
  severity    = "high"
  
  conditions {
    metric    = "cpu_usage"
    operator  = ">"
    threshold = 90
    duration  = "5m"
  }
  
  tags = ["cpu", "infrastructure"]
}
```

## 11. Kubernetes Integration

### Helm Chart
```bash
helm repo add alerthub https://charts.alerthub.example.com
helm install alerthub-agent alerthub/agent \
  --set apiKey=your-api-key-here \
  --set apiUrl=https://alerthub.example.com
```

### Kubernetes Event Watcher
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: alerthub-config
data:
  api-url: "https://alerthub.example.com"
  api-key: "your-api-key-here"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: alerthub-k8s-watcher
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: watcher
        image: alerthub/k8s-watcher:latest
        envFrom:
        - configMapRef:
            name: alerthub-config
```

## 12. API Key Management

### Generate API Key
```bash
# Via CLI
alerthub apikey create --name "prometheus-integration" --permissions "alerts.create"

# Via API
curl -X POST https://alerthub.example.com/api/v1/apikeys \
  -H "Authorization: Bearer your-jwt-token" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "grafana-integration",
    "permissions": ["alerts.create", "alerts.view"],
    "expires_in_days": 365
  }'
```

### Response
```json
{
  "success": true,
  "data": {
    "api_key": "ahk_1234567890abcdef",
    "name": "grafana-integration",
    "permissions": ["alerts.create", "alerts.view"],
    "expires_at": "2027-01-16T00:00:00Z"
  }
}
```

## 13. Batch Import

### Import Multiple Alerts
```bash
# CSV format
alerthub alert import alerts.csv

# JSON format
alerthub alert import alerts.json
```

### CSV Format
```csv
title,severity,description,source,tags
"High CPU","high","CPU > 90%","prometheus","cpu,prod"
"Disk Full","critical","Disk at 98%","monitoring","disk,prod"
```

### JSON Format
```json
{
  "alerts": [
    {
      "title": "High CPU",
      "severity": "high",
      "description": "CPU > 90%",
      "source": "prometheus",
      "tags": ["cpu", "prod"]
    }
  ]
}
```

## 14. Monitoring Stack Examples

### Prometheus + Alertmanager
```yaml
# alertmanager.yml
global:
  resolve_timeout: 5m

route:
  group_by: ['alertname', 'cluster']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 12h
  receiver: 'alerthub'

receivers:
- name: 'alerthub'
  webhook_configs:
  - url: 'https://alerthub.example.com/api/v1/webhooks/prometheus'
    send_resolved: true
    http_config:
      bearer_token: 'your-api-key-here'
```

### Grafana Alert
```json
{
  "name": "High Error Rate",
  "message": "Error rate > 5%",
  "notifications": [
    {
      "uid": "alerthub-webhook",
      "type": "webhook",
      "settings": {
        "url": "https://alerthub.example.com/api/v1/webhooks/grafana",
        "httpMethod": "POST",
        "authorization": "Bearer your-api-key-here"
      }
    }
  ]
}
```

### Dynatrace Problem Notification
```json
{
  "type": "WEBHOOK",
  "name": "AlertHub",
  "active": true,
  "alertingProfile": "default",
  "url": "https://alerthub.example.com/api/v1/webhooks/dynatrace",
  "headers": [
    {
      "name": "X-API-Key",
      "value": "{your-api-key}"
    }
  ],
  "payload": "{ProblemDetailsJSON}"
}
```

### Splunk Alert Action
```conf
[alert_actions]
alerthub.endpoint = https://alerthub.example.com/api/v1/webhooks/splunk
alerthub.api_key = your-api-key-here
```

## 15. Code Examples

### Python (from any application)
```python
import requests

def send_alert_to_alerthub(title, severity, description, **kwargs):
    """Send alert to AlertHub"""
    response = requests.post(
        'https://alerthub.example.com/api/v1/alerts/ingest',
        headers={
            'Content-Type': 'application/json',
            'X-API-Key': 'your-api-key-here'
        },
        json={
            'title': title,
            'severity': severity,
            'description': description,
            **kwargs
        }
    )
    return response.json()

# Usage
send_alert_to_alerthub(
    title="Database Query Slow",
    severity="medium",
    description="Query took 5.2 seconds",
    source="application",
    tags=["database", "performance"],
    metadata={"query_time": 5.2, "query": "SELECT * FROM users"}
)
```

### Go (from any Go application)
```go
package main

import (
    "bytes"
    "encoding/json"
    "net/http"
)

func SendAlertToAlertHub(title, severity, description string) error {
    alert := map[string]interface{}{
        "title":       title,
        "severity":    severity,
        "description": description,
        "source":      "application",
        "tags":        []string{"app", "production"},
    }
    
    jsonData, _ := json.Marshal(alert)
    
    req, _ := http.NewRequest("POST", 
        "https://alerthub.example.com/api/v1/alerts/ingest",
        bytes.NewBuffer(jsonData))
    
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-API-Key", "your-api-key-here")
    
    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    
    return nil
}
```

### JavaScript/Node.js
```javascript
async function sendAlert(title, severity, description) {
  const response = await fetch('https://alerthub.example.com/api/v1/alerts/ingest', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': 'your-api-key-here'
    },
    body: JSON.stringify({
      title,
      severity,
      description,
      source: 'application',
      tags: ['app', 'production']
    })
  });
  
  return await response.json();
}

// Usage
sendAlert('API Timeout', 'high', 'Request timeout after 30s');
```

## 16. Supported Alert Sources

AlertHub automatically detects and parses alerts from:

- ✅ **Prometheus** - Native Alertmanager format
- ✅ **Grafana** - Grafana alert format
- ✅ **Dynatrace** - Problem notifications
- ✅ **PagerDuty** - Incident webhooks
- ✅ **Splunk** - Alert actions
- ✅ **Datadog** - Monitor alerts
- ✅ **New Relic** - Violation webhooks
- ✅ **CloudWatch** - SNS notifications
- ✅ **Azure Monitor** - Action groups
- ✅ **Custom** - Generic JSON format

## 17. Alert Severity Levels

```
critical - Immediate action required
high     - Urgent attention needed
medium   - Should be addressed soon
low      - Informational
info     - For awareness only
```

## 18. Alert Lifecycle

```
open → acknowledged → investigating → resolved → closed
```

## 19. Auto-Features

When you send an alert to AlertHub, it automatically:

✅ **AI Analysis** - Classifies and analyzes the alert
✅ **Deduplication** - Groups similar alerts
✅ **Enrichment** - Adds context and metadata
✅ **Routing** - Routes to appropriate team/person
✅ **Escalation** - Escalates based on policies
✅ **Correlation** - Links related alerts
✅ **RCA** - Performs root cause analysis
✅ **Recommendations** - Suggests remediation steps

## 20. Testing Your Integration

### Test Alert
```bash
curl -X POST https://alerthub.example.com/api/v1/alerts/ingest \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key-here" \
  -d '{
    "title": "Test Alert",
    "severity": "info",
    "description": "Testing AlertHub integration",
    "source": "test",
    "tags": ["test"]
  }'
```

### Verify in UI
1. Login to https://alerthub.example.com
2. Navigate to Alerts
3. Filter by source: "test"
4. Verify alert appears with AI analysis

## 21. Best Practices

### ✅ DO
- Use descriptive titles
- Include relevant metadata
- Tag alerts appropriately
- Set correct severity
- Provide context in description
- Use source field consistently

### ❌ DON'T
- Send duplicate alerts (AlertHub deduplicates)
- Use generic titles like "Error"
- Omit severity
- Send test alerts to production
- Hardcode API keys in code

## 22. Rate Limits

- **Standard**: 1000 alerts/minute
- **Burst**: 5000 alerts/minute
- **Daily**: 100,000 alerts/day

Contact support for higher limits.

## 23. Support

- **Documentation**: https://docs.alerthub.example.com
- **Slack**: #help-interactive-sre
- **Email**: alerthub-support@apple.com
- **JIRA**: https://jira.iapps.example.com

## 24. Quick Reference

### API Endpoints
```
POST   /api/v1/alerts/ingest          - Create alert
GET    /api/v1/alerts                 - List alerts
GET    /api/v1/alerts/:id             - Get alert
PUT    /api/v1/alerts/:id             - Update alert
POST   /api/v1/alerts/:id/acknowledge - Acknowledge
POST   /api/v1/alerts/:id/resolve     - Resolve
POST   /api/v1/alerts/:id/ai-analyze  - AI analysis
```

### Webhook Endpoints
```
POST   /api/v1/webhooks/prometheus    - Prometheus
POST   /api/v1/webhooks/grafana       - Grafana
POST   /api/v1/webhooks/dynatrace     - Dynatrace
POST   /api/v1/webhooks/pagerduty     - PagerDuty
POST   /api/v1/webhooks/splunk        - Splunk
POST   /api/v1/webhooks/generic       - Generic
```

---

**Get Started in 2 Minutes!**

1. Get your API key from AlertHub UI
2. Choose your integration method
3. Send your first alert
4. View AI-powered insights

For detailed examples, visit: https://docs.alerthub.example.com/integrations
