# SRE Command Center — System Overview

> Complete walkthrough of every module, how they connect, and what the system does end-to-end.  
> Version: v1.0.0 | Last updated: 2026-05-11

---

## What the App Is

AlertHub (SRE Command Center) is an AI-powered alert correlation and incident management platform built for Aileron Platform teams. It ingests alerts from every monitoring tool — Dynatrace, Prometheus, Grafana, PagerDuty, Splunk — and automatically groups related alerts into incidents with the root cause pre-identified.

The core problem it solves: one infrastructure failure generates dozens of independent alerts across different tools. Without AlertHub, engineers wake up to 50 pages and spend 20 minutes triaging noise. With AlertHub, they see one incident titled "Kubernetes Node Down — mps-worker-z3-08", with 23 correlated alerts already grouped, blast radius mapped, and a timeline assembled.

---

## Architecture at a Glance

```
Monitoring Tools ──webhooks──► Go Backend API
                                     │
                               Alert Pipeline
                                     │
                         ┌───────────┴────────────┐
                         │  Root Cause Engine      │  ← deterministic, runs first
                         │  (Dynatrace entity +    │
                         │   topology tree walk)   │
                         └───────────┬────────────┘
                                     │ (if no match)
                         ┌───────────┴────────────┐
                         │  4-Strategy Parallel   │  ← probabilistic scoring
                         │  Correlation Engine    │
                         │  (< 30s hard timeout)  │
                         └───────────┬────────────┘
                                     │
                              Aggregator + 17-step deduplication cascade
                                     │
                           Incident Created / Merged
                                     │
                           PostgreSQL + Redis + Kafka

React Frontend  ←──── REST API ────── Go Backend
```

**Databases:**
- **PostgreSQL** — alerts, incidents, users, RBAC, correlation results, timeline
- **Redis** — alert state machine, topology graph cache, deduplication locks
- **Kafka** — alert event streaming (raw-alerts, normalized-alerts, correlation-results)
- **Weaviate** — vector embeddings for semantic similarity search
- **Neo4j** — enterprise infrastructure topology graph (optional)

---

## Module 1 — Authentication

### How It Works

Authentication is handled entirely by Apple's IdMS (Identity Management Service) via OAuth2. There is no username/password login.

**Flow:**
1. User visits the app unauthenticated
2. App immediately redirects to IdMS: `GET /api/v1/auth/mas?redirect=/dashboard`
3. IdMS validates the user's Apple credentials (SSO + 2FA)
4. IdMS redirects back with an authorization code
5. Backend exchanges code for access + refresh tokens
6. Backend provisions the user in the local DB (or updates group mappings)
7. Access token stored in `localStorage`, sent as `Authorization: Bearer <token>` on every API call

**LDAP Group → Role Mapping:**

| DS LDAP Group | AlertHub Role | Access Level |
|---|---|---|
| `interactive-apps-systems` | Admin | Full access, user management, system config |
| `IASYS-SRE` | SRE | Alert + incident management, integrations |
| `Interactive-SRE` | SRE | Alert + incident management, integrations |
| `interactive-monitoring` | Operator | Alert acknowledgment, incident updates |
| `interactive-all` | Viewer | Read-only |

**Token Refresh:** The frontend watches token expiry and silently refreshes in the background. If refresh fails, the user is redirected to IdMS again. The loading screen shown during this process displays "Refreshing Session" rather than the full initialization message.

---

## Module 2 — Alert Ingestion (Webhooks)

### How It Works

External monitoring tools send HTTP POST requests (webhooks) to AlertHub endpoints. Each source has a dedicated handler that normalizes the payload into the internal `Alert` model.

**Endpoints:**

