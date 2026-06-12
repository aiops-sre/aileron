package correlation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// CorrelationAggregatorService combines results from all correlation strategies
type CorrelationAggregatorService struct {
	db              *sql.DB
	redis           *redis.Client
	config          AggregatorConfig
	strategyWeights StrategyWeights
	weightsMu       sync.RWMutex // protects strategyWeights and weightsLocked
	weightsLocked   bool         // when true, Redis/adaptive overrides are ignored
	thresholds      DecisionThresholds
	metrics         *AggregatorMetrics
	incidentMatcher *IncidentMatcher
}

// AggregatorConfig holds aggregator configuration
type AggregatorConfig struct {
	WeightUpdateInterval  time.Duration `json:"weight_update_interval"` // 1 hour
	ConfidenceThreshold   float64       `json:"confidence_threshold"`   // 0.75
	MonitoringThreshold   float64       `json:"monitoring_threshold"`   // 0.50
	DiscardThreshold      float64       `json:"discard_threshold"`      // 0.30
	MaxCandidatesPerAlert int           `json:"max_candidates"`         // 10
	LookbackHours         int           `json:"lookback_hours"`         // 2
}

// DecisionThresholds defines thresholds for correlation decisions
type DecisionThresholds struct {
	CreateIncident float64 `json:"create_incident"` // 0.75+
	MergeIncident  float64 `json:"merge_incident"`  // 0.75+
	Monitor        float64 `json:"monitor"`         // 0.50-0.74
	Discard        float64 `json:"discard"`         // < 0.50
}

// AggregatorMetrics tracks aggregator performance
type AggregatorMetrics struct {
	TotalAlerts         int64   `json:"total_alerts"`
	IncidentsCreated    int64   `json:"incidents_created"`
	IncidentsMerged     int64   `json:"incidents_merged"`
	AlertsMonitored     int64   `json:"alerts_monitored"`
	AlertsDiscarded     int64   `json:"alerts_discarded"`
	AvgProcessingTimeMs float64 `json:"avg_processing_time_ms"`
	AvgConfidenceScore  float64 `json:"avg_confidence_score"`
}

// FinalCorrelationResult represents aggregated correlation decision
type FinalCorrelationResult struct {
	AlertID           uuid.UUID                  `json:"alert_id"`
	CorrelationID     string                     `json:"correlation_id"`
	Decision          CorrelationDecision        `json:"decision"`
	FinalScore        float64                    `json:"final_score"`
	Confidence        float64                    `json:"confidence"`
	BestMatch         *CandidateIncident         `json:"best_match,omitempty"`
	StrategyResults   map[string]*StrategyResult `json:"strategy_results"`
	WeightsUsed       StrategyWeights            `json:"weights_used"`
	ProcessingTime    time.Duration              `json:"processing_time"`
	Reasoning         string                     `json:"reasoning"`
	DominantStrategy  string                     `json:"dominant_strategy"`
	RecommendedAction string                     `json:"recommended_action"`
	Metadata          map[string]interface{}     `json:"metadata"`
	CreatedAt         time.Time                  `json:"created_at"`
}

// CorrelationDecision represents final correlation decisions
type CorrelationDecision string

const (
	DecisionCreateIncident CorrelationDecision = "create_incident"
	DecisionMergeIncident  CorrelationDecision = "merge_incident"
	DecisionMonitor        CorrelationDecision = "monitor"
	DecisionDiscard        CorrelationDecision = "discard"
)

