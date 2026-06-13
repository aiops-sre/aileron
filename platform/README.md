# AlertHub — Alert Correlation & Incident Intelligence

[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![Gin](https://img.shields.io/badge/Gin-HTTP-00ADD8?style=flat-square)](https://gin-gonic.com)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+pgvector-4169E1?style=flat-square&logo=postgresql)](https://postgresql.org)
[![Neo4j](https://img.shields.io/badge/Neo4j-5.18-008CC1?style=flat-square&logo=neo4j)](https://neo4j.com)
[![Kafka](https://img.shields.io/badge/Kafka-Strimzi-231F20?style=flat-square&logo=apache-kafka)](https://strimzi.io)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](../LICENSE)

> The core AIOps engine of the Aileron platform — multi-source alert ingestion, 4-strategy probabilistic correlation (CACIE), evidence-based root cause analysis (OIE), incident lifecycle management, policy engine, and MCP server.

AlertHub is one half of the [Aileron](../README.md) open-source AIOps platform. It ingests alerts from any monitoring tool, correlates them into causal incidents using four complementary strategies, then autonomously investigates each incident through a 16-fetcher evidence DAG before generating a grounded root cause narrative. Built with Go 1.24, Gin, PostgreSQL + pgvector, Neo4j, Redis, Kafka, and React 18.

---

## Table of Contents

- [Architecture](#architecture)
- [Alert Sources](#alert-sources)
- [CACIE — 4-Strategy Correlation](#cacie--4-strategy-correlation)
- [OIE — Evidence-Based RCA](#oie--evidence-based-rca)
- [Quick Start](#quick-start)
- [Services](#services)
- [API Reference](#api-reference)
- [Configuration](#configuration)

---

## Architecture

```mermaid
flowchart TD
    subgraph Webhooks["Webhook Ingestion"]
        DT[Dynatrace]
        PROM[Prometheus / Alertmanager]
        GRAF[Grafana]
        CLOUD[Cloud Platforms\nAWS · GCP · Azure · AliCloud]
        CNCF[CNCF Tools\nFalco · Kyverno · Flux · Keptn]
        GEN[Generic / CloudEvents / OTLP]
    end

    subgraph Norm["Normalization"]
        NRM[Normalizer\nFingerprint · Severity · EntityType]
        KAFKA[(Kafka\nalerthub.raw-alerts)]
    end

    subgraph Pipeline["Three-Stage Alert Pipeline"]
        FAST["FAST PATH\n32 workers · cap 10k\nFingerprint dedup\nResolved fast-exit"]
        TOPO["TOPO PATH\n16 workers · cap 5k\nDeterministic RCA\nTopology override ≥ 0.60"]
        FULL["FULL PATH\n8 workers · cap 2k\n4-strategy CACIE scoring\nMerge threshold 0.75"]
    end

    subgraph CACIE["CACIE — Correlation Engine"]
        T["Topology Graph · 45%\nNeo4j recursive traversal\nup to 8 hops · domain decay"]
        R["Operator Rules · 25%\nRegex + label match\npriority-ordered"]
        B["BERT Semantic · 20%\nall-MiniLM-L6-v2\n384-dim cosine similarity"]
        TMP["Temporal Decay · 10%\nexp(−λt) · half-life 30 min\ndomain-aware profiles"]
    end

    subgraph Store["Data Layer"]
        PG[(PostgreSQL 15\n+ pgvector 768-dim)]
        NEO[(Neo4j 5.18\nTopology Graph)]
        REDIS[(Redis 7\nPipeline Cache)]
    end

    subgraph OIE["OIE — Operational Intelligence Engine"]
        BUS["Evidence Bus\n16 parallel fetchers · 45s budget"]
        HYP["Hypothesis Engine\nWinnerFrom gate\nconfidence ≥ 0.75 · facts ≥ 3"]
        LLM["LLM Narrator\nOllama qwen2.5:3b\ntemp=0.1 · 7-layer guard"]
        WB["Writeback\nrca_confidence · root_cause\nevidence_count → incident"]
    end

    subgraph UI["React 18 Dashboard"]
        DASH[AIOps Page\nSituations · Incidents · RCA]
        WS[WebSocket\nReal-time streaming]
    end

    Webhooks --> NRM --> KAFKA
    KAFKA --> FAST --> TOPO --> FULL
    FULL --> CACIE
    T & R & B & TMP --> CACIE
    CACIE --> |"score ≥ 0.75: merge\nscore < 0.75: create"| PG
    PG --> |"incident.created Kafka event"| OIE
    BUS --> HYP --> LLM --> WB --> PG
    PG & NEO & REDIS --> DASH
    WB --> WS --> DASH

    style FAST fill:#1a1a2e,color:#eee
    style TOPO fill:#16213e,color:#eee
    style FULL fill:#0f3460,color:#eee
    style CACIE fill:#533483,color:#eee
    style OIE fill:#7c2d12,color:#fff
```

---

## Alert Sources

AlertHub accepts webhooks from every major monitoring and security platform. All payloads are normalized to a common `Alert` struct with `fingerprint`, `severity`, `entity_type`, `cluster`, and `labels` (JSONB) before entering the pipeline.

### Observability & APM

| Platform | Webhook Path |
|---|---|
| Dynatrace | `POST /api/v1/webhooks/event-driven/dynatrace` |
| Prometheus / Alertmanager | `POST /api/v1/webhooks/event-driven/prometheus` |
| Grafana | `POST /api/v1/webhooks/event-driven/grafana` |
| Splunk | `POST /api/v1/webhooks/event-driven/splunk` |
| Datadog | `POST /api/v1/webhooks/event-driven/datadog` |
| New Relic | `POST /api/v1/webhooks/event-driven/newrelic` |
| Thanos Ruler | `POST /api/v1/webhooks/prometheus` (Prometheus-compatible) |
| VictoriaMetrics VMAlert | `POST /api/v1/webhooks/prometheus` (Prometheus-compatible) |
| Generic JSON | `POST /api/v1/webhooks/event-driven/generic` |
| OpenTelemetry (OTLP) | `POST /api/v1/otlp/v1/metrics` · `POST /api/v1/otlp/v1/logs` |

### Cloud Platforms

| Platform | Webhook Path |
|---|---|
| AWS CloudWatch | `POST /api/v1/webhooks/cloud/aws/cloudwatch` |
| AWS GuardDuty | `POST /api/v1/webhooks/cloud/aws/guardduty` |
| AWS EventBridge | `POST /api/v1/webhooks/cloud/aws` |
| GCP Cloud Monitoring | `POST /api/v1/webhooks/cloud/gcp/monitoring` |
| GCP Security Command Center | `POST /api/v1/webhooks/cloud/gcp/scc` |
| Azure Monitor | `POST /api/v1/webhooks/cloud/azure/monitor` |
| Azure Sentinel | `POST /api/v1/webhooks/cloud/azure/sentinel` |
| Alibaba Cloud CMS | `POST /api/v1/webhooks/cloud/alicloud` |
| OpsGenie | `POST /api/v1/webhooks/cloud/opsgenie` |

### CNCF & Security Tools

| Tool | Webhook Path |
|---|---|
| Falco | `POST /api/v1/webhooks/cncf/falco` |
| Kyverno | `POST /api/v1/webhooks/cncf/kyverno` |
| OPA / Gatekeeper | `POST /api/v1/webhooks/cncf/opa` |
| Flux CD | `POST /api/v1/webhooks/cncf/flux` |
| Keptn | `POST /api/v1/webhooks/cncf/keptn` |
| CloudEvents v1.0 | `POST /api/v1/webhooks/cncf/cloudevents` |
| Grafana Loki | `POST /api/v1/webhooks/cncf/loki` |

---

## CACIE — 4-Strategy Correlation

**CACIE** (Correlation And Causation Intelligence Engine) scores incoming alerts against all open incidents using four strategies in parallel. The weighted sum determines whether an alert merges into an existing incident or creates a new one.

```
score = topology × 0.45 + rules × 0.25 + semantic × 0.20 + temporal × 0.10
merge threshold: 0.75
topology dominance override: ≥ 0.60 → deterministic attach (no ML needed)
```

| Strategy | Weight | Engine | Notes |
|---|---|---|---|
| **Topology Graph** | 45% | Neo4j 5.18 Cypher | Recursive traversal up to 8 hops. Domain-aware propagation decay. BM → KVM → VM → K8s node → pod hierarchy. |
| **Operator Rules** | 25% | DB-backed regex | Priority-ordered rules on title, entity_type, and JSONB labels. Editable at runtime via UI. |
| **BERT Semantic** | 20% | `all-MiniLM-L6-v2` (384-dim) | Cosine similarity over pgvector. Covers semantically equivalent alerts from different sources. |
| **Temporal Decay** | 10% | `exp(−λt)` | Half-life 30 min by default. Domain-specific profiles: network cascades use 3-min half-life, storage 15-min. |

The **three-stage pipeline** optimizes throughput:

- **FAST PATH** (32 workers, cap 10k): fingerprint dedup via `sync.Map`, fast-exit on resolved alerts. Sub-2ms.
- **TOPO PATH** (16 workers, cap 5k): deterministic root cause check via Redis + Neo4j. Sub-50ms.
- **FULL PATH** (8 workers, cap 2k): all four strategies in parallel, aggregator, 17-point dedup cascade.

A Dynatrace `rootCauseEntity` tag is a hard override — immediate deterministic attach at sub-millisecond latency.

---

## OIE — Evidence-Based RCA

**OIE** (Operational Intelligence Engine) is a standalone Go service that consumes `alerthub.incidents` Kafka events and runs a full evidence investigation before asserting a root cause. The LLM is the last step, not the first.

### Evidence Bus — 16 Fetchers

OIE runs 16 fetchers in parallel with a 45-second hard budget:

| # | Fetcher | What It Collects |
|---|---|---|
| 1 | K8s Node Conditions | `NotReady`, `MemoryPressure`, `DiskPressure` events |
| 2 | Pod Exit Codes | OOMKilled, CrashLoopBackOff, exit signal |
| 3 | PDB Status | PodDisruptionBudget violations, minAvailable breaches |
| 4 | CloudStack VM / Host | VM state, KVM host health |
| 5 | NetApp Volume / SVM | Volume utilization, latency, SVM status |
| 6 | KubeSense Signals | Chaos score, config violations, health events from agent |
| 7 | OKG Change Correlation | GitOps changes in the 2-hour pre-incident window |
| 8 | Runbook Catalog | Team-authored runbooks matched by domain / entity_type |
| 9 | Past Investigations | pgvector semantic search (nomic-embed-text 768-dim, cosine ≥ 0.70) |
| 10 | APM Regressions | Error rate, latency p99, saturation from golden signals |
| 11 | Topology Resolve | Entity resolution via `GET /api/v1/topology/resolve` |
| 12 | Blast Radius | Neo4j traversal — how many downstream entities are affected |
| 13 | Resource Limits | Missing requests/limits, PVC capacity |
| 14 | Chaos Score | Per-cluster readiness grade from KubeSense |
| 15 | Config Violations | Missing probes, `:latest` image, single replica workloads |
| 16 | Change Attribution | Exact ArgoCD sync / commit / actor linked to incident |

### 7-Layer Hallucination Prevention

The LLM narrator is surrounded by seven guards:

1. `sanitizeForPrompt()` — strips newlines, caps evidence descriptions at 300 chars, removes RFC-1918 IPs and internal hostnames
2. `countGroundingFacts()` — **blocks LLM if 0 real facts** — falls back to deterministic template
3. Temperature = 0.1 — near-deterministic generation
4. Anti-hallucination system prompt — "Do NOT invent names. Only use facts listed above. If unclear, say you don't know."
5. `isLLMRefusal()` — detects refusal phrases and replaces with template
6. Ensemble vote — 2-model agreement required for high-confidence assertions
7. Uncertainty template fallback — guaranteed non-empty output even when all guards fire

The **WinnerFrom gate** requires confidence ≥ 0.75 AND supporting evidence count ≥ 3 before any hypothesis is promoted to root cause.

---

## Quick Start

See the [root README](../README.md) for full Docker Compose and Helm quick start instructions.

```bash
# Clone and start locally
git clone https://github.com/aiops-sre/aileron.git
cd aileron
cp platform/.env.example platform/.env
# Edit platform/.env — set OIDC_PROVIDER_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET
docker compose up

# Send a test alert
curl -X POST http://localhost:8080/api/v1/webhooks/event-driven/prometheus \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "HighCPU", "severity": "critical", "instance": "node-01"},
      "annotations": {"summary": "CPU usage above 90%"}
    }]
  }'
```

---

## Services

| Service | Port | Image | Description |
|---|---|---|---|
| `aileron-platform` | 8080 | `ghcr.io/aiops-sre/aileron-platform` | Go 1.24 — alert pipeline, CACIE, incident CRUD, OIDC auth, MCP server, WebSocket |
| `aileron-frontend` | 80 | `ghcr.io/aiops-sre/aileron-frontend` | React 18 + TypeScript — AIOps dashboard, 30+ pages |
| `aileron-oie` | 8081 | `ghcr.io/aiops-sre/aileron-oie` | Go OIE — 16-fetcher evidence DAG, hypothesis engine, LLM narrator |
| `bert-service` | 8766 | `ghcr.io/aiops-sre/aileron-bert` | Python — `all-MiniLM-L6-v2` BERT embeddings (384-dim) |
| `ollama` | 11434 | `ollama/ollama` | Local LLM — `qwen2.5:3b` + `nomic-embed-text` |
| `postgres` | 5432 | `postgres:15` | Primary store + pgvector (768-dim embeddings) |
| `neo4j` | 7474/7687 | `neo4j:5.18` | Infrastructure topology graph |
| `redis` | 6379 | `redis:7` | Pipeline state, topology cache, rate limiting |
| `kafka` | 9092 | `strimzi/kafka` | Alert event bus (3 brokers) |

---

## API Reference

### Webhooks (no auth)

| Method | Path | Source |
|---|---|---|
| `POST` | `/api/v1/webhooks/event-driven/dynatrace` | Dynatrace problem webhook |
| `POST` | `/api/v1/webhooks/event-driven/prometheus` | Prometheus Alertmanager |
| `POST` | `/api/v1/webhooks/event-driven/grafana` | Grafana |
| `POST` | `/api/v1/webhooks/event-driven/splunk` | Splunk alert action |
| `POST` | `/api/v1/webhooks/event-driven/datadog` | Datadog monitor |
| `POST` | `/api/v1/webhooks/event-driven/newrelic` | New Relic incident |
| `POST` | `/api/v1/webhooks/event-driven/generic` | Generic JSON |
| `POST` | `/api/v1/webhooks/cloud/aws/cloudwatch` | AWS CloudWatch |
| `POST` | `/api/v1/webhooks/cloud/gcp/monitoring` | GCP Cloud Monitoring |
| `POST` | `/api/v1/webhooks/cloud/azure/monitor` | Azure Monitor |
| `POST` | `/api/v1/webhooks/cncf/falco` | Falco security events |
| `POST` | `/api/v1/webhooks/cncf/flux` | Flux CD reconciliation |

### Incidents (JWT required)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/incidents` | List incidents (filter: status, severity, source) |
| `POST` | `/api/v1/incidents` | Create incident manually |
| `GET` | `/api/v1/incidents/:id` | Incident detail + correlated alerts |
| `PATCH` | `/api/v1/incidents/:id` | Update status / severity |
| `GET` | `/api/v1/incidents/:id/timeline` | Incident event timeline |
| `GET` | `/api/v1/incidents/:id/rca` | RCA findings |
| `GET` | `/api/v1/incidents/:id/postmortem` | Auto-generated postmortem |
| `GET` | `/api/v1/incidents/:id/remediations` | Gate-hook remediation proposals |
| `POST` | `/api/v1/incidents/:id/remediations/:rid/approve` | Approve a remediation |

### RCA & Topology

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/rca/investigations` | List OIE investigations |
| `GET` | `/api/v1/rca/investigations/:id` | Investigation detail |
| `GET` | `/api/v1/topology/resolve` | Entity resolution via Neo4j + Redis |
| `GET` | `/api/v1/topology/blast-radius` | Blast radius traversal |
| `GET` | `/api/v1/topology/live` | Live topology snapshot |

### Intelligence & Policy

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/intelligence/stats` | 24-hour platform-wide stats |
| `GET` | `/api/v1/intelligence/policies` | List intelligence policies |
| `POST` | `/api/v1/intelligence/policies` | Create a policy |
| `GET` | `/api/v1/intelligence/runbooks` | Runbook catalog |
| `GET` | `/api/v1/mcp` | MCP server manifest |
| `POST` | `/api/v1/mcp` | MCP JSON-RPC endpoint (7 tools) |

### Real-Time

| Protocol | Path | Description |
|---|---|---|
| `WebSocket` | `/ws` | Alert, incident, and topology event stream |
| `WebSocket` | `/ws/investigations/:id` | OIE investigation event stream |

### Health

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Liveness probe |
| `GET` | `/health/detailed` | Component health: DB, Kafka, Neo4j, Redis |
| `GET` | `/ready` | Readiness probe |
| `GET` | `/metrics` | Prometheus metrics |

---

## Configuration

All configuration is via environment variables. Copy `.env.example` to `.env` to start.

### Required

| Variable | Description |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN: `postgres://user:pass@host:5432/db?sslmode=disable` |
| `KAFKA_BROKERS` | Comma-separated Kafka brokers: `kafka:9092` |
| `REDIS_ADDR` | Redis address: `redis:6379` |
| `NEO4J_URI` | Neo4j bolt URI: `bolt://neo4j:7687` |
| `NEO4J_USER` | Neo4j username |
| `NEO4J_PASSWORD` | Neo4j password |
| `OIDC_PROVIDER_URL` | OIDC discovery URL (Keycloak, GitHub, Google, Dex, etc.) |
| `OIDC_CLIENT_ID` | OAuth2 client ID |
| `OIDC_CLIENT_SECRET` | OAuth2 client secret |
| `JWT_SECRET` | HMAC secret, min 32 chars (`openssl rand -hex 32`) |
| `JWT_REFRESH_SECRET` | HMAC refresh token secret, min 32 chars |
| `INTERNAL_SERVICE_TOKEN` | Service-to-service auth token (fatal if unset in production) |

### Key Optional

| Variable | Default | Description |
|---|---|---|
| `OLLAMA_URL` | `http://ollama:11434` | Ollama LLM endpoint |
| `LLM_MODEL` | `qwen2.5:3b` | Default Ollama model |
| `BERT_SERVICE_URL` | `http://bert-service:8766` | BERT embedding service |
| `OIDC_ADMIN_GROUPS` | — | Comma-separated OIDC group names that map to admin role |
| `OIDC_OPERATOR_GROUPS` | — | Groups that map to operator role |
| `OIDC_VIEWER_GROUPS` | — | Groups that map to viewer role |
| `OIDC_DEFAULT_ROLE` | `viewer` | Role for users not in any configured group |
| `ALLOWED_ORIGINS` | — | CORS allowlist (required in production) |
| `CORRELATION_THRESHOLD` | `0.75` | Score threshold to merge alerts into existing incident |
| `TOPOLOGY_DOMINANCE_THRESHOLD` | `0.60` | Topology score for deterministic override |
| `INTELLIGENCE_SLACK_WEBHOOK` | — | Slack webhook for gate hook notifications |
| `KUBESENSE_CORE_URL` | `http://kubesense-core.aileron-agent.svc.cluster.local:8080` | KubeSense API proxy |
| `ENV` | — | Set to `production` to enable stricter security guards |

### OIE-Specific

| Variable | Default | Description |
|---|---|---|
| `OIE_ALERTHUB_BASE_URL` | `http://aileron-platform:8080` | AlertHub topology resolve + writeback |
| `OIE_AUTO_INVESTIGATE_SEVERITIES` | `critical,high,medium` | Severities that trigger OIE automatically |
| `OIE_INVESTIGATION_TIME_BUDGET_MS` | `45000` | Hard timeout per investigation (45s) |
| `OIE_MAX_CONCURRENT_INVESTIGATIONS` | `20` | Semaphore cap on parallel investigations |
| `OIE_OLLAMA_MODEL_NARRATIVE` | `qwen2.5:3b` | Model for root cause narrative |

---

## Project Structure

```
platform/
├── cmd/main.go                  # Entry point — router + service wiring
├── internal/
│   ├── api/                     # HTTP handlers + middleware + WebSocket
│   ├── auth/oidc/               # Generic OIDC / OAuth2 authentication
│   ├── services/
│   │   ├── pipeline/            # Kafka consumer + 3-stage alert pipeline
│   │   ├── correlation/         # CACIE — 4 strategies + aggregator + explainability
│   │   ├── topology/            # Neo4j + Redis topology service
│   │   ├── incidents/           # Incident lifecycle + evolution engine
│   │   ├── normalization/       # Per-source normalizers
│   │   └── intelligence/        # Policies, runbooks, postmortems, gate hooks
│   └── db/                      # PostgreSQL migrations + query layer
├── services/
│   ├── oie/                     # OIE — 16-fetcher evidence DAG
│   └── bert-service/            # Python BERT embedding service
├── frontend/                    # React 18 + TypeScript dashboard
├── database/
│   └── schema.sql               # PostgreSQL schema + pgvector setup
├── docker/docker-compose.yml    # Local dev stack
├── helm/                        # Helm chart
├── scripts/
│   ├── mock_alerts.sh           # Send test webhooks
│   ├── mock_cascade_alerts.sh   # Simulate cascading failure
│   └── test_comprehensive_v2.sh # Full pipeline regression test
└── docs/                        # Deep-dive architecture and operations docs
```

---

## License

Apache 2.0 — see [LICENSE](../LICENSE).

Part of the [Aileron](../README.md) open-source AIOps platform.
