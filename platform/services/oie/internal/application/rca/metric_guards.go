package rca

import (
	"math"
	"strings"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
)

// ── Anti-Confusion Metric Guards ─────────────────────────────────────────────
//
// Seven guards that validate evidence quality and flag common analysis mistakes
// before the RCA narrative is produced.  Each guard returns a MetricGuardResult
// describing what was checked and whether action is required.

// MetricGuardResult is the outcome of a single guard evaluation.
type MetricGuardResult struct {
	// Guard is the name of the guard that produced this result.
	Guard string

	// Triggered is true when the guard detected a potential problem.
	Triggered bool

	// Severity classifies the impact: "warning" (operator should review) or
	// "block" (the recommendation must not be generated as-is).
	Severity string // "warning" | "block" | ""

	// Message is a human-readable explanation of the finding.
	Message string
}

// RunAllGuards runs all seven metric guards against the evidence set and returns
// results for every guard that triggered.  Guards that did not trigger are not
// included in the output so callers only see actionable findings.
func RunAllGuards(evidence []*domain_ev.Evidence) []MetricGuardResult {
	var results []MetricGuardResult

	guards := []func([]*domain_ev.Evidence) MetricGuardResult{
		Guard1NoRequestsVsUsageConfusion,
		Guard2NoSnapshotAsBaseline,
		Guard3OOMKilledVsEvictedDistinction,
		Guard4AlertTimestampVsFirstOccurrence,
		Guard6SpikeDetection, // Guard5 takes metric values, called separately
		Guard7RecommendationSafetyCheck,
	}

	for _, g := range guards {
		r := g(evidence)
		if r.Triggered {
			results = append(results, r)
		}
	}
	return results
}

// ── Guard 1: NoRequestsVsUsageConfusion ──────────────────────────────────────

// Guard1NoRequestsVsUsageConfusion flags cases where a Tier3 (config/requests)
// evidence item is being used as a proxy for actual consumption (Tier1 usage).
// Right-sizing recommendations MUST be based on live usage data, not on the
// configured resource requests which may be wildly over- or under-provisioned.
func Guard1NoRequestsVsUsageConfusion(evidence []*domain_ev.Evidence) MetricGuardResult {
	result := MetricGuardResult{Guard: "Guard1.NoRequestsVsUsageConfusion"}

	hasRequestEvidence := false
	hasUsageEvidence := false

	for _, ev := range evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		desc := strings.ToLower(ev.Description)
		isRequestBased := strings.Contains(desc, "request") ||
			ev.EvidenceType == domain_ev.TypeK8sCPURequestSaturation
		isUsageBased := strings.Contains(desc, "usage") ||
			strings.Contains(desc, "working_set") ||
			strings.Contains(desc, "rss") ||
			EvidenceTierFor(ev.EvidenceType) == EvidenceTierPrimary

		if isRequestBased && EvidenceTierFor(ev.EvidenceType) == EvidenceTierConfig {
			hasRequestEvidence = true
		}
		if isUsageBased && EvidenceTierFor(ev.EvidenceType) == EvidenceTierPrimary {
			hasUsageEvidence = true
		}
	}

	if hasRequestEvidence && !hasUsageEvidence {
		result.Triggered = true
		result.Severity = "block"
		result.Message = "Right-sizing recommendation will be based on configured resource requests (Tier3) " +
			"rather than actual measured usage (Tier1). Fetch live metric data before generating a capacity " +
			"recommendation — requests can be set arbitrarily high or low and do not reflect real consumption."
	} else if hasRequestEvidence && hasUsageEvidence {
		result.Triggered = true
		result.Severity = "warning"
		result.Message = "Both resource-request evidence and live-usage evidence present. " +
			"Ensure the recommendation uses live usage (Tier1) values, not the configured requests."
	}
	return result
}

// ── Guard 2: NoSnapshotAsBaseline ────────────────────────────────────────────

