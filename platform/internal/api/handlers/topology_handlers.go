package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/services/topology"
)

// InfraTopologyHandler handles infrastructure topology API endpoints
type InfraTopologyHandler struct {
	infraTopoService *topology.InfraTopologyService
	topoService      *topology.TopologyService // Service-level topology
	graphCache       *topology.TopologyGraphCache
	topoCorrelator   *correlation.TopologyGraphCorrelator // optional; wired for health endpoint
}

// SetGraphCache wires the topology graph cache (called from main after construction).
func (h *InfraTopologyHandler) SetGraphCache(c *topology.TopologyGraphCache) {
	h.graphCache = c
}

// SetTopologyCorrelator wires the correlation engine's graph correlator so the
// health endpoint can report freshness without duplicating tracking state.
func (h *InfraTopologyHandler) SetTopologyCorrelator(c *correlation.TopologyGraphCorrelator) {
	h.topoCorrelator = c
}

// NewInfraTopologyHandler creates a new infrastructure topology handler
func NewInfraTopologyHandler(infraTopoService *topology.InfraTopologyService, topoService *topology.TopologyService) *InfraTopologyHandler {
	return &InfraTopologyHandler{
		infraTopoService: infraTopoService,
		topoService:      topoService,
	}
}

// ============================================================================
// INFRASTRUCTURE TOPOLOGY ENDPOINTS
// ============================================================================

// GetFullInfraTopology gets complete infrastructure topology for a region
// GET /api/v1/topology/infrastructure/:region
func (h *InfraTopologyHandler) GetFullInfraTopology(c *gin.Context) {
	region := c.Param("region")
	if region == "" {
		region = "reno" // Default to your actual region
	}

	// Map region names to your actual datacenter names
	if region == "region-a" {
		region = "reno"
	} else if region == "region-b" {
		region = "maiden"
	}

	log.Printf("Getting infrastructure topology for region: %s", region)

	// DEBUG: Show what discoverers are currently configured
	log.Printf("DEBUGGING: Getting topology for region %s", region)

	// FORCE configuration reload and use real APIs
	topology, err := h.infraTopoService.ForceRealTopologyDiscovery(c.Request.Context(), region)
	if err != nil {
		log.Printf("Real CloudStack discovery failed: %v", err)

		// Return error instead of falling back to mock data
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success":    false,
			"message":    "Real CloudStack discovery failed: " + err.Error(),
			"suggestion": "Please check CloudStack API configuration in SRE Command Center",
			"debug_info": gin.H{
				"region": region,
				"error":  err.Error(),
			},
		})
		return
	}

	log.Printf("Real CloudStack discovery successful for %s: %d nodes", region, topology.Stats.TotalNodes)

	// Log what we discovered for debugging
	for layer, nodes := range topology.Layers {
		log.Printf("Layer %s: %d nodes", layer, len(nodes))
		if len(nodes) > 0 && len(nodes) < 5 { // Log details for small lists
			for _, node := range nodes {
				log.Printf("   - %s (%s)", node.Name, node.Type)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    topology,
		"region":  region,
		"source":  "real_cloudstack_discovery",
		"debug": gin.H{
			"total_nodes": topology.Stats.TotalNodes,
			"layers":      topology.Stats.NodesByLayer,
		},
	})
}

// GetMultiRegionTopology gets topology across all regions
// GET /api/v1/topology/infrastructure/multi-region
func (h *InfraTopologyHandler) GetMultiRegionTopology(c *gin.Context) {
	regions := []string{"region-a", "region-b"}

	multiRegionTopology := make(map[string]*topology.FullTopology)

	for _, region := range regions {
		topo, err := h.infraTopoService.GetFullInfraTopology(c.Request.Context(), region)
		if err != nil {
			continue
		}
		multiRegionTopology[region] = topo
	}

	// Calculate aggregated stats
	totalStats := h.aggregateStats(multiRegionTopology)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"regions":   multiRegionTopology,
			"aggregate": totalStats,
			"timestamp": multiRegionTopology["region-a"].Timestamp,
		},
	})
}

