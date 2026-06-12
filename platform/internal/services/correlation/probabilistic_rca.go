package correlation

// probabilistic_rca.go
//
// ProbabilisticRCAEngine augments the existing deterministic RootCauseEngine
// with competing hypotheses, Bayesian evidence fusion, and confidence-tracked
// root-cause decisions. Persists hypothesis state in Redis for cross-alert tracking.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Evidence 

// EvidenceSource names the origin of a piece of RCA evidence.
type EvidenceSource string

const (
	EvidTopology  EvidenceSource = "topology"
	EvidSemantic  EvidenceSource = "semantic"
	EvidTemporal  EvidenceSource = "temporal"
	EvidRules     EvidenceSource = "rules"
	EvidDynatrace EvidenceSource = "dynatrace"
	EvidFeedback  EvidenceSource = "feedback"
	EvidContra    EvidenceSource = "contra"
)

// SourceTrust defines the default trust weight per evidence source.
// Adjusted by the adaptive learning engine over time.
var SourceTrust = map[EvidenceSource]float64{
	EvidDynatrace: 0.95,
	EvidTopology:  0.85,
	EvidRules:     0.80,
	EvidSemantic:  0.65,
	EvidTemporal:  0.55,
	EvidFeedback:  1.00,
	EvidContra:    1.00,
}

// Evidence is a single piece of information supporting or refuting a hypothesis.
type Evidence struct {
	Source      EvidenceSource
	Score       float64
	Description string
	AlertID     uuid.UUID
	Timestamp   time.Time
}

// RCAHypothesis 

// RCAHypothesis is a candidate root cause with accumulated evidence.
type RCAHypothesis struct {
	ID            string        `json:"id"`
	EntityID      string        `json:"entity_id"`
	EntityLabel   string        `json:"entity_label"`
	EntityType    string        `json:"entity_type"`
	InfraLevel    InfraLevel    `json:"infra_level"`
	Domain        FailureDomain `json:"domain"`
	IncidentID    *uuid.UUID    `json:"incident_id,omitempty"`
	RawConfidence float64       `json:"raw_confidence"`
	Confidence    float64       `json:"confidence"` // normalized posterior
	Evidence      []Evidence    `json:"evidence"`
	FirstSeen     time.Time     `json:"first_seen"`
	LastUpdated   time.Time     `json:"last_updated"`
	DecayFactor   float64       `json:"decay_factor"`
	EvidenceCount int           `json:"evidence_count"`
}

// AccumulateEvidence adds a new piece of evidence via Bayesian-inspired update.
func (h *RCAHypothesis) AccumulateEvidence(e Evidence) {
	trust := SourceTrust[e.Source]
	likelihood := e.Score * trust
	n := float64(h.EvidenceCount + 1)
	h.RawConfidence = (h.RawConfidence*float64(h.EvidenceCount) + likelihood) / n
	h.Evidence = append(h.Evidence, e)
	h.EvidenceCount++
	h.LastUpdated = time.Now()
}

// ApplyDecay reduces confidence proportionally to how long ago evidence was last seen.
func (h *RCAHypothesis) ApplyDecay(elapsed time.Duration) {
	windows := elapsed.Minutes() / 10.0
	h.DecayFactor *= math.Pow(0.97, windows)
	h.RawConfidence *= h.DecayFactor
}

// Engine 

// ProbabilisticRCAEngine wraps the existing deterministic RootCauseEngine and
// augments its decisions with competing hypotheses when confidence is ambiguous.
type ProbabilisticRCAEngine struct {
	rdb         *redis.Client
	topoEngine  *RecursiveTopoRCAEngine
	existingRCE *RootCauseEngine
	mu          sync.RWMutex
}

func NewProbabilisticRCAEngine(rdb *redis.Client, topo *RecursiveTopoRCAEngine, existingRCE *RootCauseEngine) *ProbabilisticRCAEngine {
	return &ProbabilisticRCAEngine{
		rdb:         rdb,
		topoEngine:  topo,
		existingRCE: existingRCE,
	}
}

// EnrichedRCADecision extends RCADecision with probabilistic context.
type EnrichedRCADecision struct {
	*RCADecision
	Confidence float64
	Hypotheses []*RCAHypothesis
	Reasoning  []string
}

// Evaluate calls the deterministic engine first. If its confidence is very high
// (Dynatrace entity) it returns immediately. Otherwise it builds competing
// hypotheses and returns the MAP estimate.
// incidentID scopes hypothesis persistence to this specific incident, preventing
// cross-incident contamination in the Redis hypothesis store.
func (p *ProbabilisticRCAEngine) Evaluate(ctx context.Context, alert *Alert, incidentID uuid.UUID) (*EnrichedRCADecision, error) {
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

	hypotheses, err := p.buildHypotheses(ctx, alert, existingDecision, incidentID)
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

	if incidentID != uuid.Nil {
		go p.persistHypotheses(context.Background(), incidentID, normalized)
	}

	return &EnrichedRCADecision{
		RCADecision: decision,
		Confidence:  best.Confidence,
		Hypotheses:  normalized,
		Reasoning:   p.buildReasoning(best, normalized),
	}, nil
}

