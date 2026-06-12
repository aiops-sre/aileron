package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/topology"
)

// EnhancedTopologyConfigHandler handles the new topology configuration system
type EnhancedTopologyConfigHandler struct {
	infraTopoService *topology.InfraTopologyService
}

// NewEnhancedTopologyConfigHandler creates enhanced topology config handler
func NewEnhancedTopologyConfigHandler(infraTopoService *topology.InfraTopologyService) *EnhancedTopologyConfigHandler {
	return &EnhancedTopologyConfigHandler{
		infraTopoService: infraTopoService,
	}
}

// K8sClusterConfig represents K8s cluster configuration
type K8sClusterConfig struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	ClusterName      string                 `json:"cluster_name"`
	DisplayName      string                 `json:"display_name"`
	APIServer        string                 `json:"api_server"`
	Region           string                 `json:"region"`
	Environment      string                 `json:"environment"`
	Enabled          bool                   `json:"enabled"`
	Version          string                 `json:"version,omitempty"`
	NodeCount        int                    `json:"node_count"`
	NamespaceCount   int                    `json:"namespace_count"`
	PodCount         int                    `json:"pod_count"`
	Status           string                 `json:"status"`
	ReadonlyMode     bool                   `json:"readonly_mode"`
	DiscoveryEnabled bool                   `json:"discovery_enabled"`
	Labels           map[string]interface{} `json:"labels,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
	LastDiscovery    *time.Time             `json:"last_discovery,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// CloudStackConfig represents CloudStack API configuration
