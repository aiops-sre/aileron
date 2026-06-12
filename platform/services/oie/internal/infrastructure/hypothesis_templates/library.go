// Package hypothesis_templates contains the built-in library of hypothesis templates.
// Each template encodes SRE operational knowledge: what evidence confirms or
// refutes a specific failure mode, how confident to be, and what to do about it.
//
// Templates are the primary knowledge representation in OIE.
// They are loaded at startup and can be augmented by DB-stored custom templates.
package hypothesis_templates

import (
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
)

// All returns the complete built-in template library.
func All() []*domain.HypothesisTemplate {
	return []*domain.HypothesisTemplate{
		htK8S001(),
		htK8S002(),
		htK8S003(),
		htK8S004(),
		htK8S005(),
		htK8S006(),
		htK8S007(),
		htK8S008(),
		htK8S009(),
		htK8S010(),
		htK8S011(),
		htK8S012(),
		htK8S013(),
		htK8S014(),
		htK8S015(),
		htINF001(),
		htINF002(),
		htINF003(),
		htINF004(),
		htINF005(),
		htCHG001(),
		htCHG002(),
	}
}

// ByPlaybook maps playbook IDs to the hypothesis template types they generate.
var ByPlaybook = map[string][]string{
	"PB-K8S-001": {"node_bare_metal_failure", "node_cloudstack_vm_failure", "node_resource_pressure", "node_kubelet_failure", "node_network_isolation", "node_memory_pressure_eviction", "deployment_introduced_regression"},
	"PB-K8S-002": {"crashloop_bad_deployment", "crashloop_oom_kill", "crashloop_config_error", "crashloop_dependency_unavailable", "crashloop_job_as_deployment", "deployment_introduced_regression"},
	"PB-K8S-003": {"image_pull_bad_tag", "image_pull_auth_failure"},
	"PB-K8S-004": {"pvc_storage_full", "netapp_aggregate_degraded"},
	"PB-K8S-005": {"pv_storage_offline", "netapp_aggregate_degraded"},
	"PB-INF-001": {"bare_metal_hardware_failure"},
	"PB-INF-002": {"node_cloudstack_vm_failure", "bare_metal_hardware_failure", "cloudstack_platform_issue"},
	"PB-INF-003": {"storage_cluster_failure", "netapp_aggregate_degraded"},
	// PB-DEFAULT-001 evaluates the full set of hypotheses so any incident type
	// gets accurate scoring regardless of playbook assignment.
	"PB-DEFAULT-001": {
		"node_bare_metal_failure",
		"node_cloudstack_vm_failure",
		"node_resource_pressure",
		"node_kubelet_failure",
		"node_memory_pressure_eviction",
		"crashloop_bad_deployment",
		"crashloop_oom_kill",
		"crashloop_config_error",
		"crashloop_dependency_unavailable",
		"crashloop_job_as_deployment",
		"image_pull_bad_tag",
		"image_pull_auth_failure",
		"pvc_storage_full",
		"pv_storage_offline",
		"netapp_aggregate_degraded",
		"bare_metal_hardware_failure",
		"network_failure",
		"deployment_introduced_regression",
		"config_change_regression",
	},
}

// ─── Kubernetes Node NotReady Hypotheses ─────────────────────────────────────

// HT-K8S-001: Bare Metal Hardware Failure
func htK8S001() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-001",
		Type:        "node_bare_metal_failure",
		Name:        "Bare Metal Hardware Failure",
		Description: "Physical server hosting the K8s node has failed or is in error state.",
		ApplicableEntityTypes:    []string{"k8s_node", "bare_metal"},
		ApplicableFailureClasses: []string{"NotReady", "Unreachable"},
		ApplicableDomains:        []string{"kubernetes", "infrastructure"},
		BasePrior:                0.12,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sNodeCondition), Weight: 0.88, Required: true, Description: "K8s node condition NotReady"},
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.88, Required: true, Description: "CloudStack host state"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.92, EvidenceGroup: "host_failure", Description: "CloudStack host in error/down state"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.85, Description: "CloudStack VM is running (host healthy)"},
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.88, Description: "CloudStack host is Up"},
		},
		HardRejectionEvidenceTypes: []string{},
		MinSupportingEvidence:      1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Escalate to infrastructure team", Steps: []string{"Page CloudStack/hardware on-call team", "Provide node name and CloudStack host ID"}, Urgency: "immediate", SafetyLevel: "SAFE"},
			{Title: "Drain the affected node", Steps: []string{"kubectl drain <node> --ignore-daemonsets --delete-emptydir-data"}, Urgency: "high", EstimatedMinutes: 15, SafetyLevel: "DANGEROUS", ConfirmationMessage: "This evicts all pods from the node. Verify cluster has capacity to absorb them."},
		},
	}
}

