package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/push"
)

// prometheusCollector exposes a /metrics scrape endpoint and optionally
// pushes to a Prometheus Pushgateway (useful for batch/short-lived agents).
type prometheusCollector struct {
	cfg      Config
	registry *prometheus.Registry

	healthEvents    *prometheus.CounterVec
	incidents       *prometheus.CounterVec
	chaosScore      *prometheus.GaugeVec
	violations      *prometheus.CounterVec
	apmSignal       *prometheus.GaugeVec
	rcaDuration     *prometheus.HistogramVec
	bufferLen       *prometheus.GaugeVec

	server   *http.Server
	pusher   *push.Pusher
}

func newPrometheusCollector(cfg Config) (*prometheusCollector, error) {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	c := &prometheusCollector{
		cfg:      cfg,
		registry: reg,

		healthEvents: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "kubesense",
			Name:      "health_events_total",
			Help:      "Total K8s health events processed by the KubeSense agent.",
		}, []string{"cluster_id", "event_type", "severity"}),

		incidents: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "kubesense",
			Name:      "incidents_total",
			Help:      "Total KubeSense RCA incidents by phase transition.",
		}, []string{"cluster_id", "incident_type", "phase"}),

		chaosScore: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "kubesense",
			Name:      "chaos_readiness_score",
			Help:      "Latest chaos readiness score (0-100) per namespace. 100 = fully ready.",
		}, []string{"cluster_id", "namespace"}),

		violations: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "kubesense",
			Name:      "config_violations_total",
			Help:      "Total config policy violations detected.",
		}, []string{"cluster_id", "namespace", "rule_id", "severity"}),

		apmSignal: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "kubesense",
			Name:      "apm_signal",
			Help:      "APM golden signal value. signal label: latency_p99, error_rate, saturation, throughput.",
		}, []string{"cluster_id", "namespace", "service", "signal"}),

		rcaDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "kubesense",
			Name:      "rca_duration_seconds",
			Help:      "Time taken for an RCA investigation to complete.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"cluster_id"}),

		bufferLen: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "kubesense",
			Name:      "correlation_buffer_len",
			Help:      "Current number of events in the correlation sliding window buffer.",
		}, []string{"cluster_id"}),
	}

	// Start /metrics HTTP server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	c.server = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Non-fatal — metrics collection continues even if scrape endpoint fails
			_ = err
		}
	}()

	// Wire Pushgateway if configured
	if cfg.PushgatewayURL != "" {
		c.pusher = push.New(cfg.PushgatewayURL, cfg.PushgatewayJob).Gatherer(reg)
	}

	return c, nil
}

func (c *prometheusCollector) RecordHealthEvent(_ context.Context, clusterID, eventType, severity string) {
	c.healthEvents.WithLabelValues(clusterID, eventType, severity).Inc()
	c.maybePush()
}

func (c *prometheusCollector) RecordIncident(_ context.Context, clusterID, incidentType, phase string) {
	c.incidents.WithLabelValues(clusterID, incidentType, phase).Inc()
	c.maybePush()
}

func (c *prometheusCollector) RecordChaosScore(_ context.Context, clusterID, namespace string, score float64) {
	c.chaosScore.WithLabelValues(clusterID, namespace).Set(score)
	c.maybePush()
}

func (c *prometheusCollector) RecordConfigViolation(_ context.Context, clusterID, namespace, ruleID, severity string) {
	c.violations.WithLabelValues(clusterID, namespace, ruleID, severity).Inc()
	c.maybePush()
}

func (c *prometheusCollector) RecordAPMSignal(_ context.Context, clusterID, namespace, service, signal string, value float64) {
	c.apmSignal.WithLabelValues(clusterID, namespace, service, signal).Set(value)
	c.maybePush()
}

func (c *prometheusCollector) RecordRCADuration(_ context.Context, clusterID string, seconds float64) {
	c.rcaDuration.WithLabelValues(clusterID).Observe(seconds)
	c.maybePush()
}

func (c *prometheusCollector) RecordBufferLen(_ context.Context, clusterID string, length int64) {
	c.bufferLen.WithLabelValues(clusterID).Set(float64(length))
	c.maybePush()
}

// Flush pushes metrics to Pushgateway. No-op when Pushgateway is not configured.
func (c *prometheusCollector) Flush(_ context.Context) error {
	if c.pusher == nil {
		return nil
	}
	return c.pusher.Push()
}

func (c *prometheusCollector) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c.pusher != nil {
		_ = c.pusher.Push()
	}
	if c.server != nil {
		return c.server.Shutdown(ctx)
	}
	return nil
}

// maybePush sends to Pushgateway after every write when configured.
// In high-throughput scenarios, callers should batch and call Flush() instead.
func (c *prometheusCollector) maybePush() {
	if c.pusher == nil {
		return
	}
	// Best-effort, non-blocking push
	go func() { _ = c.pusher.Push() }()
}
