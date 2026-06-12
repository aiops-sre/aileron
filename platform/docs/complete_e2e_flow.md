---
  AlertHub — Complete End-to-End Flow + Failover Architecture

  ---
  Part 1: End-to-End Flow

  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  INGESTION LAYER                                                                 │
  │  Dynatrace / Prometheus / Grafana / Splunk / PagerDuty / Generic webhook POST   │
  │                                                                                  │
  │  1. Auth: SHA-256 API key check (api_keys table)                                 │
  │  2. Normalize: parse source-specific payload → models.Alert                     │
  │  3. Dedup: SELECT by source_id → update existing alert OR insert new one        │
  │  4. Enqueue: EnqueueAlert() → alertCh (2000-buffer, non-blocking)               │
  │     - If stagedPipeline is wired → StagedPipeline.Enqueue() instead            │
  │     - If channel full → DROP alert + log (backpressure shed)                   │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ goroutine per alert
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  STAGE 0 — RESOLVED CHECK  (processAlertResolvedStage)                          │
  │                                                                                  │
  │  alert.status == "resolved"?                                                    │
  │    YES → handleResolvedAlert:                                                   │
  │            UPDATE alerts SET status='resolved'                                  │
  │            Find linked incident (via alert.incident_id OR title+source match)   │
  │            HandleAlertResolved → count open alerts in ±30s burst window        │
  │            If count == 0 → UPDATE incidents SET status='resolved' (auto-close) │
  │            Add timeline event: "auto_resolved"                                  │
  │            RETURN (terminal)                                                    │
  │    NO  → publishNormalized() → Kafka "normalized-alerts" topic                 │
  │           (nil publisher = silently skip, never crashes)                        │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ continue
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  STAGE 1 — ROOT CAUSE ENGINE  (processAlertRCEStage)                            │
  │                                                                                  │
  │  rootCauseEngine.Evaluate(alert):                                               │
  │                                                                                  │
  │  Check 1: Does alert carry a Dynatrace rootCauseEntity label?                   │
  │    → Query incidents: topology_path ILIKE '%entity%'                            │
  │       OR title ILIKE '%entity%' (2h window)                                    │
  │       MATCH → ATTACH_TO_ROOT                                                    │
  │       NO MATCH → CREATE_ROOT                                                    │
  │                                                                                  │
  │  Check 2: Walk Redis graph upward from alert entity                             │
  │    → For each ancestor, query open incidents                                    │
  │       MATCH → ATTACH_TO_ROOT                                                    │
  │       NO MATCH → try next ancestor                                              │
  │                                                                                  │
  │  Check 3: InfraLevel ≥ InfraLevelVM AND has blast_radius?                      │
  │    YES → CREATE_ROOT                                                            │
  │                                                                                  │
  │  Result:                                                                        │
  │    ATTACH_TO_ROOT:                                                              │
  │      mergeAlertIntoIncident(alert, rootIncidentID, score=1.0)                  │
  │      SetAlertState(Redis) = ATTACHED                                            │
  │      saveCorrelationResult (async)                                              │
  │      RETURN true (short-circuit — skip Stage 2)                                │
  │                                                                                  │
  │    CREATE_ROOT:                                                                  │
  │      createRootIncident → createIncident path (see Stage 3)                    │
  │      suppressDescendantAlerts (Redis, 30min lookback, by entity labels)        │
  │      triggerRCA (async)                                                         │
  │      RETURN true (short-circuit)                                                │
  │                                                                                  │
  │    NO_ROOT: return false → continue to Stage 2                                  │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ only if NO_ROOT
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  STAGE 2 — V2 ONTOLOGY CLASSIFICATION  (processAlertFullStage, pre-scoring)    │
  │                                                                                  │
  │  OntologyEngine.Classify(alert):                                                │
  │    Map title/description keywords → FailureDomain + CanonicalFailureClass       │
  │    7 domains: storage, network, compute, kubernetes, application, database,     │
  │               security                                                          │
  │    22 classes: e.g. storage.exhaustion, kubernetes.pod_crash, network.timeout  │
  │                                                                                  │
  │  AdaptiveLearningEngine.GetWeightsForContext(alert):                            │
  │    Key = (domain, source, cluster) → EMA-tuned strategy weights from           │
  │    correlation_feature_store table                                              │
  │    → Override ParallelEngine strategy weights (default: T:0.35 S:0.25 Te:0.25 R:0.15) │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  STAGE 3 — PARALLEL 4-STRATEGY SCORING  (30s timeout)                          │
  │                                                                                  │
  │  4 goroutines run concurrently:                                                 │
  │                                                                                  │
  │  ┌──────────────────────────────────────────────────────────────────────────┐   │
  │  │  SEMANTIC STRATEGY (weight 0.35)                                         │   │
  │  │  BERT embedding (768-dim) → Weaviate ANN query                          │   │
  │  │  cosine similarity > 0.75, 24h lookback                                 │   │
  │  │  FAILOVER: Weaviate CB open → Levenshtein + Jaccard on description      │   │
  │  │  FAILOVER: BERT CB open → skip embedding, pure text matching            │   │
  │  └──────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                  │
  │  ┌──────────────────────────────────────────────────────────────────────────┐   │
  │  │  temp 0.10)                                         │   │
  │  │  Query alerts from last 2h                                              │   │
  │  │  score = exp(-0.1 * (diff_min / 30))                                   │   │
  │  │  Bonuses: +0.10 same severity, +0.05 same source                       │   │
  │  │  Threshold: score ≥ 0.60                                               │   │
  │  └──────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                  │
  │  ┌──────────────────────────────────────────────────────────────────────────┐   │
  │  │  TOPOLOGY STRATEGY (weight 0.25)                                         │   │
  │  │  Priority 1: Redis graph (TopologyGraphCorrelator)                      │   │
  │  │    → findMatchingNode (label_exact > ip_match > pod_name > cluster_name) │  │
  │  │    → walkAncestors (up to 6 levels)                                     │   │
  │  │    → computeBlastRadius (BFS, max 2000 nodes)                           │   │
  │  │    → proximityScore:                                                     │   │
  │  │        same node = 1.0, parent/child = 0.92, siblings = 0.85,          │   │
  │  │        grandparent = 0.72, within 4 hops = 0.65                        │   │
  │  │    → nodesWithActiveAlerts (2h window) → RootCauseScore                │   │
  │  │  FAILOVER: Redis graph empty → Neo4j Cypher (EnhancedTopologyCorrelator) │  │
  │  │  FAILOVER: Neo4j CB open → string matching (label contains cluster/node) │  │
  │  └──────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                  │
  │  ┌──────────────────────────────────────────────────────────────────────────┐   │
  │  │  rule 0.25)                                            │   │
  │  │  Load rules from DB (max 1000, 5-min cache)                             │   │
  │  │  Operators: regex, contains, equals, gt, lt, in, exists,               │   │
  │  │             starts_with, ends_with                                       │   │
  │  │  Priority multiplier: 200+:0.98, 150+:0.93, 100+:0.88, 50+:0.75       │   │
  │  └──────────────────────────────────────────────────────────────────────────┘   │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ collect all 4 results
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  AGGREGATOR  (CorrelationAggregatorService)                                     │
  │                                                                                  │
  │  composite = Σ(strategy_score × weight)                                        │
  │  text_overlap = title/description Jaccard score                                 │
  │  final_score = 0.70 × composite + 0.30 × text_overlap                         │
  │                                                                                  │
  │  Find candidate incidents:                                                      │
  │    WHERE cluster+domain+path match, status=open, created ≥ 2h ago              │
  │                                                                                  │
  │  Signal-based decision rules:                                                   │
  │    topology ≥ 0.60                    → CREATE_INCIDENT or MERGE               │
  │    ≥2 strategies > 0.50              → CREATE_INCIDENT or MERGE                │
  │    final_score ≥ 0.40               → CREATE_INCIDENT or MERGE                 │
  │    0.20 ≤ final_score < 0.40        → MONITOR                                  │
  │    final_score < 0.20               → DISCARD                                  │
  │                                                                                  │
  │  Publish to Kafka "correlation-results" (if publisher wired)                   │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  DECISION EXECUTION                                                             │
  │                                                                                  │
  │  CREATE_INCIDENT:                                                               │
  │    17-step deduplication cascade (in order, each returns early if match found):       │
  │    1. inflightDedup sync.Map — same dedupKey already running? Poll 3s (15x     │
  │       200ms) for the racing goroutine's incident, then merge into it           │
  │    2. acquireClusterLock — per-cluster sync.Mutex, serializes creation         │
  │    3. 5-min title+source burst (tight race guard)                              │
  │    4. 2h entity_id match (Dynatrace same problem)                              │
  │    5. 6h cluster+problemDomain match                                           │
  │    6. 30-min cluster cascade (node-down storm: any source)                    │
  │    7. 2h infra causal (topology_path contains node+cluster)                    │
  │    8. Topology cache lookup (workload rescheduled to new node)                 │
  │    9. 30-min fingerprint / title+severity+source match                         │
  │   10. 2h topology-based merge (matched_label in topology_path)                 │
  │   11. → All checks passed: INSERT new incident                                 │
  │                                                                                  │
  │       incident = {title (topology-enriched), description (markdown),           │
  │                   blast_radius[], topology_path, correlation_confidence,        │
  │                   dominant_strategy, rca_status="queued"}                      │
  │       InternalCreateIncident (bypass RBAC)                                     │
  │       UPDATE alerts SET incident_id = $incidentID                              │
  │       async: triggerRCA (POST /api/v1/investigations, 10s timeout)            │
  │       async: enrichIncidentV2 (30s timeout):                                   │
  │           1. UPDATE incidents SET ontology_domain                              │
  │           2. ProbabilisticRCA.Evaluate → write rca_hypotheses (JSON)          │
  │           3. RecursiveTopoRCA.Traverse → write topo_root_entity_id,           │
  │              causal_chain (JSON)                                               │
  │           4. Regenerate ExplainabilityReport → UPDATE pipeline_correlation_    │
  │              results.explanation_json                                          │
  │       async: BERT embedding → VectorRepository.StoreEmbedding (pgvector)      │
  │                                                                                  │
  │  MERGE_INCIDENT:                                                               │
  │    mergeAlertIntoIncident:                                                     │
  │      UPDATE incidents SET alert_ids = alert_ids || [$alertID]                  │
  │      UPDATE alerts SET incident_id, correlation_confidence                     │
  │      maybePromoteToRoot: if alertInfraLevel > current root_level →             │
  │        UPDATE incidents davis_ai_analysis with new root entity                 │
  │      AddTimelineEvent("alert_added", ...)                                      │
  │      async: triggerRCA                                                         │
  │                                                                                  │
  │  MONITOR (deferred):                                                           │
  │    topoScore ≥ 0.5 && BestMatch exists → immediate MERGE (skip hold)          │
  │    else:                                                                       │
  │      holdWindow = 45s (critical = 15s)                                        │
  │      deferredIncidentCreation goroutine:                                       │
  │        sleep(holdWindow)                                                       │
  │        Check: was alert linked during hold? YES → return                       │
  │        Re-run aggregator with fresh DB state                                   │
  │        If MERGE → merge                                                        │
  │        findOpenIncidentForAlert (broad cluster+title/domain/cascade 30min)    │
  │        If found → merge                                                        │
  │        else → createIncident                                                   │
  │                                                                                  │
  │  DISCARD:                                                                      │
  │    findOpenIncidentForAlert (last-ditch):                                      │
  │      cluster+title (6h) → cluster+domain (6h) → cluster-only (30min)          │
  │    MATCH → attachAlertToIncident                                               │
  │    NO MATCH → log "discarded" (alert stays unlinked)                           │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ always async after any path
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  PERSISTENCE                                                                    │
  │  saveCorrelationResult → INSERT into pipeline_correlation_results:             │
  │    alert_id, incident_id, decision, final_score, all 4 strategy scores,       │
  │    matched_node_label, root_cause_label, ontology_domain, ontology_class,      │
  │    topo_root_entity, blast_radius_count, explanation_json, elapsed_ms          │
  │                                                                                  │
  │  SetAlertState(Redis):                                                          │
  │    incidentID set → INCIDENT_CREATED                                           │
  │    no incidentID  → BUFFERED                                                   │
  │    TTL: 72h                                                                    │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ background (every 30s)
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  BUFFER PROMOTION LOOP  (runBufferPromotionLoop, every 30s)                    │
  │                                                                                  │
  │  Query: alerts WHERE incident_id IS NULL AND status='open'                     │
  │         AND created_at < NOW()-60s AND created_at >= NOW()-2h                  │
  │  LIMIT 50                                                                      │
  │                                                                                  │
  │  For each:                                                                     │
  │    Check Redis state: already ATTACHED/INCIDENT_CREATED/SUPPRESSED? skip      │
  │    Re-check DB: incident_id set by concurrent worker? skip                    │
  │    else → createIncident (score=0.3, decision=CREATE) — "no alert missed"     │
  └───────────────────────────────────────┬─────────────────────────────────────────┘
                                          │ background (every 30s)
                                          ▼
  ┌─────────────────────────────────────────────────────────────────────────────────┐
  │  INCIDENT EVOLUTION ENGINE  (V2, every 30s)                                    │
  │                                                                                  │
  │  For each open incident:                                                       │
  │    Merge: find incidents with overlapping blast_radius/topology_path           │
  │    Split: if alert set spans radically different domains → fork incident        │
  │    Escalate: blast_radius growing → bump severity/priority                     │
  │    Auto-close: all alerts resolved? UPDATE status='resolved'                   │
  │    Update: causal_chain, rca_hypotheses, evolution_generation++                │
  └─────────────────────────────────────────────────────────────────────────────────┘

  ---
  Part 2: Failover & Resilience — Every Layer

  Circuit Breakers (per-service, circuit_breaker.go)

  ┌──────────────────┬────────────┬───────────────┬────────────────────────────────────────────────────────────────────┐
  │     Service      │ Threshold  │ Reset Timeout │                        Behaviour when OPEN                         │
  ├──────────────────┼────────────┼───────────────┼────────────────────────────────────────────────────────────────────┤
  │ Neo4j            │ 5 failures │ 30s           │ RecursiveTopoRCA.Traverse falls back to Redis graph                │
  ├──────────────────┼────────────┼───────────────┼────────────────────────────────────────────────────────────────────┤
  │ Weaviate         │ 5 failures │ 30s           │ Semantic strategy falls back to Levenshtein+Jaccard                │
  ├──────────────────┼────────────┼───────────────┼────────────────────────────────────────────────────────────────────┤
  │ BERT             │ 3 failures │ 20s           │ No embedding; semantic strategy runs text-only                     │
  ├──────────────────┼────────────┼───────────────┼────────────────────────────────────────────────────────────────────┤
  │ RCA Orchestrator │ 3 failures │ 60s           │ triggerRCA silently skips (logged); incident still created         │
  ├──────────────────┼────────────┼───────────────┼────────────────────────────────────────────────────────────────────┤
  │ Ollama           │ 3 failures │ 30s           │ LLM enrichment skipped; incident title/desc uses template fallback │
  └──────────────────┴────────────┴───────────────┴────────────────────────────────────────────────────────────────────┘

  HalfOpen recovery: 3 consecutive successes → Closed.

  ---
  Topology — Exact 3-tier Failover

  Alert arrives with entity_id or labels
          │
          ▼
  [Priority 1] Redis Graph (TopologyGraphCorrelator)
    provider.GetNodes() → in-memory snapshot
    findMatchingNode: label_exact → ip_match → pod_name → namespace → cluster → label_fuzzy
    walkAncestors (up to 6 hops), computeBlastRadius (BFS, cap 2000)
    proximityScore: parent/child=0.92, sibling=0.85, grandparent=0.72, 4-hop=0.65
          │
          │ if len(nodes) == 0 (Redis graph empty / no topology loaded)
          ▼
  [Priority 2] Neo4j Cypher (RecursiveTopoRCAEngine + EnhancedTopologyCorrelator)
    upwardSweep: MATCH path = (root)-[HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..8]->(target)
                 attenuated by domain decay (storage:0.90, network:0.82, compute:0.88)
                 path_score × pow(decay, depth) > 0.20
    downwardSweep: root → all affected (depth ≤ 10, score > 0.15)
    lateralSweep: parent's siblings (score=0.65)
    In-memory TTL cache: 2-minute per entity_id
          │
          │ if Neo4j CB is Open OR connection error
          ▼
  [Priority 3] String Matching (last resort in EnhancedTopologyCorrelator)
    Does topology_path contain cluster/node label from alert labels?
    Does incident title ILIKE '%hostname%'?
    Score: 0.50–0.70 depending on match quality

  ---
  Kafka — Graceful Degradation

  Both normalizedPublisher and correlationPublisher are optional pointers. If nil (Kafka unavailable or not configured):

  // from alert_pipeline.go:383
  func (s *AlertPipelineService) publishNormalized(alert *models.Alert) {
      if s.normalizedPublisher == nil {
          return   // silently skip
      }
      ...
  }

  The pipeline never blocks on Kafka. Alert processing continues 100% without it — Kafka is analytics/observability only, not on the critical path.

  ---
  Alert Channel Backpressure

  // alertCh has 2000-buffer; non-blocking enqueue:
  select {
  case s.alertCh <- alert:
  default:
      log.Printf("⚠️ Alert pipeline queue full, dropping alert %s", alert.ID)
  }

  Under extreme load, alerts are shed at the channel. The 30s buffer promotion loop then recovers any alert already persisted to DB that didn't get processed (queries incident_id IS NULL), so even shed alerts
  eventually get an incident created.

  ---
  Race Condition Protection (concurrent alert storms)

  Scenario: 40 pod alerts for the same cluster arrive simultaneously,
            all see no open incident, all try to createIncident

  Guard 1 — inflightDedup (sync.Map):
    First goroutine to load "titlekey|source|severity|cluster" wins the lock.
    Others spin-poll DB for 3s (15 × 200ms) waiting for the winner's incident.
    Winner creates; others merge.

  Guard 2 — clusterLocks (sync.Map per cluster):
    Serializes createIncident per cluster.
    Goroutine 2 runs dedup checks AFTER goroutine 1's incident exists → merges.

  Guard 3 — 5-min title+source dedup query:
    Belt-and-suspenders SQL check INSIDE the lock.

  ---
  Root Promotion (entity-level failover)

  If a Pod alert arrives first (InfraLevel=1) and creates an incident, then the Node alert (InfraLevel=4) arrives and merges in → maybePromoteToRoot fires:

  alertInfraLevel(nodeAlert) = InfraLevelNode (4)
  current root_level on incident = InfraLevelPod (1)
  4 > 1 → UPDATE incidents SET davis_ai_analysis->{root_entity_id, root_entity_label, root_level}

  The incident root automatically climbs up the infra hierarchy as higher-signal alerts arrive — no manual intervention needed.

  ---
  RCA Orchestrator Failure

  triggerRCA uses a 10s HTTP timeout. On failure:
  - Incident is still fully created
  - rca_status stays "queued" (not updated to "investigating")
  - Failure is logged; no retry (fire-and-forget)
  - Operators can manually re-trigger from the RCAInvestigationPage

  ---
  BERT Embedding Failure (V2)

  // Best-effort, never blocks the pipeline:
  go func(a *models.Alert, onto *correlation.OntologyResult) {
      if emb, err := fetchBERTEmbedding(bertURL, ...); err == nil {
          _ = s.vectorRepo.StoreEmbedding(...)
      }
      // error → silently dropped
  }(alert, ontologyResult)

  5-second HTTP timeout on BERT. If it fails, the pgvector store just doesn't get this embedding. Semantic correlation falls back to Levenshtein until BERT recovers.

  ---
  TL;DR on failover: Every external dependency (Neo4j, Weaviate, BERT, Kafka, RCA, Ollama) has a circuit breaker or nil-safe optional path. Topology has explicit 3-tier fallover: Redis → Neo4j → string matching.
  Race conditions have 2 in-memory guards + 10 SQL dedup queries. Shed alerts are recovered by the 30s buffer promotion loop. Nothing on the external dependencies is on the critical path to incident creation.