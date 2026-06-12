package correlation

// infra_propagation.go
//
// Hard-coded infrastructure propagation chains. These encode known failure
// cascades (e.g. NetApp PV PVC Pod) and are used to boost correlation
// confidence when two alerts match a chain step with the correct temporal
// ordering (upstream fired before downstream) within a plausible lag window.
//
// This file is deterministic and has no external dependencies — it augments,
// but does not replace, the topology graph correlator.

import (
	"strings"
	"time"
)

// ChainStep defines a single upstreamdownstream relationship in an infra chain.
type ChainStep struct {
	UpstreamKeywords   []string // any match = upstream label match
	DownstreamKeywords []string // any match = downstream label match
	MaxLagSeconds      float64  // beyond this, the score is halved
	BaseScore          float64  // strong chains 0.85–0.95, weaker 0.70–0.80
}

// InfraDomain names used by GetUpstreamInfraPrior and domain detection.
const (
	InfraDomainStorage      = "storage"
	InfraDomainCompute      = "compute"
	InfraDomainNetwork      = "network"
	InfraDomainControlPlane = "control_plane"
	InfraDomainKubernetes   = "kubernetes"
)

// KnownInfraChains enumerates the propagation cascades AlertHub recognises.
//
// Storage:      NetApp PV PVC Pod Service Ingress
// Compute:      BareMetal KVM VM K8sNode Pod Service
// Network:      NetworkDevice K8sNode Pod Service Ingress
// ControlPlane: EtcdNode APIServer Workload
var KnownInfraChains = map[string][]ChainStep{
	InfraDomainStorage: {
		// NetApp SVM/controller PV
		{
			UpstreamKeywords:   []string{"netapp", "svm", "storage_controller", "storage"},
			DownstreamKeywords: []string{"pv", "persistentvolume", "persistent_volume"},
			MaxLagSeconds:      300,
			BaseScore:          0.92,
		},
		// PV PVC
		{
			UpstreamKeywords:   []string{"pv", "persistentvolume", "persistent_volume"},
			DownstreamKeywords: []string{"pvc", "persistentvolumeclaim", "persistent_volume_claim"},
			MaxLagSeconds:      180,
			BaseScore:          0.90,
		},
		// PVC Pod
		{
			UpstreamKeywords:   []string{"pvc", "persistentvolumeclaim", "persistent_volume_claim"},
			DownstreamKeywords: []string{"pod", "container", "workload"},
			MaxLagSeconds:      180,
			BaseScore:          0.88,
		},
		// Pod Service
		{
			UpstreamKeywords:   []string{"pod", "container"},
			DownstreamKeywords: []string{"service", "svc", "endpoint"},
			MaxLagSeconds:      120,
			BaseScore:          0.80,
		},
		// Service Ingress
		{
			UpstreamKeywords:   []string{"service", "svc"},
			DownstreamKeywords: []string{"ingress", "route", "gateway"},
			MaxLagSeconds:      120,
			BaseScore:          0.75,
		},
	},
	InfraDomainCompute: {
		// BareMetal host KVM hypervisor
		{
			UpstreamKeywords:   []string{"baremetal", "bare_metal", "bm_host", "physical_host"},
			DownstreamKeywords: []string{"kvm", "hypervisor"},
			MaxLagSeconds:      300,
			BaseScore:          0.93,
		},
		// KVM hypervisor VM
		{
			UpstreamKeywords:   []string{"kvm", "hypervisor"},
			DownstreamKeywords: []string{"vm", "virtual_machine", "cloudstack_vm", "instance"},
			MaxLagSeconds:      300,
			BaseScore:          0.92,
		},
		// VM K8s node
		{
			UpstreamKeywords:   []string{"vm", "virtual_machine", "cloudstack_vm", "instance"},
			DownstreamKeywords: []string{"node", "k8s_node", "kubernetes_node", "worker"},
			MaxLagSeconds:      240,
			BaseScore:          0.90,
		},
		// K8s node Pod
		{
			UpstreamKeywords:   []string{"node", "k8s_node", "kubernetes_node", "worker"},
			DownstreamKeywords: []string{"pod", "container", "workload"},
			MaxLagSeconds:      180,
			BaseScore:          0.88,
		},
		// Pod Service
		{
			UpstreamKeywords:   []string{"pod", "container"},
			DownstreamKeywords: []string{"service", "svc"},
			MaxLagSeconds:      120,
			BaseScore:          0.78,
		},
	},
	InfraDomainNetwork: {
		// Network device K8s node
		{
			UpstreamKeywords:   []string{"network_device", "switch", "router", "firewall", "load_balancer", "lb"},
			DownstreamKeywords: []string{"node", "k8s_node", "kubernetes_node"},
			MaxLagSeconds:      240,
			BaseScore:          0.88,
		},
		// K8s node Pod
		{
			UpstreamKeywords:   []string{"node", "k8s_node", "kubernetes_node"},
			DownstreamKeywords: []string{"pod", "container"},
			MaxLagSeconds:      180,
			BaseScore:          0.85,
		},
		// Pod Service
		{
			UpstreamKeywords:   []string{"pod", "container"},
			DownstreamKeywords: []string{"service", "svc"},
			MaxLagSeconds:      120,
			BaseScore:          0.75,
		},
		// Service Ingress
		{
			UpstreamKeywords:   []string{"service", "svc"},
			DownstreamKeywords: []string{"ingress", "route", "gateway"},
			MaxLagSeconds:      120,
			BaseScore:          0.72,
		},
	},
	InfraDomainControlPlane: {
		// Etcd node API server
		{
			UpstreamKeywords:   []string{"etcd", "etcd_node"},
			DownstreamKeywords: []string{"apiserver", "kube-apiserver", "api_server", "kube_apiserver"},
			MaxLagSeconds:      180,
			BaseScore:          0.95,
		},
		// API server Workload
		{
			UpstreamKeywords:   []string{"apiserver", "kube-apiserver", "api_server", "kube_apiserver"},
			DownstreamKeywords: []string{"workload", "pod", "deployment", "statefulset", "daemonset"},
			MaxLagSeconds:      240,
			BaseScore:          0.90,
		},
	},
}

