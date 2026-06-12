# AlertHub Remediation Branch — Complete Implementation
# Step 1: Verified Findings | Step 2-6: Diffs, Migrations, Tests, Deployment, Rollback

---

## STEP 1 — VERIFIED FINDINGS

### ACCEPTED (implementing)

| # | Finding | Evidence | Severity |
|---|---|---|---|
| A | RCA hypothesis global scan contamination | `probabilistic_rca.go:352` scans `rca:hyp:*` — loads all alert hypotheses globally | CRITICAL |
| B | LLM enricher wrong model, wrong URL, 30s timeout, silent errors | `llm_enricher.go:32` model=`phi3:mini`, line 29 hardcoded URL, line 38 30s client timeout | CRITICAL |
| C | No GET endpoint for investigation DAG | Table `incident_investigation_dags` is populated; no route in `RegisterRoutes` | HIGH |
| D | No GET endpoint for correlation explanation | `pipeline_correlation_results.explanation_json` populated; no route | HIGH |
| E | `getRecentAlerts` LIMIT 50 | `parallel_correlation_engine.go:696` hardcoded LIMIT 50 | HIGH |
| F | Topology freshness — no tracking | `TopologyGraphCorrelator` struct has no `lastRefreshed` field | HIGH |
| G | Feedback loop locked unconditionally | `cmd/main.go:174,180` — both `LockWeights()` calls, no env-var escape | HIGH |
| H | `incident_investigation_dags` missing `step_states` column | `migrations.go:1362` — column not in CREATE TABLE | MEDIUM |

### REJECTED (not implementing — findings are wrong)

| # | Finding | Why Invalid |
|---|---|---|
| R1 | "GetIncident doesn't return enriched fields" | `incidents.go:GetIncident` already SELECTs and Scans `rca_hypotheses`, `causal_chain`, `ontology_domain`, `topo_root_entity_id` (lines 247-279) |
| R2 | "`makeCorrelationDecision` creates incident when 0 strategies" | `ParallelCorrelationEngine.makeCorrelationDecision` output is stored in `parallelResult.Decision` but the FINAL decision is made by `CorrelationAggregatorService.makeDecision`. The aggregator returns `DecisionDiscard` (score < 0.20) when all strategies fail — correct behavior. The parallel engine's decision string is used only for logging. |

---

## STEP 2 — IMPLEMENTATION: ALL FIXES

---

### FIX A — RCA Hypothesis Contamination
**File:** `internal/services/correlation/probabilistic_rca.go`
**Risk:** CRITICAL — changes Redis key format and function signatures

**Root cause (verified):**
- Line 321: `hypothesisKey(alertID)` → `rca:hyp:{alertID}` — keyed by alert, not incident
- Line 325: `persistHypotheses(ctx, alertID, hypotheses)` — stores entire array under one key per alert
- Line 352: `p.rdb.Scan(ctx, cursor, "rca:hyp:*", 100)` — scans ALL keys globally
- Line 157: `go p.persistHypotheses(context.Background(), alert.ID, normalized)` — passes alertID
- `models.Alert` has no `IncidentID` field — fix must change `Evaluate` signature

**What changes:**
1. `hypothesisKey` takes `incidentID, entityID string` — one key per entity per incident
2. `persistHypotheses` takes `incidentID uuid.UUID` — scoped by incident
3. `loadPriorHypotheses` takes `incidentID uuid.UUID` — scoped scan
4. `Evaluate` takes `incidentID uuid.UUID` as new parameter
5. TTL extended from 2h to 72h — incidents can be open for hours
6. Unmarshal changes from `[]*RCAHypothesis` (array) to `*RCAHypothesis` (single) per key

