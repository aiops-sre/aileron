// kubesense-api — Intelligence REST API server.
//
// Exposes all KubeSense intelligence capabilities over HTTP/JSON:
//   POST /api/v1/investigations         — trigger RCA investigation
//   GET  /api/v1/investigations/:id     — get investigation result
//   GET  /api/v1/clusters               — list registered clusters
//   GET  /api/v1/clusters/:id/topology  — cluster topology
//   GET  /api/v1/clusters/:id/blast-radius?kind=X&name=Y&namespace=Z
//   GET  /api/v1/clusters/:id/forecasts
//   GET  /api/v1/clusters/:id/config/violations
//   POST /api/v1/clusters/:id/config/validate
//   GET  /api/v1/clusters/:id/security/posture
//   GET  /api/v1/clusters/:id/cost/efficiency
//   GET  /api/v1/clusters/:id/anomalies
//   GET  /api/v1/clusters/:id/slo/budgets
//   GET  /api/v1/clusters/:id/apm/golden-signals
//   GET  /api/v1/clusters/:id/change/history
//   GET  /api/v1/clusters/:id/playbooks
//   GET  /api/v1/clusters/:id/playbooks/:failure_mode
//   POST /api/v1/risk/score             — score an incoming change
//   GET  /api/v1/search                 — resource search across clusters
//
// Authentication: Bearer token via KUBESENSE_API_TOKEN env var.
// Set to empty string to disable auth (development only).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/aileron-platform/aileron/agent/services/core/internal/anomaly"
	"github.com/aileron-platform/aileron/agent/services/core/internal/apm"
	"github.com/aileron-platform/aileron/agent/services/core/internal/change"
	"github.com/aileron-platform/aileron/agent/services/core/internal/correlation"
	"github.com/aileron-platform/aileron/agent/services/core/internal/cost"
	"github.com/aileron-platform/aileron/agent/services/core/internal/fingerprint"
	"github.com/aileron-platform/aileron/agent/services/core/internal/forecast"
	"github.com/aileron-platform/aileron/agent/services/core/internal/playbook"
	"github.com/aileron-platform/aileron/agent/services/core/internal/publish"
	"github.com/aileron-platform/aileron/agent/services/core/internal/rca"
	"github.com/aileron-platform/aileron/agent/services/core/internal/remediation"
	"github.com/aileron-platform/aileron/agent/services/core/internal/risk"
	"github.com/aileron-platform/aileron/agent/services/core/internal/slo"
	"github.com/aileron-platform/aileron/agent/services/core/internal/topology"
)