| Route | Source | Auth |
|---|---|---|
| `POST /api/v1/webhooks/dynatrace` | Dynatrace problem notifications | Shared secret |
| `POST /api/v1/webhooks/prometheus` | Prometheus Alertmanager | Shared secret |
| `POST /api/v1/webhooks/grafana` | Grafana unified alerting | Shared secret |
| `POST /api/v1/webhooks/pagerduty` | PagerDuty incidents | Shared secret |
| `POST /api/v1/splunk/webhook` | Splunk alert actions | Shared secret |
| `POST /api/v1/webhooks/ingest` | Any source | API key (SHA-256 hashed) |

**Dynatrace Handler (Primary Source):**

Dynatrace sends both PascalCase and camelCase variants of every field. The handler reads both and falls back gracefully. Key fields extracted:
- `ProblemID` → `source_id` (used for deduplication, e.g. `P-12345`)
- `State` → `status` (OPEN → open, RESOLVED → resolved)
- `Severity` → mapped to internal levels (AVAILABILITY → critical, PERFORMANCE → high, etc.)
- `RootCauseEntity` → stored separately, used by the Root Cause Engine
- `ProblemDetails` text → parsed for embedded K8s labels (`k8s.cluster.name=…`)
- `CustomProperties` → extracted as key-value labels

**Before creating a new alert**, the handler checks if an alert with the same `source_id` already exists:
- If exists and resolved: update fields in place, no new alert
- If exists and open: update fields, re-enqueue for correlation
- If not found: create new alert

**Kafka-First Pattern:**
```
Webhook → normalize → publish to Kafka raw-alerts topic → return 202
                              ↓
                       Kafka Consumer
                              ↓
                       AlertPipelineService.EnqueueAlert
```
This means webhook handlers return in < 50ms. Processing happens asynchronously. If Kafka is unavailable, the handler falls back to direct DB insert + in-process channel enqueue.

---

## Module 3 — Alert Correlation Engine

See [CORRELATION_ENGINE.md](CORRELATION_ENGINE.md) for the full deep dive. Summary:

### Stage 0 — Root Cause Engine (Deterministic)

Runs before any AI scoring. Uses structural facts.

1. **Dynatrace rootCauseEntity** — If Dynatrace tagged a root cause entity, look for an open incident for that entity. If found: attach. If not: create (and suppress downstream alerts).
2. **Topology tree walk** — Walk the infrastructure graph upward from the alert's entity. If an ancestor has an open incident and is at a higher infra level: attach.
3. **This alert is the root** — If alert is at VM-level or above and has blast radius: create new root incident.
4. **No root found** → pass to Stage 1–4.

### Stage 1–4 — Parallel Scoring (runs only if Stage 0 returns NO_ROOT)

Four strategies run simultaneously in goroutines with a 30-second hard timeout:

| Strategy | Weight | Method |
|---|---|---|
| Semantic | 20% | BERT embeddings + Weaviate cosine similarity (0.75 threshold, 24h lookback) |
| Temporal | 10% | Exponential time decay: `e^(-0.1 × timeDiff_minutes / 30)` + severity/source bonuses |
| Topology | 25% | Redis infrastructure graph → Neo4j → string matching |
| Rules | 25% | DB-backed rules with regex, priority, and weighted conditions |

**Decision Table:**

| Signal | Decision |
|---|---|
| Topology score ≥ 0.60 | Merge/Create immediately |
| ≥ 2 strategies > 0.50 | Merge/Create |
| Final score ≥ 0.40 | Merge/Create |
| 0.20 ≤ score < 0.40 | MONITOR (hold 45s, 15s for critical) |
| Score < 0.20 | DISCARD (still check for open incident before dropping) |

### Deduplication (11-point cascade)

Before creating any new incident, 17 sequential deduplication checks rule out that it's already being tracked: in-flight race guard → per-cluster mutex → 5-min title+source → 2h entity_id → 6h cluster+domain → 30-min cascade → 2h infra cascade → topology cache → 30-min fingerprint → 2h topology merge → create new.

---

## Module 4 — Incident Management

### Auto-Created Incidents

