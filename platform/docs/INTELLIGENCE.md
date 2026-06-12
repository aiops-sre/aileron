# AlertHub Intelligence Layer

Complete reference for the AIOps intelligence stack: evidence-gathering pipeline, policy engine, runbook skill catalog, postmortem generation, gate hooks, MCP server, and KubeSense integration.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [KubeSense Integration](#kubesense-integration)
3. [OIE Evidence Gathering Pipeline](#oie-evidence-gathering-pipeline)
4. [Policy Engine](#policy-engine)
5. [Runbook Skill Catalog](#runbook-skill-catalog)
6. [Postmortem Auto-Generation](#postmortem-auto-generation)
7. [Gate Hooks and Remediations](#gate-hooks-and-remediations)
8. [MCP Server](#mcp-server)
9. [Chaos Readiness Scores](#chaos-readiness-scores)
10. [Per-Role Model Routing](#per-role-model-routing)

---

## Architecture Overview

The intelligence layer sits above the raw alert pipeline as a four-layer stack:

```
┌─────────────────────────────────────────────────────────────────┐
│ Layer 4 — Output                                                │
│   Postmortem  │  Remediations (gate)  │  Slack notifications   │
│   MCP tools   │  RCA narrative        │  Citation chips (UI)   │
├─────────────────────────────────────────────────────────────────┤
│ Layer 3 — Orchestration                                         │
│   OIE (Go service)  — hypothesis engine, DAG executor           │
│   CACIE             — causal inference, confidence scoring      │
│   OllamaModel       — LLM narrator, postmortem writer           │
├─────────────────────────────────────────────────────────────────┤
│ Layer 2 — Reasoning                                             │
│   16 evidence fetchers (K8s, CloudStack, NetApp, KubeSense …)  │
│   Hypothesis template library (10+ failure templates)          │
│   PolicyEngine       — suppression, skip_rca, approval gates   │
├─────────────────────────────────────────────────────────────────┤
│ Layer 1 — Signal                                                │
│   KubeSense Kafka consumer  (18 topics → 5 DB tables)          │
│   Alert pipeline            (Kafka alerthub.incidents)          │
│   Dynatrace / Prometheus / Splunk / CloudStack webhooks         │
└─────────────────────────────────────────────────────────────────┘
```

**Key services and their roles:**

| Service | Language | Port | Role |
|---|---|---|---|
| `alerthub-backend` | Go | 8080 | Alert pipeline, CACIE, policy engine, gate hooks, MCP server |
| `oie` | Go | 8081 | Operational Intelligence Engine — evidence DAG, hypothesis scoring, LLM narrator |
| `ollama` | — | 11434 | Local LLM (qwen2.5:3b) for RCA, narrative, postmortem |
| `kubesense-core` | Go | 8080 (`aileron-agent` ns) | KubeSense API server, proxied at `/api/v1/kubesense/*` |

---

## KubeSense Integration

KubeSense is a separate Kubernetes intelligence platform that runs an agent inside each monitored cluster. AlertHub acts as its downstream consumer, turning KubeSense signals into OIE evidence.

### Data Flow

```
KubeSense agent (per cluster)
    │  Watches: pods, nodes, deployments, PVCs, HPA, RBAC, secrets, APM
    │
    ▼
Kafka (Strimzi, aileron)
    │  18 topics: kubesense.events.health, kubesense.forecasts,
    │             kubesense.config.violations, kubesense.apm.*,
    │             kubesense.chaos.scores, kubesense.drift.detected, …
    ▼
AlertHub KubeSense Consumer
(internal/services/kubesense/consumer.go, group=alerthub-kubesense-consumer)
    │
    ├─► kubesense_health_events      (health, workload, config, storage, network events;
    │                                 APM regressions stored as event_type='apm.regression.*';
    │                                 chaos scores stored as event_type='chaos.score';
    │                                 GitOps drift stored as event_type='drift.*')
    ├─► kubesense_config_violations  (OPA/policy violations with remediation hints)
    ├─► kubesense_forecasts          (PVC/CPU/memory exhaustion predictions)
    ├─► kubesense_apm_signals        (golden-signal snapshots: rate/errors/latency/saturation)
    └─► kubesense_investigation_results (KubeSense's own RCA result merged into incidents)
    │
    ▼
OIE KubeSenseFetcher
(services/oie/internal/infrastructure/fetchers/kubesense/fetcher.go)
    │  Queries the 5 DB tables as evidence during active investigations
    ▼
Hypothesis Scorer → LLM Narrator → Incident RCA update
```

### Kafka Topics Consumed

AlertHub subscribes to 16 of the 18 KubeSense topics (topology not consumed directly):

| Topic | DB target | Description |
|---|---|---|
| `kubesense.events.health` | `kubesense_health_events` | Pod/node health state changes |
| `kubesense.events.workloads` | `kubesense_health_events` | Deployment, StatefulSet changes |
| `kubesense.events.config` | `kubesense_health_events` | ConfigMap/Secret/RBAC changes |
| `kubesense.events.storage` | `kubesense_health_events` | PVC bound/evicted events |
| `kubesense.events.network` | `kubesense_health_events` | Network policy/endpoint changes |
| `kubesense.investigations.results` | `kubesense_investigation_results` | KubeSense RCA completion |
| `kubesense.forecasts` | `kubesense_forecasts` | Resource exhaustion predictions |
| `kubesense.config.violations` | `kubesense_config_violations` | Policy/best-practice violations |
| `kubesense.apm.golden-signals` | `kubesense_apm_signals` | Rate/errors/latency/saturation |
| `kubesense.apm.regressions` | `kubesense_health_events` (apm.regression.*) | EWMA anomaly detection |
| `kubesense.chaos.scores` | `kubesense_health_events` (chaos.score) | Cluster chaos readiness score |
| `kubesense.drift.detected` | `kubesense_health_events` (drift.*) | GitOps drift detection |
| `kubesense.postmortems` | logged only | KubeSense postmortem cross-reference |
| `kubesense.toil.events` | `kubesense_health_events` (sre.toil) | Toil measurement |
| `kubesense.anomalies` | `kubesense_health_events` (sre.anomaly) | Anomaly signals |
| `kubesense.noisebudget.suppressions` | `kubesense_health_events` (sre.noise) | Noise budget events |

### DB Tables

```sql
-- Health events: pod/node conditions, APM regressions, chaos scores, drift
kubesense_health_events (
    id, cluster_id, event_type, severity,
    resource_kind, namespace, resource_name, resource_uid,
    payload jsonb, occurred_at, received_at
)

-- OPA/best-practice violations with remediation hints
kubesense_config_violations (
    id, cluster_id, rule_id, severity,
    resource_kind, namespace, resource_name,
    message, remediation, occurred_at
)

-- Resource exhaustion forecasts
kubesense_forecasts (
    id, cluster_id, target, resource_kind, namespace, resource_name,
    current_value, threshold, predicted_breach, trend_per_day,
    model_confidence, payload, created_at
)

-- Four golden signals per service
kubesense_apm_signals (
    id, cluster_id, namespace, service_name,
    request_rate, error_rate, latency_p99_ms, saturation, sampled_at
)

-- KubeSense's own RCA results (merged into AlertHub incidents)
kubesense_investigation_results (
    id, incident_id, cluster_id, grade, confidence, root_cause,
    summary, evidence_count, payload, completed_at
)
```

### Investigation Request Flow

When a new high/critical incident is created, AlertHub optionally publishes an `InvestigationRequest` to `kubesense.investigations.requests`. KubeSense picks it up, runs its evidence-first investigation pipeline, and publishes the result to `kubesense.investigations.results`. The AlertHub consumer then calls `IncidentUpdater.UpdateFromKubeSense()` to merge the grade and root cause text back into the incident.

```go
// Grade mapping (types.go GradeToConfidence)
// A → 0.92, B → 0.78, C → 0.60, D → 0.40, F → 0.20
```

---

## OIE Evidence Gathering Pipeline

The OIE (Operational Intelligence Engine) is a standalone Go service that consumes the `alerthub.incidents` Kafka topic and runs a DAG of 16 evidence fetchers for each new incident.

### High-Level Flow

```
Kafka alerthub.incidents
    │
    ▼
Investigation Manager (manager.go)
    │  Creates investigation record (status=pending)
    │  Resolves entity via EIRS (EntityContextFetcher)
    ▼
Evidence DAG Executor (dag.go)
    │  Runs all 16 fetchers respecting DependsOn() order
    │  Circuit breaker per external source (breaker.go)
    ▼
Hypothesis Engine (engine.go)
    │  Scores 10+ templates against evidence
    │  Each template: supporting / contradicting / weight rules
    ▼
Hypothesis Scorer (scorer.go)
    │  Bayesian-style: prior × likelihood × (1 - contradictions)
    │  Top-ranked hypothesis becomes the winning root cause
    ▼
OIE Narrator / LLM (narrator.go + ollama/client.go)
    │  Uses LLM_NARRATIVE_MODEL to write human-readable RCA
    ▼
AlertHub writeback (OIE_ALERTHUB_BASE_URL)
    │  POST /api/v1/incidents/:id/rca-callback
    │  Updates incident.rca_status, rca_confidence, ai_root_cause
    ▼
WebSocket broadcast → UI rca_complete event
```

### Evidence Fetcher Catalog

The DAG executes all 16 fetchers in parallel (respecting declared dependencies). Each fetcher returns `[]Evidence` with `Role` (Supports/Contradicts/Context), `Weight`, and `EvidenceConfidence`.

| Fetcher ID | Source | What it fetches | DependsOn |
|---|---|---|---|
| `eirs_entity_context` | EIRS API | Canonical entity profile (cluster, namespace, K8s node, CS VM ID) | — |
| `k8s_node_conditions` | Kubernetes API | Node conditions (Ready/NotReady/Pressure), K8s events ±15 min of incident, CPU request saturation | — |
| `k8s_pod_exit_code` | Kubernetes API | Container termination state, exit codes (OOM=137, segfault=139), restart pattern | — |
| `k8s_pod_events` | Kubernetes API | Pod events ±30 min: OOMKilling, BackOff, FailedMount, ImagePullBackOff, Evicted, PDB blocked | — |
| `k8s_pvc_capacity` | Kubernetes API | PVC phase (Lost/Bound), VolumeResizeFailed events, namespace-wide storage warnings | — |
| `k8s_pdb` | Kubernetes API | PodDisruptionBudgets with DisruptionsAllowed=0 (deployment update blocking) | — |
| `k8s_service_endpoints` | Kubernetes API | Service NotReadyAddresses (pods behind service not ready) | — |
| `cloudstack_vm_state` | CloudStack API | VM state (Running/Stopped/Error), host ID output for downstream | — |
| `cloudstack_host_state` | CloudStack API | Hypervisor host state (Up/Down/Alert), ResourceState | `cloudstack_vm_state` |
| `netapp_volume_state` | NetApp ONTAP REST | Volume state (online/offline/restricted) and utilization ≥85% | — |
| `netapp_aggregate_state` | NetApp ONTAP REST | Aggregate state (online/offline/restricted/degraded) | — |
| `netapp_svm_state` | NetApp ONTAP REST | SVM state (running/stopped/starting/stopping) | — |
| `kubesense_signals` | AlertHub DB | Health events (2h), config violations (24h), forecasts (±6h), APM regressions (±30 min), chaos scores (2h), drift events (6h) | — |
| `okg_changes` | OKG API | Deployment/config changes in the 2h before incident with causality score ≥0.40 | — |
| `okg_similar_incidents` | OKG API | Historical incidents with ≥65% similarity | — |
| `past_investigations` | AlertHub DB | Completed RCA investigations from the last 30 days matching domain/entity_type/namespace | — |
| `investigation_runbooks` | AlertHub DB | Matching runbook skills (domain + entity_type + failure_class) | — |

> **Note:** The EIRS entity context fetcher runs first and populates the `EntityProfile` shared by K8s and CloudStack fetchers so they know which cluster/node/VM to query.

### Temporal Confidence Dampening

All fetchers that read _current_ state (not historical events) apply temporal dampening: if the fetcher runs more than 60 seconds after incident start, confidence is reduced linearly to 50% of base confidence at 180s (K8s) or 300s (NetApp). Historical events (timestamped) are not dampened.

### OIE Environment Variables

| Variable | Default | Description |
|---|---|---|
| `OIE_DATABASE_URL` | required | PostgreSQL DSN |
| `OIE_KAFKA_BROKERS` | `localhost:9092` | Kafka broker list |
| `OIE_KAFKA_CONSUMER_GROUP` | `oie-investigation-consumer` | Consumer group ID |
| `OIE_ALERTHUB_BASE_URL` | — | AlertHub base URL for RCA writeback. If unset, OIE results won't update the incident. Must be `http://alerthub-backend.aileron.svc.cluster.local:8080` in-cluster. |
| `OIE_OLLAMA_BASE_URL` | `http://ollama.aileron.svc.cluster.local:11434` | Ollama endpoint |
| `OIE_OLLAMA_MODEL_TRIAGE` | falls back to `OIE_OLLAMA_MODEL` | Fast model for evidence scoring |
| `OIE_OLLAMA_MODEL_RCA` | falls back to `OIE_OLLAMA_MODEL` | Quality model for RCA synthesis |
| `OIE_OLLAMA_MODEL_NARRATIVE` | falls back to `OIE_OLLAMA_MODEL` | Model for narrative generation |
| `OIE_EIRS_BASE_URL` | — | EIRS entity resolution service URL |
| `OIE_OKG_BASE_URL` | — | OKG change-correlation service URL |
| `OIE_MAX_CONCURRENT_INVESTIGATIONS` | `20` | Max parallel investigations |
| `OIE_INVESTIGATION_TIME_BUDGET_MS` | `45000` | Per-investigation time budget (45s) |
| `OIE_AUTO_INVESTIGATE_SEVERITIES` | `critical,high` | Which severities trigger auto-investigation |
| `NETAPP_CLUSTERS` | — | JSON array: `[{"cluster":"<ip>","name":"<name>","region":"mdn"}]` |
| `NETAPP_USER` | `harvest-user` | NetApp ONTAP username |
| `NETAPP_PASSWORD` | — | NetApp ONTAP password |
| `OIE_KUBECONFIGS_DIR` | `/etc/kubeconfigs` | Directory with per-cluster kubeconfig files |

---

## Policy Engine

The `PolicyEngine` (`internal/services/policy/engine.go`) replaces all hardcoded pipeline guards with DB-driven rules loaded from the `intelligence_policies` table. The cache refreshes every 5 minutes.

### Policy Types

| Type | Effect |
|---|---|
| `suppress_alert` | Drop the alert before it enters the pipeline. Used for known noisy/test sources. |
| `suppress_incident` | Allow the alert but prevent incident creation for matching signals. |
| `skip_rca` | Allow the incident to be created but skip OIE/CACIE RCA investigation. |
| `require_approval` | Flag the incident for gate-hook approval before any action proceeds. |
| `auto_resolve` | Immediately resolve matching incidents without investigation. |

### Condition Format

Conditions are stored as a JSON object in the `condition_json` column. All fields are optional and combined with AND logic:

```json
{
  "source": "prometheus",
  "severity": "low",
  "title_contains": "liveness-fail",
  "namespace_prefix": "tmp-",
  "entity_type": "pod",
  "label_key": "environment",
  "label_value": "dev",
  "cluster_ref": "example-cluster"
}
```

### Creating a Policy

```sql
INSERT INTO intelligence_policies
    (id, name, policy_type, condition_json, action, enabled, priority)
VALUES
    (uuid_generate_v4(),
     'Suppress dev-namespace low alerts',
     'suppress_alert',
     '{"severity": "low", "label_key": "environment", "label_value": "dev"}',
     'suppress',
     true,
     80);
```

- **priority**: Higher value = evaluated first (max 500 policies loaded at a time).
- **enabled**: Set to `false` to disable without deleting.
- Policies are evaluated in descending priority order. The first matching policy wins.
- After a DB change, call `GET /api/v1/intelligence/policies/reload` or wait up to 5 minutes for the cache to expire.

### Built-in Fallback Rules

When no DB policy matches, these built-in patterns from `isKnownTestWorkload()` are checked:
- Title contains: `liveness-fail`, `tmp-debug`, `rca-claude-cli`, `test-pod`, `load-test`
- Label `environment` is `test`, `dev`, or `local`

DB policies always take precedence over built-in rules.

---

## Runbook Skill Catalog

The runbook skill catalog stores team-authored runbooks in the `investigation_runbooks` table. The `RunbookFetcher` injects matching runbooks as evidence into every OIE investigation so the LLM narrator can reference team-specific remediation procedures.

### Schema

```sql
CREATE TABLE investigation_runbooks (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name         TEXT NOT NULL,
    domain       TEXT,           -- 'k8s', 'netapp', 'cloudstack', '' (wildcard)
    entity_type  TEXT,           -- 'pod', 'node', 'pvc', 'service', '' (wildcard)
    failure_class TEXT,          -- 'oom', 'crash_loop', 'storage_full', '' (wildcard)
    content      TEXT NOT NULL,  -- full runbook text (Markdown)
    source       TEXT,           -- 'confluence', 'github', 'manual'
    enabled      BOOLEAN DEFAULT true,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);
```

### Adding a Runbook

```sql
INSERT INTO investigation_runbooks (name, domain, entity_type, failure_class, content, source)
VALUES (
    'K8s OOM Kill Remediation',
    'k8s',
    'pod',
    'oom',
    '## K8s OOM Kill Runbook
    
1. Check pod memory limits: `kubectl describe pod <name> -n <ns>`
2. Review VPA recommendations: `kubectl get vpa -n <ns>`
3. Check node memory pressure: `kubectl describe node <node>`
4. If single pod: increase memory limit in Deployment and roll out.
5. If cluster-wide: check for memory leak or traffic spike via Dynatrace.',
    'manual'
);
```

**Matching logic** (priority order):
1. Exact match: `domain + entity_type + failure_class` all match.
2. Two-field match: `domain + entity_type` or `entity_type + failure_class`.
3. One-field match: `domain` only.
4. Wildcard: all fields empty — matches any investigation.

The fetcher returns up to 3 matching runbooks per investigation. Content is truncated at 600 characters for the LLM prompt (full content accessible via the API/UI).

### Accessing Runbooks via MCP

```
list_runbooks { "domain": "netapp", "entity_type": "pvc" }
```

---

## Postmortem Auto-Generation

When an incident is resolved, the `PostmortemService` (`internal/services/postmortems/postmortem_service.go`) automatically generates a structured postmortem and stores it in the `post_mortems` table.

### Trigger

Postmortems are generated when:
- An incident status transitions to `resolved` (auto-triggered in `incidents.go`)
- Manually via `POST /api/v1/incidents/:id/postmortem/generate`

### Generation Logic

```
Incident data (title, severity, rca_status, rca_confidence, topology_path)
    │
    ├─► Build timeline from incident_timeline_events
    ├─► Fetch contributing factors from rca_decisions (confidence > 0.3)
    │
    ▼
rca_confidence >= 0.60 AND Ollama reachable AND rca text available?
    │
    ├─► YES → LLM generation (qwen2.5:3b, LLM_RCA_MODEL, temperature=0.2)
    │         Sections: IMPACT / REMEDIATION / LESSONS (3 bullets) / ACTIONS (2 items)
    │         GeneratedBy = "llm"
    │
    └─► NO  → Deterministic template fallback
              GeneratedBy = "template"
```

### Structure

```json
{
  "title": "Postmortem: <incident title>",
  "severity": "critical",
  "duration": "2h15m",
  "impact_summary": "...",
  "root_cause": "<rca text>",
  "rca_confidence": 0.87,
  "contributing_factors": ["k8s (oie)", "netapp (oie)"],
  "timeline": [
    {"timestamp": "2026-06-08T10:00:00Z", "description": "Incident created", "type": "alert"},
    {"timestamp": "2026-06-08T12:15:00Z", "description": "Incident resolved", "type": "resolution"}
  ],
  "remediation": "...",
  "lessons_learned": ["...", "...", "..."],
  "action_items": [
    {"description": "...", "priority": "high", "type": "prevent"}
  ],
  "generated_by": "llm",
  "generated_at": "2026-06-08T12:16:00Z"
}
```

### Accessing via UI

1. Open an incident in the Incidents page.
2. Click the **Postmortem** tab (appears when the incident is resolved).
3. The full structured document is displayed with timeline, lessons, and action items.

### Accessing via MCP

```
get_postmortem { "incident_id": "<uuid>" }
```

---

## Gate Hooks and Remediations

The `RemediationHandler` (`internal/api/handlers/remediation_handler.go`) implements a safety gate for automated actions. No automated remediation executes without explicit oncall approval.

### Flow

```
1. PROPOSE  → OIE, postmortem service, MCP agent, or operator
              POST /api/v1/incidents/:id/remediations
              { "proposed_action": "...", "action_type": "restart_pod",
                "risk_level": "medium", "proposed_by": "oie" }
              Status: proposed

2. NOTIFY   → Slack webhook (INTELLIGENCE_SLACK_WEBHOOK) fires immediately
              Message includes action description, risk level, remediation ID,
              and a link to the incident's Actions tab

3. APPROVE  → Oncall engineer reviews in UI (Incidents → incident → Remediations tab)
              POST /api/v1/incidents/:id/remediations/:rid/approve
              Status: approved

   REJECT   → POST /api/v1/incidents/:id/remediations/:rid/reject
              { "reason": "not needed, alert was a false positive" }
              Status: rejected

4. EXECUTE  → Operator manually executes the approved action or an external
              system polls for status=approved and runs it
              Status: executing → executed | failed
```

### Action Types

| `action_type` | Description |
|---|---|
| `restart_pod` | Restart a specific pod |
| `scale_up` | Scale a deployment or HPA |
| `config_change` | Modify a ConfigMap or Secret |
| `manual` | Free-form remediation requiring human execution |

### Risk Levels

| Level | Slack indicator | Typical use |
|---|---|---|
| `low` | green circle | Log rotation, cache flush |
| `medium` | yellow circle | Pod restart, scale up |
| `high` | red circle | Config changes, infra modifications |

### DB Schema

```sql
CREATE TABLE remediations_pending (
    id               UUID PRIMARY KEY,
    incident_id      UUID NOT NULL REFERENCES incidents(id),
    proposed_action  TEXT NOT NULL,
    action_type      TEXT NOT NULL,
    risk_level       TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'proposed',
      -- proposed | approved | rejected | executing | executed | failed
    proposed_by      TEXT NOT NULL,  -- 'oie' | 'postmortem' | 'operator' | 'mcp-agent'
    approved_by      TEXT,
    rejection_reason TEXT,
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    updated_at       TIMESTAMPTZ DEFAULT NOW()
);
```

---

## MCP Server

AlertHub exposes a Model Context Protocol (MCP) server at `/api/v1/mcp` so Claude Desktop, Cursor, or Windsurf can query incidents, investigations, and runbooks directly from the AI assistant.

### Protocol

- **Transport**: JSON-RPC 2.0 over HTTP (`POST /api/v1/mcp`)
- **Discovery**: `GET /api/v1/mcp` returns the tool manifest
- **Protocol version**: `2024-11-05`

### Available Tools (7)

| Tool | Description | Key parameters |
|---|---|---|
| `list_incidents` | List AlertHub incidents with filters | `status`, `severity`, `limit` (max 50) |
| `get_incident` | Full incident details + RCA status | `id` (UUID or INC-NNN) |
| `get_rca_decisions` | CACIE/OIE decisions, confidence, evidence count | `incident_id` |
| `search_incidents` | Full-text search across titles and RCA text | `query`, `limit`, `status` |
| `get_postmortem` | Structured postmortem for a resolved incident | `incident_id` |
| `list_runbooks` | Runbook skill catalog with domain/entity filters | `domain`, `entity_type` |
| `propose_remediation` | Submit a gate-hook remediation proposal | `incident_id`, `proposed_action`, `risk_level`, `action_type` |

### Connecting Claude Desktop

Add this to your Claude Desktop `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "alerthub": {
      "url": "https://aileron.example.com/api/v1/mcp",
      "transport": "http",
      "headers": {
        "Authorization": "Bearer <your-jwt-token>"
      }
    }
  }
}
```

Replace `<your-jwt-token>` with a token obtained from `/api/v1/auth/oidc/exchange`.

### Example MCP Session

```
User: What critical incidents are open right now?

[Claude calls list_incidents {"status": "open", "severity": "critical"}]

AlertHub: • INC-042 (3f8a…) — Kubernetes PVC: Low disk space % [critical/open] rca=completed(87%)
           RCA: NetApp aggregate aggr1_node001 on mdn-cluster001 is offline — all PVCs
           Created: 2026-06-08T09:15:00Z

User: Get the full details and propose a remediation

[Claude calls get_incident {"id": "INC-042"} then propose_remediation {...}]
```

---

## Chaos Readiness Scores

The chaos readiness score is computed by KubeSense across every workload in a cluster and published to `kubesense.chaos.scores`. AlertHub persists it as a `kubesense_health_events` row with `event_type='chaos.score'`.

### Grade Mapping

| Grade | Score (0–100) | Confidence | Meaning |
|---|---|---|---|
| A | 80–100 | 0.92 | Cluster is resilient: PDBs set, multi-replica, resource limits present |
| B | 60–79 | 0.78 | Minor gaps (some workloads without PDBs or limits) |
| C | 40–59 | 0.60 | Significant gaps — notable SPOF risk |
| D | 20–39 | 0.40 | Severe gaps — single replicas, no resource limits, no PDBs |
| F | 0–19 | 0.20 | Critical — workloads will not survive node failure |

**Example: Grade F / score 33** means the cluster scored 33/100. Severity is mapped to `critical` (score < 40). The OIE `KubeSenseFetcher` will surface this as `TypeKubeSenseChaosScore` evidence with role=Supports and weight=0.70 for any incident in that cluster, indicating that missing PDB/single-replica configurations likely contributed to the blast radius.

### Checks Run by KubeSense

- Deployment replicas ≥ 2
- PodDisruptionBudget exists and `minAvailable` ≥ 1
- Resource requests and limits set on all containers
- HorizontalPodAutoscaler configured
- Liveness and readiness probes defined
- Anti-affinity rules to prevent co-location on the same node

### Viewing in UI

Open **KubeSense** in the top navigation, then select the **Chaos Readiness** tab. Each cluster shows its current grade, score, and a breakdown of failing checks per workload.

---

## Per-Role Model Routing

AlertHub routes each LLM task to the most appropriate model, enabling a trade-off between speed (triage) and quality (RCA/narrative).

### Backend (alerthub-backend)

Configure via environment variables on the `alerthub-backend` deployment:

| Env Var | Default | Used for |
|---|---|---|
| `LLM_MODEL` | `qwen2.5:3b` | Default model for all tasks |
| `LLM_TRIAGE_MODEL` | falls back to `LLM_MODEL` | Fast alert triage classification (inline enrichment on incident creation) |
| `LLM_RCA_MODEL` | falls back to `LLM_MODEL` | Root cause analysis synthesis, postmortem generation |
| `LLM_NARRATIVE_MODEL` | falls back to `LLM_MODEL` | Human-readable 2–4 sentence RCA narrative |
| `LLM_SERVICE_URL` | `http://ollama.<ns>.svc.cluster.local:11434` | Ollama endpoint |

### OIE Service

Configure via environment variables on the `oie` deployment:

| Env Var | Default | Used for |
|---|---|---|
| `OIE_OLLAMA_MODEL` | `qwen2.5:3b` | Default |
| `OIE_OLLAMA_MODEL_TRIAGE` | falls back to `OIE_OLLAMA_MODEL` | Evidence scoring, hypothesis ranking |
| `OIE_OLLAMA_MODEL_RCA` | falls back to `OIE_OLLAMA_MODEL` | Root cause synthesis |
| `OIE_OLLAMA_MODEL_NARRATIVE` | falls back to `OIE_OLLAMA_MODEL` | Narrative generation |

### Example: Using a larger model for RCA only

```yaml
# Helm values or K8s ConfigMap patch
env:
  - name: LLM_MODEL
    value: "qwen2.5:3b"          # fast, used for triage
  - name: LLM_RCA_MODEL
    value: "qwen2.5:14b"         # larger, used only for RCA synthesis
  - name: OIE_OLLAMA_MODEL_RCA
    value: "qwen2.5:14b"
```

### LLM Guard

All text passed to the LLM is sanitized by `LLMGuard` (`internal/services/pipeline/llm_guard.go`) before the API call:

- RFC-1918 IPs → `[IP]`
- Kubernetes UIDs → `[UID]`
- `*.example.com` hostnames → `[HOST]`
- Internal service DNS (`*.svc.cluster.local`) → `[SVC]`
- Credential patterns (`password=...`, `token=...`) → `[REDACTED]`
- Neo4j bolt URIs → `[DB-URL]`

SigmaHQ-pattern prompt injection detection blocks inputs containing `ignore previous instructions`, `act as`, `<|im_start|>`, `eval(`, `exec(` and similar patterns.

LLM outputs are validated: empty responses, system-prompt echoes, and outputs containing internal IPs are discarded.
