// Package topology maintains the agent's in-memory view of cluster topology.
// It is rebuilt from informer cache on startup and kept live via event handlers.
package topology

import (
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

// EdgeType classifies the relationship between two topology nodes.
type EdgeType string

const (
	EdgeRunsOn        EdgeType = "RUNS_ON"
	EdgeManagedBy     EdgeType = "MANAGED_BY"
	EdgeSelectedBy    EdgeType = "SELECTED_BY"
	EdgeRoutesTo      EdgeType = "ROUTES_TO"
	EdgeMountsPVC     EdgeType = "MOUNTS_PVC"
	EdgeBoundTo       EdgeType = "BOUND_TO"
	EdgeUsesConfigMap EdgeType = "USES_CONFIGMAP"
	EdgeUsesSecret    EdgeType = "USES_SECRET"
	EdgeUsesAccount   EdgeType = "USES_SA"
	EdgeScaledBy      EdgeType = "SCALED_BY"
	EdgeInNamespace   EdgeType = "IN_NAMESPACE"
)

// Node represents a Kubernetes resource in the topology graph.
type Node struct {
	ID        string
	Kind      string
	Namespace string
	Name      string
	UID       string
	Labels    map[string]string
	Status    string
	UpdatedAt time.Time
}

// Edge is a directed relationship between two topology nodes.
type Edge struct {
	SourceID string
	TargetID string
	Type     EdgeType
	Metadata map[string]string
}

// Graph is the agent's in-memory topology graph.
// All methods are safe for concurrent use.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	edges map[string][]Edge // key: sourceID, value: outbound edges
}

// NewGraph creates an empty topology graph.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]*Node),
		edges: make(map[string][]Edge),
	}
}

// NodeCount returns the current number of nodes.
func (g *Graph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// UpsertPod updates the topology from a pod event.
func (g *Graph) UpsertPod(pod *corev1.Pod) {
	g.mu.Lock()
	defer g.mu.Unlock()

	id := nodeKey("Pod", pod.Namespace, pod.Name)
	g.nodes[id] = &Node{
		ID: id, Kind: "Pod",
		Namespace: pod.Namespace, Name: pod.Name,
		UID: string(pod.UID), Labels: pod.Labels,
		Status:    string(pod.Status.Phase),
		UpdatedAt: time.Now(),
	}
	// Remove stale edges originating from this pod before rebuilding
	delete(g.edges, id)

	// Pod → Node
	if pod.Spec.NodeName != "" {
		g.addEdge(id, nodeKey("Node", "", pod.Spec.NodeName), EdgeRunsOn, nil)
	}

	// Pod → Namespace
	if pod.Namespace != "" {
		g.addEdge(id, nodeKey("Namespace", "", pod.Namespace), EdgeInNamespace, nil)
	}

	// Pod → owner (Deployment, StatefulSet, DaemonSet, Job via OwnerReferences)
	for _, ref := range pod.OwnerReferences {
		ownerID := nodeKey(ref.Kind, pod.Namespace, ref.Name)
		g.addEdge(id, ownerID, EdgeManagedBy, map[string]string{"controller": ref.Kind})
	}

	// Pod → ServiceAccount
	if pod.Spec.ServiceAccountName != "" {
		g.addEdge(id, nodeKey("ServiceAccount", pod.Namespace, pod.Spec.ServiceAccountName), EdgeUsesAccount, nil)
	}

	// Pod → volumes (PVC, ConfigMap, Secret)
	for _, vol := range pod.Spec.Volumes {
		switch {
		case vol.PersistentVolumeClaim != nil:
			g.addEdge(id, nodeKey("PersistentVolumeClaim", pod.Namespace, vol.PersistentVolumeClaim.ClaimName),
				EdgeMountsPVC, map[string]string{"volume": vol.Name})
		case vol.ConfigMap != nil:
			g.addEdge(id, nodeKey("ConfigMap", pod.Namespace, vol.ConfigMap.Name),
				EdgeUsesConfigMap, map[string]string{"as": "volume"})
		case vol.Secret != nil:
			g.addEdge(id, nodeKey("Secret", pod.Namespace, vol.Secret.SecretName),
				EdgeUsesSecret, map[string]string{"as": "volume"})
		}
	}

	// Pod → container env refs (ConfigMap/Secret envFrom)
	for _, c := range pod.Spec.Containers {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				g.addEdge(id, nodeKey("ConfigMap", pod.Namespace, ef.ConfigMapRef.Name),
					EdgeUsesConfigMap, map[string]string{"as": "envFrom", "container": c.Name})
			}
			if ef.SecretRef != nil {
				g.addEdge(id, nodeKey("Secret", pod.Namespace, ef.SecretRef.Name),
					EdgeUsesSecret, map[string]string{"as": "envFrom", "container": c.Name})
			}
		}
		// Individual env valueFrom refs
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef != nil {
				g.addEdge(id, nodeKey("ConfigMap", pod.Namespace, env.ValueFrom.ConfigMapKeyRef.Name),
					EdgeUsesConfigMap, map[string]string{"as": "env", "key": env.Name})
			}
			if env.ValueFrom.SecretKeyRef != nil {
				g.addEdge(id, nodeKey("Secret", pod.Namespace, env.ValueFrom.SecretKeyRef.Name),
					EdgeUsesSecret, map[string]string{"as": "env", "key": env.Name})
			}
		}
	}
}

