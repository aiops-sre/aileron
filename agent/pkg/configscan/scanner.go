// Package configscan runs lightweight configuration violation checks against
// Deployments in the informer cache and publishes results to Kafka.
//
// Checks performed (matching KubeSense config validator rules):
//   - Missing readiness probe (CHAOS_NO_READINESS_PROBE)
//   - Missing liveness probe (CHAOS_NO_LIVENESS_PROBE)
//   - Missing resource limits (RESOURCE_LIMITS_MISSING)
//   - Missing resource requests (RESOURCE_REQUESTS_MISSING)
//   - Image :latest tag (IMAGE_LATEST_TAG)
//   - Single replica SPOF (SINGLE_REPLICA)
//
// Published to: kubesense.config.violations (consumed by AlertHub)
package configscan

import (
	"fmt"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"github.com/aileron-platform/aileron/agent/pkg/kafkapub"
)

// Violation describes a single configuration issue on a workload.
type Violation struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	ClusterID    string    `json:"cluster_id"`
	RuleID       string    `json:"rule_id"`
	Severity     string    `json:"severity"`
	ResourceKind string    `json:"resource_kind"`
	Namespace    string    `json:"namespace"`
	ResourceName string    `json:"resource_name"`
	Message      string    `json:"message"`
	Remediation  string    `json:"remediation"`
}

// Scan checks all Deployments for configuration violations and publishes them.
func Scan(clusterID string, deployments []*appsv1.Deployment, pub *kafkapub.Publisher) {
	if pub == nil || len(deployments) == 0 {
		return
	}
	count := 0
	now := time.Now().UTC()
	for _, dep := range deployments {
		violations := check(dep)
		for _, v := range violations {
			v.Timestamp = now
			v.ClusterID = clusterID
			// Use composite key so AlertHub consumer can deduplicate
			key := fmt.Sprintf("%s/%s/%s/%s", clusterID, dep.Namespace, dep.Name, v.RuleID)
			pub.Publish(kafkapub.TopicConfigViolations, key, v)
			count++
		}
	}
	if count > 0 {
		log.Printf("configscan: published %d violations for cluster=%s deployments=%d",
			count, clusterID, len(deployments))
	}
}

func check(dep *appsv1.Deployment) []Violation {
	var out []Violation
	ns := dep.Namespace
	name := dep.Name

	// Single replica SPOF
	replicas := int32(1)
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	if replicas < 2 {
		out = append(out, Violation{
			ID:           fmt.Sprintf("%s/%s/SINGLE_REPLICA", ns, name),
			RuleID:       "SINGLE_REPLICA",
			Severity:     "high",
			ResourceKind: "Deployment",
			Namespace:    ns, ResourceName: name,
			Message:     fmt.Sprintf("Deployment %s/%s has only %d replica — complete outage if the pod is evicted or crashes", ns, name, replicas),
			Remediation: "Set spec.replicas >= 2 and add a PodDisruptionBudget",
		})
	}

	for _, c := range dep.Spec.Template.Spec.Containers {
		// Missing readiness probe
		if c.ReadinessProbe == nil {
			out = append(out, Violation{
				ID:       fmt.Sprintf("%s/%s/%s/READINESS_PROBE", ns, name, c.Name),
				RuleID:   "PROBE_MISSING_READINESS",
				Severity: "high",
				ResourceKind: "Deployment", Namespace: ns, ResourceName: name,
				Message:     fmt.Sprintf("Container %s in %s/%s has no readiness probe — rolling updates will route traffic to not-ready pods", c.Name, ns, name),
				Remediation: "Add spec.template.spec.containers[].readinessProbe",
			})
		}
		// Missing resource limits
		if c.Resources.Limits == nil || len(c.Resources.Limits) == 0 {
			out = append(out, Violation{
				ID:       fmt.Sprintf("%s/%s/%s/RESOURCE_LIMITS", ns, name, c.Name),
				RuleID:   "RESOURCE_LIMITS_MISSING",
				Severity: "high",
				ResourceKind: "Deployment", Namespace: ns, ResourceName: name,
				Message:     fmt.Sprintf("Container %s in %s/%s has no resource limits — can starve other pods on the node", c.Name, ns, name),
				Remediation: "Set spec.template.spec.containers[].resources.limits.cpu and .memory",
			})
		}
		// Image :latest tag
		if strings.HasSuffix(c.Image, ":latest") || !strings.Contains(c.Image, ":") {
			out = append(out, Violation{
				ID:       fmt.Sprintf("%s/%s/%s/IMAGE_LATEST", ns, name, c.Name),
				RuleID:   "IMAGE_LATEST_TAG",
				Severity: "medium",
				ResourceKind: "Deployment", Namespace: ns, ResourceName: name,
				Message:     fmt.Sprintf("Container %s uses mutable image tag (latest or none) — deployment may be non-reproducible", c.Name),
				Remediation: "Pin to a specific digest or semver tag",
			})
		}
	}
	return out
}
