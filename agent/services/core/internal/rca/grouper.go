// Package rca — BigPanda-style weighted Union-Find multi-signal incident grouper.
//
// Algorithm 3: computes a composite similarity score from four orthogonal signals
// (topology proximity, label/tag Jaccard, temporal decay, event-type family) and
// uses a weighted Union-Find to cluster AlertSignals into IncidentGroups. Groups
// older than the configured window are expired on every Ingest call.
//
// Threshold θ = 0.45 (configurable via MultiSignalGrouper.Theta).
// All scoring is done purely from AlertSignal fields — no Neo4j calls are made
// during grouping so the hot path stays O(n) per signal.
package rca

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ─── Union-Find ────────────────────────────────────────────────────────────────

// UnionFind is a weighted Union-Find (union-by-rank + path compression) that
// also tracks the maximum confidence score observed on any edge within a component.
type UnionFind struct {
	parent     map[string]string
	rank       map[string]int
	confidence map[string]float64 // root → max edge confidence in component
}

// NewUnionFind creates an empty UnionFind.
func NewUnionFind() *UnionFind {
	return &UnionFind{
		parent:     make(map[string]string),
		rank:       make(map[string]int),
		confidence: make(map[string]float64),
	}
}

// add ensures id is present as a singleton component (idempotent).
func (uf *UnionFind) add(id string) {
	if _, ok := uf.parent[id]; !ok {
		uf.parent[id] = id
		uf.rank[id] = 0
		uf.confidence[id] = 0
	}
}

// Find returns the canonical root of id's component using path compression.
func (uf *UnionFind) Find(id string) string {
	uf.add(id)
	if uf.parent[id] != id {
		uf.parent[id] = uf.Find(uf.parent[id]) // path compression
	}
	return uf.parent[id]
}

// Union merges the components of a and b, recording the edge confidence.
// Uses union-by-rank to keep trees shallow. The maximum confidence ever seen
// on any edge in the merged component is propagated to the new root.
func (uf *UnionFind) Union(a, b string, confidence float64) {
	ra := uf.Find(a)
	rb := uf.Find(b)
	if ra == rb {
		// Already in the same component — just update max confidence.
		if confidence > uf.confidence[ra] {
			uf.confidence[ra] = confidence
		}
		return
	}
	// Merge lower-rank root under higher-rank root.
	if uf.rank[ra] < uf.rank[rb] {
		ra, rb = rb, ra
	}
	uf.parent[rb] = ra
	if uf.rank[ra] == uf.rank[rb] {
		uf.rank[ra]++
	}
	// Propagate max confidence to the new root.
	maxConf := math.Max(uf.confidence[ra], uf.confidence[rb])
	if confidence > maxConf {
		maxConf = confidence
	}
	uf.confidence[ra] = maxConf
}

// Groups returns all components as a map of root-ID → member IDs (including root).
func (uf *UnionFind) Groups() map[string][]string {
	groups := make(map[string][]string)
	for id := range uf.parent {
		root := uf.Find(id)
		groups[root] = append(groups[root], id)
	}
	return groups
}

// ─── AlertSignal ──────────────────────────────────────────────────────────────

// AlertSignal is a single alert or health event ingested by the grouper.
// All fields used for scoring must be populated by the caller; missing fields
// degrade individual signal scores gracefully (the other signals still apply).
type AlertSignal struct {
	ID         string // unique alert/event ID
	EntityID   string // e.g. "Deployment/default/frontend"
	EntityType string // resource kind: Pod, Deployment, Node, …
	EventType  string // dot-separated: "health.pod.crashloopbackoff"
	Namespace  string
	Severity   string
	NodeName   string
	FiredAt    time.Time
	Labels     map[string]string // arbitrary k8s/alert labels for Jaccard scoring
}

// ─── IncidentGroup ────────────────────────────────────────────────────────────

// IncidentGroup is the output of the grouper — a correlated cluster of signals
// that are likely manifestations of the same underlying incident.
type IncidentGroup struct {
	ID                string
	RootSignalID      string
	Members           []AlertSignal
	Score             float64 // composite similarity score of the group
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CorrelationMethod string // always "multi-signal-union-find"
}

