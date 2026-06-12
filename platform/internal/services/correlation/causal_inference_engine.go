package correlation

// causal_inference_engine.go
//
// CausalInferenceEngine (CACIE) is the unified authoritative layer that sits
// ABOVE all existing correlation engines. It consumes their outputs, fuses
// evidence using Bayesian log-odds, applies topology dominance arbitration,
// and produces a single CausalInferenceResult that the pipeline can act on
// without further disambiguation.
//
// Integration: call CACIE.Infer() from enrichIncidentV2 after the
// ProbabilisticRCAEngine and RecursiveTopoRCAEngine have already run.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CausalInferenceResult 

// CausalInferenceResult is the authoritative output of a single CACIE run.
type CausalInferenceResult struct {
	IncidentID         uuid.UUID
	RootEntity         *CausalNode
	RootConfidence     float64
	Domain             FailureDomain
	FailureClass       CanonicalFailureClass
	Hypotheses         []*RCAHypothesis      // ranked by posterior confidence
	CausalChain        []*CausalLink         // root alert entity
	BlastRadius        []*ImpactedNode       // root all downstream
	SuppressedEntities []string              // entity labels to suppress
	PatternMatch       *CausalPatternMatch   // non-nil when a template matched
	EvidenceSummary    []string              // human-readable reasoning lines
	ProcessingTime     time.Duration
	ComputedAt         time.Time
}

// CausalPatternMatch describes a causal_pattern_templates row that matched.
type CausalPatternMatch struct {
	PatternName     string
	Description     string
	ConfidenceBoost float64
}

// Evidence fusion constants 

// logOddsFromProb converts probability p to log-odds. Clamps to avoid ±Inf.
func logOddsFromProb(p float64) float64 {
	p = math.Max(0.001, math.Min(0.999, p))
	return math.Log(p / (1 - p))
}

// probFromLogOdds converts log-odds back to probability.
func probFromLogOdds(lo float64) float64 {
	return 1.0 / (1.0 + math.Exp(-lo))
}

// fusedPosterior updates a prior probability with a likelihood using dampened
// Bayesian log-odds: logOdds' = logOdds(prior) + damping * logOdds(likelihood).
// Damping < 1.0 prevents a single high-trust source from dominating completely.
func fusedPosterior(prior, likelihood, damping float64) float64 {
	lo := logOddsFromProb(prior) + damping*logOddsFromProb(likelihood)
	return probFromLogOdds(lo)
}

// CausalInferenceEngine 

// CausalInferenceEngine fuses outputs from all sub-engines into one authoritative
// causal graph.  All sub-engine fields are optional; the engine degrades gracefully.
type CausalInferenceEngine struct {
	db              *sql.DB
	rce             *RootCauseEngine
	recursiveTopo   *RecursiveTopoRCAEngine
	probabilisticRCA *ProbabilisticRCAEngine
	ontology        *OntologyEngine
	mu              sync.Mutex // protects pattern-cache updates
	patternCache    []*cachedPattern
	patternCacheExp time.Time
}

type cachedPattern struct {
	name            string
	description     string
	triggerTypes    []string
	triggerDomain   FailureDomain
	confidenceBoost float64
}

// NewCausalInferenceEngine creates the engine. All deps are optional.
func NewCausalInferenceEngine(db *sql.DB) *CausalInferenceEngine {
	return &CausalInferenceEngine{db: db}
}

func (e *CausalInferenceEngine) SetRCE(rce *RootCauseEngine)                         { e.rce = rce }
func (e *CausalInferenceEngine) SetRecursiveTopo(r *RecursiveTopoRCAEngine)           { e.recursiveTopo = r }
func (e *CausalInferenceEngine) SetProbabilisticRCA(p *ProbabilisticRCAEngine)        { e.probabilisticRCA = p }
func (e *CausalInferenceEngine) SetOntology(o *OntologyEngine)                       { e.ontology = o }

