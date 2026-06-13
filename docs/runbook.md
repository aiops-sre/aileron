# Aileron Operations Runbook

> This runbook covers day-two operations: deployment health checks, diagnosing common issues, Kafka lag investigation, OIE troubleshooting, database tuning, scaling guidance, and backup procedures.

---

## Table of Contents

1. [Post-Deployment Health Checks](#1-post-deployment-health-checks)
2. [Common Issues](#2-common-issues)
3. [Kafka Consumer Lag Investigation](#3-kafka-consumer-lag-investigation)
4. [OIE Investigation Stuck](#4-oie-investigation-stuck)
5. [Database Performance](#5-database-performance)
6. [Scaling Guide](#6-scaling-guide)
7. [Backup and Recovery](#7-backup-and-recovery)

---

## 1. Post-Deployment Health Checks

Run these checks after every deployment to verify the system is healthy before declaring the deploy complete.

### 1.1 Pod Status

```bash
# AlertHub namespace
kubectl get pods -n aileron
# All pods should show Running 1/1 (or 2/2 for multi-container)
# No CrashLoopBackOff, ImagePullBackOff, or Pending

# KubeSense namespace
kubectl get pods -n aileron-agent
```

Expected output:
```
NAME                              READY   STATUS    RESTARTS
aileron-platform-xxx              1/1     Running   0
aileron-platform-yyy              1/1     Running   0
aileron-frontend-xxx              1/1     Running   0
aileron-oie-xxx                   1/1     Running   0
aileron-bert-service-xxx          1/1     Running   0
ollama-xxx                        1/1     Running   0
```

### 1.2 HTTP Health Endpoints

```bash
# Detailed health — checks all downstream dependencies
curl -s https://aileron.example.com/health/detailed | jq .

# Expected:
{
  "status": "healthy",
  "postgres": "connected",
  "kafka": "connected",
  "neo4j": "connected",
  "redis": "connected",
  "bert_service": "reachable",
  "ollama": "reachable"
}

# Readiness (used by K8s readiness probe)
curl -s https://aileron.example.com/ready
# Expected: 200 OK
```

### 1.3 Kafka Consumer Groups

```bash
# AlertHub pipeline consumer — should have zero lag
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group alerthub-pipeline-consumer

# OIE investigation consumer — may have brief lag after deploy, should drain
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group oie-investigation-consumer
```

Healthy: `LAG = 0` on all partitions within 2 minutes of deploy.

### 1.4 Correlation Engine

```bash
# KubeSense correlation engine status
kubectl exec -n aileron-agent deploy/kubesense-api -- \
  wget -qO- http://localhost:8080/api/v1/correlation/status | jq .

# Healthy indicators:
# "online": true
# "buffer_len": > 0 (events are flowing)
# "rule_count": >= 8 (built-in rules loaded)
```

### 1.5 OIE Narrative Quality

```bash
# Check that OIE is using the correct LLM model (not falling back to template)
kubectl logs -n aileron deploy/aileron-oie --since=10m | grep "narrative_model\|model_used"
# Should show: narrative_model=qwen2.5:3b

# Check for recent successful investigations
psql $DATABASE_URL -c "
SELECT status, COUNT(*), AVG(confidence)
FROM rca_investigations
WHERE created_at > NOW() - INTERVAL '1 hour'
GROUP BY status;"
# Healthy: status=complete, avg confidence > 0.65
```

### 1.6 Ollama Model Availability

```bash
kubectl exec -n aileron deploy/ollama -- ollama list
# Expected output includes:
#   qwen2.5:3b        (used by OIE narrator and inline pipeline)
#   nomic-embed-text  (used for pgvector semantic similarity)
```

---

## 2. Common Issues

| Symptom | Cause | Fix |
|---|---|---|
| OIE produces "template" narratives for all incidents | Wrong Ollama model configured — `qwen2.5:7b` specified but only `3b` is loaded | Set `OIE_OLLAMA_MODEL_NARRATIVE=qwen2.5:3b` and `OIE_OLLAMA_MODEL_RCA=qwen2.5:3b` in OIE deployment |
| `/health/detailed` returns `bert_service: unreachable` | BERT service pod in CrashLoopBackOff | Check `kubectl logs -n aileron deploy/aileron-bert-service`; model download at startup may have failed; restart pod |
| KubeSense `/db/health` endpoint returns 500 or times out | `ORDER BY occurred_at::text` cast causing full table scan on `kubesense_health_events` (3M+ rows) | Apply missing index: `CREATE INDEX CONCURRENTLY idx_ks_he_cluster_occurred ON kubesense_health_events(cluster_id, occurred_at DESC)` |
| Correlation buffer shows 0 events | Buffer feeder goroutine not running | Check `kubectl logs -n aileron-agent deploy/kubesense-api \| grep "buffer-feeder"` — should print "fed N new events" every 30s; restart pod if silent |
| Incidents being created for each alert (no correlation) | BERT service not reachable — semantic strategy scoring 0 | Verify `BERT_SERVICE_URL` env var; check BERT pod logs for startup errors |
| Duplicate incidents created | Kafka partition rebalance during high-throughput period caused concurrent processing | The 17-point dedup cascade handles this in normal cases; check `idx_alerts_source_id` unique index is present |
| `ImagePullBackOff` on newly deployed pods | Build completed after pod was scheduled — stale image tag | Delete the failing pod: `kubectl delete pod -n aileron <pod-name>`; K8s will recreate with fresh image |
| Neo4j topology queries return empty results | Neo4j pod restarted without persistent storage — graph was lost | Trigger topology resync from AlertHub: `POST /api/v1/topology/refresh` or restart kubesense-agent to re-publish topology events |
| `context deadline exceeded` in OIE logs | OIE 45s budget exceeded — evidence fetchers taking too long | Check Neo4j query performance: `CALL db.listQueries()` for slow queries; add indexes if missing |
| Alert storm: 1000+ alerts in 60 seconds | FAST PATH channel (cap 10k) may fill and drop non-critical alerts | Check `[StagedPipeline] DROPPED` log lines; scale up `aileron-platform` replicas; check Kafka partition count |
| WebSocket connections drop every 30 seconds | Load balancer idle timeout too short | Set LB idle timeout to 300s+ (default nginx: `proxy_read_timeout 300s`) |
| `rca_status = failed` on all incidents | OIE cannot reach AlertHub topology endpoint | Verify `OIE_ALERTHUB_BASE_URL` env var; check service-to-service token `INTERNAL_SERVICE_TOKEN` is set |
| Postmortem generation fails | `rca_confidence` below 0.60 threshold for LLM postmortem | This is expected — deterministic template is used instead. If always below threshold, check OIE investigation quality |
| BERT embeddings returning wrong results | BERT service restarted with different model | Model must remain `all-MiniLM-L6-v2`; check environment variable `MODEL_NAME` in BERT deployment |
| Auth loop — users redirected repeatedly | `window.location.href` redirect race on React re-render | Fixed in frontend v3.0.38+; ensure latest frontend image is deployed |
| Alerts resolved but incidents stay open | `resolved_at` UPSERT not firing for specific source | Source normalizer may not be mapping `status=resolved` correctly; check normalizer for that source |

---

## 3. Kafka Consumer Lag Investigation

### 3.1 Check All Consumer Groups

```bash
# List all consumer groups
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 --list

# Check lag for all groups at once
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --all-groups
```

### 3.2 AlertHub Pipeline Consumer Lag

The pipeline consumer (`alerthub-pipeline-consumer`) should have zero or near-zero lag under normal conditions.

```bash
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group alerthub-pipeline-consumer
```

**If lag is growing:**

1. Check if `aileron-platform` pods are running: `kubectl get pods -n aileron`
2. Check for processing errors: `kubectl logs -n aileron deploy/aileron-platform | grep ERROR`
3. Check PostgreSQL connections: if alerts are inserting slowly, UPSERT contention may be the bottleneck
4. Scale up replicas: Kafka will rebalance partitions automatically
   ```bash
   kubectl scale deployment aileron-platform -n aileron --replicas=4
   ```
5. If lag is from a partition with no consumer (OWNER=none), the consumer group has not yet claimed that partition — wait 30 seconds for rebalance

### 3.3 OIE Investigation Consumer Lag

```bash
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group oie-investigation-consumer
```

Expected: brief lag after alert storms, drains to 0 within a few minutes.

**If lag is not draining:**

1. Check OIE semaphore: `OIE_MAX_CONCURRENT_INVESTIGATIONS` (default 20). If all 20 slots are occupied by stuck investigations, new ones queue up in Kafka.
2. Check for stuck investigations: `SELECT id, status, created_at FROM rca_investigations WHERE status='investigating' AND created_at < NOW() - INTERVAL '20 minutes';`
3. Force-fail stuck investigations:
   ```sql
   UPDATE rca_investigations SET status='failed', updated_at=NOW()
   WHERE status='investigating' AND created_at < NOW() - INTERVAL '20 minutes';
   ```
4. Restart OIE pod to clear the semaphore: `kubectl rollout restart deployment aileron-oie -n aileron`

### 3.4 Reset Consumer Group Offset (emergency)

If a consumer group has consumed bad messages and is stuck:

```bash
# First, stop the consumer (scale down)
kubectl scale deployment aileron-oie -n aileron --replicas=0

# Reset offset to latest (skip the stuck messages)
kubectl exec -n aileron statefulset/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --group oie-investigation-consumer \
  --topic alerthub.incidents \
  --reset-offsets --to-latest --execute

# Restart the consumer
kubectl scale deployment aileron-oie -n aileron --replicas=1
```

---

## 4. OIE Investigation Stuck

### 4.1 Identify Stuck Investigations

```sql
-- Find investigations stuck in 'investigating' state > 15 minutes
SELECT i.id, i.incident_id, i.created_at,
       EXTRACT(EPOCH FROM NOW() - i.created_at)/60 AS minutes_stuck,
       inc.severity, inc.title
FROM rca_investigations i
JOIN incidents inc ON inc.id = i.incident_id
WHERE i.status = 'investigating'
  AND i.created_at < NOW() - INTERVAL '15 minutes'
ORDER BY i.created_at ASC;
```

### 4.2 Diagnose Cause

```bash
# Check OIE pod logs for the stuck investigation
kubectl logs -n aileron deploy/aileron-oie --since=30m | grep -A5 "investigation_id=<UUID>"

# Common causes in logs:
# "topology resolve timeout" → Neo4j is slow
# "evidence fetcher timeout" → One fetcher hanging
# "context canceled" → Investigation was cancelled by shutdown
# "semaphore: context deadline exceeded" → All 20 slots occupied
```

### 4.3 Neo4j Slow Queries

```bash
# Connect to Neo4j
kubectl exec -n aileron statefulset/neo4j-0 -- cypher-shell -u neo4j -p $NEO4J_PASSWORD

# Check slow queries
CALL db.listQueries() YIELD query, elapsedTimeMillis
WHERE elapsedTimeMillis > 5000
RETURN query, elapsedTimeMillis ORDER BY elapsedTimeMillis DESC;

# Kill a specific query
CALL dbms.killQuery('query-id-here');
```

Slow topology queries usually mean missing indexes. Apply:
```cypher
CREATE INDEX entity_id_idx IF NOT EXISTS FOR (n:K8sNode) ON (n.entity_id);
CREATE INDEX entity_id_idx IF NOT EXISTS FOR (n:K8sPod) ON (n.entity_id);
CREATE INDEX entity_id_idx IF NOT EXISTS FOR (n:CloudVM) ON (n.entity_id);
```

### 4.4 OIE KubeSense Fetcher Failing

If the `KubeSense Signals` fetcher is timing out (OIE cannot reach `kubesense-api`):

```bash
# Test connectivity from OIE pod
kubectl exec -n aileron deploy/aileron-oie -- \
  wget -qO- http://kubesense-api.aileron-agent.svc.cluster.local:8080/healthz

# If unreachable, check NetworkPolicy
kubectl get networkpolicy -n aileron-agent
kubectl describe networkpolicy -n aileron-agent allow-alerthub-oie

# Temporary workaround: disable KubeSense fetcher
# Set OIE_DISABLE_KUBESENSE_FETCHER=true in OIE deployment env
```

### 4.5 LLM Hallucination Guard Blocking All Output

If OIE consistently returns `"narrative": "Investigation inconclusive..."` for every incident:

```bash
# Check countGroundingFacts metric
kubectl logs -n aileron deploy/aileron-oie | grep "countGroundingFacts\|grounding_facts"

# If always 0:
# - OIE cannot reach KubeSense or Neo4j → evidence fetchers returning empty
# - Check each fetcher individually by enabling OIE_LOG_EVIDENCE=true
```

---

## 5. Database Performance

### 5.1 Essential Indexes

Run this query to check if critical indexes are present:

```sql
SELECT indexname, tablename
FROM pg_indexes
WHERE tablename IN ('alerts', 'incidents', 'rca_investigations',
                    'kubesense_health_events', 'kubesense_changes')
ORDER BY tablename, indexname;
```

Required indexes (apply if missing):

```sql
-- AlertHub indexes
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_fingerprint
  ON alerts(fingerprint);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_source_id
  ON alerts(source_id) WHERE source_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_incident
  ON alerts(incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_status_created
  ON alerts(status, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_status_created
  ON incidents(status, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_rca_embedding
  ON rca_investigations USING ivfflat (embedding vector_cosine_ops)
  WITH (lists = 100);

-- KubeSense indexes (critical for 17M+ row table)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ks_he_cluster_occurred
  ON kubesense_health_events(cluster_id, occurred_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ks_he_cluster_type
  ON kubesense_health_events(cluster_id, event_type);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ks_he_ns_name
  ON kubesense_health_events(namespace, resource_name)
  WHERE namespace IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ks_changes_cluster_time
  ON kubesense_changes(cluster_id, occurred_at DESC);
```

### 5.2 Identify Slow Queries

```sql
-- Queries running > 5 seconds right now
SELECT pid, now() - pg_stat_activity.query_start AS duration,
       query, state
FROM pg_stat_activity
WHERE (now() - pg_stat_activity.query_start) > INTERVAL '5 seconds'
  AND state = 'active'
ORDER BY duration DESC;

-- Top slow queries from pg_stat_statements (if enabled)
SELECT query, calls, mean_exec_time, total_exec_time
FROM pg_stat_statements
ORDER BY mean_exec_time DESC LIMIT 20;

-- Kill a long-running query
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE pid = <pid_from_above>;
```

### 5.3 Connection Pool Exhaustion

AlertHub uses pgx connection pool. If you see `connection pool exhausted` in logs:

```sql
-- Check current connections
SELECT count(*), state, wait_event_type, wait_event
FROM pg_stat_activity
GROUP BY state, wait_event_type, wait_event
ORDER BY count DESC;

-- Max connections
SHOW max_connections;
-- Aileron default: 200 connections per PostgreSQL instance

-- If > 180 connections in use, increase pool max or add read replica
```

### 5.4 Vacuum and Bloat

`kubesense_health_events` receives heavy INSERT traffic and needs regular autovacuum:

```sql
-- Check autovacuum status
SELECT schemaname, relname, last_autovacuum, last_autoanalyze,
       n_dead_tup, n_live_tup,
       round(n_dead_tup::numeric / NULLIF(n_live_tup, 0) * 100, 2) AS dead_pct
FROM pg_stat_user_tables
WHERE relname IN ('kubesense_health_events', 'alerts', 'incidents')
ORDER BY n_dead_tup DESC;

-- Manual vacuum if dead_pct > 20%
VACUUM ANALYZE kubesense_health_events;
VACUUM ANALYZE alerts;
```

### 5.5 Table Partitioning (for large deployments)

For `kubesense_health_events` tables exceeding 50M rows, enable range partitioning by `occurred_at`:

```sql
-- Partition by month (apply during maintenance window)
-- See platform/database/partition_strategy.md for the full migration plan
ALTER TABLE kubesense_health_events
  RENAME TO kubesense_health_events_old;

CREATE TABLE kubesense_health_events (LIKE kubesense_health_events_old)
  PARTITION BY RANGE (occurred_at);

CREATE TABLE kubesense_health_events_2026_06
  PARTITION OF kubesense_health_events
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
-- Repeat for each month, then insert from old table
```

---

## 6. Scaling Guide

### 6.1 When to Scale `aileron-platform`

Scale when:
- Kafka `alerthub.raw-alerts` consumer lag consistently above 1,000 messages
- Response time on `GET /api/v1/incidents` above 500ms
- `[StagedPipeline] DROPPED` log lines appearing

```bash
kubectl scale deployment aileron-platform -n aileron --replicas=4
# Kafka consumer group rebalances automatically within 30 seconds
```

AlertHub is stateless — Redis handles shared state. Scale freely. Recommended max: 8 replicas (limited by PostgreSQL connection count: 8 pods × 25 pool connections = 200 total, matching max_connections).

### 6.2 When to Scale OIE

Scale when:
- OIE Kafka consumer lag growing above 500 messages
- Investigation queue depth (waiting on semaphore) frequently above 15

```bash
kubectl scale deployment aileron-oie -n aileron --replicas=3
# Also increase the semaphore to match:
# Set OIE_MAX_CONCURRENT_INVESTIGATIONS=60 (3 pods × 20 each)
```

Note: each OIE pod connects to Ollama independently. Ollama GPU memory is the real bottleneck — at `qwen2.5:3b`, a single Ollama instance handles ~5 concurrent inference requests. For >3 OIE replicas, consider a second Ollama instance with a load balancer.

### 6.3 When to Scale `kubesense-api`

Scale when:
- `GET /api/v1/correlation/incidents` response time above 200ms
- Buffer feeder lag (watermark falling behind) — check via `correlation/status`

The buffer feeder uses a Redis distributed lock (`lock:kubesense:buffer-feeder`) to run on only one pod at a time. All other replicas serve REST API traffic only.

```bash
kubectl scale deployment kubesense-api -n aileron-agent --replicas=3
```

### 6.4 Kafka Partition Scaling

AlertHub topics use 3 partitions by default. To increase consumer parallelism, increase partition count:

```bash
kubectl exec -n aileron statefulset/kafka -- \
  kafka-topics.sh --bootstrap-server localhost:9092 \
  --alter --topic alerthub.raw-alerts --partitions 9

# Then scale platform replicas to match partition count
kubectl scale deployment aileron-platform -n aileron --replicas=9
```

Note: Kafka does not support reducing partition count. Scale up only.

### 6.5 PostgreSQL Read Replica

For read-heavy deployments (many dashboard users polling incidents), add a PostgreSQL read replica:

```bash
# In Helm values:
postgres:
  replicas: 1         # Primary
  readReplicas: 1     # Add this
  readReplicaDsn: "postgres://user:pass@pg-replica:5432/aileron?sslmode=disable"

# AlertHub will route read queries (LIST incidents, GET topology) to the replica
# Write queries (INSERT alerts, UPDATE incidents) always go to primary
```

---

## 7. Backup and Recovery

### 7.1 PostgreSQL Backup

**Scheduled backup (daily, recommended):**

```bash
# Backup AlertHub database
kubectl exec -n aileron statefulset/postgres-primary -- \
  pg_dump -U alerthub alerthub | gzip > alerthub-$(date +%Y%m%d).sql.gz

# Backup KubeSense database
kubectl exec -n aileron-agent statefulset/kubesense-postgres -- \
  pg_dump -U kubesense kubesense | gzip > kubesense-$(date +%Y%m%d).sql.gz

# Store in object storage (adjust for your provider):
aws s3 cp alerthub-$(date +%Y%m%d).sql.gz s3://your-bucket/backups/
```

**Continuous WAL archiving (recommended for production):**

```yaml
# In postgres Helm values:
postgres:
  walArchive:
    enabled: true
    s3Bucket: your-backup-bucket
    s3Region: us-east-1
    retentionDays: 30
```

### 7.2 PostgreSQL Restore

```bash
# Restore from dump
kubectl exec -i -n aileron statefulset/postgres-primary -- \
  psql -U alerthub alerthub < alerthub-20260612.sql

# Point-in-time recovery (if WAL archiving enabled):
# Restore base backup, then replay WAL to target timestamp
# See postgres Helm chart docs for PITR procedure
```

### 7.3 Neo4j Backup

Neo4j topology data can be regenerated from KubeSense if lost, but a backup prevents the 15–30 minute resync time.

```bash
# Neo4j online backup (Enterprise Edition)
kubectl exec -n aileron statefulset/neo4j-0 -- \
  neo4j-admin database backup --to-path=/tmp/neo4j-backup neo4j

kubectl cp aileron/neo4j-0:/tmp/neo4j-backup ./neo4j-backup-$(date +%Y%m%d)

# Community Edition: stop + copy data directory
kubectl scale statefulset neo4j -n aileron --replicas=0
kubectl exec -n aileron statefulset/neo4j-0 -- tar czf /tmp/neo4j-data.tar.gz /data
kubectl cp aileron/neo4j-0:/tmp/neo4j-data.tar.gz ./neo4j-data-$(date +%Y%m%d).tar.gz
kubectl scale statefulset neo4j -n aileron --replicas=1
```

**Neo4j recovery from backup:**

```bash
kubectl scale statefulset neo4j -n aileron --replicas=0
kubectl cp ./neo4j-data-20260612.tar.gz aileron/neo4j-0:/tmp/
kubectl exec -n aileron statefulset/neo4j-0 -- tar xzf /tmp/neo4j-data-20260612.tar.gz -C /
kubectl scale statefulset neo4j -n aileron --replicas=1
```

**Neo4j recovery from scratch (if no backup):**

Restart `kubesense-agent` — it will re-publish topology events which `kubesense-collector` will re-write to Neo4j. Full topology rebuild takes 10–15 minutes for a cluster with 1,000 nodes.

```bash
kubectl rollout restart deployment kubesense-agent -n aileron-agent
kubectl logs -n aileron-agent deploy/kubesense-collector | grep "topology\|neo4j" -f
# Watch for "topology synced" or "UPSERT node" log lines
```

### 7.4 Kafka Topic Backup

Kafka data is ephemeral — incidents and alerts are persisted in PostgreSQL. Kafka only needs backup if you need to replay the raw event stream.

For production, set Kafka log retention to match your RPO:

```yaml
# In Strimzi KafkaTopics:
spec:
  config:
    retention.ms: "604800000"  # 7 days
    retention.bytes: "10737418240"  # 10 GB per partition
```

### 7.5 Disaster Recovery Runbook

**Scenario: AlertHub PostgreSQL primary lost**

1. Promote read replica if available: `ALTER SYSTEM SET hot_standby = 'off'; SELECT pg_promote();`
2. Update `DATABASE_URL` secret in Kubernetes to point to promoted replica
3. Restart `aileron-platform` and `aileron-oie` pods to pick up new DSN
4. Verify `/health/detailed` shows `"postgres": "connected"`
5. Expected data loss: < 1 WAL checkpoint interval (default 5 minutes)

**Scenario: Full cluster loss (disaster)**

1. Restore PostgreSQL from latest backup
2. Restore Neo4j from backup or trigger resync
3. Deploy Aileron to new cluster via Helm: `helm upgrade --install aileron ./platform/helm`
4. Update DNS to new cluster endpoint
5. Restart `kubesense-agent` in monitored clusters to rebuild topology
6. Verify all Kafka consumer groups have reassigned to new brokers

**Scenario: Ollama GPU node lost**

1. OIE will detect Ollama unreachable and fall back to deterministic templates immediately
2. Deploy Ollama to new GPU node and pull models:
   ```bash
   kubectl exec -n aileron deploy/ollama -- ollama pull qwen2.5:3b
   kubectl exec -n aileron deploy/ollama -- ollama pull nomic-embed-text
   ```
3. OIE will resume LLM narration once Ollama health check passes (checked every 30s)
4. No data loss — templates are stored in `rca_investigations`; incidents are not affected

### 7.6 Backup Verification

Run monthly:

```bash
# Restore PostgreSQL backup to a test instance and verify
kubectl run pg-restore-test --image=postgres:15 --rm -it --restart=Never \
  -- psql -h test-pg-host -U alerthub -c "\dt" alerthub
# Expected: list of tables including alerts, incidents, rca_investigations

# Verify row counts are reasonable (not truncated backup)
psql $TEST_DATABASE_URL -c "
SELECT relname, n_live_tup
FROM pg_stat_user_tables
WHERE relname IN ('alerts', 'incidents', 'rca_investigations')
ORDER BY relname;"
```
