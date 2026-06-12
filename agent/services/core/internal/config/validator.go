// Package config implements configuration intelligence rules for Kubernetes resources.
package config

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
)

// Severity levels.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// Violation is a configuration problem found in a resource.
type Violation struct {
	RuleID       string
	Severity     string
	ResourceKind string
	Namespace    string
	ResourceName string
	Message      string
	Remediation  string
}

// Rule is a single configuration check.
type Rule struct {
	ID          string
	Name        string
	Severity    string
	Description string
	// Check receives a typed Kubernetes object and returns zero or more violations.
	// The rule must perform a type assertion to the expected resource kind.
	Check func(obj interface{}, context *ValidationContext) []Violation
}

// ValidationContext provides cross-resource information needed by some rules.
// For example: "does this ConfigMap reference actually exist?"
type ValidationContext struct {
	ClusterID        string
	ExistingSecrets  map[string]bool // namespace/name
	ExistingConfigMaps map[string]bool
	ExistingServices map[string]bool
	PodsBySelector   func(namespace string, selector map[string]string) int
}

// AllRules returns all built-in configuration validation rules.
func AllRules() []Rule {
	return []Rule{
		ruleReadinessProbeMissing(),
		ruleLivenessProbeMissing(),
		ruleResourceLimitsMissing(),
		ruleResourceRequestsMissing(),
		ruleImageLatestTag(),
		ruleSingleReplica(),
		ruleHPANoResourceRequests(),
		ruleServiceSelectorMismatch(),
		ruleNetworkPolicyAllowAll(),
	}
}

func ruleReadinessProbeMissing() Rule {
	return Rule{
		ID:       "PROBE_MISSING_READINESS",
		Name:     "Readiness probe missing",
		Severity: SeverityHigh,
		Description: "Containers without readiness probes receive traffic before they are ready, causing 5xx errors during rollouts.",
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			pod, ok := podSpec(obj)
			if !ok {
				return nil
			}
			ns, name, kind := resourceMeta(obj)
			var violations []Violation
			for _, c := range pod.Spec.Containers {
				if c.ReadinessProbe == nil {
					violations = append(violations, Violation{
						RuleID:       "PROBE_MISSING_READINESS",
						Severity:     SeverityHigh,
						ResourceKind: kind,
						Namespace:    ns,
						ResourceName: name,
						Message:      fmt.Sprintf("container %q has no readinessProbe — service traffic may be sent before the container is ready", c.Name),
						Remediation:  "Add readinessProbe with httpGet, tcpSocket, or exec. Set initialDelaySeconds and periodSeconds appropriately.",
					})
				}
			}
			return violations
		},
	}
}

func ruleLivenessProbeMissing() Rule {
	return Rule{
		ID:       "PROBE_MISSING_LIVENESS",
		Name:     "Liveness probe missing",
		Severity: SeverityMedium,
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			pod, ok := podSpec(obj)
			if !ok { return nil }
			ns, name, kind := resourceMeta(obj)
			var violations []Violation
			for _, c := range pod.Spec.Containers {
				if c.LivenessProbe == nil {
					violations = append(violations, Violation{
						RuleID: "PROBE_MISSING_LIVENESS", Severity: SeverityMedium,
						ResourceKind: kind, Namespace: ns, ResourceName: name,
						Message:     fmt.Sprintf("container %q has no livenessProbe — stuck processes will not be restarted", c.Name),
						Remediation: "Add livenessProbe. Use a different endpoint than readinessProbe to avoid flapping.",
					})
				}
			}
			return violations
		},
	}
}

func ruleResourceLimitsMissing() Rule {
	return Rule{
		ID:       "RESOURCE_LIMITS_MISSING",
		Name:     "Container missing resource limits",
		Severity: SeverityHigh,
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			pod, ok := podSpec(obj)
			if !ok { return nil }
			ns, name, kind := resourceMeta(obj)
			var violations []Violation
			for _, c := range pod.Spec.Containers {
				if c.Resources.Limits == nil {
					violations = append(violations, Violation{
						RuleID: "RESOURCE_LIMITS_MISSING", Severity: SeverityHigh,
						ResourceKind: kind, Namespace: ns, ResourceName: name,
						Message:     fmt.Sprintf("container %q has no resource limits — OOM and CPU throttling risk", c.Name),
						Remediation: "Set resources.limits.cpu and resources.limits.memory. Start with 2× the requests value.",
					})
				}
			}
			return violations
		},
	}
}

