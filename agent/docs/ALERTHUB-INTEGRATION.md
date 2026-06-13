# AlertHub ↔ KubeSense Integration Guide

KubeSense integrates with AlertHub exclusively over the shared Strimzi Kafka cluster. No direct HTTP calls are made between platforms. AlertHub publishes investigation requests and KubeSense publishes everything else — health events, RCA results, chaos scores, APM signals, config violations, forecasts, and SRE intelligence.

---

## Prerequisites

### 1. Strimzi Kafka cluster

KubeSense reuses the Kafka cluster already running for AlertHub:

```
Bootstrap: alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092
Namespace: aileron
Cluster CR: alerthub-kafka
```

All KubeSense topics must be created as `KafkaTopic` CRDs in the `aileron` namespace with `strimzi.io/cluster: alerthub-kafka`.

### 2. ArgoCD repository secret

The `github.com/aileron-platform` host uses a self-signed certificate. Register the repo with `insecure: true`:

```bash
argocd repo add https://github.com/aileron-platform/aileron.git \
  --username <service-account> \
  --password <token> \
  --insecure-skip-server-verification
```

Or as a Kubernetes secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: kubesense-repo
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
stringData:
  type: git
  url: https://github.com/aileron-platform/aileron.git
  username: <service-account>
  password: <token>
  insecure: "true"
```

### 3. SecretsManager secrets

KubeSense services read database credentials via the SecretsManager secret injection system (same pattern as AlertHub):

| Secret key | Used by |
|---|---|
| `DATABASE_URL` | collector, core, api — PostgreSQL connection string |
| `NEO4J_PASSWORD` | collector, core, api — Neo4j HTTP API password |

---

## Data Flows

### AlertHub → KubeSense: Investigation Requests

When AlertHub creates a new incident and determines a parallel RCA investigation is warranted, it publishes an `InvestigationRequest` to `kubesense.investigations.requests`.

**Topic:** `kubesense.investigations.requests`  
**Producer:** AlertHub (`internal/services/kubesense/publisher.go`)  
**Consumer:** `kubesense-core` (`services/core/internal/investigation/consumer.go`)  
**Consumer group:** `kubesense-investigation-consumer`

**Request payload:**

```json
{
  "id": "req-uuid",
  "requested_at": "2026-06-08T14:23:00Z",
  "incident_id": "INC-2026-0042",
  "cluster_id": "example-cluster",
  "namespace": "payments",
  "resource_kind": "Pod",
  "resource_name": "checkout-6d8b4f-x9k2p",
  "severity": "critical",
  "alert_title": "High error rate in payments/checkout",
  "callback_topic": "kubesense.investigations.results"
}
```

The `resource_kind` + `resource_name` + `namespace` fields are optional. If omitted, `kubesense-core` falls back to a cluster-wide event scan for the RCA.

**Processing flow inside kubesense-core:**

```
kubesense.investigations.requests
  └── investigation.Consumer.processMessage()
        ├── rca.Engine.Investigate()
        │     ├── Neo4j: GetUpstreamChain(Pod, depth=8)
        │     ├── PostgreSQL: kubesense_health_events (6h lookback)
        │     ├── PostgreSQL: kubesense_changes (causal change correlation)
        │     └── Score hypotheses (infrastructure priors: Node 1.5×, PVC 1.35×, Pod 0.8×)
        ├── Publish → kubesense.investigations.results
        └── Publish → kubesense.correlation.incident-context (for LLM narrator)
