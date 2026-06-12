// Package slo implements SLO (Service Level Objective) tracking.
// Computes error budgets, burn rates, and predicts SLO breaches.
// Integrates with AlertHub for SLO-based incident correlation.
package slo

import (
	"context"
	"fmt"
	"math"
	"time"
)

// SLOType classifies what is being measured.
type SLOType string

const (
	SLOAvailability    SLOType = "availability"    // uptime fraction
	SLOLatencyP99      SLOType = "latency_p99"     // p99 latency < threshold
	SLOErrorRate       SLOType = "error_rate"      // error fraction < threshold
	SLOThroughput      SLOType = "throughput"      // requests per second > floor
)

// SLODefinition is the specification for one SLO.
type SLODefinition struct {
	ID          string  `json:"id"`
	ServiceName string  `json:"service_name"`
	Namespace   string  `json:"namespace"`
	ClusterID   string  `json:"cluster_id"`
	Name        string  `json:"name"`
	Type        SLOType `json:"type"`

	// Target: the required level (e.g. 0.999 for 99.9% availability)
	Target      float64 `json:"target"`

	// Window: the rolling evaluation window (e.g. 30 days)
	WindowDays  int     `json:"window_days"`

	// Threshold: for latency SLOs, the max acceptable latency in ms
	ThresholdMs float64 `json:"threshold_ms,omitempty"`
}

// ErrorBudget is the computed state of an SLO's error budget.
type ErrorBudget struct {
	SLO              *SLODefinition `json:"slo"`
	ComputedAt       time.Time      `json:"computed_at"`
	WindowStart      time.Time      `json:"window_start"`

	// Budget
	TotalBudgetMinutes    float64 `json:"total_budget_minutes"`
	ConsumedBudgetMinutes float64 `json:"consumed_budget_minutes"`
	RemainingMinutes      float64 `json:"remaining_minutes"`
	RemainingPercent      float64 `json:"remaining_percent"` // 0-100

	// Current achievement
	CurrentSLI           float64 `json:"current_sli"`    // what we're measuring now
	CurrentTarget        float64 `json:"current_target"` // what we need

	// Burn rate: how fast the budget is being consumed
	// 1.0 = consuming budget at exactly the rate that will exhaust it at window end
	// >1.0 = faster than sustainable, budget will run out early
	BurnRate1h  float64 `json:"burn_rate_1h"`
	BurnRate6h  float64 `json:"burn_rate_6h"`
	BurnRate24h float64 `json:"burn_rate_24h"`

	// Predictions
	ProjectedExhaustionAt *time.Time `json:"projected_exhaustion_at,omitempty"`
	WillBreachSLO         bool       `json:"will_breach_slo"`

	// Status
	Status string `json:"status"` // healthy | warning | critical | breached
}

// BurnRateAlert is emitted when burn rate exceeds thresholds.
// Based on Google's multi-window, multi-burn-rate alerting approach.
type BurnRateAlert struct {
	SLOID         string    `json:"slo_id"`
	ServiceName   string    `json:"service_name"`
	DetectedAt    time.Time `json:"detected_at"`
	BurnRate      float64   `json:"burn_rate"`
	Window        string    `json:"window"`        // "1h" | "6h" | "24h"
	Severity      string    `json:"severity"`      // page | ticket | watch
	BudgetConsumedPct float64 `json:"budget_consumed_pct"`
	// "If current burn rate continues, SLO will breach in X hours"
	EstimatedHoursToExhaustion float64 `json:"estimated_hours_to_exhaustion"`
}

// SLIDataPoint is one observation of the SLI.
type SLIDataPoint struct {
	Timestamp     time.Time
	GoodEvents    int64   // requests that met the SLO
	TotalEvents   int64   // total requests
	SLIValue      float64 // good/total, or 1-(error_rate) etc.
}

// SLIStore fetches SLI data from the metrics backend.
type SLIStore interface {
	GetSLIData(ctx context.Context, clusterID, namespace, service string, sloType SLOType, since time.Duration) ([]SLIDataPoint, error)
}

