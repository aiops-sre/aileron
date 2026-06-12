// Package fingerprint implements incident fingerprinting and historical matching.
// New incidents are converted to a normalized signal fingerprint and matched
// against the historical database using Jaccard similarity. When a match is
// found, the historical resolution is surfaced to the operator.
//
// Market gap: PagerDuty has ML-based similar incident grouping but it's opaque
// and not Kubernetes-signal-aware. KubeSense fingerprints on K8s-specific signal
// types (failure mode + resource kind + signal source) with full transparency.
package fingerprint

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// SignalDescriptor is a normalized, comparable description of one incident signal.
// Format: "<source>/<resource_kind>/<signal_type>" e.g. "metric/Pod/cpu_throttle_high"
type SignalDescriptor string

// Fingerprint is the complete signal signature of an incident.
type Fingerprint struct {
	IncidentID  string
	ClusterID   string
	Signals     []SignalDescriptor
	set         map[SignalDescriptor]bool // fast set membership
	FailureMode string
	RootKind    string // entity kind of the root cause
	StoredAt    time.Time
	Resolution  *Resolution
}

// Resolution describes how a past incident was resolved.
type Resolution struct {
	Actions     []ResolvedAction `json:"actions"`
	ResolvedIn  time.Duration    `json:"resolved_in"`
	Notes       string           `json:"notes"`
	SuccessRate float64          `json:"success_rate"` // across similar incidents
}

// ResolvedAction is one step in a resolved incident's remediation.
type ResolvedAction struct {
	Type        string  `json:"type"`
	Command     string  `json:"command"`
	Description string  `json:"description"`
	WasEffective bool   `json:"was_effective"`
	Confidence  float64 `json:"confidence"`
}

// Match is a result from the fingerprint search.
type Match struct {
	HistoricalID string       `json:"historical_id"`
	Similarity   float64      `json:"similarity"` // Jaccard index 0-1
	FailureMode  string       `json:"failure_mode"`
	RootKind     string       `json:"root_kind"`
	OccurredAt   time.Time    `json:"occurred_at"`
	Resolution   *Resolution  `json:"resolution,omitempty"`
	Explanation  string       `json:"explanation"`
}

// IncidentInput is the current incident to be matched.
type IncidentInput struct {
	IncidentID   string
	ClusterID    string
	Signals      []RawSignal
	FailureMode  string
	RootKind     string
	DetectedAt   time.Time
}

// RawSignal is one signal from the current incident (pre-normalization).
type RawSignal struct {
	Source       string // "metric" | "log" | "k8s_event" | "change" | "topology"
	ResourceKind string
	SignalType   string // e.g. "cpu_throttle_high", "OOMKilling", "deployment_rollout"
	Severity     string
}

// Engine stores historical incident fingerprints and scores new incidents
// against them to find the closest historical match and its resolution.
type Engine struct {
	mu      sync.RWMutex
	history []*Fingerprint
}

// NewEngine creates an empty fingerprint engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Store adds a resolved incident fingerprint to the historical database.
// Call this when an incident is closed with a known resolution.
func (e *Engine) Store(fp *Fingerprint) {
	fp.set = buildSet(fp.Signals)
	if fp.StoredAt.IsZero() {
		fp.StoredAt = time.Now()
	}
	e.mu.Lock()
	e.history = append(e.history, fp)
	e.mu.Unlock()
}

// Match finds the closest historical incidents to the given input.
// Returns up to topK matches ordered by similarity descending.
// Only returns matches with Jaccard similarity >= minSimilarity (0-1).
func (e *Engine) Match(input IncidentInput, topK int, minSimilarity float64) []Match {
	if topK <= 0 {
		topK = 5
	}
	if minSimilarity <= 0 {
		minSimilarity = 0.40
	}

	// Normalize the incoming incident signals
	newFP := buildFingerprint(input)

	e.mu.RLock()
	defer e.mu.RUnlock()

	var matches []Match
	for _, hist := range e.history {
		if hist.IncidentID == input.IncidentID {
			continue // don't match against self
		}
		sim := jaccardSimilarity(newFP.set, hist.set)
		if sim < minSimilarity {
			continue
		}
		matches = append(matches, Match{
			HistoricalID: hist.IncidentID,
			Similarity:   sim,
			FailureMode:  hist.FailureMode,
			RootKind:     hist.RootKind,
			OccurredAt:   hist.StoredAt,
			Resolution:   hist.Resolution,
			Explanation:  buildMatchExplanation(newFP, hist, sim),
		})
	}

	// Sort by similarity descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})

	if len(matches) > topK {
		matches = matches[:topK]
	}
	return matches
}

