package correlation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ParallelCorrelationEngine executes 4 correlation strategies in parallel
type ParallelCorrelationEngine struct {
	db                  *sql.DB
	semanticEngine      *SemanticCorrelationEngine
	// topoGraphCorrelator uses the Redis-backed infra graph (preferred over Neo4j).
	topoGraphCorrelator *TopologyGraphCorrelator
	liveTopologyFetcher TopologyRefresher
	// topoRefreshInFlight ensures at most one background topology refresh goroutine runs at a time.
	topoRefreshInFlight int32
	weightsMu           sync.RWMutex
	weights             StrategyWeights
	// weightsLocked prevents adaptive learning from overriding go-live weights.
	weightsLocked       bool
	threshold           float64
	isInitialized       bool
}

// TopologyRefresher is implemented by LiveTopologyFetcher to refresh Neo4j before each correlation.
type TopologyRefresher interface {
	RefreshTopology(ctx context.Context) error
}

// StrategyWeights defines weights for correlation strategies
type StrategyWeights struct {
	Semantic float64 `json:"semantic"`
	Temporal float64 `json:"temporal"`
	Topology float64 `json:"topology"`
	Rules    float64 `json:"rules"`
}

// StrategyResult represents result from a single correlation strategy
type StrategyResult struct {
	StrategyName   string                 `json:"strategy_name"`
	Score          float64                `json:"score"`
	Confidence     float64                `json:"confidence"`
	BestMatch      *Alert                 `json:"best_match,omitempty"`
	SimilarAlerts  []SimilarAlert         `json:"similar_alerts"`
	ProcessingTime time.Duration          `json:"processing_time"`
	Details        map[string]interface{} `json:"details"`
	Error          error                  `json:"error,omitempty"`
}

// ParallelCorrelationResult represents final correlation result
type ParallelCorrelationResult struct {
	AlertID             uuid.UUID                  `json:"alert_id"`
	CorrelationID       string                     `json:"correlation_id"`
	FinalScore          float64                    `json:"final_score"`
	BestMatch           *Alert                     `json:"best_match,omitempty"`
	Decision            string                     `json:"decision"`
	StrategyResults     map[string]*StrategyResult `json:"strategy_results"`
	WeightsUsed         StrategyWeights            `json:"weights_used"`
	TotalProcessingTime time.Duration              `json:"total_processing_time"`
	ParallelExecution   bool                       `json:"parallel_execution"`
	Reasoning           string                     `json:"reasoning"`
	Metadata            map[string]interface{}     `json:"metadata"`
	CreatedAt           time.Time                  `json:"created_at"`
}

// NewParallelCorrelationEngine creates a new parallel correlation engine
func NewParallelCorrelationEngine(db *sql.DB) *ParallelCorrelationEngine {
	engine := &ParallelCorrelationEngine{
		db:             db,
		semanticEngine: NewSemanticCorrelationEngine(db),
		// Go-live weights: topology is the strongest signal for infrastructure-aware RCA.
		// Semantic is minimised to prevent cross-cluster false correlations.
		weights: StrategyWeights{
			Topology: 0.45,
			Rules:    0.25,
			Semantic: 0.20,
			Temporal: 0.10,
		},
		threshold:     0.75,
		weightsLocked: false,
	}

	return engine
}

// LockWeights prevents the adaptive learning engine from overriding strategy weights.
// Call at startup for go-live stability; frozen weights will be logged at each correlation.
func (pce *ParallelCorrelationEngine) LockWeights() {
	pce.weightsMu.Lock()
	pce.weightsLocked = true
	pce.weightsMu.Unlock()
	log.Printf("Parallel correlation engine: strategy weights locked (Topology=%.2f Temporal=%.2f Rules=%.2f Semantic=%.2f)",
		pce.weights.Topology, pce.weights.Temporal, pce.weights.Rules, pce.weights.Semantic)
}

// GetWeights returns a snapshot of the current strategy weights.
// Safe to call concurrently.
func (pce *ParallelCorrelationEngine) GetWeights() StrategyWeights {
	pce.weightsMu.RLock()
	defer pce.weightsMu.RUnlock()
	return pce.weights
}

// IsLocked reports whether strategy weights are frozen.
// Safe to call concurrently.
func (pce *ParallelCorrelationEngine) IsLocked() bool {
	pce.weightsMu.RLock()
	defer pce.weightsMu.RUnlock()
	return pce.weightsLocked
}

// SetStrategyWeights replaces the scoring weights for the next correlation pass.
// Called by the adaptive learning engine when per-domain weights are available.
// No-op when weights are locked via LockWeights().
func (pce *ParallelCorrelationEngine) SetStrategyWeights(w StrategyWeights) {
	pce.weightsMu.Lock()
	defer pce.weightsMu.Unlock()
	if pce.weightsLocked {
		return
	}
	pce.weights = w
}

// SetTopologyGraphCorrelator wires the Redis-backed infra graph correlator.
// When set, this takes priority over the Neo4j engine in runTopologyStrategy.
func (pce *ParallelCorrelationEngine) SetTopologyGraphCorrelator(c *TopologyGraphCorrelator) {
	pce.topoGraphCorrelator = c
}

