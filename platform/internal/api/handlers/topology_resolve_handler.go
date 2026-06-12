package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/aileron-platform/aileron/platform/internal/services/topology"
)

// TopologyResolveHandler resolves an entity from a topology_path using AlertHub's
// live Neo4j + Redis topology cache. Used by the OIE evidence bus to accurately
// populate EntityProfile (pod → node → CloudStack VM chain) for K8s incidents.
//
// Without this, the evidence bus fell back to stale Postgres tables and often
// returned empty profiles, causing evidence fetchers to return no data and the
// hypothesis scorer to pick winners based on BasePrior alone — the root cause of
// hallucinated RCA narratives.
type TopologyResolveHandler struct {
	cache *topology.TopologyGraphCache
}

func NewTopologyResolveHandler(cache *topology.TopologyGraphCache) *TopologyResolveHandler {
	return &TopologyResolveHandler{cache: cache}
}

// ResolvedEntity is what the OIE evidence bus expects back.
type ResolvedEntity struct {
	ClusterID        string `json:"cluster_id"`
	Namespace        string `json:"namespace"`
	ResourceName     string `json:"resource_name"`
	ResourceKind     string `json:"resource_kind"`
	K8sNodeName      string `json:"k8s_node_name"`
	CloudStackVMName string `json:"cloudstack_vm_name"`
	Status           string `json:"status"`  // running|stopped|degraded|unknown
	Health           string `json:"health"`  // healthy|degraded|unhealthy|unknown
	ParentID         string `json:"parent_id"`
	ResolvedAt       string `json:"resolved_at"`
	Source           string `json:"source"` // "neo4j_cache" | "not_found"
}

// Resolve handles GET /api/v1/topology/resolve?topology_path=cluster/ns/kind/name
func (h *TopologyResolveHandler) Resolve(c *gin.Context) {
	if h.cache == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "topology cache not available"})
		return
	}

	topologyPath := strings.TrimSpace(c.Query("topology_path"))
	if topologyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "topology_path is required"})
		return
	}

	ctx := c.Request.Context()

	// Extract query terms from topology_path
	// Format: "cluster/namespace/kind/name" or "cluster/namespace" or "cluster:title"
	parts := strings.Split(topologyPath, "/")
	clusterPart := parts[0]
	if idx := strings.Index(clusterPart, ":"); idx > 0 {
		clusterPart = clusterPart[:idx]
	}
	namespacePart := ""
	if len(parts) >= 2 {
		namespacePart = parts[1]
	}
	resourceName := ""
	if len(parts) >= 4 {
		resourceName = parts[3]
	} else if len(parts) == 3 {
		resourceName = parts[2]
	}

	// Search the Neo4j + Redis topology cache
	searchQuery := resourceName
	if searchQuery == "" {
		searchQuery = namespacePart
	}
	if searchQuery == "" {
		searchQuery = clusterPart
	}

	resp, err := h.cache.Search(ctx, searchQuery)
	if err != nil || resp == nil || len(resp.Results) == 0 {
		// Not found in cache — return what we can parse from the path
		c.JSON(http.StatusOK, ResolvedEntity{
			ClusterID:    clusterPart,
			Namespace:    namespacePart,
			ResourceName: resourceName,
			Source:       "not_found",
			ResolvedAt:   time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	entity := ResolvedEntity{
		ClusterID:    clusterPart,
		Namespace:    namespacePart,
		ResourceName: resourceName,
		Source:       "neo4j_cache",
		ResolvedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// Walk results: find best matching node, then traverse to node and VM parents
	for _, result := range resp.Results {
		node := result.Node
		// Filter to the right cluster and namespace
		if clusterPart != "" && !matchesCluster(node, clusterPart) {
			continue
		}

		entity.Status = node.Status
		entity.Health = node.Health
		entity.ParentID = node.ParentID

		if nsVal, ok := node.Data["namespace"].(string); ok && nsVal != "" {
			entity.Namespace = nsVal
		}
		if nameVal, ok := node.Data["name"].(string); ok && nameVal != "" {
			entity.ResourceName = nameVal
		}
		entity.ResourceKind = node.NodeType

		// Walk parents to find K8s node and CloudStack VM
		for _, parent := range result.Parents {
			switch parent.NodeType {
			case "k8s_node":
				if nname, ok := parent.Data["name"].(string); ok {
					entity.K8sNodeName = nname
				} else {
					entity.K8sNodeName = parent.Label
				}
			case "cloudstack_vm", "virtual_machine":
				if vmname, ok := parent.Data["name"].(string); ok {
					entity.CloudStackVMName = vmname
				} else {
					entity.CloudStackVMName = parent.Label
				}
			}
		}
		break // use first match
	}

	c.JSON(http.StatusOK, entity)
}

func matchesCluster(node topology.GraphNode, clusterName string) bool {
	if clusterVal, ok := node.Data["cluster_id"].(string); ok {
		return strings.Contains(clusterVal, clusterName) || strings.Contains(clusterName, clusterVal)
	}
	if clusterVal, ok := node.Data["cluster"].(string); ok {
		return strings.Contains(clusterVal, clusterName) || strings.Contains(clusterName, clusterVal)
	}
	return true // no cluster data — don't filter
}