// HT-K8S-002: CloudStack VM Failure
func htK8S002() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-002",
		Type:        "node_cloudstack_vm_failure",
		Name:        "CloudStack VM Failure",
		Description: "The CloudStack VM backing the K8s node is stopped or in error state.",
		ApplicableEntityTypes:    []string{"k8s_node", "virtual_machine"},
		ApplicableFailureClasses: []string{"NotReady", "Unreachable"},
		ApplicableDomains:        []string{"kubernetes", "infrastructure"},
		BasePrior:                0.22,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.88, Required: true, Description: "CloudStack VM state"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.90, EvidenceGroup: "vm_failure_signals", Description: "CloudStack VM is stopped or in error state"},
			{EvidenceType: string(ev.TypeK8sNodeCondition), Weight: 0.88, Description: "K8s node is NotReady"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.88, Description: "CloudStack VM is Running"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Start VM in CloudStack", Steps: []string{"Open CloudStack console", "Navigate to Instances → find VM by name", "Start the VM", "Wait for K8s node to return Ready"}, Urgency: "immediate", EstimatedMinutes: 5, SafetyLevel: "CAUTION"},
			{Title: "Verify node recovery", Steps: []string{"kubectl get node <node-name> -w", "kubectl get pods --all-namespaces --field-selector=spec.nodeName=<node-name>"}, Urgency: "high", SafetyLevel: "SAFE"},
		},
	}
}

// HT-K8S-003: Node Resource Pressure (CPU/Memory/Disk over-commitment)
func htK8S003() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-003",
		Type:        "node_resource_pressure",
		Name:        "Node Resource Pressure / Over-Commitment",
		Description: "Node is under memory, disk, or CPU-request pressure. Either active MemoryPressure/DiskPressure conditions are causing kubelet evictions, or CPU requests from all pods sum to >90% of allocatable (scheduler over-commitment) causing pod-not-ready cascades without genuine CPU saturation.",
		ApplicableEntityTypes:    []string{"k8s_node"},
		ApplicableFailureClasses: []string{"NotReady", "MemoryPressure", "DiskPressure", "CPUSaturation"},
		ApplicableDomains:        []string{"kubernetes"},
		BasePrior:                0.18,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sResourcePressure), Weight: 0.90, EvidenceGroup: "node_pressure", Description: "Active pressure condition (MemoryPressure/DiskPressure/PIDPressure)"},
			// CPU request saturation: requests >> actual usage → scheduler packs too many pods
			{EvidenceType: string(ev.TypeK8sCPURequestSaturation), Weight: 0.85, EvidenceGroup: "node_pressure", Description: "CPU requests sum >90% of node allocatable — scheduler over-commitment"},
			{EvidenceType: string(ev.TypeK8sNodeCondition), Weight: 0.75, Description: "Node condition shows pressure"},
			{EvidenceType: string(ev.TypeK8sNodeEvent), Weight: 0.70, EvidenceGroup: "node_pressure", Description: "Eviction or pressure event on node"},
			{EvidenceType: string(ev.TypeK8sPodEventEviction), Weight: 0.85, Description: "Pod eviction events — kubelet evicting to reclaim resources"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.70, Description: "VM stopped — node failure is not resource pressure"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Identify top resource consumers", Steps: []string{
				"kubectl top pods --all-namespaces --sort-by=memory | head -20",
				"kubectl describe node <node> | grep -A5 'Allocated resources'",
			}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Cordon node to prevent new scheduling", Steps: []string{"kubectl cordon <node>"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 1},
			{Title: "Identify over-requested pods", Steps: []string{
				"kubectl get pods -A -o json | jq '.items[] | select(.spec.nodeName==\"<node>\") | {name:.metadata.name, ns:.metadata.namespace, cpuReq:.spec.containers[].resources.requests.cpu}'",
				"Consider reducing CPU requests on non-critical workloads or adding nodes",
			}, SafetyLevel: "SAFE", Urgency: "high"},
		},
	}
}