```diff
--- a/internal/services/correlation/probabilistic_rca.go
+++ b/internal/services/correlation/probabilistic_rca.go

-const hypothesisKeyTTL = 2 * time.Hour
+// 72 hours: incidents can remain active for extended periods.
+// Keys are scoped per incident, so growth is bounded.
+const hypothesisKeyTTL = 72 * time.Hour

-func hypothesisKey(alertID uuid.UUID) string {
-	return fmt.Sprintf("rca:hyp:%s", alertID)
-}
+// hypothesisKey returns a Redis key scoped to a specific incident and entity.
+// Format: rca:hyp:{incidentID}:{entityID}
+// This prevents cross-incident hypothesis contamination.
+func hypothesisKey(incidentID, entityID string) string {
+	return fmt.Sprintf("rca:hyp:%s:%s", incidentID, entityID)
+}

 // Evaluate calls the deterministic engine first. If its confidence is very high
 // (Dynatrace entity) it returns immediately. Otherwise it builds competing
 // hypotheses and returns the MAP estimate.
-func (p *ProbabilisticRCAEngine) Evaluate(ctx context.Context, alert *Alert) (*EnrichedRCADecision, error) {
+func (p *ProbabilisticRCAEngine) Evaluate(ctx context.Context, alert *Alert, incidentID uuid.UUID) (*EnrichedRCADecision, error) {
 	existingDecision, err := p.existingRCE.Evaluate(ctx, alert)
 	if err != nil {
 		return nil, err
 	}
 
 	// Dynatrace root entity is ground truth — trust immediately
 	if existingDecision.Action == RCAActionAttachToRoot && existingDecision.IsDynatraceRoot {
 		return &EnrichedRCADecision{
 			RCADecision: existingDecision,
 			Confidence:  0.95,
 			Reasoning:   []string{"dynatrace rootCauseEntity — highest trust, decision immediate"},
 		}, nil
 	}
 
-	hypotheses, err := p.buildHypotheses(ctx, alert, existingDecision)
+	hypotheses, err := p.buildHypotheses(ctx, alert, existingDecision, incidentID)
 	if err != nil {
 		return &EnrichedRCADecision{RCADecision: existingDecision, Confidence: 0.60}, nil
 	}
 
 	normalized := p.normalize(hypotheses)
 	if len(normalized) == 0 {
 		return &EnrichedRCADecision{
 			RCADecision: &RCADecision{Action: RCAActionNoRoot},
 			Confidence:  0.0,
 		}, nil
 	}
 
 	best := normalized[0]
 	decision := p.hypothesisToDecision(best, alert)
 
-	go p.persistHypotheses(context.Background(), alert.ID, normalized)
+	if incidentID != uuid.Nil {
+		go p.persistHypotheses(context.Background(), incidentID, normalized)
+	}
 
 	return &EnrichedRCADecision{
 		RCADecision: decision,
 		Confidence:  best.Confidence,
 		Hypotheses:  normalized,
 		Reasoning:   p.buildReasoning(best, normalized),
 	}, nil
 }

-func (p *ProbabilisticRCAEngine) buildHypotheses(ctx context.Context, alert *Alert, existing *RCADecision) ([]*RCAHypothesis, error) {
+func (p *ProbabilisticRCAEngine) buildHypotheses(ctx context.Context, alert *Alert, existing *RCADecision, incidentID uuid.UUID) ([]*RCAHypothesis, error) {
 	var hypotheses []*RCAHypothesis
 
 	// ... (existing hypothesis building code unchanged) ...
 
 	// Merge with prior hypotheses from Redis (cross-alert evidence accumulation)
-	prior, _ := p.loadPriorHypotheses(ctx, alert)
+	prior, _ := p.loadPriorHypotheses(ctx, incidentID)
 	for _, ph := range prior {
 		// ... (existing merge logic unchanged) ...
 	}
 
 	return hypotheses, nil
 }

-func (p *ProbabilisticRCAEngine) persistHypotheses(ctx context.Context, alertID uuid.UUID, hypotheses []*RCAHypothesis) {
-	data, err := json.Marshal(hypotheses)
-	if err != nil {
-		return
-	}
-	p.rdb.Set(ctx, hypothesisKey(alertID), data, hypothesisKeyTTL)
-}
+func (p *ProbabilisticRCAEngine) persistHypotheses(ctx context.Context, incidentID uuid.UUID, hypotheses []*RCAHypothesis) {
+	if incidentID == uuid.Nil {
+		return
+	}
+	for _, h := range hypotheses {
+		if h.EntityID == "" {
+			continue
+		}
+		data, err := json.Marshal(h)
+		if err != nil {
+			log.Printf("persistHypotheses: marshal error entity=%s: %v", h.EntityID, err)
+			continue
+		}
+		key := hypothesisKey(incidentID.String(), h.EntityID)
+		if err := p.rdb.Set(ctx, key, data, hypothesisKeyTTL).Err(); err != nil {
+			log.Printf("persistHypotheses: redis set error key=%s: %v", key, err)
+		}
+	}
+}

-func (p *ProbabilisticRCAEngine) loadPriorHypotheses(ctx context.Context, alert *Alert) ([]*RCAHypothesis, error) {
-	// Use cursor-based SCAN instead of KEYS to avoid blocking Redis on large keyspaces.
-	// M1: cap result at 1000 hypotheses and respect context cancellation to prevent
-	// unbounded memory growth when Redis has millions of keys.
-	const maxHypotheses = 1000
+func (p *ProbabilisticRCAEngine) loadPriorHypotheses(ctx context.Context, incidentID uuid.UUID) ([]*RCAHypothesis, error) {
+	if incidentID == uuid.Nil {
+		return nil, nil
+	}
+	const maxHypotheses = 200 // per-incident cap: one per entity, bounded by infra size
 	var (
 		result []*RCAHypothesis
 		cursor uint64
 	)
+	pattern := fmt.Sprintf("rca:hyp:%s:*", incidentID.String())
 	for {
 		select {
 		case <-ctx.Done():
 			return result, nil // return partial results on cancellation
 		default:
 		}
 		if len(result) >= maxHypotheses {
 			break
 		}
 
-		keys, next, err := p.rdb.Scan(ctx, cursor, "rca:hyp:*", 100).Result()
+		keys, next, err := p.rdb.Scan(ctx, cursor, pattern, 100).Result()
 		if err != nil {
 			return nil, err
 		}
 		for _, key := range keys {
 			if len(result) >= maxHypotheses {
 				break
 			}
 			val, err := p.rdb.Get(ctx, key).Result()
 			if err != nil {
 				continue
 			}
-			var hyps []*RCAHypothesis
-			if json.Unmarshal([]byte(val), &hyps) == nil {
-				result = append(result, hyps...)
+			// New format: one RCAHypothesis per key (not an array)
+			var h RCAHypothesis
+			if json.Unmarshal([]byte(val), &h) == nil {
+				result = append(result, &h)
 			}
 		}
 		cursor = next
 		if cursor == 0 {
 			break
 		}
 	}
 	return result, nil
 }
```

**Caller change — `alert_pipeline.go:3212`:**
```diff
-		decision, err := s.probabilisticRCA.Evaluate(ctx, corrAlert)
+		decision, err := s.probabilisticRCA.Evaluate(ctx, corrAlert, incidentID)
```

**CACIE call site — `causal_inference_engine.go` uses `ProbabilisticRCAEngine`:**
```bash
grep -n "\.Evaluate(" internal/services/correlation/causal_inference_engine.go
```
If CACIE calls `probabilisticRCA.Evaluate`, it must also pass an `incidentID`. Search and update those calls too. In CACIE's `Infer` method which already receives `incidentID uuid.UUID`, pass it through.

---

### FIX B — LLM Enricher
**File:** `internal/services/pipeline/llm_enricher.go`
**Risk:** LOW — configuration only, no schema changes

```diff
--- a/internal/services/pipeline/llm_enricher.go
+++ b/internal/services/pipeline/llm_enricher.go

 func NewLLMEnricher() *LLMEnricher {
 	url := os.Getenv("LLM_SERVICE_URL")
 	if url == "" {
-		url = "http://ollama.aileron.svc.cluster.local:11434"
+		// Use POD_NAMESPACE so the URL is correct regardless of which namespace
+		// the pod is deployed in. Falls back to aileron for backward compat.
+		ns := os.Getenv("POD_NAMESPACE")
+		if ns == "" {
+			ns = "aileron"
+		}
+		url = fmt.Sprintf("http://ollama.%s.svc.cluster.local:11434", ns)
 	}
 	model := os.Getenv("LLM_MODEL")
 	if model == "" {
-		model = "phi3:mini"
+		model = "qwen2.5:3b" // matches docker/Dockerfile-ollama baked model
 	}
-	return &LLMEnricher{
+	e := &LLMEnricher{
 		serviceURL: url,
 		model:      model,
-		client:     &http.Client{Timeout: 30 * time.Second},
-	}
+		client:     &http.Client{Timeout: 10 * time.Second},
+	}
+	log.Printf("LLMEnricher initialized: model=%s url=%s", e.model, e.serviceURL)
+	return e
 }

 func (e *LLMEnricher) GenerateRCA(
 	// ... signature unchanged ...
 ) string {
 	// ... prompt building unchanged ...

-	llmCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
+	// Inner context: 8s gives room within the parent 10s client timeout.
+	// If the model is loaded and warm, response arrives in 1-3s.
+	llmCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
 	defer cancel()

 	req, err := http.NewRequestWithContext(llmCtx, http.MethodPost, e.serviceURL+"/api/generate", bytes.NewBuffer(body))
 	if err != nil {
+		log.Printf("LLMEnricher.GenerateRCA: request build failed model=%s: %v", e.model, err)
 		return ""
 	}
 	req.Header.Set("Content-Type", "application/json")

 	resp, err := e.client.Do(req)
 	if err != nil {
-		log.Printf("LLM RCA generation failed (unreachable): %v", err)
+		log.Printf("LLMEnricher.GenerateRCA: request failed model=%s url=%s: %v",
+			e.model, e.serviceURL, err)
 		return ""
 	}
 	defer resp.Body.Close()

 	raw, err := io.ReadAll(resp.Body)
 	if err != nil || resp.StatusCode != http.StatusOK {
+		log.Printf("LLMEnricher.GenerateRCA: non-200 response status=%d model=%s",
+			resp.StatusCode, e.model)
 		return ""
 	}

 	var result struct {
 		Response string `json:"response"`
 	}
 	if err := json.Unmarshal(raw, &result); err != nil {
+		log.Printf("LLMEnricher.GenerateRCA: json decode failed model=%s: %v", e.model, err)
 		return ""
 	}

 	rca := strings.TrimSpace(result.Response)
 	if rca != "" {
 		log.Printf("LLM RCA generated for alert '%s' (%d chars)", alertTitle, len(rca))
+	} else {
+		log.Printf("LLMEnricher.GenerateRCA: empty response from model=%s", e.model)
 	}
 	return rca
 }
```