// SetLiveTopologyFetcher wires the live topology fetcher called before each correlation.
func (pce *ParallelCorrelationEngine) SetLiveTopologyFetcher(f TopologyRefresher) {
	pce.liveTopologyFetcher = f
}

// Initialize initializes all correlation engines
func (pce *ParallelCorrelationEngine) Initialize(ctx context.Context) error {
	log.Println("Initializing Parallel Correlation Engine...")

	// Initialize semantic engine
	if err := pce.semanticEngine.InitializeModel(ctx); err != nil {
		log.Printf("Semantic engine initialization warning: %v", err)
	}

	pce.isInitialized = true
	log.Println("Parallel Correlation Engine initialized successfully")
	return nil
}

// CorrelateAlert performs parallel correlation using all 4 strategies
func (pce *ParallelCorrelationEngine) CorrelateAlert(ctx context.Context, alert *Alert) (*ParallelCorrelationResult, error) {
	startTime := time.Now()

	// Hard 30-second deadline for the entire correlation so no alert waits indefinitely.
	corrCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if !pce.isInitialized {
		if err := pce.Initialize(corrCtx); err != nil {
			return nil, fmt.Errorf("failed to initialize correlation engine: %w", err)
		}
	}

	// Snapshot weights under the read lock so concurrent SetStrategyWeights calls
	// cannot mutate the weights mid-correlation.
	pce.weightsMu.RLock()
	snapshotWeights := pce.weights
	pce.weightsMu.RUnlock()

	// Topology data in Neo4j is kept fresh by a background goroutine (5-min interval).
	// If the data is stale (first startup or long pause), trigger an async refresh
	// so this correlation uses the current Neo4j state without blocking.
	if pce.liveTopologyFetcher != nil {
		type freshChecker interface {
			IsFresh(time.Duration) bool
		}
		if fc, ok := pce.liveTopologyFetcher.(freshChecker); !ok || !fc.IsFresh(5*time.Minute) {
			// Gate to one in-flight refresh — avoids spawning one goroutine per alert under load.
			if atomic.CompareAndSwapInt32(&pce.topoRefreshInFlight, 0, 1) {
				go func() {
					defer atomic.StoreInt32(&pce.topoRefreshInFlight, 0)
					bgCtx, bgCancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer bgCancel()
					if err := pce.liveTopologyFetcher.RefreshTopology(bgCtx); err != nil {
						log.Printf("Async topology refresh failed for alert %s: %v", alert.ID, err)
					}
				}()
			}
		}
	}

	// Create result structure
	result := &ParallelCorrelationResult{
		AlertID:           alert.ID,
		CorrelationID:     fmt.Sprintf("parallel-%s-%d", alert.ID.String()[:8], startTime.Unix()),
		StrategyResults:   make(map[string]*StrategyResult),
		WeightsUsed:       snapshotWeights,
		ParallelExecution: true,
		CreatedAt:         startTime,
	}

	// CRITICAL: Execute all 4 strategies in parallel (not sequential)
	strategyResults := make(chan *StrategyResult, 4)
	var wg sync.WaitGroup

	// Start all strategies simultaneously
	wg.Add(4)

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Semantic strategy recovered from panic for alert %s: %v", alert.ID, r)
				strategyResults <- &StrategyResult{StrategyName: "semantic", Score: 0, Error: fmt.Errorf("panic: %v", r)}
			}
		}()
		strategyResults <- pce.runSemanticStrategy(corrCtx, alert)
	}()

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Temporal strategy recovered from panic for alert %s: %v", alert.ID, r)
				strategyResults <- &StrategyResult{StrategyName: "temporal", Score: 0, Error: fmt.Errorf("panic: %v", r)}
			}
		}()
		strategyResults <- pce.runTemporalStrategy(corrCtx, alert)
	}()

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Topology strategy recovered from panic for alert %s: %v", alert.ID, r)
				strategyResults <- &StrategyResult{StrategyName: "topology", Score: 0, Error: fmt.Errorf("panic: %v", r)}
			}
		}()
		strategyResults <- pce.runTopologyStrategy(corrCtx, alert)
	}()

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Rules strategy recovered from panic for alert %s: %v", alert.ID, r)
				strategyResults <- &StrategyResult{StrategyName: "rules", Score: 0, Error: fmt.Errorf("panic: %v", r)}
			}
		}()
		strategyResults <- pce.runRulesStrategy(corrCtx, alert)
	}()

	// Wait for all strategies to complete, then close the channel.
	go func() {
		wg.Wait()
		close(strategyResults)
	}()

	// Collect all strategy results, but bail as soon as corrCtx expires.
	strategies := []string{"semantic", "temporal", "topology", "rules"}
	collected := 0
	for collected < len(strategies) {
		select {
		case sr, ok := <-strategyResults:
			if !ok {
				// Channel closed — all strategies done.
				collected = len(strategies)
			} else if sr != nil {
				result.StrategyResults[sr.StrategyName] = sr
				collected++
			}
		case <-corrCtx.Done():
			// Timeout: fill missing strategies with zero scores so we still decide.
			// M3: include a Details map and Error so downstream nil checks don't panic.
			log.Printf("Correlation timeout for alert %s after %v — using partial results (%d/%d strategies)",
				alert.ID, time.Since(startTime).Round(time.Millisecond), len(result.StrategyResults), len(strategies))
			for _, name := range strategies {
				if _, exists := result.StrategyResults[name]; !exists {
					result.StrategyResults[name] = &StrategyResult{
						StrategyName:  name,
						Score:         0,
						Confidence:    0,
						SimilarAlerts: []SimilarAlert{},
						Details:       map[string]interface{}{"timeout": true},
						Error:         fmt.Errorf("strategy timed out"),
					}
				}
			}
			collected = len(strategies)
		}
	}

	// Calculate weighted final score
	finalScore, bestMatch := pce.calculateWeightedScore(result.StrategyResults, snapshotWeights)
	result.FinalScore = finalScore
	result.BestMatch = bestMatch

	// Make correlation decision
	result.Decision = pce.makeCorrelationDecision(finalScore, len(result.StrategyResults))
	result.Reasoning = pce.generateReasoning(result.StrategyResults, finalScore, result.Decision)

	result.TotalProcessingTime = time.Since(startTime)

	log.Printf("correlation alert=%s score=%.3f decision=%s time=%v",
		alert.ID, finalScore, result.Decision, result.TotalProcessingTime)

	// Store result for analytics and learning
	go pce.storeCorrelationResult(ctx, result)

	return result, nil
}

