package evidence

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ── Type enumerations ──────────────────────────────────────────────────────────

// EvidenceType is the structured classification of a piece of evidence.
type EvidenceType string

const (
	// ── Kubernetes: node ─────────────────────────────────────────────────────────
	TypeK8sNodeCondition EvidenceType = "k8s_node_condition"
	TypeK8sNodeEvent     EvidenceType = "k8s_node_event"
	TypeK8sNodeNotReady  EvidenceType = "k8s_node_not_ready"  // node confirmed NotReady
	TypeK8sNodeEvicted   EvidenceType = "k8s_node_eviction"   // kubelet evicted pods from node

	// ── Kubernetes: pod exit ──────────────────────────────────────────────────────
	TypeK8sPodExitCode        EvidenceType = "k8s_pod_exit_code"
	TypeK8sPodExitCodeZero    EvidenceType = "k8s_pod_exit_code_zero"   // completed OK but restart=many → design error
	TypeK8sPodExitCodeOOM     EvidenceType = "k8s_pod_exit_code_oom"    // exit 137 / OOMKilled
	TypeK8sPodExitCodeSegfault EvidenceType = "k8s_pod_exit_code_segfault" // exit 139

	// ── Kubernetes: pod events (specific sub-types — no template should use generic TypeK8sPodEvent) ──
	TypeK8sPodEvent          EvidenceType = "k8s_pod_event"          // legacy/fallback only
	TypeK8sPodEventCrashLoop EvidenceType = "k8s_pod_event_crashloop" // BackOff, CrashLoopBackOff
	TypeK8sPodEventStorage   EvidenceType = "k8s_pod_event_storage"   // FailedMount, FailedAttachVolume, VolumeResizeFailed
	TypeK8sPodEventImage     EvidenceType = "k8s_pod_event_image"     // ImagePullBackOff, ErrImagePull
	TypeK8sPodEventFailed    EvidenceType = "k8s_pod_event_failed"    // Failed, BackoffLimitExceeded
	TypeK8sPodEventEviction  EvidenceType = "k8s_pod_event_eviction"  // Evicted (memory/disk pressure)
	TypeK8sPodEventPending   EvidenceType = "k8s_pod_event_pending"   // FailedScheduling, Unschedulable

	// ── Kubernetes: resource ─────────────────────────────────────────────────────
	TypeK8sOOMKill              EvidenceType = "k8s_oom_kill"
	TypeK8sResourcePressure     EvidenceType = "k8s_resource_pressure"      // node memory/disk/pid pressure
	TypeK8sCPURequestSaturation EvidenceType = "k8s_cpu_request_saturation" // requests >> actual usage (scheduling)
	TypeK8sImagePullError       EvidenceType = "k8s_image_pull_error"
	TypeK8sPodLog               EvidenceType = "k8s_pod_log_pattern"

	// ── NetApp ONTAP ──────────────────────────────────────────────────────────────
	TypeNetAppVolumeState    EvidenceType = "netapp_volume_state"     // volume online/offline/restricted
	TypeNetAppVolumeFull     EvidenceType = "netapp_volume_full"      // >85% used
	TypeNetAppAggregateState EvidenceType = "netapp_aggregate_state"  // aggr degraded/restricted (ROOT CAUSE for cascades)
	TypeNetAppSVMState       EvidenceType = "netapp_svm_state"        // SVM stopped/restricted
	TypeNetAppNodeState      EvidenceType = "netapp_node_state"       // storage node degraded

	// ── CloudStack ────────────────────────────────────────────────────────────────
	TypeCloudStackVMState   EvidenceType = "cloudstack_vm_state"
	TypeCloudStackHostState EvidenceType = "cloudstack_host_state"

	// ── Change intelligence (OKG) ─────────────────────────────────────────────────
	TypeChangeDeployment  EvidenceType = "change_deployment"
	TypeChangeConfig      EvidenceType = "change_config"
	TypeOKGCausalityScore EvidenceType = "okg_causality_score"
	TypeSimilarIncident   EvidenceType = "similar_incident"

	// ── EIRS entity context ───────────────────────────────────────────────────────
	TypeEntityContext  EvidenceType = "eirs_entity_context"
	TypeTopologyParent EvidenceType = "topology_parent_entity"

	// ── KubeSense intelligence signals ───────────────────────────────────────────
	// These evidence types are populated by the KubeSense evidence fetcher,
	// which queries the kubesense_* tables written by the Kafka consumer.
	TypeKubeSenseHealthEvent     EvidenceType = "kubesense_health_event"     // pod/node health event from KubeSense
	TypeKubeSenseConfigViolation EvidenceType = "kubesense_config_violation" // pre-computed rule violation (missing probe, image:latest, etc.)
	TypeKubeSenseForecast        EvidenceType = "kubesense_forecast"         // exhaustion/breach prediction (PVC, node, cert)
	TypeKubeSenseAPMRegression   EvidenceType = "kubesense_apm_regression"   // golden-signal regression detected by EWMA
	// SRE intelligence evidence types (KubeSense v2 capabilities)
	TypeKubeSenseChaosScore EvidenceType = "kubesense_chaos_score" // chaos readiness score — low score = likely SPOF in blast radius
	TypeKubeSenseDrift      EvidenceType = "kubesense_drift"       // GitOps drift detected before incident — probable root cause
	TypeKubeSenseAnomaly    EvidenceType = "kubesense_anomaly"     // EWMA anomaly on resource metric preceding incident

	// ── Kubernetes: extended pod analyzer (K8sGPT taxonomy) ─────────────────────
	// Additional evidence types for the 12 K8sGPT waiting-reason codes.
	TypeK8sPodEventCreateConfigError EvidenceType = "k8s_pod_event_create_config_error" // CreateContainerConfigError
	TypeK8sPodEventInvalidImage      EvidenceType = "k8s_pod_event_invalid_image"       // InvalidImageName
	TypeK8sPodEventContainerCreating EvidenceType = "k8s_pod_event_container_creating"  // ContainerCreating stuck
	TypeK8sPodEventNetworkNotReady   EvidenceType = "k8s_pod_event_network_not_ready"   // NetworkNotReady
	TypeK8sPodEventPDBBlocked        EvidenceType = "k8s_pod_event_pdb_blocked"         // PodDisruptionBudget DisruptionAllowed=False
	TypeK8sWebhookCascade            EvidenceType = "k8s_webhook_cascade"               // ValidatingWebhook failure → pod-creation cascade
	TypeK8sOwnerChain                EvidenceType = "k8s_owner_chain"                   // Pod→ReplicaSet→Deployment traversal result
	TypeK8sServiceEndpointNotReady   EvidenceType = "k8s_service_endpoint_not_ready"    // Service has NotReadyAddresses (pods not ready)

	// ── Cloud topology context ────────────────────────────────────────────────────
	// TypeCloudContext is produced by the cloud topology fetcher when a topology
	// resolve call returns cloud-provider fields (provider, region, resource_type).
	// Covers AWS EC2, GCP GCE, Azure VM, AliCloud ECS, and any other provider that
	// registers nodes in the Aileron topology graph.
	TypeCloudContext EvidenceType = "cloud_context"

	// ── Circuit / source state ────────────────────────────────────────────────────
	TypeSourceCircuitOpen EvidenceType = "source_circuit_open"
	TypeSourceUnavailable EvidenceType = "source_unavailable"
)

