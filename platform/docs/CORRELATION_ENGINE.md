# AlertHub — Alert Correlation Engine: End-to-End Technical Guide

> **Scope:** How alerts arrive, flow through Kafka, enter the pipeline, and are correlated into incidents.  
> Covers all correlation strategies, the deduplication cascade, incident creation, and every external integration in the path.

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Alert Ingestion — Webhook Handlers](#2-alert-ingestion--webhook-handlers)
3. [Kafka Pipeline](#3-kafka-pipeline)
4. [Alert Pipeline Service](#4-alert-pipeline-service)
5. [Stage 0 — Root Cause Engine (Deterministic)](#5-stage-0--root-cause-engine-deterministic)
6. [Stage 1–4 — Parallel Correlation Engine](#6-stage-14--parallel-correlation-engine)
   - [Semantic Strategy](#61-semantic-strategy)
   - [Temporal Strategy](#62-temporal-strategy)
   - [Topology Strategy](#63-topology-strategy)
   - [Rules Strategy](#64-rules-strategy)
7. [Correlation Aggregator](#7-correlation-aggregator)
8. [Decision Execution](#8-decision-execution)
9. [Deduplication Cascade](#9-deduplication-cascade)
10. [Alert State Machine](#10-alert-state-machine)
11. [Incident Service](#11-incident-service)
12. [External Integrations](#12-external-integrations)
13. [Full End-to-End Flow Diagram](#13-full-end-to-end-flow-diagram)
14. [Performance Characteristics](#14-performance-characteristics)

---

## 1. System Overview

AlertHub ingests alerts from multiple monitoring sources (Dynatrace, Prometheus, Grafana, PagerDuty, Splunk) and automatically correlates them into incidents using a multi-layered engine. The system is designed around three core guarantees:

- **Zero alerts lost** — every alert either creates an incident, merges into one, or is explicitly suppressed with a recorded reason.
- **Noise reduction** — an 17-step deduplication cascade ensures a single infrastructure event never generates duplicate incidents.
- **Root-cause-first** — a deterministic root cause engine runs before probabilistic scoring, so Dynatrace entity chains and topology ancestors take priority over statistics.

### Technology Stack

| Layer | Technology |
|---|---|
| Alert Storage | PostgreSQL |
| Alert State Machine | Redis |
| Event Streaming | Kafka (3 topics) |
| Topology Graph | Redis-backed graph (primary), Neo4j (secondary) |
| Vector Similarity | Weaviate |
| Text Embeddings | BERT service |
| Local LLM | Ollama |
| API Framework | Go + Gin |

---

## 2. Alert Ingestion — Webhook Handlers

**Package:** `internal/api/handlers/`  
**File:** `webhooks.go`, `enhanced_webhooks.go`, `splunk_webhook.go`

Alerts enter the system exclusively through HTTP webhooks. Each source has a dedicated handler that normalizes the payload into the internal `Alert` model.

### 2.1 Registered Endpoints

| Route | Handler | Source |
|---|---|---|
| `POST /api/v1/webhooks/dynatrace` | `DynatraceWebhook` | Dynatrace problem notifications |
| `POST /api/v1/webhooks/prometheus` | `PrometheusWebhook` | Prometheus Alertmanager |
| `POST /api/v1/webhooks/grafana` | `GrafanaWebhook` | Grafana unified alerting |
| `POST /api/v1/webhooks/pagerduty` | `PagerDutyWebhook` | PagerDuty incidents |
| `POST /api/v1/splunk/webhook` | `HandleSplunkWebhook` | Splunk alert actions |
| `POST /api/v1/webhooks/ingest` | `IngestAlert` | Generic API key-authenticated |
| `POST /api/v1/alerts/ingest` | `IngestAlert` | Public generic endpoint |

### 2.2 WebhookHandler Struct

```go
type WebhookHandler struct {
    alertService      *alerts.AlertService
    correlationEngine *correlation.CorrelationEngine
    incidentService   *incidents.IncidentService
    db                *sql.DB
    kafkaProducer     KafkaProducer       // publishes to raw-alerts topic
    pipelineProcessor PipelineProcessor   // enqueues to in-process channel
    normRegistry      *normalization.Registry
}
```

### 2.3 Dynatrace Handler — Deep Dive

Dynatrace is the primary source. `DynatraceWebhook` has the most complex normalization logic:

**Step 1 — Field Extraction (with fallback)**  
Dynatrace sends both PascalCase (`ProblemID`, `State`) and camelCase (`problemId`, `state`) variants. The handler tries both:

```
ProblemID / problemId           → source_id (e.g. "P-12345")
ProblemTitle / problemTitle     → title
State / state                   → status (OPEN→open, RESOLVED→resolved)
Severity / severity             → severity (AVAILABILITY→critical, PERFORMANCE→high, …)
ImpactedEntityName              → part of blast_radius
RootCauseEntity                 → stored in alert.RootCauseEntity (critical for Stage 0)
CustomProperties                → extracted as labels k/v
ProblemDetails text             → parsed for embedded K8s labels
```

**Step 2 — Severity Mapping**

| Dynatrace Severity | Internal Severity |
|---|---|
| AVAILABILITY | critical |
| PERFORMANCE | high |
| ERROR | high |
| RESOURCE_CONTENTION | medium |
| CUSTOM_ALERT | low |

**Step 3 — Deduplication Check**  
Before creating a new alert, the handler queries `alerts` by `source_id`. If a match exists:
- If the existing alert is `resolved`: update its fields in place, skip new creation.
- If the existing alert is `open`: update fields, re-enqueue for correlation.

**Step 4 — Kafka-First Publish**  
The normalized alert is published to the `raw-alerts` Kafka topic. If Kafka is unavailable, the handler falls back to direct DB insert + `pipelineProcessor.EnqueueAlert`.

**Step 5 — K8s Metadata Extraction**  
Labels found in `ProblemDetails` text (e.g., `k8s.cluster.name=mps-nonprod-rno`) are parsed and stored in `alert.Labels`. These feed topology correlation later.

**Step 6 — EnhancedWebhookHandler (Autonomous Layer)**  
`enhanced_webhooks.go` wraps the base handler. After a successful Dynatrace ingest, it spawns a background goroutine that POSTs the alert payload to the optional `AUTONOMOUS_CORRELATION_URL` service. This is non-blocking — failure is logged but does not affect the primary path.

### 2.4 Authentication

Generic webhook endpoints use API key authentication:

1. Extract `X-API-Key` header.
2. SHA-256 hash the key.
3. Query `webhook_api_keys` table for hash match.
4. Update `last_used_at`, extract `user_id`.
5. Reject with 401 if not found.

Dynatrace/Prometheus/Grafana endpoints authenticate via a static shared secret configured in environment variables.

---

## 3. Kafka Pipeline

**Topics:**

| Topic | Producer | Consumer | Purpose |
|---|---|---|---|
| `raw-alerts` | WebhookHandler | AlertPipelineService | Raw normalized alert payloads from webhooks |
| `normalized-alerts` | AlertPipelineService | (analytics/monitoring) | Alerts after enrichment, before correlation |
| `correlation-results` | AlertPipelineService | (analytics/monitoring) | Final correlation decision + all strategy scores |

**Kafka-First Pattern:**

```
Webhook → publish to raw-alerts → return 202
                ↓
         Kafka Consumer (AlertPipelineService)
                ↓
         EnqueueAlert → internal channel → processAlert
```

This decouples ingestion from processing. Webhook handlers return fast (< 50ms). The pipeline processes at its own pace.

**Fallback (Kafka unavailable):**

```
Webhook → direct DB insert → EnqueueAlert (bypass Kafka)
```

No alerts are dropped if Kafka is down — they go directly into the pipeline channel.

---

## 4. Alert Pipeline Service

**Package:** `internal/services/pipeline/`  
**File:** `alert_pipeline.go`

This is the central orchestrator. It owns the channel, spawns correlation goroutines, calls all downstream engines, and writes the final result.

### 4.1 Service Struct

```go
type AlertPipelineService struct {
    parallelEngine      *correlation.ParallelCorrelationEngine
    aggregator          *correlation.CorrelationAggregatorService
    incidentSvc         *incidents.IncidentService
    db                  *sql.DB
    rcaURL              string           // RCA orchestrator; "" = disabled
    alertCh             chan *models.Alert // 2000-buffer queue
    llmEnricher         *LLMEnricher
    inflightDedup       sync.Map         // in-memory race guard
    clusterLocks        sync.Map         // per-cluster serialization
    normalizedPublisher *AlertKafkaProducer
    correlationPublisher *AlertKafkaProducer
    rootCauseEngine     *correlation.RootCauseEngine
    alertBuffer         *correlation.AlertBuffer  // Redis-backed state
}
```

### 4.2 Main Loop

```go
func (s *AlertPipelineService) Start(ctx context.Context) {
    go s.runBufferPromotionLoop(ctx)   // 30s ticker, rescues unlinked buffered alerts
    for {
        select {
        case alert := <-s.alertCh:
            go s.processAlert(ctx, alert)  // one goroutine per alert, no pool limit
        case <-ctx.Done():
            return
        }
    }
}
```

### 4.3 processAlert — Full Stage Sequence

```
processAlert(alert)
│
├─ [Resolved?] → handleResolvedAlert → update DB + auto-close incident if all alerts resolved → RETURN
│
├─ Publish alert to normalized-alerts Kafka topic
│
├─ STAGE 0: rootCauseEngine.Evaluate(alert)
│   ├─ ATTACH_TO_ROOT → mergeAlertIntoIncident → set Redis state → RETURN
│   ├─ CREATE_ROOT    → createRootIncident → suppressDescendants → triggerRCA → RETURN
│   └─ NO_ROOT        → fall through to scoring
│
├─ STAGE 1–4: parallelEngine.CorrelateAlert(alert)  [30s timeout, 4 goroutines]
│   ├─ Semantic strategy
│   ├─ Temporal strategy
│   ├─ Topology strategy
│   └─ Rules strategy
│
├─ aggregator.AggregateCorrelationResults(alert, strategyResults)
│
├─ Publish result to correlation-results Kafka topic
│
└─ Decision execution:
    ├─ CREATE_INCIDENT → createIncident (17-step deduplication cascade)
    ├─ MERGE_INCIDENT  → mergeAlertIntoIncident
    ├─ MONITOR         → deferredIncidentCreation (45s hold, 15s for critical)
    └─ DISCARD         → findOpenIncidentForAlert → attach if found, else drop
                          └─ async saveCorrelationResult (always)
```

### 4.4 Topology Path Construction

Before any correlation runs, the pipeline extracts a canonical topology path from the alert:

```
Format: <cluster>/<node>/<namespace>/<workload_kind>/<workload>
Example: mps-nonprod-rno/mps-nonprod-rno-worker-z3-08/monitoring/Deployment/prometheus

Fallback: h:<hostname>
Example:  h:bm-server-42.example.example.com
```

Source priority: alert labels (`k8s.cluster.name`, `k8s.node.name`, `k8s.namespace.name`, `k8s.workload.name`) → embedded text in description → empty string.

### 4.5 Blast Radius Fallback

After all correlation strategies run, if no topology graph returned blast radius nodes, the pipeline extracts them directly from alert labels:

```go
labelKeys := []string{
    "k8s.workload.name", "k8s.namespace.name",
    "k8s.cluster.name",  "host.name", "k8s.node.name",
}
// Unique values become blast radius nodes
```

### 4.6 LLM RCA Generation

After correlation decisions are made, the `llmEnricher` generates a human-readable root cause explanation using:
- `title`, `severity`, `dominant_strategy`
- `matched_label`, `node_type`, `root_cause_label`, `topology_path`
- Per-strategy scores

The result is stored in `pipeline_correlation_results.ai_root_cause`.

### 4.7 Deferred Incident Creation

When the aggregator returns `MONITOR` (score 0.20–0.40):

```go
func deferredIncidentCreation(alert, result, holdWindow) {
    go func() {
        time.Sleep(holdWindow)   // 45s default, 15s for critical
        // Re-check: was alert linked during hold window?
        // If yes → return (another alert created the incident)
        // If no  → re-run aggregator with current DB state
        //          → findOpenIncidentForAlert (cluster + title within 6h, then cluster-only within 30min)
        //          → create new incident if still no match
    }()
}
```

This batches bursts of related alerts before committing to incident creation.

### 4.8 Buffer Promotion Loop

Every 30 seconds, a background goroutine scans Redis for buffered alerts older than 60 seconds that have not been linked to an incident. It re-runs them through the decision path. This is the final safety net — ensures zero alerts are permanently lost.

---

## 5. Stage 0 — Root Cause Engine (Deterministic)

**Package:** `internal/services/correlation/`  
**File:** `root_cause_engine.go`

This engine runs **before** any probabilistic scoring. It uses hard structural facts — Dynatrace's own root cause entity, and the topology graph — to make deterministic decisions that skip the 4-strategy pipeline entirely.

### 5.1 Infrastructure Level Hierarchy

```
Level 5 — BM (bare metal server)
Level 4 — KVM hypervisor
Level 3 — VM (CloudStack / KVM guest)
Level 2 — K8s node
Level 1 — K8s pod / workload
Level 0 — Unknown
```

### 5.2 Three-Stage Evaluation

**Stage 1 — Dynatrace rootCauseEntity (Highest Trust)**

Dynatrace attaches a `rootCauseEntity` field to every problem notification identifying the root cause infrastructure entity (e.g., `HOST-abc123`, `KUBERNETES_NODE-xyz`).

```
alert.RootCauseEntity or alert.Labels["rootCauseEntity"] is set?
│
├─ YES → resolveDynatraceRoot:
│         Query open incidents WHERE:
│           topology_path LIKE '%<rootCauseEntity>%'
│           OR title/description CONTAINS rootCauseEntity
│           AND cluster matches (if alert has cluster)
│         
│         ├─ Incident found → ATTACH_TO_ROOT
│         └─ No incident    → CREATE_ROOT (this alert seeds the incident)
│
└─ NO  → Stage 2
```

**Stage 2 — Topology Graph: Higher Ancestor Has Incident**

```
topoCorrelator.Correlate(alert) → root_cause_node
│
├─ root_cause_node found AND root_level > alert_level?
│   Query open incidents for root entity (within 2h)
│   ├─ Found → ATTACH_TO_ROOT
│   └─ Not found → Stage 3
│
└─ No match → Stage 3
```

**Stage 3 — This Alert Is the Root**

```
alert_level ≥ VM (3) AND blast_radius.len > 0?
├─ YES → CREATE_ROOT
└─ NO  → NO_ROOT (fall through to 4-strategy pipeline)
```

### 5.3 Actions on Decision

| Decision | Action |
|---|---|
| `ATTACH_TO_ROOT` | `mergeAlertIntoIncident` — links alert to existing incident, updates `alert_ids[]`, adds timeline event |
| `CREATE_ROOT` | `createRootIncident` — creates incident, calls `suppressDescendantAlerts` (marks downstream alerts SUPPRESSED in Redis), triggers async RCA |
| `NO_ROOT` | Falls through; 4-strategy engine runs |

### 5.4 Root Promotion

When a new alert arrives and is merged into an existing incident, the engine checks if the new alert represents a **higher-level root cause** than the current incident root:

```
priority = severity_weight × infra_level

severity_weights: critical=4.0, high=3.0, medium=2.0, default=1.0

new_priority > current_priority?
→ Update incident: root_entity_id, root_entity_label, root_level (in davis_ai_analysis JSONB)
```

---

## 6. Stage 1–4 — Parallel Correlation Engine

**File:** `parallel_correlation_engine.go`

When Stage 0 returns `NO_ROOT`, this engine runs 4 strategies **concurrently** in separate goroutines with a shared 30-second timeout.

```go
type ParallelCorrelationEngine struct {
    semanticEngine         *SemanticCorrelationEngine
    enhancedTopologyEngine *EnhancedTopologyCorrelationEngine
    topoGraphCorrelator    *TopologyGraphCorrelator   // Redis-backed (priority)
    vectorStore            *WeaviateVectorStore
    weights                StrategyWeights
}

StrategyWeights{
    Semantic:  0.20,
    temp 0.10,
    Topology:  0.25,
    rule 0.25,
}
```

All 4 goroutines start simultaneously. Results are collected via a channel with per-result timeouts. If the 30s context deadline fires before all strategies complete, missing results receive a score of 0.

### 6.1 Semantic Strategy

**File:** `semantic_correlation.go`, `weaviate_vector_store.go`

**Goal:** Find alerts with semantically similar titles/descriptions, even if phrasing differs.

**Flow:**

```
1. Call semanticEngine.CorrelateAlertWithAI(alert)
   └─ If BERT service available:
       a. Generate text embedding for alert title + description
       b. Store embedding in Weaviate with alert_id, severity, source, timestamp
       c. Query Weaviate for nearest vectors within 0.75 cosine similarity threshold
          (lookback: 24 hours)
       d. Merge BERT results with Weaviate results
       e. Boost final score if Weaviate independently found matches

   └─ If BERT unavailable (fallback):
       a. Levenshtein distance on normalized title strings
       b. Jaccard similarity on description tokens
       c. Tag overlap ratio
       d. Weighted combination

2. Return: score (0.0–1.0), best_match alert, list of similar alerts
```

**Weaviate Schema:**  
Each alert stored as a vector document with metadata: `alert_id`, `severity`, `source`, `created_at`. Queries use cosine similarity with a 0.75 minimum threshold.

### 6.2 Temporal Strategy

**File:** `parallel_correlation_engine.go` → `runTemporalStrategy`

**Goal:** Find recently-created alerts that are temporally proximate, regardless of content.

**Flow:**

```
1. Query alerts from last 2 hours (excluding current alert)
2. For each candidate:
   a. Time decay score = exp(-0.1 × (timeDiffMinutes / 30))
      → Alert from 30 min ago scores ~0.90, from 60 min ago ~0.82, from 2h ~0.67
   b. Bonuses:
      +0.10 if same severity
      +0.05 if same source
   c. Cap at 1.0
3. Return candidates with score ≥ 0.60

Example: two critical Dynatrace alerts 5 min apart from same cluster
→ timeDiff=5min, score = exp(-0.1×(5/30)) = 0.984 + 0.10 + 0.05 = 1.0 (capped)
```

### 6.3 Topology Strategy

**File:** `topology_graph_correlator.go`, `enhanced_topology_correlation.go`

**Goal:** Find alerts on the same infrastructure node or parent/child nodes in the topology graph.

**Three-tier priority:**

**Priority 1 — Redis Topology Graph (TopoGraphCorrelator)**

The Redis graph stores the live infrastructure map refreshed every 5 minutes by the topology service:

```
Key structure: topo:node:<node_id>
Value: JSON blob with { node_type, parent_id, children[], cluster, labels }

Correlation:
  a. Extract entity_id from alert (from labels or metadata)
  b. Walk Redis graph upward: alert entity → parent → grandparent
  c. Compare each ancestor against recent alerts (2h lookback)
  d. If match found on ancestor node → infrastructure relationship score

Infrastructure relationship scores:
  Same BM server:          0.95
  VM on same BM:           0.92
  K8s node (same node):    0.92
  K8s cluster + namespace: 0.85
  K8s cluster only:        0.75

Also returns: blast_radius_nodes[], root_cause_node, TopoNodeInfo
```

**Priority 2 — Neo4j Enhanced Engine**  
If Redis graph returns no match, the Neo4j topology engine runs complex graph queries against the enterprise infrastructure topology database.

**Priority 3 — String Matching Fallback**  
Extracts node identifiers from label values and compares them by string equality. Used when neither Redis graph nor Neo4j is available.

### 6.4 Rules Strategy

**File:** `enhanced_rules_engine.go`

**Goal:** Apply operator-defined rules with named conditions and priority scores.

**Rule Structure:**

```go
type CorrelationRuleEnhanced struct {
    Name        string
    Priority    int     // 50–300; higher = higher score
    Conditions  []RuleConditionEnhanced
    Environment []string   // filter: "prod", "staging"
    Services    []string   // filter: service names
    SuccessRate float64    // learned from feedback
}

type RuleConditionEnhanced struct {
    Field     string    // "alert.title", "alert.severity", "alert.labels.cluster"
    Operator  string    // "equals" | "contains" | "regex" | "gt" | "lt" | "in" | "exists" | "starts_with" | "ends_with"
    Value     interface{}
    Weight    float64   // contribution weight within rule
    Required  bool      // if true, rule fails if this condition fails
    Negated   bool      // NOT operator
}
```

**Evaluation:**

```
1. Load rules from DB (synced every 5 minutes, max 1000, ordered by priority DESC)
2. For each enabled rule:
   a. Check environment/service filters match
   b. Evaluate each condition:
      - Extract field value from alert (supports nested: "alert.labels.k8s.cluster")
      - Apply operator (regex patterns pre-compiled and cached)
      - Apply negation
      - Weight condition score
   c. If any Required condition fails → skip rule
   d. Aggregate condition scores
3. Rule score = weighted_condition_average × priority_multiplier
   Priority multipliers: 200+→0.98, 150+→0.93, 100+→0.88, 50+→0.75
4. Return highest-scoring matched rule
```

**Rule Sync:**  
Background goroutine syncs rules from `correlation_rules` table every 5 minutes. Regex patterns are compiled once on sync and cached in `RuleCompiler`. Metrics tracked: `TotalRulesEvaluated`, `RulesMatched`, `AvgEvaluationTimeMs`, `CacheHitRate`.

---

## 7. Correlation Aggregator

**File:** `correlation_aggregator.go`

Receives all 4 strategy results and produces a single `FinalCorrelationResult`.

```go
StrategyWeights{
    Semantic:  0.25,
    temp 0.10,
    Topology:  0.35,   // highest weight — topology is most reliable
    rule 0.25,
}
```

### 7.1 Score Calculation

```
composite_score = Σ(strategy_score × weight)
                  for each strategy that returned a result

text_overlap_score = (title overlap + description overlap) / 2
  (compared against each candidate incident's title/description)

final_score = 0.70 × composite_score + 0.30 × text_overlap_score
              - positional_penalty (slight penalty for older candidates)
```

### 7.2 Confidence Calculation

Confidence is computed for audit purposes but does **not** gate decisions:

```
confidence = composite_score
           × strategy_agreement_factor  (1 / (1 + std_dev_of_scores))
           + 0.10 if ≥2 strategies > 0.50
           + 0.05 if all 4 strategies returned results
           (capped at 1.0)
```

High confidence means strategies agree. Low confidence means scores are spread. Neither blocks the decision.

### 7.3 Decision Rules (Signal-Based)

Decisions are made on **signal strength**, not confidence:

| Rule | Condition | Decision |
|---|---|---|
| Topology Determinism | topology_score ≥ 0.60 | MERGE (if candidates) or CREATE |
| Multi-Strategy Agreement | ≥ 2 strategies > 0.50 | MERGE (if candidates) or CREATE |
| Moderate Signal | final_score ≥ 0.40 | MERGE (if candidates) or CREATE |
| Near-Zero Signal | final_score < 0.20 | DISCARD |
| Low-Medium Signal | 0.20 ≤ score < 0.40 | MONITOR (deferred) |

**Why signal-based?**  
A single topo 0.45. Topology is deterministic — if the graph says two alerts are on the same K8s node, they correlate regardless of text similarity.

### 7.4 Candidate Incident Matching

Before scoring, the aggregator finds open incidents to merge into:

```sql
SELECT * FROM incidents
WHERE status IN ('open', 'investigating', 'acknowledged')
  AND created_at >= NOW() - INTERVAL '2 hours'
ORDER BY
  -- cascade dedup first
  CASE WHEN cluster matches THEN 0 ELSE 1 END,
  -- domain match (cpu/memory/disk/network/pod/node/host)
  CASE WHEN problem_domain matches THEN 0 ELSE 1 END,
  -- topology path overlap
  CASE WHEN topology_path overlaps THEN 0 ELSE 1 END,
  -- title similarity
  similarity(title, $alertTitle) DESC
LIMIT 20
```

---

## 8. Decision Execution

After the aggregator returns a decision, the pipeline executes it:

### 8.1 CREATE_INCIDENT

Calls `createIncident` which runs the 17-step deduplication cascade (see Section 9), then:

1. Calls `incidentSvc.InternalCreateIncident` (bypasses RBAC — automated path).
2. Sets `auto_created = true`.
3. Links `alert_ids = [alert.ID]`.
4. Sets `topology_path`, `blast_radius`, `dominant_strategy`, `correlation_confidence`.
5. Adds system timeline event: `"auto_created"`.
6. Triggers async RCA if `rcaURL` is configured.
7. Marks alert state as `INCIDENT_CREATED` in Redis.

### 8.2 MERGE_INCIDENT

Calls `mergeAlertIntoIncident`:

1. Appends `alert.ID` to `incidents.alert_ids[]`.
2. Updates `incidents.blast_radius` union.
3. Potentially promotes root (if new alert has higher infra level).
4. Adds timeline event: `"alert_added"`.
5. Marks alert state as `ATTACHED` in Redis.

### 8.3 MONITOR (Deferred)

Spawns background goroutine:

```
sleep(holdWindow)
  holdWindow = 45s  (15s for critical alerts)
↓
Was alert linked during sleep?
  YES → return (no action needed)
  NO  → re-run aggregator with fresh DB state
         → findOpenIncidentForAlert:
             1. cluster + title ILIKE match within 6h
             2. cluster-only match within 30min
         → if match: merge
         → if no match: createIncident
```

### 8.4 DISCARD

Does not create an incident. But first:

```
findOpenIncidentForAlert → if any match found, merge anyway
```

Then writes correlation result to `pipeline_correlation_results` with `decision=discard`.

### 8.5 Resolved Alert Handling

When an incoming alert has `status = "resolved"`:

1. Update alert `status = 'resolved'` in DB.
2. Find linked incident via `alert.incident_id`.
3. Query all other alerts on that incident.
4. If **all** alerts are now resolved → auto-close the incident (`status = 'resolved'`, set `resolved_at`).
5. Add timeline event: `"auto_resolved"`.

---

## 9. Deduplication Cascade

`createIncident` runs 11 checks sequentially before creating a new incident. Each check queries the DB and returns an existing incident to merge into if matched.

```
Check 1 — In-Flight Race Guard
  inflightDedup sync.Map (in-memory, per alert entity_id)
  If concurrent creation detected: poll DB for 3s, find the just-created incident
  Purpose: prevents two goroutines for the same burst creating two incidents simultaneously

Check 2 — Per-Cluster Lock
  clusterLocks sync.Map (mutex per cluster name)
  Serializes all incident creation for the same cluster
  Purpose: S23 guard — node-down followed by pod failures don't race

Check 3 — 5-Minute Title + Source Burst Dedup
  WHERE title prefix matches AND source matches AND created_at >= NOW() - 5min
  Purpose: repeated webhooks for the same event within a burst window

Check 4 — 2-Hour Entity ID Dedup
  WHERE metadata->>'entity_id' = $entityID AND created_at >= NOW() - 2h
  Purpose: Dynatrace re-fires the same problem ID

Check 5 — 6-Hour Cluster + Problem Domain Dedup
  WHERE topology_path LIKE '%<cluster>%'
    AND problem_domain IN ('cpu','memory','disk','network','pod','node','host') matches
    AND created_at >= NOW() - 6h
  Purpose: same cluster has the same class of problem; shouldn't be N separate incidents

Check 6 — 30-Minute Cross-Source Cascade Dedup
  WHERE (topology_path matches OR blast_radius overlaps)
    AND created_at >= NOW() - 30min
  Purpose: node-down from Prometheus + pod-oom from Dynatrace are the same event

Check 7 — 2-Hour Infrastructure Cascade
  WHERE topology_path contains same node/host entity
    AND created_at >= NOW() - 2h
  Purpose: long-running cascades (node recovery triggers restart storms)

Check 8 — Topology Cache Lookup
  Query topology snapshot for last-known node of the alert's workload
  If found in Redis graph → find incident for that historical node
  Purpose: rescheduled pods appear on a different node; still same underlying issue

Check 9 — 30-Minute Fingerprint Dedup
  fingerprint = MD5(normalized_title + severity + source)
  WHERE fingerprint matches AND created_at >= NOW() - 30min
  Purpose: exact duplicate from multiple webhook retries

Check 10 — 2-Hour Topology-Based Merge
  WHERE matched_label (from topology strategy) overlaps incident topology_path
    AND created_at >= NOW() - 2h
  Purpose: topology strategy identified the same infrastructure node as an existing incident

Check 11 — CREATE NEW INCIDENT
  No existing incident matched — create a fresh one
```

---

## 10. Alert State Machine

**File:** `alert_state_machine.go` (Redis-backed)

Every alert processed by the pipeline is tracked in Redis with its current state. This is the "no alert lost" guarantee.

### 10.1 States

| State | Meaning |
|---|---|
| `BUFFERED` | Received, waiting in deferred window |
| `ATTACHED` | Merged into an existing incident |
| `INCIDENT_CREATED` | Created a new incident |
| `SUPPRESSED` | Downstream of a root cause; explicitly ignored |

### 10.2 Redis Key Structure

```
Key:   alert:state:<alert_id>
Value: JSON { state, incident_id, suppression_reason, updated_at }
TTL:   72 hours
```

### 10.3 Buffer Promotion Loop

```
Every 30 seconds:
  Scan Redis for keys matching alert:state:*
  For each key with state=BUFFERED and updated_at > 60s ago:
    Re-run processAlert for this alert (fresh DB state)
    This promotes unlinked buffered alerts to incident creation
```

---

## 11. Incident Service

**Package:** `internal/services/incidents/`  
**File:** `incidents.go`

### 11.1 Incident Struct (Key Fields)

```go
type Incident struct {
    ID                    uuid.UUID
    IncidentNumber        int        // auto-generated sequential number
    Title, Description    string
    Severity              string     // critical, high, medium, low
    Status                string     // open, investigating, acknowledged, resolved, closed
    AlertIDs              []uuid.UUID  // all correlated alert IDs (JSONB)
    BlastRadius           []string     // affected entity labels
    TopologyPath          string       // "cluster/node/ns/kind/workload"
    DominantStrategy      string       // which strategy drove correlation
    CorrelationConfidence float64
    RCAStatus             string     // none, queued, investigating, complete
    RCAInvestigationID    string
    AutoCreated           bool
    DavisAIAnalysis       map[string]interface{}  // root_entity_id, root_entity_label, root_level
    CreatedAt, UpdatedAt  time.Time
    StartedAt             time.Time
    ResolvedAt            *time.Time
}
```

### 11.2 Auto-Creation Path

`InternalCreateIncident` is called by the pipeline (bypasses RBAC):

1. Validates required fields (title, severity).
2. Generates `incident_number` (atomic sequence).
3. Inserts into `incidents` table with `auto_created = true`.
4. Adds system timeline event (`event_type = 'auto_created'`).
5. Starts async AI analysis (spawns goroutine, non-blocking).

### 11.3 Timeline Synthesis

For incidents that predate the `incident_timeline` table (or have no rows), the timeline is synthesized from timestamps:

```
Synthetic events generated from:
  incidents.created_at        → "Incident Detected"
  incidents.started_at        → "Incident Auto-Created by Pipeline"
  incidents.acknowledged_at   → "Incident Acknowledged"
  incidents.resolved_at       → "Incident Resolved"
  alerts.first_seen_at        → "Alert Fired: <title>"
  alerts.last_seen_at         → "Alert Updated (if > first_seen_at)"
```

Real `incident_timeline` rows from the DB are merged with synthetic events, deduplicated by `event_type`, and sorted chronologically.

### 11.4 RCA Triggering

After incident creation, the pipeline calls:

```
POST <rcaURL>/investigate
{
  "alert_id":    "...",
  "incident_id": "...",
  "alert_body": {
    "title", "description", "severity", "source",
    "labels", "metadata"
  },
  "namespace", "cluster", "service"
}
```

Response: `{ investigation_id: "..." }`  
Written back to `incidents.rca_investigation_id`, `rca_status = 'investigating'`.

---

## 12. External Integrations

### PostgreSQL

Primary data store. Key tables:

| Table | Purpose |
|---|---|
| `alerts` | All ingested alerts, with labels, metadata, incident linkage |
| `incidents` | Correlated incidents with AI analysis, blast radius, topology |
| `incident_timeline` | Per-incident event log |
| `correlation_rules` | Operator-defined correlation rules |
| `pipeline_correlation_results` | Full strategy scores + decision for every processed alert |
| `webhook_api_keys` | API key authentication (SHA-256 hashed) |
| `alert_correlations` | Historical correlations from legacy engine |

### Redis

| Usage | Key Pattern | TTL |
|---|---|---|
| Alert state machine | `alert:state:<alert_id>` | 72h |
| Topology graph nodes | `topo:node:<node_id>` | 10min |
| Topology graph (full snapshot) | `topo:snapshot` | 5min |
| In-flight dedup | In-process `sync.Map` | Duration of goroutine |
| Per-cluster mutex | In-process `sync.Map` | Duration of goroutine |

### Kafka

| Topic | Message Schema | Producers | Consumers |
|---|---|---|---|
| `raw-alerts` | `{ alert_id, source, payload_json }` | WebhookHandler | AlertPipelineService |
| `normalized-alerts` | `{ alert }` (internal Alert struct) | AlertPipelineService | Analytics, monitoring |
| `correlation-results` | `{ alert_id, decision, scores, dominant_strategy }` | AlertPipelineService | Analytics, monitoring |

### Weaviate

Stores BERT text embeddings for alerts. Schema fields: `alert_id`, `severity`, `source`, `created_at`. Similarity threshold: 0.75 cosine. Lookback: 24 hours.

### Neo4j (optional)

Enterprise topology graph. Used as fallback if Redis topology graph is unavailable. Complex Cypher queries for infrastructure relationship detection.

### BERT Service

Two endpoints configured:
1. `BERT_SERVICE_URL` — primary (external)
2. `LOCAL_BERT_URL` — fallback (local)

Returns 768-dimensional float embeddings for input text. Used exclusively by the semantic strategy.

### Ollama (Local LLM)

Called by `ollama_service.go` for RCA explanation generation. Takes the `DavisCorrelationResult` struct and generates a natural language root cause summary. Non-blocking; failure is logged and skipped.

### RCA Orchestrator

Optional external service. Receives incident metadata and runs autonomous investigation. Returns `investigation_id`. Alerthub polls or receives callbacks to update `rca_status` and `rca_confidence`.

---

## 13. Full End-to-End Flow Diagram

```
EXTERNAL MONITORING SYSTEMS
Dynatrace │ Prometheus │ Grafana │ PagerDuty │ Splunk
          │
          ▼
┌─────────────────────────────────────────────────────┐
│           WEBHOOK HANDLER LAYER                     │
│                                                     │
│  1. Auth (API key SHA-256 / shared secret)          │
│  2. Normalize payload → internal Alert struct       │
│  3. Extract K8s labels, rootCauseEntity, severity   │
│  4. Dedup check: existing alert by source_id?       │
│  5. Publish to Kafka raw-alerts topic               │
│     (fallback: direct DB insert)                    │
└──────────────────┬──────────────────────────────────┘
                   │ Kafka: raw-alerts topic
                   ▼
┌─────────────────────────────────────────────────────┐
│           ALERT PIPELINE SERVICE                    │
│                                                     │
│  alertCh (2000-buffer) ← EnqueueAlert               │
│  Start() → goroutine per alert                      │
│                                                     │
│  ┌─────────────────────────────────────────────┐    │
│  │  processAlert(alert)                        │    │
│  │                                             │    │
│  │  Resolved? → handleResolvedAlert → DONE     │    │
│  │                                             │    │
│  │  Publish to normalized-alerts Kafka topic   │    │
│  │                                             │    │
│  │  ┌──── STAGE 0: ROOT CAUSE ENGINE ────┐     │    │
│  │  │  1. Dynatrace rootCauseEntity?     │     │    │
│  │  │     → ATTACH or CREATE             │     │    │
│  │  │  2. Topology ancestor w/ incident? │     │    │
│  │  │     → ATTACH                       │     │    │
│  │  │  3. This alert is root (level≥3)?  │     │    │
│  │  │     → CREATE                       │     │    │
│  │  │  4. NO_ROOT → fall through         │     │    │
│  │  └────────────────────────────────────┘     │    │
│  │           │ (NO_ROOT only)                  │    │
│  │           ▼                                 │    │
│  │  ┌──── PARALLEL ENGINE (30s) ─────────┐     │    │
│  │  │  ┌──────────┐  ┌──────────┐        │     │    │
│  │  │  │ Semantic │  │ Temporal │        │     │    │
│  │  │  │ (BERT +  │  │ (time    │        │     │    │
│  │  │  │ Weaviate)│  │  decay)  │        │     │    │
│  │  │  └────┬─────┘  └────┬─────┘        │     │    │
│  │  │       │             │              │     │    │
│  │  │  ┌────┴─────┐  ┌────┴──────┐       │     │    │
│  │  │  │ Topology │  │  Rules    │       │     │    │
│  │  │  │(Redis→   │  │ (DB rules │       │     │    │
│  │  │  │Neo4j→str)│  │ + regex)  │       │     │    │
│  │  │  └────┬─────┘  └────┬──────┘       │     │    │
│  │  │       └──────┬───────┘             │     │    │
│  │  │              ▼                     │     │    │
│  │  │    4 StrategyResult objects        │     │    │
│  │  └────────────────────────────────────┘     │    │
│  │           │                                 │    │
│  │           ▼                                 │    │
│  │  ┌──── AGGREGATOR ────────────────────┐     │    │
│  │  │  Weighted score (T:0.35 S:0.25     │     │    │
│  │  │    Te:0.25 R:0.15)                 │     │    │
│  │  │  Find candidate incidents          │     │    │
│  │  │  Signal-based decision             │     │    │
│  │  └────────────────────────────────────┘     │    │
│  │           │                                 │    │
│  │           ▼                                 │    │
│  │  Publish to correlation-results Kafka        │    │
│  │                                             │    │
│  │  ┌──── DECISION EXECUTION ────────────┐     │    │
│  │  │  CREATE  → 17-step deduplication cascade  │     │    │
│  │  │            → InternalCreateIncident│     │    │
│  │  │  MERGE   → mergeAlertIntoIncident  │     │    │
│  │  │  MONITOR → deferredCreation (45s)  │     │    │
│  │  │  DISCARD → findOpen or drop        │     │    │
│  │  └────────────────────────────────────┘     │    │
│  │           │                                 │    │
│  │           ▼                                 │    │
│  │  saveCorrelationResult (async)              │    │
│  │  triggerRCA (async, if rcaURL set)          │    │
│  └─────────────────────────────────────────────┘    │
│                                                     │
│  Buffer Promotion Loop (30s ticker)                 │
│  → rescues unlinked buffered alerts                 │
└─────────────────────────────────────────────────────┘
          │
          ▼
┌─────────────────────────────────────────────────────┐
│              INCIDENT SERVICE                       │
│                                                     │
│  InternalCreateIncident                             │
│    → incident_number (atomic sequence)              │
│    → auto_created = true                            │
│    → alert_ids[], blast_radius, topology_path       │
│    → timeline event: "auto_created"                 │
│    → async AI analysis (aiService.AnalyzeIncident)  │
│                                                     │
│  Incident fields:                                   │
│    severity, status, dominant_strategy              │
│    correlation_confidence, rca_status               │
│    davis_ai_analysis (root entity info)             │
└─────────────────────────────────────────────────────┘
          │
          ▼
    PostgreSQL (incidents table)
    + Redis (alert state)
    + RCA Orchestrator (async)
```

---

## 14. Performance Characteristics

| Metric | Target | Notes |
|---|---|---|
| Webhook response time | < 50ms | Kafka-first; processing is async |
| Correlation latency | < 30s | Hard deadline; distributed across 4 parallel strategies |
| Deduplication overhead | < 1s | In-memory maps + indexed DB queries |
| Incident creation | < 2s | After dedup cascade completes |
| Alert throughput | ~2000 queued | Channel buffer; scales with goroutine concurrency |
| Topology refresh | 5-minute background | Does not block correlation |
| Rules sync | 5-minute background | Max 1000 rules, regex pre-compiled |
| Buffer promotion scan | Every 30s | Ensures zero lost alerts within 90s worst-case |
| RCA trigger | Async, 10s timeout | Non-blocking; failure doesn't affect incident creation |

### Concurrency Model

```
WebhookHandler goroutine
    → Kafka publish (async)
    
Kafka Consumer goroutine
    → EnqueueAlert (non-blocking; drops if channel full with log warning)
    
Pipeline Start() goroutine
    → reads channel
    → spawns processAlert goroutine per alert (unbounded concurrency)
    
Each processAlert goroutine:
    → spawns 4 strategy sub-goroutines (with shared 30s context)
    → triggers async: saveCorrelationResult, triggerRCA, aiAnalysis
    
Buffer Promotion goroutine:
    → 30s ticker, independent of alert volume
```

---

*This document reflects the codebase as of image tag `v1.0.0` (2026-05-11).*
