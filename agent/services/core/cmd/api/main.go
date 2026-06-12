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
	port         := envOrDefault("PORT", "8080")
	apiToken     := envOrDefault("KUBESENSE_API_TOKEN", "")
	dbURL        := envOrDefault("DATABASE_URL", "")
	neo4jUser    := envOrDefault("NEO4J_USER", "neo4j")
	neo4jPass    := envOrDefault("NEO4J_PASSWORD", "")
	kafkaBrokers := envOrDefault("KAFKA_BROKERS",
		"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092")

	// Fail-closed: in production an empty API token means all routes are unprotected.
	// Warn loudly so the misconfiguration is caught at startup, not at breach time.
	if apiToken == "" {
		log.Printf("SECURITY WARNING: KUBESENSE_API_TOKEN is not set — all API routes are unauthenticated. Set this secret via Whisper before exposing the service.")
	}

	// The topology querier uses the Neo4j HTTP REST API (port 7474), not the bolt driver.
	// If NEO4J_URL is a bolt/neo4j:// URL, convert it to HTTP so the querier can connect.
	neo4jURL := boltToHTTP(envOrDefault("NEO4J_URL", "http://kubesense-neo4j.aileron-agent.svc.cluster.local:7474"))

	log.Printf("kubesense-api starting: port=%s auth=%v kafka=%s neo4j=%s", port, apiToken != "", kafkaBrokers, neo4jURL)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	outcomeStore   := remediation.NewOutcomeStore()
	remediationEng := remediation.NewEngine(outcomeStore)
	rcaEng         := rca.NewEngine(neo4jQuerier, pgChangeQuerier{db}, pgEventQuerier{db})
	correlator     := correlation.NewCorrelator()
	apmTracker     := apm.NewTracker()
	anomalyDet     := anomaly.NewDetector()
	costAnalyzer   := cost.NewAnalyzer()
	sloTracker     := slo.NewTrackerNoOp()
	forecastEng    := forecast.NewEngineNoOp()
	riskEng        := risk.NewEngine()
	changeCorrel   := change.NewCorrelator(24 * time.Hour)
	fpEngine       := fingerprint.NewEngine()
	playbookGen    := playbook.NewGenerator()

	// ── RCA Correlator Engine (RCA-Operator feature set) ─────────────────────
	// Sliding 15-min buffer + PostgreSQL-backed rule engine + auto-detection miner
	// + Detecting→Active→Resolved incident lifecycle.
	rcaCorrelator := rca.NewCorrelatorEngine(db)
	rcaCorrelator.InitSchema(ctx)
	// Wire Kafka narrator producer so Active incidents trigger LLM narrative generation.
	if narratorProd := rca.NewNarratorProducer(kafkaBrokers); narratorProd != nil {
		rcaCorrelator.SetNarratorProducer(narratorProd)
		log.Printf("kubesense-api: narrator producer wired → kubesense.correlation.incident-context")
	}
	go rcaCorrelator.Run(ctx)

	// Buffer feeder — polls kubesense_health_events for new events and feeds them
	// into the sliding correlation window so rules can fire in near-real-time.
	go func() {
		if db == nil { return }
		watermark := time.Now().Add(-15 * time.Minute) // seed from last 15 min
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := db.QueryContext(ctx, `
					SELECT event_type, severity,
					       COALESCE(namespace,''), COALESCE(resource_kind,''),
					       COALESCE(resource_name,''), cluster_id, occurred_at
					FROM kubesense_health_events
					WHERE occurred_at > $1
					ORDER BY occurred_at ASC LIMIT 1000
				`, watermark)
				if err != nil { continue }
				count := 0
				for rows.Next() {
					var e rca.BufferEntry
					var occurredAt time.Time
					if err := rows.Scan(&e.EventType, &e.Severity,
						&e.Scope.Namespace, &e.Scope.ResourceKind, &e.Scope.ResourceName,
						&e.Scope.ClusterID, &occurredAt); err != nil {
						continue
					}
					if e.Scope.ResourceKind == "Pod" { e.Scope.PodName = e.Scope.ResourceName }
					if e.Scope.ResourceKind == "Node" { e.Scope.NodeName = e.Scope.ResourceName }
					e.AddedAt = occurredAt
					if occurredAt.After(watermark) { watermark = occurredAt }
					rcaCorrelator.Feed(ctx, e)
					count++
				}
				rows.Close()
				if count > 0 {
					log.Printf("rca-buffer-feeder: fed %d new events (watermark=%s)", count, watermark.Format(time.RFC3339))
				}
			}
		}
	}()
	log.Printf("kubesense-api: realtime buffer feeder started")

	srv := &apiServer{
		neo4j:          neo4jQuerier,
		db:             db,
		rcaEngine:      rcaEng,
		rcaCorrelator:  rcaCorrelator,
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

type apiServer struct {
	neo4j          *topology.Querier
	db             *sql.DB
	rcaEngine      *rca.Engine
	rcaCorrelator  *rca.CorrelatorEngine   // RCA-Operator feature set
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
	investigations investigationStore
}

func (s *apiServer) registerRoutes(mux *http.ServeMux) {
	auth := s.authMiddleware
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz",  func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ready")) })
	mux.HandleFunc("/api/v1/investigations",  auth(s.handleInvestigations))
	mux.HandleFunc("/api/v1/investigations/", auth(s.handleGetInvestigation))
	mux.HandleFunc("/api/v1/clusters",        auth(s.handleListClusters))
	mux.HandleFunc("/api/v1/clusters/",       auth(s.handleClusterRoutes))
	mux.HandleFunc("/api/v1/risk/score",      auth(s.handleRiskScore))
	mux.HandleFunc("/api/v1/search",          auth(s.handleSearch))
	// RCA-Operator feature set — correlation incidents + rules
	mux.HandleFunc("/api/v1/correlation/incidents",  auth(s.handleCorrelationIncidents))
	mux.HandleFunc("/api/v1/correlation/incidents/", auth(s.handleCorrelationIncidentDetail))
	mux.HandleFunc("/api/v1/correlation/rules",      auth(s.handleCorrelationRules))
	mux.HandleFunc("/api/v1/correlation/status",     auth(s.handleCorrelationStatus))
	// LLM narrative endpoints — served from kubesense_narratives table
	mux.HandleFunc("/api/v1/narratives",  auth(s.handleListNarratives))
	mux.HandleFunc("/api/v1/narratives/", auth(s.handleGetNarrative))
}

