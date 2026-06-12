# AlertHub Enterprise - Complete Local Setup Guide

## 🚀 Quick Start

AlertHub Enterprise is a complete autonomous AIOps correlation platform. This local setup demonstrates the full capabilities including:

- **Autonomous AI Correlation** - Automatically correlates related alerts into incidents
- **Vector-based Semantic Analysis** - Uses machine learning for intelligent alert correlation  
- **Real-time Processing** - Processes alerts and creates incidents in real-time
- **Infrastructure Topology** - Maps service dependencies for better correlation
- **Monitoring & Observability** - Complete metrics, logs, and tracing

## 📋 Prerequisites

- **Docker** (20.10+) and **Docker Compose** (v2.0+)
- **8GB+ RAM** (16GB recommended for optimal performance)
- **10GB+ free disk space**
- **macOS, Linux, or Windows with WSL2**

## 🎯 One-Command Setup

```bash
# Start complete AlertHub Enterprise environment
./scripts/start-alerthub-local.sh start
```

This single command will:
1. ✅ Check system prerequisites
2. 🐳 Pull and build all Docker images
3. 🗄️ Start infrastructure services (PostgreSQL, Redis, Kafka, Neo4j, Weaviate)
4. 📊 Start monitoring stack (Prometheus, Grafana, Jaeger)
5. 🤖 Start AI services (Vector Embedding, Autonomous Correlation, AI Investigation)
6. 🏗️ Start core AlertHub backend
7. 🎭 Start sample data generator for demonstration
8. 🔍 Run health checks on all services

## 🌐 Service Access Points

### Core Platform
- **AlertHub Backend**: http://localhost:3001
  - Health Check: http://localhost:3001/health
  - API Docs: http://localhost:3001/api/v1
  - Ready Check: http://localhost:3001/ready

### AI & Correlation Services  
- **Vector Embedding Service**: http://localhost:8003
  - API: http://localhost:8003/api/embeddings/generate
  - Models: http://localhost:8003/api/models
- **Autonomous Correlation**: http://localhost:8005  
  - Dashboard Stats: http://localhost:8005/api/stats/dashboard
  - Phase 2 Status: http://localhost:8005/api/phase2/status
- **AI Investigation Engine**: http://localhost:8004
  - Investigation API: http://localhost:8004/investigate/autonomous
  - Analytics: http://localhost:8004/analytics/performance
- **Topology Graph Service**: http://localhost:8006
  - Graph API: http://localhost:8006/api/topology
- **Learning Engine**: http://localhost:8007
  - ML Models: http://localhost:8007/api/models

### Monitoring & Observability
- **Prometheus**: http://localhost:9090
  - Metrics: http://localhost:9090/targets
- **Grafana**: http://localhost:3000
  - Login: `admin` / `admin123`
  - Dashboards: Automatically provisioned
- **Jaeger Tracing**: http://localhost:16686
  - Distributed traces for all services

### Infrastructure Services
- **PostgreSQL**: localhost:5432
  - Database: `alerthub` / `alerthub` / `alerthub123`
- **Redis**: localhost:6379
- **Kafka**: localhost:9092  
- **Neo4j**: http://localhost:7474
  - Login: `neo4j` / `alerthub123`
- **Weaviate**: http://localhost:8080

## 🎭 Autonomous AIOps Demonstration

### Live Correlation in Action

The system automatically generates and correlates alerts to demonstrate autonomous AIOps:

1. **Sample Data Generator** creates realistic alerts every 30 seconds
2. **Autonomous Correlation Service** analyzes each alert using 4 strategies:
   - **Service-based correlation** (40% weight)
   - **Semantic similarity** (30% weight) 
   - **Temporal proximity** (20% weight)
   - **Severity alignment** (10% weight)
3. **AI Investigation Engine** automatically investigates incidents
4. **Learning Engine** continuously improves correlation accuracy

### Correlation Strategies Demonstrated

#### 1. Service-Based Correlation
```bash
# Example: Database alerts correlating with dependent services
curl -X POST http://localhost:3001/api/v1/webhooks/alerts \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "title": "High database CPU usage detected",
      "severity": "critical",
      "labels": {"service": "database-cluster"},
      "metadata": {"cpu_usage": 95.2}
    }]
  }'
```