// Infer is the primary entry point. It collects evidence from all wired engines,
// fuses it, and returns the authoritative CausalInferenceResult.
func (e *CausalInferenceEngine) Infer(
	ctx context.Context,
	alert *Alert,
	incidentID uuid.UUID,
	// Pre-computed results from engines already run upstream (may be nil)
	rceDecision  *RCADecision,
	topoResult   *RecursiveTopoResult,
	probHyps     []*RCAHypothesis,
) (*CausalInferenceResult, error) {
	start := time.Now()

	result := &CausalInferenceResult{
		IncidentID: incidentID,
		ComputedAt: start,
	}

	// 1. Ontology classification 
	var ontoResult *OntologyResult
	if e.ontology != nil {
		ontoResult = e.ontology.Classify(alert)
		if ontoResult != nil {
			result.Domain = ontoResult.Domain
			result.FailureClass = ontoResult.Class
		}
	}

	// 2. Collect hypotheses from all sources 
	hyps := e.collectHypotheses(ctx, alert, rceDecision, topoResult, probHyps)
	if len(hyps) == 0 {
		result.EvidenceSummary = []string{"no hypotheses generated — insufficient topology/signal data"}
		result.ProcessingTime = time.Since(start)
		return result, nil
	}

	// 3. Fuse evidence with Bayesian log-odds 
	e.fuseEvidence(hyps)

	// 4. Apply infra-level priors (BM > KVM > VM > node > pod) 
	e.applyInfraLevelPriors(hyps)

	// 5. Topology dominance arbitration 
	best := e.arbitrate(hyps, topoResult)

	// 6. Pattern template matching 
	if pm := e.matchPatternTemplate(ctx, alert, best); pm != nil {
		result.PatternMatch = pm
		best.Confidence = math.Min(1.0, best.Confidence+pm.ConfidenceBoost)
		result.EvidenceSummary = append(result.EvidenceSummary,
			fmt.Sprintf("pattern match: %s (+%.2f confidence)", pm.PatternName, pm.ConfidenceBoost))
		go e.updatePatternStats(pm.PatternName)
	}

	// 7. Populate result 
	result.Hypotheses = hyps
	if best != nil && best.EntityID != "" {
		result.RootEntity = &CausalNode{
			EntityID:   best.EntityID,
			EntityType: best.EntityType,
			Label:      best.EntityLabel,
			InfraLevel: best.InfraLevel,
			Domain:     best.Domain,
		}
		result.RootConfidence = best.Confidence
	}

	if topoResult != nil {
		result.CausalChain = topoResult.CausalChain
		result.BlastRadius = topoResult.BlastRadius
		// Collect suppress-candidate entity labels from blast radius
		for _, n := range topoResult.BlastRadius {
			if n.Node != nil && n.Node.Label != "" && n.PropagationScore >= 0.40 {
				result.SuppressedEntities = append(result.SuppressedEntities, n.Node.Label)
			}
		}
	}

	// Build reasoning summary
	result.EvidenceSummary = append(result.EvidenceSummary,
		e.buildReasoning(result, hyps)...)

	result.ProcessingTime = time.Since(start)

	// 8. Persist to incident_causal_graphs 
	go func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e.persistCausalGraph(persistCtx, result)
	}()

	// 9. Persist cross-alert causal relationships 
	if topoResult != nil {
		go func() {
			persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			e.persistCausalRelationships(persistCtx, incidentID, topoResult)
		}()
	}

	log.Printf("CACIE incident=%s root=%q conf=%.3f domain=%s blast=%d elapsed=%v",
		incidentID, result.RootEntity.safeLabel(), result.RootConfidence,
		result.Domain, len(result.BlastRadius), result.ProcessingTime.Round(time.Millisecond))

	return result, nil
}

// safeLabel returns the entity label or "<unknown>" if the node is nil.
func (n *CausalNode) safeLabel() string {
	if n == nil {
		return "<unknown>"
	}
	return n.Label
}

// Evidence collection 

