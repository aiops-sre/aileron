# AlertHub Enterprise — Engineering Reference

## What Is AlertHub

AlertHub Enterprise is an AIOps platform for your organization's infrastructure. It ingests alerts from monitoring
systems (Dynatrace, Prometheus, Grafana, Splunk), normalises them into a unified model, correlates them
using a multi-strategy engine, automatically creates and evolves incidents, and runs AI-powered root
cause analysis (RCA) investigations.

**Target infrastructure:** 150+ bare-metal nodes across two Aileron data-centre regions — Reno (rno) and
Maiden (mdn). Workloads run on CloudStack VMs, Kubernetes clusters (`example-cluster`, `mps-tooling-mdn`),
and NetApp ONTAP storage.

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            ALERT SOURCES                                     │
│   Dynatrace  │  Prometheus  │  Grafana  │  Splunk  │  Generic Webhook        │
└──────────────┬──────────────────────────────────────────────────────────────┘
               │ HTTP Webhooks  +  Kafka topic: raw-alerts
               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                       NORMALIZATION LAYER                                    │
│  DynatraceNormalizer  │  PrometheusNormalizer  │  GrafanaNormalizer          │
│  SplunkNormalizer     │  GenericFallbackNormalizer                           │
│  ─────────────────────────────────────────────────────────────────────────  │
│  Output: Unified Alert { fingerprint · entity_id/type · labels · metadata } │
└──────────────┬──────────────────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    STAGED PIPELINE (StagedPipeline)                          │
│                                                                              │
│  Stage 1 — FAST PATH  (32 workers, cap 10 000)                               │
│    · Fingerprint dedup (30-min window) → DEDUPLICATE (count++)               │
│    · resolved alert → close + check incident auto-close                      │
│    · severity=critical → jump directly to Stage 2                            │
│                          │                                                   │
│  Stage 2 — TOPO PATH  (16 workers, cap 5 000)                                │
│    · RootCauseEngine (deterministic):                                        │
│      Dynatrace rootCauseEntity → topology graph → self-as-root               │
│      → ATTACH_TO_ROOT or CREATE_ROOT → exit ✓                                │
│      → NO_ROOT → fall through to Stage 3                                     │
│                          │                                                   │
│  Stage 3 — FULL PATH   (8 workers, cap 2 000)                                │
│    · 4-strategy parallel scoring (30 s timeout each)                         │
│    · CACIE Bayesian fusion                                                   │
│    · CorrelationAggregator → final MERGE / CREATE / MONITOR / DISCARD        │
└──────────────┬──────────────────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CORRELATION ENGINE                                      │
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │  PARALLEL STRATEGIES  (run concurrently, 30 s timeout each)           │  │
│  │  Topology 45%  │  Rules 25%  │  Semantic 20%  │  Temporal 10%         │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │  CACIE — Causal Inference Engine  (authoritative layer above all)     │  │
│  │  Bayesian log-odds fusion · infra-level priors (BM 1.50 → Pod 0.80)  │  │
│  │  Topology dominance: score ≥0.75 AND ≥1.4× next-best → override      │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │  AGGREGATOR — Final Decision                                          │  │
│  │  MERGE · CREATE · MONITOR · DISCARD · SUPPRESS                       │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└──────────────┬──────────────────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                     INCIDENT MANAGEMENT                                      │
│  IncidentService · EvolutionEngine (merge/evolve) · IdempotentCreate         │
│  IncidentManager (ticket creation) · Timeline events · RBAC-gated transitions      │
└──────────────┬──────────────────────────────────────────────────────────────┘
               │ RCA trigger (HTTP + Kafka)
               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│              RCA ORCHESTRATOR  —  Python FastAPI  (port 3000)                │
│                                                                              │
│  V2 (deterministic, default when RCA_V2=true)                                │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ 1.Ingest → 2.Domain Classify → 3.Topology Retrieval                 │    │
│  │ 4.DAG Execution → 5.Temporal Reconstruct → 6.Causal Graph           │    │
│  │ 7.Probabilistic Score → 8.LLM Narration → 9.Finalize               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  V1 (ReAct LLM agent, fallback)                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ context_gathering → hypothesis_formation → evidence_collection       │    │
│  │ → root_cause_analysis → remediation_planning                        │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  Tools: K8s · Dynatrace · CloudStack · Neo4j · Postgres · Temporal          │
│  LLMs:  Ollama (qwen2.5:3b) · OIDC Provider (Claude) · Endor (internal)  │
└──────────────┬──────────────────────────────────────────────────────────────┘
               │ WebSocket stream
               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                   FRONTEND  —  React + TypeScript + Vite                     │
