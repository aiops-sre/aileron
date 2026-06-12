package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// ── Node conditions fetcher ────────────────────────────────────────────────────

// NodeConditionsFetcher fetches K8s node conditions and events.
// Uses K8s events (timestamped) as primary historical evidence.
// Current conditions are included but with temporal dampening.
type NodeConditionsFetcher struct {
	clients map[string]kubernetes.Interface // clusterRef → k8s client
}

// NewNodeConditionsFetcher builds the fetcher with per-cluster K8s clients.
// kubeconfigPaths is a map of clusterRef → kubeconfig file path.
func NewNodeConditionsFetcher(kubeconfigPaths map[string]string) (*NodeConditionsFetcher, error) {
	clients := make(map[string]kubernetes.Interface, len(kubeconfigPaths))
	for clusterRef, path := range kubeconfigPaths {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig for cluster %s: %w", clusterRef, err)
		}
		cfg.Timeout = 10 * time.Second
		cfg.QPS = 20
		cfg.Burst = 30
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("creating k8s client for cluster %s: %w", clusterRef, err)
		}
		clients[clusterRef] = client
	}
	return &NodeConditionsFetcher{clients: clients}, nil
}

// NewNodeConditionsFetcherFromConfig builds the fetcher from an in-cluster config
// (for when OIE itself runs inside the cluster it monitors).
func NewNodeConditionsFetcherFromConfig(cfg *rest.Config, clusterRef string) (*NodeConditionsFetcher, error) {
	cfg.Timeout = 10 * time.Second
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}
	return &NodeConditionsFetcher{
		clients: map[string]kubernetes.Interface{clusterRef: client},
	}, nil
}

func (f *NodeConditionsFetcher) ID() fetchers.FetcherID        { return "k8s_node_conditions" }

// Clients returns the map of cluster clients so other fetchers can reuse them.
func (f *NodeConditionsFetcher) Clients() map[string]kubernetes.Interface {
	return f.clients
}
func (f *NodeConditionsFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *NodeConditionsFetcher) SourceName() string             { return "kubernetes" }

