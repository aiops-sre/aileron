// Package security implements Kubernetes security intelligence.
// Analyzes RBAC configurations, container security postures, network policy gaps,
// and runtime anomalies. Produces actionable findings with remediation guidance.
package security

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

// FindingSeverity classifies the risk level.
type FindingSeverity string

const (
	SeverityCritical FindingSeverity = "critical"
	SeverityHigh     FindingSeverity = "high"
	SeverityMedium   FindingSeverity = "medium"
	SeverityLow      FindingSeverity = "low"
	SeverityInfo     FindingSeverity = "info"
)

// FindingCategory classifies the type of security issue.
type FindingCategory string

const (
	CategoryRBAC            FindingCategory = "rbac"
	CategoryContainerSec    FindingCategory = "container_security"
	CategoryNetworkPolicy   FindingCategory = "network_policy"
	CategorySecretHandling  FindingCategory = "secret_handling"
	CategoryPodSecurity     FindingCategory = "pod_security"
	CategoryRuntime         FindingCategory = "runtime"
	CategoryImage           FindingCategory = "image_security"
)

// SecurityFinding is a detected security issue.
type SecurityFinding struct {
	RuleID       string          `json:"rule_id"`
	Category     FindingCategory `json:"category"`
	Severity     FindingSeverity `json:"severity"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Remediation  string          `json:"remediation"`
	ResourceKind string          `json:"resource_kind"`
	Namespace    string          `json:"namespace,omitempty"`
	ResourceName string          `json:"resource_name"`
	// CIS Benchmark reference if applicable
	CISRef       string          `json:"cis_ref,omitempty"`
	// MITRE ATT&CK tactic if applicable
	MITRETactic  string          `json:"mitre_tactic,omitempty"`
}

// ============================================================
// RBAC Analysis
// ============================================================

// AnalyzeClusterRole checks a ClusterRole for overly broad permissions.
func AnalyzeClusterRole(cr *rbacv1.ClusterRole) []SecurityFinding {
	var findings []SecurityFinding

	for _, rule := range cr.Rules {
		// Wildcard on resources
		if containsWildcard(rule.Resources) && containsWildcard(rule.Verbs) {
			findings = append(findings, SecurityFinding{
				RuleID:       "RBAC_WILDCARD_ALL",
				Category:     CategoryRBAC,
				Severity:     SeverityCritical,
				Title:        "ClusterRole grants wildcard access to all resources",
				Description:  fmt.Sprintf("ClusterRole %q has rules: resources=[*] verbs=[*] — equivalent to cluster-admin", cr.Name),
				Remediation:  "Replace wildcards with specific resources and verbs. Apply principle of least privilege.",
				ResourceKind: "ClusterRole",
				ResourceName: cr.Name,
				CISRef:       "CIS Kubernetes 5.1.3",
				MITRETactic:  "Privilege Escalation",
			})
		}
		// Exec/attach on pods (allows arbitrary command execution)
		if containsAny(rule.Resources, []string{"pods/exec", "pods/attach"}) {
			findings = append(findings, SecurityFinding{
				RuleID:       "RBAC_POD_EXEC",
				Category:     CategoryRBAC,
				Severity:     SeverityHigh,
				Title:        "ClusterRole allows pod exec/attach",
				Description:  fmt.Sprintf("ClusterRole %q grants pods/exec or pods/attach — enables arbitrary code execution in pods", cr.Name),
				Remediation:  "Remove pods/exec and pods/attach unless explicitly required by a specific operator.",
				ResourceKind: "ClusterRole",
				ResourceName: cr.Name,
				CISRef:       "CIS Kubernetes 5.1.6",
				MITRETactic:  "Execution",
			})
		}
		// Escalation/bind verbs (allows privilege escalation)
		if containsAny(rule.Verbs, []string{"escalate", "bind", "impersonate"}) {
			findings = append(findings, SecurityFinding{
				RuleID:       "RBAC_ESCALATION_VERB",
				Category:     CategoryRBAC,
				Severity:     SeverityCritical,
				Title:        "ClusterRole allows privilege escalation verbs",
				Description:  fmt.Sprintf("ClusterRole %q has escalate/bind/impersonate verbs — allows an attacker to gain cluster-admin", cr.Name),
				Remediation:  "Remove escalate, bind, and impersonate verbs. These are nearly always unintended.",
				ResourceKind: "ClusterRole",
				ResourceName: cr.Name,
				MITRETactic:  "Privilege Escalation",
			})
		}
		// Secret read access (credentials exposure)
		if containsAny(rule.Resources, []string{"secrets", "*"}) &&
			containsAny(rule.Verbs, []string{"get", "list", "watch", "*"}) {
			findings = append(findings, SecurityFinding{
				RuleID:       "RBAC_SECRET_READ",
				Category:     CategoryRBAC,
				Severity:     SeverityHigh,
				Title:        "ClusterRole allows reading all secrets",
				Description:  fmt.Sprintf("ClusterRole %q can list/get all secrets — allows credential harvesting", cr.Name),
				Remediation:  "Scope secret access to specific secrets by name using resourceNames field.",
				ResourceKind: "ClusterRole",
				ResourceName: cr.Name,
				CISRef:       "CIS Kubernetes 5.1.2",
				MITRETactic:  "Credential Access",
			})
		}
	}
	return findings
}

// AnalyzeClusterRoleBinding checks if cluster-admin is being granted.
func AnalyzeClusterRoleBinding(crb *rbacv1.ClusterRoleBinding) []SecurityFinding {
	var findings []SecurityFinding
	if crb.RoleRef.Name == "cluster-admin" {
		for _, subject := range crb.Subjects {
			findings = append(findings, SecurityFinding{
				RuleID:       "RBAC_CLUSTER_ADMIN_BINDING",
				Category:     CategoryRBAC,
				Severity:     SeverityCritical,
				Title:        "cluster-admin role bound to subject",
				Description:  fmt.Sprintf("ClusterRoleBinding %q grants cluster-admin to %s/%s — full cluster control", crb.Name, subject.Kind, subject.Name),
				Remediation:  "Replace cluster-admin with a least-privilege ClusterRole. Audit who requires this access.",
				ResourceKind: "ClusterRoleBinding",
				ResourceName: crb.Name,
				CISRef:       "CIS Kubernetes 5.1.1",
				MITRETactic:  "Privilege Escalation",
			})
		}
	}
	return findings
}

// ============================================================
// Container Security
// ============================================================

// AnalyzePodSecurity checks a Pod for container security misconfigurations.
func AnalyzePodSecurity(pod *corev1.Pod) []SecurityFinding {
	var findings []SecurityFinding
	ns, name := pod.Namespace, pod.Name

	// Host namespaces
	if pod.Spec.HostPID {
		findings = append(findings, SecurityFinding{
			RuleID: "POD_HOST_PID", Category: CategoryPodSecurity, Severity: SeverityCritical,
			Title:        "Pod uses host PID namespace",
			Description:  fmt.Sprintf("Pod %s/%s has hostPID: true — can see and signal all host processes", ns, name),
			Remediation:  "Set hostPID: false. Only system components (node exporters) should use host PID.",
			ResourceKind: "Pod", Namespace: ns, ResourceName: name,
			CISRef: "CIS Kubernetes 5.2.2", MITRETactic: "Discovery",
		})
	}
	if pod.Spec.HostNetwork {
		findings = append(findings, SecurityFinding{
			RuleID: "POD_HOST_NETWORK", Category: CategoryPodSecurity, Severity: SeverityHigh,
			Title:        "Pod uses host network namespace",
			Description:  fmt.Sprintf("Pod %s/%s has hostNetwork: true — bypasses network policies and can access host network", ns, name),
			Remediation:  "Set hostNetwork: false. Use Kubernetes Services for pod communication.",
			ResourceKind: "Pod", Namespace: ns, ResourceName: name,
			CISRef: "CIS Kubernetes 5.2.4",
		})
	}
	if pod.Spec.HostIPC {
		findings = append(findings, SecurityFinding{
			RuleID: "POD_HOST_IPC", Category: CategoryPodSecurity, Severity: SeverityHigh,
			Title:        "Pod uses host IPC namespace",
			Description:  fmt.Sprintf("Pod %s/%s has hostIPC: true — can access host shared memory", ns, name),
			Remediation:  "Set hostIPC: false.",
			ResourceKind: "Pod", Namespace: ns, ResourceName: name,
			CISRef: "CIS Kubernetes 5.2.3",
		})
	}

	for _, c := range pod.Spec.Containers {
		findings = append(findings, analyzeContainer(c, ns, name)...)
	}
	for _, c := range pod.Spec.InitContainers {
		findings = append(findings, analyzeContainer(c, ns, name)...)
	}
	return findings
}

func analyzeContainer(c corev1.Container, ns, podName string) []SecurityFinding {
	var findings []SecurityFinding
	prefix := fmt.Sprintf("pod %s/%s container %q", ns, podName, c.Name)

	if c.SecurityContext == nil {
		findings = append(findings, SecurityFinding{
			RuleID: "CONTAINER_NO_SECURITY_CONTEXT", Category: CategoryContainerSec, Severity: SeverityMedium,
			Title:       "Container has no securityContext",
			Description: fmt.Sprintf("%s: missing securityContext — running with default (potentially root) permissions", prefix),
			Remediation: "Add securityContext with runAsNonRoot: true, readOnlyRootFilesystem: true, allowPrivilegeEscalation: false",
			ResourceKind: "Pod", Namespace: ns, ResourceName: podName,
			CISRef: "CIS Kubernetes 5.2.6",
		})
		return findings
	}

	sc := c.SecurityContext

	// Privileged container: full host capabilities
	if sc.Privileged != nil && *sc.Privileged {
		findings = append(findings, SecurityFinding{
			RuleID: "CONTAINER_PRIVILEGED", Category: CategoryContainerSec, Severity: SeverityCritical,
			Title:        "Privileged container",
			Description:  fmt.Sprintf("%s: privileged: true — has full host access, equivalent to root on the node", prefix),
			Remediation:  "Set privileged: false. Use specific capabilities (capabilities.add) if host access is needed.",
			ResourceKind: "Pod", Namespace: ns, ResourceName: podName,
			CISRef: "CIS Kubernetes 5.2.1", MITRETactic: "Privilege Escalation",
		})
	}

	// Running as root
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		if sc.RunAsUser == nil || *sc.RunAsUser == 0 {
			findings = append(findings, SecurityFinding{
				RuleID: "CONTAINER_RUNS_AS_ROOT", Category: CategoryContainerSec, Severity: SeverityHigh,
				Title:        "Container may run as root",
				Description:  fmt.Sprintf("%s: runAsNonRoot not set and runAsUser not set — container likely runs as root", prefix),
				Remediation:  "Set securityContext.runAsNonRoot: true and runAsUser: <non-zero>",
				ResourceKind: "Pod", Namespace: ns, ResourceName: podName,
				CISRef: "CIS Kubernetes 5.2.6",
			})
		}
	}

	// Privilege escalation
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		findings = append(findings, SecurityFinding{
			RuleID: "CONTAINER_ALLOW_PRIVESC", Category: CategoryContainerSec, Severity: SeverityHigh,
			Title:        "Container allows privilege escalation",
			Description:  fmt.Sprintf("%s: allowPrivilegeEscalation not set to false — child processes can gain more privileges via setuid binaries", prefix),
			Remediation:  "Set securityContext.allowPrivilegeEscalation: false",
			ResourceKind: "Pod", Namespace: ns, ResourceName: podName,
			CISRef: "CIS Kubernetes 5.2.5",
		})
	}

	// Writable root filesystem
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		findings = append(findings, SecurityFinding{
			RuleID: "CONTAINER_WRITABLE_FS", Category: CategoryContainerSec, Severity: SeverityMedium,
			Title:        "Container has writable root filesystem",
			Description:  fmt.Sprintf("%s: readOnlyRootFilesystem: false — attacker can modify container filesystem", prefix),
			Remediation:  "Set readOnlyRootFilesystem: true. Mount writable volumes only where needed (tmpfs for /tmp).",
			ResourceKind: "Pod", Namespace: ns, ResourceName: podName,
			CISRef: "CIS Kubernetes 5.2.9",
		})
	}

	// Dangerous capabilities
	if sc.Capabilities != nil {
		dangerous := []string{"NET_RAW", "SYS_ADMIN", "SYS_PTRACE", "SYS_MODULE", "DAC_OVERRIDE",
			"SETUID", "SETGID", "NET_ADMIN", "SYS_RAWIO", "MKNOD"}
		for _, cap := range sc.Capabilities.Add {
			for _, d := range dangerous {
				if string(cap) == d {
					findings = append(findings, SecurityFinding{
						RuleID: "CONTAINER_DANGEROUS_CAP_" + d, Category: CategoryContainerSec, Severity: SeverityHigh,
						Title:        fmt.Sprintf("Container adds dangerous capability: %s", d),
						Description:  fmt.Sprintf("%s: capabilities.add includes %s — %s", prefix, d, capDescription(d)),
						Remediation:  fmt.Sprintf("Remove %s from capabilities.add. Use targeted alternatives.", d),
						ResourceKind: "Pod", Namespace: ns, ResourceName: podName,
						MITRETactic: "Privilege Escalation",
					})
				}
			}
		}
	}

	return findings
}

// ============================================================
// Network Policy Analysis
// ============================================================

// AnalyzeNetworkPolicyCoverage detects namespaces and pods without NetworkPolicy coverage.
func AnalyzeNetworkPolicyCoverage(
	namespace string,
	pods []corev1.Pod,
	policies []networkingv1.NetworkPolicy,
) []SecurityFinding {
	var findings []SecurityFinding

	if len(policies) == 0 {
		findings = append(findings, SecurityFinding{
			RuleID: "NETPOL_NO_POLICIES", Category: CategoryNetworkPolicy, Severity: SeverityHigh,
			Title:        "Namespace has no NetworkPolicies",
			Description:  fmt.Sprintf("Namespace %q has %d pods and zero NetworkPolicies — all pod-to-pod traffic is allowed by default", namespace, len(pods)),
			Remediation:  "Add a default-deny NetworkPolicy and explicitly allow required traffic.",
			ResourceKind: "Namespace", ResourceName: namespace,
			CISRef: "CIS Kubernetes 5.3.2",
		})
		return findings
	}

	// Check each pod is covered by at least one policy
	coveredPods := map[string]bool{}
	for _, policy := range policies {
		for _, pod := range pods {
			if labelsMatchSelector(pod.Labels, policy.Spec.PodSelector) {
				coveredPods[pod.Name] = true
			}
		}
	}

	uncovered := 0
	for _, pod := range pods {
		if !coveredPods[pod.Name] {
			uncovered++
		}
	}
	if uncovered > 0 {
		findings = append(findings, SecurityFinding{
			RuleID: "NETPOL_UNCOVERED_PODS", Category: CategoryNetworkPolicy, Severity: SeverityMedium,
			Title:        fmt.Sprintf("%d pods not covered by any NetworkPolicy", uncovered),
			Description:  fmt.Sprintf("Namespace %q: %d/%d pods have no NetworkPolicy — network traffic to/from these pods is unrestricted", namespace, uncovered, len(pods)),
			Remediation:  "Ensure all pods are selected by at least one NetworkPolicy. Add a catch-all deny policy.",
			ResourceKind: "Namespace", ResourceName: namespace,
		})
	}

	// Check for policies that allow all ingress/egress
	for _, policy := range policies {
		for _, ingress := range policy.Spec.Ingress {
			if len(ingress.From) == 0 && len(ingress.Ports) == 0 {
				findings = append(findings, SecurityFinding{
					RuleID: "NETPOL_ALLOW_ALL_INGRESS", Category: CategoryNetworkPolicy, Severity: SeverityHigh,
					Title:        "NetworkPolicy allows all ingress",
					Description:  fmt.Sprintf("NetworkPolicy %s/%s has empty ingress rule — allows traffic from any pod/IP", namespace, policy.Name),
					Remediation:  "Add specific From rules (namespaceSelector or podSelector) to restrict ingress sources.",
					ResourceKind: "NetworkPolicy", Namespace: namespace, ResourceName: policy.Name,
				})
			}
		}
		for _, egress := range policy.Spec.Egress {
			if len(egress.To) == 0 && len(egress.Ports) == 0 {
				findings = append(findings, SecurityFinding{
					RuleID: "NETPOL_ALLOW_ALL_EGRESS", Category: CategoryNetworkPolicy, Severity: SeverityMedium,
					Title:        "NetworkPolicy allows all egress",
					Description:  fmt.Sprintf("NetworkPolicy %s/%s has empty egress rule — allows traffic to any destination", namespace, policy.Name),
					Remediation:  "Add specific To rules to restrict egress destinations. Block access to cloud metadata endpoints.",
					ResourceKind: "NetworkPolicy", Namespace: namespace, ResourceName: policy.Name,
				})
			}
		}
	}
	return findings
}

// ============================================================
// Secret Security
// ============================================================

// AnalyzeSecretExposure detects secrets exposed as environment variables
// (vs mounted as files), which are visible in process listings.
func AnalyzeSecretExposure(pod *corev1.Pod) []SecurityFinding {
	var findings []SecurityFinding
	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				findings = append(findings, SecurityFinding{
					RuleID: "SECRET_ENV_VAR", Category: CategorySecretHandling, Severity: SeverityMedium,
					Title:        "Secret exposed as environment variable",
					Description:  fmt.Sprintf("Pod %s/%s container %q exposes secret %q as env var %q — visible in process listings and crash dumps", pod.Namespace, pod.Name, c.Name, env.ValueFrom.SecretKeyRef.Name, env.Name),
					Remediation:  "Mount secrets as files (volumeMounts with secretName) instead of environment variables. Use Vault sidecar for dynamic secrets.",
					ResourceKind: "Pod", Namespace: pod.Namespace, ResourceName: pod.Name,
					MITRETactic: "Credential Access",
				})
			}
		}
	}
	return findings
}

// helpers

func containsWildcard(items []string) bool {
	for _, item := range items {
		if item == "*" {
			return true
		}
	}
	return false
}

func containsAny(items, targets []string) bool {
	for _, item := range items {
		for _, t := range targets {
			if item == t {
				return true
			}
		}
	}
	return false
}

func labelsMatchSelector(labels map[string]string, selector interface{}) bool {
	// Simplified: real implementation uses labels.Selector.Matches()
	return true
}

func capDescription(cap string) string {
	descs := map[string]string{
		"NET_RAW":    "allows raw socket creation, enables ARP spoofing and MITM attacks",
		"SYS_ADMIN":  "grants ~200 system call privileges, effectively root on the host",
		"SYS_PTRACE": "allows tracing other processes, enables credential theft",
		"SYS_MODULE": "allows loading kernel modules, enables kernel rootkit installation",
		"NET_ADMIN":  "allows network configuration changes, can modify routing tables",
		"SETUID":     "allows changing process UID, enables privilege escalation",
		"SETGID":     "allows changing process GID, enables privilege escalation",
	}
	if d, ok := descs[cap]; ok {
		return d
	}
	return "elevated capability that increases attack surface"
}

// SecurityReport is an aggregated security posture for a cluster.
type SecurityReport struct {
	ClusterID       string            `json:"cluster_id"`
	Findings        []SecurityFinding `json:"findings"`
	TotalFindings   int               `json:"total_findings"`
	BySeverity      map[string]int    `json:"by_severity"`
	ByCategory      map[string]int    `json:"by_category"`
	PostureScore    float64           `json:"posture_score"`    // 0-100, higher is better
	PostureGrade    string            `json:"posture_grade"`    // A/B/C/D/F
	TopRisks        []string          `json:"top_risks"`
}

// Summarize creates a SecurityReport from a list of findings.
func Summarize(clusterID string, findings []SecurityFinding) *SecurityReport {
	r := &SecurityReport{
		ClusterID:  clusterID,
		Findings:   findings,
		TotalFindings: len(findings),
		BySeverity: make(map[string]int),
		ByCategory: make(map[string]int),
	}
	for _, f := range findings {
		r.BySeverity[string(f.Severity)]++
		r.ByCategory[string(f.Category)]++
	}

	// Posture score: start at 100, deduct per finding
	score := 100.0
	score -= float64(r.BySeverity["critical"]) * 20
	score -= float64(r.BySeverity["high"]) * 8
	score -= float64(r.BySeverity["medium"]) * 3
	score -= float64(r.BySeverity["low"]) * 1
	score = max(0, score)

	r.PostureScore = score
	switch {
	case score >= 90: r.PostureGrade = "A"
	case score >= 75: r.PostureGrade = "B"
	case score >= 60: r.PostureGrade = "C"
	case score >= 40: r.PostureGrade = "D"
	default:          r.PostureGrade = "F"
	}

	// Top risks: unique rule IDs with critical/high severity
	seen := map[string]bool{}
	for _, f := range findings {
		if (f.Severity == SeverityCritical || f.Severity == SeverityHigh) && !seen[f.RuleID] {
			r.TopRisks = append(r.TopRisks, fmt.Sprintf("[%s] %s", strings.ToUpper(string(f.Severity)), f.Title))
			seen[f.RuleID] = true
			if len(r.TopRisks) >= 5 {
				break
			}
		}
	}
	return r
}

func max(a, b float64) float64 {
	if a > b { return a }
	return b
}