// BuildAndStore is a convenience that builds a fingerprint from an IncidentInput
// and stores it. Use this when closing a resolved incident.
func (e *Engine) BuildAndStore(input IncidentInput, resolution *Resolution) *Fingerprint {
	fp := buildFingerprint(input)
	fp.Resolution = resolution
	e.Store(fp)
	return fp
}

// HistorySize returns the number of historical fingerprints stored.
func (e *Engine) HistorySize() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.history)
}

// ─── Core algorithm ────────────────────────────────────────────────────────────

// buildFingerprint normalizes raw signals into a comparable fingerprint.
// Normalization removes cluster/namespace specifics so incidents from different
// clusters/namespaces can still match on structural similarity.
func buildFingerprint(input IncidentInput) *Fingerprint {
	var descs []SignalDescriptor
	for _, s := range input.Signals {
		desc := normalizeSignal(s)
		descs = append(descs, desc)
	}

	// Deduplicate
	seen := map[SignalDescriptor]bool{}
	var unique []SignalDescriptor
	for _, d := range descs {
		if !seen[d] {
			seen[d] = true
			unique = append(unique, d)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })

	return &Fingerprint{
		IncidentID:  input.IncidentID,
		ClusterID:   input.ClusterID,
		Signals:     unique,
		set:         seen,
		FailureMode: input.FailureMode,
		RootKind:    input.RootKind,
		StoredAt:    input.DetectedAt,
	}
}

// normalizeSignal converts a raw signal to a canonical descriptor string.
// Removes namespace/name specifics so structural patterns emerge.
func normalizeSignal(s RawSignal) SignalDescriptor {
	source := strings.ToLower(s.Source)
	kind := s.ResourceKind
	if kind == "" {
		kind = "unknown"
	}
	sigType := normalizeSignalType(s.SignalType)
	return SignalDescriptor(fmt.Sprintf("%s/%s/%s", source, kind, sigType))
}

// normalizeSignalType collapses variations of the same signal into one canonical form.
func normalizeSignalType(t string) string {
	t = strings.ToLower(t)
	// Group OOM variants
	if strings.Contains(t, "oom") || strings.Contains(t, "memory") {
		return "memory_pressure"
	}
	// Group CPU variants
	if strings.Contains(t, "cpu") || strings.Contains(t, "throttl") {
		return "cpu_pressure"
	}
	// Group crash variants
	if strings.Contains(t, "crash") || strings.Contains(t, "backoff") ||
		strings.Contains(t, "restart") {
		return "crash_loop"
	}
	// Group node variants
	if strings.Contains(t, "notready") || strings.Contains(t, "not_ready") {
		return "node_not_ready"
	}
	// Group deploy variants
	if strings.Contains(t, "rollout") || strings.Contains(t, "deploy") {
		return "deployment_change"
	}
	// Group network variants
	if strings.Contains(t, "network") || strings.Contains(t, "connection") ||
		strings.Contains(t, "timeout") {
		return "network_issue"
	}
	// Group storage variants
	if strings.Contains(t, "pvc") || strings.Contains(t, "volume") ||
		strings.Contains(t, "storage") {
		return "storage_issue"
	}
	return t
}

// buildSet converts a signal list to a set for O(1) lookup.
func buildSet(signals []SignalDescriptor) map[SignalDescriptor]bool {
	s := make(map[SignalDescriptor]bool, len(signals))
	for _, sig := range signals {
		s[sig] = true
	}
	return s
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two signal sets.
// Range: 0 (no overlap) to 1 (identical sets).
func jaccardSimilarity(a, b map[SignalDescriptor]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	intersection := 0
	for sig := range a {
		if b[sig] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// buildMatchExplanation generates a human-readable explanation of the match.
func buildMatchExplanation(newFP *Fingerprint, hist *Fingerprint, similarity float64) string {
	// Find the shared signals
	var shared []string
	for _, sig := range newFP.Signals {
		if hist.set[sig] {
			// Make it readable: "metric/Pod/memory_pressure" → "Pod memory pressure"
			parts := strings.SplitN(string(sig), "/", 3)
			if len(parts) == 3 {
				shared = append(shared, parts[1]+" "+strings.ReplaceAll(parts[2], "_", " "))
			}
		}
	}

	if len(shared) == 0 {
		return fmt.Sprintf("%.0f%% structural similarity to incident %s",
			similarity*100, hist.IncidentID)
	}

	sharedStr := strings.Join(shared[:min(3, len(shared))], ", ")
	return fmt.Sprintf("%.0f%% match to %s — same signals: %s",
		similarity*100, hist.IncidentID, sharedStr)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
