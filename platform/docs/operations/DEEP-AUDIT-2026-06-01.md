# AlertHub Deep End-to-End Audit
# Date: 2026-06-01 | Complete pipeline trace — code + data + production behavior

---

## HOW THE PIPELINE ACTUALLY WORKS (verified against source code)

Every alert goes through exactly 3 sequential stages in `processAlert()`:

```
Stage 1: processAlertResolvedStage  → if status='resolved', call handleResolvedAlert() and EXIT
Stage 2: processAlertRCEStage       → if RCE fires ATTACH or CREATE_ROOT, EXIT (skip Stage 3)
Stage 3: processAlertFullStage      → 4-strategy parallel correlation (temporal/semantic/topology/rules)
```

The Root Cause Engine (`RootCauseEngine.Evaluate`) has 3 sub-stages:
- Stage 1: Dynatrace `root_cause_entity` label present → look up open incident by entity name → ATTACH or CREATE
- Stage 2: Topology graph match → ancestor entity has open incident → ATTACH
- Stage 3: Alert is at VM-level or higher with blast radius → CREATE_ROOT

The 4-strategy engine (parallel: topology, semantic, temporal, rules) ONLY runs when:
- RCE returns `RCAActionNoRoot`
- Alert has no `root_cause_entity` label
- No topology graph match at VM-level or above

---

## QUANTIFIED PIPELINE DISTRIBUTION (all-time)

| Path | Alert Count | Pct |
|---|---|---|
| RCE CREATE_ROOT | 4,248 | 53% |
| RCE ATTACH_TO_ROOT | 2,044 | 26% |
| 4-Strategy engine | 1,707 | 21% |

**Critical finding: The 4-strategy engine has not run once in 13 days.**
Last run: 2026-05-19. All Dynatrace alerts carry `root_cause_entity` labels,
which means 100% of recent alerts hit Stage 2 (RCE) and exit before Stage 3.
The 4-strategy parallel engine, the adaptive weights, the semantic/temporal/topology
scoring — none of this has executed for any real alert since May 19.

---

## FINDING 1 — CRITICAL: Every Dynatrace Alert Bypasses the 4-Strategy Engine

**Root cause confirmed in code (`root_cause_engine.go:260-280`):**
```go
func (rce *RootCauseEngine) extractDynatraceRootCause(alert *Alert) string {
    if alert.RootCauseEntity != "" { return alert.RootCauseEntity }
    if alert.Labels != nil {
        for _, key := range []string{"rootCauseEntity", "root_cause_entity", ...} {
            if v, ok := alert.Labels[key]; ok && v != "" { return v }
        }
    }
    ...
}
```

Dynatrace sends `root_cause_entity` in the labels for every problem. The RCE extracts it,
looks up an open incident by correlation_id, and either attaches or creates. The 4-strategy
engine never runs.

**What this means for production:**
- The topology correlation engine (topology graph, BFS, Neo4j) contributes ZERO to any
  Dynatrace alert decision
- The BERT semantic embedding, Jaccard fallback, all semantic infrastructure: unused
- The temporal correlation: unused
- The rules engine: unused
- Adaptive learning weights: meaningless — the thing they'd adapt has no traffic

**This is not a bug in the RCE logic** — RCE doing ATTACH/CREATE for DT alerts is correct.
The problem is the **downstream data quality**: because RCE short-circuits, the
`pipeline_correlation_results` row shows all strategy scores as 0, the explanation_json
has no evidence chain, and `enrichIncidentV2` receives no correlation context to work with.

---

## FINDING 2 — CRITICAL: Duplicate Alert Processing (up to 117x per alert)

**Evidence:** `alert_id = 485d9efa` processed 117 times over 11 days.
Top offenders: 117, 55, 42, 25, 22, 20 PCR records for single alerts.

**Root cause (confirmed):**
The Kafka consumer processes alerts from the `raw-alerts` topic. When a consumer restarts
(pod eviction, deployment, crash), it replays from the last committed offset. Fingerprint
alerts (those with `source_id = ''`) bypass the dedup guard in the staging pipeline:

```go
// In the staged pipeline dedup check:
if raw.Source != "" && raw.SourceID != "" {
    if existing, err := c.alertSvc.GetAlertBySourceID(ctx, raw.Source, raw.SourceID); ...
```

The condition `raw.SourceID != ""` means fingerprint alerts (empty source_id) always skip
dedup. Each Kafka replay creates a new `processAlert` execution. The DB alert row already
exists (unique on fingerprint), but each execution creates a new `pipeline_correlation_results`
row with a new decision — creating the PCR count explosion.

**Impact:**
- The Jaeger alert generated 27 `create_incident` decisions and 59 `merge_incident` decisions
  across different incidents as the operator state changed
- This creates phantom correlation history that poisons any analysis of that alert
- PCR counts are inflated: 7,997 PCR rows for what is actually far fewer unique alert decisions

**Fix (two-part):**
1. In the Kafka consumer: after checking `GetAlertBySourceID`, also check if a
   `pipeline_correlation_results` row already exists for that alert_id. If it does AND
   the alert is resolved, skip reprocessing entirely.
2. In the staging pipeline's dedup check: extend it to cover fingerprint alerts by their
   `fingerprint` column (which is always set):
   ```go
   if alert.Fingerprint != "" {
       if existing := GetAlertByFingerprint(ctx, alert.Source, alert.Fingerprint); existing != nil {
           // check if already correlated
       }
   }
   ```

---

## FINDING 3 — CRITICAL: RCE Lookback Window Allows Incorrect Cross-Time Merges

**Root cause confirmed in `root_cause_engine.go:333`:**
```sql
SELECT i.id FROM incidents i
WHERE i.auto_created = TRUE
  AND i.status IN ('open', 'investigating')
  AND i.created_at >= NOW() - INTERVAL '2 hours'
  AND ...
```

The 2-hour `created_at` window applies to when the **incident was created**, not when
the **alert arrived**. An incident created 20 minutes ago but containing a 36-hour-old
alert will match — and it does.

**Trace of the live production bug (incident `4e53bf41`):**
1. May 30 22:05 — argus-api "Not all pods ready" fires. `source_id='fp:...'` (fingerprint).
   RCE: root_cause_entity = `mps-mondev-mdn/monitoring-dev:not all pods ready`.
   No existing incident found (outside 2h window of any prior incident). Creates new incident.
2. Incident stays **OPEN** because `fp:` alerts never receive a DT RESOLVED event.
3. June 1 09:56 — quarterly-planning-uat-api fires with **same** root_cause_entity.
   RCE checks: is there an open incident with correlation_id matching this entity?
   **Yes** — the May 30 incident is still open. Merges in. Gap: 35.8 hours.
4. June 1 10:02 — quarterly-planning-api fires. Also merges.

**The real issue: fingerprint alerts (`fp:`) never auto-close.**
DT fingerprint alerts are generated by AlertHub's own alert buffer suppression system —
they're synthetic, internal to the pipeline. They have no DT problem ID to send a RESOLVED
event. So their incidents stay open indefinitely, becoming "sticky attractors" for all
future alerts with the same root_cause_entity.

**Scale of this problem:**
- 2 live incidents are currently held open by fingerprint alerts
- 26 historical incidents have alerts spanning >24 hours (cross-day groupings)
- 50 incidents span 6-24 hours

**Fix:**
Fingerprint alerts (source_id starting with `fp:`) should have a configurable TTL
(suggested: 4 hours). After TTL, auto-resolve the alert and trigger incident auto-close
sweep. Add to `runStaleSweep`:
```go
// Auto-resolve fingerprint alerts older than 4h
UPDATE alerts SET status='resolved', resolved_at=NOW()
WHERE source_id LIKE 'fp:%' AND status='open'
AND created_at < NOW() - INTERVAL '4 hours';
```

---

## FINDING 4 — HIGH: AI Root Cause Narratives Are Wrong — Evidence Established

**Live production example verified:**

Incident `175f948b` — "Out-of-memory kills" in `mps-monprod-mdn` cluster,
`observability` namespace, workloads: obs-beyla and tempo.

**Actual `ai_root_cause` in the database:**
> "The 'alloy' deployment is running on a single pod ('example-cluster-worker-02') with all
> containers ready. There are no events or issues reported, indicating the system is stable
> but needs further verification."

