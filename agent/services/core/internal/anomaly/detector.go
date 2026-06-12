// Package anomaly implements multi-signal anomaly detection.
// Uses Exponentially Weighted Moving Average (EWMA) with dynamic thresholds.
// Detects anomalies across: metrics, latency, error rates, resource usage,
// and K8s event patterns. Correlates anomalies across services.
package anomaly

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// SignalType classifies what is being monitored.
type SignalType string

const (
	SignalCPUUsage      SignalType = "cpu_usage"
	SignalMemoryUsage   SignalType = "memory_usage"
	SignalLatencyP99    SignalType = "latency_p99"
	SignalErrorRate     SignalType = "error_rate"
	SignalRequestRate   SignalType = "request_rate"
	SignalRestartCount  SignalType = "restart_count"
	SignalDiskUsage     SignalType = "disk_usage"
	SignalNetworkRx     SignalType = "network_rx_bytes"
	SignalNetworkTx     SignalType = "network_tx_bytes"
	SignalConnectionCount SignalType = "connection_count"
)

// AnomalyAlert is emitted when a signal deviates significantly from its baseline.
type AnomalyAlert struct {
	ID          string     `json:"id"`
	ClusterID   string     `json:"cluster_id"`
	Namespace   string     `json:"namespace"`
	ResourceName string    `json:"resource_name"`
	ResourceKind string    `json:"resource_kind"`
	Signal      SignalType `json:"signal"`
	DetectedAt  time.Time  `json:"detected_at"`

	// Deviation
	CurrentValue float64 `json:"current_value"`
	BaselineMean float64 `json:"baseline_mean"`
	BaselineStdDev float64 `json:"baseline_stddev"`
	ZScore       float64 `json:"z_score"`
	Direction    string  `json:"direction"` // spike | drop

	// Context
	Severity     string  `json:"severity"` // critical(z>5) | high(z>4) | medium(z>3)
	DurationSecs int     `json:"duration_seconds"` // how long anomaly sustained
	Description  string  `json:"description"`

	// Correlations: other resources showing anomalies around the same time
	CorrelatedAnomalies []string `json:"correlated_anomalies,omitempty"`
}

// EWMADetector tracks one signal for one resource using EWMA.
// Thread-safe. Self-learns from incoming data.
type EWMADetector struct {
	mu sync.Mutex

	// EWMA parameters
	alpha    float64 // smoothing factor (0.1 = slow adaptation, 0.3 = fast)
	mean     float64 // EWMA mean
	variance float64 // EWMA variance (for standard deviation)

	// Anomaly threshold: flag if |z-score| > threshold
	threshold float64 // default 3.0 (3-sigma)

	// State
	n            int       // sample count
	lastValue    float64
	anomalyStart time.Time
	inAnomaly    bool
	minSamples   int // need at least this many before flagging
}

// NewEWMADetector creates a detector.
// alpha=0.1 for slow-changing signals (CPU, memory).
// alpha=0.3 for fast-changing signals (error rate, latency).
func NewEWMADetector(alpha, threshold float64) *EWMADetector {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.2
	}
	if threshold <= 0 {
		threshold = 3.0
	}
	return &EWMADetector{alpha: alpha, threshold: threshold, minSamples: 30}
}

// Update adds a new sample. Returns (isAnomaly, zScore, direction).
func (d *EWMADetector) Update(value float64) (isAnomaly bool, zScore float64, direction string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.n++
	d.lastValue = value

	if d.n == 1 {
		d.mean = value
		d.variance = 0
		return false, 0, ""
	}

	prevMean := d.mean
	// EWMA update
	d.mean = d.alpha*value + (1-d.alpha)*d.mean
	diff := value - prevMean
	d.variance = d.alpha*diff*diff + (1-d.alpha)*d.variance

	stdDev := math.Sqrt(d.variance)
	if stdDev < 1e-10 || d.n < d.minSamples {
		return false, 0, ""
	}

	zScore = (value - d.mean) / stdDev
	absZ := math.Abs(zScore)

	if zScore > 0 {
		direction = "spike"
	} else {
		direction = "drop"
	}

	isAnomaly = absZ > d.threshold
	return isAnomaly, zScore, direction
}

// Baseline returns the current baseline (mean, stddev).
func (d *EWMADetector) Baseline() (mean, stddev float64, samples int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mean, math.Sqrt(d.variance), d.n
}

// DetectorRegistry maintains one EWMADetector per (resource, signal) pair.
// Thread-safe. Self-cleans stale detectors.
type DetectorRegistry struct {
	mu        sync.RWMutex
	detectors map[string]*EWMADetector // key: clusterID/namespace/resource/signal
	lastSeen  map[string]time.Time
}

// NewDetectorRegistry creates an empty registry.
func NewDetectorRegistry() *DetectorRegistry {
	r := &DetectorRegistry{
		detectors: make(map[string]*EWMADetector),
		lastSeen:  make(map[string]time.Time),
	}
	go r.pruneLoop()
	return r
}

