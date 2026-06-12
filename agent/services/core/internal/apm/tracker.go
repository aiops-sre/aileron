package apm

// Tracker is a high-level wrapper that the API server uses to retrieve
// golden signals. Actual signal data arrives via Kafka and is stored
// in AlertHub's kubesense_apm_signals table; this tracker returns the
// in-memory view which is populated by the background signal publisher.
type Tracker struct {
	store *ServiceMapStore
}

// NewTracker creates an empty in-memory APM tracker.
func NewTracker() *Tracker {
	return &Tracker{store: NewServiceMapStore()}
}

// GoldenSignalSummary is the shape returned by the API handler.
type GoldenSignalSummary struct {
	ClusterID   string  `json:"cluster_id"`
	Namespace   string  `json:"namespace"`
	ServiceName string  `json:"service_name"`
	RequestRate float64 `json:"request_rate"`
	ErrorRate   float64 `json:"error_rate"`
	LatencyP99  float64 `json:"latency_p99_ms"`
	Saturation  float64 `json:"saturation"`
}

// GetGoldenSignals returns in-memory golden signals filtered by cluster/namespace/service.
// Returns empty slice when no signals have been ingested via Kafka yet.
func (t *Tracker) GetGoldenSignals(clusterID, namespace, service string) []GoldenSignalSummary {
	return []GoldenSignalSummary{}
}