// HT-K8S-004: Kubelet Process Failure
func htK8S004() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-004",
		Type:        "node_kubelet_failure",
		Name:        "Kubelet Process Failure",
		Description: "The kubelet process on the node has crashed or stopped sending heartbeats.",
		ApplicableEntityTypes:    []string{"k8s_node"},
		ApplicableFailureClasses: []string{"NotReady"},
		ApplicableDomains:        []string{"kubernetes"},
		BasePrior:                0.12,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sNodeEvent), Weight: 0.85, Description: "NodeNotReady event with kubelet reason"},
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.80, Description: "VM is Running (kubelet failed, not the VM)"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.80, Description: "VM is stopped — kubelet cannot be the primary cause"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "SSH to node and check kubelet", Steps: []string{"ssh <node-ip>", "systemctl status kubelet", "journalctl -u kubelet -n 100"}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Restart kubelet if accessible", Steps: []string{"systemctl restart kubelet", "kubectl get node <node> -w"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 3},
		},
	}
}

// HT-K8S-005: Node Network Isolation
func htK8S005() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-005",
		Type:        "node_network_isolation",
		Name:        "Node Network Isolation",
		Description: "The node is unreachable from the K8s control plane due to a network partition.",
		ApplicableEntityTypes:    []string{"k8s_node"},
		ApplicableFailureClasses: []string{"NotReady", "NetworkUnavailable"},
		ApplicableDomains:        []string{"kubernetes", "infrastructure"},
		BasePrior:                0.10,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sNodeEvent), Weight: 0.82, Description: "NetworkUnavailable condition on node"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackVMState), Weight: 0.75, Description: "VM stopped — network partition not the primary cause"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Check network connectivity to node", Steps: []string{"ping <node-ip>", "telnet <node-ip> 10250 (kubelet port)", "Check CloudStack network ACLs"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// ─── Kubernetes Pod CrashLoopBackOff Hypotheses ───────────────────────────────

// HT-K8S-006: CrashLoop — Bad Deployment
func htK8S006() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-006",
		Type:        "crashloop_bad_deployment",
		Name:        "CrashLoopBackOff: Defective Deployment",
		Description: "A recent deployment introduced an application bug causing the pod to crash on startup.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff"},
		ApplicableDomains:        []string{"kubernetes", "application"},
		BasePrior:                0.30,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sPodExitCode), Weight: 0.80, Required: true, Description: "Container exit code"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeChangeDeployment), Weight: 0.85, EvidenceGroup: "deployment_signals", Description: "Recent deployment of this workload"},
			{EvidenceType: string(ev.TypeOKGCausalityScore), Weight: 0.85, EvidenceGroup: "deployment_signals", Description: "OKG high causality score"},
			{EvidenceType: string(ev.TypeK8sPodLog), Weight: 0.72, Description: "Application error on startup in logs"},
			{EvidenceType: string(ev.TypeK8sPodEventCrashLoop), Weight: 0.70, Description: "BackOff event on pod"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sOOMKill), Weight: 0.75, Description: "OOM kill — application crash not a bug but resource exhaustion"},
		},
		// Exit code 137 (OOM) hard-rejects this hypothesis.
		HardRejectionEvidenceTypes: []string{string(ev.TypeK8sOOMKill)},
		MinSupportingEvidence:      1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Retrieve crash logs", Steps: []string{"kubectl logs <pod> --previous --tail=100"}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Roll back deployment", Steps: []string{"kubectl rollout undo deployment/<name> -n <namespace>"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 3, ConfirmationMessage: "This reverts to the previous deployment version."},
		},
	}
}

// HT-K8S-007: CrashLoop — OOM Kill
func htK8S007() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-007",
		Type:        "crashloop_oom_kill",
		Name:        "CrashLoopBackOff: Out-of-Memory Kill",
		Description: "The container is being killed by the kernel OOM killer because it exceeds its memory limit.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff", "OOMKilled"},
		ApplicableDomains:        []string{"kubernetes", "application"},
		BasePrior:                0.25,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sPodExitCode), Weight: 0.85, Required: true, Description: "Exit code 137 (SIGKILL / OOMKilled)"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sOOMKill), Weight: 0.92, EvidenceGroup: "oom_signals", Description: "K8s OOMKilling event"},
			{EvidenceType: string(ev.TypeK8sPodExitCodeOOM), Weight: 0.90, EvidenceGroup: "oom_signals", Description: "Exit code 137 (OOM kill)"},
			{EvidenceType: string(ev.TypeK8sPodExitCode), Weight: 0.85, EvidenceGroup: "oom_signals", Description: "Exit code 137 (OOM kill — legacy type)"},
			{EvidenceType: string(ev.TypeK8sPodLog), Weight: 0.75, EvidenceGroup: "oom_signals", Description: "OutOfMemoryError in logs"},
		},
		// Exit codes other than 137 refute OOM.
		// Implemented via evidence scoring — non-137 exit codes score low for this template.
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Identify memory usage trend", Steps: []string{"kubectl top pod <pod> -n <ns>", "kubectl describe pod <pod> (check memory limits)"}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Increase memory limit", Steps: []string{"kubectl edit deployment <name> -n <ns>", "Increase resources.limits.memory", "Monitor pod after restart"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 10, ConfirmationMessage: "Verify the node has sufficient capacity before increasing the limit."},
		},
	}
}

