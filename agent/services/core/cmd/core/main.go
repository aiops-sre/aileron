// kubesense-core — intelligence API server.
// Uses Neo4j HTTP API (no Go driver needed — smaller binary, faster builds).
// Uses lib/pq for PostgreSQL cluster registry queries.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/aileron-platform/aileron/agent/services/core/internal/investigation"
	"github.com/aileron-platform/aileron/agent/services/core/internal/rca"
)

func main() {
	port      := envOrDefault("PORT", "8080")
	dbURL     := envOrDefault("DATABASE_URL", "")
	neo4jURL  := envOrDefault("NEO4J_URL", "http://kubesense-neo4j.aileron-agent.svc.cluster.local:7474")
	neo4jUser := envOrDefault("NEO4J_USER", "neo4j")
	neo4jPass := envOrDefault("NEO4J_PASSWORD", "")
	kafkaBrokers := strings.Split(envOrDefault("KAFKA_BROKERS",
		"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"), ",")

	// Convert bolt URL to HTTP for the REST API
	neo4jHTTP := boltToHTTP(neo4jURL)

	log.Printf("kubesense-core starting: port=%s neo4j=%s kafka=%s", port, neo4jHTTP, strings.Join(kafkaBrokers, ","))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// PostgreSQL
	var db *sql.DB
	if dbURL != "" {
		var err error
		db, err = sql.Open("postgres", dbURL)
		if err != nil {
			log.Printf("core: postgres: %v", err)
		} else {
			db.SetMaxOpenConns(20)
			db.SetConnMaxLifetime(5 * time.Minute)
			log.Printf("core: PostgreSQL connected")
		}
	}

	neo4j := &neo4jHTTPClient{
		baseURL:  neo4jHTTP,
		user:     neo4jUser,
		password: neo4jPass,
		client:   &http.Client{Timeout: 30 * time.Second},
	}

	// Verify Neo4j connectivity
	if neo4jPass != "" {
		if err := neo4j.ping(); err != nil {
			log.Printf("core: Neo4j not reachable: %v — topology queries disabled", err)
		} else {
			log.Printf("core: Neo4j HTTP API connected")
		}
	}

	// ── Investigation consumer — wires AlertHub investigation requests to the RCA engine ──
	// Consumes kubesense.investigations.requests; publishes to kubesense.investigations.results.
	// Fail-open: if Kafka is unavailable the HTTP server still starts.
	if len(kafkaBrokers) > 0 && kafkaBrokers[0] != "" {
		neo4jAdapter := rca.NewNeo4jAdapter(neo4j)
		dbAdapter := rca.NewDBAdapter(db)
		rcaEngine := rca.NewEngine(neo4jAdapter, dbAdapter, dbAdapter)
		invConsumer, err := investigation.NewConsumer(kafkaBrokers, rcaEngine)
		if err != nil {
			log.Printf("core: investigation consumer init failed: %v — investigations disabled", err)
		} else {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("core: investigation consumer panicked: %v", r)
					}
				}()
				invConsumer.Run(ctx)
			}()
			defer invConsumer.Close()
			log.Printf("core: investigation consumer started — consuming from %s", investigation.TopicRequests)
		}
	}

	srv := &coreServer{db: db, neo4j: neo4j}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ready")) })

	mux.HandleFunc("/api/v1/clusters", srv.listClusters)
	mux.HandleFunc("/api/v1/clusters/", srv.getCluster)
	mux.HandleFunc("/api/v1/topology", srv.getTopology)

	httpSrv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("kubesense-core listening on :%s", port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("core: http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("kubesense-core: shutdown")
}

// ─── Neo4j HTTP client ────────────────────────────────────────────────────────

type neo4jHTTPClient struct {
	baseURL  string
	user     string
	password string
	client   *http.Client
}

type neo4jRequest struct {
	Statements []neo4jStatement `json:"statements"`
}

type neo4jStatement struct {
	Statement  string         `json:"statement"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type neo4jResponse struct {
	Results []struct {
		Columns []string `json:"columns"`
		Data    []struct {
			Row []any `json:"row"`
		} `json:"data"`
	} `json:"results"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *neo4jHTTPClient) query(cypher string, params map[string]any) (*neo4jResponse, error) {
	if c.baseURL == "" || c.password == "" {
		return nil, fmt.Errorf("neo4j not configured")
	}
	body, _ := json.Marshal(neo4jRequest{
		Statements: []neo4jStatement{{Statement: cypher, Parameters: params}},
	})
	req, err := http.NewRequest("POST", c.baseURL+"/db/neo4j/tx/commit", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neo4j http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var nr neo4jResponse
	if err := json.Unmarshal(raw, &nr); err != nil {
		return nil, fmt.Errorf("neo4j parse: %w", err)
	}
	if len(nr.Errors) > 0 {
		return nil, fmt.Errorf("neo4j error: %s", nr.Errors[0].Message)
	}
	return &nr, nil
}

func (c *neo4jHTTPClient) ping() error {
	_, err := c.query("RETURN 1 AS ok", nil)
	return err
}

// Query implements rca.Neo4jHTTPQuerier — exposes the private query method
// so the rca package adapters can execute Cypher without importing cmd/core.
func (c *neo4jHTTPClient) Query(cypher string, params map[string]any) (*rca.Neo4jResponse, error) {
	resp, err := c.query(cypher, params)
	if err != nil {
		return nil, err
	}
	// Convert internal neo4jResponse to rca.Neo4jResponse (same structure).
	data, _ := json.Marshal(resp)
	var rcaResp rca.Neo4jResponse
	if err := json.Unmarshal(data, &rcaResp); err != nil {
		return nil, fmt.Errorf("response conversion: %w", err)
	}
	return &rcaResp, nil
}

func (c *neo4jHTTPClient) countNodes(clusterID string) int {
	res, err := c.query(
		`MATCH (n) WHERE n.cluster_id = $cid AND (n.deleted IS NULL OR n.deleted = false) RETURN count(n) AS cnt`,
		map[string]any{"cid": clusterID},
	)
	if err != nil || len(res.Results) == 0 || len(res.Results[0].Data) == 0 {
		return 0
	}
	row := res.Results[0].Data[0].Row
	if len(row) == 0 {
		return 0
	}
	if n, ok := row[0].(float64); ok {
		return int(n)
	}
	return 0
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

type coreServer struct {
	db    *sql.DB
	neo4j *neo4jHTTPClient
}

func (s *coreServer) listClusters(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		jsonError(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, first_seen, last_heartbeat, last_agent_id, agent_version, node_count, status
		FROM kubesense_clusters ORDER BY last_heartbeat DESC`)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type cluster struct {
		ID            string    `json:"id"`
		FirstSeen     time.Time `json:"first_seen"`
		LastHeartbeat time.Time `json:"last_heartbeat"`
		LastAgentID   string    `json:"last_agent_id"`
		AgentVersion  string    `json:"agent_version"`
		NodeCount     int       `json:"node_count"`
		Status        string    `json:"status"`
	}
	var clusters []cluster
	for rows.Next() {
		var c cluster
		if err := rows.Scan(&c.ID, &c.FirstSeen, &c.LastHeartbeat,
			&c.LastAgentID, &c.AgentVersion, &c.NodeCount, &c.Status); err != nil {
			continue
		}
		// Live node count from Neo4j
		if s.neo4j != nil {
			if n := s.neo4j.countNodes(c.ID); n > 0 {
				c.NodeCount = n
			}
		}
		clusters = append(clusters, c)
	}
	if clusters == nil {
		clusters = []cluster{}
	}
	jsonOK(w, map[string]any{"clusters": clusters, "total": len(clusters)})
}

func (s *coreServer) getCluster(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/v1/clusters/"):]
	if id == "" || s.db == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	type cluster struct {
		ID            string    `json:"id"`
		FirstSeen     time.Time `json:"first_seen"`
		LastHeartbeat time.Time `json:"last_heartbeat"`
		AgentVersion  string    `json:"agent_version"`
		NodeCount     int       `json:"node_count"`
		Status        string    `json:"status"`
	}
	var c cluster
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, first_seen, last_heartbeat, agent_version, node_count, status
		FROM kubesense_clusters WHERE id = $1`, id,
	).Scan(&c.ID, &c.FirstSeen, &c.LastHeartbeat, &c.AgentVersion, &c.NodeCount, &c.Status)
	if err == sql.ErrNoRows {
		jsonError(w, "cluster not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.neo4j != nil {
		if n := s.neo4j.countNodes(c.ID); n > 0 {
			c.NodeCount = n
		}
	}
	jsonOK(w, c)
}

func (s *coreServer) getTopology(w http.ResponseWriter, r *http.Request) {
	if s.neo4j == nil || s.neo4j.password == "" {
		jsonError(w, "Neo4j not configured", http.StatusServiceUnavailable)
		return
	}
	clusterID := r.URL.Query().Get("cluster")
	kind := r.URL.Query().Get("kind")

	var cypher string
	var params map[string]any

	if kind != "" {
		label := sanitizeLabel(kind)
		cypher = fmt.Sprintf(`
			MATCH (n:%s)
			WHERE n.cluster_id = $cid AND (n.deleted IS NULL OR n.deleted = false)
			RETURN n.node_id AS id, n.kind AS kind, n.namespace AS ns,
			       n.name AS name, n.labels AS labels
			LIMIT 500`, label)
		params = map[string]any{"cid": clusterID}
	} else {
		cypher = `
			MATCH (n)
			WHERE n.cluster_id = $cid AND (n.deleted IS NULL OR n.deleted = false)
			RETURN n.node_id AS id, n.kind AS kind, n.namespace AS ns,
			       n.name AS name, n.labels AS labels
			LIMIT 500`
		params = map[string]any{"cid": clusterID}
	}

	res, err := s.neo4j.query(cypher, params)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type node struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Labels    string `json:"labels"`
	}
	var nodes []node
	if len(res.Results) > 0 {
		cols := res.Results[0].Columns
		colIdx := map[string]int{}
		for i, c := range cols {
			colIdx[c] = i
		}
		for _, row := range res.Results[0].Data {
			n := node{}
			if i, ok := colIdx["id"];   ok && i < len(row.Row) { n.ID, _        = row.Row[i].(string) }
			if i, ok := colIdx["kind"]; ok && i < len(row.Row) { n.Kind, _      = row.Row[i].(string) }
			if i, ok := colIdx["ns"];   ok && i < len(row.Row) { n.Namespace, _ = row.Row[i].(string) }
			if i, ok := colIdx["name"]; ok && i < len(row.Row) { n.Name, _      = row.Row[i].(string) }
			if i, ok := colIdx["labels"]; ok && i < len(row.Row) { n.Labels, _  = row.Row[i].(string) }
			nodes = append(nodes, n)
		}
	}
	if nodes == nil {
		nodes = []node{}
	}
	jsonOK(w, map[string]any{
		"cluster_id": clusterID,
		"nodes":      nodes,
		"total":      len(nodes),
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func sanitizeLabel(kind string) string {
	if kind == "Node" {
		return "KNode"
	}
	return kind
}

// boltToHTTP converts bolt:// or neo4j:// URL to the HTTP API URL.
func boltToHTTP(u string) string {
	for _, prefix := range []string{"neo4j://", "bolt://"} {
		if len(u) > len(prefix) && u[:len(prefix)] == prefix {
			host := u[len(prefix):]
			return "http://" + host[:len(host)-4] + "7474" // replace :7687 with :7474
		}
	}
	return u
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
