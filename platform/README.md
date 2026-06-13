# SRE Command Center — AlertHub + KubeSense

```
███████╗██████╗ ███████╗     ██████╗ ██████╗ ███╗   ███╗███╗   ███╗ █████╗ ███╗   ██╗██████╗
██╔════╝██╔══██╗██╔════╝    ██╔════╝██╔═══██╗████╗ ████║████╗ ████║██╔══██╗████╗  ██║██╔══██╗
███████╗██████╔╝█████╗      ██║     ██║   ██║██╔████╔██║██╔████╔██║███████║██╔██╗ ██║██║  ██║
╚════██║██╔══██╗██╔══╝      ██║     ██║   ██║██║╚██╔╝██║██║╚██╔╝██║██╔══██║██║╚██╗██║██║  ██║
███████║██║  ██║███████╗    ╚██████╗╚██████╔╝██║ ╚═╝ ██║██║ ╚═╝ ██║██║  ██║██║ ╚████║██████╔╝
╚══════╝╚═╝  ╚═╝╚══════╝    ╚═════╝ ╚═════╝ ╚═╝     ╚═╝╚═╝     ╚═╝╚═╝  ╚═╝╚═╝  ╚═══╝╚═════╝
```

> **Enterprise AIOps Platform — Unified SRE Command Center**
> AI-powered alert correlation + Kubernetes causal intelligence, comparable to Dynatrace Davis AI, Moogsoft, and BigPanda.
> Turns thousands of monitoring alerts into a handful of causal situations — with root cause, change attribution, and evidence trail.

| | |
|---|---|
| **Live URL** | https://aileron.example.com |
| **AlertHub Namespace** | `aileron` on `example-cluster` |
| **KubeSense Namespace** | `aileron-agent` on `example-cluster` |
| **AlertHub Repo** | `github.com/aileron-platform/aileron` |
| **KubeSense Repo** | `github.com/aileron-platform/aileron` |
| **Registry** | `ghcr.io/aileron-platform/aileron-admins` |

---

## What It Does

SRE Command Center is a **unified AIOps platform** — AlertHub handles multi-source alert correlation and incident management; KubeSense delivers Kubernetes causal intelligence. Both surface through the **AIOps page** as one integrated experience.

- **Alert ingestion**: Dynatrace, Prometheus, Grafana, Splunk, CloudStack, HCL, PagerDuty
- **Correlation**: 4-strategy CACIE engine (topology 45% + rules 25% + BERT 20% + temporal 10%)
- **Investigation**: OIE runs 16 evidence fetchers autonomously for every critical/high incident
- **K8s intelligence**: 5 Davis AI / Moogsoft / BigPanda algorithms — adaptive baselines, flap detection, Union-Find grouping, topology RCA, change correlation
- **Situations**: 1000 raw K8s events → ~5 namespace-level situations (67%+ noise reduction)
- **Narratives**: Local LLM (`qwen2.5:3b` + `nomic-embed-text`) for evidence-grounded explanations

---

## Platform Architecture

```mermaid
flowchart TD
    subgraph External["External Sources"]
        DT[Dynatrace]
        PROM[Prometheus]
        GRAF[Grafana]
        SPL[Splunk]
        CS[CloudStack MDN/RNO]
        NETAPP[NetApp Harvest]
        HCL[HCL]
    end

    subgraph Frontend["Frontend — React 18 + TypeScript"]
        UI[AIOps Dashboard<br/>30+ pages]
        WS_CLIENT[WebSocket Client]
        SSE_CLIENT[SSE Stream]
    end

    subgraph Backend["Backend — Go 1.24"]
        WEBHOOK[Webhook Handlers]
        NORM[Normalization Service]
        PIPE[Alert Pipeline FAST/TOPO/FULL]
        RCE[Root Cause Engine]
        CORR[CACIE Correlation Engine]
        TOPO[Topology Service + Neo4j Cache]
        AUTH[Auth Service OIDC/LDAP]
        WS_SRV[WebSocket Server]
        KS_PROXY[KubeSense Proxy]
    end

    subgraph OIE["OIE — Operational Intelligence Engine"]
        OIE_CONS[Kafka Consumer<br/>alerthub.incidents]
        OIE_BUS[Evidence Bus<br/>16 fetchers · 45s budget]
        OIE_HYP[Hypothesis Engine]
        OIE_RCA[RCA Generator<br/>+ LLM Narrator]
        OIE_WB[Writeback to Incidents]
    end

    subgraph KubeSense["KubeSense — aileron-agent namespace"]
        KS_AGENT[kubesense-agent<br/>K8s informers]
        KS_COLL[kubesense-collector<br/>Kafka consumer]
        KS_API[kubesense-api<br/>Intelligence + Correlation Engine]
        KS_LLM[kubesense-llm<br/>Incident Narrator]
        KS_NEO[(KubeSense Neo4j)]
        KS_PG[(KubeSense PostgreSQL)]
    end

    subgraph Storage["Storage"]
        PG[(PostgreSQL 15<br/>pgvector)]
        NEO[(Neo4j 5.15<br/>topology graph)]
        REDIS[(Redis 7<br/>cache + graph)]
    end

    subgraph AI["AI / ML"]
        BERT[BERT Service<br/>all-MiniLM-L6-v2 384-dim]
        OLLAMA[Ollama<br/>qwen2.5:3b + nomic-embed-text]
    end

    subgraph AuthSvc["Auth"]
        OIDC[Aileron OIDC OAuth2]
        LDAP[LDAP Group Sync]
        FG[OIDC Provider OIDC]
    end

    DT & PROM & GRAF & SPL & CS & NETAPP & HCL -->|webhooks| WEBHOOK
    WEBHOOK --> NORM --> PIPE
    PIPE --> RCE --> CORR
    CORR -->|embeddings| BERT
    CORR -->|graph query| REDIS
    CORR -->|topology| NEO
    PIPE --> PG
    RCE -->|deep RCA| OIE_CONS
    OIE_CONS --> OIE_BUS --> OIE_HYP --> OIE_RCA --> OIE_WB
    OIE_RCA -->|topology resolve| TOPO
    OIE_WB --> PG
    TOPO --> NEO & REDIS
    AUTH --> OIDC & LDAP & FG
    WS_SRV -->|WebSocket| WS_CLIENT
    Backend -->|SSE| SSE_CLIENT
    UI --> WS_CLIENT & SSE_CLIENT
    KS_AGENT -->|Kafka| KS_COLL
    KS_COLL --> KS_PG & KS_NEO
    KS_API --> KS_PG & KS_NEO
    KS_API -->|narrator trigger| KS_LLM
    KS_PROXY -->|proxy| KS_API
    Backend --> PG & NEO & REDIS & KS_PROXY

    classDef fe fill:#5497C1,stroke:#3d7aab,color:#fff
    classDef be fill:#A196CC,stroke:#7d6eb8,color:#fff
    classDef store fill:#FDC35D,stroke:#e0a83a,color:#333
    classDef ext fill:#FA975C,stroke:#e07a3d,color:#fff
    classDef ai fill:#E35E69,stroke:#c0404d,color:#fff
    classDef auth fill:#7FB1D1,stroke:#5a90b8,color:#fff
    classDef ks fill:#53AC79,stroke:#3d8a5e,color:#fff
    classDef oie fill:#B5D2E8,stroke:#5a90b8,color:#333

    class UI,WS_CLIENT,SSE_CLIENT fe
    class WEBHOOK,NORM,PIPE,RCE,CORR,TOPO,AUTH,WS_SRV,KS_PROXY be
    class PG,NEO,REDIS store
    class DT,PROM,GRAF,SPL,CS,NETAPP,HCL ext
    class BERT,OLLAMA ai
    class OIDC,LDAP,FG auth
    class KS_AGENT,KS_COLL,KS_API,KS_LLM,KS_NEO,KS_PG ks
    class OIE_CONS,OIE_BUS,OIE_HYP,OIE_RCA,OIE_WB oie
```

---

## Alert Processing Pipeline

