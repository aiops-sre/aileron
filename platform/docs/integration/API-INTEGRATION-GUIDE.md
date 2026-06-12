# AlertHub API Integration Guide

## Overview

AlertHub provides a comprehensive REST API for accessing alerts, incidents, and analytics data. Other applications can integrate with AlertHub to retrieve alert information, create incidents, and manage alert lifecycle.

**Base URL**: `https://aileron.example.com/api/v1`

## Authentication

### Method 1: JWT Token (Recommended for User Applications)

**Step 1: Get Access Token**

```bash
curl -X POST https://aileron.example.com/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "your_username",
    "password": "your_password"
  }'
```

**Response**:
```json
{
  "success": true,
  "data": {
    "user": {...},
    "tokens": {
      "access_token": "eyJhbGciOiJIUzI1NiIs...",
      "refresh_token": "eyJhbGciOiJIUzI1NiIs...",
      "expires_in": 3600
    }
  }
}
```

**Step 2: Use Access Token**

Include the token in the `Authorization` header for all API requests:

```bash
curl https://aileron.example.com/api/v1/alerts \
  -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIs..."
```

**Token Expiration**: Access tokens expire after 1 hour. Use the refresh token to get a new one:

```bash
curl -X POST https://aileron.example.com/api/v1/auth/refresh \
  -H "Content-Type: application/json" \
  -d '{
    "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
  }'
```

### Method 2: API Key (Recommended for Service-to-Service)

**Coming Soon**: API key authentication for service accounts.

For now, create a service account user and use JWT authentication.

## Core API Endpoints

### 1. Get Alerts

**Endpoint**: `GET /alerts`

**Parameters**:
- `limit` (optional): Number of alerts to return (default: 100, max: 1000)
- `status` (optional): Filter by status (open, acknowledged, resolved)
- `severity` (optional): Filter by severity (critical, high, medium, low)
- `source` (optional): Filter by source (prometheus, dynatrace, etc.)

**Example**:
```bash
curl "https://aileron.example.com/api/v1/alerts?limit=50&status=open&severity=critical" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Response**:
```json
{
  "success": true,
  "data": {
    "alerts": [
      {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "title": "High CPU Usage on prod-k8s-01",
        "description": "CPU usage exceeded 90% for 5 minutes",
        "severity": "critical",
        "status": "open",
        "source": "prometheus",
        "source_id": "prom-alert-12345",
        "source_url": "https://prometheus.example.com/alert/12345",
        "labels": {
          "cluster": "prod-us-east-1",
          "namespace": "default"
        },
        "metadata": {
          "cluster": "prod-us-east-1",
          "host": "k8s-node-01",
          "service": "api-service"
        },
        "created_at": "2026-03-03T10:00:00Z",
        "updated_at": "2026-03-03T10:05:00Z",
        "acknowledged_at": null,
        "resolved_at": null
      }
    ],
    "total": 50,
    "limit": 50
  }
}
```

### 2. Get Single Alert

**Endpoint**: `GET /alerts/:id`

**Example**:
```bash
curl "https://aileron.example.com/api/v1/alerts/550e8400-e29b-41d4-a716-446655440000" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

### 3. Create Alert

**Endpoint**: `POST /alerts`

**Example**:
```bash
curl -X POST "https://aileron.example.com/api/v1/alerts" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Database Connection Pool Exhausted",
    "description": "Connection pool reached maximum capacity",
    "severity": "high",
    "source": "my-monitoring-system",
    "source_id": "alert-12345",
    "source_url": "https://my-system.com/alerts/12345",
    "labels": {
      "environment": "production",
      "team": "backend"
    },
    "metadata": {
      "cluster": "prod-db-cluster",
      "database": "users-db"
    }
  }'
```

**Response**:
```json
{
  "success": true,
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "title": "Database Connection Pool Exhausted",
    "severity": "high",
    "status": "open",
    "created_at": "2026-03-03T10:15:00Z"
  }
}
```

### 4. Acknowledge Alert

**Endpoint**: `POST /alerts/:id/acknowledge`

**Example**:
```bash
curl -X POST "https://aileron.example.com/api/v1/alerts/550e8400-e29b-41d4-a716-446655440000/acknowledge" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

### 5. Resolve Alert

**Endpoint**: `POST /alerts/:id/resolve`

**Body** (optional):
```json
{
  "resolution": "Restarted the service and verified connection pool",
  "root_cause": "configuration"
}
```

**Example**:
```bash
curl -X POST "https://aileron.example.com/api/v1/alerts/550e8400-e29b-41d4-a716-446655440000/resolve" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "resolution": "Increased connection pool size from 10 to 50",
    "root_cause": "capacity"
  }'