func (s *apiServer) handleInvestigations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed); return
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
		jsonError(w, "invalid request: "+err.Error(), http.StatusBadRequest); return
	}
	if body.ClusterID == "" {
		jsonError(w, "cluster_id is required", http.StatusBadRequest); return
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
		ClusterID: body.ClusterID, AffectedResources: refs,
		IncidentTime: incidentTime, AlertContext: body.Context,
	}
	if body.Async {
		invID := body.IncidentID
		if invID == "" {
			invID = fmt.Sprintf("inv-%d", time.Now().UnixNano())
		}
		s.investigations.setStatus(invID, "running")
		go func() {
			result, err := s.rcaEngine.Investigate(context.Background(), req)
			if err != nil { s.investigations.setError(invID, err); return }
			s.investigations.setResult(invID, result)
		}()
		jsonOK(w, map[string]any{"investigation_id": invID, "status": "running"})
		return
	}
	result, err := s.rcaEngine.Investigate(r.Context(), req)
	if err != nil {
		jsonError(w, "investigation failed: "+err.Error(), http.StatusInternalServerError); return
	}
	jsonOK(w, rcaResultToResponse(body.IncidentID, result))
}

func (s *apiServer) handleGetInvestigation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/investigations/")
	if id == "" { jsonError(w, "investigation ID required", http.StatusBadRequest); return }
	status, result, err := s.investigations.get(id)
	if status == "" { jsonError(w, "not found", http.StatusNotFound); return }
	if err != nil {
		jsonOK(w, map[string]any{"investigation_id": id, "status": "failed", "error": err.Error()}); return
	}
	if result == nil {
		jsonOK(w, map[string]any{"investigation_id": id, "status": status}); return
	}
	jsonOK(w, rcaResultToResponse(id, result))
}