type CloudStackConfig struct {
	ID                string                 `json:"id"`
	ClusterName       string                 `json:"cluster_name"`
	DisplayName       string                 `json:"display_name"`
	Endpoint          string                 `json:"endpoint"`
	APIKey            string                 `json:"api_key"`
	SecretKey         string                 `json:"secret_key"`
	Region            string                 `json:"region"`
	Zone              string                 `json:"zone,omitempty"`
	Status            string                 `json:"status"`
	HostCount         int                    `json:"host_count"`
	VMCount           int                    `json:"vm_count"`
	LastDiscovery     *time.Time             `json:"last_discovery,omitempty"`
	DiscoveryEnabled  bool                   `json:"discovery_enabled"`
	ConnectionTimeout int                    `json:"connection_timeout"`
	Labels            map[string]interface{} `json:"labels,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// GetK8sClustersConfig gets all K8s cluster configurations
// GET /api/v1/topology/k8s-clusters
func (h *EnhancedTopologyConfigHandler) GetK8sClustersConfig(c *gin.Context) {
	region := c.Query("region")

	query := `
		SELECT id, name, COALESCE(cluster_name, name), COALESCE(display_name, name),
		       COALESCE(api_server, api_server_url, ''), region, COALESCE(environment, 'production'),
		       enabled, COALESCE(version, ''), node_count, namespace_count, pod_count,
		       COALESCE(status, 'unknown'), readonly_mode, discovery_enabled,
		       COALESCE(labels, '{}'), COALESCE(metadata, '{}'),
		       last_discovery, created_at, updated_at
		FROM k8s_cluster_configs
	`
	args := []interface{}{}

	if region != "" {
		query += " WHERE region = $1"
		args = append(args, region)
	}

	query += " ORDER BY region, name"

	rows, err := h.infraTopoService.DB.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		log.Printf("Failed to get K8s cluster configs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get K8s cluster configurations: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	var clusters []K8sClusterConfig
	for rows.Next() {
		var cluster K8sClusterConfig
		var labelsJSON, metadataJSON []byte

		err := rows.Scan(
			&cluster.ID, &cluster.Name, &cluster.ClusterName, &cluster.DisplayName,
			&cluster.APIServer, &cluster.Region, &cluster.Environment,
			&cluster.Enabled,
			&cluster.Version, &cluster.NodeCount, &cluster.NamespaceCount,
			&cluster.PodCount, &cluster.Status, &cluster.ReadonlyMode,
			&cluster.DiscoveryEnabled, &labelsJSON, &metadataJSON,
			&cluster.LastDiscovery, &cluster.CreatedAt, &cluster.UpdatedAt,
		)
		if err != nil {
			log.Printf("Error scanning K8s cluster row: %v", err)
			continue
		}

		// Parse JSON fields
		if len(labelsJSON) > 0 {
			json.Unmarshal(labelsJSON, &cluster.Labels)
		}
		if len(metadataJSON) > 0 {
			json.Unmarshal(metadataJSON, &cluster.Metadata)
		}

		clusters = append(clusters, cluster)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"clusters": clusters,
			"total":    len(clusters),
			"region":   region,
		},
	})
}

// CreateK8sClusterConfig creates new K8s cluster configuration
// POST /api/v1/topology/config/k8s-clusters
func (h *EnhancedTopologyConfigHandler) CreateK8sClusterConfig(c *gin.Context) {
	var req K8sClusterConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid cluster configuration: " + err.Error(),
		})
		return
	}

	// Set defaults
	if req.Region == "" {
		req.Region = "reno"
	}
	if req.Environment == "" {
		req.Environment = "production"
	}
	req.ReadonlyMode = true // Always readonly from UI
	req.DiscoveryEnabled = true

	labelsJSON, _ := json.Marshal(req.Labels)
	metadataJSON, _ := json.Marshal(req.Metadata)

	query := `
		INSERT INTO k8s_cluster_configs (
			cluster_name, display_name, api_server, region, environment,
			version, readonly_mode, discovery_enabled, labels, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (cluster_name, region)
		DO UPDATE SET
			display_name = EXCLUDED.display_name,
			api_server = EXCLUDED.api_server,
			environment = EXCLUDED.environment,
			version = EXCLUDED.version,
			labels = EXCLUDED.labels,
			metadata = EXCLUDED.metadata,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`

	err := h.infraTopoService.DB.QueryRowContext(c.Request.Context(), query,
		req.ClusterName, req.DisplayName, req.APIServer, req.Region, req.Environment,
		req.Version, req.ReadonlyMode, req.DiscoveryEnabled, labelsJSON, metadataJSON,
	).Scan(&req.ID, &req.CreatedAt, &req.UpdatedAt)

	if err != nil {
		log.Printf("Failed to create K8s cluster config: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create K8s cluster configuration: " + err.Error(),
		})
		return
	}

	log.Printf("Created K8s cluster config: %s in %s", req.ClusterName, req.Region)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    req,
		"message": "K8s cluster configuration created successfully",
	})
}

// GetCloudStackConfigs gets all CloudStack configurations
// GET /api/v1/topology/config/cloudstack
func (h *EnhancedTopologyConfigHandler) GetCloudStackConfigs(c *gin.Context) {
	region := c.Query("region")

	query := `
		SELECT id, cluster_name, display_name, endpoint, region, zone,
		       status, host_count, vm_count, last_discovery, discovery_enabled,
		       connection_timeout, labels, metadata, created_at, updated_at
		FROM cloudstack_configs
	`
	args := []interface{}{}

	if region != "" {
		query += " WHERE region = $1"
		args = append(args, region)
	}

	query += " ORDER BY region, cluster_name"

	rows, err := h.infraTopoService.DB.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		log.Printf("Failed to get CloudStack configs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get CloudStack configurations: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	var configs []CloudStackConfig
	for rows.Next() {
		var config CloudStackConfig
		var labelsJSON, metadataJSON []byte

		err := rows.Scan(
			&config.ID, &config.ClusterName, &config.DisplayName,
			&config.Endpoint, &config.Region, &config.Zone,
			&config.Status, &config.HostCount, &config.VMCount,
			&config.LastDiscovery, &config.DiscoveryEnabled,
			&config.ConnectionTimeout, &labelsJSON, &metadataJSON,
			&config.CreatedAt, &config.UpdatedAt,
		)
		if err != nil {
			log.Printf("Error scanning CloudStack config row: %v", err)
			continue
		}

		// Don't return sensitive API keys
		config.APIKey = "***configured***"
		config.SecretKey = "***configured***"

		// Parse JSON fields
		if len(labelsJSON) > 0 {
			json.Unmarshal(labelsJSON, &config.Labels)
		}
		if len(metadataJSON) > 0 {
			json.Unmarshal(metadataJSON, &config.Metadata)
		}

		configs = append(configs, config)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"configs": configs,
			"total":   len(configs),
			"region":  region,
		},
	})
}

// DebugTopologyConfig debugs the current topology configuration loading
// GET /api/v1/topology/debug/config
func (h *EnhancedTopologyConfigHandler) DebugTopologyConfig(c *gin.Context) {
	log.Printf("DEBUGGING TOPOLOGY CONFIGURATION...")

	// Check main topology_config table
	var configJSON []byte
	query := `SELECT config FROM topology_config WHERE id = 'default' LIMIT 1`
	err := h.infraTopoService.DB.QueryRowContext(c.Request.Context(), query).Scan(&configJSON)

	debugInfo := gin.H{
		"timestamp": time.Now(),
		"checks":    make([]gin.H, 0),
	}

	if err != nil {
		log.Printf("No config in topology_config table: %v", err)
		debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
			"check":   "topology_config_table",
			"status":  "failed",
			"message": "No config found: " + err.Error(),
		})
	} else {
		log.Printf("Found config in database: %d bytes", len(configJSON))

		var config map[string]interface{}
		if parseErr := json.Unmarshal(configJSON, &config); parseErr != nil {
			log.Printf("Config parse failed: %v", parseErr)
			debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
				"check":      "config_parsing",
				"status":     "failed",
				"message":    "Parse error: " + parseErr.Error(),
				"raw_config": string(configJSON),
			})
		} else {
			// Successfully parsed - analyze content
			configKeys := make([]string, 0, len(config))
			for k := range config {
				configKeys = append(configKeys, k)
			}

			log.Printf("Config keys: %v", configKeys)

			debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
				"check":       "config_parsing",
				"status":      "success",
				"config_size": len(configJSON),
				"config_keys": configKeys,
			})

			// Check CloudStack configuration specifically
			if cloudstackRaw, exists := config["cloudstack"]; exists {
				log.Printf("CloudStack key exists in config")
				log.Printf("CloudStack type: %T", cloudstackRaw)

				if cloudstackConfigs, ok := cloudstackRaw.([]interface{}); ok {
					log.Printf("CloudStack is array with %d items", len(cloudstackConfigs))

					cloudstackDebug := gin.H{
						"check":   "cloudstack_config",
						"status":  "found",
						"type":    "array",
						"count":   len(cloudstackConfigs),
						"configs": make([]gin.H, 0),
					}

					for i, csConfigRaw := range cloudstackConfigs {
						if cs, csOk := csConfigRaw.(map[string]interface{}); csOk {
							endpoint, _ := cs["endpoint"].(string)
							apiKey, _ := cs["apiKey"].(string)
							secretKey, _ := cs["secretKey"].(string)
							clusterName, _ := cs["clusterName"].(string)

							configAnalysis := gin.H{
								"index":       i + 1,
								"endpoint":    endpoint,
								"cluster":     clusterName,
								"has_api_key": apiKey != "",
								"has_secret":  secretKey != "",
								"valid":       (endpoint != "" && apiKey != "" && secretKey != ""),
							}

							log.Printf("CloudStack config %d: endpoint=%s, valid=%v",
								i+1, endpoint, configAnalysis["valid"])

							cloudstackDebug["configs"] = append(
								cloudstackDebug["configs"].([]gin.H),
								configAnalysis,
							)
						}
					}

					debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), cloudstackDebug)
				} else {
					log.Printf("CloudStack config is not array: %T", cloudstackRaw)
					debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
						"check":   "cloudstack_config",
						"status":  "invalid_type",
						"type":    fmt.Sprintf("%T", cloudstackRaw),
						"message": "Expected array of CloudStack configurations",
					})
				}
			} else {
				log.Printf("No cloudstack key in config")
				debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
					"check":   "cloudstack_key",
					"status":  "missing",
					"message": "No cloudstack key found in configuration",
				})
			}
		}
	}

	// Check CloudStack configs table
	csQuery := `SELECT COUNT(*) FROM cloudstack_configs WHERE discovery_enabled = true`
	var csCount int
	if err := h.infraTopoService.DB.QueryRowContext(c.Request.Context(), csQuery).Scan(&csCount); err != nil {
		log.Printf("Failed to count CloudStack configs: %v", err)
	} else {
		log.Printf("CloudStack configs in table: %d", csCount)
		debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
			"check":  "cloudstack_configs_table",
			"status": "success",
			"count":  csCount,
		})
	}

	// Check K8s configs table
	k8sQuery := `SELECT COUNT(*) FROM k8s_cluster_configs WHERE discovery_enabled = true`
	var k8sCount int
	if err := h.infraTopoService.DB.QueryRowContext(c.Request.Context(), k8sQuery).Scan(&k8sCount); err != nil {
		log.Printf("Failed to count K8s configs: %v", err)
	} else {
		log.Printf("K8s configs in table: %d", k8sCount)
		debugInfo["checks"] = append(debugInfo["checks"].([]gin.H), gin.H{
			"check":  "k8s_configs_table",
			"status": "success",
			"count":  k8sCount,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    debugInfo,
	})
}

// TestCloudStackConnection tests connection to a CloudStack API
// POST /api/v1/topology/config/test-cloudstack
func (h *EnhancedTopologyConfigHandler) TestCloudStackConnection(c *gin.Context) {
	var req struct {
		Endpoint  string `json:"endpoint" binding:"required"`
		APIKey    string `json:"api_key" binding:"required"`
		SecretKey string `json:"secret_key" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	log.Printf("Testing CloudStack connection to: %s", req.Endpoint)

	// Create CloudStack client and test connection
	client := topology.NewCloudStackClient(req.Endpoint, req.APIKey, req.SecretKey)

	// Test with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	hosts, err := client.ListHosts(ctx)
	if err != nil {
		log.Printf("CloudStack connection test failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success":  false,
			"message":  "CloudStack connection failed: " + err.Error(),
			"endpoint": req.Endpoint,
		})
		return
	}

	vms, vmErr := client.ListVirtualMachines(ctx)
	if vmErr != nil {
		log.Printf("VM listing failed: %v", vmErr)
	}

	log.Printf("CloudStack connection SUCCESS: %d hosts, %d VMs", len(hosts), len(vms))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "CloudStack connection successful",
		"data": gin.H{
			"endpoint":   req.Endpoint,
			"host_count": len(hosts),
			"vm_count":   len(vms),
			"test_time":  time.Now(),
		},
	})
}

