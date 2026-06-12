package evidence

import (
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// PlaybookRegistry maps playbook IDs to ordered fetcher ID lists.
// The DAG executor handles dependencies — fetchers here are a declarative set,
// not necessarily an execution order.
type PlaybookRegistry struct {
	playbooks map[string][]fetchers.FetcherID
}

// NewPlaybookRegistry constructs the registry with the built-in playbooks.
func NewPlaybookRegistry() *PlaybookRegistry {
	r := &PlaybookRegistry{
		playbooks: make(map[string][]fetchers.FetcherID),
	}
	r.registerBuiltins()
	return r
}

// FetchersFor returns the fetcher IDs for a playbook.
// Falls back to the default playbook if the specific one is not registered.
func (r *PlaybookRegistry) FetchersFor(playbookID string) []fetchers.FetcherID {
	if ids, ok := r.playbooks[playbookID]; ok {
		return ids
	}
	return r.playbooks["PB-DEFAULT-001"]
}

// Register adds or replaces a playbook at runtime (for operator customisation).
func (r *PlaybookRegistry) Register(playbookID string, fetcherIDs []fetchers.FetcherID) {
	r.playbooks[playbookID] = fetcherIDs
}

func (r *PlaybookRegistry) registerBuiltins() {
	// ── PB-K8S-001: Kubernetes Node NotReady ────────────────────────────────
	r.playbooks["PB-K8S-001"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"k8s_node_conditions",
		"k8s_node_events",
		"cloudstack_vm_state",
		"cloudstack_host_state", // DependsOn: cloudstack_vm_state
		"okg_changes",
		"okg_similar_incidents",
	}

	// ── PB-K8S-002: Kubernetes Pod CrashLoopBackOff ─────────────────────────
	r.playbooks["PB-K8S-002"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"k8s_pod_exit_code",
		"k8s_pod_logs",
		"k8s_pod_events",
		"okg_changes",
		"okg_similar_incidents",
	}

	// ── PB-K8S-003: ImagePullBackOff ────────────────────────────────────────
	r.playbooks["PB-K8S-003"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"k8s_pod_exit_code",
		"k8s_pod_events",
		"okg_changes",
	}

	// ── PB-K8S-004: PVC Full ────────────────────────────────────────────────
	r.playbooks["PB-K8S-004"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"k8s_pod_events",
		"k8s_pvc_capacity",      // PVC Lost/Warning events from K8s API
		"netapp_volume_state",   // volume utilization + offline volumes
		"netapp_aggregate_state", // root cause: aggregate degraded blocks all PVC I/O
		"netapp_svm_state",      // SVM accessibility
		"okg_changes",
		"okg_similar_incidents",
	}

	// ── PB-K8S-005: PV Failure ──────────────────────────────────────────────
	r.playbooks["PB-K8S-005"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"k8s_pod_events",
		"k8s_pvc_capacity",
		"netapp_volume_state",
		"netapp_aggregate_state",
		"netapp_svm_state",
		"okg_changes",
	}

	// ── PB-INF-001: Bare Metal Failure ──────────────────────────────────────
	r.playbooks["PB-INF-001"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"cloudstack_host_state",
		"okg_changes",
		"okg_similar_incidents",
	}

	// ── PB-INF-002: CloudStack VM Failure ───────────────────────────────────
	r.playbooks["PB-INF-002"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"cloudstack_vm_state",
		"cloudstack_host_state",
		"okg_changes",
		"okg_similar_incidents",
	}

	// ── PB-INF-003: Storage Failure ─────────────────────────────────────────
	r.playbooks["PB-INF-003"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"netapp_volume_state",
		"netapp_aggregate_state",
		"netapp_svm_state",
		"okg_changes",
		"okg_similar_incidents",
	}

	// ── PB-DEFAULT-001: Generic fallback ────────────────────────────────────
	// Includes all available fetchers so any incident gets full evidence coverage
	// regardless of playbook assignment. Specific playbooks prune to their subset.
	r.playbooks["PB-DEFAULT-001"] = []fetchers.FetcherID{
		"eirs_entity_context",
		"k8s_node_conditions",
		"k8s_pod_exit_code",
		"k8s_pod_events",
		"k8s_pvc_capacity",
		"cloudstack_vm_state",
		"cloudstack_host_state",
		"netapp_volume_state",
		"netapp_aggregate_state",
		"netapp_svm_state",
		"okg_changes",
		"okg_similar_incidents",
	}
}
