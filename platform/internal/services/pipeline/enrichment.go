package pipeline

import (
	"strings"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

func enrichEntityLabels(alert *models.Alert) {
	if alert.Labels == nil {
		alert.Labels = make(map[string]string)
	}

	// Step 1: extract k8s label lines from description text when labels are sparse.
	// Only run if at least one k8s key is missing AND description looks like a DT payload.
	if alert.Description != "" && alert.Labels["k8s.cluster.uid"] == "" {
		for _, line := range strings.Split(alert.Description, "\n") {
			line = strings.TrimSpace(line)
			for _, kv := range []struct{ prefix, key string }{
				{"k8s.cluster.name:", "k8s.cluster.name"},
				{"k8s.cluster.uid:", "k8s.cluster.uid"},
				{"k8s.namespace.name:", "k8s.namespace.name"},
				{"k8s.node.name:", "k8s.node.name"},
				{"k8s.workload.name:", "k8s.workload.name"},
				{"k8s.workload.kind:", "k8s.workload.kind"},
			} {
				if strings.HasPrefix(line, kv.prefix) {
					val := strings.TrimSpace(strings.TrimPrefix(line, kv.prefix))
					if val != "" && alert.Labels[kv.key] == "" {
						alert.Labels[kv.key] = val
					}
				}
			}
		}
		// Derive shorthand aliases used elsewhere in the pipeline.
		if alert.Labels["k8s.cluster.name"] != "" && alert.Labels["cluster"] == "" {
			alert.Labels["cluster"] = alert.Labels["k8s.cluster.name"]
		}
		if alert.Labels["k8s.cluster.uid"] != "" && alert.Labels["cluster_uid"] == "" {
			alert.Labels["cluster_uid"] = alert.Labels["k8s.cluster.uid"]
		}
		if alert.Labels["k8s.workload.name"] != "" && alert.Labels["workload"] == "" {
			alert.Labels["workload"] = alert.Labels["k8s.workload.name"]
		}
		if alert.Labels["k8s.node.name"] != "" && alert.Labels["node"] == "" {
			alert.Labels["node"] = alert.Labels["k8s.node.name"]
		}
		if alert.Labels["k8s.namespace.name"] != "" && alert.Labels["namespace"] == "" {
			alert.Labels["namespace"] = alert.Labels["k8s.namespace.name"]
		}
	}

	// Step 2: infer entity_type from the most-specific entity label present.
	if alert.Labels["entity_type"] == "" {
		switch {
		case alert.Labels["k8s.node.name"] != "" || alert.Labels["node"] != "":
			alert.Labels["entity_type"] = "k8s_node"
		case alert.Labels["k8s.workload.name"] != "" || alert.Labels["workload"] != "":
			alert.Labels["entity_type"] = "k8s_workload"
		case alert.Labels["k8s.cluster.uid"] != "" || alert.Labels["k8s.cluster.name"] != "" || alert.Labels["cluster"] != "":
			// Only infer k8s_cluster when no more specific entity (node/workload) present.
			alert.Labels["entity_type"] = "k8s_cluster"
		case alert.Labels["host.name"] != "" || alert.Labels["host"] != "":
			alert.Labels["entity_type"] = "vm"
		// NetApp entity type: set by Dynatrace normalizer for DT sources, but Prometheus
		// netapp-exporter alerts may carry netapp_entity without entity_type.
		case alert.Labels["netapp_entity"] != "":
			// Use netapp_cluster label to distinguish aggregate/svm/cluster level.
			nc := strings.ToLower(alert.Labels["netapp_cluster"])
			switch {
			case strings.HasPrefix(nc, "netapp_cluster"):
				alert.Labels["entity_type"] = "netapp_cluster"
			case strings.HasPrefix(nc, "netapp_svm"):
				alert.Labels["entity_type"] = "netapp_svm"
			case strings.HasPrefix(nc, "netapp_aggregate"):
				alert.Labels["entity_type"] = "netapp_aggregate"
			default:
				alert.Labels["entity_type"] = "netapp_storage"
			}
		}
	}
}

// alertInfraLevel derives the InfraLevel of an alert from its entity type labels.
// Returns InfraLevelUnknown if the level cannot be determined.
func alertInfraLevel(alert *models.Alert) correlation.InfraLevel {
	if alert.Labels == nil {
		return correlation.InfraLevelUnknown
	}
	entityType := strings.ToLower(alert.Labels["entity_type"])
	if entityType == "" {
		entityType = strings.ToLower(alert.Labels["entityType"])
	}
	switch {
	case entityType == "host" || alert.Labels["dt.entity.host"] != "" || alert.Labels["host.name"] != "":
		if alert.Labels["dt.entity.kubernetes_node"] != "" {
			return correlation.InfraLevelNode
		}
		return correlation.InfraLevelBM
	case entityType == "kubernetes_node" || alert.Labels["dt.entity.kubernetes_node"] != "" || alert.Labels["node"] != "":
		return correlation.InfraLevelNode
	case entityType == "kubernetes_cluster" || alert.Labels["dt.entity.kubernetes_cluster"] != "":
		return correlation.InfraLevelKVM
	case entityType == "cloud_application" || alert.Labels["dt.entity.cloud_application"] != "" ||
		alert.Labels["k8s.workload.name"] != "" || alert.Labels["workload"] != "":
		return correlation.InfraLevelPod
	// NetApp storage entities: cluster-wide storage failures rank at KVM level (cascade to all PVCs/pods).
	case entityType == "netapp_cluster" || entityType == "netapp_controller" || entityType == "storage_controller":
		return correlation.InfraLevelKVM
	// Per-SVM / per-aggregate NetApp failures: rank at VM level.
	case entityType == "netapp_svm" || entityType == "netapp_aggregate" || entityType == "netapp_node":
		return correlation.InfraLevelVM
	// Other netapp_ types (volume, lun, disk): rank at Node level.
	case strings.HasPrefix(entityType, "netapp_"):
		return correlation.InfraLevelNode
	}
	return correlation.InfraLevelUnknown
}

func severityToPriority(severity string) string {
	switch severity {
	case "critical":
		return "critical"
	case "high":
		return "high"
	default:
		return "medium"
	}
}

// entityTypeFamily returns the broad infrastructure family of an alert used to isolate
// merge decisions: k8s alerts must not be grouped with vm/host alerts, and neither
// should land in netapp incidents.  Returns "" when the family cannot be determined.
func entityTypeFamily(alert *models.Alert) string {
	if alert.Labels == nil {
		return ""
	}
	et := strings.ToLower(alert.Labels["entity_type"])
	switch {
	case et == "k8s_node" || et == "k8s_workload" || et == "k8s_cluster" ||
		et == "cloud_application" || et == "kubernetes_node" || et == "kubernetes_cluster":
		return "k8s"
	case et == "vm" || et == "host":
		return "vm"
	case strings.HasPrefix(et, "netapp_"):
		return "netapp"
	}
	return ""
}

// triggerRCA fires a POST to the RCA orchestrator and updates the incident with the investigation ID.
// rcaCtx is optional — when non-nil it carries pre-computed Go engine results that the V2
// orchestrator uses as a deterministic foundation instead of re-deriving them from scratch.

// isKnownTestWorkload returns true for known test/debug/synthetic workloads that
// permanently fire alerts but should not generate production incidents.
// These are added based on operational observation — a workload intentionally named
// "liveness-fail" or a manually-created "tmp-debug" pod is noise, not signal.
func isKnownTestWorkload(alert *models.Alert) bool {
	if alert.Labels == nil {
		return false
	}
	workload := coalesceStr(
		alert.Labels["workload"],
		alert.Labels["k8s.workload.name"],
		alert.Labels["service"],
	)
	namespace := coalesceStr(
		alert.Labels["namespace"],
		alert.Labels["k8s.namespace.name"],
	)

	// Known test fixtures by name pattern
	testWorkloadPatterns := []string{
		"liveness-fail",  // intentionally failing liveness probe test deployment
		"rca-claude-cli", // LLM/RCA experimentation tool
		"tmp-debug",      // ephemeral debug pod
		"bm-test",        // bare-metal test pod
		"vm-test",        // VM test pod
	}
	for _, pattern := range testWorkloadPatterns {
		if strings.Contains(strings.ToLower(workload), pattern) {
			return true
		}
		// Also match by alert title
		if strings.Contains(strings.ToLower(alert.Title), pattern) {
			return true
		}
	}

	// Suppress debug pods in default namespace (they are almost always manual ephemeral pods)
	if namespace == "default" && strings.HasPrefix(strings.ToLower(workload), "tmp-") {
		return true
	}

	return false
}

func coalesceStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
