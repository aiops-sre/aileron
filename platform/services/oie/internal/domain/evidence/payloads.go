package evidence

import "time"

// ── K8s Payloads ──────────────────────────────────────────────────────────────

// K8sNodeConditionPayload carries the node's condition set at investigation time.
type K8sNodeConditionPayload struct {
	NodeName   string         `json:"node_name"`
	Conditions []K8sCondition `json:"conditions"`
	// Derived: which pressure types are active
	ActivePressures []string `json:"active_pressures,omitempty"`
}

type K8sCondition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"last_transition_time"`
}

// K8sNodeEventPayload carries a single K8s event related to the node.
type K8sNodeEventPayload struct {
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	EventType string    `json:"event_type"` // Normal | Warning
	Count     int32     `json:"count"`
	FirstTime time.Time `json:"first_time"`
	LastTime  time.Time `json:"last_time"`
	Source    string    `json:"source"`
}

// K8sPodExitCodePayload carries container termination details.
type K8sPodExitCodePayload struct {
	PodName       string    `json:"pod_name"`
	Namespace     string    `json:"namespace"`
	ContainerName string    `json:"container_name"`
	ExitCode      int32     `json:"exit_code"`
	Reason        string    `json:"reason"` // "OOMKilled", "Error", "Completed", etc.
	RestartCount  int32     `json:"restart_count"`
	LastRestartAt time.Time `json:"last_restart_at"`
	// Derived: pattern of restarts ("immediate", "linear", "exponential")
	RestartPattern string `json:"restart_pattern,omitempty"`
}

// K8sLogPatternPayload carries extracted log error patterns.
type K8sLogPatternPayload struct {
	PodName        string   `json:"pod_name"`
	Namespace      string   `json:"namespace"`
	PatternType    string   `json:"pattern_type"` // "oom", "connection_refused", "config_error", etc.
	Occurrences    int      `json:"occurrences"`
	SampleLines    []string `json:"sample_lines"` // up to 3 examples
	ErrorLevel     string   `json:"error_level"`
	EarliestAt     time.Time `json:"earliest_at"`
	LatestAt       time.Time `json:"latest_at"`
}

// ── CloudStack Payloads ────────────────────────────────────────────────────────

// CloudStackVMStatePayload carries the VM's current or historical state.
type CloudStackVMStatePayload struct {
	VMID            string    `json:"vm_id"`
	VMName          string    `json:"vm_name"`
	State           string    `json:"state"` // Running | Stopped | Error | Migrating
	HostID          string    `json:"host_id"`
	HostName        string    `json:"host_name"`
	LastStateChange time.Time `json:"last_state_change"`
	// For audit-log derived evidence:
	IsFromAuditLog  bool      `json:"is_from_audit_log"`
}

// CloudStackHostStatePayload carries the hypervisor host's state.
type CloudStackHostStatePayload struct {
	HostID          string `json:"host_id"`
	HostName        string `json:"host_name"`
	State           string `json:"state"` // Up | Down | Alert | Error
	ResourceState   string `json:"resource_state"`
	ClusterName     string `json:"cluster_name"`
	VMCount         int    `json:"vm_count"`
}

// ── OKG Payloads ──────────────────────────────────────────────────────────────

// ChangeEvidencePayload carries a change event from OKG.
type ChangeEvidencePayload struct {
	ChangeID        string    `json:"change_id"`
	ChangeType      string    `json:"change_type"` // "deployment_event" | "config_change"
	Title           string    `json:"title"`
	Service         string    `json:"service,omitempty"`
	FromVersion     string    `json:"from_version,omitempty"`
	ToVersion       string    `json:"to_version,omitempty"`
	AuthorDisplay   string    `json:"author_display"`
	OccurredAt      time.Time `json:"occurred_at"`
	DeltaMinutes    int       `json:"delta_minutes"` // minutes before incident start
	CausalityScore  float64   `json:"causality_score"`
	RiskLevel       string    `json:"risk_level"`
	SourceURL       string    `json:"source_url,omitempty"`
}

// SimilarIncidentPayload carries a similar historical incident from OKG.
type SimilarIncidentPayload struct {
	IncidentID      string    `json:"incident_id"`
	IncidentNumber  string    `json:"incident_number"`
	Title           string    `json:"title"`
	Similarity      float64   `json:"similarity"`
	RootCause       string    `json:"root_cause,omitempty"`
	HypothesisType  string    `json:"hypothesis_type,omitempty"`
	MTTRSeconds     int       `json:"mttr_seconds,omitempty"`
	ResolvedWith    string    `json:"resolved_with,omitempty"`
	OccurredAt      time.Time `json:"occurred_at"`
}

// ── NetApp ONTAP Payloads ─────────────────────────────────────────────────────

// NetAppVolumePayload carries volume utilization and state from ONTAP REST API.
type NetAppVolumePayload struct {
	ClusterName string `json:"cluster_name"`
	VolumeName  string `json:"volume_name"`
	SVMName     string `json:"svm_name"`
	State       string `json:"state"`        // online | offline | restricted | mixed
	SizeBytes   int64  `json:"size_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
	PercentUsed int    `json:"percent_used"`
}

// NetAppAggregatePayload carries RAID aggregate state — degraded aggregates are
// the root cause for cascading PVC mount failures across multiple namespaces.
type NetAppAggregatePayload struct {
	ClusterName string `json:"cluster_name"`
	AggrName    string `json:"aggr_name"`
	NodeName    string `json:"node_name"`
	State       string `json:"state"`        // online | offline | restricted | degraded
	UsedPercent int    `json:"used_percent"`
	SizeBytes   int64  `json:"size_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
}

// NetAppSVMPayload carries Storage Virtual Machine state.
type NetAppSVMPayload struct {
	ClusterName string `json:"cluster_name"`
	SVMName     string `json:"svm_name"`
	State       string `json:"state"`   // running | stopped | starting | stopping
	Subtype     string `json:"subtype"` // default | dp_destination | sync_source
}

// ── EIRS Payloads ─────────────────────────────────────────────────────────────

// EntityContextPayload carries the EIRS-resolved entity profile.
type EntityContextPayload struct {
	CanonicalEntityID string            `json:"canonical_entity_id"`
	EntityType        string            `json:"entity_type"`
	DisplayName       string            `json:"display_name"`
	ClusterRef        string            `json:"cluster_ref,omitempty"`
	NamespaceRef      string            `json:"namespace_ref,omitempty"`
	GraphNodeID       string            `json:"graph_node_id"`
	ResolutionMethod  string            `json:"resolution_method"`
	Confidence        float64           `json:"confidence"`
	SourceIDs         map[string]string `json:"source_ids,omitempty"` // source→source_entity_id
}

// TopologyParentPayload carries a parent entity from the topology graph.
type TopologyParentPayload struct {
	ParentEntityID   string `json:"parent_entity_id"`
	ParentEntityType string `json:"parent_entity_type"`
	ParentName       string `json:"parent_name"`
	RelationshipType string `json:"relationship_type"` // RUNS_ON | HOSTED_BY | PART_OF
	GraphNodeID      string `json:"graph_node_id"`
}
