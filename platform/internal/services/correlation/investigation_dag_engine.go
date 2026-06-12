package correlation

import (
	"fmt"
	"time"
)

// InvestigationStepType classifies what kind of action a DAG step represents.
type InvestigationStepType string

const (
	StepTypeQuery    InvestigationStepType = "query"    // fetch data from a system
	StepTypeCheck    InvestigationStepType = "check"    // assert a condition
	StepTypeCommand  InvestigationStepType = "command"  // run a CLI / kubectl command
	StepTypeEscalate InvestigationStepType = "escalate" // page a team or open a war-room
)

// InvestigationStep is one node in the investigation DAG.
type InvestigationStep struct {
	ID          string
	Title       string
	Description string
	Type        InvestigationStepType
	// Command holds the suggested shell / kubectl invocation, if any.
	Command     string
	// DependsOn lists IDs that must complete before this step runs.
	DependsOn   []string
	// ExpectedOutcome describes what a healthy result looks like.
	ExpectedOutcome string
	// EscalationTarget is the team / rotation to page if this step uncovers the issue.
	EscalationTarget string
}

// InvestigationDAG is the directed acyclic graph of steps for one incident.
type InvestigationDAG struct {
	IncidentID string
	Domain     FailureDomain
	RootEntity string
	Steps      []*InvestigationStep
	GeneratedAt time.Time
}

// InvestigationDAGEngine generates domain-specific investigation playbooks.
type InvestigationDAGEngine struct{}

func NewInvestigationDAGEngine() *InvestigationDAGEngine {
	return &InvestigationDAGEngine{}
}

// GenerateDAG returns an ordered investigation playbook for the given domain.
// rootEntity is the CMDB/topology label of the identified root-cause node.
func (e *InvestigationDAGEngine) GenerateDAG(
	domain FailureDomain,
	rootEntity string,
	incidentID string,
) *InvestigationDAG {
	dag := &InvestigationDAG{
		IncidentID:  incidentID,
		Domain:      domain,
		RootEntity:  rootEntity,
		GeneratedAt: time.Now(),
	}

	switch domain {
	case DomainStorage:
		dag.Steps = storageDAG(rootEntity)
	case DomainCompute:
		dag.Steps = computeDAG(rootEntity)
	case DomainNetwork:
		dag.Steps = networkDAG(rootEntity)
	case DomainKubernetes:
		dag.Steps = kubernetesDAG(rootEntity)
	case DomainDatabase:
		dag.Steps = databaseDAG(rootEntity)
	case DomainApplication:
		dag.Steps = applicationDAG(rootEntity)
	default:
		dag.Steps = genericDAG(rootEntity)
	}

	return dag
}

// Storage DAG 

func storageDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "s1", Title: "Check IOPS saturation",
			Description: "Verify whether the affected volume / aggregate has hit its IOPS ceiling.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("netapp-cli perf top -object volume -counter iops_write,iops_read -node %s", entity),
			ExpectedOutcome:  "IOPS well below provisioned limit",
			DependsOn: nil,
		},
		{
			ID: "s2", Title: "Check volume free space",
			Description: "Confirm the volume has > 10 % free space; NetApp thin-provisioned volumes can block writes at 0 %.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("netapp-cli volume show -vserver * -volume %s -fields size,available,percent-used", entity),
			ExpectedOutcome: "percent-used < 90",
			DependsOn: []string{"s1"},
		},
		{
			ID: "s3", Title: "Check PVC bound status",
			Description: "Confirm all PersistentVolumeClaims backed by this volume are in Bound phase.",
			Type:    StepTypeCommand,
			Command: "kubectl get pvc --all-namespaces -o wide | grep Pending",
			ExpectedOutcome: "No Pending PVCs",
			DependsOn: []string{"s2"},
		},
		{
			ID: "s4", Title: "Check volume mount errors in pod logs",
			Description: "Search for mount timeout or I/O error messages in pods that consume this volume.",
			Type:    StepTypeCommand,
			Command: "kubectl get events --all-namespaces --field-selector reason=FailedMount --sort-by='.lastTimestamp' | tail -30",
			ExpectedOutcome: "No recent FailedMount events",
			DependsOn: []string{"s3"},
		},
		{
			ID: "s5", Title: "Escalate to Storage team",
			Description: "If IOPS or space issue confirmed, page the storage on-call rotation.",
			Type:             StepTypeEscalate,
			EscalationTarget: "storage-oncall",
			DependsOn:        []string{"s1", "s2"},
		},
	}
}