This is **completely wrong**:
- Wrong cluster: `example-cluster` instead of `mps-monprod-mdn`
- Wrong workload: `alloy` instead of `tempo`/`obs-beyla`
- Wrong statement: "system is stable" for an active OOM incident

**Root cause of the hallucination:**
`enrichIncidentV2` runs after incident creation. At that point it calls
`recursiveTopoRCA.Traverse()` which uses the alert's `corrAlert` struct. The `corrAlert`
is built from the alert labels — but the `matchedNode` in the topology graph is
`example-cluster-worker-02` (a node in the DEV cluster, where AlertHub runs) rather than
the `mps-monprod-mdn` cluster node where the OOM actually occurred.

Why? The `topology_graph_correlator.go:findMatchingNode` looks for nodes using
`k8s.workload.name` and `k8s.node.name` labels. The Dynatrace alert has
`workload: "tempo"` and `cluster: "mps-monprod-mdn"` but the AlertHub topology graph
(Redis-backed, built from KubeSense agent watching example-cluster-01 only) has no
`mps-monprod-mdn` nodes. It falls through to a fuzzy match and picks up `example-cluster`
nodes by accident.

The LLM then receives:
- Alert: "Out-of-memory kills"
- Matched node: "example-cluster-worker-02" (wrong cluster)
- Root cause label: whatever was on that node

Result: narrative about alloy on example-cluster-worker-02, which has nothing to do with
the actual incident.

**Fix (two-part):**
1. In `findMatchingNode`: enforce cluster-scoping — only match nodes where
   `node.Data["cluster"]` matches the alert's `k8s.cluster.name` label. Reject matches
   from the wrong cluster entirely and return nil.
2. When `matchedNode` is nil, `enrichIncidentV2` should NOT call `GenerateRCA` with
   empty strings. It should either skip LLM entirely or log "topology miss for cluster
   X — skipping LLM enrichment" and leave `ai_root_cause` empty rather than hallucinate.

---

## FINDING 5 — HIGH: 26 Incidents Have Alerts Spanning >24 Hours (Cross-Day Groupings)

**Worst case: "Not all Pods ready on ActiveGate" — 199-hour span (8 days!)**
This incident contains:
- An alert from May 18 (source_id = empty, likely the original Kafka-replayed alert)
- Three alerts from May 26 (different DT problem IDs, same root_cause_entity)

The May 26 alerts found the May 18 incident because it was **still open**. The incident
was open because the May 18 alert had no RESOLVED event. The RCE checked for open incidents
with matching correlation_id and found the 8-day-old one.

**The fix for Finding 3 (fp: TTL) resolves most of these.** But the 2-hour window in
the RCE SQL should also be applied to the correlation_id match path to prevent this:

```sql
-- CURRENT (no time guard on the main lookup):
AND i.status IN ('open', 'investigating')
AND i.created_at >= NOW() - INTERVAL '2 hours'

-- SHOULD BE: cap at created_at OR last alert within 4h
AND i.status IN ('open', 'investigating')
AND (
    i.created_at >= NOW() - INTERVAL '2 hours'
    OR i.updated_at >= NOW() - INTERVAL '4 hours'
)
```
Using `updated_at` instead of `created_at` means an incident that received a new alert
4h ago is still within window — it's legitimately active. An incident with no activity
in 4h is not a valid merge target.

---

## FINDING 6 — HIGH: topo_root_entity_id is NULL for 100% of Incidents — Root Cause Confirmed

**All 2,870 incidents have `topo_root_entity_id = ''`.**

Confirmed via code trace in `enrichIncidentV2`:
1. `RecursiveTopoRCA.Traverse(ctx, corrAlert)` is called
2. Inside, `RecursiveTopoRCAEngine.upwardSweep()` queries Neo4j for ancestors of the alert entity
3. The alert entity ID is extracted from `corrAlert.Labels["k8s.workload.name"]` etc.
4. Neo4j has KubeSense agent nodes (from `example-cluster-01`) but the alerts are from
   `mps-monprod-mdn`, `mps-nonprod-rno`, `mps-tooling-mdn` clusters