```mermaid
flowchart LR
    subgraph Ingest["1 — Ingest"]
        W_DT[Dynatrace<br/>Webhook]
        W_PROM[Prometheus<br/>Webhook]
        W_GRAF[Grafana<br/>Webhook]
        W_SPL[Splunk<br/>Webhook]
        W_GEN[Generic<br/>Webhook]
    end

    subgraph Normalize["2 — Normalize"]
        NORM_SVC[Normalization<br/>Service]
        FP[Fingerprint<br/>Generator]
        VALID[Schema<br/>Validator]
    end

    subgraph Kafka["3 — Stream"]
        K_RAW[(raw-alerts)]
        CONSUMER[Kafka Consumer<br/>group: alerthub-pipeline-consumer]
        K_NORM[(normalized-alerts)]
        K_CORR[(correlation-results)]
    end

    subgraph Stage1["Stage 1 — FAST PATH<br/>32 workers · cap 10 000"]
        FP_DEDUP[Fingerprint dedup<br/>idempotency 10 min TTL]
        RESOLVED{Status =<br/>resolved?}
        FAST_EXIT[Fast-exit<br/>auto-close incident]
    end

    subgraph Stage2["Stage 2 — TOPO PATH<br/>16 workers · cap 5 000"]
        PG_UPSERT[PostgreSQL<br/>Upsert source_id]
        DT_TAG{Dynatrace<br/>rootCauseEntity<br/>tag?}
        TOPO_SCORE{Topology<br/>score ≥ 0.60?}
        ATTACH[ATTACH_TO_ROOT<br/>or CREATE_ROOT]
    end

    subgraph Stage3["Stage 3 — FULL PATH<br/>8 workers · cap 2 000"]
        SEM[Semantic<br/>all-MiniLM-L6-v2<br/>+ Weaviate · 20%]
        TEMP[Temporal<br/>exp decay<br/>λ=0.005/min · 10%]
        TOPO[Topology<br/>Redis Graph<br/>+ Neo4j · 45%]
        RULES[Rules<br/>DB Regex · 25%]
        AGG[Score Aggregator<br/>Topo·0.45 + Rules·0.25<br/>+ Sem·0.20 + Temp·0.10<br/>≥ 0.65 → merge]
    end

    subgraph Output["Output"]
        INC[Incident<br/>CREATE / MERGE]
        ENRICH[qwen2.5:3b<br/>Inline RCA<br/>2-4 sentences]
        WS[WebSocket<br/>Broadcast]
        OIE_TRIG[Kafka publish<br/>alerthub.incidents<br/>→ OIE investigation]
    end

    W_DT & W_PROM & W_GRAF & W_SPL & W_GEN --> NORM_SVC
    NORM_SVC --> FP --> VALID --> K_RAW
    K_RAW --> CONSUMER --> FP_DEDUP
    FP_DEDUP --> RESOLVED
    RESOLVED -->|yes| FAST_EXIT
    RESOLVED -->|no| PG_UPSERT
    PG_UPSERT --> DT_TAG
    DT_TAG -->|yes| ATTACH
    DT_TAG -->|no| TOPO_SCORE
    TOPO_SCORE -->|yes| ATTACH
    TOPO_SCORE -->|no| SEM & TEMP & TOPO & RULES
    SEM & TEMP & TOPO & RULES --> AGG
    AGG --> INC
    ATTACH --> INC
    INC --> ENRICH & WS & OIE_TRIG
    INC --> K_NORM
    AGG --> K_CORR

    classDef ingest fill:#FA975C,stroke:#e07a3d,color:#fff
    classDef norm fill:#A196CC,stroke:#7d6eb8,color:#fff
    classDef kafka fill:#53AC79,stroke:#3d8a5e,color:#fff
    classDef s1 fill:#7FB1D1,stroke:#5a90b8,color:#fff
    classDef s2 fill:#5497C1,stroke:#3d7aab,color:#fff
    classDef s3 fill:#E35E69,stroke:#c0404d,color:#fff
    classDef out fill:#FDC35D,stroke:#e0a83a,color:#333

    class W_DT,W_PROM,W_GRAF,W_SPL,W_GEN ingest
    class NORM_SVC,FP,VALID norm
    class K_RAW,CONSUMER,K_NORM,K_CORR kafka
    class FP_DEDUP,RESOLVED,FAST_EXIT s1
    class PG_UPSERT,DT_TAG,TOPO_SCORE,ATTACH s2
    class SEM,TEMP,TOPO,RULES,AGG s3
    class INC,ENRICH,WS,OIE_TRIG out
```

---

## CACIE — Correlation Engine Detail

```mermaid
flowchart TD
    subgraph Input["Incoming Alert"]
        A[Alert<br/>title + labels<br/>+ entity_type<br/>+ cluster]
    end

    subgraph RCE["Stage 2 — Root Cause Engine (Deterministic)"]
        DT_ROOT{rootCauseEntity<br/>tag present?}
        TOPO_DOM{Topology score<br/>≥ 0.60?}
        FAST_PATH[Deterministic<br/>attach / create]
    end

    subgraph Strategies["Stage 3 — Parallel Correlation Strategies"]
        subgraph TOPO["Topology — 45%"]
            RGRAPH[Redis Graph<br/>same cluster<br/>node / VM / KVM]
            NEO_Q[Neo4j Cypher<br/>infra path query]
        end

        subgraph TEMP["Temporal — 10%"]
            DECAY[Exponential<br/>time-decay<br/>λ = 0.005/min<br/>half-life 30 min]
        end

        subgraph RULES["Rules — 25%"]
            DB_RULES[DB operator rules<br/>regex + entity_type<br/>+ priority]
        end

        subgraph SEM["Semantic — 20%"]
            EMBED[BERT Embedding<br/>all-MiniLM-L6-v2<br/>384-dim :8766]
            ANN[Weaviate ANN<br/>k=10 neighbours]
        end
    end

    subgraph Aggregation["Score Aggregation"]
        WEIGHT[Weighted sum<br/>0.45·topo + 0.10·temp<br/>+ 0.25·rules + 0.20·sem]
        THRESH{score ≥ 0.65?}
        CREATE_INC[Create new<br/>Incident]
        MERGE_INC[Merge into<br/>existing Incident]
    end

    subgraph Enrich["Enrichment"]
        INLINE[qwen2.5:3b<br/>Inline 2-4 sentence<br/>RCA narrative]
        OIE_KICK[OIE trigger<br/>Kafka: alerthub.incidents<br/>deep investigation]
    end

    A --> DT_ROOT
    DT_ROOT -->|yes| FAST_PATH
    DT_ROOT -->|no| TOPO_DOM
    TOPO_DOM -->|yes| FAST_PATH
    TOPO_DOM -->|no| RGRAPH & DECAY & DB_RULES & EMBED
    RGRAPH --> NEO_Q
    EMBED --> ANN
    NEO_Q & DECAY & DB_RULES & ANN --> WEIGHT
    WEIGHT --> THRESH
    THRESH -->|yes| MERGE_INC
    THRESH -->|no| CREATE_INC
    FAST_PATH --> MERGE_INC
    MERGE_INC & CREATE_INC --> INLINE
    MERGE_INC & CREATE_INC --> OIE_KICK
```

---

## OIE Investigation Flow

```mermaid
sequenceDiagram
    autonumber
    participant KAFKA as Kafka<br/>alerthub.incidents
    participant OIE as OIE Service
    participant BUS as Evidence Bus<br/>16 fetchers · 45s
    participant NEO as Neo4j<br/>Topology
    participant KS as KubeSense DB<br/>health_events · violations
    participant PG as PostgreSQL
    participant LLM as Ollama<br/>qwen2.5:3b
    participant AH as AlertHub<br/>Backend

    KAFKA->>OIE: incident.created {incident_id, severity, topology_path}
    Note over OIE: Idempotency check — skip if already investigating

    OIE->>AH: GET /api/v1/topology/resolve?topology_path=X
    AH-->>OIE: {cluster_id, namespace, k8s_node, cloudstack_vm}
    Note over OIE: EntityProfile enriched — fetchers have accurate coordinates

    par Evidence gathering (parallel, 45s budget)
        OIE->>NEO: GetUpstreamChain(pod, depth=8)
        NEO-->>OIE: dependency chain nodes
        OIE->>NEO: GetK8sEventsFor(each node)
        NEO-->>OIE: Warning events, OOMKilling, BackOff
        OIE->>KS: SELECT FROM kubesense_health_events WHERE namespace=$1
        KS-->>OIE: health.pod.crashloopbackoff, change.deployment.rollout
        OIE->>KS: SELECT FROM kubesense_config_violations
        KS-->>OIE: SINGLE_REPLICA, RESOURCE_LIMITS_MISSING
        OIE->>PG: SELECT past_investigations WHERE similarity > 0.70
        PG-->>OIE: past RCAs (nomic-embed-text 768-dim cosine similarity)
    end

    Note over OIE: Hypothesis Engine scores 6-23 candidates
    Note over OIE: WinnerFrom() — requires SupportingEvidenceCount≥1 OR confidence≥0.50
    Note over OIE: countGroundingFacts() — blocks LLM if 0 actual facts

    OIE->>LLM: Generate narrative (temperature=0.1, verified facts only)
    Note over LLM: 5 rules: no invented names, facts must be listed, refuse if unclear
    LLM-->>OIE: evidence-grounded 3-sentence narrative

    OIE->>AH: POST /api/v1/incidents/:id/oie-result
    Note over AH: confidence, band, root_cause, evidence_count written to incident
    AH-->>OIE: 200 OK
```