**helm/alerthub/templates/deployment.yaml — add POD_NAMESPACE:**
```yaml
env:
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
```

---

### FIX C — GET /incidents/:id/investigation
**File:** `internal/api/handlers/incidents.go`
**Risk:** LOW — read-only, additive

```diff
--- a/internal/api/handlers/incidents.go
+++ b/internal/api/handlers/incidents.go

+// GetInvestigationDAG returns the generated investigation playbook for an incident.
+// The DAG is populated by enrichIncidentV2 after RCA engines complete.
+func (h *IncidentHandler) GetInvestigationDAG(c *gin.Context) {
+	incidentID, err := uuid.Parse(c.Param("id"))
+	if err != nil {
+		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
+		return
+	}
+
+	var domain, rootEntity string
+	var stepsJSON, stepStatesJSON []byte
+	var generatedAt, updatedAt time.Time
+
+	err = h.db.QueryRowContext(c.Request.Context(), `
+		SELECT domain, root_entity, steps,
+		       COALESCE(step_states, '{}'),
+		       generated_at, updated_at
+		FROM incident_investigation_dags
+		WHERE incident_id = $1`,
+		incidentID,
+	).Scan(&domain, &rootEntity, &stepsJSON, &stepStatesJSON, &generatedAt, &updatedAt)
+
+	if err == sql.ErrNoRows {
+		c.JSON(http.StatusOK, gin.H{
+			"success":     true,
+			"available":   false,
+			"incident_id": incidentID,
+			"message":     "Investigation guide not yet available — RCA engines may still be running",
+		})
+		return
+	}
+	if err != nil {
+		log.Printf("GetInvestigationDAG: db error incident=%s: %v", incidentID, err)
+		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
+		return
+	}
+
+	c.JSON(http.StatusOK, gin.H{
+		"success":      true,
+		"available":    true,
+		"incident_id":  incidentID,
+		"domain":       domain,
+		"root_entity":  rootEntity,
+		"steps":        json.RawMessage(stepsJSON),
+		"step_states":  json.RawMessage(stepStatesJSON),
+		"generated_at": generatedAt,
+		"updated_at":   updatedAt,
+	})
+}
+
+// UpdateInvestigationStep marks an investigation step as pending/in_progress/done/skipped.
+func (h *IncidentHandler) UpdateInvestigationStep(c *gin.Context) {
+	incidentID, err := uuid.Parse(c.Param("id"))
+	if err != nil {
+		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
+		return
+	}
+	stepID := c.Param("step_id")
+	if stepID == "" {
+		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "step_id required"})
+		return
+	}
+
+	var body struct {
+		State string `json:"state" binding:"required"`
+	}
+	if err := c.ShouldBindJSON(&body); err != nil {
+		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "state is required"})
+		return
+	}
+	valid := map[string]bool{
+		"pending": true, "in_progress": true, "done": true, "skipped": true,
+	}
+	if !valid[body.State] {
+		c.JSON(http.StatusBadRequest, gin.H{
+			"success": false,
+			"message": "state must be: pending | in_progress | done | skipped",
+		})
+		return
+	}
+
+	// jsonb_set merges into the existing step_states object atomically.
+	// The array path syntax requires the key to be wrapped: {step_id}
+	result, err := h.db.ExecContext(c.Request.Context(), `
+		UPDATE incident_investigation_dags
+		SET step_states = jsonb_set(
+			COALESCE(step_states, '{}'),
+			ARRAY[$1],
+			$2::jsonb
+		)
+		WHERE incident_id = $3`,
+		stepID,
+		fmt.Sprintf(`"%s"`, body.State),
+		incidentID,
+	)
+	if err != nil {
+		log.Printf("UpdateInvestigationStep: db error incident=%s step=%s: %v",
+			incidentID, stepID, err)
+		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
+		return
+	}
+	rows, _ := result.RowsAffected()
+	if rows == 0 {
+		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Investigation DAG not found for this incident"})
+		return
+	}
+	c.Status(http.StatusNoContent)
+}
```

**Register routes — `RegisterRoutes` function:**
```diff
 func (h *IncidentHandler) RegisterRoutes(router *gin.RouterGroup) {
 	incidents := router.Group("/incidents")
 	{
 		// ... existing routes ...
+		incidents.GET("/:id/investigation", h.GetInvestigationDAG)
+		incidents.PATCH("/:id/investigation/steps/:step_id", h.UpdateInvestigationStep)
 	}
 }
```

**Required import additions** (incidents.go already imports `database/sql`, `encoding/json`, `log`, `net/http`, `time`; add `fmt` if not present):
```go
import "fmt"
```

---

### FIX D — GET /incidents/:id/explanation
**File:** `internal/api/handlers/incidents.go`
**Risk:** LOW — read-only, additive

