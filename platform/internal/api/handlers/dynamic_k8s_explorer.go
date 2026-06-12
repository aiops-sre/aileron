package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// DynamicK8sExplorerHandler handles dynamic Kubernetes cluster exploration
type DynamicK8sExplorerHandler struct {
	k8sServiceURL string
}

// NewDynamicK8sExplorerHandler creates new dynamic K8s explorer handler
func NewDynamicK8sExplorerHandler(k8sServiceURL string) *DynamicK8sExplorerHandler {
	return &DynamicK8sExplorerHandler{
		k8sServiceURL: k8sServiceURL,
	}
}

// ClusterCredentials represents cluster connection credentials
type ClusterCredentials struct {
	ClusterName string `json:"cluster_name" binding:"required"`
	APIServer   string `json:"api_server" binding:"required"`
	Token       string `json:"token" binding:"required"`
	CACert      string `json:"ca_cert,omitempty"`
}

// DynamicClusterConnection represents a dynamic cluster connection request
type DynamicClusterConnection struct {
	Credentials ClusterCredentials `json:"credentials" binding:"required"`
	Action      string             `json:"action" binding:"required"` // connect, explore, logs, events, etc.
	Namespace   string             `json:"namespace,omitempty"`
	Resource    string             `json:"resource,omitempty"`
	Name        string             `json:"name,omitempty"`
	Options     map[string]string  `json:"options,omitempty"`
}

// ConnectToCluster establishes dynamic connection to a Kubernetes cluster
// POST /api/v1/k8s-explorer/connect
func (h *DynamicK8sExplorerHandler) ConnectToCluster(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid cluster connection request: " + err.Error(),
		})
		return
	}

	log.Printf("Dynamic connection to cluster: %s (%s)", req.Credentials.ClusterName, req.Credentials.APIServer)

	// Forward to K8s Intelligence Service with dynamic credentials
	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/connect", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to connect to cluster: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to connect to cluster: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetClusterTopology gets real-time cluster topology without storing credentials
// POST /api/v1/k8s-explorer/topology
func (h *DynamicK8sExplorerHandler) GetClusterTopology(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid topology request: " + err.Error(),
		})
		return
	}

	req.Action = "topology"

	log.Printf("Getting topology for cluster: %s", req.Credentials.ClusterName)

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/topology", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to get topology: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to get cluster topology: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetPodLogs gets real-time pod logs from the cluster
// POST /api/v1/k8s-explorer/logs
func (h *DynamicK8sExplorerHandler) GetPodLogs(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid logs request: " + err.Error(),
		})
		return
	}

	req.Action = "logs"

	log.Printf("Getting logs for pod %s/%s in cluster: %s", req.Namespace, req.Name, req.Credentials.ClusterName)

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/logs", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to get pod logs: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to get pod logs: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetClusterEvents gets real-time cluster events
// POST /api/v1/k8s-explorer/events
func (h *DynamicK8sExplorerHandler) GetClusterEvents(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid events request: " + err.Error(),
		})
		return
	}

	req.Action = "events"

	log.Printf("Getting events for cluster: %s", req.Credentials.ClusterName)

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/events", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to get cluster events: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to get cluster events: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetPodDetails gets detailed information about a specific pod
// POST /api/v1/k8s-explorer/pod
func (h *DynamicK8sExplorerHandler) GetPodDetails(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid pod request: " + err.Error(),
		})
		return
	}

	req.Action = "pod"

	log.Printf("Getting pod details for %s/%s in cluster: %s", req.Namespace, req.Name, req.Credentials.ClusterName)

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/pod", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to get pod details: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to get pod details: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetNamespaceResources gets all resources in a namespace
// POST /api/v1/k8s-explorer/namespace
func (h *DynamicK8sExplorerHandler) GetNamespaceResources(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid namespace request: " + err.Error(),
		})
		return
	}

	req.Action = "namespace"

	log.Printf("Getting namespace resources for %s in cluster: %s", req.Namespace, req.Credentials.ClusterName)

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/namespace", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to get namespace resources: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to get namespace resources: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetNodeDetails gets detailed information about cluster nodes
// POST /api/v1/k8s-explorer/nodes
func (h *DynamicK8sExplorerHandler) GetNodeDetails(c *gin.Context) {
	var req DynamicClusterConnection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid node request: " + err.Error(),
		})
		return
	}

	req.Action = "nodes"

	log.Printf("Getting node details for cluster: %s", req.Credentials.ClusterName)

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/nodes", req, c.Request.Context())
	if err != nil {
		log.Printf("Failed to get node details: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Failed to get node details: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// TestClusterConnection tests connection to cluster with given credentials
// POST /api/v1/k8s-explorer/test
func (h *DynamicK8sExplorerHandler) TestClusterConnection(c *gin.Context) {
	var req ClusterCredentials
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid credentials: " + err.Error(),
		})
		return
	}

	log.Printf("Testing connection to cluster: %s", req.ClusterName)

	testReq := DynamicClusterConnection{
		Credentials: req,
		Action:      "test",
	}

	response, err := h.forwardToK8sService("POST", "/api/v1/dynamic/test", testReq, c.Request.Context())
	if err != nil {
		log.Printf("Connection test failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Connection test failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// forwardToK8sService forwards requests to the K8s Intelligence Service
func (h *DynamicK8sExplorerHandler) forwardToK8sService(method, endpoint string, payload interface{}, ctx context.Context) (map[string]interface{}, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, h.k8sServiceURL+endpoint, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// RegisterDynamicK8sRoutes registers dynamic Kubernetes explorer routes
func (h *DynamicK8sExplorerHandler) RegisterDynamicK8sRoutes(protected *gin.RouterGroup) {
	k8s := protected.Group("/k8s-explorer")
	{
		// Dynamic cluster operations (no persistent storage)
		k8s.POST("/test", h.TestClusterConnection)      // Test cluster connection
		k8s.POST("/connect", h.ConnectToCluster)        // Connect to cluster
		k8s.POST("/topology", h.GetClusterTopology)     // Get cluster topology
		k8s.POST("/events", h.GetClusterEvents)         // Get cluster events
		k8s.POST("/logs", h.GetPodLogs)                 // Get pod logs
		k8s.POST("/pod", h.GetPodDetails)               // Get pod details
		k8s.POST("/namespace", h.GetNamespaceResources) // Get namespace resources
		k8s.POST("/nodes", h.GetNodeDetails)            // Get node details
	}
}
