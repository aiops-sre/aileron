package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/topology"
)

// K8sClusterSyncHandler handles syncing K8s cluster statistics with real topology data
type K8sClusterSyncHandler struct {
	infraTopoService *topology.InfraTopologyService
}

// NewK8sClusterSyncHandler creates K8s cluster sync handler
func NewK8sClusterSyncHandler(infraTopoService *topology.InfraTopologyService) *K8sClusterSyncHandler {
	return &K8sClusterSyncHandler{
		infraTopoService: infraTopoService,
	}
}

// K8sTopologyResponse represents the response from K8s Intelligence Service
type K8sTopologyResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Cluster    string        `json:"cluster"`
		Timestamp  string        `json:"timestamp"`
		Nodes      []interface{} `json:"nodes"`
		Namespaces []interface{} `json:"namespaces"`
		Pods       []interface{} `json:"pods"`
		Summary    struct {
			Cluster     string `json:"cluster"`
			Nodes       int    `json:"nodes"`
			Namespaces  int    `json:"namespaces"`
			Pods        int    `json:"pods"`
			ReadyNodes  int    `json:"ready_nodes"`
			RunningPods int    `json:"running_pods"`
		} `json:"summary"`
	} `json:"data"`
}

// SyncClusterTopology syncs a specific cluster's topology data
// POST /api/v1/topology/k8s-clusters/{cluster}/sync
func (h *K8sClusterSyncHandler) SyncClusterTopology(c *gin.Context) {
	clusterName := c.Param("cluster")
	if clusterName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Cluster name is required",
		})
		return
	}

	log.Printf("Syncing topology for cluster: %s", clusterName)

	// Call K8s Intelligence Service to get real topology
	k8sServiceURL := fmt.Sprintf("http://localhost:8001/api/v1/clusters/%s/topology", clusterName)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", k8sServiceURL, nil)
	if err != nil {
		log.Printf("Failed to create K8s service request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create request to K8s service: " + err.Error(),
		})
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to call K8s Intelligence Service: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "K8s Intelligence Service unavailable: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read K8s service response: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to read K8s service response: " + err.Error(),
		})
		return
	}

	var topologyResp K8sTopologyResponse
	if err := json.Unmarshal(body, &topologyResp); err != nil {
		log.Printf("Failed to parse K8s service response: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to parse K8s service response: " + err.Error(),
		})
		return
	}

	if !topologyResp.Success {
		log.Printf("K8s service returned error for cluster %s", clusterName)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "K8s service failed to get topology for cluster: " + clusterName,
		})
		return
	}

	// Update database with real topology statistics
	updateQuery := `
		UPDATE k8s_cluster_configs 
		SET 
			node_count = $1,
			namespace_count = $2,
			pod_count = $3,
			status = $4,
			last_discovery = NOW(),
			updated_at = NOW()
		WHERE cluster_name = $5
		RETURNING id, display_name, updated_at
	`

	var clusterID, displayName string
	var updatedAt time.Time

	status := "healthy"
	if topologyResp.Data.Summary.ReadyNodes != topologyResp.Data.Summary.Nodes {
		status = "degraded"
	}

	err = h.infraTopoService.DB.QueryRowContext(c.Request.Context(), updateQuery,
		topologyResp.Data.Summary.Nodes,
		topologyResp.Data.Summary.Namespaces,
		topologyResp.Data.Summary.Pods,
		status,
		clusterName,
	).Scan(&clusterID, &displayName, &updatedAt)

	if err != nil {
		log.Printf("Failed to update cluster statistics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update cluster statistics: " + err.Error(),
		})
		return
	}

	log.Printf("Successfully synced cluster %s: %d nodes, %d pods, %d namespaces",
		clusterName,
		topologyResp.Data.Summary.Nodes,
		topologyResp.Data.Summary.Pods,
		topologyResp.Data.Summary.Namespaces)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Cluster topology synchronized successfully",
		"data": gin.H{
			"cluster_id":         clusterID,
			"cluster_name":       clusterName,
			"display_name":       displayName,
			"node_count":         topologyResp.Data.Summary.Nodes,
			"namespace_count":    topologyResp.Data.Summary.Namespaces,
			"pod_count":          topologyResp.Data.Summary.Pods,
			"ready_nodes":        topologyResp.Data.Summary.ReadyNodes,
			"running_pods":       topologyResp.Data.Summary.RunningPods,
			"status":             status,
			"synced_at":          updatedAt,
			"topology_timestamp": topologyResp.Data.Timestamp,
		},
	})
}

