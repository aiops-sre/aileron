# Floodgate + IDMS OAuth Integration
## End-to-End Technical Documentation

**System**: AlertHub Enterprise  
**Audience**: Platform engineers onboarding to or debugging the IdMS/Floodgate auth stack  
**Last updated**: 2026-05-11

---

## Table of Contents

1. [Overview](#1-overview)
2. [Application Registration](#2-application-registration)
3. [OAuth Scopes](#3-oauth-scopes)
4. [Authorization Code Flow — Step by Step](#4-authorization-code-flow--step-by-step)
5. [Floodgate Token Refresh (Silent)](#5-floodgate-token-refresh-silent)
6. [Making Floodgate API Calls](#6-making-floodgate-api-calls)
7. [DS-LDAP Group → Role Mapping](#7-ds-ldap-group--role-mapping)
8. [Kubernetes Configuration](#8-kubernetes-configuration)
9. [Redis Cache Keys](#9-redis-cache-keys)
10. [API Endpoints Reference](#10-api-endpoints-reference)
11. [AD Groups Required](#11-ad-groups-required)
12. [Troubleshooting](#12-troubleshooting)
13. [Local Development (appleconnect CLI)](#13-local-development-appleconnect-cli)
14. [Source Files Reference](#14-source-files-reference)

---

## 1. Overview

AlertHub authenticates users via **Apple IdMS** (Identity Management System) using the **OAuth 2.0 Authorization Code Flow**. The same IdMS session is used to obtain a **Floodgate id_token** that grants access to LLM models (Claude, Gemini, GPT). Group membership from **DS-LDAP** maps AD groups to RBAC roles inside AlertHub.

```
Browser
  │
  ▼
AlertHub Backend
  │  1. /api/v1/auth/mas  →  redirect to IdMS /authorize
  │  2. IdMS callback     →  exchange code for tokens
  │  3. Parse id_token    →  extract user claims + groups
  │  4. DS-LDAP lookup    →  groups → RBAC role
  │  5. Issue JWT session →  AlertHub access_token + refresh_token
  │  6. Cache Floodgate id_token in Redis
  │
  ▼
Frontend
  │  receives: AlertHub JWT + Floodgate token
  │
  ├─▶ AlertHub API calls   →  Authorization: Bearer <alerthub_access_token>
  └─▶ Floodgate API calls  →  Authorization: Bearer <floodgate_token>  (id_token)
```

The **critical design point**: the Floodgate `id_token` is obtained during the normal IdMS login by requesting `audience=hvys3fcwcteqrvw3qzkvtk86viuoqv` in both the `/authorize` and `/token` requests. No separate Floodgate login is needed.

---

## 2. Application Registration

| Field | Value |
|---|---|
| **AlertHub App ID (IdMS)** | `961469` |
| **OAuth Client ID** | `7jdvu5f1gxuuckpbdb5s7jw6tcwpf3` |
| **OAuth Client Secret** | `<in alerthub-secrets K8s Secret — never in ConfigMap>` |
| **Floodgate App ID** | `928148` |
| **Floodgate OIDC Client ID** (audience param) | `hvys3fcwcteqrvw3qzkvtk86viuoqv` |
| **IdMS Base URL (production)** | `` |
| **IdMS Base URL (UAT)** | `https://idmsac-uat.example.com` |
| **Floodgate Base URL** | `` |
| **Registered Redirect URI** | `https://aileron.example.com/api/v1/auth` |

> **Registration**: The OAuth client (`7jdvu5f1gxuuckpbdb5s7jw6tcwpf3`) is registered in the IdMS portal under App ID `961469`. The redirect URI must be registered exactly — including path but no trailing slash. Any change to the callback URL requires updating the registration.

---

## 3. OAuth Scopes

All scopes are requested as a space-separated string in the `/authorize` call.

| Scope | Purpose |
|---|---|
| `openid` | Required for OIDC id_token |
| `api` | API access grant |
| `dsid` | Apple user DSID (unique user identifier) |
| `accountname` | Apple short username (e.g. `vpatha`) |
| `email` | User email address |
| `groups` | AD group memberships — used for RBAC role assignment |
| `profile` | Full name + profile picture URL |
| `offline_access` | Issues a refresh token — **required** for silent Floodgate token renewal |

---

## 4. Authorization Code Flow — Step by Step

### Step 1 — Initiate Login

**Frontend calls:**
```
GET /api/v1/auth/mas
```

**Backend** (`MASHandler.InitiateMASLogin`):

1. Generates 32-byte cryptographically random CSRF state token (base64-encoded)
2. Stores state in Redis: `mas:state:{state}` with TTL = 10 minutes
3. Builds the IdMS authorization URL and redirects the browser (HTTP 302):

```
GET /IDMSWebAuth/appleauth/auth/oauth2/v2/authorize
  ?client_id=7jdvu5f1gxuuckpbdb5s7jw6tcwpf3
  &redirect_uri=https://aileron.example.com/api/v1/auth
  &response_type=code
  &scope=openid api dsid accountname email groups profile offline_access
  &state=<32-byte-random-base64>
  &audience=hvys3fcwcteqrvw3qzkvtk86viuoqv
```

> **`audience` is critical**: Setting `audience=hvys3fcwcteqrvw3qzkvtk86viuoqv` here tells IdMS to embed Floodgate consent in the authorization grant. The returned `refresh_token` will carry this audience, enabling silent Floodgate token renewals without re-login. Without this, every Floodgate call requires a fresh user login.

---

### Step 2 — User Authenticates at IdMS

The user enters their Apple corporate credentials at the IdMS login page. IdMS validates the user's identity and consent, then redirects back to the registered redirect URI:

```
GET https://aileron.example.com/api/v1/auth
  ?code=<one-time-authorization-code>
  &state=<must-match-step-1-state>
```

On denial or error:
```
GET https://aileron.example.com/api/v1/auth
  ?error=access_denied
  &error_description=User+denied+access
  &state=<state>
```

---

### Step 3 — Callback & CSRF Validation

**Handler**: `MASHandler.MASCallback` at `/api/v1/auth`

1. Reads `state` from query parameter
2. Looks up `mas:state:{state}` in Redis → **returns 401** if key is missing or expired (CSRF protection)
3. Deletes the state key from Redis (one-time use)
4. If `error` param is present, returns 401 with the error description
5. Passes `code` forward to the token exchange

---

### Step 4 — Token Exchange

**Function**: `OAuthClient.ExchangeCodeForTokens()`  
**Source**: `internal/services/oauth/enhanced_oauth.go:129`

```http
POST /auth/oauth2/token
Content-Type: application/x-www-form-urlencoded

client_id=7jdvu5f1gxuuckpbdb5s7jw6tcwpf3
&client_secret=<OAUTH_CLIENT_SECRET>
&grant_type=authorization_code
&code=<code-from-step-3>
&redirect_uri=https://aileron.example.com/api/v1/auth
&audience=hvys3fcwcteqrvw3qzkvtk86viuoqv
```

> `redirect_uri` must be **byte-for-byte identical** to the value used in Step 1 and to the registered URI. Any mismatch returns HTTP 400 `invalid_grant`.

**Response:**
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiJ9...",
  "id_token": "eyJhbGciOiJSUzI1NiJ9...",
  "refresh_token": "eyJhbGciOiJSUzI1NiJ9...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "openid api dsid accountname email groups profile offline_access"
}
```

The **`id_token`** is the Floodgate credential. Its `aud` claim includes `hvys3fcwcteqrvw3qzkvtk86viuoqv`, making it directly usable as a `Bearer` token for all Floodgate API calls.

---

### Step 5 — ID Token Claims Extraction

**Function**: `OAuthClient.parseIDToken()` → `extractUserClaims()`  
**Source**: `internal/services/oauth/enhanced_oauth.go:237`

The id_token JWT payload contains:

| Claim | Type | Example |
|---|---|---|
| `sub` | string | DSID (unique Apple user ID) |
| `email` | string | `vpatha@apple.com` |
| `name` | string | `Vishwa Kumar Patha` |
| `picture` | string | Profile photo URL |
| `accountname` | string | `vpatha` |
| `groups` | array | `["aileron-admins", "aileron-operators"]` |
| `iss` | string | `` |
| `aud` | string | `7jdvu5f1gxuuckpbdb5s7jw6tcwpf3 hvys3fcwcteqrvw3qzkvtk86viuoqv` |
| `exp` | int64 | Unix expiry timestamp |
| `iat` | int64 | Unix issued-at timestamp |

If `picture` is absent from the id_token, backend calls the OIDC userinfo endpoint:
```http
GET /auth/oauth2/userinfo
Authorization: Bearer <access_token>
```
The backend checks `picture`, `photo`, `photo_url`, `avatar_url`, and `profile_image_url` keys (different IdMS environments use different field names).

---

### Step 6 — DS-LDAP Group Lookup

**Service**: `dsldap.GetUserGroups()`  
**Source**: `internal/services/dsldap/dsldap.go`

When `DSLDAP_ENABLED=true`, backend performs an LDAP search to get the user's current AD group memberships:

**Bind:**
```
Server:  
Bind DN: appid=<LDAP_APP_ID>,ou=applications,o=apple
Bind PW: <LDAP_APP_PASSWORD>
```

**Search:**
```
Base:       ou=people,o=apple
Filter:     (mail=vpatha@apple.com)
Attributes: memberOf
```

Returns a list of group DNs, e.g.:
```
cn=aileron-admins,ou=groups,o=apple
cn=aileron-operators,ou=groups,o=apple
```

The `cn=` value is extracted as the group name. Results are cached in Redis at `ldap:groups:{email}` for **5 minutes** (configurable via `DSLDAP_CACHE_TTL_MINUTES`).

---

### Step 7 — RBAC Role Assignment

**Highest privilege wins.** Role evaluated in order: admin → operator → viewer.

**Default group → role mapping** (from env vars, overridable in Admin UI):

| AD Group | Role |
|---|---|
| `aileron-admins` | `admin` |
| `aileron-operators` | `operator` |
| `aileron-operators` | `operator` |
| `aileron-viewers` | `viewer` |
| *(no match)* | `viewer` |

Custom mappings are stored in the `ldap_group_role_mappings` DB table and managed at runtime via **Admin → RBAC → Group Mappings**.

**Permission matrix by role:**

| Permission | admin | operator | viewer |
|---|---|---|---|
| `alerts.view` | ✓ | ✓ | ✓ |
| `alerts.create` | ✓ | ✓ | — |
| `alerts.update` | ✓ | ✓ | — |
| `alerts.delete` | ✓ | — | — |
| `incidents.view` | ✓ | ✓ | ✓ |
| `incidents.create` | ✓ | ✓ | — |
| `incidents.resolve` | ✓ | ✓ | — |
| `users.manage` | ✓ | — | — |
| `system.configure` | ✓ | — | — |
| `ai.use` | ✓ | ✓ | — |
| `analytics.view` | ✓ | ✓ | ✓ |

---

### Step 8 — User Provisioning

When `MAS_AUTO_PROVISION=true` (default), the backend upserts a row in the `users` table:

```sql
INSERT INTO users (id, email, username, full_name, photo_url, role_id, oauth_source, oauth_id)
VALUES (...)
ON CONFLICT (email) DO UPDATE SET
  full_name        = EXCLUDED.full_name,
  photo_url        = EXCLUDED.photo_url,
  role_id          = EXCLUDED.role_id,     -- refreshed on every login
  oauth_source     = 'idms',
  last_login       = NOW()
```

On subsequent logins, the role is always refreshed from the current LDAP group state.

---

### Step 9 — AlertHub JWT Session Tokens

Backend mints its own JWT pair (independent of IdMS tokens):

| Token | TTL | Stored |
|---|---|---|
| `access_token` | 24 hours | Client (localStorage / memory) |
| `refresh_token` | 7 days | Client (HttpOnly cookie) |

JWT Claims: `sub` (AlertHub user UUID), `email`, `username`, `role`, `permissions[]`

The IDMS `access_token` and `refresh_token` are stored separately in Redis (`idms:token:` and `idms:refresh:`) — these are never exposed to the frontend.

---

### Step 10 — Floodgate Token Delivery to Frontend

1. IDMS `id_token` cached in Redis: `floodgate:token:{user_uuid}` (TTL 55 min)
2. Backend generates a one-time exchange code: `mas:code:{exchange_code}` → `{session_data}` (TTL 2 min)
3. Browser redirected to:
   ```
   https://aileron.example.com/auth/callback?code=<exchange_code>
   ```
4. Frontend calls:
   ```
   GET /api/v1/auth/mas/exchange?code=<exchange_code>
   ```
5. Response:
   ```json
   {
     "success": true,
     "data": {
       "tokens": {
         "access_token": "eyJ...",
         "refresh_token": "eyJ..."
       },
       "user": {
         "id": "uuid",
         "email": "vpatha@apple.com",
         "full_name": "Vishwa Kumar Patha",
         "role_name": "admin"
       },
       "floodgate_token": "eyJ...",
       "floodgate_expires_in": 3300
     }
   }
   ```

The exchange code is deleted from Redis after use. Calling it twice returns 401.

---

## 5. Floodgate Token Refresh (Silent)

Floodgate tokens expire after ~1 hour. The frontend should proactively refresh before expiry.

**Frontend calls:**
```
GET /api/v1/auth/mas/floodgate-refresh
Authorization: Bearer <alerthub_access_token>
```

**Backend** (`MASHandler.RefreshFloodgateToken`):

1. Loads IDMS refresh token from Redis: `idms:refresh:{user_uuid}`
2. Posts to IdMS:

```http
POST /auth/oauth2/token
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token
&refresh_token=<stored-idms-refresh-token>
&client_id=7jdvu5f1gxuuckpbdb5s7jw6tcwpf3
&client_secret=<OAUTH_CLIENT_SECRET>
&audience=hvys3fcwcteqrvw3qzkvtk86viuoqv
&scope=openid dsid accountname profile groups
```

3. Extracts new `id_token` from response
4. Updates Redis: `floodgate:token:{user_uuid}` (TTL 55 min)
5. Returns:

```json
{
  "success": true,
  "data": {
    "floodgate_token": "eyJ...",
    "expires_in": 3300
  }
}
```

**Failure path**: If the IDMS refresh token has expired (after 7 days), the endpoint returns HTTP 401 with `"IDMS session expired — please re-authenticate"`. The frontend must redirect the user to `/api/v1/auth/mas` for a full re-login.

---

## 6. Making Floodgate API Calls

### From Frontend (Direct)

```http
POST /api/anthropic/v1/messages
Authorization: Bearer <floodgate_token>
Content-Type: application/json

{
  "model": "claude-opus-4-5",
  "max_tokens": 4096,
  "messages": [
    { "role": "user", "content": "Summarise this incident timeline..." }
  ]
}
```

### Via AlertHub Backend Proxy

```http
POST /api/v1/floodgate/api/anthropic/v1/messages
Authorization: Bearer <alerthub_access_token>
Content-Type: application/json
```

The proxy (`/api/v1/floodgate/*`) automatically substitutes the caller's cached Floodgate token — the frontend never needs to manage Floodgate credentials explicitly.

### Supported Floodgate Endpoints

| Method | Path | Model Family |
|---|---|---|
| `GET` | `/api/openai/v1/models` | List all available models |
| `POST` | `/api/openai/v1/chat/completions` | OpenAI-compatible (GPT) |
| `POST` | `/api/anthropic/v1/messages` | Claude (Anthropic) |
| `POST` | `/api/gemini/v1/publishers/google/models/{model}:generateContent` | Gemini |

---

## 7. DS-LDAP Group → Role Mapping

### Architecture

```
Login
 └─▶ LDAP bind (appid=LDAP_APP_ID)
      └─▶ search user by email → memberOf attribute
           └─▶ extract CN from each group DN
                └─▶ match against ldap_group_role_mappings table (DB, dynamic)
                     └─▶ fallback to env var group lists
                          └─▶ assign highest matched role
```

### LDAP Bind Credentials

Obtain from the IdMS Portal:
- `LDAP_APP_ID` — the application ID registered for LDAP access
- `LDAP_APP_PASSWORD` — 16-character application password

Both are stored in K8s Secret `alerthub-dsldap-credentials`.

### Runtime Group Mapping (Admin UI)

Navigate to **Admin → RBAC → LDAP Group Mappings** to add/remove group → role mappings without redeploying. Stored in `ldap_group_role_mappings` table:

```sql
CREATE TABLE ldap_group_role_mappings (
  id         UUID PRIMARY KEY,
  ldap_group VARCHAR(256),   -- e.g. "aileron-admins"
  role_id    UUID,           -- references roles.id
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);
```

---

## 8. Kubernetes Configuration

### Secrets

#### `alerthub-secrets`
```bash
kubectl -n aileron create secret generic alerthub-secrets \
  --from-literal=OAUTH_CLIENT_SECRET="<from-idms-portal>" \
  --from-literal=jwt-secret="<32-random-bytes>" \
  --from-literal=postgres-password="<pg-password>" \
  --from-literal=redis-password="<redis-password>" \
  --from-literal=neo4j-auth="neo4j/<neo4j-password>"
```

#### `alerthub-dsldap-credentials`
```bash
kubectl -n aileron create secret generic alerthub-dsldap-credentials \
  --from-literal=LDAP_APP_ID="<app-id-from-idms-portal>" \
  --from-literal=LDAP_APP_PASSWORD="<16-char-app-password>"
```

### ConfigMap `alerthub-config`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: alerthub-config
  namespace: aileron
data:
  # ── IdMS OAuth ────────────────────────────────────────────────────
  OIDC_PROVIDER_URL:       ""
  OAUTH_CLIENT_ID:     "7jdvu5f1gxuuckpbdb5s7jw6tcwpf3"
  OAUTH_CALLBACK_URL:  "https://aileron.example.com/api/v1/auth"

  # ── MAS / Auto-provision ─────────────────────────────────────────
  MAS_ENABLED:         "true"
  MAS_AUTO_PROVISION:  "true"
  MAS_DEFAULT_ROLE:    "operator"
  MAS_STRICT_MODE:     "false"

  # ── DS-LDAP ──────────────────────────────────────────────────────
  DSLDAP_ENABLED:            "true"
  DSLDAP_SERVER_URL:         ""
  DSLDAP_USER_SEARCH_BASE:   "ou=people,o=apple"
  DSLDAP_CACHE_TTL_MINUTES:  "5"
  OIDC_ADMIN_GROUPS:       "aileron-admins"
  OIDC_OPERATOR_GROUPS:    "aileron-operators,aileron-operators"
  OIDC_VIEWER_GROUPS:      "aileron-viewers"

  # ── Floodgate ────────────────────────────────────────────────────
  FLOODGATE_APP_ID:    "928148"
  FLOODGATE_BASE_URL:  ""

  # ── Internal TLS ─────────────────────────────────────────────────
  # Must be "true" for Apple internal services (Floodgate, DS-LDAP,
  # IDMS CSS) which use the Apple internal CA not in the system store.
  INTERNAL_TLS_INSECURE: "true"
```

### Deployment — env wiring (required section)

```yaml
env:
# ── Secrets ───────────────────────────────────────────────────────────
- name: OAUTH_CLIENT_SECRET
  valueFrom:
    secretKeyRef:
      name: alerthub-secrets
      key: OAUTH_CLIENT_SECRET
- name: LDAP_APP_ID
  valueFrom:
    secretKeyRef:
      name: alerthub-dsldap-credentials
      key: LDAP_APP_ID
- name: LDAP_APP_PASSWORD
  valueFrom:
    secretKeyRef:
      name: alerthub-dsldap-credentials
      key: LDAP_APP_PASSWORD

# ── ConfigMap ────────────────────────────────────────────────────────
- name: OIDC_PROVIDER_URL
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: OIDC_PROVIDER_URL
- name: OAUTH_CLIENT_ID
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: OAUTH_CLIENT_ID
- name: OAUTH_CALLBACK_URL
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: OAUTH_CALLBACK_URL
- name: INTERNAL_TLS_INSECURE
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: INTERNAL_TLS_INSECURE
- name: MAS_ENABLED
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: MAS_ENABLED
- name: MAS_AUTO_PROVISION
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: MAS_AUTO_PROVISION
- name: DSLDAP_ENABLED
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: DSLDAP_ENABLED
- name: DSLDAP_SERVER_URL
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: DSLDAP_SERVER_URL
- name: OIDC_ADMIN_GROUPS
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: OIDC_ADMIN_GROUPS
- name: OIDC_OPERATOR_GROUPS
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: OIDC_OPERATOR_GROUPS
- name: OIDC_VIEWER_GROUPS
  valueFrom:
    configMapKeyRef:
      name: alerthub-config
      key: OIDC_VIEWER_GROUPS
```

> **Important**: `INTERNAL_TLS_INSECURE` must be wired as an **env var** in the deployment spec, not just present in the ConfigMap. The backend reads it as `os.Getenv("INTERNAL_TLS_INSECURE")` at startup. If absent, all TLS calls to Floodgate, DS-LDAP, and IDMS will fail with certificate verification errors.

---

## 9. Redis Cache Keys

| Key pattern | TTL | Content |
|---|---|---|
| `mas:state:{state}` | 10 min | CSRF guard — validates OAuth state param on callback |
| `mas:code:{exchange_code}` | 2 min | One-time exchange code → full session payload |
| `idms:token:{user_uuid}` | 55 min | IDMS access_token (for userinfo calls) |
| `idms:refresh:{user_uuid}` | 7 days | IDMS refresh_token (used to renew Floodgate token) |
| `floodgate:token:{user_uuid}` | 55 min | Floodgate id_token — the Bearer credential for LLM calls |
| `user:photo:{user_uuid}` | 24 hours | Profile picture URL |
| `ldap:groups:{email}` | 5 min | DS-LDAP group memberships |

When Redis is unavailable, the service falls back to an in-process TTL store. Tokens survive the Redis outage but are lost on pod restart.

---

## 10. API Endpoints Reference

### Authentication

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/v1/auth/mas` | None | Initiate OAuth login → redirects to IdMS |
| `GET` | `/api/v1/auth` | None | IdMS OAuth callback (registered redirect URI) |
| `GET` | `/api/v1/auth/mas/callback` | None | Alternative callback path |
| `GET` | `/api/v1/auth/mas/exchange` | None | Exchange one-time code → JWT + Floodgate token |
| `GET` | `/api/v1/auth/mas/settings` | None | Public endpoint: returns MAS config (is_enabled, etc.) |
| `GET` | `/api/v1/auth/mas/floodgate-refresh` | JWT | Silently refresh Floodgate token |
| `POST` | `/api/v1/oauth/refresh` | JWT | Refresh AlertHub access_token |
| `POST` | `/api/v1/oauth/token` | JWT | Generate token with JWT assertion |

### Floodgate Proxy

| Method | Path | Auth | Description |
|---|---|---|---|
| `ANY` | `/api/v1/floodgate/*` | JWT | Proxy to Floodgate — substitutes caller's Floodgate token |

The proxy forwards all HTTP methods (GET, POST, PUT, DELETE, PATCH) and passes the cached Floodgate id_token in `Authorization: Bearer`.

### IdMS (External — called by backend, not frontend)

| Method | URL | Description |
|---|---|---|
| `GET` | `/IDMSWebAuth/appleauth/auth/oauth2/v2/authorize` | User authorization |
| `POST` | `/auth/oauth2/token` | Token exchange and refresh |
| `GET` | `/auth/oauth2/userinfo` | Fetch additional user claims |
| `GET` | `/.well-known/openid_configuration` | OIDC discovery (health check) |

---

## 11. AD Groups Required

### Floodgate Access

| Group | Grants |
|---|---|
| `aileron-operators` | Base LLM access — required for any model |
| `floodgate-anthropic-access` | Claude model access |
| `floodgate-google-models-access` | Gemini model access |

### AlertHub Roles

| Group | AlertHub Role | Can be customized |
|---|---|---|
| `aileron-admins` | `admin` | Yes — Admin → RBAC |
| `aileron-operators` | `operator` | Yes |
| `aileron-operators` | `operator` | Yes |
| `aileron-viewers` | `viewer` | Yes |

Groups are sourced from **two places**: the `groups` claim in the IdMS id_token AND a live DS-LDAP `memberOf` lookup. The LDAP result takes precedence over the token claim when `DSLDAP_ENABLED=true`, since token claims can lag real group changes by up to the OIDC session lifetime.

---

## 12. Troubleshooting

| Symptom | Root Cause | Fix |
|---|---|---|
| Floodgate returns 401 | `audience` missing from `/authorize` or `/token` request | Ensure `audience=hvys3fcwcteqrvw3qzkvtk86viuoqv` in both calls |
| `token exchange failed with status 400` | `redirect_uri` mismatch between calls or vs registration | Must be byte-for-byte identical; check trailing slash |
| LDAP groups not loading | `INTERNAL_TLS_INSECURE` not wired as env var in deployment | Add to deployment env block, not just ConfigMap |
| User always gets `viewer` role | DS-LDAP disabled or group name mismatch | Check `DSLDAP_ENABLED=true`; verify group names are exact CN values |
| "IDMS session expired" error | IDMS refresh token TTL (7 days) elapsed | User must re-login; no silent recovery |
| `no token available after 3 attempts` | `appleconnect` not installed or pod not on Apple network | Use OAuth browser flow; verify pod network policy allows egress to `` |
| Floodgate models endpoint returns 403 | User not in `aileron-operators` | Add user to AD group in IdMS portal |
| Profile photo not loading | `picture` claim absent from id_token | Backend falls back to userinfo endpoint automatically; if still missing, IdMS may not have a photo registered |
| Callback returns 401 "state mismatch" | Redis evicted state key (Redis memory pressure or restart) | Increase Redis `maxmemory` or re-login; state TTL is 10 min |
| TLS errors to `` | `INTERNAL_TLS_INSECURE` not set | Set env var in deployment spec |

---

## 13. Local Development (appleconnect CLI)

For non-browser environments (local scripts, CI, direct API testing), obtain a Floodgate token directly without going through the full OAuth flow:

```bash
appleconnect getToken \
  -C hvys3fcwcteqrvw3qzkvtk86viuoqv \
  --token-type=oauth \
  --interactivity-type=none \
  -E prod \
  -G pkce \
  -o "openid,dsid,accountname,profile,groups"
```

Copy the `oauth-id-token` value from the output and use it directly:

```bash
curl -X POST /api/anthropic/v1/messages \
  -H "Authorization: Bearer <oauth-id-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-4-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

The backend `FloodgateService.GetTokenDirect()` uses this command internally with **3 retry attempts** and exponential backoff: 2s → 4s → 8s.

For K8s pods that have mTLS certificates mounted at `/narrative/kube-actor/cert.pem` and `/narrative/kube-actor/private.pem`, the Floodgate service uses mTLS authentication automatically and skips the `appleconnect` flow.

---

## 14. Source Files Reference

| File | Purpose |
|---|---|
| `internal/services/oauth/enhanced_oauth.go` | Authorization URL generation, token exchange, ID token parsing, userinfo fetch |
| `internal/services/oauth/oauth.go` | OAuth client, Floodgate token refresh, in-memory token cache |
| `internal/services/floodgate/floodgate.go` | Floodgate API client, token acquisition, model listing |
| `internal/services/dsldap/dsldap.go` | DS-LDAP bind, user group search, group→role mapping |
| `internal/api/handlers/mas_handlers.go` | HTTP handlers: login initiation, callback, exchange, Floodgate refresh |
| `internal/api/handlers/oauth_handlers.go` | Token management endpoints (refresh, cache clear) |
| `internal/services/rbac/rbac.go` | RBAC permission check, role resolution |
| `k8s/production-deployment-example-cluster.yaml` | Production K8s deployment with all env wiring |
| `cmd/main.go` | OAuth client initialization (lines ~1127–1138), service wiring |
