<p align="center">
  <img src="logo-full.svg" alt="Aileron — Event Intelligence Platform" width="320"/>
</p>

<p align="center">
  <img src="logo-icon.svg" alt="Aileron icon" width="64"/>&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
  <img src="logo-horizontal.svg" alt="Aileron horizontal" height="48"/>&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
  <img src="logo-mono.svg" alt="Aileron monochrome" height="48"/>
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