func (f *NodeConditionsFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" || profile.K8sNodeName == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		return &fetchers.FetchResult{
			Status: domain.FetchMissing,
			Evidence: []*domain.Evidence{{
				EvidenceType: domain.TypeSourceUnavailable,
				Source:       "kubernetes",
				Role:         domain.RoleContext,
				Description:  fmt.Sprintf("No K8s client configured for cluster %s", profile.ClusterRef),
				FetchStatus:  domain.FetchMissing,
				GatheredAt:   time.Now().UTC(),
				CreatedAt:    time.Now().UTC(),
			}},
		}, nil
	}

	var evidence []*domain.Evidence
	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	// ── K8s Events (HISTORICAL — timestamped) ────────────────────────────────
	// Events are our primary historical evidence. They carry precise timestamps.
	events, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf(
			"involvedObject.kind=Node,involvedObject.name=%s",
			profile.K8sNodeName,
		),
	})
	if err == nil {
		windowStart := req.IncidentStartAt.Add(-15 * time.Minute)
		windowEnd := req.IncidentStartAt.Add(5 * time.Minute)

		for i := range events.Items {
			ev := &events.Items[i]
			evTime := ev.LastTimestamp.Time
			if evTime.IsZero() {
				evTime = ev.FirstTimestamp.Time
			}
			if evTime.Before(windowStart) || evTime.After(windowEnd) {
				continue
			}

			payload, _ := json.Marshal(domain.K8sNodeEventPayload{
				Reason:    ev.Reason,
				Message:   ev.Message,
				EventType: ev.Type,
				Count:     ev.Count,
				FirstTime: ev.FirstTimestamp.Time,
				LastTime:  ev.LastTimestamp.Time,
				Source:    ev.Source.Component,
			})

			e := &domain.Evidence{
				EvidenceType:       domain.TypeK8sNodeEvent,
				Source:             "kubernetes",
				TemporalMode:       domain.TemporalHistorical,
				AsOfTime:           &evTime,
				Description:        fmt.Sprintf("K8s node event at %v: %s — %s", evTime.Format(time.RFC3339), ev.Reason, ev.Message),
				Payload:            payload,
				OccurredAt:         &evTime,
				GatheredAt:         time.Now().UTC(),
				EvidenceConfidence: 0.97,
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          time.Now().UTC(),
			}

			// Classify role based on event reason.
			switch ev.Reason {
			case "NodeNotReady", "NodeLost", "NodeHasSufficientMemory":
				e.Role = domain.RoleSupports
				e.Weight = 0.88
			case "NodeReady":
				if evTime.Before(req.IncidentStartAt) {
					e.Role = domain.RoleContext
				} else {
					// Node recovered after incident — context, not contradiction
					e.Role = domain.RoleContext
				}
			case "OOMKilling":
				e.EvidenceType = domain.TypeK8sOOMKill
				e.Role = domain.RoleSupports
				e.Weight = 0.92
			default:
				if ev.Type == corev1.EventTypeWarning {
					e.Role = domain.RoleContext
				} else {
					e.Role = domain.RoleContext
				}
			}

			evidence = append(evidence, e)
		}
	}

	// ── Current node conditions (CURRENT — dampened by temporal gap) ────────
	node, err := client.CoreV1().Nodes().Get(ctx, profile.K8sNodeName, metav1.GetOptions{})
	if err == nil {
		conditions := make([]domain.K8sCondition, 0, len(node.Status.Conditions))
		var activePressures []string

		for _, cond := range node.Status.Conditions {
			conditions = append(conditions, domain.K8sCondition{
				Type:               string(cond.Type),
				Status:             string(cond.Status),
				Reason:             cond.Reason,
				Message:            cond.Message,
				LastTransitionTime: cond.LastTransitionTime.Time,
			})

			// Pressure conditions.
			if cond.Status == corev1.ConditionTrue {
				switch cond.Type {
				case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
					activePressures = append(activePressures, string(cond.Type))
				}
			}
		}

		payload, _ := json.Marshal(domain.K8sNodeConditionPayload{
			NodeName:        profile.K8sNodeName,
			Conditions:      conditions,
			ActivePressures: activePressures,
		})

		e := &domain.Evidence{
			EvidenceType:       domain.TypeK8sNodeCondition,
			Source:             "kubernetes",
			TemporalMode:       domain.TemporalCurrent,
			TemporalGapSecs:    &gapSecs,
			Description:        fmt.Sprintf("K8s node %s current conditions (fetched %ds after incident)", profile.K8sNodeName, gapSecs),
			Payload:            payload,
			GatheredAt:         time.Now().UTC(),
			EvidenceConfidence: temporallyDampedConfidence(0.98, gapSecs),
			FetchStatus:        domain.FetchSuccess,
			CreatedAt:          time.Now().UTC(),
		}

		// Determine role based on current Ready condition.
		readyStatus := corev1.ConditionUnknown
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				readyStatus = cond.Status
				break
			}
		}

		switch readyStatus {
		case corev1.ConditionFalse:
			e.Role = domain.RoleSupports
			e.Weight = 0.90
			e.Description = fmt.Sprintf("Node %s is currently NotReady (fetched %ds after incident)", profile.K8sNodeName, gapSecs)
		case corev1.ConditionTrue:
			// Node is currently Ready. If gap is small → contradiction. If large → context (may have recovered).
			if gapSecs < 60 {
				e.Role = domain.RoleContradicts
				e.Weight = 0.70
				e.Description = fmt.Sprintf("Node %s is currently Ready (fetched %ds after incident)", profile.K8sNodeName, gapSecs)
			} else {
				e.Role = domain.RoleContext
				e.Description = fmt.Sprintf("Node %s is currently Ready (fetched %ds after incident — node may have auto-recovered)", profile.K8sNodeName, gapSecs)
			}
		default:
			e.Role = domain.RoleContext
		}

		if len(activePressures) > 0 {
			e.EvidenceType = domain.TypeK8sResourcePressure
			e.Role = domain.RoleSupports
			e.Weight = 0.85
			e.Description = fmt.Sprintf("Node %s has active pressure conditions: %s", profile.K8sNodeName, strings.Join(activePressures, ", "))
		}

		evidence = append(evidence, e)

		// ── CPU request saturation check ─────────────────────────────────────────
		// Compare total CPU requests from all running pods against node allocatable.
		// Saturation (requests > 90% allocatable) causes scheduler over-commitment
		// which leads to pod evictions and "Not all pods ready" cascades.
		// This is the root cause for 142 DT RESOURCE_CONTENTION problems identified
		// in the 3-day production incident analysis.
		if allocCPU, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
			pods, podErr := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
				FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.phase=Running", profile.K8sNodeName),
			})
			if podErr == nil {
				var totalRequestMillis int64
				for _, pod := range pods.Items {
					for _, c := range pod.Spec.Containers {
						if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
							totalRequestMillis += req.MilliValue()
						}
					}
				}
				allocMillis := allocCPU.MilliValue()
				if allocMillis > 0 {
					pct := float64(totalRequestMillis) / float64(allocMillis) * 100
					if pct >= 90 {
						cpuPayload, _ := json.Marshal(map[string]interface{}{
							"node":            profile.K8sNodeName,
							"allocatable_cpu": allocMillis,
							"requested_cpu":   totalRequestMillis,
							"saturation_pct":  pct,
							"pod_count":       len(pods.Items),
						})
						evidence = append(evidence, &domain.Evidence{
							EvidenceType:       domain.TypeK8sCPURequestSaturation,
							Source:             "kubernetes",
							TemporalMode:       domain.TemporalCurrent,
							TemporalGapSecs:    &gapSecs,
							Role:               domain.RoleSupports,
							Weight:             0.80,
							Description:        fmt.Sprintf("Node %s CPU requests at %.0f%% of allocatable (%dm / %dm) — scheduler over-commitment", profile.K8sNodeName, pct, totalRequestMillis, allocMillis),
							Payload:            cpuPayload,
							GatheredAt:         time.Now().UTC(),
							EvidenceConfidence: temporallyDampedConfidence(0.92, gapSecs),
							FetchStatus:        domain.FetchSuccess,
							CreatedAt:          time.Now().UTC(),
						})
					}
				}
			}
		}
	}

	return &fetchers.FetchResult{
		Evidence: evidence,
		Status:   domain.FetchSuccess,
	}, nil
}

