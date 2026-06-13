<p align="center">
  <img src="logo-full.svg" alt="Aileron — Event Intelligence Platform" width="800"/>
</p>

# Aileron — Open-Source Enterprise AIOps Platform

[![CI](https://github.com/aiops-sre/aileron/actions/workflows/ci.yml/badge.svg)](https://github.com/aiops-sre/aileron/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](https://golang.org)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react)](https://reactjs.org)
[![Kafka](https://img.shields.io/badge/Kafka-3.x-231F20?logo=apache-kafka)](https://kafka.apache.org)
[![Ollama](https://img.shields.io/badge/LLM-Ollama-black)](https://ollama.com)
[![OIDC](https://img.shields.io/badge/Auth-OIDC%20%2F%20OAuth2-orange)](https://openid.net/connect/)

> **Turn thousands of monitoring alerts into a handful of causal situations — with evidence-based root cause, change attribution, and runbook suggestions.**

Aileron is a **self-hosted, open-source AIOps platform** comparable to Dynatrace Davis AI, Moogsoft, and BigPanda — built for teams that need enterprise-grade alert correlation, automated root cause analysis, and Kubernetes causal intelligence without vendor lock-in or per-host pricing.

---

## Table of Contents

- [What is Aileron?](#what-is-aileron)
- [Platform Architecture](#platform-architecture)
- [Alert Processing Pipeline](#alert-processing-pipeline)
- [CACIE — Correlation Engine](#cacie--correlation-engine)
- [OIE — Operational Intelligence Engine](#oie--operational-intelligence-engine)
- [Davis AI Algorithms — KubeSense](#davis-ai-algorithms--kubesense)
- [Auth Flow](#auth-flow)
- [GitOps CI/CD](#gitops-cicd)
- [Key Features](#key-features)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Services](#services)
- [API Reference](#api-reference)
- [Contributing](#contributing)
- [License](#license)

---

## What is Aileron?

Aileron ships two deeply integrated products:

| Product | Description |
|---|---|
| **AlertHub** | Multi-source alert ingestion, 4-strategy correlation engine (CACIE), incident lifecycle management, evidence-based RCA via OIE, policy engine, runbooks, postmortems, MCP server |
| **KubeSense Agent** | Kubernetes causal intelligence — topology mapping, chaos readiness scoring, APM golden signals, pre-deploy risk assessment, change correlation, 5 Davis AI algorithms |

Both surface through a unified **AIOps dashboard** — one pane of glass for SRE and platform teams.

---

## Platform Architecture

```mermaid
flowchart TD
    subgraph Sources["Alert Sources"]
        DT[Dynatrace]
        PROM[Prometheus / Alertmanager]
        GRAF[Grafana]
        SPL[Splunk]
        GEN[Generic Webhook]
        KS[KubeSense Agent]
    end

    subgraph Ingest["Ingestion Layer"]
        GW[Universal Alert Gateway]
        NORM[Normalizer\nDT · Prometheus · Grafana · Splunk]
        KAFKA[(Kafka\nalerthub.raw-alerts)]
    end

    subgraph Pipeline["Three-Stage Alert Pipeline"]
        direction LR
        FAST["FAST PATH\n32 workers · cap 10k\nFingerprint dedup\nResolved fast-exit"]
        TOPO["TOPO PATH\n16 workers · cap 5k\nDeterministic RCA\nTopology override ≥0.60"]
        FULL["FULL PATH\n8 workers · cap 2k\n4-strategy scoring\nMerge threshold 0.75"]
    end

    subgraph CACIE["CACIE — Correlation Engine"]
        direction TB
        T["Topology Graph\n45% weight\nNeo4j Cypher"]
        R["Operator Rules\n25% weight\nRegex + label match"]
        B["BERT Semantic\n20% weight\nall-MiniLM-L6-v2\n384-dim embeddings"]
        TMP["Temporal Decay\n10% weight\nhalf-life 30 min"]
    end

    subgraph OIE["OIE — Operational Intelligence Engine"]
        direction TB
        BUS["Evidence Bus\n16 parallel fetchers"]
        HYP["Hypothesis Engine\nScoring + ranking"]
        LLM["LLM Narrator\nOllama qwen2.5:3b\ntemp=0.1 · 7-layer guard"]
        WB["Writeback\nIncident RCA result"]
    end

    subgraph Store["Data Layer"]
        PG[(PostgreSQL 15\n+ pgvector)]
        NEO[(Neo4j 5.18\nTopology Graph)]
        REDIS[(Redis 7\nCache + Pub/Sub)]
    end

    subgraph UI["React Dashboard"]
        DASH[AIOps Page\nSituations · Incidents · RCA]
        KSP[KubeSense Page\n13 intelligence tabs]
        WS[WebSocket\nReal-time streaming]
    end

    Sources --> GW
    GW --> NORM --> KAFKA
    KAFKA --> FAST --> TOPO --> FULL
    FULL --> CACIE
    T & R & B & TMP --> CACIE
    CACIE --> |"incident.created"| OIE
    BUS --> HYP --> LLM --> WB
    OIE --> PG
    CACIE --> PG
    PG & NEO & REDIS --> UI
    WB --> WS --> DASH

    style FAST fill:#1a1a2e,color:#eee
    style TOPO fill:#16213e,color:#eee
    style FULL fill:#0f3460,color:#eee
    style CACIE fill:#533483,color:#eee
    style OIE fill:#e94560,color:#fff
    style Sources fill:#1a1a2e,color:#eee
    style UI fill:#0d7377,color:#fff
```

---

## Alert Processing Pipeline

```mermaid
sequenceDiagram
    participant W as Webhook
    participant GW as Gateway
    participant K as Kafka
    participant P as Pipeline
    participant C as CACIE
    participant I as Incident DB
    participant O as OIE

    W->>GW: POST /webhooks/{source}
    GW->>GW: Normalize → Alert struct
    Note over GW: fingerprint · severity · entity_type · labels
    GW->>K: Publish raw-alerts topic
    K->>P: Consume (32 fast workers)
    P->>P: Fingerprint dedup\n17-point cascade
    alt Resolved alert
        P->>I: Fast-exit UPSERT resolved_at
    else Active alert
        P->>C: Score correlation
        C->>C: Topology 45% + Rules 25%\n+ BERT 20% + Temporal 10%
        alt Score ≥ 0.75
            C->>I: Merge into existing incident
        else New situation
            C->>I: CREATE new incident
            I->>K: Publish alerthub.incidents
            K->>O: Trigger OIE investigation
        end
    end
    O-->>I: Writeback RCA result
```

---

## CACIE — Correlation Engine

```mermaid
flowchart LR
    subgraph Input
        A[Alert A\nCPU spike · node-01]
        B[Alert B\nPod evicted · app-ns]
        C[Alert C\nDisk IO high · node-01]
    end

    subgraph Strategies["4 Correlation Strategies"]
        T["Topology Graph\n45%\nNeo4j path distance\nnode-01 → pod → app\nOverride at ≥0.60"]
        R["Operator Rules\n25%\nRegex pattern match\nlabel key/value filter"]
        S["BERT Semantic\n20%\nall-MiniLM-L6-v2\n384-dim · Weaviate ANN\ncosine similarity"]
        D["Temporal Decay\n10%\nexp(-λt)\nhalf-life 30 min"]
    end

    subgraph Decision
        SCORE["Weighted Score\n0.0 → 1.0"]
        THR{"≥ 0.75?"}
        MERGE[Merge into\nexisting incident]
        NEW[Create new\nincident]
    end

    A & B & C --> T & R & S & D
    T & R & S & D --> SCORE --> THR
    THR -->|Yes| MERGE
    THR -->|No| NEW

    style T fill:#7c3aed,color:#fff
    style R fill:#0369a1,color:#fff
    style S fill:#065f46,color:#fff
    style D fill:#92400e,color:#fff
    style MERGE fill:#166534,color:#fff
    style NEW fill:#991b1b,color:#fff
```

---

## OIE — Operational Intelligence Engine

```mermaid
flowchart TD
    subgraph Trigger
        INC[incident.created\nKafka event]
    end

    subgraph Evidence["Evidence Bus — 16 Parallel Fetchers"]
        direction TB
        F1[K8s Node Conditions]
        F2[Pod Exit Codes]
        F3[PDB Status]
        F4[CloudStack VM/Host]
        F5[NetApp Volume/SVM]
        F6[KubeSense Signals]
        F7[OKG Change Correlation]
        F8[Runbook Catalog]
        F9[Past Investigations\n+ pgvector semantic]
        F10[APM Regressions]
        F11[Topology Resolve]
        F12[Blast Radius]
        F13[Resource Limits]
        F14[Chaos Score]
        F15[Config Violations]
        F16[Change Attribution]
    end

    subgraph Hypothesis["Hypothesis Engine"]
        GEN[Generate hypotheses\nfrom evidence]
        SCORE[Score each\nhypothesis]
        WIN["WinnerFrom gate\nconfidence ≥ 0.75\nfacts ≥ 3"]
    end

    subgraph LLMLayer["LLM Narrator — 7-layer Guard"]
        P1[sanitizeForPrompt\nnewline strip · 300 char cap]
        P2[countGroundingFacts\nblock if 0 real facts]
        P3[temperature = 0.1]
        P4[Anti-hallucination\nsystem prompt]
        P5[isLLMRefusal\nrefusal detection]
        P6[Ensemble vote\n2-model agreement]
        P7[Uncertainty template\nfallback]
        NARR[3-sentence\nRoot Cause Narrative]
    end

    INC --> Evidence
    F1 & F2 & F3 & F4 & F5 & F6 & F7 & F8 & F9 & F10 & F11 & F12 & F13 & F14 & F15 & F16 --> GEN
    GEN --> SCORE --> WIN
    WIN --> P1 --> P2 --> P3 --> P4 --> P5 --> P6 --> P7 --> NARR

    style WIN fill:#dc2626,color:#fff
    style NARR fill:#16a34a,color:#fff
    style P1 fill:#1e3a5f,color:#eee
    style P2 fill:#1e3a5f,color:#eee
    style P3 fill:#1e3a5f,color:#eee
    style P4 fill:#1e3a5f,color:#eee
    style P5 fill:#1e3a5f,color:#eee
    style P6 fill:#1e3a5f,color:#eee
    style P7 fill:#1e3a5f,color:#eee
```

---

## Davis AI Algorithms — KubeSense

```mermaid
flowchart TD
    subgraph Input["KubeSense Events\n16 Kafka topics"]
        HE[Health Events]
        WE[Workload Events]
        CE[Config Events]
        SE[Storage Events]
    end

    subgraph A1["Algorithm 1 — Holt-Winters Baseline\n(Dynatrace-style)"]
        HW["BaselineModel\nLevel · Trend · Seasonal\nα=0.1 · β=0.01 · γ=0.05\n168-slot seasonal window"]
        ANOM["IsAnomaly()\nMAD-based threshold"]
    end

    subgraph A2["Algorithm 2 — Alert State Machine\n(Universal)"]
        SM["States: OK → Pending → Firing\n→ Resolved → Suppressed\n2min pending · 5min resolve\n4-flap suppress"]
    end

    subgraph A3["Algorithm 3 — Union-Find Grouper\n(BigPanda-style)"]
        UF["Multi-signal similarity\nTopology 0.4 + Label Jaccard 0.3\n+ Temporal exp 0.2 + Family 0.1\nθ=0.45 · 15-min window"]
        RESULT["15 raw alerts\n→ 5 situations\n67% noise reduction"]
    end

    subgraph A4["Algorithm 4 — Topology RCA\n(Davis AI-style)"]
        TOPO["entityDepth() scoring\nNode=0 · Deployment=1 · Pod=3\ninfraBost: Node×1.5 · PVC×1.3\nO(1) deterministic · no ML"]
        SYMP["SymptomFilter()\nSuppress pod alerts\nwhen node is root cause"]
    end

    subgraph A5["Algorithm 5 — Change Correlation\n(BigPanda-style)"]
        CC["2-hour lookback\nkubesense_changes table\nconfidence = overlap × exp(-dt/30)\nLinks incident → ArgoCD commit"]
    end

    Input --> A1 & A2
    A1 --> A3
    A2 --> A3
    A3 --> A4
    A4 --> A5

    RESULT -.-> |"Published to\nalerthub.incidents"| CORE[AlertHub\nOIE Investigation]

    style A1 fill:#1e3a5f,color:#eee
    style A2 fill:#1e3a5f,color:#eee
    style A3 fill:#3b0764,color:#eee
    style A4 fill:#1c1917,color:#eee
    style A5 fill:#064e3b,color:#eee
    style RESULT fill:#dc2626,color:#fff
    style CORE fill:#0369a1,color:#fff
```

---

## Auth Flow

```mermaid
sequenceDiagram
    participant U as User Browser
    participant F as Frontend
    participant B as AlertHub Backend
    participant O as OIDC Provider\n(Keycloak/Okta/GitHub)
    participant L as OIDC Groups Claim

    U->>F: Visit https://aileron.example.com
    F->>B: GET /api/v1/auth/oidc
    B->>O: Redirect → authorization_endpoint
    O->>U: Login page
    U->>O: Credentials
    O->>B: Callback with code
    B->>O: Exchange code → access_token + id_token
    B->>L: Parse groups claim from id_token
    Note over B: ResolveRole(groups)\nadmin → operator → viewer → default
    B->>B: Auto-provision user\nif OIDC_AUTO_PROVISION=true
    B->>B: Sign JWT (HS256)\nAccess token 15min\nRefresh token 7d
    B->>F: Set JWT in sessionStorage
    F->>U: Dashboard
```

---

## GitOps CI/CD

```mermaid
flowchart LR
    DEV[git push\nmain branch] --> CI

    subgraph CI["GitHub Actions CI"]
        TEST["test-platform\ngo build + vet\nGo 1.24"]
        TEST2["test-agent\ngo build\nGo 1.22"]
        TEST3["test-frontend\nnpm ci + build\nNode 20"]
    end

    CI --> BUILD

    subgraph BUILD["Image Build & Publish"]
        P["aileron-platform\nghcr.io/aiops-sre/aileron-platform"]
        OI["aileron-oie\nghcr.io/aiops-sre/aileron-oie"]
        AG["aileron-agent\nghcr.io/aiops-sre/aileron-agent"]
        CO["aileron-collector\nghcr.io/aiops-sre/aileron-collector"]
    end

    BUILD --> HELM

    subgraph HELM["Helm Deploy"]
        ARGO[ArgoCD\nPreSync BuildKit hook]
        SYNC[helm upgrade --install\naileron ./platform/helm\n--namespace aileron]
    end

    HELM --> LIVE[Live Cluster]

    style CI fill:#1e3a5f,color:#eee
    style BUILD fill:#3b0764,color:#eee
    style HELM fill:#064e3b,color:#eee
    style LIVE fill:#166534,color:#fff
```

---

## Key Features

### AlertHub — Alert Correlation & Incident Management

- **Multi-source ingestion** — Dynatrace, Prometheus/Alertmanager, Grafana, Splunk, Datadog, New Relic, generic webhook. All normalized to a common `Alert` struct with fingerprint, severity, entity_type, cluster, and JSONB labels.
- **Three-stage pipeline** — FAST PATH (32 workers, fingerprint dedup), TOPO PATH (16 workers, deterministic topology RCA), FULL PATH (8 workers, 4-strategy probabilistic scoring). 17-point dedup cascade prevents duplicate incidents.
- **CACIE 4-strategy correlation** — topology graph (45%), operator regex rules (25%), BERT semantic embeddings (20%), temporal decay (10%). Topology ≥ 0.60 is a deterministic override. Merge threshold: 0.75.
- **OIE evidence-first RCA** — 16-fetcher parallel evidence DAG. 7-layer hallucination prevention: `sanitizeForPrompt()`, `countGroundingFacts()`, temperature=0.1, WinnerFrom gate (confidence ≥ 0.75, facts ≥ 3), anti-injection prompt, `isLLMRefusal()` detection, ensemble agreement voting.
- **Policy engine** — DB-driven `intelligence_policies` table. Types: `suppress_alert`, `suppress_incident`, `skip_rca`, `require_approval`, `auto_resolve`. 5-minute cache, 500 policy limit.
- **Gate hooks** — Proposed remediations enter `remediations_pending` with `status=proposed`. Slack notification → operator approves/rejects in UI. **No automated action without explicit approval.**
- **MCP Server** — `POST /api/v1/mcp` exposes 7 tools to Claude Desktop, Cursor, Windsurf: `list_incidents`, `get_incident`, `get_rca_decisions`, `search_incidents`, `get_postmortem`, `list_runbooks`, `propose_remediation`.
- **Postmortem auto-generation** — On incident resolution, generates structured document (impact, root cause, timeline, lessons, action items). LLM-generated when `rca_confidence ≥ 0.60`, deterministic fallback otherwise.
- **Runbook catalog** — Team-authored runbooks matched by domain/entity_type/failure_class, injected into every OIE investigation.
- **Real-time dashboard** — WebSocket streaming, 30+ React pages, dark-mode design system.

### KubeSense Agent — Kubernetes Causal Intelligence

- **5 Davis AI algorithms** — Holt-Winters baseline, state machine + flap suppression, Union-Find multi-signal grouper (67% noise reduction), topology-anchored RCA (O(1), no ML), change correlation RCA.
- **Chaos readiness scoring** — Per-cluster grade A–F based on: PDB presence, replica count, resource limits, HPA, probes, anti-affinity rules.
- **APM golden signals** — Latency, traffic, errors, saturation tracked per service.
- **Pre-deploy risk assessment** — `POST /risk/score` evaluates a proposed change before deployment.
- **Config violation detection** — Continuous drift monitoring against defined policies.
- **Change attribution** — Links incidents to exact ArgoCD sync/deployment commit (2-hour lookback window).
- **pgvector semantic search** — Past investigations stored as 768-dim nomic-embed-text embeddings for similarity-based RCA context.

### Security

- JWT Bearer-only (no URL token parameters)
- OIDC group-based RBAC (admin / operator / viewer)
- WebSocket `CheckOrigin` validates against `ALLOWED_ORIGINS` allowlist
- Redis-backed rate limiting with burst protection (Lua scripts)
- CORS strict allowlist — fatal startup error if unset in production
- Security headers: `CSP`, `HSTS`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`
- Prompt injection prevention — evidence descriptions sanitized before LLM
- `InternalServiceAuth` — service-to-service token, fatal in production if unset
- `err.Error()` never returned in HTTP responses — server-side log only

---

## Quick Start

### Option 1 — Docker Compose (Local Dev)

```bash
git clone https://github.com/aiops-sre/aileron.git
cd aileron
cp platform/.env.example platform/.env
# Edit platform/.env — set OIDC_PROVIDER_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET
docker compose up
```

| Service | URL |
|---|---|
| Frontend | http://localhost:3000 |
| Backend API | http://localhost:8080 |
| OIE | http://localhost:8081 |
| Ollama | http://localhost:11434 |
| Neo4j Browser | http://localhost:7474 |

On first start, pull the LLM model:
```bash
docker exec aileron-ollama-1 ollama pull qwen2.5:3b
docker exec aileron-ollama-1 ollama pull nomic-embed-text
```

### Option 2 — Kubernetes (Helm)

```bash
helm upgrade --install aileron ./platform/helm \
  --namespace aileron --create-namespace \
  --set oidc.providerUrl=https://your-keycloak.example.com/realms/aileron \
  --set oidc.clientId=aileron \
  --set oidc.clientSecret=your-secret \
  --set ingress.host=aileron.example.com
```

### Connect Claude Desktop via MCP

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "aileron": {
      "url": "https://aileron.example.com/api/v1/mcp",
      "transport": "http",
      "headers": {
        "Authorization": "Bearer <your-jwt-token>"
      }
    }
  }
}
```

### Send a Test Alert

```bash
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

## Configuration

All configuration is via environment variables. Copy `platform/.env.example` to `platform/.env` to get started.

### Required

| Variable | Description |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN: `postgres://user:pass@host:5432/db?sslmode=disable` |
| `KAFKA_BROKERS` | Comma-separated Kafka brokers: `kafka:9092` |
| `REDIS_ADDR` | Redis address: `redis:6379` |
| `NEO4J_URI` | Neo4j bolt URI: `bolt://neo4j:7687` |
| `OIDC_PROVIDER_URL` | OIDC discovery URL: `https://keycloak.example.com/realms/aileron` |
| `OIDC_CLIENT_ID` | OAuth2 client ID |
| `OIDC_CLIENT_SECRET` | OAuth2 client secret |
| `JWT_SECRET` | HMAC secret, min 32 chars (generate: `openssl rand -hex 32`) |
| `JWT_REFRESH_SECRET` | HMAC refresh secret, min 32 chars |
| `INTERNAL_SERVICE_TOKEN` | Service-to-service auth token (fatal if unset in production) |

### Optional

| Variable | Default | Description |
|---|---|---|
| `OLLAMA_URL` | `http://ollama:11434` | Ollama LLM endpoint |
| `LLM_MODEL` | `qwen2.5:3b` | Default Ollama model |
| `OIDC_ADMIN_GROUPS` | — | Comma-separated admin group names |
| `OIDC_OPERATOR_GROUPS` | — | Comma-separated operator group names |
| `OIDC_VIEWER_GROUPS` | — | Comma-separated viewer group names |
| `OIDC_DEFAULT_ROLE` | `viewer` | Default role for new users |
| `ALLOWED_ORIGINS` | — | CORS allowlist (required in production) |
| `CLAUDE_API_KEY` | — | Enable Claude API as LLM fallback |
| `INTELLIGENCE_SLACK_WEBHOOK` | — | Slack webhook for gate hook notifications |
| `KUBESENSE_API_URL` | `http://kubesense-api:8080` | KubeSense API proxy target |

### OIDC Provider Examples

<details>
<summary><b>Keycloak</b></summary>

```
OIDC_PROVIDER_URL=https://keycloak.example.com/realms/aileron
OIDC_CLIENT_ID=aileron
OIDC_CLIENT_SECRET=your-secret
OIDC_REDIRECT_URI=https://aileron.example.com/api/v1/auth/oidc/callback
OIDC_ADMIN_GROUPS=aileron-admins
```
Create realm → client (confidential) → add `groups` mapper to include group membership in token.
</details>

<details>
<summary><b>GitHub OAuth2</b></summary>

```
OIDC_PROVIDER_URL=https://github.com
OIDC_CLIENT_ID=your-github-app-client-id
OIDC_CLIENT_SECRET=your-github-app-secret
OIDC_REDIRECT_URI=https://aileron.example.com/api/v1/auth/oidc/callback
```
Create OAuth App at github.com/settings/applications/new.
</details>

<details>
<summary><b>Google</b></summary>

```
OIDC_PROVIDER_URL=https://accounts.google.com
OIDC_CLIENT_ID=your-client-id.apps.googleusercontent.com
OIDC_CLIENT_SECRET=your-secret
OIDC_REDIRECT_URI=https://aileron.example.com/api/v1/auth/oidc/callback
```
Create OAuth client at console.cloud.google.com → APIs & Services → Credentials.
</details>

---

## Services

### AlertHub Platform (`aileron` namespace)

| Service | Port | Replicas | Description |
|---|---|---|---|
| `aileron-platform` | 8080 | 2 | Go 1.24 — alert pipeline, CACIE, incident CRUD, OIDC auth, MCP server |
| `aileron-frontend` | 80 | 2 | React 18 + TypeScript — AIOps dashboard, 30+ pages |
| `aileron-oie` | 8081 | 1 | Go OIE — 16-fetcher evidence DAG, hypothesis engine, LLM narrator |
| `bert-service` | 8766 | 1 | Python BERT embeddings (all-MiniLM-L6-v2, 384-dim) |
| `ollama` | 11434 | 1 | Local LLM — qwen2.5:3b + nomic-embed-text |
| `neo4j` | 7474/7687 | 1 | Topology graph (K8s + infra) |
| `postgres` | 5432 | 1 | Primary store + pgvector (768-dim embeddings) |
| `redis` | 6379 | 3 | Pipeline state, topology cache, rate limiting |
| `kafka` | 9092 | 3 | Alert event bus |

### KubeSense Agent (`aileron-agent` namespace)

| Service | Description |
|---|---|
| `aileron-agent` | In-cluster K8s watcher — chaos scorer, config scanner (5-min interval) |
| `aileron-collector` | Kafka consumer → PostgreSQL `kubesense_health_events` + `kubesense_changes` + Neo4j |
| `aileron-core` | Cluster registry + topology REST API |
| `aileron-api` | 15 intelligence endpoints + correlation engine + Davis AI algorithms |
| `aileron-llm` | Incident narrator — Claude API → Ollama → deterministic fallback |

---

## API Reference

### Webhooks (no auth required)

| Method | Path | Source |
|---|---|---|
| `POST` | `/api/v1/webhooks/event-driven/dynatrace` | Dynatrace |
| `POST` | `/api/v1/webhooks/event-driven/prometheus` | Prometheus Alertmanager |
| `POST` | `/api/v1/webhooks/event-driven/grafana` | Grafana |
| `POST` | `/api/v1/webhooks/event-driven/splunk` | Splunk |
| `POST` | `/api/v1/webhooks/event-driven/generic` | Generic JSON |

### Core (JWT required)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/incidents` | List incidents |
| `GET` | `/api/v1/incidents/:id` | Incident + RCA result |
| `GET` | `/api/v1/rca/investigations` | RCA investigations |
| `GET` | `/api/v1/topology/resolve` | Entity resolution via Neo4j+Redis |
| `GET` | `/api/v1/topology/blast-radius` | Blast radius traversal |
| `GET` | `/api/v1/intelligence/stats` | Platform-wide stats |
| `POST` | `/api/v1/mcp` | MCP JSON-RPC endpoint |
| `GET` | `/api/v1/kubesense/correlation/incidents` | Active situations |
| `GET` | `/api/v1/kubesense/db/health` | K8s health events |
| `POST` | `/api/v1/kubesense/risk/score` | Pre-deploy risk |

Full API reference: [`docs/api.md`](docs/api.md)

---

## Project Structure

```
aileron/
├── platform/                    # AlertHub — alert correlation + incident management
│   ├── cmd/main.go              # Entry point — router + service wiring
│   ├── internal/
│   │   ├── api/                 # HTTP handlers + middleware
│   │   ├── auth/oidc/           # Generic OIDC authentication
│   │   ├── services/
│   │   │   ├── pipeline/        # Kafka consumer + 3-stage alert pipeline
│   │   │   ├── correlation/     # CACIE 4-strategy engine
│   │   │   ├── topology/        # Neo4j + Redis topology service
│   │   │   └── incidents/       # Incident lifecycle management
│   │   └── db/                  # PostgreSQL migrations + query layer
│   ├── services/oie/            # OIE — 16-fetcher evidence DAG
│   ├── frontend/                # React 18 + TypeScript dashboard
│   └── helm/                    # Helm chart
│
├── agent/                       # KubeSense — K8s causal intelligence
│   ├── services/
│   │   ├── agent/               # In-cluster K8s watcher
│   │   ├── collector/           # Kafka → PostgreSQL + Neo4j
│   │   ├── core/                # Cluster registry + topology API
│   │   └── llm/                 # Incident narrator
│   └── helm/                    # Helm chart
│
├── docker-compose.yml           # Full local dev stack
├── Makefile                     # dev / build / push / helm-install
├── .github/workflows/ci.yml     # GitHub Actions CI/CD
├── LICENSE                      # Apache 2.0
└── README.md
```

---

## Cloud Platform Integrations

Aileron natively ingests alerts and topology from every major cloud platform. Enable providers via environment variables — no code changes needed.

### Supported Alert Sources

| Platform | Webhook Endpoint | Events |
|---|---|---|
| **AWS CloudWatch** | `POST /api/v1/webhooks/cloud/aws/cloudwatch` | Metric alarms, composite alarms |
| **AWS GuardDuty** | `POST /api/v1/webhooks/cloud/aws/guardduty` | Threat detections, findings |
| **AWS EventBridge** | `POST /api/v1/webhooks/cloud/aws` | Any EventBridge event rule |
| **GCP Cloud Monitoring** | `POST /api/v1/webhooks/cloud/gcp/monitoring` | Metric / log-based alerts |
| **GCP Security Command Center** | `POST /api/v1/webhooks/cloud/gcp/scc` | Security findings |
| **Azure Monitor** | `POST /api/v1/webhooks/cloud/azure/monitor` | Metric + log alerts |
| **Microsoft Sentinel** | `POST /api/v1/webhooks/cloud/azure/sentinel` | Security incidents |
| **Azure Service Health** | `POST /api/v1/webhooks/cloud/azure` | Platform health events |
| **Alibaba Cloud CMS** | `POST /api/v1/webhooks/cloud/alicloud` | ECS, RDS, SLB alarms |
| **OpsGenie** | `POST /api/v1/webhooks/cloud/opsgenie` | On-call alerts |

### Topology Discovery

When enabled, Aileron discovers your cloud infrastructure and builds a topology graph in Neo4j. This powers accurate RCA (blast radius, dependency chains, change attribution):

| Provider | Resources Discovered | Enable Var |
|---|---|---|
| **AWS** | EC2, EKS, RDS, Lambda, ELB, VPC, S3 | `AILERON_AWS_ENABLED=true` |
| **GCP** | GCE, GKE, Cloud SQL, GCS, Cloud Functions, Pub/Sub | `AILERON_GCP_ENABLED=true` |
| **Azure** | VMs, VMSS, AKS, SQL, Functions, Storage, Cosmos DB | `AILERON_AZURE_ENABLED=true` |
| **Alibaba Cloud** | ECS, ACK, RDS, OSS, SLB, Function Compute | `AILERON_ALICLOUD_ENABLED=true` |
| **Kubernetes** | All clusters (EKS/GKE/AKS/ACK/on-prem) | Always on |
| **OpenStack/CloudStack** | VMs, hypervisors, volumes | Via CloudStack API |

### Kubernetes Deployment

Deploy Aileron on any managed Kubernetes service with provider-specific values:

```bash
# AWS EKS
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-aws-eks.yaml \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::ACCOUNT:role/aileron

# Google GKE
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-gcp-gke.yaml \
  --set serviceAccount.annotations."iam\.gke\.io/gcp-service-account"=aileron@PROJECT.iam.gserviceaccount.com

# Azure AKS
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-azure-aks.yaml \
  --set serviceAccount.annotations."azure\.workload\.identity/client-id"=CLIENT_ID
```

### Cloud Credential Setup

<details>
<summary><b>AWS — IAM Role (recommended for EKS)</b></summary>

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "cloudwatch:DescribeAlarms",
      "ec2:DescribeInstances", "ec2:DescribeVpcs",
      "eks:ListClusters", "eks:DescribeCluster",
      "rds:DescribeDBInstances",
      "lambda:ListFunctions",
      "elasticloadbalancing:DescribeLoadBalancers",
      "guardduty:ListFindings", "guardduty:GetFindings"
    ],
    "Resource": "*"
  }]
}
```

Set `AILERON_AWS_ENABLED=true` and `AWS_DEFAULT_REGION`.
</details>

<details>
<summary><b>GCP — Service Account (Workload Identity recommended for GKE)</b></summary>

Required roles: `roles/monitoring.viewer`, `roles/compute.viewer`, `roles/container.viewer`, `roles/cloudsql.viewer`

```bash
gcloud iam service-accounts create aileron-sa
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:aileron-sa@PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/monitoring.viewer"
# ... repeat for other roles
```

Set `AILERON_GCP_ENABLED=true` and `GCP_PROJECT_IDS`.
</details>

<details>
<summary><b>Azure — Workload Identity (recommended for AKS)</b></summary>

Required roles: `Monitoring Reader`, `Reader` on subscriptions

```bash
az ad sp create-for-rbac --name aileron --role "Monitoring Reader" \
  --scopes /subscriptions/SUBSCRIPTION_ID
```

Set `AILERON_AZURE_ENABLED=true`, `AZURE_SUBSCRIPTION_IDS`, `AZURE_TENANT_ID`.
</details>

<details>
<summary><b>Alibaba Cloud — RAM User</b></summary>

Required permissions: `AliyunCloudMonitorReadOnlyAccess`, `AliyunECSReadOnlyAccess`

Set `AILERON_ALICLOUD_ENABLED=true`, `ALICLOUD_ACCESS_KEY_ID`, `ALICLOUD_ACCESS_KEY_SECRET`.
</details>

---

## CNCF Ecosystem — End-to-End Integration

Aileron is designed to be a first-class citizen of the CNCF landscape. Every component has a CNCF-native alternative and all integrations are wired end-to-end, not just documented.

### Identity & Authentication — Dex and Friends

Aileron uses standard OIDC/OAuth2. **No code changes** are needed to switch providers — set three env vars.

```bash
# Works with ANY of these CNCF / open-source identity providers:
OIDC_PROVIDER_URL=https://dex.example.com          # Dex (CNCF)
OIDC_PROVIDER_URL=https://pinniped.example.com      # Pinniped (CNCF)
OIDC_PROVIDER_URL=https://your-org.zitadel.cloud    # Zitadel (CNCF Sandbox)
OIDC_PROVIDER_URL=https://keycloak.example.com/realms/aileron
OIDC_PROVIDER_URL=https://accounts.google.com
OIDC_PROVIDER_URL=https://github.com
OIDC_PROVIDER_URL=https://login.microsoftonline.com/<tenant>/v2.0
```

#### Deploy Dex alongside Aileron (recommended for self-hosted)

Dex runs **in-cluster** and federates to your existing directory (LDAP, GitHub, Google, SAML):

```bash
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-dex.yaml \
  --set dex.enabled=true \
  --set dex.issuerURL=https://dex.aileron.example.com \
  --set dex.client.secret=$(openssl rand -hex 16) \
  --set dex.connectors.github.enabled=true \
  --set dex.connectors.github.clientID=<your-github-app-client-id> \
  --set dex.connectors.github.clientSecret=<your-github-app-client-secret>
```

Aileron automatically picks up `OIDC_PROVIDER_URL=https://dex.aileron.example.com` and configures the auth middleware. Groups from Dex map to Aileron roles:

| Dex Group | Aileron Role |
|---|---|
| `aileron-admins` | Admin — full platform control |
| `aileron-operators` | Operator — create rules, approve remediations |
| `aileron-viewers` | Viewer — read-only dashboard |

Configure via `OIDC_ADMIN_GROUPS`, `OIDC_OPERATOR_GROUPS`, `OIDC_VIEWER_GROUPS`.

#### Supported Identity Providers

| Provider | Type | Connector |
|---|---|---|
| **Dex** | CNCF project | In-cluster, ships in Helm chart |
| **Pinniped** | CNCF project | Standard OIDC |
| **Zitadel** | CNCF Sandbox | Standard OIDC |
| **Keycloak** | Open Source | Standard OIDC |
| **Authelia** | Open Source | Standard OIDC |
| **Casdoor** | CNCF Sandbox | Standard OIDC |
| **GitHub** | Cloud | OAuth2 |
| **Google Workspace** | Cloud | Standard OIDC |
| **Microsoft Entra ID** | Cloud | Standard OIDC |
| **Okta** | SaaS | Standard OIDC |
| **Auth0** | SaaS | Standard OIDC |

---

### Secrets Management — External Secrets Operator

Never store secrets in Git. Aileron's Helm chart ships `ExternalSecret` CRDs that sync from any secrets backend:

```bash
# Enable in Helm values
helm upgrade --install aileron ./platform/helm \
  --set externalSecrets.enabled=true \
  --set externalSecrets.vaultAddr=https://vault.example.com \
  --set externalSecrets.secretStore=aileron-vault
```

Supported backends (via [External Secrets Operator](https://external-secrets.io)):

| Backend | Helm Value |
|---|---|
| HashiCorp Vault | `externalSecrets.vaultAddr` |
| AWS Secrets Manager | `externalSecrets.awsRegion` |
| GCP Secret Manager | `externalSecrets.gcpProjectId` |
| Azure Key Vault | `externalSecrets.azureVaultUrl` |
| 1Password, Doppler, ... | configure SecretStore manually |

---

### TLS — cert-manager

Auto-provision and rotate TLS certificates via Let's Encrypt or your internal CA:

```bash
helm upgrade --install aileron ./platform/helm \
  --set certManager.enabled=true \
  --set certManager.clusterIssuer=letsencrypt-prod \
  --set certManager.email=admin@example.com
```

The Helm chart automatically creates a `Certificate` resource and annotates the Ingress. cert-manager handles renewal 15 days before expiry.

---

### GitOps — Flux CD

Deploy and manage Aileron entirely via Flux CD:

```bash
# Bootstrap Flux
flux bootstrap github \
  --owner=your-org \
  --repository=your-gitops-repo \
  --path=clusters/production

# Apply Aileron HelmRelease
kubectl apply -f deploy/flux/aileron-helmrelease.yaml
```

Flux watches the Aileron Git repository and automatically syncs:
- **HelmRelease** — deploys the Aileron Helm chart, rolls back on failure
- **ImageUpdateAutomation** — auto-commits new `ghcr.io/aiops-sre` image tags
- **Kustomization** — applies RBAC, NetworkPolicy, and cluster-level config

Aileron also **receives** Flux events — Flux reconciliation failures create incidents:
```yaml
# In your Flux Notification Controller config:
apiVersion: notification.toolkit.fluxcd.io/v1
kind: Alert
spec:
  eventSources:
  - kind: HelmRelease
    name: "*"
  provider:
    name: aileron-webhook  # points to /api/v1/webhooks/cncf/flux
```

---

### Alert Sources — CNCF Security & Platform Tools

Register Aileron as a webhook sink in any CNCF tool:

| Tool | Webhook URL | What Aileron Receives |
|---|---|---|
| **Falco** | `POST /api/v1/webhooks/cncf/falco` | Runtime security rules (syscall anomalies, container escapes) |
| **Kyverno** | `POST /api/v1/webhooks/cncf/kyverno` | Policy admission violations |
| **OPA/Gatekeeper** | `POST /api/v1/webhooks/cncf/opa` | Constraint audit violations |
| **Flux CD** | `POST /api/v1/webhooks/cncf/flux` | Reconciliation failures, GitOps drift |
| **Keptn** | `POST /api/v1/webhooks/cncf/keptn` | Quality gate failures, SLO breaches |
| **CloudEvents** | `POST /api/v1/webhooks/cncf/cloudevents` | Any CloudEvents v1.0 envelope |
| **NATS** | `POST /api/v1/webhooks/cncf/nats` | NATS JetStream messages via bridge |
| **Thanos Ruler** | `POST /api/v1/webhooks/prometheus` | Long-range metric alert rules |
| **VictoriaMetrics** | `POST /api/v1/webhooks/prometheus` | VMAlert rules (Prometheus-compatible) |
| **Grafana Loki** | `POST /api/v1/webhooks/cncf/loki` | Log-based alerting rules |

**Example: Configure Falco to push to Aileron**
```yaml
# /etc/falco/falco.yaml
json_output: true
http_output:
  enabled: true
  url: https://aileron.example.com/api/v1/webhooks/cncf/falco
  user_agent: falcosecurity/falco
```

**Example: Kyverno policy alert**
```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: require-resource-limits
  annotations:
    policies.kyverno.io/description: "Containers must have resource limits"
spec:
  validationFailureAction: Audit
  webhookTimeoutSeconds: 10
  # Kyverno sends violations to Aileron automatically
  # Set KYVERNO_VIOLATION_WEBHOOK=https://aileron.example.com/api/v1/webhooks/cncf/kyverno
```

---

### OpenTelemetry — OTLP Receiver

Aileron exposes a native OTLP endpoint. Route alerts from **any OTel-instrumented application** or **OpenTelemetry Collector** pipeline:

```yaml
# OpenTelemetry Collector config (deploy/otel/collector-config.yaml)
exporters:
  otlphttp/aileron:
    endpoint: https://aileron.example.com/api/v1/otlp
    headers:
      Authorization: "Bearer ${AILERON_SERVICE_TOKEN}"

service:
  pipelines:
    metrics/alerts:
      receivers: [prometheus, otlp]
      processors: [filter/severity_warning_plus]
      exporters: [otlphttp/aileron]
    logs/errors:
      receivers: [otlp, k8s_events]
      processors: [filter/warn_plus]
      exporters: [otlphttp/aileron]
```

In your application, set the `aileron.severity` attribute on metrics/logs to route them as alerts:
```go
// Go OpenTelemetry SDK example
meter.Int64Gauge("service.error.count",
  metric.WithDescription("Error count — high value triggers Aileron incident"),
).Record(ctx, errorCount,
  attribute.String("aileron.severity", "high"),
  attribute.String("aileron.status", "firing"),
)
```

**OTLP endpoints:**
- `POST /api/v1/otlp/v1/metrics` — ExportMetricsServiceRequest (JSON or Protobuf)
- `POST /api/v1/otlp/v1/logs` — ExportLogsServiceRequest (severity ≥ WARN forwarded)

---

### RCA Evidence — Observability Stack Integration

When OIE investigates an incident, it **automatically queries** your observability stack for corroborating evidence:

```bash
# Configure via env vars (all optional — OIE uses whichever are set)
LOKI_URL=http://loki.monitoring.svc.cluster.local:3100
JAEGER_URL=http://jaeger-query.monitoring.svc.cluster.local:16686
TEMPO_URL=http://tempo.monitoring.svc.cluster.local:3200
METRICS_STORE_URL=http://thanos-query.monitoring.svc.cluster.local:9090
# VictoriaMetrics: METRICS_STORE_URL=http://victoria-metrics:8428
# Cortex/Mimir:    METRICS_STORE_URL=http://cortex-query-frontend:9009
```

| Tool | Evidence Fetcher | What It Adds to RCA |
|---|---|---|
| **Grafana Loki** | `loki_logs` fetcher | Error/warning logs for affected pod (30 min window) |
| **Jaeger** | `distributed_traces` fetcher | Failed distributed traces for the service |
| **Grafana Tempo** | `distributed_traces` fetcher | Same — tries Tempo if Jaeger unavailable |
| **Thanos / VictoriaMetrics / Cortex** | `long_term_metrics` fetcher | CPU%, memory%, error rate at incident start |

---

### Kubernetes Deployment — Any Managed Cluster

```bash
# AWS EKS (IRSA service account, ALB ingress, gp3 storage)
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-aws-eks.yaml \
  --set dex.enabled=true \
  --set certManager.enabled=true

# Google GKE (Workload Identity, GCLB, pd-ssd)
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-gcp-gke.yaml

# Azure AKS (Azure Workload Identity, AAG ingress)
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-azure-aks.yaml

# Full CNCF stack (Dex + cert-manager + ExternalSecrets + Flux)
helm upgrade --install aileron ./platform/helm \
  -f platform/helm/alerthub/values-cncf-full.yaml
```

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, style, and PR guidelines.

```bash
git clone https://github.com/aiops-sre/aileron.git
cd aileron
cp platform/.env.example platform/.env
make dev          # start full stack
make test         # run all tests
```

**Good first issues** are tagged [`good first issue`](https://github.com/aiops-sre/aileron/issues?q=label%3A%22good+first+issue%22) on GitHub.

---

## Roadmap

- [ ] Helm chart on ArtifactHub
- [ ] OpenTelemetry traces through the pipeline
- [ ] Grafana dashboard bundle
- [ ] Slack / PagerDuty native integrations
- [ ] Multi-cluster support (single Aileron instance, multiple K8s clusters)
- [ ] Alert suppression ML model (replace rule-based with learned patterns)
- [ ] Operator UI for runbook authoring

---

## License

Apache 2.0 — see [LICENSE](LICENSE).

Copyright 2025 Aileron Platform Contributors.
