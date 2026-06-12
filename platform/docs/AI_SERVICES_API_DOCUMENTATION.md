# AI Microservices Integration API Documentation

## Overview

This document describes the API contracts for the hybrid architecture implementation that integrates AI microservices with the monolithic AlertHub platform.

## Architecture

```
Frontend → Monolithic Platform (cmd/main-integrated.go)
              ↓ HTTP/REST
        AI Microservices Cluster
        (correlation, investigation, vector, learning, BERT)
              ↓ Kafka/Events
        Shared Data Layer (PostgreSQL + Redis + Neo4j)
```

## Resilience Features

All AI service calls implement:
- **Circuit Breaker Pattern**: 5 failures trigger open state, 30s reset timeout
- **Exponential Backoff Retry**: 3 attempts max, 100ms base delay, 5s max delay
- **Comprehensive Error Handling**: Graceful degradation on service failures

## AI Services

### 1. Autonomous AI Correlation Service (Phase 2 Production)

**Endpoint**: `http://autonomous-ai-correlation.aileron.svc.cluster.local:8005`

#### Health Check
```http
GET /health
Response: 200 OK
{
  "status": "healthy",
  "service": "autonomous-ai-correlation-phase2",
  "integration": "alerthub-enterprise",
  "frontend": "sre-command-center",
  "autonomous_running": true,
  "shadow_mode": true,
  "cluster": "production",
  "namespace": "sre-hub-alerthub",
  "timestamp": "2024-12-17T20:00:00Z"
}
```

#### Readiness Check
```http
GET /ready
Response: 200 OK
{
  "status": "ready",
  "alerthub_backend": "connected",
  "autonomous_engine": "running",
  "integration": "active",
  "shadow_mode": true
}
```

#### Autonomous Correlation Analysis (Main Integration Endpoint)
```http
POST /api/correlation/autonomous
Content-Type: application/json

{
  "alert_id": "alerthub-1702838400",
  "alert_data": {
    "id": "alert-123",
    "title": "High CPU Usage",
    "severity": "high",
    "source": "prometheus",
    "service_name": "web-server",
    "metadata": {
      "service_name": "web-api"
    }
  },
  "correlation_context": {
    "source": "alerthub-main"
  }
}

Response: 200 OK
{
  "correlation_id": "autonomous_alerthub-1702838400_1702838460",
  "should_correlate": true,
  "incident_id": "incident-456",
  "confidence": 0.85,
  "reasoning": "Multi-strategy correlation: semantic=0.85",
  "autonomous_decision": true,
  "shadow_mode": true,
  "processing_time_ms": 150.5
}
```

#### Dashboard Statistics (For SRE Command Center)
```http
GET /api/stats/dashboard
Response: 200 OK
{
  "autonomous_engine_status": "running",
  "shadow_mode": true,
  "total_alerts_processed": 1247,
  "correlations_found": 523,
  "correlation_rate": 41.9,
  "new_incidents_suggested": 724,
  "autonomous_decisions": 1247,
  "correlation_threshold": 0.6,
  "correlation_accuracy": 0.89,
  "integration_status": {
    "alerthub_backend": "connected",
    "dynatrace_webhooks": "monitoring",
    "sre_command_center": "integrated",
    "phase1_infrastructure": "connected"
  },
  "last_update": "2024-12-17T20:00:00Z"
}
```

#### Phase 2 Integration Status
```http
GET /api/phase2/status
Response: 200 OK
{
  "phase2_status": "active",
  "deployment_mode": "shadow",
  "alerthub_enterprise": {
    "url": "http://alerthub-backend.sre-hub-alerthub.svc.cluster.local",
    "status": "connected"
  },
  "sre_command_center": {
    "dashboard_integration": "ready",
    "stats_endpoint": "/api/stats/dashboard"
  },
  "phase1_integration": {
    "kafka_bootstrap": "kafka.sre-hub-alerthub.svc.cluster.local:9092",
    "redis_url": "redis://redis.sre-hub-alerthub.svc.cluster.local:6379",
    "service_mesh": "integrated"
  },
  "autonomous_features": {
    "correlation_analysis": true,
    "background_processing": true,
    "multi_strategy_correlation": true,
    "continuous_learning": true,
    "shadow_mode_validation": true
  }
}
```

#### Learning Feedback
```http
POST /api/feedback/correlation
Content-Type: application/json

{
  "correlation_id": "autonomous_alert-123_1702838460",
  "correct": true,
  "feedback": "Correlation was accurate"
}

Response: 200 OK
{
  "message": "Feedback recorded - autonomous engine will improve",
  "feedback_count": 156,
  "shadow_mode": true
}
```

