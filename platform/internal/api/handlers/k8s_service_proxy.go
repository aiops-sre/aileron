package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/topology"
)

// K8sServiceProxyHandler serves Kubernetes Intelligence API requests directly
// using KubernetesTopologyService — no external microservice required.
type K8sServiceProxyHandler struct {
	k8sTopologyService *topology.KubernetesTopologyService
}

// NewK8sServiceProxyHandler creates the handler. The second string parameter
// (legacy serviceURL) is kept for signature compatibility but ignored.
func NewK8sServiceProxyHandler(_ string) *K8sServiceProxyHandler {
	return &K8sServiceProxyHandler{}
}

// SetK8sTopologyService wires the topology service after construction.
func (h *K8sServiceProxyHandler) SetK8sTopologyService(svc *topology.KubernetesTopologyService) {
	h.k8sTopologyService = svc
}

// ProxyToK8sService handles all /api/v1/k8s-service/* requests directly.
func (h *K8sServiceProxyHandler) ProxyToK8sService(c *gin.Context) {
	path := c.Param("path")

	// Health check
	if path == "/health" || path == "/api/v1/health" {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "kubernetes-intelligence"})
		return
	}

	if h.k8sTopologyService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "Kubernetes topology service not initialized",
		})
		return
	}

	// Match /api/v1/clusters/{name}/topology
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "clusters" && parts[4] == "topology" {
		clusterName := parts[3]
		h.handleClusterTopology(c, clusterName)
		return
	}

	// Match /api/v1/clusters (list all)
	if path == "/api/v1/clusters" || path == "/api/v1/clusters/" {
		h.handleListClusters(c)
		return
	}

	// Match /api/v1/clusters/{name}/pods/{pod}/logs
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "clusters" && parts[4] == "pods" && parts[6] == "logs" {
		clusterName := parts[3]
		podName := parts[5]
		namespace := c.DefaultQuery("namespace", "default")
		container := c.Query("container")
		tailLines := 200
		if tl := c.Query("tail_lines"); tl != "" {
			if n, err := strconv.Atoi(tl); err == nil && n > 0 && n <= 5000 {
				tailLines = n
			}
		}
		h.handlePodLogs(c, clusterName, podName, namespace, container, tailLines)
		return
	}

	// Match /api/v1/clusters/{name}/events
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "clusters" && parts[4] == "events" {
		clusterName := parts[3]
		namespace := c.Query("namespace")
		sinceMinutes := 60
		if sm := c.Query("since_minutes"); sm != "" {
			if n, err := strconv.Atoi(sm); err == nil && n > 0 {
				sinceMinutes = n
			}
		}
		h.handleClusterEvents(c, clusterName, namespace, sinceMinutes)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "endpoint not found: " + path})
}

func (h *K8sServiceProxyHandler) handleClusterTopology(c *gin.Context, clusterName string) {
	results, err := h.k8sTopologyService.DiscoverTopology(c.Request.Context(), clusterName)
	if err != nil {
		log.Printf("handleTopologyDiscover: cluster=%s: %v", clusterName, err)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "topology data unavailable for cluster",
		})
		return
	}
	if len(results) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "no topology data for cluster: " + clusterName,
		})
		return
	}
	r := results[0]
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"cluster":                  r.ClusterName,
			"timestamp":                r.DiscoveredAt.Format(time.RFC3339),
			"nodes":                    r.Nodes,
			"namespaces":               r.Namespaces,
			"pods":                     r.Pods,
			"services":                 r.Services,
			"deployments":              r.Deployments,
			"ingresses":                r.Ingresses,
			"persistent_volumes":       r.PersistentVolumes,
			"persistent_volume_claims": r.PersistentVolumeClaims,
			"summary": gin.H{
				"cluster":             r.ClusterName,
				"nodes":               r.Summary.TotalNodes,
				"ready_nodes":         r.Summary.ReadyNodes,
				"namespaces":          r.Summary.NamespaceCount,
				"pods":                r.Summary.TotalPods,
				"running_pods":        r.Summary.RunningPods,
				"deployments":         r.Summary.TotalDeployments,
				"healthy_deployments": r.Summary.HealthyDeployments,
				"services":            r.Summary.TotalServices,
				"ingresses":           r.Summary.TotalIngresses,
				"total_pvcs":          r.Summary.TotalPVCs,
				"bound_pvcs":          r.Summary.BoundPVCs,
				"pending_pvcs":        r.Summary.PendingPVCs,
				"total_storage_gb":    r.Summary.TotalStorageGB,
				"storage_health":      r.Summary.StorageHealth,
			},
		},
	})
}

func (h *K8sServiceProxyHandler) handlePodLogs(c *gin.Context, clusterName, podName, namespace, container string, tailLines int) {
	if h.k8sTopologyService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "service not initialized"})
		return
	}
	logs, err := h.k8sTopologyService.GetPodLogs(c.Request.Context(), clusterName, namespace, podName, container, tailLines)
	if err != nil {
		log.Printf("handlePodLogs: cluster=%s ns=%s pod=%s: %v", clusterName, namespace, podName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"logs": logs, "pod": podName, "container": container, "tail_lines": tailLines}})
}

func (h *K8sServiceProxyHandler) handleClusterEvents(c *gin.Context, clusterName, namespace string, sinceMinutes int) {
	if h.k8sTopologyService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "service not initialized"})
		return
	}
	events, err := h.k8sTopologyService.GetClusterEvents(c.Request.Context(), clusterName, namespace, sinceMinutes)
	if err != nil {
		log.Printf("handleClusterEvents: cluster=%s ns=%s: %v", clusterName, namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "internal server error"})
		return
	}
	if events == nil {
		events = []topology.K8sEvent{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"events":        events,
			"cluster":       clusterName,
			"since_minutes": sinceMinutes,
			"timestamp":     time.Now(),
		},
	})
}

func (h *K8sServiceProxyHandler) handleListClusters(c *gin.Context) {
	configs := h.k8sTopologyService.GetClusterConfigurations()
	clusters := make([]gin.H, 0, len(configs))
	for _, cfg := range configs {
		clusters = append(clusters, gin.H{
			"name":        cfg.Name,
			"environment": cfg.Environment,
			"region":      cfg.Region,
			"api_server":  cfg.APIServerURL,
			"enabled":     cfg.Enabled,
			"sync_status": cfg.SyncStatus,
			"last_sync":   cfg.LastSync,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"clusters": clusters}})
}

// GetK8sServiceStatus returns the status of the Kubernetes topology service.
func (h *K8sServiceProxyHandler) GetK8sServiceStatus(c *gin.Context) {
	if h.k8sTopologyService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"service": "kubernetes-intelligence",
			"status":  "not initialized",
		})
		return
	}
	configs := h.k8sTopologyService.GetClusterConfigurations()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"service":       "kubernetes-intelligence",
			"status":        "ok",
			"cluster_count": len(configs),
		},
	})
}

// RegisterK8sProxyRoutes registers the K8s service proxy routes.
func (h *K8sServiceProxyHandler) RegisterK8sProxyRoutes(protected *gin.RouterGroup) {
	k8sProxy := protected.Group("/k8s-service")
	{
		k8sProxy.Any("/*path", h.ProxyToK8sService)
	}
}