---

## Auth Flow (OIDC OAuth2 + LDAP + OIDC Provider)

```mermaid
sequenceDiagram
    autonumber
    participant U as User Browser
    participant NGINX as NGINX Ingress
    participant FE as React Frontend
    participant BE as Go Backend
    participant OIDC as Aileron OIDC<br/>
    participant LDAP as LDAP<br/>:636
    participant FG as OIDC Provider<br/>
    participant PG as PostgreSQL

    U->>NGINX: GET https://aileron.example.com
    NGINX-->>U: 302 → /api/v1/auth/oidc (no session)

    U->>BE: GET /api/v1/auth/oidc?redirect=/dashboard
    BE->>OIDC: 302 → Authorization URL<br/>client_id, redirect_uri, scope=openid profile email<br/>audience=[sre-command-center, sear-oidc]
    OIDC-->>U: Aileron SSO login page

    U->>OIDC: Authenticate (Aileron SSO)
    OIDC-->>U: 302 → /api/v1/auth?code=xxx&state=yyy

    U->>BE: GET /api/v1/auth?code=xxx&state=yyy
    BE->>OIDC: POST /auth/oauth2/token (code exchange)
    OIDC-->>BE: access_token + refresh_token + id_token + groups claim

    alt OIDC groups claim empty
        BE->>LDAP: Lookup memberOf for user email
        LDAP-->>BE: [aileron-admins, aileron-operators, ...]
    end

    Note over BE: RBAC resolution (priority order):<br/>1. DB ldap_group_role_mappings (Admin UI configured)<br/>2. ALERTHUB_ADMIN_GROUPS env var<br/>3. ALERTHUB_OPERATOR_GROUPS env var<br/>4. Existing DB role (never downgrade)<br/>5. OIDC_DEFAULT_ROLE (default: viewer)

    BE->>FG: POST exchange refresh_token → OIDC Provider id_token<br/>audience=sear-oidc (for AI assistant)
    FG-->>BE: oidc_id_token (TTL 55 min)

    BE->>PG: UPSERT users (auto-provision role from LDAP groups)

    BE->>BE: Generate JWT access_token (24h) + refresh_token (7d)
    BE-->>U: 302 → /oauth/callback?exchange_code=yyy

    U->>BE: GET /api/v1/auth/oidc/exchange?code=yyy
    BE-->>U: {access_token, refresh_token, oidc_token, oidc_token}

    FE->>FE: Store in enhancedAuthStore (Zustand)<br/>visibilitychange resets lastActivity<br/>proactive refresh 5min before expiry

    Note over FE,BE: Tab reactivation fix (June 2026):<br/>window.location.href moved to useEffect+useRef guard<br/>— prevents duplicate redirects on React re-renders
```

---

## Infrastructure Topology Graph

```mermaid
flowchart TD
    subgraph Compute["Compute Hierarchy"]
        CL[k8s_cluster<br/>or cloudstack_zone]
        KVM[bare_metal<br/>KVM Host]
        VM[cloudstack_vm]
        NODE[k8s_node]
        POD[k8s_pod]
    end

    subgraph Storage["NetApp Storage Hierarchy"]
        NC[netapp_cluster]
        NN[netapp_node]
        NA[netapp_aggregate]
        NS[netapp_svm]
        NV[netapp_volume]
        NB[netapp_s3_bucket]
        PV[k8s_pv]
        PVC[k8s_pvc]
    end

    subgraph Refresh["Sync Intervals"]
        R_K8S[K8s — every 5 min]
        R_CS[CloudStack — every 15 min]
        R_NA[NetApp — every 30 min]
        R_CACHE[Redis graph cache<br/>TTL 5 min]
    end

    CL --> KVM --> VM
    CL --> NODE --> POD
    NC --> NN --> NA --> NS --> NV --> NB
    NV --> PV --> PVC --> POD

    style Compute fill:#5497C1,stroke:#3d7aab,color:#fff
    style Storage fill:#53AC79,stroke:#3d8a5e,color:#fff
    style Refresh fill:#FDC35D,stroke:#e0a83a,color:#333
```

**Topology refresh:** K8s 5 min · CloudStack 15 min · NetApp 30 min · Redis cache TTL 5 min

---

## Data Model

```mermaid
classDiagram
    class Alert {
        +UUID id
        +string source
        +string source_id
        +string title
        +string severity
        +string status
        +jsonb labels
        +string entity_type
        +string fingerprint
        +string cluster
        +timestamp created_at
    }

    class Incident {
        +UUID id
        +string title
        +string status
        +string severity
        +string rca_status
        +float rca_confidence
        +text ai_root_cause
        +text[] blast_radius
        +string topology_path
        +timestamp created_at
        +timestamp resolved_at
    }

    class RcaInvestigation {
        +UUID id
        +UUID incident_id
        +string status
        +float confidence
        +text root_cause_summary
        +string domain
        +vector embedding_768dim
        +timestamp created_at
    }

    class User {
        +UUID id
        +string username
        +string email
        +string role
        +text[] ldap_groups
        +timestamp last_login
    }

    class TopologyEntity {
        +UUID id
        +string entity_type
        +string name
        +string cluster
        +string zone
        +string kvm_host
        +jsonb labels
    }

    class CorrelationRule {
        +UUID id
        +string name
        +string pattern
        +string entity_type
        +float weight
        +int priority
        +bool enabled
    }

    Incident "1" --> "many" Alert : groups
    Incident "1" --> "1" RcaInvestigation : investigated_by
    TopologyEntity "many" --> "many" TopologyEntity : relates_to
    CorrelationRule "many" --> "many" Alert : matches
```

---

## GitOps CI/CD Pipeline

```mermaid
flowchart LR
    subgraph Dev["Developer"]
        PUSH[git push<br/>Conventional Commit]
    end

    subgraph GitHooks["Branch Protection"]
        LINT[commit-lint]
        NAMING[branch-naming check]
        SECRET_SCAN[secret-scan]
    end

    subgraph BuildKit["Buildkit — Rootless In-Cluster"]
        BK_BE[Build alerthub-backend]
        BK_FE[Build sre-command-center]
        BK_BERT[Build alerthub-bert]
        BK_OIE[Build OIE]
        BK_KS[Build kubesense services x5]
    end

    subgraph Registry["Registry"]
        REG[ghcr.io/aileron-platform<br/>aileron-admins]
    end

    subgraph Cosign["Image Signing"]
        SIGN[Cosign sign<br/>SecretsManager Aileron corp CA]
    end

    subgraph ArgoCD["ArgoCD — Applications"]
        APP_BE[alerthub-backend]
        APP_FE[sre-command-center]
        APP_OIE[alerthub-oie]
        APP_KS[kubesense-hub]
    end

    subgraph K8s["Kubernetes"]
        DEP_BE[alerthub-backend<br/>2 replicas]
        DEP_FE[alerthub-frontend<br/>2 replicas]
        DEP_OIE[oie<br/>2 replicas]
        DEP_KS[5 kubesense services]
    end

    PUSH --> LINT & NAMING & SECRET_SCAN
    LINT & NAMING & SECRET_SCAN --> BK_BE & BK_FE & BK_BERT & BK_OIE & BK_KS
    BK_BE & BK_FE & BK_BERT & BK_OIE & BK_KS --> REG
    REG --> SIGN --> ArgoCD
    APP_BE --> DEP_BE
    APP_FE --> DEP_FE
    APP_OIE --> DEP_OIE
    APP_KS --> DEP_KS
```

---

## KubeSense — Davis AI / Moogsoft / BigPanda Algorithms

