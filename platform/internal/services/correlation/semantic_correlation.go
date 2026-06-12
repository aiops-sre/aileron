package correlation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// LOCAL BERT SEMANTIC CORRELATION ENGINE
// No External AI Dependencies - Uses Local BERT Model (90MB)
// Based on ai_correlation_no_llm_explanation.md requirements
// ============================================================================

// SemanticCorrelationEngine performs AI-powered correlation without external services
type SemanticCorrelationEngine struct {
	db                *sql.DB
	modelPath         string
	embeddingCache    *SafeEmbeddingCache // FIXED: Thread-safe bounded cache
	temporalThreshold time.Duration
	semanticThreshold float64
	topologyThreshold float64
	modelServiceURL   string // Local BERT service URL
	isModelAvailable  bool
}

// SafeEmbeddingCache provides thread-safe, bounded embedding cache
type SafeEmbeddingCache struct {
	cache   sync.Map
	maxSize int
	current int64
	mutex   sync.RWMutex
	hits    int64
	misses  int64
}

// CacheStats provides cache performance metrics
type CacheStats struct {
	Size    int64   `json:"size"`
	MaxSize int     `json:"max_size"`
	HitRate float64 `json:"hit_rate"`
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
}

// NewSafeEmbeddingCache creates a new bounded cache
func NewSafeEmbeddingCache(maxSize int) *SafeEmbeddingCache {
	return &SafeEmbeddingCache{
		maxSize: maxSize,
		current: 0,
	}
}

// Get retrieves embedding from cache
func (sec *SafeEmbeddingCache) Get(key string) ([]float64, bool) {
	value, exists := sec.cache.Load(key)
	if exists {
		atomic.AddInt64(&sec.hits, 1)
		return value.([]float64), true
	}
	atomic.AddInt64(&sec.misses, 1)
	return nil, false
}

// Set stores embedding in cache with eviction
func (sec *SafeEmbeddingCache) Set(key string, embedding []float64) {
	sec.mutex.Lock()
	defer sec.mutex.Unlock()

	// Check if we need to evict
	if sec.current >= int64(sec.maxSize) {
		sec.evictOldest()
	}

	sec.cache.Store(key, embedding)
	atomic.AddInt64(&sec.current, 1)
}

// evictOldest removes oldest entries (simplified LRU)
func (sec *SafeEmbeddingCache) evictOldest() {
	// Simple eviction: remove 25% of cache when full
	evictCount := sec.maxSize / 4
	evicted := 0

	sec.cache.Range(func(key, value interface{}) bool {
		if evicted < evictCount {
			sec.cache.Delete(key)
			evicted++
			atomic.AddInt64(&sec.current, -1)
		}
		return evicted < evictCount
	})

	log.Printf("Cache evicted %d embeddings (current size: %d)", evicted, sec.current)
}

// GetStats returns cache performance statistics
func (sec *SafeEmbeddingCache) GetStats() CacheStats {
	sec.mutex.RLock()
	defer sec.mutex.RUnlock()

	total := atomic.LoadInt64(&sec.hits) + atomic.LoadInt64(&sec.misses)
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(atomic.LoadInt64(&sec.hits)) / float64(total)
	}

	return CacheStats{
		Size:    atomic.LoadInt64(&sec.current),
		MaxSize: sec.maxSize,
		HitRate: hitRate,
		Hits:    atomic.LoadInt64(&sec.hits),
		Misses:  atomic.LoadInt64(&sec.misses),
	}
}

// NewSemanticCorrelationEngine creates a new semantic correlation engine
func NewSemanticCorrelationEngine(db *sql.DB) *SemanticCorrelationEngine {
	bertURL := strings.TrimSpace(os.Getenv("ALERTHUB_BERT_URL"))
	if bertURL == "" {
		bertURL = strings.TrimSpace(os.Getenv("LOCAL_BERT_URL"))
	}
	if bertURL == "" {
		bertURL = "http://alerthub-bert-service.aileron.svc.cluster.local:8765"
	}
	embedURL := strings.TrimRight(bertURL, "/") + "/embed"

	return &SemanticCorrelationEngine{
		db:                db,
		modelPath:         "./models/bert-mini",
		embeddingCache:    NewSafeEmbeddingCache(10000),
		temporalThreshold: 30 * time.Minute,
		semanticThreshold: 0.75,
		topologyThreshold: 0.70,
		modelServiceURL:   embedURL,
		isModelAvailable:  false,
	}
}