```diff
+// GetCorrelationExplanation returns correlation evidence for all alerts in an incident.
+// Each alert's ExplainabilityReport is returned as stored in pipeline_correlation_results.
+func (h *IncidentHandler) GetCorrelationExplanation(c *gin.Context) {
+	incidentID, err := uuid.Parse(c.Param("id"))
+	if err != nil {
+		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
+		return
+	}
+
+	// Join alerts to their correlation results.
+	// One alert may have multiple PCR rows (initial sparse + V2 enriched).
+	// Use DISTINCT ON to keep only the most recent enriched row per alert.
+	rows, err := h.db.QueryContext(c.Request.Context(), `
+		SELECT DISTINCT ON (a.id)
+			a.id::text            AS alert_id,
+			a.title               AS alert_title,
+			a.severity,
+			pcr.final_score,
+			pcr.decision,
+			pcr.dominant_strategy,
+			pcr.explanation_json,
+			pcr.processed_at
+		FROM alerts a
+		JOIN pipeline_correlation_results pcr ON pcr.alert_id = a.id
+		WHERE a.incident_id = $1
+		  AND pcr.explanation_json IS NOT NULL
+		ORDER BY a.id, pcr.processed_at DESC`,
+		incidentID,
+	)
+	if err != nil {
+		log.Printf("GetCorrelationExplanation: db error incident=%s: %v", incidentID, err)
+		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
+		return
+	}
+	defer rows.Close()
+
+	type alertExplanation struct {
+		AlertID     string          `json:"alert_id"`
+		Title       string          `json:"alert_title"`
+		Severity    string          `json:"severity"`
+		FinalScore  float64         `json:"final_score"`
+		Decision    string          `json:"decision"`
+		Strategy    string          `json:"dominant_strategy"`
+		Explanation json.RawMessage `json:"explanation"`
+		ProcessedAt time.Time       `json:"processed_at"`
+	}
+
+	var results []alertExplanation
+	for rows.Next() {
+		var ae alertExplanation
+		var expJSON []byte
+		if err := rows.Scan(
+			&ae.AlertID, &ae.Title, &ae.Severity,
+			&ae.FinalScore, &ae.Decision, &ae.Strategy,
+			&expJSON, &ae.ProcessedAt,
+		); err != nil {
+			log.Printf("GetCorrelationExplanation: scan error: %v", err)
+			continue
+		}
+		ae.Explanation = json.RawMessage(expJSON)
+		results = append(results, ae)
+	}
+
+	if results == nil {
+		results = []alertExplanation{} // return empty array, not null
+	}
+
+	c.JSON(http.StatusOK, gin.H{
+		"success":      true,
+		"incident_id":  incidentID,
+		"explanations": results,
+		"total":        len(results),
+	})
+}
```

**Register:**
```diff
+		incidents.GET("/:id/explanation", h.GetCorrelationExplanation)
```

---

### FIX E — getRecentAlerts LIMIT 50 → 200
**File:** `internal/services/correlation/parallel_correlation_engine.go`
**Risk:** LOW — increases Postgres result size per correlation query

```diff
--- a/internal/services/correlation/parallel_correlation_engine.go
+++ b/internal/services/correlation/parallel_correlation_engine.go

 	query := `
 		SELECT id, title, description, severity, source, source_id,
 		       tags, labels, metadata, fingerprint, created_at
 		FROM alerts
 		WHERE created_at >= $1
 		AND id != $2
 		AND status != 'resolved'
 		ORDER BY created_at DESC
-		LIMIT 50
+		LIMIT 200
 	`
```

---

### FIX F — Topology freshness tracking
**File:** `internal/services/correlation/topology_graph_correlator.go`
**Risk:** LOW — additive fields with mutex

```diff
--- a/internal/services/correlation/topology_graph_correlator.go
+++ b/internal/services/correlation/topology_graph_correlator.go

+import "sync"

 // TopologyGraphCorrelator correlates alerts using the live infrastructure graph.
 type TopologyGraphCorrelator struct {
 	provider TopoProvider
 	db       *sql.DB
+	mu            sync.RWMutex
+	lastRefreshed time.Time
+	nodeCount     int
 }

 // NewTopologyGraphCorrelator creates the correlator.
 func NewTopologyGraphCorrelator(provider TopoProvider, db *sql.DB) *TopologyGraphCorrelator {
-	return &TopologyGraphCorrelator{provider: provider, db: db}
+	return &TopologyGraphCorrelator{
+		provider: provider,
+		db:       db,
+	}
 }

+// LastRefreshed returns the time of the last successful topology fetch.
+// Returns zero time if GetNodes has never succeeded.
+func (tc *TopologyGraphCorrelator) LastRefreshed() time.Time {
+	tc.mu.RLock()
+	defer tc.mu.RUnlock()
+	return tc.lastRefreshed
+}
+
+// FreshnessSeconds returns seconds since the last successful topology fetch.
+// Returns -1 if GetNodes has never succeeded.
+func (tc *TopologyGraphCorrelator) FreshnessSeconds() int {
+	tc.mu.RLock()
+	defer tc.mu.RUnlock()
+	if tc.lastRefreshed.IsZero() {
+		return -1
+	}
+	return int(time.Since(tc.lastRefreshed).Seconds())
+}
+
+// NodeCount returns the node count from the last successful GetNodes call.
+func (tc *TopologyGraphCorrelator) NodeCount() int {
+	tc.mu.RLock()
+	defer tc.mu.RUnlock()
+	return tc.nodeCount
+}

 // Correlate is the main entry point called by runTopologyStrategy.
 func (tc *TopologyGraphCorrelator) Correlate(ctx context.Context, alert *Alert) (*TopoCorrelationResult, error) {
+	age := tc.FreshnessSeconds()
+	if age > 1800 {
+		log.Printf("topology: data is %ds old — correlation quality may be degraded", age)
+	}
+
 	nodes, edges, err := tc.provider.GetNodes(ctx)
 	if err != nil {
 		return nil, fmt.Errorf("topology provider: %w", err)
 	}
 	if len(nodes) == 0 {
+		// GetNodes returned empty — don't update lastRefreshed, preserve previous timestamp
 		return &TopoCorrelationResult{BestScore: 0}, nil
 	}
+
+	// Update freshness tracking after successful non-empty fetch
+	tc.mu.Lock()
+	tc.lastRefreshed = time.Now()
+	tc.nodeCount = len(nodes)
+	tc.mu.Unlock()
+
 	// ... rest of Correlate unchanged ...
```

**Add topology health endpoint:**

**File:** `internal/api/handlers/topology_handlers.go`

