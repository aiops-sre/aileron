package pipeline

import (
	"encoding/json"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
)

// RCAInvestigationContext carries all Go-computed deterministic results to the
// Python RCA orchestrator. It is serialised to JSON and sent in the
// `go_context` field of the /api/v1/investigations POST body.
type RCAInvestigationContext struct {
	// Ontology classification
	Domain        string  `json:"domain"`
	OntologyClass string  `json:"ontology_class"`
	OntologyConf  float64 `json:"ontology_confidence"`

	// Recursive topology traversal (Go Neo4j engine)
	RootEntityID    string             `json:"root_entity_id"`
	RootEntityLabel string             `json:"root_entity_label"`
	RootEntityType  string             `json:"root_entity_type"`
	RootConfidence  float64            `json:"root_confidence"`
	CausalChain     []RCAChainLink     `json:"causal_chain"`
	BlastRadiusIDs  []string           `json:"blast_radius_entity_ids"`
	BlastRadiusSize int                `json:"blast_radius_size"`
	PropagationMap  map[string]float64 `json:"propagation_map"`
	TopoDomain      string             `json:"topo_domain"`
	TopoDepth       int                `json:"topo_depth"`
	TopoReasoning   []string           `json:"topo_reasoning"`

	// Probabilistic hypotheses (ranked by confidence)
	Hypotheses []RCAHypothesisSummary `json:"hypotheses"`

	// Correlation engine metadata
	CorrelationID    string  `json:"correlation_id"`
	CorrelationScore float64 `json:"correlation_score"`
	CorrelationConf  float64 `json:"correlation_confidence"`
	DominantStrategy string  `json:"dominant_strategy"`

	// CACIE fused outputs (pattern-boosted root entity and confidence)
	CACIEConfidence    float64 `json:"cacie_confidence,omitempty"`
	CACIEPatternName   string  `json:"cacie_pattern_name,omitempty"`
	CACIEPatternBoost  float64 `json:"cacie_pattern_boost,omitempty"`

	// Temporal anchor — when the Go engine last ran topology traversal
	TemporalAnchor time.Time `json:"temporal_anchor"`
	ComputedAt     time.Time `json:"computed_at"`
}

// RCAChainLink is a single directed hop in the causal chain.
type RCAChainLink struct {
	FromID    string  `json:"from_id"`
	FromLabel string  `json:"from_label"`
	ToID      string  `json:"to_id"`
	ToLabel   string  `json:"to_label"`
	EdgeType  string  `json:"edge_type"`
	Weight    float64 `json:"weight"`
	HopIndex  int     `json:"hop_index"`
}

// RCAHypothesisSummary is a compact serialisable form of correlation.RCAHypothesis.
type RCAHypothesisSummary struct {
	ID          string  `json:"id"`
	EntityID    string  `json:"entity_id"`
	EntityLabel string  `json:"entity_label"`
	EntityType  string  `json:"entity_type"`
	Domain      string  `json:"domain"`
	Confidence  float64 `json:"confidence"`
}

// BuildRCAContext assembles an RCAInvestigationContext from Go pipeline outputs.
// Nil inputs are handled gracefully so callers with partial results can still pass context.
func BuildRCAContext(
	result *correlation.FinalCorrelationResult,
	topoResult *correlation.RecursiveTopoResult,
	hypotheses []*correlation.RCAHypothesis,
	onto *correlation.OntologyResult,
	cacie *correlation.CausalInferenceResult,
) *RCAInvestigationContext {
	ctx := &RCAInvestigationContext{
		ComputedAt:     time.Now(),
		CausalChain:    []RCAChainLink{},
		BlastRadiusIDs: []string{},
		Hypotheses:     []RCAHypothesisSummary{},
		TopoReasoning:  []string{},
		PropagationMap: map[string]float64{},
	}

	if onto != nil {
		ctx.Domain = string(onto.Domain)
		ctx.OntologyClass = string(onto.Class)
		ctx.OntologyConf = onto.Confidence
	}

	if result != nil {
		ctx.CorrelationID = result.CorrelationID
		ctx.CorrelationScore = result.FinalScore
		ctx.CorrelationConf = result.Confidence
		ctx.DominantStrategy = result.DominantStrategy
	}

	if topoResult != nil {
		if topoResult.RootEntity != nil {
			ctx.RootEntityID = topoResult.RootEntity.EntityID
			ctx.RootEntityLabel = topoResult.RootEntity.Label
			ctx.RootEntityType = topoResult.RootEntity.EntityType
		}
		ctx.RootConfidence = topoResult.RootConfidence
		ctx.TopoDomain = string(topoResult.Domain)
		ctx.TopoDepth = topoResult.Depth
		ctx.TopoReasoning = topoResult.Reasoning
		ctx.TemporalAnchor = topoResult.ComputedAt

		if topoResult.PropagationMap != nil {
			ctx.PropagationMap = topoResult.PropagationMap
		}

		for _, link := range topoResult.CausalChain {
			if link.From != nil && link.To != nil {
				ctx.CausalChain = append(ctx.CausalChain, RCAChainLink{
					FromID:    link.From.EntityID,
					FromLabel: link.From.Label,
					ToID:      link.To.EntityID,
					ToLabel:   link.To.Label,
					EdgeType:  link.EdgeType,
					Weight:    link.Weight,
					HopIndex:  link.HopIndex,
				})
			}
		}

		for _, imp := range topoResult.BlastRadius {
			if imp.Node != nil {
				ctx.BlastRadiusIDs = append(ctx.BlastRadiusIDs, imp.Node.EntityID)
			}
		}
		ctx.BlastRadiusSize = len(ctx.BlastRadiusIDs)
	}

	for _, h := range hypotheses {
		ctx.Hypotheses = append(ctx.Hypotheses, RCAHypothesisSummary{
			ID:          h.ID,
			EntityID:    h.EntityID,
			EntityLabel: h.EntityLabel,
			EntityType:  h.EntityType,
			Domain:      string(h.Domain),
			Confidence:  h.Confidence,
		})
	}

	// Apply CACIE fused outputs — override topo-only fields when CACIE produces richer results.
	if cacie != nil {
		if cacie.RootEntity != nil && cacie.RootConfidence > ctx.RootConfidence {
			ctx.RootEntityID    = cacie.RootEntity.EntityID
			ctx.RootEntityLabel = cacie.RootEntity.Label
			ctx.RootEntityType  = cacie.RootEntity.EntityType
			ctx.RootConfidence  = cacie.RootConfidence
		}
		if ctx.Domain == "" && cacie.Domain != "" {
			ctx.Domain = string(cacie.Domain)
		}
		ctx.CACIEConfidence = cacie.RootConfidence
		if cacie.PatternMatch != nil {
			ctx.CACIEPatternName  = cacie.PatternMatch.PatternName
			ctx.CACIEPatternBoost = cacie.PatternMatch.ConfidenceBoost
		}
		// Replace hypothesis list with CACIE's fused version when available.
		if len(cacie.Hypotheses) > 0 {
			ctx.Hypotheses = ctx.Hypotheses[:0]
			for _, h := range cacie.Hypotheses {
				ctx.Hypotheses = append(ctx.Hypotheses, RCAHypothesisSummary{
					ID:          h.ID,
					EntityID:    h.EntityID,
					EntityLabel: h.EntityLabel,
					EntityType:  h.EntityType,
					Domain:      string(h.Domain),
					Confidence:  h.Confidence,
				})
			}
		}
	}

	return ctx
}

// ToJSON serialises the context for HTTP transport.
func (r *RCAInvestigationContext) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}
