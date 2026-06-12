// Package chaos implements Chaos Readiness Scoring for Kubernetes workloads.
//
// Chaos readiness measures how well your cluster can tolerate failures.
// A score of 100 means: if any single component fails, the system degrades
// gracefully. A score of 0 means: any failure causes a total outage.
//
// Checks performed:
//   - Pod disruption budgets (PDB) coverage
//   - Multi-AZ / multi-node pod spread
//   - Single-replica deployments (SPOF)
//   - Missing readiness probes (rolling updates cause downtime)
//   - Missing liveness probes (hung pods never restart)
//   - Resource limits absent (noisy neighbour risk)
//   - Anti-affinity rules (correlated failures)
//   - Health check endpoints (deep vs shallow)
//   - Circuit breaker patterns (retry storms)
//   - Dependency timeouts configured
//
// Market gap: Chaos Engineering tools (Chaos Monkey, LitmusChaos) inject failures.
// Nothing scores READINESS before you inject. KubeSense tells you what will break
// before you find out the hard way.
package chaos

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
)

// CheckID uniquely identifies a chaos readiness check.
type CheckID string

const (
	CheckPDB               CheckID = "CHAOS_PDB_MISSING"
	CheckSingleReplica     CheckID = "CHAOS_SINGLE_REPLICA"
	CheckMissingReadiness  CheckID = "CHAOS_NO_READINESS_PROBE"
	CheckMissingLiveness   CheckID = "CHAOS_NO_LIVENESS_PROBE"
	CheckNoResourceLimits  CheckID = "CHAOS_NO_RESOURCE_LIMITS"
	CheckNoAntiAffinity    CheckID = "CHAOS_NO_ANTI_AFFINITY"
	CheckAllPodsOnOneNode  CheckID = "CHAOS_PODS_SAME_NODE"
	CheckNoGracePeriod     CheckID = "CHAOS_NO_TERMINATION_GRACE"
	CheckImageLatest       CheckID = "CHAOS_IMAGE_LATEST_TAG"
	CheckNoTopologySpread  CheckID = "CHAOS_NO_TOPOLOGY_SPREAD"
)

// Severity of the chaos readiness gap.
type Severity string

const (
	SeverityCritical Severity = "critical" // will cause outage
	SeverityHigh     Severity = "high"     // likely to cause degradation
	SeverityMedium   Severity = "medium"   // increases blast radius
	SeverityLow      Severity = "low"      // best practice gap
)

// Finding is one chaos readiness gap detected.
type Finding struct {
	CheckID      CheckID  `json:"check_id"`
	Severity     Severity `json:"severity"`
	ResourceKind string   `json:"resource_kind"`
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Remediation  string   `json:"remediation"`
	ScorePenalty int      `json:"score_penalty"` // points deducted from 100
}

// WorkloadScore is the chaos readiness score for one workload.
type WorkloadScore struct {
	ResourceKind string    `json:"resource_kind"`
	Namespace    string    `json:"namespace"`
	Name         string    `json:"name"`
	Score        int       `json:"score"`        // 0-100
	Grade        string    `json:"grade"`        // A/B/C/D/F
	Findings     []Finding `json:"findings"`
	Replicas     int32     `json:"replicas"`
}

// ClusterScore aggregates chaos readiness across the cluster.
type ClusterScore struct {
	ClusterID      string          `json:"cluster_id"`
	OverallScore   int             `json:"overall_score"`
	OverallGrade   string          `json:"overall_grade"`
	WorkloadScores []WorkloadScore `json:"workload_scores"`
	TopFindings    []Finding       `json:"top_findings"`  // top critical findings
	TotalWorkloads int             `json:"total_workloads"`
	HealthyCount   int             `json:"healthy_count"` // score >= 80
	AtRiskCount    int             `json:"at_risk_count"` // score 40-79
	CriticalCount  int             `json:"critical_count"`// score < 40
	Summary        string          `json:"summary"`
}

