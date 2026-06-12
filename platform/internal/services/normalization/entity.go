package normalization

import (
	"regexp"
	"strings"
)

// EntityInfo carries the infrastructure entity dimensions extracted from a payload.
type EntityInfo struct {
	Cluster     string
	Namespace   string
	Node        string
	Pod         string
	Service     string
	Host        string
	Workload    string
	EntityType  string // "k8s_pod" | "k8s_node" | "vm" | "service" | "application"
	EntityID    string // stable topology key
	EntityName  string // human-readable
}

// Dynatrace entity-ID prefixes that should NOT be treated as human-readable names.
var dynatraceEntityPrefixes = []string{
	"KUBERNETES_CLUSTER-", "KUBERNETES_NODE-", "KUBERNETES_SERVICE-",
	"KUBERNETES_WORKLOAD-",
	"HOST-", "PROCESS_GROUP-", "PROCESS_GROUP_INSTANCE-",
	"SERVICE-", "APPLICATION-", "CLOUD_APPLICATION-",
	"CLOUD_APPLICATION_NAMESPACE-", "CUSTOM_DEVICE-",
	"ENVIRONMENT-",
}

// IsDynatraceEntityID returns true when the value looks like a Dynatrace entityId.
func IsDynatraceEntityID(v string) bool {
	for _, pfx := range dynatraceEntityPrefixes {
		if strings.HasPrefix(v, pfx) {
			return true
		}
	}
	return false
}

var (
	reKVColon = regexp.MustCompile(`(?i)([\w.\-]+)\s*:\s*(\S+)`)
)

// ExtractFromLabels extracts entity dimensions from a flat label map.
// It understands both Prometheus-style labels and Dynatrace customProperties keys.
func ExtractFromLabels(labels map[string]string) EntityInfo {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := labels[k]; v != "" && !IsDynatraceEntityID(v) {
				return v
			}
		}
		return ""
	}

	e := EntityInfo{
		Cluster:   get("cluster", "k8s.cluster.name", "kubernetes_cluster", "k8s_cluster"),
		Namespace: get("namespace", "k8s.namespace.name", "kubernetes_namespace"),
		Node:      get("node", "k8s.node.name", "kubernetes_node", "nodename", "node_name"),
		Pod:       get("pod", "k8s.pod.name", "kubernetes_pod", "pod_name"),
		Service:   get("service", "service_name", "k8s.service.name"),
		Host:      get("host.name", "hostname", "host", "instance"),
		Workload:  get("workload", "k8s.workload.name", "deployment", "app"),
	}

	e.resolve()
	return e
}

// ExtractFromText uses heuristic line-by-line parsing to find k/v pairs embedded
// in description text.  Understands the Dynatrace "k8s.X.name: value" format.
func ExtractFromText(text string) EntityInfo {
	e := EntityInfo{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, kv := range []struct{ key, field string }{
			{"k8s.cluster.name", "Cluster"},
			{"k8s.namespace.name", "Namespace"},
			{"k8s.node.name", "Node"},
			{"k8s.workload.name", "Workload"},
			{"k8s.deployment.name", "Workload"},
			{"k8s.pod.name", "Pod"},
			{"host.name", "Host"},
		} {
			pfx := kv.key + ":"
			if strings.HasPrefix(line, pfx) {
				val := strings.TrimSpace(strings.TrimPrefix(line, pfx))
				if val == "" || IsDynatraceEntityID(val) {
					continue
				}
				switch kv.field {
				case "Cluster":
					if e.Cluster == "" {
						e.Cluster = val
					}
				case "Namespace":
					if e.Namespace == "" {
						e.Namespace = val
					}
				case "Node":
					if e.Node == "" {
						e.Node = val
					}
				case "Workload":
					if e.Workload == "" {
						e.Workload = val
					}
				case "Pod":
					if e.Pod == "" {
						e.Pod = val
					}
				case "Host":
					if e.Host == "" {
						e.Host = val
					}
				}
			}
		}
	}
	e.resolve()
	return e
}

// MergeEntityInfo merges src into dst, only filling blank fields.
func MergeEntityInfo(dst, src EntityInfo) EntityInfo {
	if dst.Cluster == "" {
		dst.Cluster = src.Cluster
	}
	if dst.Namespace == "" {
		dst.Namespace = src.Namespace
	}
	if dst.Node == "" {
		dst.Node = src.Node
	}
	if dst.Pod == "" {
		dst.Pod = src.Pod
	}
	if dst.Service == "" {
		dst.Service = src.Service
	}
	if dst.Host == "" {
		dst.Host = src.Host
	}
	if dst.Workload == "" {
		dst.Workload = src.Workload
	}
	if dst.EntityType == "" {
		dst.EntityType = src.EntityType
	}
	if dst.EntityID == "" {
		dst.EntityID = src.EntityID
	}
	if dst.EntityName == "" {
		dst.EntityName = src.EntityName
	}
	return dst
}

// resolve sets EntityType, EntityName and EntityID from the most specific entity present.
func (e *EntityInfo) resolve() {
	switch {
	case e.Pod != "":
		e.EntityType = "k8s_pod"
		e.EntityName = e.Pod
		e.EntityID = entityKey(e.Cluster, e.Namespace, "pod", e.Pod)
	case e.Node != "":
		e.EntityType = "k8s_node"
		e.EntityName = e.Node
		e.EntityID = entityKey(e.Cluster, "", "node", e.Node)
	case e.Host != "":
		e.EntityType = "vm"
		e.EntityName = e.Host
		e.EntityID = entityKey("", "", "host", e.Host)
	case e.Service != "":
		e.EntityType = "service"
		e.EntityName = e.Service
		e.EntityID = entityKey(e.Cluster, e.Namespace, "svc", e.Service)
	case e.Workload != "":
		e.EntityType = "k8s_workload"
		e.EntityName = e.Workload
		e.EntityID = entityKey(e.Cluster, e.Namespace, "deploy", e.Workload)
	}
}

func entityKey(cluster, namespace, kind, name string) string {
	parts := make([]string, 0, 4)
	if cluster != "" {
		parts = append(parts, cluster)
	}
	if namespace != "" {
		parts = append(parts, namespace)
	}
	parts = append(parts, kind, name)
	return strings.Join(parts, "/")
}
