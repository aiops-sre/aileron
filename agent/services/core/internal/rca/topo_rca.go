// Package rca — Topology-Anchored Root Cause Scoring (Davis-style BFS).
//
// TopoRCAEngine traverses the Kubernetes resource hierarchy using static depth
// weights to distinguish root causes from symptoms. The algorithm is inspired by
// the Davis causal-ordering model: infrastructure-layer failures (Node, PV) score
// higher because they propagate downward to cause pod/container symptoms.
package rca

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// IncidentMember is one entity participating in an incident group.
type IncidentMember struct {
	EntityID   string
	EntityKind string
	Namespace  string
	Name       string
	NodeName   string // node the pod runs on (may be empty for non-pod resources)
	EventTime  time.Time
}

// ─── TopoIncidentGroup ───────────────────────────────────────────────────────

// TopoIncidentGroup is a correlated set of events that belong to the same
// operational incident. Groups are produced by the correlation engine and
// passed to the RCA algorithms for root-cause disambiguation.
type TopoIncidentGroup struct {
	GroupID           string
	ClusterID         string
	Members           []IncidentMember
	EarliestEventTime time.Time
	CorrelationMethod string // set by enrichment; e.g. "change_correlation"
	ChangeInfo        *ChangeCandidate
}

// ─── TopoRCAEngine ────────────────────────────────────────────────────────────

// TopoRCAEngine scores candidate root causes using the K8s resource hierarchy.
// It does not query Neo4j at runtime — depth is a static, deterministic function
// of resource kind — so scoring is O(members) with zero I/O.
//
// neo4j accepts any value that satisfies Neo4jQuerier (e.g. *topology.Querier
// from the topology package) and is retained for future topology-enrichment
// extensions. Pass nil to operate in static-scoring-only mode.
type TopoRCAEngine struct {
	neo4j Neo4jQuerier
	db    *sql.DB
}

// NewTopoRCAEngine creates a topology-anchored RCA engine.
// neo4j may be nil; the current scoring algorithm uses only static kind
// metadata and does not issue live graph queries.
func NewTopoRCAEngine(neo4j Neo4jQuerier, db *sql.DB) *TopoRCAEngine {
	return &TopoRCAEngine{neo4j: neo4j, db: db}
}

// ─── CausalScore ──────────────────────────────────────────────────────────────

// CausalScore is the output scoring for a single entity candidate.
type CausalScore struct {
	EntityID    string
	EntityKind  string
	EntityNS    string
	EntityName  string
	Score       float64
	IsCause     bool
	Explanation string
}

// ─── ScoreRootCause ───────────────────────────────────────────────────────────

// ScoreRootCause scores each incident group and returns one CausalScore per
// group, identifying the member with the highest causal score.
//
// Scoring formula per member:
//
//	causalScore = depthScore × timeEarliestScore × infraBoost
//
// where:
//
//	depthScore         = 1 / (1 + hops)   — hops derived from entityDepth()
//	timeEarliestScore  = 1.0 if this member's EventTime is the group's
//	                     earliest, 0.3 otherwise
//	infraBoost         = per-kind multiplier (Node ×1.5, PVC ×1.3, …)
func (e *TopoRCAEngine) ScoreRootCause(
	ctx context.Context,
	clusterID string,
	incidents []TopoIncidentGroup,
	firedAt time.Time,
) []CausalScore {
	_ = ctx       // reserved for future async topology enrichment
	_ = clusterID // available for cluster-scoped filtering
	_ = firedAt   // available for time-relative scoring extensions

	results := make([]CausalScore, 0, len(incidents))

	for _, grp := range incidents {
		// Determine the earliest event time in the group.
		earliest := findEarliestTime(grp.Members)

		// Score every member and pick the winner.
		var best CausalScore
		bestScore := -1.0

		for _, m := range grp.Members {
			hops := entityDepth(m.EntityKind)
			depthScore := 1.0 / (1.0 + float64(hops))

			timeEarliestScore := 0.3
			if !m.EventTime.IsZero() && !earliest.IsZero() && m.EventTime.Equal(earliest) {
				timeEarliestScore = 1.0
			}

			boost := infraBoost(m.EntityKind)
			score := depthScore * timeEarliestScore * boost

			if score > bestScore {
				bestScore = score
				best = CausalScore{
					EntityID:   m.EntityID,
					EntityKind: m.EntityKind,
					EntityNS:   m.Namespace,
					EntityName: m.Name,
					Score:      score,
					IsCause:    true,
					Explanation: fmt.Sprintf(
						"kind=%s depth=%d depthScore=%.3f timing=%s infraBoost=%.1f → score=%.4f",
						m.EntityKind, hops, depthScore,
						timingLabel(timeEarliestScore),
						boost, score,
					),
				}
			}
		}

		if bestScore >= 0 {
			results = append(results, best)
		}
	}

	return results
}