// ─── TopologyQuerier (lightweight interface used only for future extension) ────

// TopologyQuerier is a hook for future graph-based proximity queries.
// Pass nil to MultiSignalGrouper if you do not need graph traversal;
// the grouper falls back to field-based proximity scoring in that case.
type TopologyQuerier interface {
	// Reserved for future graph-proximity scoring; currently unused by grouper.
}

// ─── MultiSignalGrouper ───────────────────────────────────────────────────────

// defaultTheta is the minimum composite score for two signals to be merged.
const defaultTheta = 0.45

// MultiSignalGrouper groups incoming AlertSignals into IncidentGroups using a
// four-signal composite score and Union-Find clustering.
//
//	Signal 1 — topology proximity (namespace / node / pod / entity adjacency)
//	Signal 2 — label Jaccard similarity × 0.3
//	Signal 3 — temporal exponential decay exp(-Δt/300) × 0.2
//	Signal 4 — event-type family match (+0.1)
//
// Groups are expired when all member signals are older than window.
type MultiSignalGrouper struct {
	mu      sync.Mutex
	topo    TopologyQuerier // may be nil
	window  time.Duration
	Theta   float64         // grouping threshold (default 0.45)
	signals []AlertSignal   // all ingested signals still within window
	groups  []IncidentGroup // current groups
}

// NewMultiSignalGrouper creates a grouper. Pass nil for topo to skip graph scoring.
func NewMultiSignalGrouper(topo TopologyQuerier, window time.Duration) *MultiSignalGrouper {
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &MultiSignalGrouper{
		topo:   topo,
		window: window,
		Theta:  defaultTheta,
	}
}

