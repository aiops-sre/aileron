package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// BlastRadiusHandler exposes topology blast-radius for OIE's RCA generator.
// OIE calls GET /api/v1/topology/blast-radius?topology_path=cluster/ns/kind/name
// instead of the undeployed OKG service.
type BlastRadiusHandler struct {
	neo4jDriver neo4j.DriverWithContext
}

func NewBlastRadiusHandler(driver neo4j.DriverWithContext) *BlastRadiusHandler {
	return &BlastRadiusHandler{neo4jDriver: driver}
}

type blastRadiusService struct {
	EntityID  string `json:"entity_id"`
	Name      string `json:"name"`
	NodeType  string `json:"type"`
	Namespace string `json:"namespace,omitempty"`
}

type blastRadiusResponse struct {
	Source           string               `json:"source"`
	SourceEntityID   string               `json:"source_entity_id"`
	AffectedServices []blastRadiusService `json:"affected_services"`
	Depth            int                  `json:"depth"`
	QueryTimeMs      int64                `json:"query_time_ms"`
}

// GetBlastRadius handles GET /api/v1/topology/blast-radius
// Query params:
//   - topology_path: AlertHub topology path (cluster/ns/kind/name or cluster/ns)
//   - depth: optional hop depth (default 2, max 3)
func (h *BlastRadiusHandler) GetBlastRadius(c *gin.Context) {
	if h.neo4jDriver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "topology graph not available"})
		return
	}

	topologyPath := strings.TrimSpace(c.Query("topology_path"))
	if topologyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "topology_path is required"})
		return
	}

	depth := 2
	if d := c.Query("depth"); d == "1" {
		depth = 1
	} else if d == "3" {
		depth = 3
	}

	// Derive search terms from topology_path.
	// Format variants: "cluster/ns/kind/name", "cluster/ns", "cluster:title", "h:hostname"
	parts := strings.Split(topologyPath, "/")
	clusterPart := parts[0]
	namespacePart := ""
	if len(parts) >= 2 {
		namespacePart = parts[1]
	}
	// Strip "cluster:title" format
	if idx := strings.Index(clusterPart, ":"); idx > 0 {
		clusterPart = clusterPart[:idx]
	}

	ctx := c.Request.Context()
	start := time.Now()

	session := h.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// Match the source node by entity_id prefix or cluster + namespace.
	// Walk up to $depth hops of outgoing relationships to find dependents.
	cypher := `
MATCH (src:Service)
WHERE src.entity_id = $topologyPath
   OR src.entity_id STARTS WITH $prefix
   OR (src.cluster_id = $cluster AND ($namespace = '' OR src.namespace = $namespace))
WITH src ORDER BY src.entity_id LIMIT 1
OPTIONAL MATCH (src)-[*1..` + strings.Repeat("1", depth) + `]->(dep:Service)
WHERE dep <> src
RETURN src.entity_id  AS src_eid,
       src.name       AS src_name,
       src.type       AS src_type,
       collect(DISTINCT {
           entity_id:  dep.entity_id,
           name:       dep.name,
           node_type:  dep.type,
           namespace:  dep.namespace
       }) AS affected
`
	// Build a proper variable-depth query — Neo4j requires literal range in the path pattern.
	cypher = buildBlastRadiusCypher(depth)

	result, err := session.Run(ctx, cypher, map[string]interface{}{
		"topologyPath": topologyPath,
		"prefix":       clusterPart + "/",
		"cluster":      clusterPart,
		"namespace":    namespacePart,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "graph query failed", "detail": err.Error()})
		return
	}

	resp := blastRadiusResponse{
		Source:           topologyPath,
		AffectedServices: []blastRadiusService{},
		Depth:            depth,
	}

	if result.Next(ctx) {
		record := result.Record()
		if v, ok := record.Get("src_eid"); ok && v != nil {
			resp.SourceEntityID = toString(v)
		}
		if v, ok := record.Get("src_name"); ok && v != nil {
			resp.Source = toString(v)
		}
		if v, ok := record.Get("affected"); ok && v != nil {
			if items, ok := v.([]interface{}); ok {
				for _, item := range items {
					if m, ok := item.(map[string]interface{}); ok {
						svc := blastRadiusService{
							EntityID:  toString(m["entity_id"]),
							Name:      toString(m["name"]),
							NodeType:  toString(m["node_type"]),
							Namespace: toString(m["namespace"]),
						}
						if svc.EntityID != "" || svc.Name != "" {
							resp.AffectedServices = append(resp.AffectedServices, svc)
						}
					}
				}
			}
		}
	}

	resp.QueryTimeMs = time.Since(start).Milliseconds()
	c.JSON(http.StatusOK, resp)
}

func buildBlastRadiusCypher(depth int) string {
	depthStr := "2"
	switch depth {
	case 1:
		depthStr = "1"
	case 3:
		depthStr = "3"
	}
	return `
MATCH (src:Service)
WHERE src.entity_id = $topologyPath
   OR src.entity_id STARTS WITH $prefix
   OR (src.cluster_id = $cluster AND ($namespace = '' OR src.namespace = $namespace))
WITH src ORDER BY src.entity_id LIMIT 1
OPTIONAL MATCH (src)-[*1..` + depthStr + `]->(dep:Service)
WHERE dep <> src
RETURN src.entity_id  AS src_eid,
       src.name       AS src_name,
       src.type       AS src_type,
       collect(DISTINCT {
           entity_id:  dep.entity_id,
           name:       dep.name,
           node_type:  dep.type,
           namespace:  dep.namespace
       }) AS affected`
}

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