│  Dashboard · Alerts · Incidents · RCA Investigation · AI Chat                │
│  Admin · Analytics · K8s Management · Topology · Workflows · On-Call        │
│  Design: Aileron HIG tokens (inline styles, CSS variables — no Tailwind)       │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Technology Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.21+ |
| Frontend | TypeScript · React 18 · Vite |
| HTTP framework | Gin (Go) · FastAPI (Python) |
| Primary database | PostgreSQL 15 |
| Cache / pub-sub | Redis 7 |
| Graph database | Neo4j 5 (APOC enabled) |
| Message queue | Kafka |
| Vector store | Weaviate (nomic-embed-text embeddings) |
| Local LLM | Ollama (qwen2.5:3b default) |
| Cloud LLM | OIDC Provider (Claude via Aileron proxy) · Endor (internal LLM gateway) |
| Embedding service | Local BERT — all-MiniLM-L6-v2 · 384-dim · port 8766 |
| Auth | Aileron MAS/OIDC OAuth2 · LDAP · JWT |
| Infrastructure | Kubernetes (example-cluster, mps-tooling-mdn) |
| VM platform | CloudStack (rno + mdn regions) |
| Storage | NetApp ONTAP |
| Monitoring sources | Dynatrace · Prometheus · Grafana · Splunk |
| CI/CD | Buildkit PreSync + ArgoCD (helm-revision CMP plugin) |

---

## Repository Structure

```
alerthub-enterprise/
├── cmd/
│   └── main.go                    # Entry point: all service wiring, HTTP router, server startup
├── internal/
│   ├── api/
│   │   ├── handlers/              # HTTP handlers — one file per domain
│   │   │   ├── alerts.go          # Alert CRUD, ack, resolve, analyze
│   │   │   ├── incidents.go       # Incident CRUD, ack, escalate, resolve, merge
│   │   │   ├── external_incidents.go   # HCL ServiceNow incident CRUD via IncidentManager
│   │   │   ├── webhooks.go        # Alert ingestion webhooks (Dynatrace, Prometheus, etc.)
│   │   │   ├── auth.go            # Login, logout, token refresh
│   │   │   ├── users.go           # User management (with real names + avatars)
│   │   │   ├── mas_handlers.go    # MAS OAuth initiate + callback
│   │   │   ├── analytics.go       # Insights, correlation stats, ontology distribution
│   │   │   ├── topology.go        # Topology nodes, graph, blast radius
│   │   │   ├── ai.go              # Summarize, explain, recommend, investigate
│   │   │   ├── workflows.go       # Workflow CRUD + manual execution
│   │   │   ├── integrations.go    # Integration registry
│   │   │   ├── notifications.go   # Channels, rules, send, history
│   │   │   ├── roles.go           # Role + permission management
│   │   │   ├── websocket.go       # WebSocket upgrade + multiplexing
│   │   │   └── health_handler.go  # /health, /ready, /metrics
│   │   └── middleware/
│   │       ├── auth.go            # JWT validation + MAS header extraction
│   │       ├── api_key.go         # API key authentication
│   │       ├── audit.go           # Async audit log to api_request_log
│   │       ├── cache.go           # Response caching
│   │       └── rate_limiting.go   # Sliding-window rate limiter (Redis)
│   ├── auth/
│   │   └── mas/
│   │       ├── mas_auth.go        # Header extraction, group→role mapping
│   │       └── user_provisioner.go# Auto-create/update users from MAS context
│   ├── cache/
│   │   └── redis.go               # Sessions, rate limits, alert cache, pub/sub
│   ├── db/
│   │   ├── migrations.go          # Embedded SQL schema (auto-runs on startup)
│   │   └── pool.go                # PostgreSQL pool (100 max open, 25 idle)
│   ├── security/
│   │   └── enhanced_security.go
│   ├── services/
│   │   ├── ai/                    # LLM summarisation + multi-turn investigation
│   │   ├── alerts/                # Alert CRUD + lifecycle + async AI analysis
│   │   ├── analytics/             # Metrics aggregation + strategy performance
│   │   ├── apikeys/               # API key management
│   │   ├── audit/                 # Audit log service
│   │   ├── config/                # Runtime config management
│   │   ├── correlation/           # 16-component correlation engine (see below)
│   │   ├── ldap/                # LDAP (Aileron Directory Service)
│   │   ├── oidc/             # OIDC Provider OAuth token helper
│   │   ├── incidents/             # Incident CRUD, evolution, IncidentManager, idempotent create
│   │   ├── integrations/          # Third-party integration registry
│   │   ├── jwt/                   # JWT generation + validation (15 min / 7 day TTL)
│   │   ├── maintenance/           # Maintenance window suppression
│   │   ├── normalization/         # Multi-source normalizers + fingerprinting
│   │   ├── notifications/         # Slack, PagerDuty, email, webhook delivery
│   │   ├── oauth/                 # OAuth 2.0 + enhanced multi-tenant OAuth
│   │   ├── pipeline/              # Alert pipeline (staged + Kafka consumer/producer)
│   │   ├── rbac/                  # Role-based access control
│   │   ├── sso/                   # SSO integration
│   │   ├── topology/              # Infrastructure discovery (K8s, CloudStack, NetApp)
│   │   └── workflows/             # Workflow engine + action executors
│   └── shared/
│       ├── config/config.go       # AlertHubConfig struct + validation
│       ├── container/container.go # Dependency injection container (thread-safe RWMutex)
│       ├── interfaces/services.go # All service interface contracts + response types
│       ├── models/alert.go        # Unified Alert struct (96 fields)
│       ├── models/extended_models.go  # Incident, Topology, User, Workflow structs
│       └── logging/ · monitoring/ · registry/ · resource/
├── services/
│   ├── rca-orchestrator/          # Python FastAPI RCA service
│   │   ├── agent/
│   │   │   ├── main.py            # FastAPI app, WebSocket, Kafka consumer, background learner
│   │   │   ├── orchestrator.py    # V1: ReAct LLM agent
│   │   │   ├── orchestrator_v2.py # V2: deterministic 9-phase pipeline
│   │   │   ├── llm_narrator.py    # Prose narration layer (V2, prose-only)
│   │   │   └── prompts.py         # System prompts, RCA extraction, forecast
│   │   ├── engine/
│   │   │   ├── causal_graph.py    # InfraNode graph, infra-level priors, path reconstruction
│   │   │   ├── domain_classifier.py  # Signal-based domain classification
│   │   │   ├── investigation_dag.py  # Domain-specific mandatory tool sequences
│   │   │   └── rca_scorer.py      # 6-source probabilistic scoring (softmax)
│   │   ├── learning/
│   │   │   ├── knowledge_store.py # Weaviate vector store (RCAIncident + SREKnowledge)
│   │   │   ├── topology_aware_retrieval.py  # Hybrid 70% topo + 30% semantic retrieval
│   │   │   └── continuous.py      # Weekly model trainer + Kafka incident ingester
│   │   ├── models/schemas.py      # Pydantic models (Investigation, RootCause, StreamEvent…)
│   │   └── tools/                 # K8s, Dynatrace, CloudStack, Neo4j v1/v2, Postgres, Temporal
│   └── local-bert/
│       └── bert_service.py        # Flask, all-MiniLM-L6-v2, /embed POST, port 8766
├── frontend/alerthub-frontend/
│   └── src/
│       ├── App.tsx                # Router, auth guard
│       ├── pages/                 # One file per route (see Frontend section)
│       ├── components/            # Shared UI — Layout, Header, AlertCard, etc.
│       ├── stores/alertsStore.ts  # Zustand store — alerts, filters, WebSocket
│       ├── services/              # IncidentManagerService, AIService, CorporateOAuthService, PagerDuty
│       ├── hooks/                 # useBreakpoint, useKeyboard, useSound, useTheme, useWebSocket
│       └── lib/
│           ├── api.ts             # Base HTTP client
│           ├── api-axios.ts       # Axios client with interceptors
│           ├── design-tokens.ts    # Design token object `c` (CSS variables)
│           └── design-system.ts
├── database/
│   ├── schema.sql                 # Full reference schema
│   └── migrations/                # Individual migration SQL files
├── proto/                         # gRPC .proto definitions
├── pkg/proto/                     # Generated gRPC Go stubs
├── k8s/                           # Legacy manifest (superseded by Helm CI/CD)
├── docker/                        # docker-compose.yml + Dockerfiles for local dev
├── scripts/                       # Test/simulation shell scripts
└── docs/                          # Architecture + operational documentation
```