// ── Pod exit code fetcher ──────────────────────────────────────────────────────

// PodExitCodeFetcher fetches pod termination state and recent logs.
type PodExitCodeFetcher struct {
	clients map[string]kubernetes.Interface
}

func NewPodExitCodeFetcher(clients map[string]kubernetes.Interface) *PodExitCodeFetcher {
	return &PodExitCodeFetcher{clients: clients}
}

func (f *PodExitCodeFetcher) ID() fetchers.FetcherID          { return "k8s_pod_exit_code" }
func (f *PodExitCodeFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *PodExitCodeFetcher) SourceName() string              { return "kubernetes" }

func (f *PodExitCodeFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" || profile.K8sNamespace == "" || profile.ResourceName == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	// Try exact pod name first. If not found (topology_path may carry a deployment name),
	// list pods with a label selector matching the workload name to find crashed pods.
	pod, err := client.CoreV1().Pods(profile.K8sNamespace).Get(ctx, profile.ResourceName, metav1.GetOptions{})
	if err != nil {
		// Fallback: list pods whose app/name label matches the resource name.
		// Handles the case where topology_path carries a deployment name, not a pod name.
		pods, listErr := client.CoreV1().Pods(profile.K8sNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=" + profile.ResourceName + ",app.kubernetes.io/name=" + profile.ResourceName,
			Limit:         5,
		})
		if listErr != nil || len(pods.Items) == 0 {
			// Also try prefix match for pod names (deployment generates "name-<hash>-<hash>").
			pods, listErr = client.CoreV1().Pods(profile.K8sNamespace).List(ctx, metav1.ListOptions{Limit: 50})
			if listErr != nil {
				return &fetchers.FetchResult{Status: domain.FetchError, FetchError: listErr}, nil
			}
			// Filter: pods whose name starts with the resource name.
			var matched []corev1.Pod
			for _, p := range pods.Items {
				if len(p.Name) >= len(profile.ResourceName) &&
					p.Name[:len(profile.ResourceName)] == profile.ResourceName {
					matched = append(matched, p)
				}
			}
			if len(matched) == 0 {
				return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
			}
			pod = &matched[0]
		} else if len(pods.Items) > 0 {
			pod = &pods.Items[0]
		} else {
			return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
		}
	}

	var evidence []*domain.Evidence

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated == nil {
			continue
		}

		term := cs.LastTerminationState.Terminated
		restartPattern := classifyRestartPattern(cs.RestartCount, cs.LastTerminationState)

		payload, _ := json.Marshal(domain.K8sPodExitCodePayload{
			PodName:        pod.Name,
			Namespace:      pod.Namespace,
			ContainerName:  cs.Name,
			ExitCode:       term.ExitCode,
			Reason:         term.Reason,
			RestartCount:   cs.RestartCount,
			LastRestartAt:  term.FinishedAt.Time,
			RestartPattern: restartPattern,
		})

		e := &domain.Evidence{
			EvidenceType:       domain.TypeK8sPodExitCode,
			Source:             "kubernetes",
			TemporalMode:       domain.TemporalHistorical,
			OccurredAt:         &term.FinishedAt.Time,
			Description:        fmt.Sprintf("Container %s in pod %s/%s exited with code %d (%s), restarts: %d", cs.Name, pod.Namespace, pod.Name, term.ExitCode, term.Reason, cs.RestartCount),
			Payload:            payload,
			GatheredAt:         time.Now().UTC(),
			EvidenceConfidence: 0.97,
			FetchStatus:        domain.FetchSuccess,
			CreatedAt:          time.Now().UTC(),
		}

		// Route hypothesis based on exit code with specific evidence sub-types.
		switch {
		case term.ExitCode == 137 || term.Reason == "OOMKilled":
			e.EvidenceType = domain.TypeK8sPodExitCodeOOM
			e.Role = domain.RoleSupports
			e.Weight = 0.90
			e.EvidenceGroup = strPtr("oom_signals")
		case term.ExitCode == 139:
			e.EvidenceType = domain.TypeK8sPodExitCodeSegfault
			e.Role = domain.RoleSupports
			e.Weight = 0.85
		case term.ExitCode == 0 && cs.RestartCount >= 5:
			// Exit-0 with many restarts = job/batch process deployed as Deployment
			// or dependency unavailable (app starts, checks connection, exits cleanly).
			e.EvidenceType = domain.TypeK8sPodExitCodeZero
			e.Role = domain.RoleSupports
			e.Weight = 0.70
			e.EvidenceGroup = strPtr("k8s_pod_crash_signals")
		case term.ExitCode == 0:
			e.EvidenceType = domain.TypeK8sPodExitCodeZero
			e.Role = domain.RoleContext
			e.Weight = 0.30
		default:
			e.Role = domain.RoleSupports
			e.Weight = 0.55
		}

		evidence = append(evidence, e)
	}

	// Contradiction evidence: if the pod is currently Running with 0 restarts,
	// it contradicts all crash-loop hypotheses. This enables the scorer's
	// elimination step (contradicting_evidence_count) to down-score them.
	if pod.Status.Phase == corev1.PodRunning {
		allHealthy := true
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready || cs.RestartCount > 2 {
				allHealthy = false
				break
			}
		}
		if allHealthy && len(pod.Status.ContainerStatuses) > 0 {
			gapSecs := int(time.Since(req.IncidentStartAt).Seconds())
			contradPayload, _ := json.Marshal(map[string]interface{}{
				"pod_name": pod.Name, "phase": "Running", "all_containers_ready": true,
			})
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeK8sPodExitCode,
				Source:             "kubernetes",
				TemporalMode:       domain.TemporalCurrent,
				TemporalGapSecs:    &gapSecs,
				Role:               domain.RoleContradicts,
				Weight:             0.60,
				Description:        fmt.Sprintf("Pod %s/%s is Running with all containers ready — contradicts crash-loop hypotheses", pod.Namespace, pod.Name),
				Payload:            contradPayload,
				GatheredAt:         time.Now().UTC(),
				EvidenceConfidence: temporallyDampedConfidence(0.90, gapSecs),
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          time.Now().UTC(),
			})
		}
	}

	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