#### Enable Production Mode
```http
POST /api/phase2/enable-production
Response: 200 OK
{
  "message": "Production mode enabled successfully",
  "status": "production",
  "correlation_accuracy": 0.89
}
```

### 2. AI Investigation Engine

**Endpoint**: `http://ai-investigation-engine.aileron.svc.cluster.local:8004`

#### Health Check
```http
GET /health
Response: 200 OK
{
  "status": "healthy",
  "investigation_engine_ready": true,
  "tool_orchestrator_ready": true,
  "streaming_manager_ready": true,
  "active_investigations": 0,
  "available_tools": 7,
  "version": "2.0.0",
  "phase": "2"
}
```

#### Start Autonomous Investigation
```http
POST /investigate/autonomous
Content-Type: application/json

{
  "incident_id": "incident-1702838400",
  "incident_data": {
    "title": "Service Outage",
    "description": "Multiple services down",
    "severity": "critical",
    "affected_services": ["web-api", "database"]
  },
  "investigation_scope": "full",
  "priority": "medium",
  "context": {
    "monolithic_integration": true,
    "source": "alerthub-main"
  }
}

Response: 200 OK
{
  "investigation_id": "inv-xyz789",
  "status": "running",
  "estimated_duration_minutes": 15,
  "investigation_plan": [...],
  "streaming_endpoint": "/stream/inv-xyz789",
  "tools_selected": ["kubernetes", "logs", "network"],
  "autonomous_mode": true,
  "phase2_enhanced": true
}
```

#### Real-time Investigation Stream
```websocket
WS /stream/{investigation_id}

Messages:
{
  "type": "status_update",
  "data": {
    "investigation_id": "inv-xyz789",
    "status": "analyzing",
    "progress": 45,
    "current_step": "Analyzing network connectivity"
  },
  "phase": "2",
  "timestamp": "2024-12-17T20:00:00Z"
}
```

### 3. Autonomous Learning Engine

**Endpoint**: `http://autonomous-learning-engine.aileron.svc.cluster.local:8006`

#### Health Check
```http
GET /health
Response: 200 OK
{
  "status": "healthy",
  "service": "autonomous-learning-engine",
  "phase": "3",
  "learning_engine_status": "active",
  "patterns_discovered": 23
}
```

#### Submit Learning Event
```http
POST /api/learning/event
Content-Type: application/json

{
  "event_type": "correlation_feedback",
  "service_name": "alerthub-main",
  "event_data": {
    "accuracy": 0.92,
    "false_positives": 2,
    "false_negatives": 1,
    "resolution_time_seconds": 300
  },
  "confidence_score": 0.85,
  "correlation_id": "learning-1702838400",
  "feedback_score": 0.9
}

Response: 200 OK
{
  "success": true,
  "message": "Learning event processed successfully",
  "correlation_id": "learning-1702838400"
}
```

#### Get Learning Analytics
```http
GET /api/learning/analytics

Response: 200 OK
{
  "learning_strategies": {
    "correlation": {
      "average_reward": 0.78,
      "total_decisions": 156,
      "success_rate": 0.85
    },
    "investigation": {
      "average_reward": 0.82,
      "total_decisions": 98,
      "success_rate": 0.88
    }
  },
  "cross_service_patterns": 12,
  "total_events_processed": 1247
}
```

### 4. AlertHub BERT Service (Enhanced)

**Endpoint**: `http://alerthub-bert-service.aileron.svc.cluster.local:8766`

#### Health Check
```http
GET /health
Response: 200 OK
{
  "status": "healthy",
  "model_loaded": true,
  "model_name": "all-MiniLM-L6-v2",
  "cache_size": 245,
  "performance_metrics": {
    "total_requests": 1523,
    "cache_hit_rate": 0.67,
    "average_processing_time_ms": 45.2
  }
}
```

#### Single Text Embedding
```http
POST /embed
Content-Type: application/json

{
  "text": "High CPU usage detected on production server"
}

Response: 200 OK
{
  "embedding": [0.123, -0.456, 0.789, ...],
  "model": "all-MiniLM-L6-v2",
  "processing_time_ms": 23.5,
  "cached": false,
  "vector_length": 384
}
```

#### Batch Text Embedding (32x Performance)
```http
POST /embed/batch
Content-Type: application/json

{
  "texts": [
    "High CPU usage detected",
    "Memory leak in application",
    "Network connectivity issues"
  ]
}

Response: 200 OK
{
  "embeddings": [
    [0.123, -0.456, 0.789, ...],
    [0.234, -0.567, 0.890, ...],
    [0.345, -0.678, 0.901, ...]
  ],
  "count": 3,
  "model": "all-MiniLM-L6-v2",
  "processing_time_ms": 67.8,
  "batch_processing_time_ms": 45.2,
  "cache_hits": 1,
  "cache_misses": 2,
  "performance_improvement": "3x faster than individual calls"
}
```