```

### 6. Get Incidents

**Endpoint**: `GET /incidents`

**Parameters**:
- `limit` (optional): Number of incidents (default: 100)
- `status` (optional): Filter by status

**Example**:
```bash
curl "https://aileron.example.com/api/v1/incidents?limit=20&status=investigating" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

### 7. Create Incident

**Endpoint**: `POST /incidents`

**Example**:
```bash
curl -X POST "https://aileron.example.com/api/v1/incidents" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Production Database Outage",
    "description": "Multiple alerts indicating database issues",
    "severity": "critical",
    "alert_ids": [
      "550e8400-e29b-41d4-a716-446655440000",
      "660e8400-e29b-41d4-a716-446655440001"
    ]
  }'
```

### 8. Get Alert Statistics

**Endpoint**: `GET /alerts/stats`

**Example**:
```bash
curl "https://aileron.example.com/api/v1/alerts/stats" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Response**:
```json
{
  "success": true,
  "data": {
    "total": 1247,
    "by_severity": {
      "critical": 45,
      "high": 128,
      "medium": 342,
      "low": 732
    },
    "by_status": {
      "open": 142,
      "acknowledged": 89,
      "resolved": 1016
    },
    "by_source": {
      "prometheus": 450,
      "dynatrace": 380,
      "grafana": 250,
      "webhook": 167
    }
  }
}
```

## Integration Examples

### Python

```python
import requests

class AlertHubClient:
    def __init__(self, base_url, username, password):
        self.base_url = base_url
        self.token = self._login(username, password)
    
    def _login(self, username, password):
        response = requests.post(
            f"{self.base_url}/auth/login",
            json={"username": username, "password": password}
        )
        data = response.json()
        return data["data"]["tokens"]["access_token"]
    
    def get_alerts(self, limit=100, **filters):
        headers = {"Authorization": f"Bearer {self.token}"}
        params = {"limit": limit, **filters}
        response = requests.get(
            f"{self.base_url}/alerts",
            headers=headers,
            params=params
        )
        return response.json()["data"]["alerts"]
    
    def create_alert(self, title, severity, **kwargs):
        headers = {"Authorization": f"Bearer {self.token}"}
        data = {"title": title, "severity": severity, **kwargs}
        response = requests.post(
            f"{self.base_url}/alerts",
            headers=headers,
            json=data
        )
        return response.json()["data"]
    
    def acknowledge_alert(self, alert_id):
        headers = {"Authorization": f"Bearer {self.token}"}
        response = requests.post(
            f"{self.base_url}/alerts/{alert_id}/acknowledge",
            headers=headers
        )
        return response.json()

# Usage
client = AlertHubClient(
    "https://aileron.example.com/api/v1",
    "your_username",
    "your_password"
)

# Get critical alerts
alerts = client.get_alerts(limit=50, severity="critical", status="open")
for alert in alerts:
    print(f"{alert['severity']}: {alert['title']}")

# Create new alert
new_alert = client.create_alert(
    title="API Response Time Degraded",
    severity="high",
    source="custom-monitor",
    description="P99 latency > 2s for 10 minutes"
)
print(f"Created alert: {new_alert['id']}")
```

### JavaScript/Node.js

```javascript
const axios = require('axios');

class AlertHubClient {
  constructor(baseURL, username, password) {
    this.baseURL = baseURL;
    this.axiosInstance = axios.create({ baseURL });
    this.init(username, password);
  }

  async init(username, password) {
    const response = await this.axiosInstance.post('/auth/login', {
      username,
      password
    });
    
    this.token = response.data.data.tokens.access_token;
    this.axiosInstance.defaults.headers.common['Authorization'] = `Bearer ${this.token}`;
  }

  async getAlerts(params = {}) {
    const response = await this.axiosInstance.get('/alerts', { params });
    return response.data.data.alerts;
  }

  async createAlert(alert) {
    const response = await this.axiosInstance.post('/alerts', alert);
    return response.data.data;
  }

  async acknowledgeAlert(alertId) {
    const response = await this.axiosInstance.post(`/alerts/${alertId}/acknowledge`);
    return response.data;
  }

  async resolveAlert(alertId, resolution) {
    const response = await this.axiosInstance.post(`/alerts/${alertId}/resolve`, resolution);
    return response.data;
  }
}

// Usage
const client = new AlertHubClient(
  'https://aileron.example.com/api/v1',
  'your_username',
  'your_password'
);

// Get all critical alerts
const criticalAlerts = await client.getAlerts({
  severity: 'critical',
  status: 'open',
  limit: 100
});

console.log(`Found ${criticalAlerts.length} critical alerts`);

// Create alert
const newAlert = await client.createAlert({
  title: 'High Memory Usage',
  severity: 'high',
  source: 'node-monitor',
  description: 'Memory usage > 85%'
});