func (p *ProbabilisticRCAEngine) buildHypotheses(ctx context.Context, alert *Alert, existing *RCADecision, incidentID uuid.UUID) ([]*RCAHypothesis, error) {
	var hypotheses []*RCAHypothesis

	// Hypothesis from the existing deterministic engine
	if existing.Action != RCAActionNoRoot {
		h := &RCAHypothesis{
			ID:          existing.RootEntityID + "_existing",
			EntityID:    existing.RootEntityID,
			EntityLabel: existing.RootEntityLabel,
			InfraLevel:  existing.RootLevel,
			FirstSeen:   time.Now(),
			LastUpdated: time.Now(),
			DecayFactor: 1.0,
		}
		trust := 0.85
		if existing.IsDynatraceRoot {
			trust = 0.95
		}
		h.AccumulateEvidence(Evidence{
			Source:      EvidTopology,
			Score:       trust,
			Description: fmt.Sprintf("deterministic engine: %s", existing.Reason),
			AlertID:     alert.ID,
			Timestamp:   time.Now(),
		})
		hypotheses = append(hypotheses, h)
	}

	// Hypothesis from recursive topology traversal
	if p.topoEngine != nil {
		topoResult, err := p.topoEngine.Traverse(ctx, alert)
		if err == nil && topoResult.RootEntity != nil && topoResult.RootConfidence > 0.30 {
			h := &RCAHypothesis{
				ID:          topoResult.RootEntity.EntityID + "_recursive_topo",
				EntityID:    topoResult.RootEntity.EntityID,
				EntityLabel: topoResult.RootEntity.Label,
				EntityType:  topoResult.RootEntity.EntityType,
				InfraLevel:  topoResult.RootEntity.InfraLevel,
				Domain:      topoResult.Domain,
				FirstSeen:   time.Now(),
				LastUpdated: time.Now(),
				DecayFactor: 1.0,
			}
			h.AccumulateEvidence(Evidence{
				Source:      EvidTopology,
				Score:       topoResult.RootConfidence,
				Description: fmt.Sprintf("recursive traversal: depth %d, chain length %d", topoResult.Depth, len(topoResult.CausalChain)),
				AlertID:     alert.ID,
				Timestamp:   time.Now(),
			})
			hypotheses = append(hypotheses, h)
		}
	}

	// Merge with prior hypotheses from Redis (cross-alert evidence accumulation).
	// Scoped to this incident only — no cross-incident contamination.
	prior, _ := p.loadPriorHypotheses(ctx, incidentID)
	for _, ph := range prior {
		merged := false
		for _, h := range hypotheses {
			if h.EntityID == ph.EntityID {
				ageFactor := math.Exp(-0.01 * time.Since(ph.LastUpdated).Minutes())
				h.RawConfidence = (h.RawConfidence + ph.RawConfidence*ageFactor) / 2
				h.EvidenceCount += ph.EvidenceCount
				merged = true
				break
			}
		}
		if !merged && ph.RawConfidence > 0.30 {
			ph.ApplyDecay(time.Since(ph.LastUpdated))
			hypotheses = append(hypotheses, ph)
		}
	}

	return hypotheses, nil
}

func (p *ProbabilisticRCAEngine) normalize(hypotheses []*RCAHypothesis) []*RCAHypothesis {
	if len(hypotheses) == 0 {
		return nil
	}

	infraPrior := map[InfraLevel]float64{
		InfraLevelBM:      1.40,
		InfraLevelKVM:     1.25,
		InfraLevelVM:      1.15,
		InfraLevelNode:    1.05,
		InfraLevelPod:     0.80,
		InfraLevelUnknown: 0.60,
	}
	for _, h := range hypotheses {
		prior := infraPrior[h.InfraLevel]
		h.RawConfidence = math.Min(1.0, h.RawConfidence*prior)
	}

	total := 0.0
	for _, h := range hypotheses {
		total += h.RawConfidence
	}
	if total > 0 {
		for _, h := range hypotheses {
			h.Confidence = h.RawConfidence / total
		}
	}

	sort.Slice(hypotheses, func(i, j int) bool {
		return hypotheses[i].Confidence > hypotheses[j].Confidence
	})
	return hypotheses
}