// Tracker computes and tracks SLOs for registered services.
type Tracker struct {
	slis     SLIStore
}

// NewTracker creates an SLO tracker.
func NewTracker(slis SLIStore) *Tracker {
	return &Tracker{slis: slis}
}

// ComputeErrorBudget computes the current error budget state for an SLO.
func (t *Tracker) ComputeErrorBudget(ctx context.Context, slo *SLODefinition) (*ErrorBudget, error) {
	lookback := time.Duration(slo.WindowDays) * 24 * time.Hour
	data, err := t.slis.GetSLIData(ctx, slo.ClusterID, slo.Namespace, slo.ServiceName, slo.Type, lookback)
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("no SLI data for %s/%s: %w", slo.Namespace, slo.ServiceName, err)
	}

	now := time.Now()
	windowStart := now.Add(-lookback)
	windowMinutes := lookback.Minutes()

	// Total allowed failure budget in minutes
	totalBudgetMinutes := windowMinutes * (1 - slo.Target)

	// Consumed budget: time periods where SLI was below target
	var consumedMinutes float64
	var totalGood, totalRequests int64
	for _, d := range data {
		totalGood += d.GoodEvents
		totalRequests += d.TotalEvents
		if d.SLIValue < slo.Target {
			// Approximate: each data point represents ~1 minute
			consumedMinutes += 1.0
		}
	}

	currentSLI := 1.0
	if totalRequests > 0 {
		currentSLI = float64(totalGood) / float64(totalRequests)
	}

	remaining := math.Max(0, totalBudgetMinutes-consumedMinutes)
	remainingPct := remaining / totalBudgetMinutes * 100

	// Burn rates over different windows
	burn1h := t.burnRate(data, slo.Target, 60, totalBudgetMinutes)
	burn6h := t.burnRate(data, slo.Target, 360, totalBudgetMinutes)
	burn24h := t.burnRate(data, slo.Target, 1440, totalBudgetMinutes)

	// Projection: if current burn rate continues, when does budget exhaust?
	var projectedExhaustion *time.Time
	willBreach := false
	if burn1h > 0 && remaining > 0 {
		hoursToExhaustion := remaining / 60 / burn1h
		if hoursToExhaustion < float64(slo.WindowDays)*24 {
			t := now.Add(time.Duration(hoursToExhaustion * float64(time.Hour)))
			projectedExhaustion = &t
			willBreach = true
		}
	}

	status := budgetStatus(remainingPct, burn1h, burn6h)

	return &ErrorBudget{
		SLO: slo, ComputedAt: now, WindowStart: windowStart,
		TotalBudgetMinutes:    totalBudgetMinutes,
		ConsumedBudgetMinutes: consumedMinutes,
		RemainingMinutes:      remaining,
		RemainingPercent:      remainingPct,
		CurrentSLI:            currentSLI,
		CurrentTarget:         slo.Target,
		BurnRate1h:            burn1h,
		BurnRate6h:            burn6h,
		BurnRate24h:           burn24h,
		ProjectedExhaustionAt: projectedExhaustion,
		WillBreachSLO:         willBreach,
		Status:                status,
	}, nil
}