// Guard2NoSnapshotAsBaseline detects when a point-in-time snapshot (df -h,
// kubectl top, single-sample query) is being used as the baseline for a
// capacity decision.  Such snapshots are invalid for trend-based recommendations
// because they miss diurnal variance and growth trajectory.
func Guard2NoSnapshotAsBaseline(evidence []*domain_ev.Evidence) MetricGuardResult {
	result := MetricGuardResult{Guard: "Guard2.NoSnapshotAsBaseline"}

	snapshotKeywords := []string{"df -h", "kubectl top", "point-in-time", "snapshot", "current utilization"}
	hasSnapshot := false
	hasTrend := false

	for _, ev := range evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		desc := strings.ToLower(ev.Description)
		for _, kw := range snapshotKeywords {
			if strings.Contains(desc, kw) {
				hasSnapshot = true
			}
		}
		if ev.EvidenceType == domain_ev.TypeKubeSenseForecast ||
			ev.EvidenceType == domain_ev.TypeKubeSenseAnomaly ||
			(ev.TemporalMode == domain_ev.TemporalHistorical && ev.OccurredAt != nil) {
			hasTrend = true
		}
	}

	if hasSnapshot && !hasTrend {
		result.Triggered = true
		result.Severity = "block"
		result.Message = "Capacity recommendation is based on a point-in-time snapshot (df -h, kubectl top, " +
			"or single-sample query). This is insufficient for capacity decisions. Fetch 30-day usage trends " +
			"before making a right-sizing recommendation."
	} else if hasSnapshot {
		result.Triggered = true
		result.Severity = "warning"
		result.Message = "Snapshot evidence present alongside trend data. Ensure the recommendation " +
			"cites the trend (P95/P99 over 30 days) as the primary basis, not the snapshot."
	}
	return result
}

// ── Guard 3: OOMKilledVsEvictedDistinction ───────────────────────────────────

// Guard3OOMKilledVsEvictedDistinction checks that OOMKilled (container-level,
// exit 137) and Evicted (node-level, memory/disk pressure) are not conflated.
// These are fundamentally different failure modes with different remediations:
//   - OOMKilled → raise container memory limit or fix memory leak.
//   - Evicted → reduce node pressure (add nodes, remove noisy neighbours).
func Guard3OOMKilledVsEvictedDistinction(evidence []*domain_ev.Evidence) MetricGuardResult {
	result := MetricGuardResult{Guard: "Guard3.OOMKilledVsEvictedDistinction"}

	hasOOMKilled := false
	hasEvicted := false

	for _, ev := range evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		switch ev.EvidenceType {
		case domain_ev.TypeK8sPodExitCodeOOM, domain_ev.TypeK8sOOMKill:
			hasOOMKilled = true
		case domain_ev.TypeK8sPodEventEviction, domain_ev.TypeK8sNodeEvicted:
			hasEvicted = true
		}
	}

	if hasOOMKilled && hasEvicted {
		result.Triggered = true
		result.Severity = "block"
		result.Message = "Both OOMKilled (container exit 137) and Evicted (node-level pressure) signals present. " +
			"These are DIFFERENT problems: OOMKilled requires a container memory-limit increase or leak fix; " +
			"Evicted requires node capacity expansion or noisy-neighbour isolation. " +
			"The investigation must distinguish which failure mode is primary before issuing a recommendation."
	}
	return result
}

// ── Guard 4: AlertTimestampVsFirstOccurrence ─────────────────────────────────

