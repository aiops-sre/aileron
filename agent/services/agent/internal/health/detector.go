// Package health detects pod and node health issues and emits intelligence events.
package health

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"github.com/aileron-platform/aileron/agent/pkg/events"
)

// Publisher is the interface the health detector uses to emit events.
type Publisher interface {
	Publish(ctx context.Context, event *events.IntelligenceEvent) error
}

// Detector converts pod/node state changes into health intelligence events.
type Detector struct {
	publisher Publisher
	clusterID string
}

// NewDetector creates a health detector.
func NewDetector(publisher Publisher, clusterID string) *Detector {
	return &Detector{publisher: publisher, clusterID: clusterID}
}

// OnPodUpdate evaluates a pod and emits health events for any problems found.
func (d *Detector) OnPodUpdate(ctx context.Context, pod *corev1.Pod) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting == nil {
			continue
		}
		switch cs.State.Waiting.Reason {
		case "CrashLoopBackOff":
			d.publish(ctx, &events.IntelligenceEvent{
				Type:     events.EventPodCrashLoopBackOff,
				Severity: events.SeverityCritical,
				Resource: podRef(pod),
				Annotations: map[string]string{
					"container":     cs.Name,
					"restart_count": fmt.Sprintf("%d", cs.RestartCount),
					"reason":        cs.State.Waiting.Message,
				},
			})

		case "OOMKilled":
			d.publish(ctx, &events.IntelligenceEvent{
				Type:     events.EventPodOOMKilled,
				Severity: events.SeverityCritical,
				Resource: podRef(pod),
				Annotations: map[string]string{
					"container":     cs.Name,
					"memory_limit":  containerMemoryLimit(pod, cs.Name),
					"restart_count": fmt.Sprintf("%d", cs.RestartCount),
				},
			})

		case "ImagePullBackOff", "ErrImagePull":
			d.publish(ctx, &events.IntelligenceEvent{
				Type:     events.EventPodImagePullError,
				Severity: events.SeverityHigh,
				Resource: podRef(pod),
				Annotations: map[string]string{
					"container": cs.Name,
					"image":     cs.Image,
					"message":   cs.State.Waiting.Message,
				},
			})

		case "CreateContainerConfigError", "CreateContainerError":
			d.publish(ctx, &events.IntelligenceEvent{
				Type:     events.EventPodPending,
				Severity: events.SeverityHigh,
				Resource: podRef(pod),
				Annotations: map[string]string{
					"container": cs.Name,
					"reason":    cs.State.Waiting.Reason,
					"message":   cs.State.Waiting.Message,
				},
			})
		}
	}

	// Detect pods stuck in Pending (likely PVC/resource/scheduler issue)
	if pod.Status.Phase == corev1.PodPending {
		age := time.Since(pod.CreationTimestamp.Time)
		if age > 5*time.Minute {
			var reason string
			for _, cond := range pod.Status.Conditions {
				if cond.Status != corev1.ConditionTrue {
					reason = fmt.Sprintf("%s: %s", cond.Reason, cond.Message)
					break
				}
			}
			d.publish(ctx, &events.IntelligenceEvent{
				Type:     events.EventPodPending,
				Severity: events.SeverityMedium,
				Resource: podRef(pod),
				Annotations: map[string]string{
					"pending_minutes": fmt.Sprintf("%.0f", age.Minutes()),
					"reason":          reason,
				},
			})
		}
	}
}

// OnNodeUpdate evaluates a node and emits health events for any problems.
func (d *Detector) OnNodeUpdate(ctx context.Context, node *corev1.Node) {
	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			if cond.Status != corev1.ConditionTrue {
				d.publish(ctx, &events.IntelligenceEvent{
					Type:     events.EventNodeNotReady,
					Severity: events.SeverityCritical,
					Resource: nodeRef(node),
					Annotations: map[string]string{
						"status":  string(cond.Status),
						"reason":  cond.Reason,
						"message": cond.Message,
					},
				})
			}
		case corev1.NodeDiskPressure:
			if cond.Status == corev1.ConditionTrue {
				d.publish(ctx, &events.IntelligenceEvent{
					Type:     events.EventNodeDiskPressure,
					Severity: events.SeverityHigh,
					Resource: nodeRef(node),
					Annotations: map[string]string{"reason": cond.Reason},
				})
			}
		case corev1.NodeMemoryPressure:
			if cond.Status == corev1.ConditionTrue {
				d.publish(ctx, &events.IntelligenceEvent{
					Type:     events.EventNodeMemPressure,
					Severity: events.SeverityHigh,
					Resource: nodeRef(node),
					Annotations: map[string]string{"reason": cond.Reason},
				})
			}
		}
	}
	// Detect cordoned nodes
	if node.Spec.Unschedulable {
		d.publish(ctx, &events.IntelligenceEvent{
			Type:     events.EventNodeCordoned,
			Severity: events.SeverityMedium,
			Resource: nodeRef(node),
		})
	}
}

func (d *Detector) publish(ctx context.Context, ev *events.IntelligenceEvent) {
	ev.ClusterID = d.clusterID
	_ = d.publisher.Publish(ctx, ev)
}

func podRef(pod *corev1.Pod) events.ResourceRef {
	return events.ResourceRef{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  pod.Namespace,
		Name:       pod.Name,
		UID:        string(pod.UID),
		Labels:     pod.Labels,
	}
}

func nodeRef(node *corev1.Node) events.ResourceRef {
	return events.ResourceRef{
		APIVersion: "v1",
		Kind:       "Node",
		Name:       node.Name,
		UID:        string(node.UID),
		Labels:     node.Labels,
	}
}

func containerMemoryLimit(pod *corev1.Pod, containerName string) string {
	for _, c := range pod.Spec.Containers {
		if c.Name == containerName && c.Resources.Limits != nil {
			if mem, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				return mem.String()
			}
		}
	}
	return "none"
}