---

## Alert Ingestion Flow (End-to-End)

```
External Alert Source
        │
        │  POST /api/v1/webhooks/{source}
        │  — or —
        │  Kafka topic: raw-alerts
        ▼
Normalization Registry
  ├─ Exact source name → specific normalizer
  ├─ Auto-detect via CanHandle() → specific normalizer
  └─ Fallback → GenericFallbackNormalizer
        │
        │  NormalizedAlert {
        │    source, source_id, title, description, severity,
        │    entity_id, entity_type, cluster, namespace,
        │    labels{}, fingerprint
        │  }
        ▼
AlertPipelineService.EnqueueAlert()
  └─ Non-blocking channel send (capacity 2 000)
        │
        ▼
StagedPipeline
  │
  ├─ Stage 1  FAST PATH  (32 workers)
  │    ├─ Same fingerprint in last 30 min?  ─→  DEDUPLICATE (count++)
  │    ├─ status = resolved?  ──────────────→  Close + check incident auto-close
  │    └─ severity = critical?  ────────────→  Skip to Stage 2 directly
  │
  ├─ Stage 2  TOPO PATH  (16 workers)
  │    ├─ RootCauseEngine
  │    │   ├─ Dynatrace rootCauseEntity label present?  →  ATTACH_TO_ROOT ✓
  │    │   ├─ Found in topology graph with high confidence?  →  CREATE_ROOT or ATTACH ✓
  │    │   └─ NO_ROOT  →  pass to Stage 3
  │    └─ Infra hierarchy: BM (5) > KVM (4) > VM (3) > K8s Node (2) > Pod (1)
  │
  └─ Stage 3  FULL PATH  (8 workers)
       ├─ 4 goroutines (parallel, 30 s timeout each):
       │    · SemanticCorrelation   — BERT cosine similarity  (→ port 8766)
       │    · TemporalCorrelation   — Exponential time decay
       │    · TopologyCorrelation   — Redis infrastructure graph traversal
       │    · RulesCorrelation      — DB-loaded rule patterns
       │
       ├─ CACIE (Causal Inference Engine) fusion:
       │    · Bayesian log-odds across all evidence
       │    · Infra-level priors:  BM(1.50) KVM(1.30) VM(1.15) Node(1.05) Pod(0.80)
       │    · Topology dominance check: score ≥0.75 AND ≥1.4× next-best → override
       │
       └─ CorrelationAggregator:
            · Topology ≥0.75 AND dominant → MERGE  (topology wins directly)
            · Final score ≥0.60            → MERGE  into existing incident
            · Final score ≥0.40            → CREATE new incident
            · Final score ≥0.20            → MONITOR (buffer, wait for more)
            · Final score <0.20            → DISCARD
```