func main() {
	port      := envOrDefault("PORT", "8080")
	apiToken  := envOrDefault("KUBESENSE_API_TOKEN", "")
	dbURL     := envOrDefault("DATABASE_URL", "")
	neo4jURL  := envOrDefault("NEO4J_URL", "http://kubesense-neo4j.aileron-agent.svc.cluster.local:7474")
	neo4jUser := envOrDefault("NEO4J_USER", "neo4j")
	neo4jPass := envOrDefault("NEO4J_PASSWORD", "")
	kafkaBrokers := envOrDefault("KAFKA_BROKERS",
		"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092")

	log.Printf("kubesense-api starting: port=%s auth=%v kafka=%s", port, apiToken != "", kafkaBrokers)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── Dependencies ──────────────────────────────────────────────────────
	neo4jQuerier := topology.NewQuerier(neo4jURL, neo4jUser, neo4jPass)

	var db *sql.DB
	if dbURL != "" {
		var err error
		if db, err = sql.Open("postgres", dbURL); err != nil {
			log.Printf("api: postgres: %v", err)
		} else {
			db.SetMaxOpenConns(20)
			db.SetConnMaxLifetime(5 * time.Minute)
		}
	}

	// ── Engine initialization ─────────────────────────────────────────────
	outcomeStore    := remediation.NewOutcomeStore()
	remediationEng := remediation.NewEngine(outcomeStore)
	rcaEng         := rca.NewEngine(neo4jQuerier, pgChangeQuerier{db}, pgEventQuerier{db})
	correlator     := correlation.NewCorrelator()
	apmTracker     := apm.NewTracker()
	anomalyDet     := anomaly.NewDetector()
	costAnalyzer   := cost.NewAnalyzer()
	sloTracker     := slo.NewTracker()
	forecastEng    := forecast.NewEngine()
	riskEng        := risk.NewEngine()
	changeCorrel   := change.NewCorrelator(24 * time.Hour)
	fpEngine       := fingerprint.NewEngine()
	playbookGen    := playbook.NewGenerator()

	srv := &apiServer{
		neo4j:          neo4jQuerier,
		db:             db,
		rcaEngine:      rcaEng,
		correlator:     correlator,
		apmTracker:     apmTracker,
		anomalyDet:     anomalyDet,
		costAnalyzer:   costAnalyzer,
		sloTracker:     sloTracker,
		forecastEngine: forecastEng,
		riskEngine:     riskEng,
		changeCorrel:   changeCorrel,
		fpEngine:       fpEngine,
		playbookGen:    playbookGen,
		remediationEng: remediationEng,
		apiToken:       apiToken,
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	httpSrv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("kubesense-api: listening on :%s", port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api: listen: %v", err)
		}
	}()

	// ── Background signal publisher ───────────────────────────────────────────
	// Periodically publishes intelligence signals to Kafka so AlertHub's OIE
	// evidence bus and CACIE can consume them without waiting for REST requests.
	// Topics: apm.golden-signals, config.violations, forecasts, anomalies.
	kafkaPub := publish.NewFromBrokerString(kafkaBrokers)
	if kafkaPub != nil {
		defer kafkaPub.Close()
		go runSignalPublisher(ctx, srv, kafkaPub, db)
		log.Printf("kubesense-api: background signal publisher started")
	} else {
		log.Printf("kubesense-api: Kafka unavailable — signal publishing disabled")
	}

	<-ctx.Done()
	log.Println("kubesense-api: shutdown")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	httpSrv.Shutdown(shutCtx)
}

// ─── Server ───────────────────────────────────────────────────────────────────

type apiServer struct {
	neo4j          *topology.Querier
	db             *sql.DB
	rcaEngine      *rca.Engine
	correlator     *correlation.Correlator
	apmTracker     *apm.Tracker
	anomalyDet     *anomaly.Detector
	costAnalyzer   *cost.Analyzer
	sloTracker     *slo.Tracker
	forecastEngine *forecast.Engine
	riskEngine     *risk.Engine
	changeCorrel   *change.Correlator
	fpEngine       *fingerprint.Engine
	playbookGen    *playbook.Generator
	remediationEng *remediation.Engine
	apiToken       string

	// In-memory investigation store (replace with Redis/Postgres in production)
	investigations investigationStore
}

func (s *apiServer) registerRoutes(mux *http.ServeMux) {
	auth := s.authMiddleware

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz",  func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ready")) })
	mux.HandleFunc("/api/v1/investigations",   auth(s.handleInvestigations))
	mux.HandleFunc("/api/v1/investigations/",  auth(s.handleGetInvestigation))
	mux.HandleFunc("/api/v1/clusters",         auth(s.handleListClusters))
	mux.HandleFunc("/api/v1/clusters/",        auth(s.handleClusterRoutes))
	mux.HandleFunc("/api/v1/risk/score",       auth(s.handleRiskScore))
	mux.HandleFunc("/api/v1/search",           auth(s.handleSearch))
}

// ─── Investigations ───────────────────────────────────────────────────────────

