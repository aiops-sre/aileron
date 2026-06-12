# HCL Kentaurus API Integration

## Overview

HCL Help Central (Kentaurus) is Apple's internal ITSM platform. This documents how to authenticate and interact with the Kentaurus API for incident management.

---

## Credentials

| Field | Value |
|---|---|
| Consumer / App ID | `928952` |
| Configuration | `Interactive-Infra-ADC` |
| Assignment Group ID | `13283967` |
| Base URL | `` |

Credentials (app password) are stored as environment variables — never hardcode them.

---

## Authentication

Kentaurus requires a short-lived token generated via Apple's IDMS app-to-app token service. Tokens expire in ~100 minutes (`timeToLive: 6000000` ms).

### Token Generation Endpoint

```
POST https://idmsservice.example.com/auth/apptoapp/token/generate
```

### Request

```json
{
  "appId": "928952",
  "appPassword": "<IDMS_APP_PASSWORD>",
  "otherApp": "150899",
  "context": "#GrandPrix#",
  "oneTimeToken": "false",
  "contextVersion": 3,
  "timeToLive": 6000000
}
```

### Response

```json
{
  "token": "<kentaurus-auth-token>"
}
```

### Required Headers (all Kentaurus requests)

```
HTTP_HEADER_KENTAURUS_AUTH_TOKEN: <token>
HTTP_HEADER_KENTAURUS_CONSUMER_ID: 928952
Content-Type: application/json
```

### Python Example

```python
import requests

def generate_token():
    url = "https://idmsservice.example.com/auth/apptoapp/token/generate"
    payload = {
        "appId": "928952",
        "appPassword": IDMS_APP_PASSWORD,  # from env
        "otherApp": "150899",
        "context": "#GrandPrix#",
        "oneTimeToken": "false",
        "contextVersion": 3,
        "timeToLive": 6000000
    }
    resp = requests.post(url, json=payload, headers={"Content-Type": "application/json"})
    return resp.json().get("token")
```

---

## API Operations

### 1. Create Incident

```
POST /createRecords
```

**Request body:**

```json
{
  "module": "incident",
  "callingApp": "928952",
  "configuration": "Interactive-Infra-ADC",
  "title": "<incident title>",
  "description": "<description>",
  "impact": "3 - Low",
  "urgency": "3 - Low",
  "assignmentGroupId": "13283967"
}
```

**Impact / Urgency values:** `"1 - High"`, `"2 - Medium"`, `"3 - Low"`

**Response (201):**

```json
{
  "result": {
    "status": { "httpStatusCode": 201, "state": "success" },
    "data": [
      { "number": "INC101839774", "UUID": "b0ee3fd247300b14931eff3f62901371" }
    ]
  }
}
```

---

### 2. Query Incidents

```
POST /queryRecords
```

**Request body:**

```json
{
  "module": "incident",
  "offset": 0,
  "count": 50,
  "query": "<servicenow-style query>"
}
```

Either `query` or `number` is required. `query` takes precedence if both are sent.

**Pagination:** increment `offset` by `count` to page through results. Total available records are in `meta.queryResultCount`.

**Response (200):**

```json
{
  "result": {
    "status": { "httpStatusCode": 200, "state": "success" },
    "meta": {
      "queryResultCount": 254826,
      "resultCount": 50,
      "offset": 0
    },
    "data": [ /* incident records */ ]
  }
}
```

**Incident record fields:** `number`, `UUID`, `title`, `description`, `state`, `impact`, `urgency`, `priority`, `openedDate`, `resolvedDate`, `assignmentGroup`, `assignedTo`, `callerID`, `environment`

---

## Query Reference

Queries use ServiceNow-style syntax: `field=value^ANDfield2=value2`

| Use case | Query |
|---|---|
| All incidents last 30 days | `sys_created_on>javascript:gs.beginningOfLast30Days()` |
| By assignment group name | `assignment_group=aileron-admins` |
| By assignment group UUID | `assignment_group=740df08c2d0dea104c5df117ebee2a1f` |
| By business org | `u_business_org=7fad712f55b0b200b7b1b313be98e279` |
| Date range | `sys_created_on>'2026-05-01'^sys_created_on<'2026-05-14'` |
| Combined | `assignment_group=740df08c2d0dea104c5df117ebee2a1f^sys_created_on>javascript:gs.beginningOfLast30Days()` |

---

## curl Quick Reference

```bash
# Generate token
TOKEN=$(curl -s -X POST "https://idmsservice.example.com/auth/apptoapp/token/generate" \
  -H "Content-Type: application/json" \
  -d '{"appId":"928952","appPassword":"<IDMS_APP_PASSWORD>","otherApp":"150899","context":"#GrandPrix#","oneTimeToken":"false","contextVersion":3,"timeToLive":6000000}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))")

# Query incidents (last 30 days)
curl -s -X POST "/queryRecords" \
  -H "Content-Type: application/json" \
  -H "HTTP_HEADER_KENTAURUS_AUTH_TOKEN: $TOKEN" \
  -H "HTTP_HEADER_KENTAURUS_CONSUMER_ID: 928952" \
  -d '{"module":"incident","offset":0,"count":50,"query":"sys_created_on>javascript:gs.beginningOfLast30Days()"}'

# Create incident
curl -s -X POST "/createRecords" \
  -H "Content-Type: application/json" \
  -H "HTTP_HEADER_KENTAURUS_AUTH_TOKEN: $TOKEN" \
  -H "HTTP_HEADER_KENTAURUS_CONSUMER_ID: 928952" \
  -d '{"module":"incident","callingApp":"928952","configuration":"Interactive-Infra-ADC","title":"<title>","description":"<desc>","impact":"3 - Low","urgency":"3 - Low","assignmentGroupId":"13283967"}'
```

---

## Error Codes

| Code | Message | Cause |
|---|---|---|
| 401 | `KERR01: Authentication failed` | Token expired or invalid — regenerate |
| 400 | `Neither number nor query was sent` | Missing required `query` or `number` in body |
| 404 | `KERR05: Invalid version/operation combination` | Wrong endpoint path |

---

## Environment Variables

```
IDMS_APP_PASSWORD=<app password for app ID 928952>
```

In Kubernetes deployments the token is written to `/secrets/app/kentaurus-token` by an init container and read at runtime — no env var needed for the token itself.
