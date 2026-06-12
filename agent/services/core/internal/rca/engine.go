// Package rca implements the evidence-first root cause analysis engine.
package rca

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Engine performs evidence-first RCA by traversing the topology graph,
// gathering evidence, building hypotheses, and ranking them by confidence.
type Engine struct {
	neo4j   Neo4jQuerier
	changes ChangeQuerier
	events  EventQuerier
}

// NewEngine creates an RCA engine.
func NewEngine(neo4j Neo4jQuerier, changes ChangeQuerier, events EventQuerier) *Engine {
	return &Engine{neo4j: neo4j, changes: changes, events: events}
}

// Request is the input to an investigation.
type Request struct {
	ClusterID         string
	AffectedResources []ResourceRef
	IncidentTime      time.Time
	AlertContext      map[string]string
}

// Result is the output of an investigation.
type Result struct {
	RootCause          *Hypothesis
	AllHypotheses      []*Hypothesis
	RejectedHypotheses []*Hypothesis
	Evidence           []Evidence
	DependencyChain    []DependencyNode
	RecentChanges      []ChangeRecord
	Confidence         float64
	Duration           time.Duration
}

// Hypothesis is a candidate root cause.
type Hypothesis struct {
	EntityID      string
	EntityKind    string
	EntityName    string
	EntityNS      string
	Confidence    float64
	FailureMode   string
	SupportingEvidence []Evidence
	RefutingEvidence   []Evidence
	RejectedReason     string
}

// Evidence is a single piece of information supporting a hypothesis.
type Evidence struct {
	Type        string // "k8s_event" | "metric" | "change" | "topology"
	Source      string
	Description string
	Strength    float64 // 0-1
	OccurredAt  time.Time
	ResourceRef ResourceRef
}

// ChangeRecord describes a recent cluster change.
type ChangeRecord struct {
	ChangeType            string
	ResourceKind          string
	Namespace             string
	ResourceName          string
	Actor                 string
	OccurredAt            time.Time
	SecondsBeforeIncident int64
	CorrelationScore      float64
}

// DependencyNode is one node in the causal chain.
type DependencyNode struct {
	EntityID   string
	EntityKind string
	Name       string
	Namespace  string
	Depth      int
}

// ResourceRef identifies a Kubernetes resource.
type ResourceRef struct {
	Kind      string
	Namespace string
	Name      string
	UID       string
}

// Interfaces for external dependencies — allow testing without real Neo4j/DB.
type Neo4jQuerier interface {
	GetUpstreamChain(ctx context.Context, clusterID, kind, namespace, name string, maxDepth int) ([]DependencyNode, error)
	GetK8sEventsFor(ctx context.Context, clusterID, kind, namespace, name string, since time.Time) ([]Evidence, error)
}

type ChangeQuerier interface {
	GetRecent(ctx context.Context, clusterID string, from, until time.Time) ([]ChangeRecord, error)
}

type EventQuerier interface {
	GetK8sEvents(ctx context.Context, clusterID, namespace string, since time.Time) ([]Evidence, error)
}

// Investigate runs the full RCA workflow.
func (e *Engine) Investigate(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()
	result := &Result{}

	// Step 1: Build dependency chain (upstream traversal from affected resources)
	var chain []DependencyNode
	for _, r := range req.AffectedResources {
		nodes, err := e.neo4j.GetUpstreamChain(ctx, req.ClusterID, r.Kind, r.Namespace, r.Name, 8)
		if err != nil {
			continue
		}
		chain = append(chain, nodes...)
	}
	// Deduplicate chain nodes
	seen := map[string]bool{}
	var dedupedChain []DependencyNode
	for _, n := range chain {
		key := n.EntityKind + "/" + n.Namespace + "/" + n.Name
		if !seen[key] {
			seen[key] = true
			dedupedChain = append(dedupedChain, n)
		}
	}
	result.DependencyChain = dedupedChain

	// Step 2: Gather K8s events for each node in the chain
	since := req.IncidentTime.Add(-30 * time.Minute)
	var allEvidence []Evidence
	for _, node := range dedupedChain {
		evs, err := e.neo4j.GetK8sEventsFor(ctx, req.ClusterID, node.EntityKind, node.Namespace, node.Name, since)
		if err != nil {
			continue
		}
		allEvidence = append(allEvidence, evs...)
	}
	result.Evidence = allEvidence

	// Step 3: Get recent changes (6 hour lookback)
	lookback := req.IncidentTime.Add(-6 * time.Hour)
	changes, err := e.changes.GetRecent(ctx, req.ClusterID, lookback, req.IncidentTime)
	if err == nil {
		for i := range changes {
			secs := int64(req.IncidentTime.Sub(changes[i].OccurredAt).Seconds())
			changes[i].SecondsBeforeIncident = secs
		}
		result.RecentChanges = changes
	}

	// Step 4: Build hypotheses — one per node in the dependency chain
	hypotheses := e.buildHypotheses(dedupedChain, allEvidence, changes, req.IncidentTime)

	// Step 5: Score hypotheses
	e.scoreHypotheses(hypotheses, allEvidence, changes, req.IncidentTime)

	// Step 6: Filter and rank
	confirmed, rejected := e.filter(hypotheses)
	sort.Slice(confirmed, func(i, j int) bool {
		return confirmed[i].Confidence > confirmed[j].Confidence
	})

	result.AllHypotheses = confirmed
	result.RejectedHypotheses = rejected
	if len(confirmed) > 0 {
		result.RootCause = confirmed[0]
		result.Confidence = confirmed[0].Confidence
	}
	result.Duration = time.Since(start)
	return result, nil
}