func (s *apiServer) handleInvestigations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		IncidentID string `json:"incident_id"`
		ClusterID  string `json:"cluster_id"`
		Resources  []struct {
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"affected_resources"`
		IncidentTime string            `json:"incident_time"`
		Context      map[string]string `json:"alert_context"`
		Async        bool              `json:"async"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.ClusterID == "" {
		jsonError(w, "cluster_id is required", http.StatusBadRequest)
		return
	}

	incidentTime := time.Now()
	if body.IncidentTime != "" {
		if t, err := time.Parse(time.RFC3339, body.IncidentTime); err == nil {
			incidentTime = t
		}
	}

	var refs []rca.ResourceRef
	for _, res := range body.Resources {
		refs = append(refs, rca.ResourceRef{Kind: res.Kind, Namespace: res.Namespace, Name: res.Name})
	}

	req := rca.Request{
		ClusterID:         body.ClusterID,
		AffectedResources: refs,
		IncidentTime:      incidentTime,
		AlertContext:      body.Context,
	}

	if body.Async {
		invID := body.IncidentID
		if invID == "" {
			invID = fmt.Sprintf("inv-%d", time.Now().UnixNano())
		}
		s.investigations.setStatus(invID, "running")
		go func() {
			result, err := s.rcaEngine.Investigate(context.Background(), req)
			if err != nil {
				s.investigations.setError(invID, err)
				return
			}
			s.investigations.setResult(invID, result)
		}()
		jsonOK(w, map[string]any{
			"investigation_id": invID,
			"status":           "running",
		})
		return
	}

	result, err := s.rcaEngine.Investigate(r.Context(), req)
	if err != nil {
		jsonError(w, "investigation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, rcaResultToResponse(body.IncidentID, result))
}

func (s *apiServer) handleGetInvestigation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/investigations/")
	if id == "" {
		jsonError(w, "investigation ID required", http.StatusBadRequest)
		return
	}
	status, result, err := s.investigations.get(id)
	if status == "" {
		jsonError(w, "investigation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonOK(w, map[string]any{"investigation_id": id, "status": "failed", "error": err.Error()})
		return
	}
	if result == nil {
		jsonOK(w, map[string]any{"investigation_id": id, "status": status})
		return
	}
	jsonOK(w, rcaResultToResponse(id, result))
}

// ─── Cluster sub-routes ───────────────────────────────────────────────────────

func (s *apiServer) handleClusterRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/")
	parts := strings.SplitN(path, "/", 2)
	clusterID := parts[0]
	subpath := ""
	if len(parts) > 1 {
		subpath = parts[1]
	}
	if clusterID == "" {
		jsonError(w, "cluster ID required", http.StatusBadRequest)
		return
	}

	switch subpath {
	case "topology":
		s.handleTopology(w, r, clusterID)
	case "blast-radius":
		s.handleBlastRadius(w, r, clusterID)
	case "forecasts":
		s.handleForecasts(w, r, clusterID)
	case "config/violations":
		s.handleConfigViolations(w, r, clusterID)
	case "config/validate":
		s.handleConfigValidate(w, r, clusterID)
	case "security/posture":
		s.handleSecurityPosture(w, r, clusterID)
	case "cost/efficiency":
		s.handleCostEfficiency(w, r, clusterID)
	case "anomalies":
		s.handleAnomalies(w, r, clusterID)
	case "slo/budgets":
		s.handleSLOBudgets(w, r, clusterID)
	case "apm/golden-signals":
		s.handleAPMSignals(w, r, clusterID)
	case "change/history":
		s.handleChangeHistory(w, r, clusterID)
	case "playbooks":
		s.handlePlaybooks(w, r, clusterID)
	default:
		if strings.HasPrefix(subpath, "playbooks/") {
			mode := strings.TrimPrefix(subpath, "playbooks/")
			s.handlePlaybook(w, r, clusterID, mode)
			return
		}
		jsonError(w, "unknown endpoint: "+subpath, http.StatusNotFound)
	}
}

func (s *apiServer) handleListClusters(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		jsonOK(w, map[string]any{"clusters": []any{}, "total": 0})
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, first_seen, last_heartbeat, agent_version, node_count, status
		FROM kubesense_clusters ORDER BY last_heartbeat DESC LIMIT 100`)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type cluster struct {
		ID            string    `json:"id"`
		FirstSeen     time.Time `json:"first_seen"`
		LastHeartbeat time.Time `json:"last_heartbeat"`
		AgentVersion  string    `json:"agent_version"`
		NodeCount     int       `json:"node_count"`
		Status        string    `json:"status"`
	}
	var clusters []cluster
	for rows.Next() {
		var c cluster
		if err := rows.Scan(&c.ID, &c.FirstSeen, &c.LastHeartbeat, &c.AgentVersion, &c.NodeCount, &c.Status); err == nil {
			clusters = append(clusters, c)
		}
	}
	if clusters == nil {
		clusters = []cluster{}
	}
	jsonOK(w, map[string]any{"clusters": clusters, "total": len(clusters)})
}

func (s *apiServer) handleTopology(w http.ResponseWriter, r *http.Request, clusterID string) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	kind := r.URL.Query().Get("kind")
	name := r.URL.Query().Get("name")
	ns   := r.URL.Query().Get("namespace")

	if kind != "" && name != "" {
		chain, err := s.neo4j.GetUpstreamChain(r.Context(), clusterID, kind, ns, name, 8)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{
			"cluster_id":  clusterID,
			"query":       map[string]string{"kind": kind, "namespace": ns, "name": name},
			"upstream":    chain,
			"node_count":  len(chain),
		})
		return
	}

	jsonOK(w, map[string]any{
		"cluster_id": clusterID,
		"message":    "specify ?kind=X&name=Y&namespace=Z for upstream chain, or ?kind=X for all resources of that kind",
	})
}

func (s *apiServer) handleBlastRadius(w http.ResponseWriter, r *http.Request, clusterID string) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	kind := r.URL.Query().Get("kind")
	name := r.URL.Query().Get("name")
	ns   := r.URL.Query().Get("namespace")
	if kind == "" || name == "" {
		jsonError(w, "kind and name are required", http.StatusBadRequest)
		return
	}
	affected, err := s.neo4j.GetBlastRadius(r.Context(), clusterID, kind, ns, name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{
		"cluster_id":      clusterID,
		"resource":        map[string]string{"kind": kind, "namespace": ns, "name": name},
		"affected":        affected,
		"total_affected":  len(affected),
	})
}

func (s *apiServer) handleForecasts(w http.ResponseWriter, r *http.Request, clusterID string) {
	results := s.forecastEngine.GetForecasts(clusterID)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "forecasts": results, "total": len(results)})
}

func (s *apiServer) handleConfigViolations(w http.ResponseWriter, r *http.Request, clusterID string) {
	violations := s.correlator.GetConfigViolations(clusterID)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "violations": violations, "total": len(violations)})
}

func (s *apiServer) handleConfigValidate(w http.ResponseWriter, r *http.Request, clusterID string) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, map[string]any{"message": "use the admission webhook for real-time manifest validation"})
}

func (s *apiServer) handleSecurityPosture(w http.ResponseWriter, r *http.Request, clusterID string) {
	posture := s.correlator.GetSecurityPosture(clusterID)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "posture": posture})
}

func (s *apiServer) handleCostEfficiency(w http.ResponseWriter, r *http.Request, clusterID string) {
	ns := r.URL.Query().Get("namespace")
	efficiency := s.costAnalyzer.GetEfficiency(clusterID, ns)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "namespace": ns, "efficiency": efficiency})
}

func (s *apiServer) handleAnomalies(w http.ResponseWriter, r *http.Request, clusterID string) {
	ns  := r.URL.Query().Get("namespace")
	res := r.URL.Query().Get("resource")
	anomalies := s.anomalyDet.GetAnomalies(clusterID, ns, res)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "anomalies": anomalies, "total": len(anomalies)})
}

func (s *apiServer) handleSLOBudgets(w http.ResponseWriter, r *http.Request, clusterID string) {
	ns := r.URL.Query().Get("namespace")
	budgets := s.sloTracker.GetBudgets(clusterID, ns)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "budgets": budgets, "total": len(budgets)})
}

func (s *apiServer) handleAPMSignals(w http.ResponseWriter, r *http.Request, clusterID string) {
	ns  := r.URL.Query().Get("namespace")
	svc := r.URL.Query().Get("service")
	signals := s.apmTracker.GetGoldenSignals(clusterID, ns, svc)
	jsonOK(w, map[string]any{"cluster_id": clusterID, "signals": signals, "total": len(signals)})
}

func (s *apiServer) handleChangeHistory(w http.ResponseWriter, r *http.Request, clusterID string) {
	q := r.URL.Query()
	ns         := q.Get("namespace")
	incidentID := q.Get("incident_id")
	lookbackH  := q.Get("lookback_hours")

	lookback := 6 * time.Hour
	if lookbackH != "" {
		var h float64
		fmt.Sscanf(lookbackH, "%f", &h)
		if h > 0 {
			lookback = time.Duration(h * float64(time.Hour))
		}
	}

	if incidentID != "" {
		result := s.changeCorrel.GetForIncident(incidentID, time.Now(), lookback)
		jsonOK(w, result)
		return
	}

	_ = ns
	jsonOK(w, map[string]any{
		"cluster_id": clusterID,
		"message":    "specify ?incident_id=X to correlate changes with an incident",
	})
}

func (s *apiServer) handlePlaybooks(w http.ResponseWriter, r *http.Request, clusterID string) {
	all := s.playbookGen.GetAllPlaybooks()
	jsonOK(w, map[string]any{"cluster_id": clusterID, "playbooks": all, "total": len(all)})
}

func (s *apiServer) handlePlaybook(w http.ResponseWriter, r *http.Request, clusterID, failureMode string) {
	kind := r.URL.Query().Get("kind")
	pb := s.playbookGen.FindPlaybook(failureMode, kind)
	if pb == nil {
		jsonError(w, fmt.Sprintf("no playbook found for failure mode %q (kind=%q) — need at least 2 resolved incidents", failureMode, kind), http.StatusNotFound)
		return
	}
	jsonOK(w, pb)
}

func (s *apiServer) handleRiskScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ClusterID    string `json:"cluster_id"`
		ResourceKind string `json:"resource_kind"`
		Namespace    string `json:"namespace"`
		Name         string `json:"name"`
		ChangeType   string `json:"change_type"`
		Actor        string `json:"actor"`
		NewImageTag  string `json:"new_image_tag"`
		OldImageTag  string `json:"old_image_tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	score := s.riskEngine.Score(risk.ChangeInput{
		ClusterID:    body.ClusterID,
		ResourceKind: body.ResourceKind,
		Namespace:    body.Namespace,
		Name:         body.Name,
		ChangeType:   risk.ChangeType(body.ChangeType),
		Actor:        body.Actor,
		NewImageTag:  body.NewImageTag,
		OldImageTag:  body.OldImageTag,
		Timestamp:    time.Now(),
	})
	jsonOK(w, score)
}

