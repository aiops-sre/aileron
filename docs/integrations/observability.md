# Observability Stack Integrations

Aileron's OIE (Operational Intelligence Engine) automatically queries your observability stack when investigating an incident. This page covers how to configure each tool to both **send alerts to Aileron** and **serve as an evidence source for RCA**.

---

## Table of Contents

- [Loki — Log-Based Alerting](#loki--log-based-alerting)
- [Thanos — Long-Range Metrics](#thanos--long-range-metrics)
- [VictoriaMetrics — VMAlert](#victoriametrics--vmalert)
- [Jaeger — Distributed Tracing](#jaeger--distributed-tracing)
- [Tempo — Distributed Tracing](#tempo--distributed-tracing)
- [OpenTelemetry Collector](#opentelemetry-collector)

---

## Loki — Log-Based Alerting

### How It Works

Loki Ruler evaluates LogQL alert rules on a schedule and fires alerts to AlertManager (or directly to Aileron's Prometheus-compatible webhook). Aileron also queries Loki directly during RCA to fetch log lines from the affected pod in the 30-minute window around the incident.

### Step 1 — Configure Loki Ruler

Add the Ruler configuration to your Loki deployment. The Ruler needs to be enabled and pointed at an AlertManager or Aileron directly.

**Loki Ruler config (add to `loki.yaml`):**

```yaml
# loki.yaml — Ruler section
ruler:
  enabled: true
  storage:
    type: local
    local:
      directory: /etc/loki/rules
  rule_path: /tmp/loki-rules
  alertmanager_url: http://alertmanager.monitoring.svc.cluster.local:9093
  # Alternative: send directly to Aileron (Prometheus webhook format)
  # alertmanager_url: https://aileron.example.com/api/v1/webhooks/prometheus
  ring:
    kvstore:
      store: inmemory    # use memberlist for multi-replica setups
  enable_api: true
  enable_alertmanager_v2: true
```

**For Helm (grafana/loki):**

```yaml
# loki-values.yaml
loki:
  rulerConfig:
    enabled: true
    alertmanager_url: http://alertmanager.monitoring.svc.cluster.local:9093
    storage:
      type: local
      local:
        directory: /etc/loki/rules
    enable_api: true

# Create a ConfigMap with your alert rules
# (see Step 2 below)
```

### Step 2 — LogQL Alert Rules

Create alert rules as Loki Ruler groups. These examples cover common error detection scenarios:

```yaml
# /etc/loki/rules/default/aileron-alerts.yaml
groups:
- name: aileron-log-alerts
  interval: 1m
  rules:

  # High error rate in any namespace
  - alert: HighErrorRateInLogs
    expr: |
      sum by (namespace, pod, app) (
        rate({namespace=~".+"}
          |~ "(?i)(error|exception|fatal|panic)"
          | logfmt
          | level=~"error|fatal|panic"
          [5m])
      ) > 0.5
    for: 2m
    labels:
      severity: warning
      source: loki
      team: platform
    annotations:
      summary: "High error rate in {{ $labels.namespace }}/{{ $labels.pod }}"
      description: >
        Pod {{ $labels.pod }} in namespace {{ $labels.namespace }}
        is producing more than 0.5 error log lines per second.

  # OOMKill detected via log pattern
  - alert: OOMKillDetectedInLogs
    expr: |
      count by (namespace, pod) (
        rate({namespace=~".+"}
          |~ "OOMKilled|out of memory|killed process"
          [5m])
      ) > 0
    for: 0m
    labels:
      severity: critical
      source: loki
    annotations:
      summary: "OOMKill detected in {{ $labels.namespace }}/{{ $labels.pod }}"
      description: "OOM kill event found in pod logs."

  # CrashLoopBackOff pattern in events
  - alert: CrashLoopBackOffInEvents
    expr: |
      count by (namespace, pod) (
        rate({job="kubernetes-events"}
          |~ "CrashLoopBackOff|Back-off restarting failed container"
          [5m])
      ) > 0
    for: 0m
    labels:
      severity: critical
      source: loki
    annotations:
      summary: "CrashLoopBackOff: {{ $labels.namespace }}/{{ $labels.pod }}"
      description: "Pod is crashing repeatedly per Kubernetes events."

  # HTTP 5xx errors from access logs (nginx/envoy format)
  - alert: High5xxRateInAccessLogs
    expr: |
      sum by (namespace, app) (
        rate({namespace=~".+", job=~".*nginx.*|.*envoy.*|.*istio.*"}
          | regexp `(?P<status>\d{3}) \d+ "-"`
          | status=~"5.."
          [5m])
      ) > 1
    for: 3m
    labels:
      severity: warning
      source: loki
    annotations:
      summary: "High 5xx rate: {{ $labels.namespace }}/{{ $labels.app }}"
      description: "More than 1 HTTP 5xx per second in access logs."
```

Apply the rules:

```bash
# If using the Ruler API
curl -X POST http://loki.monitoring.svc.cluster.local:3100/loki/api/v1/rules/default \
  -H "Content-Type: application/yaml" \
  --data-binary @aileron-alerts.yaml

# Or mount as ConfigMap
kubectl create configmap loki-aileron-rules \
  --from-file=aileron-alerts.yaml \
  --namespace monitoring
```

### Step 3 — OIE Evidence Query

Set this environment variable so OIE can query Loki for log evidence during RCA:

```bash
LOKI_URL=http://loki.monitoring.svc.cluster.local:3100
```

OIE's `loki_logs` fetcher runs the following LogQL for each incident:

```logql
{namespace="$NAMESPACE", pod=~"$POD_NAME_PATTERN"}
  |~ "(?i)(error|exception|fatal|panic|fail|critical)"
  | limit 50
```

Time range: 30 minutes before → 5 minutes after incident creation time.

---

## Thanos — Long-Range Metrics

### How It Works

Thanos Ruler evaluates Prometheus-compatible alert rules against the Thanos Query frontend (which fans out across Prometheus replicas and long-term object storage). Alerts fire to AlertManager, which forwards to Aileron via the Prometheus webhook receiver.

### Step 1 — Thanos Ruler Config

```yaml
# thanos-ruler-config.yaml
objstore:
  type: S3
  config:
    bucket: your-thanos-bucket
    endpoint: s3.us-east-1.amazonaws.com

query:
  - dnssrv+_grpc._tcp.thanos-query.monitoring.svc.cluster.local

alertmanager:
  - http://alertmanager.monitoring.svc.cluster.local:9093

# Rule files are loaded from ConfigMaps via --rule-file flag
# --rule-file=/etc/thanos/rules/*.yaml
```

**Helm deployment (bitnami/thanos):**

```yaml
# thanos-values.yaml
ruler:
  enabled: true
  replicaCount: 2
  alertmanagersUrl:
  - http://alertmanager.monitoring.svc.cluster.local:9093
  config: |-
    groups:
    - name: aileron-platform
      interval: 1m
      rules:
      - alert: HighMemoryUsage
        expr: |
          (
            sum by (namespace, pod, container) (
              container_memory_working_set_bytes{container!=""}
            )
            /
            sum by (namespace, pod, container) (
              kube_pod_container_resource_limits{resource="memory",container!=""}
            )
          ) > 0.85
        for: 5m
        labels:
          severity: warning
          source: thanos
        annotations:
          summary: "High memory: {{ $labels.namespace }}/{{ $labels.pod }}/{{ $labels.container }}"
          description: "Container is using {{ $value | humanizePercentage }} of its memory limit."

      - alert: PodRestartingFrequently
        expr: |
          increase(kube_pod_container_status_restarts_total[1h]) > 5
        for: 0m
        labels:
          severity: warning
          source: thanos
        annotations:
          summary: "Pod restarting: {{ $labels.namespace }}/{{ $labels.pod }}"
          description: "Pod has restarted {{ $value }} times in the last hour."
```

### Step 2 — AlertManager Webhook to Aileron

Configure AlertManager to forward all alerts to Aileron:

```yaml
# alertmanager-config.yaml
global:
  resolve_timeout: 5m

route:
  group_by: [alertname, namespace, cluster]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  receiver: aileron-default
  routes:
  # Critical alerts go to Aileron immediately (no grouping delay)
  - matchers:
    - severity = critical
    group_wait: 0s
    receiver: aileron-critical
  # Security alerts bypass normal grouping
  - matchers:
    - source =~ "falco|opa|kyverno"
    receiver: aileron-security

receivers:
- name: aileron-default
  webhook_configs:
  - url: https://aileron.example.com/api/v1/webhooks/prometheus
    send_resolved: true
    http_config:
      authorization:
        credentials: YOUR_AILERON_SERVICE_TOKEN
    max_alerts: 50

- name: aileron-critical
  webhook_configs:
  - url: https://aileron.example.com/api/v1/webhooks/prometheus
    send_resolved: true
    http_config:
      authorization:
        credentials: YOUR_AILERON_SERVICE_TOKEN

- name: aileron-security
  webhook_configs:
  - url: https://aileron.example.com/api/v1/webhooks/prometheus
    send_resolved: true
    http_config:
      authorization:
        credentials: YOUR_AILERON_SERVICE_TOKEN

inhibit_rules:
- source_matchers: [severity = critical]
  target_matchers: [severity = warning]
  equal: [namespace, pod]
```

Apply:

```bash
kubectl create secret generic alertmanager-config \
  --from-file=alertmanager.yaml=alertmanager-config.yaml \
  --namespace monitoring
```

### Step 3 — OIE Evidence Query

```bash
# Thanos Query frontend (Prometheus-compatible API)
METRICS_STORE_URL=http://thanos-query.monitoring.svc.cluster.local:9090
```

OIE's `long_term_metrics` fetcher queries:
- `container_cpu_usage_seconds_total` — CPU usage at incident time
- `container_memory_working_set_bytes` — Memory usage
- `http_requests_total` — Request rate and error rate
- Range: 1 hour around incident creation timestamp

---

## VictoriaMetrics — VMAlert

### How It Works

VMAlert evaluates MetricsQL rules (Prometheus-compatible with extensions) and sends firing alerts to AlertManager or directly to Aileron. VictoriaMetrics exposes a Prometheus-compatible query API, so Aileron's OIE can query it using the same `METRICS_STORE_URL` variable.

### Step 1 — VMAlert Config

```yaml
# vmalert-rules.yaml — ConfigMap with alert rules
apiVersion: v1
kind: ConfigMap
metadata:
  name: vmalert-aileron-rules
  namespace: monitoring
data:
  aileron-rules.yaml: |
    groups:
    - name: aileron-infrastructure
      interval: 30s
      rules:

      - alert: NodeCPUSaturation
        expr: |
          (
            1 - avg by (instance, node) (
              rate(node_cpu_seconds_total{mode="idle"}[5m])
            )
          ) > 0.90
        for: 3m
        labels:
          severity: critical
          source: victoriametrics
          entity_type: node
        annotations:
          summary: "CPU saturation on {{ $labels.node }}"
          description: "Node CPU usage is {{ $value | humanizePercentage }}"

      - alert: DiskSpaceLow
        expr: |
          (
            node_filesystem_avail_bytes{fstype!="tmpfs",mountpoint="/"}
            /
            node_filesystem_size_bytes{fstype!="tmpfs",mountpoint="/"}
          ) < 0.10
        for: 5m
        labels:
          severity: warning
          source: victoriametrics
          entity_type: node
        annotations:
          summary: "Low disk on {{ $labels.instance }}"
          description: "Less than 10% disk space remaining on {{ $labels.mountpoint }}"

      - alert: KafkaConsumerLag
        expr: |
          kafka_consumergroup_lag_sum > 10000
        for: 5m
        labels:
          severity: warning
          source: victoriametrics
          entity_type: kafka
        annotations:
          summary: "Kafka consumer lag: {{ $labels.consumergroup }}"
          description: "Consumer group {{ $labels.consumergroup }} lag is {{ $value }}"
```

**VMAlert Deployment:**

```yaml
# vmalert-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vmalert
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vmalert
  template:
    metadata:
      labels:
        app: vmalert
    spec:
      containers:
      - name: vmalert
        image: victoriametrics/vmalert:v1.101.0
        args:
        - -datasource.url=http://victoriametrics.monitoring.svc.cluster.local:8428
        - -remoteWrite.url=http://victoriametrics.monitoring.svc.cluster.local:8428
        - -notifier.url=http://alertmanager.monitoring.svc.cluster.local:9093
        # Alternatively, send directly to Aileron:
        # - -notifier.url=https://aileron.example.com/api/v1/webhooks/prometheus
        - -rule=/etc/vmalert/rules/*.yaml
        - -evaluationInterval=30s
        volumeMounts:
        - name: rules
          mountPath: /etc/vmalert/rules
        resources:
          limits:
            cpu: 500m
            memory: 256Mi
      volumes:
      - name: rules
        configMap:
          name: vmalert-aileron-rules
```

### Step 2 — OIE Evidence Query

VictoriaMetrics exposes a Prometheus-compatible query API:

```bash
# VictoriaMetrics single-node
METRICS_STORE_URL=http://victoriametrics.monitoring.svc.cluster.local:8428

# VMCluster (select layer)
METRICS_STORE_URL=http://vmselect.monitoring.svc.cluster.local:8481/select/0/prometheus
```

No other changes needed — OIE uses the same Prometheus HTTP API (`/api/v1/query_range`).

---

## Jaeger — Distributed Tracing

### How Aileron Uses Jaeger

Aileron does not receive alerts from Jaeger. Instead, OIE's `distributed_traces` fetcher **queries Jaeger** when investigating an incident to find failed or slow traces for the affected service during the incident window. This adds distributed trace context to the RCA narrative.

### Step 1 — Set Environment Variable

```bash
JAEGER_URL=http://jaeger-query.monitoring.svc.cluster.local:16686
```

### Step 2 — What OIE Queries

For each incident with `entity_type=service` or `entity_type=pod`, OIE queries:

```
GET /api/traces?service=<service_name>&start=<incident_time-30m>&end=<incident_time+5m>&tags=error%3Dtrue&limit=20
```

OIE extracts:
- Number of failed traces (spans with `error=true`)
- Slowest trace duration in the window
- Most common error tag values
- Root span operation names for error traces

This evidence appears in the RCA as: `"23 failed traces found in Jaeger for service 'payment-api' during incident window. Slowest trace: 12.4s on operation 'POST /charge'."`.

### Step 3 — Instrument Your Services (OpenTelemetry)

For OIE to find traces, your services must export traces to Jaeger. Use the OpenTelemetry SDK:

```go
// Go — OpenTelemetry with Jaeger exporter
import (
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

exporter, _ := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint("jaeger-collector.monitoring.svc.cluster.local:4317"),
    otlptracegrpc.WithInsecure(),
)

tp := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(exporter),
    sdktrace.WithResource(resource.NewWithAttributes(
        semconv.SchemaURL,
        semconv.ServiceName("payment-api"),
        semconv.ServiceVersion("1.2.3"),
    )),
)
otel.SetTracerProvider(tp)
```

### Step 4 — Jaeger All-in-One (Dev/Test)

```yaml
# jaeger-allinone.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaeger
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: jaeger
  template:
    metadata:
      labels:
        app: jaeger
    spec:
      containers:
      - name: jaeger
        image: jaegertracing/all-in-one:1.57
        ports:
        - containerPort: 16686   # UI + Query API
        - containerPort: 4317    # OTLP gRPC
        - containerPort: 4318    # OTLP HTTP
        env:
        - name: COLLECTOR_OTLP_ENABLED
          value: "true"
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
---
apiVersion: v1
kind: Service
metadata:
  name: jaeger-query
  namespace: monitoring
spec:
  selector:
    app: jaeger
  ports:
  - name: query
    port: 16686
    targetPort: 16686
  - name: otlp-grpc
    port: 4317
    targetPort: 4317
```

---

## Tempo — Distributed Tracing

### How Aileron Uses Tempo

Same as Jaeger — OIE queries Tempo for failed traces during the incident window. Aileron tries Jaeger first (`JAEGER_URL`), then Tempo (`TEMPO_URL`). Set one or both.

### Step 1 — Set Environment Variable

```bash
TEMPO_URL=http://tempo.monitoring.svc.cluster.local:3200
```

### Step 2 — What OIE Queries

OIE uses Tempo's TraceQL API:

```
GET /api/search?tags=error%3Dtrue+service.name%3D<service>&start=<unix>&end=<unix>&limit=20
```

And fetches full trace details for the first 5 error traces via:

```
GET /api/traces/<trace_id>
```

### Step 3 — Tempo Config with Aileron-Friendly Retention

```yaml
# tempo-values.yaml (grafana/tempo Helm chart)
tempo:
  storage:
    trace:
      backend: s3
      s3:
        bucket: your-tempo-traces-bucket
        endpoint: s3.us-east-1.amazonaws.com
        region: us-east-1
  # Retention: 14 days (covers OIE lookbacks easily)
  compactor:
    compaction:
      block_retention: 336h   # 14 days

# Enable TraceQL search API
querier:
  search:
    external_endpoints: []
    query_timeout: 30s

# Receivers — accept from OTel Collector
distributor:
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: 0.0.0.0:4317
        http:
          endpoint: 0.0.0.0:4318
```

### Step 4 — Route Traces via OTel Collector to Tempo

```yaml
# In your OTel Collector config, add Tempo as a traces exporter:
exporters:
  otlp/tempo:
    endpoint: tempo.monitoring.svc.cluster.local:4317
    tls:
      insecure: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch, k8sattributes]
      exporters: [otlp/tempo]
```

---

## OpenTelemetry Collector

The OTel Collector is the recommended way to route telemetry from your entire infrastructure into Aileron. A complete pipeline configuration ships at `deploy/otel/collector-config.yaml` — reference it for production deployment.

### Quick Reference

The collector config at [`deploy/otel/collector-config.yaml`](../../deploy/otel/collector-config.yaml) defines three pipelines:

| Pipeline | Receivers | Processors | Exporters |
|---|---|---|---|
| `metrics/alerts` | prometheus, otlp, hostmetrics | memory_limiter, k8sattributes, filter/alerts, attributes/aileron, batch | otlphttp/aileron |
| `metrics/storage` | prometheus, otlp, hostmetrics | memory_limiter, k8sattributes, batch | prometheusremotewrite |
| `logs/alerts` | otlp, k8s_events, fluentforward | memory_limiter, k8sattributes, filter/alerts, attributes/aileron, batch | otlphttp/aileron |

The `filter/alerts` processor passes only:
- Metrics with the `aileron.severity` resource attribute set (any value)
- Logs with severity `WARN` or above

### Deploy the Collector

```bash
helm upgrade --install otel-collector open-telemetry/opentelemetry-collector \
  --namespace monitoring --create-namespace \
  -f deploy/otel/collector-config.yaml \
  --set mode=daemonset \
  --set image.tag=0.103.0
```

### Send Aileron a Test Metric via OTLP

```bash
# Using otelcli
otelcli metrics gauge \
  --endpoint https://aileron.example.com/api/v1/otlp \
  --headers "Authorization=Bearer YOUR_SERVICE_TOKEN" \
  --name service.error.count \
  --value 42 \
  --attrs "aileron.severity=high,aileron.status=firing,service.name=payment-api,k8s.namespace.name=production"
```

### Mark Application Metrics as Alert-Worthy

Set the `aileron.severity` attribute on any metric or log that should trigger an Aileron incident:

```go
// Go SDK
meter.Float64ObservableGauge("cache.hit.rate",
    metric.WithDescription("Cache hit rate — low value triggers Aileron"),
).RegisterCallback(func(ctx context.Context, o metric.Observer) error {
    hitRate := computeCacheHitRate()
    attrs := []attribute.KeyValue{
        attribute.String("service.name", "recommendation-api"),
    }
    if hitRate < 0.60 {
        attrs = append(attrs,
            attribute.String("aileron.severity", "warning"),
            attribute.String("aileron.status", "firing"),
        )
    }
    o.ObserveFloat64(hitRate, metric.WithAttributes(attrs...))
    return nil
})
```

```python
# Python SDK
from opentelemetry import metrics
from opentelemetry.sdk.metrics import MeterProvider

meter = MeterProvider().get_meter("my-service")
counter = meter.create_counter("http.errors")
counter.add(1, {
    "aileron.severity": "high",
    "aileron.status": "firing",
    "http.status_code": "500",
    "service.name": "checkout-api",
})
```

### OTLP Endpoints

| Endpoint | Protocol | Description |
|---|---|---|
| `POST /api/v1/otlp/v1/metrics` | JSON or Protobuf | ExportMetricsServiceRequest |
| `POST /api/v1/otlp/v1/logs` | JSON or Protobuf | ExportLogsServiceRequest — severity ≥ WARN forwarded |