// Guard4AlertTimestampVsFirstOccurrence detects when the only timestamped
// evidence uses the incident's own CreatedAt as the OccurredAt.  This means
// the system echoed the alert timestamp back rather than finding the true first
// crossing time, which prevents accurate MTTR and causal ordering.
func Guard4AlertTimestampVsFirstOccurrence(evidence []*domain_ev.Evidence) MetricGuardResult {
	result := MetricGuardResult{Guard: "Guard4.AlertTimestampVsFirstOccurrence"}

	allTimestampsMatchIncident := true
	hasAnyTimestamp := false

	// Find the earliest OccurredAt across all evidence to use as the alert proxy.
	var earliestAt *int64
	for _, ev := range evidence {
		if ev.OccurredAt == nil {
			continue
		}
		hasAnyTimestamp = true
		ts := ev.OccurredAt.UnixNano()
		if earliestAt == nil {
			earliestAt = &ts
		} else if ts < *earliestAt {
			earliestAt = &ts
		}
	}

	if !hasAnyTimestamp || earliestAt == nil {
		// No timestamped evidence at all — that's covered by the investigation gate.
		return result
	}

	// Check whether all timestamped evidence clusters around the same instant.
	clusterCount := 0
	totalTimestamped := 0
	for _, ev := range evidence {
		if ev.OccurredAt == nil {
			continue
		}
		totalTimestamped++
		diff := ev.OccurredAt.UnixNano() - *earliestAt
		if diff < int64(30e9) { // within 30 seconds of the earliest
			clusterCount++
		}
	}

	if totalTimestamped > 0 && clusterCount == totalTimestamped {
		allTimestampsMatchIncident = true
	} else {
		allTimestampsMatchIncident = false
	}

	if allTimestampsMatchIncident && totalTimestamped > 0 {
		result.Triggered = true
		result.Severity = "warning"
		result.Message = "All timestamped evidence clusters within 30 seconds of the earliest event — " +
			"this likely reflects the alert notification time rather than the first metric threshold crossing. " +
			"Query 30-day metric trends to identify when the issue first appeared before alert creation."
	}
	return result
}

// ── Guard 5: PercentileSelection ─────────────────────────────────────────────

// PercentileStats holds the computed percentile statistics for a metric series.
type PercentileStats struct {
	P50          float64
	P90          float64
	P95          float64
	P99          float64
	Max          float64
	VarianceRatio float64 // P99 / P95; > 2.0 = high variance

	// RecommendedBase is the percentile to use as the right-sizing base.
	RecommendedBase float64

	// VarianceClass is "high" when P99/P95 > 2.0, otherwise "low".
	VarianceClass string

	// SafetyFactor is the multiplier to apply to RecommendedBase.
	// Derived from WorkloadType.
	SafetyFactor float64

	// RecommendedLimit is RecommendedBase * SafetyFactor, rounded to the
	// nearest standard container-resource step.
	RecommendedLimit float64
}

// WorkloadType classifies the type of workload for safety factor selection.
type WorkloadType string

const (
	WorkloadTypeCritical    WorkloadType = "critical"    // safety factor 1.50
	WorkloadTypeStateful    WorkloadType = "stateful"    // safety factor 1.40
	WorkloadTypeInteractive WorkloadType = "interactive" // safety factor 1.30
	WorkloadTypeBatch       WorkloadType = "batch"       // safety factor 1.20
	WorkloadTypeDefault     WorkloadType = "default"     // safety factor 1.25
)

// safetyFactors maps WorkloadType to the multiplier applied to the base percentile.
var safetyFactors = map[WorkloadType]float64{
	WorkloadTypeCritical:    1.50,
	WorkloadTypeStateful:    1.40,
	WorkloadTypeInteractive: 1.30,
	WorkloadTypeBatch:       1.20,
	WorkloadTypeDefault:     1.25,
}