// ── Pod events fetcher ────────────────────────────────────────────────────────

type PodEventsFetcher struct {
	clients map[string]kubernetes.Interface
}

func NewPodEventsFetcher(clients map[string]kubernetes.Interface) *PodEventsFetcher {
	return &PodEventsFetcher{clients: clients}
}

func (f *PodEventsFetcher) ID() fetchers.FetcherID          { return "k8s_pod_events" }
func (f *PodEventsFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *PodEventsFetcher) SourceName() string              { return "kubernetes" }

func (f *PodEventsFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" || profile.K8sNamespace == "" || profile.ResourceName == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	// Query events for the exact pod name first.
	// If no events found, also query by namespace to catch pods with generated names.
	events, err := client.CoreV1().Events(profile.K8sNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", profile.ResourceName),
	})
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	// If no events matched the exact name, fetch all pod events for the namespace
	// and filter by name prefix (handles deployment-generated pod names).
	if len(events.Items) == 0 {
		allEvents, listErr := client.CoreV1().Events(profile.K8sNamespace).List(ctx, metav1.ListOptions{
			FieldSelector: "involvedObject.kind=Pod",
		})
		if listErr == nil {
			for _, ev := range allEvents.Items {
				if len(ev.InvolvedObject.Name) >= len(profile.ResourceName) &&
					ev.InvolvedObject.Name[:len(profile.ResourceName)] == profile.ResourceName {
					events.Items = append(events.Items, ev)
				}
			}
		}
	}

	// Use a wide window: incident might be detected before pod symptoms appear,
	// or well after if the incident was raised from a DT alert.
	// Primary window: [-30min, +120min] of incident start covers both cases.
	windowStart := req.IncidentStartAt.Add(-30 * time.Minute)
	windowEnd := req.IncidentStartAt.Add(120 * time.Minute)
	// If incident is old (> 6 hours ago), use all events in the last 24 hours.
	if time.Since(req.IncidentStartAt) > 6*time.Hour {
		windowStart = time.Now().UTC().Add(-24 * time.Hour)
		windowEnd = time.Now().UTC()
	}

	var evidence []*domain.Evidence
	for i := range events.Items {
		ev := &events.Items[i]
		evTime := ev.LastTimestamp.Time
		if evTime.IsZero() {
			evTime = ev.FirstTimestamp.Time
		}
		if evTime.Before(windowStart) || evTime.After(windowEnd) {
			continue
		}

		payload, _ := json.Marshal(domain.K8sNodeEventPayload{
			Reason:    ev.Reason,
			Message:   ev.Message,
			EventType: ev.Type,
			Count:     ev.Count,
			FirstTime: ev.FirstTimestamp.Time,
			LastTime:  ev.LastTimestamp.Time,
		})

		e := &domain.Evidence{
			EvidenceType:       domain.TypeK8sPodEvent,
			Source:             "kubernetes",
			TemporalMode:       domain.TemporalHistorical,
			AsOfTime:           &evTime,
			OccurredAt:         &evTime,
			Description:        fmt.Sprintf("Pod event at %v: %s — %s", evTime.Format(time.RFC3339), ev.Reason, ev.Message),
			Payload:            payload,
			GatheredAt:         time.Now().UTC(),
			EvidenceConfidence: 0.95,
			FetchStatus:        domain.FetchSuccess,
			CreatedAt:          time.Now().UTC(),
		}

		switch ev.Reason {
		case "OOMKilling":
			e.EvidenceType = domain.TypeK8sOOMKill
			e.Role = domain.RoleSupports
			e.Weight = 0.92
			e.EvidenceGroup = strPtr("oom_signals")
		case "BackOff", "CrashLoopBackOff":
			e.EvidenceType = domain.TypeK8sPodEventCrashLoop
			e.Role = domain.RoleSupports
			e.Weight = 0.75
			e.EvidenceGroup = strPtr("k8s_pod_crash_signals")
		case "FailedMount", "FailedAttachVolume", "VolumeResizeFailed":
			e.EvidenceType = domain.TypeK8sPodEventStorage
			e.Role = domain.RoleSupports
			e.Weight = 0.85
		case "Failed", "BackoffLimitExceeded":
			e.EvidenceType = domain.TypeK8sPodEventFailed
			e.Role = domain.RoleSupports
			e.Weight = 0.75
		case "ImagePullBackOff", "ErrImagePull":
			e.EvidenceType = domain.TypeK8sPodEventImage
			e.Role = domain.RoleSupports
			e.Weight = 0.88
		case "Evicted":
			// Pod evicted due to node memory/disk pressure — signals node resource exhaustion
			e.EvidenceType = domain.TypeK8sPodEventEviction
			e.Role = domain.RoleSupports
			e.Weight = 0.80
			e.EvidenceGroup = strPtr("node_pressure_signals")
		case "FailedScheduling", "Unschedulable":
			// Pod can't be scheduled — node resource requests exhausted
			e.EvidenceType = domain.TypeK8sPodEventPending
			e.Role = domain.RoleSupports
			e.Weight = 0.75
		// ── K8sGPT extended waiting-reason codes ─────────────────────────────────
		case "CreateContainerConfigError", "CreateContainerError":
			// ConfigMap or Secret referenced by container is missing or wrong
			e.EvidenceType = domain.TypeK8sPodEventCreateConfigError
			e.Role = domain.RoleSupports
			e.Weight = 0.85
		case "InvalidImageName", "InvalidImage":
			e.EvidenceType = domain.TypeK8sPodEventInvalidImage
			e.Role = domain.RoleSupports
			e.Weight = 0.82
		case "ContainerCreating":
			// Stuck ContainerCreating — often a CSI/volume mount deadlock
			e.EvidenceType = domain.TypeK8sPodEventContainerCreating
			e.Role = domain.RoleSupports
			e.Weight = 0.65
		case "NetworkNotReady", "NetworkPlugin":
			e.EvidenceType = domain.TypeK8sPodEventNetworkNotReady
			e.Role = domain.RoleSupports
			e.Weight = 0.70
		case "Pulling", "Pulled", "Scheduled", "Started":
			e.Role = domain.RoleContext
		default:
			if ev.Type == corev1.EventTypeWarning {
				e.Role = domain.RoleContext
			} else {
				e.Role = domain.RoleContext
			}
		}

		evidence = append(evidence, e)
	}

	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