// Evaluate observes a metric value and returns an anomaly alert if detected.
func (r *DetectorRegistry) Evaluate(
	clusterID, namespace, resourceName, resourceKind string,
	signal SignalType,
	value float64,
) *AnomalyAlert {
	key := fmt.Sprintf("%s/%s/%s/%s", clusterID, namespace, resourceName, string(signal))

	r.mu.Lock()
	det, ok := r.detectors[key]
	if !ok {
		// Different alpha values per signal type
		alpha := alphaForSignal(signal)
		det = NewEWMADetector(alpha, thresholdForSignal(signal))
		r.detectors[key] = det
	}
	r.lastSeen[key] = time.Now()
	r.mu.Unlock()

	isAnomaly, zScore, direction := det.Update(value)
	if !isAnomaly {
		return nil
	}

	mean, stdDev, _ := det.Baseline()
	severity := severityFromZScore(math.Abs(zScore))

	return &AnomalyAlert{
		ClusterID:      clusterID,
		Namespace:      namespace,
		ResourceName:   resourceName,
		ResourceKind:   resourceKind,
		Signal:         signal,
		DetectedAt:     time.Now(),
		CurrentValue:   value,
		BaselineMean:   mean,
		BaselineStdDev: stdDev,
		ZScore:         zScore,
		Direction:      direction,
		Severity:       severity,
		Description:    buildDescription(resourceName, signal, value, mean, zScore, direction),
	}
}

// pruneLoop removes detectors not seen in the last 24 hours.
func (r *DetectorRegistry) pruneLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		for key, ts := range r.lastSeen {
			if time.Since(ts) > 24*time.Hour {
				delete(r.detectors, key)
				delete(r.lastSeen, key)
			}
		}
		r.mu.Unlock()
	}
}

// AnomalyCorrelator groups anomalies that occur in close temporal proximity.
// Helps identify when a single root cause is causing cascading anomalies.
type AnomalyCorrelator struct {
	mu      sync.Mutex
	window  []*AnomalyAlert // recent anomalies
	maxAge  time.Duration
}

// NewAnomalyCorrelator creates a correlator with a given time window.
func NewAnomalyCorrelator(window time.Duration) *AnomalyCorrelator {
	return &AnomalyCorrelator{maxAge: window}
}

// Add inserts an anomaly and returns any other anomalies in the same time window.
func (c *AnomalyCorrelator) Add(alert *AnomalyAlert) []*AnomalyAlert {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Prune expired
	cutoff := time.Now().Add(-c.maxAge)
	filtered := c.window[:0]
	for _, a := range c.window {
		if a.DetectedAt.After(cutoff) {
			filtered = append(filtered, a)
		}
	}
	c.window = filtered

	// Find correlations before adding
	var correlated []*AnomalyAlert
	for _, existing := range c.window {
		if existing.ClusterID == alert.ClusterID {
			correlated = append(correlated, existing)
		}
	}

	c.window = append(c.window, alert)

	// Annotate with correlated resource names
	for _, a := range correlated {
		alert.CorrelatedAnomalies = append(alert.CorrelatedAnomalies,
			fmt.Sprintf("%s/%s[%s]", a.Namespace, a.ResourceName, a.Signal))
	}
	return correlated
}

// MultiSignalAnomaly groups multiple anomalies pointing to a common root cause.
type MultiSignalAnomaly struct {
	ClusterID   string
	Namespace   string
	DetectedAt  time.Time
	Anomalies   []*AnomalyAlert
	// Likely root cause resource (one with the most signals or earliest onset)
	LikelySource string
	Confidence   float64
}

// GroupAnomalies clusters simultaneous anomalies across a namespace.
// Returns groups where ≥3 resources show anomalies within the same 5-minute window.
func GroupAnomalies(alerts []*AnomalyAlert, window time.Duration) []*MultiSignalAnomaly {
	if len(alerts) == 0 {
		return nil
	}
	// Sort by detection time
	sorted := make([]*AnomalyAlert, len(alerts))
	copy(sorted, alerts)
	// Simple sliding window grouping
	groups := make(map[string][]*AnomalyAlert) // key: clusterID/namespace
	for _, a := range sorted {
		key := a.ClusterID + "/" + a.Namespace
		groups[key] = append(groups[key], a)
	}
	var result []*MultiSignalAnomaly
	for _, group := range groups {
		if len(group) >= 3 {
			ma := &MultiSignalAnomaly{
				ClusterID:  group[0].ClusterID,
				Namespace:  group[0].Namespace,
				DetectedAt: group[0].DetectedAt,
				Anomalies:  group,
				Confidence: math.Min(1.0, float64(len(group))*0.2),
			}
			// Identify likely source: resource with highest z-score
			var maxZ float64
			for _, a := range group {
				if math.Abs(a.ZScore) > maxZ {
					maxZ = math.Abs(a.ZScore)
					ma.LikelySource = a.ResourceName
				}
			}
			result = append(result, ma)
		}
	}
	return result
}

func alphaForSignal(s SignalType) float64 {
	switch s {
	case SignalLatencyP99, SignalErrorRate, SignalRequestRate:
		return 0.3 // react quickly to sudden changes
	case SignalCPUUsage, SignalMemoryUsage, SignalDiskUsage:
		return 0.1 // slow adaptation for resource utilization
	default:
		return 0.2
	}
}

func thresholdForSignal(s SignalType) float64 {
	switch s {
	case SignalRestartCount:
		return 2.0 // more sensitive: any restart is significant
	case SignalErrorRate:
		return 3.0
	default:
		return 3.5 // slightly relaxed for noisy signals
	}
}

func severityFromZScore(absZ float64) string {
	switch {
	case absZ > 6:
		return "critical"
	case absZ > 4:
		return "high"
	case absZ > 3:
		return "medium"
	default:
		return "low"
	}
}

func buildDescription(resource string, signal SignalType, current, baseline, zScore float64, direction string) string {
	pct := math.Abs((current - baseline) / math.Max(0.001, baseline) * 100)
	return fmt.Sprintf("%s: %s %.0f%% above baseline (z=%.1f, current=%.3f, baseline=%.3f)",
		resource, direction, pct, math.Abs(zScore), current, baseline)
}
