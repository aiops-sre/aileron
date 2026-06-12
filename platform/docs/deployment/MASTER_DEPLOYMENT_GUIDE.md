# SRE Command Center — Deployment Guide

> Build, push, and deploy both services to the Apple internal Kubernetes cluster.  
> Version: v1.0.0 | Cluster: example-cluster | Namespace: aileron

---

## Quick Reference

```bash
# Context
kubectl config use-context example-cluster

# Registry
REGISTRY=ghcr.io/aileron-platform/aileron-admins

# Current images
BE_IMAGE=$REGISTRY/sre-command-center-be
FE_IMAGE=$REGISTRY/sre-command-center-fe
```

---

## Prerequisites

| Tool | Required Version | Notes |
|---|---|---|
| Go | 1.21+ | Backend build |
| Node.js | 18+ | Frontend build |
| Docker | 24+ | Image build |
| kubectl | 1.28+ | K8s operations |
| Docker Desktop / Colima | Any | Must be running for builds |

**Critical:** Always build with `--platform linux/amd64`. Cluster nodes are amd64. Mac M-series without this flag produces arm64 images that will crash on the cluster.

---

## Full Build & Deploy Procedure

### Step 1 — Build the Go Binary

```bash
cd /path/to/alerthub-enterprise

GOOS=linux GOARCH=amd64 go build -o alerthub_linux_amd64 ./cmd/...
```

This produces a statically-linked Linux binary. The Dockerfile copies this pre-built binary into the image (no in-container build step).

### Step 2 — Build the Backend Image

```bash
TAG=v1.0.1   # increment from previous

docker build \
  --platform linux/amd64 \
  -f docker/Dockerfile.backend-update \
  -t ghcr.io/aileron-platform/aileron-admins/sre-command-center-be:$TAG \
  .
```

### Step 3 — Build the Frontend

```bash
cd frontend/alerthub-frontend

npm run build   # produces dist/ directory
```

### Step 4 — Build the Frontend Image

```bash
# Still in frontend/alerthub-frontend/

TAG=v1.0.1

docker build \
  --platform linux/amd64 \
  -t ghcr.io/aileron-platform/aileron-admins/sre-command-center-fe:$TAG \
  .
```

### Step 5 — Push Both Images

```bash
docker push ghcr.io/aileron-platform/aileron-admins/sre-command-center-be:$TAG
docker push ghcr.io/aileron-platform/aileron-admins/sre-command-center-fe:$TAG
```

If push fails with auth error: `docker login ghcr.io/aileron-platform`

### Step 6 — Deploy to Cluster

```bash
kubectl config use-context example-cluster

# Deploy backend
kubectl -n aileron set image deployment/alerthub-backend \
  alerthub-backend=ghcr.io/aileron-platform/aileron-admins/sre-command-center-be:$TAG

# Deploy frontend
kubectl -n aileron set image deployment/frontend \
  frontend=ghcr.io/aileron-platform/aileron-admins/sre-command-center-fe:$TAG

# Watch rollout (waits for completion)
kubectl -n aileron rollout status \
  deployment/alerthub-backend \
  deployment/frontend
```

---

## Cluster Reference

### Namespace: `aileron`

| Resource | Name | Description |
|---|---|---|
| Deployment | `alerthub-backend` | Go API server (2 replicas) |
| Deployment | `frontend` | Nginx + React SPA (2 replicas) |
| Service | `alerthub-backend` | ClusterIP, port 8080 |
| Service | `frontend` | ClusterIP, port 80 |
| Ingress | `alerthub-ingress` | Routes to frontend + /api to backend |
| Secret | `alerthub-secrets` | App credentials (postgres, JWT, redis, neo4j) |
| Secret | `alerthub-dsldap-credentials` | LDAP_APP_ID, LDAP_APP_PASSWORD |
| Secret | `jfrog-dockers-access` | Image pull secret for JFrog registry |

### Useful kubectl Commands

```bash
# Check pod status
kubectl -n aileron get pods

# Tail backend logs (all replicas)
kubectl -n aileron logs -l app=alerthub-backend -f

# Exec into backend pod
kubectl -n aileron exec -it \
  $(kubectl -n aileron get pods -l app=alerthub-backend -o name | head -1) \
  -- sh

# Describe backend deployment
kubectl -n aileron describe deployment alerthub-backend

# Rollback backend to previous version
kubectl -n aileron rollout undo deployment/alerthub-backend
```

### Access the PostgreSQL Database

