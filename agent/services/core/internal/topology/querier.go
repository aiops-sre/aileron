// Package topology implements the Neo4j topology querier for the RCA engine.
// Uses the same HTTP API transport as the collector writer — no Go driver needed.
package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aileron-platform/aileron/agent/services/core/internal/rca"
)

// Querier implements rca.Neo4jQuerier via the Neo4j HTTP transactional API.
type Querier struct {
	baseURL  string
	user     string
	password string
	client   *http.Client
}

// NewQuerier creates a Neo4j topology querier.
func NewQuerier(baseURL, user, password string) *Querier {
	return &Querier{
		baseURL:  baseURL,
		user:     user,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// GetUpstreamChain traverses all outbound relationship types from the given
// resource up to maxDepth hops, returning every unique upstream node.
// Edge types mirror the agent topology graph (RUNS_ON, MANAGED_BY, etc.).
func (q *Querier) GetUpstreamChain(
	ctx context.Context,
	clusterID, kind, namespace, name string,
	maxDepth int,
) ([]rca.DependencyNode, error) {
	if maxDepth <= 0 || maxDepth > 12 {
		maxDepth = 8
	}
	nodeID := nodeKey(clusterID, kind, namespace, name)

	// Variable-length path — discovers all upstream owners and dependencies.
	// The DISTINCT prevents duplicates when multiple paths lead to the same node.
	cypher := fmt.Sprintf(`
		MATCH (start {node_id: $node_id})
		MATCH path = (start)-[*1..%d]->(n)
		WHERE n.cluster_id = $cid
		  AND n.node_id <> $node_id
		  AND (n.deleted IS NULL OR n.deleted = false)
		WITH DISTINCT n, min(length(path)) AS depth
		RETURN n.node_id AS id, n.kind AS kind,
		       n.namespace AS ns, n.name AS name, depth
		ORDER BY depth ASC
		LIMIT 200`, maxDepth)

	res, err := q.run(ctx, cypher, map[string]any{
		"node_id": nodeID,
		"cid":     clusterID,
	})
	if err != nil {
		return nil, fmt.Errorf("upstream chain query: %w", err)
	}
	return extractDependencyNodes(res), nil
}

// GetK8sEventsFor returns Kubernetes events stored in Neo4j as KubeEvent nodes
// for the specified resource since the given time. The collector's topology writer
// calls StoreEvent to populate these from the health/event Kafka topics.
// Returns nil (not an error) if no KubeEvent nodes exist for this resource.
func (q *Querier) GetK8sEventsFor(
	ctx context.Context,
	clusterID, kind, namespace, name string,
	since time.Time,
) ([]rca.Evidence, error) {
	cypher := `
		MATCH (e:KubeEvent)
		WHERE e.cluster_id = $cid
		  AND e.resource_kind = $kind
		  AND e.resource_namespace = $ns
		  AND e.resource_name = $name
		  AND e.occurred_at >= $since
		RETURN e.reason    AS reason,
		       e.message   AS message,
		       e.type      AS event_type,
		       e.occurred_at AS occurred_at,
		       e.resource_uid AS uid
		ORDER BY e.occurred_at DESC
		LIMIT 100`

	res, err := q.run(ctx, cypher, map[string]any{
		"cid":   clusterID,
		"kind":  kind,
		"ns":    namespace,
		"name":  name,
		"since": since.UTC().Format(time.RFC3339),
	})
	if err != nil {
		// KubeEvent nodes may not exist yet — graceful degradation.
		return nil, nil
	}
	return extractEvidence(res, kind, namespace, name), nil
}

// StoreEvent writes a Kubernetes event into Neo4j as a KubeEvent node and
// creates a RELATES_TO edge to the affected resource node. Idempotent via MERGE.
// Call this from the collector when processing health/pod events from Kafka.
func (q *Querier) StoreEvent(
	ctx context.Context,
	clusterID, kind, namespace, name, uid,
	reason, message, eventType string,
	occurredAt time.Time,
) error {
	// Event ID is deterministic to prevent duplicates on re-delivery.
	eventID := fmt.Sprintf("evt/%s/%s/%s/%s/%s/%s",
		clusterID, kind, namespace, name, reason,
		occurredAt.UTC().Format("20060102T150405Z"))

	_, err := q.run(ctx, `
		MERGE (e:KubeEvent {event_id: $event_id})
		SET e.cluster_id         = $cid,
		    e.resource_kind      = $kind,
		    e.resource_namespace = $ns,
		    e.resource_name      = $name,
		    e.resource_uid       = $uid,
		    e.reason             = $reason,
		    e.message            = $message,
		    e.type               = $event_type,
		    e.occurred_at        = $occurred_at,
		    e.stored_at          = datetime()
		WITH e
		OPTIONAL MATCH (n {node_id: $node_id})
		FOREACH (x IN CASE WHEN n IS NOT NULL THEN [1] ELSE [] END |
		    MERGE (e)-[:RELATES_TO]->(n)
		)`,
		map[string]any{
			"event_id":    eventID,
			"cid":         clusterID,
			"kind":        kind,
			"ns":          namespace,
			"name":        name,
			"uid":         uid,
			"reason":      reason,
			"message":     message,
			"event_type":  eventType,
			"occurred_at": occurredAt.UTC().Format(time.RFC3339),
			"node_id":     nodeKey(clusterID, kind, namespace, name),
		})
	return err
}

// GetBlastRadius returns all nodes downstream of the given resource —
// everything that depends on it and would be affected if it failed.
func (q *Querier) GetBlastRadius(
	ctx context.Context,
	clusterID, kind, namespace, name string,
) ([]rca.DependencyNode, error) {
	nodeID := nodeKey(clusterID, kind, namespace, name)

	// Reverse direction: find everything that has a path TO this node.
	cypher := `
		MATCH (start {node_id: $node_id})
		MATCH path = (n)-[*1..8]->(start)
		WHERE n.cluster_id = $cid
		  AND n.node_id <> $node_id
		  AND (n.deleted IS NULL OR n.deleted = false)
		WITH DISTINCT n, min(length(path)) AS depth
		RETURN n.node_id AS id, n.kind AS kind,
		       n.namespace AS ns, n.name AS name, depth
		ORDER BY depth ASC
		LIMIT 500`

	res, err := q.run(ctx, cypher, map[string]any{
		"node_id": nodeID,
		"cid":     clusterID,
	})
	if err != nil {
		return nil, fmt.Errorf("blast radius query: %w", err)
	}
	return extractDependencyNodes(res), nil
}

// GetServiceDependencies returns the direct service-level dependencies
// of the given service — services that it routes to or is selected by.
func (q *Querier) GetServiceDependencies(
	ctx context.Context,
	clusterID, namespace, serviceName string,
) ([]rca.DependencyNode, error) {
	nodeID := nodeKey(clusterID, "Service", namespace, serviceName)

	cypher := `
		MATCH (svc {node_id: $node_id})-[:ROUTES_TO|SELECTED_BY]->(n)
		WHERE n.cluster_id = $cid
		  AND (n.deleted IS NULL OR n.deleted = false)
		RETURN n.node_id AS id, n.kind AS kind,
		       n.namespace AS ns, n.name AS name, 1 AS depth
		LIMIT 50`

	res, err := q.run(ctx, cypher, map[string]any{
		"node_id": nodeID,
		"cid":     clusterID,
	})
	if err != nil {
		return nil, fmt.Errorf("service dependency query: %w", err)
	}
	return extractDependencyNodes(res), nil
}

// ─── HTTP transport ───────────────────────────────────────────────────────────

type neo4jReq struct {
	Statements []neo4jStmt `json:"statements"`
}

type neo4jStmt struct {
	Statement  string         `json:"statement"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type neo4jResp struct {
	Results []struct {
		Columns []string `json:"columns"`
		Data    []struct {
			Row []any `json:"row"`
		} `json:"data"`
	} `json:"results"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (q *Querier) run(ctx context.Context, cypher string, params map[string]any) (*neo4jResp, error) {
	if q.baseURL == "" || q.password == "" {
		return nil, fmt.Errorf("neo4j not configured")
	}
	body, _ := json.Marshal(neo4jReq{
		Statements: []neo4jStmt{{Statement: cypher, Parameters: params}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		q.baseURL+"/db/neo4j/tx/commit", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(q.user, q.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neo4j http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var nr neo4jResp
	if err := json.Unmarshal(raw, &nr); err != nil {
		return nil, fmt.Errorf("neo4j parse: %w", err)
	}
	if len(nr.Errors) > 0 {
		return nil, fmt.Errorf("neo4j: %s", nr.Errors[0].Message)
	}
	return &nr, nil
}

// ─── Result extraction ────────────────────────────────────────────────────────

func extractDependencyNodes(res *neo4jResp) []rca.DependencyNode {
	if res == nil || len(res.Results) == 0 {
		return nil
	}
	cols := columnIndex(res.Results[0].Columns)
	var nodes []rca.DependencyNode
	for _, row := range res.Results[0].Data {
		r := row.Row
		n := rca.DependencyNode{}
		if i, ok := cols["id"];   ok && i < len(r) { n.EntityID, _   = r[i].(string) }
		if i, ok := cols["kind"]; ok && i < len(r) { n.EntityKind, _ = r[i].(string) }
		if i, ok := cols["ns"];   ok && i < len(r) { n.Namespace, _  = r[i].(string) }
		if i, ok := cols["name"]; ok && i < len(r) { n.Name, _       = r[i].(string) }
		if i, ok := cols["depth"]; ok && i < len(r) {
			if d, ok := r[i].(float64); ok {
				n.Depth = int(d)
			}
		}
		if n.EntityID != "" {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func extractEvidence(res *neo4jResp, kind, namespace, name string) []rca.Evidence {
	if res == nil || len(res.Results) == 0 {
		return nil
	}
	cols := columnIndex(res.Results[0].Columns)
	ref := rca.ResourceRef{Kind: kind, Namespace: namespace, Name: name}
	var evs []rca.Evidence
	for _, row := range res.Results[0].Data {
		r := row.Row
		ev := rca.Evidence{
			Type:        "k8s_event",
			ResourceRef: ref,
			Strength:    0.5,
		}
		var reason, message, eventType string
		if i, ok := cols["reason"];      ok && i < len(r) { reason, _      = r[i].(string) }
		if i, ok := cols["message"];     ok && i < len(r) { message, _     = r[i].(string) }
		if i, ok := cols["event_type"];  ok && i < len(r) { eventType, _   = r[i].(string) }
		if i, ok := cols["occurred_at"]; ok && i < len(r) {
			if ts, ok := r[i].(string); ok {
				ev.OccurredAt, _ = time.Parse(time.RFC3339, ts)
			}
		}
		if i, ok := cols["uid"]; ok && i < len(r) {
			ev.ResourceRef.UID, _ = r[i].(string)
		}
		ev.Source = reason
		ev.Description = message
		// Warning-class events are stronger evidence than Normal events.
		if strings.EqualFold(eventType, "Warning") {
			ev.Strength = 0.75
		}
		// Crash/OOM events are the strongest evidence.
		if reason == "OOMKilling" || reason == "BackOff" || reason == "Failed" {
			ev.Strength = 0.95
		}
		evs = append(evs, ev)
	}
	return evs
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func columnIndex(cols []string) map[string]int {
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[c] = i
	}
	return idx
}

func nodeKey(clusterID, kind, namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("%s/%s/%s", clusterID, kind, name)
	}
	return fmt.Sprintf("%s/%s/%s/%s", clusterID, kind, namespace, name)
}