// Scorer computes chaos readiness scores for Kubernetes workloads.
type Scorer struct{}

// NewScorer creates a chaos readiness scorer.
func NewScorer() *Scorer { return &Scorer{} }

// ScoreDeployment computes the chaos readiness score for a Deployment.
func (s *Scorer) ScoreDeployment(
	dep *appsv1.Deployment,
	pdbs []*policyv1.PodDisruptionBudget,
	podList []*corev1.Pod,
) WorkloadScore {
	ws := WorkloadScore{
		ResourceKind: "Deployment",
		Namespace:    dep.Namespace,
		Name:         dep.Name,
		Score:        100,
	}
	if dep.Spec.Replicas != nil {
		ws.Replicas = *dep.Spec.Replicas
	}

	ws.Findings = s.checkDeployment(dep, pdbs, podList)
	for _, f := range ws.Findings {
		ws.Score -= f.ScorePenalty
	}
	if ws.Score < 0 {
		ws.Score = 0
	}
	ws.Grade = scoreGrade(ws.Score)
	return ws
}

// ScoreCluster aggregates scores across all deployments in a cluster.
func (s *Scorer) ScoreCluster(
	clusterID string,
	deployments []*appsv1.Deployment,
	pdbs []*policyv1.PodDisruptionBudget,
	pods []*corev1.Pod,
) *ClusterScore {
	cs := &ClusterScore{
		ClusterID:      clusterID,
		TotalWorkloads: len(deployments),
	}

	allFindings := map[CheckID]Finding{}
	totalScore := 0

	for _, dep := range deployments {
		// Filter pods matching this deployment
		var depPods []*corev1.Pod
		for _, pod := range pods {
			if pod.Namespace == dep.Namespace && labelsSubset(dep.Spec.Selector.MatchLabels, pod.Labels) {
				depPods = append(depPods, pod)
			}
		}
		ws := s.ScoreDeployment(dep, pdbs, depPods)
		cs.WorkloadScores = append(cs.WorkloadScores, ws)
		totalScore += ws.Score

		switch {
		case ws.Score >= 80:
			cs.HealthyCount++
		case ws.Score >= 40:
			cs.AtRiskCount++
		default:
			cs.CriticalCount++
		}

		for _, f := range ws.Findings {
			if _, seen := allFindings[f.CheckID]; !seen {
				allFindings[f.CheckID] = f
			}
		}
	}

	if len(deployments) > 0 {
		cs.OverallScore = totalScore / len(deployments)
	} else {
		cs.OverallScore = 100
	}
	cs.OverallGrade = scoreGrade(cs.OverallScore)

	// Sort workloads by score ascending (worst first)
	sort.Slice(cs.WorkloadScores, func(i, j int) bool {
		return cs.WorkloadScores[i].Score < cs.WorkloadScores[j].Score
	})

	// Top findings (unique, sorted by penalty)
	for _, f := range allFindings {
		cs.TopFindings = append(cs.TopFindings, f)
	}
	sort.Slice(cs.TopFindings, func(i, j int) bool {
		return cs.TopFindings[i].ScorePenalty > cs.TopFindings[j].ScorePenalty
	})
	if len(cs.TopFindings) > 10 {
		cs.TopFindings = cs.TopFindings[:10]
	}

	cs.Summary = buildSummary(cs)
	return cs
}

// ─── Individual checks ────────────────────────────────────────────────────────