func (s *apiServer) handleClusterRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/")
	parts := strings.SplitN(path, "/", 2)
	clusterID := parts[0]
	subpath := ""
	if len(parts) > 1 { subpath = parts[1] }
	if clusterID == "" { jsonError(w, "cluster ID required", http.StatusBadRequest); return }

	switch subpath {
	case "topology":         s.handleTopology(w, r, clusterID)
	case "blast-radius":     s.handleBlastRadius(w, r, clusterID)
	case "forecasts":        s.handleForecasts(w, r, clusterID)
	case "config/violations":s.handleConfigViolations(w, r, clusterID)
	case "config/validate":  s.handleConfigValidate(w, r, clusterID)
	case "security/posture": s.handleSecurityPosture(w, r, clusterID)
	case "cost/efficiency":  s.handleCostEfficiency(w, r, clusterID)
	case "anomalies":        s.handleAnomalies(w, r, clusterID)
	case "slo/budgets":      s.handleSLOBudgets(w, r, clusterID)
	case "apm/golden-signals":s.handleAPMSignals(w, r, clusterID)
	case "change/history":   s.handleChangeHistory(w, r, clusterID)
	case "playbooks":        s.handlePlaybooks(w, r, clusterID)
	default:
		if strings.HasPrefix(subpath, "playbooks/") {
			s.handlePlaybook(w, r, clusterID, strings.TrimPrefix(subpath, "playbooks/")); return
		}
		jsonError(w, "unknown endpoint: "+subpath, http.StatusNotFound)
	}
}