func (p *ProbabilisticRCAEngine) hypothesisToDecision(h *RCAHypothesis, alert *Alert) *RCADecision {
	if h.IncidentID != nil {
		return &RCADecision{
			Action:          RCAActionAttachToRoot,
			RootIncidentID:  h.IncidentID,
			RootEntityID:    h.EntityID,
			RootEntityLabel: h.EntityLabel,
			RootLevel:       h.InfraLevel,
			Reason:          fmt.Sprintf("probabilistic: confidence=%.3f, evidence=%d", h.Confidence, h.EvidenceCount),
		}
	}
	if h.Confidence > 0.65 && h.InfraLevel >= InfraLevelNode {
		return &RCADecision{
			Action:          RCAActionCreateRoot,
			RootEntityID:    h.EntityID,
			RootEntityLabel: h.EntityLabel,
			RootLevel:       h.InfraLevel,
			Reason:          fmt.Sprintf("probabilistic root: confidence=%.3f", h.Confidence),
		}
	}
	return &RCADecision{
		Action: RCAActionNoRoot,
		Reason: fmt.Sprintf("insufficient confidence: best=%.3f", h.Confidence),
	}
}

func (p *ProbabilisticRCAEngine) buildReasoning(best *RCAHypothesis, all []*RCAHypothesis) []string {
	var r []string
	r = append(r, fmt.Sprintf("MAP estimate: %s (%s) confidence=%.3f",
		best.EntityLabel, best.EntityType, best.Confidence))
	if len(all) > 1 {
		r = append(r, fmt.Sprintf("competing hypotheses: %d candidates, runner-up confidence=%.3f",
			len(all), all[1].Confidence))
	}
	for _, ev := range best.Evidence {
		r = append(r, fmt.Sprintf("[%s] score=%.3f: %s", ev.Source, ev.Score, ev.Description))
	}
	return r
}

// Redis persistence

// hypothesisKeyTTL: 72 hours. Incidents can remain active for extended periods;
// 2 hours was too short — hypotheses were expiring mid-investigation.
// Keys are scoped per incident (not global), so growth is bounded.
const hypothesisKeyTTL = 72 * time.Hour

// hypothesisKey returns a Redis key scoped to a specific incident and entity.
// Format: rca:hyp:{incidentID}:{entityID}
// This prevents cross-incident hypothesis contamination — the previous format
// rca:hyp:{alertID} was a global key that caused unrelated incidents to bleed
// into each other's RCA hypothesis sets.
func hypothesisKey(incidentID, entityID string) string {
	return fmt.Sprintf("rca:hyp:%s:%s", incidentID, entityID)
}

// persistHypotheses writes one Redis key per hypothesis entity, scoped to the
// incident. If incidentID is nil/zero, no write is performed.
func (p *ProbabilisticRCAEngine) persistHypotheses(ctx context.Context, incidentID uuid.UUID, hypotheses []*RCAHypothesis) {
	if incidentID == uuid.Nil {
		return
	}
	for _, h := range hypotheses {
		if h.EntityID == "" {
			continue
		}
		data, err := json.Marshal(h)
		if err != nil {
			log.Printf("probabilisticRCA.persistHypotheses: marshal error entity=%s: %v", h.EntityID, err)
			continue
		}
		key := hypothesisKey(incidentID.String(), h.EntityID)
		if err := p.rdb.Set(ctx, key, data, hypothesisKeyTTL).Err(); err != nil {
			log.Printf("probabilisticRCA.persistHypotheses: redis error key=%s: %v", key, err)
		}
	}
}

// loadPriorHypotheses fetches previously-persisted hypotheses for this specific
// incident. The scan pattern rca:hyp:{incidentID}:* ensures only this incident's
// hypotheses are returned — no cross-incident contamination.
// Returns (nil, nil) when incidentID is zero (no-op).
func (p *ProbabilisticRCAEngine) loadPriorHypotheses(ctx context.Context, incidentID uuid.UUID) ([]*RCAHypothesis, error) {
	if incidentID == uuid.Nil {
		return nil, nil
	}
	// Per-incident cap: one key per entity in the infrastructure graph.
	// 200 is far above any realistic topology size.
	const maxHypotheses = 200
	var (
		result []*RCAHypothesis
		cursor uint64
	)
	pattern := fmt.Sprintf("rca:hyp:%s:*", incidentID.String())
	for {
		select {
		case <-ctx.Done():
			return result, nil // return partial results on cancellation
		default:
		}
		if len(result) >= maxHypotheses {
			break
		}

		keys, next, err := p.rdb.Scan(ctx, cursor, pattern, 100).Result()
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
			// Each key holds exactly one RCAHypothesis (not an array).
			var h RCAHypothesis
			if json.Unmarshal([]byte(val), &h) == nil {
				result = append(result, &h)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return result, nil
}