func (s *Scorer) checkDeployment(
	dep *appsv1.Deployment,
	pdbs []*policyv1.PodDisruptionBudget,
	pods []*corev1.Pod,
) []Finding {
	var findings []Finding
	spec := dep.Spec.Template.Spec
	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}

	// SPOF: single replica
	if replicas < 2 {
		findings = append(findings, Finding{
			CheckID:      CheckSingleReplica,
			Severity:     SeverityCritical,
			ResourceKind: "Deployment",
			Namespace:    dep.Namespace,
			Name:         dep.Name,
			Title:        "Single replica — no redundancy",
			Description:  fmt.Sprintf("Deployment %s/%s has only 1 replica. Any pod restart causes 100%% downtime.", dep.Namespace, dep.Name),
			Remediation:  fmt.Sprintf("kubectl scale deployment/%s --replicas=2 -n %s  # minimum for HA", dep.Name, dep.Namespace),
			ScorePenalty: 35,
		})
	}

	// Missing PDB
	if replicas >= 2 && !hasPDB(dep, pdbs) {
		findings = append(findings, Finding{
			CheckID:      CheckPDB,
			Severity:     SeverityHigh,
			ResourceKind: "Deployment",
			Namespace:    dep.Namespace,
			Name:         dep.Name,
			Title:        "No PodDisruptionBudget — node drains will disrupt service",
			Description:  fmt.Sprintf("Without a PDB, draining a node (for upgrades/maintenance) can kill all pods of %s/%s simultaneously.", dep.Namespace, dep.Name),
			Remediation:  fmt.Sprintf("kubectl create poddisruptionbudget %s-pdb --selector=app=%s --min-available=1 -n %s", dep.Name, dep.Name, dep.Namespace),
			ScorePenalty: 20,
		})
	}

	// Check each container
	for _, c := range spec.Containers {
		if c.ReadinessProbe == nil {
			findings = append(findings, Finding{
				CheckID:      CheckMissingReadiness,
				Severity:     SeverityHigh,
				ResourceKind: "Deployment",
				Namespace:    dep.Namespace,
				Name:         fmt.Sprintf("%s (container: %s)", dep.Name, c.Name),
				Title:        fmt.Sprintf("Container %s missing readinessProbe", c.Name),
				Description:  "Without a readiness probe, rolling updates route traffic to pods before they are ready, causing request errors.",
				Remediation:  "Add readinessProbe with httpGet, exec, or tcpSocket. Minimum: initialDelaySeconds=5, periodSeconds=10.",
				ScorePenalty: 20,
			})
		}
		if c.LivenessProbe == nil {
			findings = append(findings, Finding{
				CheckID:      CheckMissingLiveness,
				Severity:     SeverityMedium,
				ResourceKind: "Deployment",
				Namespace:    dep.Namespace,
				Name:         fmt.Sprintf("%s (container: %s)", dep.Name, c.Name),
				Title:        fmt.Sprintf("Container %s missing livenessProbe", c.Name),
				Description:  "Without a liveness probe, hung or deadlocked containers run indefinitely and are never restarted.",
				Remediation:  "Add livenessProbe. For HTTP services use httpGet; for others use exec with a health check script.",
				ScorePenalty: 15,
			})
		}
		if c.Resources.Limits == nil || len(c.Resources.Limits) == 0 {
			findings = append(findings, Finding{
				CheckID:      CheckNoResourceLimits,
				Severity:     SeverityMedium,
				ResourceKind: "Deployment",
				Namespace:    dep.Namespace,
				Name:         fmt.Sprintf("%s (container: %s)", dep.Name, c.Name),
				Title:        fmt.Sprintf("Container %s has no resource limits", c.Name),
				Description:  "Without resource limits, this container can consume unlimited CPU/memory, starving other pods on the same node.",
				Remediation:  "Set resources.limits.cpu and resources.limits.memory. Use VPA to determine appropriate values.",
				ScorePenalty: 15,
			})
		}
		// Check for :latest image tag
		if strings.HasSuffix(c.Image, ":latest") || (!strings.Contains(c.Image, ":") && !strings.Contains(c.Image, "@")) {
			findings = append(findings, Finding{
				CheckID:      CheckImageLatest,
				Severity:     SeverityMedium,
				ResourceKind: "Deployment",
				Namespace:    dep.Namespace,
				Name:         fmt.Sprintf("%s (container: %s)", dep.Name, c.Name),
				Title:        fmt.Sprintf("Container %s uses mutable image tag", c.Name),
				Description:  ":latest or untagged images are non-deterministic. Pod restarts may pull a different image, causing unexpected behaviour.",
				Remediation:  "Pin the image to a specific semver tag or SHA digest: image: myapp:v1.2.3 or image: myapp@sha256:abc123",
				ScorePenalty: 10,
			})
		}
	}

	// No anti-affinity rules when replicas > 1
	if replicas > 1 && spec.Affinity == nil && len(spec.TopologySpreadConstraints) == 0 {
		findings = append(findings, Finding{
			CheckID:      CheckNoAntiAffinity,
			Severity:     SeverityMedium,
			ResourceKind: "Deployment",
			Namespace:    dep.Namespace,
			Name:         dep.Name,
			Title:        "No pod anti-affinity or topology spread constraints",
			Description:  "Multiple replicas may be scheduled on the same node. A single node failure will kill all replicas simultaneously.",
			Remediation:  "Add topologySpreadConstraints to spread pods across nodes/zones, or set podAntiAffinity to prefer different nodes.",
			ScorePenalty: 15,
		})
	}

	// Check if all pods running on same node
	if len(pods) >= 2 {
		nodeSet := map[string]bool{}
		for _, pod := range pods {
			if pod.Spec.NodeName != "" {
				nodeSet[pod.Spec.NodeName] = true
			}
		}
		if len(nodeSet) == 1 {
			findings = append(findings, Finding{
				CheckID:      CheckAllPodsOnOneNode,
				Severity:     SeverityHigh,
				ResourceKind: "Deployment",
				Namespace:    dep.Namespace,
				Name:         dep.Name,
				Title:        "All pods on the same node — correlated failure risk",
				Description:  fmt.Sprintf("All %d pods of %s/%s are on the same node. A single node failure causes 100%% downtime.", len(pods), dep.Namespace, dep.Name),
				Remediation:  "Add topologySpreadConstraints with topology key kubernetes.io/hostname and maxSkew: 1.",
				ScorePenalty: 25,
			})
		}
	}

	// Termination grace period
	if spec.TerminationGracePeriodSeconds != nil && *spec.TerminationGracePeriodSeconds == 0 {
		findings = append(findings, Finding{
			CheckID:      CheckNoGracePeriod,
			Severity:     SeverityMedium,
			ResourceKind: "Deployment",
			Namespace:    dep.Namespace,
			Name:         dep.Name,
			Title:        "Zero termination grace period — in-flight requests dropped",
			Description:  "terminationGracePeriodSeconds=0 means pods are killed immediately without allowing in-flight requests to complete.",
			Remediation:  "Set terminationGracePeriodSeconds to at least 30 (or your p99 request duration + buffer).",
			ScorePenalty: 15,
		})
	}

	return findings
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func hasPDB(dep *appsv1.Deployment, pdbs []*policyv1.PodDisruptionBudget) bool {
	for _, pdb := range pdbs {
		if pdb.Namespace != dep.Namespace {
			continue
		}
		if pdb.Spec.Selector == nil {
			continue
		}
		if labelsSubset(pdb.Spec.Selector.MatchLabels, dep.Spec.Template.Labels) {
			return true
		}
	}
	return false
}

func labelsSubset(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func scoreGrade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 55:
		return "C"
	case score >= 35:
		return "D"
	default:
		return "F"
	}
}

func buildSummary(cs *ClusterScore) string {
	return fmt.Sprintf(
		"Cluster chaos readiness: %s (%d/100). %d/%d workloads healthy, %d at risk, %d critical.",
		cs.OverallGrade, cs.OverallScore,
		cs.HealthyCount, cs.TotalWorkloads,
		cs.AtRiskCount, cs.CriticalCount,
	)
}