### 5. Local BERT Service

**Endpoint**: `http://local-bert-service.aileron.svc.cluster.local:8766`

#### Health Check
```http
GET /health
Response: 200 OK
{
  "status": "healthy",
  "model_loaded": true
}
```

#### Text Embedding
```http
POST /embed
Content-Type: application/json

{
  "text": "Database connection timeout"
}

Response: 200 OK
{
  "embedding": [0.123, -0.456, 0.789, ...],
  "model": "all-MiniLM-L6-v2"
}
```

### 6. Vector Embedding Service

**Endpoint**: `http://vector-embedding-service.aileron.svc.cluster.local:80`

#### Health Check
```http
GET /health
Response: 200 OK
{
  "status": "healthy",
  "service": "vector-embedding"
}
```

#### Generate Embedding
```http
POST /api/embeddings/generate
Content-Type: application/json

{
  "text": "Application server restart required"
}

Response: 200 OK
{
  "embedding": [0.123, -0.456, 0.789, ...]
}
```

## Monolithic Platform Integration Endpoints

### Comprehensive AI Status
```http
GET /api/v1/ai/status

Response: 200 OK
{
  "ai_services": {
    "autonomous_correlation": {
      "url": "http://autonomous-ai-correlation.aileron.svc.cluster.local:8005",
      "status": true,
      "healthy": true
    },
    "ai_investigation_engine": {
      "url": "http://ai-investigation-engine.aileron.svc.cluster.local:8004",
      "status": true,
      "healthy": true
    },
    "autonomous_learning": {
      "url": "http://autonomous-learning-engine.aileron.svc.cluster.local:8006",
      "status": true,
      "healthy": true
    },
    "alerthub_bert_service": {
      "url": "http://alerthub-bert-service.aileron.svc.cluster.local:8766",
      "status": true,
      "healthy": true
    },
    "local_bert_service": {
      "url": "http://local-bert-service.aileron.svc.cluster.local:8766",
      "status": true,
      "healthy": true
    }
  },
  "architecture": "hybrid_monolith_microservices",
  "total_services": 7
}
```

### AI Correlation (Parallel Processing)
```http
POST /api/v1/ai/correlate
Content-Type: application/json

{
  "title": "High Memory Usage Alert",
  "severity": "high",
  "source": "datadog",
  "tags": {
    "service": "api-gateway",
    "environment": "production"
  },
  "metrics": {
    "memory_usage": 87.5,
    "cpu_usage": 65.2
  }
}

Response: 200 OK
{
  "autonomous_correlation": {
    "correlation_id": "corr-auto-123",
    "correlated_alerts": [...],
    "confidence_score": 0.88
  },
  "ai_correlation_engine": {
    "correlation_id": "corr-engine-456",
    "correlated_alerts": [...],
    "confidence_score": 0.82
  },
  "timestamp": "2024-12-17T20:00:00Z"
}
```

### AI Investigation
```http
POST /api/v1/ai/investigate
Content-Type: application/json

{
  "incident_title": "Service Degradation",
  "affected_services": ["web-api", "database"],
  "severity": "critical",
  "start_time": "2024-12-17T19:45:00Z"
}

Response: 200 OK
{
  "investigation_result": {
    "investigation_id": "inv-789",
    "status": "running",
    "estimated_duration_minutes": 12,
    "streaming_endpoint": "/stream/inv-789"
  }
}
```

### AI Learning Integration
```http
POST /api/v1/ai/learning
Content-Type: application/json

{
  "event_type": "investigation_result",
  "confidence_score": 0.85,
  "event_data": {
    "resolved": true,
    "resolution_time_seconds": 420,
    "tools_used": ["kubernetes", "logs"],
    "escalation_needed": false
  }
}

Response: 200 OK
{
  "learning_result": {
    "success": true,
    "message": "Learning event processed successfully"
  }
}
```

### BERT Embedding Integration
```http
POST /api/v1/ai/bert/embed
Content-Type: application/json

{
  "texts": [
    "Application crash detected",
    "Database query timeout",
    "Network latency spike"
  ],
  "batch": true
}

Response: 200 OK
{
  "bert_result": {
    "embeddings": [...],
    "count": 3,
    "processing_time_ms": 56.7,
    "performance_improvement": "3x faster than individual calls"
  }
}
```

