// Package cost implements Kubernetes resource efficiency and cost intelligence.
// Computes waste from over-provisioned workloads, generates rightsizing
// recommendations, attributes costs to teams/namespaces, and detects idle resources.
package cost

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// ResourceEfficiency measures how efficiently a workload uses its requested resources.
type ResourceEfficiency struct {
	ClusterID    string    `json:"cluster_id"`
	Namespace    string    `json:"namespace"`
	WorkloadName string    `json:"workload_name"`
	WorkloadKind string    `json:"workload_kind"`
	ComputedAt   time.Time `json:"computed_at"`

	// CPU
	CPURequestedMillicores  float64 `json:"cpu_requested_millicores"`
	CPUActualMillicores     float64 `json:"cpu_actual_millicores_p95"`   // p95 over 7d
	CPUEfficiency           float64 `json:"cpu_efficiency"`              // actual/requested, 0-1
	CPUWasteMillicores      float64 `json:"cpu_waste_millicores"`

	// Memory
	MemoryRequestedBytes    float64 `json:"memory_requested_bytes"`
	MemoryActualBytes       float64 `json:"memory_actual_bytes_p95"`     // p95 over 7d
	MemoryEfficiency        float64 `json:"memory_efficiency"`
	MemoryWasteBytes        float64 `json:"memory_waste_bytes"`

	// Replicas
	ReplicaCount            int     `json:"replica_count"`

	// Estimated monthly waste cost (CPU + memory at cloud list prices)
	EstimatedMonthlyCostUSD float64 `json:"estimated_monthly_cost_usd"`
	EstimatedWasteUSD       float64 `json:"estimated_waste_usd"`

	// Classification
	EfficiencyGrade  string `json:"efficiency_grade"`   // A/B/C/D/F
	IsIdle           bool   `json:"is_idle"`             // <5% CPU over 24h
	IsOverProvisioned bool  `json:"is_overprovisioned"` // efficiency < 30%
}

// RightsizingRecommendation suggests new resource requests/limits.
type RightsizingRecommendation struct {
	ClusterID    string    `json:"cluster_id"`
	Namespace    string    `json:"namespace"`
	WorkloadName string    `json:"workload_name"`
	WorkloadKind string    `json:"workload_kind"`
	ContainerName string   `json:"container_name"`
	GeneratedAt  time.Time `json:"generated_at"`

	// Current settings
	CurrentCPURequest    string `json:"current_cpu_request"`
	CurrentCPULimit      string `json:"current_cpu_limit"`
	CurrentMemoryRequest string `json:"current_memory_request"`
	CurrentMemoryLimit   string `json:"current_memory_limit"`

	// Recommended settings
	RecommendedCPURequest    string `json:"recommended_cpu_request"`
	RecommendedCPULimit      string `json:"recommended_cpu_limit"`
	RecommendedMemoryRequest string `json:"recommended_memory_request"`
	RecommendedMemoryLimit   string `json:"recommended_memory_limit"`

	// Impact
	CPUSavingMillicores  float64 `json:"cpu_saving_millicores"`
	MemorySavingMiB      float64 `json:"memory_saving_mib"`
	MonthlySavingsUSD    float64 `json:"monthly_savings_usd"`

	// Confidence: how much data supports this recommendation
	Confidence           float64 `json:"confidence"` // 0-1
	DataDays             int     `json:"data_days"`   // days of usage data used

	// Safety margin applied (10-30%)
	SafetyMarginPercent  int     `json:"safety_margin_percent"`
	Rationale            string  `json:"rationale"`
}

// NamespaceCostSummary attributes resource costs to a namespace.
type NamespaceCostSummary struct {
	ClusterID    string  `json:"cluster_id"`
	Namespace    string  `json:"namespace"`
	Team         string  `json:"team,omitempty"` // from namespace labels
	Month        string  `json:"month"`           // YYYY-MM

	// Totals
	CPUCoreHours    float64 `json:"cpu_core_hours"`
	MemoryGiBHours  float64 `json:"memory_gib_hours"`
	StorageGiBHours float64 `json:"storage_gib_hours"`

	// Estimated costs
	CPUCostUSD      float64 `json:"cpu_cost_usd"`
	MemoryCostUSD   float64 `json:"memory_cost_usd"`
	StorageCostUSD  float64 `json:"storage_cost_usd"`
	TotalCostUSD    float64 `json:"total_cost_usd"`

	// Month-over-month change
	PreviousMonthUSD float64 `json:"previous_month_usd"`
	ChangePercent    float64 `json:"change_percent"`

	// Top cost drivers
	TopWorkloads []WorkloadCost `json:"top_workloads"`
}