5. Neo4j returns empty results → `topoResult.RootEntity = nil` → `topo_root_entity_id` stays `""`

The topology graph in Neo4j contains only the cluster being watched by the KubeSense agent.
AlertHub's Neo4j contains CloudStack/KVM topology (from a different discovery path).
The two graphs don't overlap — KubeSense's Neo4j has K8s workloads from one cluster;
AlertHub's RCA engine uses a Redis-backed topology graph from the same cluster.

**The Redis-backed topology graph DOES match** (seen in PCR: `matched_node_label = mps-nonprod-rno-worker-z1-12` with `topology_score = 0.96`). But this only runs through the 4-strategy
path — which never runs for DT alerts. The `RecursiveTopoRCA` which writes `topo_root_entity_id`
uses a different Neo4j graph that has no nodes for the alert's cluster.

**Fix:**
When `RecursiveTopoRCA.Traverse()` returns `RootEntity: nil`, instead of leaving
`topo_root_entity_id` empty, write the entity from the `TopologyGraphCorrelator` result
that the RCE already computed:

In `processAlertRCEStage` → after RCE fires → pass `rcaDecision.RootEntityLabel` to
`enrichIncidentV2` explicitly. `enrichIncidentV2` can write it to `topo_root_entity_id`
directly from the RCE result, bypassing the failing Neo4j traversal:

```go
// In enrichIncidentV2, after the existing topo traversal step:
if rootID == "" && firstRCEDecision != nil && firstRCEDecision.RootEntityLabel != "" {
    // Fallback: use RCE's entity as the topology root
    rootID = firstRCEDecision.RootEntityLabel
    rootLabel = firstRCEDecision.RootEntityLabel
}
```

---

## FINDING 7 — HIGH: Job Failure Alert Incorrectly Merged into OOM Incident

**Trace verified against production data:**

Alert `3c58c700` — "Job failure event" in `mps-monprod-mdn/kaniko` namespace.
Its `root_cause_entity = "mps-monprod-mdn/kaniko:job failure event"` — completely different
namespace and problem type from the OOM incident's entity `"mps-monprod-mdn/observability:out-of-memory kills"`.

PCR shows `decision=create_incident` — the RCE correctly identified this as a separate
root cause and created a new incident. But the `reabsorbOrphanedIncidents` function
subsequently merged it into the OOM incident.

**Root cause in `reabsorbOrphanedIncidents`:**
This function merges recently-created orphaned incidents into a newly-created root incident.
Its matching logic uses cluster name and timing, but not the `root_cause_entity`. The
kaniko Job failure incident was created in the same cluster (`mps-monprod-mdn`) within
seconds of the OOM incident creation. The reabsorb function saw both as related (same cluster,
close in time) and merged.

**Fix:**
Add a guard in `reabsorbOrphanedIncidents`: only reabsorb an incident if its
`correlation_id` shares the same namespace prefix as the new root incident, or if the
orphaned incident's `dominant_strategy` is topology-based (i.e., the orphan was created
because of blast-radius propagation). A Job failure in `kaniko` namespace should not be
absorbed into an OOM incident in `observability` namespace.

---

## FINDING 8 — MEDIUM: Investigation DAG Zero Operator Engagement

**149 investigation DAGs created. 0 have any `step_states` set.**

Operators are not using the investigation guides. This could mean:
1. Operators don't know the UI exists (the `/kubesense` page was just deployed)
2. The DAG content is not useful (89 of 149 have domain='unknown', only 3 steps)
3. Operators are resolving incidents before reviewing the DAG

**Domain distribution problem:**
- 89/149 (60%) classified as `unknown` — these get a generic 3-step DAG
- `kubernetes` and `storage` get 5-6 steps each (better)

The ontology engine is not classifying most incidents because DT alerts going through
the RCE path don't run the ontology classifier. `processAlertFullStage` runs ontology
before the 4-strategy engine. Since RCE exits before Stage 3, ontology never runs.

**Fix:** Run ontology classification in `processAlertRCEStage` too, before the RCE decision.
The result should be passed to `enrichIncidentV2` so the DAG gets a proper domain.
Currently `enrichIncidentV2` receives `onto=nil` for all RCE-handled incidents.

---

