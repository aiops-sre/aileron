package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	InvestigationsStarted = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "investigations_started_total",
			Help: "Total investigations triggered, by playbook and severity."},
		[]string{"playbook", "severity"},
	)

	InvestigationsCompleted = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "investigations_completed_total",
			Help: "Completed investigations, by playbook, confidence_band, and outcome."},
		[]string{"playbook", "confidence_band", "outcome"},
	)

	InvestigationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: "oie", Name: "investigation_duration_seconds",
			Help:    "End-to-end investigation duration in seconds.",
			Buckets: []float64{5, 10, 15, 20, 30, 45, 60, 90}},
		[]string{"playbook", "outcome"},
	)

	DuplicateInvestigationsPrevented = promauto.NewCounter(
		prometheus.CounterOpts{Namespace: "oie", Name: "duplicate_investigations_prevented_total",
			Help: "Investigations rejected due to idempotency key collision."},
	)

	OrphanedInvestigationsRecovered = promauto.NewCounter(
		prometheus.CounterOpts{Namespace: "oie", Name: "orphaned_investigations_recovered_total",
			Help: "Orphaned investigations successfully re-queued by the recovery job."},
	)

	OrphanedInvestigationsFailed = promauto.NewCounter(
		prometheus.CounterOpts{Namespace: "oie", Name: "orphaned_investigations_failed_total",
			Help: "Orphaned investigations marked as failed due to exceeding max age."},
	)

	SemaphoreUtilization = promauto.NewGauge(
		prometheus.GaugeOpts{Namespace: "oie", Name: "investigation_semaphore_utilization",
			Help: "Current number of investigation goroutines running."},
	)

	SemaphoreFullTotal = promauto.NewCounter(
		prometheus.CounterOpts{Namespace: "oie", Name: "investigation_semaphore_full_total",
			Help: "Times a new investigation could not be started due to semaphore capacity."},
	)

	KafkaMessagesProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "kafka_messages_processed_total",
			Help: "Kafka messages processed, by result (success, retry, dlq)."},
		[]string{"result"},
	)

	DLQMessagesTotal = promauto.NewCounter(
		prometheus.CounterOpts{Namespace: "oie", Name: "dlq_messages_total",
			Help: "Messages sent to the dead letter queue."},
	)

	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "http_requests_total",
			Help: "HTTP requests handled, by method, path, and status code."},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: "oie", Name: "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets},
		[]string{"method", "path"},
	)

	EvidenceFetchDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: "oie", Name: "evidence_fetch_duration_seconds",
			Help:    "Evidence fetcher duration in seconds, by fetcher and status.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 8, 15, 30}},
		[]string{"fetcher", "status"},
	)

	EvidenceFetchTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "evidence_fetch_total",
			Help: "Evidence fetcher calls, by fetcher and status."},
		[]string{"fetcher", "status"},
	)

	HypothesisConfidence = promauto.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: "oie", Name: "hypothesis_final_confidence",
			Help:    "Final hypothesis confidence score, by type and status.",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}},
		[]string{"type", "status"},
	)

	HypothesisRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "hypotheses_rejected_total",
			Help: "Hypotheses rejected, by type and reason."},
		[]string{"type", "reason"},
	)

	DBQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: "oie", Name: "db_query_duration_seconds",
			Help:    "Database query duration in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0}},
		[]string{"operation"},
	)

	LLMNarrativeCalls = promauto.NewCounterVec(
		prometheus.CounterOpts{Namespace: "oie", Name: "llm_narrative_calls_total",
			Help: "LLM narrative generation calls, by outcome."},
		[]string{"outcome"},
	)

	LLMNarrativeDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{Namespace: "oie", Name: "llm_narrative_duration_seconds",
			Help:    "LLM narrative generation duration in seconds.",
			Buckets: []float64{0.5, 1, 2, 5, 8, 12, 15, 20}},
	)
)