```mermaid
flowchart TD
    subgraph Input["Event Arrives in Buffer"]
        EV[BufferEntry<br/>event_type · namespace · resource]
    end

    subgraph SM["Algorithm 2 — Alert State Machine<br/>statemachine.go"]
        direction LR
        OK_S[OK]
        PEND[Pending<br/>wait 2min]
        FIRE[Firing]
        RESV[Resolved<br/>wait 5min]
        SUPP[Suppressed<br/>flap detected]
        OK_S -->|anomaly| PEND
        PEND -->|"2min sustained"| FIRE
        PEND -->|"cleared early"| OK_S
        FIRE -->|resolved| RESV
        RESV -->|"5min quiet"| OK_S
        RESV -->|re-anomaly| FIRE
        FIRE -->|">4 flaps/15min"| SUPP
        SUPP -->|"window expired"| OK_S
    end

    subgraph BASE["Algorithm 1 — Holt-Winters Baseline<br/>baseline.go"]
        HW[Level · Trend · Seasonal-168h<br/>α=0.1  β=0.01  γ=0.05<br/>threshold = 3.5 × MAD]
    end

    subgraph GRP["Algorithm 3 — Union-Find Grouper<br/>grouper.go"]
        UF[Multi-signal similarity<br/>Topology 0.4 · Labels 0.3<br/>Temporal exp-dt/300s 0.2 · Family 0.1<br/>θ = 0.45 → merge]
    end

    subgraph RULES["Existing — Rule Engine<br/>rule_engine.go"]
        RE[8 built-in rules<br/>+ auto-detected patterns<br/>oom-then-crash · node-eviction<br/>rollout-degraded · config-crash...]
    end

    subgraph TOPO_RCA["Algorithm 4 — Topology RCA<br/>topo_rca.go"]
        TR[causalScore = depthScore × timeEarliest × infraBoost<br/>Node 1.5× · PVC 1.3× · Deployment 1.2× · Pod 1.0×<br/>SymptomFilter: pod alerts suppressed when node is root cause]
    end

    subgraph CHG_RCA["Algorithm 5 — Change Correlation<br/>change_rca.go"]
        CR[Scan kubesense_changes 2h before incident<br/>confidence = overlapScore × exp-dt/30min<br/>Links incident to exact ArgoCD sync / commit]
    end

    subgraph Output["Incident Lifecycle"]
        DET[Detecting]
        ACT[Active]
        RESV2[Resolved]
        DET -->|"StabilizationWindow 30s"| ACT
        ACT -->|"HealthyResolveWindow 5min"| RESV2
    end

    EV --> SM
    SM -->|SUPPRESS| DROP[Dropped<br/>flap suppressed]
    SM -->|FIRE| BASE
    BASE --> GRP
    GRP & RULES --> Output
    Output -->|Active| TOPO_RCA
    Output -->|Active| CHG_RCA
    TOPO_RCA & CHG_RCA -->|Root cause attributed| NAR[LLM Narrator<br/>kubesense.correlation.incident-context]
```

---

## KubeSense Event Flow

```mermaid
sequenceDiagram
    autonumber
    participant AG as kubesense-agent<br/>aileron-agent namespace
    participant KF as Strimzi Kafka<br/>kubesense.events.*
    participant CL as kubesense-collector
    participant PG as KubeSense PostgreSQL
    participant NE as KubeSense Neo4j
    participant API as kubesense-api<br/>Correlation Engine
    participant AH as AlertHub Backend
    participant LLM as kubesense-llm<br/>Ollama narrator

    Note over AG: K8s informers watch 25 resource types
    AG->>KF: Publish IntelligenceEvent<br/>health.pod.crashloopbackoff · namespace=production
    AG->>KF: Publish chaos.scores every 5min<br/>cluster_score=33.2 · grade=F · 239 high-risk workloads
    AG->>KF: Publish config.violations every 5min<br/>SINGLE_REPLICA · RESOURCE_LIMITS_MISSING

    KF->>CL: Consume kubesense.events.health
    CL->>PG: INSERT kubesense_health_events
    CL->>PG: INSERT kubesense_changes (change.* events)
    CL->>NE: UPSERT topology nodes + edges

    Note over API: Buffer feeder polls every 30s
    API->>PG: SELECT events WHERE occurred_at > watermark
    PG-->>API: 60-76 new events per poll
    API->>API: Feed(entry) → State Machine → Grouper → Rules

    Note over API: Rule fires: rollout-degraded
    API->>API: Incident transitions: Detecting → Active
    API->>KF: Publish kubesense.correlation.incident-context<br/>{incident_id, cluster_id, signals[], evidence_grade}

    KF->>LLM: Consume incident context
    LLM->>LLM: Call Ollama qwen2.5:3b (temperature=0.1)
    LLM->>PG: INSERT kubesense_narratives
    LLM->>KF: Publish kubesense.llm.narratives

    AH->>API: GET /api/v1/correlation/incidents
    API-->>AH: 13 active situations with full field data
    Note over AH: Surfaces in AIOps → Situations tab
```

---

## Unified AIOps Platform — Situations View

```mermaid
flowchart LR
    subgraph Sources["Alert Sources"]
        DT2[Dynatrace]
        PROM2[Prometheus]
        KS2[KubeSense<br/>K8s events]
    end

    subgraph Correlation["Correlation Engines"]
        AH_CORR[AlertHub CACIE<br/>4-strategy engine<br/>topology 45% + rules 25%<br/>BERT 20% + temporal 10%]
        KS_CORR[KubeSense Correlator<br/>Union-Find grouper θ=0.45<br/>State machine + flap detection<br/>15 alerts → 5 situations]
    end

    subgraph AIOps["AIOps Page — Single Pane of Glass"]
        OVERVIEW[Overview<br/>AlertHub stats +<br/>K8s infra card]
        SITUATIONS[Situations ⭐<br/>Unified Davis AI view<br/>Phase · Severity · Method<br/>CHANGE RCA · AUTO LEARNED]
        CORRELATIONS[Correlations<br/>CACIE pipeline results]
        RCA_TAB[RCA<br/>OIE investigation stream]
    end

    DT2 & PROM2 --> AH_CORR --> CORRELATIONS & OVERVIEW
    KS2 --> KS_CORR --> SITUATIONS & OVERVIEW

    classDef src fill:#FA975C,stroke:#e07a3d,color:#fff
    classDef corr fill:#A196CC,stroke:#7d6eb8,color:#fff
    classDef tab fill:#5497C1,stroke:#3d7aab,color:#fff
    classDef star fill:#FDC35D,stroke:#e0a83a,color:#333

    class DT2,PROM2,KS2 src
    class AH_CORR,KS_CORR corr
    class OVERVIEW,CORRELATIONS,RCA_TAB tab
    class SITUATIONS star
```

---

## Project Structure

```
alerthub-enterprise/
├── cmd/
│   └── main.go                     # Entry point — router + service wiring
├── internal/
│   ├── api/                        # HTTP handlers (REST + WebSocket + SSE)
│   ├── models/                     # Alert, Incident, User, Topology structs
│   ├── services/
│   │   ├── normalization/          # Dynatrace, Prometheus, Grafana, Splunk normalizers
│   │   ├── pipeline/               # Kafka consumer, alert pipeline orchestrator
│   │   ├── correlation/            # 4-strategy correlation engine + aggregator
│   │   ├── topology/               # Neo4j + Redis graph topology service
│   │   ├── rca/                    # RCA trigger + result storage
│   │   └── auth/                   # OIDC OAuth2, LDAP sync, JWT
│   ├── db/                         # PostgreSQL migrations + query layer
│   └── kafka/                      # Producer + consumer wrappers
├── frontend/
│   └── alerthub-frontend/
│       ├── src/
│       │   ├── pages/              # 30+ routed pages
│       │   ├── components/         # Shared UI components
│       │   ├── store/              # Zustand stores (enhancedAuthStore, etc.)
│       │   ├── api/                # API client + WebSocket client
│       │   └── styles/             # Design tokens (c object, CSS variables)
│       ├── vite.config.ts
│       └── package.json
├── services/
│   ├── oie/                        # Go OIE — 16-fetcher evidence bus, hypothesis engine
│   ├── bert-service/               # Python BERT embedding service :8766
│   └── rca-orchestrator/           # Python FastAPI RCA service :8006
├── database/
│   └── migrations/                 # PostgreSQL schema migrations
├── helm/                           # Helm chart (all 4 ArgoCD apps)
├── argocd/                         # ArgoCD Application manifests
├── k8s/                            # Raw Kubernetes manifests
├── scripts/
│   ├── mock_alerts.sh              # Send test alert webhooks
│   ├── mock_cascade_alerts.sh      # Simulate cascading failure
│   ├── mock_chaos_tests.sh         # Chaos injection scenarios
│   ├── mock_infra_tests.sh         # Infrastructure alert tests
│   ├── test_comprehensive_v2.sh    # Full pipeline regression test
│   └── test_real_topology.sh       # Live topology query tests
├── docs/                           # Architecture docs + runbooks
├── Dockerfile                      # Multi-stage Go + frontend build
├── Makefile                        # dev, build, test, push targets
└── go.mod
```