// runSemanticStrategy executes semantic correlation strategy
func (pce *ParallelCorrelationEngine) runSemanticStrategy(ctx context.Context, alert *Alert) *StrategyResult {
	start := time.Now()

	log.Printf("Running semantic strategy for alert %s", alert.ID)

	semanticResult, err := pce.semanticEngine.CorrelateAlertWithAI(ctx, alert)
	if err != nil {
		return &StrategyResult{
			StrategyName:   "semantic",
			Score:          0.0,
			ProcessingTime: time.Since(start),
			Error:          err,
		}
	}

	strategyResult := &StrategyResult{
		StrategyName:   "semantic",
		Score:          semanticResult.SemanticSimilarity,
		Confidence:     semanticResult.ConfidenceScore,
		SimilarAlerts:  semanticResult.SimilarAlerts,
		ProcessingTime: time.Since(start),
		Details: map[string]interface{}{
			"model_used":    semanticResult.ModelUsed,
			"methods_used":  semanticResult.CorrelationMethods,
			"similar_count": len(semanticResult.SimilarAlerts),
		},
	}

	return strategyResult
}

// runTemporalStrategy executes temporal correlation strategy
func (pce *ParallelCorrelationEngine) runTemporalStrategy(ctx context.Context, alert *Alert) *StrategyResult {
	start := time.Now()

	log.Printf("Running temporal strategy for alert %s", alert.ID)

	// Get recent alerts for temporal analysis
	recentAlerts, err := pce.getRecentAlerts(ctx, alert, 2*time.Hour)
	if err != nil {
		return &StrategyResult{
			StrategyName:   "temporal",
			Score:          0.0,
			ProcessingTime: time.Since(start),
			Error:          err,
		}
	}

	var maxSimilarity float64
	var bestMatch *Alert
	var similarAlerts []SimilarAlert

	for _, candidate := range recentAlerts {
		// Calculate time-based similarity
		timeDiff := alert.CreatedAt.Sub(candidate.CreatedAt).Minutes()
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		// Exponential decay: half-life = 30 min. At 300 min score 0.031 (was 0.42 with 0.1).
		temporalScore := math.Exp(-0.5 * timeDiff / 30)

		// Severity matching bonus
		if alert.Severity == candidate.Severity {
			temporalScore += 0.1
		}

		// Source matching bonus
		if alert.Source == candidate.Source {
			temporalScore += 0.05
		}

		// Cap at 1.0 — bonuses can push past 100% which is misleading
		if temporalScore > 1.0 {
			temporalScore = 1.0
		}

		if temporalScore > maxSimilarity {
			maxSimilarity = temporalScore
			bestMatch = &candidate
		}

		// C4: cap per-strategy similar-alert list to 10 — prevents pipeline_correlation_results
		// from bloating to hundreds of items per alert, which causes GB/day table growth.
		if temporalScore >= 0.6 && len(similarAlerts) < 10 {
			similarAlerts = append(similarAlerts, SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				TemporalSimilarity: temporalScore,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			})
		}
	}

	return &StrategyResult{
		StrategyName:   "temporal",
		Score:          maxSimilarity,
		Confidence:     maxSimilarity,
		BestMatch:      bestMatch,
		SimilarAlerts:  similarAlerts,
		ProcessingTime: time.Since(start),
		Details: map[string]interface{}{
			"time_window_hours":  2,
			"decay_half_life":    30,
			"candidates_checked": len(recentAlerts),
		},
	}
}