// ── PVC Capacity Fetcher ──────────────────────────────────────────────────────

// PVCCapacityFetcher checks PVC utilization and state in the incident's namespace.
// DT fires "Kubernetes PVC: Low disk space %" from K8s metrics, not ONTAP metrics.
// This fetcher queries PVC objects to find bound/lost PVCs and emits storage evidence.
type PVCCapacityFetcher struct {
	clients map[string]kubernetes.Interface
}

func NewPVCCapacityFetcher(clients map[string]kubernetes.Interface) *PVCCapacityFetcher {
	return &PVCCapacityFetcher{clients: clients}
}

func (f *PVCCapacityFetcher) ID() fetchers.FetcherID          { return "k8s_pvc_capacity" }
func (f *PVCCapacityFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *PVCCapacityFetcher) SourceName() string              { return "kubernetes" }

func (f *PVCCapacityFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" || profile.K8sNamespace == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	pvcs, err := client.CoreV1().PersistentVolumeClaims(profile.K8sNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())
	var evidence []*domain.Evidence
	now := time.Now().UTC()

	for _, pvc := range pvcs.Items {
		switch pvc.Status.Phase {
		case corev1.ClaimLost:
			// PVC is Lost — backing PV is gone or unavailable.
			capacity := pvc.Status.Capacity.Storage()
			var capBytes int64
			if capacity != nil {
				capBytes = capacity.Value()
			}
			payload, _ := json.Marshal(map[string]interface{}{
				"pvc_name":   pvc.Name,
				"namespace":  pvc.Namespace,
				"phase":      string(pvc.Status.Phase),
				"capacity_b": capBytes,
			})
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeNetAppVolumeState,
				Source:             "kubernetes",
				TemporalMode:       domain.TemporalCurrent,
				TemporalGapSecs:    &gapSecs,
				Role:               domain.RoleSupports,
				Weight:             0.88,
				Description:        fmt.Sprintf("PVC %s/%s is in Lost phase — backing volume is unavailable", pvc.Namespace, pvc.Name),
				Payload:            payload,
				GatheredAt:         now,
				EvidenceConfidence: temporallyDampedConfidence(0.95, gapSecs),
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          now,
			})

		case corev1.ClaimBound:
			// Check for storage-pressure events on this PVC.
			events, evErr := client.CoreV1().Events(pvc.Namespace).List(ctx, metav1.ListOptions{
				FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=PersistentVolumeClaim", pvc.Name),
			})
			if evErr != nil {
				continue
			}
			for _, ev := range events.Items {
				if ev.Reason != "VolumeResizeFailed" && ev.Reason != "Provisioning" &&
					ev.Reason != "ExternalProvisioning" {
					continue
				}
				evTime := ev.LastTimestamp.Time
				if evTime.IsZero() {
					evTime = ev.FirstTimestamp.Time
				}
				payload, _ := json.Marshal(map[string]interface{}{
					"pvc_name":  pvc.Name,
					"namespace": pvc.Namespace,
					"reason":    ev.Reason,
					"message":   ev.Message,
				})
				evidence = append(evidence, &domain.Evidence{
					EvidenceType:       domain.TypeK8sPodEventStorage,
					Source:             "kubernetes",
					TemporalMode:       domain.TemporalHistorical,
					AsOfTime:           &evTime,
					OccurredAt:         &evTime,
					Role:               domain.RoleSupports,
					Weight:             0.78,
					Description:        fmt.Sprintf("PVC %s/%s: %s — %s", pvc.Namespace, pvc.Name, ev.Reason, ev.Message),
					Payload:            payload,
					GatheredAt:         now,
					EvidenceConfidence: 0.92,
					FetchStatus:        domain.FetchSuccess,
					CreatedAt:          now,
				})
			}
		}
	}

	// Also check for Warning events in the namespace mentioning disk/storage
	nsEvents, evErr := client.CoreV1().Events(profile.K8sNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: "type=Warning",
	})
	if evErr == nil {
		windowStart := req.IncidentStartAt.Add(-2 * time.Hour)
		windowEnd := req.IncidentStartAt.Add(2 * time.Hour)
		for _, ev := range nsEvents.Items {
			evTime := ev.LastTimestamp.Time
			if evTime.IsZero() {
				evTime = ev.FirstTimestamp.Time
			}
			if evTime.Before(windowStart) || evTime.After(windowEnd) {
				continue
			}
			if ev.InvolvedObject.Kind != "PersistentVolumeClaim" {
				continue
			}
			payload, _ := json.Marshal(map[string]interface{}{
				"object": fmt.Sprintf("%s/%s", ev.InvolvedObject.Kind, ev.InvolvedObject.Name),
				"reason": ev.Reason, "message": ev.Message,
			})
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeK8sPodEventStorage,
				Source:             "kubernetes",
				TemporalMode:       domain.TemporalHistorical,
				AsOfTime:           &evTime,
				OccurredAt:         &evTime,
				Role:               domain.RoleSupports,
				Weight:             0.75,
				Description:        fmt.Sprintf("PVC event in %s: %s on %s — %s", profile.K8sNamespace, ev.Reason, ev.InvolvedObject.Name, ev.Message),
				Payload:            payload,
				GatheredAt:         now,
				EvidenceConfidence: 0.90,
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          now,
			})
		}
	}

	status := domain.FetchSuccess
	if len(evidence) == 0 {
		status = domain.FetchMissing
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: status}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func temporallyDampedConfidence(base float64, gapSeconds int) float64 {
	if gapSeconds <= 60 {
		return base
	}
	if gapSeconds >= 180 {
		return base * 0.50
	}
	decay := float64(gapSeconds-60) / float64(120)
	return base * (1.0 - decay*0.50)
}