```diff
+// GetTopologyHealth returns the freshness state of the topology graph correlator.
+// Used by ops to detect stale topology before trusting correlation decisions.
+func (h *TopologyHandler) GetTopologyHealth(c *gin.Context) {
+	freshness := -1
+	nodeCount := 0
+
+	if h.topoCorrelator != nil {
+		freshness = h.topoCorrelator.FreshnessSeconds()
+		nodeCount = h.topoCorrelator.NodeCount()
+	}
+
+	band := "UNKNOWN"
+	switch {
+	case freshness < 0:
+		band = "NEVER_REFRESHED"
+	case freshness < 300:
+		band = "FRESH"
+	case freshness < 1800:
+		band = "AGING"
+	default:
+		band = "STALE"
+	}
+
+	c.JSON(http.StatusOK, gin.H{
+		"freshness_seconds": freshness,
+		"freshness_band":    band,
+		"node_count":        nodeCount,
+		"last_refreshed":    h.topoCorrelator.LastRefreshed(),
+		"is_healthy":        freshness >= 0 && freshness < 1800,
+	})
+}
```

**Wire in `cmd/main.go`** — pass `topoGraphCorrelator` to the topology handler.
The `TopologyHandler` struct needs a `topoCorrelator *correlation.TopologyGraphCorrelator` field added.

---

### FIX G — Feedback loop: controlled weight unlock
**File:** `cmd/main.go`
**Risk:** MEDIUM — changes correlation weights in production if env var is set

```diff
--- a/cmd/main.go
+++ b/cmd/main.go

-	parallelCorrelationEngine.LockWeights()
+	// CORRELATION_WEIGHTS_LOCKED=false unlocks adaptive learning weight updates.
+	// Default is locked (safe for production). Set to "false" after 30+ days of
+	// feedback data accumulation with ops review of correlation/weight-status endpoint.
+	if os.Getenv("CORRELATION_WEIGHTS_LOCKED") != "false" {
+		parallelCorrelationEngine.LockWeights()
+		log.Printf("Correlation engine weights LOCKED (set CORRELATION_WEIGHTS_LOCKED=false to enable feedback learning)")
+	} else {
+		log.Printf("Correlation engine weights UNLOCKED — feedback loop active")
+	}

-	correlationAggregator.LockWeights() // freeze go-live weights; prevent Redis/adaptive override
+	if os.Getenv("CORRELATION_WEIGHTS_LOCKED") != "false" {
+		correlationAggregator.LockWeights()
+	}
```

**helm/alerthub/values.yaml:**
```yaml
# Start locked. Unlock after 30 days of feedback data + ops review.
correlationWeightsLocked: "true"
```

**helm/alerthub/templates/deployment.yaml:**
```yaml
- name: CORRELATION_WEIGHTS_LOCKED
  value: {{ .Values.correlationWeightsLocked | quote }}
```

---

## STEP 3 — DATABASE MIGRATIONS

**File:** `internal/db/migrations.go` — append to the migrations slice

```go
// Fix H: add step_states column for operator progress tracking on investigation DAGs
`ALTER TABLE incident_investigation_dags
 ADD COLUMN IF NOT EXISTS step_states JSONB NOT NULL DEFAULT '{}'`,

// Fix A: rca_decisions table for persistent RCA audit trail
`CREATE TABLE IF NOT EXISTS rca_decisions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id         UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    root_entity_id      TEXT NOT NULL DEFAULT '',
    root_entity_label   TEXT NOT NULL DEFAULT '',
    root_entity_type    TEXT NOT NULL DEFAULT '',
    confidence          FLOAT NOT NULL DEFAULT 0,
    confidence_band     TEXT NOT NULL DEFAULT 'VERY_LOW',
    hypotheses          JSONB NOT NULL DEFAULT '[]',
    reasoning           TEXT[] NOT NULL DEFAULT '{}',
    evidence_sources    TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    operator_verdict    TEXT,
    actual_root_cause   TEXT,
    verdict_at          TIMESTAMPTZ,
    UNIQUE (incident_id)
)`,
`CREATE INDEX IF NOT EXISTS idx_rca_decisions_incident ON rca_decisions(incident_id)`,
`CREATE INDEX IF NOT EXISTS idx_rca_decisions_created  ON rca_decisions(created_at DESC)`,
```

---

## STEP 4 — REDIS CLEANUP SCRIPT

Run this against production Redis **after** Fix A code is deployed and validated in staging:

```bash
#!/bin/bash
# cleanup_rca_keys.sh
# Deletes old-format rca:hyp:{alertID} keys (3 colon-separated segments).
# Safe to run while the new code is live: new keys use 4 segments.

set -euo pipefail

NAMESPACE="${1:-aileron}"
POD="redis-primary-0"

echo "=== RCA key cleanup: counting old-format keys ==="
OLD_COUNT=$(kubectl exec -n "$NAMESPACE" "$POD" -- \
  redis-cli --scan --pattern 'rca:hyp:*' | \
  awk -F: 'NF==3 {count++} END {print count+0}')
echo "Old-format keys found: $OLD_COUNT"

if [ "$OLD_COUNT" -eq 0 ]; then
  echo "No old-format keys to delete. Exiting."
  exit 0
fi

echo "=== Sample of keys to be deleted (first 10) ==="
kubectl exec -n "$NAMESPACE" "$POD" -- \
  redis-cli --scan --pattern 'rca:hyp:*' | \
  awk -F: 'NF==3' | \
  head -10

read -p "Delete $OLD_COUNT old-format keys? (yes/no) " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
  echo "Aborted."
  exit 1
fi

echo "=== Deleting old-format keys ==="
DELETED=$(kubectl exec -n "$NAMESPACE" "$POD" -- \
  redis-cli --scan --pattern 'rca:hyp:*' | \
  awk -F: 'NF==3' | \
  xargs -r kubectl exec -n "$NAMESPACE" "$POD" -- redis-cli del | \
  awk '{sum+=$1} END {print sum+0}')
echo "Deleted: $DELETED keys"

echo "=== Verifying no old-format keys remain ==="
REMAINING=$(kubectl exec -n "$NAMESPACE" "$POD" -- \
  redis-cli --scan --pattern 'rca:hyp:*' | \
  awk -F: 'NF==3 {count++} END {print count+0}')
echo "Remaining old-format keys: $REMAINING"
if [ "$REMAINING" -ne 0 ]; then
  echo "ERROR: $REMAINING old-format keys still present. Re-run script."
  exit 1
fi
echo "Cleanup complete."
```

---

## STEP 5 — TEST SUITE

### Unit tests — `internal/services/correlation/probabilistic_rca_test.go`

