package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/topology"
)

// TopologyHandler handles topology configuration endpoints
type TopologyHandler struct {
	k8sService *topology.KubernetesTopologyService
}

// NewTopologyHandler creates a new topology handler
func NewTopologyHandler(k8sService *topology.KubernetesTopologyService) *TopologyHandler {
	return &TopologyHandler{
		k8sService: k8sService,
	}
}

// RegisterRoutes registers topology configuration endpoints
func (th *TopologyHandler) RegisterRoutes(router *gin.RouterGroup) {
	topology := router.Group("/topology")
	{
		// Cluster management
		topology.GET("/clusters", th.GetClusters)
		topology.POST("/clusters", th.CreateCluster)
		topology.PUT("/clusters/:id", th.UpdateCluster)
		topology.DELETE("/clusters/:id", th.DeleteCluster)

		// Discovery management
		topology.POST("/discovery", th.TriggerDiscoveryAll)
		topology.POST("/discovery/:clusterName", th.TriggerDiscovery)

		// Environment configurations
		topology.GET("/environments", th.GetEnvironments)
		topology.PUT("/environments/:id", th.UpdateEnvironment)

		// Service mappings
		topology.GET("/mappings", th.GetMappings)
		topology.PUT("/mappings/:id", th.UpdateMapping)

		// Health status
		topology.GET("/health", th.GetHealth)
	}
}

// GetClusters retrieves all cluster configurations
func (th *TopologyHandler) GetClusters(c *gin.Context) {
	clusters := th.k8sService.GetClusterConfigurations()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"clusters": clusters,
			"total":    len(clusters),
		},
	})
}

// CreateCluster creates a new cluster configuration
func (th *TopologyHandler) CreateCluster(c *gin.Context) {
	var req topology.KubernetesClusterConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	err := th.k8sService.AddClusterConfiguration(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to create cluster: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Cluster configuration created successfully",
		"data":    req,
	})
}

// UpdateCluster updates an existing cluster configuration
func (th *TopologyHandler) UpdateCluster(c *gin.Context) {
	clusterID := c.Param("id")

	var req topology.KubernetesClusterConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	// Parse and set the cluster ID
	id, err := uuid.Parse(clusterID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid cluster ID",
		})
		return
	}
	req.ID = id

	// For updates, we remove and re-add the cluster
	err = th.k8sService.RemoveClusterConfiguration(c.Request.Context(), req.Name)
	if err != nil {
		// If cluster doesn't exist, continue with creation
	}

	err = th.k8sService.AddClusterConfiguration(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to update cluster: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Cluster configuration updated successfully",
		"data":    req,
	})
}

// DeleteCluster removes a cluster configuration
func (th *TopologyHandler) DeleteCluster(c *gin.Context) {
	clusterID := c.Param("id")

	// Find cluster by ID to get name
	clusters := th.k8sService.GetClusterConfigurations()
	var clusterName string

	for _, cluster := range clusters {
		if cluster.ID.String() == clusterID {
			clusterName = cluster.Name
			break
		}
	}

	if clusterName == "" {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Cluster not found",
		})
		return
	}

	err := th.k8sService.RemoveClusterConfiguration(c.Request.Context(), clusterName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to delete cluster: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Cluster configuration deleted successfully",
	})
}

// TriggerDiscovery triggers discovery for a specific cluster
func (th *TopologyHandler) TriggerDiscovery(c *gin.Context) {
	clusterName := c.Param("clusterName")

	results, err := th.k8sService.DiscoverTopology(c.Request.Context(), clusterName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Discovery failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Discovery completed successfully",
		"data":    results,
	})
}

// TriggerDiscoveryAll triggers discovery for all clusters
func (th *TopologyHandler) TriggerDiscoveryAll(c *gin.Context) {
	results, err := th.k8sService.DiscoverTopology(c.Request.Context(), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Discovery failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Discovery completed for all clusters",
		"data":    results,
	})
}

