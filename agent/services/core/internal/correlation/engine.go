// Package correlation implements cross-signal correlation intelligence.
// Combines metrics, logs, traces, events, and topology into a unified
// incident context. This is the intelligence layer that makes KubeSense
// understand WHAT is happening, WHERE, and WHY — simultaneously.
package correlation

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// SignalSource classifies where evidence came from.
type SignalSource string

const (
	SourceMetric   SignalSource = "metric"
	SourceLog      SignalSource = "log"
	SourceTrace    SignalSource = "trace"
	SourceEvent    SignalSource = "k8s_event"
	SourceTopology SignalSource = "topology"
	SourceChange   SignalSource = "change"
	SourceSLO      SignalSource = "slo"
	SourceSecurity SignalSource = "security"
)

// Signal is one piece of observed evidence.
type Signal struct {
	Source       SignalSource `json:"source"`
	ClusterID    string       `json:"cluster_id"`
	Namespace    string       `json:"namespace"`
	ResourceKind string       `json:"resource_kind"`
	ResourceName string       `json:"resource_name"`
	Timestamp    time.Time    `json:"timestamp"`
	Type         string       `json:"type"`        // e.g. "error_rate_spike", "node_not_ready"
	Severity     string       `json:"severity"`    // critical | high | medium | low
	Value        float64      `json:"value,omitempty"`
	Description  string       `json:"description"`
	Strength     float64      `json:"strength"`    // 0-1: how strong this evidence is
}

// IncidentContext is the unified view of an active incident,
// combining all signals from all intelligence layers.
type IncidentContext struct {
	IncidentID   string    `json:"incident_id"`  // AlertHub incident ID
	ClusterID    string    `json:"cluster_id"`
	DetectedAt   time.Time `json:"detected_at"`
	Title        string    `json:"title"`

	// All signals contributing to this incident
	Signals []Signal `json:"signals"`

	// Signal breakdown by source
	MetricSignals   []Signal `json:"metric_signals,omitempty"`
	LogSignals      []Signal `json:"log_signals,omitempty"`
	TraceSignals    []Signal `json:"trace_signals,omitempty"`
	EventSignals    []Signal `json:"event_signals,omitempty"`
	ChangeSignals   []Signal `json:"change_signals,omitempty"`
	TopologyChain   []string `json:"topology_chain,omitempty"`

	// Intelligence layers
	PerformanceRegression *PerformanceRegressionSummary `json:"performance_regression,omitempty"`
	AnomaliesDetected     []AnomalySummary              `json:"anomalies_detected,omitempty"`
	SLOImpact             *SLOImpactSummary             `json:"slo_impact,omitempty"`
	SecurityFindings      []SecuritySummary             `json:"security_findings,omitempty"`
	CostImpact            *CostImpactSummary            `json:"cost_impact,omitempty"`

	// Root cause (from RCA engine)
	RootCause *RootCauseSummary `json:"root_cause,omitempty"`

	// Recommended actions (evidence-based, not LLM)
	RecommendedActions []Action `json:"recommended_actions"`

	// Evidence completeness
	SignalCount       int     `json:"signal_count"`
	EvidenceGrade     string  `json:"evidence_grade"` // A/B/C/D/F
	Confidence        float64 `json:"confidence"`
}

type PerformanceRegressionSummary struct {
	Service       string  `json:"service"`
	Signal        string  `json:"signal"`
	ChangePercent float64 `json:"change_percent"`
	Severity      string  `json:"severity"`
}

type AnomalySummary struct {
	Resource  string  `json:"resource"`
	Signal    string  `json:"signal"`
	ZScore    float64 `json:"z_score"`
	Direction string  `json:"direction"`
}

type SLOImpactSummary struct {
	Service            string  `json:"service"`
	SLOName            string  `json:"slo_name"`
	BudgetRemainingPct float64 `json:"budget_remaining_pct"`
	BurnRate           float64 `json:"burn_rate"`
}

type SecuritySummary struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
}

type CostImpactSummary struct {
	AffectedWorkloads int     `json:"affected_workloads"`
	EstimatedWasteUSD float64 `json:"estimated_waste_usd"`
}

type RootCauseSummary struct {
	EntityLabel    string  `json:"entity_label"`
	EntityKind     string  `json:"entity_kind"`
	Confidence     float64 `json:"confidence"`
	ConfidenceBand string  `json:"confidence_band"`
}

// Action is a recommended remediation step.
type Action struct {
	Priority    int    `json:"priority"`  // 1 = highest
	Type        string `json:"type"`      // restart | scale | rollback | investigate | alert
	Target      string `json:"target"`    // resource name
	Description string `json:"description"`
	Command     string `json:"command,omitempty"` // kubectl command
	Confidence  float64 `json:"confidence"`
	Rationale   string `json:"rationale"`
}

// Correlator fuses signals from all intelligence layers into IncidentContext.
type Correlator struct{}

// NewCorrelator creates a correlator.
func NewCorrelator() *Correlator { return &Correlator{} }