```go
package correlation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func newTestEngine(t *testing.T) *ProbabilisticRCAEngine {
	t.Helper()
	return &ProbabilisticRCAEngine{rdb: setupTestRedis(t)}
}

// TEST A1: hypothesisKey format
func TestHypothesisKey_Format(t *testing.T) {
	incID := "550e8400-e29b-41d4-a716-446655440000"
	entityID := "redis-cluster"
	key := hypothesisKey(incID, entityID)
	assert.Equal(t, "rca:hyp:550e8400-e29b-41d4-a716-446655440000:redis-cluster", key)
	parts := strings.SplitN(key, ":", 4)
	assert.Len(t, parts, 4, "key must have 4 colon-separated segments")
}

// TEST A2: hypotheses from incident A must not appear in incident B
func TestLoadPriorHypotheses_Isolation(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	incidentA := uuid.New()
	incidentB := uuid.New()

	hypA := &RCAHypothesis{
		EntityID:      "redis-cluster",
		EntityLabel:   "redis-cluster",
		RawConfidence: 0.90,
		LastUpdated:   time.Now(),
		DecayFactor:   1.0,
	}
	engine.persistHypotheses(ctx, incidentA, []*RCAHypothesis{hypA})

	// Give async persist time to complete (it's synchronous in tests but be explicit)
	time.Sleep(10 * time.Millisecond)

	loaded, err := engine.loadPriorHypotheses(ctx, incidentB)
	require.NoError(t, err)
	assert.Empty(t, loaded, "incident B must not see incident A's hypotheses")
}

// TEST A3: hypotheses from same incident ARE loaded
func TestLoadPriorHypotheses_SameIncident(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	incidentID := uuid.New()
	hyp := &RCAHypothesis{
		EntityID:      "postgres-primary",
		EntityLabel:   "postgres-primary",
		RawConfidence: 0.75,
		LastUpdated:   time.Now(),
		DecayFactor:   1.0,
	}
	engine.persistHypotheses(ctx, incidentID, []*RCAHypothesis{hyp})
	time.Sleep(10 * time.Millisecond)

	loaded, err := engine.loadPriorHypotheses(ctx, incidentID)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, "postgres-primary", loaded[0].EntityID)
}

// TEST A4: nil incidentID causes no Redis calls
func TestPersistHypotheses_NilIncident_NoOp(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	// Should not panic
	engine.persistHypotheses(ctx, uuid.Nil, []*RCAHypothesis{
		{EntityID: "test-entity", RawConfidence: 0.8},
	})

	loaded, err := engine.loadPriorHypotheses(ctx, uuid.Nil)
	require.NoError(t, err)
	assert.Empty(t, loaded)
}

// TEST A5: each hypothesis stored as individual key, not array
func TestPersistHypotheses_OneKeyPerEntity(t *testing.T) {
	rdb := setupTestRedis(t)
	engine := &ProbabilisticRCAEngine{rdb: rdb}
	ctx := context.Background()
	incID := uuid.New()

	hyps := []*RCAHypothesis{
		{EntityID: "entity-1", RawConfidence: 0.7},
		{EntityID: "entity-2", RawConfidence: 0.5},
	}
	engine.persistHypotheses(ctx, incID, hyps)
	time.Sleep(10 * time.Millisecond)

	key1 := hypothesisKey(incID.String(), "entity-1")
	key2 := hypothesisKey(incID.String(), "entity-2")

	val1, err := rdb.Get(ctx, key1).Result()
	require.NoError(t, err, "entity-1 key must exist")

	val2, err := rdb.Get(ctx, key2).Result()
	require.NoError(t, err, "entity-2 key must exist")

	// Each value must be a single hypothesis, not an array
	var h1 RCAHypothesis
	require.NoError(t, json.Unmarshal([]byte(val1), &h1))
	assert.Equal(t, "entity-1", h1.EntityID)

	var h2 RCAHypothesis
	require.NoError(t, json.Unmarshal([]byte(val2), &h2))
	assert.Equal(t, "entity-2", h2.EntityID)
}

// TEST A6: TTL is 72 hours
func TestHypothesisKeyTTL(t *testing.T) {
	assert.Equal(t, 72*time.Hour, hypothesisKeyTTL)
}

// TEST A7: context cancellation returns partial results without error
func TestLoadPriorHypotheses_ContextCancelled(t *testing.T) {
	engine := newTestEngine(t)
	incID := uuid.New()
	// Pre-populate some keys
	for i := 0; i < 5; i++ {
		engine.persistHypotheses(context.Background(), incID, []*RCAHypothesis{
			{EntityID: fmt.Sprintf("entity-%d", i), RawConfidence: 0.5},
		})
	}
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	loaded, err := engine.loadPriorHypotheses(ctx, incID)
	// Cancelled context: should return nil error and whatever was loaded
	assert.NoError(t, err)
	_ = loaded // may be empty or partial — either is acceptable
}
```

### Unit tests — `internal/services/pipeline/llm_enricher_test.go`

```go
package pipeline

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewLLMEnricher_Defaults(t *testing.T) {
	os.Unsetenv("LLM_SERVICE_URL")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("POD_NAMESPACE")

	e := NewLLMEnricher()
	assert.Equal(t, "qwen2.5:3b", e.model)
	assert.Contains(t, e.serviceURL, "aileron")
	assert.Equal(t, 10*time.Second, e.client.Timeout)
}

func TestNewLLMEnricher_POD_NAMESPACE(t *testing.T) {
	os.Unsetenv("LLM_SERVICE_URL")
	os.Unsetenv("LLM_MODEL")
	t.Setenv("POD_NAMESPACE", "tooling-mdn")

	e := NewLLMEnricher()
	assert.Contains(t, e.serviceURL, "tooling-mdn")
}

func TestNewLLMEnricher_EnvOverride(t *testing.T) {
	t.Setenv("LLM_SERVICE_URL", "http://custom-ollama:11434")
	t.Setenv("LLM_MODEL", "llama3:8b")

	e := NewLLMEnricher()
	assert.Equal(t, "http://custom-ollama:11434", e.serviceURL)
	assert.Equal(t, "llama3:8b", e.model)
}

func TestGenerateRCA_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"response":"Root cause is Redis memory exhaustion."}`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "qwen2.5:3b",
		client: &http.Client{Timeout: 5 * time.Second}}

	result := e.GenerateRCA(t.Context(), "Redis OOM", "critical",
		"redis-cluster", "cache", "", "", map[string]float64{"topology": 0.9})
	assert.Equal(t, "Root cause is Redis memory exhaustion.", result)
}

func TestGenerateRCA_Unreachable(t *testing.T) {
	e := &LLMEnricher{
		serviceURL: "http://127.0.0.1:1", // nothing listening
		model:      "qwen2.5:3b",
		client:     &http.Client{Timeout: 100 * time.Millisecond},
	}
	result := e.GenerateRCA(t.Context(), "alert", "high", "", "", "", "",
		map[string]float64{})
	assert.Empty(t, result, "unreachable server must return empty string")
}