// Compute DAG 

func computeDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "c1", Title: "Check node CPU / memory pressure",
			Description: "Inspect node resource utilisation and conditions.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl describe node %s | grep -A5 Conditions", entity),
			ExpectedOutcome: "No MemoryPressure or DiskPressure conditions",
			DependsOn: nil,
		},
		{
			ID: "c2", Title: "Check top resource consumers on node",
			Description: "Identify pods monopolising CPU or memory.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl top pods --all-namespaces --sort-by=cpu | grep %s || kubectl top pods --all-namespaces --sort-by=cpu | head -20", entity),
			ExpectedOutcome: "No single pod above 80 % of node allocatable",
			DependsOn: []string{"c1"},
		},
		{
			ID: "c3", Title: "Check pod eviction events",
			Description: "Determine whether the node has recently evicted pods due to resource pressure.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl get events --all-namespaces --field-selector reason=Evicted,involvedObject.name=%s --sort-by='.lastTimestamp' | tail -20", entity),
			ExpectedOutcome: "No recent evictions",
			DependsOn: []string{"c1"},
		},
		{
			ID: "c4", Title: "Check container restart counts",
			Description: "Find crash-looping containers on the affected node.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl get pods --all-namespaces --field-selector spec.nodeName=%s -o wide | awk '$5 > 2'", entity),
			ExpectedOutcome: "No containers with > 2 restarts",
			DependsOn: []string{"c2", "c3"},
		},
		{
			ID: "c5", Title: "Cordon node if actively degraded",
			Description: "If node conditions are unhealthy, cordon to prevent new scheduling while investigating.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl cordon %s", entity),
			ExpectedOutcome: "Node marked Unschedulable",
			DependsOn: []string{"c1"},
		},
		{
			ID: "c6", Title: "Escalate to Platform / Infra team",
			Description: "Page the platform on-call if node is degraded and cannot self-heal.",
			Type:             StepTypeEscalate,
			EscalationTarget: "platform-oncall",
			DependsOn:        []string{"c4", "c5"},
		},
	}
}

// Network DAG 

func networkDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "n1", Title: "Check DNS resolution",
			Description: "Verify cluster DNS can resolve the service name associated with the failing entity.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl run dns-test --image=busybox --restart=Never --rm -it -- nslookup %s", entity),
			ExpectedOutcome: "DNS resolves to one or more pod IPs",
			DependsOn: nil,
		},
		{
			ID: "n2", Title: "Check service endpoints",
			Description: "Confirm the Kubernetes Service has ready endpoints backing it.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl get endpoints %s --all-namespaces", entity),
			ExpectedOutcome: "At least one ready endpoint",
			DependsOn: []string{"n1"},
		},
		{
			ID: "n3", Title: "Check ingress / load-balancer status",
			Description: "Verify the ingress controller has published an external address.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl get ingress --all-namespaces | grep %s", entity),
			ExpectedOutcome: "ADDRESS column is non-empty",
			DependsOn: []string{"n2"},
		},
		{
			ID: "n4", Title: "Check NetworkPolicy restrictions",
			Description: "Identify any NetworkPolicy that might block traffic to/from the entity.",
			Type:    StepTypeCommand,
			Command: "kubectl get networkpolicy --all-namespaces -o yaml | grep -A20 podSelector",
			ExpectedOutcome: "No overly-restrictive deny-all policy for the affected namespace",
			DependsOn: []string{"n2"},
		},
		{
			ID: "n5", Title: "Check node firewall / security-group rules",
			Description: "Confirm cloud security groups allow the required traffic on affected VMs.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("cloud-cli security-group list --instance %s", entity),
			ExpectedOutcome: "Required ports open in both inbound and outbound directions",
			DependsOn: []string{"n3", "n4"},
		},
		{
			ID: "n6", Title: "Escalate to Network team",
			Description: "Page the network on-call if DNS, endpoints, or firewall issues persist.",
			Type:             StepTypeEscalate,
			EscalationTarget: "network-oncall",
			DependsOn:        []string{"n5"},
		},
	}
}

