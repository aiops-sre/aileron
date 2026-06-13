package pipeline

import (
	"regexp"
	"strings"
)

// Finding is a structured alert failure extracted by the AlertAnalyzer (K8sGPT pattern).
// Feeding structured findings to the LLM instead of raw alert text dramatically improves
// RCA accuracy — the model receives error_class, entity_type, and parent_object rather
// than free-form DT or Prometheus description strings.
type Finding struct {
	EntityType   string // "pod", "node", "deployment", "service", "pvc", etc.
	Namespace    string
	ResourceName string
	ErrorClass   string // "CrashLoopBackOff", "OOMKilled", "ImagePullBackOff", etc.
	ErrorCode    string // waiting.reason code or DT problem type
	ParentObject string // "Deployment/nginx" if Pod→RS→Deployment chain resolved
	Description  string // 1-line summary stripped of internal noise (≤280 chars)
	Source       string // "dynatrace", "prometheus", "kubernetes", "netapp"
}

// podWaitingReasons is the full K8sGPT taxonomy of pod container waiting reasons.
var podWaitingReasons = map[string]string{
	"crashloopbackoff":           "CrashLoopBackOff",
	"imagepullbackoff":           "ImagePullBackOff",
	"errimagepull":               "ErrImagePull",
	"oomkilled":                  "OOMKilled",
	"createcontainerconfigerror": "CreateContainerConfigError",
	"createcontainererror":       "CreateContainerError",
	"invalidimagename":           "InvalidImageName",
	"unschedulable":              "Unschedulable",
	"evicted":                    "Evicted",
	"failedmount":                "FailedMount",
	"failedattachvolume":         "FailedAttachVolume",
	"errnetwork":                 "NetworkNotReady",
	"containercreating":          "ContainerCreating",
	"podnotscheduled":            "PodNotScheduled",
}

// dtProblemTypes maps Dynatrace problem type strings to structured error classes.
var dtProblemTypes = map[string]string{
	"error_rate_increase":             "ErrorRateIncrease",
	"response_time_degradation":       "LatencyDegradation",
	"availability":                    "Availability",
	"resource_contention":             "ResourceContention",
	"custom_app_performance_loss":     "AppPerformanceLoss",
	"custom_availability":             "CustomAvailability",
	"application_unexpected_high_load": "HighLoad",
	"process_crashed":                 "ProcessCrash",
	"process_na":                      "ProcessNotAvailable",
	"host_of_service_unavailable":     "HostUnavailable",
}

// internalNoise strips Dynatrace/internal infrastructure strings from descriptions
// before they reach the LLM — prevents the model from hallucinating on internal IPs.
var internalNoise = regexp.MustCompile(
	`(?i)(neo4j|bolt://|\.cluster\.local|\.svc\.|\.internal\.example.com|10\.\d+\.\d+\.\d+:\d+|bolt\+s?://[^\s]+)`,
)

// Analyze extracts structured findings from a raw alert.
// Returns at least one primary Finding; may return additional secondary findings
// (e.g., a parent Deployment when the primary entity is a Pod).
func Analyze(title, description, source string, labels map[string]string) []Finding {
	entityType := resolveEntityType(title, description, labels)
	namespace := extractNamespace(labels)
	resourceName := extractResourceName(labels, title)
	errorClass, errorCode := classifyError(title, description, source, labels)

	primary := Finding{
		EntityType:   entityType,
		Namespace:    namespace,
		ResourceName: resourceName,
		ErrorClass:   errorClass,
		ErrorCode:    errorCode,
		Source:       source,
		Description:  cleanDescription(title, description),
	}

	// Walk parent chain: pod → deployment (K8sGPT OwnerReference pattern).
	if entityType == "pod" {
		if depName, ok := labels["deployment"]; ok && depName != "" {
			primary.ParentObject = "Deployment/" + depName
		} else if rsName, ok := labels["replicaset"]; ok && rsName != "" {
			primary.ParentObject = "ReplicaSet/" + rsName
		}
	}

	return []Finding{primary}
}

func resolveEntityType(title, description string, labels map[string]string) string {
	if v, ok := labels["entity_type"]; ok {
		return strings.ToLower(v)
	}
	combined := strings.ToLower(title + " " + description)
	switch {
	case containsAny(combined, "pod", "container", "crashloop", "imagepull", "oomkill", "crashloopbackoff"):
		return "pod"
	case containsAny(combined, "deployment", "rollout"):
		return "deployment"
	case containsAny(combined, "replicaset"):
		return "replicaset"
	case containsAny(combined, "statefulset"):
		return "statefulset"
	case containsAny(combined, "node"):
		return "node"
	case containsAny(combined, "service", "endpoint", "svc"):
		return "service"
	case containsAny(combined, "pvc", "persistentvolumeclaim", "volume full", "disk pressure"):
		return "pvc"
	case containsAny(combined, "certificate", "cert expir", "tls", "x509"):
		return "certificate"
	case containsAny(combined, "ingress"):
		return "ingress"
	case containsAny(combined, "hpa", "horizontalpodautoscal"):
		return "hpa"
	case containsAny(combined, "database", "postgres", "mysql", "redis"):
		return "database"
	case containsAny(combined, "virtual machine", " vm "):
		return "virtual_machine"
	case containsAny(combined, "bare metal", "baremetal", "host down"):
		return "bare_metal"
	case containsAny(combined, "netapp", "ontap", "aggregate", "svm"):
		return "storage_volume"
	default:
		return "unknown"
	}
}