---

## Correlation Engine — All 16 Components

### Layer 1 — Deterministic (runs first, can short-circuit)

| Component | File | What It Does |
|-----------|------|-------------|
| RootCauseEngine | root_cause_engine.go | Dynatrace rootCauseEntity → topology graph → self. Returns ATTACH / CREATE / NO_ROOT |
| AlertStateMachine | alert_state_machine.go | Guarantees every alert reaches an outcome. Redis-backed, 2 h TTL. States: NEW → DEDUPED / BUFFERED / ATTACHED / SUPPRESSED / INCIDENT_CREATED |
| InfraPropagation | infra_propagation.go | Hard-coded propagation chains: Storage (NetApp→PV→PVC→Pod), Compute (BM→KVM→VM→Node→Pod), Network, ControlPlane. ChainStep has MaxLagSeconds + BaseScore |

### Layer 2 — Probabilistic Scoring (parallel, 30 s timeout per strategy)

| Component | Weight | File | What It Does |
|-----------|--------|------|-------------|
| TopologyGraphCorrelator | 45% | topology_graph_correlator.go | Redis infrastructure graph. BM→VM→K8s hierarchy. Blast radius + proximity. Score ≥0.60 → wins |
| RulesCorrelation | 25% | correlation.go | DB-loaded correlation rules with JSONB conditions/actions |
| SemanticCorrelationEngine | 20% | semantic_correlation.go | BERT all-MiniLM-L6-v2 cosine similarity. LRU cache (10 k entries). Jaccard keyword fallback |
| TemporalCorrelation | 10% | parallel_correlation_engine.go | Exponential time decay — same-cluster alerts within window |

### Layer 3 — Fusion & Decision

| Component | File | What It Does |
|-----------|------|-------------|
| CACIE | causal_inference_engine.go | Bayesian log-odds fusion. Infra-level priors. Topology dominance arbitration. Persists to `incident_causal_graphs` |
| ProbabilisticRCA | probabilistic_rca.go | Competing hypotheses, normalised posterior. Evidence trust: DT(0.95) Topo(0.85) Rules(0.80) Sem(0.65) Temp(0.55) Feedback(1.0). Redis 2 h TTL |
| CorrelationAggregator | correlation_aggregator.go | Converts scores → MERGE / CREATE / MONITOR / DISCARD / SUPPRESS. 70% strategy + 30% text overlap for candidate ranking |
| OntologyEngine | ontology.go | Classifies failure domain (Storage / Network / Compute / K8s / App / DB / Security). 20+ canonical failure classes. DomainPropagationMatrix |
| AdaptiveLearning | adaptive_learning.go | EMA weight tuning per (domain, source, cluster). alpha=0.08, weights clamped [0.05, 0.55]. Disabled for go-live stability |
| FeedbackLoop | feedback_loop.go | Records operator verdicts. Recalibrates every 10 entries. Damped: 70% new signal + 30% prior |
| VectorRepository | vector_repository.go | pgvector ANN (768-dim). 24 h lookback, cosine distance ordering |
| ExplainabilityReport | explainability.go | Structured reasoning chain per correlation decision → stored in `pipeline_correlation_results` |
| InvestigationDAGEngine | investigation_dag_engine.go | Domain-specific investigation playbooks (Storage/Compute/Network/K8s/DB/App) for operator runbooks |

---

## RCA Orchestrator — V2 Deterministic Flow

```
1. INGEST           ← Go context: root_entity_label, blast_radius_size, hypotheses, causal_chain
         │
2. DOMAIN CLASSIFY  ← Signal matching → K8s / Storage / Network / Compute / Database / Virtualization
         │           Priority: Go engine (≥0.6 conf) → signal match → fallback KUBERNETES
         │
3. TOPO RETRIEVAL   ← Hybrid: 70% topology signature + 30% semantic (Weaviate)
         │           TopologySignature: root_entity_type, domain, infra_levels, blast_radius_bucket, causal_depth
         │
4. DAG EXECUTION    ← Domain-specific mandatory tool sequence (parallel, 30 s timeout)
         │           K8s:      pod_status → deployment → k8s_events → pvc → node → recent_changes
         │           Storage:  pvc → k8s_events → topology_recursive → blast_radius_deep
         │           Compute:  vm_status → host_status → k8s_events → node
         │           Network:  endpoints → dns → k8s_events → ingress
         │
5. TEMPORAL         ← Reconstruct alert sequence: seconds_after_first per entity, propagation direction
         │
6. CAUSAL GRAPH     ← Build InfraNode graph. Score = infra_prior(0.4) + blast(0.35) + path(0.15) + temporal(0.20)
         │           Infra level priors: PHYSICAL(0.90) HYPERVISOR(0.85) OS(0.80) … APPLICATION(0.30)
         │
7. PROBABILISTIC    ← 6-source softmax fusion:
         │           go_engine(0.40) + graph(0.25) + blast(0.15) + temporal(0.10) + domain(0.05) + historical(0.05)
         │
8. LLM NARRATION    ← prose-only (qwen2.5:3b / OIDC Provider Claude). Cannot override deterministic scores.
         │           Output: summary, causal_explanation, remediation_steps (3–5 concrete), confidence_assessment
         │
9. FINALIZE         ← Persist to rca_investigations · stream via WebSocket to frontend
```