---

## Tech Stack

| Layer | Technology | Version | Purpose |
|---|---|---|---|
| **Frontend** | React | 18 | UI framework |
| **Frontend** | TypeScript | 5 | Type safety |
| **Frontend** | Vite | latest | Build tool |
| **Frontend** | Zustand | latest | State management |
| **Backend** | Go | 1.24 | API server, pipeline orchestration |
| **Backend** | Gin | latest | HTTP router + middleware |
| **Backend** | gorilla/websocket | latest | Real-time alert streaming |
| **Database** | PostgreSQL | 15 + pgvector | Primary store, vector similarity (768-dim) |
| **Database** | Neo4j | 5.15 | Infrastructure topology graph |
| **Database** | Redis | 7 | Cache, pub/sub, rate limiting |
| **Streaming** | Kafka | 3 (Strimzi) | Alert event bus |
| **AI — Embeddings** | BERT Service | all-MiniLM-L6-v2 · 384-dim · Python :8766 | Local text embeddings for CACIE |
| **AI — Embeddings** | nomic-embed-text | 768-dim via Ollama | Semantic past-investigation search (pgvector) |
| **AI — Inline LLM** | Ollama qwen2.5:3b | — | 2–4 sentence RCA narrative on incident creation |
| **AI — Deep RCA** | OIE (Go) | — | 16-fetcher evidence DAG, hypothesis scoring, LLM narrator |
| **Auth** | Aileron OIDC OAuth2 |  | Primary SSO |
| **Auth** | LDAP | — | Group-based role assignment |
| **Auth** | OIDC Provider | OIDC | Access gate |
| **GitOps** | ArgoCD | latest | Continuous deployment |
| **GitOps** | BuildKit | rootless | In-cluster image builds |
| **GitOps** | Cosign | SecretsManager CA | Image signing |
| **Infra** | Kubernetes | 1.28+ | Container orchestration |
| **Infra** | Helm | 3 | Chart templating |

---

## Key Features

- **Unified Alert Ingestion** — Single webhook surface for Dynatrace, Prometheus, Grafana, Splunk, and generic sources; each normalized to a common `Alert` struct with fingerprint, severity, entity_type, cluster, and JSONB labels before hitting Kafka.

- **Three-Stage Alert Pipeline** — Alerts from Kafka enter a `StagedPipeline` with three worker pools: FAST PATH (32 workers, cap 10 000) for fingerprint dedup and fast-exit on resolved alerts; TOPO PATH (16 workers, cap 5 000) for deterministic root cause decisions; and FULL PATH (8 workers, cap 2 000) for four-strategy probabilistic scoring.

- **Four-Strategy Correlation Engine (CACIE)** — Alerts correlated by weighted combination: topology graph proximity (45%), operator regex rules (25%), BERT semantic embeddings (20%, `all-MiniLM-L6-v2` 384-dim), exponential temporal decay (10%, half-life 30 min). Topology score ≥ 0.60 is a deterministic override. Merge threshold: 0.75.

- **Dynatrace Root Cause Fast Path** — When a Dynatrace alert carries a `rootCauseEntity` tag, the engine immediately attaches it without ML inference — sub-millisecond decision.

- **17-Point Dedup Cascade** — `inflight sync.Map` + per-cluster mutex locks prevent duplicate incidents from concurrent Kafka consumer goroutines, racing normalization retries, or Kafka replay.

- **OIE Evidence-First RCA** — Standalone Go service consuming `alerthub.incidents` Kafka topic. Runs 16-fetcher evidence DAG (K8s node conditions, pod exit codes, PDB checks, CloudStack VM/host state, NetApp volume/aggregate/SVM state, KubeSense signals, OKG change correlations, runbooks, past investigations, APM regressions) before scoring hypotheses and generating a grounded narrative. 7-layer hallucination prevention: temperature=0.1, WinnerFrom gate, countGroundingFacts blocker, thresholds 0.75/3 facts, anti-injection prompt sanitization, FetchMissing sentinel.

- **Dual LLM Enrichment** — New incidents immediately receive an inline `qwen2.5:3b` narrative (sub-second). OIE then runs a deeper evidence-grounded investigation (45s budget, 16 fetchers, pgvector semantic past-investigation search) before writing back a confidence-scored root cause to the incident.

- **Live Infrastructure Topology** — Neo4j 5.15 stores the full infrastructure graph (CloudStack VMs, KVM hosts, K8s nodes, clusters, NetApp volumes). Redis provides fast in-memory cache for correlation scoring. `GET /api/v1/topology/resolve` resolves any entity in <10ms.

- **Role-Based Access Control** — Four-tier RBAC (admin / sre / operator / viewer) from LDAP groups via OIDC OAuth2. Priority: DB mappings → `ALERTHUB_ADMIN_GROUPS` env → preserved role → `OIDC_DEFAULT_ROLE`. Groups: `aileron-admins` → admin, `aileron-operators` → operator, `aileron-viewers` → viewer.

- **KubeSense Integration** — 5 Go services (agent, collector, core, api, llm) in `aileron-agent` namespace. 5 Davis AI algorithms: Holt-Winters baseline anomaly detection, alert state machine + flap suppression, Union-Find multi-signal grouper (67% noise reduction), topology-anchored root cause scoring, change correlation RCA. All signals feed OIE evidence DAG and surface in the Situations tab.

- **Postmortem Auto-Generation** — On incident resolution, `PostmortemService` generates a structured document (impact, root cause, timeline, lessons learned, action items). LLM-generated when `rca_confidence >= 0.60`, deterministic template fallback. Accessible via MCP `get_postmortem` tool.

- **Gate Hooks (Remediations)** — Proposed automated actions enter `remediations_pending` with `status=proposed`. Slack webhook notifies on-call; engineer approves/rejects in UI. No automated remediation executes without explicit approval.

- **MCP Server** — `POST /api/v1/mcp` exposes 7 tools to Claude Desktop, Cursor, Windsurf: `list_incidents`, `get_incident`, `get_rca_decisions`, `search_incidents`, `get_postmortem`, `list_runbooks`, `propose_remediation`. JSON-RPC 2.0, protocol version `2024-11-05`.

- **Policy Engine** — DB-driven `intelligence_policies` table. Types: `suppress_alert`, `suppress_incident`, `skip_rca`, `require_approval`, `auto_resolve`. 5-minute cache, 500 policy limit, priority-ordered.

- **LLM Guard** — RFC-1918 IPs, K8s UIDs, Aileron hostnames, internal DNS, credentials redacted before LLM. SigmaHQ-pattern injection detection blocks `ignore previous instructions`, `act as`, `eval(` in alert payloads.

- **Stale Sweep** — Hourly background goroutine resolves: fingerprint alerts open >4h, all alerts with no update in 24h, incidents with all alerts resolved, OIE investigations stuck in `investigating` >15min.

- **Real-Time Dashboard** — WebSocket streaming to all connected clients. 30+ React pages: alert feed, incident timeline, blast radius visualizer, topology graph, RCA viewer, correlation rules editor, Situations tab, KubeSense intelligence tabs (chaos, violations, forecasts, APM, changes, risk, playbooks, correlation).

- **GitOps Automation** — Single `git push` triggers: commit-lint + secret-scan, rootless BuildKit image builds, Cosign signing with SecretsManager Aileron corp CA, ArgoCD sync. `helm-revision-v1.0` CMP derives `imageTag` from `git log -- <service-paths>` — no manual `values.yaml` edits.

---

## Quick Start

### Prerequisites

- `kubectl` configured for `oidc02@example-cluster-01`
- Access to `aileron` namespace
- VPN (if required by your cluster) connected

### Access the Live UI

