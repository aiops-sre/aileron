# SRE Command Center — Operational Runbook

> Day-to-day operations, health checks, common issues, and on-call procedures.  
> Version: v1.0.0 | Cluster: example-cluster | Namespace: aileron

---

## Quick Reference

```bash
# Cluster context
kubectl config use-context example-cluster

# Check all pods
kubectl -n aileron get pods

# Live pipeline log
kubectl logs -n aileron -l app=alerthub-backend -f \
  | grep -E "🎯 RCE|✅ Created|🔗 merged|❌ Failed|📥 Dynatrace|🆕 Creating"

# Demo watcher (use during presentations)
bash scripts/demo/watch.sh both

# Connect to DB
POD=$(kubectl get pods -n aileron --no-headers \
  | grep "alerthub-backend.*Running" | head -1 | awk '{print $1}')
kubectl exec -n aileron $POD -- \
  psql "postgresql://alerthub:pg-AIOps-Secure-2024-Prod@postgres-primary.aileron.svc.cluster.local:5432/alerthub"
```

---

## Health Checks

### Pod Status
Expected: all pods `Running 2/2`, zero restarts.
```bash
kubectl -n aileron get pods
kubectl -n aileron get deployments
```

### Application Health
```bash
curl https://aileron.example.com/health
# Expect: {"status":"ok","database":"connected","redis":"connected"}
```

### Correlation Performance (Last Hour)
```sql
SELECT decision, COUNT(*) AS count,
  ROUND(AVG(final_score)::numeric, 3) AS avg_score,
  ROUND(AVG(elapsed_ms)::numeric, 0) AS avg_ms,
  dominant_strategy
FROM pipeline_correlation_results
WHERE processed_at >= NOW() - INTERVAL '1 hour'
GROUP BY decision, dominant_strategy
ORDER BY count DESC;
```

### Noise Reduction Ratio (Last 24h)
```sql
SELECT
  COUNT(*) FILTER (WHERE auto_created = TRUE) AS incidents,
  (SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - INTERVAL '24 hours') AS alerts,
  ROUND(
    (SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - INTERVAL '24 hours')::numeric
    / NULLIF(COUNT(*) FILTER (WHERE auto_created = TRUE), 0), 1
  ) AS alerts_per_incident
FROM incidents WHERE created_at >= NOW() - INTERVAL '24 hours';
```

---

## Common Operations

### Restart a Deployment
```bash
kubectl -n aileron rollout restart deployment/alerthub-backend
kubectl -n aileron rollout restart deployment/frontend
```

### Rollback to Previous Image
```bash
kubectl -n aileron rollout undo deployment/alerthub-backend
kubectl -n aileron rollout undo deployment/frontend
```

### View Current Image Tags
```bash
kubectl -n aileron get deployment alerthub-backend \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
kubectl -n aileron get deployment frontend \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
```

### Update a Secret
```bash
kubectl -n aileron edit secret alerthub-secrets
kubectl -n aileron rollout restart deployment/alerthub-backend
```

---

## Incident Response

### Severity Levels

| Level | Description | Response Time |
|---|---|---|
| P1 — Critical | Service down, data loss risk | Immediate (< 15 min) |
| P2 — High | Significant degradation | < 1 hour |
| P3 — Medium | Partial degradation | < 4 hours |
| P4 — Low | Minor issue, workaround available | Next business day |

### Backend Pod Down
```bash
kubectl -n aileron describe pod <pod-name>
kubectl -n aileron logs <pod-name> --previous
kubectl -n aileron rollout restart deployment/alerthub-backend
# If still failing:
kubectl -n aileron rollout undo deployment/alerthub-backend
```

### Alerts Not Being Ingested
1. `curl -X POST https://aileron.example.com/api/v1/webhooks/dynatrace`
2. Check backend pods are running
3. `kubectl logs ... | grep -i kafka` — Kafka connectivity
4. Verify webhook shared secret in Dynatrace hasn't changed

### Incidents Not Being Created
```bash
kubectl logs -n aileron -l app=alerthub-backend -f \
  | grep -E "❌|ERROR|panic"
kubectl exec -n aileron $POD -- psql "$DATABASE_URL" -c "SELECT 1"
kubectl exec -n aileron $POD -- redis-cli -u $REDIS_URL ping
```

### High Correlation Latency (> 30s)
- **Semantic slow**: BERT unreachable — acceptable, falls back to text matching
- **Topology slow**: Redis stale — restart backend to force refresh
- **DB slow**: Check PostgreSQL (max 25 connections)
- **All slow**: Goroutine leak — restart backend

---

## Useful DB Queries

**Recent incidents:**
```sql
SELECT 'INC-' || incident_number AS number, severity, status,
  jsonb_array_length(COALESCE(alert_ids,'[]'::jsonb)) AS alerts,
  rca_status, to_char(created_at,'HH24:MI:SS') AS created, LEFT(title,50) AS title
FROM incidents
WHERE auto_created = TRUE AND created_at >= NOW() - INTERVAL '30 minutes'
ORDER BY created_at DESC;
```

**Unlinked alerts:**
```sql
SELECT id, title, severity, source, created_at FROM alerts
WHERE incident_id IS NULL AND status != 'resolved'
  AND created_at >= NOW() - INTERVAL '1 hour'
ORDER BY created_at DESC;
```

---

## Demo & Presentation

```bash
bash scripts/demo/present.sh    # 9 interactive scenarios
bash scripts/demo/watch.sh both # live watcher (second terminal)
bash scripts/demo/cleanup.sh    # remove demo data after
```

---

## Maintenance

### Clean Up Old Incidents (> 90 days)
```sql
DELETE FROM incident_timeline WHERE incident_id IN (
  SELECT id FROM incidents WHERE status='resolved' AND resolved_at < NOW()-INTERVAL '90 days'
);
DELETE FROM incidents WHERE status='resolved' AND resolved_at < NOW()-INTERVAL '90 days';
```

### Add Correlation Rule
```sql
INSERT INTO correlation_rules (name, description, priority, enabled, conditions, actions, environment)
VALUES ('Rule Name','Description',150,true,
  '[{"field":"alert.title","operator":"contains","value":"OOM","required":true}]'::jsonb,
  '[{"type":"correlate"}]'::jsonb,'["production"]'::jsonb);
```
Rules sync every 5 minutes — no restart needed.

---

## Key Metrics

| Metric | Good | Warning | Critical |
|---|---|---|---|
| Pipeline queue depth | < 100 | 100–500 | > 500 |
| Correlation latency avg | < 5s | 5–20s | > 20s |
| Backend pod restarts | 0 | 1–2/day | > 5/day |
| DB connection pool | < 20 | 20–24 | 25 (exhausted) |
| Alerts per incident ratio | 5–50 | > 100 | < 2 |

---

## Original Full Runbook (Legacy Reference)