func ruleResourceRequestsMissing() Rule {
	return Rule{
		ID:       "RESOURCE_REQUESTS_MISSING",
		Name:     "Container missing resource requests",
		Severity: SeverityHigh,
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			pod, ok := podSpec(obj)
			if !ok { return nil }
			ns, name, kind := resourceMeta(obj)
			var violations []Violation
			for _, c := range pod.Spec.Containers {
				if c.Resources.Requests == nil {
					violations = append(violations, Violation{
						RuleID: "RESOURCE_REQUESTS_MISSING", Severity: SeverityHigh,
						ResourceKind: kind, Namespace: ns, ResourceName: name,
						Message:     fmt.Sprintf("container %q has no resource requests — scheduler cannot make informed placement decisions", c.Name),
						Remediation: "Set resources.requests.cpu and resources.requests.memory based on observed baseline usage.",
					})
				}
			}
			return violations
		},
	}
}

func ruleImageLatestTag() Rule {
	return Rule{
		ID:       "IMAGE_LATEST_TAG",
		Name:     "Container uses :latest image tag",
		Severity: SeverityMedium,
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			pod, ok := podSpec(obj)
			if !ok { return nil }
			ns, name, kind := resourceMeta(obj)
			var violations []Violation
			for _, c := range pod.Spec.Containers {
				if strings.HasSuffix(c.Image, ":latest") || !strings.Contains(c.Image, ":") {
					violations = append(violations, Violation{
						RuleID: "IMAGE_LATEST_TAG", Severity: SeverityMedium,
						ResourceKind: kind, Namespace: ns, ResourceName: name,
						Message:     fmt.Sprintf("container %q uses image %q — :latest tag breaks reproducibility and rollback", c.Name, c.Image),
						Remediation: "Pin to a specific digest (sha256:...) or semantic version tag (v1.2.3).",
					})
				}
			}
			return violations
		},
	}
}

func ruleSingleReplica() Rule {
	return Rule{
		ID:       "SINGLE_REPLICA",
		Name:     "Deployment has a single replica",
		Severity: SeverityHigh,
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			dep, ok := obj.(*appsv1.Deployment)
			if !ok { return nil }
			if dep.Spec.Replicas == nil || *dep.Spec.Replicas > 1 { return nil }
			return []Violation{{
				RuleID: "SINGLE_REPLICA", Severity: SeverityHigh,
				ResourceKind: "Deployment", Namespace: dep.Namespace, ResourceName: dep.Name,
				Message:     "deployment has 1 replica — node drain, eviction, or restart will cause downtime",
				Remediation: "Set replicas ≥ 2. Add a PodDisruptionBudget with minAvailable=1.",
			}}
		},
	}
}

func ruleHPANoResourceRequests() Rule {
	return Rule{
		ID:       "HPA_NO_RESOURCE_REQUESTS",
		Name:     "HPA target lacks resource requests",
		Severity: SeverityCritical,
		Check: func(obj interface{}, ctx *ValidationContext) []Violation {
			hpa, ok := obj.(*autoscalingv2.HorizontalPodAutoscaler)
			if !ok { return nil }
			// Check if any metric is CPU or memory utilization type.
			// These require resource requests to compute percentage.
			for _, metric := range hpa.Spec.Metrics {
				if metric.Type == autoscalingv2.ResourceMetricSourceType {
					// We can only fully verify this with the target pod spec,
					// but flag it as a potential issue for operator review.
					return []Violation{{
						RuleID: "HPA_NO_RESOURCE_REQUESTS", Severity: SeverityCritical,
						ResourceKind: "HorizontalPodAutoscaler",
						Namespace: hpa.Namespace, ResourceName: hpa.Name,
						Message:     fmt.Sprintf("HPA %q uses CPU/memory utilization metrics — target workload must have resource requests set", hpa.Name),
						Remediation: "Ensure the target Deployment/StatefulSet containers have resources.requests.cpu set.",
					}}
				}
			}
			return nil
		},
	}
}