func (e *CausalInferenceEngine) collectHypotheses(
	ctx context.Context,
	alert *Alert,
	rceDecision *RCADecision,
	topoResult *RecursiveTopoResult,
	probHyps []*RCAHypothesis,
) []*RCAHypothesis {
	byEntityID := map[string]*RCAHypothesis{}

	addOrMerge := func(entityID, label, entityType string, level InfraLevel, domain FailureDomain, source EvidenceSource, score float64, desc string) {
		if entityID == "" {
			return
		}
		h, ok := byEntityID[entityID]
		if !ok {
			h = &RCAHypothesis{
				ID:          entityID + "_cacie",
				EntityID:    entityID,
				EntityLabel: label,
				EntityType:  entityType,
				InfraLevel:  level,
				Domain:      domain,
				FirstSeen:   time.Now(),
				LastUpdated: time.Now(),
				DecayFactor: 1.0,
			}
			byEntityID[entityID] = h
		}
		if h.EntityLabel == "" {
			h.EntityLabel = label
		}
		h.AccumulateEvidence(Evidence{
			Source:      source,
			Score:       score,
			Description: desc,
			AlertID:     alert.ID,
			Timestamp:   time.Now(),
		})
	}

	// From RCE deterministic decision
	if rceDecision != nil && rceDecision.RootEntityID != "" {
		trust := 0.85
		if rceDecision.IsDynatraceRoot {
			trust = 0.95
		}
		addOrMerge(rceDecision.RootEntityID, rceDecision.RootEntityLabel, "",
			rceDecision.RootLevel, DomainUnknown, EvidTopology, trust,
			"rce: "+rceDecision.Reason)
	}

	// From recursive topology traversal
	if topoResult != nil && topoResult.RootEntity != nil {
		addOrMerge(topoResult.RootEntity.EntityID, topoResult.RootEntity.Label,
			topoResult.RootEntity.EntityType, topoResult.RootEntity.InfraLevel,
			topoResult.Domain, EvidTopology, topoResult.RootConfidence,
			fmt.Sprintf("neo4j traversal: depth=%d chain=%d", topoResult.Depth, len(topoResult.CausalChain)))
	}

	// From probabilistic RCA hypotheses
	for _, ph := range probHyps {
		addOrMerge(ph.EntityID, ph.EntityLabel, ph.EntityType,
			ph.InfraLevel, ph.Domain, EvidSemantic, ph.RawConfidence,
			fmt.Sprintf("probabilistic: n_evidence=%d", ph.EvidenceCount))
	}

	// Compile
	result := make([]*RCAHypothesis, 0, len(byEntityID))
	for _, h := range byEntityID {
		result = append(result, h)
	}
	return result
}

// Evidence fusion 

// infraLevelDamping returns an evidence trust dampening multiplier for a given
// infra level. Higher-level nodes (BM, KVM) propagate failures reliably, so
// their evidence should be trusted more before priors are applied.
func infraLevelDamping(level InfraLevel) float64 {
	switch level {
	case InfraLevelBM:
		return 1.00
	case InfraLevelKVM:
		return 0.92
	case InfraLevelVM:
		return 0.85
	case InfraLevelNode:
		return 0.75
	case InfraLevelPod:
		return 0.60
	default:
		return 0.70
	}
}

// fuseEvidence applies the Bayesian log-odds update to each hypothesis using
// accumulated evidence, weighted by source trust and infra-level dampening.
func (e *CausalInferenceEngine) fuseEvidence(hyps []*RCAHypothesis) {
	for _, h := range hyps {
		posterior := 0.50 // uniform prior
		damp := infraLevelDamping(h.InfraLevel)
		for _, ev := range h.Evidence {
			trust := SourceTrust[ev.Source]
			posterior = fusedPosterior(posterior, ev.Score, trust*damp)
		}
		h.RawConfidence = posterior
	}
}

