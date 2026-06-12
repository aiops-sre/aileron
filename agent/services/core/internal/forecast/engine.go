// Package forecast implements the KubeSense forecasting engine.
// Uses linear regression over time-series data to predict resource exhaustion.
package forecast

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Target identifies what is being forecasted.
type Target string

const (
	TargetPVCExhaustion   Target = "pvc_exhaustion"
	TargetNodeCPU         Target = "node_cpu_saturation"
	TargetNodeMemory      Target = "node_memory_saturation"
	TargetCertExpiry      Target = "cert_expiry"
	TargetPodEvictionRisk Target = "pod_eviction_risk"
)

// Result is the output of a forecast.
type Result struct {
	Target           Target
	ClusterID        string
	Namespace        string
	ResourceName     string
	ResourceKind     string
	CurrentValue     float64   // current utilization 0-1
	Threshold        float64   // alert threshold (e.g. 0.85)
	PredictedBreach  time.Time // when value crosses threshold
	ConfidenceLow    time.Time // pessimistic estimate
	ConfidenceHigh   time.Time // optimistic estimate
	TrendPerDay      float64   // % per day (positive = growing)
	DataPoints       int
	ModelConfidence  float64   // R² goodness of fit 0-1
	NoBreach         bool      // true if trend is flat or declining
}

// MetricSample is one data point for regression.
type MetricSample struct {
	Time     time.Time
	Used     float64 // bytes, cores, or 0-1 depending on metric
	Capacity float64 // max value; 0 if already normalized
}

// MetricsStore is the interface the forecast engine uses to fetch historical data.
type MetricsStore interface {
	GetPVCUsageSamples(ctx context.Context, clusterID, namespace, pvcName string, lookback time.Duration) ([]MetricSample, error)
	GetNodeMetricSamples(ctx context.Context, clusterID, nodeName, metric string, lookback time.Duration) ([]MetricSample, error)
}

// Engine runs forecasts against historical metric data.
type Engine struct {
	metrics MetricsStore
}

// NewEngine creates a forecast engine backed by the given metrics store.
func NewEngine(metrics MetricsStore) *Engine {
	return &Engine{metrics: metrics}
}

// NewEngineNoOp creates an in-memory-only forecast engine with no data source.
// Used by the API server when no external metrics store is configured;
// results arrive via Kafka and are served from AlertHub's DB tables instead.
func NewEngineNoOp() *Engine {
	return &Engine{metrics: noOpMetrics{}}
}

// GetForecasts returns all active forecasts for a cluster.
// With noOpMetrics this always returns empty; real forecasts come from Kafka.
func (e *Engine) GetForecasts(clusterID string) []Result {
	return []Result{}
}

// ForecastPVCExhaustion predicts when a PVC will reach 85% capacity.
// Returns nil if data is insufficient (< 24 samples) or trend is flat/declining.
func (e *Engine) ForecastPVCExhaustion(ctx context.Context, clusterID, namespace, pvcName string) (*Result, error) {
	samples, err := e.metrics.GetPVCUsageSamples(ctx, clusterID, namespace, pvcName, 30*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("fetch PVC samples: %w", err)
	}
	if len(samples) < 24 {
		return nil, fmt.Errorf("insufficient data: have %d samples, need ≥24", len(samples))
	}

	slope, _, r2 := linearRegression(samples)
	last := samples[len(samples)-1]

	// Normalize to capacity
	capacity := last.Capacity
	if capacity == 0 {
		return nil, fmt.Errorf("PVC capacity is zero")
	}
	currentPct := last.Used / capacity
	threshold := 0.85

	// Flat or declining trend — no exhaustion forecast needed
	if slope <= 0 {
		return &Result{
			Target: TargetPVCExhaustion, ClusterID: clusterID,
			Namespace: namespace, ResourceName: pvcName, ResourceKind: "PersistentVolumeClaim",
			CurrentValue: currentPct, Threshold: threshold,
			TrendPerDay: slope / capacity * 100, DataPoints: len(samples),
			ModelConfidence: r2, NoBreach: true,
		}, nil
	}

	// Days until threshold: solve for x where (slope*x + intercept) = threshold*capacity
	currentUsed := last.Used
	daysUntilBreach := (threshold*capacity - currentUsed) / slope
	if daysUntilBreach < 0 {
		daysUntilBreach = 0 // already breached
	}

	now := time.Now()
	breach := now.Add(time.Duration(daysUntilBreach*24) * time.Hour)

	// Uncertainty scales with inverse of R²
	uncertaintyDays := (1 - r2) * daysUntilBreach * 0.5
	return &Result{
		Target: TargetPVCExhaustion, ClusterID: clusterID,
		Namespace: namespace, ResourceName: pvcName, ResourceKind: "PersistentVolumeClaim",
		CurrentValue:    currentPct,
		Threshold:       threshold,
		PredictedBreach: breach,
		ConfidenceLow:   breach.Add(-time.Duration(uncertaintyDays*24) * time.Hour),
		ConfidenceHigh:  breach.Add(time.Duration(uncertaintyDays*24) * time.Hour),
		TrendPerDay:     slope / capacity * 100,
		DataPoints:      len(samples),
		ModelConfidence: r2,
	}, nil
}