> The sections below are retained from the previous runbook for historical reference.
> The cluster, namespace, and image names referenced below may be outdated.

---

## Table of Contents (Legacy)

1. [System Overview](#system-overview)
2. [Daily Operations](#daily-operations)
3. [Monitoring & Alerting](#monitoring--alerting)
4. [Troubleshooting Guide](#troubleshooting-guide)
5. [Maintenance Procedures](#maintenance-procedures)
6. [Scaling Operations](#scaling-operations)
7. [Emergency Procedures](#emergency-procedures)
8. [Performance Optimization](#performance-optimization)
9. [Security Operations](#security-operations)
10. [Backup & Recovery](#backup--recovery)

---

## System Overview

### Architecture Components
```
AlertHub Enterprise Microservices Architecture
├── API Gateway (Istio Ingress)
├── Authentication Service
├── Alert Management Service  
├── Incident Management Service
├── Configuration Management Service
├── Topology Knowledge Service
├── Correlation Engine Service
├── Notification Service
├── Analytics Service
└── Infrastructure Components
    ├── PostgreSQL Cluster
    ├── Redis Cluster
    ├── RabbitMQ Cluster
    ├── Prometheus/Grafana
    └── Service Mesh (Istio)
```

### Key Metrics & SLAs
| Service | Availability SLA | Response Time SLA | Error Rate SLA |
|---------|------------------|-------------------|----------------|
| API Gateway | 99.95% | <500ms (P95) | <0.1% |
| Auth Service | 99.9% | <200ms (P95) | <0.1% |
| Alert Management | 99.95% | <1s (P95) | <0.1% |
| Incident Management | 99.9% | <1s (P95) | <0.2% |
| Correlation Engine | 99.5% | <2s (P95) | <0.5% |

### Contact Information
- **On-Call Engineer**: +1-XXX-XXX-XXXX (PagerDuty)
- **Platform Team Lead**: platform-lead@company.com
- **Security Team**: security@company.com
- **Database Team**: dba@company.com

---

## Daily Operations

### Morning Health Check Routine

#### 1. System Status Overview
```bash
#!/bin/bash
# Daily health check script - run at 9:00 AM

echo "=== AlertHub Enterprise Daily Health Check - $(date) ==="

# 1. Check all pods status
echo "1. Pod Health Status:"
kubectl get pods -n alerthub -o custom-columns=NAME:.metadata.name,STATUS:.status.phase,READY:.status.containerStatuses[0].ready,RESTARTS:.status.containerStatuses[0].restartCount

# 2. Check service endpoints
echo -e "\n2. Service Endpoint Status:"
kubectl get endpoints -n alerthub

# 3. Check resource usage
echo -e "\n3. Resource Utilization:"
kubectl top nodes
kubectl top pods -n alerthub --sort-by=memory | head -10

# 4. Check persistent volumes
echo -e "\n4. Storage Status:"
kubectl get pv,pvc -n alerthub

# 5. Quick connectivity test
echo -e "\n5. Connectivity Tests:"
for service in auth-service alert-management config-management; do
    if kubectl exec -n alerthub deployment/$service -- curl -f -s http://localhost:8080/health &> /dev/null; then
        echo "✓ $service: Healthy"
    else
        echo "✗ $service: Unhealthy"
    fi
done

echo -e "\n=== Health Check Complete ==="
```

#### 2. Performance Metrics Review
```bash
#!/bin/bash
# Performance metrics review

echo "=== Performance Metrics Review ==="

# Get current performance metrics from Prometheus
PROMETHEUS_URL="http://prometheus.monitoring:9090"

# Response times
echo "Response Time Metrics (Last 24 hours):"
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode 'query=histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{namespace="alerthub"}[24h]))' | \
    jq -r '.data.result[] | "\(.metric.service): \(.value[1])s"'

# Error rates  
echo -e "\nError Rate Metrics (Last 24 hours):"
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode 'query=rate(http_requests_total{namespace="alerthub",code!~"2.."}[24h]) / rate(http_requests_total{namespace="alerthub"}[24h])' | \
    jq -r '.data.result[] | "\(.metric.service): \((.value[1] | tonumber) * 100)%"'

# Throughput
echo -e "\nThroughput Metrics (Current):"
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode 'query=rate(http_requests_total{namespace="alerthub"}[5m])' | \
    jq -r '.data.result[] | "\(.metric.service): \(.value[1]) RPS"'
```

#### 3. Critical Alerts Review
```bash
#!/bin/bash
# Review critical alerts from last 24 hours

echo "=== Critical Alerts Review ==="

# Check AlertManager for active alerts
ALERTMANAGER_URL="http://alertmanager.monitoring:9093"

curl -s "${ALERTMANAGER_URL}/api/v1/alerts?active=true&severity=critical" | \
    jq -r '.data[] | "Alert: \(.labels.alertname) | Service: \(.labels.service) | Started: \(.startsAt)"'

# Check for any failed deployments
kubectl get events -n alerthub --field-selector type=Warning --sort-by='.lastTimestamp' | tail -10
```

### End-of-Day Procedures

#### 1. Backup Verification
```bash
#!/bin/bash
# Verify daily backups completed successfully

echo "=== Backup Verification ==="

# Check database backup status
kubectl get jobs -n alerthub -l app=database-backup --sort-by='.status.startTime' | tail -5

# Verify backup files exist
BACKUP_DATE=$(date +%Y-%m-%d)
if kubectl exec -n alerthub deployment/postgresql-primary -- ls /backups/ | grep -q "${BACKUP_DATE}"; then
    echo "✓ Database backup for ${BACKUP_DATE} exists"
else
    echo "✗ Database backup for ${BACKUP_DATE} missing - INVESTIGATE"
fi

# Check Redis backup
if kubectl exec -n alerthub deployment/redis-master -- ls /data/backups/ | grep -q "${BACKUP_DATE}"; then
    echo "✓ Redis backup for ${BACKUP_DATE} exists"
else
    echo "✗ Redis backup for ${BACKUP_DATE} missing - INVESTIGATE"
fi
```

#### 2. Security Scan Status
```bash
#!/bin/bash
# Check security scan results

echo "=== Security Scan Status ==="

# Check for vulnerability scan results
kubectl get jobs -n security -l app=vulnerability-scanner --sort-by='.status.startTime' | tail -3

# Check security alerts
curl -s "${ALERTMANAGER_URL}/api/v1/alerts?active=true&alertname=SecurityVulnerability" | \
    jq -r '.data | length' | xargs echo "Active security alerts:"
```

---

## Monitoring & Alerting

### Key Dashboards

#### 1. System Overview Dashboard
**URL**: `https://grafana.company.com/d/alerthub-overview`

**Key Panels**:
- Service Health Status
- Request Rate & Response Time
- Error Rate Trends
- Resource Utilization
- Database Performance
- Message Queue Status

#### 2. Service-Specific Dashboards
- **Auth Service**: `https://grafana.company.com/d/auth-service`
- **Alert Management**: `https://grafana.company.com/d/alert-management`
- **Correlation Engine**: `https://grafana.company.com/d/correlation-engine`

### Alert Response Procedures

#### P0 - Critical Alerts (Response: 15 minutes)

##### Service Down Alert
```bash
# Service Down Response Procedure
echo "P0 ALERT: Service Down - $(date)"

SERVICE_NAME="$1"  # e.g., auth-service

# 1. Immediate Assessment
echo "1. Checking service status..."
kubectl get pods -n alerthub -l app=$SERVICE_NAME
kubectl describe deployment/$SERVICE_NAME -n alerthub

# 2. Check recent events
echo "2. Checking recent events..."
kubectl get events -n alerthub --field-selector involvedObject.name=$SERVICE_NAME --sort-by='.lastTimestamp' | tail -10

# 3. Check logs for errors
echo "3. Checking service logs..."
kubectl logs -n alerthub deployment/$SERVICE_NAME --tail=50 | grep -i error

# 4. Attempt service recovery
echo "4. Attempting service recovery..."
kubectl rollout restart deployment/$SERVICE_NAME -n alerthub

# 5. Monitor recovery
kubectl rollout status deployment/$SERVICE_NAME -n alerthub --timeout=300s

# 6. Verify health
kubectl exec -n alerthub deployment/$SERVICE_NAME -- curl -f http://localhost:8080/health

# 7. Update incident ticket
echo "Service recovery attempted. Monitor for 10 minutes before escalating."
```

##### Database Connection Failure
```bash
# Database Connection Failure Response
echo "P0 ALERT: Database Connection Failure - $(date)"

# 1. Check database cluster status
kubectl get pods -n alerthub -l app=postgresql
kubectl get postgresql -n alerthub

# 2. Check database connectivity
kubectl exec -n alerthub deployment/postgresql-primary -- pg_isready

# 3. Check connection pool status
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT count(*) FROM pg_stat_activity WHERE state = 'active';"

# 4. Check for long-running queries
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT pid, now() - pg_stat_activity.query_start AS duration, query 
             FROM pg_stat_activity 
             WHERE (now() - pg_stat_activity.query_start) > interval '5 minutes';"

# 5. Restart database if needed (LAST RESORT)
# kubectl rollout restart statefulset/postgresql-primary -n alerthub
```

#### P1 - High Priority Alerts (Response: 30 minutes)

##### High Error Rate
```bash
# High Error Rate Response Procedure
echo "P1 ALERT: High Error Rate - $(date)"

SERVICE_NAME="$1"
ERROR_THRESHOLD="$2"

# 1. Identify error sources
echo "1. Analyzing error patterns..."
kubectl logs -n alerthub deployment/$SERVICE_NAME --tail=100 | grep -i "error\|exception\|fail" | tail -20

# 2. Check error rate trend
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode "query=rate(http_requests_total{service=\"$SERVICE_NAME\",code!~\"2..\"}[5m])" | \
    jq -r '.data.result[0].value[1]'

# 3. Identify problematic endpoints
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode "query=rate(http_requests_total{service=\"$SERVICE_NAME\",code!~\"2..\"}[5m]) by (path)" | \
    jq -r '.data.result[] | "\(.metric.path): \(.value[1])"'

# 4. Check resource constraints
kubectl top pods -n alerthub -l app=$SERVICE_NAME

# 5. Scale up if resource constrained
if [[ $(kubectl get hpa -n alerthub $SERVICE_NAME -o jsonpath='{.status.currentCPUUtilizationPercentage}') -gt 80 ]]; then
    echo "High CPU detected, scaling up..."
    kubectl scale deployment/$SERVICE_NAME --replicas=$(( $(kubectl get deployment/$SERVICE_NAME -n alerthub -o jsonpath='{.spec.replicas}') + 1 )) -n alerthub
fi
```

##### Slow Response Time
```bash
# Slow Response Time Response Procedure
echo "P1 ALERT: Slow Response Time - $(date)"

SERVICE_NAME="$1"

# 1. Check current response times
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode "query=histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{service=\"$SERVICE_NAME\"}[5m]))"

# 2. Identify slow endpoints
curl -s "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode "query=histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{service=\"$SERVICE_NAME\"}[5m])) by (path)"

# 3. Check database query performance
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT query, mean_time, calls FROM pg_stat_statements ORDER BY mean_time DESC LIMIT 10;"

# 4. Check for resource contention
kubectl describe node $(kubectl get pods -n alerthub -l app=$SERVICE_NAME -o jsonpath='{.items[0].spec.nodeName}')

# 5. Check cache hit rates
kubectl exec -n alerthub deployment/redis-master -- \
    redis-cli info stats | grep -E "(keyspace_hits|keyspace_misses)"
```

### Custom Alert Rules

#### Application-Level Alerts
```yaml
# prometheus-rules.yaml
groups:
- name: alerthub-application
  rules:
  - alert: ServiceDown
    expr: up{job="kubernetes-pods", namespace="alerthub"} == 0
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "Service {{ $labels.service }} is down"
      
  - alert: HighErrorRate
    expr: rate(http_requests_total{namespace="alerthub", code!~"2.."}[5m]) / rate(http_requests_total{namespace="alerthub"}[5m]) > 0.01
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High error rate detected for {{ $labels.service }}"
      
  - alert: SlowResponseTime
    expr: histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{namespace="alerthub"}[5m])) > 2
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "Slow response time for {{ $labels.service }}"
      
  - alert: DatabaseConnectionHigh
    expr: postgresql_connections_active / postgresql_connections_max > 0.8
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High database connection usage"
      
  - alert: RedisMemoryHigh
    expr: redis_memory_used_bytes / redis_memory_max_bytes > 0.9
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "Redis memory usage critical"
```

---

## Troubleshooting Guide

### Common Issues & Solutions

#### Issue 1: Pods Stuck in Pending State
```bash
# Diagnosis procedure
kubectl describe pod <pod-name> -n alerthub

# Common causes and solutions:

# 1. Resource constraints
kubectl get nodes
kubectl describe nodes | grep -A 5 "Allocated resources"

# Solution: Add more nodes or reduce resource requests
kubectl scale deployment <service> --replicas=0 -n alerthub
# Wait for resources to free up, then scale back
kubectl scale deployment <service> --replicas=3 -n alerthub

# 2. Persistent Volume issues
kubectl get pv,pvc -n alerthub
kubectl describe pvc <pvc-name> -n alerthub

# Solution: Check storage class and provisioner
kubectl get storageclass
```

#### Issue 2: Service Connectivity Problems
```bash
# Network connectivity troubleshooting

SERVICE_NAME="$1"

# 1. Check service and endpoints
kubectl get svc $SERVICE_NAME -n alerthub
kubectl get endpoints $SERVICE_NAME -n alerthub

# 2. Test internal connectivity
kubectl run debug-pod --rm -i --restart=Never --image=nicolaka/netshoot -- \
    curl -v http://$SERVICE_NAME.alerthub.svc.cluster.local:80/health

# 3. Check DNS resolution
kubectl run debug-pod --rm -i --restart=Never --image=nicolaka/netshoot -- \
    nslookup $SERVICE_NAME.alerthub.svc.cluster.local

# 4. Check network policies
kubectl get networkpolicies -n alerthub
kubectl describe networkpolicy -n alerthub

# 5. Check service mesh configuration
kubectl get virtualservice -n alerthub
kubectl get destinationrule -n alerthub
```

#### Issue 3: Database Performance Issues
```bash
# Database performance troubleshooting

# 1. Check active connections
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT count(*) FROM pg_stat_activity WHERE state = 'active';"

# 2. Identify slow queries
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT pid, now() - pg_stat_activity.query_start AS duration, query 
             FROM pg_stat_activity 
             WHERE (now() - pg_stat_activity.query_start) > interval '1 minute'
             ORDER BY duration DESC;"

# 3. Check table statistics
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT schemaname, tablename, n_tup_ins, n_tup_upd, n_tup_del, n_live_tup, n_dead_tup 
             FROM pg_stat_user_tables 
             ORDER BY n_dead_tup DESC;"

# 4. Check index usage
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT schemaname, tablename, indexname, idx_tup_read, idx_tup_fetch 
             FROM pg_stat_user_indexes 
             WHERE idx_tup_read = 0;"

# 5. Run VACUUM and ANALYZE if needed
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "VACUUM ANALYZE;"
```

#### Issue 4: Memory Leaks
```bash
# Memory leak investigation

SERVICE_NAME="$1"

# 1. Check current memory usage
kubectl top pods -n alerthub -l app=$SERVICE_NAME

# 2. Check memory trend over time
curl -s "${PROMETHEUS_URL}/api/v1/query_range" \
    --data-urlencode "query=container_memory_usage_bytes{pod=~\"$SERVICE_NAME.*\"}" \
    --data-urlencode "start=$(date -d '1 hour ago' -u +%s)" \
    --data-urlencode "end=$(date -u +%s)" \
    --data-urlencode "step=60"

# 3. Get heap dump (for Java services)
kubectl exec -n alerthub deployment/$SERVICE_NAME -- \
    jcmd 1 GC.run_finalization
kubectl exec -n alerthub deployment/$SERVICE_NAME -- \
    jcmd 1 VM.gc

# 4. Check for OOM kills
kubectl get events -n alerthub --field-selector reason=OOMKilled

# 5. Restart service to free memory (temporary fix)
kubectl rollout restart deployment/$SERVICE_NAME -n alerthub
```

### Diagnostic Commands Reference

#### System Information
```bash
# Cluster information
kubectl cluster-info
kubectl get nodes -o wide
kubectl get componentstatuses

# Namespace overview
kubectl get all -n alerthub
kubectl get events -n alerthub --sort-by='.lastTimestamp' | tail -20

# Resource usage
kubectl top nodes
kubectl top pods -n alerthub --sort-by=memory
kubectl describe nodes | grep -A 5 "Allocated resources"
```

#### Service Debugging
```bash
# Pod debugging
kubectl get pods -n alerthub -o wide
kubectl describe pod <pod-name> -n alerthub
kubectl logs <pod-name> -n alerthub --tail=100
kubectl logs <pod-name> -n alerthub --previous  # Previous container logs

# Service debugging
kubectl get services -n alerthub
kubectl describe service <service-name> -n alerthub
kubectl get endpoints -n alerthub

# Network debugging
kubectl exec -n alerthub <pod-name> -- netstat -tulpn
kubectl exec -n alerthub <pod-name> -- ss -tulpn
```

---

## Maintenance Procedures

### Regular Maintenance Tasks

#### Weekly Maintenance

##### Certificate Renewal Check
```bash
#!/bin/bash
# Check SSL certificate expiration dates

echo "=== Certificate Expiration Check ==="

# Check ingress TLS certificates
kubectl get secrets -n alerthub -o json | jq -r '
.items[] | 
select(.type == "kubernetes.io/tls") | 
.metadata.name as $name | 
.data."tls.crt" | 
@base64d' | while read -r cert_data; do
    echo "$cert_data" | openssl x509 -noout -enddate -subject
done

# Check internal service certificates
kubectl get secrets -n istio-system -o json | jq -r '
.items[] | 
select(.type == "kubernetes.io/tls") | 
.metadata.name'
```

##### Database Maintenance
```bash
#!/bin/bash
# Weekly database maintenance

echo "=== Database Maintenance - $(date) ==="

# 1. Run VACUUM and ANALYZE
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "VACUUM (ANALYZE, VERBOSE);"

# 2. Update table statistics
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "ANALYZE;"

# 3. Check database size
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT pg_database.datname, 
                    pg_size_pretty(pg_database_size(pg_database.datname)) AS size 
             FROM pg_database;"

# 4. Check for unused indexes
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT schemaname, tablename, indexname, idx_tup_read, idx_tup_fetch 
             FROM pg_stat_user_indexes 
             WHERE idx_tup_read < 1000 AND idx_tup_fetch < 1000;"

# 5. Archive old data (if applicable)
# kubectl exec -n alerthub deployment/postgresql-primary -- \
#     psql -c "DELETE FROM alerts WHERE created_at < NOW() - INTERVAL '90 days';"
```

##### Log Rotation & Cleanup
```bash
#!/bin/bash
# Log rotation and cleanup

echo "=== Log Cleanup - $(date) ==="

# 1. Clean up old container logs (handled by logrotate usually)
# Check log sizes
kubectl exec -n kube-system daemonset/fluentd -- \
    du -sh /var/log/containers/*.log | sort -hr | head -10

# 2. Clean up old backup files
kubectl exec -n alerthub deployment/postgresql-primary -- \
    find /backups -name "*.sql.gz" -mtime +30 -delete

# 3. Clean up completed jobs
kubectl delete jobs -n alerthub --field-selector status.successful=1

# 4. Clean up old events
kubectl get events -n alerthub --field-selector type=Warning --sort-by='.lastTimestamp' | \
    head -n -50 | awk '{print $1}' | xargs -r kubectl delete event -n alerthub
```

#### Monthly Maintenance

##### Security Updates
```bash
#!/bin/bash
# Monthly security updates

echo "=== Security Updates - $(date) ==="

# 1. Update base images
echo "Checking for image updates..."
kubectl get pods -n alerthub -o jsonpath='{range .items[*]}{.spec.containers[*].image}{"\n"}{end}' | \
    sort -u > current-images.txt

# 2. Scan for vulnerabilities
trivy image --exit-code 1 $(cat current-images.txt)

# 3. Update Kubernetes
kubectl version --client
kubectl version --server

# 4. Update Helm charts
helm repo update
helm list -n alerthub

# 5. Check for security patches
kubectl get nodes -o wide
```

##### Performance Review
```bash
#!/bin/bash
# Monthly performance review

echo "=== Performance Review - $(date) ==="

# 1. Generate performance report
PROMETHEUS_URL="http://prometheus.monitoring:9090"

# Response time trends (last 30 days)
curl -s "${PROMETHEUS_URL}/api/v1/query_range" \
    --data-urlencode "query=histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{namespace=\"alerthub\"}[1d]))" \
    --data-urlencode "start=$(date -d '30 days ago' -u +%s)" \
    --data-urlencode "end=$(date -u +%s)" \
    --data-urlencode "step=86400" > monthly-performance-report.json

# 2. Resource utilization trends
kubectl top nodes > monthly-node-usage.txt
kubectl top pods -n alerthub --sort-by=cpu > monthly-pod-usage.txt

# 3. Database performance review
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT query, calls, total_time, mean_time, rows 
             FROM pg_stat_statements 
             ORDER BY total_time DESC 
             LIMIT 20;" > monthly-db-performance.txt

# 4. Capacity planning recommendations
echo "=== Capacity Planning Recommendations ===" > capacity-recommendations.txt
# Add logic to analyze trends and generate recommendations
```

### Update Procedures

#### Application Updates
```bash
#!/bin/bash
# Application update procedure

SERVICE_NAME="$1"
NEW_VERSION="$2"

echo "=== Updating $SERVICE_NAME to $NEW_VERSION ==="

# 1. Pre-update validation
echo "1. Pre-update validation..."
kubectl get deployment $SERVICE_NAME -n alerthub
./scripts/deploy/validation/health-checks.sh alerthub production

# 2. Create rollback point
echo "2. Creating rollback point..."
kubectl get deployment $SERVICE_NAME -n alerthub -o yaml > rollback-$SERVICE_NAME-$(date +%Y%m%d-%H%M%S).yaml

# 3. Update deployment
echo "3. Updating deployment..."
kubectl set image deployment/$SERVICE_NAME -n alerthub \
    $SERVICE_NAME=ghcr.io/company/alerthub/$SERVICE_NAME:$NEW_VERSION

# 4. Monitor rollout
echo "4. Monitoring rollout..."
kubectl rollout status deployment/$SERVICE_NAME -n alerthub --timeout=600s

# 5. Post-update validation
echo "5. Post-update validation..."
sleep 30
./scripts/deploy/validation/smoke-tests.sh alerthub production

# 6. Performance comparison
echo "6. Running performance comparison..."
./scripts/deploy/validation/performance-tests.sh alerthub production 300 50 100

echo "Update completed successfully"
```

#### Infrastructure Updates
```bash
#!/bin/bash
# Infrastructure update procedure (Kubernetes, Istio, etc.)

COMPONENT="$1"  # kubernetes, istio, prometheus, etc.
NEW_VERSION="$2"

echo "=== Updating $COMPONENT to $NEW_VERSION ==="

case "$COMPONENT" in
    "kubernetes")
        # Kubernetes cluster update
        echo "Updating Kubernetes cluster..."
        
        # 1. Update master nodes (if managed)
        # 2. Update worker nodes one by one
        kubectl get nodes
        
        # For each worker node:
        # kubectl drain <node-name> --ignore-daemonsets --delete-local-data
        # Update the node (cloud provider specific)
        # kubectl uncordon <node-name>
        ;;
        
    "istio")
        # Istio service mesh update
        echo "Updating Istio service mesh..."
        
        # 1. Download new Istio version
        curl -L https://istio.io/downloadIstio | ISTIO_VERSION=$NEW_VERSION sh -
        
        # 2. Update control plane
        istioctl upgrade --set values.pilot.image=pilot:$NEW_VERSION
        
        # 3. Update data plane (restart pods with new sidecar)
        kubectl rollout restart deployment -n alerthub
        ;;
        
    "prometheus")
        # Prometheus monitoring stack update
        echo "Updating Prometheus..."
        
        helm repo update
        helm upgrade prometheus prometheus-community/kube-prometheus-stack \
            -n monitoring --version $NEW_VERSION
        ;;
esac
```

---

## Scaling Operations

### Horizontal Pod Autoscaling

#### Configure HPA
```yaml
# hpa-configuration.yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: auth-service-hpa
  namespace: alerthub
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: auth-service
  minReplicas: 3
  maxReplicas: 20
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 60
      policies:
      - type: Percent
        value: 100
        periodSeconds: 60
    scaleDown:
      stabilizationWindowSeconds: 300
      policies:
      - type: Percent
        value: 10
        periodSeconds: 60
```

#### Manual Scaling Operations
```bash
#!/bin/bash
# Manual scaling operations

SERVICE_NAME="$1"
REPLICAS="$2"
NAMESPACE="${3:-alerthub}"

echo "=== Scaling $SERVICE_NAME to $REPLICAS replicas ==="

# 1. Check current status
echo "Current status:"
kubectl get deployment $SERVICE_NAME -n $NAMESPACE
kubectl get hpa -n $NAMESPACE | grep $SERVICE_NAME

# 2. Scale the deployment
echo "Scaling deployment..."
kubectl scale deployment $SERVICE_NAME --replicas=$REPLICAS -n $NAMESPACE

# 3. Monitor scaling progress
echo "Monitoring scaling progress..."
kubectl rollout status deployment/$SERVICE_NAME -n $NAMESPACE --timeout=300s

# 4. Verify new pod distribution
echo "Pod distribution:"
kubectl get pods -n $NAMESPACE -l app=$SERVICE_NAME -o wide

# 5. Check resource usage after scaling
sleep 30
kubectl top pods -n $NAMESPACE -l app=$SERVICE_NAME
```

### Cluster Scaling

#### Node Scaling Operations
```bash
#!/bin/bash
# Cluster node scaling operations

ACTION="$1"  # scale-up or scale-down
NODE_COUNT="$2"

echo "=== Cluster Node Scaling: $ACTION ==="

case "$ACTION" in
    "scale-up")
        echo "Scaling cluster UP to $NODE_COUNT nodes..."
        
        # For cloud providers (example for AWS EKS)
        aws eks update-nodegroup \
            --cluster-name alerthub-cluster \
            --nodegroup-name worker-nodes \
            --scaling-config minSize=3,maxSize=$NODE_COUNT,desiredSize=$NODE_COUNT
            
        # Monitor node provisioning
        watch kubectl get nodes
        ;;
        
    "scale-down")
        echo "Scaling cluster DOWN to $NODE_COUNT nodes..."
        
        # 1. Identify nodes to remove
        kubectl get nodes --no-headers | tail -n +$(($NODE_COUNT + 1))
        
        # 2. Drain nodes safely
        for node in $(kubectl get nodes --no-headers | tail -n +$(($NODE_COUNT + 1)) | awk '{print $1}'); do
            echo "Draining node: $node"
            kubectl drain $node --ignore-daemonsets --delete-local-data --force
            
            # Remove from cloud provider
            # aws ec2 terminate-instances --instance-ids $(aws ec2 describe-instances --filters "Name=private-dns-name,Values=$node" --query 'Reservations[].Instances[].InstanceId' --output text)
        done
        ;;
esac
```

### Database Scaling

#### PostgreSQL Scaling
```bash
#!/bin/bash
# PostgreSQL scaling operations

ACTION="$1"  # scale-up, scale-down, add-replica

echo "=== PostgreSQL Scaling: $ACTION ==="

case "$ACTION" in
    "scale-up")
        echo "Scaling PostgreSQL resources..."
        
        # 1. Update resource limits
        kubectl patch postgresql postgresql-cluster -n alerthub --type='merge' -p='
        spec:
          instances: 3
          postgresql:
            parameters:
              max_connections: "200"
              shared_buffers: "256MB"
              effective_cache_size: "1GB"
        '
        
        # 2. Wait for changes to apply
        kubectl rollout status statefulset/postgresql-cluster -n alerthub
        ;;
        
    "add-replica")
        echo "Adding PostgreSQL read replica..."
        
        kubectl patch postgresql postgresql-cluster -n alerthub --type='merge' -p='
        spec:
          instances: 4
        '
        ;;
esac
```

---

## Emergency Procedures

### Disaster Recovery

#### Complete System Failure
```bash
#!/bin/bash
# Complete system disaster recovery procedure

echo "=== DISASTER RECOVERY PROCEDURE ==="
echo "Timestamp: $(date)"
echo "Incident: Complete system failure"

# 1. Assess the situation
echo "1. Assessing system status..."
kubectl get nodes
kubectl get pods --all-namespaces | grep -v Running

# 2. Activate backup systems
echo "2. Activating backup systems..."

# Switch DNS to backup cluster (if available)
# aws route53 change-resource-record-sets --hosted-zone-id Z123456789 --change-batch file://dns-failover.json

# 3. Restore from backup
echo "3. Initiating restore from backup..."

# Get latest backup
LATEST_BACKUP=$(kubectl get backups -n alerthub --sort-by='.metadata.creationTimestamp' -o name | tail -1)
echo "Latest backup: $LATEST_BACKUP"

# Restore database
kubectl apply -f - << EOF
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: postgresql-restored
  namespace: alerthub
spec:
  instances: 3
  bootstrap:
    recovery:
      backup:
        name: ${LATEST_BACKUP}
EOF

# 4. Redeploy services
echo "4. Redeploying services..."
./scripts/deploy/master-deployment.sh --environment production --namespace alerthub --type full

# 5. Validate recovery
echo "5. Validating recovery..."
./scripts/deploy/validation/smoke-tests.sh alerthub production

echo "Disaster recovery procedure completed. Monitor system closely."
```

#### Security Incident Response
```bash
#!/bin/bash
# Security incident response procedure

INCIDENT_TYPE="$1"  # breach, malware, unauthorized-access
AFFECTED_COMPONENT="$2"

echo "=== SECURITY INCIDENT RESPONSE ==="
echo "Incident Type: $INCIDENT_TYPE"
echo "Affected Component: $AFFECTED_COMPONENT"
echo "Timestamp: $(date)"

# 1. Immediate containment
echo "1. Immediate containment..."

case "$INCIDENT_TYPE" in
    "breach")
        # Isolate affected components
        kubectl scale deployment $AFFECTED_COMPONENT --replicas=0 -n alerthub
        
        # Block network access
        kubectl apply -f - << EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: emergency-isolation
  namespace: alerthub
spec:
  podSelector:
    matchLabels:
      app: $AFFECTED_COMPONENT
  policyTypes:
  - Ingress
  - Egress
EOF
        ;;
        
    "unauthorized-access")
        # Revoke all authentication tokens
        kubectl delete secrets -n alerthub -l type=auth-token
        
        # Force password reset for all users
        kubectl exec -n alerthub deployment/auth-service -- \
            psql -c "UPDATE users SET password_reset_required = TRUE, auth_token = NULL;"
        ;;
esac

# 2. Evidence collection
echo "2. Collecting evidence..."
mkdir -p /tmp/security-incident-$(date +%Y%m%d-%H%M%S)
kubectl logs -n alerthub deployment/$AFFECTED_COMPONENT --since=24h > /tmp/security-incident-*/logs.txt
kubectl get events -n alerthub --sort-by='.lastTimestamp' > /tmp/security-incident-*/events.txt

# 3. Notify security team
echo "3. Notifying security team..."
curl -X POST https://security-webhook.company.com/incident \
    -H "Content-Type: application/json" \
    -d "{
        \"type\": \"$INCIDENT_TYPE\",
        \"component\": \"$AFFECTED_COMPONENT\",
        \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
        \"severity\": \"high\"
    }"

echo "Security incident response initiated. Awaiting security team guidance."
```

### Recovery Procedures

#### Service Recovery
```bash
#!/bin/bash
# Service recovery procedure

SERVICE_NAME="$1"
RECOVERY_METHOD="$2"  # restart, rollback, restore

echo "=== SERVICE RECOVERY: $SERVICE_NAME ==="

case "$RECOVERY_METHOD" in
    "restart")
        echo "Restarting service..."
        kubectl rollout restart deployment/$SERVICE_NAME -n alerthub
        kubectl rollout status deployment/$SERVICE_NAME -n alerthub --timeout=300s
        ;;
        
    "rollback")
        echo "Rolling back service..."
        kubectl rollout undo deployment/$SERVICE_NAME -n alerthub
        kubectl rollout status deployment/$SERVICE_NAME -n alerthub --timeout=300s
        ;;
        
    "restore")
        echo "Restoring service from backup..."
        # Implementation depends on backup strategy
        ./scripts/deploy/restore-service.sh $SERVICE_NAME
        ;;
esac

# Validate recovery
./scripts/deploy/validation/smoke-tests.sh alerthub production
```

---

## Performance Optimization

### Database Optimization

#### Query Performance Tuning
```bash
#!/bin/bash
# Database query performance tuning

echo "=== Database Performance Tuning ==="

# 1. Identify slow queries
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT query, calls, total_time, mean_time 
             FROM pg_stat_statements 
             WHERE mean_time > 100 
             ORDER BY mean_time DESC 
             LIMIT 10;"

# 2. Check missing indexes
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SELECT schemaname, tablename, seq_scan, seq_tup_read, 
                    seq_tup_read / seq_scan as avg_tup_read
             FROM pg_stat_user_tables 
             WHERE seq_scan > 0 
             ORDER BY seq_tup_read DESC 
             LIMIT 10;"

# 3. Update table statistics
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "ANALYZE VERBOSE;"

# 4. Check connection pool settings
kubectl exec -n alerthub deployment/postgresql-primary -- \
    psql -c "SHOW max_connections; SHOW shared_buffers; SHOW effective_cache_size;"

# 5. Optimize PostgreSQL configuration
kubectl patch postgresql postgresql-cluster -n alerthub --type='merge' -p='
spec:
  postgresql:
    parameters:
      max_connections: "200"
      shared_buffers: "512MB"
      effective_cache_size: "2GB"
      work_mem: "4MB"
      maintenance_work_mem: "64MB"
      checkpoint_completion_target: "0.9"
      wal_buffers: "16MB"
      random_page_cost: "1.1"
'
```

#### Cache Optimization
```bash
#!/bin/bash
# Redis cache optimization

echo "=== Cache Optimization ==="

# 1. Check cache hit rate
kubectl exec -n alerthub deployment/redis-master -- \
    redis-cli info stats | grep -E "(keyspace_hits|keyspace_misses)"

# 2. Analyze memory usage
kubectl exec -n alerthub deployment/redis-master -- \
    redis-cli info memory

# 3. Check slow operations
kubectl exec -n alerthub deployment/redis-master -- \
    redis-cli slowlog get 10

# 4. Optimize Redis configuration
kubectl patch deployment redis-master -n alerthub --type='json' -p='[
    {
        "op": "replace",
        "path": "/spec/template/spec/containers/0/args",
        "value": [
            "redis-server",
            "--maxmemory", "2gb",
            "--maxmemory-policy", "allkeys-lru",
            "--save", "900", "1",
            "--save", "300", "10",
            "--save", "60", "10000"
        ]
    }
]'

# 5. Monitor cache performance
watch -n 5 'kubectl exec -n alerthub deployment/redis-master -- redis-cli info stats | grep -E "(keyspace_hits|keyspace_misses|used_memory_human)"'
```

### Application Performance

#### JVM Tuning (for Java services)
```bash
#!/bin/bash
# JVM performance tuning

SERVICE_NAME="$1"

echo "=== JVM Performance Tuning for $SERVICE_NAME ==="

# 1. Get current JVM settings
kubectl exec -n alerthub deployment/$SERVICE_NAME -- \
    java -XX:+PrintFlagsFinal -version | grep -E "(HeapSize|NewRatio|SurvivorRatio)"

# 2. Update JVM parameters
kubectl patch deployment $SERVICE_NAME -n alerthub --type='json' -p='[
    {
        "op": "replace",
        "path": "/spec/template/spec/containers/0/env/-",
        "value": {
            "name": "JAVA_OPTS",
            "value": "-Xms512m -Xmx2g -XX:+UseG1GC -XX:MaxGCPauseMillis=200 -XX:+PrintGC -XX:+PrintGCDetails"
        }
    }
]'

# 3. Monitor GC performance
kubectl logs -n alerthub deployment/$SERVICE_NAME | grep -E "(GC|pause)"
```

---

## Security Operations

### Security Monitoring

#### Daily Security Checks
```bash
#!/bin/bash
# Daily security monitoring

echo "=== Daily Security Check - $(date) ==="

# 1. Check for failed authentication attempts
kubectl logs -n alerthub deployment/auth-service --since=24h | grep -i "authentication failed" | wc -l

# 2. Monitor privilege escalation attempts
kubectl get events --all-namespaces --field-selector reason=FailedMount,reason=Failed | grep -i privilege

# 3. Check for suspicious network activity
kubectl get networkpolicies --all-namespaces
kubectl logs -n kube-system daemonset/calico-node | grep -i "denied"

# 4. Scan for vulnerabilities
trivy image --severity HIGH,CRITICAL $(kubectl get pods -n alerthub -o jsonpath='{.items[*].spec.containers[*].image}' | tr ' ' '\n' | sort -u)

# 5. Check certificate status
kubectl get secrets -n alerthub -o json | jq -r '
.items[] | 
select(.type == "kubernetes.io/tls") | 
.metadata.name as $name | 
.data."tls.crt" | 
@base64d' | openssl x509 -noout -enddate
```

#### Access Audit
```bash
#!/bin/bash
# Access audit procedure

echo "=== Access Audit - $(date) ==="

# 1. Review RBAC permissions
kubectl get rolebindings,clusterrolebindings --all-namespaces -o wide

# 2. Check service account usage
kubectl get serviceaccounts --all-namespaces
kubectl auth can-i --list --as=system:serviceaccount:alerthub:default

# 3. Audit API server access
kubectl logs -n kube-system $(kubectl get pods -n kube-system -l component=kube-apiserver -o name | head -1) | \
    grep -E "(401|403)" | tail -20

# 4. Check for unused permissions
kubectl auth can-i --list --as=system:serviceaccount:alerthub:alerthub-service | \
    grep -E "(create|delete|update)" > /tmp/permissions.txt
```

### Compliance Operations

#### SOC 2 Compliance Check
```bash
#!/bin/bash
# SOC 2 compliance verification

echo "=== SOC 2 Compliance Check - $(date) ==="

# 1. Verify data encryption at rest
kubectl get secrets -n alerthub | wc -l
kubectl describe storageclass | grep -i encrypt

# 2. Check access logging
kubectl get events -n alerthub --field-selector type=Normal | grep -E "(Created|Updated|Deleted)" | wc -l

# 3. Verify backup procedures
kubectl get backups -n alerthub --sort-by='.metadata.creationTimestamp' | tail -7

# 4. Check network segmentation
kubectl get networkpolicies -n alerthub
kubectl describe networkpolicy -n alerthub

# 5. Verify user access controls
kubectl get rolebindings -n alerthub
kubectl describe rolebinding -n alerthub | grep -E "(User|Group)"
```

---

## Backup & Recovery

### Backup Procedures

#### Daily Backup Script
```bash
#!/bin/bash
# Daily backup procedure

BACKUP_DATE=$(date +%Y-%m-%d)
BACKUP_DIR="/backups/${BACKUP_DATE}"

echo "=== Daily Backup Procedure - $BACKUP_DATE ==="

# 1. Create backup directory
mkdir -p "$BACKUP_DIR"

# 2. Database backup
echo "Backing up PostgreSQL..."
kubectl exec -n alerthub deployment/postgresql-primary -- \
    pg_dumpall -U postgres | gzip > "$BACKUP_DIR/postgresql-${BACKUP_DATE}.sql.gz"

# 3. Redis backup
echo "Backing up Redis..."
kubectl exec -n alerthub deployment/redis-master -- redis-cli BGSAVE
kubectl cp alerthub/redis-master-0:/data/dump.rdb "$BACKUP_DIR/redis-${BACKUP_DATE}.rdb"

# 4. Configuration backup
echo "Backing up configurations..."
kubectl get configmaps -n alerthub -o yaml > "$BACKUP_DIR/configmaps-${BACKUP_DATE}.yaml"
kubectl get secrets -n alerthub -o yaml > "$BACKUP_DIR/secrets-${BACKUP_DATE}.yaml"

# 5. Application state backup
echo "Backing up application state..."
kubectl get all -n alerthub -o yaml > "$BACKUP_DIR/applications-${BACKUP_DATE}.yaml"

# 6. Upload to cloud storage (example for AWS S3)
echo "Uploading to cloud storage..."
aws s3 sync "$BACKUP_DIR" "s3://alerthub-backups/${BACKUP_DATE}/"

# 7. Cleanup old local backups (keep 7 days)
find /backups -name "20*" -type d -mtime +7 -exec rm -rf {} \;

echo "Backup completed successfully"
```

### Recovery Procedures

#### Point-in-Time Recovery
```bash
#!/bin/bash
# Point-in-time recovery procedure

RECOVERY_TIMESTAMP="$1"  # Format: YYYY-MM-DD HH:MM:SS
RECOVERY_TYPE="$2"       # full, database-only, config-only

echo "=== Point-in-Time Recovery to $RECOVERY_TIMESTAMP ==="

case "$RECOVERY_TYPE" in
    "full")
        echo "Performing full system recovery..."
        
        # 1. Scale down all services
        kubectl scale deployments --all --replicas=0 -n alerthub
        
        # 2. Restore database
        restore_database "$RECOVERY_TIMESTAMP"
        
        # 3. Restore configurations
        restore_configurations "$RECOVERY_TIMESTAMP"
        
        # 4. Scale services back up
        kubectl scale deployments --all --replicas=3 -n alerthub
        ;;
        
    "database-only")
        echo "Performing database-only recovery..."
        restore_database "$RECOVERY_TIMESTAMP"
        ;;
        
    "config-only")
        echo "Performing configuration-only recovery..."
        restore_configurations "$RECOVERY_TIMESTAMP"
        ;;
esac

# Validate recovery
./scripts/deploy/validation/smoke-tests.sh alerthub production
```

#### Backup Validation
```bash
#!/bin/bash
# Backup validation procedure

BACKUP_DATE="$1"

echo "=== Backup Validation for $BACKUP_DATE ==="

# 1. Verify backup files exist
BACKUP_DIR="/backups/${BACKUP_DATE}"
if [[ ! -d "$BACKUP_DIR" ]]; then
    echo "ERROR: Backup directory not found: $BACKUP_DIR"
    exit 1
fi

# 2. Validate database backup
echo "Validating database backup..."
gunzip -c "$BACKUP_DIR/postgresql-${BACKUP_DATE}.sql.gz" | head -20 | grep -q "PostgreSQL database dump"
if [[ $? -eq 0 ]]; then
    echo "✓ Database backup is valid"
else
    echo "✗ Database backup is corrupted"
fi

# 3. Validate Redis backup
echo "Validating Redis backup..."
if [[ -f "$BACKUP_DIR/redis-${BACKUP_DATE}.rdb" ]]; then
    file "$BACKUP_DIR/redis-${BACKUP_DATE}.rdb" | grep -q "RDB"
    if [[ $? -eq 0 ]]; then
        echo "✓ Redis backup is valid"
    else
        echo "✗ Redis backup is corrupted"
    fi
fi

# 4. Validate configuration backups
echo "Validating configuration backups..."
kubectl apply --dry-run=client -f "$BACKUP_DIR/configmaps-${BACKUP_DATE}.yaml" > /dev/null 2>&1
if [[ $? -eq 0 ]]; then
    echo "✓ ConfigMaps backup is valid"
else
    echo "✗ ConfigMaps backup is corrupted"
fi

echo "Backup validation completed"
```

---

## Appendix

### Quick Reference Commands

#### Essential kubectl Commands
```bash
# Pod management
kubectl get pods -n alerthub -o wide
kubectl describe pod <pod-name> -n alerthub
kubectl logs <pod-name> -n alerthub --tail=100
kubectl exec -it <pod-name> -n alerthub -- /bin/bash

# Service management
kubectl get services -n alerthub
kubectl describe service <service-name> -n alerthub
kubectl port-forward service/<service-name> 8080:80 -n alerthub

# Deployment management
kubectl get deployments -n alerthub
kubectl scale deployment <deployment-name> --replicas=3 -n alerthub
kubectl rollout status deployment/<deployment-name> -n alerthub
kubectl rollout restart deployment/<deployment-name> -n alerthub
kubectl rollout undo deployment/<deployment-name> -n alerthub

# Resource monitoring
kubectl top nodes
kubectl top pods -n alerthub --sort-by=memory
kubectl get events -n alerthub --sort-by='.lastTimestamp'
```

#### Common Troubleshooting Commands
```bash
# Network debugging
kubectl run debug --rm -i --restart=Never --image=nicolaka/netshoot -- /bin/bash
nslookup <service-name>.alerthub.svc.cluster.local
curl -v http://<service-name>.alerthub.svc.cluster.local:80/health

# Performance analysis
kubectl top pods -n alerthub --containers
kubectl describe nodes | grep -A 5 "Allocated resources"
kubectl get hpa -n alerthub

# Log analysis
kubectl logs -n alerthub deployment/<service-name> --since=1h
kubectl logs -n alerthub deployment/<service-name> --previous
kubectl logs -n alerthub -l app=<service-name> --tail=100
```

### Contact Information & Escalation

#### Emergency Contacts
- **Primary On-Call**: +1-XXX-XXX-XXXX (PagerDuty)
- **Secondary On-Call**: +1-XXX-XXX-XXXX (PagerDuty)
- **Platform Team Lead**: platform-lead@company.com
- **Security Incident**: security-incident@company.com
- **Database Emergency**: dba-oncall@company.com

#### Escalation Matrix
| Issue Type | Response Time | Primary Contact | Escalation |
|------------|---------------|-----------------|------------|
| P0 (System Down) | 15 minutes | On-Call Engineer | Platform Team Lead |
| P1 (Performance) | 30 minutes | On-Call Engineer | Platform Team Lead |
| P2 (Non-Critical) | 4 hours | Platform Team | Team Lead |
| Security Incident | 15 minutes | Security Team | CISO |

---

*This operational runbook should be reviewed and updated monthly to ensure accuracy and completeness. All team members should be familiar with these procedures and participate in regular drills.*