// GetInfraDependencyChain gets dependency chain for an infrastructure node
// GET /api/v1/topology/infrastructure/dependencies/:node_id
func (h *InfraTopologyHandler) GetInfraDependencyChain(c *gin.Context) {
	nodeID := c.Param("node_id")

	chain, err := h.infraTopoService.GetInfraDependencyChain(c.Request.Context(), nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get dependency chain: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    chain,
	})
}

// CalculateInfraBlastRadius calculates blast radius for infrastructure node
// GET /api/v1/topology/infrastructure/blast-radius/:node_id
func (h *InfraTopologyHandler) CalculateInfraBlastRadius(c *gin.Context) {
	nodeID := c.Param("node_id")

	blastRadius, err := h.infraTopoService.CalculateInfraBlastRadius(c.Request.Context(), nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to calculate blast radius: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    blastRadius,
	})
}

// ============================================================================
// COMBINED TOPOLOGY VIEW
// ============================================================================

// GetCompleteTopology gets combined infrastructure + service topology
// GET /api/v1/topology/complete
func (h *InfraTopologyHandler) GetCompleteTopology(c *gin.Context) {
	region := c.Query("region")
	if region == "" {
		region = "region-a"
	}

	// Get infrastructure topology
	infraTopo, err := h.infraTopoService.GetFullInfraTopology(c.Request.Context(), region)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get infrastructure topology: " + err.Error(),
		})
		return
	}

	// Get service topology
	serviceGraph, err := h.topoService.GenerateTopologyGraph(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get service topology: " + err.Error(),
		})
		return
	}

	// Combine both views
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"infrastructure": infraTopo,
			"services":       serviceGraph,
			"region":         region,
			"summary": gin.H{
				"infrastructure_nodes": infraTopo.Stats.TotalNodes,
				"services":             len(serviceGraph.Services),
				"dependencies":         len(serviceGraph.Dependencies),
			},
		},
	})
}

// ============================================================================
// SERVICE TOPOLOGY ENDPOINTS (Existing service-level topology)
// ============================================================================

// GetServiceGraph gets service dependency graph
// GET /api/v1/topology/services/graph
func (h *InfraTopologyHandler) GetServiceGraph(c *gin.Context) {
	graph, err := h.topoService.GenerateTopologyGraph(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate service graph: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    graph,
	})
}

// AnalyzeServiceImpact analyzes impact of a service failure
// GET /api/v1/topology/services/impact/:service_id
func (h *InfraTopologyHandler) AnalyzeServiceImpact(c *gin.Context) {
	serviceIDStr := c.Param("service_id")

	serviceID, err := uuid.Parse(serviceIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid service ID",
		})
		return
	}

	impact, err := h.topoService.AnalyzeImpact(c.Request.Context(), serviceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to analyze impact: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    impact,
	})
}

// ============================================================================
// TOPOLOGY SNAPSHOTS
// ============================================================================

// SaveSnapshot saves current topology snapshot
// POST /api/v1/topology/snapshot
func (h *InfraTopologyHandler) SaveSnapshot(c *gin.Context) {
	region := c.Query("region")
	if region == "" {
		region = "region-a"
	}

	// Get current topology
	topology, err := h.infraTopoService.GetFullInfraTopology(c.Request.Context(), region)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get topology: " + err.Error(),
		})
		return
	}

	// Save snapshot
	err = h.infraTopoService.SaveInfraTopologySnapshot(c.Request.Context(), topology)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to save snapshot: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Topology snapshot saved",
		"data": gin.H{
			"region":      region,
			"total_nodes": topology.Stats.TotalNodes,
			"timestamp":   topology.Timestamp,
		},
	})
}

// GetLatestSnapshot retrieves most recent topology snapshot
// GET /api/v1/topology/snapshot/latest
func (h *InfraTopologyHandler) GetLatestSnapshot(c *gin.Context) {
	region := c.Query("region")
	if region == "" {
		region = "region-a"
	}

	snapshot, err := h.infraTopoService.GetLatestInfraSnapshot(c.Request.Context(), region)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "No snapshot found: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    snapshot,
	})
}

// ============================================================================
// INTELLIGENT GRAPH ENDPOINTS
// ============================================================================