// TriggerRealDiscovery forces real topology discovery using current configurations
// POST /api/v1/topology/force-real-discovery
func (h *EnhancedTopologyConfigHandler) TriggerRealDiscovery(c *gin.Context) {
	region := c.DefaultQuery("region", "reno")

	log.Printf("TRIGGERING FORCE REAL DISCOVERY for region: %s", region)

	topology, err := h.infraTopoService.ForceRealTopologyDiscovery(c.Request.Context(), region)
	if err != nil {
		log.Printf("Force real discovery failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Real topology discovery failed: " + err.Error(),
			"region":  region,
		})
		return
	}

	log.Printf("Force real discovery SUCCESS: %d nodes", topology.Stats.TotalNodes)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Real topology discovery completed successfully",
		"data": gin.H{
			"region":      region,
			"total_nodes": topology.Stats.TotalNodes,
			"layers":      topology.Stats.NodesByLayer,
			"timestamp":   topology.Timestamp,
		},
	})
}

// RegisterEnhancedConfigRoutes registers enhanced topology configuration routes
func (h *EnhancedTopologyConfigHandler) RegisterEnhancedConfigRoutes(protected *gin.RouterGroup) {
	// Register routes directly under topology (already protected)
	topology := protected.Group("/topology")
	{
		// K8s cluster management (readonly configs) - Use existing topology auth
		topology.GET("/k8s-clusters", h.GetK8sClustersConfig)
		topology.POST("/k8s-clusters", h.CreateK8sClusterConfig)

		// CloudStack configuration management - Use existing topology auth
		topology.GET("/cloudstack-configs", h.GetCloudStackConfigs)
		topology.POST("/test-cloudstack", h.TestCloudStackConnection)

		// Debug and testing - Use existing topology auth
		topology.GET("/config-debug", h.DebugTopologyConfig)
		topology.POST("/force-discovery", h.TriggerRealDiscovery)
	}
}