```
https://aileron.example.com
```

Login via Aileron OIDC OAuth2. Your LDAP group determines your role (`aileron-admins` → admin).

### Send a Test Alert

```bash
# Fire a synthetic Dynatrace alert at the live cluster
curl -X POST https://aileron.example.com/api/v1/webhooks/event-driven/dynatrace \
  -H "Content-Type: application/json" \
  -d '{
    "title": "CPU spike on test-node",
    "state": "OPEN",
    "impactedEntityName": "test-node",
    "severityLevel": "PERFORMANCE",
    "ImpactedEntities": [{"name": "test-node", "type": "HOST"}]
  }'

# Fire a batch of mixed alerts (targets localhost:8080 by default)
./scripts/mock_alerts.sh
```

### Connect Claude Desktop via MCP

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "alerthub": {
      "url": "https://aileron.example.com/api/v1/mcp",
      "transport": "http",
      "headers": {
        "Authorization": "Bearer <jwt-from-oidc-exchange>"
      }
    }
  }
}
```

### Check System Health

```bash
kubectl config use-context oidc02@example-cluster-01
kubectl get pods -n aileron
curl https://aileron.example.com/health/detailed
```

---

## Services

### AlertHub (aileron namespace)

| Service | Image | Port | Replicas | Description |
|---|---|---|---|---|
| `alerthub-backend` | `ghcr.io/aileron-platform/aileron-admins/alerthub-backend` | 8080 | 2 | Go API server: pipeline, correlation, MCP, gate hooks, policies |
| `alerthub-frontend` | `ghcr.io/aileron-platform/aileron-admins/sre-command-center` | 80 | 2 | React/TypeScript dashboard (Vite, Zustand) |
| `alerthub-bert-service` | `ghcr.io/aileron-platform/aileron-admins/alerthub-bert` | 8766 | 1 | Python BERT embedding service (all-MiniLM-L6-v2, 384-dim) |
| `rca-orchestrator` | `ghcr.io/aileron-platform/aileron-admins/rca-orchestrator` | 8006 | 1 | Python FastAPI legacy deep RCA (≤12 tool rounds, 900s timeout) |
| `oie` | `ghcr.io/aileron-platform/aileron-admins/oie` | 8081 | 1 | Go OIE: 16-fetcher evidence DAG, hypothesis scoring, LLM narrator |
| `ollama` | `ollama/ollama` | 11434 | 1 | Local LLM server (GPU nodeSelector), serves qwen2.5:3b + nomic-embed-text |

### KubeSense (aileron-agent namespace)

| Service | Image | Port | Replicas | Description |
|---|---|---|---|---|
| `kubesense-agent` | `ghcr.io/aileron-platform/aileron-admins/kubesense-agent` | — | 1 | In-cluster K8s watcher — chaos scorer + config scanner every 5min |
| `kubesense-collector` | `ghcr.io/aileron-platform/aileron-admins/kubesense-collector` | — | 1 | Kafka consumer → PostgreSQL `kubesense_health_events` + `kubesense_changes` + Neo4j |
| `kubesense-core` | `ghcr.io/aileron-platform/aileron-admins/kubesense-core` | 8080 | 1 | Cluster registry + topology REST API |
| `kubesense-api` | `ghcr.io/aileron-platform/aileron-admins/kubesense-api` | 8080 | 1 | 15 intelligence endpoints + correlation engine + buffer feeder + signal publisher |
| `kubesense-llm` | `ghcr.io/aileron-platform/aileron-admins/kubesense-llm` | 8080 | 1 | Incident narrator — Claude API → Ollama `qwen2.5:3b` → deterministic fallback |

---

## API Reference

### Auth

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/auth/oidc` | Initiate OIDC OAuth2 → redirect to  |
| `GET` | `/api/v1/auth` | OIDC OAuth2 callback |
| `GET` | `/api/v1/auth/oidc/callback` | Alias for OIDC callback |
| `GET` | `/api/v1/auth/oidc/exchange` | Redeem one-time code → JWT access + refresh tokens |
| `GET` | `/api/v1/auth/oidc/oidc-refresh` | Silent OIDC Provider token refresh |
| `GET` | `/api/v1/auth/oidc/groups` | Current user's synced LDAP groups |
| `GET` | `/api/v1/auth/oidc/settings` | Auth settings from DB |

### Webhooks

| Method | Path | Source |
|---|---|---|
| `POST` | `/api/v1/webhooks/event-driven/dynatrace` | Dynatrace problem webhook |
| `POST` | `/api/v1/webhooks/event-driven/prometheus` | Prometheus Alertmanager webhook |
| `POST` | `/api/v1/webhooks/event-driven/splunk` | Splunk alert action |
| `POST` | `/api/v1/webhooks/event-driven/datadog` | Datadog monitor alert |
| `POST` | `/api/v1/webhooks/event-driven/newrelic` | New Relic incident webhook |
| `POST` | `/api/v1/webhooks/event-driven/generic` | Generic JSON alert |

### Alerts

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/alerts` | List alerts (filter by status, severity, source) |
| `POST` | `/api/v1/alerts` | Manually create alert |
| `GET` | `/api/v1/alerts/:id` | Get single alert |
| `PATCH` | `/api/v1/alerts/:id` | Update alert status |

### Incidents

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/incidents` | List incidents |
| `POST` | `/api/v1/incidents` | Create incident manually |
| `GET` | `/api/v1/incidents/:id` | Get incident detail + correlated alerts |
| `PATCH` | `/api/v1/incidents/:id` | Update status / severity |
| `GET` | `/api/v1/incidents/:id/timeline` | Incident event timeline |
| `GET` | `/api/v1/incidents/:id/rca` | RCA findings for incident |

### RCA

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/rca/investigations` | List investigations (limit=30) |
| `GET` | `/api/v1/rca/investigations/:id` | Get investigation detail |
| `POST` | `/api/v1/rca/investigations` | Create manual investigation |
| `POST` | `/api/v1/rca/investigations/:id/feedback` | Submit feedback |
| `GET` | `/api/v1/rca/knowledge` | Knowledge base entries |
| `POST` | `/api/v1/rca/knowledge` | Add knowledge base entry |
| `GET` | `/api/v1/rca/model/info` | LLM model info |
| `POST` | `/api/v1/rca/model/train` | Trigger model training |
| `POST` | `/api/v1/incidents/:id/rca-callback` | Internal RCA completion callback |

### Topology

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/topology/live` | Live topology snapshot (Neo4j) |
| `GET` | `/api/v1/topology/entity/:id` | Single entity neighbours |
| `GET` | `/api/v1/topology/cluster/:name` | All entities in a cluster |
| `GET` | `/api/v1/topology/resolve` | Entity resolution via Neo4j+Redis cache (`?topology_path=X`) |
| `GET` | `/api/v1/topology/blast-radius` | Blast radius via Neo4j traversal (`?topology_path=X`) |

### Intelligence

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/intelligence/stats` | 24-hour intelligence activity summary |
| `GET` | `/api/v1/intelligence/policies` | List intelligence policies |
| `POST` | `/api/v1/intelligence/policies` | Create a policy |
| `PUT` | `/api/v1/intelligence/policies/:id` | Update a policy |
| `DELETE` | `/api/v1/intelligence/policies/:id` | Delete a policy |
| `GET` | `/api/v1/intelligence/runbooks` | List runbook skill catalog |
| `POST` | `/api/v1/intelligence/runbooks` | Add a runbook |
| `GET` | `/api/v1/incidents/:id/postmortem` | Get postmortem for an incident |
| `POST` | `/api/v1/incidents/:id/postmortem/generate` | Trigger postmortem generation |
| `GET` | `/api/v1/incidents/:id/remediations` | List gate-hook remediation proposals |
| `POST` | `/api/v1/incidents/:id/remediations` | Propose a remediation |
| `POST` | `/api/v1/incidents/:id/remediations/:rid/approve` | Approve a remediation |
| `POST` | `/api/v1/incidents/:id/remediations/:rid/reject` | Reject a remediation |
| `GET` | `/api/v1/mcp` | MCP server manifest (tool discovery) |
| `POST` | `/api/v1/mcp` | MCP server JSON-RPC endpoint |

### KubeSense

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/kubesense/db/violations` | Config violations from DB (last 100) |
| `GET` | `/api/v1/kubesense/db/forecasts` | Resource exhaustion forecasts from DB |
| `GET` | `/api/v1/kubesense/db/chaos` | Chaos readiness scores from DB |
| `GET` | `/api/v1/kubesense/db/health` | Health events from DB (last 200) |
| `GET` | `/api/v1/kubesense/db/investigations` | KubeSense investigation results from DB |
| `GET` | `/api/v1/kubesense/db/apm` | APM golden signals from DB |
| `GET` | `/api/v1/kubesense/db/stats` | Aggregated K8s statistics |
| `GET` | `/api/v1/kubesense/clusters` | Clusters (proxied to kubesense-core) |
| `GET` | `/api/v1/kubesense/topology` | Topology (proxied to kubesense-core) |
| `GET` | `/api/v1/kubesense/correlation/incidents` | Active situations (Union-Find grouped) |
| `GET` | `/api/v1/kubesense/correlation/rules` | Correlation rules (8 built-in + auto-detected) |
| `GET` | `/api/v1/kubesense/correlation/status` | Correlation engine stats |
| `GET` | `/api/v1/kubesense/clusters/:id/blast-radius` | K8s blast radius (Neo4j) |
| `GET` | `/api/v1/kubesense/clusters/:id/playbooks` | Auto-generated runbooks |
| `POST` | `/api/v1/kubesense/risk/score` | Pre-deployment change risk assessment |
| `GET` | `/api/v1/kubesense/narratives` | LLM incident narratives |
| `GET` | `/api/v1/incidents/:id/kubesense-investigation` | KubeSense investigation result for incident |