The pipeline creates incidents automatically with:
- `auto_created = true`
- `incident_number` (sequential, e.g. `INC-1820`)
- `alert_ids[]` — all correlated alert IDs
- `blast_radius[]` — affected entity labels
- `topology_path` — e.g. `mps-nonprod-rno/mps-worker-z3-08/monitoring/Deployment/prometheus`
- `dominant_strategy` — which correlation method drove the decision
- `correlation_confidence` — 0.0–1.0
- `rca_status` — `none` / `queued` / `investigating` / `complete`

### Timeline Synthesis

For every incident (including historical ones without timeline rows), a timeline is synthesized from timestamps:
- `incidents.created_at` → "Incident Detected"
- `incidents.started_at` → "Incident Auto-Created by Pipeline"
- `incidents.acknowledged_at` → "Incident Acknowledged"
- `incidents.resolved_at` → "Incident Resolved"
- `alerts.first_seen_at` → "Alert Fired: \<title\>"
- `alerts.last_seen_at` → "Alert Updated" (if after first_seen)

Real `incident_timeline` rows (manual entries) are merged with synthetic events and sorted chronologically.

### Search

The search box works across: title, description, correlation_id, topology_path, dominant_strategy, and incident number. Supports `INC-1820`, `INC1820`, or just `1820` — the `INC-` prefix is stripped before querying.

### Auto-Resolution

When an incoming alert has `status = resolved`, the pipeline checks if all alerts linked to the incident are now resolved. If yes, the incident is automatically closed with `resolved_at` set and a timeline event added.

---

## Module 5 — Infrastructure Topology

### Data Sources

The topology service continuously discovers and maps your infrastructure from:
- **Kubernetes** — nodes, namespaces, deployments, pods (via K8s API, every 5 minutes)
- **CloudStack** — VMs, hypervisors, compute clusters
- **Manual config** — static host/VM mappings for bare metal servers

### Graph Structure

```
Bare Metal Server (Level 5)
  └─ KVM Hypervisor (Level 4)
       └─ Virtual Machine / CloudStack VM (Level 3)
            └─ Kubernetes Node (Level 2)
                 ├─ Pod: service-a (Level 1)
                 ├─ Pod: service-b (Level 1)
                 └─ Pod: service-c (Level 1)
```

This map is stored in Redis for sub-millisecond correlation lookups and backed up to Neo4j for complex graph queries.

### Stale Data Protection

If topology discovery fails (API unreachable, timeout), the service retains the last-good snapshot in Redis rather than serving empty data. The frontend shows a banner when topology data is stale.

---

## Module 6 — AI & RCA

### AI Root Cause Analysis

After an incident is created, AlertHub can trigger an autonomous RCA investigation by POSTing to a configurable RCA orchestrator service:

```json
POST <RCA_ORCHESTRATOR_URL>/investigate
{
  "alert_id": "...",
  "incident_id": "...",
  "alert_body": { "title", "severity", "source", "labels", "metadata" },
  "namespace": "monitoring",
  "cluster": "mps-nonprod-rno"
}
```

The orchestrator returns an `investigation_id`. The incident's `rca_status` is set to `investigating`. When the investigation completes, results are written back to `incidents.rca_confidence` and displayed in the UI.

### LLM Enrichment (Ollama)

A local Ollama instance (model: `qwen2.5:3b`) generates human-readable root cause summaries using the correlation result context:
- Alert title, severity, topology path
- Which strategy dominated, what was matched
- Infrastructure level of the matched entity

This becomes the `ai_root_cause` text shown in the incident view.

### BERT Semantic Service

Two BERT endpoints are configured (primary + local fallback). The service generates 768-dimensional text embeddings for alert titles/descriptions. These are stored in Weaviate and used for semantic similarity queries during correlation.

---

## Module 7 — Frontend (SRE Command Center)

### Pages