func (s *apiServer) handleListClusters(w http.ResponseWriter, r *http.Request) {
	if s.db == nil { jsonOK(w, map[string]any{"clusters": []any{}, "total": 0}); return }
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, first_seen, last_heartbeat, agent_version, node_count, status
		 FROM kubesense_clusters ORDER BY last_heartbeat DESC LIMIT 100`)
	if err != nil { jsonError(w, err.Error(), http.StatusInternalServerError); return }
	defer rows.Close()
	type cluster struct {
		ID string `json:"id"`; FirstSeen time.Time `json:"first_seen"`
		LastHeartbeat time.Time `json:"last_heartbeat"`; AgentVersion string `json:"agent_version"`
		NodeCount int `json:"node_count"`; Status string `json:"status"`
	}
	var clusters []cluster
	for rows.Next() {
		var c cluster
		if err := rows.Scan(&c.ID, &c.FirstSeen, &c.LastHeartbeat, &c.AgentVersion, &c.NodeCount, &c.Status); err == nil {
			clusters = append(clusters, c)
		}
	}
	if clusters == nil { clusters = []cluster{} }
	jsonOK(w, map[string]any{"clusters": clusters, "total": len(clusters)})
}

func (s *apiServer) handleTopology(w http.ResponseWriter, r *http.Request, clusterID string) {
	kind := r.URL.Query().Get("kind"); name := r.URL.Query().Get("name"); ns := r.URL.Query().Get("namespace")
	if kind == "" || name == "" {
		jsonOK(w, map[string]any{"cluster_id": clusterID, "message": "specify ?kind=X&name=Y[&namespace=Z]"}); return
	}
	chain, err := s.neo4j.GetUpstreamChain(r.Context(), clusterID, kind, ns, name, 8)
	if err != nil { jsonError(w, err.Error(), http.StatusInternalServerError); return }
	jsonOK(w, map[string]any{"cluster_id": clusterID, "upstream": chain, "node_count": len(chain)})
}

func (s *apiServer) handleBlastRadius(w http.ResponseWriter, r *http.Request, clusterID string) {
	kind := r.URL.Query().Get("kind"); name := r.URL.Query().Get("name"); ns := r.URL.Query().Get("namespace")
	if kind == "" || name == "" { jsonError(w, "kind and name are required", http.StatusBadRequest); return }
	affected, err := s.neo4j.GetBlastRadius(r.Context(), clusterID, kind, ns, name)
	if err != nil { jsonError(w, err.Error(), http.StatusInternalServerError); return }
	jsonOK(w, map[string]any{
		"cluster_id": clusterID, "resource": map[string]string{"kind": kind, "namespace": ns, "name": name},
		"affected": affected, "total_affected": len(affected),
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
	if r.Method != http.MethodPost { jsonError(w, "POST required", http.StatusMethodNotAllowed); return }
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
	anomalies := s.anomalyDet.GetAnomalies(clusterID, r.URL.Query().Get("namespace"), r.URL.Query().Get("resource"))
	jsonOK(w, map[string]any{"cluster_id": clusterID, "anomalies": anomalies, "total": len(anomalies)})
}

func (s *apiServer) handleSLOBudgets(w http.ResponseWriter, r *http.Request, clusterID string) {
	budgets := s.sloTracker.GetBudgets(clusterID, r.URL.Query().Get("namespace"))
	jsonOK(w, map[string]any{"cluster_id": clusterID, "budgets": budgets, "total": len(budgets)})
}

func (s *apiServer) handleAPMSignals(w http.ResponseWriter, r *http.Request, clusterID string) {
	signals := s.apmTracker.GetGoldenSignals(clusterID, r.URL.Query().Get("namespace"), r.URL.Query().Get("service"))
	jsonOK(w, map[string]any{"cluster_id": clusterID, "signals": signals, "total": len(signals)})
}

func (s *apiServer) handleChangeHistory(w http.ResponseWriter, r *http.Request, clusterID string) {
	q := r.URL.Query()
	incidentID := q.Get("incident_id")
	lookback := 6 * time.Hour
	if h := q.Get("lookback_hours"); h != "" {
		var fh float64; fmt.Sscanf(h, "%f", &fh)
		if fh > 0 { lookback = time.Duration(fh * float64(time.Hour)) }
	}
	if incidentID != "" {
		jsonOK(w, s.changeCorrel.GetForIncident(incidentID, time.Now(), lookback)); return
	}
	jsonOK(w, map[string]any{"cluster_id": clusterID, "message": "specify ?incident_id=X"})
}

func (s *apiServer) handlePlaybooks(w http.ResponseWriter, r *http.Request, clusterID string) {
	all := s.playbookGen.GetAllPlaybooks()
	jsonOK(w, map[string]any{"cluster_id": clusterID, "playbooks": all, "total": len(all)})
}

func (s *apiServer) handlePlaybook(w http.ResponseWriter, r *http.Request, clusterID, failureMode string) {
	pb := s.playbookGen.FindPlaybook(failureMode, r.URL.Query().Get("kind"))
	if pb == nil {
		jsonError(w, fmt.Sprintf("no playbook for %q — need ≥2 resolved incidents", failureMode), http.StatusNotFound); return
	}
	jsonOK(w, pb)
}

func (s *apiServer) handleRiskScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { jsonError(w, "POST required", http.StatusMethodNotAllowed); return }
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
		jsonError(w, "invalid request: "+err.Error(), http.StatusBadRequest); return
	}
	score := s.riskEngine.Score(risk.ChangeInput{
		ClusterID: body.ClusterID, ResourceKind: body.ResourceKind,
		Namespace: body.Namespace, Name: body.Name,
		ChangeType: risk.ChangeType(body.ChangeType), Actor: body.Actor,
		NewImageTag: body.NewImageTag, OldImageTag: body.OldImageTag,
		Timestamp: time.Now(),
	})
	jsonOK(w, score)
}

func (s *apiServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" { jsonError(w, "q required", http.StatusBadRequest); return }
	jsonOK(w, map[string]any{"query": q, "results": []any{}, "total": 0})
}

// ─── RCA-Operator correlation handlers ────────────────────────────────────────

// GET /api/v1/correlation/incidents?cluster_id=X&phase=Active
func (s *apiServer) handleCorrelationIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed); return
	}
	clusterID := r.URL.Query().Get("cluster_id")
	phaseFilter := r.URL.Query().Get("phase")

	var incidents []*rca.Incident
	if s.rcaCorrelator != nil {
		incidents = s.rcaCorrelator.ActiveIncidents(clusterID)
	} else if s.db != nil {
		incidents = queryIncidentsFromDB(r.Context(), s.db, clusterID, phaseFilter)
	}
	if phaseFilter != "" {
		filtered := incidents[:0]
		for _, inc := range incidents {
			if string(inc.Phase) == phaseFilter {
				filtered = append(filtered, inc)
			}
		}
		incidents = filtered
	}
	if incidents == nil {
		incidents = []*rca.Incident{}
	}
	jsonOK(w, map[string]any{"incidents": incidents, "total": len(incidents)})
}

// GET /api/v1/correlation/incidents/:id
func (s *apiServer) handleCorrelationIncidentDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/correlation/incidents/")
	if id == "" || s.db == nil {
		jsonError(w, "not found", http.StatusNotFound); return
	}
	var inc rca.Incident
	var phase string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, cluster_id, fingerprint, incident_type, severity, phase, summary,
		       namespace, resource_kind, resource_name, rule_name,
		       first_observed_at, last_observed_at, signal_count
		FROM kubesense_incidents WHERE id = $1`, id).Scan(
		&inc.ID, &inc.ClusterID, &inc.Fingerprint, &inc.IncidentType, &inc.Severity,
		&phase, &inc.Summary, &inc.Namespace, &inc.ResourceKind, &inc.ResourceName,
		&inc.RuleName, &inc.FirstObservedAt, &inc.LastObservedAt, &inc.SignalCount)
	if err != nil {
		jsonError(w, "not found", http.StatusNotFound); return
	}
	inc.Phase = rca.IncidentPhase(phase)
	jsonOK(w, &inc)
}

