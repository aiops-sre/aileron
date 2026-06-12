# AlertHub Enterprise - Production Alert Correlation & Incident Management

## 🚀 Productionization Complete

This document outlines the complete productionization of your AlertHub Enterprise system with automatic alert correlation and incident management.

## ✅ What's Been Implemented

### 1. Automatic Alert Correlation Pipeline
- **Location**: [`internal/services/correlation/auto_correlation_service.go`](internal/services/correlation/auto_correlation_service.go)
- **Features**:
  - Real-time alert correlation with ML similarity scoring
  - Rule-based correlation with configurable conditions
  - Duplicate detection and suppression
  - Confidence scoring for correlation decisions
  - Automatic incident creation from correlated alerts

### 2. Enhanced Incident Management Integration
- **Location**: [`internal/services/integration/alert_incident_integration.go`](internal/services/integration/alert_incident_integration.go)
- **Features**:
  - Complete alert-to-incident pipeline
  - Automatic incident creation from correlated alerts
  - Workflow integration for automation
  - Support for multiple alert sources (Dynatrace, Prometheus, Grafana)
  - End-to-end testing capabilities

### 3. Production Webhook Handlers
- **Location**: [`internal/api/handlers/production_webhooks.go`](internal/api/handlers/production_webhooks.go)
- **Endpoints**:
  - `/api/v1/webhooks/dynatrace` - Dynatrace alert webhooks
  - `/api/v1/webhooks/prometheus` - Prometheus alertmanager webhooks
  - `/api/v1/webhooks/grafana` - Grafana alert webhooks
  - `/api/v1/webhooks/generic` - Generic alert webhooks
  - `/api/v1/webhooks/test` - End-to-end pipeline testing

### 4. Kubernetes Topology Discovery with Service Accounts
- **Location**: [`internal/services/topology/k8s_topology_service.go`](internal/services/topology/k8s_topology_service.go)
- **Features**:
  - Multi-cluster topology discovery using service account tokens
  - Background discovery with configurable intervals
  - Real-time cluster health monitoring
  - Automatic service mapping
  - Cross-cluster relationship detection

### 5. Enhanced Admin Topology Configuration
- **Location**: [`sre-command-center/src/pages/TopologyConfigurationPage.tsx`](sre-command-center/src/pages/TopologyConfigurationPage.tsx)
- **Features**:
  - K8s cluster configuration management
  - Service account token configuration
  - Environment-specific discovery settings
  - Service mapping management
  - Real-time health monitoring
  - Discovery triggering and scheduling

### 6. Database Schema Enhancements
- **Location**: [`database/migrations/k8s_topology_config.sql`](database/migrations/k8s_topology_config.sql)
- **Tables**:
  - `k8s_cluster_configs` - Cluster configurations with service account tokens
  - `k8s_topology_snapshots` - Topology discovery results
  - `k8s_services_discovered` - Discovered K8s services
  - `topology_environment_configs` - Environment-specific settings
  - `service_discovery_mappings` - Service mapping configurations

## 🔧 Configuration Steps

### 1. Database Migration
```bash
# Apply the new database migrations
psql -h localhost -p 5432 -U postgres -d alerthub -f database/migrations/k8s_topology_config.sql
```

### 2. Configure Kubernetes Clusters
1. Access the SRE Command Center admin interface
2. Navigate to **Admin → Topology Configuration**
3. Click **Add Cluster** in the K8s Clusters tab
4. Configure each cluster with:
   - **Cluster Name**: e.g., `mps-sandbox-rno`
   - **Environment**: `production`/`staging`/`development`
   - **Region**: e.g., `reno`, `maiden`
   - **API Server URL**: Your cluster's API endpoint
   - **Service Account Token**: Read-only service account token
   - **CA Certificate**: Base64 encoded CA certificate
   - **Namespace**: Target namespace for discovery

### 3. Service Account Setup

For each Kubernetes cluster, create a read-only service account:

```yaml
# k8s-alerthub-readonly-rbac.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: alerthub-readonly
  namespace: alerthub-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: alerthub-readonly
rules:
- apiGroups: [""]
  resources: ["nodes", "namespaces", "services", "pods", "endpoints"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps"]
  resources: ["deployments", "replicasets", "statefulsets", "daemonsets"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["networking.k8s.io"]
  resources: ["ingresses", "networkpolicies"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: alerthub-readonly
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: alerthub-readonly
subjects:
- kind: ServiceAccount
  name: alerthub-readonly
  namespace: alerthub-system
---
apiVersion: v1
kind: Secret
metadata:
  name: alerthub-readonly-token
  namespace: alerthub-system
  annotations:
    kubernetes.io/service-account.name: alerthub-readonly
type: kubernetes.io/service-account-token
```

Get the service account token:
```bash
kubectl get secret alerthub-readonly-token -n alerthub-system -o jsonpath='{.data.token}' | base64 -d
```

### 4. Environment Configuration
Configure each environment in the admin interface:
- **Production**: Full automation enabled (5-minute discovery, auto-correlation, auto-incidents)
- **Staging**: Limited automation (10-minute discovery, correlation only)
- **Development**: Basic discovery (15-minute discovery, no automation)

### 5. Webhook Configuration

#### Dynatrace Setup
Configure webhook in Dynatrace to send alerts to:
```
https://your-alerthub-domain/api/v1/webhooks/dynatrace
```

#### Prometheus Setup
Configure Alertmanager webhook:
```yaml
global:
  resolve_timeout: 5m

route:
  group_by: ['alertname']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 1h
  receiver: 'alerthub'

receivers:
- name: 'alerthub'
  webhook_configs:
  - url: 'https://your-alerthub-domain/api/v1/webhooks/prometheus'
    send_resolved: true
```

#### Grafana Setup
Configure Grafana notification channel:
```json
{
  "name": "AlertHub",
  "type": "webhook", 
  "settings": {
    "url": "https://your-alerthub-domain/api/v1/webhooks/grafana",
    "httpMethod": "POST"
  }
}
```

## 🔄 How It Works

### Alert Processing Flow
1. **Alert Received** → Webhook handler receives alert from monitoring system
2. **Correlation Analysis** → Auto-correlation service analyzes alert for:
   - Exact duplicates (fingerprint matching)
   - Similar alerts (ML similarity scoring)
   - Rule-based correlation (user-defined rules)
3. **Decision Making** → System decides action based on correlation:
   - **Create New Incident** → No correlation found
   - **Correlate to Existing** → High confidence match with open incident
   - **Suppress Duplicate** → Exact duplicate detected
4. **Incident Management** → Enhanced incident service creates/updates incidents
5. **Workflow Automation** → Workflows triggered for notifications, escalations, etc.

### Topology Discovery Flow
1. **Background Discovery** → Service runs every N minutes per environment config
2. **Multi-Cluster Scan** → Discovers topology from all configured K8s clusters
3. **Service Mapping** → Maps discovered K8s services to AlertHub services
4. **Relationship Detection** → Creates service dependencies and relationships
5. **Health Monitoring** → Monitors cluster and service health status

## 🎯 Key Production Features

### Automatic Correlation
- **Smart Duplicate Detection**: Prevents alert spam by detecting exact duplicates
- **ML-Based Similarity**: Groups related alerts using machine learning similarity
- **Rule-Based Logic**: User-configurable correlation rules for specific scenarios
- **Confidence Scoring**: Provides confidence scores for correlation decisions

### Intelligent Incident Creation
- **Auto-Generation**: Creates incidents automatically from correlated alerts
- **Smart Grouping**: Groups related alerts into single incidents
- **Priority Assignment**: Automatically assigns priority based on severity and correlation
- **Impact Assessment**: Analyzes business impact using service topology

### Self-Configurable Topology
- **Admin Interface**: Complete admin interface for topology configuration
- **Multi-Environment**: Different settings for prod/staging/dev environments
- **Service Account Security**: Uses read-only service account tokens for K8s access
- **Real-Time Discovery**: Continuous topology discovery with configurable intervals