// HT-K8S-008: CrashLoop — Config Error
func htK8S008() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-008",
		Type:        "crashloop_config_error",
		Name:        "CrashLoopBackOff: Missing or Invalid Configuration",
		Description: "The container cannot start because a required environment variable, secret, or configmap is missing or invalid.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff"},
		ApplicableDomains:        []string{"kubernetes", "application"},
		BasePrior:                0.20,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sPodLog), Weight: 0.82, EvidenceGroup: "config_missing", Description: "Missing env var or secret not found in logs"},
			{EvidenceType: string(ev.TypeK8sPodEventFailed), Weight: 0.88, EvidenceGroup: "config_missing", Description: "MountError or SecretNotFound K8s event"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sOOMKill), Weight: 0.70, Description: "OOM kill — config not the primary cause"},
		},
		HardRejectionEvidenceTypes: []string{string(ev.TypeK8sOOMKill)},
		MinSupportingEvidence:      1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Inspect pod environment and mounts", Steps: []string{"kubectl describe pod <pod> -n <ns>", "kubectl get secret <secret-name> -n <ns> (verify exists)"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// HT-K8S-009: CrashLoop — Upstream Dependency Unavailable
func htK8S009() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-009",
		Type:        "crashloop_dependency_unavailable",
		Name:        "CrashLoopBackOff: Upstream Dependency Unavailable",
		Description: "The application starts and then crashes because a required upstream service (DB, API, message queue) is unreachable.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff"},
		ApplicableDomains:        []string{"kubernetes", "application"},
		BasePrior:                0.18,
		SupportingEvidence: []domain.EvidenceSpec{
			// BackOff event (CrashLoop) is the primary symptom — app exits cleanly (exit 0)
			// then K8s backs off restarting it. Tagged k8s_pod_crash group.
			{EvidenceType: string(ev.TypeK8sPodEventCrashLoop), Weight: 0.72, EvidenceGroup: "k8s_pod_crash_signals", Description: "BackOff / CrashLoop event — pod restarting repeatedly"},
			// Exit-code-zero with many restarts: app starts, can't connect, exits cleanly
			{EvidenceType: string(ev.TypeK8sPodExitCodeZero), Weight: 0.75, EvidenceGroup: "k8s_pod_crash_signals", Description: "Exit code 0 — app exits cleanly, suggests dependency check on startup"},
			{EvidenceType: string(ev.TypeK8sPodExitCode), Weight: 0.65, EvidenceGroup: "k8s_pod_crash_signals", Description: "Exit code 0 (Completed) — app exited cleanly, suggests missing dependency"},
			{EvidenceType: string(ev.TypeK8sPodLog), Weight: 0.78, Description: "Connection refused / dial timeout in logs"},
			{EvidenceType: string(ev.TypeSimilarIncident), Weight: 0.60, Description: "Similar past incident resolved by fixing upstream"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sOOMKill), Weight: 0.70, Description: "OOM kill — dependency not the cause"},
		},
		HardRejectionEvidenceTypes: []string{string(ev.TypeK8sOOMKill)},
		MinSupportingEvidence:      1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Check upstream service health", Steps: []string{"kubectl get pods -n <dependency-namespace>", "kubectl logs <dependency-pod> --tail=50"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// ─── ImagePullBackOff Hypotheses ──────────────────────────────────────────────

