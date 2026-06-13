# AlertHub Enterprise - Comprehensive Operational Runbooks
# Week 4 Production Readiness - Complete Operations Guide

## Table of Contents
1. [System Overview](#system-overview)
2. [Daily Operations](#daily-operations)
3. [Incident Response](#incident-response)
4. [Troubleshooting Guide](#troubleshooting-guide)
5. [Maintenance Procedures](#maintenance-procedures)
6. [Performance Monitoring](#performance-monitoring)
7. [Security Operations](#security-operations)
8. [Backup and Recovery](#backup-and-recovery)
9. [Escalation Procedures](#escalation-procedures)
10. [Emergency Contacts](#emergency-contacts)

## System Overview

### AlertHub Enterprise Architecture
- **Namespace**: `alerthub-production`
- **Core Services**:
  - AI Correlation Engine (5 replicas)
  - Autonomous AI Service (3 replicas)
  - Backend API (4 replicas)
  - Frontend UI (2 replicas)
- **Infrastructure**:
  - PostgreSQL Cluster (Primary + 2 Read Replicas)
  - Redis Cluster (3 nodes)
  - Kafka Cluster (3 brokers)
  - Weaviate Vector DB (2 nodes)

### Key Monitoring URLs
- **Grafana**: https://grafana.monitoring.example.com/d/alerthub-production
- **Prometheus**: https://prometheus.monitoring.example.com
- **Jaeger**: https://jaeger.monitoring.example.com
- **AlertManager**: https://alertmanager.monitoring.example.com

---

## Daily Operations

### Morning Health Check (9:00 AM Daily)

```bash
#!/bin/bash
# Daily health check routine

echo "AlertHub Daily Health Check - $(date)"
echo "======================================="

# 1. Check namespace and pods
kubectl get pods -n alerthub-production -o wide

# 2. Check service status
kubectl get services -n alerthub-production

# 3. Check ingress status
kubectl get ingress -n alerthub-production

# 4. Check HPA status
kubectl get hpa -n alerthub-production

# 5. Check node resources
kubectl top nodes

# 6. Check pod resources
kubectl top pods -n alerthub-production

# 7. Test application endpoints
curl -f https://alerthub-production.k.example.com/api/health || echo "❌ API health check failed"
curl -f https://alerthub-production.k.example.com/api/correlation/health || echo "❌ Correlation health check failed"

# 8. Check recent alerts
kubectl logs -n alerthub-production deployment/alerthub-backend-prod --tail=50 | grep -i error

echo "✅ Daily health check completed"
```

### Weekly Maintenance (Sundays 2:00 AM)

```bash
#!/bin/bash
# Weekly maintenance routine

echo "AlertHub Weekly Maintenance - $(date)"
echo "====================================="

# 1. Update deployment images (if available)
kubectl set image deployment/ai-correlation-engine-prod ai-correlation-engine=ghcr.io/aileron-platform/aileron-admins/ai-correlation-engine:latest -n alerthub-production

# 2. Restart services for fresh state
kubectl rollout restart deployment/alerthub-backend-prod -n alerthub-production
kubectl rollout restart deployment/autonomous-ai-correlation-prod -n alerthub-production

# 3. Clean up old logs
kubectl delete pods -n alerthub-production --field-selector=status.phase=Succeeded

# 4. Verify backups
aws s3 ls s3://alerthub-backups/database/ | tail -7

# 5. Update certificates (if needed)
kubectl get secrets -n alerthub-production | grep tls

echo "✅ Weekly maintenance completed"
```

---

## Incident Response

### Severity Levels

#### Severity 1 (Critical) - Response Time: 15 minutes
- Complete system outage
- Data corruption/loss
- Security breach
- >50% of users affected

#### Severity 2 (High) - Response Time: 1 hour
- Major feature unavailable
- Performance severely degraded
- 10-50% of users affected

#### Severity 3 (Medium) - Response Time: 4 hours
- Minor feature issues
- Performance moderately impacted
- <10% of users affected

#### Severity 4 (Low) - Response Time: Next business day
- Cosmetic issues
- Enhancement requests
- No user impact

### Incident Response Workflow

#### Step 1: Detection and Alert
```bash
# Check current alerts
kubectl get pods -n alerthub-production | grep -v Running
kubectl get events -n alerthub-production --sort-by='.lastTimestamp' | tail -10

# Check monitoring dashboards
echo "Check Grafana: https://grafana.monitoring.example.com/d/alerthub-production"
```

#### Step 2: Initial Assessment
```bash
# Gather system status
./scripts/deploy/production-validation-rollback.sh validate

# Check resource utilization
kubectl top nodes
kubectl top pods -n alerthub-production

# Check recent logs
kubectl logs -n alerthub-production deployment/alerthub-backend-prod --tail=100
kubectl logs -n alerthub-production deployment/ai-correlation-engine-prod --tail=100
```

#### Step 3: Immediate Actions
```bash
# If pods are failing, check resources
kubectl describe pods -n alerthub-production | grep -A 10 Events

# If performance issues, scale up
kubectl scale deployment ai-correlation-engine-prod --replicas=10 -n alerthub-production

# If complete outage, execute emergency procedures
./scripts/deploy/disaster-recovery/failover-procedure.sh
```

#### Step 4: Communication
```bash
# Send status update
curl -X POST $SLACK_INCIDENT_WEBHOOK \
  -H "Content-Type: application/json" \
  -d '{"text": "🚨 AlertHub Incident - Investigating [BRIEF_DESCRIPTION]"}'
```

#### Step 5: Resolution and Post-Mortem
- Document root cause
- Implement permanent fix
- Update monitoring/alerting
- Conduct post-mortem meeting

---

## Troubleshooting Guide

### Common Issues and Solutions

#### Issue: High Memory Usage
```bash
# Symptoms: Pods being OOMKilled
kubectl get events -n alerthub-production | grep OOMKilled

# Solution 1: Increase memory limits
kubectl patch deployment ai-correlation-engine-prod -n alerthub-production -p '{"spec":{"template":{"spec":{"containers":[{"name":"ai-correlation-engine","resources":{"limits":{"memory":"8Gi"}}}]}}}}'

# Solution 2: Scale horizontally
kubectl scale deployment ai-correlation-engine-prod --replicas=8 -n alerthub-production
```

#### Issue: Database Connection Failures
```bash
# Check database connectivity
kubectl run db-test --image=postgres:14-alpine --rm -it --restart=Never -n alerthub-production -- psql -h postgres-primary -U alerthub -d alerthub_production -c "SELECT 1;"

# Check database pod status
kubectl get pods -n alerthub-production | grep postgres

# Check database logs
kubectl logs -n alerthub-production statefulset/postgres-primary --tail=100
```

#### Issue: AI Services Not Responding
```bash
# Check AI service pods
kubectl get pods -n alerthub-production -l app=ai-correlation-engine

# Check resource constraints
kubectl describe pods -n alerthub-production -l app=ai-correlation-engine | grep -A 5 Conditions

# Restart AI services
kubectl rollout restart deployment/ai-correlation-engine-prod -n alerthub-production
kubectl rollout restart deployment/autonomous-ai-correlation-prod -n alerthub-production

# Scale if needed
kubectl scale deployment ai-correlation-engine-prod --replicas=8 -n alerthub-production
```

#### Issue: Kafka Message Lag
```bash
# Check Kafka lag
kubectl exec -n alerthub-production kafka-0 -- kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group alerthub-group

# Scale consumers
kubectl scale deployment alerthub-backend-prod --replicas=6 -n alerthub-production
```

#### Issue: Redis Connection Issues
```bash
# Test Redis connectivity
kubectl run redis-test --image=redis:7-alpine --rm -it --restart=Never -n alerthub-production -- redis-cli -h redis-cluster -p 6379 ping

# Check Redis cluster status
kubectl exec -n alerthub-production redis-cluster-0 -- redis-cli cluster info
```

#### Issue: Frontend Not Loading
```bash
# Check frontend pods
kubectl get pods -n alerthub-production -l app=alerthub-frontend-enhanced

# Check ingress configuration
kubectl describe ingress -n alerthub-production

# Check backend API
curl -f https://alerthub-production.k.example.com/api/health
```

#### Issue: High CPU Usage
```bash
# Identify high CPU pods
kubectl top pods -n alerthub-production --sort-by=cpu

# Scale affected services
kubectl scale deployment ai-correlation-engine-prod --replicas=10 -n alerthub-production

# Check for resource limits
kubectl describe deployment -n alerthub-production ai-correlation-engine-prod | grep -A 10 resources
```

---

## Maintenance Procedures

### Planned Maintenance Window (Monthly - 1st Sunday 2-6 AM)

#### Pre-Maintenance Checklist
```bash
# 1. Notify stakeholders
curl -X POST $SLACK_MAINTENANCE_WEBHOOK \
  -H "Content-Type: application/json" \
  -d '{"text": "🔧 AlertHub Maintenance Window Starting - Expected 4 hours"}'

# 2. Create maintenance backup
kubectl create job manual-backup-$(date +%Y%m%d) --from=cronjob/postgres-backup -n alerthub-production

# 3. Scale down non-critical services
kubectl scale deployment vector-embedding-service --replicas=1 -n alerthub-production
```

#### During Maintenance
```bash
# 1. Update container images
kubectl set image deployment/alerthub-backend-prod alerthub-backend=ghcr.io/aileron-platform/aileron-admins/alerthub-backend:v2.5.0 -n alerthub-production

# 2. Apply configuration updates
kubectl apply -f k8s/production-optimized-deployment.yaml

# 3. Update secrets if needed
kubectl apply -f k8s/production-secrets-management.yaml

# 4. Database migrations
kubectl run db-migrate --image=alerthub-migrate:latest --rm -it --restart=Never -n alerthub-production

# 5. Verify services
./scripts/deploy/production-validation-rollback.sh full
```

#### Post-Maintenance Checklist
```bash
# 1. Scale services back to normal
kubectl scale deployment vector-embedding-service --replicas=2 -n alerthub-production

# 2. Run full validation
./scripts/deploy/production-validation-rollback.sh full --integration --load-test

# 3. Monitor for 30 minutes
watch kubectl get pods -n alerthub-production

# 4. Notify completion
curl -X POST $SLACK_MAINTENANCE_WEBHOOK \
  -H "Content-Type: application/json" \
  -d '{"text": "✅ AlertHub Maintenance Completed Successfully"}'
```

### Emergency Maintenance

#### Immediate Security Patch
```bash
# 1. Apply security updates immediately
kubectl set image deployment/alerthub-backend-prod alerthub-backend=ghcr.io/aileron-platform/aileron-admins/alerthub-backend:security-patch-latest -n alerthub-production

# 2. Force restart all services
kubectl rollout restart deployment -n alerthub-production

# 3. Monitor deployment
kubectl rollout status deployment -n alerthub-production

# 4. Validate security fix
./scripts/security/validate-security-patch.sh
```

---

## Performance Monitoring

### Key Performance Indicators (KPIs)

#### Service Level Objectives (SLOs)
- **Availability**: 99.9% (43.2 minutes downtime per month)
- **Response Time**: 95% of requests < 500ms
- **Correlation Accuracy**: >95% for known patterns
- **Alert Processing**: <30 seconds end-to-end

#### Critical Metrics to Monitor

```bash
# 1. Response Time Monitoring
curl -w "@curl-format.txt" -s -o /dev/null https://alerthub-production.k.example.com/api/health

# 2. Correlation Engine Performance
kubectl exec -n alerthub-production deployment/ai-correlation-engine-prod -- curl -s http://localhost:8002/metrics | grep correlation_processing_time

# 3. Database Performance
kubectl exec -n alerthub-production postgres-primary-0 -- psql -U alerthub -d alerthub_production -c "SELECT * FROM pg_stat_activity WHERE state = 'active';"

# 4. Cache Hit Ratio
kubectl exec -n alerthub-production redis-cluster-0 -- redis-cli info stats | grep cache_hit_ratio
```

### Performance Tuning Actions

#### When Response Time > 1s
```bash
# Scale critical services
kubectl scale deployment alerthub-backend-prod --replicas=6 -n alerthub-production
kubectl scale deployment ai-correlation-engine-prod --replicas=8 -n alerthub-production

# Increase resource limits
kubectl patch deployment alerthub-backend-prod -n alerthub-production -p '{"spec":{"template":{"spec":{"containers":[{"name":"alerthub-backend","resources":{"limits":{"cpu":"2000m","memory":"4Gi"}}}]}}}}'
```

#### When Memory Usage > 80%
```bash
# Add horizontal scaling
kubectl patch hpa ai-correlation-hpa-prod -n alerthub-production -p '{"spec":{"maxReplicas":15}}'

# Increase memory limits
kubectl patch deployment ai-correlation-engine-prod -n alerthub-production -p '{"spec":{"template":{"spec":{"containers":[{"name":"ai-correlation-engine","resources":{"limits":{"memory":"8Gi"}}}]}}}}'
```

#### When CPU Usage > 75%
```bash
# Scale up replicas
kubectl scale deployment autonomous-ai-correlation-prod --replicas=5 -n alerthub-production

# Increase CPU limits
kubectl patch deployment autonomous-ai-correlation-prod -n alerthub-production -p '{"spec":{"template":{"spec":{"containers":[{"name":"autonomous-ai-correlation","resources":{"limits":{"cpu":"3000m"}}}]}}}}'
```

---

## Security Operations

### Security Monitoring Checklist (Daily)

```bash
# 1. Check for unauthorized access attempts
kubectl logs -n alerthub-production deployment/alerthub-backend-prod | grep -i "unauthorized\|forbidden\|401\|403"

# 2. Monitor certificate expiration
kubectl get secrets -n alerthub-production -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.data.tls\.crt}{"\n"}{end}' | while read name cert; do
  if [ -n "$cert" ]; then
    echo "$name: $(echo $cert | base64 -d | openssl x509 -noout -enddate)"
  fi
done

# 3. Check for security policy violations
kubectl get networkpolicies -n alerthub-production
kubectl describe pod -n alerthub-production | grep "Security Context"

# 4. Review RBAC permissions
kubectl auth can-i --list --as=system:serviceaccount:alerthub-production:alerthub-production-sa -n alerthub-production
```

### Security Incident Response

#### Suspected Security Breach
```bash
# 1. Immediate isolation
kubectl patch networkpolicy default-deny -n alerthub-production -p '{"spec":{"policyTypes":["Ingress","Egress"],"ingress":[],"egress":[]}}'

# 2. Gather evidence
kubectl get events -n alerthub-production --sort-by='.lastTimestamp'
kubectl logs -n alerthub-production --all-containers=true --since=1h > security-incident-logs-$(date +%Y%m%d-%H%M).log

# 3. Notify security team
curl -X POST $SECURITY_ALERT_WEBHOOK \
  -H "Content-Type: application/json" \
  -d '{"text": "🚨 SECURITY INCIDENT - AlertHub Production - Immediate Investigation Required"}'

# 4. Preserve forensic evidence
kubectl get pods -n alerthub-production -o yaml > incident-pod-state-$(date +%Y%m%d-%H%M).yaml
```

---

## Backup and Recovery

### Backup Verification (Daily)

```bash
#!/bin/bash
# Daily backup verification

echo "AlertHub Backup Verification - $(date)"
echo "===================================="

# 1. Check database backups
LATEST_DB_BACKUP=$(aws s3 ls s3://alerthub-backups/database/ | sort | tail -1 | awk '{print $4}')
if [ -n "$LATEST_DB_BACKUP" ]; then
  echo "✅ Latest database backup: $LATEST_DB_BACKUP"
else
  echo "❌ No database backup found"
fi

# 2. Check Redis backups
LATEST_REDIS_BACKUP=$(aws s3 ls s3://alerthub-backups/redis/ | sort | tail -1 | awk '{print $4}')
if [ -n "$LATEST_REDIS_BACKUP" ]; then
  echo "✅ Latest Redis backup: $LATEST_REDIS_BACKUP"
else
  echo "❌ No Redis backup found"
fi

# 3. Check configuration backups
LATEST_CONFIG_BACKUP=$(aws s3 ls s3://alerthub-backups/config/ | sort | tail -1 | awk '{print $4}')
if [ -n "$LATEST_CONFIG_BACKUP" ]; then
  echo "✅ Latest config backup: $LATEST_CONFIG_BACKUP"
else
  echo "❌ No config backup found"
fi

# 4. Test backup integrity
echo "Testing backup integrity..."
aws s3 cp s3://alerthub-backups/database/$LATEST_DB_BACKUP /tmp/test-backup.sql
if [ $? -eq 0 ]; then
  echo "✅ Database backup download successful"
  rm -f /tmp/test-backup.sql
else
  echo "❌ Database backup download failed"
fi
```

### Disaster Recovery Procedures

#### Complete System Recovery
```bash
#!/bin/bash
# Complete system recovery from backups

echo "AlertHub Disaster Recovery - $(date)"
echo "=================================="

# 1. Restore database
LATEST_DB_BACKUP=$(aws s3 ls s3://alerthub-backups/database/ | sort | tail -1 | awk '{print $4}')
./scripts/deploy/disaster-recovery/restore-database.sh "$LATEST_DB_BACKUP" "alerthub_production"

# 2. Restore Redis
LATEST_REDIS_BACKUP=$(aws s3 ls s3://alerthub-backups/redis/ | sort | tail -1 | awk '{print $4}')
./scripts/deploy/disaster-recovery/restore-redis.sh "$LATEST_REDIS_BACKUP"

# 3. Apply latest configurations
kubectl apply -f k8s/production-optimized-deployment.yaml
kubectl apply -f k8s/production-secrets-management.yaml

# 4. Validate recovery
./scripts/deploy/production-validation-rollback.sh full --integration

echo "✅ Disaster recovery completed"
```

---

## Escalation Procedures

### Escalation Matrix

#### Level 1: On-Call Engineer (0-15 minutes)
- **Responsibility**: Initial response and basic troubleshooting
- **Authority**: Restart services, scale resources, basic configuration changes
- **Escalate when**: Unable to resolve within 30 minutes or severity increases

#### Level 2: Senior SRE (15-60 minutes)  
- **Responsibility**: Advanced troubleshooting and system analysis
- **Authority**: Database operations, infrastructure changes, rollbacks
- **Escalate when**: Root cause unclear or requires architectural changes

#### Level 3: Engineering Manager (1-4 hours)
- **Responsibility**: Coordinate with development teams, major decisions
- **Authority**: Emergency maintenance, resource allocation, external coordination
- **Escalate when**: Business impact exceeds thresholds or customer escalation

#### Level 4: VP Engineering (4+ hours)
- **Responsibility**: Executive decision making, customer communication
- **Authority**: All technical and business decisions
- **Escalate when**: Major business impact or regulatory implications

### Escalation Commands

```bash
# Auto-escalate based on duration
INCIDENT_START_TIME=$(date +%s)
CURRENT_TIME=$(date +%s)
DURATION=$((CURRENT_TIME - INCIDENT_START_TIME))

if [ $DURATION -gt 1800 ]; then  # 30 minutes
  curl -X POST $PAGERDUTY_ESCALATION_WEBHOOK \
    -H "Content-Type: application/json" \
    -d '{"incident_id":"'$INCIDENT_ID'","action":"escalate","level":"senior-sre"}'
fi

if [ $DURATION -gt 3600 ]; then  # 1 hour
  curl -X POST $PAGERDUTY_ESCALATION_WEBHOOK \
    -H "Content-Type: application/json" \
    -d '{"incident_id":"'$INCIDENT_ID'","action":"escalate","level":"engineering-manager"}'
fi
```

---

## Emergency Contacts

### Primary Contacts
- **On-Call Engineer**: +1-XXX-XXX-XXXX (PagerDuty rotation)
- **SRE Manager**: engineering-sre-manager@example.com
- **Development Lead**: alerthub-dev-lead@example.com
- **Product Manager**: alerthub-pm@example.com

### Vendor Contacts
- **Cloud Provider**: AWS Enterprise Support (+1-XXX-XXX-XXXX)
- **Database Support**: PostgreSQL Enterprise (+1-XXX-XXX-XXXX)
- **Monitoring Support**: Datadog Enterprise (+1-XXX-XXX-XXXX)

### Internal Teams
- **Security Operations**: security-ops@example.com
- **Network Operations**: network-ops@example.com
- **Database Team**: dba-team@example.com
- **Platform Engineering**: platform-eng@example.com

---

## Appendices

### A. Environment Variables Reference
```bash
# Production environment variables
export NAMESPACE="alerthub-production"
export CLUSTER_NAME="alerthub-prod-cluster"
export GRAFANA_URL="https://grafana.monitoring.example.com"
export PROMETHEUS_URL="https://prometheus.monitoring.example.com"
export SLACK_WEBHOOK="https://hooks.slack.com/services/..."
export PAGERDUTY_API_KEY="your-pagerduty-api-key"
```

### B. Useful Commands Reference
```bash
# Quick health check
kubectl get pods -n alerthub-production | grep -v Running

# Resource usage overview
kubectl top pods -n alerthub-production --sort-by=memory

# Recent events
kubectl get events -n alerthub-production --sort-by='.lastTimestamp' | tail -10

# Service logs
kubectl logs -n alerthub-production deployment/alerthub-backend-prod --tail=50

# Scale service
kubectl scale deployment ai-correlation-engine-prod --replicas=8 -n alerthub-production

# Restart service
kubectl rollout restart deployment/alerthub-backend-prod -n alerthub-production

# Check HPA status
kubectl get hpa -n alerthub-production

# Port forwarding for debugging
kubectl port-forward -n alerthub-production service/alerthub-backend-prod 8080:3000
```

### C. Monitoring Queries (Prometheus)
```promql
# Application availability
up{namespace="alerthub-production"}

# Request rate
rate(http_requests_total{namespace="alerthub-production"}[5m])

# Error rate
rate(http_requests_total{namespace="alerthub-production",status=~"5.."}[5m]) / rate(http_requests_total{namespace="alerthub-production"}[5m])

# Response time
histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{namespace="alerthub-production"}[5m]))

# Memory usage
container_memory_usage_bytes{namespace="alerthub-production"} / container_spec_memory_limit_bytes{namespace="alerthub-production"}

# CPU usage
rate(container_cpu_usage_seconds_total{namespace="alerthub-production"}[5m]) / container_spec_cpu_quota{namespace="alerthub-production"}
```

---

**Document Version**: v1.0
**Last Updated**: 2024-04-14
**Next Review**: 2024-05-14