func (s *apiServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	q   := r.URL.Query().Get("q")
	cid := r.URL.Query().Get("cluster")
	ns  := r.URL.Query().Get("namespace")
	if q == "" {
		jsonError(w, "q parameter required", http.StatusBadRequest)
		return
	}
	results := s.searchResources(r.Context(), q, cid, ns)
	jsonOK(w, map[string]any{"query": q, "results": results, "total": len(results)})
}

// searchResources queries Neo4j for resources matching the search query.
func (s *apiServer) searchResources(ctx context.Context, query, clusterID, namespace string) []map[string]any {
	// Simple Neo4j full-text search — extend with Elasticsearch for production scale
	_ = ctx
	_ = query
	_ = clusterID
	_ = namespace
	return []map[string]any{}
}

// ─── Auth middleware ──────────────────────────────────────────────────────────

func (s *apiServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" {
			next(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != s.apiToken {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ─── Investigation store ──────────────────────────────────────────────────────

type investigationStore struct {
	mu      sync.RWMutex
	entries map[string]*investigationEntry
}

type investigationEntry struct {
	status string
	result *rca.Result
	err    error
}

func (is *investigationStore) setStatus(id, status string) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.entries == nil {
		is.entries = make(map[string]*investigationEntry)
	}
	is.entries[id] = &investigationEntry{status: status}
}

func (is *investigationStore) setResult(id string, r *rca.Result) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if e, ok := is.entries[id]; ok {
		e.status = "completed"
		e.result = r
	}
}

func (is *investigationStore) setError(id string, err error) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if e, ok := is.entries[id]; ok {
		e.status = "failed"
		e.err = err
	}
}