// ForecastNodeCPU predicts when a node will sustain CPU utilization > 90%.
func (e *Engine) ForecastNodeCPU(ctx context.Context, clusterID, nodeName string) (*Result, error) {
	return e.forecastNodeMetric(ctx, clusterID, nodeName, "cpu_utilization", TargetNodeCPU, 0.90)
}

// ForecastNodeMemory predicts when a node will sustain memory utilization > 85%.
func (e *Engine) ForecastNodeMemory(ctx context.Context, clusterID, nodeName string) (*Result, error) {
	return e.forecastNodeMetric(ctx, clusterID, nodeName, "memory_utilization", TargetNodeMemory, 0.85)
}

func (e *Engine) forecastNodeMetric(ctx context.Context, clusterID, nodeName, metric string, target Target, threshold float64) (*Result, error) {
	samples, err := e.metrics.GetNodeMetricSamples(ctx, clusterID, nodeName, metric, 14*24*time.Hour)
	if err != nil || len(samples) < 12 {
		return nil, fmt.Errorf("insufficient node metric data")
	}
	slope, _, r2 := linearRegression(samples)
	last := samples[len(samples)-1]

	if slope <= 0 {
		return &Result{
			Target: target, ClusterID: clusterID, ResourceName: nodeName, ResourceKind: "Node",
			CurrentValue: last.Used, Threshold: threshold,
			TrendPerDay: slope * 100, DataPoints: len(samples), ModelConfidence: r2, NoBreach: true,
		}, nil
	}

	daysUntilBreach := (threshold - last.Used) / slope
	if daysUntilBreach < 0 { daysUntilBreach = 0 }
	now := time.Now()
	breach := now.Add(time.Duration(daysUntilBreach*24) * time.Hour)
	uncertainty := (1 - r2) * daysUntilBreach * 0.5

	return &Result{
		Target: target, ClusterID: clusterID, ResourceName: nodeName, ResourceKind: "Node",
		CurrentValue: last.Used, Threshold: threshold,
		PredictedBreach: breach,
		ConfidenceLow:   breach.Add(-time.Duration(uncertainty*24) * time.Hour),
		ConfidenceHigh:  breach.Add(time.Duration(uncertainty*24) * time.Hour),
		TrendPerDay: slope * 100, DataPoints: len(samples), ModelConfidence: r2,
	}, nil
}

// linearRegression fits y = slope*x + intercept to the samples.
// x is days since first sample, y is the metric value.
// Returns slope, intercept, and R² (goodness of fit).
func linearRegression(samples []MetricSample) (slope, intercept, r2 float64) {
	n := float64(len(samples))
	if n < 2 {
		return 0, 0, 0
	}
	t0 := samples[0].Time.Unix()
	var sumX, sumY, sumXY, sumX2, sumY2 float64

	for _, s := range samples {
		x := float64(s.Time.Unix()-t0) / 86400.0 // days
		y := s.Used
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}

	denom := n*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-10 {
		return 0, sumY / n, 0
	}

	slope = (n*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / n

	// R²
	yMean := sumY / n
	var ssTot, ssRes float64
	for _, s := range samples {
		x := float64(s.Time.Unix()-t0) / 86400.0
		pred := slope*x + intercept
		ssTot += math.Pow(s.Used-yMean, 2)
		ssRes += math.Pow(s.Used-pred, 2)
	}
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	// Clamp R² to [0, 1]
	if r2 < 0 { r2 = 0 }
	if r2 > 1 { r2 = 1 }
	return
}

// noOpMetrics is a no-op MetricsStore for the API server when no external
// metrics source is configured. Forecasts flow via Kafka to AlertHub's DB.
type noOpMetrics struct{}

func (noOpMetrics) GetPVCUsageSamples(_ context.Context, _, _, _ string, _ time.Duration) ([]MetricSample, error) {
	return nil, nil
}
func (noOpMetrics) GetNodeMetricSamples(_ context.Context, _, _, _ string, _ time.Duration) ([]MetricSample, error) {
	return nil, nil
}