// containsAny returns true if `label` (lower-cased) contains any of the keywords.
func containsAnyInfra(label string, keywords []string) bool {
	ll := strings.ToLower(label)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(ll, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// MatchesUpstreamChain returns true if the (upstream, downstream) label pair
// matches a step in any known propagation chain.
func MatchesUpstreamChain(upstreamLabel, downstreamLabel string) bool {
	if upstreamLabel == "" || downstreamLabel == "" {
		return false
	}
	for _, chain := range KnownInfraChains {
		for _, step := range chain {
			if containsAnyInfra(upstreamLabel, step.UpstreamKeywords) &&
				containsAnyInfra(downstreamLabel, step.DownstreamKeywords) {
				return true
			}
		}
	}
	return false
}

// ScoreInfraPropagation scores an alert pair against known infrastructure
// propagation chains. The score is 0 when no chain step matches, otherwise
// it is the matching step's BaseScore adjusted for temporal ordering and lag:
//
//   - Upstream fired AFTER downstream (wrong direction) score * 0.25
//   - Lag > MaxLagSeconds (plausible but stale) score * 0.5
//   - Otherwise BaseScore
//
// `upstreamSource` is accepted for future source-specific tuning but is not
// currently used in scoring — propagation is source-agnostic today.
func ScoreInfraPropagation(
	upstreamSource, upstreamLabel, downstreamLabel string,
	upstreamFiredAt, downstreamFiredAt time.Time,
) float64 {
	_ = upstreamSource // reserved for future use
	if upstreamLabel == "" || downstreamLabel == "" {
		return 0
	}

	// Find best-matching chain step (highest BaseScore wins if multiple match).
	var best *ChainStep
	for _, chain := range KnownInfraChains {
		for i := range chain {
			step := &chain[i]
			if containsAnyInfra(upstreamLabel, step.UpstreamKeywords) &&
				containsAnyInfra(downstreamLabel, step.DownstreamKeywords) {
				if best == nil || step.BaseScore > best.BaseScore {
					best = step
				}
			}
		}
	}
	if best == nil {
		return 0
	}

	score := best.BaseScore

	// Temporal ordering check — only apply when both timestamps are populated.
	if !upstreamFiredAt.IsZero() && !downstreamFiredAt.IsZero() {
		lag := downstreamFiredAt.Sub(upstreamFiredAt).Seconds()
		if lag < 0 {
			// Upstream fired AFTER downstream — wrong cascade direction.
			score *= 0.25
		} else if lag > best.MaxLagSeconds {
			// Plausible but the lag is beyond the normal propagation window.
			score *= 0.5
		}
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}

// InfraDomainFromLabels inspects label keys and returns the best-guess
// infrastructure domain for the alert. Returns "" if nothing matches.
//
// Precedence (most specific first): control_plane, storage, network,
// compute, kubernetes.
func InfraDomainFromLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	// Lower-case every label key+value for substring matching.
	var keyBlob strings.Builder
	for k, v := range labels {
		keyBlob.WriteString(strings.ToLower(k))
		keyBlob.WriteByte(' ')
		keyBlob.WriteString(strings.ToLower(v))
		keyBlob.WriteByte(' ')
	}
	blob := keyBlob.String()

	switch {
	case strings.Contains(blob, "etcd") ||
		strings.Contains(blob, "apiserver") ||
		strings.Contains(blob, "kube-apiserver") ||
		strings.Contains(blob, "control_plane") ||
		strings.Contains(blob, "control-plane"):
		return InfraDomainControlPlane

	case strings.Contains(blob, "netapp") ||
		strings.Contains(blob, "svm") ||
		strings.Contains(blob, "storage_controller") ||
		strings.Contains(blob, "pvc") ||
		strings.Contains(blob, "persistentvolume") ||
		strings.Contains(blob, "netapp_entity") ||
		strings.Contains(blob, "netapp_cluster") ||
		strings.Contains(blob, "netapp_aggregate") ||
		strings.Contains(blob, " pv ") ||
		strings.Contains(blob, "pv.") ||
		strings.Contains(blob, "pv_") ||
		strings.Contains(blob, "=pv") ||
		strings.Contains(blob, "storage"):
		return InfraDomainStorage

	case strings.Contains(blob, "network_device") ||
		strings.Contains(blob, "switch") ||
		strings.Contains(blob, "router") ||
		strings.Contains(blob, "firewall") ||
		strings.Contains(blob, "load_balancer") ||
		strings.Contains(blob, "ingress") ||
		strings.Contains(blob, "route"):
		return InfraDomainNetwork

	case strings.Contains(blob, "baremetal") ||
		strings.Contains(blob, "bare_metal") ||
		strings.Contains(blob, "kvm") ||
		strings.Contains(blob, "hypervisor") ||
		strings.Contains(blob, "cloudstack_vm") ||
		strings.Contains(blob, "virtual_machine") ||
		strings.Contains(blob, " vm "):
		return InfraDomainCompute

	case strings.Contains(blob, "pod") ||
		strings.Contains(blob, "node") ||
		strings.Contains(blob, "kubernetes") ||
		strings.Contains(blob, "k8s") ||
		strings.Contains(blob, "deployment") ||
		strings.Contains(blob, "statefulset") ||
		strings.Contains(blob, "daemonset"):
		return InfraDomainKubernetes
	}
	return ""
}

// GetUpstreamInfraPrior returns a prior multiplier reflecting how "upstream"
// the alert is in the infrastructure stack. Deep infrastructure alerts
// (bare-metal, hypervisor, etcd) get a boost because they cause cascades,
// while leaf alerts (pods) are demoted because they are usually effects.
//
// Inputs are checked in order: entityType, nodeType, source, then label
// values. First match wins. Default multiplier is 1.00.
func GetUpstreamInfraPrior(entityType, nodeType, source string, labels map[string]string) float64 {
	// Build a single lower-cased blob to test against.
	hints := make([]string, 0, 4+len(labels))
	if entityType != "" {
		hints = append(hints, strings.ToLower(entityType))
	}
	if nodeType != "" {
		hints = append(hints, strings.ToLower(nodeType))
	}
	if source != "" {
		hints = append(hints, strings.ToLower(source))
	}
	for _, v := range labels {
		if v != "" {
			hints = append(hints, strings.ToLower(v))
		}
	}

	contains := func(needle string) bool {
		for _, h := range hints {
			if strings.Contains(h, needle) {
				return true
			}
		}
		return false
	}

	switch {
	// Control plane: etcd / apiserver are the most upstream — cluster-wide blast radius.
	case contains("etcd") || contains("control_plane") || contains("kube-apiserver") || contains("kube_apiserver") || contains("apiserver"):
		return 1.45

	// Storage controllers: NetApp/SVM failures cascade to every PVC/Pod on them.
	case contains("netapp_svm") || contains("storage_controller") || contains("netapp_controller") || contains("netapp"):
		return 1.35

	// Physical infra: BM / KVM / hypervisor host failures affect every VM above.
	case contains("baremetal") || contains("bare_metal") || contains("kvm") || contains("hypervisor"):
		return 1.40

	// VMs: between hypervisor and K8s node.
	case contains("cloudstack_vm") || contains("virtual_machine") || contains(" vm ") || contains("vm_"):
		return 1.20

	// K8s nodes: between VM and pod.
	case contains("k8s_node") || contains("kubernetes_node") || contains(" node ") || contains("node_") || contains(":node"):
		return 1.10

	// Pods: usually effects, not causes.
	case contains("k8s_pod") || contains(" pod ") || contains("pod_") || contains(":pod"):
		return 0.80
	}

	// Fallback: when only the raw entityType equals a bare keyword.
	switch strings.ToLower(entityType) {
	case "bare_metal", "baremetal", "kvm", "hypervisor":
		return 1.40
	case "cloudstack_vm", "vm":
		return 1.20
	case "k8s_node", "node", "kubernetes_node":
		return 1.10
	case "netapp_svm", "storage_controller", "netapp", "netapp_cluster", "netapp_controller":
		return 1.35
	case "netapp_aggregate", "netapp_node":
		return 1.25
	case "netapp_volume", "netapp_lun", "netapp_storage":
		return 1.10
	case "etcd", "control_plane", "kube-apiserver":
		return 1.45
	case "k8s_pod", "pod":
		return 0.80
	}

	return 1.00
}