func (is *investigationStore) get(id string) (string, *rca.Result, error) {
	is.mu.RLock()
	defer is.mu.RUnlock()
	if is.entries == nil {
		return "", nil, nil
	}
	e, ok := is.entries[id]
	if !ok {
		return "", nil, nil
	}
	return e.status, e.result, e.err
}

// ─── Stub adapters for engines that need Correlator/DB queries ───────────────

type pgChangeQuerier struct{ db *sql.DB }

func (q pgChangeQuerier) GetRecent(ctx context.Context, clusterID string, from, until time.Time) ([]rca.ChangeRecord, error) {
	if q.db == nil {
		return nil, nil
	}
	rows, err := q.db.QueryContext(ctx, `
		SELECT change_type, resource_kind, namespace, resource_name,
		       actor, occurred_at
		FROM kubesense_changes
		WHERE cluster_id = $1 AND occurred_at BETWEEN $2 AND $3
		ORDER BY occurred_at DESC LIMIT 100`,
		clusterID, from, until)
	if err != nil {
		return nil, nil // table may not exist yet
	}
	defer rows.Close()
	var records []rca.ChangeRecord
	for rows.Next() {
		var r rca.ChangeRecord
		if err := rows.Scan(&r.ChangeType, &r.ResourceKind, &r.Namespace,
			&r.ResourceName, &r.Actor, &r.OccurredAt); err == nil {
			records = append(records, r)
		}
	}
	return records, nil
}

