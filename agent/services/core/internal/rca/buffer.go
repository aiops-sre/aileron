// Package rca — correlation buffer: 15-minute sliding window of cluster events.
//
// Implements the RCA-Operator buffer pattern: thread-safe append-only log with
// TTL-based expiry. Events are keyed by scope (namespace/pod/node) for fast
// scope-matching during rule evaluation.
package rca

import (
	"sync"
	"time"
)

const defaultBufferWindow = 15 * time.Minute

// EventScope identifies which resource an event belongs to.
type EventScope struct {
	Namespace    string
	PodName      string
	NodeName     string
	ResourceKind string
	ResourceName string
	ClusterID    string
}

// BufferEntry is a single event record in the sliding window.
type BufferEntry struct {
	EventType string
	Severity  string
	Scope     EventScope
	AddedAt   time.Time
}

// Buffer is a sliding-window event store for the correlation engine.
// Events older than `window` are pruned on every Snapshot call.
type Buffer struct {
	mu      sync.Mutex
	entries []BufferEntry
	window  time.Duration
}

// NewBuffer creates a correlation buffer with the given TTL.
func NewBuffer(window time.Duration) *Buffer {
	if window <= 0 {
		window = defaultBufferWindow
	}
	return &Buffer{window: window}
}

// Add appends an event to the buffer.
func (b *Buffer) Add(entry BufferEntry) {
	if entry.AddedAt.IsZero() {
		entry.AddedAt = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, entry)
}

// Snapshot returns a copy of all non-expired entries, pruning the internal slice.
func (b *Buffer) Snapshot() []BufferEntry {
	now := time.Now()
	cutoff := now.Add(-b.window)
	b.mu.Lock()
	defer b.mu.Unlock()
	// Find first non-expired entry
	i := 0
	for i < len(b.entries) && b.entries[i].AddedAt.Before(cutoff) {
		i++
	}
	if i > 0 {
		b.entries = b.entries[i:]
	}
	out := make([]BufferEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

// Len returns the current number of buffered entries without pruning.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// ─── Scope classification helpers ─────────────────────────────────────────────

// IsPodEventType returns true for events scoped to individual pods.
func IsPodEventType(t string) bool {
	return t == "health.pod.crashloopbackoff" ||
		t == "health.pod.oomkilled" ||
		t == "health.pod.imagepull_error" ||
		t == "health.pod.pending" ||
		t == "health.pod.evicted" ||
		t == "health.deployment.degraded"
}

// IsNodeEventType returns true for events scoped to cluster nodes.
func IsNodeEventType(t string) bool {
	return t == "health.node.not_ready" ||
		t == "health.node.disk_pressure" ||
		t == "health.node.memory_pressure" ||
		t == "health.node.cordoned"
}

// IsWorkloadEventType returns true for events scoped to namespaces/workloads.
func IsWorkloadEventType(t string) bool {
	return t == "storage.pvc.pending" ||
		t == "storage.pvc.lost" ||
		t == "storage.pvc.near_full" ||
		t == "change.deployment.rollout" ||
		t == "change.configmap.updated" ||
		t == "change.secret.rotated" ||
		t == "change.rbac.updated" ||
		t == "change.hpa.scaled"
}

// ScopeOf classifies an event type into its correlation scope.
func ScopeOf(eventType string) string {
	switch {
	case IsPodEventType(eventType):
		return "samePod"
	case IsNodeEventType(eventType):
		return "sameNode"
	case IsWorkloadEventType(eventType):
		return "sameNamespace"
	default:
		return "any"
	}
}

// ScopeMatches checks whether trigger and candidate share the required scope.
func ScopeMatches(trigger, candidate BufferEntry, scope string) bool {
	switch scope {
	case "samePod":
		return trigger.Scope.Namespace == candidate.Scope.Namespace &&
			trigger.Scope.PodName == candidate.Scope.PodName &&
			trigger.Scope.PodName != ""
	case "sameNode":
		return trigger.Scope.NodeName == candidate.Scope.NodeName &&
			trigger.Scope.NodeName != ""
	case "sameNamespace":
		return trigger.Scope.Namespace == candidate.Scope.Namespace &&
			trigger.Scope.Namespace != ""
	case "any":
		return true
	default:
		return trigger.Scope.Namespace == candidate.Scope.Namespace &&
			trigger.Scope.PodName == candidate.Scope.PodName
	}
}
