# AlertHub — Alert & Correlation Audit
# Date: 2026-06-01 | Scope: example-cluster-01, aileron namespace

---

## EXECUTIVE SUMMARY

AlertHub is ingesting and correlating alerts at production scale. The core pipeline
is healthy. There are 7 significant findings — 2 critical, 3 high, 2 medium — that
must be addressed before enterprise adoption. Several are correctness bugs, not
feature gaps.

---

## DATA AT A GLANCE

| Metric | Value |
|---|---|
| Total alerts in DB | 14,776 (all from Dynatrace) |
| Open alerts | 16 |
| Total incidents | 2,870 |
| Auto-created incidents | 2,868 (99.9%) |
| Open incidents | 12 |
| Last 24h alert volume | 66 |
| Last 24h incidents | 45 |
| Alert dedup collisions | 0 (clean — unique source_ids) |
| Orphaned open alerts | 0 (all linked or closed) |

---

## FINDING 1 — CRITICAL: 76% of Incidents Bypassed the 4-Strategy Correlation Engine

**What is happening:**
The `root_cause_engine` is listed as the dominant strategy for 2,025 incidents (76.6%).
However, when checking the pipeline results table for ALL those incidents, **6,260 out of
6,354 (98.5%) have topology_score=0, semantic_score=0, temporal_score=0, rules_score=0**.

The explanation_json for these incidents contains only:
```json
"reasoning": ["Dominant correlation signal: root_cause_engine (final weighted score: 1.000)"]
```

**Root cause:**
The `processAlertRCEStage` runs BEFORE the 4-strategy parallel correlation. When RCE
fires with `RCAActionAttachToRoot` or `RCAActionCreateRoot`, `alert_pipeline.go` sets
`handled=true` and skips the aggregator entirely. The Dynatrace root-cause entity signal
is so strong it short-circuits everything else. The 4-strategy engine never runs.

**Impact:**
- Operators see `confidence: 1.000` for every RCE-handled alert — regardless of whether
  the grouping is actually correct. This is a false confidence signal.
- No topology evidence, no semantic evidence, no explainability — just "root_cause_engine said so."
- The explanation pipeline has nothing to explain.

**Fix required:**
When RCE handles an alert, still run the 4 strategies asynchronously and store their scores
in `pipeline_correlation_results` for audit and explainability. The RCE decision stands —
but the evidence chain must be populated.

In `alert_pipeline.go`, the `processAlertRCEStage` path that sets `handled=true` must:
1. Still call `saveCorrelationResult` with actual strategy scores
2. Still call `GenerateExplainabilityReport` with the RCE decision as context
3. Mark the explanation with `triggered_by: "root_cause_engine"` so operators know why

---

## FINDING 2 — CRITICAL: topo_root_entity_id is NULL for 100% of Incidents

**What is happening:**
Zero incidents have `topo_root_entity_id` populated. The column exists, `enrichIncidentV2`
writes it from `RecursiveTopoRCA.Traverse()`, but the result is always empty string.

**Root cause (verified):**
`RecursiveTopoRCA` traverses the Neo4j topology graph looking for the root entity. The
Dynatrace alerts include `entity_id` labels like `P-BM1-S1-1779130472` (Dynatrace problem
IDs) — not Neo4j node IDs. The topology graph nodes are keyed by K8s resource names
(`example-cluster-worker-z3-08`, `kubesense-agent-*`) from the KubeSense agent.

There is a **namespace mismatch**: Dynatrace `entity_id` → Neo4j has no matching node →
`RecursiveTopoRCA` returns `RootEntity: nil` → `topo_root_entity_id` stays empty.

AlertHub's topology graph contains CloudStack/KVM/K8s nodes but the `findMatchingNode`
function in `topology_graph_correlator.go` matches on alert labels
(`k8s.workload.name`, `host.name`). These labels ARE on the alerts from Dynatrace, but
only when Dynatrace sends the K8s-specific fields. Many DT alerts only have `entity_id`
(a DT-internal ID, not a K8s resource name).

**Impact:**
- No blast radius computation for 100% of incidents
- No causal chain populated  
- `blast_radius_details` is empty everywhere (KubeSense writes topology-accurate blast radius
  but the enrichV2 path still finds no root entity to traverse from)