// runTopologyStrategy executes topology correlation strategy.
// Priority: (1) Redis infra graph correlator (2) Neo4j enhanced engine (3) string-match fallback.
func (pce *ParallelCorrelationEngine) runTopologyStrategy(ctx context.Context, alert *Alert) *StrategyResult {
	start := time.Now()
	log.Printf("Running topology strategy for alert %s", alert.ID)

	// Priority 1: Redis-backed infra graph (BM VM K8s Pod) 
	if pce.topoGraphCorrelator != nil {
		topoResult, err := pce.topoGraphCorrelator.Correlate(ctx, alert)
		if err != nil {
			log.Printf("topo graph correlator error for alert %s: %v", alert.ID, err)
		} else if topoResult != nil && topoResult.BestScore > 0 {
			details := topoResult.Details
			if details == nil {
				details = map[string]interface{}{}
			}
			details["engine"] = "redis_infra_graph"
			if topoResult.MatchedNode != nil {
				details["matched_node_type"] = topoResult.MatchedNode.NodeType
				details["matched_label"] = topoResult.MatchedNode.Label
				details["match_method"] = topoResult.MatchMethod
			}
			if topoResult.RootCauseNode != nil {
				details["root_cause"] = topoResult.RootCauseNode.Label
				details["root_cause_type"] = topoResult.RootCauseNode.NodeType
			}
			details["blast_radius"] = len(topoResult.BlastRadius)
			details["reasoning"] = topoResult.Reasoning
			if len(topoResult.BlastRadius) > 0 {
				nodeLabels := make([]string, 0, len(topoResult.BlastRadius))
				for _, n := range topoResult.BlastRadius {
					if n.Label != "" {
						nodeLabels = append(nodeLabels, n.Label)
					}
				}
				details["blast_radius_nodes"] = nodeLabels
			}

			return &StrategyResult{
				StrategyName:   "topology",
				Score:          topoResult.BestScore,
				Confidence:     topoResult.BestScore,
				BestMatch:      topoResult.BestMatch,
				SimilarAlerts:  topoResult.RelatedAlerts,
				ProcessingTime: time.Since(start),
				Details:        details,
			}
		}
		// If no match found, fall through to string-match.
	}

	// Priority 2: string-matching fallback 
	alertNode := pce.extractInfrastructureNode(alert)
	if alertNode == "" {
		return &StrategyResult{
			StrategyName:   "topology",
			Score:          0.0,
			ProcessingTime: time.Since(start),
			Details:        map[string]interface{}{"no_infrastructure_info": true},
		}
	}

	recentAlerts, err := pce.getRecentAlerts(ctx, alert, 2*time.Hour)
	if err != nil {
		return &StrategyResult{
			StrategyName:   "topology",
			Score:          0.0,
			ProcessingTime: time.Since(start),
			Error:          err,
		}
	}

	var maxSimilarity float64
	var bestMatch *Alert
	var similarAlerts []SimilarAlert

	for _, candidate := range recentAlerts {
		candidateNode := pce.extractInfrastructureNode(&candidate)
		relationshipScore := pce.calculateInfrastructureRelationship(alertNode, candidateNode)

		// Problem-domain boost: same cluster + same problem type lift score
		domain1 := pce.extractProblemDomain(alert)
		domain2 := pce.extractProblemDomain(&candidate)
		if domain1 != "" && domain1 == domain2 && relationshipScore >= 0.70 {
			relationshipScore = math.Min(1.0, relationshipScore+0.10)
		}

		if relationshipScore > maxSimilarity {
			maxSimilarity = relationshipScore
			bestMatch = &candidate
		}
		if relationshipScore >= 0.7 && len(similarAlerts) < 10 { // C4: cap per-strategy list
			similarAlerts = append(similarAlerts, SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				TopologySimilarity: relationshipScore,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			})
		}
	}

	return &StrategyResult{
		StrategyName:   "topology",
		Score:          maxSimilarity,
		Confidence:     maxSimilarity,
		BestMatch:      bestMatch,
		SimilarAlerts:  similarAlerts,
		ProcessingTime: time.Since(start),
		Details: map[string]interface{}{
			"engine":             "string_matching_fallback",
			"alert_node":         alertNode,
			"candidates_checked": len(recentAlerts),
		},
	}
}

// runRulesStrategy executes rules-based correlation strategy
func (pce *ParallelCorrelationEngine) runRulesStrategy(ctx context.Context, alert *Alert) *StrategyResult {
	start := time.Now()

	log.Printf("Running rules strategy for alert %s", alert.ID)

	// Load active correlation rules
	rules, err := pce.getActiveRules(ctx)
	if err != nil {
		return &StrategyResult{
			StrategyName:   "rules",
			Score:          0.0,
			ProcessingTime: time.Since(start),
			Error:          err,
		}
	}

	var maxScore float64
	var bestMatch *Alert
	var matchedRule string

	// C6: propagate getRecentAlerts error — discarding it caused ALL alerts to be
	// silently dropped (score=0 discard) whenever the DB was momentarily slow.
	recentAlerts, err := pce.getRecentAlerts(ctx, alert, 1*time.Hour)
	if err != nil {
		return &StrategyResult{
			StrategyName:   "rules",
			Score:          0.0,
			ProcessingTime: time.Since(start),
			Error:          fmt.Errorf("getRecentAlerts: %w", err),
		}
	}

	for _, rule := range rules {
		for _, candidate := range recentAlerts {
			if pce.evaluateRule(rule, alert, &candidate) {
				ruleScore := pce.calculateRuleScore(rule, alert, &candidate)

				if ruleScore > maxScore {
					maxScore = ruleScore
					bestMatch = &candidate
					matchedRule = rule.Name
				}
			}
		}
	}

	return &StrategyResult{
		StrategyName:   "rules",
		Score:          maxScore,
		Confidence:     maxScore,
		BestMatch:      bestMatch,
		ProcessingTime: time.Since(start),
		Details: map[string]interface{}{
			"matched_rule":       matchedRule,
			"rules_evaluated":    len(rules),
			"candidates_checked": len(recentAlerts),
		},
	}
}