// Correlate builds an IncidentContext from all available signals for an incident.
// This is the single entry point that merges all intelligence layers.
func (c *Correlator) Correlate(
	ctx context.Context,
	incidentID, clusterID, title string,
	detectedAt time.Time,
	allSignals []Signal,
) *IncidentContext {
	ic := &IncidentContext{
		IncidentID: incidentID,
		ClusterID:  clusterID,
		Title:      title,
		DetectedAt: detectedAt,
		Signals:    allSignals,
	}

	// Group signals by source
	for _, s := range allSignals {
		switch s.Source {
		case SourceMetric:
			ic.MetricSignals = append(ic.MetricSignals, s)
		case SourceLog:
			ic.LogSignals = append(ic.LogSignals, s)
		case SourceTrace:
			ic.TraceSignals = append(ic.TraceSignals, s)
		case SourceEvent:
			ic.EventSignals = append(ic.EventSignals, s)
		case SourceChange:
			ic.ChangeSignals = append(ic.ChangeSignals, s)
		}
	}

	ic.SignalCount = len(allSignals)
	ic.EvidenceGrade = evidenceGrade(ic)
	ic.Confidence = overallConfidence(allSignals)

	// Generate recommended actions from evidence (no LLM)
	ic.RecommendedActions = generateActions(ic)

	return ic
}

// generateActions produces ordered remediation actions based on evidence.
// Every action has a rationale citing the specific evidence that motivated it.
func generateActions(ic *IncidentContext) []Action {
	var actions []Action
	priority := 1

	// Change signals: rollback if a recent deployment correlates
	for _, s := range ic.ChangeSignals {
		if s.Type == "deployment_rollout" && s.Strength > 0.6 {
			actions = append(actions, Action{
				Priority:    priority,
				Type:        "rollback",
				Target:      s.ResourceName,
				Description: fmt.Sprintf("Roll back deployment %q — recent rollout correlates with incident (score: %.2f)", s.ResourceName, s.Strength),
				Command:     fmt.Sprintf("kubectl rollout undo deployment/%s -n %s", s.ResourceName, s.Namespace),
				Confidence:  s.Strength,
				Rationale:   fmt.Sprintf("Deployment %q completed %s before incident started. %s", s.ResourceName, humanDuration(time.Since(s.Timestamp)), s.Description),
			})
			priority++
		}
	}

	// Metric signals: scale up if CPU/memory pressure
	for _, s := range ic.MetricSignals {
		if (s.Type == "cpu_throttle_high" || s.Type == "memory_pressure") && s.Strength > 0.5 {
			actions = append(actions, Action{
				Priority:    priority,
				Type:        "scale",
				Target:      s.ResourceName,
				Description: fmt.Sprintf("Scale up %q — resource pressure detected (z=%.1f)", s.ResourceName, s.Value),
				Command:     fmt.Sprintf("kubectl scale deployment/%s --replicas=+2 -n %s", s.ResourceName, s.Namespace),
				Confidence:  s.Strength,
				Rationale:   s.Description,
			})
			priority++
		}
	}

	// Event signals: node restart if NotReady
	for _, s := range ic.EventSignals {
		if s.Type == "health.node.not_ready" {
			actions = append(actions, Action{
				Priority:    priority,
				Type:        "investigate",
				Target:      s.ResourceName,
				Description: fmt.Sprintf("Investigate node %q — NotReady condition", s.ResourceName),
				Command:     fmt.Sprintf("kubectl describe node %s | tail -20", s.ResourceName),
				Confidence:  0.9,
				Rationale:   fmt.Sprintf("Node %q transitioned to NotReady at %s", s.ResourceName, s.Timestamp.Format(time.RFC3339)),
			})
			priority++
		}
	}

	// Sort by confidence descending within same priority
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Priority != actions[j].Priority {
			return actions[i].Priority < actions[j].Priority
		}
		return actions[i].Confidence > actions[j].Confidence
	})
	return actions
}

func evidenceGrade(ic *IncidentContext) string {
	score := 0
	if len(ic.MetricSignals) > 0 { score++ }
	if len(ic.LogSignals) > 0     { score++ }
	if len(ic.TraceSignals) > 0   { score++ }
	if len(ic.ChangeSignals) > 0  { score++ }
	if len(ic.EventSignals) > 0   { score++ }
	if ic.RootCause != nil        { score++ }
	switch score {
	case 6:     return "A"
	case 5:     return "B"
	case 3, 4:  return "C"
	case 2:     return "D"
	default:    return "F"
	}
}

func overallConfidence(signals []Signal) float64 {
	if len(signals) == 0 { return 0 }
	total := 0.0
	for _, s := range signals { total += s.Strength }
	// Diminishing returns: more signals = more confidence but capped at 0.95
	rawConf := total / float64(len(signals))
	multiplier := 1.0 - math.Exp(-float64(len(signals))/5.0) // asymptotic
	return math.Min(0.95, rawConf*multiplier)
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:   return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:     return fmt.Sprintf("%dm", int(d.Minutes()))
	default:                return fmt.Sprintf("%.1fh", d.Hours())
	}
}
