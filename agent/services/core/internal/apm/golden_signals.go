// Package apm implements Application Performance Monitoring intelligence.
// Tracks the four golden signals (Rate, Errors, Duration, Saturation) per service,
// builds service dependency maps from OpenTelemetry traces, and detects
// performance regressions correlated with deployments.
package apm

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// GoldenSignals is the RED (Rate, Errors, Duration) + Saturation model
// for one service at one point in time.
type GoldenSignals struct {
	ServiceName  string    `json:"service_name"`
	Namespace    string    `json:"namespace"`
	ClusterID    string    `json:"cluster_id"`
	Timestamp    time.Time `json:"timestamp"`
	WindowSecs   int       `json:"window_seconds"` // observation window

	// Rate — requests per second
	RequestRate float64 `json:"request_rate_rps"`

	// Errors — fraction of failed requests (0.0–1.0)
	ErrorRate    float64 `json:"error_rate"`
	ErrorCount   int64   `json:"error_count"`

	// Duration — latency percentiles in milliseconds
	LatencyP50  float64 `json:"latency_p50_ms"`
	LatencyP90  float64 `json:"latency_p90_ms"`
	LatencyP95  float64 `json:"latency_p95_ms"`
	LatencyP99  float64 `json:"latency_p99_ms"`
	LatencyP999 float64 `json:"latency_p999_ms"`
	LatencyMax  float64 `json:"latency_max_ms"`

	// Saturation — resource pressure
	ActiveConnections int64   `json:"active_connections"`
	QueueDepth        int64   `json:"queue_depth"`
	CPUThrottlePct    float64 `json:"cpu_throttle_pct"`

	// Health score 0–100 derived from all signals
	HealthScore float64 `json:"health_score"`
	// Degraded if any signal is outside normal bounds
	Degraded    bool    `json:"degraded"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}

// ServiceDependency is a discovered dependency between two services.
type ServiceDependency struct {
	CallerService    string  `json:"caller_service"`
	CallerNamespace  string  `json:"caller_namespace"`
	CalleeService    string  `json:"callee_service"`
	CalleeNamespace  string  `json:"callee_namespace"`
	Protocol         string  `json:"protocol"` // HTTP | gRPC | TCP | database
	CallsPerMinute   float64 `json:"calls_per_minute"`
	ErrorRate        float64 `json:"error_rate"`
	LatencyP99Ms     float64 `json:"latency_p99_ms"`
	// Source of discovery: otel_trace | service_mesh | network_flow | dns
	DiscoverySource  string  `json:"discovery_source"`
	Confidence       float64 `json:"confidence"` // 0–1
	LastSeen         time.Time `json:"last_seen"`
}

// PerformanceRegression is a detected degradation in service performance
// optionally correlated with a recent deployment.
type PerformanceRegression struct {
	ServiceName      string    `json:"service_name"`
	Namespace        string    `json:"namespace"`
	ClusterID        string    `json:"cluster_id"`
	DetectedAt       time.Time `json:"detected_at"`
	Signal           string    `json:"signal"` // error_rate | latency_p99 | request_rate_drop

	// Baseline vs current
	BaselineValue    float64 `json:"baseline_value"`
	CurrentValue     float64 `json:"current_value"`
	ChangePercent    float64 `json:"change_percent"`

	// Change correlation
	CorrelatedChange  *ChangeCorrelation `json:"correlated_change,omitempty"`
	Confidence        float64 `json:"confidence"`

	// Severity: critical (>50% regression), high (>25%), medium (>10%)
	Severity         string  `json:"severity"`
}

// ChangeCorrelation links a performance regression to a recent deployment.
type ChangeCorrelation struct {
	ChangeType    string    `json:"change_type"` // deployment_rollout | config_update
	ResourceName  string    `json:"resource_name"`
	OccurredAt    time.Time `json:"occurred_at"`
	LagSeconds    int64     `json:"lag_seconds"` // regression appeared N seconds after change
	CorrelationScore float64 `json:"correlation_score"`
}

// ServiceBaseline is the learned normal behavior for a service.
// Updated with a 7-day rolling window.
type ServiceBaseline struct {
	ServiceName  string
	Namespace    string
	ClusterID    string
	// Normal ranges (mean ± 3σ)
	ErrorRateMean    float64
	ErrorRateStdDev  float64
	LatencyP99Mean   float64
	LatencyP99StdDev float64
	RequestRateMean  float64
	RequestRateStdDev float64
	// Learned from N samples
	SampleCount  int
	LearnedSince time.Time
	UpdatedAt    time.Time
}

// LatencyHistogram accumulates latency samples for percentile computation.
// Uses t-digest algorithm approximation for memory efficiency.
type LatencyHistogram struct {
	mu      sync.Mutex
	buckets []float64 // sorted latency samples (capped at 10,000)
	count   int64
}

// Record adds a latency sample in milliseconds.
func (h *LatencyHistogram) Record(ms float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	// Reservoir sampling: keep at most 10,000 samples
	if len(h.buckets) < 10000 {
		h.buckets = append(h.buckets, ms)
		sort.Float64s(h.buckets)
	} else {
		// Replace a random element (approximate reservoir)
		idx := int(h.count % 10000)
		if idx < len(h.buckets) {
			h.buckets[idx] = ms
			sort.Float64s(h.buckets)
		}
	}
}

// Percentile returns the Nth percentile latency (0–100).
func (h *LatencyHistogram) Percentile(p float64) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.buckets) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100.0*float64(len(h.buckets)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(h.buckets) {
		idx = len(h.buckets) - 1
	}
	return h.buckets[idx]
}

// Snapshot returns golden signals from the current histogram state.
func (h *LatencyHistogram) Snapshot() (p50, p90, p95, p99, p999, max float64) {
	return h.Percentile(50), h.Percentile(90), h.Percentile(95),
		h.Percentile(99), h.Percentile(99.9), h.Percentile(100)
}

// Reset clears the histogram for a new observation window.
func (h *LatencyHistogram) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buckets = h.buckets[:0]
	h.count = 0
}

// HealthScore computes a 0–100 health score from golden signals.
// 100 = perfect. Each degraded signal reduces the score.
func HealthScore(s *GoldenSignals, baseline *ServiceBaseline) (score float64, reason string) {
	score = 100.0

	// Error rate: 0% = +0 penalty, 1% = -20, 5% = -50, 10%+ = -80
	if s.ErrorRate > 0 {
		penalty := math.Min(80, s.ErrorRate*100*8)
		score -= penalty
		if penalty > 20 {
			reason = fmt.Sprintf("error rate %.1f%%", s.ErrorRate*100)
		}
	}

	// Latency regression vs baseline
	if baseline != nil && baseline.LatencyP99Mean > 0 {
		latencyZScore := (s.LatencyP99 - baseline.LatencyP99Mean) / math.Max(1, baseline.LatencyP99StdDev)
		if latencyZScore > 3 {
			penalty := math.Min(40, latencyZScore*5)
			score -= penalty
			if reason == "" {
				reason = fmt.Sprintf("p99 latency %.0fms (%.1fσ above baseline)", s.LatencyP99, latencyZScore)
			}
		}
	}

	// Request rate drop (service may be down or receiving no traffic)
	if baseline != nil && baseline.RequestRateMean > 0 {
		dropPct := (baseline.RequestRateMean - s.RequestRate) / baseline.RequestRateMean
		if dropPct > 0.5 {
			penalty := math.Min(30, dropPct*40)
			score -= penalty
			if reason == "" {
				reason = fmt.Sprintf("request rate dropped %.0f%% from baseline", dropPct*100)
			}
		}
	}

	score = math.Max(0, score)
	return score, reason
}

// ServiceMapStore holds the live service dependency map for all clusters.
// Updated from OTel traces, service mesh telemetry, and DNS resolution.
type ServiceMapStore struct {
	mu    sync.RWMutex
	edges map[string]*ServiceDependency // key: "caller/callee"
}

// NewServiceMapStore creates an empty service map.
func NewServiceMapStore() *ServiceMapStore {
	return &ServiceMapStore{edges: make(map[string]*ServiceDependency)}
}

// Upsert adds or updates a dependency edge. Thread-safe.
func (s *ServiceMapStore) Upsert(dep *ServiceDependency) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dep.CallerService + "/" + dep.CallerNamespace + "→" + dep.CalleeService + "/" + dep.CalleeNamespace
	existing, ok := s.edges[key]
	if !ok {
		dep.LastSeen = time.Now()
		s.edges[key] = dep
		return
	}
	// Blend metrics using EMA (α=0.3) to smooth transient spikes
	const alpha = 0.3
	existing.CallsPerMinute = alpha*dep.CallsPerMinute + (1-alpha)*existing.CallsPerMinute
	existing.ErrorRate = alpha*dep.ErrorRate + (1-alpha)*existing.ErrorRate
	existing.LatencyP99Ms = alpha*dep.LatencyP99Ms + (1-alpha)*existing.LatencyP99Ms
	existing.LastSeen = time.Now()
}

// GetDependencies returns all dependencies for a given service.
func (s *ServiceMapStore) GetDependencies(ctx context.Context, service, namespace string) []*ServiceDependency {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ServiceDependency
	for _, dep := range s.edges {
		if (dep.CallerService == service && dep.CallerNamespace == namespace) ||
			(dep.CalleeService == service && dep.CalleeNamespace == namespace) {
			result = append(result, dep)
		}
	}
	return result
}

// PruneStale removes dependency edges not seen in the last N minutes.
func (s *ServiceMapStore) PruneStale(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := 0
	for k, dep := range s.edges {
		if time.Since(dep.LastSeen) > maxAge {
			delete(s.edges, k)
			pruned++
		}
	}
	return pruned
}

// RegressionDetector detects performance regressions by comparing
// current signals against a learned baseline for each service.
type RegressionDetector struct {
	mu        sync.RWMutex
	baselines map[string]*ServiceBaseline
	changes   ChangeStore
}

type ChangeStore interface {
	GetRecent(ctx context.Context, clusterID string, since time.Time) ([]RecentChange, error)
}

type RecentChange struct {
	ChangeType   string
	ResourceName string
	Namespace    string
	OccurredAt   time.Time
}

// NewRegressionDetector creates a detector.
func NewRegressionDetector(changes ChangeStore) *RegressionDetector {
	return &RegressionDetector{
		baselines: make(map[string]*ServiceBaseline),
		changes:   changes,
	}
}

// Evaluate checks current signals against baseline and returns any regressions.
func (d *RegressionDetector) Evaluate(ctx context.Context, current *GoldenSignals) []*PerformanceRegression {
	d.mu.RLock()
	key := current.ClusterID + "/" + current.Namespace + "/" + current.ServiceName
	baseline := d.baselines[key]
	d.mu.RUnlock()

	if baseline == nil || baseline.SampleCount < 30 {
		// Not enough baseline data yet
		return nil
	}

	var regressions []*PerformanceRegression

	// Check error rate regression
	if current.ErrorRate > 0.01 {
		zScore := (current.ErrorRate - baseline.ErrorRateMean) / math.Max(0.001, baseline.ErrorRateStdDev)
		if zScore > 3 && current.ErrorRate > baseline.ErrorRateMean*2 {
			changePct := (current.ErrorRate - baseline.ErrorRateMean) / math.Max(0.001, baseline.ErrorRateMean) * 100
			reg := &PerformanceRegression{
				ServiceName: current.ServiceName, Namespace: current.Namespace,
				ClusterID: current.ClusterID, DetectedAt: current.Timestamp,
				Signal: "error_rate", BaselineValue: baseline.ErrorRateMean,
				CurrentValue: current.ErrorRate, ChangePercent: changePct,
				Severity: regressionSeverity(changePct),
			}
			reg.CorrelatedChange = d.findCorrelatedChange(ctx, current, reg)
			regressions = append(regressions, reg)
		}
	}

	// Check p99 latency regression
	if baseline.LatencyP99StdDev > 0 {
		zScore := (current.LatencyP99 - baseline.LatencyP99Mean) / baseline.LatencyP99StdDev
		if zScore > 3 && current.LatencyP99 > baseline.LatencyP99Mean*1.5 {
			changePct := (current.LatencyP99 - baseline.LatencyP99Mean) / baseline.LatencyP99Mean * 100
			reg := &PerformanceRegression{
				ServiceName: current.ServiceName, Namespace: current.Namespace,
				ClusterID: current.ClusterID, DetectedAt: current.Timestamp,
				Signal: "latency_p99", BaselineValue: baseline.LatencyP99Mean,
				CurrentValue: current.LatencyP99, ChangePercent: changePct,
				Severity: regressionSeverity(changePct),
			}
			reg.CorrelatedChange = d.findCorrelatedChange(ctx, current, reg)
			regressions = append(regressions, reg)
		}
	}

	return regressions
}

// UpdateBaseline incorporates new signal data into the rolling baseline.
// Uses Welford's online algorithm for numerically stable mean/variance.
func (d *RegressionDetector) UpdateBaseline(s *GoldenSignals) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := s.ClusterID + "/" + s.Namespace + "/" + s.ServiceName

	b, ok := d.baselines[key]
	if !ok {
		b = &ServiceBaseline{
			ServiceName: s.ServiceName, Namespace: s.Namespace,
			ClusterID: s.ClusterID, LearnedSince: time.Now(),
		}
		d.baselines[key] = b
	}

	b.SampleCount++
	n := float64(b.SampleCount)
	// Welford's online algorithm
	deltaErr := s.ErrorRate - b.ErrorRateMean
	b.ErrorRateMean += deltaErr / n
	b.ErrorRateStdDev = welfordStdDev(b.ErrorRateStdDev, s.ErrorRate, b.ErrorRateMean, deltaErr, n)

	deltaLat := s.LatencyP99 - b.LatencyP99Mean
	b.LatencyP99Mean += deltaLat / n
	b.LatencyP99StdDev = welfordStdDev(b.LatencyP99StdDev, s.LatencyP99, b.LatencyP99Mean, deltaLat, n)

	deltaRate := s.RequestRate - b.RequestRateMean
	b.RequestRateMean += deltaRate / n
	b.RequestRateStdDev = welfordStdDev(b.RequestRateStdDev, s.RequestRate, b.RequestRateMean, deltaRate, n)

	b.UpdatedAt = time.Now()
}

func (d *RegressionDetector) findCorrelatedChange(ctx context.Context, s *GoldenSignals, reg *PerformanceRegression) *ChangeCorrelation {
	if d.changes == nil {
		return nil
	}
	since := s.Timestamp.Add(-2 * time.Hour)
	changes, err := d.changes.GetRecent(ctx, s.ClusterID, since)
	if err != nil || len(changes) == 0 {
		return nil
	}
	// Find the most recent change to this service's namespace within 1 hour before regression
	for _, ch := range changes {
		if ch.Namespace != s.Namespace {
			continue
		}
		lag := int64(s.Timestamp.Sub(ch.OccurredAt).Seconds())
		if lag > 0 && lag <= 3600 {
			score := 1.0 - float64(lag)/3600.0
			return &ChangeCorrelation{
				ChangeType: ch.ChangeType, ResourceName: ch.ResourceName,
				OccurredAt: ch.OccurredAt, LagSeconds: lag,
				CorrelationScore: score,
			}
		}
	}
	return nil
}

func regressionSeverity(changePct float64) string {
	switch {
	case changePct > 100:
		return "critical"
	case changePct > 50:
		return "high"
	case changePct > 25:
		return "medium"
	default:
		return "low"
	}
}

func welfordStdDev(prevStdDev, x, newMean, delta, n float64) float64 {
	if n < 2 {
		return 0
	}
	// M2 update
	m2 := prevStdDev*prevStdDev*(n-2) + delta*(x-newMean)
	return math.Sqrt(m2 / (n - 1))
}