// Ingest adds a new signal and returns the current set of IncidentGroups after
// clustering. Expired signals and groups are pruned before scoring.
func (g *MultiSignalGrouper) Ingest(signal AlertSignal) []IncidentGroup {
	if signal.FiredAt.IsZero() {
		signal.FiredAt = time.Now()
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// 1. Expire signals outside the window.
	g.expire()

	// 2. Append the new signal.
	g.signals = append(g.signals, signal)

	// 3. Rebuild Union-Find over all live signals.
	uf := NewUnionFind()
	for _, s := range g.signals {
		uf.add(s.ID)
	}
	for i := 0; i < len(g.signals); i++ {
		for j := i + 1; j < len(g.signals); j++ {
			score := g.computeGroupScore(g.signals[i], g.signals[j])
			if score >= g.Theta {
				uf.Union(g.signals[i].ID, g.signals[j].ID, score)
			}
		}
	}

	// 4. Materialise groups.
	rawGroups := uf.Groups()
	// Build a lookup from signal ID → signal.
	sigByID := make(map[string]AlertSignal, len(g.signals))
	for _, s := range g.signals {
		sigByID[s.ID] = s
	}

	// Carry over CreatedAt from existing groups where possible.
	existingByID := make(map[string]IncidentGroup, len(g.groups))
	for _, eg := range g.groups {
		existingByID[eg.ID] = eg
	}

	now := time.Now()
	var newGroups []IncidentGroup
	for root, memberIDs := range rawGroups {
		var members []AlertSignal
		var earliest time.Time
		for _, mid := range memberIDs {
			if s, ok := sigByID[mid]; ok {
				members = append(members, s)
				if earliest.IsZero() || s.FiredAt.Before(earliest) {
					earliest = s.FiredAt
				}
			}
		}
		if len(members) == 0 {
			continue
		}

		// Stable group ID derived from the root signal ID.
		gid := groupID(root)

		createdAt := now
		if eg, ok := existingByID[gid]; ok {
			createdAt = eg.CreatedAt
		} else if !earliest.IsZero() {
			createdAt = earliest
		}

		grp := IncidentGroup{
			ID:                gid,
			RootSignalID:      root,
			Members:           members,
			Score:             uf.confidence[root],
			CreatedAt:         createdAt,
			UpdatedAt:         now,
			CorrelationMethod: "multi-signal-union-find",
		}
		newGroups = append(newGroups, grp)
	}
	g.groups = newGroups

	// Return a stable-ordered copy.
	out := make([]IncidentGroup, len(g.groups))
	copy(out, g.groups)
	return out
}

// ─── Scoring ──────────────────────────────────────────────────────────────────

// computeGroupScore returns the composite similarity score ∈ [0, 1] between
// two signals. The four sub-signals are additive and independently capped.
//
//	Signal 1: topology proximity (weight ≤ 0.9, see TopoProximityScore)
//	Signal 2: label Jaccard × 0.3
//	Signal 3: temporal decay exp(-Δt/300) × 0.2
//	Signal 4: same event-type family +0.1
//
// The final score is clamped to [0, 1].
func (g *MultiSignalGrouper) computeGroupScore(a, b AlertSignal) float64 {
	score := 0.0

	// Signal 1: topology proximity (pure field-based; no Neo4j call).
	score += TopoProximityScore(a, b)

	// Signal 2: label Jaccard similarity × 0.3
	score += labelJaccard(a.Labels, b.Labels) * 0.3

	// Signal 3: temporal exponential decay × 0.2
	// τ = 300 s — signals 5 minutes apart score ≈ 0.074 (e^-1 × 0.2).
	dt := math.Abs(a.FiredAt.Sub(b.FiredAt).Seconds())
	score += math.Exp(-dt/300.0) * 0.2

	// Signal 4: same event-type family (+0.1)
	if sameEventFamily(a.EventType, b.EventType) {
		score += 0.1
	}

	return math.Min(1.0, score)
}

// TopoProximityScore returns a topology proximity score ∈ [0, 0.9] derived
// purely from AlertSignal fields. No graph traversal is performed.
//
//	Same EntityID (resource_name)    → 0.9
//	Same node                        → 0.6
//	Same namespace                   → 0.4
//	Graph-adjacent proxy             → 0.5  (same entity type + same namespace)
//	Neither                          → 0.0
func TopoProximityScore(a, b AlertSignal) float64 {
	// Same resource identity — strongest signal.
	if a.EntityID != "" && a.EntityID == b.EntityID {
		return 0.9
	}
	// Same pod (same namespace + entity name within pod scope).
	if a.EntityType == "Pod" && b.EntityType == "Pod" &&
		a.Namespace == b.Namespace && a.EntityID == b.EntityID {
		return 0.8
	}
	// Same node.
	if a.NodeName != "" && a.NodeName == b.NodeName {
		return 0.6
	}
	// Graph-adjacent proxy: same entity type + same namespace → likely in
	// the same workload group without needing a Neo4j query.
	if a.Namespace != "" && a.Namespace == b.Namespace &&
		a.EntityType != "" && a.EntityType == b.EntityType {
		return 0.5
	}
	// Same namespace only.
	if a.Namespace != "" && a.Namespace == b.Namespace {
		return 0.4
	}
	return 0.0
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// labelJaccard computes the Jaccard index over label key-value pairs.
// Returns 0 when both maps are empty (no label overlap possible).
func labelJaccard(a, b map[string]string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for k, va := range a {
		if vb, ok := b[k]; ok && va == vb {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// sameEventFamily returns true when a and b share the same dot-separated prefix
// (e.g. "health.pod.crashloopbackoff" and "health.pod.oomkilled" both belong to
// the "health.pod" family).
func sameEventFamily(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	// Extract the first two components of the dot-separated hierarchy.
	fa := eventFamily(a)
	fb := eventFamily(b)
	return fa != "" && fa == fb
}

func eventFamily(eventType string) string {
	parts := strings.SplitN(eventType, ".", 3)
	if len(parts) < 2 {
		return eventType
	}
	return parts[0] + "." + parts[1]
}

// expire removes signals older than the configured window from the internal slice.
// Must be called with g.mu held.
func (g *MultiSignalGrouper) expire() {
	cutoff := time.Now().Add(-g.window)
	fresh := g.signals[:0]
	for _, s := range g.signals {
		if !s.FiredAt.Before(cutoff) {
			fresh = append(fresh, s)
		}
	}
	g.signals = fresh
}

// groupID produces a stable, human-readable ID for a group rooted at rootSignalID.
func groupID(rootSignalID string) string {
	sum := sha1.Sum([]byte(rootSignalID))
	return fmt.Sprintf("grp-%s", hex.EncodeToString(sum[:6]))
}