**Fix required:**
In `topology_graph_correlator.go → findMatchingNode`: add a fallback that extracts the
node name from the Dynatrace `entity.name` metadata label, which DT includes in all alerts
as a human-readable resource name. Match on that against Neo4j node `name` field.
Additionally: when `topo_root_entity_id` stays empty after traversal, log a specific
`"topology_miss: no matching node for entity_id=%s labels=%v"` so the gap is auditable.

---

## FINDING 3 — HIGH: 674 Incidents Have No Correlation Score (674/2870 = 23%)

**What is happening:**
674 incidents have `correlation_confidence = 0` and `dominant_strategy = NULL`. These are
incidents where no pipeline correlation result was stored — likely created via a code path
that bypasses `saveCorrelationResult`.

**Checked:**
- These are NOT manual incidents (all 2,868 auto-created incidents should have scores)
- They are resolved, suggesting they were valid alerts that went through the pipeline

**Root cause (likely):**
Looking at `alert_pipeline.go`: the `processAlertRCEStage` early-exit path calls
`mergeAlertIntoIncident` directly and returns without going through the aggregator code
that calls `saveCorrelationResult`. For the `RCAActionAttachToRoot` case, the correlation
result is never saved at all — not even with score=1.0.

**Fix:**
Same as Finding 1 — ensure `saveCorrelationResult` is called on ALL alert processing
paths, including the RCE early-exit path.

---

## FINDING 4 — HIGH: RCA AI Narratives Are Hallucinating — Zero Evidence Grounding

**What is happening:**
Every open incident has `ai_root_cause` populated, but the narratives are factually wrong
or contradict the alert context:

| Incident | Alert Title | ai_root_cause claim |
|---|---|---|
| ad3f55d6 | CPU-request saturation on node | "author pod in kubesense-agent not ready due to non-ready container" |
| 175f948b | Out-of-memory kills | "alloy deployment running on single pod... system is stable" |
| e471160d | Host or monitoring unavailable | "root cause: unknown in domain DATABASE with 0% confidence" |
| 2f02af54 | [P2] Not all pods ready | "internal issue that may require immediate investigation" |

The first example is completely wrong — the incident is about CPU saturation on a node,
but the LLM produced an RCA about the kubesense-agent pod (which happens to be running
on the dev cluster at the time). This is a **hallucination from lack of evidence grounding**.

**Root cause:**
`enrichIncidentV2` calls `llmEnricher.GenerateRCA` with:
- Alert title ✅
- Alert severity ✅  
- Matched node label (often empty) ❌
- Root cause label (often empty) ❌
- Topology path (often empty because topo_root_entity_id is null) ❌
- Strategy scores ✅

When the topology fields are empty, the LLM has almost no relevant context and produces
generic or completely fabricated narratives pulled from whatever K8s resources it saw
mentioned in the prompt or its training data.

**Fix:**
The evidence grounding code (`EvidenceCollector`, `GenerateRCAFromEvidence`) was designed
and is in `llm_enricher.go` but the pipeline's `enrichIncidentV2` still calls the old
`GenerateRCA` with flat strings. Connect the evidence pipeline: when `ai_root_cause`
is set from CACIE (which we fixed), use that as the ground-truth context for the LLM
narrative. Never let the LLM produce a narrative without at least the CACIE hypothesis.

---

## FINDING 5 — HIGH: Temporal Over-Correlation for "Aggregate State" Alerts

**What is happening:**
8+ distinct incidents all titled "Incident: Aggregate state - MDN" were created on 2026-05-14
with temporal=1.0, semantic=0.998, topology=0.0. Each has exactly 5 alerts.

The same alert type fired repeatedly (MDN aggregate state = a synthetic/heartbeat check).
Instead of one incident being created and subsequent alerts merged, 8+ separate incidents
were created. This is 8× the correct number.

**Root cause:**
The burst detection (50 alerts/60 seconds) threshold was not triggered. These alerts
arrived in a pattern that spaced them just enough to avoid burst mode. Each new alert
matched temporally to a recently-closed alert from a just-resolved incident, not to the
current open incident — likely because the lookback window for `getRecentAlerts` returned
stale resolved alerts as candidates.