// calculateWeightedScore calculates final weighted score from all strategies
func (pce *ParallelCorrelationEngine) calculateWeightedScore(strategyResults map[string]*StrategyResult, weights StrategyWeights) (float64, *Alert) {
	var totalScore float64
	var bestMatch *Alert
	var bestScore float64

	// Topology is authoritative: if it fires with high confidence it wins bestMatch
	// regardless of which strategy produced the raw-highest score.
	var topoMatch *Alert
	var topoScore float64

	for strategyName, result := range strategyResults {
		if result.Error != nil {
			continue // Skip failed strategies
		}

		// Get strategy weight
		var weight float64
		switch strategyName {
		case "semantic":
			weight = weights.Semantic
		case "temporal":
			weight = weights.Temporal
		case "topology":
			weight = weights.Topology
			if result.BestMatch != nil {
				topoScore = result.Score
				topoMatch = result.BestMatch
			}
		case "rules":
			weight = weights.Rules
		default:
			weight = 0.1 // Default weight
		}

		totalScore += result.Score * weight

		// Track best match across all strategies
		if result.BestMatch != nil && result.Score > bestScore {
			bestScore = result.Score
			bestMatch = result.BestMatch
		}
	}

	// Topology authority override: if topology scored 0.60, its BestMatch is the
	// canonical merge target — temporal at 0.85 on the wrong incident must not win.
	if topoMatch != nil && topoScore >= 0.60 {
		bestMatch = topoMatch
	}

	if totalScore > 1.0 {
		totalScore = 1.0
	}
	return totalScore, bestMatch
}

// makeCorrelationDecision determines what action to take based on final score
func (pce *ParallelCorrelationEngine) makeCorrelationDecision(score float64, strategyCount int) string {
	if score >= 0.75 {
		return "correlate" // High confidence correlation
	} else if score >= 0.50 {
		return "monitor" // Medium confidence, keep watching
	} else if strategyCount == 0 {
		return "create_incident" // No strategies executed
	} else {
		return "discard" // Low confidence
	}
}

// generateReasoning creates human-readable explanation of correlation decision
func (pce *ParallelCorrelationEngine) generateReasoning(strategyResults map[string]*StrategyResult, finalScore float64, decision string) string {
	var contributions []string

	for strategyName, result := range strategyResults {
		if result.Error != nil {
			continue
		}
		if result.Score > 0.5 {
			contributions = append(contributions, fmt.Sprintf("%s: %.1f%%", strategyName, result.Score*100))
		}
	}

	if len(contributions) == 0 {
		return fmt.Sprintf("No significant correlation found (final score: %.1f%%)", finalScore*100)
	}

	return fmt.Sprintf("Decision '%s' based on parallel analysis: %s (final score: %.1f%%)",
		decision, strings.Join(contributions, ", "), finalScore*100)
}

// Helper methods

func (pce *ParallelCorrelationEngine) getRecentAlerts(ctx context.Context, alert *Alert, lookback time.Duration) ([]Alert, error) {
	query := `
		SELECT id, title, description, severity, source, source_id,
		       tags, labels, metadata, fingerprint, created_at
		FROM alerts
		WHERE created_at >= $1
		AND id != $2  
		AND status != 'resolved'
		ORDER BY created_at DESC
		LIMIT 200
	`

	since := time.Now().Add(-lookback)
	rows, err := pce.db.QueryContext(ctx, query, since, alert.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var candidate Alert
		var tagsJSON, labelsJSON, metadataJSON []byte

		err := rows.Scan(
			&candidate.ID, &candidate.Title, &candidate.Description,
			&candidate.Severity, &candidate.Source, &candidate.SourceID,
			&tagsJSON, &labelsJSON, &metadataJSON,
			&candidate.Fingerprint, &candidate.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Unmarshal JSON fields — log corruption but still include the candidate
		// so temporal/title scoring can still work (C1).
		if tagsJSON != nil {
			if err := json.Unmarshal(tagsJSON, &candidate.Tags); err != nil {
				log.Printf("[correlation] unmarshal tags alert=%s: %v", candidate.ID, err)
			}
		}
		if labelsJSON != nil {
			if err := json.Unmarshal(labelsJSON, &candidate.Labels); err != nil {
				log.Printf("[correlation] unmarshal labels alert=%s: %v", candidate.ID, err)
			}
		}
		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &candidate.Metadata); err != nil {
				log.Printf("[correlation] unmarshal metadata alert=%s: %v", candidate.ID, err)
			}
		}

		alerts = append(alerts, candidate)
	}

	// C3: propagate any mid-iteration DB error instead of returning partial results.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanning recent alerts: %w", err)
	}
	return alerts, nil
}