// applyInfraLevelPriors multiplies RawConfidence by an infra-level multiplier
// before normalization, reflecting the empirical observation that higher-level
// entities (BM, KVM) are far more likely to be the true root cause.
func (e *CausalInferenceEngine) applyInfraLevelPriors(hyps []*RCAHypothesis) {
	priors := map[InfraLevel]float64{
		InfraLevelBM:      1.50,
		InfraLevelKVM:     1.30,
		InfraLevelVM:      1.15,
		InfraLevelNode:    1.05,
		InfraLevelPod:     0.80,
		InfraLevelUnknown: 0.65,
	}
	for _, h := range hyps {
		h.RawConfidence = math.Min(1.0, h.RawConfidence*priors[h.InfraLevel])
	}

	// Normalize so confidences sum to 1.0
	total := 0.0
	for _, h := range hyps {
		total += h.RawConfidence
	}
	if total > 0 {
		for _, h := range hyps {
			h.Confidence = h.RawConfidence / total
		}
	}

	// Confidence diversity penalty: a single hypothesis normalized against itself
	// always produces 1.0, giving operators false certainty (symptom=cause tautology).
	// When the pipeline has only one candidate (single RCE source, no topo corroboration)
	// cap at 0.85 (LIKELY) so CONFIRMED is reserved for multi-source agreement.
	if len(hyps) == 1 && hyps[0].Confidence > 0.85 {
		hyps[0].Confidence = 0.85
	}

	sort.Slice(hyps, func(i, j int) bool {
		return hyps[i].Confidence > hyps[j].Confidence
	})
}

// Topology-dominant arbitration 

// arbitrate picks the best hypothesis using the topology-dominance rules:
//  1. If topology evidence confidence 0.75 AND 1.4× runner-up topology wins
//  2. If single hypothesis with confidence > 0.65 use it
//  3. If top hypothesis InfraLevel > all others by 2 levels it wins regardless of score
//  4. Otherwise return the highest-confidence hypothesis
func (e *CausalInferenceEngine) arbitrate(hyps []*RCAHypothesis, topo *RecursiveTopoResult) *RCAHypothesis {
	if len(hyps) == 0 {
		return nil
	}

	best := hyps[0]

	// Rule 1: topology source dominance
	if topo != nil && topo.RootEntity != nil && topo.RootConfidence >= 0.75 {
		for _, h := range hyps {
			if h.EntityID == topo.RootEntity.EntityID {
				if len(hyps) < 2 || h.Confidence/hyps[1].Confidence >= 1.4 {
					return h
				}
			}
		}
	}

	// Rule 3: infra-level dominance (BM always wins over pod-level)
	if len(hyps) > 1 {
		for _, h := range hyps[1:] {
			if int(best.InfraLevel)-int(h.InfraLevel) >= 2 {
				return best // best is already highest infra level
			}
		}
	}

	return best
}

// Pattern template matching 

func (e *CausalInferenceEngine) matchPatternTemplate(ctx context.Context, alert *Alert, best *RCAHypothesis) *CausalPatternMatch {
	if best == nil {
		return nil
	}
	patterns := e.loadPatternTemplates(ctx)
	alertDomain := AlertDomain(alert)
	for _, p := range patterns {
		if p.triggerDomain != alertDomain {
			continue
		}
		for _, t := range p.triggerTypes {
			if strings.EqualFold(best.EntityType, t) ||
				(alert.Labels != nil && strings.EqualFold(alert.Labels["entity_type"], t)) {
				return &CausalPatternMatch{
					PatternName:     p.name,
					Description:     p.description,
					ConfidenceBoost: p.confidenceBoost,
				}
			}
		}
	}
	return nil
}

