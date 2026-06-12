// Package chaos provides chaos readiness scoring accessible to all services.
// Implements the same checks as services/core/internal/chaos but as a standalone
// package in pkg/ so the agent and other services can import it without
// violating Go's internal package rule.
package chaos

import (
	"fmt"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"

	"github.com/aileron-platform/aileron/agent/pkg/kafkapub"
)

// ClusterScore is the aggregate chaos readiness score for a cluster.
type ClusterScore struct {
	ClusterID        string          `json:"cluster_id"`
	ClusterScore     float64         `json:"cluster_score"`    // 0–100
	Grade            string          `json:"grade"`            // A/B/C/D/F
	TotalWorkloads   int             `json:"total_workloads"`
	HighRiskCount    int             `json:"high_risk_count"`  // deployments scoring <50
	Summary          string          `json:"summary"`
	Timestamp        time.Time       `json:"timestamp"`
}

// RunScoring scores all Deployments in the cluster and publishes the result.
// Called from the agent after cache sync.
func RunScoring(clusterID string,
	deployments []*appsv1.Deployment, pods []*corev1.Pod, pdbs []*policyv1.PodDisruptionBudget,
	pub *kafkapub.Publisher) {
	if len(deployments) == 0 || pub == nil {
		return
	}

	// Build PDB index: namespace/selector label → covered.
	pdbCovered := make(map[string]bool)
	for _, pdb := range pdbs {
		if pdb.Status.DisruptionsAllowed > 0 {
			key := pdb.Namespace + "/" + labelsKey(pdb.Spec.Selector.MatchLabels)
			pdbCovered[key] = true
		}
	}

	totalScore := 0
	highRisk := 0
	for _, dep := range deployments {
		score := scoreDeployment(dep, pdbCovered)
		totalScore += score
		if score < 50 {
			highRisk++
		}
	}

	clusterScore := 0.0
	if len(deployments) > 0 {
		clusterScore = float64(totalScore) / float64(len(deployments))
	}

	grade := "A"
	switch {
	case clusterScore < 40:
		grade = "F"
	case clusterScore < 55:
		grade = "D"
	case clusterScore < 70:
		grade = "C"
	case clusterScore < 85:
		grade = "B"
	}

	score := ClusterScore{
		ClusterID:      clusterID,
		ClusterScore:   clusterScore,
		Grade:          grade,
		TotalWorkloads: len(deployments),
		HighRiskCount:  highRisk,
		Summary:        buildSummary(clusterID, clusterScore, highRisk, len(deployments)),
		Timestamp:      time.Now().UTC(),
	}

	pub.Publish(kafkapub.TopicChaosScores, clusterID, score)
	log.Printf("chaos: published cluster=%s score=%.0f/100 grade=%s workloads=%d highRisk=%d",
		clusterID, clusterScore, grade, len(deployments), highRisk)
}

// scoreDeployment returns a 0–100 chaos readiness score for a single Deployment.
func scoreDeployment(dep *appsv1.Deployment, pdbCovered map[string]bool) int {
	score := 100

	// Single replica = SPOF (-40 points — complete outage on pod failure)
	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	if replicas < 2 {
		score -= 40
	}

	// Missing PDB = no controlled disruption (-20 points)
	pdbKey := dep.Namespace + "/" + labelsKey(dep.Spec.Selector.MatchLabels)
	if !pdbCovered[pdbKey] {
		score -= 20
	}

	// Check container probes
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.ReadinessProbe == nil {
			score -= 15 // missing readiness probe = rolling update causes downtime
			break
		}
		if c.LivenessProbe == nil {
			score -= 10 // missing liveness probe = hung pods never restart
			break
		}
	}

	// Missing resource limits = noisy neighbour risk (-10 points)
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Resources.Limits == nil || len(c.Resources.Limits) == 0 {
			score -= 10
			break
		}
	}

	if score < 0 {
		score = 0
	}
	return score
}

func labelsKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func buildSummary(clusterID string, score float64, highRisk, total int) string {
	if highRisk == 0 {
		return fmt.Sprintf("Cluster %s: all %d workloads pass chaos readiness checks (score=%.0f/100)",
			clusterID, total, score)
	}
	return fmt.Sprintf("Cluster %s: %d/%d workloads have chaos readiness issues (cluster score=%.0f/100) — single replicas, missing PDBs, or absent probes",
		clusterID, highRisk, total, score)
}