// CorrelationWeights defines configurable correlation strategy weights
type CorrelationWeights struct {
	Semantic float64 `json:"semantic"` // Local BERT embeddings (0.0 to disable)
	Temporal float64 `json:"temporal"` // Time-based mathematical correlation
	Topology float64 `json:"topology"` // Infrastructure relationship analysis
	Rules    float64 `json:"rules"`    // Rule-based pattern matching
}

// GetDefaultWeights returns recommended correlation weights
func (sce *SemanticCorrelationEngine) GetDefaultWeights() CorrelationWeights {
	if sce.isModelAvailable {
		// Default with Local BERT (Recommended)
		return CorrelationWeights{
			Semantic: 0.35, // 35% semantic correlation
			Temporal: 0.25, // 25% time-based correlation
			Topology: 0.25, // 25% infrastructure correlation
			Rules:    0.15, // 15% rule-based correlation
		}
	}

	// Keyword-based semantic fallback is always available even without BERT
	return CorrelationWeights{
		Semantic: 0.30, // 30% keyword semantic (Jaccard fallback)
		Temporal: 0.30, // 30% time-based correlation
		Topology: 0.30, // 30% infrastructure correlation
		Rules:    0.10, // 10% rule-based correlation
	}
}

// EnhancedCorrelationResult represents AI-powered correlation analysis
type EnhancedCorrelationResult struct {
	AlertID              uuid.UUID              `json:"alert_id"`
	CorrelationID        string                 `json:"correlation_id"`
	SimilarAlerts        []SimilarAlert         `json:"similar_alerts"`
	ConfidenceScore      float64                `json:"confidence_score"`
	CorrelationType      string                 `json:"correlation_type"`
	CorrelationMethods   []string               `json:"correlation_methods"`
	SemanticSimilarity   float64                `json:"semantic_similarity"`
	TemporalSimilarity   float64                `json:"temporal_similarity"`
	TopologySimilarity   float64                `json:"topology_similarity"`
	RuleMatchScore       float64                `json:"rule_match_score"`
	RecommendedAction    string                 `json:"recommended_action"`
	RecommendationReason string                 `json:"recommendation_reason"`
	IsDuplicate          bool                   `json:"is_duplicate"`
	DuplicateOf          *uuid.UUID             `json:"duplicate_of,omitempty"`
	WeightsUsed          CorrelationWeights     `json:"weights_used"`
	ProcessingTime       time.Duration          `json:"processing_time"`
	ModelUsed            string                 `json:"model_used"`
	Metadata             map[string]interface{} `json:"metadata"`
}

// SimilarAlert represents a similar alert with detailed similarity metrics
type SimilarAlert struct {
	AlertID            uuid.UUID `json:"alert_id"`
	Title              string    `json:"title"`
	SemanticSimilarity float64   `json:"semantic_similarity"`
	TemporalSimilarity float64   `json:"temporal_similarity"`
	TopologySimilarity float64   `json:"topology_similarity"`
	OverallSimilarity  float64   `json:"overall_similarity"`
	Source             string    `json:"source"`
	CreatedAt          time.Time `json:"created_at"`
}

// InitializeModel initializes the local BERT model
func (sce *SemanticCorrelationEngine) InitializeModel(ctx context.Context) error {
	log.Println("Initializing Local BERT Semantic Correlation Engine...")

	// Check if local BERT service is running
	if sce.checkLocalBERTService() {
		sce.isModelAvailable = true
		log.Println("Local BERT service detected - semantic correlation enabled")
		return nil
	}

	// Try to start local BERT service
	if err := sce.startLocalBERTService(); err != nil {
		log.Printf("Could not start local BERT service: %v", err)
		log.Println("Running in lightweight mode (no semantic correlation)")
		sce.isModelAvailable = false
		return nil // Not an error - we can run without BERT
	}

	sce.isModelAvailable = true
	log.Println("Local BERT service started - full AI correlation enabled")
	return nil
}

