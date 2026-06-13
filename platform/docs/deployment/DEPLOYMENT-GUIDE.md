# Enterprise AlertHub - Deployment Guide

## 🚀 Production Deployment

### Prerequisites
- PostgreSQL 14+
- Go 1.21+
- Redis (for session management)
- LDAP/AD server (optional)
- AI Service endpoint (optional)

## Quick Deploy

### 1. Database Setup
```bash
# Create database
createdb alerthub_production

# Run schema
psql alerthub_production < database/schema.sql

# Verify tables
psql alerthub_production -c "\dt"
```

### 2. Environment Configuration
```bash
# Create production config
cat > .env.production << EOF
# Database
DB_HOST=postgres.example.com
DB_PORT=5432
DB_NAME=alerthub_production
DB_USER=alerthub_app
DB_PASSWORD=\${DB_PASSWORD}
DB_SSL_MODE=require

# Server
PORT=3000
ENV=production
LOG_LEVEL=info

# JWT
JWT_SECRET=\${JWT_SECRET}
JWT_REFRESH_SECRET=\${JWT_REFRESH_SECRET}
JWT_ACCESS_TTL=24h
JWT_REFRESH_TTL=7d

# LDAP
LDAP_ENABLED=true
LDAP_SERVER=ldap.example.com
LDAP_PORT=636
LDAP_BASE_DN=dc=example,dc=com
LDAP_BIND_DN=cn=alerthub,ou=services,dc=example,dc=com
LDAP_BIND_PASSWORD=\${LDAP_PASSWORD}
LDAP_USER_FILTER=(sAMAccountName=%s)
LDAP_USE_TLS=true

# AI Service
AI_SERVICE_URL=https://ai.alerthub.example.com
AI_API_KEY=\${AI_API_KEY}

# Notifications
SLACK_WEBHOOK_URL=\${SLACK_WEBHOOK}
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=alerts@example.com
SMTP_PASSWORD=\${SMTP_PASSWORD}

# Redis
REDIS_HOST=redis.example.com
REDIS_PORT=6379
REDIS_PASSWORD=\${REDIS_PASSWORD}
EOF
```

### 3. Build Application
```bash
# Build binary
go build -o alerthub-server cmd/alerthub/main.go

# Or build Docker image
docker build -t alerthub:latest -f Dockerfile .
```

### 4. Deploy

#### Option A: Direct Deployment
```bash
# Run server
./alerthub-server
```

#### Option B: Docker
```bash
docker run -d \
  --name alerthub \
  -p 3000:3000 \
  --env-file .env.production \
  alerthub:latest
```

#### Option C: Kubernetes
```bash
# Apply manifests
kubectl apply -f k8s/alerthub-deployment.yaml
kubectl apply -f k8s/alerthub-service.yaml
kubectl apply -f k8s/alerthub-ingress.yaml
```

### 5. Verify Deployment
```bash
# Health check
curl https://alerthub.example.com/health

# Login test
curl -X POST https://alerthub.example.com/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"Admin@123"}'
```

## 🔐 Security Checklist

### Before Production
- [ ] Change default admin password
- [ ] Generate strong JWT secrets
- [ ] Configure SSL/TLS certificates
- [ ] Enable LDAP/SSO
- [ ] Set up firewall rules
- [ ] Configure rate limiting
- [ ] Enable audit logging
- [ ] Set up backup strategy
- [ ] Configure monitoring
- [ ] Review RBAC permissions

### Recommended Settings
```bash
# Strong JWT secret (32+ characters)
JWT_SECRET=$(openssl rand -base64 32)
JWT_REFRESH_SECRET=$(openssl rand -base64 32)

# Database connection pooling
DB_MAX_CONNECTIONS=100
DB_MAX_IDLE=10
DB_CONN_MAX_LIFETIME=1h

# Rate limiting
RATE_LIMIT_REQUESTS_PER_MINUTE=1000
RATE_LIMIT_BURST=5000

# Session timeout
SESSION_TIMEOUT=24h
SESSION_IDLE_TIMEOUT=2h
```