func ruleServiceSelectorMismatch() Rule {
	return Rule{
		ID:       "SERVICE_SELECTOR_MISMATCH",
		Name:     "Service selector matches no pods",
		Severity: SeverityCritical,
		Check: func(obj interface{}, ctx *ValidationContext) []Violation {
			svc, ok := obj.(*corev1.Service)
			if !ok { return nil }
			if svc.Spec.Type == corev1.ServiceTypeExternalName { return nil }
			if len(svc.Spec.Selector) == 0 { return nil } // headless or manually managed
			if ctx == nil || ctx.PodsBySelector == nil { return nil }
			if ctx.PodsBySelector(svc.Namespace, svc.Spec.Selector) == 0 {
				return []Violation{{
					RuleID: "SERVICE_SELECTOR_MISMATCH", Severity: SeverityCritical,
					ResourceKind: "Service", Namespace: svc.Namespace, ResourceName: svc.Name,
					Message:     fmt.Sprintf("service %q selector matches 0 pods — all traffic will receive connection refused", svc.Name),
					Remediation: "Verify selector labels match the pod labels exactly. Check for typos.",
				}}
			}
			return nil
		},
	}
}

func ruleNetworkPolicyAllowAll() Rule {
	return Rule{
		ID:       "NETPOL_ALLOW_ALL",
		Name:     "NetworkPolicy allows all traffic",
		Severity: SeverityHigh,
		Check: func(obj interface{}, _ *ValidationContext) []Violation {
			np, ok := obj.(*networkingv1.NetworkPolicy)
			if !ok { return nil }
			// A NetworkPolicy with empty podSelector + empty Ingress = allow all ingress
			if len(np.Spec.PodSelector.MatchLabels) == 0 &&
				len(np.Spec.PodSelector.MatchExpressions) == 0 &&
				len(np.Spec.Ingress) == 1 &&
				len(np.Spec.Ingress[0].From) == 0 {
				return []Violation{{
					RuleID: "NETPOL_ALLOW_ALL", Severity: SeverityHigh,
					ResourceKind: "NetworkPolicy", Namespace: np.Namespace, ResourceName: np.Name,
					Message:     fmt.Sprintf("NetworkPolicy %q allows all ingress traffic to all pods in namespace", np.Name),
					Remediation: "Restrict podSelector to specific pods. Add From rules to limit ingress sources.",
				}}
			}
			return nil
		},
	}
}

// Helpers

type podSpecHolder struct {
	Spec corev1.PodSpec
}

func podSpec(obj interface{}) (*podSpecHolder, bool) {
	switch v := obj.(type) {
	case *appsv1.Deployment:
		return &podSpecHolder{Spec: v.Spec.Template.Spec}, true
	case *appsv1.StatefulSet:
		return &podSpecHolder{Spec: v.Spec.Template.Spec}, true
	case *appsv1.DaemonSet:
		return &podSpecHolder{Spec: v.Spec.Template.Spec}, true
	case *corev1.Pod:
		return &podSpecHolder{Spec: v.Spec}, true
	}
	return nil, false
}

func resourceMeta(obj interface{}) (namespace, name, kind string) {
	switch v := obj.(type) {
	case *appsv1.Deployment:
		return v.Namespace, v.Name, "Deployment"
	case *appsv1.StatefulSet:
		return v.Namespace, v.Name, "StatefulSet"
	case *appsv1.DaemonSet:
		return v.Namespace, v.Name, "DaemonSet"
	case *corev1.Pod:
		return v.Namespace, v.Name, "Pod"
	}
	return "", "", "Unknown"
}