// extractInfrastructureNode extracts a structured topology key from a K8s/infra alert
func (pce *ParallelCorrelationEngine) extractInfrastructureNode(alert *Alert) string {
	getLabel := func(keys ...string) string {
		for _, k := range keys {
			if alert.Labels != nil {
				if v, ok := alert.Labels[k]; ok && v != "" {
					return v
				}
			}
			if alert.Metadata != nil {
				if v, ok := alert.Metadata[k].(string); ok && v != "" {
					return v
				}
			}
		}
		return ""
	}

	cluster := getLabel("cluster", "kubernetes_cluster", "dt.entity.kubernetes_cluster", "k8s.cluster.name")
	namespace := getLabel("namespace", "kubernetes_namespace", "k8s.namespace.name")
	node := getLabel("node", "kubernetes_node", "nodename", "k8s.node.name", "host.name")
	workload := getLabel("deployment", "workload", "app", "service", "app.kubernetes.io/name", "k8s.deployment.name")

	// Parse K8s labels from description text (Dynatrace embeds them there)
	if cluster == "" {
		cluster = parseDescriptionLabel(alert.Description, "k8s.cluster.name")
	}
	if namespace == "" {
		namespace = parseDescriptionLabel(alert.Description, "k8s.namespace.name")
	}
	if node == "" {
		node = parseDescriptionLabel(alert.Description, "k8s.node.name")
	}
	if workload == "" {
		workload = parseDescriptionLabel(alert.Description, "k8s.workload.name")
		if workload == "" {
			workload = parseDescriptionLabel(alert.Description, "k8s.deployment.name")
		}
	}

	var parts []string
	if cluster != "" {
		parts = append(parts, "c:"+cluster)
	}
	if namespace != "" {
		parts = append(parts, "ns:"+namespace)
	}
	if node != "" {
		parts = append(parts, "n:"+node)
	}
	if workload != "" {
		parts = append(parts, "w:"+workload)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "/")
	}

	// Legacy fallbacks for non-K8s alerts (BM host, service)
	if h := getLabel("host.name", "hostname", "host", "instance", "dt.entity.host"); h != "" {
		return "h:" + h
	}
	if s := getLabel("service", "component"); s != "" {
		return "s:" + s
	}
	// NetApp storage alerts: key on netapp_cluster (set by normalizer) + optional entity name.
	// All NetApp entity types (netapp_cluster, netapp_svm, netapp_aggregate, netapp_volume, etc.)
	// carry netapp_cluster so alerts from the same storage cluster map to the same topology key.
	if nc := getLabel("netapp_cluster"); nc != "" {
		ne := getLabel("netapp_entity")
		if ne != "" {
			return "na:" + nc + "/" + ne
		}
		return "na:" + nc
	}
	return ""
}

// extractProblemDomain extracts the problem type from alert title/description (cpu/memory/network/disk/latency)
func (pce *ParallelCorrelationEngine) extractProblemDomain(alert *Alert) string {
	text := strings.ToLower(alert.Title + " " + alert.Description)
	switch {
	case strings.Contains(text, "cpu") || strings.Contains(text, "throttl"):
		return "cpu"
	case strings.Contains(text, "memory") || strings.Contains(text, "oom") || strings.Contains(text, "heap"):
		return "memory"
	case strings.Contains(text, "netapp") || strings.Contains(text, "aggregate") ||
		strings.Contains(text, "pvc") || strings.Contains(text, "volume mount") ||
		strings.Contains(text, "disk") || strings.Contains(text, "storage") || strings.Contains(text, "iops"):
		return "storage"
	case strings.Contains(text, "network") || strings.Contains(text, "latency") || strings.Contains(text, "timeout") || strings.Contains(text, "connect"):
		return "network"
	case strings.Contains(text, "pod") || strings.Contains(text, "container") || strings.Contains(text, "crash") || strings.Contains(text, "restart"):
		return "pod"
	case strings.Contains(text, "node") && (strings.Contains(text, "not ready") || strings.Contains(text, "pressure") || strings.Contains(text, "unavailable")):
		return "node"
	}
	return ""
}

// parseTopologyKey parses a structured topology key back into parts
func parseTopologyKey(key string) map[string]string {
	parts := make(map[string]string)
	for _, segment := range strings.Split(key, "/") {
		kv := strings.SplitN(segment, ":", 2)
		if len(kv) == 2 {
			parts[kv[0]] = kv[1]
		}
	}
	return parts
}