// GetEnvironments retrieves environment configurations
func (th *TopologyHandler) GetEnvironments(c *gin.Context) {
	// Mock environment configurations - would integrate with database
	environments := []gin.H{
		{
			"id":                         uuid.New(),
			"environment":                "production",
			"description":                "Production environment with full automation",
			"auto_discovery_enabled":     true,
			"auto_mapping_enabled":       true,
			"discovery_interval_minutes": 5,
			"retention_days":             30,
			"alert_correlation_enabled":  true,
			"incident_auto_creation":     true,
			"notification_channels":      []string{"slack", "email", "pagerduty"},
		},
		{
			"id":                         uuid.New(),
			"environment":                "staging",
			"description":                "Staging environment with limited automation",
			"auto_discovery_enabled":     true,
			"auto_mapping_enabled":       true,
			"discovery_interval_minutes": 10,
			"retention_days":             14,
			"alert_correlation_enabled":  true,
			"incident_auto_creation":     false,
			"notification_channels":      []string{"slack", "email"},
		},
		{
			"id":                         uuid.New(),
			"environment":                "development",
			"description":                "Development environment with basic discovery",
			"auto_discovery_enabled":     true,
			"auto_mapping_enabled":       false,
			"discovery_interval_minutes": 15,
			"retention_days":             7,
			"alert_correlation_enabled":  false,
			"incident_auto_creation":     false,
			"notification_channels":      []string{"slack"},
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"environments": environments,
		},
	})
}

// UpdateEnvironment updates environment configuration
func (th *TopologyHandler) UpdateEnvironment(c *gin.Context) {
	environmentID := c.Param("id")

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	// In a real implementation, this would update the database
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Environment configuration updated successfully",
		"data":    gin.H{"id": environmentID, "config": req},
	})
}

// GetMappings retrieves service mappings
func (th *TopologyHandler) GetMappings(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// Mock service mappings - would integrate with database
	mappings := []gin.H{
		{
			"id":                  uuid.New(),
			"source_type":         "kubernetes",
			"source_identifier":   "alerthub-backend",
			"source_cluster":      "mps-sandbox-rno",
			"source_namespace":    "alerthub-system",
			"target_service_name": "AlertHub Backend API",
			"confidence_score":    0.95,
			"enabled":             true,
			"auto_created":        true,
			"last_mapped":         "2024-01-15T10:30:00Z",
		},
		{
			"id":                  uuid.New(),
			"source_type":         "kubernetes",
			"source_identifier":   "prometheus",
			"source_cluster":      "mps-sandbox-rno",
			"source_namespace":    "monitoring",
			"target_service_name": "Prometheus Monitoring",
			"confidence_score":    0.88,
			"enabled":             true,
			"auto_created":        true,
			"last_mapped":         "2024-01-15T09:15:00Z",
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"mappings": mappings,
			"total":    len(mappings),
			"page":     page,
			"limit":    limit,
		},
	})
}

// UpdateMapping updates service mapping configuration
func (th *TopologyHandler) UpdateMapping(c *gin.Context) {
	mappingID := c.Param("id")

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	// In a real implementation, this would update the database
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Service mapping updated successfully",
		"data":    gin.H{"id": mappingID, "config": req},
	})
}

// GetHealth retrieves cluster health status
func (th *TopologyHandler) GetHealth(c *gin.Context) {
	clusters := th.k8sService.GetClusterConfigurations()

	// Mock health data - would integrate with actual health metrics
	healthData := make([]gin.H, 0, len(clusters))

	for _, cluster := range clusters {
		healthData = append(healthData, gin.H{
			"cluster_name":        cluster.Name,
			"environment":         cluster.Environment,
			"region":              cluster.Region,
			"enabled":             cluster.Enabled,
			"sync_status":         cluster.SyncStatus,
			"last_sync":           cluster.LastSync,
			"latest_health_score": 0.85, // Mock score
			"nodes_count":         3,    // Mock count
			"pods_count":          45,   // Mock count
			"services_count":      12,   // Mock count
			"last_discovery":      cluster.LastSync,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"health": healthData,
		},
	})
}