```

---

### KubeSense → AlertHub: Investigation Results

**Topic:** `kubesense.investigations.results`  
**Producer:** `kubesense-core`  
**Consumer:** AlertHub — merges `confidence` and `root_cause` into the incident's `rca_confidence` field

**Result payload:**

```json
{
  "id": "result-uuid",
  "completed_at": "2026-06-08T14:23:02Z",
  "incident_id": "INC-2026-0042",
  "cluster_id": "example-cluster",
  "grade": "A",
  "confidence": 0.87,
  "root_cause": "Deployment/checkout/payments: OOMKilled",
  "summary": "Root cause: Deployment/checkout in namespace payments (OOMKilled). Confidence: 87%. A deploy on Deployment/checkout was deployed 420s before the incident. 3 alternative hypotheses evaluated.",
  "hypotheses": [
    {
      "entity_id": "payments/checkout",
      "entity_kind": "Deployment",
      "entity_name": "checkout",
      "namespace": "payments",
      "failure_mode": "OOMKilled",
      "confidence": 0.87,
      "supporting_evidence": 6,
      "refuting_evidence": 1
    }
  ],
  "evidence_count": 11,
  "rca_duration_ms": 342
}
```

**Evidence grades:**

| Grade | Confidence threshold | Meaning |
|---|---|---|
| A | ≥ 88% | Strong evidence, high-confidence root cause |
| B | ≥ 72% | Good evidence, likely root cause |
| C | ≥ 55% | Moderate evidence, probable root cause |
| D | ≥ 35% | Weak evidence, possible root cause |
| F | < 35% | Insufficient evidence for a conclusive finding |

AlertHub surfaces this result in the incident's **RCA tab** — grade badge, confidence bar, root cause entity, and expandable hypothesis list.

---

### KubeSense → AlertHub: Health Events

**Topics:** `kubesense.events.topology`, `kubesense.events.health`, `kubesense.events.workloads`, `kubesense.events.config`, `kubesense.events.storage`, `kubesense.events.network`  
**Producer:** `kubesense-agent`  
**Consumer:** `kubesense-collector` (writes to Neo4j + PostgreSQL)

All events use the `IntelligenceEvent` envelope (`pkg/events/events.go`):

```json
{
  "id": "evt-uuid",
  "timestamp": "2026-06-08T14:22:55Z",
  "cluster_id": "example-cluster",
  "agent_id": "kubesense-agent-7d8b4f",
  "agent_version": "v0.1.0",
  "type": "health.pod.oomkilled",
  "severity": "critical",
  "resource": {
    "api_version": "v1",
    "kind": "Pod",
    "namespace": "payments",
    "name": "checkout-6d8b4f-x9k2p",
    "uid": "abc-123"
  },
  "new_state": { ... },
  "triggered_by": "kubelet"
}
```

The Kafka **message key** is `cluster_id`, ensuring all events from the same cluster land on the same partition (preserving intra-cluster event ordering).

---

### KubeSense → AlertHub: Chaos Readiness Scores

**Topic:** `kubesense.chaos.scores` (compacted — latest score per cluster is always available)  
**Producer:** `kubesense-agent` via `pkg/chaos/scorer.go`  
**Consumer:** AlertHub — displayed on the KubeSense page **Chaos** tab  
**Cadence:** Every 5 minutes

**Score payload:**

```json
{
  "cluster_id": "example-cluster",
  "cluster_score": 62.4,
  "grade": "C",
  "total_workloads": 47,
  "high_risk_count": 8,
  "summary": "Cluster example-cluster: 8/47 workloads have chaos readiness issues (cluster score=62/100) — single replicas, missing PDBs, or absent probes",
  "timestamp": "2026-06-08T14:25:00Z"
}
```

**Scoring breakdown (per Deployment, 0–100):**

| Check | Penalty | Reason |
|---|---|---|
| Single replica (< 2) | −40 pts | Complete outage on pod failure (SPOF) |
| No PodDisruptionBudget | −20 pts | Uncontrolled disruption during node drain |
| Missing readiness probe | −15 pts | Rolling update causes downtime |
| Missing liveness probe | −10 pts | Hung pods never restart |
| No resource limits | −10 pts | Noisy neighbour risk |

The cluster score is the mean across all Deployments. Grade thresholds:
- **A**: ≥ 85  
- **B**: ≥ 70  
- **C**: ≥ 55  
- **D**: ≥ 40  
- **F**: < 40

---

### KubeSense → AlertHub: APM Golden Signals

**Topic:** `kubesense.apm.golden-signals`  
**Producer:** `kubesense-api` background publisher  
**Consumer:** AlertHub OIE evidence bus, KubeSense APM tab  
**Cadence:** Every 60 seconds

Per-service RED + Saturation metrics: request rate, error fraction, p50/p90/p95/p99 latency, active connections. Discovered from OTel traces, service mesh telemetry, and EndpointSlice watch.

---

### KubeSense → AlertHub: Config Violations

**Topic:** `kubesense.config.violations`  
**Producer:** `kubesense-api` background publisher  
**Consumer:** AlertHub KubeSense Config tab  
**Cadence:** Every 5 minutes

Config violations for 9 rules: missing readiness/liveness probes, no resource limits/requests, `:latest` image tag, no anti-affinity, no PDB, single replica in production, missing network policy.

---

### KubeSense → AlertHub: Forecasts

**Topic:** `kubesense.forecasts`  
**Producer:** `kubesense-api` background publisher  
**Consumer:** AlertHub KubeSense Forecasts tab  
**Cadence:** Every 5 minutes

Linear regression forecasts for CPU/memory/disk exhaustion per workload. Includes `hours_until_exhaustion`, confidence interval, and recommended action.

---

### KubeSense → AlertHub: Anomalies

**Topic:** `kubesense.anomalies`  
**Producer:** `kubesense-api` background publisher  
**Consumer:** AlertHub OIE evidence bus  
**Cadence:** Every 2 minutes

EWMA-based anomaly detection across 10 signals per resource (CPU, memory, latency p99, error rate, request rate, restart count, disk usage, network bytes, connection count). Fires when signal deviates >3σ from the rolling baseline (minimum 30 samples before alerting).

---

### KubeSense → AlertHub: SRE Intelligence Topics

These topics carry SRE intelligence signals to AlertHub:

| Topic | Cadence | Meaning |
|---|---|---|
| `kubesense.drift.detected` | On change | GitOps live-vs-desired state drift with actor attribution |
| `kubesense.postmortems` | After incident close | Full evidence-grounded postmortem (retained forever) |
| `kubesense.toil.events` | On operation | Manual kubectl/restart/scale operation tracking (90d retention) |
| `kubesense.oncall.routing` | On routing decision | Expertise-based on-call routing decisions for ML training (90d) |
| `kubesense.noisebudget.suppressions` | On suppression | Suppressed alert audit trail (30d) |

---

## How AlertHub Surfaces KubeSense Data

### KubeSense Page

The AlertHub KubeSense page (`/kubesense`) has tabs populated from Kafka topics:

| Tab | Data source | Update cadence |
|---|---|---|
| **Overview** | `kubesense_clusters` PostgreSQL table | Live (via API) |
| **Chaos** | `kubesense.chaos.scores` | 5 min (compacted topic) |
| **APM** | `kubesense.apm.golden-signals` | 60 s |
| **Config Violations** | `kubesense.config.violations` | 5 min |
| **Forecasts** | `kubesense.forecasts` | 5 min |
| **Anomalies** | `kubesense.anomalies` | 2 min |

### Incident RCA Tab

When AlertHub publishes an investigation request and receives a result from `kubesense.investigations.results`, the incident detail view shows:

- **Grade badge** (A/B/C/D/F) — coloured pill, green → red
- **Confidence bar** — percentage from `result.confidence`
- **Root cause entity** — `kind/namespace/name: failure_mode`
- **Summary** — one-sentence plain text from `result.summary`
- **Hypothesis list** (expandable) — all ranked hypotheses with supporting/refuting evidence counts
- **LLM narrative** (when available) — plain-language summary from `kubesense.llm.narratives`, clearly attributed to the narrator and citing signal IDs

---

## Troubleshooting

### No RCA results appearing in AlertHub

1. Check kubesense-core is consuming from the requests topic:
   ```bash
   kubectl logs -n aileron-agent -l app=kubesense-core -f | grep "investigation consumer"
   ```
   Expected: `investigation consumer: ready on topic=kubesense.investigations.requests group=kubesense-investigation-consumer`

2. Check the consumer group lag:
   ```bash
   kubectl exec -n aileron \
     $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-kafka -o name | head -1) \
     -- bin/kafka-consumer-groups.sh \
     --bootstrap-server localhost:9092 \
     --group kubesense-investigation-consumer \
     --describe
   ```

3. Verify Neo4j is reachable from kubesense-core:
   ```bash
   kubectl exec -n aileron-agent deploy/kubesense-core -- \
     wget -qO- http://kubesense-neo4j.aileron-agent.svc.cluster.local:7474/db/neo4j/tx/commit \
     --header "Content-Type: application/json" \
     --post-data '{"statements":[{"statement":"RETURN 1"}]}'
   ```

4. Verify PostgreSQL is reachable and the schema exists:
   ```bash
   kubectl exec -n aileron-agent deploy/kubesense-core -- \
     sh -c 'psql $DATABASE_URL -c "\dt kubesense_*"'
   ```
   Expected tables: `kubesense_clusters`, `kubesense_health_events`, `kubesense_changes`

### No chaos scores appearing

1. Check agent is running and publishing:
   ```bash
   kubectl logs -n aileron-agent -l app=kubesense-agent -f | grep "chaos:"
   ```
   Expected every 5 min: `chaos: published cluster=example-cluster score=62/100 grade=C workloads=47 highRisk=8`

2. Verify the `kubesense.chaos.scores` topic exists:
   ```bash
   kubectl exec -n aileron \
     $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-kafka -o name | head -1) \
     -- bin/kafka-topics.sh --bootstrap-server localhost:9092 --describe \
     --topic kubesense.chaos.scores
   ```

### No APM / violations / forecasts in AlertHub tabs

1. Check kubesense-api signal publisher is running:
   ```bash
   kubectl logs -n aileron-agent -l app=kubesense-api -f | grep "published"
   ```
   Expected: `kubesense-api: published N APM golden signals to kubesense.apm.golden-signals`

2. If `kubesense-api: Kafka unavailable — signal publishing disabled`, the `KAFKA_BROKERS` env var is not set or Kafka is unreachable. Check the SecretsManager secret injection.

3. If the publisher runs but AlertHub tabs are empty, check the AlertHub `kubesense` consumer service is running and consuming the correct Kafka groups.

### Collector not writing to Neo4j

```bash
kubectl logs -n aileron-agent -l app=kubesense-collector -f | grep -E "(neo4j|topology)"
```

If you see `topology writer: ... connection refused`, verify:
```bash
kubectl get svc -n aileron-agent kubesense-neo4j
kubectl exec -n aileron-agent deploy/kubesense-collector -- \
  wget -qO- http://kubesense-neo4j.aileron-agent.svc.cluster.local:7474