// SyncAllClusters syncs topology for all configured clusters
// POST /api/v1/topology/k8s-clusters/sync-all
func (h *K8sClusterSyncHandler) SyncAllClusters(c *gin.Context) {
	log.Printf("Syncing topology for all clusters...")

	// Get all clusters from database
	query := `
		SELECT cluster_name, display_name, discovery_enabled
		FROM k8s_cluster_configs
		WHERE discovery_enabled = true
		ORDER BY cluster_name
	`

	rows, err := h.infraTopoService.DB.QueryContext(c.Request.Context(), query)
	if err != nil {
		log.Printf("Failed to get clusters for sync: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get clusters: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	var clusters []string

	for rows.Next() {
		var clusterName, displayName string
		var discoveryEnabled bool

		err := rows.Scan(&clusterName, &displayName, &discoveryEnabled)
		if err != nil {
			log.Printf("Error scanning cluster row: %v", err)
			continue
		}

		clusters = append(clusters, clusterName)
	}

	log.Printf("Found %d clusters to sync", len(clusters))

	syncResults := make([]gin.H, 0, len(clusters))
	successCount := 0

	for _, clusterName := range clusters {
		log.Printf("Syncing cluster: %s", clusterName)

		// Directly sync cluster without using gin context
		syncResult := h.syncSingleCluster(c.Request.Context(), clusterName)

		if syncResult["success"].(bool) {
			successCount++
		}

		syncResults = append(syncResults, syncResult)
	}

	log.Printf("Cluster sync completed: %d/%d successful", successCount, len(clusters))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Synchronized %d/%d clusters successfully", successCount, len(clusters)),
		"data": gin.H{
			"total_clusters":   len(clusters),
			"successful_syncs": successCount,
			"failed_syncs":     len(clusters) - successCount,
			"sync_results":     syncResults,
			"sync_timestamp":   time.Now(),
		},
	})
}

// syncSingleCluster syncs a single cluster and returns result
func (h *K8sClusterSyncHandler) syncSingleCluster(ctx context.Context, clusterName string) gin.H {
	log.Printf("Syncing topology for cluster: %s", clusterName)

	// Call K8s Intelligence Service to get real topology
	k8sServiceURL := fmt.Sprintf("http://localhost:8001/api/v1/clusters/%s/topology", clusterName)

	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(syncCtx, "GET", k8sServiceURL, nil)
	if err != nil {
		log.Printf("Failed to create K8s service request for %s: %v", clusterName, err)
		return gin.H{
			"success":      false,
			"cluster_name": clusterName,
			"message":      "Failed to create request: " + err.Error(),
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to call K8s Intelligence Service for %s: %v", clusterName, err)
		return gin.H{
			"success":      false,
			"cluster_name": clusterName,
			"message":      "K8s service unavailable: " + err.Error(),
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read K8s service response for %s: %v", clusterName, err)
		return gin.H{
			"success":      false,
			"cluster_name": clusterName,
			"message":      "Failed to read response: " + err.Error(),
		}
	}

	var topologyResp K8sTopologyResponse
	if err := json.Unmarshal(body, &topologyResp); err != nil {
		log.Printf("Failed to parse K8s service response for %s: %v", clusterName, err)
		return gin.H{
			"success":      false,
			"cluster_name": clusterName,
			"message":      "Failed to parse response: " + err.Error(),
		}
	}

	if !topologyResp.Success {
		log.Printf("K8s service returned error for cluster %s", clusterName)
		return gin.H{
			"success":      false,
			"cluster_name": clusterName,
			"message":      "K8s service failed to get topology",
		}
	}

	// Update database with real topology statistics
	updateQuery := `
		UPDATE k8s_cluster_configs
		SET
			node_count = $1,
			namespace_count = $2,
			pod_count = $3,
			status = $4,
			last_discovery = NOW(),
			updated_at = NOW()
		WHERE cluster_name = $5
		RETURNING display_name, updated_at
	`

	var displayName string
	var updatedAt time.Time

	status := "healthy"
	if topologyResp.Data.Summary.ReadyNodes != topologyResp.Data.Summary.Nodes {
		status = "degraded"
	}

	err = h.infraTopoService.DB.QueryRowContext(ctx, updateQuery,
		topologyResp.Data.Summary.Nodes,
		topologyResp.Data.Summary.Namespaces,
		topologyResp.Data.Summary.Pods,
		status,
		clusterName,
	).Scan(&displayName, &updatedAt)

	if err != nil {
		log.Printf("Failed to update cluster statistics for %s: %v", clusterName, err)
		return gin.H{
			"success":      false,
			"cluster_name": clusterName,
			"message":      "Failed to update database: " + err.Error(),
		}
	}

	log.Printf("Successfully synced cluster %s: %d nodes, %d pods, %d namespaces",
		clusterName,
		topologyResp.Data.Summary.Nodes,
		topologyResp.Data.Summary.Pods,
		topologyResp.Data.Summary.Namespaces)

	return gin.H{
		"success":      true,
		"cluster_name": clusterName,
		"display_name": displayName,
		"message":      "Cluster synced successfully",
		"data": gin.H{
			"node_count":      topologyResp.Data.Summary.Nodes,
			"namespace_count": topologyResp.Data.Summary.Namespaces,
			"pod_count":       topologyResp.Data.Summary.Pods,
			"ready_nodes":     topologyResp.Data.Summary.ReadyNodes,
			"running_pods":    topologyResp.Data.Summary.RunningPods,
			"status":          status,
			"synced_at":       updatedAt,
		},
	}
}

// RegisterSyncRoutes registers K8s cluster sync routes
func (h *K8sClusterSyncHandler) RegisterSyncRoutes(protected *gin.RouterGroup) {
	topology := protected.Group("/topology")
	{
		topology.POST("/k8s-clusters/:cluster/sync", h.SyncClusterTopology)
		topology.POST("/k8s-clusters/sync-all", h.SyncAllClusters)
	}
}