### V1 (ReAct LLM Agent — fallback when RCA_V2 is not set)

- Multi-turn tool calling, max 3 rounds
- Phases: context_gathering → hypothesis_formation → evidence_collection → root_cause_analysis → remediation_planning
- RAG: Weaviate similar-incident search (top 3 injected into system prompt)
- LLM options: Ollama (local) · OIDC Provider (Claude, Aileron proxy) · Endor (internal gateway, TOTP auth)

### RCA Tool Suite

| Tool | Purpose |
|------|---------|
| GetPodStatusTool | Pod phase, container states, restart counts |
| GetK8sEventsTool | Namespace events (50 max) sorted by timestamp |
| GetPodLogsTool | Tail logs (default 100 lines, 8000 char limit) |
| DescribePodTool | Full pod diagnostic (exit codes, probes, volumes, events) |
| GetDeploymentStatusTool | Replica counts and conditions |
| GetNodeStatusTool | Resource pressure, taints, allocatable capacity |
| GetPVCStatusTool | PVC phase, storage class, capacity |
| GetDynatraceProblems | Active/closed problems + impacted entities |
| GetDynatraceMetrics | Metric selector queries (error rates, latency, etc.) |
| GetDynatraceEvents | Deployments, config changes, anomalies |
| GetCloudStackVMsTool | VM state, zone, CPU/memory per region |
| GetCloudStackHostsTool | Hypervisor status, allocated resources |
| GetTopologyRecursiveTool | APOC spanningTree upstream traversal (DEPENDS_ON, CALLS, RUNS_ON…) |
| GetBlastRadiusDeepTool | Downstream blast radius (0–4 hops), propagation_score = 0.7^hops |
| GetHistoricalAlertsTool | Service alert history (default 72 h) |
| GetAlertFrequencyTool | Time-bucket COUNT aggregation |
| QueryTemporalPropagation | Alert sequence reconstruction within blast radius |

---

## Topology Service — Infrastructure Discovery

```
Infrastructure Sources         Graph Representation
──────────────────────────     ──────────────────────────────────────────────
CloudStack API (rno + mdn)     bare_metal (BM)
  └─ Hypervisors                  └─[hosts]──────→ cloudstack_vm
  └─ VMs (state, CPU/RAM)                              └─[runs_on]──→ k8s_cluster
                                                                        └─[k8s_on]──→ k8s_node
K8s API (per cluster)                                                                  └─[pod_on]──→ pod
  └─ Nodes (health, taints)
  └─ Pods (phase, restarts)   Neo4j (secondary, complex traversal):
  └─ Deployments (replicas)     DEPENDS_ON · CALLS · USES · RUNS_ON · HOSTED_ON
                                APOC spanningTree for upstream walks
NetApp ONTAP REST API           apoc.path for blast radius queries
  └─ Clusters → SVMs
  └─ Aggregates → Volumes     Redis (primary, fast correlation lookup):
                                TopologyGraph: in-memory adjacency list
                                Refreshed every 10 minutes
```

Supported discovery files:
- `k8s_topology_service.go` — K8s nodes, pods, deployments
- `cloudstack_api.go` — HMAC-SHA1 signed CloudStack API (rno + mdn)
- `netapp_topology_service.go` — NetApp ONTAP REST API
- `live_topology.go` — Neo4j write path
- `enterprise_topology.go` — Orchestrates all three, refresh goroutine (5 min interval)

---

## Authentication & Authorization

```
Browser Request
      │
      ├── MAS Ingress (Aileron infrastructure) injects:
      │     X-Forwarded-User:   username
      │     X-Forwarded-Mail:   email
      │     X-Forwarded-Groups: group1,group2,…
      │
      ▼
MAS Middleware (mas_auth.go)
  └─ Maps AD groups → AlertHub roles:
       interactive-apps-systems  →  admin
       IASYS-SRE / Interactive-SRE  →  sre
       interactive-monitoring    →  operator
       interactive-all           →  viewer
      │
      ▼
UserProvisioner (user_provisioner.go)
  Creates or updates user record. Role resolution priority:
    1. DB LDAP group mappings          ← highest
    2. ALERTHUB_ADMIN_GROUPS env
    3. ALERTHUB_OPERATOR_GROUPS env
    4. OIDC_VIEWER_GROUPS env
    5. Existing DB role (never downgrade)
    6. First-user bootstrap (auto-admin when no admins exist)
    7. config.DefaultRole              ← lowest
      │
      ▼
JWT Token Issued
  Claims: user_id, email, roles, permissions, cluster_scopes
  Access token TTL:  15 min
  Refresh token TTL: 7 days
      │
      ▼
Subsequent Requests → auth.go Authenticate()
  └─ RequirePermission()   — JWT claim check (cached, fast)
  └─ RequirePermissionDB() — live DB query (secure, slower)
  └─ RequireAdmin()        — admin role gate
```