### Correlation Rules

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/rules` | List correlation rules |
| `POST` | `/api/v1/rules` | Create rule |
| `PUT` | `/api/v1/rules/:id` | Update rule |
| `DELETE` | `/api/v1/rules/:id` | Delete rule |

### Real-Time

| Protocol | Path | Description |
|---|---|---|
| `WebSocket` | `/ws/investigations/:inv_id` | Real-time RCA investigation event stream |
| `WebSocket` | `/ws` | General alert, incident, and topology event stream |

### Health

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Liveness probe |
| `GET` | `/health/detailed` | Detailed component health (DB, Kafka, Neo4j, Redis) |
| `GET` | `/ready` | Readiness probe (checks DB + Kafka) |
| `GET` | `/metrics` | Prometheus metrics endpoint |

---

## Kafka Topics

### AlertHub Topics

| Topic | Publisher | Consumer |
|---|---|---|
| `alerthub.incidents` | Backend pipeline | OIE — triggers investigation |
| `oie.investigations` | OIE | Frontend SSE stream |
| `oie.investigations.dlq` | OIE | Dead-letter queue |

### KubeSense Topics

| Topic | Publisher | Consumer |
|---|---|---|
| `kubesense.events.health` | agent | collector → kubesense_health_events |
| `kubesense.events.workloads` | agent | collector → kubesense_health_events |
| `kubesense.events.config` | agent | collector → kubesense_health_events |
| `kubesense.events.storage` | agent | collector → kubesense_health_events |
| `kubesense.chaos.scores` | agent | AlertHub DB |
| `kubesense.config.violations` | agent/api | AlertHub DB |
| `kubesense.correlation.incident-context` | kubesense-api | kubesense-llm |
| `kubesense.llm.narratives` | kubesense-llm | AlertHub |
| `kubesense.apm.golden-signals` | api | AlertHub DB |
| `kubesense.forecasts` | api | AlertHub DB |

---

## Configuration

All runtime configuration is supplied via environment variables. In Kubernetes these are injected from the `alerthub-secrets` secret and `alerthub-infra` ConfigMap.

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | PostgreSQL DSN (`postgres://user:pass@host:5432/db`) |
| `NEO4J_URI` | yes | Bolt URI (`bolt://neo4j:7687`) |
| `NEO4J_USER` | yes | Neo4j username |
| `NEO4J_PASSWORD` | yes | Neo4j password |
| `REDIS_ADDR` | yes | Redis address (`redis:6379`) |
| `KAFKA_BROKERS` | yes | Comma-separated broker list |
| `BERT_SERVICE_URL` | yes | BERT embedding service (`http://bert-service:8766`) |
| `OLLAMA_URL` | yes | Ollama base URL (`http://ollama:11434`) |
| `OIDC_CLIENT_ID` | yes | OAuth2 client ID |
| `OIDC_CLIENT_SECRET` | yes | OAuth2 client secret (from SecretsManager) |
| `OIDC_APP_ID` | yes | OIDC application ID (`961469`) |
| `OIDC_REDIRECT_URI` | yes | OAuth2 callback URL |
| `JWT_SECRET` | yes | HMAC secret for JWT signing (min 32 chars) |
| `JWT_REFRESH_SECRET` | yes | HMAC secret for refresh tokens (min 32 chars) |
| `LDAP_URL` | yes | LDAP server URL |
| `LDAP_BIND_DN` | yes | LDAP bind distinguished name |
| `LDAP_BIND_PASSWORD` | yes | LDAP bind password |
| `FLOODGATE_OIDC_CLIENT_ID` | yes | OIDC Provider OIDC client ID |
| `DYNATRACE_API_TOKEN` | yes | Dynatrace API token for RCA metrics |
| `CLOUDSTACK_API_URL` | yes | CloudStack API endpoint |
| `CLOUDSTACK_API_KEY` | yes | CloudStack API key |
| `CLOUDSTACK_SECRET_KEY` | yes | CloudStack secret key |
| `INTERNAL_SERVICE_TOKEN` | yes (prod) | Service-to-service auth token (fatal if unset in production) |
| `PORT` | no | HTTP listen port (default `8080`) |
| `LOG_LEVEL` | no | Log verbosity (`info`, `debug`, `warn`) |
| `ENV` | no | Environment name — `production` enables stricter security guards |
| `ALLOWED_ORIGINS` | no | Comma-separated CORS origin whitelist (required in production) |
| `CORRELATION_THRESHOLD` | no | Score threshold to merge incidents (default `0.75`) |
| `TOPOLOGY_DOMINANCE_THRESHOLD` | no | Score for deterministic topology override (default `0.60`) |
| `RCA_TIMEOUT_SECONDS` | no | RCA orchestrator timeout (default `900`) |
| `LLM_MODEL` | no | Default Ollama model (default `qwen2.5:3b`) |
| `LLM_TRIAGE_MODEL` | no | Fast model for alert triage (falls back to `LLM_MODEL`) |
| `LLM_RCA_MODEL` | no | Quality model for RCA and postmortem generation |
| `LLM_NARRATIVE_MODEL` | no | Model for 2–4 sentence RCA narrative |
| `INTELLIGENCE_SLACK_WEBHOOK` | no | Slack webhook for remediation gate proposals and RCA notifications |
| `KUBESENSE_CORE_URL` | no | KubeSense API proxy target (default `http://kubesense-core.aileron-agent.svc.cluster.local:8080`) |
| `MFA_ENFORCEMENT` | no | Set to `true` to enforce MFA for admin/operator roles |

### OIE Environment Variables

| Variable | Value | Notes |
|---|---|---|
| `OIE_OLLAMA_MODEL_NARRATIVE` | `qwen2.5:3b` | Only 3b loaded on cluster |
| `OIE_OLLAMA_MODEL_RCA` | `qwen2.5:3b` | |
| `OIE_OLLAMA_MODEL_TRIAGE` | `qwen2.5:3b` | |
| `OIE_ALERTHUB_BASE_URL` | `http://alerthub-backend:3000` | Topology resolve + writeback |
| `OIE_AUTO_INVESTIGATE_SEVERITIES` | `critical,high,medium` | |
| `OIE_INVESTIGATION_TIME_BUDGET_MS` | `45000` | 45 second hard budget |
| `OIE_MAX_CONCURRENT_INVESTIGATIONS` | `20` | Semaphore-limited |

---

## Kubernetes Resources