```bash
# Get the running backend pod name
POD=$(kubectl get pods -n aileron --no-headers \
  | grep "alerthub-backend.*Running" | head -1 | awk '{print $1}')

# Run psql
kubectl exec -n aileron $POD -- \
  psql "postgresql://alerthub:pg-AIOps-Secure-2024-Prod@postgres-primary.aileron.svc.cluster.local:5432/alerthub"
```

---

## Environment Variables

Set via the `alerthub-secrets` Kubernetes secret. To update:

```bash
kubectl -n aileron edit secret alerthub-secrets
```

| Variable | Purpose | Example |
|---|---|---|
| `DATABASE_URL` | PostgreSQL DSN | `postgresql://user:pass@host:5432/db` |
| `REDIS_URL` | Redis connection | `redis://:pass@host:6379` |
| `KAFKA_BROKERS` | Broker list | `kafka-0:9092,kafka-1:9092` |
| `JWT_SECRET` | Token signing key | 32+ char random string |
| `MAS_CLIENT_ID` | IdMS OAuth client | From IdMS app registration |
| `MAS_CLIENT_SECRET` | IdMS OAuth secret | From IdMS app registration |
| `MAS_REDIRECT_URL` | OAuth callback | `https://aileron.example.com/auth/mas/callback` |
| `BERT_SERVICE_URL` | BERT embedding service | `http://bert-service:8080` |
| `RCA_ORCHESTRATOR_URL` | RCA service | `http://rca-svc:8006` (leave empty to disable) |
| `NEO4J_URI` | Neo4j bolt | `bolt://neo4j:7687` (optional) |
| `WEAVIATE_URL` | Vector store | `http://weaviate:8080` (optional) |
| `AUTONOMOUS_CORRELATION_URL` | Autonomous AI svc | Optional external correlation service |
| `INSECURE_SKIP_VERIFY` | TLS skip (dev only) | `false` in production |

LDAP credentials are in the separate `alerthub-dsldap-credentials` secret:

| Variable | Purpose |
|---|---|
| `LDAP_APP_ID` | DS LDAP application ID |
| `LDAP_APP_PASSWORD` | DS LDAP application password |

---

## Image Version History

| Version | Date | Changes |
|---|---|---|
| `v1.0.0` | 2026-05-11 | First release under new image names. Correlation fixes (blast radius, timeline, INC- search), full dead code cleanup, demo scripts, docs. |

**Previous image names (archived — no longer used):**
- `ghcr.io/aileron-platform/aileron-admins/alerthub` (backend, up to v3.0.43)
- `ghcr.io/aileron-platform/aileron-admins/alerthub-frontend` (frontend, up to v20260511-search-timeline)

---

## Tagging Convention

```
v<major>.<minor>.<patch>

Patch (v1.0.x): bug fixes, minor tweaks
Minor (v1.x.0): new features, no breaking changes
Major (vX.0.0): breaking API changes, major architectural shifts
```

---

## Troubleshooting

### Pod stuck in ImagePullBackOff
```bash
kubectl -n aileron describe pod <pod-name>
# Check Events section for specific pull error

# Verify image exists in registry
docker manifest inspect ghcr.io/aileron-platform/aileron-admins/sre-command-center-be:v1.0.0

# Verify pull secret is present
kubectl -n aileron get secret jfrog-dockers-access
```

### Rollout stuck (pods not becoming ready)
```bash
kubectl -n aileron rollout status deployment/alerthub-backend
kubectl -n aileron describe pod <new-pod-name>
# Check readiness probe failures in Events

# Rollback if needed
kubectl -n aileron rollout undo deployment/alerthub-backend
```

### Backend CrashLoopBackOff
```bash
# Check logs from crashed container
kubectl -n aileron logs <pod-name> --previous

# Common causes:
# - DATABASE_URL wrong/unreachable
# - Missing secret keys
# - arm64 image built without --platform linux/amd64
```

### Frontend loads but API calls fail (401/403)
```bash
# Check MAS OAuth env vars
kubectl -n aileron exec <backend-pod> -- env | grep MAS

# Verify JWT_SECRET is set
kubectl -n aileron exec <backend-pod> -- env | grep JWT
```

### Alerts not correlating
```bash
# Watch live pipeline decisions
kubectl logs -n aileron -l app=alerthub-backend -f \
  | grep -E "🎯 RCE|✅ Created|🔗 merged|❌ Failed|📥 Dynatrace|🆕 Creating"

# Check Kafka connectivity
kubectl -n aileron exec <backend-pod> -- env | grep KAFKA
```