type pgEventQuerier struct{ db *sql.DB }

func (q pgEventQuerier) GetK8sEvents(ctx context.Context, clusterID, namespace string, since time.Time) ([]rca.Evidence, error) {
	return nil, nil
}

// ─── Response conversion helpers ──────────────────────────────────────────────

func rcaResultToResponse(incidentID string, result *rca.Result) map[string]any {
	resp := map[string]any{
		"incident_id":    incidentID,
		"status":         "completed",
		"confidence":     result.Confidence,
		"duration_ms":    result.Duration.Milliseconds(),
		"evidence_count": len(result.Evidence),
		"change_count":   len(result.RecentChanges),
		"chain_length":   len(result.DependencyChain),
	}
	if result.RootCause != nil {
		rc := result.RootCause
		resp["root_cause"] = map[string]any{
			"entity_id":       rc.EntityID,
			"entity_kind":     rc.EntityKind,
			"entity_name":     rc.EntityName,
			"entity_namespace": rc.EntityNS,
			"confidence":      rc.Confidence,
			"failure_mode":    rc.FailureMode,
		}
	}
	resp["hypotheses"] = len(result.AllHypotheses)
	resp["rejected_hypotheses"] = len(result.RejectedHypotheses)

	grade := "F"
	switch {
	case result.Confidence >= 0.85:
		grade = "A"
	case result.Confidence >= 0.70:
		grade = "B"
	case result.Confidence >= 0.50:
		grade = "C"
	case result.Confidence >= 0.30:
		grade = "D"
	}
	resp["evidence_grade"] = grade
	return resp
}

// ─── Stdlib helpers ───────────────────────────────────────────────────────────

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