// RemovePod removes a pod from the topology.
func (g *Graph) RemovePod(namespace, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := nodeKey("Pod", namespace, name)
	delete(g.nodes, id)
	delete(g.edges, id)
}

// UpsertDeployment updates the topology from a deployment event.
func (g *Graph) UpsertDeployment(dep *appsv1.Deployment) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := nodeKey("Deployment", dep.Namespace, dep.Name)
	g.nodes[id] = &Node{
		ID: id, Kind: "Deployment",
		Namespace: dep.Namespace, Name: dep.Name,
		UID: string(dep.UID), Labels: dep.Labels,
		UpdatedAt: time.Now(),
	}
}

// ComputeServiceEdges resolves Service selector → matching Pods and upserts edges.
// Must be called after both the service and pods are synced.
func (g *Graph) ComputeServiceEdges(svc *corev1.Service, podList []*corev1.Pod) {
	if svc.Spec.Selector == nil || len(svc.Spec.Selector) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	svcID := nodeKey("Service", svc.Namespace, svc.Name)
	g.nodes[svcID] = &Node{
		ID: svcID, Kind: "Service",
		Namespace: svc.Namespace, Name: svc.Name,
		UID: string(svc.UID), Labels: svc.Labels,
		UpdatedAt: time.Now(),
	}

	for _, pod := range podList {
		if pod.Namespace != svc.Namespace {
			continue
		}
		if labelsMatch(svc.Spec.Selector, pod.Labels) {
			podID := nodeKey("Pod", pod.Namespace, pod.Name)
			g.addEdge(svcID, podID, EdgeSelectedBy, nil)
		}
	}
}

// UpsertIngress adds Ingress → Service edges.
func (g *Graph) UpsertIngress(ing *networkingv1.Ingress) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ingID := nodeKey("Ingress", ing.Namespace, ing.Name)
	g.nodes[ingID] = &Node{
		ID: ingID, Kind: "Ingress",
		Namespace: ing.Namespace, Name: ing.Name,
		UID: string(ing.UID),
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil {
				continue
			}
			svcID := nodeKey("Service", ing.Namespace, path.Backend.Service.Name)
			g.addEdge(ingID, svcID, EdgeRoutesTo, map[string]string{
				"host": rule.Host,
				"path": path.Path,
			})
		}
	}
}

// GetUpstreamChain returns the nodes reachable upstream from a given node ID,
// up to maxDepth hops. Used by the RCA engine for dependency traversal.
func (g *Graph) GetUpstreamChain(startID string, maxDepth int) []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := map[string]bool{startID: true}
	queue := []string{startID}
	var result []Node
	depth := 0

	for len(queue) > 0 && depth < maxDepth {
		next := []string{}
		for _, id := range queue {
			for _, edge := range g.edges[id] {
				if !visited[edge.TargetID] {
					visited[edge.TargetID] = true
					next = append(next, edge.TargetID)
					if n, ok := g.nodes[edge.TargetID]; ok {
						result = append(result, *n)
					}
				}
			}
		}
		queue = next
		depth++
	}
	return result
}

// addEdge is called with g.mu held.
func (g *Graph) addEdge(sourceID, targetID string, t EdgeType, meta map[string]string) {
	g.edges[sourceID] = append(g.edges[sourceID], Edge{
		SourceID: sourceID,
		TargetID: targetID,
		Type:     t,
		Metadata: meta,
	})
}

func nodeKey(kind, namespace, name string) string {
	if namespace == "" {
		return kind + "/" + name
	}
	return fmt.Sprintf("%s/%s/%s", kind, namespace, name)
}

func labelsMatch(selector, labels map[string]string) bool {
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