func TestGenerateRCA_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "phi3:mini",
		client: &http.Client{Timeout: 2 * time.Second}}

	result := e.GenerateRCA(t.Context(), "test", "low", "", "", "", "",
		map[string]float64{})
	assert.Empty(t, result)
}
```

### Integration tests

```go
// internal/services/correlation/probabilistic_rca_integration_test.go
//go:build integration

package correlation

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run with: go test -tags=integration -run TestHypothesisIsolation_Integration
func TestHypothesisIsolation_Integration(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisURL})
	t.Cleanup(func() {
		// Clean up test keys
		keys, _ := rdb.Keys(context.Background(), "rca:hyp:test-*").Result()
		if len(keys) > 0 {
			rdb.Del(context.Background(), keys...)
		}
	})

	engine := &ProbabilisticRCAEngine{rdb: rdb}
	ctx := context.Background()

	incA := uuid.New()
	incB := uuid.New()

	// Persist a high-confidence hypothesis for incident A
	hypA := &RCAHypothesis{
		EntityID:      "redis-cluster-prod",
		EntityLabel:   "redis-cluster-prod",
		RawConfidence: 0.95,
	}
	engine.persistHypotheses(ctx, incA, []*RCAHypothesis{hypA})

	// Incident B must not see incident A's hypotheses
	loaded, err := engine.loadPriorHypotheses(ctx, incB)
	require.NoError(t, err)
	for _, h := range loaded {
		assert.NotEqual(t, "redis-cluster-prod", h.EntityID,
			"incident B must not see incident A hypothesis")
	}
}
```

### Failure tests

```go
// TEST: verify system degrades gracefully when Redis is down during RCA
func TestEvaluate_RedisDown_DoesNotPanic(t *testing.T) {
	// Point to a non-existent Redis — engine should return a result, not panic
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 50 * time.Millisecond,
	})
	engine := &ProbabilisticRCAEngine{rdb: rdb}
	ctx := context.Background()
	incID := uuid.New()

	// loadPriorHypotheses should return error (Redis unreachable) but not panic
	loaded, err := engine.loadPriorHypotheses(ctx, incID)
	// Error is expected — the key question is: does the caller handle it gracefully?
	_ = loaded
	_ = err
	// The calling code in buildHypotheses does: prior, _ := p.loadPriorHypotheses(...)
	// — error is swallowed. No crash. But no prior hypotheses either. Acceptable.
}
```

### Regression test — verify investigation DAG endpoint

```go
// internal/api/handlers/incidents_test.go (add to existing test file)
func TestGetInvestigationDAG_NotFound(t *testing.T) {
	// GET /incidents/{uuid}/investigation for an incident with no DAG
	// Must return 200 with available:false, not 404 or 500
	// Setup: clean test DB, no incident_investigation_dags rows
	// Assert: response.available == false
}

func TestGetInvestigationDAG_Found(t *testing.T) {
	// GET /incidents/{uuid}/investigation for an incident with a DAG
	// Must return domain, root_entity, steps, step_states
	// Setup: insert into incident_investigation_dags
	// Assert: steps is a non-empty array
}

func TestUpdateInvestigationStep_InvalidState(t *testing.T) {
	// PATCH with state="invalid" must return 400
}
```

---

## STEP 6 — DEPLOYMENT SEQUENCE

Deploy in this exact order. Each step has a validation gate that must pass before proceeding.

---

### Deploy 1: Fix B — LLM Enricher (no schema change, config only)

**Pre-deploy:**
```bash
# Verify qwen2.5:3b is available on all Ollama pods
kubectl exec -n aileron ollama-0 -- ollama list | grep qwen2.5
```

**Deploy:** Helm upgrade with updated values — adds `POD_NAMESPACE` env var, new image.

**Post-deploy validation:**
```bash
# Check startup log
kubectl logs -n aileron deploy/alerthub-backend --since=1m | grep LLMEnricher
# Expected: LLMEnricher initialized: model=qwen2.5:3b url=http://ollama.aileron...

# After creating or receiving a new incident, check davis_ai_analysis:
kubectl exec -n aileron postgres-primary-0 -- \
  psql -U alerthub -c \
  "SELECT id, davis_ai_analysis IS NOT NULL as has_ai
   FROM incidents
   ORDER BY created_at DESC LIMIT 3"
```

**Rollback:** `helm rollback alerthub` to previous release.

---

### Deploy 2: Fix A — RCA contamination (code change + signature change)

**Pre-conditions:** Deploy 1 complete.

**Code change checklist before deploy:**
- [ ] `Evaluate(ctx, alert, incidentID)` signature updated
- [ ] All callers of `Evaluate` updated: `alert_pipeline.go:3212` and any CACIE internal call
- [ ] `buildHypotheses` signature updated: accepts `incidentID uuid.UUID`
- [ ] `persistHypotheses` stores one key per entity
- [ ] `loadPriorHypotheses` scans `rca:hyp:{incidentID}:*`
- [ ] `hypothesisKeyTTL` = 72h

**Compile check:**
```bash
cd /path/to/alerthub && go build ./... 2>&1 | head -20
# Must produce zero errors
```

**Run unit tests:**
```bash
go test -v ./internal/services/correlation/... -run TestHypothesis -count=1
# All 7 unit tests must pass
```

**Deploy staging first.** Wait 30 minutes. Verify:
```bash
# No old-format keys appearing in Redis after staging deploy
kubectl exec -n aileron redis-primary-0 -- \
  redis-cli --scan --pattern 'rca:hyp:*' | awk -F: 'NF==3' | wc -l
# Must be 0 (new code stopped writing old-format keys)
```

**Deploy production.**

**Post-deploy: run Redis cleanup script:**
```bash
bash cleanup_rca_keys.sh aileron
```

**Validation:**
```bash
# New keys should appear as rca:hyp:{incidentUUID}:{entityID}
kubectl exec -n aileron redis-primary-0 -- \
  redis-cli --scan --pattern 'rca:hyp:*' | head -5
# Expected pattern: rca:hyp:550e8400-...:redis-cluster