func classifyError(title, description, source string, labels map[string]string) (errorClass, errorCode string) {
	combined := strings.ToLower(title + " " + description)

	// Pod waiting-reason codes (K8sGPT taxonomy — 14 distinct codes).
	for code, class := range podWaitingReasons {
		if strings.Contains(combined, code) {
			return class, code
		}
	}

	// Dynatrace problem types.
	if source == "dynatrace" {
		for pattern, class := range dtProblemTypes {
			if strings.Contains(combined, pattern) {
				return class, pattern
			}
		}
	}

	// Generic semantic patterns.
	switch {
	case containsAny(combined, "not ready", "notready", "node not ready"):
		return "NotReady", "not_ready"
	case containsAny(combined, "high cpu", "cpu throttl", "cpu limit"):
		return "CPUThrottling", "cpu_limit"
	case containsAny(combined, "high memory", "memory limit", "memory pressure"):
		return "MemoryPressure", "memory_limit"
	case containsAny(combined, "disk full", "disk pressure", "pvc full", "volume full", "storage full"):
		return "StorageFull", "disk_pressure"
	case containsAny(combined, "connection refused", "connection timeout", "econnrefused"):
		return "ConnectivityFailure", "connection_refused"
	case containsAny(combined, "certificate expired", "cert expired", "tls expired", "x509"):
		return "CertificateExpired", "cert_expired"
	case containsAny(combined, "timeout", "timed out"):
		return "Timeout", "request_timeout"
	case containsAny(combined, "5xx", "internal server error", "http 5", "http/1.1 5"):
		return "ServerError", "5xx_errors"
	case containsAny(combined, "latency", "slow", "degraded", "response time"):
		return "LatencyDegradation", "high_latency"
	case containsAny(combined, "restart", "restarting", "restarts"):
		return "Restarting", "repeated_restarts"
	case containsAny(combined, "pdb", "poddisruptionbudget", "disruption allowed", "cannot disrupt"):
		return "PDBBlocking", "pdb_disallowed"
	case containsAny(combined, "webhook", "admission", "mutatingwebhook", "validatingwebhook"):
		return "WebhookFailure", "admission_webhook"
	default:
		return "Unknown", "unknown"
	}
}

func extractNamespace(labels map[string]string) string {
	for _, key := range []string{"namespace", "kubernetes_namespace", "k8s_namespace", "dt.entity.namespace"} {
		if v, ok := labels[key]; ok && v != "" {
			return v
		}
	}
	return ""
}

func extractResourceName(labels map[string]string, title string) string {
	for _, key := range []string{"pod", "pod_name", "resource_name", "service_name", "workload", "app", "deployment"} {
		if v, ok := labels[key]; ok && v != "" {
			return v
		}
	}
	return ""
}

func cleanDescription(title, description string) string {
	combined := title
	if description != "" && description != title {
		combined += " — " + description
	}
	clean := internalNoise.ReplaceAllString(combined, "[internal]")
	if len(clean) > 280 {
		clean = clean[:280]
	}
	return strings.TrimSpace(clean)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// FormatFindingsForLLM returns a structured representation suitable for injecting
// into an LLM prompt. Uses K8sGPT's "Error: {}\nDescription: {}" format.
// The 280-char limit prevents context window overflow from long DT narratives.
func FormatFindingsForLLM(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, f := range findings {
		sb.WriteString("Error: ")
		sb.WriteString(f.ErrorClass)
		if f.ErrorCode != "" && f.ErrorCode != strings.ToLower(f.ErrorClass) {
			sb.WriteString(" (")
			sb.WriteString(f.ErrorCode)
			sb.WriteString(")")
		}
		if f.ResourceName != "" {
			sb.WriteString(" on ")
			sb.WriteString(f.EntityType)
			sb.WriteString("/")
			sb.WriteString(f.ResourceName)
			if f.Namespace != "" {
				sb.WriteString(" in namespace ")
				sb.WriteString(f.Namespace)
			}
		}
		if f.ParentObject != "" {
			sb.WriteString(" (owned by ")
			sb.WriteString(f.ParentObject)
			sb.WriteString(")")
		}
		sb.WriteString("\n")
		sb.WriteString("Description: ")
		sb.WriteString(f.Description)
		sb.WriteString("\n")
	}
	return sb.String()
}