`getRecentAlerts` at line 688 in `parallel_correlation_engine.go` queries:
```sql
WHERE created_at >= $1 AND id != $2 AND status != 'resolved'
```
This is correct — it excludes resolved alerts. The issue is the aggregator's
`FindCandidateIncidents` may have a timing window where the previous incident was just
resolved before the next alert arrived, leaving no open candidate to merge into.

**Fix:**
Add a lookback window for recently-resolved incidents (within 15 minutes) as merge
candidates. If an alert title is identical to alerts in a recently-resolved incident,
prefer reopening/linking over creating a new incident. This is a common AIOps pattern
for recurring synthetic checks.

---

## FINDING 6 — MEDIUM: 570 Alerts Have resolved_at=NULL Despite Being Resolved

**What is happening:**
570 alerts have `status='resolved'` but `resolved_at IS NULL`. These were created as
resolved (no transition from open → resolved, so no timestamp was set).

**Root cause:**
Historical Kafka consumer bug — confirmed in prior audit (last occurrence 2026-05-01).
The guard was added to `kafka_consumer.go` but the 570 legacy records remain.

**Impact:**
- MTTD calculation errors: `resolved_at - created_at` returns NULL for these records
- Any query computing resolution time excludes them, skewing averages
- Forecasting models using these records will produce incorrect baselines

**Fix:**
One-time cleanup (safe, bounded):
```sql
UPDATE alerts 
SET resolved_at = updated_at
WHERE status = 'resolved' 
  AND resolved_at IS NULL
  AND updated_at IS NOT NULL;
-- If updated_at also null, use created_at as fallback:
UPDATE alerts 
SET resolved_at = created_at
WHERE status = 'resolved' 
  AND resolved_at IS NULL;
```
570 rows. No incident impact — these are already resolved.

---

## FINDING 7 — MEDIUM: Stale Test Incident Open for 17 Days

**What is happening:**
Incident `5889e1da` ("Dynatrace problem notification test run") has been open since
2026-05-13 — 17+ days — with 0 alerts attached and no `ai_root_cause`. It was created
from a Dynatrace test notification and was never closed.

**Fix:**
```sql
UPDATE incidents 
SET status = 'resolved', resolved_at = NOW(), resolution_notes = 'Auto-closed: test incident with no alerts, open >14 days'
WHERE id = '5889e1da-e3c2-4fa4-a4ec-6a237e7ccb0e';
```

---

## WHAT IS WORKING CORRECTLY

| Area | Status | Evidence |
|---|---|---|
| Alert dedup by source_id | ✅ Perfect | 0 duplicate source_ids |
| Dynatrace resolved alert guard | ✅ Working | 0 orphaned open alerts |
| Alert→Incident linkage | ✅ 48.3% of alerts linked | Correct for Dynatrace (many DT alerts auto-resolve fast) |
| Investigation DAG generation | ✅ 149 DAGs stored | Domain classification working for storage/kubernetes/network |
| ExplainabilityReport storage | ✅ 92% coverage | 7,354 of 7,997 PCR rows have explanation_json |
| Feedback recording | ✅ Working | 3 feedback records (confirmed×2, missed_correlation×1) |
| MTTD (median) | ✅ 0.1 minutes | Alert → incident creation is essentially instant |
| 301-alert mega-incident | ✅ Legitimate | 107 distinct alert types over 54 minutes — genuine BM node failure cascade |
| Alert storm (95th pctile MTTD) | ⚠️ 73.7 minutes | High tail caused by backlogged alerts during storm events |

---

## PRIORITIZED ACTION PLAN

### Do now (data integrity):
1. **Close stale test incident** — 1 SQL statement, 30 seconds
2. **Fix 570 resolved_at=NULL** — 1 SQL statement, safe

### Do this sprint (correctness):
3. **Save correlation result on RCE path** — 20-line change in `alert_pipeline.go`
4. **Wire evidence grounding to LLM** — connect `EvidenceCollector` to `enrichIncidentV2`

### Do next sprint (accuracy):
5. **Fix topology node matching for Dynatrace alerts** — update `findMatchingNode` in `topology_graph_correlator.go`
6. **Add recently-resolved incident as merge candidate** — `correlation_aggregator.go`

### Ongoing:
7. **Get operator feedback rate above 10%** — currently 3 records total; feedback buttons are deployed, usage needs to be encouraged