// Kubernetes DAG 

func kubernetesDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "k1", Title: "Check control-plane component health",
			Description: "Ensure kube-apiserver, kube-scheduler, and kube-controller-manager are responding.",
			Type:    StepTypeCommand,
			Command: "kubectl get componentstatus",
			ExpectedOutcome: "All components Healthy",
			DependsOn: nil,
		},
		{
			ID: "k2", Title: "Check etcd cluster health",
			Description: "Verify etcd quorum is intact and latency is within bounds.",
			Type:    StepTypeCommand,
			Command: "etcdctl endpoint health --cluster",
			ExpectedOutcome: "All endpoints report healthy, latency < 100 ms",
			DependsOn: []string{"k1"},
		},
		{
			ID: "k3", Title: "Check scheduler pending pods",
			Description: "Identify pods stuck in Pending state and the scheduling failure reason.",
			Type:    StepTypeCommand,
			Command: "kubectl get pods --all-namespaces --field-selector=status.phase=Pending -o wide | head -30",
			ExpectedOutcome: "No pods pending for > 5 minutes",
			DependsOn: []string{"k1"},
		},
		{
			ID: "k4", Title: "Check workload health",
			Description: "Inspect Deployment / StatefulSet rollout status for the affected workload.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl rollout status deployment %s --timeout=60s 2>/dev/null || kubectl rollout status statefulset %s --timeout=60s", entity, entity),
			ExpectedOutcome: "Rollout complete, all replicas available",
			DependsOn: []string{"k3"},
		},
		{
			ID: "k5", Title: "Check recent cluster events",
			Description: "Review Warning-level events cluster-wide for the last 10 minutes.",
			Type:    StepTypeCommand,
			Command: "kubectl get events --all-namespaces --field-selector type=Warning --sort-by='.lastTimestamp' | tail -40",
			ExpectedOutcome: "No recurring node or volume errors",
			DependsOn: []string{"k2", "k3"},
		},
		{
			ID: "k6", Title: "Escalate to Platform SRE",
			Description: "Page the platform SRE on-call if control-plane or etcd issues are confirmed.",
			Type:             StepTypeEscalate,
			EscalationTarget: "platform-sre-oncall",
			DependsOn:        []string{"k2", "k4"},
		},
	}
}

// Database DAG 

func databaseDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "d1", Title: "Check replication lag",
			Description: "Confirm primaryreplica lag is below the alerting threshold.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("psql -h %s -c 'SELECT now() - pg_last_xact_replay_timestamp() AS replication_lag;'", entity),
			ExpectedOutcome: "Lag < 30 seconds",
			DependsOn: nil,
		},
		{
			ID: "d2", Title: "Check active connections",
			Description: "Verify connection pool is not exhausted.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("psql -h %s -c 'SELECT count(*), state FROM pg_stat_activity GROUP BY state;'", entity),
			ExpectedOutcome: "Active connections < 80 % of max_connections",
			DependsOn: nil,
		},
		{
			ID: "d3", Title: "Check long-running queries",
			Description: "Identify queries running for > 5 minutes that may cause lock waits.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("psql -h %s -c \"SELECT pid, now()-query_start AS duration, query FROM pg_stat_activity WHERE state='active' AND now()-query_start > interval '5 min' ORDER BY duration DESC;\"", entity),
			ExpectedOutcome: "No queries older than 5 minutes",
			DependsOn: []string{"d2"},
		},
		{
			ID: "d4", Title: "Check table bloat / vacuum status",
			Description: "Confirm autovacuum is keeping up with dead tuple accumulation.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("psql -h %s -c 'SELECT schemaname, relname, n_dead_tup, last_autovacuum FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10;'", entity),
			ExpectedOutcome: "No table with n_dead_tup > 1 million and no recent autovacuum",
			DependsOn: []string{"d3"},
		},
		{
			ID: "d5", Title: "Escalate to DBA team",
			Description: "Page the DBA on-call if replication lag, connection exhaustion, or bloat is confirmed.",
			Type:             StepTypeEscalate,
			EscalationTarget: "dba-oncall",
			DependsOn:        []string{"d1", "d3", "d4"},
		},
	}
}

