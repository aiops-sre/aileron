# Aileron — Architecture Deep Dive

> Version: 2026-06 | Platform: AlertHub + KubeSense Agent

This document covers the internal architecture of the Aileron AIOps platform in engineering detail. For setup and quick start, see the [root README](../README.md).

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [End-to-End Data Flow](#2-end-to-end-data-flow)
3. [CACIE — Correlation Algorithm Details](#3-cacie--correlation-algorithm-details)
4. [OIE — Evidence DAG and Narrator Guards](#4-oie--evidence-dag-and-narrator-guards)
5. [Davis AI Algorithms in KubeSense](#5-davis-ai-algorithms-in-kubesense)
6. [Data Model](#6-data-model)
7. [Scalability Design](#7-scalability-design)

---

## 1. System Overview

Aileron ships two deeply integrated products running as separate Kubernetes deployments in the same cluster:

| Product | Namespace | Core Function |
|---|---|---|
| **AlertHub** | `aileron` | Multi-source alert ingestion → CACIE correlation → incident lifecycle → OIE evidence RCA → policy engine → MCP server |
| **KubeSense Agent** | `aileron-agent` | K8s topology mapping → 5 Davis AI algorithms → chaos readiness → change attribution → OIE evidence provider |

### Integration Points

KubeSense feeds AlertHub through three channels:

1. **Kafka topics** — `kubesense.chaos.scores`, `kubesense.config.violations`, `kubesense.llm.narratives` forwarded directly to AlertHub's Kafka broker
2. **OIE evidence fetcher** — OIE's `KubeSense Signals` fetcher queries `kubesense-api` during investigation via `GET /api/v1/correlation/incidents` and internal DB queries
3. **AIOps Situations tab** — AlertHub proxies `GET /api/v1/kubesense/*` requests to `kubesense-api` for unified display

AlertHub's backend acts as the single frontend gateway. Users never talk directly to KubeSense services.

---

## 2. End-to-End Data Flow

### Phase 1: Ingestion

```
Monitoring tool → POST /api/v1/webhooks/{source}/{platform}
                → NormalizationService.Normalize(rawPayload)
                → Alert{
                      id:          UUID,
                      source:      "dynatrace" | "prometheus" | "grafana" | ...,
                      source_id:   original alert ID (for dedup),
                      title:       normalized title,
                      severity:    "critical" | "high" | "medium" | "low",
                      status:      "firing" | "resolved",
                      entity_type: "HOST" | "SERVICE" | "K8S_POD" | ...,
                      cluster:     extracted cluster name,
                      labels:      JSONB (all original labels preserved),
                      fingerprint: SHA-256(source+entity_type+title+cluster),
                  }
                → Kafka producer → alerthub.raw-alerts
```

Fingerprinting ensures that repeated firing of the same alert (e.g., Prometheus resending every minute) generates a single unique token for deduplication.

### Phase 2: Three-Stage Pipeline

```
Kafka consumer (group: alerthub-pipeline-consumer)
       ↓
FAST PATH (32 workers, channel cap 10,000):
  - Check inflight sync.Map: if fingerprint already processing → DROP
  - Check PostgreSQL: if source_id already exists with status=firing → DROP
  - If status == resolved:
      → UPSERT alerts SET resolved_at = NOW() WHERE source_id = ?
      → UPSERT incidents SET status = resolved WHERE all alerts resolved
      → RETURN (fast exit, no correlation needed)
  - Otherwise: forward to TOPO PATH
       ↓
TOPO PATH (16 workers, channel cap 5,000):
  - UPSERT alert to PostgreSQL (source_id unique constraint)
  - Check for Dynatrace rootCauseEntity tag:
      → if present: deterministic ATTACH_TO_ROOT or CREATE_ROOT
  - Check topology score (Redis graph + Neo4j):
      → if score ≥ 0.60: deterministic ATTACH_TO_ROOT or CREATE_ROOT
  - Otherwise: forward to FULL PATH
       ↓
FULL PATH (8 workers, channel cap 2,000):
  - Run 4 correlation strategies in parallel (goroutines):
      → TopologyGraphCorrelator    (45% weight)
      → OperatorRulesCorrelator    (25% weight)
      → SemanticCorrelator/BERT    (20% weight)
      → TemporalDecayCorrelator    (10% weight)
  - Aggregate: weighted_sum = T×0.45 + R×0.25 + S×0.20 + Temp×0.10
  - 17-point dedup cascade (cluster mutex per entity)
  - if weighted_sum ≥ 0.75: MERGE into existing incident
  - else: CREATE new incident
       ↓
Output:
  - Incident upserted to PostgreSQL
  - Inline LLM narrative triggered (qwen2.5:3b, sub-second)
  - WebSocket broadcast to connected dashboards
  - Kafka event published → alerthub.incidents → OIE investigation
```

### Phase 3: OIE Investigation

```
Kafka consumer (group: oie-investigation-consumer)
→ alerthub.incidents event {incident_id, severity, topology_path}
→ Idempotency check (skip if already investigating)
→ GET /api/v1/topology/resolve → EntityProfile{cluster, namespace, node, vm}
→ Evidence Bus: 16 fetchers run in parallel (45s hard timeout)
→ Hypothesis Engine:
    - Generate hypotheses from collected evidence
    - Score each hypothesis: topology chain + evidence count + confidence
    - WinnerFrom gate: confidence ≥ 0.75 AND facts ≥ 3
→ LLM Narrator (7-layer guard, temperature=0.1):
    - sanitizeForPrompt()
    - countGroundingFacts() — block if 0 real facts
    - Generate 3-sentence root cause narrative
→ Writeback: POST /api/v1/incidents/:id/oie-result
    {confidence, band, root_cause_narrative, evidence_count, root_entity}
→ WebSocket broadcast: RCA result streaming to dashboard
```

### Phase 4: Resolution

```
Monitoring tool sends resolved webhook OR stale sweep fires
→ NormalizationService detects status=resolved
→ FAST PATH fast-exit:
    UPDATE alerts SET status='resolved', resolved_at=NOW()
    UPDATE incidents SET status='resolved', resolved_at=NOW()
         WHERE all linked alerts are resolved
→ PostmortemService.Generate() triggered:
    - if rca_confidence ≥ 0.60: LLM-generated postmortem
    - else: deterministic template
    - Stored in incidents.postmortem_json
```

---

## 3. CACIE — Correlation Algorithm Details

### Scoring Formula

```
final_score = topology_score × 0.45
            + rules_score × 0.25
            + semantic_score × 0.20
            + temporal_score × 0.10

Merge into existing incident if:  final_score ≥ 0.75
Create new incident if:           final_score < 0.75
Deterministic topology override:  topology_score ≥ 0.60 (bypass FULL PATH)
```

### Strategy 1: Topology Graph (45%)

The topology correlator uses a two-layer approach:

**Layer 1 — Redis fast path (sub-5ms):**
```
RedisGraph: MATCH (a)-[:RUNS_ON|MEMBER_OF|HOSTS]->(parent)
            WHERE a.entity_id = $entity_id
            RETURN parent.entity_id, parent.cluster
```
Checks if the alert entity shares a direct parent with any alert in open incidents. Redis TTL 5 minutes.

**Layer 2 — Neo4j recursive traversal (used for FULL PATH):**
```cypher
MATCH path = (root)-[rels:HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..8]->(target)
WHERE target.entity_id = $entity_id
WITH path_score = reduce(s=1.0, r IN rels | s × r.weight) × (decay ^ depth)
WHERE path_score > 0.20
RETURN root.entity_id, attenuated_score, depth, causal_chain
ORDER BY attenuated_score DESC LIMIT 10
```

Edge weights: `HOSTS=0.95`, `RUNS_ON=0.92`, `MEMBER_OF=0.88`, `MOUNTS=0.90`, `USES_NETWORK=0.70`, `DEPENDS_ON=0.65`

Domain-aware propagation decay: storage `0.90`, compute `0.88`, kubernetes `0.85`, network `0.82`

**Infrastructure priority boost** (selectBestRoot):
```
BareMetal × 1.50
KVM Host  × 1.30
VM        × 1.20
K8s Node  × 1.10
Pod       × 0.90
```

### Strategy 2: Operator Rules (25%)

Rules are stored in the `correlation_rules` table and loaded at startup with a 5-minute refresh cache:

```sql
SELECT id, name, pattern, entity_type, alert_type, weight, priority, enabled
FROM correlation_rules WHERE enabled = true ORDER BY priority DESC
```

Matching: each rule specifies a regex `pattern` matched against `alert.title + alert.source_id`. If `entity_type` is set, must also match `alert.entity_type`. Score is the rule's `weight` (0.0–1.0). Multiple matching rules take the maximum score.

### Strategy 3: BERT Semantic Embeddings (20%)

```
New alert title → BERT service (POST http://bert-service:8766/embed)
               → 384-dim float32 vector (all-MiniLM-L6-v2)
               → pgvector cosine similarity search:
                  SELECT incident_id, 1 - (embedding <=> $new_embedding) AS similarity
                  FROM alert_embeddings
                  WHERE status = 'firing'
                  ORDER BY similarity DESC LIMIT 10

Semantic score = highest cosine similarity from top-10 matches
```

The pgvector `ivfflat` index uses 100 lists (tuned for ~500k vectors). Cosine similarity is preferred over L2 for text embeddings.

### Strategy 4: Temporal Decay (10%)

```
temporal_score = exp(−λ × Δt)

Default: λ = 0.005/min → half-life = 30 min
Domain-aware profiles override λ:
  network:    half-life = 3 min  (fast cascades)
  storage:    half-life = 15 min (slow propagation)
  compute:    half-life = 8 min
  kubernetes: half-life = 5 min
  unknown:    half-life = 30 min (conservative)

Burst detection: if ≥ 3 alerts arrive within the burst threshold window,
apply burst boost (0.08–0.15) to temporal score.
```

### Dedup Cascade (17-point)

The full-path dedup cascade runs after scoring to prevent race conditions between concurrent Kafka consumer goroutines:

1. Check `inflight` sync.Map (in-memory, goroutine-safe)
2. Acquire per-cluster mutex
3. Check `source_id` unique constraint in PostgreSQL
4. Check fingerprint + 10-minute TTL window
5. Check existing incident for same entity in same cluster (< 5 min)
6. Check topology path overlap ≥ 0.70 with open incident
7. Check BERT similarity ≥ 0.85 with open incident
8–17: Various entity-type-specific and severity-based checks

---

## 4. OIE — Evidence DAG and Narrator Guards

### Evidence DAG Execution

OIE runs 16 fetchers concurrently using a `errgroup` with a 45-second context deadline:

```go
g, ctx := errgroup.WithContext(budgetCtx)
for _, fetcher := range oie.fetchers {
    f := fetcher
    g.Go(func() error {
        result, err := f.Fetch(ctx, profile)
        if err != nil { /* log, skip — don't fail investigation */ return nil }
        oie.bus.Add(result)
        return nil
    })
}
g.Wait() // waits up to 45s, partial results are fine
```

Each fetcher returns an `EvidenceItem{Type, Description, Confidence, Facts[]string}`. Fetchers that fail (timeout, connection error) are skipped — partial evidence is better than no investigation.

### Hypothesis Engine

```
1. Generate hypotheses from evidence items:
   - Topology upward sweep (root candidates from Neo4j)
   - Entity-specific hypotheses (node down → hypothesis: infra failure)
   - Change-based hypotheses (deployment rollout → hypothesis: change regression)

2. Score each hypothesis:
   score = (supporting_evidence_count / total_fetchers)
           × average_confidence_of_supporting_items
           × infra_level_weight

3. WinnerFrom gate:
   require: confidence ≥ 0.75 AND supporting_evidence_count ≥ 3
   if no winner: return uncertainty template (do NOT call LLM)
```

### 7-Layer LLM Guard

Layer execution order (each layer can block progression to the next):

| Layer | Function | Action on Trigger |
|---|---|---|
| 1 | `sanitizeForPrompt(text)` | Strip newlines, cap at 300 chars, remove RFC-1918 IPs, K8s UIDs, internal DNS names |
| 2 | `countGroundingFacts(items)` | If 0 numbered facts → return deterministic template, skip LLM |
| 3 | Temperature = 0.1 | Near-deterministic generation, applied to all LLM calls |
| 4 | Anti-hallucination system prompt | "Do NOT invent names, IP addresses, or service names. Only use facts numbered below." |
| 5 | `isLLMRefusal(response)` | Detects "I cannot", "as an AI", "I don't have information" → replace with template |
| 6 | Ensemble vote | If 2-model agreement enabled: both must agree on root entity name |
| 7 | Uncertainty fallback | `fmt.Sprintf("Investigation inconclusive. %d evidence items collected. Manual review required.", count)` |

The prompt structure enforces grounding:
```
SYSTEM: You are an SRE root cause analyst. You MUST only use the facts provided below.
        Do NOT invent names, IP addresses, pod names, or service names.
        If you are unsure, say so explicitly.

FACTS:
  1. {evidence_item_1.description}
  2. {evidence_item_2.description}
  ...

QUESTION: Based ONLY on the facts above, what is the most likely root cause?
          Write exactly 3 sentences. Start with "Root cause:".
```

---

## 5. Davis AI Algorithms in KubeSense

### Algorithm 1 — Holt-Winters Baseline

The baseline model tracks `level`, `trend`, and `seasonal` components per entity. The seasonal window is 168 slots (one per hour = 7 days).

```go
type BaselineModel struct {
    Level    float64
    Trend    float64
    Seasonal [168]float64  // 7-day hourly seasonal component
    MAD      float64       // Median Absolute Deviation for threshold
}

func (m *BaselineModel) Update(x float64, hour int) float64 {
    // Triple exponential smoothing (Holt-Winters multiplicative)
    L := alpha*(x/m.Seasonal[hour]) + (1-alpha)*(m.Level+m.Trend)
    T := beta*(L-m.Level) + (1-beta)*m.Trend
    S := gamma*(x/L) + (1-gamma)*m.Seasonal[hour]
    predicted := (L + T) * S
    m.Level, m.Trend, m.Seasonal[hour] = L, T, S
    m.MAD = updateMAD(m.MAD, math.Abs(x-predicted))
    return predicted
}

func (m *BaselineModel) IsAnomaly(x float64, hour int) bool {
    predicted := (m.Level + m.Trend) * m.Seasonal[hour]
    threshold := 3.5 * m.MAD
    return math.Abs(x-predicted) > threshold
}

// SeedFromDB pre-warms the model from 7 days of historical events on startup
// to avoid the cold-start false positive window
func (m *BaselineModel) SeedFromDB(ctx context.Context, db *sql.DB, entityID string) error
```

### Algorithm 3 — Union-Find Grouper

```go
type UnionFind struct {
    parent map[string]string
    rank   map[string]int
}

func computeGroupScore(a, b BufferEntry) float64 {
    topo := topologyProximityScore(a, b)   // 0.0–1.0
    label := jaccardSimilarity(a.Labels, b.Labels)  // |A∩B| / |A∪B|
    temporal := math.Exp(-math.Abs(a.OccurredAt.Sub(b.OccurredAt).Seconds()) / 300)
    family := boolToFloat(sameEventFamily(a.EventType, b.EventType))
    return topo*0.40 + label*0.30 + temporal*0.20 + family*0.10
}

// theta = 0.45: alerts scoring above this threshold are merged
// Window: 15 minutes — only alerts within this window are grouped
func (g *Grouper) Process(entries []BufferEntry) []Situation {
    uf := NewUnionFind()
    for i := range entries {
        for j := i+1; j < len(entries); j++ {
            if computeGroupScore(entries[i], entries[j]) >= g.Theta {
                uf.Union(entries[i].ID, entries[j].ID)
            }
        }
    }
    return uf.Groups() // each group becomes a Situation
}
```

### Algorithm 4 — Topology RCA Scoring

```go
var depthScore = map[string]int{
    "Node":       0,  // infrastructure root — highest score
    "Deployment": 1,
    "ReplicaSet": 2,
    "Pod":        3,  // symptom — lowest score
}

var infraBoost = map[string]float64{
    "Node":       1.50,
    "PVC":        1.30,
    "Deployment": 1.20,
    "Pod":        1.00,
}

func causalScore(entity Entity, incident Incident) float64 {
    depth := depthScore[entity.Kind]
    // Earlier events score higher (time recency boost)
    timeScore := math.Exp(-entity.FirstSeen.Sub(incident.StartedAt).Minutes() / 30.0)
    boost := infraBoost[entity.Kind]
    // Higher depth means deeper in the hierarchy = lower base score
    return (1.0 / float64(depth+1)) * timeScore * boost
}

// SymptomFilter: if a Node is identified as root cause,
// suppress Pod alerts from the same node to prevent noise
func SymptomFilter(incidents []Incident, rootCause Entity) []Incident {
    if rootCause.Kind != "Node" { return incidents }
    return filterOut(incidents, func(i Incident) bool {
        return i.Entity.Kind == "Pod" && i.Entity.NodeName == rootCause.Name
    })
}
```

### Algorithm 5 — Change Correlation

```go
func CorrelateChange(incident Incident, db *sql.DB) (*ChangeAttribution, error) {
    // Scan kubesense_changes for the 2-hour pre-incident window
    rows, _ := db.QueryContext(ctx, `
        SELECT id, change_type, resource_kind, resource_name, namespace,
               actor, occurred_at
        FROM kubesense_changes
        WHERE cluster_id = $1
          AND occurred_at BETWEEN $2 - INTERVAL '2 hours' AND $2
          AND namespace = $3
        ORDER BY occurred_at DESC`, incident.ClusterID, incident.StartedAt, incident.Namespace)

    for _, change := range changes {
        // overlapScore: fraction of incident resources touched by this change
        overlapScore := resourceOverlap(incident.AffectedResources, change.AffectedResources)
        // time delta from change to incident onset (minutes)
        dt := incident.StartedAt.Sub(change.OccurredAt).Minutes()
        // confidence decays exponentially with time delta (half-life ~30 min)
        confidence := overlapScore * math.Exp(-dt/30.0)
        if confidence > bestConfidence {
            bestConfidence = confidence
            bestChange = change
        }
    }
    return &ChangeAttribution{
        ChangeID: bestChange.ID, Actor: bestChange.Actor,
        Confidence: bestConfidence, TimeDelta: dt,
    }, nil
}
```

---

## 6. Data Model

### Key PostgreSQL Tables (AlertHub)

```sql
-- alerts: normalized alert from any source
CREATE TABLE alerts (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source       VARCHAR(50) NOT NULL,          -- "dynatrace", "prometheus", etc.
    source_id    VARCHAR(500) UNIQUE,           -- original ID for dedup
    title        TEXT NOT NULL,
    severity     VARCHAR(20) NOT NULL,          -- critical, high, medium, low
    status       VARCHAR(20) NOT NULL DEFAULT 'firing',
    entity_type  VARCHAR(100),                  -- HOST, K8S_POD, SERVICE, etc.
    fingerprint  VARCHAR(64) NOT NULL,          -- SHA-256 hash for fast dedup
    cluster      VARCHAR(255),
    labels       JSONB NOT NULL DEFAULT '{}',   -- all original labels preserved
    topology_path TEXT,                         -- e.g. "prod-cluster/node-01/app-pod"
    incident_id  UUID REFERENCES incidents(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at  TIMESTAMPTZ
);
CREATE INDEX idx_alerts_fingerprint ON alerts(fingerprint);
CREATE INDEX idx_alerts_source_id ON alerts(source_id) WHERE source_id IS NOT NULL;
CREATE INDEX idx_alerts_incident ON alerts(incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX idx_alerts_status_severity ON alerts(status, severity);

-- incidents: correlated group of alerts with shared root cause
CREATE TABLE incidents (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title               TEXT NOT NULL,
    status              VARCHAR(20) NOT NULL DEFAULT 'open',
    severity            VARCHAR(20) NOT NULL,
    rca_status          VARCHAR(20) DEFAULT 'pending',  -- pending, investigating, complete
    rca_confidence      FLOAT,
    ai_root_cause       TEXT,                -- 3-sentence LLM narrative
    blast_radius        JSONB DEFAULT '[]',  -- affected entity IDs
    topology_path       TEXT,
    alert_ids           JSONB DEFAULT '[]',
    correlation_confidence FLOAT,
    davis_ai_analysis   JSONB DEFAULT '{}',  -- full hypothesis registry
    postmortem_json     JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at         TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_incidents_status ON incidents(status);
CREATE INDEX idx_incidents_severity ON incidents(severity);
CREATE INDEX idx_incidents_created ON incidents(created_at DESC);

-- rca_investigations: OIE investigation results with semantic embeddings
CREATE TABLE rca_investigations (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    incident_id         UUID REFERENCES incidents(id),
    status              VARCHAR(20) NOT NULL,  -- investigating, complete, failed
    confidence          FLOAT,
    root_cause_summary  TEXT,
    domain              VARCHAR(50),           -- storage, network, compute, kubernetes
    evidence_count      INTEGER DEFAULT 0,
    embedding           vector(768),           -- nomic-embed-text for semantic search
    raw_evidence        JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rca_embedding ON rca_investigations USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- intelligence_policies: operator-configurable suppression and routing policies
CREATE TABLE intelligence_policies (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name       VARCHAR(255) NOT NULL,
    type       VARCHAR(50) NOT NULL,   -- suppress_alert, suppress_incident, skip_rca,
                                       -- require_approval, auto_resolve
    conditions JSONB NOT NULL DEFAULT '{}',
    priority   INTEGER NOT NULL DEFAULT 100,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- 5-minute TTL cache in-memory, 500 policy hard limit

-- correlation_rules: operator-authored rules for CACIE rules strategy
CREATE TABLE correlation_rules (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        VARCHAR(255) NOT NULL,
    pattern     TEXT NOT NULL,         -- regex matched against title
    entity_type VARCHAR(100),          -- if set, must match alert.entity_type
    weight      FLOAT NOT NULL DEFAULT 0.8,
    priority    INTEGER NOT NULL DEFAULT 100,
    enabled     BOOLEAN NOT NULL DEFAULT true
);
```

### Key KubeSense PostgreSQL Tables

```sql
-- kubesense_health_events: all K8s health events from the agent
CREATE TABLE kubesense_health_events (
    id            VARCHAR(64) PRIMARY KEY,
    cluster_id    VARCHAR(128) NOT NULL,
    event_type    VARCHAR(100) NOT NULL,  -- health.pod.crashloopbackoff, etc.
    severity      VARCHAR(20) NOT NULL,
    resource_kind VARCHAR(64),
    namespace     VARCHAR(255),
    resource_name VARCHAR(255),
    resource_uid  VARCHAR(64),
    occurred_at   TIMESTAMPTZ NOT NULL,
    received_at   TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_ks_he_cluster_occurred ON kubesense_health_events(cluster_id, occurred_at DESC);
CREATE INDEX idx_ks_he_cluster_type ON kubesense_health_events(cluster_id, event_type);

-- kubesense_changes: GitOps and K8s change events for change correlation
CREATE TABLE kubesense_changes (
    id            VARCHAR(64) PRIMARY KEY,
    cluster_id    VARCHAR(128) NOT NULL,
    change_type   VARCHAR(100) NOT NULL,  -- change.deployment.rollout, etc.
    resource_kind VARCHAR(64),
    namespace     VARCHAR(255),
    resource_name VARCHAR(255),
    actor         VARCHAR(255),           -- ArgoCD, kubectl, operator
    occurred_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_ks_changes_cluster_time ON kubesense_changes(cluster_id, occurred_at DESC);
CREATE INDEX idx_ks_changes_ns ON kubesense_changes(namespace, occurred_at DESC);

-- kubesense_incidents: correlated K8s situations
CREATE TABLE kubesense_incidents (
    id                 VARCHAR(64) PRIMARY KEY,
    cluster_id         VARCHAR(128) NOT NULL,
    fingerprint        VARCHAR(64) NOT NULL,
    incident_type      VARCHAR(100) NOT NULL,
    severity           VARCHAR(10) NOT NULL,    -- P1, P2, P3, P4
    phase              VARCHAR(20) NOT NULL DEFAULT 'Detecting',
    summary            TEXT,
    namespace          VARCHAR(255),
    resource_kind      VARCHAR(64),
    resource_name      VARCHAR(255),
    rule_name          VARCHAR(255),
    signal_count       INTEGER NOT NULL DEFAULT 1,
    correlated_signals JSONB NOT NULL DEFAULT '[]',
    timeline           JSONB NOT NULL DEFAULT '[]',
    first_observed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_at          TIMESTAMPTZ,
    resolved_at        TIMESTAMPTZ
);
```

### Neo4j Graph Schema

```cypher
// Node types and uniqueness constraints
CREATE CONSTRAINT ON (n:BareMetal)   ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:KVMHost)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:CloudVM)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:K8sCluster)  ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:K8sNode)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:K8sPod)      ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:NetAppVol)   ASSERT n.entity_id IS UNIQUE;

// Node properties
// entity_id, entity_type, label, cluster, namespace, infra_level
// infra_level: 0=BareMetal, 1=KVM, 2=VM, 3=K8sNode, 4=Deployment, 5=Pod

// Edge types with propagation weights
// (bm:BareMetal)-[:HOSTS {weight:0.95}]->(kvm:KVMHost)
// (kvm:KVMHost)-[:HOSTS {weight:0.95}]->(vm:CloudVM)
// (vm:CloudVM)-[:RUNS_ON {weight:0.92}]->(node:K8sNode)
// (node:K8sNode)-[:MEMBER_OF {weight:0.88}]->(cluster:K8sCluster)
// (pod:K8sPod)-[:RUNS_ON {weight:0.92}]->(node:K8sNode)
// (pod:K8sPod)-[:MOUNTS {weight:0.90}]->(vol:NetAppVol)

// Topology refresh intervals
// K8s topology:      every 5 minutes (via kubesense-agent informers)
// CloudStack VMs:    every 15 minutes
// NetApp volumes:    every 30 minutes
// Redis cache TTL:   5 minutes
```

---

## 7. Scalability Design

### Worker Pool Sizing

The three-stage pipeline uses separate channel-backed worker pools to isolate backpressure:

| Stage | Workers | Channel Cap | Target Latency | Bottleneck |
|---|---|---|---|---|
| FAST PATH | 32 | 10,000 | < 2ms | CPU (fingerprint hash) |
| TOPO PATH | 16 | 5,000 | < 50ms | Redis RTT |
| FULL PATH | 8 | 2,000 | < 5s | Neo4j + BERT embedding RTT |

Backpressure propagation: if FULL PATH channel is full, TOPO PATH drops to FULL PATH overflow (logged). If FAST PATH is full, critical alerts bypass to TOPO PATH directly; non-critical are dropped with a counter increment.

### Kafka Partitioning

Alerts are partitioned by cluster name to ensure in-order deduplication:

```go
func partitionKey(alert *Alert) string {
    if cluster := alert.Labels["k8s.cluster.name"]; cluster != "" {
        return cluster
    }
    return alert.Source // fallback
}
```

This guarantees that all alerts from the same cluster hit the same consumer goroutine, making the fingerprint dedup map effective without distributed locking.

### pgvector Index Tuning

The RCA investigation embedding index uses `ivfflat` with 100 lists:

```sql
CREATE INDEX idx_rca_embedding ON rca_investigations
USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
-- Tune: lists = sqrt(row_count), set_limit for query: probes = 10
SET ivfflat.probes = 10;  -- recall vs. speed tradeoff
```

For tables above 1M rows, switch to `hnsw` for better recall:
```sql
CREATE INDEX idx_rca_embedding ON rca_investigations
USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
```

### OIE Investigation Concurrency

OIE is semaphore-limited to prevent overwhelming Neo4j and PostgreSQL during alert storms:

```go
type OIEService struct {
    sem *semaphore.Weighted  // max 20 concurrent investigations
}

func (s *OIEService) Investigate(ctx context.Context, incident Incident) error {
    if err := s.sem.Acquire(ctx, 1); err != nil {
        return fmt.Errorf("investigation queue full: %w", err)
    }
    defer s.sem.Release(1)
    // ...run investigation...
}
```

The 45-second budget per investigation means at 20 concurrent investigations, OIE can process 20 × (60/45) ≈ 27 new incidents per minute at saturation.

### Redis Cluster Configuration

Redis runs as a 3-node cluster:

- **Topology cache**: `topology:{entity_id}` keys, TTL 5 minutes. LRU eviction.
- **Pipeline state**: fingerprint inflight keys, TTL 10 minutes.
- **Rate limiting**: Lua scripts for sliding window rate limits (burst protection).
- **Pub/Sub**: WebSocket broadcast channel for real-time dashboard updates.
- **Hypothesis registry**: `rca:hyp:{alert_id}` keys, TTL 2 hours.

Separate Redis keyspace prefixes prevent cross-feature interference. Use `redis-cli --cluster info` to verify hash slot distribution across the 3 nodes.

### Horizontal Scaling

| Service | Scale Out | Constraint |
|---|---|---|
| `aileron-platform` | Add replicas freely | Redis-backed rate limiting ensures consistent behavior |
| `aileron-oie` | Add replicas | Kafka consumer group auto-rebalances; sem limit is per-pod |
| `aileron-frontend` | Add replicas | Stateless |
| `kubesense-api` | Add replicas | Buffer feeder uses Redis distributed lock to run on only one pod |
| `kubesense-collector` | Single instance recommended | Kafka consumer group handles it; multiple instances can double-write |
| `bert-service` | Add replicas | Stateless |
| `ollama` | Single GPU instance | Model loading is expensive; add a second for HA |

### Stale Sweep

A background goroutine in `aileron-platform` runs hourly to clean up stuck states:

- Fingerprint-only alerts open > 4 hours with no incident → auto-resolve
- All alerts with no update in 24 hours → auto-resolve
- Incidents with all alerts resolved → mark resolved
- OIE investigations stuck in `investigating` > 15 minutes → mark failed

This prevents accumulating open incidents from monitoring sources that send firing but never resolved events.
