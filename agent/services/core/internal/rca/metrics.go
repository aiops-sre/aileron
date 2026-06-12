// Package rca — Prometheus metrics matching RCA-Operator's metric set.
//
// All metrics use the "rca" namespace prefix and are registered on the
// default Prometheus registry, same pattern as the rest of KubeSense.
package rca

import "github.com/prometheus/client_golang/prometheus"

// RCAMetrics holds all Prometheus instruments for the correlation engine.
type RCAMetrics struct {
	// SignalsReceived counts events that entered the correlation pipeline.
	SignalsReceived *prometheus.CounterVec
	// SignalsDeduplicated counts signals suppressed by fingerprint dedup.
	SignalsDeduplicated *prometheus.CounterVec
	// IncidentsDetecting counts IncidentReports entering the Detecting phase.
	IncidentsDetecting *prometheus.CounterVec
	// IncidentsActivated counts Detecting → Active transitions.
	IncidentsActivated *prometheus.CounterVec
	// IncidentsResolved counts transitions to the Resolved phase.
	IncidentsResolved *prometheus.CounterVec
	// ActiveIncidents is a gauge of currently non-Resolved incidents.
	ActiveIncidents *prometheus.GaugeVec
	// TransitionSeconds observes wall-clock duration of phase transitions.
	TransitionSeconds *prometheus.HistogramVec
	// RulesLoaded is a gauge of active correlation rules.
	RulesLoaded *prometheus.GaugeVec
	// PatternsTracked is a gauge of patterns the miner is accumulating.
	PatternsTracked prometheus.Gauge
	// AutoRulesCreated counts auto-detection rules generated from patterns.
	AutoRulesCreated prometheus.Counter
	// AutoRulesExpired counts auto-generated rules that expired.
	AutoRulesExpired prometheus.Counter
	// AnalysisDuration observes pattern mining tick duration.
	AnalysisDuration prometheus.Histogram
}

// NewRCAMetrics creates and registers all RCA metrics.
// Returns nil if registration fails (e.g. when called in a test where metrics
// are already registered — the engine still works, just without metrics).
func NewRCAMetrics() *RCAMetrics {
	m := &RCAMetrics{
		SignalsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rca", Name: "signals_received_total",
			Help: "Total signals accepted by the KubeSense correlation pipeline.",
		}, []string{"event_type"}),

		SignalsDeduplicated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rca", Name: "signals_deduplicated_total",
			Help: "Signals suppressed by incident fingerprint deduplication.",
		}, []string{"event_type"}),

		IncidentsDetecting: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rca", Name: "incidents_detecting_total",
			Help: "Incidents that entered the Detecting phase.",
		}, []string{"incident_type", "severity"}),

		IncidentsActivated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rca", Name: "incidents_activated_total",
			Help: "Incidents promoted from Detecting to Active.",
		}, []string{"incident_type", "severity"}),

		IncidentsResolved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rca", Name: "incidents_resolved_total",
			Help: "Incidents that reached the Resolved phase.",
		}, []string{"incident_type", "severity"}),

		ActiveIncidents: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "rca", Name: "active_incidents",
			Help: "Currently non-Resolved incidents.",
		}, []string{"incident_type", "severity"}),

		TransitionSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "rca", Name: "incident_transition_seconds",
			Help:    "Duration of incident phase transitions.",
			Buckets: []float64{10, 30, 60, 120, 300, 600, 1800, 3600},
		}, []string{"from_phase", "to_phase"}),

		RulesLoaded: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "rca", Name: "rules_loaded",
			Help: "Active correlation rules currently loaded in memory.",
		}, []string{"type"}), // "builtin" | "auto"

		PatternsTracked: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "rca", Name: "patterns_tracked",
			Help: "Event co-occurrence patterns the miner is accumulating.",
		}),

		AutoRulesCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "rca", Name: "auto_rules_created_total",
			Help: "Auto-generated correlation rules created from pattern mining.",
		}),

		AutoRulesExpired: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "rca", Name: "auto_rules_expired_total",
			Help: "Auto-generated rules that expired due to quiet patterns.",
		}),

		AnalysisDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "rca", Name: "analysis_duration_seconds",
			Help:    "Time taken for one pattern mining analysis tick.",
			Buckets: prometheus.DefBuckets,
		}),
	}

	// Best-effort registration — never panic if already registered.
	collectors := []prometheus.Collector{
		m.SignalsReceived, m.SignalsDeduplicated,
		m.IncidentsDetecting, m.IncidentsActivated, m.IncidentsResolved,
		m.ActiveIncidents, m.TransitionSeconds, m.RulesLoaded,
		m.PatternsTracked, m.AutoRulesCreated, m.AutoRulesExpired, m.AnalysisDuration,
	}
	for _, c := range collectors {
		_ = prometheus.Register(c)
	}
	return m
}