## 📊 Monitoring & Observability

### Metrics to Monitor
- API response times
- Alert ingestion rate
- Notification delivery rate
- AI analysis latency
- Database connection pool
- Authentication success/failure rate
- Active user sessions

### Prometheus Metrics
```yaml
# AlertHub exposes metrics at /metrics
scrape_configs:
  - job_name: 'alerthub'
    static_configs:
      - targets: ['alerthub.example.com:3000']
```

### Key Metrics
- `alerthub_alerts_total` - Total alerts received
- `alerthub_alerts_by_severity` - Alerts by severity
- `alerthub_api_requests_total` - API requests
- `alerthub_api_duration_seconds` - API latency
- `alerthub_auth_attempts_total` - Auth attempts
- `alerthub_notifications_sent_total` - Notifications sent
- `alerthub_ai_analysis_duration_seconds` - AI analysis time

## 🔄 Backup & Recovery

### Database Backup
```bash
# Daily backup
pg_dump alerthub_production > backup_$(date +%Y%m%d).sql

# Automated backup script
0 2 * * * /usr/local/bin/backup-alerthub.sh
```

### Restore
```bash
# Restore from backup
psql alerthub_production < backup_20260116.sql
```

## 📈 Scaling

### Horizontal Scaling
```yaml
# Kubernetes HPA
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: alerthub
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: alerthub
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
```

### Database Scaling
- Read replicas for queries
- Connection pooling
- Query optimization
- Partitioning for large tables

## 🔧 Maintenance

### Regular Tasks
- **Daily**: Check logs, monitor metrics
- **Weekly**: Review audit logs, check disk space
- **Monthly**: Update dependencies, security patches
- **Quarterly**: Performance review, capacity planning

### Health Checks
```bash
# Application health
curl https://alerthub.example.com/health

# Database health
curl https://alerthub.example.com/health/db

# AI service health
curl https://alerthub.example.com/health/ai
```

## 📞 Support

### Production Issues
1. Check application logs
2. Check database connections
3. Verify external service connectivity
4. Review recent changes
5. Contact: alerthub-support@example.com

### Emergency Contacts
- **Slack**: #alerthub-oncall
- **PagerDuty**: AlertHub Service
- **Phone**: +1-XXX-XXX-XXXX

## 🎯 Post-Deployment

### Initial Setup
1. Login as admin
2. Change admin password
3. Create user accounts
4. Assign roles
5. Configure LDAP/SSO
6. Set up notification channels
7. Create notification rules
8. Generate API keys for integrations
9. Test alert ingestion
10. Configure maintenance windows

### Integration Testing
```bash
# Test alert ingestion
curl -X POST https://alerthub.example.com/api/v1/alerts/ingest \
  -H "X-API-Key: test-key" \
  -d '{"title":"Test Alert","severity":"info","source":"test"}'

# Test LDAP
curl -X POST https://alerthub.example.com/api/v1/auth/login/ldap \
  -d '{"username":"testuser","password":"testpass"}'

# Test notification
curl -X POST https://alerthub.example.com/api/v1/notifications/test \
  -H "Authorization: Bearer <token>" \
  -d '{"channel_id":"<channel-id>"}'
```

## 📋 Checklist

### Pre-Production
- [x] Database schema deployed
- [x] Application built
- [x] Environment configured
- [ ] SSL certificates installed
- [ ] LDAP configured
- [ ] Notification channels set up
- [ ] Monitoring configured
- [ ] Backup strategy implemented
- [ ] Load testing completed
- [ ] Security audit passed

### Go-Live
- [ ] DNS configured
- [ ] Load balancer configured
- [ ] Health checks passing
- [ ] Monitoring active
- [ ] Team trained
- [ ] Documentation published
- [ ] Support process defined

---

**Enterprise AlertHub v1.0**  
**Production Ready** ✅  
**Last Updated**: 2026-01-16
