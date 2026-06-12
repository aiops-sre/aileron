package correlation

// explainability.go
//
// GenerateExplainabilityReport builds a structured, operator-readable explanation
// for every correlation decision. Stored as explanation_json in
// pipeline_correlation_results for audit and feedback workflows.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EvidenceChainEntry describes one strategy's contribution to the final score.
type EvidenceChainEntry struct {
	Source       string  `json:"source"`
	Score        float64 `json:"score"`
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution"` // score × weight
	Description  string  `json:"description"`
}

// RootCauseExplanation describes the identified root cause entity and causal chain.
type RootCauseExplanation struct {
	EntityID        string   `json:"entity_id"`
	EntityLabel     string   `json:"entity_label"`
	EntityType      string   `json:"entity_type"`
	Confidence      float64  `json:"confidence"`
	CausalChain     []string `json:"causal_chain"`
	EvidenceSources []string `json:"evidence_sources"`
}

// ExplainabilityReport is the full operator-facing explanation for a correlation decision.
type ExplainabilityReport struct {
	AlertID       uuid.UUID             `json:"alert_id"`
	Decision      interface{}           `json:"decision"` // CorrelationDecision or string
	FinalScore    float64               `json:"final_score"`
	RootCause     *RootCauseExplanation `json:"root_cause,omitempty"`
	BlastRadius   []string              `json:"blast_radius,omitempty"`
	EvidenceChain []EvidenceChainEntry  `json:"evidence_chain"`
	Reasoning     []string              `json:"reasoning"`
	WhyGrouped    string                `json:"why_grouped,omitempty"`
	WhyRootCause  string                `json:"why_root_cause,omitempty"`
	WhyAttached   string                `json:"why_attached,omitempty"`
	DomainContext string                `json:"domain_context,omitempty"`
	GeneratedAt   time.Time             `json:"generated_at"`
}

// GenerateExplainabilityReport builds the full operator-facing explanation.
func GenerateExplainabilityReport(
	alert *Alert,
	decision *FinalCorrelationResult,
	topoResult *RecursiveTopoResult,
	hypotheses []*RCAHypothesis,
	ontologyResult *OntologyResult,
) *ExplainabilityReport {

	r := &ExplainabilityReport{
		AlertID:     alert.ID,
		Decision:    decision.Decision,
		FinalScore:  decision.FinalScore,
		GeneratedAt: time.Now(),
	}

	// Evidence chain 
	weights := decision.WeightsUsed
	for name, sr := range decision.StrategyResults {
		if sr == nil {
			continue
		}
		var weight float64
		switch name {
		case "semantic":
			weight = weights.Semantic
		case "temporal":
			weight = weights.Temporal
		case "topology":
			weight = weights.Topology
		case "rules":
			weight = weights.Rules
		}
		r.EvidenceChain = append(r.EvidenceChain, EvidenceChainEntry{
			Source:       name,
			Score:        sr.Score,
			Weight:       weight,
			Contribution: sr.Score * weight,
			Description:  buildStrategyDesc(name, sr),
		})
	}

	// Domain context 
	if ontologyResult != nil && ontologyResult.Domain != DomainUnknown {
		r.DomainContext = fmt.Sprintf("failure domain: %s | class: %s | keywords: %s",
			ontologyResult.Domain,
			ontologyResult.Class,
			strings.Join(ontologyResult.MatchedKeywords, ", "))
	}

	// Root cause 
	if topoResult != nil && topoResult.RootEntity != nil {
		chain := make([]string, len(topoResult.CausalChain))
		for i, link := range topoResult.CausalChain {
			chain[i] = fmt.Sprintf("%s -[%s]%s (weight: %.2f)",
				link.From.EntityID, link.EdgeType, link.To.EntityID, link.Weight)
		}
		r.RootCause = &RootCauseExplanation{
			EntityID:    topoResult.RootEntity.EntityID,
			EntityLabel: topoResult.RootEntity.Label,
			EntityType:  topoResult.RootEntity.EntityType,
			Confidence:  topoResult.RootConfidence,
			CausalChain: chain,
		}
		for _, n := range topoResult.BlastRadius {
			if n.PropagationScore > 0.50 {
				r.BlastRadius = append(r.BlastRadius, n.Node.Label)
			}
		}
	}

	// Human-readable sentences 
	r.Reasoning = buildReasoningParagraph(alert, decision, topoResult, ontologyResult, hypotheses)

	decStr := fmt.Sprintf("%v", decision.Decision)
	switch decStr {
	case "merge_incident":
		r.WhyGrouped = buildWhyGrouped(decision)
	case "create_incident":
		if r.RootCause != nil {
			r.WhyRootCause = buildWhyRootCause(r.RootCause, topoResult)
		}
	case "merge", "attach":
		r.WhyAttached = buildWhyAttached(decision, topoResult)
	}

	return r
}