#### 2. Semantic Similarity
The vector embedding service finds semantically related alerts:
```bash
# Test semantic similarity
curl -X POST http://localhost:8003/api/similarity \
  -H "Content-Type: application/json" \
  -d '{
    "text1": "Database connection pool exhausted",
    "text2": "High database connection count detected"
  }'
```

#### 3. Temporal Proximity
Alerts occurring within correlation windows are automatically grouped.

#### 4. Severity Alignment
Critical alerts enhance correlation scores for related medium/high severity alerts.

### Real-time Monitoring

Monitor autonomous correlation in real-time:

```bash
# View correlation statistics
curl http://localhost:8005/api/stats/dashboard

# Monitor active investigations  
curl http://localhost:8004/analytics/performance

# Check vector embedding cache performance
curl http://localhost:8003/api/stats
```

## 🔍 Testing Correlation Scenarios

### Generate Correlated Alert Scenarios

1. **Database Cascade Failure**
```bash
# This creates a realistic scenario where database issues cascade
curl -X POST http://localhost:3001/api/v1/ai/correlate \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Database performance degradation",
    "severity": "critical", 
    "labels": {"service": "database-cluster"},
    "metadata": {"correlation_group": "db-cascade-demo"}
  }'
```

2. **Network Partition Scenario**
```bash
# Simulates network issues affecting multiple services
# The sample data generator creates these automatically every few minutes
```

3. **Memory Pressure Cascade**
```bash
# Memory issues leading to service degradation
# Watch the correlation happen in real-time via Grafana
```

### Autonomous Investigation

Watch AI investigation in action:
```bash
# WebSocket connection for real-time investigation streaming
wscat -c ws://localhost:8004/stream/investigation-id-here

# Or check investigation status
curl http://localhost:8004/investigations
```

## 📊 Observability & Monitoring

### Grafana Dashboards

Access pre-configured dashboards at http://localhost:3000:

1. **AlertHub Overview** - System-wide metrics
2. **Correlation Performance** - AI correlation accuracy and performance
3. **Service Health** - All microservices health status
4. **Infrastructure Metrics** - Database, Redis, Kafka performance
5. **AI Services Dashboard** - Vector embeddings, investigation metrics

### Prometheus Metrics

Key metrics available at http://localhost:9090:

- `correlation_requests_total` - Total correlation requests
- `correlation_accuracy_score` - Current correlation accuracy
- `embedding_requests_total` - Vector embedding requests
- `investigation_duration_seconds` - AI investigation times
- `alert_processing_duration_seconds` - Alert processing latency

### Distributed Tracing

View request flows across all services at http://localhost:16686:

- Search by service: `autonomous-correlation`, `vector-embedding`
- Trace correlation workflows end-to-end
- Identify performance bottlenecks

## 🛠️ Management Commands

### Service Control
```bash
# Start all services
./scripts/start-alerthub-local.sh start

# Stop all services  
./scripts/start-alerthub-local.sh stop

# Restart all services
./scripts/start-alerthub-local.sh restart

# Check service health
./scripts/start-alerthub-local.sh status

# View service logs
./scripts/start-alerthub-local.sh logs [service-name]
```

### Individual Service Management
```bash
# Start specific services
docker-compose -f docker/docker-compose.complete-local.yaml up -d postgres-alerthub redis-alerthub

# View logs for specific service
docker-compose -f docker/docker-compose.complete-local.yaml logs -f autonomous-correlation

# Scale services
docker-compose -f docker/docker-compose.complete-local.yaml up -d --scale vector-embedding=2
```

### Demonstration Commands
```bash
# Generate test correlation scenario
curl -X POST http://localhost:8005/api/correlation/autonomous \
  -H "Content-Type: application/json" \
  -d '{"alert_id": "test-123", "alert_data": {"title": "Test alert", "service": "demo"}}'

# Force learning engine training
curl -X POST http://localhost:8007/api/training/trigger

# Reset correlation statistics
curl -X POST http://localhost:8005/api/stats/reset
```

## 🔧 Configuration

### Environment Variables

Key configuration options in `.env` (automatically generated):

```bash
# Development Mode
DEV_MODE=true
ENV=development

# Database  
DB_HOST=postgres-alerthub
DB_NAME=alerthub
DB_USER=alerthub
DB_PASSWORD=alerthub123

# AI Services
CORRELATION_THRESHOLD=0.7
SHADOW_MODE=false
SAMPLE_DATA_ENABLED=true

# Monitoring
PROMETHEUS_URL=http://prometheus-alerthub:9090
GRAFANA_URL=http://grafana-alerthub:3000
```