### Local BERT Integration
```http
POST /api/v1/ai/local-bert
Content-Type: application/json

{
  "text": "SSL certificate expiring soon"
}

Response: 200 OK
{
  "embedding": [0.123, -0.456, 0.789, ...],
  "vector_length": 384
}
```

### Vector Embedding Integration
```http
POST /api/v1/ai/embed
Content-Type: application/json

{
  "text": "Disk space running low"
}

Response: 200 OK
{
  "embedding": [0.123, -0.456, 0.789, ...],
  "vector_length": 384
}
```

## Error Handling

All endpoints implement comprehensive error handling:

### Circuit Breaker Open
```http
Response: 503 Service Unavailable
{
  "error": "circuit breaker is open",
  "service": "autonomous_correlation",
  "retry_after": 30
}
```

### Service Timeout
```http
Response: 504 Gateway Timeout
{
  "error": "all retry attempts failed: HTTP request failed: context deadline exceeded",
  "attempts": 3,
  "total_time_ms": 5000
}
```

### Invalid Request
```http
Response: 400 Bad Request
{
  "error": "Invalid JSON payload"
}
```

### Service Unavailable
```http
Response: 500 Internal Server Error
{
  "error": "AI investigation failed: service unavailable"
}
```

## Circuit Breaker States

### Service Health Monitoring
```http
GET /ready

Response: 200 OK
{
  "status": "ready",
  "version": "2.0.0-integrated-hybrid",
  "ai_services": {
    "autonomous_correlation": true,
    "ai_correlation_engine": false,  // Service down
    "vector_embedding": true,
    "ai_investigation_engine": true,
    "autonomous_learning": true,
    "alerthub_bert_service": true,
    "local_bert_service": true
  }
}
```

## Performance Characteristics

| Service | Typical Response Time | Batch Support | Caching |
|---------|---------------------|---------------|---------|
| Autonomous Correlation | 150-300ms | No | Redis |
| AI Investigation | 200-500ms | No | Redis |
| Autonomous Learning | 50-150ms | Yes | Redis |
| AlertHub BERT | 25-100ms | Yes (32x faster) | In-memory |
| Local BERT | 30-80ms | No | None |
| Vector Embedding | 100-250ms | No | Redis |

## Environment Variables

```bash
# AI Service URLs
AUTONOMOUS_CORRELATION_URL=http://autonomous-ai-correlation.aileron.svc.cluster.local:8005
AI_INVESTIGATION_ENGINE_URL=http://ai-investigation-engine.aileron.svc.cluster.local:8004
AUTONOMOUS_LEARNING_URL=http://autonomous-learning-engine.aileron.svc.cluster.local:8006
ALERTHUB_BERT_URL=http://alerthub-bert-service.aileron.svc.cluster.local:8766
LOCAL_BERT_URL=http://local-bert-service.aileron.svc.cluster.local:8766
VECTOR_EMBEDDING_URL=http://vector-embedding-service.aileron.svc.cluster.local:80

# Circuit Breaker Configuration (defaults shown)
AI_CIRCUIT_BREAKER_MAX_FAILURES=5
AI_CIRCUIT_BREAKER_TIMEOUT=30s
AI_RETRY_MAX_ATTEMPTS=3
AI_RETRY_BASE_DELAY=100ms
AI_RETRY_MAX_DELAY=5s
```

## Integration Examples

### Complete Incident Workflow
```javascript
// 1. Receive alert webhook
const alert = {
  title: "High CPU Usage",
  severity: "critical",
  source: "prometheus"
};

// 2. Correlate with existing alerts
const correlation = await fetch('/api/v1/ai/correlate', {
  method: 'POST',
  body: JSON.stringify(alert)
});

// 3. Start investigation if correlation found
if (correlation.confidence_score > 0.8) {
  const investigation = await fetch('/api/v1/ai/investigate', {
    method: 'POST',
    body: JSON.stringify({
      incident_title: alert.title,
      correlated_alerts: correlation.correlated_alerts
    })
  });
  
  // 4. Stream real-time investigation
  const ws = new WebSocket(`/stream/${investigation.investigation_id}`);
  ws.onmessage = (event) => {
    const update = JSON.parse(event.data);
    console.log('Investigation update:', update);
  };
}

// 5. Submit learning feedback
await fetch('/api/v1/ai/learning', {
  method: 'POST',
  body: JSON.stringify({
    event_type: 'correlation_feedback',
    confidence_score: 0.85,
    event_data: { accuracy: 0.92 }
  })
});
```

This hybrid architecture provides the perfect balance of monolithic efficiency with microservices flexibility for AI/ML capabilities.