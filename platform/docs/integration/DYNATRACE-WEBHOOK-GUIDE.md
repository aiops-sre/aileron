# Dynatrace Webhook Integration Guide

## Overview

AlertHub already has a fully functional Dynatrace webhook endpoint that receives problem notifications from Dynatrace and automatically creates alerts in AlertHub.

## Webhook Endpoint

**URL**: `https://aileron.example.com/api/v1/webhooks/dynatrace`  
**Method**: POST  
**Authentication**: X-API-Key header (optional for now)

## Configure in Dynatrace

### Step 1: Get Webhook URL

Your AlertHub webhook URL:
```
https://aileron.example.com/api/v1/webhooks/dynatrace
```

### Step 2: Configure in Dynatrace

1. **Log into your Dynatrace environment**
2. **Navigate to**: Settings → Integration → Problem notifications
3. **Click**: "Add notification"
4. **Select**: "Custom integration" or "Webhook"
5. **Configure**:
   - **Name**: AlertHub Integration
   - **Webhook URL**: `https://aileron.example.com/api/v1/webhooks/dynatrace`
   - **HTTP Method**: POST
   - **Custom Headers** (optional):
     ```
     X-API-Key: your-api-key-here
     Content-Type: application/json
     ```

### Step 3: Configure Payload

Dynatrace will send a JSON payload like this:

```json
{
  "ProblemID": "P-12345",
  "ProblemTitle": "High CPU usage on production-server",
  "ProblemDetailsText": "CPU usage exceeded 80% for 5 minutes",
  "State": "OPEN",
  "ImpactLevel": "APPLICATION",
  "ProblemURL": "https://abc123.live.dynatrace.com/#problems/problemdetails;pid=P-12345",
  "Tags": ["environment:production", "team:backend"],
  "AffectedEntities": [
    {
      "entity": "HOST-123",
      "name": "production-server-01",
      "type": "HOST"
    }
  ]
}
```

### Step 4: Test the Integration

In Dynatrace:
1. Click "Send test notification"
2. Check AlertHub to see if alert appears
3. Navigate to: https://aileron.example.com/alerts.html
4. You should see the test alert

## How It Works

When Dynatrace sends a problem notification:

1. **Webhook receives** problem data at `/api/v1/webhooks/dynatrace`
2. **AlertHub maps** Dynatrace fields:
   - `ProblemTitle` → Alert Title
   - `ProblemDetailsText` → Alert Description
   - `ImpactLevel` → Severity (APPLICATION/SERVICE → critical, INFRASTRUCTURE → high)
   - `State` → Status (OPEN → open, RESOLVED → resolved)
   - `ProblemID` → Source ID (for tracking)
   - `ProblemURL` → Deep link back to Dynatrace
   - `AffectedEntities` → Metadata

3. **Alert created** in AlertHub database
4. **Appears immediately** in Alerts page
5. **Can be managed** through AlertHub UI:
   - Acknowledge
   - Resolve
   - Assign to team members
   - Create incident
   - Add to timeline

## Severity Mapping

| Dynatrace Impact | AlertHub Severity |
|------------------|-------------------|
| APPLICATION      | critical          |
| SERVICE          | critical          |
| INFRASTRUCTURE   | high              |
| Others           | medium            |

## State Mapping

| Dynatrace State | AlertHub Status |
|-----------------|-----------------|
| OPEN            | open            |
| RESOLVED        | resolved        |

## Viewing Dynatrace Alerts

1. **Navigate to**: https://aileron.example.com/alerts.html
2. **Filter by source**: Select "dynatrace" from source dropdown
3. **View details**: Click on any alert card
4. **Deep link**: Click the Dynatrace icon to open problem in Dynatrace

## Automatic Features

When Dynatrace sends problems to AlertHub:

