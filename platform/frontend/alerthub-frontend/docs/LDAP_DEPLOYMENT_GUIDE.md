# LDAP Deployment Guide for SRE Command Center

## Overview

This guide provides step-by-step instructions for deploying SRE Command Center with LDAP authentication enabled, using Apple's internal LDAP infrastructure.

## Prerequisites

✅ **Required:**
- Kubernetes cluster access
- kubectl configured and authenticated
- AppID credentials for LDAP binding
- Network connectivity to `:636`
- Namespace created (e.g., `alerthub`)

✅ **Backend Configuration:**
- Go backend with LDAP support ([`internal/services/sso/sso.go`](../../internal/services/sso/sso.go))
- Auth handlers with LDAP endpoint ([`internal/api/handlers/auth.go`](../../internal/api/handlers/auth.go))

✅ **Frontend Configuration:**
- React frontend with LDAP login UI ([`src/pages/LoginPage.tsx`](../src/pages/LoginPage.tsx))
- Login method toggle (LDAP vs Manual)
- Apple Connect username field

## Deployment Steps

### Step 1: Obtain LDAP Credentials

Contact your Apple IT administrator to get:

1. **AppID** for LDAP service account
2. **AppID Password** (encrypted)
3. Verify access to `:636`

Example AppID format:
```
appid=171372,ou=applications,o=apple
```

### Step 2: Create Kubernetes Namespace

```bash
# Create namespace if it doesn't exist
kubectl create namespace alerthub

# Set as default namespace (optional)
kubectl config set-context --current --namespace=alerthub
```

### Step 3: Deploy LDAP Configuration

```bash
# Navigate to k8s directory
cd sre-command-center/k8s

# Update the ldap-config.yaml with your credentials
# Edit the Secret section:
# - LDAP_BIND_DN: Your AppID
# - LDAP_BIND_PASSWORD: Your AppID password
# - JWT_SECRET: Generate a random secret
# - DATABASE_URL: Your PostgreSQL connection string

# Apply configuration
kubectl apply -f ldap-config.yaml
```

Verify the configuration:

```bash
# Check ConfigMap
kubectl get configmap sre-command-center-ldap-config -o yaml

# Check Secret (base64 encoded)
kubectl get secret sre-command-center-ldap-secret -o yaml
```

### Step 4: Update Backend Deployment

Create or update your backend deployment to use LDAP configuration:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sre-command-center-backend
  namespace: alerthub
spec:
  replicas: 2
  selector:
    matchLabels:
      app: sre-command-center
      component: backend
  template:
    metadata:
      labels:
        app: sre-command-center
        component: backend
    spec:
      containers:
      - name: backend
        image: your-registry/sre-command-center-backend:latest
        ports:
        - containerPort: 8080
          name: http
        envFrom:
        - configMapRef:
            name: sre-command-center-ldap-config
        - secretRef:
            name: sre-command-center-ldap-secret
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
```

Apply the deployment:

```bash
kubectl apply -f backend-deployment.yaml

# Watch the rollout
kubectl rollout status deployment/sre-command-center-backend
```

### Step 5: Update Frontend Deployment

Ensure the frontend is deployed with the updated LoginPage:

```bash
# Build frontend with LDAP support
cd sre-command-center
npm run build

# Build and push Docker image
docker build -t your-registry/sre-command-center-frontend:ldap-v1 -f Dockerfile .
docker push your-registry/sre-command-center-frontend:ldap-v1

# Update deployment
kubectl set image deployment/sre-command-center-frontend \
  frontend=your-registry/sre-command-center-frontend:ldap-v1
```

### Step 6: Configure Ingress

Ensure your ingress is configured for both frontend and backend:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: sre-command-center-ingress
  namespace: alerthub
  annotations:
    kubernetes.io/ingress.class: nginx
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  tls:
  - hosts:
    - aileron.example.com
    secretName: sre-command-center-tls
  rules:
  - host: aileron.example.com
    http:
      paths:
      - path: /api
        pathType: Prefix
        backend:
          service:
            name: sre-command-center-backend
            port:
              number: 8080
      - path: /
        pathType: Prefix
        backend:
          service:
            name: sre-command-center-frontend
            port:
              number: 80
```

Apply ingress:

```bash
kubectl apply -f ingress.yaml
```

### Step 7: Test LDAP Connection

#### From Backend Pod

```bash
# Get backend pod name
BACKEND_POD=$(kubectl get pods -l component=backend -o jsonpath='{.items[0].metadata.name}')

# Exec into pod
kubectl exec -it $BACKEND_POD -- /bin/sh

# Test LDAP connectivity (if ldapsearch is available)
apk add --no-cache openldap-clients  # if Alpine
ldapsearch -H  \
  -D "$LDAP_BIND_DN" \
  -w "$LDAP_BIND_PASSWORD" \
  -b "ou=people,o=apple" \
  "appleconnectname=testuser"

# Test DNS resolution
nslookup 

# Test port connectivity
nc -zv  636
```

#### Via API Endpoint