```

---

## What "Grade F" Chaos Score Means and How to Improve It

A cluster score of **F** (< 40/100) means the average Deployment in the cluster would likely cause a full service outage if a single pod or node failed. The most impactful fixes are ordered by penalty size:

### Fix 1: Add a second replica (−40 pts penalty per single-replica Deployment)

```bash
# Find all single-replica Deployments
kubectl get deployments -A -o json | \
  jq '.items[] | select(.spec.replicas == 1) | "\(.metadata.namespace)/\(.metadata.name)"'

# Scale up
kubectl scale deployment <name> -n <namespace> --replicas=2
```

### Fix 2: Add PodDisruptionBudgets (−20 pts penalty)

For each Deployment with 2+ replicas, create a PDB that allows at most 1 disruption:

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: <deployment-name>-pdb
  namespace: <namespace>
spec:
  maxUnavailable: 1
  selector:
    matchLabels:
      app: <deployment-label>
```

### Fix 3: Add readiness probes (−15 pts penalty)

Without a readiness probe, rolling updates send traffic to pods before they're ready, causing 502/503 errors. Minimum viable probe:

```yaml
readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
```

### Fix 4: Add liveness probes (−10 pts penalty)

Without a liveness probe, hung pods are never restarted automatically:

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 30
  failureThreshold: 3
```

### Fix 5: Add resource limits (−10 pts penalty)

Without limits, a memory-leaking container can consume all node memory and trigger OOM evictions of other pods:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

After applying fixes, the next chaos scoring run (within 5 minutes) will update the score and publish a new message to `kubesense.chaos.scores`. AlertHub's Chaos tab will reflect the updated grade automatically.
