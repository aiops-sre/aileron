package slo

import (
	"context"
	"time"
)

// NewTrackerNoOp creates an in-memory SLO tracker with no external data source.
// Actual SLO data flows via Kafka to AlertHub's DB tables.
func NewTrackerNoOp() *Tracker {
	return &Tracker{slis: noOpSLIStore{}}
}

// BudgetSummary is the shape returned by the API endpoint.
type BudgetSummary struct {
	ClusterID string  `json:"cluster_id"`
	Namespace string  `json:"namespace"`
	SLOName   string  `json:"slo_name"`
	BurnRate  float64 `json:"burn_rate"`
	BudgetPct float64 `json:"budget_remaining_pct"`
	Status    string  `json:"status"`
}

// GetBudgets returns SLO budget summaries for a cluster/namespace.
// Returns empty slice when no SLOs have been configured.
func (t *Tracker) GetBudgets(clusterID, namespace string) []BudgetSummary {
	return []BudgetSummary{}
}

// noOpSLIStore satisfies SLIStore returning empty data.
type noOpSLIStore struct{}

func (noOpSLIStore) GetSLIData(_ context.Context, _, _, _ string, _ SLOType, _ time.Duration) ([]SLIDataPoint, error) {
	return nil, nil
}
