// Package topology writes K8s resource events into Neo4j topology graph.
// Uses Neo4j HTTP API — no Go driver dependency, faster builds.
package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aileron-platform/aileron/agent/pkg/events"
)

// Writer maintains topology in Neo4j from incoming IntelligenceEvents.
type Writer struct {
	baseURL  string
	user     string
	password string
	client   *http.Client
}

// NewWriter creates a topology writer using the Neo4j HTTP API.
func NewWriter(baseURL, user, password string) *Writer {
	return &Writer{
		baseURL:  baseURL,
		user:     user,
		password: password,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// Verify checks Neo4j is reachable.
func (w *Writer) Verify(ctx context.Context) error {
	_, err := w.run(ctx, "RETURN 1 AS ok", nil)
	return err
}

// EnsureConstraints creates uniqueness constraints for all K8s resource kinds.
func EnsureConstraints(ctx context.Context, w *Writer) error {
	kinds := []string{
		"Pod", "KNode", "Deployment", "StatefulSet", "DaemonSet",
		"ReplicaSet", "Service", "Ingress", "PersistentVolumeClaim",
		"PersistentVolume", "ConfigMap", "Secret", "Namespace",
		"ServiceAccount", "HorizontalPodAutoscaler",
	}
	for _, kind := range kinds {
		label := sanitizeLabel(kind)
		q := fmt.Sprintf(
			`CREATE CONSTRAINT IF NOT EXISTS FOR (n:%s) REQUIRE n.node_id IS UNIQUE`,
			label)
		if _, err := w.run(ctx, q, nil); err != nil {
			log.Printf("topology: constraint %s: %v", label, err)
		}
	}
	return nil
}

// Process implements ingestion.EventProcessor.
// Routes resource lifecycle events to upsert or delete in Neo4j.
func (w *Writer) Process(ctx context.Context, ev *events.IntelligenceEvent) error {
	r := ev.Resource
	switch ev.Type {
	case events.EventResourceCreated, events.EventResourceUpdated,
		events.EventDeploymentRollout, events.EventConfigMapChanged,
		events.EventSecretRotated, events.EventRBACChanged, events.EventHPAScaled:
		return w.upsertNode(ctx, ev.ClusterID, r.Kind, r.Namespace, r.Name, r.UID, r.Labels)
	case events.EventResourceDeleted:
		return w.deleteNode(ctx, ev.ClusterID, r.Kind, r.Namespace, r.Name)
	}
	return nil
}

// upsertNode creates or updates a node in Neo4j.
func (w *Writer) upsertNode(ctx context.Context,
	clusterID, kind, namespace, name, uid string,
	labels map[string]string,
) error {
	label := sanitizeLabel(kind)
	nodeID := nodeKey(clusterID, kind, namespace, name)
	labelStr := formatLabels(labels)

	q := fmt.Sprintf(`
		MERGE (n:%s {node_id: $node_id})
		SET n.cluster_id = $cluster_id,
		    n.kind       = $kind,
		    n.namespace  = $namespace,
		    n.name       = $name,
		    n.uid        = $uid,
		    n.labels     = $labels,
		    n.last_seen  = datetime(),
		    n.deleted    = false`, label)

	_, err := w.run(ctx, q, map[string]any{
		"node_id": nodeID, "cluster_id": clusterID,
		"kind": kind, "namespace": namespace,
		"name": name, "uid": uid, "labels": labelStr,
	})
	if err != nil {
		log.Printf("topology: upsert %s: %v", nodeID, err)
	}
	return nil
}

// deleteNode soft-deletes a node.
func (w *Writer) deleteNode(ctx context.Context, clusterID, kind, namespace, name string) error {
	nodeID := nodeKey(clusterID, kind, namespace, name)
	_, err := w.run(ctx, `
		MATCH (n {node_id: $node_id})
		SET n.deleted = true, n.deleted_at = datetime()`,
		map[string]any{"node_id": nodeID})
	return err
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
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (w *Writer) run(ctx context.Context, cypher string, params map[string]any) (*neo4jResp, error) {
	body, _ := json.Marshal(neo4jReq{
		Statements: []neo4jStmt{{Statement: cypher, Parameters: params}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		w.baseURL+"/db/neo4j/tx/commit", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(w.user, w.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neo4j http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var nr neo4jResp
	if json.Unmarshal(raw, &nr) == nil && len(nr.Errors) > 0 {
		return nil, fmt.Errorf("neo4j: %s", nr.Errors[0].Message)
	}
	return &nr, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func nodeKey(clusterID, kind, namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("%s/%s/%s", clusterID, kind, name)
	}
	return fmt.Sprintf("%s/%s/%s/%s", clusterID, kind, namespace, name)
}

func sanitizeLabel(kind string) string {
	if kind == "Node" {
		return "KNode"
	}
	return strings.ReplaceAll(kind, "-", "_")
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}