func (e *CausalInferenceEngine) loadPatternTemplates(ctx context.Context) []*cachedPattern {
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Now().Before(e.patternCacheExp) && len(e.patternCache) > 0 {
		return e.patternCache
	}
	if e.db == nil {
		return nil
	}
	rows, err := e.db.QueryContext(ctx, `
		SELECT name, description, trigger_domain, trigger_entity_types, confidence_boost
		FROM causal_pattern_templates
		WHERE is_active = true
	`)
	if err != nil {
		return e.patternCache // serve stale on error
	}
	defer rows.Close()
	var patterns []*cachedPattern
	for rows.Next() {
		var p cachedPattern
		var domainStr string
		var types []string
		if err := rows.Scan(&p.name, &p.description, &domainStr, (*stringSlice)(&types), &p.confidenceBoost); err != nil {
			continue
		}
		p.triggerDomain = FailureDomain(domainStr)
		p.triggerTypes = types
		patterns = append(patterns, &p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[causal] pattern cache load error: %v", err)
	}
	e.patternCache = patterns
	e.patternCacheExp = time.Now().Add(5 * time.Minute)
	return patterns
}

// stringSlice is a helper to scan a TEXT[] column into []string.
type stringSlice []string

func (s *stringSlice) Scan(src interface{}) error {
	if src == nil {
		return nil
	}
	switch v := src.(type) {
	case string:
		// Postgres array literal: {a,b,c}
		v = strings.Trim(v, "{}")
		if v == "" {
			return nil
		}
		for _, part := range strings.Split(v, ",") {
			*s = append(*s, strings.TrimSpace(part))
		}
	case []byte:
		return s.Scan(string(v))
	}
	return nil
}

func (e *CausalInferenceEngine) updatePatternStats(name string) {
	if e.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = e.db.ExecContext(ctx, `
		UPDATE causal_pattern_templates
		SET observation_count = observation_count + 1, last_matched_at = NOW()
		WHERE name = $1
	`, name)
}

// Persistence 

func (e *CausalInferenceEngine) persistCausalGraph(ctx context.Context, r *CausalInferenceResult) {
	if e.db == nil || r.IncidentID == uuid.Nil {
		return
	}

	blastJSON, _ := json.Marshal(r.BlastRadius)
	hypsJSON, _ := json.Marshal(r.Hypotheses)
	chainJSON, _ := json.Marshal(r.CausalChain)
	propagationMap := map[string]float64{}
	for _, n := range r.BlastRadius {
		if n.Node != nil {
			propagationMap[n.Node.EntityID] = n.PropagationScore
		}
	}
	propMapJSON, _ := json.Marshal(propagationMap)

	rootID, rootLabel, rootType := "", "", ""
	rootLevel := 0
	rootConf := r.RootConfidence
	if r.RootEntity != nil {
		rootID = r.RootEntity.EntityID
		rootLabel = r.RootEntity.Label
		rootType = r.RootEntity.EntityType
		rootLevel = int(r.RootEntity.InfraLevel)
	}

	_, err := e.db.ExecContext(ctx, `
		INSERT INTO incident_causal_graphs
			(incident_id, root_entity_id, root_entity_label, root_entity_type,
			 root_infra_level, root_confidence, domain,
			 blast_radius, hypotheses, causal_chain, propagation_map,
			 suppressed_entities, reasoning, engine_version, computed_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'v2',NOW(),NOW())
		ON CONFLICT (incident_id) DO UPDATE SET
			root_entity_id     = EXCLUDED.root_entity_id,
			root_entity_label  = EXCLUDED.root_entity_label,
			root_entity_type   = EXCLUDED.root_entity_type,
			root_infra_level   = EXCLUDED.root_infra_level,
			root_confidence    = EXCLUDED.root_confidence,
			domain             = EXCLUDED.domain,
			blast_radius       = EXCLUDED.blast_radius,
			hypotheses         = EXCLUDED.hypotheses,
			causal_chain       = EXCLUDED.causal_chain,
			propagation_map    = EXCLUDED.propagation_map,
			suppressed_entities= EXCLUDED.suppressed_entities,
			reasoning          = EXCLUDED.reasoning,
			updated_at         = NOW()
	`,
		r.IncidentID, rootID, rootLabel, rootType,
		rootLevel, rootConf, string(r.Domain),
		blastJSON, hypsJSON, chainJSON, propMapJSON,
		r.SuppressedEntities, r.EvidenceSummary,
	)
	if err != nil {
		log.Printf("CACIE: failed to persist causal graph for incident %s: %v", r.IncidentID, err)
	}

	// Update incidents.davis_ai_analysis and correlation_confidence so the
	// EvolutionEngine's sameRoot merge check can find the CACIE root entity.
	if r.RootEntity != nil {
		davisJSON, _ := json.Marshal(map[string]interface{}{
			"root_entity_id":    rootID,
			"root_entity_label": rootLabel,
			"root_entity_type":  rootType,
			"root_infra_level":  rootLevel,
			"domain":            string(r.Domain),
			"failure_class":     string(r.FailureClass),
		})
		_, uerr := e.db.ExecContext(ctx, `
			UPDATE incidents
			SET davis_ai_analysis    = $1,
			    correlation_confidence = GREATEST(COALESCE(correlation_confidence, 0), $2),
			    updated_at            = NOW()
			WHERE id = $3`,
			davisJSON, rootConf, r.IncidentID)
		if uerr != nil {
			log.Printf("CACIE: failed to update incidents.davis_ai_analysis for %s: %v", r.IncidentID, uerr)
		}
	}
}

func (e *CausalInferenceEngine) persistCausalRelationships(ctx context.Context, incidentID uuid.UUID, topo *RecursiveTopoResult) {
	if e.db == nil || topo == nil || topo.RootEntity == nil {
		return
	}
	for _, n := range topo.BlastRadius {
		if n.Node == nil {
			continue
		}
		_, _ = e.db.ExecContext(ctx, `
			INSERT INTO causal_relationships
				(source_entity_id, target_entity_id, relationship_type, edge_type,
				 confidence_score, propagation_score, incident_id,
				 infra_level_source, infra_level_target, hop_index,
				 first_seen_at, last_confirmed_at, observation_count)
			VALUES ($1,$2,'downstream',$3,$4,$5,$6,$7,$8,$9,NOW(),NOW(),1)
			ON CONFLICT (source_entity_id, target_entity_id, relationship_type, edge_type)
			DO UPDATE SET
				last_confirmed_at = NOW(),
				observation_count = causal_relationships.observation_count + 1,
				propagation_score = EXCLUDED.propagation_score,
				incident_id       = EXCLUDED.incident_id
		`,
			topo.RootEntity.EntityID, n.Node.EntityID,
			firstEdgeType(n.CausalChain),
			math.Min(1.0, topo.RootConfidence),
			n.PropagationScore,
			incidentID,
			int(topo.RootEntity.InfraLevel), int(n.Node.InfraLevel),
			n.Depth,
		)
	}

	// Record the rootalert entity as a root_cause relationship
	_, _ = e.db.ExecContext(ctx, `
		INSERT INTO causal_relationships
			(source_entity_id, target_entity_id, relationship_type, edge_type,
			 confidence_score, incident_id, infra_level_source, hop_index,
			 first_seen_at, last_confirmed_at, observation_count)
		VALUES ($1,$2,'root_cause','HOSTS',$3,$4,$5,0,NOW(),NOW(),1)
		ON CONFLICT (source_entity_id, target_entity_id, relationship_type, edge_type)
		DO UPDATE SET
			last_confirmed_at = NOW(),
			observation_count = causal_relationships.observation_count + 1,
			confidence_score  = GREATEST(causal_relationships.confidence_score, EXCLUDED.confidence_score)
	`, topo.RootEntity.EntityID, topo.AlertEntityID,
		topo.RootConfidence, incidentID, int(topo.RootEntity.InfraLevel))
}

func firstEdgeType(chain []*CausalLink) string {
	if len(chain) > 0 {
		return chain[0].EdgeType
	}
	return "HOSTS"
}

// Reasoning 

func (e *CausalInferenceEngine) buildReasoning(r *CausalInferenceResult, hyps []*RCAHypothesis) []string {
	var lines []string
	if r.RootEntity != nil {
		lines = append(lines, fmt.Sprintf("root cause: %s (%s) at level %s, confidence=%.3f",
			r.RootEntity.Label, r.RootEntity.EntityType,
			r.RootEntity.InfraLevel.String(), r.RootConfidence))
	}
	if len(hyps) > 1 {
		lines = append(lines, fmt.Sprintf("competing hypotheses: %d, runner-up=%.3f",
			len(hyps), hyps[1].Confidence))
	}
	if len(r.BlastRadius) > 0 {
		lines = append(lines, fmt.Sprintf("blast radius: %d downstream entities (top score=%.3f)",
			len(r.BlastRadius), highestPropagationScore(r.BlastRadius)))
	}
	if r.Domain != "" {
		lines = append(lines, fmt.Sprintf("failure domain: %s / class: %s", r.Domain, r.FailureClass))
	}
	return lines
}