// calculateInfrastructureRelationship scores topology relationship with K8s/BM hierarchy awareness.
// Returns scores based on shared infrastructure components:
//   - 1.00: identical nodes
//   - 0.95: same BM host (host label matches node or host label)
//   - 0.92: same K8s node (pod on same node as node alert)
//   - 0.85: same cluster + same namespace
//   - 0.75: same cluster
func (pce *ParallelCorrelationEngine) calculateInfrastructureRelationship(node1, node2 string) float64 {
	if node1 == node2 {
		return 1.0
	}
	if node1 == "" || node2 == "" {
		return 0.0
	}

	p1 := parseTopologyKey(node1)
	p2 := parseTopologyKey(node2)

	// NetApp storage key (na:cluster or na:cluster/entity):
	// Same cluster = strong storage cascade signal.
	na1 := p1["na"]
	na2 := p2["na"]
	if na1 != "" && na2 != "" {
		if na1 == na2 {
			return 0.92 // Same NetApp cluster/entity — storage cascade
		}
		// Different entities on same cluster (e.g. aggregate + svm): extract cluster prefix.
		cluster1 := strings.SplitN(na1, "/", 2)[0]
		cluster2 := strings.SplitN(na2, "/", 2)[0]
		if cluster1 == cluster2 {
			return 0.88 // Different entities, same NetApp cluster
		}
	}

	// Same cluster check (prerequisite for hierarchy matching)
	c1, c1ok := p1["c"]
	c2, c2ok := p2["c"]
	sameCluster := c1ok && c2ok && c1 != "" && c1 == c2

	// BM/host K8s node causal relationship:
	// host.name in one alert == k8s.node.name in other (BM IS the K8s node)
	h1 := p1["h"]
	h2 := p2["h"]
	n1 := p1["n"]
	n2 := p2["n"]

	if h1 != "" || h2 != "" {
		// BM host match: same host label, or host label matches node name
		if (h1 != "" && h1 == h2) || (h1 != "" && h1 == n2) || (h2 != "" && h2 == n1) {
			return 0.95 // BM-level causal relationship
		}
	}

	// K8s hierarchy: same node name in same cluster (node alert + pod/workload alert)
	if sameCluster && n1 != "" && n1 == n2 {
		return 0.92 // Pod is running on the same node node-down causes pod-down
	}

	// Same cluster + same namespace
	if sameCluster {
		ns1, ns1ok := p1["ns"]
		ns2, ns2ok := p2["ns"]
		if ns1ok && ns2ok && ns1 != "" && ns1 == ns2 {
			w1 := p1["w"]
			w2 := p2["w"]
			if w1 != "" && w1 == w2 {
				return 0.95 // Same cluster + namespace + workload (different nodes)
			}
			return 0.85 // Same cluster + namespace, different workload
		}
		// One side has no namespace (e.g. node-level alert), other has namespace+workload
		// still same cluster, treat as cluster-level relationship
		return 0.75 // Same cluster, different namespace
	}

	// Legacy string-matching for non-K8s or partially structured keys
	if strings.Contains(node1, node2) || strings.Contains(node2, node1) {
		return 0.80
	}

	parts1 := strings.Split(node1, "-")
	parts2 := strings.Split(node2, "-")
	if len(parts1) >= 2 && len(parts2) >= 2 && parts1[0] == parts2[0] {
		return 0.60
	}

	return 0.0
}