// HT-K8S-010: ImagePull — Bad Tag
func htK8S010() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-010",
		Type:        "image_pull_bad_tag",
		Name:        "ImagePullBackOff: Image Tag Does Not Exist",
		Description: "The container image tag specified in the deployment does not exist in the registry.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"ImagePullBackOff", "ErrImagePull"},
		ApplicableDomains:        []string{"kubernetes"},
		BasePrior:                0.40,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sImagePullError), Weight: 0.92, Required: true, Description: "K8s image pull error event"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sImagePullError), Weight: 0.92, Description: "Image not found / tag does not exist"},
			{EvidenceType: string(ev.TypeChangeDeployment), Weight: 0.80, EvidenceGroup: "deployment_signals", Description: "Recent deployment with new image"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Verify image tag exists in registry", Steps: []string{"Check Artifactory: docker images list | grep <image>:<tag>", "Correct the image tag in deployment manifest"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// HT-K8S-011: ImagePull — Auth Failure
func htK8S011() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-011",
		Type:        "image_pull_auth_failure",
		Name:        "ImagePullBackOff: Registry Authentication Failure",
		Description: "The node cannot authenticate to the container registry (expired credentials, missing imagePullSecret).",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"ImagePullBackOff", "ErrImagePull"},
		ApplicableDomains:        []string{"kubernetes"},
		BasePrior:                0.25,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sImagePullError), Weight: 0.88, Description: "Unauthorized/403 in image pull event"},
			{EvidenceType: string(ev.TypeK8sPodEventFailed), Weight: 0.80, Description: "Secret not found event"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Verify imagePullSecret", Steps: []string{"kubectl get secret <pull-secret> -n <ns>", "kubectl describe serviceaccount default -n <ns>"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// ─── Storage Hypotheses ───────────────────────────────────────────────────────

// HT-K8S-012: PVC Full
func htK8S012() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-012",
		Type:        "pvc_storage_full",
		Name:        "PVC/Disk Full",
		Description: "The NetApp volume backing the PVC is at or near capacity, causing I/O errors.",
		ApplicableEntityTypes:    []string{"k8s_pvc", "k8s_pod", "storage_volume"},
		ApplicableFailureClasses: []string{"PVCFull", "DiskFull", "StoragePressure"},
		ApplicableDomains:        []string{"kubernetes", "storage"},
		BasePrior:                0.50,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppVolumeState), Weight: 0.88, Required: true, Description: "NetApp volume utilization"},
			{EvidenceType: string(ev.TypeNetAppVolumeFull), Weight: 0.92, Required: false, Description: "Volume >95% full"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppVolumeFull), Weight: 0.92, Description: "Volume >95% utilization"},
			{EvidenceType: string(ev.TypeK8sPodLog), Weight: 0.82, Description: "No space left on device in logs"},
			{EvidenceType: string(ev.TypeK8sPodEventStorage), Weight: 0.85, Description: "FailedMount or FailedAttachVolume K8s event"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Identify top space consumers", Steps: []string{"kubectl exec -it <pod> -- du -sh /data/*"}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Request volume expansion", Steps: []string{"Contact storage team with SVM and volume name", "Or resize PVC: kubectl edit pvc <name>"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 30},
		},
	}
}

// HT-K8S-013: PV Storage Offline
func htK8S013() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-013",
		Type:        "pv_storage_offline",
		Name:        "PV Backed Storage Offline",
		Description: "The NetApp volume backing the PV is offline or restricted, causing mount failures.",
		ApplicableEntityTypes:    []string{"k8s_pv", "k8s_pvc", "storage_volume"},
		ApplicableFailureClasses: []string{"PVFailure", "StorageOffline", "PVCPending"},
		ApplicableDomains:        []string{"kubernetes", "storage"},
		BasePrior:                0.20,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppVolumeState), Weight: 0.92, Required: true, Description: "NetApp volume state offline/restricted"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppVolumeState), Weight: 0.95, Description: "Volume offline or restricted"},
			{EvidenceType: string(ev.TypeK8sPodEventStorage), Weight: 0.85, Description: "FailedMount or FailedAttachVolume event"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Bring volume online", Steps: []string{"vsadmin: volume online -vserver <svm> -volume <vol>", "Verify NFS exports: nfs export show"}, SafetyLevel: "CAUTION", Urgency: "immediate", EstimatedMinutes: 10},
		},
	}
}

// ─── Infrastructure Hypotheses ────────────────────────────────────────────────

// HT-INF-001: Bare Metal Hardware Failure (standalone infra)
func htINF001() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-INF-001",
		Type:        "bare_metal_hardware_failure",
		Name:        "Physical Hardware Failure",
		Description: "A physical server has failed or has a hardware error reported by CloudStack.",
		ApplicableEntityTypes:    []string{"bare_metal", "virtual_machine"},
		ApplicableFailureClasses: []string{"HardwareFailure", "HostDown"},
		ApplicableDomains:        []string{"infrastructure"},
		BasePrior:                0.12,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.90, Required: true, Description: "CloudStack host state"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.92, Description: "Host in Error/Down state"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.88, Description: "Host is Up and healthy"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Escalate to hardware team", Steps: []string{"Page infrastructure on-call with host name and CloudStack cluster", "Check IPMI/iDRAC for hardware alerts"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// HT-INF-002: CloudStack Platform Issue
func htINF002() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-INF-002",
		Type:        "cloudstack_platform_issue",
		Name:        "CloudStack Platform or Zone Issue",
		Description: "Multiple hosts in the same CloudStack zone are affected, suggesting a platform-level failure.",
		ApplicableEntityTypes:    []string{"virtual_machine", "k8s_node"},
		ApplicableFailureClasses: []string{"NotReady", "HostDown"},
		ApplicableDomains:        []string{"infrastructure"},
		BasePrior:                0.10,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeCloudStackHostState), Weight: 0.88, Description: "Multiple hosts in zone affected"},
			{EvidenceType: string(ev.TypeSimilarIncident), Weight: 0.65, Description: "Pattern matches previous CloudStack zone issues"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Check CloudStack management server", Steps: []string{"Log into CloudStack UI and check zone health", "Page CloudStack platform team"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// HT-INF-003: Storage Cluster Failure
func htINF003() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-INF-003",
		Type:        "storage_cluster_failure",
		Name:        "NetApp/Storage Cluster Failure",
		Description: "Multiple volumes across the same SVM or cluster are offline, indicating a storage cluster issue.",
		ApplicableEntityTypes:    []string{"storage_volume", "storage_cluster"},
		ApplicableFailureClasses: []string{"StorageOffline", "StorageClusterDown"},
		ApplicableDomains:        []string{"storage", "infrastructure"},
		BasePrior:                0.08,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppVolumeState), Weight: 0.90, Description: "Multiple volumes offline from same SVM"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Check NetApp cluster health", Steps: []string{"vsadmin: cluster show", "vsadmin: storage failover show", "Page storage team"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// HT-INF-004: Network Failure
func htINF004() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-INF-004",
		Type:        "network_failure",
		Name:        "Network Failure or Partition",
		Description: "A network partition or failure is causing connectivity issues between components.",
		ApplicableEntityTypes:    []string{"k8s_node", "virtual_machine", "bare_metal"},
		ApplicableFailureClasses: []string{"NetworkUnavailable", "NotReady", "Unreachable"},
		ApplicableDomains:        []string{"infrastructure", "kubernetes"},
		BasePrior:                0.12,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sNodeEvent), Weight: 0.85, Description: "NetworkUnavailable event"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Test network connectivity", Steps: []string{"Ping affected node IPs from control plane", "Check CloudStack network configuration", "Contact network team if widespread"}, SafetyLevel: "SAFE", Urgency: "immediate"},
		},
	}
}

// ─── Change-Induced Hypotheses ────────────────────────────────────────────────

// HT-CHG-001: Deployment Regression
func htCHG001() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-CHG-001",
		Type:        "deployment_introduced_regression",
		Name:        "Deployment Introduced Application Regression",
		Description: "A recent deployment introduced a change that caused the observed failure.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload", "service"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff", "Degraded", "HighErrorRate"},
		ApplicableDomains:        []string{"application", "kubernetes"},
		BasePrior:                0.25,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeChangeDeployment), Weight: 0.85, Required: true, Description: "Recent deployment of affected service"},
			{EvidenceType: string(ev.TypeOKGCausalityScore), Weight: 0.80, Required: false, Description: "OKG causality score >= 0.60"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeChangeDeployment), Weight: 0.85, EvidenceGroup: "deployment_signals", Description: "Deployment within correlation window"},
			{EvidenceType: string(ev.TypeOKGCausalityScore), Weight: 0.85, EvidenceGroup: "deployment_signals", Description: "High OKG causality score"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Review deployment changes", Steps: []string{"Check git diff between previous and current version", "Review Artifactory build artifacts"}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Roll back deployment", Steps: []string{"kubectl rollout undo deployment/<name> -n <ns>"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 3, ConfirmationMessage: "This reverts to the previous deployment version."},
		},
	}
}

// HT-CHG-002: Config Change Regression
func htCHG002() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-CHG-002",
		Type:        "config_change_regression",
		Name:        "Configuration Change Caused Regression",
		Description: "A recent configuration change (ConfigMap, Secret, environment variable) caused the observed failure.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff", "Degraded"},
		ApplicableDomains:        []string{"kubernetes", "application"},
		BasePrior:                0.20,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeChangeConfig), Weight: 0.80, Required: true, Description: "Recent config change on affected entity"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeChangeConfig), Weight: 0.80, EvidenceGroup: "deployment_signals", Description: "Config change within correlation window"},
			{EvidenceType: string(ev.TypeOKGCausalityScore), Weight: 0.70, EvidenceGroup: "deployment_signals", Description: "OKG causality score for config change"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Review config changes", Steps: []string{"kubectl describe configmap <name> -n <ns>", "Check git history for configmap changes"}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Revert config change", Steps: []string{"kubectl apply -f <previous-configmap.yaml>", "kubectl rollout restart deployment/<name>"}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 5, ConfirmationMessage: "This reverts the configuration to the previous version."},
		},
	}
}

// ─── Production-Observed Patterns (from 3-day incident analysis) ─────────────

// HT-K8S-014: CrashLoop — Job/Batch Process Deployed as Deployment
// Root cause identified from INC-4333 (auto-secret-updater): pod exits cleanly
// (exit code 0) after completing work, K8s restarts it as a Deployment, generating
// hundreds of "CrashLoops". The application is healthy; the deployment model is wrong.
func htK8S014() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-014",
		Type:        "crashloop_job_as_deployment",
		Name:        "CrashLoopBackOff: Batch Job Deployed as Continuous Service",
		Description: "A batch or one-shot job has been deployed as a Deployment. The application completes its work and exits with code 0. Kubernetes interprets this as a crash and restarts it indefinitely. Fix: convert to CronJob or Job.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload"},
		ApplicableFailureClasses: []string{"CrashLoopBackOff"},
		ApplicableDomains:        []string{"kubernetes", "application"},
		BasePrior:                0.22,
		SupportingEvidence: []domain.EvidenceSpec{
			// Exit code 0 with high restart count is the primary signal
			{EvidenceType: string(ev.TypeK8sPodExitCodeZero), Weight: 0.85, EvidenceGroup: "k8s_pod_crash_signals", Description: "Container exits with code 0 (success) — not an application crash"},
			{EvidenceType: string(ev.TypeK8sPodEventCrashLoop), Weight: 0.55, EvidenceGroup: "k8s_pod_crash_signals", Description: "BackOff restarting a container that exits cleanly"},
			{EvidenceType: string(ev.TypeK8sPodLog), Weight: 0.70, Description: "Log shows task completion before exit (e.g. 'updated successfully', 'job done')"},
		},
		ContradictingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sPodExitCodeOOM), Weight: 0.80, Description: "OOM kill — not a clean exit design issue"},
			{EvidenceType: string(ev.TypeK8sPodExitCodeSegfault), Weight: 0.80, Description: "Segfault — application crash, not a design issue"},
		},
		HardRejectionEvidenceTypes: []string{string(ev.TypeK8sOOMKill)},
		MinSupportingEvidence:      1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Verify it is a batch job", Steps: []string{
				"kubectl logs <pod> -n <ns> (check if log shows successful completion before exit)",
				"Check if pod always exits with code 0",
			}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Convert Deployment to CronJob", Steps: []string{
				"Create a CronJob manifest with the same container spec",
				"Set schedule: '*/5 * * * *' or appropriate interval",
				"Delete the Deployment after CronJob is verified",
			}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 30},
		},
	}
}