console.log(`Created alert: ${newAlert.id}`);
```

### cURL Examples

**Get all open alerts**:
```bash
curl "https://aileron.example.com/api/v1/alerts?status=open" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Get alerts from last 24 hours**:
```bash
curl "https://aileron.example.com/api/v1/alerts?created_after=$(date -u -d '24 hours ago' +%Y-%m-%dT%H:%M:%SZ)" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Create alert**:
```bash
curl -X POST "https://aileron.example.com/api/v1/alerts" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Service Down",
    "severity": "critical",
    "source": "healthcheck",
    "description": "Service health check failed"
  }'
```

## Webhooks (Alternative to Polling)

Instead of polling for alerts, you can receive webhooks when alerts are created:

**Endpoint**: `POST /webhooks/subscribe`

**Request**:
```json
{
  "url": "https://your-app.com/webhooks/alerthub",
  "events": ["alert.created", "alert.acknowledged", "alert.resolved"],
  "filters": {
    "severity": ["critical", "high"]
  }
}
```

When an alert matching your filters is created/updated, AlertHub will POST to your webhook URL:

```json
{
  "event": "alert.created",
  "timestamp": "2026-03-03T10:00:00Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "title": "High CPU Usage",
    "severity": "critical",
    ...
  }
}
```

## Rate Limiting

- **Rate Limit**: 1000 requests per hour per user
- **Burst**: 100 requests per minute
- **Headers Returned**:
  - `X-RateLimit-Limit`: Total requests allowed
  - `X-RateLimit-Remaining`: Requests remaining
  - `X-RateLimit-Reset`: Unix timestamp when limit resets

**Example Response When Rate Limited**:
```json
{
  "success": false,
  "error": "Rate limit exceeded. Retry after 60 seconds.",
  "retry_after": 60
}
```

## Error Handling

All error responses follow this format:

```json
{
  "success": false,
  "message": "Human-readable error message",
  "error": "ERROR_CODE",
  "details": {}
}
```

**Common Error Codes**:
- `401`: Unauthorized - Invalid or expired token
- `403`: Forbidden - Insufficient permissions
- `404`: Not Found - Resource doesn't exist
- `429`: Too Many Requests - Rate limit exceeded
- `500`: Internal Server Error - Server issue

## Pagination

For endpoints returning lists:

```bash
curl "https://aileron.example.com/api/v1/alerts?limit=50&offset=100" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Response includes**:
```json
{
  "success": true,
  "data": {
    "alerts": [...],
    "total": 1247,
    "limit": 50,
    "offset": 100,
    "has_more": true
  }
}
```

## Best Practices

### 1. Token Management

```javascript
class TokenManager {
  constructor(baseURL, username, password) {
    this.baseURL = baseURL;
    this.credentials = { username, password };
    this.token = null;
    this.refreshToken = null;
    this.tokenExpiry = null;
  }

  async ensureValidToken() {
    if (!this.token || this.isTokenExpiring()) {
      await this.refreshAccessToken();
    }
    return this.token;
  }

  isTokenExpiring() {
    // Refresh if less than 5 minutes remaining
    return this.tokenExpiry && (this.tokenExpiry - Date.now() < 5 * 60 * 1000);
  }

  async refreshAccessToken() {
    // Implementation...
  }
}
```

### 2. Error Retry Logic

```python
import time

def api_call_with_retry(func, max_retries=3):
    for attempt in range(max_retries):
        try:
            return func()
        except requests.exceptions.RequestException as e:
            if attempt == max_retries - 1:
                raise
            wait_time = 2 ** attempt  # Exponential backoff
            print(f"Retry {attempt + 1}/{max_retries} after {wait_time}s")
            time.sleep(wait_time)
```

### 3. Batch Operations

Instead of individual requests, use bulk endpoints when available:

```bash
curl -X POST "https://aileron.example.com/api/v1/alerts/bulk/acknowledge" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "alert_ids": [
      "550e8400-e29b-41d4-a716-446655440000",
      "660e8400-e29b-41d4-a716-446655440001",
      "770e8400-e29b-41d4-a716-446655440002"
    ]
  }'
```

## Complete API Reference

### Alerts

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/alerts` | List alerts with filters |
| GET | `/alerts/:id` | Get single alert |
| POST | `/alerts` | Create new alert |
| POST | `/alerts/:id/acknowledge` | Acknowledge alert |
| POST | `/alerts/:id/resolve` | Resolve alert |
| POST | `/alerts/:id/assign` | Assign alert to user |
| DELETE | `/alerts/:id` | Delete alert (admin only) |
| GET | `/alerts/stats` | Get alert statistics |
| POST | `/alerts/bulk/acknowledge` | Bulk acknowledge |
| POST | `/alerts/bulk/resolve` | Bulk resolve |

### Incidents

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/incidents` | List incidents |
| GET | `/incidents/:id` | Get single incident |
| POST | `/incidents` | Create incident |
| POST | `/incidents/:id/update` | Update incident |
| POST | `/incidents/:id/resolve` | Resolve incident |