### AI Correlation Tuning

Adjust correlation behavior:
```bash
# Increase correlation sensitivity
curl -X PUT http://localhost:8005/api/config \
  -H "Content-Type: application/json" \
  -d '{"correlation_threshold": 0.6}'

# Enable shadow mode for testing
curl -X PUT http://localhost:8005/api/config \
  -H "Content-Type: application/json" \
  -d '{"shadow_mode": true}'
```

## 🐛 Troubleshooting

### Common Issues

#### Services Not Starting
```bash
# Check Docker daemon
docker info

# Check available resources
docker system df
docker system prune -f  # Clean up if needed

# Check logs
./scripts/start-alerthub-local.sh logs
```

#### Correlation Not Working
```bash
# Check autonomous correlation service
curl http://localhost:8005/health

# Verify backend connectivity  
curl http://localhost:3001/ready

# Check correlation statistics
curl http://localhost:8005/api/stats/dashboard
```

#### Database Connection Issues
```bash
# Check PostgreSQL health
docker exec alerthub-postgres pg_isready -U alerthub

# View database logs
docker logs alerthub-postgres

# Connect to database
docker exec -it alerthub-postgres psql -U alerthub -d alerthub
```

#### Memory/Performance Issues
```bash
# Check resource usage
docker stats

# Reduce services if needed
docker-compose -f docker/docker-compose.complete-local.yaml stop learning-engine ai-investigation
```

### Service Health Checks

```bash
# Automated health check for all services
curl -s http://localhost:3001/ready | jq
curl -s http://localhost:8005/health | jq
curl -s http://localhost:8003/health | jq
curl -s http://localhost:8004/health | jq
curl -s http://localhost:8006/health | jq
curl -s http://localhost:9090/-/healthy
```

### Log Analysis

```bash
# View correlation decisions
docker logs alerthub-autonomous-correlation | grep "correlation"

# Monitor alert processing
docker logs alerthub-backend | grep "webhook"

# Check AI service performance
docker logs alerthub-vector-embedding | grep "embedding"
```

## 📈 Performance Optimization

### For Better Performance

1. **Allocate More Memory**
   ```bash
   # Increase Docker memory to 8GB+ in Docker Desktop
   ```

2. **Use SSD Storage**
   ```bash
   # Store Docker volumes on SSD for better I/O performance
   ```

3. **Tune Service Resources**
   ```yaml
   # Increase resources in docker-compose.complete-local.yaml
   services:
     autonomous-correlation:
       deploy:
         resources:
           limits:
             memory: 2G
             cpus: '1.0'
   ```

### Production Considerations

- Use external managed databases (RDS PostgreSQL, ElastiCache Redis)
- Deploy AI services on GPU-enabled instances
- Scale services horizontally using Kubernetes
- Implement proper security (OAuth, TLS, secrets management)
- Configure backup and disaster recovery

## 🎯 Next Steps

1. **Explore Correlation Rules**
   - Modify correlation thresholds
   - Add custom correlation strategies
   - Train models with your own data

2. **Integrate Real Monitoring**
   - Connect Dynatrace, Datadog, or Prometheus
   - Configure real alert sources
   - Set up production workflows

3. **Scale for Production**
   - Review Kubernetes manifests in `k8s/`
   - Configure enterprise authentication
   - Set up multi-region deployment

4. **Customize AI Models**
   - Train custom vector embedding models
   - Implement domain-specific correlation logic
   - Add new investigation tools

## 📚 Additional Resources

- [API Documentation](docs/API_GUIDE.md)
- [Correlation Algorithm Details](docs/CORRELATION_ALGORITHMS.md)
- [Architecture Overview](docs/ARCHITECTURE.md)
- [Production Deployment Guide](docs/PRODUCTION_DEPLOYMENT.md)
- [Troubleshooting Guide](docs/TROUBLESHOOTING.md)

## 🤝 Support

For issues, questions, or contributions:

- Check service health: `./scripts/start-alerthub-local.sh status`
- View logs: `./scripts/start-alerthub-local.sh logs`
- Reset environment: `./scripts/start-alerthub-local.sh stop && ./scripts/start-alerthub-local.sh start`

---

**🎉 Congratulations!** You now have a complete, autonomous AIOps correlation platform running locally with real-time alert processing, AI-powered correlation, and comprehensive monitoring.