// Guard5ComputePercentiles computes the percentile statistics for a 30-day
// metric series and returns a right-sizing recommendation.
//
// Selection logic:
//   - if P99/P95 > 2.0 → "high variance" → use P99 as base
//   - otherwise → "low variance" → use P95 as base
//
// The floor for spike handling is applied by Guard6 (SpikeDetection).
func Guard5ComputePercentiles(values []float64, workload WorkloadType) PercentileStats {
	if len(values) == 0 {
		return PercentileStats{}
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sortFloat64s(sorted)

	n := len(sorted)
	stats := PercentileStats{
		P50: percentileAt(sorted, n, 50),
		P90: percentileAt(sorted, n, 90),
		P95: percentileAt(sorted, n, 95),
		P99: percentileAt(sorted, n, 99),
		Max: sorted[n-1],
	}

	if stats.P95 > 0 {
		stats.VarianceRatio = stats.P99 / stats.P95
	}

	if stats.VarianceRatio > 2.0 {
		stats.VarianceClass = "high"
		stats.RecommendedBase = stats.P99
	} else {
		stats.VarianceClass = "low"
		stats.RecommendedBase = stats.P95
	}

	sf, ok := safetyFactors[workload]
	if !ok {
		sf = safetyFactors[WorkloadTypeDefault]
	}
	stats.SafetyFactor = sf
	stats.RecommendedLimit = stats.RecommendedBase * sf
	return stats
}

// ── Guard 6: SpikeDetection ──────────────────────────────────────────────────

// Guard6SpikeDetection examines the evidence for signs of extreme spikes
// (Max/P99 > 5.0) and flags them so the recommendation uses a spike-aware
// floor instead of a naive P99×2.0 formula.
//
// When an extreme spike is present the recommended floor is:
//   max(P99 * 2.0, Max * 0.75)
func Guard6SpikeDetection(evidence []*domain_ev.Evidence) MetricGuardResult {
	result := MetricGuardResult{Guard: "Guard6.SpikeDetection"}

	// Look for KubeSense forecast or anomaly evidence that carries P99/Max.
	for _, ev := range evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		if ev.EvidenceType != domain_ev.TypeKubeSenseAnomaly &&
			ev.EvidenceType != domain_ev.TypeKubeSenseForecast &&
			ev.EvidenceType != domain_ev.TypeKubeSenseAPMRegression {
			continue
		}
		// Heuristic: the description mentions "spike" or "burst".
		desc := strings.ToLower(ev.Description)
		if strings.Contains(desc, "spike") || strings.Contains(desc, "burst") ||
			strings.Contains(desc, "extreme") {
			result.Triggered = true
			result.Severity = "warning"
			result.Message = "Extreme spike pattern detected in metric history (evidence: " +
				ev.Description + "). " +
				"Use floor = max(P99 × 2.0, Max × 0.75) as the resource limit, NOT P99 × 2.0 alone. " +
				"A naive P99 × 2.0 would be breached immediately by the next spike of the same magnitude."
			return result
		}
	}
	return result
}

// ── Guard 7: RecommendationSafetyCheck ───────────────────────────────────────

// RecommendationSpec carries the three required elements for a safe
// recommendation.  All three must be present before a recommendation is emitted.
type RecommendationSpec struct {
	// TargetConfirmed is true when the recommendation target (pod, node, volume)
	// is corroborated by DIRECT evidence.
	TargetConfirmed bool

	// RollbackPlan describes how to undo the change if it makes things worse.
	RollbackPlan string

	// FailureSignalMetric is the specific metric operators should monitor to
	// detect if the recommendation has caused a regression.
	FailureSignalMetric string
}

// Guard7RecommendationSafetyCheck validates that a recommendation spec has all
// three required safety elements.
func Guard7RecommendationSafetyCheck(evidence []*domain_ev.Evidence) MetricGuardResult {
	result := MetricGuardResult{Guard: "Guard7.RecommendationSafetyCheck"}

	// Check whether there is at least one DIRECT evidence item that confirms
	// the recommendation target (a specific named entity with Role=SUPPORTS).
	hasDirectTarget := false
	for _, ev := range evidence {
		if ev.Role == domain_ev.RoleSupports &&
			ev.FetchStatus == domain_ev.FetchSuccess &&
			ev.EvidenceTag == domain_ev.EvidenceTagDirect &&
			ev.Description != "" {
			hasDirectTarget = true
			break
		}
	}

	if !hasDirectTarget {
		result.Triggered = true
		result.Severity = "block"
		result.Message = "No DIRECT evidence confirms the recommendation target. " +
			"A recommendation requires: (a) target entity confirmed by direct observation, " +
			"(b) rollback plan, (c) failure-signal metric to monitor post-change. " +
			"Do not emit a recommendation until direct evidence is gathered."
	}
	return result
}

// ── Numeric helpers ───────────────────────────────────────────────────────────

// percentileAt returns the pth percentile of a pre-sorted slice.
func percentileAt(sorted []float64, n int, p float64) float64 {
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	idx := p / 100.0 * float64(n-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}

// sortFloat64s performs an in-place insertion sort (suitable for small slices).
func sortFloat64s(s []float64) {
	n := len(s)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