// CheckBurnRateAlerts applies Google's multi-window burn rate alerting rules.
// Returns alerts if burn rates cross the defined thresholds.
func CheckBurnRateAlerts(budget *ErrorBudget) []*BurnRateAlert {
	var alerts []*BurnRateAlert
	consumed := 100 - budget.RemainingPercent

	// Rule 1: fast burn — page immediately (burn rate > 14.4x over 1h, consuming >2% in 1h)
	if budget.BurnRate1h > 14.4 {
		hours := budget.RemainingMinutes / 60 / budget.BurnRate1h
		alerts = append(alerts, &BurnRateAlert{
			SLOID: budget.SLO.ID, ServiceName: budget.SLO.ServiceName,
			DetectedAt: time.Now(), BurnRate: budget.BurnRate1h,
			Window: "1h", Severity: "page",
			BudgetConsumedPct:          consumed,
			EstimatedHoursToExhaustion: hours,
		})
	}

	// Rule 2: medium burn — page (burn rate > 6x over 6h, consuming >5% in 6h)
	if budget.BurnRate6h > 6.0 {
		hours := budget.RemainingMinutes / 60 / budget.BurnRate6h
		alerts = append(alerts, &BurnRateAlert{
			SLOID: budget.SLO.ID, ServiceName: budget.SLO.ServiceName,
			DetectedAt: time.Now(), BurnRate: budget.BurnRate6h,
			Window: "6h", Severity: "page",
			BudgetConsumedPct:          consumed,
			EstimatedHoursToExhaustion: hours,
		})
	}

	// Rule 3: slow burn — ticket (burn rate > 3x over 24h, consuming >10% in 24h)
	if budget.BurnRate24h > 3.0 {
		hours := budget.RemainingMinutes / 60 / budget.BurnRate24h
		alerts = append(alerts, &BurnRateAlert{
			SLOID: budget.SLO.ID, ServiceName: budget.SLO.ServiceName,
			DetectedAt: time.Now(), BurnRate: budget.BurnRate24h,
			Window: "24h", Severity: "ticket",
			BudgetConsumedPct:          consumed,
			EstimatedHoursToExhaustion: hours,
		})
	}

	return alerts
}

// burnRate computes the burn rate over the last windowMinutes.
// A burn rate of 1.0 means budget is consumed at exactly the sustainable rate.
func (t *Tracker) burnRate(data []SLIDataPoint, target float64, windowMinutes int, totalBudgetMinutes float64) float64 {
	if totalBudgetMinutes == 0 { return 0 }

	cutoff := time.Now().Add(-time.Duration(windowMinutes) * time.Minute)
	var failedMinutes, totalMinutes float64
	for _, d := range data {
		if d.Timestamp.After(cutoff) {
			totalMinutes++
			if d.SLIValue < target {
				failedMinutes++
			}
		}
	}
	if totalMinutes == 0 { return 0 }

	// Sustainable failure rate = (1-target) minutes per window minute
	sustainableFailureRate := (1 - target)
	actualFailureRate := failedMinutes / totalMinutes
	if sustainableFailureRate == 0 { return 0 }
	return actualFailureRate / sustainableFailureRate
}

func budgetStatus(remainingPct, burn1h, burn6h float64) string {
	switch {
	case remainingPct <= 0:
		return "breached"
	case remainingPct < 5 || burn1h > 14.4:
		return "critical"
	case remainingPct < 25 || burn6h > 6:
		return "warning"
	default:
		return "healthy"
	}
}

// SLOReport is a summary of all SLOs for a service.
type SLOReport struct {
	ServiceName  string         `json:"service_name"`
	Namespace    string         `json:"namespace"`
	ClusterID    string         `json:"cluster_id"`
	ComputedAt   time.Time      `json:"computed_at"`
	Budgets      []*ErrorBudget `json:"budgets"`
	// Overall health: worst status across all SLOs
	OverallStatus string `json:"overall_status"`
	// SLOs at risk (burning budget faster than sustainable)
	AtRisk        int    `json:"at_risk"`
}

// Aggregate creates an SLOReport from multiple error budgets.
func Aggregate(budgets []*ErrorBudget) *SLOReport {
	if len(budgets) == 0 {
		return nil
	}
	r := &SLOReport{
		ServiceName: budgets[0].SLO.ServiceName,
		Namespace:   budgets[0].SLO.Namespace,
		ClusterID:   budgets[0].SLO.ClusterID,
		ComputedAt:  time.Now(),
		Budgets:     budgets,
		OverallStatus: "healthy",
	}
	statusPriority := map[string]int{"healthy": 0, "warning": 1, "critical": 2, "breached": 3}
	for _, b := range budgets {
		if statusPriority[b.Status] > statusPriority[r.OverallStatus] {
			r.OverallStatus = b.Status
		}
		if b.WillBreachSLO || b.BurnRate1h > 1.5 {
			r.AtRisk++
		}
	}
	return r
}
