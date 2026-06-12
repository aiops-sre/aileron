// Package events defines all intelligence event types shared across KubeSense services.
package events

import (
	"encoding/json"
	"time"
)

// EventType classifies what happened.
type EventType string

// EventSeverity rates the operational impact.
type EventSeverity string

const (
	SeverityCritical EventSeverity = "critical"
	SeverityHigh     EventSeverity = "high"
	SeverityMedium   EventSeverity = "medium"
	SeverityLow      EventSeverity = "low"
	SeverityInfo     EventSeverity = "info"
)

const (
	// Resource lifecycle
	EventResourceCreated EventType = "resource.created"
	EventResourceUpdated EventType = "resource.updated"
	EventResourceDeleted EventType = "resource.deleted"

	// Workload health
	EventPodCrashLoopBackOff EventType = "health.pod.crashloopbackoff"
	EventPodOOMKilled         EventType = "health.pod.oomkilled"
	EventPodImagePullError    EventType = "health.pod.imagepull_error"
	EventPodPending           EventType = "health.pod.pending"
	EventPodEvicted           EventType = "health.pod.evicted"
	EventDeploymentDegraded   EventType = "health.deployment.degraded"
	EventStatefulSetDegraded  EventType = "health.statefulset.degraded"

	// Node health
	EventNodeNotReady     EventType = "health.node.not_ready"
	EventNodeDiskPressure EventType = "health.node.disk_pressure"
	EventNodeMemPressure  EventType = "health.node.memory_pressure"
	EventNodeCordoned     EventType = "health.node.cordoned"

	// Storage
	EventPVCPending  EventType = "storage.pvc.pending"
	EventPVCLost     EventType = "storage.pvc.lost"
	EventPVCNearFull EventType = "storage.pvc.near_full"

	// Changes
	EventDeploymentRollout EventType = "change.deployment.rollout"
	EventConfigMapChanged  EventType = "change.configmap.updated"
	EventSecretRotated     EventType = "change.secret.rotated"
	EventRBACChanged       EventType = "change.rbac.updated"
	EventHPAScaled         EventType = "change.hpa.scaled"

	// Configuration
	EventConfigViolation EventType = "config.violation"

	// Topology
	EventTopologyChanged EventType = "topology.changed"

	// Forecasting
	EventForecastAlert EventType = "forecast.threshold_approaching"
)

// IntelligenceEvent is the canonical event envelope for all KubeSense events.
type IntelligenceEvent struct {
	// Identity — set by publisher
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	ReceivedAt   time.Time `json:"received_at,omitempty"`

	// Source
	ClusterID    string `json:"cluster_id"`
	AgentID      string `json:"agent_id,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`

	// Classification
	Type     EventType     `json:"type"`
	Severity EventSeverity `json:"severity"`

	// Subject
	Resource ResourceRef `json:"resource"`

	// State change
	OldState json.RawMessage `json:"old_state,omitempty"`
	NewState json.RawMessage `json:"new_state,omitempty"`
	Diff     json.RawMessage `json:"diff,omitempty"`

	// Context
	TriggeredBy string            `json:"triggered_by,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ResourceRef identifies a Kubernetes resource.
type ResourceRef struct {
	APIVersion string            `json:"api_version"`
	Kind       string            `json:"kind"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	UID        string            `json:"uid"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// Topic returns the Kafka topic for this event type.
func Subject(clusterID string, t EventType) string {
	switch {
	case isHealth(t):
		return "kubesense.events." + clusterID + ".health"
	case isWorkload(t):
		return "kubesense.events." + clusterID + ".workloads"
	case isStorage(t):
		return "kubesense.events." + clusterID + ".storage"
	case isConfig(t):
		return "kubesense.events." + clusterID + ".config"
	case isChange(t):
		return "kubesense.events." + clusterID + ".workloads"
	default:
		return "kubesense.events." + clusterID + ".workloads"
	}
}

func isHealth(t EventType) bool {
	return len(t) > 7 && t[:7] == "health."
}
func isWorkload(t EventType) bool {
	return len(t) > 9 && t[:9] == "resource." || (len(t) > 7 && t[:7] == "change.")
}
func isStorage(t EventType) bool {
	return len(t) > 8 && t[:8] == "storage."
}
func isConfig(t EventType) bool {
	return len(t) > 7 && t[:7] == "config."
}
func isChange(t EventType) bool {
	return len(t) > 7 && t[:7] == "change."
}
