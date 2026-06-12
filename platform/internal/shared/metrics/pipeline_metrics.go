package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// AlertsIngested counts every alert that enters the pipeline, labelled by source and status.
	AlertsIngested = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "alerts_ingested_total",
		Help:      "Total alerts received by the pipeline, by source and initial status.",
	}, []string{"source", "status"})

	// CorrelationDecisions counts pipeline correlation outcomes by decision type.
	CorrelationDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "correlation_decisions_total",
		Help:      "Total correlation decisions made by the pipeline, by decision type.",
	}, []string{"decision"})

	// IncidentsCreated counts new incidents created by the pipeline.
	IncidentsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "incidents_created_total",
		Help:      "Total incidents created by the correlation pipeline.",
	})

	// IncidentsAutoResolved counts incidents closed by the stale sweep or HandleAlertResolved.
	IncidentsAutoResolved = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "incidents_auto_resolved_total",
		Help:      "Total incidents auto-resolved, by reason (all_alerts_resolved, stale_sweep).",
	}, []string{"reason"})

	// AlertsAutoResolved counts alerts closed by the stale sweep.
	AlertsAutoResolved = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "alerts_stale_resolved_total",
		Help:      "Total alerts auto-resolved by the stale-sweep (no Dynatrace RESOLVED received in 24h).",
	})

	// PipelineQueueDepth is the instantaneous number of alerts waiting in the fast-path channel.
	PipelineQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "queue_depth",
		Help:      "Current number of alerts queued in each pipeline stage channel.",
	}, []string{"stage"})

	// CorrelationDuration measures end-to-end correlation latency per decision type.
	CorrelationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "alerthub",
		Subsystem: "pipeline",
		Name:      "correlation_duration_seconds",
		Help:      "End-to-end correlation duration in seconds, by decision type.",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"decision"})

	// RCARequests counts RCA investigation triggers.
	RCARequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alerthub",
		Subsystem: "rca",
		Name:      "requests_total",
		Help:      "Total RCA investigation requests sent to the orchestrator, by status (ok, error, skipped).",
	}, []string{"status"})
)

func init() {
	prometheus.MustRegister(
		AlertsIngested,
		CorrelationDecisions,
		IncidentsCreated,
		IncidentsAutoResolved,
		AlertsAutoResolved,
		PipelineQueueDepth,
		CorrelationDuration,
		RCARequests,
	)
}
