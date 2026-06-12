package kubesense

import (
	"encoding/json"
	"time"
)

// Kafka topics published by KubeSense agents to the shared Strimzi cluster.
const (
	TopicEventsTopology   = "kubesense.events.topology"
	TopicEventsHealth     = "kubesense.events.health"
	TopicEventsWorkloads  = "kubesense.events.workloads"
	TopicEventsConfig     = "kubesense.events.config"
	TopicEventsStorage    = "kubesense.events.storage"
	TopicEventsNetwork    = "kubesense.events.network"
	TopicInvRequests      = "kubesense.investigations.requests"
	TopicInvResults       = "kubesense.investigations.results"
	TopicForecasts        = "kubesense.forecasts"
	TopicConfigViolations = "kubesense.config.violations"
	TopicAPMGoldenSignals = "kubesense.apm.golden-signals"
	TopicAPMRegressions   = "kubesense.apm.regressions"
	// SRE intelligence topics — KubeSense v2 capabilities
	TopicChaosScores            = "kubesense.chaos.scores"
	TopicDriftDetected          = "kubesense.drift.detected"
	TopicPostmortems            = "kubesense.postmortems"
	TopicToilEvents             = "kubesense.toil.events"
	TopicAnomalies              = "kubesense.anomalies"
	TopicNoiseBudgetSuppressions = "kubesense.noisebudget.suppressions"
)

// EventType is the structured classification of a KubeSense intelligence event.
type EventType string

const (
	EventHealthPodCrashLoopBackOff EventType = "health.pod.crashloopbackoff"
	EventHealthPodOOMKilled        EventType = "health.pod.oomkilled"
	EventHealthPodImagePullError   EventType = "health.pod.imagepull_error"
	EventHealthPodPending          EventType = "health.pod.pending"
	EventHealthPodEvicted          EventType = "health.pod.evicted"
	EventHealthNodeNotReady        EventType = "health.node.not_ready"
	EventHealthNodeDiskPressure    EventType = "health.node.disk_pressure"
	EventHealthNodeMemoryPressure  EventType = "health.node.memory_pressure"
	EventHealthNodeCordoned        EventType = "health.node.cordoned"
	EventChangeDeploymentRollout   EventType = "change.deployment.rollout"
	EventChangeConfigMapUpdated    EventType = "change.configmap.updated"
	EventChangeSecretRotated       EventType = "change.secret.rotated"
	EventChangeRBACUpdated         EventType = "change.rbac.updated"
	EventChangeHPAScaled           EventType = "change.hpa.scaled"
	EventStoragePVCBound           EventType = "storage.pvc.bound"
	EventStoragePVCEvicted         EventType = "storage.pvc.evicted"
	EventConfigViolation           EventType = "config.violation"
	EventForecastThreshold         EventType = "forecast.threshold_approaching"
	EventResourceCreated           EventType = "resource.created"
	EventResourceUpdated           EventType = "resource.updated"
	EventResourceDeleted           EventType = "resource.deleted"
	EventAPMGoldenSignal           EventType = "apm.golden_signal"
	EventAPMRegression             EventType = "apm.regression"
)

// EventSeverity maps to AlertHub severity levels.
type EventSeverity string

const (
	SeverityCritical EventSeverity = "critical"
	SeverityHigh     EventSeverity = "high"
	SeverityMedium   EventSeverity = "medium"
	SeverityLow      EventSeverity = "low"
	SeverityInfo     EventSeverity = "info"
)

// ResourceRef is a reference to a Kubernetes resource.
type ResourceRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
	Cluster   string `json:"cluster"`
}

