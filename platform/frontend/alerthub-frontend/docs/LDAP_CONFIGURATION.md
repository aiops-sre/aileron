# LDAP Authentication Configuration Guide

This guide explains how to configure LDAP authentication for SRE Command Center based on Apple's internal LDAP infrastructure.

## Overview

SRE Command Center supports LDAP authentication via Apple's LDAP infrastructure (``), similar to the Jenkins configuration.

## Prerequisites

- Access to Apple's internal network
- LDAP service account credentials (AppID)
- Network connectivity to `:636`

## Configuration

### 1. Environment Variables

Add the following environment variables to your deployment configuration:

```bash
# LDAP Configuration
LDAP_ENABLED=true
LDAP_SERVER=
LDAP_PORT=636
LDAP_BASE_DN=o=apple
LDAP_USER_SEARCH_BASE=ou=people
LDAP_USER_FILTER=appleconnectname=%s
LDAP_GROUP_SEARCH_BASE=ou=groups
LDAP_GROUP_FILTER=uniquemember=%s
LDAP_BIND_DN=appid=<YOUR_APP_ID>,ou=applications,o=apple
LDAP_BIND_PASSWORD=<YOUR_APP_PASSWORD>
LDAP_USE_TLS=true
LDAP_UPDATE_INTERVAL=15

# Display name and email attributes
LDAP_DISPLAY_NAME_ATTR=cn
LDAP_EMAIL_ATTR=mail
```

### 2. LDAP Connection Details

Based on Jenkins configuration:

```yaml
Server: /
Root DN: o=apple
User Search Base: ou=people
User Search Filter: appleconnectname={0}
Group Search Base: ou=groups
Group Membership Filter: uniquemember={0}
Manager DN: appid=<APP_ID>,ou=applications,o=apple
```

### 3. Group to Role Mapping

The application automatically maps LDAP groups to application roles:

| LDAP Group Pattern | Application Role |
|-------------------|------------------|
| `*AlertHub-Admins*` or `*admin*` | `admin` |
| `*AlertHub-Managers*` or `*manager*` | `manager` |
| `*AlertHub-Engineers*` or `*engineer*` | `engineer` |
| `*AlertHub-Viewers*` or `*viewer*` | `viewer` |
| Default (no match) | `viewer` |

### 4. Example Groups from Jenkins

Based on the Jenkins configuration, these Apple groups can access the system:

**Admin Groups:**
- `aileron`
- `aileron-operators`
- `aileron-operators`
- `aileron-admins`

**Developer/Engineer Groups:**
- `int-auto-sample-build`
- `interactive-apps-dev`
- `interactive-release-engineering`
- `interactive-release-operations`

**Manager Groups:**
- `interactive-apps-systems`

**Viewer Groups:**
- `iasys-rome-cigniti`
- `interactive-rome-dev-team-core`
- `marcom-dotcom-qa-tools`

## Backend Configuration

The backend already includes LDAP support through:

### File: `internal/services/sso/sso.go`

The LDAP provider implementation includes:
- TLS/SSL connection support
- User authentication
- Group membership retrieval
- Attribute extraction (email, displayName, cn)

### File: `internal/api/handlers/auth.go`

The LDAP login endpoint is available at:

```
POST /api/v1/auth/login/ldap
```

Request body:
```json
{
  "username": "your_appleconnectname",
  "password": "your_password"
}
```

Response:
```json
{
  "success": true,
  "message": "LDAP login successful",
  "data": {
    "user": {
      "id": "uuid",
      "username": "username",
      "email": "user@apple.com",
      "full_name": "Full Name",
      "role": "admin"
    },
    "tokens": {
      "access_token": "jwt_token",
      "refresh_token": "refresh_token"
    }
  }
}
```

## Frontend Integration

The frontend login page needs to be updated to include an LDAP login option.

### Login Flow

1. User enters Apple Connect username and password
2. Frontend sends POST request to `/api/v1/auth/login/ldap`
3. Backend authenticates against LDAP server
4. Backend syncs user to local database
5. Backend generates JWT tokens
6. Frontend stores tokens and redirects to dashboard

## Deployment Steps

### 1. Update Kubernetes ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: alerthub-ldap-config
  namespace: your-namespace
data:
  LDAP_ENABLED: "true"
  LDAP_SERVER: ""
  LDAP_PORT: "636"
  LDAP_BASE_DN: "o=apple"
  LDAP_USER_SEARCH_BASE: "ou=people"
  LDAP_USER_FILTER: "appleconnectname=%s"
  LDAP_GROUP_SEARCH_BASE: "ou=groups"
  LDAP_GROUP_FILTER: "uniquemember=%s"
  LDAP_USE_TLS: "true"
  LDAP_DISPLAY_NAME_ATTR: "cn"
  LDAP_EMAIL_ATTR: "mail"