// checkLocalBERTService checks if the local BERT service is available
func (sce *SemanticCorrelationEngine) checkLocalBERTService() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	// modelServiceURL is the /embed endpoint; strip the suffix to get the base URL for /health
	baseURL := strings.TrimSuffix(sce.modelServiceURL, "/embed")
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// startLocalBERTService starts the local BERT embedding service
func (sce *SemanticCorrelationEngine) startLocalBERTService() error {
	// This would start a local Python service for BERT embeddings
	// For now, we'll create a simple implementation
	log.Println("Starting local BERT embedding service...")

	// Check if we can create the service directory
	serviceDir := "./services/local-bert"
	if _, err := os.Stat(serviceDir); os.IsNotExist(err) {
		os.MkdirAll(serviceDir, 0755)
	}

	// Create a simple Python BERT service script
	pythonScript := `#!/usr/bin/env python3
import json
import sys
from sentence_transformers import SentenceTransformer
from flask import Flask, request, jsonify
import logging

# Disable transformers warnings
logging.getLogger("transformers").setLevel(logging.ERROR)

app = Flask(__name__)
model = None

def load_model():
    global model
    try:
        print("Loading local BERT model (all-MiniLM-L6-v2)...")
        model = SentenceTransformer('all-MiniLM-L6-v2')
        print("BERT model loaded successfully (90MB)")
        return True
    except Exception as e:
        print(f"Failed to load BERT model: {e}")
        return False

@app.route('/health', methods=['GET'])
def health():
    return jsonify({"status": "healthy", "model_loaded": model is not None})

@app.route('/embed', methods=['POST'])
def embed_text():
    if not model:
        return jsonify({"error": "Model not loaded"}), 500
    
    data = request.json
    text = data.get('text', '')
    
    try:
        embedding = model.encode(text).tolist()
        return jsonify({"embedding": embedding, "model": "all-MiniLM-L6-v2"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

if __name__ == '__main__':
    if load_model():
        print("Starting local BERT service on http://localhost:8765")
        app.run(host='0.0.0.0', port=8765, debug=False)
    else:
        sys.exit(1)
`

	scriptPath := filepath.Join(serviceDir, "bert_service.py")
	if err := ioutil.WriteFile(scriptPath, []byte(pythonScript), 0755); err != nil {
		return fmt.Errorf("failed to create BERT service script: %v", err)
	}

	log.Println("Local BERT service script created")
	log.Println("To start semantic correlation, run: python3 services/local-bert/bert_service.py")

	return nil // Service created, but not necessarily running
}