## 📊 Monitoring & Observability

### Correlation Metrics
- Correlation accuracy rate
- Incident auto-creation rate
- Duplicate suppression rate
- Processing time metrics
- Confidence score distributions

### Topology Health
- Cluster connectivity status
- Discovery success rates
- Service mapping accuracy
- Cross-cluster relationship health

## 🚨 Testing the Pipeline

### Manual Testing
Use the test endpoint to verify the complete pipeline:
```bash
curl -X POST https://your-alerthub-domain/api/v1/webhooks/test
```

### Sample Alert Injection
Test with sample alerts:
```bash
# Critical alert (should create incident immediately)
curl -X POST https://your-alerthub-domain/api/v1/webhooks/prometheus \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "labels": {
        "alertname": "HighCPUUsage",
        "instance": "prod-server-01",
        "service": "web-backend",
        "severity": "critical"
      },
      "annotations": {
        "summary": "CPU usage above 90%",
        "description": "CPU usage has exceeded 90% for 5 minutes"
      }
    }]
  }'

# Similar alert (should correlate to existing incident)
curl -X POST https://your-alerthub-domain/api/v1/webhooks/prometheus \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "labels": {
        "alertname": "HighMemoryUsage", 
        "instance": "prod-server-01",
        "service": "web-backend",
        "severity": "high"
      },
      "annotations": {
        "summary": "Memory usage above 85%",
        "description": "Memory usage has exceeded 85% for 3 minutes"
      }
    }]
  }'
```

## 🔐 Security Considerations

### Service Account Permissions
- All K8s service accounts use **read-only** permissions
- No write access to cluster resources
- Scoped to specific namespaces where possible
- Token rotation recommended every 90 days

### API Security
- All webhook endpoints support authentication
- Rate limiting implemented for webhook endpoints
- Audit logging for all correlation decisions
- Encrypted storage of service account tokens

## 📈 Performance Optimizations

### Correlation Engine
- Similarity caching to reduce computation
- Parallel processing of correlation analysis
- Background correlation for non-critical alerts
- Configurable confidence thresholds

### Topology Discovery
- Concurrent discovery across multiple clusters
- Incremental discovery with change detection
- Caching of topology snapshots
- Configurable discovery intervals per environment

## 🔧 Production Deployment

### 1. Build and Deploy
```bash
# Build the enhanced backend
docker build -t alerthub-enterprise:latest .

# Deploy with new features
kubectl apply -f k8s/backend-deployment.yaml
```

### 2. Environment Variables
Add to your deployment:
```yaml
env:
- name: CORRELATION_ENABLED
  value: "true"
- name: AUTO_INCIDENT_CREATION
  value: "true"
- name: TOPOLOGY_DISCOVERY_INTERVAL
  value: "5m"
- name: CORRELATION_THRESHOLD
  value: "0.7"
```

### 3. Configure Monitoring
- Set up alerts for correlation engine health
- Monitor incident creation rates
- Track topology discovery success rates
- Alert on high correlation processing times

## 🎉 Ready for Production

Your AlertHub Enterprise system is now fully productionized with:

✅ **Automatic alert correlation** - No more manual correlation needed
✅ **Intelligent incident creation** - Incidents created automatically from correlated alerts  
✅ **Self-configurable topology** - Admin can configure all environments and clusters
✅ **Multi-cluster K8s support** - Works with all your service account configs
✅ **CloudStack integration** - Already working and verified
✅ **Complete admin interface** - Full control through SRE Command Center
✅ **Production webhooks** - Ready to receive alerts from all monitoring systems
✅ **End-to-end testing** - Comprehensive testing pipeline included

The system will now automatically:
1. Receive alerts from Dynatrace/Prometheus/Grafana
2. Run correlation analysis
3. Create incidents for correlated alerts
4. Display everything in the SRE Command Center incident page
5. Maintain topology discovery for all configured environments

**Ready to handle production alert volumes with intelligent automation! 🎯**