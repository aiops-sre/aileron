package rca

// neo4j_adapter.go — implements Neo4jQuerier against the neo4jHTTPClient
// that already exists in kubesense-core/cmd/core/main.go.
// This file lives in the rca package so the engine can use it directly.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Neo4jAdapter implements Neo4jQuerier using the shared neo4jHTTPClient.
// It executes Cypher queries via the Neo4j HTTP transactional API.
type Neo4jAdapter struct {
	client Neo4jHTTPQuerier
}

// Neo4jHTTPQuerier is a minimal interface for the HTTP-based Neo4j client
// already implemented in kubesense-core. Keeps rca package dependency-free.
type Neo4jHTTPQuerier interface {
	Query(cypher string, params map[string]any) (*Neo4jResponse, error)
}

// Neo4jResponse mirrors the HTTP response structure.
type Neo4jResponse struct {
	Results []struct {
		Columns []string `json:"columns"`
		Data    []struct {
			Row []any `json:"row"`
		} `json:"data"`
	} `json:"results"`
}

func NewNeo4jAdapter(client Neo4jHTTPQuerier) *Neo4jAdapter {
	return &Neo4jAdapter{client: client}
}

// GetUpstreamChain traverses the topology graph from a given resource upward
// (UPSTREAM direction — following dependency edges) up to maxDepth hops.
// Returns all nodes in the causal chain ordered by depth.
func (a *Neo4jAdapter) GetUpstreamChain(ctx context.Context, clusterID, kind, namespace, name string, maxDepth int) ([]DependencyNode, error) {
	// Walk upstream dependencies: follow DEPENDS_ON / MANAGED_BY / RUNS_ON edges.
	cypher := fmt.Sprintf(`
		MATCH path = (start {cluster_id: $cluster, kind: $kind, namespace: $ns, name: $name})
		             -[*1..%d]->(upstream)
		WHERE upstream.deleted IS NULL OR upstream.deleted = false
		RETURN upstream.node_id AS entity_id,
		       upstream.kind AS kind,
		       upstream.name AS name,
		       upstream.namespace AS ns,
		       length(path) AS depth
		ORDER BY depth ASC
		LIMIT 50
	`, maxDepth)

	resp, err := a.client.Query(cypher, map[string]any{
		"cluster": clusterID,
		"kind":    kind,
		"ns":      namespace,
		"name":    name,
	})
	if err != nil {
		// Fail open: return just the affected resource itself as the chain.
		return []DependencyNode{{
			EntityID:   kind + "/" + namespace + "/" + name,
			EntityKind: kind,
			Name:       name,
			Namespace:  namespace,
			Depth:      0,
		}}, nil
	}

	// Always include the starting resource.
	nodes := []DependencyNode{{
		EntityID:   kind + "/" + namespace + "/" + name,
		EntityKind: kind,
		Name:       name,
		Namespace:  namespace,
		Depth:      0,
	}}

	if len(resp.Results) == 0 || len(resp.Results[0].Data) == 0 {
		return nodes, nil
	}

	for _, row := range resp.Results[0].Data {
		if len(row.Row) < 5 {
			continue
		}
		entityID, _ := row.Row[0].(string)
		nodeKind, _ := row.Row[1].(string)
		nodeName, _ := row.Row[2].(string)
		nodeNS, _ := row.Row[3].(string)
		depth := 1
		if d, ok := row.Row[4].(float64); ok {
			depth = int(d)
		}
		nodes = append(nodes, DependencyNode{
			EntityID:   entityID,
			EntityKind: nodeKind,
			Name:       nodeName,
			Namespace:  nodeNS,
			Depth:      depth,
		})
	}
	return nodes, nil
}

// GetK8sEventsFor returns K8s events stored in Neo4j for a specific resource
// within the lookback window. Events are stored by the KubeSense collector.
func (a *Neo4jAdapter) GetK8sEventsFor(ctx context.Context, clusterID, kind, namespace, name string, since time.Time) ([]Evidence, error) {
	cypher := `
		MATCH (e:K8sEvent {cluster_id: $cluster, resource_kind: $kind,
		                    resource_namespace: $ns, resource_name: $name})
		WHERE e.timestamp >= $since
		RETURN e.event_type AS type, e.reason AS reason, e.message AS message,
		       e.timestamp AS ts, e.severity AS severity
		ORDER BY e.timestamp DESC
		LIMIT 20
	`
	resp, err := a.client.Query(cypher, map[string]any{
		"cluster": clusterID,
		"kind":    kind,
		"ns":      namespace,
		"name":    name,
		"since":   since.Format(time.RFC3339),
	})
	if err != nil || len(resp.Results) == 0 {
		return nil, nil
	}

	var evidence []Evidence
	for _, row := range resp.Results[0].Data {
		if len(row.Row) < 4 {
			continue
		}
		evType, _ := row.Row[0].(string)
		reason, _ := row.Row[1].(string)
		message, _ := row.Row[2].(string)
		tsStr, _ := row.Row[3].(string)
		severity, _ := row.Row[4].(string)

		ts, _ := time.Parse(time.RFC3339, tsStr)
		strength := severityToStrength(severity)
		desc := reason
		if message != "" {
			desc = reason + ": " + message
			if len(desc) > 200 {
				desc = desc[:200]
			}
		}
		evidence = append(evidence, Evidence{
			Type:        "k8s_event",
			Source:      "neo4j",
			Description: desc,
			Strength:    strength,
			OccurredAt:  ts,
			ResourceRef: ResourceRef{
				Kind:      kind,
				Namespace: namespace,
				Name:      name,
			},
		})
		_ = evType
	}
	return evidence, nil
}

func severityToStrength(severity string) float64 {
	switch strings.ToLower(severity) {
	case "critical":
		return 1.0
	case "high", "warning":
		return 0.80
	case "medium", "normal":
		return 0.50
	default:
		return 0.30
	}
}