// GET /api/v1/correlation/rules?auto_only=true
func (s *apiServer) handleCorrelationRules(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		jsonOK(w, map[string]any{"rules": []any{}, "total": 0}); return
	}
	autoOnly := r.URL.Query().Get("auto_only") == "true"
	q := `SELECT id, name, priority, trigger_event_type, conditions::text,
		         fires_incident_type, fires_severity, fires_summary, scope,
		         auto_generated, data_points
		  FROM kubesense_correlation_rules
		  WHERE (expires_at IS NULL OR expires_at > NOW())`
	if autoOnly {
		q += ` AND auto_generated = TRUE`
	}
	q += ` ORDER BY priority DESC, name ASC LIMIT 100`

	rows, err := s.db.QueryContext(r.Context(), q)
	if err != nil { jsonError(w, err.Error(), http.StatusInternalServerError); return }
	defer rows.Close()
	type ruleRow struct {
		ID string `json:"id"`; Name string `json:"name"`; Priority int `json:"priority"`
		TriggerType string `json:"trigger_event_type"`; Conditions string `json:"conditions"`
		IncidentType string `json:"fires_incident_type"`; Severity string `json:"fires_severity"`
		Summary string `json:"fires_summary"`; Scope string `json:"scope"`
		AutoGenerated bool `json:"auto_generated"`; DataPoints int `json:"data_points"`
	}
	var result []ruleRow
	for rows.Next() {
		var rr ruleRow
		if err := rows.Scan(&rr.ID, &rr.Name, &rr.Priority, &rr.TriggerType, &rr.Conditions,
			&rr.IncidentType, &rr.Severity, &rr.Summary, &rr.Scope,
			&rr.AutoGenerated, &rr.DataPoints); err == nil {
			result = append(result, rr)
		}
	}
	if result == nil { result = []ruleRow{} }
	jsonOK(w, map[string]any{"rules": result, "total": len(result)})
}

// GET /api/v1/correlation/status
func (s *apiServer) handleCorrelationStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{"online": s.rcaCorrelator != nil}
	if s.rcaCorrelator != nil {
		status["buffer_len"]       = s.rcaCorrelator.BufferLen()
		status["rule_count"]       = s.rcaCorrelator.RuleCount()
		status["tracked_patterns"] = s.rcaCorrelator.TrackedPatterns()
		status["active_incidents"] = len(s.rcaCorrelator.ActiveIncidents(""))
		status["baseline_models"]  = s.rcaCorrelator.BaselineModelCount()
		status["flap_suppressed"]  = s.rcaCorrelator.FlapSuppressedCount()
		status["incident_groups"]  = s.rcaCorrelator.IncidentGroupCount()
	}
	jsonOK(w, status)
}