```bash
# Test LDAP login endpoint
curl -X POST https://aileron.example.com/api/v1/auth/login/ldap \
  -H "Content-Type: application/json" \
  -d '{
    "username": "your_appleconnect_username",
    "password": "your_password"
  }'
```

Expected successful response:

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
      "role_name": "admin"
    },
    "tokens": {
      "access_token": "jwt_token",
      "refresh_token": "refresh_token"
    }
  }
}
```

### Step 8: Verify Frontend Access

1. Open browser and navigate to `https://aileron.example.com`
2. You should see the login page with two tabs:
   - **LDAP (Apple Connect)** - For LDAP authentication
   - **Manual Login** - For local database authentication
3. Select "LDAP (Apple Connect)" tab
4. Enter your Apple Connect username and password
5. Click "Sign In with Apple LDAP"
6. Upon successful authentication, you should be redirected to the dashboard

### Step 9: Monitor and Verify

#### Check Backend Logs

```bash
# Stream backend logs
kubectl logs -f deployment/sre-command-center-backend | grep -i ldap

# Look for LDAP authentication attempts
# ✅ Success: "ldap_login_success"
# ❌ Failure: "LDAP authentication failed"
```

#### Check Database Audit Logs

```bash
# Connect to database
kubectl exec -it postgres-pod -- psql -U postgres -d alerthub

# Query audit logs for LDAP logins
SELECT 
  action,
  details->>'username' as username,
  details->>'groups' as groups,
  ip_address,
  created_at
FROM audit_logs
WHERE action LIKE '%ldap%'
ORDER BY created_at DESC
LIMIT 10;
```

#### Verify User Sync

```bash
# Check if LDAP users are being synced to database
SELECT 
  username,
  email,
  full_name,
  role_name,
  is_verified,
  last_login,
  created_at
FROM users
WHERE is_verified = true
ORDER BY created_at DESC
LIMIT 10;
```

## Group-Based Role Assignment

Users are automatically assigned roles based on their LDAP group membership:

| LDAP Group | Application Role |
|-----------|-----------------|
| `aileron` | admin |
| `aileron-operators` | admin |
| `aileron-operators` | admin |
| `aileron-admins` | admin |
| `interactive-apps-systems` | manager |
| `interactive-release-engineering` | manager |
| `interactive-apps-dev` | engineer |
| `interactive-release-operations` | engineer |
| `int-auto-sample-build` | engineer |
| `iasys-rome-cigniti` | viewer |
| `interactive-rome-dev-team-core` | viewer |
| `marcom-dotcom-qa-tools` | viewer |

Users not in any configured group receive the `viewer` role by default.

## Troubleshooting

### Issue: "LDAP connection failed"

**Symptoms:**
- Backend logs show connection errors
- Login fails with "LDAP authentication not available"

**Solutions:**
1. Check network connectivity:
   ```bash
   kubectl exec -it $BACKEND_POD -- nc -zv  636
   ```

2. Verify DNS resolution:
   ```bash
   kubectl exec -it $BACKEND_POD -- nslookup 
   ```

3. Check TLS certificate:
   ```bash
   kubectl exec -it $BACKEND_POD -- openssl s_client -connect :636
   ```

4. Verify LDAP configuration in ConfigMap:
   ```bash
   kubectl get configmap sre-command-center-ldap-config -o yaml
   ```

### Issue: "LDAP authentication failed"

**Symptoms:**
- Connection successful but authentication fails
- Audit logs show failed login attempts

**Solutions:**
1. Verify bind credentials:
   ```bash
   kubectl get secret sre-command-center-ldap-secret -o jsonpath='{.data.LDAP_BIND_DN}' | base64 -d
   ```

2. Test bind manually:
   ```bash
   ldapsearch -H  \
     -D "appid=YOUR_APP_ID,ou=applications,o=apple" \
     -w "password" \
     -b "o=apple" \
     "(objectClass=*)" \
     -LLL
   ```

3. Check user search filter in ConfigMap:
   - Ensure `LDAP_USER_FILTER` is set to `appleconnectname=%s`

4. Verify user search base:
   - Ensure `LDAP_USER_SEARCH_BASE` is set to `ou=people`

### Issue: "User assigned wrong role"

**Symptoms:**
- User can login but has incorrect permissions
- Role doesn't match expected group membership

**Solutions:**
1. Check user's LDAP groups:
   ```bash
   ldapsearch -H  \
     -D "$LDAP_BIND_DN" \
     -w "$LDAP_BIND_PASSWORD" \
     -b "ou=people,o=apple" \
     "appleconnectname=username" \
     memberOf
   ```

2. Verify group mapping configuration in ConfigMap:
   ```bash
   kubectl get configmap sre-command-center-ldap-config \
     -o jsonpath='{.data.LDAP_ADMIN_GROUPS}'
   ```

3. Update group mappings if needed:
   ```bash
   kubectl edit configmap sre-command-center-ldap-config
   # Restart backend pods to pick up changes
   kubectl rollout restart deployment/sre-command-center-backend
   ```

### Issue: Frontend not showing LDAP option

**Symptoms:**
- Login page only shows MAS and manual login
- LDAP tab missing