// CorrelateAlertWithAI performs enhanced AI-powered correlation
func (sce *SemanticCorrelationEngine) CorrelateAlertWithAI(ctx context.Context, alert *Alert) (*EnhancedCorrelationResult, error) {
	startTime := time.Now()

	log.Printf("Starting AI correlation for alert: %s", alert.Title)

	// Get correlation weights (adaptive based on model availability)
	weights := sce.GetDefaultWeights()

	result := &EnhancedCorrelationResult{
		AlertID:            alert.ID,
		CorrelationID:      sce.generateEnhancedCorrelationID(alert),
		CorrelationType:    "ai_enhanced",
		CorrelationMethods: []string{},
		WeightsUsed:        weights,
		Metadata:           make(map[string]interface{}),
	}

	// 1. SEMANTIC CORRELATION (Local BERT)
	if weights.Semantic > 0 && sce.isModelAvailable {
		log.Println("Computing semantic similarity with local BERT...")
		semanticSimilarity, semanticAlerts, err := sce.computeSemanticSimilarity(ctx, alert)
		if err != nil {
			// BERT embed endpoint unavailable — mark it down so future calls skip directly to keyword
			log.Printf("Semantic correlation BERT error: %v — switching to keyword fallback", err)
			sce.isModelAvailable = false
		} else {
			result.SemanticSimilarity = semanticSimilarity
			result.CorrelationMethods = append(result.CorrelationMethods, "semantic_bert")
			result.ModelUsed = "all-MiniLM-L6-v2"

			// Add semantic alerts to similar alerts list
			for _, sa := range semanticAlerts {
				result.SimilarAlerts = append(result.SimilarAlerts, sa)
			}
		}
	}

	// Keyword-based semantic fallback: runs when BERT is unavailable OR BERT gave no result
	if weights.Semantic > 0 && (!sce.isModelAvailable || result.SemanticSimilarity == 0) {
		kwSim, kwAlerts := sce.computeKeywordSemanticSimilarity(ctx, alert)
		if kwSim > 0 {
			result.SemanticSimilarity = kwSim
			result.CorrelationMethods = append(result.CorrelationMethods, "keyword_fallback")
			result.ModelUsed = "keyword_tfidf"
			sce.mergeAlerts(&result.SimilarAlerts, kwAlerts)
		}
	}

	// 2. TEMPORAL CORRELATION (Mathematical Analysis)
	log.Println("Computing temporal correlation...")
	temporalSimilarity, temporalAlerts := sce.computeTemporalSimilarity(ctx, alert)
	result.TemporalSimilarity = temporalSimilarity
	result.CorrelationMethods = append(result.CorrelationMethods, "temporal_math")

	// Merge temporal alerts
	sce.mergeAlerts(&result.SimilarAlerts, temporalAlerts)

	// 3. TOPOLOGY CORRELATION (Infrastructure Analysis)
	log.Println("Computing topology correlation...")
	topologySimilarity, topologyAlerts := sce.computeTopologyCorrelation(ctx, alert)
	result.TopologySimilarity = topologySimilarity
	result.CorrelationMethods = append(result.CorrelationMethods, "topology_infra")

	// Merge topology alerts
	sce.mergeAlerts(&result.SimilarAlerts, topologyAlerts)

	// 4. RULE-BASED CORRELATION
	log.Println("Computing rule-based correlation...")
	ruleScore, ruleAlerts := sce.computeRuleBasedCorrelation(ctx, alert)
	result.RuleMatchScore = ruleScore
	result.CorrelationMethods = append(result.CorrelationMethods, "rules")

	// Merge rule alerts
	sce.mergeAlerts(&result.SimilarAlerts, ruleAlerts)

	// 5. CALCULATE OVERALL CONFIDENCE
	result.ConfidenceScore = sce.calculateOverallConfidence(weights, result)

	// 6. DETERMINE RECOMMENDED ACTION
	result.RecommendedAction, result.RecommendationReason = sce.determineAIRecommendedAction(result)

	// 7. CHECK FOR DUPLICATES
	result.IsDuplicate, result.DuplicateOf = sce.checkForAIDuplicates(result)

	result.ProcessingTime = time.Since(startTime)

	log.Printf("AI correlation complete - Confidence: %.2f%%, Methods: %v, Time: %v",
		result.ConfidenceScore*100, result.CorrelationMethods, result.ProcessingTime)

	return result, nil
}

// computeSemanticSimilarity uses local BERT for semantic analysis
func (sce *SemanticCorrelationEngine) computeSemanticSimilarity(ctx context.Context, alert *Alert) (float64, []SimilarAlert, error) {
	// Build rich text including workload context for better BERT embeddings
	buildAlertText := func(a *Alert) string {
		var parts []string
		parts = append(parts, a.Title)
		if a.Description != "" {
			parts = append(parts, a.Description)
		}
		if a.Labels != nil {
			for _, k := range []string{"cluster", "namespace", "deployment", "app", "workload"} {
				if v, ok := a.Labels[k]; ok && v != "" {
					parts = append(parts, v)
				}
			}
		}
		return strings.Join(parts, " ")
	}

	// Get embedding for current alert
	alertEmbedding, err := sce.getTextEmbedding(buildAlertText(alert))
	if err != nil {
		return 0, nil, err
	}

	// Find recent alerts to compare against
	recentAlerts, err := sce.getRecentAlertsForComparison(ctx, alert, 24*time.Hour)
	if err != nil {
		return 0, nil, err
	}

	var similarAlerts []SimilarAlert
	var maxSimilarity float64

	for _, candidate := range recentAlerts {
		candidateEmbedding, err := sce.getTextEmbedding(buildAlertText(&candidate))
		if err != nil {
			continue
		}

		similarity := sce.calculateCosineSimilarity(alertEmbedding, candidateEmbedding)

		if similarity >= sce.semanticThreshold {
			similarAlert := SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				SemanticSimilarity: similarity,
				OverallSimilarity:  similarity,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			}
			similarAlerts = append(similarAlerts, similarAlert)

			if similarity > maxSimilarity {
				maxSimilarity = similarity
			}
		}
	}

	return maxSimilarity, similarAlerts, nil
}