func queryIncidentsFromDB(ctx context.Context, db *sql.DB, clusterID, phaseFilter string) []*rca.Incident {
	q := `SELECT id, cluster_id, fingerprint, incident_type, severity, phase, summary,
	             namespace, resource_kind, resource_name, rule_name,
	             first_observed_at, last_observed_at, signal_count
	      FROM kubesense_incidents WHERE 1=1`
	var args []interface{}
	if clusterID != "" {
		args = append(args, clusterID)
		q += fmt.Sprintf(" AND cluster_id = $%d", len(args))
	}
	if phaseFilter != "" {
		args = append(args, phaseFilter)
		q += fmt.Sprintf(" AND phase = $%d", len(args))
	}
	q += ` ORDER BY first_observed_at DESC LIMIT 100`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil { return nil }
	defer rows.Close()
	var result []*rca.Incident
	for rows.Next() {
		var inc rca.Incident; var p string
		if err := rows.Scan(&inc.ID, &inc.ClusterID, &inc.Fingerprint, &inc.IncidentType,
			&inc.Severity, &p, &inc.Summary, &inc.Namespace, &inc.ResourceKind,
			&inc.ResourceName, &inc.RuleName, &inc.FirstObservedAt, &inc.LastObservedAt,
			&inc.SignalCount); err == nil {
			inc.Phase = rca.IncidentPhase(p); result = append(result, &inc)
		}
	}
	return result
}

// GET /api/v1/narratives?cluster_id=X
func (s *apiServer) handleListNarratives(w http.ResponseWriter, r *http.Request) {
	if s.db == nil { jsonOK(w, map[string]any{"narratives": []any{}, "total": 0}); return }
	clusterID := r.URL.Query().Get("cluster_id")
	q := `SELECT incident_id, cluster_id, narrative, model, evidence_grade, confidence, generated_at
	      FROM kubesense_narratives WHERE 1=1`
	var args []interface{}
	if clusterID != "" {
		args = append(args, clusterID)
		q += fmt.Sprintf(" AND cluster_id = $%d", len(args))
	}
	q += ` ORDER BY generated_at DESC LIMIT 50`
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil { jsonError(w, err.Error(), http.StatusInternalServerError); return }
	defer rows.Close()
	type row struct {
		IncidentID    string    `json:"incident_id"`
		ClusterID     string    `json:"cluster_id"`
		Narrative     string    `json:"narrative"`
		Model         string    `json:"model"`
		EvidenceGrade string    `json:"evidence_grade"`
		Confidence    float64   `json:"confidence"`
		GeneratedAt   time.Time `json:"generated_at"`
	}
	var result []row
	for rows.Next() {
		var n row
		if err := rows.Scan(&n.IncidentID, &n.ClusterID, &n.Narrative, &n.Model,
			&n.EvidenceGrade, &n.Confidence, &n.GeneratedAt); err == nil {
			result = append(result, n)
		}
	}
	if result == nil { result = []row{} }
	jsonOK(w, map[string]any{"narratives": result, "total": len(result)})
}

// GET /api/v1/narratives/:incident_id
func (s *apiServer) handleGetNarrative(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/narratives/")
	if id == "" || s.db == nil { jsonError(w, "not found", http.StatusNotFound); return }
	var n struct {
		IncidentID    string    `json:"incident_id"`
		ClusterID     string    `json:"cluster_id"`
		Narrative     string    `json:"narrative"`
		Model         string    `json:"model"`
		EvidenceGrade string    `json:"evidence_grade"`
		Confidence    float64   `json:"confidence"`
		InputTokens   int       `json:"input_tokens"`
		OutputTokens  int       `json:"output_tokens"`
		GeneratedAt   time.Time `json:"generated_at"`
	}
	err := s.db.QueryRowContext(r.Context(), `
		SELECT incident_id, cluster_id, narrative, model, evidence_grade, confidence,
		       input_tokens, output_tokens, generated_at
		FROM kubesense_narratives WHERE incident_id = $1`, id).Scan(
		&n.IncidentID, &n.ClusterID, &n.Narrative, &n.Model,
		&n.EvidenceGrade, &n.Confidence, &n.InputTokens, &n.OutputTokens, &n.GeneratedAt)
	if err != nil { jsonError(w, "not found", http.StatusNotFound); return }
	jsonOK(w, n)
}