### Analytics

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/analytics/overview` | System overview stats |
| GET | `/analytics/trends` | Alert trends over time |
| GET | `/analytics/correlations` | Alert correlations |

### AI Tools

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/ai/tools` | List available AI tools |
| POST | `/ai/tools/execute` | Execute AI tool |
| GET | `/ai/capabilities` | Get AI capabilities |

## Getting Started Checklist

For other teams to integrate:

- [ ] **Create Service Account**: Contact AlertHub admins to create a service account user
- [ ] **Get Credentials**: Receive username and password for service account
- [ ] **Test Authentication**: Verify you can get JWT token
- [ ] **Test Alerts API**: Fetch alerts to confirm access
- [ ] **Implement Client**: Use examples above for your language
- [ ] **Add Error Handling**: Implement retry logic and error handling
- [ ] **Setup Monitoring**: Monitor your integration's API usage

## Support

**For Questions**:
- Slack: `#help-interactive-sre`
- Email: `interactive-sre@group.example.com`
- Documentation: https://wiki.iapps.example.com/display/ISRE/AlertHub

**For Service Account Creation**:
Contact AlertHub administrators via Slack `#help-interactive-sre`

## Example: Real-Time Alert Monitor

```python
#!/usr/bin/env python3
"""
Real-time alert monitor that polls AlertHub and sends notifications
"""
import requests
import time
from datetime import datetime

class AlertMonitor:
    def __init__(self, base_url, username, password):
        self.base_url = base_url
        self.session = requests.Session()
        self.login(username, password)
        self.last_alert_id = None
    
    def login(self, username, password):
        response = self.session.post(
            f"{self.base_url}/auth/login",
            json={"username": username, "password": password}
        )
        token = response.json()["data"]["tokens"]["access_token"]
        self.session.headers.update({"Authorization": f"Bearer {token}"})
    
    def check_new_alerts(self):
        response = self.session.get(
            f"{self.base_url}/alerts",
            params={"status": "open", "severity": "critical", "limit": 10}
        )
        alerts = response.json()["data"]["alerts"]
        
        new_alerts = []
        for alert in alerts:
            if self.last_alert_id and alert["id"] == self.last_alert_id:
                break
            new_alerts.append(alert)
        
        if new_alerts:
            self.last_alert_id = new_alerts[0]["id"]
        
        return new_alerts
    
    def run(self, interval=30):
        print(f"Monitoring AlertHub for critical alerts every {interval}s...")
        
        while True:
            try:
                new_alerts = self.check_new_alerts()
                for alert in new_alerts:
                    print(f"🚨 NEW ALERT: [{alert['severity'].upper()}] {alert['title']}")
                    # Send to your notification system here
                    self.send_notification(alert)
            except Exception as e:
                print(f"Error: {e}")
            
            time.sleep(interval)
    
    def send_notification(self, alert):
        # Implement your notification logic here
        # Examples: Slack, PagerDuty, email, etc.
        pass

if __name__ == "__main__":
    monitor = AlertMonitor(
        "https://aileron.example.com/api/v1",
        "service-account-user",
        "service-account-password"
    )
    monitor.run(interval=30)
```

## Security Considerations

1. **Store Credentials Securely**: Use environment variables or secret management
2. **Use HTTPS Only**: All API calls must use HTTPS
3. **Rotate Tokens**: Refresh tokens regularly
4. **Limit Permissions**: Service accounts should have minimal required permissions
5. **Log API Usage**: Monitor your integration's API calls
6. **Handle Token Expiry**: Implement automatic token refresh

## Troubleshooting

**Problem**: 401 Unauthorized
- **Solution**: Token expired, refresh it

**Problem**: 403 Forbidden
- **Solution**: Service account lacks required permissions

**Problem**: 429 Rate Limited
- **Solution**: Implement exponential backoff, reduce request frequency

**Problem**: Alerts not showing up
- **Solution**: Check filters, verify alert was created, check time range

## Quick Start

1. **Get your credentials** from AlertHub admins
2. **Test authentication**:
   ```bash
   curl -X POST https://aileron.example.com/api/v1/auth/login \
     -H "Content-Type: application/json" \
     -d '{"username":"YOUR_USER","password":"YOUR_PASS"}'
   ```
3. **Save the access_token** from response
4. **Test alerts endpoint**:
   ```bash
   curl https://aileron.example.com/api/v1/alerts \
     -H "Authorization: Bearer YOUR_TOKEN"
   ```
5. **You're ready!** Use the examples above for your language

---

**API Version**: v1  
**Last Updated**: March 2026  
**Status**: Production Ready ✅