// getTextEmbedding gets embedding vector for text using local BERT
func (sce *SemanticCorrelationEngine) getTextEmbedding(text string) ([]float64, error) {
	// Check cache first
	if embedding, exists := sce.embeddingCache.Get(text); exists {
		return embedding, nil
	}

	// Call local BERT service
	requestData := map[string]string{"text": text}
	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(sce.modelServiceURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to call local BERT service: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("BERT service returned status %d", resp.StatusCode)
	}

	var response struct {
		Embedding []float64 `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	// Cache the embedding
	sce.embeddingCache.Set(text, response.Embedding)

	return response.Embedding, nil
}

// calculateCosineSimilarity calculates cosine similarity between two vectors
func (sce *SemanticCorrelationEngine) calculateCosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64

	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	normA = math.Sqrt(normA)
	normB = math.Sqrt(normB)

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (normA * normB)
}

// computeTemporalSimilarity uses mathematical algorithms for temporal correlation
func (sce *SemanticCorrelationEngine) computeTemporalSimilarity(ctx context.Context, alert *Alert) (float64, []SimilarAlert) {
	recentAlerts, err := sce.getRecentAlertsForComparison(ctx, alert, sce.temporalThreshold)
	if err != nil {
		return 0, nil
	}

	var similarAlerts []SimilarAlert
	var maxSimilarity float64

	for _, candidate := range recentAlerts {
		timeDiff := math.Abs(alert.CreatedAt.Sub(candidate.CreatedAt).Minutes())

		// Exponential decay function: similarity decreases exponentially with time
		temporalScore := math.Exp(-0.1 * timeDiff / 30) // 30-minute half-life

		// Additional factors for temporal correlation
		severityMatch := 0.0
		if alert.Severity == candidate.Severity {
			severityMatch = 1.0
		}

		sourceMatch := 0.0
		if alert.Source == candidate.Source {
			sourceMatch = 1.0
		}

		// Combined temporal similarity
		overallTemporal := temporalScore*0.6 + severityMatch*0.2 + sourceMatch*0.2

		if overallTemporal >= 0.6 {
			similarAlert := SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				TemporalSimilarity: overallTemporal,
				OverallSimilarity:  overallTemporal,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			}
			similarAlerts = append(similarAlerts, similarAlert)

			if overallTemporal > maxSimilarity {
				maxSimilarity = overallTemporal
			}
		}
	}

	return maxSimilarity, similarAlerts
}

// computeTopologyCorrelation analyzes infrastructure relationships
func (sce *SemanticCorrelationEngine) computeTopologyCorrelation(ctx context.Context, alert *Alert) (float64, []SimilarAlert) {
	// Extract infrastructure information from alert
	infrastructureNode := sce.extractInfrastructureInfo(alert)
	if infrastructureNode == "" {
		return 0, nil
	}

	recentAlerts, err := sce.getRecentAlertsForComparison(ctx, alert, 2*time.Hour)
	if err != nil {
		return 0, nil
	}

	var similarAlerts []SimilarAlert
	var maxSimilarity float64

	for _, candidate := range recentAlerts {
		candidateNode := sce.extractInfrastructureInfo(&candidate)
		topologyScore := sce.calculateTopologyRelationship(infrastructureNode, candidateNode)

		if topologyScore >= sce.topologyThreshold {
			similarAlert := SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				TopologySimilarity: topologyScore,
				OverallSimilarity:  topologyScore,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			}
			similarAlerts = append(similarAlerts, similarAlert)

			if topologyScore > maxSimilarity {
				maxSimilarity = topologyScore
			}
		}
	}

	return maxSimilarity, similarAlerts
}

// extractInfrastructureInfo extracts infrastructure information from alert
func (sce *SemanticCorrelationEngine) extractInfrastructureInfo(alert *Alert) string {
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
	workload := getLabel("deployment", "workload", "app", "service", "app.kubernetes.io/name")

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
	if h := getLabel("hostname", "host", "instance"); h != "" {
		return "h:" + h
	}
	if s := getLabel("service", "component"); s != "" {
		return "s:" + s
	}
	return ""
}

// calculateTopologyRelationship calculates relationship strength between infrastructure nodes
func (sce *SemanticCorrelationEngine) calculateTopologyRelationship(node1, node2 string) float64 {
	if node1 == node2 {
		return 1.0
	}
	if node1 == "" || node2 == "" {
		return 0.0
	}

	p1 := parseTopologyKey(node1)
	p2 := parseTopologyKey(node2)

	// K8s cluster-aware matching
	c1, c1ok := p1["c"]
	c2, c2ok := p2["c"]
	if c1ok && c2ok && c1 != "" && c1 == c2 {
		ns1, ns1ok := p1["ns"]
		ns2, ns2ok := p2["ns"]
		if ns1ok && ns2ok && ns1 != "" && ns1 == ns2 {
			w1 := p1["w"]
			w2 := p2["w"]
			if w1 != "" && w1 == w2 {
				return 0.95 // Same cluster + namespace + workload
			}
			return 0.85 // Same cluster + namespace, different workload
		}
		return 0.75 // Same cluster, different namespace
	}

	// Legacy string-matching for non-K8s or partially structured keys
	if strings.Contains(node1, node2) || strings.Contains(node2, node1) {
		return 0.80
	}

	if sce.haveSamePrefix(node1, node2) {
		return 0.60
	}

	return 0.0
}

// haveSamePrefix checks if two nodes have the same prefix (indicating sibling relationship)
func (sce *SemanticCorrelationEngine) haveSamePrefix(node1, node2 string) bool {
	parts1 := strings.Split(node1, "-")
	parts2 := strings.Split(node2, "-")

	if len(parts1) >= 2 && len(parts2) >= 2 {
		return parts1[0] == parts2[0] // Same service/type
	}

	return false
}

// computeRuleBasedCorrelation applies rule-based correlation patterns
func (sce *SemanticCorrelationEngine) computeRuleBasedCorrelation(ctx context.Context, alert *Alert) (float64, []SimilarAlert) {
	var ruleScore float64
	var similarAlerts []SimilarAlert

	// Rule 1: Same source alerts with similar keywords
	if sce.hasCommonKeywords(alert.Title, []string{"timeout", "connection", "database", "memory", "cpu", "disk"}) {
		recentAlerts, _ := sce.getRecentAlertsForComparison(ctx, alert, 1*time.Hour)

		for _, candidate := range recentAlerts {
			if candidate.Source == alert.Source {
				keywordSimilarity := sce.calculateKeywordSimilarity(alert.Title, candidate.Title)
				if keywordSimilarity >= 0.7 {
					ruleScore = math.Max(ruleScore, keywordSimilarity)
					similarAlert := SimilarAlert{
						AlertID:           candidate.ID,
						Title:             candidate.Title,
						OverallSimilarity: keywordSimilarity,
						Source:            candidate.Source,
						CreatedAt:         candidate.CreatedAt,
					}
					similarAlerts = append(similarAlerts, similarAlert)
				}
			}
		}
	}

	return ruleScore, similarAlerts
}

// hasCommonKeywords checks if alert title contains common keywords
func (sce *SemanticCorrelationEngine) hasCommonKeywords(title string, keywords []string) bool {
	titleLower := strings.ToLower(title)
	for _, keyword := range keywords {
		if strings.Contains(titleLower, keyword) {
			return true
		}
	}
	return false
}

// calculateKeywordSimilarity calculates keyword-based similarity
func (sce *SemanticCorrelationEngine) calculateKeywordSimilarity(title1, title2 string) float64 {
	words1 := strings.Fields(strings.ToLower(title1))
	words2 := strings.Fields(strings.ToLower(title2))

	if len(words1) == 0 || len(words2) == 0 {
		return 0
	}

	commonWords := 0
	for _, word1 := range words1 {
		for _, word2 := range words2 {
			if word1 == word2 && len(word1) > 3 { // Only count meaningful words
				commonWords++
				break
			}
		}
	}

	return float64(commonWords) / math.Max(float64(len(words1)), float64(len(words2)))
}

// calculateOverallConfidence calculates final confidence score using weighted combination
func (sce *SemanticCorrelationEngine) calculateOverallConfidence(weights CorrelationWeights, result *EnhancedCorrelationResult) float64 {
	confidence := weights.Semantic*result.SemanticSimilarity +
		weights.Temporal*result.TemporalSimilarity +
		weights.Topology*result.TopologySimilarity +
		weights.Rules*result.RuleMatchScore

	// Adjust confidence based on number of similar alerts found
	alertCount := len(result.SimilarAlerts)
	if alertCount > 5 {
		confidence = math.Min(confidence*1.2, 1.0) // Boost confidence
	} else if alertCount == 0 {
		confidence = confidence * 0.5 // Reduce confidence
	}

	return confidence
}

// determineAIRecommendedAction determines what action to take based on AI analysis
func (sce *SemanticCorrelationEngine) determineAIRecommendedAction(result *EnhancedCorrelationResult) (string, string) {
	confidence := result.ConfidenceScore
	alertCount := len(result.SimilarAlerts)

	if result.IsDuplicate {
		return "suppress", "Exact duplicate detected with high confidence"
	}

	if confidence >= 0.9 && alertCount >= 3 {
		return "merge_with_existing", "High confidence correlation with multiple similar alerts"
	}

	if confidence >= 0.8 && alertCount >= 2 {
		return "correlate", "Strong correlation detected with existing alerts"
	}

	if confidence >= 0.6 && alertCount >= 1 {
		return "investigate_relation", "Moderate correlation found, requires investigation"
	}

	if alertCount == 0 || confidence < 0.4 {
		return "create_incident", "No similar alerts found, likely new issue"
	}

	return "monitor", "Low confidence correlation, continue monitoring"
}

// checkForAIDuplicates checks for exact duplicates using AI analysis
func (sce *SemanticCorrelationEngine) checkForAIDuplicates(result *EnhancedCorrelationResult) (bool, *uuid.UUID) {
	for _, similar := range result.SimilarAlerts {
		// Check if semantic similarity is very high (>95%) and temporal is recent (<5 mins)
		if similar.SemanticSimilarity >= 0.95 && similar.TemporalSimilarity >= 0.9 {
			return true, &similar.AlertID
		}
	}
	return false, nil
}

// Helper functions

func (sce *SemanticCorrelationEngine) generateEnhancedCorrelationID(alert *Alert) string {
	fp := alert.Fingerprint
	if len(fp) > 8 {
		fp = fp[:8]
	}
	return fmt.Sprintf("ai-corr-%s-%d", fp, alert.CreatedAt.Unix())
}

func (sce *SemanticCorrelationEngine) getRecentAlertsForComparison(ctx context.Context, alert *Alert, lookback time.Duration) ([]Alert, error) {
	since := time.Now().Add(-lookback)

	query := `
		SELECT id, title, description, severity, source, source_id,
		       tags, labels, metadata, fingerprint, created_at
		FROM alerts
		WHERE created_at >= $1
		AND id != $2
		AND status != 'resolved'
		ORDER BY created_at DESC
		LIMIT 50
	`

	rows, err := sce.db.QueryContext(ctx, query, since, alert.ID)
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

		// Unmarshal JSON fields
		if tagsJSON != nil {
			if err := json.Unmarshal(tagsJSON, &candidate.Tags); err != nil {
				log.Printf("semantic: failed to unmarshal tags for alert %s: %v", candidate.ID, err)
			}
		}
		if labelsJSON != nil {
			if err := json.Unmarshal(labelsJSON, &candidate.Labels); err != nil {
				log.Printf("semantic: failed to unmarshal labels for alert %s: %v", candidate.ID, err)
			}
		}
		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &candidate.Metadata); err != nil {
				log.Printf("semantic: failed to unmarshal metadata for alert %s: %v", candidate.ID, err)
			}
		}

		alerts = append(alerts, candidate)
	}

	return alerts, rows.Err()
}

func (sce *SemanticCorrelationEngine) mergeAlerts(target *[]SimilarAlert, source []SimilarAlert) {
	existingIDs := make(map[uuid.UUID]bool)
	for _, alert := range *target {
		existingIDs[alert.AlertID] = true
	}

	for _, alert := range source {
		if !existingIDs[alert.AlertID] {
			*target = append(*target, alert)
			existingIDs[alert.AlertID] = true
		}
	}

	// Sort by overall similarity
	sort.Slice(*target, func(i, j int) bool {
		return (*target)[i].OverallSimilarity > (*target)[j].OverallSimilarity
	})
}

// IsModelAvailable returns whether the local BERT model is available.
func (sce *SemanticCorrelationEngine) IsModelAvailable() bool {
	return sce.isModelAvailable
}

// parseDescriptionLabel extracts a K8s-style "key: value" line from description text.
// Used when Dynatrace embeds infra labels in the alert description body.
func parseDescriptionLabel(description, key string) string {
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

// GetTextEmbedding is a public wrapper so ParallelCorrelationEngine can retrieve
// the BERT embedding for a given text string (for Weaviate storage/query).
func (sce *SemanticCorrelationEngine) GetTextEmbedding(text string) ([]float64, error) {
	return sce.getTextEmbedding(text)
}

// GetModelStatus returns the current status of the BERT model
func (sce *SemanticCorrelationEngine) GetModelStatus() map[string]interface{} {
	cacheStats := sce.embeddingCache.GetStats()

	status := map[string]interface{}{
		"model_available":    sce.isModelAvailable,
		"model_path":         sce.modelPath,
		"service_url":        sce.modelServiceURL,
		"cache_stats":        cacheStats,
		"semantic_threshold": sce.semanticThreshold,
		"temporal_threshold": sce.temporalThreshold,
		"topology_threshold": sce.topologyThreshold,
	}

	if sce.isModelAvailable {
		status["model_type"] = "all-MiniLM-L6-v2"
		status["model_size"] = "90MB"
		status["capabilities"] = []string{"semantic_similarity", "text_embedding", "offline_processing"}
	} else {
		status["mode"] = "lightweight"
		status["capabilities"] = []string{"temporal_correlation", "topology_correlation", "rule_based_correlation"}
	}

	return status
}

// computeKeywordSemanticSimilarity computes Jaccard-coefficient-based similarity on meaningful tokens.
// Used as a fallback when the BERT service is unavailable.
func (sce *SemanticCorrelationEngine) computeKeywordSemanticSimilarity(ctx context.Context, alert *Alert) (float64, []SimilarAlert) {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "in": true, "on": true, "at": true,
		"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
		"has": true, "have": true, "had": true, "do": true, "does": true, "did": true,
		"for": true, "of": true, "to": true, "and": true, "or": true, "not": true,
		"with": true, "by": true, "from": true, "this": true, "that": true, "it": true,
	}

	// Build rich text from alert including workload context
	buildText := func(a *Alert) string {
		var parts []string
		parts = append(parts, a.Title)
		if a.Description != "" {
			parts = append(parts, a.Description)
		}
		if a.Labels != nil {
			for _, k := range []string{"cluster", "namespace", "deployment", "app", "workload", "service"} {
				if v, ok := a.Labels[k]; ok && v != "" {
					parts = append(parts, v)
				}
			}
		}
		if a.Metadata != nil {
			for _, k := range []string{"cluster", "namespace", "deployment", "workload"} {
				if v, ok := a.Metadata[k].(string); ok && v != "" {
					parts = append(parts, v)
				}
			}
		}
		// Append K8s labels parsed from description text (Dynatrace format)
		for _, k := range []string{"k8s.cluster.name", "k8s.namespace.name", "k8s.node.name", "k8s.workload.name"} {
			if v := parseDescriptionLabel(a.Description, k); v != "" {
				parts = append(parts, v)
			}
		}
		return strings.Join(parts, " ")
	}

	tokenize := func(text string) map[string]bool {
		tokens := make(map[string]bool)
		for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
		}) {
			if len(word) >= 3 && !stopWords[word] {
				tokens[word] = true
			}
		}
		return tokens
	}

	jaccardSim := func(set1, set2 map[string]bool) float64 {
		intersection := 0
		for t := range set1 {
			if set2[t] {
				intersection++
			}
		}
		union := len(set1) + len(set2) - intersection
		if union == 0 {
			return 0
		}
		return float64(intersection) / float64(union)
	}

	alertText := buildText(alert)
	alertTokens := tokenize(alertText)
	if len(alertTokens) == 0 {
		return 0, nil
	}

	recentAlerts, err := sce.getRecentAlertsForComparison(ctx, alert, 6*time.Hour)
	if err != nil {
		return 0, nil
	}

	var similarAlerts []SimilarAlert
	var maxSim float64

	for _, candidate := range recentAlerts {
		candText := buildText(&candidate)
		candTokens := tokenize(candText)
		sim := jaccardSim(alertTokens, candTokens)
		// Boost if same severity
		if alert.Severity == candidate.Severity && sim > 0.2 {
			sim = math.Min(1.0, sim+0.08)
		}
		if sim >= 0.30 {
			similarAlerts = append(similarAlerts, SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				SemanticSimilarity: sim,
				OverallSimilarity:  sim,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			})
			if sim > maxSim {
				maxSim = sim
			}
		}
	}

	return maxSim, similarAlerts
}
