package handlers

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus metric definitions 

var (
	// Counters
	AlertsIngestedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "alerthub_alerts_ingested_total",
		Help: "Total number of alerts ingested, partitioned by source.",
	}, []string{"source"})

	IncidentsCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "alerthub_incidents_created_total",
		Help: "Total number of incidents created, partitioned by method (auto|manual).",
	}, []string{"method"})

	CorrelationDecisionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "alerthub_correlation_decisions_total",
		Help: "Total correlation decisions made by strategy and decision outcome.",
	}, []string{"strategy", "decision"})

	// Gauges (refreshed by background goroutine)
	ActiveIncidentsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "alerthub_active_incidents",
		Help: "Current number of active (non-resolved) incidents.",
	})

	OpenAlertsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "alerthub_open_alerts",
		Help: "Current number of open alerts.",
	})

	TopologyNodesGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "alerthub_topology_nodes",
		Help: "Current number of topology nodes by type.",
	}, []string{"type"})

	TopologyAgeGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "alerthub_topology_age_seconds",
		Help: "Seconds since the topology graph was last rebuilt.",
	})

	// Histograms
	PipelineProcessingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "alerthub_pipeline_processing_duration_seconds",
		Help:    "Time from alert creation to incident correlation decision.",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 12), // 50ms ~200s
	})

	CorrelationScore = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "alerthub_correlation_score",
		Help:    "Correlation confidence scores by strategy.",
		Buckets: prometheus.LinearBuckets(0, 0.1, 11), // 0.0 1.0
	}, []string{"strategy"})
)

// PrometheusMetricsHandler wraps the standard promhttp handler with a DB-backed gauge refresher.
type PrometheusMetricsHandler struct {
	db      *sql.DB
	handler http.Handler
}

// NewPrometheusMetricsHandler creates the handler and starts a background gauge refresher.
func NewPrometheusMetricsHandler(db *sql.DB) *PrometheusMetricsHandler {
	h := &PrometheusMetricsHandler{
		db:      db,
		handler: promhttp.Handler(),
	}
	go h.gaugeRefreshLoop()
	return h
}

// ServeHTTP implements http.Handler — used as a gin handler via gin.WrapH.
func (h *PrometheusMetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

// GinHandler returns a gin.HandlerFunc that delegates to the Prometheus HTTP handler.
func (h *PrometheusMetricsHandler) GinHandler() func(c interface{ Writer() http.ResponseWriter; Request() *http.Request }) {
	return nil // not used; register via router.GET("/metrics", gin.WrapH(h))
}

// gaugeRefreshLoop updates all gauge metrics every 30 seconds by querying the DB.
func (h *PrometheusMetricsHandler) gaugeRefreshLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run immediately on start
	h.refreshGauges()

	for range ticker.C {
		h.refreshGauges()
	}
}

// refreshGauges queries the DB and updates Prometheus gauge metrics.
func (h *PrometheusMetricsHandler) refreshGauges() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Active incidents
	var activeIncidents float64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM incidents WHERE status NOT IN ('resolved','closed')`).
		Scan(&activeIncidents); err == nil {
		ActiveIncidentsGauge.Set(activeIncidents)
	}

	// Open alerts
	var openAlerts float64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE status NOT IN ('resolved','closed')`).
		Scan(&openAlerts); err == nil {
		OpenAlertsGauge.Set(openAlerts)
	}

	// Topology nodes by type
	rows, err := h.db.QueryContext(ctx,
		`SELECT entity_type, COUNT(*) FROM topology_entities GROUP BY entity_type`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var entityType string
			var cnt float64
			if rows.Scan(&entityType, &cnt) == nil {
				TopologyNodesGauge.WithLabelValues(entityType).Set(cnt)
			}
		}
	} else {
		log.Printf("prometheus: topology_entities query failed: %v", err)
	}

	// Topology age from pipeline_correlation_results max processed_at
	var lastProcessed time.Time
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(processed_at), NOW() - INTERVAL '999 hours') FROM pipeline_correlation_results`).
		Scan(&lastProcessed); err == nil {
		TopologyAgeGauge.Set(time.Since(lastProcessed).Seconds())
	}
}