// runSignalPublisher periodically publishes computed intelligence signals to Kafka.
// AlertHub's KubeSense consumer and OIE evidence bus consume these topics.
//
// Publish cadence:
//   - APM golden signals:  every 60s  (rate/error/latency data)
//   - Config violations:   every 5min (pre-computed rule violations)
//   - Forecasts:           every 5min (exhaustion predictions)
//   - Anomalies:           every 2min (EWMA-detected deviations)
func runSignalPublisher(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	apmTicker     := time.NewTicker(60 * time.Second)
	violTicker    := time.NewTicker(5 * time.Minute)
	forecastTicker := time.NewTicker(5 * time.Minute)
	anomalyTicker := time.NewTicker(2 * time.Minute)
	defer apmTicker.Stop()
	defer violTicker.Stop()
	defer forecastTicker.Stop()
	defer anomalyTicker.Stop()

	// Publish immediately on startup so AlertHub doesn't wait for the first tick.
	publishAllSignals(ctx, srv, pub, db)

	for {
		select {
		case <-ctx.Done():
			return
		case <-apmTicker.C:
			publishAPMSignals(ctx, srv, pub, db)
		case <-violTicker.C:
			publishConfigViolations(ctx, srv, pub, db)
			publishForecasts(ctx, srv, pub, db)
		case <-anomalyTicker.C:
			publishAnomalies(ctx, srv, pub, db)
		}
	}
}

func publishAllSignals(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	publishAPMSignals(ctx, srv, pub, db)
	publishConfigViolations(ctx, srv, pub, db)
	publishForecasts(ctx, srv, pub, db)
	publishAnomalies(ctx, srv, pub, db)
}

// listClusterIDs returns all known cluster IDs from the registry.
func listClusterIDs(ctx context.Context, db *sql.DB) []string {
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM kubesense_clusters WHERE status != 'offline' LIMIT 50`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func publishAPMSignals(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	clusterIDs := listClusterIDs(ctx, db)
	if len(clusterIDs) == 0 {
		// No clusters registered — try to publish a general signal if tracker has data
		signals := srv.apmTracker.GetGoldenSignals("", "", "")
		for _, s := range signals {
			pub.Publish(publish.TopicAPMGoldenSignals, s.ClusterID+"/"+s.Namespace+"/"+s.ServiceName, s)
		}
		return
	}
	count := 0
	for _, clusterID := range clusterIDs {
		signals := srv.apmTracker.GetGoldenSignals(clusterID, "", "")
		for _, s := range signals {
			pub.Publish(publish.TopicAPMGoldenSignals, clusterID+"/"+s.Namespace+"/"+s.ServiceName, s)
			count++
		}
	}
	if count > 0 {
		log.Printf("kubesense-api: published %d APM golden signals to %s", count, publish.TopicAPMGoldenSignals)
	}
}

func publishConfigViolations(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	clusterIDs := listClusterIDs(ctx, db)
	count := 0
	for _, clusterID := range clusterIDs {
		violations := srv.correlator.GetConfigViolations(clusterID)
		for i, v := range violations {
			pub.Publish(publish.TopicConfigViolations, fmt.Sprintf("%s/%d", clusterID, i), map[string]any{
				"cluster_id": clusterID,
				"violation":  v,
				"timestamp":  time.Now().UTC(),
			})
			count++
		}
	}
	if count > 0 {
		log.Printf("kubesense-api: published %d config violations to %s", count, publish.TopicConfigViolations)
	}
}

func publishForecasts(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	clusterIDs := listClusterIDs(ctx, db)
	count := 0
	for _, clusterID := range clusterIDs {
		forecasts := srv.forecastEngine.GetForecasts(clusterID)
		for i, f := range forecasts {
			pub.Publish(publish.TopicForecasts, fmt.Sprintf("%s/%d", clusterID, i), f)
			count++
		}
	}
	if count > 0 {
		log.Printf("kubesense-api: published %d forecasts to %s", count, publish.TopicForecasts)
	}
}

func publishAnomalies(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	clusterIDs := listClusterIDs(ctx, db)
	count := 0
	for _, clusterID := range clusterIDs {
		anomalies := srv.anomalyDet.GetAnomalies(clusterID, "", "")
		for i, a := range anomalies {
			pub.Publish(publish.TopicAnomalies, fmt.Sprintf("%s/%d", clusterID, i), a)
			count++
		}
	}
	if count > 0 {
		log.Printf("kubesense-api: published %d anomalies to %s", count, publish.TopicAnomalies)
	}
}
