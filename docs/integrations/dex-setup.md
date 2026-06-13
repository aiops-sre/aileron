# Dex Identity Provider Setup

This guide walks through deploying Dex as the OIDC identity provider for Aileron, configuring upstream connectors (GitHub and LDAP), mapping groups to Aileron roles, and verifying the auth flow.

Dex federates your existing identity sources (GitHub teams, LDAP groups, Google Workspace) into a single OIDC endpoint that Aileron consumes. No code changes are needed in Aileron to switch providers — only three environment variables change.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Install Dex via Aileron Helm Chart](#2-install-dex-via-aileron-helm-chart)
3. [Configure GitHub Connector](#3-configure-github-connector)
4. [Configure LDAP Connector](#4-configure-ldap-connector)
5. [Map Groups to Aileron Roles](#5-map-groups-to-aileron-roles)
6. [Test the Auth Flow](#6-test-the-auth-flow)
7. [Troubleshooting](#7-troubleshooting)

---

## 1. Prerequisites

Before starting:

- Aileron deployed via Helm in Kubernetes (see the [root README](../../README.md))
- `kubectl` access to the `aileron` namespace
- A publicly reachable DNS hostname for Dex (e.g. `dex.aileron.example.com`)
- TLS certificates (cert-manager recommended — see Helm chart)
- For GitHub connector: a GitHub OAuth App with callback URL set
- For LDAP connector: network access from the `aileron` namespace to your LDAP server

---

## 2. Install Dex via Aileron Helm Chart

Dex ships as an optional component in the Aileron Helm chart. Enable it with a single flag.

### 2.1 Minimal Deployment (no connector yet)

```bash
helm upgrade --install aileron ./platform/helm \
  --namespace aileron --create-namespace \
  --set dex.enabled=true \
  --set dex.issuerURL=https://dex.aileron.example.com \
  --set dex.client.id=aileron \
  --set dex.client.secret=$(openssl rand -hex 16) \
  --set dex.ingress.host=dex.aileron.example.com \
  --set certManager.enabled=true \
  --set certManager.clusterIssuer=letsencrypt-prod
```

This deploys Dex with:
- OIDC discovery at `https://dex.aileron.example.com/.well-known/openid-configuration`
- Aileron configured as a downstream client
- Let's Encrypt TLS on the Dex ingress

Verify Dex is running:

```bash
kubectl get pods -n aileron -l app=dex
# Expected: dex-xxx   1/1   Running

curl -s https://dex.aileron.example.com/.well-known/openid-configuration | jq .issuer
# Expected: "https://dex.aileron.example.com"
```

### 2.2 Configure Aileron to Use Dex

Update the Aileron platform deployment to point at Dex:

```bash
kubectl set env deployment/aileron-platform -n aileron \
  OIDC_PROVIDER_URL=https://dex.aileron.example.com \
  OIDC_CLIENT_ID=aileron \
  OIDC_CLIENT_SECRET=<the-secret-you-set-above> \
  OIDC_REDIRECT_URI=https://aileron.example.com/api/v1/auth/oidc/callback

kubectl rollout restart deployment aileron-platform -n aileron
kubectl rollout status deployment aileron-platform -n aileron
```

Aileron auto-discovers Dex's endpoints via the `.well-known/openid-configuration` URL.

---

## 3. Configure GitHub Connector

The GitHub connector lets users log in with their GitHub account. Dex reads team membership and passes it as the `groups` claim, which Aileron uses for role mapping.

### 3.1 Create a GitHub OAuth App

1. Go to https://github.com/settings/developers → **OAuth Apps** → **New OAuth App**
2. Fill in:
   - **Application name**: `Aileron`
   - **Homepage URL**: `https://aileron.example.com`
   - **Authorization callback URL**: `https://dex.aileron.example.com/callback`
3. Click **Register application**
4. Note the **Client ID**
5. Click **Generate a new client secret** and note the **Client Secret**

### 3.2 Store GitHub Credentials as a Kubernetes Secret

```bash
kubectl create secret generic dex-github-connector \
  --namespace aileron \
  --from-literal=clientID=<your-github-client-id> \
  --from-literal=clientSecret=<your-github-client-secret>
```

### 3.3 Dex GitHub Connector Configuration

Create a `dex-config.yaml` with the GitHub connector:

```yaml
# dex-config.yaml — complete working GitHub connector config
issuer: https://dex.aileron.example.com

storage:
  type: kubernetes
  config:
    inCluster: true

web:
  http: 0.0.0.0:5556

frontend:
  issuer: Aileron
  logoURL: https://aileron.example.com/logo.svg

oauth2:
  responseTypes:
    - code
  skipApprovalScreen: true
  # Always include groups claim so Aileron can map roles
  alwaysShowLoginScreen: false

# Downstream client — Aileron backend
staticClients:
  - id: aileron
    redirectURIs:
      - https://aileron.example.com/api/v1/auth/oidc/callback
    name: Aileron AIOps
    secretEnv: DEX_CLIENT_SECRET  # injected from Kubernetes secret

connectors:
  - type: github
    id: github
    name: GitHub
    config:
      # Read from Kubernetes secret (mounted as env vars)
      clientID: $GITHUB_CLIENT_ID
      clientSecret: $GITHUB_CLIENT_SECRET
      redirectURI: https://dex.aileron.example.com/callback

      # Restrict to your GitHub organization(s)
      # Remove this block to allow any GitHub user
      orgs:
        - name: your-github-org
          # Optional: restrict to specific teams within the org
          # teams:
          #   - aileron-admins
          #   - aileron-operators
          #   - sre-team

      # Load all team memberships as groups claim
      # Groups will appear as "your-github-org:team-slug"
      loadAllGroups: true

      # Use GitHub login as preferred username
      useLoginAsID: false

# Token validity
expiry:
  signingKeys: 6h
  idTokens: 24h
  refreshTokens:
    validIfNotUsedFor: 720h  # 30 days
    absoluteLifetime: 720h

# Always emit groups claim
enablePasswordDB: false
```

Apply the config as a ConfigMap and update the Dex deployment:

```bash
kubectl create configmap dex-config \
  --namespace aileron \
  --from-file=config.yaml=dex-config.yaml \
  --dry-run=client -o yaml | kubectl apply -f -

# Restart Dex to pick up new config
kubectl rollout restart deployment dex -n aileron
```

### 3.4 Verify GitHub Groups in Token

After configuring the connector, log in and check that Dex is passing team groups:

```bash
# Decode the id_token JWT (base64 decode the middle section)
# Look for the "groups" claim — should list org:team pairs:
# "groups": ["your-github-org:aileron-admins", "your-github-org:sre-team"]
```

---

## 4. Configure LDAP Connector

The LDAP connector lets users log in with corporate directory credentials. Dex binds to LDAP, authenticates the user, and reads group membership.

### 4.1 Store LDAP Bind Credentials

```bash
kubectl create secret generic dex-ldap-connector \
  --namespace aileron \
  --from-literal=bindDN="CN=svc-aileron,OU=ServiceAccounts,DC=corp,DC=example,DC=com" \
  --from-literal=bindPW="your-service-account-password"
```

### 4.2 Dex LDAP Connector Configuration

Add this connector block to your `dex-config.yaml`:

```yaml
connectors:
  - type: ldap
    id: ldap
    name: Corporate LDAP
    config:
      # LDAP server address
      # Use ldaps:// for TLS (recommended for production)
      host: ldap.corp.example.com:636

      # TLS configuration
      # Set insecureNoSSL: true ONLY for testing
      insecureSkipVerify: false
      rootCAData: |
        -----BEGIN CERTIFICATE-----
        # Paste your LDAP server CA cert here (base64-encoded)
        -----END CERTIFICATE-----

      # Bind credentials from Kubernetes secret
      # Mount the secret as environment variables in the Dex deployment
      bindDN: $LDAP_BIND_DN
      bindPW: $LDAP_BIND_PW

      # User search configuration
      userSearch:
        # Base DN for user search
        baseDN: "OU=Users,DC=corp,DC=example,DC=com"
        # Filter: only active users in the Aileron OU (adjust to your schema)
        filter: "(objectClass=person)"
        # Attribute used as login username (sAMAccountName for AD, uid for OpenLDAP)
        username: sAMAccountName
        # Attribute to use as user ID (must be unique and stable)
        idAttr: sAMAccountName
        # Attribute for display name in Dex UI
        nameAttr: displayName
        # Email attribute
        emailAttr: mail
        # Optional: prefer email as ID (set true if using OpenLDAP with mail as UID)
        emailSuffix: ""

      # Group search configuration
      groupSearch:
        # Base DN for group search
        baseDN: "OU=Groups,DC=corp,DC=example,DC=com"
        # Filter: only security groups (adjust for distribution groups, etc.)
        filter: "(objectClass=group)"
        # Attribute that contains the list of members
        userMatchers:
          - userAttr: DN
            groupAttr: member
        # Attribute to use as the group name (becomes the "groups" claim value)
        nameAttr: cn
```

**For OpenLDAP** (instead of Active Directory), adjust:

```yaml
      userSearch:
        baseDN: "ou=people,dc=example,dc=com"
        filter: "(objectClass=inetOrgPerson)"
        username: uid
        idAttr: uid
        emailAttr: mail

      groupSearch:
        baseDN: "ou=groups,dc=example,dc=com"
        filter: "(objectClass=posixGroup)"
        userMatchers:
          - userAttr: uid
            groupAttr: memberUid
        nameAttr: cn
```

### 4.3 Test LDAP Connectivity from Dex Pod

```bash
# Test LDAP bind and search from inside the cluster
kubectl exec -n aileron deploy/dex -- \
  ldapsearch -H ldaps://ldap.corp.example.com:636 \
  -D "CN=svc-aileron,OU=ServiceAccounts,DC=corp,DC=example,DC=com" \
  -w "$LDAP_BIND_PW" \
  -b "OU=Users,DC=corp,DC=example,DC=com" \
  "(sAMAccountName=your.username)" cn mail memberOf
```

If this returns results, the LDAP connector will work correctly.

---

## 5. Map Groups to Aileron Roles

Aileron resolves roles from OIDC group claims in priority order:

1. `ldap_group_role_mappings` table (Admin UI configured — highest priority)
2. `OIDC_ADMIN_GROUPS` environment variable
3. `OIDC_OPERATOR_GROUPS` environment variable
4. `OIDC_VIEWER_GROUPS` environment variable
5. Existing DB role (never downgraded automatically)
6. `OIDC_DEFAULT_ROLE` (default: `viewer`)

### 5.1 Configure via Environment Variables

Set these on the `aileron-platform` deployment:

```bash
# GitHub connector: groups appear as "org:team-slug"
kubectl set env deployment/aileron-platform -n aileron \
  OIDC_ADMIN_GROUPS="your-github-org:aileron-admins,your-github-org:platform-leads" \
  OIDC_OPERATOR_GROUPS="your-github-org:sre-team,your-github-org:on-call" \
  OIDC_VIEWER_GROUPS="your-github-org:developers,your-github-org:engineering" \
  OIDC_DEFAULT_ROLE=viewer

# LDAP connector: groups appear as the CN of the group
kubectl set env deployment/aileron-platform -n aileron \
  OIDC_ADMIN_GROUPS="aileron-admins,platform-engineering" \
  OIDC_OPERATOR_GROUPS="sre-team,noc-team" \
  OIDC_VIEWER_GROUPS="engineering,developers" \
  OIDC_DEFAULT_ROLE=viewer
```

### 5.2 Configure via Admin UI (runtime, no restart required)

Navigate to **Admin → Auth Settings → Group Role Mappings** in the Aileron dashboard:

```
Group Name: aileron-admins      → Role: admin
Group Name: sre-team            → Role: operator
Group Name: developers          → Role: viewer
```

DB-backed mappings take effect within 5 minutes (cache TTL). No pod restart required.

### 5.3 Role Capabilities

| Role | Capabilities |
|---|---|
| **admin** | Full platform control: user management, correlation rules, policies, all settings |
| **operator** | Create/edit correlation rules, approve/reject remediation proposals, manage runbooks |
| **viewer** | Read-only: view incidents, RCA results, topology, dashboard |

---

## 6. Test the Auth Flow

### 6.1 Verify OIDC Discovery

```bash
# Dex discovery endpoint must be reachable from the Aileron backend pod
kubectl exec -n aileron deploy/aileron-platform -- \
  wget -qO- https://dex.aileron.example.com/.well-known/openid-configuration | jq .

# Expected fields:
{
  "issuer": "https://dex.aileron.example.com",
  "authorization_endpoint": "https://dex.aileron.example.com/auth",
  "token_endpoint": "https://dex.aileron.example.com/token",
  "jwks_uri": "https://dex.aileron.example.com/keys",
  "scopes_supported": ["openid", "email", "profile", "groups", "offline_access"]
}
```

### 6.2 Manual Auth Flow Test

```bash
# 1. Initiate OIDC login — should redirect to Dex
curl -v "https://aileron.example.com/api/v1/auth/oidc?redirect=/dashboard"
# Expected: 302 redirect to https://dex.aileron.example.com/auth?...

# 2. Complete login in browser at https://aileron.example.com
# Log in with GitHub or LDAP credentials

# 3. After redirect back, exchange the one-time code for JWT
# The frontend does this automatically; for testing:
curl "https://aileron.example.com/api/v1/auth/oidc/exchange?code=<one-time-code>"
# Expected: {"access_token": "...", "refresh_token": "...", "role": "admin"}
```

### 6.3 Verify Groups in JWT

```bash
# Decode the access_token (JWT) — base64 decode the middle section
echo "<access_token>" | cut -d. -f2 | base64 -d 2>/dev/null | jq .

# Expected fields:
{
  "sub": "github|12345678",
  "email": "user@example.com",
  "role": "admin",         # ← resolved by Aileron from OIDC groups
  "groups": ["your-github-org:aileron-admins"]  # ← from Dex groups claim
}
```

### 6.4 Verify Role Resolution

```bash
# Check what role a specific user has
kubectl exec -n aileron deploy/aileron-platform -- \
  psql $DATABASE_URL -c "SELECT username, email, role FROM users WHERE email = 'user@example.com';"

# Check group → role mappings in DB
kubectl exec -n aileron deploy/aileron-platform -- \
  psql $DATABASE_URL -c "SELECT group_name, role FROM ldap_group_role_mappings ORDER BY group_name;"
```

---

## 7. Troubleshooting

### 7.1 "Unable to reach OIDC provider" on startup

```bash
# Check Aileron backend logs
kubectl logs -n aileron deploy/aileron-platform | grep "oidc\|OIDC\|provider"

# Common causes:
# 1. DNS not resolving inside cluster
kubectl exec -n aileron deploy/aileron-platform -- nslookup dex.aileron.example.com
# Fix: verify the Ingress for Dex is created and DNS is pointed correctly

# 2. TLS certificate not trusted
kubectl exec -n aileron deploy/aileron-platform -- \
  wget -qO- https://dex.aileron.example.com/.well-known/openid-configuration
# Fix: if using self-signed cert, add CA to aileron-platform pod's trust store
# or set OIDC_INSECURE_SKIP_VERIFY=true (not recommended for production)
```

### 7.2 Users Get "viewer" Role When They Should Be Admin

```bash
# 1. Check what groups Dex is returning
kubectl logs -n aileron deploy/dex | grep "groups\|connector"

# 2. Check that group names exactly match OIDC_ADMIN_GROUPS env var
kubectl get deployment aileron-platform -n aileron -o jsonpath='{.spec.template.spec.containers[0].env}' | \
  python3 -c "import json,sys; [print(e['name'],'=',e.get('value','')) for e in json.load(sys.stdin)]" | \
  grep -i group

# 3. Verify groups are in the JWT
# Decode the id_token and check the "groups" claim
# GitHub: "groups": ["org:team-slug"]  (format is exact — case sensitive)
# LDAP: "groups": ["aileron-admins"]  (CN of the group)
```

### 7.3 GitHub Connector: "organization not accessible"

```bash
# The GitHub OAuth App must be authorized by your organization
# Go to: https://github.com/organizations/<your-org>/settings/oauth_application_policy
# Find "Aileron" and click "Grant"

# Also verify the OAuth App has been authorized for the organization:
# https://github.com/settings/connections/applications/<client-id>
```

### 7.4 LDAP Connector: "LDAP Result Code 49 'Invalid Credentials'"

```bash
# Test bind credentials directly
ldapsearch -H ldaps://ldap.corp.example.com:636 \
  -D "CN=svc-aileron,OU=ServiceAccounts,DC=corp,DC=example,DC=com" \
  -w "your-password" \
  -b "DC=corp,DC=example,DC=com" \
  "(cn=*)" dn 2>&1 | head -20

# If "Invalid credentials": the bindDN or bindPW in the Kubernetes secret is wrong
kubectl get secret dex-ldap-connector -n aileron -o jsonpath='{.data.bindDN}' | base64 -d
# Verify this exactly matches the DN in your directory
```

### 7.5 LDAP Connector: Groups Not Appearing in Token

```bash
# Test group search independently
ldapsearch -H ldaps://ldap.corp.example.com:636 \
  -D "CN=svc-aileron,OU=ServiceAccounts,DC=corp,DC=example,DC=com" \
  -w "your-password" \
  -b "OU=Groups,DC=corp,DC=example,DC=com" \
  "(objectClass=group)" cn member 2>&1 | head -50

# If groups are found but not in token:
# 1. Check the groupSearch.userMatchers configuration:
#    userAttr must match userSearch.idAttr
#    For AD: userAttr=DN, groupAttr=member
#    For OpenLDAP: userAttr=uid, groupAttr=memberUid
# 2. Check if the service account has permission to read group members
```

### 7.6 "Redirect URI Mismatch" Error

```bash
# The redirectURI in Dex staticClients must exactly match
# what Aileron sends in the auth request

# Check Aileron's configured redirect URI:
kubectl get deployment aileron-platform -n aileron \
  -o jsonpath='{.spec.template.spec.containers[0].env}' | \
  python3 -c "import json,sys; envs=json.load(sys.stdin); [print(e['value']) for e in envs if e['name']=='OIDC_REDIRECT_URI']"

# Must match exactly:
# https://aileron.example.com/api/v1/auth/oidc/callback
# (no trailing slash, exact scheme and hostname)
```

### 7.7 Token Refresh Fails After Long Inactivity

By default, Dex refresh tokens expire after 720 hours (30 days) of non-use. Aileron's frontend proactively refreshes tokens 5 minutes before expiry.

If users are being logged out unexpectedly:

```bash
# Check Dex refresh token settings
kubectl get configmap dex-config -n aileron -o jsonpath='{.data.config\.yaml}' | \
  grep -A5 refreshTokens

# Extend refresh token lifetime if needed:
expiry:
  refreshTokens:
    validIfNotUsedFor: 2160h   # 90 days
    absoluteLifetime: 2160h

# Also check the frontend inactivity timeout:
# ACTIVITY_TIMEOUT env var on aileron-frontend (default: 3600s = 1 hour)
```

### 7.8 Dex Pod CrashLoopBackOff

```bash
kubectl logs -n aileron deploy/dex --previous
# Common causes:

# 1. ConfigMap syntax error
#    Fix: validate YAML: kubectl apply --dry-run=client -f dex-config.yaml

# 2. "storage backend not ready"
#    Fix: Dex uses Kubernetes CRDs for storage — verify RBAC
kubectl get clusterrole dex -n aileron
kubectl get clusterrolebinding dex -n aileron

# 3. "failed to read connector config: env var not found"
#    Fix: ensure GITHUB_CLIENT_ID / LDAP_BIND_DN etc. are mounted from secrets
kubectl get deployment dex -n aileron -o yaml | grep -A10 envFrom
```

### 7.9 Enable Dex Debug Logging

```bash
# Add debug logging to Dex config:
logger:
  level: debug
  format: text

# Then check logs:
kubectl logs -n aileron deploy/dex -f | grep -E "connector|token|groups|error"
```