| Kind | Name | Namespace | Notes |
|---|---|---|---|
| **ArgoCD App** | `alert-engine` | `argocd` | Go backend |
| **ArgoCD App** | `sre-command-center` | `argocd` | React frontend |
| **ArgoCD App** | `alerthub-bert` | `argocd` | BERT embedding service |
| **ArgoCD App** | `alerthub-infra` | `argocd` | RCA orchestrator + Ollama |
| **Deployment** | `alerthub-backend` | `aileron` | 2 replicas |
| **Deployment** | `alerthub-frontend` | `aileron` | 2 replicas |
| **Deployment** | `alerthub-bert-service` | `aileron` | 1 replica |
| **Deployment** | `rca-orchestrator` | `aileron` | 1 replica |
| **Deployment** | `oie` | `aileron` | 1 replica — OIE evidence DAG + hypothesis engine |
| **Deployment** | `ollama` | `aileron` | 1 replica, GPU nodeSelector |
| **Deployment** | `kubesense-agent` | `aileron-agent` | 1 replica |
| **Deployment** | `kubesense-collector` | `aileron-agent` | 1 replica |
| **Deployment** | `kubesense-core` | `aileron-agent` | 1 replica |
| **Deployment** | `kubesense-api` | `aileron-agent` | 1 replica |
| **Deployment** | `kubesense-llm` | `aileron-agent` | 1 replica |
| **StatefulSet** | `postgres-primary` | `aileron` | 1 replica, pgvector extension |
| **StatefulSet** | `neo4j-0` | `aileron` | 1 replica |
| **StatefulSet** | `redis-cluster` | `aileron` | 3 replicas |
| **StatefulSet** | `kafka` | `aileron` | 3 brokers + ZooKeeper |
| **Secret** | `alerthub-secrets` | `aileron` | DB, JWT, OAuth credentials |
| **Secret** | `alerthub-ldap-credentials` | `aileron` | LDAP bind credentials |
| **Secret** | `alerthub-hcl-credentials` | `aileron` | HCL API credentials |
| **Secret** | `infrastructure-credentials` | `aileron` | CloudStack, Dynatrace tokens |
| **Secret** | `alerthub-secrets_manager-cert` | `aileron` | Cosign SecretsManager Aileron corp CA cert |
| **Secret** | `kubesense-postgres-secret` | `aileron-agent` | DATABASE_URL |
| **Secret** | `kubesense-neo4j-secret` | `aileron-agent` | NEO4J_URL, NEO4J_PASSWORD |
| **Secret** | `kubesense-llm-secret` | `aileron-agent` | CLAUDE_API_KEY (optional) |
| **Ingress** | `alerthub-ingress` | `aileron` | `aileron.example.com`, nginx, TLS |

---

## Demo Scripts

All scripts live in `scripts/` and target `http://localhost:8080` by default. Set `BASE_URL` to override.

```bash
# Send a realistic mix of Dynatrace + Prometheus alerts
./scripts/mock_alerts.sh

# Simulate a cascading infrastructure failure across multiple services
./scripts/mock_cascade_alerts.sh

# Inject chaos scenarios (split-brain, network partition, storage degraded)
./scripts/mock_chaos_tests.sh

# Fire infrastructure-specific alerts (CloudStack, NetApp, KVM host)
./scripts/mock_infra_tests.sh

# Full pipeline regression — sends 50+ alerts and validates incident creation
./scripts/test_comprehensive_v2.sh

# Live topology query test against real Neo4j + Redis graph
./scripts/test_real_topology.sh
```

---

## Deployment

### CI/CD

ArgoCD + Buildkit PreSync — images auto-build on every `master`/`main` push.

```bash
# Trigger manual sync
kubectl patch application alerthub-backend -n argocd \
  --type merge -p '{"operation":{"initiatedBy":{"username":"cli"},"sync":{"revision":"HEAD","syncStrategy":{"hook":{}}}}}'

# Watch builds
kubectl get jobs -n buildkit | grep -E "alerthub|kubesense"

# Watch pods
kubectl get pods -n aileron -w
kubectl get pods -n aileron-agent -w
```

### Secrets (via SecretsManager)

| Secret | Contents |
|---|---|
| `alerthub-app-secrets` | `DATABASE_URL`, `REDIS_URL`, `NEO4J_PASSWORD`, `KAFKA_BROKERS`, `OIDC_CLIENT_SECRET` |
| `alerthub-infra` | `NETAPP_PASSWORD`, `CLOUDSTACK_API_KEY` |
| `kubesense-postgres-secret` | `DATABASE_URL` (KubeSense PostgreSQL) |
| `kubesense-neo4j-secret` | `NEO4J_URL`, `NEO4J_PASSWORD` |
| `kubesense-llm-secret` | `CLAUDE_API_KEY` (optional, falls back to Ollama) |

---

## Monitoring Checklist

| Check | Command | Healthy When |
|---|---|---|
| OIE Kafka consumer | `kafka-consumer-groups --describe --group oie-investigation-consumer` | LAG=0 all partitions |
| KubeSense buffer | `GET /api/v1/kubesense/correlation/status` | `buffer_len > 1000` |
| Buffer feeder active | `kubectl logs -n aileron-agent kubesense-api-<pod> \| grep buffer-feeder` | "fed N new events" every 30s |
| OIE narrative model | `kubectl logs -l app=oie \| grep narrative_model` | `qwen2.5:3b` not `template` |
| Noise reduction | `total situations / buffer_len` | < 0.01 (1 situation per 100 events) |
| Investigation quality | DB query | `SELECT avg(confidence) FROM rca_investigations WHERE created_at > now()-'24h'` > 0.75 |

---

## Common Issues

| Symptom | Cause | Fix |
|---|---|---|
| `/db/health` 500 or slow | `ORDER BY occurred_at::text` — PG resolves to text column, no index | Apply `idx_ks_health_cluster_occurred (cluster_id, occurred_at DESC)` |
| OIE all "template" narratives | `qwen2.5:7b` configured but only 3b loaded | Set `ollamaModelRCA/Narrative: "qwen2.5:3b"` in values |
| Storage tab timeout | Zero storage events — full scan of 3.3M rows | 5s query timeout returns empty gracefully |
| Correlation buffer = 0 | Buffer feeder goroutine not running | Check `rca-buffer-feeder` log line in kubesense-api |
| ImagePullBackOff on new pods | Build completed after pod scheduled | Delete failing pod, K8s will retry with fresh image |
| OIE `context canceled` on health query | Column alias missing — `occurred_at::text` sort not using index | Fixed in `idx_ks_health_cluster_occurred` index |

---

## Architecture Decisions

### One Platform Not Two Products
KubeSense feeds into AlertHub's AIOps page Situations tab. Operators use one command center. The `/kubesense` route remains for deep-dive K8s intelligence but the primary experience is the unified AIOps page.

### Local LLM for Privacy + Latency
Alert payloads contain service names, IPs, error messages — data sovereignty requirement. Ollama (`qwen2.5:3b`) keeps everything in-datacenter with zero per-token cost and <2s latency.

### Evidence-First, Never Guess-First
Every OIE narrative cites specific numbered facts. The LLM cannot invent facts. If there are fewer than 3 verified grounding facts, the system returns a deterministic template. `countGroundingFacts()` is the last gate before the LLM call.

### Rule-Based + Auto-Learning Correlation
KubeSense uses explicit rules (auditable) plus pattern mining (auto-learns from production co-occurrences every 60s). Day-1 value with 8 built-in rules, continuous improvement as the system learns your cluster's failure modes.

### Static Topology Depth for RCA
K8s has a fixed hierarchy (Node > Deployment > ReplicaSet > Pod). The topology RCA scores this statically — no graph queries at scoring time — making it O(1) and deterministic. No ML training required.

---

## Documentation

| Doc | Location |
|---|---|
| **Intelligence layer** (OIE, KubeSense, policies, runbooks, MCP, postmortems, gate hooks) | `docs/INTELLIGENCE.md` |
| **Operations runbook** (health checks, common issues, diagnostics, CI/CD) | `docs/OPERATIONS.md` |
| Architecture deep-dive | `docs/architecture.md` |
| Correlation engine tuning | `docs/correlation-tuning.md` |
| Topology graph schema | `docs/topology-schema.md` |
| Alert normalization | `docs/normalization.md` |
| Runbooks | `docs/runbooks/` |
| Helm chart values | `helm/values.yaml` |
| Contributing guide | `CONTRIBUTING.md` |
| Security policy | `SECURITY.md` |

---

## Support

| Channel | Detail |
|---|---|
| **Slack** | `#help-interactive-sre` |
| **On-call** | `aileron-admins` PagerDuty rotation |
| **Issues** | `github.com/aileron-platform/aileron/issues` |
| **CODEOWNERS** | `@aileron-admins` — all PRs require approval |
| **Namespace** | `aileron` on `example-cluster` |

---

## License

Apache 2.0 — Open Source. See LICENSE.