// EvidenceRole describes how a piece of evidence relates to a hypothesis.
type EvidenceRole string

const (
	RoleSupports    EvidenceRole = "SUPPORTS"
	RoleContradicts EvidenceRole = "CONTRADICTS"
	RoleContext     EvidenceRole = "CONTEXT"
)

// TemporalMode indicates whether evidence was collected at incident time or now.
type TemporalMode string

const (
	// TemporalHistorical: evidence from timestamped source records (K8s events, audit logs).
	// Authoritative — reflects state at incident time.
	TemporalHistorical TemporalMode = "HISTORICAL"

	// TemporalCurrent: current state fetched now. May differ from incident state
	// if entity auto-recovered. Contradiction weight is dampened by temporal gap.
	TemporalCurrent TemporalMode = "CURRENT"
)

// FetchStatus records why evidence was or was not gathered.
type FetchStatus string

const (
	FetchSuccess     FetchStatus = "SUCCESS"
	FetchTimeout     FetchStatus = "TIMEOUT"
	FetchCircuitOpen FetchStatus = "CIRCUIT_OPEN"
	FetchError       FetchStatus = "ERROR"
	FetchMissing     FetchStatus = "MISSING" // entity not known to this source
	FetchLate        FetchStatus = "LATE"    // arrived after investigation completed
)

// ── Evidence aggregate ─────────────────────────────────────────────────────────