func (pce *ParallelCorrelationEngine) getActiveRules(ctx context.Context) ([]SimpleRule, error) {
	if pce.db == nil {
		return nil, nil
	}
	rows, err := pce.db.QueryContext(ctx, `
		SELECT id, name, priority, conditions
		FROM correlation_rules
		WHERE enabled = TRUE
		ORDER BY priority DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []SimpleRule
	for rows.Next() {
		var r SimpleRule
		var condJSON string
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &condJSON); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(condJSON), &r.Conditions)
		r.Pattern = "db_rule"
		rules = append(rules, r)
	}
	return rules, nil
}

func (pce *ParallelCorrelationEngine) evaluateRule(rule SimpleRule, alert *Alert, candidate *Alert) bool {
	if rule.Pattern != "db_rule" {
		// Legacy hardcoded patterns
		switch rule.Pattern {
		case "service_match":
			return pce.extractServiceName(alert.Title) == pce.extractServiceName(candidate.Title)
		case "database_keywords":
			return strings.Contains(strings.ToLower(alert.Title), "database") &&
				strings.Contains(strings.ToLower(candidate.Title), "database")
		}
		return false
	}

	// Evaluate DB-loaded conditions against (alert, candidate) pair
	for _, cond := range rule.Conditions {
		if !pce.matchCondition(cond, alert, candidate) {
			return false // all conditions must match (AND logic)
		}
	}
	return len(rule.Conditions) > 0
}

func (pce *ParallelCorrelationEngine) matchCondition(c RuleCondition, alert *Alert, candidate *Alert) bool {
	strVal := func(v interface{}) string {
		if v == nil {
			return ""
		}
		return strings.ToLower(fmt.Sprintf("%v", v))
	}
	condVal := strVal(c.Value)

	switch c.Field {
	case "title":
		alertTitle := strings.ToLower(alert.Title)
		switch c.Operator {
		case "contains":
			return strings.Contains(alertTitle, condVal) && strings.Contains(strings.ToLower(candidate.Title), condVal)
		case "equals":
			return alertTitle == strings.ToLower(candidate.Title)
		case "contains_any":
			if vals, ok := c.Value.([]interface{}); ok {
				candTitle := strings.ToLower(candidate.Title)
				for _, v := range vals {
					vs := strings.ToLower(fmt.Sprintf("%v", v))
					// Both alert AND candidate must match at least one value in the list.
					if strings.Contains(alertTitle, vs) && strings.Contains(candTitle, vs) {
						return true
					}
				}
			}
		}
	case "source":
		aSource := strings.ToLower(alert.Source)
		switch c.Operator {
		case "equals":
			return aSource == condVal && strings.ToLower(candidate.Source) == condVal
		}
	case "severity":
		switch c.Operator {
		case "equals":
			return strings.ToLower(alert.Severity) == condVal
		case "in":
			if vals, ok := c.Value.([]interface{}); ok {
				sev := strings.ToLower(alert.Severity)
				for _, v := range vals {
					if sev == strings.ToLower(fmt.Sprintf("%v", v)) {
						return true
					}
				}
			}
		}
	case "description":
		desc := strings.ToLower(alert.Description)
		switch c.Operator {
		case "contains":
			return strings.Contains(desc, condVal)
		}
	case "fingerprint":
		switch c.Operator {
		case "equals":
			if condVal == "same" {
				return alert.Fingerprint == candidate.Fingerprint && alert.Fingerprint != ""
			}
			return alert.Fingerprint == condVal
		}
	case "same_namespace":
		// Check if both alerts have same namespace label
		alertNS := ""
		candNS := ""
		if alert.Labels != nil {
			alertNS = alert.Labels["namespace"]
		}
		if candidate.Labels != nil {
			candNS = candidate.Labels["namespace"]
		}
		if condVal == "true" {
			return alertNS != "" && alertNS == candNS
		}
	case "time_window_minutes":
		// Enforce the time window: both alerts must fall within the configured window.
		windowMinutes := 60.0 // default
		if v, err := strconv.ParseFloat(fmt.Sprintf("%v", c.Value), 64); err == nil && v > 0 {
			windowMinutes = v
		}
		diff := alert.CreatedAt.Sub(candidate.CreatedAt)
		if diff < 0 {
			diff = -diff
		}
		return diff.Minutes() <= windowMinutes

	case "count_threshold":
		// count_threshold is evaluated at the aggregation level (requires incident query).
		// Return true here so the rule proceeds; aggregator applies the count guard.
		return true
	}
	// Unknown condition field — log it but don't silently pass (security-relevant rules
	// with a typo should fail closed, not open).
	log.Printf("[rules] unknown condition field %q in rule — skipping condition (returns false)", c.Field)
	return false
}

func (pce *ParallelCorrelationEngine) calculateRuleScore(rule SimpleRule, alert *Alert, candidate *Alert) float64 {
	// Priority 100-300 in DB maps to 0.7-1.0 confidence range
	if rule.Priority >= 200 {
		return 0.98
	} else if rule.Priority >= 150 {
		return 0.93
	} else if rule.Priority >= 100 {
		return 0.88
	} else if rule.Priority >= 50 {
		return 0.75
	}
	return float64(rule.Priority) / 10.0
}

func (pce *ParallelCorrelationEngine) extractServiceName(title string) string {
	titleLower := strings.ToLower(title)
	services := []string{"web-service", "api-gateway", "user-service", "auth-service", "payment-service", "database"}

	for _, service := range services {
		if strings.Contains(titleLower, service) {
			return service
		}
	}
	return ""
}

func (pce *ParallelCorrelationEngine) storeCorrelationResult(ctx context.Context, result *ParallelCorrelationResult) {
	weightsJSON, _ := json.Marshal(result.WeightsUsed)

	// Pack fields not in the schema into metadata so nothing is lost.
	meta := map[string]interface{}{
		"correlation_id":      result.CorrelationID,
		"decision":            result.Decision,
		"strategy_results":    result.StrategyResults,
		"processing_time_ms":  result.TotalProcessingTime.Milliseconds(),
		"parallel_execution":  result.ParallelExecution,
		"reasoning":           result.Reasoning,
	}
	for k, v := range result.Metadata {
		if _, exists := meta[k]; !exists {
			meta[k] = v
		}
	}
	metadataJSON, _ := json.Marshal(meta)

	query := `
		INSERT INTO correlation_results (
			id, alert_id, correlation_type, confidence_score,
			recommended_action, engine_name, strategy_weights, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT DO NOTHING
	`

	_, err := pce.db.ExecContext(ctx, query,
		uuid.New(), result.AlertID, "pipeline", result.FinalScore,
		result.Decision, "parallel_engine", weightsJSON, metadataJSON, result.CreatedAt)

	if err != nil {
		log.Printf("Failed to store correlation result: %v", err)
	}
}

// GetPerformanceMetrics returns performance statistics
func (pce *ParallelCorrelationEngine) GetPerformanceMetrics() map[string]interface{} {
	pce.weightsMu.RLock()
	w := pce.weights
	pce.weightsMu.RUnlock()
	return map[string]interface{}{
		"parallel_execution": true,
		"strategy_weights":   w,
		"threshold":          pce.threshold,
		"initialized":        pce.isInitialized,
		"semantic_available": pce.semanticEngine.isModelAvailable,
		"cache_stats":        pce.semanticEngine.embeddingCache.GetStats(),
	}
}

// SimpleRule represents a DB-loaded correlation rule
type SimpleRule struct {
	ID         string
	Name       string
	Priority   int
	Pattern    string
	Conditions []RuleCondition
}