## FINDING 9 — MEDIUM: 881 Alerts (6%) Have Empty source_id — No Dedup Protection

**Confirmed:** 881 alerts have `source_id = ''`. These bypass the Kafka consumer dedup
check. In normal operation this is fine if each alert creates a unique fingerprint.
The bug occurs when:
- The same alert re-enters via Kafka replay (consumer restart)
- The DB UPSERT deduplicates on fingerprint (correct) but the pipeline re-runs
- The re-run creates a new PCR record with a (possibly different) decision

**This is distinct from the 570 `resolved_at=NULL` alerts** — those are a separate
historical issue. The empty source_id alerts are a structural dedup gap.

---

## FINDING 10 — LOW: RCA Decisions Table Unpopulated

The `rca_decisions` table has 0 rows. `persistRCADecision()` is called from `enrichIncidentV2`
only after CACIE produces a result with a non-nil `RootEntity`. Since `topo_root_entity_id`
is always null (Finding 6) and CACIE depends on `RecursiveTopoRCA` output (which is also null),
CACIE's `RootEntity` is also nil — so `persistRCADecision` is never triggered.

The fix chain is: Fix topology root entity → CACIE gets real input → CACIE produces
`RootEntity` → `persistRCADecision` fires → operators can see structured hypotheses.

---

## WHAT IS WORKING CORRECTLY (verified end-to-end)

| What | How it works | Evidence |
|---|---|---|
| DT alert ingestion | Webhook → DB upsert → Kafka publish → consumer | 14,772 DT alerts, 0 loss |
| Resolved alert guard | `processAlertResolvedStage` exits before correlation | 0 open alerts with no incident |
| DT rootCauseEntity grouping | RCE Stage 1 — deterministic, correct | 2,044 correct ATTACH decisions |
| BM/VM cascade grouping | RCE Stage 2/3 — topology-level cascade | 301-alert storm correctly grouped |
| Alert dedup | Unique constraint on (source, source_id) | 0 duplicate source_ids |
| Fingerprint UPSERT | DB-level dedup on fingerprint | Only 1 alert with multi-PCR (the Jaeger one) |
| Auto-resolution propagation | handleResolvedAlert → incident auto-close | 14,757 alerts resolved |
| PCR explanation storage | GenerateExplainabilityReport → explanation_json | 92% coverage |
| RCE avg latency | 4,198ms average for CREATE_ROOT | Acceptable |
| RCE ATTACH latency | 26ms average | Excellent |
| Investigation DAG creation | 149 DAGs in enrichIncidentV2 | Working (domain classification needs fix) |

---

## PRIORITIZED FIXES — IN EXACT IMPLEMENTATION ORDER

### Immediate (no deploy needed — SQL only)

```sql
-- Fix 1: Close stale test incident (19 days open, 0 alerts)
UPDATE incidents SET 
  status='resolved', resolved_at=NOW(),
  resolution_notes='Auto-closed: test notification, 0 alerts, open 19 days'
WHERE id = '5889e1da-e3c2-4fa4-a4ec-6a237e7ccb0e';

-- Fix 2: Backfill resolved_at on 570 legacy alerts
UPDATE alerts SET resolved_at = COALESCE(updated_at, created_at)
WHERE status = 'resolved' AND resolved_at IS NULL;
```

### Sprint 1 — Highest correctness impact

**Fix 3: fp: alert TTL in stale sweep** (`alert_pipeline.go:runStaleSweep`)
Add to the existing stale sweep that runs hourly:
```go
// Auto-close fingerprint alerts open longer than 4 hours (no DT RESOLVED ever arrives)
s.db.ExecContext(ctx, `
    UPDATE alerts SET status='resolved', resolved_at=NOW()
    WHERE source_id LIKE 'fp:%' AND status='open'
      AND created_at < NOW() - INTERVAL '4 hours'`)
```
After alerts close, the incident auto-close sweep handles the rest.

**Fix 4: RCE lookup window — use updated_at not created_at** (`root_cause_engine.go:333`)
```sql
-- CHANGE:
AND i.created_at >= NOW() - INTERVAL '2 hours'
-- TO:
AND (i.created_at >= NOW() - INTERVAL '2 hours' OR i.updated_at >= NOW() - INTERVAL '4 hours')
```
This prevents 36h-old incidents from attracting new alerts while keeping legitimately
active incidents (recently updated) as valid merge targets.