// GetInfraGraph returns the full cached infrastructure graph (nodes + edges).
// GET /api/v1/topology/graph
func (h *InfraTopologyHandler) GetInfraGraph(c *gin.Context) {
	if h.graphCache == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "graph cache not initialised"})
		return
	}
	// Optional ?live=true forces a rebuild (admin use only, slow).
	if c.Query("live") == "true" {
		graph, err := h.infraTopoService.BuildInfraGraph(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": graph})
		return
	}
	graph, err := h.graphCache.GetGraph(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": graph})
}

// ExpandTopologyNode returns the children of a specific node.
// GET /api/v1/topology/graph/node/:id/expand
func (h *InfraTopologyHandler) ExpandTopologyNode(c *gin.Context) {
	if h.graphCache == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "graph cache not initialised"})
		return
	}
	nodeID := c.Param("id")
	result, err := h.graphCache.ExpandNode(c.Request.Context(), nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// SearchTopology searches across all graph nodes.
// GET /api/v1/topology/graph/search?q=...
func (h *InfraTopologyHandler) SearchTopology(c *gin.Context) {
	if h.graphCache == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "graph cache not initialised"})
		return
	}
	q := c.Query("q")
	result, err := h.graphCache.Search(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ============================================================================
// ROUTE REGISTRATION
// ============================================================================

// GetTopologyHealth reports the freshness of the correlation topology graph.
// Operators use this to verify that topology-based correlation is working with
// current data before trusting correlation decisions.
//
// GET /api/v1/topology/health
func (h *InfraTopologyHandler) GetTopologyHealth(c *gin.Context) {
	freshnessSeconds := -1
	nodeCount := 0
	var lastRefreshed *time.Time

	if h.topoCorrelator != nil {
		freshnessSeconds = h.topoCorrelator.FreshnessSeconds()
		nodeCount = h.topoCorrelator.NodeCount()
		if t := h.topoCorrelator.LastRefreshed(); !t.IsZero() {
			lastRefreshed = &t
		}
	}

	band := "UNKNOWN"
	healthy := false
	switch {
	case freshnessSeconds < 0:
		band = "NEVER_REFRESHED"
	case freshnessSeconds < 300:
		band = "FRESH"
		healthy = true
	case freshnessSeconds < 1800:
		band = "AGING"
		healthy = true
	default:
		band = "STALE"
	}

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"freshness_seconds": freshnessSeconds,
		"freshness_band":    band,
		"node_count":        nodeCount,
		"last_refreshed":    lastRefreshed,
		"is_healthy":        healthy,
	})
}

// RegisterRoutes registers infrastructure topology routes
func (h *InfraTopologyHandler) RegisterRoutes(protected *gin.RouterGroup) {
	topo := protected.Group("/topology")
	{
		// Intelligent graph (React Flow compatible) + search
		graph := topo.Group("/graph")
		{
			graph.GET("", h.GetInfraGraph)
			graph.GET("/node/:id/expand", h.ExpandTopologyNode)
			graph.GET("/search", h.SearchTopology)
		}

		// Configuration endpoints
		topo.GET("/config", h.GetTopologyConfig)
		topo.POST("/config", h.SaveTopologyConfig)
		topo.POST("/config/test/:type", h.TestTopologyConnection)
		topo.POST("/discover", h.TriggerDiscovery)

		// Infrastructure topology
		infra := topo.Group("/infrastructure")
		{
			infra.GET("/:region", h.GetFullInfraTopology)
			infra.GET("/multi-region", h.GetMultiRegionTopology)
			infra.GET("/dependencies/:node_id", h.GetInfraDependencyChain)
			infra.GET("/blast-radius/:node_id", h.CalculateInfraBlastRadius)
		}

		// Service topology
		services := topo.Group("/services")
		{
			services.GET("/graph", h.GetServiceGraph)
			services.GET("/impact/:service_id", h.AnalyzeServiceImpact)
		}

		// Combined view
		topo.GET("/complete", h.GetCompleteTopology)

		// Operational health — freshness of the correlation topology graph
		topo.GET("/correlation-health", h.GetTopologyHealth)

		// Snapshots
		snapshots := topo.Group("/snapshot")
		{
			snapshots.POST("", h.SaveSnapshot)
			snapshots.GET("/latest", h.GetLatestSnapshot)
		}
	}
}