// ─── entityDepth ─────────────────────────────────────────────────────────────

// entityDepth returns the static K8s hierarchy depth for a resource kind.
// Lower depth = closer to the infrastructure root = higher depthScore.
//
//	Depth 0 → cluster-infrastructure: Node, PersistentVolume
//	Depth 1 → cluster-workload owners: ConfigMap, Secret, Deployment, StatefulSet, DaemonSet
//	Depth 2 → workload replicas: ReplicaSet
//	Depth 3 → running instances: Pod
//	Depth 4 → sub-pod: Container
func entityDepth(kind string) int {
	switch kind {
	case "Node", "PersistentVolume":
		return 0
	case "ConfigMap", "Secret", "Deployment", "StatefulSet", "DaemonSet":
		return 1
	case "ReplicaSet":
		return 2
	case "Pod":
		return 3
	case "Container":
		return 4
	default:
		return 3 // treat unknown kinds like pods (leaf-level)
	}
}

// infraBoost returns the infrastructure-layer prior multiplier for a kind.
// Node failures cascade to all pods on that node; PVC failures cascade to
// all pods that mount them — so they get higher multipliers.
func infraBoost(kind string) float64 {
	switch kind {
	case "Node":
		return 1.5
	case "PersistentVolumeClaim":
		return 1.3
	case "Deployment":
		return 1.2
	default:
		return 1.0
	}
}

// ─── SymptomFilter ────────────────────────────────────────────────────────────

// SymptomFilter partitions incident groups into root causes and symptoms.
//
// Heuristic: if >80% of a group's members are pods sharing the same node,
// AND there exists another group whose primary resource is that node, then
// the pod group is classified as a symptom and the node group is the root cause.
//
// Groups that do not match this pattern are conservatively classified as
// potential root causes and returned in rootCauses.
func (e *TopoRCAEngine) SymptomFilter(
	groups []TopoIncidentGroup,
) (rootCauses []TopoIncidentGroup, symptoms []TopoIncidentGroup) {
	// Build a set of nodes that are the primary subject of their own group.
	nodeGroups := nodeGroupSet(groups)

	for _, grp := range groups {
		if isSymptomGroup(grp, nodeGroups) {
			symptoms = append(symptoms, grp)
		} else {
			rootCauses = append(rootCauses, grp)
		}
	}
	return rootCauses, symptoms
}

// nodeGroupSet returns a set of node names that have their own incident group
// (i.e., a group whose majority members are of kind "Node").
func nodeGroupSet(groups []TopoIncidentGroup) map[string]bool {
	nodes := make(map[string]bool)
	for _, grp := range groups {
		nodeCount := 0
		for _, m := range grp.Members {
			if m.EntityKind == "Node" {
				nodeCount++
			}
		}
		// >80% node members → this group represents a node event.
		if len(grp.Members) > 0 && nodeCount*10 >= len(grp.Members)*8 {
			for _, m := range grp.Members {
				if m.EntityKind == "Node" {
					nodes[m.Name] = true
				}
			}
		}
	}
	return nodes
}

// isSymptomGroup returns true when >80% of a group's members are pods that
// all share the same node AND that node appears as a root-cause group.
func isSymptomGroup(grp TopoIncidentGroup, nodeGroups map[string]bool) bool {
	if len(grp.Members) == 0 {
		return false
	}

	// Count pod members and tally which nodes they run on.
	podCount := 0
	nodeTally := make(map[string]int)
	for _, m := range grp.Members {
		if m.EntityKind == "Pod" {
			podCount++
			if m.NodeName != "" {
				nodeTally[m.NodeName]++
			}
		}
	}

	// Check the >80% pod threshold.
	if float64(podCount)/float64(len(grp.Members)) <= 0.8 {
		return false
	}

	// Check whether the dominant node is covered by its own node group.
	for nodeName, count := range nodeTally {
		if float64(count)/float64(podCount) > 0.8 && nodeGroups[nodeName] {
			return true
		}
	}
	return false
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// findEarliestTime returns the earliest non-zero EventTime among the members.
func findEarliestTime(members []IncidentMember) time.Time {
	var earliest time.Time
	for _, m := range members {
		if m.EventTime.IsZero() {
			continue
		}
		if earliest.IsZero() || m.EventTime.Before(earliest) {
			earliest = m.EventTime
		}
	}
	return earliest
}

// timingLabel converts a timeEarliestScore value to a descriptive label.
func timingLabel(score float64) string {
	if score == 1.0 {
		return "earliest-in-group"
	}
	return "not-earliest"
}