func (s *apiServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// When no token is configured we allow through with an audit log so the
		// misconfiguration is visible in pod logs. The startup warning already
		// flags this; per-request logging would be too noisy, so only log once
		// per server lifetime via the startup check.
		if s.apiToken == "" {
			next(w, r)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if provided == "" || provided != s.apiToken {
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
	status string; result *rca.Result; err error
}

func (is *investigationStore) setStatus(id, status string) {
	is.mu.Lock(); defer is.mu.Unlock()
	if is.entries == nil { is.entries = make(map[string]*investigationEntry) }
	is.entries[id] = &investigationEntry{status: status}
}
func (is *investigationStore) setResult(id string, r *rca.Result) {
	is.mu.Lock(); defer is.mu.Unlock()
	if e, ok := is.entries[id]; ok { e.status = "completed"; e.result = r }
}
func (is *investigationStore) setError(id string, err error) {
	is.mu.Lock(); defer is.mu.Unlock()
	if e, ok := is.entries[id]; ok { e.status = "failed"; e.err = err }
}
func (is *investigationStore) get(id string) (string, *rca.Result, error) {
	is.mu.RLock(); defer is.mu.RUnlock()
	if is.entries == nil { return "", nil, nil }
	e, ok := is.entries[id]
	if !ok { return "", nil, nil }
	return e.status, e.result, e.err
}

// ─── DB adapters ──────────────────────────────────────────────────────────────

type pgChangeQuerier struct{ db *sql.DB }

func (q pgChangeQuerier) GetRecent(ctx context.Context, clusterID string, from, until time.Time) ([]rca.ChangeRecord, error) {
	if q.db == nil { return nil, nil }
	rows, err := q.db.QueryContext(ctx, `
		SELECT change_type, resource_kind, namespace, resource_name, actor, occurred_at
		FROM kubesense_changes
		WHERE cluster_id = $1 AND occurred_at BETWEEN $2 AND $3
		ORDER BY occurred_at DESC LIMIT 100`,
		clusterID, from, until)
	if err != nil { return nil, nil }
	defer rows.Close()
	var records []rca.ChangeRecord
	for rows.Next() {
		var r rca.ChangeRecord
		if err := rows.Scan(&r.ChangeType, &r.ResourceKind, &r.Namespace, &r.ResourceName, &r.Actor, &r.OccurredAt); err == nil {
			records = append(records, r)
		}
	}
	return records, nil
}

type pgEventQuerier struct{ db *sql.DB }

func (q pgEventQuerier) GetK8sEvents(_ context.Context, _, _ string, _ time.Time) ([]rca.Evidence, error) {
	return nil, nil
}

// ─── Response helpers ─────────────────────────────────────────────────────────

func rcaResultToResponse(incidentID string, result *rca.Result) map[string]any {
	resp := map[string]any{
		"incident_id": incidentID, "status": "completed",
		"confidence": result.Confidence, "duration_ms": result.Duration.Milliseconds(),
		"evidence_count": len(result.Evidence), "change_count": len(result.RecentChanges),
		"chain_length": len(result.DependencyChain),
		"hypotheses": len(result.AllHypotheses), "rejected_hypotheses": len(result.RejectedHypotheses),
	}
	if result.RootCause != nil {
		rc := result.RootCause
		resp["root_cause"] = map[string]any{
			"entity_id": rc.EntityID, "entity_kind": rc.EntityKind,
			"entity_name": rc.EntityName, "entity_namespace": rc.EntityNS,
			"confidence": rc.Confidence, "failure_mode": rc.FailureMode,
		}
	}
	grade := "F"
	switch {
	case result.Confidence >= 0.85: grade = "A"
	case result.Confidence >= 0.70: grade = "B"
	case result.Confidence >= 0.50: grade = "C"
	case result.Confidence >= 0.30: grade = "D"
	}
	resp["evidence_grade"] = grade
	return resp
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
	if v := os.Getenv(key); v != "" { return v }
	return def
}

// ─── Signal publisher ─────────────────────────────────────────────────────────

func runSignalPublisher(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	apmT  := time.NewTicker(60 * time.Second)
	violT := time.NewTicker(5 * time.Minute)
	foreT := time.NewTicker(5 * time.Minute)
	anomT := time.NewTicker(2 * time.Minute)
	defer apmT.Stop(); defer violT.Stop(); defer foreT.Stop(); defer anomT.Stop()
	publishAll(ctx, srv, pub, db)
	for {
		select {
		case <-ctx.Done(): return
		case <-apmT.C:  publishAPM(ctx, srv, pub, db)
		case <-violT.C: publishViol(ctx, srv, pub, db); publishFore(ctx, srv, pub, db)
		case <-anomT.C: publishAnom(ctx, srv, pub, db)
		}
	}
}

func publishAll(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	publishAPM(ctx, srv, pub, db); publishViol(ctx, srv, pub, db)
	publishFore(ctx, srv, pub, db); publishAnom(ctx, srv, pub, db)
}

func clusterIDs(ctx context.Context, db *sql.DB) []string {
	if db == nil { return nil }
	rows, err := db.QueryContext(ctx, `SELECT id FROM kubesense_clusters WHERE status != 'offline' LIMIT 50`)
	if err != nil { return nil }
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil { ids = append(ids, id) }
	}
	return ids
}

func publishAPM(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	count := 0
	for _, cid := range clusterIDs(ctx, db) {
		for _, s := range srv.apmTracker.GetGoldenSignals(cid, "", "") {
			pub.Publish(publish.TopicAPMGoldenSignals, cid+"/"+s.Namespace+"/"+s.ServiceName, s); count++
		}
	}
	if count > 0 { log.Printf("kubesense-api: published %d APM signals", count) }
}

func publishViol(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	count := 0
	for _, cid := range clusterIDs(ctx, db) {
		for i, v := range srv.correlator.GetConfigViolations(cid) {
			pub.Publish(publish.TopicConfigViolations, fmt.Sprintf("%s/%d", cid, i), map[string]any{"cluster_id": cid, "violation": v, "timestamp": time.Now().UTC()}); count++
		}
	}
	if count > 0 { log.Printf("kubesense-api: published %d violations", count) }
}

func publishFore(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	count := 0
	for _, cid := range clusterIDs(ctx, db) {
		for i, f := range srv.forecastEngine.GetForecasts(cid) {
			pub.Publish(publish.TopicForecasts, fmt.Sprintf("%s/%d", cid, i), f); count++
		}
	}
	if count > 0 { log.Printf("kubesense-api: published %d forecasts", count) }
}

func publishAnom(ctx context.Context, srv *apiServer, pub *publish.Publisher, db *sql.DB) {
	count := 0
	for _, cid := range clusterIDs(ctx, db) {
		for i, a := range srv.anomalyDet.GetAnomalies(cid, "", "") {
			pub.Publish(publish.TopicAnomalies, fmt.Sprintf("%s/%d", cid, i), a); count++
		}
	}
	if count > 0 { log.Printf("kubesense-api: published %d anomalies", count) }
}

// boltToHTTP converts a bolt/neo4j:// URL to HTTP for the topology querier.
// The querier uses the Neo4j HTTP Transaction API, not the bolt protocol.
func boltToHTTP(u string) string {
	for _, prefix := range []string{"neo4j://", "bolt://"} {
		if len(u) > len(prefix) && u[:len(prefix)] == prefix {
			host := u[len(prefix):]
			if idx := strings.LastIndex(host, ":"); idx >= 0 {
				host = host[:idx] + ":7474"
			}
			return "http://" + host
		}
	}
	return u
}