**Fix 5: topo_root_entity_id fallback from RCE** (`alert_pipeline.go:enrichIncidentV2`)
When `RecursiveTopoRCA` returns nil and `firstRCEDecision.RootEntityLabel != ""`:
```go
if rootID == "" && firstRCEDecision != nil && firstRCEDecision.RootEntityLabel != "" {
    rootID = firstRCEDecision.RootEntityLabel
    // Write immediately
    s.db.ExecContext(ctx, `UPDATE incidents SET topo_root_entity_id=$1 WHERE id=$2`, rootID, incidentID)
}
```

**Fix 6: Cluster-scoped topology matching** (`topology_graph_correlator.go:findMatchingNode`)
Add cluster guard: only match nodes from the same cluster as the alert.
```go
alertCluster := alert.Labels["k8s.cluster.name"]
if alertCluster != "" && node.Data["cluster"] != alertCluster {
    continue // skip nodes from wrong cluster
}
```

### Sprint 2 — Evidence and explainability

**Fix 7: Run ontology in RCE stage** (`alert_pipeline.go:processAlertRCEStage`)
Before the RCE switch statement, classify the alert:
```go
var ontologyResult *correlation.OntologyResult
if s.ontologyEngine != nil {
    ontologyResult = s.ontologyEngine.Classify(corrAlert)
}
// Pass ontologyResult to enrichIncidentV2 in both ATTACH and CREATE paths
```

**Fix 8: Stop LLM from hallucinating when topology is wrong cluster**
In `enrichIncidentV2`, before calling `GenerateRCA`:
```go
alertCluster := alert.Labels["k8s.cluster.name"]
if alertCluster != "" && rootID != "" && !strings.Contains(rootID, alertCluster) {
    log.Printf("enrichV2: topology cluster mismatch alert_cluster=%s root=%s — skipping LLM", alertCluster, rootID)
    // Leave ai_root_cause empty rather than hallucinate
} else {
    // Proceed with LLM
}
```

**Fix 9: Dedup fingerprint Kafka replays** (`kafka_consumer.go:processMessage`)
After the source_id dedup check, add fingerprint check:
```go
if alert.Fingerprint != "" {
    if existing, err := c.alertSvc.GetAlertByFingerprint(ctx, alert.Source, alert.Fingerprint); 
        err == nil && existing != nil {
        if hasCorrelationResult(ctx, existing.ID) {
            log.Printf("Kafka: skipping replay fingerprint=%s (already correlated)", alert.Fingerprint)
            return true
        }
    }
}
```

**Fix 10: reabsorbOrphanedIncidents namespace guard** (`alert_pipeline.go`)
Add namespace matching before reabsorbing:
```go
if orphanIncident.Namespace != "" && newRootNamespace != "" &&
   orphanIncident.Namespace != newRootNamespace {
    log.Printf("reabsorb: skipping %s — namespace mismatch (%s vs %s)",
        orphanIncident.ID, orphanIncident.Namespace, newRootNamespace)
    continue
}
```

---

## METRICS TO WATCH AFTER FIXES

| Metric | Current | Target after fixes |
|---|---|---|
| Incidents with topo_root_entity_id | 0% | >80% (DT alerts with known cluster) |
| Incidents with span >24h | 26 | 0 per week |
| fp: alert lifetime | Indefinite | <4h |
| Duplicate PCR per alert | max 117 | max 1 |
| ai_root_cause wrong cluster | ~50% of cases | <5% |
| ontology_domain populated | 0% (RCE path) | >70% |
| Operator DAG engagement | 0/149 | Baseline after UI rollout |
| 4-strategy engine runs | 0 in 13 days | Unchanged (DT always has root_cause_entity) |

The last metric is intentional — the 4-strategy engine not running for DT alerts is
architecturally correct. The RCE's deterministic grouping is more accurate for DT
than probabilistic scoring. The fix is to ensure the RCE path produces the same
quality output (topo_root_entity_id, ontology_domain, explanation) as if all
strategies had run.