| Route | Page | Purpose |
|---|---|---|
| `/dashboard` | Dashboard | Alert velocity, incident summary, real-time stats |
| `/incidents` | Incidents | AI-correlated incident list with search, timeline, blast radius |
| `/alerts` | Alerts | Raw alert feed with filtering and correlation detail |
| `/aiops` | AIOps | Correlation engine controls, strategy weights, feedback |
| `/rca` | RCA Investigation | Active investigation status and findings |
| `/kubernetes` | Kubernetes Management | Cluster health, node/pod status |
| `/infra-topology` | Infra Topology | Interactive infrastructure graph |
| `/analytics` | Analytics | Correlation performance, noise reduction metrics |
| `/ai-chat` | AI Chat | Conversational interface to ask questions about incidents |
| `/workflows` | Workflow Builder | Automated response rule configuration |
| `/deduplication` | Deduplication | Dedup rule management |
| `/admin` | Admin | Users, roles, RBAC, integrations, system config |
| `/settings` | Settings | User preferences, notification config |

### State Management

| Store | Purpose |
|---|---|
| `enhancedAuthStore` | Authentication state, token lifecycle, MAS flow |
| `universalDataStore` | Global dashboard + incident data with background refresh |
| `enhancedUniversalDataStore` | Enhanced data loading with error handling |
| `alertsStore` | Alert list with real-time background refresh |
| `kentaurusIncidentsStore` | Incident list with filtering and unread tracking |
| `settingsStore` | User preferences (theme, notifications, API keys) |
| `themeStore` | Dark/light mode |

### Design System

Uses Apple's design language throughout: SF Pro font, Apple color tokens, HIG-compliant spacing and radius values. All styling uses CSS variables via inline styles — no Tailwind, no CSS modules.

---

## Module 8 — Integrations

### Monitoring Sources (Inbound)
- Dynatrace — problem notifications with Davis AI root cause
- Prometheus Alertmanager — firing/resolved alert groups
- Grafana — panel alerts + unified alerting (v9+)
- PagerDuty — incident webhooks
- Splunk — alert action webhooks
- Generic — API-key authenticated JSON

### Notification Targets (Outbound)
- Configured via Admin → Notifications Hub
- Supports Slack, email, PagerDuty, webhook (configurable per severity/team)

### Ticketing
- JIRA integration for incident-to-ticket sync
- ServiceNow support (configurable)

---

## Module 9 — RBAC & Permissions

| Permission | Admin | SRE | Operator | Viewer |
|---|---|---|---|---|
| View alerts/incidents | ✅ | ✅ | ✅ | ✅ |
| Acknowledge / update incidents | ✅ | ✅ | ✅ | ❌ |
| Create/delete alerts | ✅ | ✅ | ❌ | ❌ |
| Manage integrations | ✅ | ✅ | ❌ | ❌ |
| Manage users | ✅ | ❌ | ❌ | ❌ |
| Manage roles | ✅ | ❌ | ❌ | ❌ |
| System configuration | ✅ | ❌ | ❌ | ❌ |
| API key management | ✅ | ✅ | ❌ | ❌ |

---

## Zero-Alert-Lost Guarantee

Every alert processed by the pipeline is tracked in Redis with a state:

```
BUFFERED → ATTACHED (merged into incident)
         → INCIDENT_CREATED (new incident)
         → SUPPRESSED (downstream of root cause)
```

A background goroutine scans every 30 seconds for alerts stuck in `BUFFERED` for > 60 seconds and re-processes them. Worst-case time from alert arrival to incident creation: **90 seconds**.

---

## Kafka Topics

| Topic | Producer | Consumer | Content |
|---|---|---|---|
| `raw-alerts` | Webhook handlers | AlertPipelineService | Raw normalized alert payloads |
| `normalized-alerts` | AlertPipelineService | Analytics/monitoring | Enriched alerts pre-correlation |
| `correlation-results` | AlertPipelineService | Analytics/monitoring | Decision + all 4 strategy scores |
