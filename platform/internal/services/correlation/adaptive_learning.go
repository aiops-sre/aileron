package correlation

// adaptive_learning.go
//
// AdaptiveLearningEngine extends the existing CorrelationFeedbackService with
// per-domain strategy weight tuning via Exponential Moving Average (EMA).
// Weights are learned independently per (domain, source, cluster) context and
// persisted in the correlation_feature_store table.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Feature key & domain weights 

// FeatureKey identifies a model parameter context.
type FeatureKey struct {
	Domain  FailureDomain
	Source  string // "dynatrace", "prometheus", "" = all
	Cluster string // specific cluster or "" = all
}

func (fk FeatureKey) String() string {
	return fmt.Sprintf("feature:%s:%s:%s", fk.Domain, fk.Source, fk.Cluster)
}

// DomainWeights holds strategy weights tuned for a specific domain context.
type DomainWeights struct {
	FeatureKey FeatureKey      `json:"feature_key"`
	Weights    StrategyWeights `json:"weights"`
	SampleSize int             `json:"sample_size"`
	Accuracy   float64         `json:"accuracy"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// LearningEvent is emitted when operator feedback is received on a correlation.
type LearningEvent struct {
	AlertID        string
	IncidentID     string
	Domain         FailureDomain
	Source         string
	Cluster        string
	DecisionMade   string // "create_incident", "merge_incident", "discard"
	IsCorrect      bool
	StrategyScores map[string]float64
	Timestamp      time.Time
}

// Engine 

// AdaptiveLearningEngine wraps CorrelationFeedbackService with per-domain
// weight tuning and a persistent feature store.
type AdaptiveLearningEngine struct {
	db        *sql.DB
	rdb       *redis.Client
	existing  *CorrelationFeedbackService
	globalEMA float64 // EMA smoothing factor (0.05–0.15)
	featureMu sync.RWMutex
	features  map[string]*DomainWeights
	// disabled freezes learned-weight output at defaults and short-circuits
	// feedback processing. atomic.Bool so concurrent reads/writes are race-free.
	disabled atomic.Bool
}

// SetDisabled toggles adaptive learning. When disabled, GetWeightsForContext
// returns default weights and OnFeedback is a no-op. Persisted feature data
// is retained so learning can be re-enabled later without loss.
func (ale *AdaptiveLearningEngine) SetDisabled(disabled bool) { ale.disabled.Store(disabled) }

func NewAdaptiveLearningEngine(db *sql.DB, rdb *redis.Client, existing *CorrelationFeedbackService) *AdaptiveLearningEngine {
	e := &AdaptiveLearningEngine{
		db:        db,
		rdb:       rdb,
		existing:  existing,
		globalEMA: 0.08,
		features:  make(map[string]*DomainWeights),
	}
	go e.loadFeaturesFromDB(context.Background())
	return e
}

// OnFeedback is called when an operator confirms or rejects a correlation decision.
func (e *AdaptiveLearningEngine) OnFeedback(ctx context.Context, event LearningEvent) {
	if e.disabled.Load() {
		return // accumulate no learning during go-live
	}
	// Delegate to existing feedback service (preserve existing behavior)
	if e.existing != nil {
		go func() {
			fb := &CorrelationFeedback{
				DominantStrategy: dominantStrategyFromScores(event.StrategyScores),
				StrategyScores:   event.StrategyScores,
				CreatedAt:        event.Timestamp,
			}
			if event.IsCorrect {
				fb.FeedbackType = FeedbackConfirmed
			} else {
				fb.FeedbackType = FeedbackFalsePositive
			}
			if err := e.existing.RecordFeedback(ctx, fb); err != nil {
				log.Printf("[AdaptiveLearning] record feedback: %v", err)
			}
		}()
	}

	// Update domain-specific weights via EMA
	e.updateDomainWeights(ctx, event)
}

func (e *AdaptiveLearningEngine) updateDomainWeights(ctx context.Context, event LearningEvent) {
	key := FeatureKey{Domain: event.Domain, Source: event.Source, Cluster: event.Cluster}
	keyStr := key.String()

	e.featureMu.Lock()
	defer e.featureMu.Unlock()

	fw, exists := e.features[keyStr]
	if !exists {
		fw = &DomainWeights{
			FeatureKey: key,
			Weights:    defaultWeights(),
			UpdatedAt:  time.Now(),
		}
		e.features[keyStr] = fw
	}

	alpha := e.globalEMA
	dominant := dominantStrategyFromScores(event.StrategyScores)

	if event.IsCorrect {
		fw.Weights = emaBoost(fw.Weights, dominant, alpha)
		fw.Accuracy = fw.Accuracy*(1-alpha) + 1.0*alpha
	} else {
		fw.Weights = emaPenalty(fw.Weights, dominant, alpha)
		fw.Accuracy = fw.Accuracy*(1-alpha) + 0.0*alpha
	}
	fw.SampleSize++
	fw.UpdatedAt = time.Now()

	if fw.SampleSize >= 5 {
		go e.persistFeature(context.Background(), fw)
	}
}

// GetWeightsForContext returns learned strategy weights for a given alert context.
// Falls back through hierarchy: domain+source+cluster domain+source domain global.
func (e *AdaptiveLearningEngine) GetWeightsForContext(alert *Alert) StrategyWeights {
	if e.disabled.Load() {
		return defaultWeights()
	}
	onto := NewOntologyEngine().Classify(alert)

	cluster := ""
	if alert.Labels != nil {
		cluster = alert.Labels["k8s.cluster.name"]
	}

	candidates := []FeatureKey{
		{Domain: onto.Domain, Source: alert.Source, Cluster: cluster},
		{Domain: onto.Domain, Source: alert.Source},
		{Domain: onto.Domain},
		{Domain: DomainUnknown},
	}

	e.featureMu.RLock()
	defer e.featureMu.RUnlock()

	for _, key := range candidates {
		if fw, ok := e.features[key.String()]; ok && fw.SampleSize >= 10 {
			return fw.Weights
		}
	}
	return defaultWeights()
}

// EMA helpers 

func emaBoost(w StrategyWeights, dominant string, alpha float64) StrategyWeights {
	boost := alpha * 0.03
	switch dominant {
	case "semantic":
		w.Semantic = clampWeight(w.Semantic + boost)
	case "temporal":
		w.Temporal = clampWeight(w.Temporal + boost)
	case "topology":
		w.Topology = clampWeight(w.Topology + boost)
	case "rules":
		w.Rules = clampWeight(w.Rules + boost)
	}
	return normalizeStrategyWeights(w)
}

func emaPenalty(w StrategyWeights, dominant string, alpha float64) StrategyWeights {
	penalty := alpha * 0.03
	switch dominant {
	case "semantic":
		w.Semantic = clampWeight(w.Semantic - penalty)
	case "temporal":
		w.Temporal = clampWeight(w.Temporal - penalty)
	case "topology":
		w.Topology = clampWeight(w.Topology - penalty)
	case "rules":
		w.Rules = clampWeight(w.Rules - penalty)
	}
	return normalizeStrategyWeights(w)
}

func normalizeStrategyWeights(w StrategyWeights) StrategyWeights {
	total := w.Semantic + w.Temporal + w.Topology + w.Rules
	if total == 0 {
		return defaultWeights()
	}
	return StrategyWeights{
		Semantic: w.Semantic / total,
		Temporal: w.Temporal / total,
		Topology: w.Topology / total,
		Rules:    w.Rules / total,
	}
}

func defaultWeights() StrategyWeights {
	return StrategyWeights{Semantic: 0.20, Temporal: 0.10, Topology: 0.45, Rules: 0.25}
}

func clampWeight(v float64) float64 {
	return math.Max(0.05, math.Min(0.55, v))
}

func dominantStrategyFromScores(scores map[string]float64) string {
	best, bestScore := "", 0.0
	for k, v := range scores {
		if v > bestScore {
			best, bestScore = k, v
		}
	}
	return best
}

// Persistence 

func (e *AdaptiveLearningEngine) persistFeature(ctx context.Context, fw *DomainWeights) {
	if e.db == nil {
		return
	}
	data, err := json.Marshal(fw)
	if err != nil {
		return
	}
	_, err = e.db.ExecContext(ctx, `
		INSERT INTO correlation_feature_store (feature_key, domain, source, cluster, weights_json, sample_size, accuracy, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (feature_key) DO UPDATE SET
			weights_json = EXCLUDED.weights_json,
			sample_size  = EXCLUDED.sample_size,
			accuracy     = EXCLUDED.accuracy,
			updated_at   = NOW()`,
		fw.FeatureKey.String(), string(fw.FeatureKey.Domain),
		fw.FeatureKey.Source, fw.FeatureKey.Cluster,
		string(data), fw.SampleSize, fw.Accuracy)
	if err != nil {
		log.Printf("[AdaptiveLearning] persist feature %s: %v", fw.FeatureKey.String(), err)
		return
	}
	if e.rdb != nil {
		e.rdb.Set(ctx, "feature:"+fw.FeatureKey.String(), data, 24*time.Hour)
	}
}

func (e *AdaptiveLearningEngine) loadFeaturesFromDB(ctx context.Context) {
	if e.db == nil {
		return
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT feature_key, weights_json, sample_size, accuracy FROM correlation_feature_store`)
	if err != nil {
		return
	}
	defer rows.Close()

	e.featureMu.Lock()
	defer e.featureMu.Unlock()

	for rows.Next() {
		var key, weightsJSON string
		var sampleSize int
		var accuracy float64
		if err := rows.Scan(&key, &weightsJSON, &sampleSize, &accuracy); err != nil {
			continue
		}
		var fw DomainWeights
		if err := json.Unmarshal([]byte(weightsJSON), &fw); err != nil {
			continue
		}
		fw.SampleSize = sampleSize
		fw.Accuracy = accuracy
		e.features[key] = &fw
	}
}