**Solutions:**
1. Verify frontend is using updated image:
   ```bash
   kubectl describe pod <frontend-pod> | grep Image:
   ```

2. Check browser console for errors:
   - Open DevTools (F12)
   - Look for JavaScript errors
   - Verify API calls to `/api/v1/auth/sso-providers`

3. Rebuild and redeploy frontend:
   ```bash
   cd sre-command-center
   npm run build
   docker build -t your-registry/sre-command-center-frontend:latest .
   docker push your-registry/sre-command-center-frontend:latest
   kubectl rollout restart deployment/sre-command-center-frontend
   ```

## Security Best Practices

1. **Credential Management:**
   - ✅ Store LDAP credentials in Kubernetes Secrets
   - ✅ Use encrypted AppID password
   - ✅ Rotate credentials regularly
   - ❌ Never commit credentials to Git

2. **TLS/SSL:**
   - ✅ Always use LDAPS (port 636)
   - ✅ Verify TLS certificates
   - ✅ Use TLS 1.2 or higher

3. **Network Security:**
   - ✅ Restrict LDAP access to backend pods only
   - ✅ Use network policies to limit egress
   - ✅ Enable audit logging for all LDAP operations

4. **Session Management:**
   - ✅ JWT tokens expire after 24 hours
   - ✅ Refresh tokens expire after 7 days
   - ✅ Implement token revocation
   - ✅ Use secure, HTTP-only cookies

## Rollback Procedure

If issues occur, rollback using:

```bash
# Rollback backend deployment
kubectl rollout undo deployment/sre-command-center-backend

# Rollback frontend deployment
kubectl rollout undo deployment/sre-command-center-frontend

# Disable LDAP temporarily
kubectl patch configmap sre-command-center-ldap-config \
  -p '{"data":{"LDAP_ENABLED":"false"}}'

# Restart backend to pick up changes
kubectl rollout restart deployment/sre-command-center-backend
```

## Performance Considerations

1. **LDAP Connection Pooling:**
   - Backend maintains connection pool
   - Connections are reused for efficiency
   - Idle connections timeout after 15 minutes

2. **Caching:**
   - User group memberships cached for 15 minutes
   - Reduces LDAP queries
   - Configurable via `LDAP_UPDATE_INTERVAL`

3. **Rate Limiting:**
   - Implement rate limiting on login endpoint
   - Prevent brute force attacks
   - Log excessive failed attempts

## Monitoring

### Metrics to Monitor

1. **LDAP Connection Metrics:**
   - Connection success/failure rate
   - Connection latency
   - Active connections

2. **Authentication Metrics:**
   - Login success rate
   - Login failures by user
   - LDAP vs. other auth methods

3. **Performance Metrics:**
   - Authentication latency
   - User sync time
   - Group lookup time

### Alerting

Set up alerts for:
- High LDAP authentication failure rate (>10% in 5 minutes)
- LDAP connection failures
- LDAP response time > 3 seconds
- Unusual login patterns

## Support and Contacts

For issues or questions:

1. **LDAP/AppID Issues:**
   - Contact Apple IT Support
   - Submit ticket for AppID access

2. **Application Issues:**
   - Check documentation in [`docs/LDAP_CONFIGURATION.md`](LDAP_CONFIGURATION.md)
   - Review backend logs
   - Check audit logs

3. **Network Issues:**
   - Verify network policies
   - Check firewall rules
   - Contact network team

## References

- **Jenkins LDAP Configuration:** `/opt/data/jenkins/config.xml`
- **LDAP Configuration Guide:** [`docs/LDAP_CONFIGURATION.md`](LDAP_CONFIGURATION.md)
- **Environment Variables:** [`.env.ldap.example`](../.env.ldap.example)
- **Kubernetes Config:** [`k8s/ldap-config.yaml`](../k8s/ldap-config.yaml)
- **Backend LDAP Service:** [`internal/services/sso/sso.go`](../../internal/services/sso/sso.go)
- **Auth Handler:** [`internal/api/handlers/auth.go`](../../internal/api/handlers/auth.go)
- **Frontend Login:** [`src/pages/LoginPage.tsx`](../src/pages/LoginPage.tsx)

## Appendix: Quick Commands Reference

```bash
# View LDAP config
kubectl get configmap sre-command-center-ldap-config -o yaml

# View LDAP secret (base64 encoded)
kubectl get secret sre-command-center-ldap-secret -o yaml

# Test LDAP from backend pod
kubectl exec -it $(kubectl get pods -l component=backend -o jsonpath='{.items[0].metadata.name}') -- sh

# Stream backend logs
kubectl logs -f -l component=backend | grep -i ldap

# Check recent LDAP logins
kubectl exec -it postgres-pod -- psql -U postgres -d alerthub \
  -c "SELECT * FROM audit_logs WHERE action LIKE '%ldap%' ORDER BY created_at DESC LIMIT 10;"

# Restart backend after config changes
kubectl rollout restart deployment/sre-command-center-backend

# Scale backend replicas
kubectl scale deployment/sre-command-center-backend --replicas=3
```
