# AlertHub Operations Runbook

Operational reference for the AlertHub AIOps platform running in the `aileron` namespace on `example-cluster`.

---

## Table of Contents

1. [Service Topology](#service-topology)
2. [Health Checks and Monitoring](#health-checks-and-monitoring)
3. [Common Issues and Fixes](#common-issues-and-fixes)
4. [Log Patterns Reference](#log-patterns-reference)
5. [Database Diagnostic Queries](#database-diagnostic-queries)
6. [CI/CD Troubleshooting](#cicd-troubleshooting)
7. [Key Environment Variables](#key-environment-variables)
8. [Cluster Context and Access](#cluster-context-and-access)

---

## Service Topology

```
Internet / Apple Corp Network
        │
        ▼
aileron.example.com  (NGINX Ingress, TLS)
        │
        ▼
┌───────────────────────────────────────────────────────────────┐
│                    aileron namespace                  │
│                                                               │
│  alerthub-frontend (2 replicas, :80)                          │
│      └─► React/Vite SPA served by NGINX                       │
│                                                               │
│  alerthub-backend (2 replicas, :8080)                         │
│      ├─► Webhook ingestion (DT/Prom/Grafana/Splunk)           │
│      ├─► Alert pipeline (staged: FAST/TOPO/FULL)              │
│      ├─► CACIE correlation engine                             │
│      ├─► Policy engine (intelligence_policies)                │
│      ├─► Postmortem service                                   │
│      ├─► Gate hooks (remediations_pending)                    │
│      ├─► MCP server (/api/v1/mcp)                             │
│      ├─► KubeSense Kafka consumer (16 topics)                 │
│      └─► WebSocket / SSE broadcast                            │
│                                                               │
│  oie (1 replica, :8081)                                       │
│      ├─► Kafka consumer: alerthub.incidents                   │
│      ├─► Evidence DAG (16 fetchers)                           │
│      ├─► Hypothesis engine + LLM narrator                     │
│      └─► Writeback: POST alerthub-backend/rca-callback        │
│                                                               │
│  alerthub-bert-service (1 replica, :8766)                     │
│      └─► BERT embeddings (all-MiniLM-L6-v2, 384-dim)         │
│                                                               │
│  ollama (1 replica, :11434, GPU nodeSelector)                 │
│      └─► qwen2.5:3b (inline RCA, postmortem, OIE narrator)   │
│                                                               │
│  rca-orchestrator (1 replica, :8006) [legacy]                 │
│      └─► FastAPI deep RCA (multi-round tool calls)            │
│                                                               │
│  kubesense-core (in aileron-agent ns, :8080)                        │
│      └─► KubeSense API, proxied at /api/v1/kubesense/*        │
│                                                               │
│  ─── Storage ──────────────────────────────────────────────── │
│  postgres-primary  (:5432, StatefulSet, pgvector)             │
│  neo4j-0           (:7687 bolt, :7474 HTTP, StatefulSet)      │
│  redis-cluster     (:6379, 3-replica StatefulSet)             │
│  weaviate          (:8080, 1 replica)                         │
│                                                               │
│  ─── Streaming ────────────────────────────────────────────── │
│  kafka (3 brokers + ZooKeeper, Strimzi)                       │
│      Topics: raw-alerts, normalized-alerts, correlation-      │
│              results, alerthub.incidents, kubesense.*,        │
│              oie.investigations                               │
└───────────────────────────────────────────────────────────────┘
```

---

## Health Checks and Monitoring

### Readiness and Liveness Probes

```bash
# Backend liveness
curl https://aileron.example.com/health

# Backend readiness (checks DB + Kafka)
curl https://aileron.example.com/ready

# Detailed component health (DB, Kafka, Neo4j, Redis)
curl https://aileron.example.com/health/detailed

# Prometheus metrics
curl https://aileron.example.com/metrics
```

### Checking Pod Status

```bash
# Set cluster context first (see Cluster Context section)
kubectl get pods -n aileron

# Expected READY states:
# alerthub-backend-*      2/2   Running
# alerthub-frontend-*     2/2   Running
# alerthub-bert-service-* 1/1   Running
# ollama-*                1/1   Running
# rca-orchestrator-*      1/1   Running
# postgres-primary-0      1/1   Running
# neo4j-0                 1/1   Running
# redis-cluster-*         1/1   Running (3 pods)
# kafka-*                 1/1   Running (3 brokers)
```

### ArgoCD Application Health

```bash
# Check all four ArgoCD apps
kubectl get application -n argocd | grep -E "alert-engine|sre-command|alerthub"

# Expected: Synced / Healthy for all four
# alert-engine         Synced   Healthy
# sre-command-center   Synced   Healthy
# alerthub-bert        Synced   Healthy
# alerthub-infra       Synced   Healthy
```

### Intelligence Stats

The `/api/v1/intelligence/stats` endpoint returns a summary of intelligence activity. A healthy response shows non-zero counts in the past 24 hours:

```bash
curl -H "Authorization: Bearer <token>" \
  https://aileron.example.com/api/v1/intelligence/stats
```

---

## Common Issues and Fixes

### Backend CrashLoopBackOff

**Symptom:** `alerthub-backend` pod in `CrashLoopBackOff`. Logs show `duplicate route` or `router: path conflict`.

**Cause:** Gin router conflict. Two handlers registered for the same HTTP method + path. This is a startup panic that prevents the process from initializing.

**Fix:**
1. Check recent commits to `cmd/main.go` for duplicate route registrations.
2. Look for routes that use both a fixed path and a wildcard: e.g., `/api/v1/kubesense/clusters` registered twice, or a wildcard `/*path` conflicting with a fixed sub-path.
3. Fix the conflict in `main.go` — the wildcard proxy catch-all must be registered last and must not overlap with specific sub-routes.
4. Push the fix; BuildKit + ArgoCD will redeploy automatically.

```bash
# View crash logs
kubectl logs -n aileron deployment/alerthub-backend --previous
```

### OIE Stuck in `investigating` Status

**Symptom:** Incidents remain in `rca_status='investigating'` indefinitely. No RCA completion in the UI.

**Cause / Fix — check in order:**

1. **OIE_ALERTHUB_BASE_URL not set or wrong.** OIE cannot write back its result to AlertHub.
   ```bash
   kubectl exec -n aileron deployment/oie -- env | grep OIE_ALERTHUB_BASE_URL
   # Expected: OIE_ALERTHUB_BASE_URL=http://alerthub-backend.aileron.svc.cluster.local:8080
   ```
   Fix: Set the env var in the OIE deployment and restart.

2. **Stale sweep.** The backend runs `sweepStuckRCAInvestigations()` every hour (and at startup). Incidents stuck for >15 minutes are automatically promoted to `completed` (if confidence > 0.5) or `failed`.
   ```bash
   kubectl logs -n aileron deployment/alerthub-backend | grep stale-sweep
   # Should see: [stale-sweep] marked N incident(s) failed (no confident RCA in 15min)
   ```

3. **OIE pod not running.**
   ```bash
   kubectl get pod -n aileron -l app=oie
   kubectl logs -n aileron deployment/oie | tail -50
   ```

4. **Kafka consumer lag.** OIE may be behind on the `alerthub.incidents` topic.
   ```bash
   kubectl exec -n aileron kafka-0 -- \
     kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
     --describe --group oie-investigation-consumer
   ```

### KubeSense Tabs Empty

**Symptom:** KubeSense page in the UI shows empty tabs (Violations, Forecasts, Chaos, APM, Investigations).

**Check 1: DB tables populated?**

```sql
SELECT COUNT(*), MAX(occurred_at) FROM kubesense_health_events;
SELECT COUNT(*), MAX(occurred_at) FROM kubesense_config_violations;
SELECT COUNT(*), MAX(created_at)  FROM kubesense_forecasts;
SELECT COUNT(*), MAX(sampled_at)  FROM kubesense_apm_signals;
SELECT COUNT(*), MAX(completed_at) FROM kubesense_investigation_results;
```

If counts are 0 or timestamps are stale (>2h), the Kafka consumer is not running or no events are being published.

**Check 2: KubeSense consumer running?**

```bash
kubectl logs -n aileron deployment/alerthub-backend | grep -i kubesense
# Expected on startup: KubeSense consumer ready: 16 topics brokers=...
# If you see: KubeSense consumer unavailable ... KubeSense intelligence disabled
# → Kafka is unreachable or the broker list is wrong
```

**Check 3: kubesense-core reachable?**

The `/api/v1/kubesense/clusters`, `/api/v1/kubesense/topology`, and proxy tabs call `kubesense-core` in the `aileron-agent` namespace.

```bash
kubectl exec -n aileron deployment/alerthub-backend -- \
  wget -qO- http://kubesense-core.aileron-agent.svc.cluster.local:8080/health
# Expected: {"status":"ok"}
# If connection refused: kubesense-core is down or not deployed in aileron-agent ns
```

Set `KUBESENSE_CORE_URL` in the backend env to override the default.

**Check 4: DB-backed tabs use different endpoints.** The tabs for Violations/Forecasts/Chaos/Health/APM query `/api/v1/kubesense/db/*` which reads the `kubesense_*` tables directly — these do not depend on `kubesense-core`.

### Stats Showing 0

**Symptom:** Intelligence Stats panel shows 0 incidents/alerts/investigations for all counters.

**Cause:** The `intelligence_stats_handler.go` queries use a 24-hour time window. If the system has been idle or clocks are skewed, counts may appear as 0.

**Diagnostics:**

```sql
-- Check recent incident count regardless of time window
SELECT COUNT(*), MAX(created_at) FROM incidents;

-- Check if query is returning data for the expected window
SELECT COUNT(*) FROM incidents
WHERE created_at > NOW() - INTERVAL '24 hours';
```

If incidents exist but stats show 0, verify the backend is querying the correct schema and that `NOW()` returns the expected timestamp (clock skew in the pod).

### RCA Always Failed

**Symptom:** All incidents end with `rca_status='failed'`. No root cause is populated.

**Cause 1: CACIE tautological guard.** CACIE (`causal_inference_engine.go`) rejects hypotheses with confidence >= 0.65 as "tautological" (the hypothesis is trivially true given the alert itself). This was tightened from the initial 0.80 threshold. If alerts are very specific (DT rootCauseEntity present), CACIE may over-suppress.

Check CACIE logs:
```bash
kubectl logs -n aileron deployment/alerthub-backend | grep -i "cacie\|tautological"
```

**Cause 2: Ollama unreachable.** Inline RCA enrichment fails silently; incidents get `rca_status='failed'` if neither CACIE nor OIE produces a result.

```bash
kubectl exec -n aileron deployment/alerthub-backend -- \
  wget -qO- http://ollama.aileron.svc.cluster.local:11434/api/tags
# Should list available models including qwen2.5:3b
```

**Cause 3: OIE writeback failing.** See "OIE Stuck" above.

**Cause 4: Policy engine skip_rca.** Check if a `skip_rca` policy is matching.

```sql
SELECT * FROM intelligence_policies WHERE policy_type = 'skip_rca' AND enabled = true;
```

### `api_request_log` Table Missing

**Symptom:** Backend logs show `relation "api_request_log" does not exist` at startup.

**Cause:** Migration for `api_request_log` was not applied (it was added as an idempotent migration in `migrations.go`).

**Fix:** The migration is idempotent — it will apply automatically on the next backend restart via `CREATE TABLE IF NOT EXISTS`. If the table is still missing after a restart:

```sql
-- Check migration status
SELECT * FROM schema_migrations WHERE version LIKE '%api_request%';

-- Apply manually if needed
CREATE TABLE IF NOT EXISTS api_request_log (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID,
    method       TEXT,
    path         TEXT,
    status_code  INT,
    duration_ms  INT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);
```

### Alerts Not Correlating / All Creating New Incidents

**Symptom:** Every incoming alert creates a new incident rather than merging into an existing one.

**Diagnostics:**

```bash
# Check correlation scores in logs
kubectl logs -n aileron deployment/alerthub-backend | grep -E "score|threshold|correlation"

# Check if BERT service is reachable (semantic correlation)
kubectl exec -n aileron deployment/alerthub-backend -- \
  wget -qO- http://alerthub-bert-service.aileron.svc.cluster.local:8766/health

# Check Weaviate
kubectl exec -n aileron deployment/alerthub-backend -- \
  wget -qO- http://weaviate.aileron.svc.cluster.local:8080/v1/.well-known/ready
```

Check correlation threshold:
```bash
kubectl exec -n aileron deployment/alerthub-backend -- env | grep CORRELATION_THRESHOLD
# Default 0.75 — alerts must score ≥0.75 to merge
```

For Dynatrace alerts, verify the `rootCauseEntity` tag is present in the webhook payload — it triggers the deterministic fast path and bypasses scoring.

### Resolved Alerts Not Closing Incidents

**Symptom:** Incidents stay `open` after all their alerts are resolved.

**Cause:** Three known bugs were fixed (2026-05-28):
1. Prometheus/Grafana webhook missing resolved-state guard
2. UPSERT not setting `resolved_at` on the alert record
3. A resolved-to-resolved re-open bug

**Verify the stale sweep is running:**

```bash
kubectl logs -n aileron deployment/alerthub-backend | grep "stale-sweep"
```

The sweep runs every hour and resolves incidents with all alerts resolved. Manual resolution:

```sql
UPDATE incidents
SET status = 'resolved', resolved_at = NOW()
WHERE id = '<incident-uuid>'
  AND NOT EXISTS (
    SELECT 1 FROM alerts WHERE incident_id = '<incident-uuid>' AND status != 'resolved'
  );
```

---

## Log Patterns Reference

| Log prefix | What it means |
|---|---|
| `[stale-sweep] auto-resolving N stale fp: alerts` | Fingerprint alerts older than 4h are being closed |
| `[stale-sweep] auto-resolving N stale open alerts` | Alerts with no update in 24h are being closed |
| `[stale-sweep] marked N incident(s) failed (no confident RCA)` | OIE didn't complete in 15min — incidents marked failed |
| `[stale-sweep] promoted N incident(s) from investigating to completed` | CACIE found confident result; OIE callback never arrived |
| `KubeSense consumer ready: 16 topics` | KubeSense Kafka consumer initialized successfully |
| `KubeSense consumer unavailable ... KubeSense intelligence disabled` | Kafka unreachable — KubeSense tabs will be empty |
| `KubeSense: investigation result incident=... grade=... confidence=...` | KubeSense RCA result merged into incident |
| `KubeSense drift: ns/kind/name drift_type=... severity=...` | GitOps drift detected and stored |
| `KubeSense chaos score: cluster=... score=.../100 grade=...` | Chaos readiness score received |
| `LLMEnricher initialized: default=... triage=... rca=... narrative=...` | Per-role model routing confirmed at startup |
| `Postmortem generated for incident ... (duration=... by=llm\|template)` | Postmortem auto-generated on incident resolution |
| `[pipeline] incident created: id=... alerts=N strategy=...` | New incident from correlation engine |
| `[cacie] tautological hypothesis suppressed` | CACIE confidence ≥ 0.65 guard fired — hypothesis was obvious |
| `policy engine: load failed` | DB query for `intelligence_policies` failed; cached list used |

---

## Database Diagnostic Queries

### Check Active Incidents and RCA Status

```sql
SELECT status, rca_status, COUNT(*) AS count
FROM incidents
WHERE created_at > NOW() - INTERVAL '24 hours'
GROUP BY status, rca_status
ORDER BY count DESC;
```

### Find Incidents Stuck in `investigating`

```sql
SELECT id, incident_number, title, rca_status, rca_confidence, updated_at
FROM incidents
WHERE rca_status = 'investigating'
  AND updated_at < NOW() - INTERVAL '15 minutes'
ORDER BY updated_at ASC;
```

### Recent Alert Volume by Source

```sql
SELECT source, status, COUNT(*) AS count
FROM alerts
WHERE created_at > NOW() - INTERVAL '1 hour'
GROUP BY source, status
ORDER BY count DESC;
```

### KubeSense Signal Health

```sql
-- Last event received per topic category
SELECT event_type, COUNT(*), MAX(occurred_at) AS latest
FROM kubesense_health_events
GROUP BY event_type
ORDER BY latest DESC
LIMIT 20;

-- Recent config violations
SELECT rule_id, severity, namespace, resource_name, occurred_at
FROM kubesense_config_violations
ORDER BY occurred_at DESC
LIMIT 10;

-- Upcoming resource exhaustion forecasts
SELECT target, namespace, resource_name, predicted_breach, model_confidence
FROM kubesense_forecasts
WHERE predicted_breach > NOW()
ORDER BY predicted_breach ASC
LIMIT 10;
```

### OIE Investigation Status

```sql
-- OIE investigation outcomes in the last 24 hours
SELECT status, COUNT(*) AS count
FROM rca_investigations
WHERE created_at > NOW() - INTERVAL '24 hours'
GROUP BY status;

-- Recent RCA decisions with confidence
SELECT i.incident_number, d.decision_type, d.root_cause_domain,
       d.final_confidence, d.root_cause_description, d.created_at
FROM rca_decisions d
JOIN incidents i ON i.id = d.incident_id
ORDER BY d.created_at DESC
LIMIT 10;
```

### Intelligence Policies

```sql
-- All active policies sorted by priority
SELECT name, policy_type, condition_json, priority, enabled
FROM intelligence_policies
WHERE enabled = true
ORDER BY priority DESC;
```

### Pending Remediations Requiring Approval

```sql
SELECT r.id, i.incident_number, r.proposed_action, r.risk_level,
       r.proposed_by, r.created_at
FROM remediations_pending r
JOIN incidents i ON i.id = r.incident_id
WHERE r.status = 'proposed'
ORDER BY r.created_at DESC;
```

### Postmortem Coverage

```sql
-- Resolved incidents without postmortems in the last 7 days
SELECT i.incident_number, i.title, i.resolved_at
FROM incidents i
WHERE i.status = 'resolved'
  AND i.resolved_at > NOW() - INTERVAL '7 days'
  AND NOT EXISTS (
    SELECT 1 FROM post_mortems p WHERE p.incident_id = i.id
  )
ORDER BY i.resolved_at DESC;
```

---

## CI/CD Troubleshooting

### Build Not Triggered After Push

The CI/CD pipeline is fully automated: `git push` → commit-lint + secret-scan → BuildKit → Registry → ArgoCD.

**Check GitHub Actions:**

```bash
gh run list --repo interactive-service-delivery/alert-engine --limit 5
gh run view <run-id>
```

**Common failures:**

| Failure | Fix |
|---|---|
| `commit-lint: conventional commit format required` | Use `feat:`, `fix:`, `chore:`, `docs:` prefix in commit message |
| `secret-scan: potential secret detected` | Remove the secret. Do not commit `.env` files. |
| `branch-naming: invalid branch name` | Branch must match `feat/*`, `fix/*`, `chore/*`, `hotfix/*` |
| BuildKit: `npm registry unreachable` | Retry; Apple npm proxy may be transient. Or add `--registry https://registry.npmjs.org` to package.json |
| BuildKit: `Go module proxy timeout` | Set `GONOSUMCHECK=*` and `GOFLAGS=-mod=mod` in BuildKit env |
| Cosign: `Whisper auth: 401` | Whisper namespace token expired. Rotate `alerthub-whisper-cert` secret |

### ArgoCD App Not Syncing

```bash
# Check sync status
kubectl get application -n argocd alert-engine -o yaml | grep -A5 'status:'

# Force sync
kubectl patch application -n argocd alert-engine \
  --type merge -p '{"operation": {"sync": {"revision": "HEAD"}}}'
```

**Known issue: ArgoCD stuck sync goroutine.** If ArgoCD shows `Running` but never completes a sync, restart the ArgoCD application controller:

```bash
kubectl rollout restart deployment/argocd-application-controller -n argocd
```

### Checking Image Tags

The `helm-revision-v1.0` CMP plugin derives `imageTag` from `git log -- <service-paths>`. If a service directory path changed, the tag may not update.

```bash
# Check what imageTag ArgoCD is using
kubectl get application -n argocd alert-engine -o jsonpath='{.status.sync.comparedTo.source.helm.values}'
```

---

## Key Environment Variables

### alerthub-backend

| Variable | Description |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN |
| `NEO4J_URI` | Neo4j Bolt URI (`bolt://neo4j:7687`) |
| `REDIS_ADDR` | Redis address |
| `KAFKA_BROKERS` | Comma-separated Kafka brokers |
| `BERT_SERVICE_URL` | BERT embedding service (`http://alerthub-bert-service:8766`) |
| `OLLAMA_URL` | Ollama base URL (`http://ollama:11434`) |
| `LLM_MODEL` | Default LLM model (default `qwen2.5:3b`) |
| `LLM_TRIAGE_MODEL` | Fast model for alert triage (falls back to `LLM_MODEL`) |
| `LLM_RCA_MODEL` | Quality model for RCA/postmortem (falls back to `LLM_MODEL`) |
| `LLM_NARRATIVE_MODEL` | Narrative generation model (falls back to `LLM_MODEL`) |
| `INTELLIGENCE_SLACK_WEBHOOK` | Slack webhook for remediation gate notifications |
| `KUBESENSE_CORE_URL` | KubeSense API proxy target (default `http://kubesense-core.aileron-agent.svc.cluster.local:8080`) |
| `CORRELATION_THRESHOLD` | Score threshold to merge incidents (default `0.75`) |
| `TOPOLOGY_DOMINANCE_THRESHOLD` | Score for deterministic topology override (default `0.60`) |
| `OIDC_CLIENT_ID` | IDMS OAuth2 client ID |
| `OIDC_CLIENT_SECRET` | IDMS OAuth2 client secret |
| `JWT_SECRET` | HMAC secret for JWT signing |
| `INTERNAL_SERVICE_TOKEN` | Token for RCA callback from rca-orchestrator |

### OIE Service

| Variable | Description |
|---|---|
| `OIE_DATABASE_URL` | PostgreSQL DSN (same DB as backend) |
| `OIE_KAFKA_BROKERS` | Kafka broker list |
| `OIE_ALERTHUB_BASE_URL` | AlertHub base URL for RCA writeback. **Critical — must be set for RCA to work.** |
| `OIE_OLLAMA_BASE_URL` | Ollama endpoint |
| `OIE_OLLAMA_MODEL_TRIAGE` | Triage model |
| `OIE_OLLAMA_MODEL_RCA` | RCA synthesis model |
| `OIE_OLLAMA_MODEL_NARRATIVE` | Narrative model |
| `OIE_EIRS_BASE_URL` | EIRS entity resolution service |
| `OIE_OKG_BASE_URL` | OKG change-correlation service |
| `NETAPP_CLUSTERS` | JSON array of NetApp cluster configs |
| `NETAPP_USER` | NetApp ONTAP user (default `harvest-user`) |
| `NETAPP_PASSWORD` | NetApp ONTAP password |
| `OIE_KUBECONFIGS_DIR` | Directory with per-cluster kubeconfig files (default `/etc/kubeconfigs`) |
| `OIE_MAX_CONCURRENT_INVESTIGATIONS` | Max parallel investigations (default `20`) |
| `OIE_INVESTIGATION_TIME_BUDGET_MS` | Time budget per investigation in ms (default `45000`) |
| `OIE_AUTO_INVESTIGATE_SEVERITIES` | Auto-investigate these severities (default `critical,high`) |

---

## Cluster Context and Access

### Setting Cluster Context

```bash
# List available contexts
kubectl config get-contexts

# Switch to example-cluster
kubectl config use-context oidc02@example-cluster-01

# Verify
kubectl config current-context
# Expected: oidc02@example-cluster-01
```

### Namespace

All AlertHub workloads run in:

```
aileron
```

KubeSense core runs in:

```
aileron-agent
```

ArgoCD itself runs in:

```
argocd
```

### Useful kubectl Shortcuts

```bash
# Set default namespace for the session
kubectl config set-context --current --namespace=aileron

# Tail backend logs
kubectl logs -f deployment/alerthub-backend -n aileron --tail=100

# Tail OIE logs
kubectl logs -f deployment/oie -n aileron --tail=100

# Exec into backend for ad-hoc diagnostics
kubectl exec -it deployment/alerthub-backend -n aileron -- /bin/sh

# Port-forward PostgreSQL for direct queries
kubectl port-forward -n aileron svc/postgres-primary 5432:5432

# Port-forward Neo4j browser
kubectl port-forward -n aileron svc/neo4j 7474:7474 7687:7687

# Port-forward Ollama for local model testing
kubectl port-forward -n aileron svc/ollama 11434:11434

# Rolling restart backend (picks up ConfigMap/Secret changes)
kubectl rollout restart deployment/alerthub-backend -n aileron

# Rolling restart OIE
kubectl rollout restart deployment/oie -n aileron
```

### Secrets

| Secret | Namespace | Contents |
|---|---|---|
| `alerthub-secrets` | `aileron` | DB, JWT, OAuth credentials, Dynatrace token |
| `alerthub-dsldap-credentials` | `aileron` | DS-LDAP bind DN + password |
| `alerthub-hcl-credentials` | `aileron` | HCL API credentials |
| `infrastructure-credentials` | `aileron` | CloudStack API key/secret, Dynatrace API token |
| `alerthub-whisper-cert` | `aileron` | Cosign Whisper Apple corp CA cert for image signing |

```bash
# View secret keys (not values)
kubectl get secret alerthub-secrets -n aileron -o jsonpath='{.data}' | jq 'keys'
```

### Live URL

```
https://aileron.example.com
```

### Slack Channel

```
#help-interactive-sre
```