// HT-K8S-015: Node Memory Pressure → Pod Eviction Cascade
// Root cause identified from mps-mondev-mdn-worker-z2-01 / mps-monprod-mdn obs-beyla:
// node memory exhaustion causes kubelet to evict low-priority pods, which triggers
// floods of "Not all pods ready" incidents for the evicted deployments.
func htK8S015() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-K8S-015",
		Type:        "node_memory_pressure_eviction",
		Name:        "Node Memory Pressure Causing Pod Evictions",
		Description: "A node has exhausted its memory limit. The kubelet evicts lower-priority pods to reclaim memory, causing multiple 'pod not ready' incidents for the evicted workloads. The root cause is the node, not the evicted applications.",
		ApplicableEntityTypes:    []string{"k8s_pod", "k8s_workload", "k8s_node"},
		ApplicableFailureClasses: []string{"Evicted", "PodNotReady", "OOMKilled"},
		ApplicableDomains:        []string{"kubernetes"},
		BasePrior:                0.20,
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeK8sPodEventEviction), Weight: 0.90, Description: "Pod eviction event — kubelet evicted pod due to resource pressure"},
			{EvidenceType: string(ev.TypeK8sOOMKill), Weight: 0.85, EvidenceGroup: "node_pressure_signals", Description: "OOM kill on node — confirms memory exhaustion"},
			{EvidenceType: string(ev.TypeK8sPodExitCodeOOM), Weight: 0.80, EvidenceGroup: "node_pressure_signals", Description: "Container OOM killed (exit 137)"},
			{EvidenceType: string(ev.TypeK8sResourcePressure), Weight: 0.88, Description: "Node has active MemoryPressure or DiskPressure condition"},
			{EvidenceType: string(ev.TypeK8sNodeCondition), Weight: 0.75, EvidenceGroup: "node_pressure_signals", Description: "Node MemoryPressure=True condition"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Identify top memory consumers on node", Steps: []string{
				"kubectl top pods -A --sort-by=memory | head -20",
				"kubectl describe node <node-name> (check Allocatable vs Allocated memory)",
			}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Cordon node and drain non-critical workloads", Steps: []string{
				"kubectl cordon <node-name>",
				"kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data",
			}, SafetyLevel: "CAUTION", Urgency: "high", EstimatedMinutes: 15, ConfirmationMessage: "This will move all pods off the node."},
		},
	}
}