// CandidateIncident represents an incident that could be correlated
type CandidateIncident struct {
	ID               uuid.UUID              `json:"id"`
	Title            string                 `json:"title"`
	Description      string                 `json:"description"`
	Severity         string                 `json:"severity"`
	Status           string                 `json:"status"`
	AlertCount       int                    `json:"alert_count"`
	AffectedServices []string               `json:"affected_services"`
	Environment      string                 `json:"environment"`
	Region           string                 `json:"region"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	Metadata         map[string]interface{} `json:"metadata"`
}

// IncidentMatcher handles finding and scoring candidate incidents
type IncidentMatcher struct {
	db     *sql.DB
	config AggregatorConfig
}

// NewCorrelationAggregatorService creates new aggregator service
func NewCorrelationAggregatorService(db *sql.DB, redis *redis.Client) *CorrelationAggregatorService {
	config := AggregatorConfig{
		WeightUpdateInterval:  1 * time.Hour,
		ConfidenceThreshold:   0.75,
		MonitoringThreshold:   0.50,
		DiscardThreshold:      0.30,
		MaxCandidatesPerAlert: 10,
		LookbackHours:         2,
	}

	thresholds := DecisionThresholds{
		CreateIncident: 0.75,
		MergeIncident:  0.75,
		Monitor:        0.50,
		Discard:        0.30,
	}

	return &CorrelationAggregatorService{
		db:         db,
		redis:      redis,
		config:     config,
		thresholds: thresholds,
		// Go-live weights — MUST match ParallelCorrelationEngine and defaultWeights() in adaptive_learning.go.
		// Topology=0.45: K8s/CloudStack graph is the most reliable signal.
		// Rules=0.25: DB-backed operator-configured rules.
		// Semantic=0.20: BERT similarity — useful but kept below topology to prevent cross-cluster merges.
		// Temporal=0.10: Pure time proximity is a weak signal; used as tiebreaker only.
		//
		// Historical note: an earlier version used Topology=0.55, Temporal=0.25.
		// That over-weighted temporal, causing false merges for co-located unrelated alerts.
		strategyWeights: StrategyWeights{
			Topology: 0.45,
			Rules:    0.25,
			Semantic: 0.20,
			Temporal: 0.10,
		},
		incidentMatcher: &IncidentMatcher{
			db:     db,
			config: config,
		},
		metrics: &AggregatorMetrics{},
	}
}

// LockWeights prevents getCurrentWeights from overwriting strategyWeights from Redis.
// Call once at startup after setting go-live weights to guarantee deterministic behaviour.
func (cas *CorrelationAggregatorService) LockWeights() {
	cas.weightsMu.Lock()
	cas.weightsLocked = true
	cas.weightsMu.Unlock()
	log.Printf("Correlation aggregator: strategy weights locked (Topology=%.2f Temporal=%.2f Rules=%.2f Semantic=%.2f)",
		cas.strategyWeights.Topology, cas.strategyWeights.Temporal,
		cas.strategyWeights.Rules, cas.strategyWeights.Semantic)
}

// AggregateCorrelationResults processes results from all correlation strategies
func (cas *CorrelationAggregatorService) AggregateCorrelationResults(ctx context.Context, alert *Alert, strategyResults map[string]*StrategyResult) (*FinalCorrelationResult, error) {
	start := time.Now()

	log.Printf("Aggregating correlation results for alert %s", alert.ID)

	// Create final result structure
	result := &FinalCorrelationResult{
		AlertID:         alert.ID,
		CorrelationID:   fmt.Sprintf("agg-%s-%d", alert.ID.String()[:8], start.Unix()),
		StrategyResults: strategyResults,
		WeightsUsed:     cas.getCurrentWeights(),
		CreatedAt:       start,
		Metadata:        make(map[string]interface{}),
	}

	// Find candidate incidents
	candidates, err := cas.incidentMatcher.FindCandidateIncidents(ctx, alert)
	if err != nil {
		log.Printf("Failed to find candidate incidents: %v", err)
		candidates = []CandidateIncident{} // Continue with empty candidates
	}

	// Calculate weighted scores for each candidate
	bestScore, bestCandidate := cas.calculateWeightedScores(alert, strategyResults, candidates)

	// Apply confidence adjustments
	confidence := cas.calculateConfidence(bestScore, strategyResults)

	// Make final decision (passes strategy results for topology determinism override)
	decision := cas.makeDecision(bestScore, confidence, len(candidates), strategyResults)

	// Determine dominant strategy
	dominantStrategy := cas.findDominantStrategy(strategyResults)

	// Generate human-readable reasoning
	reasoning := cas.generateReasoning(strategyResults, bestScore, decision, dominantStrategy)

	// Determine recommended action
	recommendedAction := cas.determineRecommendedAction(decision, bestScore, bestCandidate)

	// Populate final result
	result.FinalScore = bestScore
	result.Confidence = confidence
	result.BestMatch = bestCandidate
	result.Decision = decision
	result.DominantStrategy = dominantStrategy
	result.Reasoning = reasoning
	result.RecommendedAction = recommendedAction
	result.ProcessingTime = time.Since(start)

	// Add metadata for analysis
	result.Metadata = map[string]interface{}{
		"candidates_evaluated":   len(candidates),
		"strategies_successful":  len(strategyResults),
		"confidence_adjustments": cas.getConfidenceAdjustments(strategyResults),
		"weight_source":          "current_adaptive",
		"decision_factors":       cas.getDecisionFactors(bestScore, confidence),
	}

	// Update metrics
	cas.updateMetrics(result)

	// Store result for learning
	go cas.storeAggregationResult(ctx, result)

	log.Printf("Correlation aggregated: score=%.3f, decision=%s, confidence=%.3f, time=%v",
		bestScore, decision, confidence, result.ProcessingTime)

	return result, nil
}

// calculateWeightedScores calculates final weighted score from all strategies and picks
// the best candidate incident by combining the composite strategy score with per-candidate
// title/description overlap so noisy SQL matches don't win on recency alone.
func (cas *CorrelationAggregatorService) calculateWeightedScores(alert *Alert, strategyResults map[string]*StrategyResult, candidates []CandidateIncident) (float64, *CandidateIncident) {
	weights := cas.getCurrentWeights()

	// Topology dominance override 
	// A topology score 0.75 is structurally authoritative — the infra graph has
	// found a definitive parent/child relationship.  Diluting it through a weighted
	// average with semantic/temporal/rules scores would reduce a 0.90 topology
	// signal to ~0.26, causing correct merges to fall below the 0.60 merge threshold.
	// When topology is dominant (score 0.75 AND at least 1.4× the next-best
	// strategy), use it directly as the final score without mixing.
	if topoSR, ok := strategyResults["topology"]; ok && topoSR != nil && topoSR.Error == nil && topoSR.Score >= 0.75 {
		nextBest := 0.0
		for name, sr := range strategyResults {
			if name != "topology" && sr.Error == nil && sr.Score > nextBest {
				nextBest = sr.Score
			}
		}
		if nextBest == 0 || topoSR.Score/nextBest >= 1.4 {
			if len(candidates) == 0 {
				return topoSR.Score, nil
			}
			// H1: Trust the SQL-ordered first candidate when topology dominates.
			// FindCandidateIncidents already orders by topology relevance (cluster+domain
			// first), so the SQL winner is more accurate than re-ranking by keyword overlap
			// which picks by text similarity regardless of structural relationship.
			return topoSR.Score, &candidates[0]
		}
	}

	// Compute composite strategy score (sum of weight * strategy_score)
	var totalScore float64
	for strategyName, result := range strategyResults {
		if result.Error != nil {
			continue
		}
		var weight float64
		switch strategyName {
		case "semantic":
			weight = weights.Semantic
		case "temporal":
			weight = weights.Temporal
		case "topology":
			weight = weights.Topology
		case "rules":
			weight = weights.Rules
		default:
			weight = 0.1
		}
		// Clamp individual strategy scores to [0,1] before weighting.
		// Live data showed temporal_score > 1.0 (up to 1.15) from floating-point
		// accumulation of severity/source bonuses — this inflates the composite score.
		clampedScore := result.Score
		if clampedScore > 1.0 {
			clampedScore = 1.0
		} else if clampedScore < 0.0 {
			clampedScore = 0.0
		}
		totalScore += clampedScore * weight
	}

	if len(candidates) == 0 || totalScore <= 0.1 {
		return totalScore, nil
	}

	// Score each candidate: 70% strategy composite score + 30% text overlap with the alert.
	// Candidates are already ordered by SQL priority (cascade > domain > infra > title),
	// so we apply a tiny positional penalty so SQL ordering acts as a tiebreaker.
	alertKeywords := extractCorrelationKeywords(alert.Title + " " + alert.Description)
	bestIdx := 0
	bestCombined := -1.0

	for i, c := range candidates {
		candKeywords := extractCorrelationKeywords(c.Title + " " + c.Description)
		overlap := 0
		for kw := range alertKeywords {
			if candKeywords[kw] {
				overlap++
			}
		}
		textScore := 0.0
		if len(alertKeywords) > 0 {
			textScore = float64(overlap) / float64(len(alertKeywords))
		}
		combined := totalScore*0.7 + textScore*0.3 - float64(i)*0.01
		if combined > bestCombined {
			bestCombined = combined
			bestIdx = i
		}
	}

	bestCandidate := &candidates[bestIdx]

	// Temporal ordering: upstream incidents that pre-date this alert get a confidence boost
	topoScore := 0.0
	if topoSR, ok := strategyResults["topology"]; ok && topoSR != nil && topoSR.Error == nil {
		topoScore = topoSR.Score
	}
	if topoScore > 0.40 && !bestCandidate.CreatedAt.IsZero() && !alert.CreatedAt.IsZero() {
		if bestCandidate.CreatedAt.Before(alert.CreatedAt) {
			totalScore += 0.08
		} else if bestCandidate.CreatedAt.After(alert.CreatedAt) {
			totalScore -= 0.05
		}
		if totalScore > 1.0 {
			totalScore = 1.0
		}
	}

	// Infrastructure propagation chain boost: when the candidate and alert titles match
	// a known hard-coded cascade (e.g. BMKVMVMPod), apply up to 10% score uplift.
	// Capped so it can tip a near-threshold composite over the merge boundary but cannot
	// override a structurally wrong topology signal.
	chainScore := ScoreInfraPropagation(
		"",
		bestCandidate.Title+" "+bestCandidate.Description,
		alert.Title+" "+string(alert.EntityType),
		bestCandidate.CreatedAt,
		alert.CreatedAt,
	)
	if chainScore > 0 {
		totalScore = math.Min(1.0, totalScore+chainScore*0.10)
		log.Printf("InfraChain boost: alert=%s candidate=%s chain_score=%.3f final=%.3f",
			alert.ID, bestCandidate.ID, chainScore, totalScore)
	}

	return totalScore, bestCandidate
}

// extractCorrelationKeywords tokenises text into a lowercase word-frequency set,
// stripping punctuation and skipping common stop words.
func extractCorrelationKeywords(text string) map[string]bool {
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "is": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "has": true, "have": true, "not": true,
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '.' || r == ',' ||
			r == ':' || r == ';' || r == '!' || r == '?' || r == '(' || r == ')'
	})
	result := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) > 2 && !stop[w] {
			result[w] = true
		}
	}
	return result
}

// calculateConfidence calculates overall confidence in the correlation decision
func (cas *CorrelationAggregatorService) calculateConfidence(baseScore float64, strategyResults map[string]*StrategyResult) float64 {
	confidence := baseScore

	// Confidence adjustments based on strategy agreement
	strategyAgreement := cas.calculateStrategyAgreement(strategyResults)
	confidence *= strategyAgreement

	// Confidence boost if multiple strategies agree
	successfulStrategies := 0
	highConfidenceStrategies := 0

	for _, result := range strategyResults {
		if result.Error == nil {
			successfulStrategies++
			if result.Score > 0.7 {
				highConfidenceStrategies++
			}
		}
	}

	// Agreement boost
	if highConfidenceStrategies >= 2 {
		confidence += 0.1 // 10% boost for strategy agreement
	}
	if successfulStrategies == len(strategyResults) {
		confidence += 0.05 // 5% boost for all strategies working
	}

	// Cap confidence at 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// calculateStrategyAgreement measures how much strategies agree
func (cas *CorrelationAggregatorService) calculateStrategyAgreement(strategyResults map[string]*StrategyResult) float64 {
	scores := make([]float64, 0)

	for _, result := range strategyResults {
		if result.Error == nil {
			scores = append(scores, result.Score)
		}
	}

	if len(scores) < 2 {
		return 1.0 // Single strategy, no disagreement
	}

	// Calculate standard deviation of scores
	mean := cas.calculateMean(scores)
	variance := 0.0

	for _, score := range scores {
		variance += math.Pow(score-mean, 2)
	}
	variance /= float64(len(scores))

	stdDev := math.Sqrt(variance)

	// Convert standard deviation to agreement factor (lower stdDev = higher agreement)
	agreement := math.Max(0.5, 1.0-(stdDev*2)) // Scale appropriately

	return agreement
}

func (cas *CorrelationAggregatorService) calculateMean(scores []float64) float64 {
	sum := 0.0
	for _, score := range scores {
		sum += score
	}
	return sum / float64(len(scores))
}

// makeDecision determines the final correlation decision using signal-based rules only.
// Confidence is NOT used as a gate — it is stored for audit but does not block decisions.
//
// Decision hierarchy (first matching rule wins):
//  1. Topology signal 0.60 deterministic MERGE/CREATE (structural relationship found)
//  2. Multi-strategy agreement (2 strategies > 0.50) MERGE/CREATE
//  3. Raw score 0.40 MERGE if candidates exist, CREATE otherwise
//  4. Raw score < 0.20 DISCARD (noise)
//  5. Otherwise (0.20 score < 0.40) MONITOR (hold for burst window)
func (cas *CorrelationAggregatorService) makeDecision(score, _ float64, candidateCount int, strategyResults map[string]*StrategyResult) CorrelationDecision {
	// Rule 1: Topology determinism at 0.60 
	// Any structural topology signal above 0.60 is authoritative — the infra graph
	// found a parent/child relationship.  No confidence multiplier needed.
	if strategyResults != nil {
		if topoSR, ok := strategyResults["topology"]; ok && topoSR.Error == nil && topoSR.Score >= 0.60 {
			if candidateCount > 0 {
				return DecisionMergeIncident
			}
			return DecisionCreateIncident
		}
	}

	// Rule 2: Multi-strategy agreement 
	// Two or more independent strategies each scoring > 0.50 gives corroborating
	// evidence that overrides the need for a high aggregate score.
	// Topology must be present for multi-strategy agreement to trigger — prevents semantic+temporal false merges
	if strategyResults != nil {
		topoPresent := false
		if topoSR, ok := strategyResults["topology"]; ok && topoSR != nil && topoSR.Error == nil && topoSR.Score > 0.25 {
			topoPresent = true
		}
		highSignal := 0
		for _, sr := range strategyResults {
			if sr.Error == nil && sr.Score > 0.50 {
				highSignal++
			}
		}
		if highSignal >= 2 && topoPresent {
			if candidateCount > 0 {
				return DecisionMergeIncident
			}
			return DecisionCreateIncident
		}
	}

	// Rule 3: Moderate raw signal with candidate 
	if score >= 0.40 {
		if candidateCount > 0 {
			return DecisionMergeIncident
		}
		return DecisionCreateIncident
	}

	// Rule 4: Near-zero signal discard noise 
	if score < 0.20 {
		return DecisionDiscard
	}

	// Rule 5: Low-medium signal monitor / hold 
	// The pipeline's deferred-creation path will group these with correlated
	// events and create an incident after the hold window if no match surfaces.
	return DecisionMonitor
}

// findDominantStrategy identifies which strategy contributed most to the decision
func (cas *CorrelationAggregatorService) findDominantStrategy(strategyResults map[string]*StrategyResult) string {
	var dominantStrategy string
	var highestContribution float64

	weights := cas.getCurrentWeights()

	for strategyName, result := range strategyResults {
		if result.Error != nil {
			continue
		}

		var weight float64
		switch strategyName {
		case "semantic":
			weight = weights.Semantic
		case "temporal":
			weight = weights.Temporal
		case "topology":
			weight = weights.Topology
		case "rules":
			weight = weights.Rules
		}

		contribution := result.Score * weight
		if contribution > highestContribution {
			highestContribution = contribution
			dominantStrategy = strategyName
		}
	}

	if dominantStrategy == "" {
		return "multi_strategy"
	}
	return dominantStrategy
}

// generateReasoning creates human-readable explanation in a structured,
// multi-line format for audit logs and incident timelines.
func (cas *CorrelationAggregatorService) generateReasoning(strategyResults map[string]*StrategyResult, finalScore float64, decision CorrelationDecision, dominantStrategy string) string {
	var lines []string

	// Dominant strategy + its score
	dominantScore := 0.0
	if dominantStrategy != "" {
		if sr, ok := strategyResults[dominantStrategy]; ok && sr != nil && sr.Error == nil {
			dominantScore = sr.Score
		}
		lines = append(lines, fmt.Sprintf("Dominant strategy: %s (score: %.3f)", dominantStrategy, dominantScore))
	} else {
		lines = append(lines, "Dominant strategy: none")
	}

	// Infrastructure topology
	if topoSR, ok := strategyResults["topology"]; ok && topoSR != nil && topoSR.Error == nil {
		lines = append(lines, fmt.Sprintf("Infrastructure topology: score=%.3f", topoSR.Score))

		// Blast radius (from topology details, if available)
		if topoSR.Details != nil {
			blast := 0
			for _, k := range []string{"blast_radius", "blast_radius_nodes", "affected_nodes", "node_count"} {
				if v, ok := topoSR.Details[k]; ok {
					switch n := v.(type) {
					case int:
						blast = n
					case int64:
						blast = int(n)
					case float64:
						blast = int(n)
					}
					if blast > 0 {
						break
					}
				}
			}
			if blast > 0 {
				lines = append(lines, fmt.Sprintf("Blast radius: %d nodes", blast))
			}
		}
	}

	// Temporal correlation
	if tempSR, ok := strategyResults["temporal"]; ok && tempSR != nil && tempSR.Error == nil {
		lines = append(lines, fmt.Sprintf("Temporal correlation: score=%.3f", tempSR.Score))
	}

	// Semantic correlation
	if semSR, ok := strategyResults["semantic"]; ok && semSR != nil && semSR.Error == nil {
		lines = append(lines, fmt.Sprintf("Semantic correlation: score=%.3f", semSR.Score))
	}

	// Rules
	if rulesSR, ok := strategyResults["rules"]; ok && rulesSR != nil && rulesSR.Error == nil && rulesSR.Score > 0 {
		lines = append(lines, fmt.Sprintf("Rules correlation: score=%.3f", rulesSR.Score))
	}

	// Decision line with the threshold that applies to it
	threshold := cas.thresholds.Monitor
	switch decision {
	case DecisionCreateIncident:
		threshold = cas.thresholds.CreateIncident
	case DecisionMergeIncident:
		threshold = cas.thresholds.MergeIncident
	case DecisionMonitor:
		threshold = cas.thresholds.Monitor
	case DecisionDiscard:
		threshold = cas.thresholds.Discard
	}
	lines = append(lines, fmt.Sprintf("Decision: %s (threshold: %.2f, final_score=%.3f)",
		decision, threshold, finalScore))

	return strings.Join(lines, "\n")
}

// determineRecommendedAction suggests what action to take
func (cas *CorrelationAggregatorService) determineRecommendedAction(decision CorrelationDecision, score float64, bestMatch *CandidateIncident) string {
	switch decision {
	case DecisionCreateIncident:
		if score > 0.8 {
			return "create_incident_high_priority"
		}
		return "create_incident"

	case DecisionMergeIncident:
		if bestMatch != nil && score > 0.9 {
			return fmt.Sprintf("merge_with_incident_%s_high_confidence", bestMatch.ID.String()[:8])
		}
		return "merge_with_existing_incident"

	case DecisionMonitor:
		return "add_to_monitoring_queue"

	case DecisionDiscard:
		return "discard_low_correlation"

	default:
		return "manual_review_required"
	}
}

// IncidentMatcher finds and scores potential incidents for correlation
func (im *IncidentMatcher) FindCandidateIncidents(ctx context.Context, alert *Alert) ([]CandidateIncident, error) {
	// 6-hour lookback: wide enough to catch recurring issues (CPU, disk, etc.) that
	// recur every 30–60 min but should still map to the same root-cause incident.
	lookbackTime := time.Now().Add(-6 * time.Hour)
	causalWindow := time.Now().Add(-2 * time.Hour)

	// Extract cluster, problem domain, and infrastructure node labels for matching
	cluster := im.extractCluster(alert)
	problemDomain := im.extractProblemDomain(alert)
	nodeLabel := im.extractNode(alert)
	hostLabel := im.extractHost(alert)

	// Title key (first 50 chars, lowercase) used for direct title-match dedup.
	titleKey := im.extractTitleKey(alert)

	// Build query — only high-signal matching conditions.
	// Broad fallbacks (same severity, same environment, any critical) have been
	// removed: they returned unrelated incidents and caused wrong merges.
	//
	// Priority 0: K8s cascade — same cluster + same source, within 30 min (any domain).
	//   This is the critical path for node-down storms: host unavailable node not ready
	//   pods not ready are all different domains but one root cause. Grouping by cluster
	//   + source + time window catches all of them.
	// Priority 1: Same cluster + same problem domain (strongest — recurring same issue).
	// Priority 2: Causal infra hierarchy — same K8s node in topology_path (nodepod, BMnode).
	// Priority 3: Causal host/BM match — same host label in topology_path.
	// Priority 4: Same title prefix + same source within 2h (catches multi-source duplicates).
	//
	// $1=lookbackTime  $2=maxCandidates  $3=cluster  $4=problemDomain
	// $5=nodeLabel  $6=hostLabel  $7=causalWindow  $8=titleKey  $9=alertSource
	// $10=cascadeWindow (30 min)
	cascadeWindow := time.Now().Add(-30 * time.Minute)
	query := `
		SELECT
			i.id, i.title, i.description, i.severity,
			i.status,
			jsonb_array_length(COALESCE(i.alert_ids, '[]'::jsonb)) as alert_count,
			COALESCE(i.davis_ai_analysis->>'affected_services', '') as affected_services,
			COALESCE(i.davis_ai_analysis->>'environment', '') as environment,
			COALESCE(i.davis_ai_analysis->>'region', '') as region,
			i.created_at, i.updated_at, i.davis_ai_analysis
		FROM incidents i
		WHERE i.updated_at >= $1
		AND (
		    i.status IN ('open', 'investigating', 'acknowledged')
		    OR (
		        -- Reopen recently-resolved incidents for the same entity.
		        -- Covers flapping DT problems: disk pressure that clears then returns,
		        -- PVC that DT briefly marks resolved then reopens with a new problem ID.
		        -- 30-minute window prevents spurious merges across unrelated events.
		        i.status = 'resolved'
		        AND i.resolved_at >= NOW() - INTERVAL '30 minutes'
		    )
		)
		AND (
			-- Priority 0: K8s cluster cascade — same cluster + same source + 30 min, any domain
			-- Groups node-down storms regardless of whether domain is host/node/pod/ingress
			($3 != '' AND $9 != '' AND i.created_at >= $10
				AND i.source = $9
				AND (
					i.title ILIKE '%' || $3 || '%'
					OR i.description ILIKE '%' || $3 || '%'
					OR i.topology_path ILIKE '%' || $3 || '%'
				)
			)
			OR
			-- Priority 1: Same cluster + same problem domain
			($3 != '' AND $4 != '' AND (
				i.title ILIKE '%' || $3 || '%'
				OR i.description ILIKE '%' || $3 || '%'
				OR i.topology_path ILIKE '%' || $3 || '%'
			) AND (
				i.title ILIKE '%' || $4 || '%'
				OR i.description ILIKE '%' || $4 || '%'
			))
			OR
			-- Priority 2: Causal infra — same K8s node in topology_path
			($5 != '' AND $3 != '' AND i.created_at >= $7
				AND i.topology_path ILIKE '%' || $3 || '%'
				AND i.topology_path ILIKE '%' || $5 || '%'
			)
			OR
			-- Priority 3: Causal host/BM match
			($6 != '' AND $3 != '' AND i.created_at >= $7
				AND i.topology_path ILIKE '%' || $3 || '%'
				AND i.topology_path ILIKE '%' || $6 || '%'
			)
			OR
			-- Priority 4: Same title prefix + same source within 2h (multi-source duplicate)
			-- Cluster guard: if we know the cluster, require it to appear in topology_path/title
			-- so same-workload-name alerts from DIFFERENT clusters don't merge.
			($8 != '' AND $9 != '' AND i.created_at >= $7
				AND i.source = $9
				AND i.title ILIKE '%' || $8 || '%'
				AND ($3 = '' OR i.topology_path ILIKE '%' || $3 || '%'
				                OR i.title ILIKE '%' || $3 || '%'
				                OR i.description ILIKE '%' || $3 || '%')
			)
		)
		ORDER BY
			CASE
				WHEN $3 != '' AND $9 != '' AND i.created_at >= $10
					AND i.source = $9
					AND (i.title ILIKE '%' || $3 || '%'
					OR i.description ILIKE '%' || $3 || '%'
					OR i.topology_path ILIKE '%' || $3 || '%') THEN 0
				WHEN $3 != '' AND $4 != '' AND (
					i.title ILIKE '%' || $3 || '%'
					OR i.description ILIKE '%' || $3 || '%'
					OR i.topology_path ILIKE '%' || $3 || '%'
				) AND (
					i.title ILIKE '%' || $4 || '%'
					OR i.description ILIKE '%' || $4 || '%'
				) THEN 1
				WHEN $5 != '' AND $3 != '' AND i.topology_path ILIKE '%' || $3 || '%'
					AND i.topology_path ILIKE '%' || $5 || '%' THEN 2
				WHEN $6 != '' AND $3 != '' AND i.topology_path ILIKE '%' || $3 || '%'
					AND i.topology_path ILIKE '%' || $6 || '%' THEN 2
				WHEN $8 != '' AND $9 != '' AND i.source = $9
					AND i.title ILIKE '%' || $8 || '%'
					AND ($3 = '' OR i.topology_path ILIKE '%' || $3 || '%'
					               OR i.title ILIKE '%' || $3 || '%') THEN 3
				ELSE 4
			END,
			i.updated_at DESC
		LIMIT $2
	`

	rows, err := im.db.QueryContext(ctx, query,
		lookbackTime, im.config.MaxCandidatesPerAlert,
		cluster, problemDomain, nodeLabel, hostLabel, causalWindow,
		titleKey, alert.Source, cascadeWindow)
	if err == nil {
		log.Printf("FindCandidateIncidents: cluster=%q domain=%q node=%q host=%q titleKey=%q source=%q",
			cluster, problemDomain, nodeLabel, hostLabel, titleKey, alert.Source)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query candidate incidents: %w", err)
	}
	defer rows.Close()

	var candidates []CandidateIncident
	for rows.Next() {
		var candidate CandidateIncident
		var metadataJSON []byte
		var affectedServicesStr, environmentStr, regionStr sql.NullString

		err := rows.Scan(
			&candidate.ID, &candidate.Title, &candidate.Description,
			&candidate.Severity, &candidate.Status, &candidate.AlertCount,
			&affectedServicesStr, &environmentStr, &regionStr,
			&candidate.CreatedAt, &candidate.UpdatedAt, &metadataJSON,
		)
		if err != nil {
			continue
		}

		// Parse optional fields
		if affectedServicesStr.Valid {
			if err := json.Unmarshal([]byte(affectedServicesStr.String), &candidate.AffectedServices); err != nil {
				log.Printf("[aggregator] unmarshal affected_services for incident %s: %v", candidate.ID, err)
			}
		}
		if environmentStr.Valid {
			candidate.Environment = environmentStr.String
		}
		if regionStr.Valid {
			candidate.Region = regionStr.String
		}
		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &candidate.Metadata); err != nil {
				log.Printf("[aggregator] unmarshal metadata for incident %s: %v", candidate.ID, err)
			}
		}

		candidates = append(candidates, candidate)
	}

	log.Printf("Found %d candidate incidents for correlation (cluster=%s domain=%s)", len(candidates), cluster, problemDomain)

	// H3: propagate mid-iteration DB errors rather than returning partial results.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanning candidate incidents: %w", err)
	}
	return candidates, nil
}

// getCurrentWeights returns the current strategy weights under the lock.
// When weightsLocked is true, Redis key overrides are skipped — go-live weights
// remain deterministic regardless of what adaptive learning wrote to Redis.
func (cas *CorrelationAggregatorService) getCurrentWeights() StrategyWeights {
	// Fast path: weights are locked — read-only, use RLock to allow concurrent readers.
	cas.weightsMu.RLock()
	if cas.weightsLocked {
		w := cas.strategyWeights
		cas.weightsMu.RUnlock()
		return w
	}
	cas.weightsMu.RUnlock()

	// Slow path: weights are not locked — refresh from Redis under write lock.
	// Do the Redis reads outside the lock to avoid holding it during network I/O.
	ctx := context.Background()
	var w StrategyWeights
	var updated bool

	if cas.redis != nil {
		cas.weightsMu.RLock()
		w = cas.strategyWeights
		cas.weightsMu.RUnlock()

		if v, err := cas.redis.Get(ctx, "strategy_weight:semantic").Float64(); err == nil {
			w.Semantic = v
			updated = true
		}
		if v, err := cas.redis.Get(ctx, "strategy_weight:temporal").Float64(); err == nil {
			w.Temporal = v
			updated = true
		}
		if v, err := cas.redis.Get(ctx, "strategy_weight:topology").Float64(); err == nil {
			w.Topology = v
			updated = true
		}
		if v, err := cas.redis.Get(ctx, "strategy_weight:rules").Float64(); err == nil {
			w.Rules = v
			updated = true
		}

		if updated {
			// Validate weight sum. Accept [0.90, 1.10] to tolerate minor floating-point drift
			// from independent Redis key updates. Outside that range, re-normalize rather than
			// discard — learned weights are valuable and should not silently revert to defaults.
			sum := w.Semantic + w.Temporal + w.Topology + w.Rules
			if sum > 0 && (sum < 0.90 || sum > 1.10) {
				log.Printf("[correlation] strategy weights sum=%.3f outside [0.90,1.10] — re-normalizing", sum)
				w.Semantic /= sum
				w.Temporal /= sum
				w.Topology /= sum
				w.Rules /= sum
			}
			if sum > 0 {
				cas.weightsMu.Lock()
				cas.strategyWeights = w
				cas.weightsMu.Unlock()
			}
		}
	}

	cas.weightsMu.RLock()
	result := cas.strategyWeights
	cas.weightsMu.RUnlock()
	return result
}

// updateMetrics updates aggregator performance metrics
func (cas *CorrelationAggregatorService) updateMetrics(result *FinalCorrelationResult) {
	cas.metrics.TotalAlerts++

	switch result.Decision {
	case DecisionCreateIncident:
		cas.metrics.IncidentsCreated++
	case DecisionMergeIncident:
		cas.metrics.IncidentsMerged++
	case DecisionMonitor:
		cas.metrics.AlertsMonitored++
	case DecisionDiscard:
		cas.metrics.AlertsDiscarded++
	}

	// Update running averages
	cas.metrics.AvgProcessingTimeMs = cas.updateRunningAverage(
		cas.metrics.AvgProcessingTimeMs,
		float64(result.ProcessingTime.Milliseconds()),
		cas.metrics.TotalAlerts)

	cas.metrics.AvgConfidenceScore = cas.updateRunningAverage(
		cas.metrics.AvgConfidenceScore,
		result.Confidence,
		cas.metrics.TotalAlerts)
}

func (cas *CorrelationAggregatorService) updateRunningAverage(current, new float64, count int64) float64 {
	if count == 1 {
		return new
	}
	return ((current * float64(count-1)) + new) / float64(count)
}

// storeAggregationResult stores final result for audit and learning.
// Uses the actual correlation_results schema: confidence_score + strategy_weights + metadata.
// Fields that don't exist in the table (final_score, best_match_id, reasoning, etc.) are
// folded into the metadata JSONB column so no data is lost.
func (cas *CorrelationAggregatorService) storeAggregationResult(ctx context.Context, result *FinalCorrelationResult) {
	// Fold all extra fields into metadata to avoid schema mismatch errors.
	var bestMatchID *uuid.UUID
	if result.BestMatch != nil {
		bestMatchID = &result.BestMatch.ID
	}
	enriched := map[string]interface{}{
		"decision":          result.Decision,
		"final_score":       result.FinalScore,
		"best_match_id":     bestMatchID,
		"reasoning":         result.Reasoning,
		"dominant_strategy": result.DominantStrategy,
		"processing_time_ms": result.ProcessingTime.Milliseconds(),
		"strategy_results":  result.StrategyResults,
	}
	for k, v := range result.Metadata {
		enriched[k] = v
	}
	metadataJSON, _ := json.Marshal(enriched)
	weightsJSON, _ := json.Marshal(result.WeightsUsed)

	// Recommended action must match the DB CHECK constraint enum.
	action := result.RecommendedAction
	switch action {
	case "new_incident", "merge", "attach", "monitor", "suppress":
		// valid
	default:
		action = "monitor"
	}

	query := `
		INSERT INTO correlation_results (
			id, alert_id, correlation_type, confidence_score,
			recommended_action, engine_name, strategy_weights, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id, created_at) DO NOTHING
	`

	_, err := cas.db.ExecContext(ctx, query,
		uuid.New(), result.AlertID, "pipeline", result.FinalScore,
		action, "aggregator", weightsJSON, metadataJSON, result.CreatedAt)

	if err != nil {
		log.Printf("Failed to store aggregation result: %v", err)
	}
}

// Helper methods

func (cas *CorrelationAggregatorService) extractEnvironment(alert *Alert) string {
	if alert.Metadata != nil {
		if env, ok := alert.Metadata["environment"].(string); ok {
			return env
		}
	}
	if alert.Labels != nil {
		if env, ok := alert.Labels["environment"]; ok {
			return env
		}
	}
	return "unknown"
}

func (cas *CorrelationAggregatorService) getConfidenceAdjustments(strategyResults map[string]*StrategyResult) map[string]float64 {
	adjustments := make(map[string]float64)

	for strategyName, result := range strategyResults {
		if result.Error == nil {
			adjustments[strategyName+"_contribution"] = result.Score
		}
	}

	return adjustments
}

// extractTitleKey extracts a normalized title key for direct-duplicate detection.
func (im *IncidentMatcher) extractTitleKey(alert *Alert) string {
	t := strings.ToLower(strings.TrimSpace(alert.Title))
	for _, prefix := range []string{"[alert]", "[warning]", "[critical]", "[high]", "[medium]", "[low]", "alert:", "warning:", "incident:"} {
		t = strings.TrimPrefix(t, prefix)
	}
	t = strings.TrimSpace(t)
	if len(t) > 50 {
		t = t[:50]
	}
	return t
}

// extractEnvironment helper method for IncidentMatcher
func (im *IncidentMatcher) extractEnvironment(alert *Alert) string {
	if alert.Metadata != nil {
		if env, ok := alert.Metadata["environment"].(string); ok {
			return env
		}
	}
	if alert.Labels != nil {
		if env, ok := alert.Labels["environment"]; ok {
			return env
		}
	}
	return "unknown"
}

// extractCluster extracts the K8s cluster name from alert labels/metadata/description.
func (im *IncidentMatcher) extractCluster(alert *Alert) string {
	keys := []string{"cluster", "kubernetes_cluster", "k8s.cluster.name", "dt.entity.kubernetes_cluster"}
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
	// Parse description text (Dynatrace format: "k8s.cluster.name: mps-nonprod-rno")
	if v := parseDescriptionLabelInAggregator(alert.Description, "k8s.cluster.name"); v != "" {
		return v
	}
	// Last resort: extract cluster keyword from title text
	return extractClusterFromTitleText(alert.Title)
}

// extractClusterFromTitleText finds a cluster name embedded in an alert title, e.g.
// "Worker Node Not Ready in Prod Kubeadm cluster" "kubeadm"
// "Not all Pods ready on Ingress on Kubeadm Cluster" "kubeadm"
func extractClusterFromTitleText(title string) string {
	words := strings.Fields(strings.ToLower(title))
	skip := map[string]bool{
		"the": true, "a": true, "in": true, "on": true, "at": true,
		"prod": true, "nonprod": true, "dev": true, "staging": true,
		"k8s": true, "kubernetes": true, "cluster:": true,
	}
	for i, w := range words {
		if w == "cluster" && i > 0 {
			// Try token immediately before "cluster"
			prev := words[i-1]
			if !skip[prev] && len(prev) > 2 {
				return strings.TrimRight(prev, ".,:")
			}
			// Try two tokens back (skipping adjectives like "prod")
			if i > 1 {
				prev2 := words[i-2]
				if !skip[prev2] && len(prev2) > 2 {
					return strings.TrimRight(prev2, ".,:") + "-" + strings.TrimRight(prev, ".,:")
				}
			}
		}
	}
	return ""
}

// extractNode extracts the K8s node name from alert labels/metadata/description.
func (im *IncidentMatcher) extractNode(alert *Alert) string {
	keys := []string{"node", "kubernetes_node", "nodename", "k8s.node.name"}
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
	return parseDescriptionLabelInAggregator(alert.Description, "k8s.node.name")
}

// extractHost extracts the bare-metal/VM hostname from alert labels/metadata/description.
func (im *IncidentMatcher) extractHost(alert *Alert) string {
	keys := []string{"host.name", "hostname", "host", "instance", "dt.entity.host"}
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

// extractProblemDomain extracts problem type keyword from alert title+description.
func (im *IncidentMatcher) extractProblemDomain(alert *Alert) string {
	t := strings.ToLower(alert.Title + " " + alert.Description)
	switch {
	case strings.Contains(t, "cpu") || strings.Contains(t, "throttl") || strings.Contains(t, "saturation"):
		return "cpu"
	case strings.Contains(t, "memory") || strings.Contains(t, "oom") || strings.Contains(t, "heap"):
		return "memory"
	case strings.Contains(t, "disk") || strings.Contains(t, "storage") || strings.Contains(t, "iops"):
		return "disk"
	case strings.Contains(t, "network") || strings.Contains(t, "latency") || strings.Contains(t, "timeout"):
		return "network"
	case strings.Contains(t, "pod") || strings.Contains(t, "container") || strings.Contains(t, "crash") || strings.Contains(t, "restart") || strings.Contains(t, "backoff"):
		return "pod"
	case strings.Contains(t, "node") && (strings.Contains(t, "not ready") || strings.Contains(t, "pressure")):
		return "node"
	case strings.Contains(t, "unavailable") || strings.Contains(t, "monitoring unavailable") || strings.Contains(t, "host unavailable") || strings.Contains(t, "unreachable"):
		return "host"
	}
	return ""
}

// parseDescriptionLabelInAggregator parses a "key: value" line from description text.
func parseDescriptionLabelInAggregator(description, key string) string {
	if description == "" {
		return ""
	}
	keyColon := key + ":"
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, keyColon) {
			val := strings.TrimSpace(strings.TrimPrefix(line, keyColon))
			if val != "" {
				return val
			}
		}
	}
	return ""
}

func (cas *CorrelationAggregatorService) getDecisionFactors(score, confidence float64) map[string]interface{} {
	return map[string]interface{}{
		"raw_score":      score,
		"confidence":     confidence,
		"decision_basis": "signal_based_rules",
	}
}

// GetAggregatorMetrics returns current aggregator performance metrics
func (cas *CorrelationAggregatorService) GetAggregatorMetrics() AggregatorMetrics {
	return *cas.metrics
}

// HealthCheck verifies aggregator service health
func (cas *CorrelationAggregatorService) HealthCheck() map[string]interface{} {
	dbHealthy := cas.db.Ping() == nil
	redisHealthy := cas.redis != nil && cas.redis.Ping(context.Background()).Err() == nil

	return map[string]interface{}{
		"status":           "healthy",
		"database_healthy": dbHealthy,
		"redis_healthy":    redisHealthy,
		"configuration":    cas.config,
		"current_weights":  cas.strategyWeights,
		"thresholds":       cas.thresholds,
		"metrics":          cas.metrics,
		"capabilities": []string{
			"strategy_aggregation",
			"adaptive_weights",
			"confidence_scoring",
			"incident_matching",
			"decision_reasoning",
		},
	}
}