func (e *Engine) buildHypotheses(
	chain []DependencyNode,
	evidence []Evidence,
	changes []ChangeRecord,
	incidentTime time.Time,
) []*Hypothesis {
	var hyps []*Hypothesis

	for _, node := range chain {
		h := &Hypothesis{
			EntityID:   fmt.Sprintf("%s/%s/%s", node.EntityKind, node.Namespace, node.Name),
			EntityKind: node.EntityKind,
			EntityName: node.Name,
			EntityNS:   node.Namespace,
		}
		// Attach evidence relevant to this node
		nodeKey := node.EntityKind + "/" + node.Namespace + "/" + node.Name
		for _, ev := range evidence {
			evKey := ev.ResourceRef.Kind + "/" + ev.ResourceRef.Namespace + "/" + ev.ResourceRef.Name
			if evKey == nodeKey {
				h.SupportingEvidence = append(h.SupportingEvidence, ev)
			}
		}
		hyps = append(hyps, h)
	}
	return hyps
}

func (e *Engine) scoreHypotheses(
	hyps []*Hypothesis,
	evidence []Evidence,
	changes []ChangeRecord,
	incidentTime time.Time,
) {
	for _, h := range hyps {
		score := 0.0

		// Score: K8s events directly on this resource
		for _, ev := range h.SupportingEvidence {
			if ev.Type == "k8s_event" {
				score += 0.30 * ev.Strength
			}
		}

		// Score: recent change to this exact resource (strongest signal)
		for _, ch := range changes {
			if ch.ResourceKind == h.EntityKind && ch.ResourceName == h.EntityName {
				lag := ch.SecondsBeforeIncident
				if lag > 0 && lag <= 3600 {
					// Change within 1h before incident: very strong causal signal
					changeFactor := 1.0 - (float64(lag) / 3600.0)
					score += 0.45 * changeFactor
				} else if lag > 3600 && lag <= 21600 {
					// Change 1-6h before incident: moderate signal
					score += 0.15
				}
			}
		}

		// Infrastructure level prior: upstream failures cause downstream symptoms
		switch h.EntityKind {
		case "Node":
			score *= 1.50 // node failure causes all pods on it to fail
		case "PersistentVolume":
			score *= 1.35
		case "PersistentVolumeClaim":
			score *= 1.30
		case "StatefulSet":
			score *= 1.20
		case "ConfigMap", "Secret":
			score *= 1.15 // config errors are frequently root causes
		case "Deployment":
			score *= 1.10
		case "Service":
			score *= 1.05
		case "Pod":
			score *= 0.80 // pods are usually symptoms not root causes
		}

		h.Confidence = clamp(score, 0, 1)
	}
}

func (e *Engine) filter(hyps []*Hypothesis) (confirmed, rejected []*Hypothesis) {
	for _, h := range hyps {
		if h.Confidence < 0.10 {
			h.RejectedReason = fmt.Sprintf("confidence %.3f below minimum threshold 0.10", h.Confidence)
			rejected = append(rejected, h)
		} else if len(h.SupportingEvidence) == 0 {
			h.RejectedReason = "no supporting evidence found in lookback window"
			rejected = append(rejected, h)
		} else {
			confirmed = append(confirmed, h)
		}
	}
	return
}

func clamp(v, min, max float64) float64 {
	return math.Max(min, math.Min(max, v))
}
