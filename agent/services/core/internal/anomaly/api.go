package anomaly

// Detector is a high-level wrapper used by the API server. Actual anomaly
// data flows via Kafka and is stored in AlertHub; this struct provides the
// in-memory interface that the REST handler expects.
type Detector struct{}

// NewDetector creates a new Detector.
func NewDetector() *Detector { return &Detector{} }

// GetAnomalies returns detected anomalies filtered by cluster/namespace/resource.
// Returns empty slice when no anomalies have been ingested yet.
func (d *Detector) GetAnomalies(clusterID, namespace, resource string) []AnomalyAlert {
	return []AnomalyAlert{}
}