**RBAC Roles & Permissions:**

| Role | Key Permissions |
|------|----------------|
| admin | Full access — manage users, roles, integrations, system config |
| sre | Incidents, correlations, workflows, on-call, topology |
| operator | Create / ack / resolve alerts and incidents |
| viewer | Read-only across all resources |

---

## Frontend

**Stack:** React 18 · TypeScript · Vite · Zustand (state) · Axios (HTTP)
**Design System:** Aileron HIG tokens in `src/lib/design-tokens.ts` as `c` object (inline styles, CSS variables — no Tailwind, no CSS modules)

### Routes

| Path | Page | Description |
|------|------|-------------|
| `/` | DashboardPage | Real-time overview, active alert/incident counts |
| `/alerts` | AlertsPage | Alert list, filtering, bulk ack/resolve |
| `/incidents` | IncidentsPage | Incident list + detail panel + timeline |
| `/rca` | RCAInvestigationPage | Live RCA investigation stream (WebSocket) |
| `/ai-chat` | AIChatPage | Multi-turn AI investigation chat |
| `/aiops` | AIOpsPage | Autonomous AIOps command centre |
| `/analytics` | AnalyticsPage | Correlation strategy performance, trend charts |
| `/admin` | AdminPage | 9-section admin (users, roles, integrations, K8s clusters, workflows, system config, dedup rules, notification channels, alert sources) |
| `/settings` | SettingsPage | User preferences, API keys, notification settings |
| `/k8s` | KubernetesManagementPage | K8s cluster management + live pod view |
| `/topology` | IntelligentInfraTopology | Visual infrastructure topology graph |
| `/oncall` | OnCallSchedule | On-call schedule management |
| `/workflows` | WorkflowBuilderPage | Workflow builder / editor |
| `/observability` | ObservabilityPage | SLO / SLA metrics |
| `/integrations` | IntegrationHealthPage | Integration health status |
| `/host-vm-mapping` | HostVMMapping | BM → VM mapping viewer |
| `/oauth/callback` | OAuthCallbackPage | OAuth2 callback handler |
| `/login` | ManualLoginPage | Username/password fallback login |
| `/oidc-test` | OIDC ProviderTestPage | OIDC Provider LLM test page |

### State

- `alertsStore.ts` — alert list, active filters, WebSocket connection, real-time updates, correlation results

### Key Service Wrappers

| File | Purpose |
|------|---------|
| `src/lib/api.ts` | Base HTTP client (fetch) |
| `src/lib/api-axios.ts` | Axios client with auth interceptors |
| `services/IncidentManagerService.ts` | HCL / IncidentManager incident API calls |
| `services/AIService.ts` | AI summarise / investigate calls |
| `services/CorporateOAuthService.ts` | MAS / OIDC OAuth flow |
| `services/PagerDutyService.ts` | PagerDuty integration |

---

## API Reference

### Alerts
```
POST   /api/v1/alerts                    Ingest alert
GET    /api/v1/alerts                    List (filters: status, severity, source, assigned_to)
GET    /api/v1/alerts/:id                Get alert
PUT    /api/v1/alerts/:id/acknowledge    Acknowledge
PUT    /api/v1/alerts/:id/resolve        Resolve
POST   /api/v1/alerts/:id/analyze        Trigger async AI analysis
POST   /api/v1/alerts/:id/correlate      Run correlation on single alert
```

### Incidents
```
POST   /api/v1/incidents                  Create incident
GET    /api/v1/incidents                  List (filters: status, severity, cluster, domain)
GET    /api/v1/incidents/:id              Get incident
PUT    /api/v1/incidents/:id              Update
POST   /api/v1/incidents/:id/acknowledge  Acknowledge
POST   /api/v1/incidents/:id/escalate     Escalate
POST   /api/v1/incidents/:id/resolve      Resolve
GET    /api/v1/incidents/:id/timeline     Event timeline
POST   /api/v1/incidents/:id/merge        Merge into another incident
```

### Webhooks (alert ingestion)
```
POST   /api/v1/webhooks/dynatrace         Dynatrace problem webhook
POST   /api/v1/webhooks/prometheus        Prometheus Alertmanager
POST   /api/v1/webhooks/grafana           Grafana unified alerting
POST   /api/v1/webhooks/splunk            Splunk saved search result
POST   /api/v1/webhooks/generic           Generic JSON alert
```

### Authentication
```
POST   /api/v1/auth/login                 Username + password → JWT
POST   /api/v1/auth/refresh               Refresh access token
POST   /api/v1/auth/logout                Invalidate session
GET    /api/v1/auth/me                    Current user info
GET    /api/v1/auth/mas/login             Initiate MAS OAuth flow
GET    /api/v1/auth/mas/callback          MAS OAuth callback
```

### Users & RBAC
```
GET    /api/v1/users                      List users (admin only)
GET    /api/v1/users/:id                  Get user (includes real name + avatar_url)
PUT    /api/v1/users/:id                  Update user
GET    /api/v1/roles                      List roles + permissions
POST   /api/v1/roles                      Create custom role
PUT    /api/v1/roles/:id                  Update role permissions
```