// Application DAG 

func applicationDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "a1", Title: "Check application error rate",
			Description: "Pull the 5xx error rate from the APM / metrics backend for the last 15 minutes.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("promtool query instant 'rate(http_requests_total{status=~\"5..\",service=\"%s\"}[5m])'", entity),
			ExpectedOutcome: "Error rate < 1 %",
			DependsOn: nil,
		},
		{
			ID: "a2", Title: "Check pod readiness",
			Description: "Confirm the service's pods report Ready and are receiving traffic.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl get pods -l app=%s --all-namespaces -o wide", entity),
			ExpectedOutcome: "All pods Running and Ready",
			DependsOn: []string{"a1"},
		},
		{
			ID: "a3", Title: "Tail recent application logs",
			Description: "Look for exceptions, OOM kills, or dependency failures in the last 100 lines.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl logs -l app=%s --all-namespaces --tail=100 --since=15m 2>/dev/null | grep -i 'error\\|panic\\|fatal\\|oom' | tail -30", entity),
			ExpectedOutcome: "No fatal errors or panics",
			DependsOn: []string{"a2"},
		},
		{
			ID: "a4", Title: "Check downstream dependency health",
			Description: "Verify that databases, caches, and external APIs the service depends on are reachable.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("kubectl exec -it deploy/%s -- /bin/sh -c 'curl -s http://localhost:8080/healthz | jq .'", entity),
			ExpectedOutcome: "Health endpoint returns 200 with all checks passing",
			DependsOn: []string{"a3"},
		},
		{
			ID: "a5", Title: "Escalate to Application team",
			Description: "Page the application on-call if error rate is elevated and root cause is in app code or config.",
			Type:             StepTypeEscalate,
			EscalationTarget: "app-oncall",
			DependsOn:        []string{"a3", "a4"},
		},
	}
}

// Generic fallback DAG 

func genericDAG(entity string) []*InvestigationStep {
	return []*InvestigationStep{
		{
			ID: "g1", Title: "Identify affected entity",
			Description: "Confirm the entity involved and its current operational status.",
			Type:    StepTypeQuery,
			Command: fmt.Sprintf("kubectl get all --all-namespaces | grep %s", entity),
			ExpectedOutcome: "Entity found and status clear",
			DependsOn: nil,
		},
		{
			ID: "g2", Title: "Check recent events for entity",
			Description: "Review Kubernetes events and system logs for the entity.",
			Type:    StepTypeCommand,
			Command: fmt.Sprintf("kubectl get events --all-namespaces --field-selector involvedObject.name=%s --sort-by='.lastTimestamp' | tail -20", entity),
			ExpectedOutcome: "No Warning events in the last 5 minutes",
			DependsOn: []string{"g1"},
		},
		{
			ID: "g3", Title: "Escalate to on-call",
			Description: "If the entity type is unknown or the issue is not resolved, page the general on-call.",
			Type:             StepTypeEscalate,
			EscalationTarget: "general-oncall",
			DependsOn:        []string{"g2"},
		},
	}
}