// WorkloadCost breaks down cost for one workload.
type WorkloadCost struct {
	WorkloadName string  `json:"workload_name"`
	WorkloadKind string  `json:"workload_kind"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Replicas     int     `json:"replicas"`
}

// CloudPricing holds per-unit costs for resource estimation.
// Defaults to AWS us-east-1 on-demand pricing; configurable per deployment.
type CloudPricing struct {
	CPUPerCoreHourUSD    float64 // default: $0.048 (c5.large equivalent)
	MemoryPerGiBHourUSD  float64 // default: $0.006
	StoragePerGiBMonthUSD float64 // default: $0.10 (EBS gp3)
}

// DefaultPricing returns conservative AWS-equivalent default pricing.
var DefaultPricing = CloudPricing{
	CPUPerCoreHourUSD:    0.048,
	MemoryPerGiBHourUSD:  0.006,
	StoragePerGiBMonthUSD: 0.10,
}

// MetricsSample is one observed usage data point.
type MetricsSample struct {
	Timestamp       time.Time
	CPUMillicores   float64
	MemoryBytes     float64
}

// MetricsStore fetches historical usage metrics.
type MetricsStore interface {
	GetWorkloadSamples(ctx context.Context, clusterID, namespace, workloadName string, since time.Duration) ([]MetricsSample, error)
}

// EfficiencyAnalyzer computes resource efficiency for workloads.
type EfficiencyAnalyzer struct {
	metrics MetricsStore
	pricing CloudPricing
}

// NewEfficiencyAnalyzer creates an analyzer.
func NewEfficiencyAnalyzer(metrics MetricsStore, pricing CloudPricing) *EfficiencyAnalyzer {
	return &EfficiencyAnalyzer{metrics: metrics, pricing: pricing}
}

// Analyze computes efficiency for one workload.
func (a *EfficiencyAnalyzer) Analyze(
	ctx context.Context,
	clusterID, namespace, workloadName, workloadKind string,
	cpuRequestedMillicores, memoryRequestedBytes float64,
	replicas int,
) (*ResourceEfficiency, error) {
	samples, err := a.metrics.GetWorkloadSamples(ctx, clusterID, namespace, workloadName, 7*24*time.Hour)
	if err != nil || len(samples) < 12 {
		return nil, fmt.Errorf("insufficient data: %d samples", len(samples))
	}

	cpuP95 := percentile95(extractCPU(samples))
	memP95 := percentile95(extractMemory(samples))

	cpuEff := 0.0
	memEff := 0.0
	if cpuRequestedMillicores > 0 {
		cpuEff = math.Min(1.0, cpuP95/cpuRequestedMillicores)
	}
	if memoryRequestedBytes > 0 {
		memEff = math.Min(1.0, memP95/memoryRequestedBytes)
	}

	cpuWaste := math.Max(0, cpuRequestedMillicores-cpuP95) * float64(replicas)
	memWaste := math.Max(0, memoryRequestedBytes-memP95) * float64(replicas)

	// Monthly cost estimate
	hoursPerMonth := 730.0
	cpuCostMonthly := (cpuRequestedMillicores * float64(replicas) / 1000) * hoursPerMonth * a.pricing.CPUPerCoreHourUSD
	memCostMonthly := (memoryRequestedBytes * float64(replicas) / (1024 * 1024 * 1024)) * hoursPerMonth * a.pricing.MemoryPerGiBHourUSD
	totalCost := cpuCostMonthly + memCostMonthly

	wasteCPUCost := (cpuWaste / 1000) * hoursPerMonth * a.pricing.CPUPerCoreHourUSD
	wasteMemCost := (memWaste / (1024 * 1024 * 1024)) * hoursPerMonth * a.pricing.MemoryPerGiBHourUSD
	totalWaste := wasteCPUCost + wasteMemCost

	avgEff := (cpuEff + memEff) / 2
	grade := efficiencyGrade(avgEff)
	isIdle := cpuP95 < cpuRequestedMillicores*0.05 // using <5% of request
	isOverProv := avgEff < 0.30

	return &ResourceEfficiency{
		ClusterID: clusterID, Namespace: namespace,
		WorkloadName: workloadName, WorkloadKind: workloadKind,
		ComputedAt: time.Now(),
		CPURequestedMillicores: cpuRequestedMillicores,
		CPUActualMillicores:    cpuP95,
		CPUEfficiency:          cpuEff,
		CPUWasteMillicores:     cpuWaste,
		MemoryRequestedBytes:   memoryRequestedBytes,
		MemoryActualBytes:      memP95,
		MemoryEfficiency:       memEff,
		MemoryWasteBytes:       memWaste,
		ReplicaCount:           replicas,
		EstimatedMonthlyCostUSD: totalCost,
		EstimatedWasteUSD:       totalWaste,
		EfficiencyGrade:         grade,
		IsIdle:                  isIdle,
		IsOverProvisioned:       isOverProv,
	}, nil
}

// Recommend generates a rightsizing recommendation with a safety margin.
// safetyMarginPercent (e.g. 20) adds headroom above the p95 usage.
func Recommend(eff *ResourceEfficiency, containerName string, safetyMarginPercent int) *RightsizingRecommendation {
	margin := 1.0 + float64(safetyMarginPercent)/100.0

	recCPU := math.Ceil(eff.CPUActualMillicores*margin/10) * 10 // round to 10m
	recMem := math.Ceil(eff.MemoryActualBytes*margin/(64*1024*1024)) * 64 * 1024 * 1024 // round to 64Mi

	cpuSaving := eff.CPUWasteMillicores
	memSavingMiB := eff.MemoryWasteBytes / (1024 * 1024)

	hoursPerMonth := 730.0
	pricing := DefaultPricing
	savings := (cpuSaving/1000)*hoursPerMonth*pricing.CPUPerCoreHourUSD +
		(eff.MemoryWasteBytes/(1024*1024*1024))*hoursPerMonth*pricing.MemoryPerGiBHourUSD

	rationale := fmt.Sprintf(
		"p95 CPU=%.0fm (%.0f%% of %.0fm requested); p95 memory=%.0fMiB (%.0f%% of %.0fMiB requested). "+
			"%d%% safety margin applied.",
		eff.CPUActualMillicores, eff.CPUEfficiency*100, eff.CPURequestedMillicores,
		eff.MemoryActualBytes/(1024*1024), eff.MemoryEfficiency*100, eff.MemoryRequestedBytes/(1024*1024),
		safetyMarginPercent,
	)

	return &RightsizingRecommendation{
		ClusterID: eff.ClusterID, Namespace: eff.Namespace,
		WorkloadName: eff.WorkloadName, WorkloadKind: eff.WorkloadKind,
		ContainerName: containerName, GeneratedAt: time.Now(),
		CurrentCPURequest:    fmt.Sprintf("%.0fm", eff.CPURequestedMillicores),
		CurrentMemoryRequest: fmt.Sprintf("%.0fMi", eff.MemoryRequestedBytes/(1024*1024)),
		RecommendedCPURequest:    fmt.Sprintf("%.0fm", recCPU),
		RecommendedCPULimit:      fmt.Sprintf("%.0fm", recCPU*2),   // limit = 2× request
		RecommendedMemoryRequest: fmt.Sprintf("%.0fMi", recMem/(1024*1024)),
		RecommendedMemoryLimit:   fmt.Sprintf("%.0fMi", recMem/(1024*1024)),
		CPUSavingMillicores: cpuSaving, MemorySavingMiB: memSavingMiB,
		MonthlySavingsUSD: savings,
		Confidence:        math.Min(1.0, float64(7)/7.0), // 7 days of data = full confidence
		DataDays:          7, SafetyMarginPercent: safetyMarginPercent,
		Rationale: rationale,
	}
}

// TopWasters returns the N workloads with the highest estimated monthly waste.
func TopWasters(efficiencies []*ResourceEfficiency, n int) []*ResourceEfficiency {
	sorted := make([]*ResourceEfficiency, len(efficiencies))
	copy(sorted, efficiencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EstimatedWasteUSD > sorted[j].EstimatedWasteUSD
	})
	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// helpers

func percentile95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if idx < 0 { idx = 0 }
	return sorted[idx]
}

func extractCPU(samples []MetricsSample) []float64 {
	v := make([]float64, len(samples))
	for i, s := range samples { v[i] = s.CPUMillicores }
	return v
}

func extractMemory(samples []MetricsSample) []float64 {
	v := make([]float64, len(samples))
	for i, s := range samples { v[i] = s.MemoryBytes }
	return v
}

func efficiencyGrade(eff float64) string {
	switch {
	case eff >= 0.80: return "A"
	case eff >= 0.60: return "B"
	case eff >= 0.40: return "C"
	case eff >= 0.20: return "D"
	default:          return "F"
	}
}