### Correlation & Analytics
```
GET    /api/v1/correlation/results/:id    Correlation detail for an alert
GET    /api/v1/analytics/insights         Incident insights + trending
GET    /api/v1/analytics/correlation-stats  Strategy performance metrics
GET    /api/v1/analytics/ontology         Failure domain distribution
```

### Topology
```
GET    /api/v1/topology/nodes             Infrastructure nodes
GET    /api/v1/topology/graph             Full graph export
GET    /api/v1/topology/blast-radius      Descendants of a node
POST   /api/v1/topology/refresh           Trigger manual refresh
```

### K8s
```
GET    /api/v1/k8s/clusters               K8s clusters list
GET    /api/v1/k8s/clusters/:name/pods    Pods in cluster
POST   /api/v1/k8s/clusters/:name/sync    Trigger topology sync
```

### Integrations & Notifications
```
GET    /api/v1/integrations               List integrations
POST   /api/v1/integrations               Register integration
GET    /api/v1/notifications/channels     Notification channels
POST   /api/v1/notifications/channels     Create channel (Slack / PagerDuty / email / webhook)
POST   /api/v1/notifications/send         Send manual notification
GET    /api/v1/notifications/history      Notification history
```

### Workflows
```
GET    /api/v1/workflows                  List workflows
POST   /api/v1/workflows                  Create
PUT    /api/v1/workflows/:id              Update
DELETE /api/v1/workflows/:id              Delete
POST   /api/v1/workflows/:id/execute      Run workflow manually
GET    /api/v1/workflows/:id/history      Execution history
```

### AI
```
POST   /api/v1/ai/summarize               Summarise incident (LLM)
POST   /api/v1/ai/explain                 Explain correlation decision
POST   /api/v1/ai/recommend-actions       Get AI-recommended actions
POST   /api/v1/ai/investigate             Start multi-turn investigation chat
```

### HCL / IncidentManager
```
POST   /api/v1/hcl/incidents              Create HCL ServiceNow incident via IncidentManager
GET    /api/v1/hcl/incidents              List External incidents
GET    /api/v1/hcl/incidents/:id          Get External incident
PUT    /api/v1/hcl/incidents/:id          Update External incident
DELETE /api/v1/hcl/incidents/:id          Delete External incident
```

### RCA (proxied to Python service)
```
POST   /api/v1/rca/investigations         Start investigation
GET    /api/v1/rca/investigations/:id     Get status + results
GET    /ws/investigations/:id             WebSocket stream of live investigation events
```

### System
```
GET    /health                            Service health (all dependencies)
GET    /ready                             Readiness probe
GET    /metrics                           Prometheus metrics
```

---

## Database (PostgreSQL 15) — Key Tables

| Table | Purpose |
|-------|---------|
| `users` | Accounts, roles, MFA, avatar_url, preferences (JSONB) |
| `roles` / `permissions` / `role_permissions` | RBAC model |
| `user_mas_groups` | MAS AD group membership sync |
| `ldap_group_role_mappings` | AD group → AlertHub role |
| `mas_group_mappings` | Pre-seeded MAS group → role (5 entries) |
| `mas_auth_logs` | Every MAS auth event (success / failure / denied) |
| `alerts` | All alerts — 96+ fields including AIOps extensions (entity_type, blast_radius_score, rca_confidence, agent_processed) |
| `alert_correlations` | Per-alert correlation result |
| `alert_similarity_cache` | Pairwise similarity scores |
| `incidents` | Incidents with JSONB alert_ids, timeline, RCA fields (rca_investigation_id, rca_status, blast_radius) |
| `correlation_rules` | DB-loaded rules (3 pre-seeded: High Severity Auto-Escalate, Database Correlation, Infra Grouping) |
| `pipeline_correlation_results` | Per-alert: all 4 strategy scores + final decision + dominant strategy + explanation |
| `causal_relationships` | Entity-to-entity causal graph edges |
| `propagation_paths` | Root→leaf propagation per incident |
| `incident_causal_graphs` | Per-incident CACIE result (JSONB) |
| `causal_pattern_templates` | 15 seeded domain propagation patterns |
| `correlation_feedback` | Operator verdicts for adaptive learning |
| `k8s_cluster_configs` | K8s cluster registry (API server URL, token, health score) |
| `k8s_topology_snapshots` | Periodic topology snapshots (JSONB) |
| `cloudstack_configs` / `netapp_configs` | Infrastructure API configuration |
| `workflows` / `workflow_executions` | Workflow definitions + execution history |
| `llm_configs` | LLM provider config (Ollama, OIDC Provider, Endor) |
| `alert_sources` | Alert source registry |
| `notification_channels` / `notification_rules` / `notification_log` | Notification subsystem |
| `api_request_log` | Full async audit trail of all API calls |
| `system_config` | Runtime key-value config (category + key → value) |
| `rca_investigations` | RCA results mirrored from Python service |
| `deduplication_rules` | Custom dedup rules |
| `post_mortems` | Incident post-mortem records |
| `slos` | Service level objectives |
| `maintenance_windows` | Alert suppression windows |
| `infra_regions` | Region registry (rno, mdn) |