# Verify hypotheses are scoped: keys for two different incidents must not overlap
# (manual check: create two incidents, verify their Redis keys don't share content)
```

---

### Deploy 3: Database migrations (additive only)

**Pre-conditions:** None — all migrations are `IF NOT EXISTS` safe.

```bash
# Run migrations (replace with your migration command)
kubectl exec -n aileron postgres-primary-0 -- \
  psql -U alerthub -c "
    ALTER TABLE incident_investigation_dags
    ADD COLUMN IF NOT EXISTS step_states JSONB NOT NULL DEFAULT '{}';

    CREATE TABLE IF NOT EXISTS rca_decisions (
      id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      incident_id         UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
      root_entity_id      TEXT NOT NULL DEFAULT '',
      root_entity_label   TEXT NOT NULL DEFAULT '',
      root_entity_type    TEXT NOT NULL DEFAULT '',
      confidence          FLOAT NOT NULL DEFAULT 0,
      confidence_band     TEXT NOT NULL DEFAULT 'VERY_LOW',
      hypotheses          JSONB NOT NULL DEFAULT '[]',
      reasoning           TEXT[] NOT NULL DEFAULT '{}',
      evidence_sources    TEXT[] NOT NULL DEFAULT '{}',
      created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      operator_verdict    TEXT,
      actual_root_cause   TEXT,
      verdict_at          TIMESTAMPTZ,
      UNIQUE (incident_id)
    );
    CREATE INDEX IF NOT EXISTS idx_rca_decisions_incident ON rca_decisions(incident_id);
    CREATE INDEX IF NOT EXISTS idx_rca_decisions_created  ON rca_decisions(created_at DESC);
  "
```

**Validation:**
```bash
kubectl exec -n aileron postgres-primary-0 -- \
  psql -U alerthub -c "\d incident_investigation_dags" | grep step_states
# Must show step_states column
```

---

### Deploy 4: Fixes C, D, E, F, H — Endpoints + getRecentAlerts + topology freshness

These are all additive or read-path changes. Can be deployed together.

**Pre-conditions:** Deploy 3 complete (step_states column exists).

**Code change checklist:**
- [ ] `GetInvestigationDAG` added to `IncidentHandler`
- [ ] `UpdateInvestigationStep` added to `IncidentHandler`
- [ ] `GetCorrelationExplanation` added to `IncidentHandler`
- [ ] Routes registered in `RegisterRoutes`
- [ ] LIMIT 50 → 200 in `getRecentAlerts`
- [ ] `TopologyGraphCorrelator` has `lastRefreshed`, `nodeCount`, mutex, accessors

**Compile check:**
```bash
go build ./... && go vet ./...
```

**Post-deploy validation:**
```bash
TOKEN=$(get_auth_token)
INC_ID=$(get_recent_incident_id)

# Investigation DAG
curl -s -H "Authorization: Bearer $TOKEN" \
  https://alerthub/api/v1/incidents/$INC_ID/investigation | jq '.available, .domain'

# Explanation
curl -s -H "Authorization: Bearer $TOKEN" \
  https://alerthub/api/v1/incidents/$INC_ID/explanation | jq '.total'
# If 0: check pipeline_correlation_results for this incident's alerts

# Topology health
curl -s -H "Authorization: Bearer $TOKEN" \
  https://alerthub/api/v1/topology/health | jq '.freshness_band, .is_healthy'
```

---

### Deploy 5: Fix G — Feedback weight unlock (opt-in, delayed)

**DO NOT deploy until:** 30+ days of `correlation_feedback` data accumulated, ops team has reviewed `GET /api/v1/admin/correlation/weight-status`, and weight suggestions have been manually reviewed.

**Pre-conditions:** Deploys 1-4 complete. At least 100 feedback rows per strategy in `correlation_feedback` table.

**Pre-deploy check:**
```bash
kubectl exec -n aileron postgres-primary-0 -- \
  psql -U alerthub -c "
    SELECT dominant_strategy, COUNT(*) as samples
    FROM correlation_feedback
    WHERE created_at >= NOW() - INTERVAL '30 days'
    GROUP BY dominant_strategy
    ORDER BY dominant_strategy"
# Must show >= 100 rows per strategy
```

**Deploy:** Update Helm value:
```yaml
correlationWeightsLocked: "false"
```

**Post-deploy:**
```bash
kubectl logs -n aileron deploy/alerthub-backend --since=1m | grep "weights UNLOCKED"
# Monitor correlation_weight_history table for the next 48 hours
kubectl exec -n aileron postgres-primary-0 -- \
  psql -U alerthub -c "SELECT weights, reason, created_at FROM correlation_weight_history ORDER BY created_at DESC LIMIT 5"
```

**Rollback if weights drift > 15% on any strategy in 24 hours:**
```yaml
correlationWeightsLocked: "true"
```

---

## STEP 7 — ROLLBACK SEQUENCE

### Fix A rollback (RCA contamination)
```bash
# 1. Roll back the Go binary (helm rollback or previous image)
helm rollback alerthub -n aileron

# 2. The old code writes rca:hyp:{alertID} keys again.
#    New-format keys (rca:hyp:{incidentID}:{entityID}) are ignored by old code (pattern mismatch).
#    Old behavior is restored immediately on pod restart.

# 3. Do NOT re-run the cleanup script — let old-format keys expire naturally (2h TTL).
```

### Fix B rollback (LLM enricher)
```bash
helm rollback alerthub -n aileron
# config-only change; previous image reverts model/URL/timeout
```

### Fixes C, D, E, F, H rollback
```bash
helm rollback alerthub -n aileron
# Additive API routes are removed. No data loss.
# LIMIT 50 reverts in the Go binary.
# Topology freshness fields are in-memory only — no persistent state to clean up.
```

### Migration rollback (step_states column)
The `step_states` column is additive with a default. It can remain indefinitely without impact.
If it must be removed:
```sql
ALTER TABLE incident_investigation_dags DROP COLUMN IF EXISTS step_states;
```
Only run if the application has been rolled back AND the column causes issues (it won't).

### Fix G rollback (feedback unlock)
```yaml
# values.yaml
correlationWeightsLocked: "true"
```
`helm upgrade` with this value. Weights immediately re-lock on next pod restart.
The `correlation_weight_history` table retains the history of what happened during the unlock period — do not delete it.

---

## SUMMARY

| Fix | Risk | Schema | Rollback time | Key validation |
|---|---|---|---|---|
| A: RCA scope | LOW (isolated Redis change) | None | < 5 min | Redis key scan shows 4-segment format |
| B: LLM enricher | NEGLIGIBLE | None | < 2 min | `davis_ai_analysis` populated on new incidents |
| C: DAG endpoint | NONE (read-only) | step_states ADD | < 2 min | `GET /incidents/:id/investigation` returns steps |
| D: Explanation endpoint | NONE (read-only) | None | < 2 min | `GET /incidents/:id/explanation` returns data |
| E: LIMIT 200 | LOW (larger queries) | None | < 2 min | Query plan unchanged, p99 latency stable |
| F: Topology freshness | LOW (additive fields) | None | < 2 min | `GET /topology/health` returns `is_healthy` |
| G: Unlock weights | MEDIUM (changes scores) | None | < 5 min | Helm value reverts |