✅ **Auto-categorization** - Severity based on impact level  
✅ **Deep linking** - Direct link to Dynatrace problem  
✅ **Entity tracking** - Affected services tracked  
✅ **Status sync** - Resolved problems auto-resolve alerts  
✅ **Tag preservation** - Dynatrace tags carried over  
✅ **Metadata storage** - Full problem context saved  

## Testing the Webhook

### Manual Test

You can test the webhook with curl:

```bash
curl -X POST https://aileron.example.com/api/v1/webhooks/dynatrace \
  -H "Content-Type: application/json" \
  -H "X-API-Key: optional-key" \
  -d '{
    "ProblemID": "TEST-001",
    "ProblemTitle": "Test Alert from Dynatrace",
    "ProblemDetailsText": "This is a test problem notification",
    "State": "OPEN",
    "ImpactLevel": "APPLICATION",
    "ProblemURL": "https://your-dynatrace.com/problems/TEST-001",
    "Tags": ["test", "integration"],
    "AffectedEntities": [
      {
        "entity": "HOST-001",
        "name": "test-server",
        "type": "HOST"
      }
    ]
  }'
```

Expected response:
```json
{
  "status": "success"
}
```

### Verify in AlertHub

1. Go to https://aileron.example.com/alerts.html
2. Look for alert titled "Test Alert from Dynatrace"
3. Source badge should show "dynatrace"
4. Severity should be "critical"

## Troubleshooting

### Webhook Not Receiving Data

**Check**:
1. Dynatrace can reach AlertHub URL (not blocked by firewall)
2. URL is correct (no typos)
3. Dynatrace problem notification is enabled
4. Test notification was sent

**View Logs**:
```bash
kubectl logs -n sre-hub-alerthub deployment/alerthub-backend --tail=100 | grep webhook
```

### Alerts Not Appearing

**Check**:
1. Login to AlertHub
2. Navigate to Alerts page
3. Clear any active filters
4. Check backend logs for errors

## Advanced Configuration

### API Key Authentication (Optional)

1. **Generate API Key** in AlertHub:
   - Admin → Integrations tab
   - Click "Generate API Key"
   - Copy the generated key

2. **Add to Dynatrace**:
   - In webhook configuration
   - Add custom header: `X-API-Key: your-generated-key`

### Alert Routing

Alerts from Dynatrace can be:
- Filtered by tags
- Auto-assigned based on affected entities
- Escalated to PagerDuty if critical
- Grouped into incidents
- Analyzed by AI for root cause

### Bi-directional Sync (Future Enhancement)

Planned features:
- Update Dynatrace when alert resolved in AlertHub
- Add comments from AlertHub to Dynatrace problem
- Close Dynatrace problem from AlertHub

## Example Dynatrace Setup

### Problem Notification Configuration

```
Name: AlertHub Integration
Type: Custom Integration
Webhook URL: https://aileron.example.com/api/v1/webhooks/dynatrace
Payload: Custom JSON (use Dynatrace variables)

Trigger conditions:
- Problem opened: ✅ YES (REQUIRED - must be enabled!)
- Problem updated: Yes (optional)
- Problem resolved: ✅ YES (REQUIRED - must be enabled!)

Filter:
- Impact level: APPLICATION, SERVICE, INFRASTRUCTURE
- Tags: (optional) production, critical
```

**IMPORTANT**: You MUST enable webhooks for BOTH "Problem opened" AND "Problem resolved" for auto-resolution to work correctly!

### Recommended Settings

- **Send on**: Problem opened (REQUIRED), Problem resolved (REQUIRED), Problem updated (optional)
- **Throttling**: 1 notification per minute
- **Retry**: 3 attempts with 30s interval
- **Timeout**: 30 seconds

## Monitoring Integration Health

Check integration status:
1. Navigate to Integrations page
2. Dynatrace card shows connection status
3. Last sync time displayed
4. Test connection button available

---

**The Dynatrace webhook is live and ready to receive problem notifications!**

Simply configure the webhook URL in your Dynatrace environment and problems will automatically flow into AlertHub.