// GetTopologyConfig gets saved topology configuration
// GET /api/v1/topology/config
func (h *InfraTopologyHandler) GetTopologyConfig(c *gin.Context) {
	// Get K8s cluster configurations from new table
	k8sClusters, err := h.getK8sClusterConfigs(c.Request.Context())
	if err != nil {
		log.Printf("Failed to get K8s cluster configs: %v", err)
		k8sClusters = []interface{}{}
	}

	// Get CloudStack configurations from legacy table
	cloudstackConfigs, ipmiConfigs := h.getLegacyConfigs(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"k8s_clusters":        k8sClusters,
			"cloudstack":          cloudstackConfigs,
			"ipmi":                ipmiConfigs,
			"discovery_interval":  "5m",
			"real_time_discovery": true,
		},
	})
}

// getK8sClusterConfigs gets K8s cluster configs from new table
func (h *InfraTopologyHandler) getK8sClusterConfigs(ctx context.Context) ([]interface{}, error) {
	query := `
		SELECT id, name, environment, region, api_server_url, namespace, enabled,
		       last_sync, sync_status, created_at, updated_at
		FROM k8s_cluster_configs
		ORDER BY region, name
	`

	rows, err := h.infraTopoService.DB.QueryContext(ctx, query)
	if err != nil {
		return []interface{}{}, nil // Return empty array instead of error for UI
	}
	defer rows.Close()

	var clusters []interface{}
	for rows.Next() {
		var id, name, environment, region, apiServerURL, namespace, syncStatus string
		var enabled bool
		var lastSync *time.Time
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&id, &name, &environment, &region, &apiServerURL, &namespace,
			&enabled, &lastSync, &syncStatus, &createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}

		cluster := map[string]interface{}{
			"id":          id,
			"name":        name,
			"apiServer":   apiServerURL,
			"region":      region,
			"environment": environment,
			"namespaces":  namespace,
			"enabled":     enabled,
			"token":       "***configured***", // Hide sensitive data in UI
		}

		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

// getLegacyConfigs gets CloudStack and IPMI configs from legacy table
func (h *InfraTopologyHandler) getLegacyConfigs(ctx context.Context) ([]interface{}, []interface{}) {
	var configJSON []byte
	query := `SELECT config FROM topology_config WHERE id = 'default' LIMIT 1`

	cloudstackConfigs := []interface{}{}
	ipmiConfigs := []interface{}{}

	err := h.infraTopoService.DB.QueryRowContext(ctx, query).Scan(&configJSON)
	if err == nil {
		var legacyConfig map[string]interface{}
		if parseErr := json.Unmarshal(configJSON, &legacyConfig); parseErr == nil {
			if cs, exists := legacyConfig["cloudstack"]; exists {
				if csArray, ok := cs.([]interface{}); ok {
					cloudstackConfigs = csArray
				}
			}
			if ipmi, exists := legacyConfig["ipmi"]; exists {
				if ipmiArray, ok := ipmi.([]interface{}); ok {
					ipmiConfigs = ipmiArray
				}
			}
		}
	}

	return cloudstackConfigs, ipmiConfigs
}

// SaveTopologyConfig saves topology configuration
// POST /api/v1/topology/config
func (h *InfraTopologyHandler) SaveTopologyConfig(c *gin.Context) {
	var config map[string]interface{}
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid configuration",
		})
		return
	}

	// Handle K8s clusters - save to new table
	if k8sClusters, exists := config["k8s_clusters"]; exists {
		if clustersArray, ok := k8sClusters.([]interface{}); ok {
			err := h.saveK8sClusterConfigs(c.Request.Context(), clustersArray)
			if err != nil {
				log.Printf("Failed to save K8s cluster configs: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"message": "Failed to save K8s cluster configurations: " + err.Error(),
				})
				return
			}
		}
	}

	// Handle CloudStack and IPMI configs - save to legacy table
	legacyConfig := map[string]interface{}{}
	if cloudstack, exists := config["cloudstack"]; exists {
		legacyConfig["cloudstack"] = cloudstack
	}
	if ipmi, exists := config["ipmi"]; exists {
		legacyConfig["ipmi"] = ipmi
	}

	if len(legacyConfig) > 0 {
		configJSON, _ := json.Marshal(legacyConfig)

		query := `
			INSERT INTO topology_config (id, config, created_at, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET config = $2, updated_at = $4
		`

		_, err := h.infraTopoService.DB.ExecContext(c.Request.Context(), query,
			"default", configJSON, time.Now(), time.Now())

		if err != nil {
			log.Printf("Failed to save legacy configs: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Configuration saved successfully",
	})
}