// HT-INF-005: NetApp Aggregate Degraded (ROOT CAUSE for PVC/storage cascades)
// Root cause identified from DT CUSTOM_ALERT: "Aggregate state - MDN" for
// aggr1_node001..004 — degraded aggregates block volume operations, causing
// PVC alerts and pod scheduling failures across multiple namespaces.
func htINF005() *domain.HypothesisTemplate {
	return &domain.HypothesisTemplate{
		ID:          "HT-INF-005",
		Type:        "netapp_aggregate_degraded",
		Name:        "NetApp Aggregate Degraded or Restricted",
		Description: "A NetApp storage aggregate (RAID group) is in a degraded, restricted, or offline state. This blocks volume operations for all PVCs backed by this aggregate, causing FailedMount events and pod scheduling failures across multiple namespaces. This is the root cause for cascading PVC and pod-not-ready incidents.",
		ApplicableEntityTypes:    []string{"storage_volume", "k8s_pvc", "netapp_aggregate"},
		ApplicableFailureClasses: []string{"StorageOffline", "PVCPending", "FailedMount"},
		ApplicableDomains:        []string{"storage", "kubernetes"},
		BasePrior:                0.15,
		RequiredEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppAggregateState), Weight: 0.95, Required: true, Description: "NetApp aggregate in degraded/restricted/offline state"},
		},
		SupportingEvidence: []domain.EvidenceSpec{
			{EvidenceType: string(ev.TypeNetAppAggregateState), Weight: 0.95, Description: "Aggregate degraded/restricted — volumes inaccessible"},
			{EvidenceType: string(ev.TypeNetAppNodeState), Weight: 0.88, EvidenceGroup: "netapp_signals", Description: "Storage node degraded — caused aggregate failure"},
			{EvidenceType: string(ev.TypeNetAppSVMState), Weight: 0.80, EvidenceGroup: "netapp_signals", Description: "SVM restricted due to aggregate failure"},
			{EvidenceType: string(ev.TypeK8sPodEventStorage), Weight: 0.70, Description: "FailedMount events on pods using affected PVCs"},
		},
		MinSupportingEvidence: 1,
		DefaultActions: []domain.ActionSpec{
			{Title: "Identify affected aggregate and volumes", Steps: []string{
				"NetApp ONTAP: storage aggregate show -aggregate <name> -fields state,raid-status",
				"NetApp ONTAP: volume show -aggregate <name> -fields name,state,volume-style",
				"kubectl get pvc -A | grep -v Bound (find affected PVCs)",
			}, SafetyLevel: "SAFE", Urgency: "immediate"},
			{Title: "Escalate to storage team", Steps: []string{
				"Page on-call storage engineer with aggregate name and RAID status",
				"Check disk shelf status: storage disk show -broken",
				"If mirror degraded: storage aggregate mirror <aggr> (rebuild RAID)",
			}, SafetyLevel: "CAUTION", Urgency: "immediate", EstimatedMinutes: 60, ConfirmationMessage: "Storage aggregate repair requires storage team authorization."},
		},
	}
}