func classifyRestartPattern(count int32, lastState corev1.ContainerState) string {
	if count == 0 {
		return "none"
	}
	if count >= 10 {
		return "exponential"
	}
	return "linear"
}

func strPtr(s string) *string { return &s }

// ── PDB Detector ──────────────────────────────────────────────────────────────

// PDBFetcher checks PodDisruptionBudgets in the namespace for DisruptionAllowed=False.
// K8sGPT pattern: a PDB blocking disruption is a common root cause for "deployment update stuck" incidents.
type PDBFetcher struct {
	clients map[string]kubernetes.Interface
}

func NewPDBFetcher(clients map[string]kubernetes.Interface) *PDBFetcher {
	return &PDBFetcher{clients: clients}
}

func (f *PDBFetcher) ID() fetchers.FetcherID          { return "k8s_pdb" }
func (f *PDBFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *PDBFetcher) SourceName() string              { return "kubernetes" }

func (f *PDBFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" || profile.K8sNamespace == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	pdbs, err := client.PolicyV1().PodDisruptionBudgets(profile.K8sNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	var evidence []*domain.Evidence
	now := time.Now().UTC()

	for i := range pdbs.Items {
		pdb := &pdbs.Items[i]
		// DisruptionsAllowed == 0 means no pod can be disrupted — deployment updates block.
		if pdb.Status.DisruptionsAllowed == 0 {
			evidence = append(evidence, &domain.Evidence{
				EvidenceType: domain.TypeK8sPodEventPDBBlocked,
				Source:       "kubernetes",
				Role:         domain.RoleSupports,
				Description: fmt.Sprintf("PodDisruptionBudget %s/%s has DisruptionsAllowed=0 (desired=%d, current=%d): deployment updates or drains will block",
					profile.K8sNamespace, pdb.Name,
					pdb.Status.DesiredHealthy, pdb.Status.CurrentHealthy),
				Weight:             0.85,
				FetchStatus:        domain.FetchSuccess,
				EvidenceConfidence: 0.90,
				GatheredAt:         now,
				CreatedAt:          now,
			})
		}
	}

	if len(evidence) == 0 {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

// ── Service Endpoint Probe ────────────────────────────────────────────────────

// ServiceEndpointFetcher checks whether a degraded service has NotReadyAddresses.
// K8sGPT pattern: if Endpoints.Subsets.NotReadyAddresses is non-empty, the root
// cause is the pods behind the service, not the service configuration itself.
type ServiceEndpointFetcher struct {
	clients map[string]kubernetes.Interface
}

func NewServiceEndpointFetcher(clients map[string]kubernetes.Interface) *ServiceEndpointFetcher {
	return &ServiceEndpointFetcher{clients: clients}
}

func (f *ServiceEndpointFetcher) ID() fetchers.FetcherID          { return "k8s_service_endpoints" }
func (f *ServiceEndpointFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *ServiceEndpointFetcher) SourceName() string              { return "kubernetes" }

func (f *ServiceEndpointFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" || profile.K8sNamespace == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	// Target service name from entity context.
	serviceName := profile.ResourceName
	if serviceName == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	endpoints, err := client.CoreV1().Endpoints(profile.K8sNamespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	var notReadyPods []string
	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.NotReadyAddresses {
			name := addr.IP
			if addr.TargetRef != nil {
				name = addr.TargetRef.Name
			}
			notReadyPods = append(notReadyPods, name)
		}
	}

	if len(notReadyPods) == 0 {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	now := time.Now().UTC()
	podList := strings.Join(notReadyPods, ", ")
	if len(podList) > 200 {
		podList = podList[:200] + "..."
	}
	evidence := []*domain.Evidence{{
		EvidenceType: domain.TypeK8sServiceEndpointNotReady,
		Source:       "kubernetes",
		Role:         domain.RoleSupports,
		Description: fmt.Sprintf("Service %s/%s has %d NotReadyAddress(es) — root cause is pods not the service: [%s]",
			profile.K8sNamespace, serviceName, len(notReadyPods), podList),
		Weight:             0.88,
		FetchStatus:        domain.FetchSuccess,
		EvidenceConfidence: 0.92,
		GatheredAt:         now,
		CreatedAt:          now,
	}}
	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}