// ToJSON serializes the report for storage in pipeline_correlation_results.explanation_json.
func (r *ExplainabilityReport) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// Internal builders 

func buildStrategyDesc(name string, sr *StrategyResult) string {
	if sr.BestMatch != nil {
		return fmt.Sprintf("matched alert '%s' (%.3f confidence, %s processing time)",
			sr.BestMatch.Title, sr.Score, sr.ProcessingTime.Round(time.Millisecond))
	}
	if sr.Error != nil {
		return fmt.Sprintf("strategy failed: %v", sr.Error)
	}
	if sr.Score == 0 {
		return "no match found"
	}
	return fmt.Sprintf("score %.3f, %d similar alerts", sr.Score, len(sr.SimilarAlerts))
}

func buildReasoningParagraph(
	alert *Alert,
	decision *FinalCorrelationResult,
	topo *RecursiveTopoResult,
	onto *OntologyResult,
	hyps []*RCAHypothesis,
) []string {
	var r []string

	if onto != nil && onto.Domain != DomainUnknown {
		r = append(r, fmt.Sprintf("Alert classified as %s failure (class: %s, confidence: %.2f)",
			onto.Domain, onto.Class, onto.Confidence))
	}

	if topo != nil && topo.RootEntity != nil {
		r = append(r, fmt.Sprintf(
			"Topology traversal identified root cause: %s (%s) at infrastructure level %s, reachable via %d-hop path with confidence %.3f",
			topo.RootEntity.Label, topo.RootEntity.EntityType, topo.RootEntity.InfraLevel,
			topo.Depth, topo.RootConfidence))
	}

	if decision.FinalScore > 0 {
		r = append(r, fmt.Sprintf("Dominant correlation signal: %s (final weighted score: %.3f)",
			decision.DominantStrategy, decision.FinalScore))
	}

	if len(hyps) > 1 {
		r = append(r, fmt.Sprintf(
			"Competing RCA hypotheses evaluated (%d candidates); MAP estimate selected with confidence %.3f",
			len(hyps), hyps[0].Confidence))
	}

	if len(r) == 0 {
		r = append(r, fmt.Sprintf("Decision: %v (score: %.3f)", decision.Decision, decision.FinalScore))
	}
	return r
}

func buildWhyGrouped(d *FinalCorrelationResult) string {
	if d.BestMatch == nil {
		return ""
	}
	return fmt.Sprintf(
		"Grouped with incident because: dominant strategy '%s' scored %.3f (threshold: 0.40), matching incident '%s' on shared infrastructure",
		d.DominantStrategy, d.FinalScore, d.BestMatch.ID)
}

func buildWhyRootCause(rc *RootCauseExplanation, topo *RecursiveTopoResult) string {
	chain := strings.Join(rc.CausalChain, " ")
	return fmt.Sprintf(
		"Identified as root cause: %s (%s) with confidence %.3f. Causal path: %s",
		rc.EntityLabel, rc.EntityType, rc.Confidence, chain)
}

func buildWhyAttached(d *FinalCorrelationResult, topo *RecursiveTopoResult) string {
	if topo != nil && topo.RootEntity != nil {
		return fmt.Sprintf(
			"Attached to existing incident: topology graph confirmed this alert is a downstream effect of root entity %s (propagation score: %.3f)",
			topo.RootEntity.Label, topo.RootConfidence)
	}
	return fmt.Sprintf(
		"Attached to existing incident: %s strategy confirmed correlation (score: %.3f)",
		d.DominantStrategy, d.FinalScore)
}