// IntelligenceEvent is the canonical event envelope published by KubeSense agents.
// Matches pkg/events/events.go in the KubeSense repository.
type IntelligenceEvent struct {
	ID           string            `json:"id"`
	Timestamp    time.Time         `json:"timestamp"`
	ReceivedAt   time.Time         `json:"received_at,omitempty"`
	ClusterID    string            `json:"cluster_id"`
	AgentID      string            `json:"agent_id,omitempty"`
	AgentVersion string            `json:"agent_version,omitempty"`
	Type         EventType         `json:"type"`
	Severity     EventSeverity     `json:"severity"`
	Resource     ResourceRef       `json:"resource"`
	OldState     json.RawMessage   `json:"old_state,omitempty"`
	NewState     json.RawMessage   `json:"new_state,omitempty"`
	Diff         json.RawMessage   `json:"diff,omitempty"`
	TriggeredBy  string            `json:"triggered_by,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// ForecastEvent is the envelope for kubesense.forecasts messages.
type ForecastEvent struct {
	ID              string    `json:"id"`
	Timestamp       time.Time `json:"timestamp"`
	ClusterID       string    `json:"cluster_id"`
	Target          string    `json:"target"`          // "pvc", "node_cpu", "node_memory", "cert"
	ResourceKind    string    `json:"resource_kind"`
	Namespace       string    `json:"namespace"`
	ResourceName    string    `json:"resource_name"`
	CurrentValue    float64   `json:"current_value"`
	Threshold       float64   `json:"threshold"`
	PredictedBreach time.Time `json:"predicted_breach"`
	TrendPerDay     float64   `json:"trend_per_day"`
	ModelConfidence float64   `json:"model_confidence"`
}

// ConfigViolationEvent is the envelope for kubesense.config.violations messages.
type ConfigViolationEvent struct {
	ID           string        `json:"id"`
	Timestamp    time.Time     `json:"timestamp"`
	ClusterID    string        `json:"cluster_id"`
	RuleID       string        `json:"rule_id"`
	Severity     EventSeverity `json:"severity"`
	ResourceKind string        `json:"resource_kind"`
	Namespace    string        `json:"namespace"`
	ResourceName string        `json:"resource_name"`
	Message      string        `json:"message"`
	Remediation  string        `json:"remediation"`
}

// APMGoldenSignal is the envelope for kubesense.apm.golden-signals messages.
type APMGoldenSignal struct {
	ID          string        `json:"id"`
	Timestamp   time.Time     `json:"timestamp"`
	ClusterID   string        `json:"cluster_id"`
	Namespace   string        `json:"namespace"`
	ServiceName string        `json:"service_name"`
	RequestRate float64       `json:"request_rate"`    // requests/sec
	ErrorRate   float64       `json:"error_rate"`      // 0.0–1.0
	Latency     float64       `json:"latency_p99_ms"`
	Saturation  float64       `json:"saturation"`      // 0.0–1.0 CPU saturation
	Severity    EventSeverity `json:"severity,omitempty"`
}

// APMRegression is the envelope for kubesense.apm.regressions messages.
type APMRegression struct {
	ID          string        `json:"id"`
	Timestamp   time.Time     `json:"timestamp"`
	ClusterID   string        `json:"cluster_id"`
	Namespace   string        `json:"namespace"`
	ServiceName string        `json:"service_name"`
	Dimension   string        `json:"dimension"`        // "error_rate", "latency", "request_rate"
	BaselineVal float64       `json:"baseline_value"`
	CurrentVal  float64       `json:"current_value"`
	Deviation   float64       `json:"deviation_sigma"` // standard deviations above baseline
	Severity    EventSeverity `json:"severity"`
}

// InvestigationRequest is published to kubesense.investigations.requests
// by AlertHub to trigger a KubeSense evidence-first investigation for an incident.
type InvestigationRequest struct {
	ID            string    `json:"id"`
	RequestedAt   time.Time `json:"requested_at"`
	IncidentID    string    `json:"incident_id"`
	ClusterID     string    `json:"cluster_id"`
	Namespace     string    `json:"namespace,omitempty"`
	ResourceKind  string    `json:"resource_kind,omitempty"`
	ResourceName  string    `json:"resource_name,omitempty"`
	Severity      string    `json:"severity"`
	AlertTitle    string    `json:"alert_title"`
	CallbackTopic string    `json:"callback_topic"` // always TopicInvResults
}

// InvestigationResult is consumed from kubesense.investigations.results
// by AlertHub after KubeSense completes its evidence-first RCA.
type InvestigationResult struct {
	ID            string               `json:"id"`
	CompletedAt   time.Time            `json:"completed_at"`
	IncidentID    string               `json:"incident_id"`
	ClusterID     string               `json:"cluster_id"`
	Grade         string               `json:"grade"`          // A/B/C/D/F
	Confidence    float64              `json:"confidence"`     // 0.0–1.0
	RootCause     string               `json:"root_cause"`
	Summary       string               `json:"summary"`
	Hypotheses    []KubeSenseHypothesis `json:"hypotheses,omitempty"`
	EvidenceCount int                  `json:"evidence_count"`
	RCADurationMs int64                `json:"rca_duration_ms"`
}

// KubeSenseHypothesis is a single ranked hypothesis from KubeSense's RCA engine.
type KubeSenseHypothesis struct {
	EntityID       string  `json:"entity_id"`
	EntityKind     string  `json:"entity_kind"`
	EntityName     string  `json:"entity_name"`
	Namespace      string  `json:"namespace"`
	FailureMode    string  `json:"failure_mode"`
	Confidence     float64 `json:"confidence"`
	SupportingEvid int     `json:"supporting_evidence"`
	RefutingEvid   int     `json:"refuting_evidence"`
}

// GradeToConfidence converts a KubeSense evidence grade (A–F) to a 0.0–1.0 confidence.
func GradeToConfidence(grade string) float64 {
	switch grade {
	case "A":
		return 0.92
	case "B":
		return 0.78
	case "C":
		return 0.60
	case "D":
		return 0.40
	case "F":
		return 0.20
	default:
		return 0.50
	}
}