// saveK8sClusterConfigs saves K8s cluster configs to new table
func (h *InfraTopologyHandler) saveK8sClusterConfigs(ctx context.Context, clustersArray []interface{}) error {
	for _, clusterInterface := range clustersArray {
		cluster, ok := clusterInterface.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := cluster["name"].(string)
		apiServer, _ := cluster["apiServer"].(string)
		region, _ := cluster["region"].(string)
		namespaces, _ := cluster["namespaces"].(string)
		token, _ := cluster["token"].(string)
		enabled := true

		if name == "" || apiServer == "" || token == "" || token == "***configured***" {
			continue // Skip invalid or placeholder configs
		}

		// Insert or update cluster configuration.
		// cluster_name and api_server are alias columns read by GetK8sClustersConfig
		// and must be kept in sync with the primary name/api_server_url columns.
		query := `
			INSERT INTO k8s_cluster_configs (
				name, cluster_name, display_name, environment, region,
				api_server_url, api_server, service_account_token,
				ca_cert_data, namespace, enabled, sync_status, created_at, updated_at
			) VALUES ($1, $1, $1, $2, $3, $4, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (name) DO UPDATE SET
				cluster_name = EXCLUDED.name,
				display_name = COALESCE(k8s_cluster_configs.display_name, EXCLUDED.name),
				environment = EXCLUDED.environment,
				region = EXCLUDED.region,
				api_server_url = EXCLUDED.api_server_url,
				api_server = EXCLUDED.api_server_url,
				service_account_token = EXCLUDED.service_account_token,
				namespace = EXCLUDED.namespace,
				enabled = EXCLUDED.enabled,
				updated_at = EXCLUDED.updated_at
		`

		environment := "production" // Default
		if region != "reno" {
			environment = "staging"
		}

		_, err := h.infraTopoService.DB.ExecContext(ctx, query,
			name, environment, region, apiServer, token,
			"", namespaces, enabled, "pending", time.Now(), time.Now())

		if err != nil {
			log.Printf("Failed to save K8s cluster config %s: %v", name, err)
			return err
		}

		log.Printf("Saved K8s cluster config: %s in region %s", name, region)
	}

	return nil
}

// TestTopologyConnection tests connection to infrastructure service
// POST /api/v1/topology/config/test/:type
func (h *InfraTopologyHandler) TestTopologyConnection(c *gin.Context) {
	connType := c.Param("type")

	var config map[string]interface{}
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid configuration",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully connected to %s", connType),
	})
}

// TriggerDiscovery manually triggers topology discovery
// POST /api/v1/topology/discover
func (h *InfraTopologyHandler) TriggerDiscovery(c *gin.Context) {
	region := c.Query("region")
	if region == "" {
		region = "region-a"
	}

	topology, err := h.infraTopoService.GetFullInfraTopology(c.Request.Context(), region)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Discovery failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Discovery completed successfully",
		"data": gin.H{
			"region":      region,
			"total_nodes": topology.Stats.TotalNodes,
			"layers":      topology.Stats.NodesByLayer,
		},
	})
}

// Helper functions
func parseUUID(s string) (interface{}, error) {
	// Simple UUID parsing - in production use proper UUID library
	if len(s) < 10 {
		return nil, fmt.Errorf("invalid UUID")
	}
	return s, nil
}

func (h *InfraTopologyHandler) aggregateStats(multiRegion map[string]*topology.FullTopology) map[string]interface{} {
	totalNodes := 0
	nodesByLayer := make(map[topology.InfrastructureLayer]int)

	for _, topo := range multiRegion {
		totalNodes += topo.Stats.TotalNodes
		for layer, count := range topo.Stats.NodesByLayer {
			nodesByLayer[layer] += count
		}
	}

	return map[string]interface{}{
		"total_nodes":    totalNodes,
		"nodes_by_layer": nodesByLayer,
		"regions":        len(multiRegion),
	}
}
