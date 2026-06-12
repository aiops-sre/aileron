package correlation

// feedback_loop.go
//
// Records operator decisions (merge / split / false positive / confirmed) and
// uses them to continuously adjust the four strategy weights in
// ParallelCorrelationEngine.  Weights are persisted in the DB so they survive
// restarts and are visible via the API.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
)

// types 

// FeedbackType describes the operator's verdict on a correlation decision.
type FeedbackType string

const (
	// FeedbackConfirmed — operator merged/kept the incident as-is (correct).
	FeedbackConfirmed FeedbackType = "confirmed"
	// FeedbackFalsePositive — correlation wrongly grouped unrelated alerts.
	FeedbackFalsePositive FeedbackType = "false_positive"
	// FeedbackMissed — alerts that should have been grouped were not.
	FeedbackMissed FeedbackType = "missed_correlation"
	// FeedbackSplit — operator split an over-correlated incident.
	FeedbackSplit FeedbackType = "split"
)

// CorrelationFeedback captures a single operator decision.
type CorrelationFeedback struct {
	ID              uuid.UUID    `json:"id"`
	AlertID         uuid.UUID    `json:"alert_id"`
	IncidentID      *uuid.UUID   `json:"incident_id,omitempty"`
	CorrelationID   string       `json:"correlation_id"`
	FeedbackType    FeedbackType `json:"feedback_type"`
	// DominantStrategy is the strategy name that drove the decision being judged.
	DominantStrategy string    `json:"dominant_strategy"`
	StrategyScores   map[string]float64 `json:"strategy_scores,omitempty"`
	OperatorID       *uuid.UUID `json:"operator_id,omitempty"`
	Notes            string     `json:"notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// WeightHistory records every weight update so operators can audit the drift.
type WeightHistory struct {
	ID        uuid.UUID      `json:"id"`
	Weights   StrategyWeights `json:"weights"`
	Reason    string         `json:"reason"`
	CreatedAt time.Time      `json:"created_at"`
}

// service 

// CorrelationFeedbackService records feedback and periodically recalibrates
// strategy weights in the parent ParallelCorrelationEngine.
type CorrelationFeedbackService struct {
	db     *sql.DB
	engine *ParallelCorrelationEngine
}

// NewCorrelationFeedbackService creates the service.
func NewCorrelationFeedbackService(db *sql.DB, engine *ParallelCorrelationEngine) *CorrelationFeedbackService {
	return &CorrelationFeedbackService{db: db, engine: engine}
}

// RecordFeedback persists a single operator verdict.
func (s *CorrelationFeedbackService) RecordFeedback(ctx context.Context, fb *CorrelationFeedback) error {
	if fb.ID == uuid.Nil {
		fb.ID = uuid.New()
	}
	fb.CreatedAt = time.Now()

	scoresJSON, _ := json.Marshal(fb.StrategyScores)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO correlation_feedback (
			id, alert_id, incident_id, correlation_id,
			feedback_type, dominant_strategy, strategy_scores,
			operator_id, notes, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO NOTHING
	`,
		fb.ID, fb.AlertID, fb.IncidentID, fb.CorrelationID,
		string(fb.FeedbackType), fb.DominantStrategy, scoresJSON,
		fb.OperatorID, fb.Notes, fb.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record feedback: %w", err)
	}

	log.Printf("Feedback recorded: alert=%s type=%s strategy=%s",
		fb.AlertID, fb.FeedbackType, fb.DominantStrategy)

	// Recalibrate weights after every 10 new feedback entries.
	go s.maybeRecalibrate(context.Background())
	return nil
}

// GetFeedbackStats returns aggregate feedback counts per strategy.
func (s *CorrelationFeedbackService) GetFeedbackStats(ctx context.Context) (map[string]interface{}, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT dominant_strategy, feedback_type, COUNT(*) AS cnt
		FROM correlation_feedback
		WHERE created_at >= NOW() - INTERVAL '30 days'
		GROUP BY dominant_strategy, feedback_type
		ORDER BY dominant_strategy, feedback_type
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]map[string]int64)
	for rows.Next() {
		var strategy, ftype string
		var cnt int64
		if err := rows.Scan(&strategy, &ftype, &cnt); err != nil {
			continue
		}
		if stats[strategy] == nil {
			stats[strategy] = make(map[string]int64)
		}
		stats[strategy][ftype] = cnt
	}

	return map[string]interface{}{
		"by_strategy":      stats,
		"current_weights":  s.engine.weights,
		"threshold":        s.engine.threshold,
	}, nil
}

// GetWeightHistory returns the last N weight adjustments.
func (s *CorrelationFeedbackService) GetWeightHistory(ctx context.Context, limit int) ([]WeightHistory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, weights, reason, created_at
		FROM correlation_weight_history
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []WeightHistory
	for rows.Next() {
		var h WeightHistory
		var weightsJSON []byte
		if err := rows.Scan(&h.ID, &weightsJSON, &h.Reason, &h.CreatedAt); err != nil {
			continue
		}
		json.Unmarshal(weightsJSON, &h.Weights)
		history = append(history, h)
	}
	return history, nil
}

