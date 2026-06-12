package topology

import (
	"context"
	"fmt"
	"log"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ============================================================================
// KUBERNETES API CLIENT FOR REAL TOPOLOGY DISCOVERY
// Queries K8s API to get nodes, pods, and correlate node failures
// ============================================================================

type K8sClient struct {
	clientset *kubernetes.Clientset
	config    *rest.Config
}

// NewK8sClient creates a K8s API client from kubeconfig or token
func NewK8sClient(apiServer, token string, kubeconfig []byte) (*K8sClient, error) {
	var config *rest.Config
	var err error

	if len(kubeconfig) > 0 {
		// Use kubeconfig
		config, err = clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	} else {
		// Use token
		config = &rest.Config{
			Host:        apiServer,
			BearerToken: token,
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true, // In production, set to false and provide CA cert
			},
		}
	}

	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &K8sClient{
		clientset: clientset,
		config:    config,
	}, nil
}

// ============================================================================
// REAL KUBERNETES DISCOVERER
// ============================================================================

type RealKubernetesDiscoverer struct {
	client *K8sClient
}

func NewRealKubernetesDiscoverer(apiServer, token string, kubeconfig []byte) (*RealKubernetesDiscoverer, error) {
	client, err := NewK8sClient(apiServer, token, kubeconfig)
	if err != nil {
		return nil, err
	}

	return &RealKubernetesDiscoverer{
		client: client,
	}, nil
}

func (d *RealKubernetesDiscoverer) GetName() string {
	return "Real Kubernetes API Discoverer"
}

func (d *RealKubernetesDiscoverer) GetLayer() InfrastructureLayer {
	return LayerKubernetes
}

func (d *RealKubernetesDiscoverer) Discover(ctx context.Context, region string) ([]InfraNode, error) {
	nodes := make([]InfraNode, 0)

	// Get K8s nodes
	k8sNodes, err := d.client.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("K8s ListNodes failed: %w", err)
	}

	log.Printf("Discovered %d K8s nodes from API", len(k8sNodes.Items))

	// Create nodes for K8s worker nodes
	for _, k8sNode := range k8sNodes.Items {
		// Determine VM parent (extract from node name or labels)
		vmParent := ""
		if vmName, ok := k8sNode.Labels["topology.kubernetes.io/vm"]; ok {
			vmParent = vmName
		} else if hostname, ok := k8sNode.Labels["kubernetes.io/hostname"]; ok {
			vmParent = hostname
		}

		// Get node condition (Ready/NotReady)
		nodeStatus := "unknown"
		healthStatus := "unknown"
		for _, condition := range k8sNode.Status.Conditions {
			if condition.Type == "Ready" {
				if condition.Status == "True" {
					nodeStatus = "Ready"
					healthStatus = "healthy"
				} else {
					nodeStatus = "NotReady"
					healthStatus = "unhealthy"
				}
				break
			}
		}

		node := InfraNode{
			ID:           string(k8sNode.UID),
			Name:         k8sNode.Name,
			Type:         "k8s_node",
			Layer:        LayerKubernetes,
			Region:       region,
			ParentID:     vmParent, // Links K8s node to VM!
			Status:       nodeStatus,
			HealthStatus: healthStatus,
			Properties: map[string]interface{}{
				"hostname":          k8sNode.Name,
				"internal_ip":       getNodeIP(k8sNode.Status.Addresses),
				"os_image":          k8sNode.Status.NodeInfo.OSImage,
				"kernel_version":    k8sNode.Status.NodeInfo.KernelVersion,
				"kubelet_version":   k8sNode.Status.NodeInfo.KubeletVersion,
				"container_runtime": k8sNode.Status.NodeInfo.ContainerRuntimeVersion,
				"cpu_capacity":      k8sNode.Status.Capacity.Cpu().String(),
				"memory_capacity":   k8sNode.Status.Capacity.Memory().String(),
				"pods_capacity":     k8sNode.Status.Capacity.Pods().String(),
			},
			Labels: k8sNode.Labels,
			Dependencies: func() []string {
				if vmParent != "" {
					return []string{vmParent}
				}
				return []string{}
			}(),
			Dependents:    []string{}, // Will be populated with pods
			LastDiscovery: time.Now(),
			CreatedAt:     k8sNode.CreationTimestamp.Time,
			UpdatedAt:     time.Now(),
		}

		nodes = append(nodes, node)
	}

	// Get pods to map to nodes
	pods, err := d.client.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("K8s ListPods failed: %v", err)
		return nodes, nil // Return nodes at least
	}

	log.Printf("Discovered %d pods from K8s API", len(pods.Items))

	// Create nodes for pods and link to K8s nodes
	for _, pod := range pods.Items {
		// Only track running/pending pods
		if pod.Status.Phase != "Running" && pod.Status.Phase != "Pending" {
			continue
		}

		podNode := InfraNode{
			ID:           string(pod.UID),
			Name:         pod.Name,
			Type:         "k8s_pod",
			Layer:        LayerApplication,
			Region:       region,
			ParentID:     pod.Spec.NodeName, // Links pod to K8s node!
			Status:       string(pod.Status.Phase),
			HealthStatus: getPodHealth(pod.Status.Phase, pod.Status.Conditions),
			Properties: map[string]interface{}{
				"namespace":     pod.Namespace,
				"node_name":     pod.Spec.NodeName,
				"pod_ip":        pod.Status.PodIP,
				"host_ip":       pod.Status.HostIP,
				"restart_count": getRestartCount(pod.Status.ContainerStatuses),
				"containers":    len(pod.Spec.Containers),
				"phase":         string(pod.Status.Phase),
			},
			Labels:        pod.Labels,
			Dependencies:  []string{pod.Spec.NodeName}, // Pod depends on K8s node
			Dependents:    []string{},
			LastDiscovery: time.Now(),
			CreatedAt:     pod.CreationTimestamp.Time,
			UpdatedAt:     time.Now(),
		}

		nodes = append(nodes, podNode)

		// Update K8s node's dependents list
		for i := range nodes {
			if nodes[i].Name == pod.Spec.NodeName {
				nodes[i].Dependents = append(nodes[i].Dependents, string(pod.UID))
			}
		}
	}

	log.Printf("K8s topology: %d nodes hosting %d pods", len(k8sNodes.Items), len(pods.Items))

	return nodes, nil
}

// Helper functions
func getNodeIP(addresses []v1.NodeAddress) string {
	for _, addr := range addresses {
		if addr.Type == v1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}

func getPodHealth(phase v1.PodPhase, conditions []v1.PodCondition) string {
	if phase == "Running" {
		for _, condition := range conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				return "healthy"
			}
		}
		return "degraded"
	}
	if phase == "Pending" {
		return "degraded"
	}
	return "unhealthy"
}

func getRestartCount(statuses []v1.ContainerStatus) int {
	total := 0
	for _, status := range statuses {
		total += int(status.RestartCount)
	}
	return total
}