---

## CI/CD & Deployment

```
git push to branch
      │
      ▼
Buildkit PreSync Job  (CI)
  ├─ Build Go backend   → Docker image (CGO_ENABLED=0 linux/amd64)
  ├─ Build React SPA    → Docker image (nginx)
  └─ Push to internal container registry
      │
      ▼
ArgoCD  (helm-revision CMP plugin)
  └─ Detects new image tag → updates Helm values → applies to cluster
      │
      ▼
Kubernetes  (example-cluster)
  Namespace: aileron
  ├─ alerthub-backend      Go service          port 3000
  ├─ alerthub-frontend     nginx + React SPA   port 80
  ├─ rca-orchestrator      Python FastAPI       port 3000
  ├─ local-bert            Flask BERT service  port 8766
  ├─ PostgreSQL            StatefulSet
  ├─ Redis                 StatefulSet
  ├─ Neo4j                 StatefulSet (APOC)
  ├─ Weaviate              Vector store
  └─ Ollama                LLM inference (qwen2.5:3b)
```

**No manual docker build or values.yaml edits needed.** git push triggers the full pipeline automatically.

### Local Development

```bash
# Start all infrastructure dependencies
docker-compose -f docker/docker-compose.yml up -d
# PostgreSQL :5432  |  Redis :6379  |  Neo4j :7474/:7687

# Go backend
go run cmd/main.go

# React frontend
cd frontend/alerthub-frontend
npm install && npm run dev

# Python RCA orchestrator
cd services/rca-orchestrator
pip install -r requirements.txt
uvicorn agent.main:app --port 3000 --reload

# BERT embedding service
cd services/local-bert
pip install sentence-transformers flask
python bert_service.py   # port 8766
```

---

## Key Environment Variables

| Variable | Description | Example / Default |
|----------|-------------|-------------------|
| `DB_HOST/PORT/USER/PASSWORD/NAME` | PostgreSQL connection | — |
| `REDIS_HOST/PORT/PASSWORD` | Redis connection | — |
| `NEO4J_URL` | Neo4j Bolt URL | `bolt://neo4j.aileron.svc:7687` |
| `JWT_SECRET` / `JWT_REFRESH_SECRET` | Token signing keys | openssl rand -base64 32 |
| `OAUTH_CLIENT_ID` | OIDC OAuth app ID | `961469` |
| `DYNATRACE_URL` / `DYNATRACE_API_TOKEN` | Dynatrace API | `mps-dynatrace-hybrid.k.example.com` |
| `OLLAMA_URL` / `OLLAMA_MODEL` | Local LLM | `qwen2.5:3b` |
| `WEAVIATE_URL` | Weaviate vector store | — |
| `KAFKA_BROKERS` | Kafka connection string | — |
| `ALERTHUB_ADMIN_GROUPS` | AD groups → admin | `interactive-apps-systems` |
| `ALERTHUB_OPERATOR_GROUPS` | AD groups → operator | `interactive-monitoring` |
| `OIDC_VIEWER_GROUPS` | AD groups → viewer | `interactive-all` |
| `RCA_V2` | Use V2 deterministic RCA | `true` |
| `INVESTIGATION_TIMEOUT_SECONDS` | Max RCA duration | `900` |
| `CLOUDSTACK_RNO_URL` / `_MDN_URL` | CloudStack regions | — |
| `INTERNAL_TLS_INSECURE` | Skip internal CA verify | `true` |

---

## Critical Patterns (Read Before Changing Code)

### API Response Envelope
All backend responses are double-wrapped:
```json
{ "data": { "data": [...], "total": 123 } }
```
Frontend unwraps with: `const items = r.data?.data ?? r.data`
Never use `.data.data` directly — always use the null-safe form.

### Topology-First Correlation
The topology score (45%) dominates. If topology ≥0.75 **and** ≥1.4× the next-best strategy, the aggregator
uses topology's answer directly — no weighted averaging. Do not modify this threshold without understanding
the blast-radius tests in `scripts/test_real_topology.sh`.

### Alert Fingerprint
Dedup key = `entity_id + metric_name + normalised_title` (MD5, first 16 chars).
Same fingerprint within 30 minutes → increment `count`, do not create a new alert row.

### Incident Idempotency
`correlation_id` is the dedup key for incident creation. Kafka at-least-once redelivery is safe — same
`correlation_id` returns the existing incident row rather than creating a duplicate.

### K8s Entity Scoping
Dynatrace K8s alerts are scoped by `{cluster}:{namespace}:{entity_id}` in their fingerprint.
This prevents false cross-cluster correlation even when entity names collide.

### Go-Live Weight Lock
Adaptive learning (EMA weight tuning) is **disabled by default** for production stability.
Production go-live weights are locked at: Topology(45%) Rules(25%) Semantic(20%) Temporal(10%).

### Commit Style
Do **not** add `Co-Authored-By` lines. Commits show only the user's name.

### Design System
Inline styles only, using the `c` token object (`src/lib/design-tokens.ts`) with CSS variables.
Never use Tailwind classes or external CSS frameworks.