```

### 2. Update Kubernetes Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: alerthub-ldap-secret
  namespace: your-namespace
type: Opaque
stringData:
  LDAP_BIND_DN: "appid=YOUR_APP_ID,ou=applications,o=apple"
  LDAP_BIND_PASSWORD: "YOUR_APP_PASSWORD"
```

### 3. Update Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: alerthub-backend
spec:
  template:
    spec:
      containers:
      - name: backend
        envFrom:
        - configMapRef:
            name: alerthub-ldap-config
        - secretRef:
            name: alerthub-ldap-secret
```

## Testing LDAP Connection

### 1. Test LDAP Connectivity

```bash
# Test LDAP connection from pod
kubectl exec -it <backend-pod> -- bash

# Install ldapsearch if not available
ldapsearch -H  \
  -D "appid=YOUR_APP_ID,ou=applications,o=apple" \
  -w "YOUR_PASSWORD" \
  -b "ou=people,o=apple" \
  "appleconnectname=testuser"
```

### 2. Test via API

```bash
curl -X POST https://your-alerthub-domain/api/v1/auth/login/ldap \
  -H "Content-Type: application/json" \
  -d '{
    "username": "your_appleconnect_username",
    "password": "your_password"
  }'
```

### 3. Check Logs

```bash
# View backend logs
kubectl logs -f deployment/alerthub-backend | grep -i ldap
```

## Security Considerations

1. **TLS/SSL**: Always use LDAPS (port 636) for encrypted connections
2. **Credentials**: Store LDAP bind credentials in Kubernetes Secrets
3. **Service Account**: Use dedicated AppID for LDAP binding
4. **Audit Logging**: All LDAP login attempts are logged in `audit_logs` table
5. **Session Management**: JWT tokens expire after configured time
6. **Group Validation**: Validate user group membership on each login

## Troubleshooting

### Connection Issues

```bash
# Test DNS resolution
nslookup 

# Test port connectivity
telnet  636

# Check TLS certificate
openssl s_client -connect :636 -showcerts
```

### Authentication Issues

1. **Invalid Credentials**: Verify bind DN and password
2. **User Not Found**: Check user search filter and base DN
3. **No Groups**: Verify group search base and filter
4. **Wrong Role**: Check group-to-role mapping logic

### Audit Logs

Check the `audit_logs` table for authentication attempts:

```sql
SELECT 
  action,
  details->>'username' as username,
  details->>'groups' as groups,
  ip_address,
  created_at
FROM audit_logs
WHERE action IN ('ldap_login_success', 'ldap_login_failed')
ORDER BY created_at DESC
LIMIT 20;
```

## Group Mapping Configuration

The current group mapping logic is in [`internal/services/sso/sso.go`](../../internal/services/sso/sso.go):

```go
func (s *SSOService) MapLDAPGroupsToRole(groups []string) string {
    // Define group to role mapping
    groupRoleMap := map[string]string{
        "CN=AlertHub-Admins,OU=Groups,DC=apple,DC=com":    "admin",
        "CN=AlertHub-Managers,OU=Groups,DC=apple,DC=com":  "manager",
        "CN=AlertHub-Engineers,OU=Groups,DC=apple,DC=com": "engineer",
        "CN=AlertHub-Viewers,OU=Groups,DC=apple,DC=com":   "viewer",
    }

    // Check groups in priority order
    for _, group := range groups {
        if role, exists := groupRoleMap[group]; exists {
            return role
        }
    }

    // Default to viewer role
    return "viewer"
}
```

To customize group mappings, update this function or make it configurable via environment variables.

## Database Schema

Users authenticated via LDAP are automatically synced to the `users` table:

```sql
-- LDAP users are created with:
-- - username from appleconnectname
-- - email from mail attribute
-- - full_name from cn/displayName attribute
-- - is_verified = true
-- - random password (not used for LDAP users)
```

## References

- Jenkins LDAP Configuration: `/opt/data/jenkins/config.xml`
- Backend LDAP Service: [`internal/services/sso/sso.go`](../../internal/services/sso/sso.go)
- Auth Handler: [`internal/api/handlers/auth.go`](../../internal/api/handlers/auth.go)
- Frontend Login: [`src/pages/LoginPage.tsx`](../src/pages/LoginPage.tsx)

## Support

For issues or questions:
1. Check audit logs for authentication failures
2. Verify LDAP connectivity from the backend pod
3. Review group memberships in LDAP
4. Contact SRE team for LDAP service account issues