// ForceRecalibrate immediately recomputes and applies new weights (useful for testing).
func (s *CorrelationFeedbackService) ForceRecalibrate(ctx context.Context) (StrategyWeights, error) {
	return s.recalibrate(ctx)
}

// GetCurrentWeights returns the engine's current strategy weights.
// Delegates to ParallelCorrelationEngine.GetWeights().
func (s *CorrelationFeedbackService) GetCurrentWeights() StrategyWeights {
	if s.engine == nil {
		return StrategyWeights{}
	}
	return s.engine.GetWeights()
}

// IsEngineLocked reports whether the engine's strategy weights are frozen.
// Delegates to ParallelCorrelationEngine.IsLocked().
func (s *CorrelationFeedbackService) IsEngineLocked() bool {
	if s.engine == nil {
		return false
	}
	return s.engine.IsLocked()
}

// weight recalibration 

func (s *CorrelationFeedbackService) maybeRecalibrate(ctx context.Context) {
	// Only recalibrate every 10 entries to avoid thrashing.
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM correlation_feedback WHERE created_at >= NOW() - INTERVAL '7 days'`,
	).Scan(&total); err != nil {
		return
	}
	if total%10 != 0 {
		return
	}
	if _, err := s.recalibrate(ctx); err != nil {
		log.Printf("Weight recalibration failed: %v", err)
	}
}

// recalibrate computes new weights from the last 30 days of feedback.
//
// Algorithm:
//   - For each strategy, compute a "quality ratio" = confirmed / (confirmed + false_positive + split)
//   - Weight the quality ratio with feedback volume (more data stronger signal)
//   - Blend 70% new signal with 30% current weight (damped update to avoid oscillation)
//   - Renormalize so weights sum to 1.0
//   - Clamp each weight to [0.05, 0.60]
func (s *CorrelationFeedbackService) recalibrate(ctx context.Context) (StrategyWeights, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT dominant_strategy,
		       SUM(CASE WHEN feedback_type = 'confirmed'         THEN 1 ELSE 0 END) AS correct,
		       SUM(CASE WHEN feedback_type IN ('false_positive','split') THEN 1 ELSE 0 END) AS wrong,
		       SUM(CASE WHEN feedback_type = 'missed_correlation' THEN 1 ELSE 0 END) AS missed,
		       COUNT(*) AS total
		FROM correlation_feedback
		WHERE created_at >= NOW() - INTERVAL '30 days'
		  AND dominant_strategy != ''
		GROUP BY dominant_strategy
	`)
	if err != nil {
		return s.engine.weights, err
	}
	defer rows.Close()

	type stratStats struct {
		correct, wrong, missed, total int64
	}
	data := map[string]*stratStats{}
	for rows.Next() {
		var strat string
		var st stratStats
		if err := rows.Scan(&strat, &st.correct, &st.wrong, &st.missed, &st.total); err != nil {
			continue
		}
		data[strat] = &st
	}

	if len(data) == 0 {
		return s.engine.weights, nil // not enough data
	}

	current := s.engine.weights
	strategies := map[string]*float64{
		"semantic": &current.Semantic,
		"temporal": &current.Temporal,
		"topology": &current.Topology,
		"rules":    &current.Rules,
	}

	for strat, w := range strategies {
		st, ok := data[strat]
		// M4: require at least 10 samples before recalibrating. 5 was too low — a single
		// false positive dropped the weight to the 0.05 minimum, causing thrashing where
		// the strategy was under-used for hundreds of subsequent alerts.
		if !ok || st.total < 10 {
			continue
		}

		denominator := st.correct + st.wrong
		if denominator == 0 {
			continue
		}

		qualityRatio := float64(st.correct) / float64(denominator)

		// Volume factor: more feedback more trust in the signal (log scale, max 1.0).
		volumeFactor := math.Min(1.0, math.Log(float64(st.total)+1)/math.Log(50))

		// Damped update: 70% new signal, 30% prior.
		newWeight := qualityRatio*volumeFactor*0.7 + (*w)*0.3

		// Clamp to [0.05, 0.60].
		newWeight = math.Max(0.05, math.Min(0.60, newWeight))
		*w = newWeight
	}

	// Renormalize to sum to 1.0.
	sum := current.Semantic + current.Temporal + current.Topology + current.Rules
	if sum > 0 {
		current.Semantic /= sum
		current.Temporal /= sum
		current.Topology /= sum
		current.Rules /= sum
	}

	// Round to 3 decimal places for readability.
	round3 := func(f float64) float64 { return math.Round(f*1000) / 1000 }
	current.Semantic = round3(current.Semantic)
	current.Temporal = round3(current.Temporal)
	current.Topology = round3(current.Topology)
	current.Rules    = round3(current.Rules)

	// Apply to live engine.
	s.engine.weights = current

	// Persist to audit log.
	weightsJSON, _ := json.Marshal(current)
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO correlation_weight_history (id, weights, reason, created_at)
		VALUES ($1, $2, $3, NOW())
	`, uuid.New(), weightsJSON, "auto_recalibration_from_feedback")

	log.Printf("Strategy weights recalibrated: semantic=%.3f temporal=%.3f topology=%.3f rules=%.3f",
		current.Semantic, current.Temporal, current.Topology, current.Rules)

	return current, nil
}