// Evidence is a single structured observation used to validate or reject a hypothesis.
type Evidence struct {
	ID              uuid.UUID
	InvestigationID uuid.UUID
	// HypothesisID is set after hypothesis evaluation links evidence to a hypothesis.
	HypothesisID *uuid.UUID
	// FetcherID identifies which fetcher produced this evidence.
	FetcherID string
	// IdempotencyKey prevents duplicate inserts on crash recovery.
	// Format: "{investigation_id}::{fetcher_id}::{entity_id}::{evidence_type}"
	IdempotencyKey string
	// RunID groups all evidence from a single investigation run.
	// On crash recovery, new run gets a new RunID; scoring uses only the latest run.
	RunID uuid.UUID

	// Classification
	EvidenceType EvidenceType
	Source       string // "kubernetes" | "cloudstack" | "netapp" | "okg" | "eirs"
	Role         EvidenceRole
	// Weight is how much this evidence contributes to hypothesis scoring (0-1).
	Weight float64
	// EvidenceConfidence is the intrinsic reliability of this source (0-1).
	// K8s API = 0.98, CloudStack = 0.95, OKG change = 0.85, etc.
	EvidenceConfidence float64
	// EvidenceGroup groups correlated evidence (e.g. "oom_signals").
	// Within a group, max-weight scoring is used instead of independence-complement.
	EvidenceGroup *string

	// Temporal
	TemporalMode    TemporalMode
	AsOfTime        *time.Time // The time this evidence reflects (incident start for HISTORICAL)
	TemporalGapSecs *int       // Seconds between incident start and when evidence was gathered

	// Content
	Description string
	// Payload is a typed JSON blob. The concrete struct depends on EvidenceType.
	Payload json.RawMessage

	// Timing
	OccurredAt  *time.Time // When the event actually happened
	GatheredAt  time.Time

	// Fetch metadata
	FetchDurationMs int
	FetchStatus     FetchStatus
	FetchError      *string

	// Applied to scoring: set true after hypothesis engine uses this evidence.
	// Prevents double-scoring on crash recovery / late evidence re-injection.
	AppliedToScoring bool

	CreatedAt time.Time
}

// NewEvidence constructs an Evidence with required fields validated.
func NewEvidence(
	investigationID uuid.UUID,
	runID uuid.UUID,
	fetcherID string,
	idempotencyKey string,
	evType EvidenceType,
	source string,
	role EvidenceRole,
	weight float64,
	confidence float64,
	description string,
	temporalMode TemporalMode,
	payload json.RawMessage,
) *Evidence {
	now := time.Now().UTC()
	return &Evidence{
		ID:                 uuid.New(),
		InvestigationID:    investigationID,
		RunID:              runID,
		FetcherID:          fetcherID,
		IdempotencyKey:     idempotencyKey,
		EvidenceType:       evType,
		Source:             source,
		Role:               role,
		Weight:             weight,
		EvidenceConfidence: confidence,
		TemporalMode:       temporalMode,
		Description:        description,
		Payload:            payload,
		FetchStatus:        FetchSuccess,
		GatheredAt:         now,
		CreatedAt:          now,
	}
}

// NewContextEvidence creates a CONTEXT-role evidence piece (neutral, not scored).
func NewContextEvidence(
	investigationID uuid.UUID,
	runID uuid.UUID,
	fetcherID string,
	idempotencyKey string,
	evType EvidenceType,
	source string,
	description string,
	payload json.RawMessage,
) *Evidence {
	return NewEvidence(investigationID, runID, fetcherID, idempotencyKey,
		evType, source, RoleContext, 0.0, 1.0, description, TemporalCurrent, payload)
}

// NewCircuitOpenEvidence creates a CONTEXT evidence piece when a source circuit is open.
func NewCircuitOpenEvidence(investigationID, runID uuid.UUID, fetcherID, sourceName string) *Evidence {
	payload, _ := json.Marshal(map[string]string{
		"source": sourceName,
		"reason": "circuit breaker open — source has recent repeated failures",
	})
	return &Evidence{
		ID:              uuid.New(),
		InvestigationID: investigationID,
		RunID:           runID,
		FetcherID:       fetcherID,
		IdempotencyKey:  fetcherID + "::circuit_open",
		EvidenceType:    TypeSourceCircuitOpen,
		Source:          sourceName,
		Role:            RoleContext,
		Weight:          0.0,
		Description:     "Evidence source " + sourceName + " circuit is open — evidence not gathered",
		Payload:         payload,
		FetchStatus:     FetchCircuitOpen,
		GatheredAt:      time.Now().UTC(),
		CreatedAt:       time.Now().UTC(),
	}
}

// TemporalGapDampening returns the effective evidence confidence after applying
// temporal gap dampening for CURRENT-mode evidence.
// Gap 0-60s: full confidence. Gap 60-180s: linear decay to 50%. Gap >180s: 50%.
func (e *Evidence) TemporalGapDampening() float64 {
	if e.TemporalMode == TemporalHistorical || e.TemporalGapSecs == nil {
		return e.EvidenceConfidence
	}
	gap := *e.TemporalGapSecs
	if gap <= 60 {
		return e.EvidenceConfidence
	}
	if gap >= 180 {
		return e.EvidenceConfidence * 0.50
	}
	decay := float64(gap-60) / float64(120) // 0 at gap=60, 1 at gap=180
	return e.EvidenceConfidence * (1.0 - decay*0.50)
}

// EffectiveWeight returns weight × dampened-confidence.
func (e *Evidence) EffectiveWeight() float64 {
	return e.Weight * e.TemporalGapDampening()
}
