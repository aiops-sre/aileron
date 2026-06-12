package analytics

import (
	"context"
	"time"
)

// OverviewAnalytics is the single-shot payload for the Overview tab.
type OverviewAnalytics struct {
	TimeRange string `json:"time_range"`

	// KPI cards
	TotalAlerts       int     `json:"total_alerts"`
	OpenAlerts        int     `json:"open_alerts"`
	CriticalAlerts    int     `json:"critical_alerts"`
	TotalIncidents    int     `json:"total_incidents"`
	OpenIncidents     int     `json:"open_incidents"`
	ResolvedIncidents int     `json:"resolved_incidents"`
	MTTR              float64 `json:"mttr_hours"`
	MTTA              float64 `json:"mtta_hours"`
	ResolutionRate    float64 `json:"resolution_rate"`
	NoiseReduction    float64 `json:"noise_reduction_pct"` // 1 - (incidents/alerts)
	AutoCreatedRate   float64 `json:"auto_created_rate"`   // % incidents auto-created by pipeline
	RCACompletionRate float64 `json:"rca_completion_rate"` // % incidents with completed RCA

	// Trends (daily buckets for the selected range)
	AlertTrend    []DailyBucket `json:"alert_trend"`
	IncidentTrend []DailyBucket `json:"incident_trend"`

	// Distribution
	AlertsBySeverity   map[string]int `json:"alerts_by_severity"`
	AlertsBySource     map[string]int `json:"alerts_by_source"`
	IncidentsBySeverity map[string]int `json:"incidents_by_severity"`
	IncidentsByStatus  map[string]int `json:"incidents_by_status"`
}

// DailyBucket is a single day in a time-series.
type DailyBucket struct {
	Date  string `json:"date"`  // YYYY-MM-DD
	Count int    `json:"count"`
}

// AlertDetailAnalytics is the payload for the Alert Analytics tab.
type AlertDetailAnalytics struct {
	TimeRange string `json:"time_range"`

	TotalAlerts     int     `json:"total_alerts"`
	CriticalAlerts  int     `json:"critical_alerts"`
	ResolutionRate  float64 `json:"resolution_rate"`
	AvgMTTR         float64 `json:"avg_mttr_hours"`
	DedupCount      int     `json:"dedup_count"` // sum of count field (how many raw events were deduped)

	Trend       []DailyBucket  `json:"trend"`
	BySeverity  map[string]int `json:"by_severity"`
	ByStatus    map[string]int `json:"by_status"`
	BySource    map[string]int `json:"by_source"`
	ByRegion    map[string]int `json:"by_region"`

	// Hourly distribution (0-23) averaged over the period
	HourlyDistribution []HourBucket `json:"hourly_distribution"`

	// Top services/entities generating most alerts
	TopServices []ServiceAlertCount `json:"top_services"`

	// MTTR by severity
	MTTRBySeverity map[string]float64 `json:"mttr_by_severity"`
}

// HourBucket is an hour-of-day alert count.
type HourBucket struct {
	Hour  int `json:"hour"`
	Count int `json:"count"`
}

// ServiceAlertCount is a service + count tuple.
type ServiceAlertCount struct {
	Service string `json:"service"`
	Count   int    `json:"count"`
}

// IncidentDetailAnalytics is the payload for the Incident Analytics tab.
type IncidentDetailAnalytics struct {
	TimeRange string `json:"time_range"`

	TotalIncidents    int     `json:"total_incidents"`
	OpenIncidents     int     `json:"open_incidents"`
	ResolvedIncidents int     `json:"resolved_incidents"`
	AutoCreated       int     `json:"auto_created"`
	ManualCreated     int     `json:"manual_created"`
	MTTR              float64 `json:"mttr_hours"`
	MTTA              float64 `json:"mtta_hours"`
	ResolutionRate    float64 `json:"resolution_rate"`
	EscalationRate    float64 `json:"escalation_rate"`
	RCACompletedCount int     `json:"rca_completed_count"`

	Trend       []DailyBucket  `json:"trend"`
	BySeverity  map[string]int `json:"by_severity"`
	ByStatus    map[string]int `json:"by_status"`
	ByPriority  map[string]int `json:"by_priority"`

	// MTTR trend per day
	MTTRTrend []MTTRBucket `json:"mttr_trend"`

	// Top services involved in incidents (from title/source heuristic)
	TopServicesByIncidents []ServiceAlertCount `json:"top_services_by_incidents"`
}

// MTTRBucket is a day + avg MTTR value.
type MTTRBucket struct {
	Date string  `json:"date"`
	MTTR float64 `json:"mttr_hours"`
}

// CorrelationAnalytics is the payload for the Correlation Intelligence tab.
type CorrelationAnalytics struct {
	TimeRange string `json:"time_range"`

	TotalProcessed    int     `json:"total_processed"`
	AlertsMerged      int     `json:"alerts_merged"`
	IncidentsCreated  int     `json:"incidents_created"`
	MonitoringQueued  int     `json:"monitoring_queued"`
	NoiseReductionPct float64 `json:"noise_reduction_pct"`
	AvgScore          float64 `json:"avg_correlation_score"`
	AvgConfidence     float64 `json:"avg_confidence"`

	// Decision distribution: created / merged / monitoring / dropped
	DecisionDistribution map[string]int `json:"decision_distribution"`

	// Strategy avg scores
	StrategyScores StrategyAvgScores `json:"strategy_scores"`

	// Alerts per incident distribution
	AlertsPerIncident []AlertsPerIncidentBucket `json:"alerts_per_incident"`

	// Daily processing trend
	Trend []DailyBucket `json:"trend"`
}

// StrategyAvgScores holds avg contribution of each correlation strategy.
type StrategyAvgScores struct {
	Topology float64 `json:"topology"`
	Semantic float64 `json:"semantic"`
	Temporal float64 `json:"temporal"`
	Rules    float64 `json:"rules"`
}

// AlertsPerIncidentBucket buckets how many alerts each incident has.
type AlertsPerIncidentBucket struct {
	Bucket string `json:"bucket"` // "1", "2-5", "6-10", "11-20", "20+"
	Count  int    `json:"count"`
}

// RCAAnalytics is the payload for the RCA Insights tab.
type RCAAnalytics struct {
	TimeRange string `json:"time_range"`

	TotalInvestigations int     `json:"total_investigations"`
	Completed           int     `json:"completed"`
	Failed              int     `json:"failed"`
	InProgress          int     `json:"in_progress"`
	CompletionRate      float64 `json:"completion_rate"`
	AvgConfidence       float64 `json:"avg_confidence"`
	AvgInvestigationMin float64 `json:"avg_investigation_minutes"`

	// Phase distribution
	PhaseDistribution map[string]int `json:"phase_distribution"`

	// Confidence score buckets: 0-0.2, 0.2-0.4, 0.4-0.6, 0.6-0.8, 0.8-1.0
	ConfidenceBuckets []ConfidenceBucket `json:"confidence_buckets"`

	// Recent completed investigations (for table display)
	RecentInvestigations []RCASummary `json:"recent_investigations"`

	// Trend
	Trend []DailyBucket `json:"trend"`
}

// ConfidenceBucket is a confidence range + count.
type ConfidenceBucket struct {
	Range string `json:"range"`
	Count int    `json:"count"`
}

// RCASummary is a row in the recent investigations table.
type RCASummary struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incident_id"`
	Phase      string    `json:"phase"`
	Confidence float64   `json:"confidence"`
	RootCause  string    `json:"root_cause"`
	CreatedAt  time.Time `json:"created_at"`
}

// SLOAnalytics is the payload for the SLO Performance tab.
type SLOAnalytics struct {
	TimeRange string `json:"time_range"`

	// Overall scores
	OverallCompliancePct float64 `json:"overall_compliance_pct"`
	MTTRCompliancePct    float64 `json:"mttr_compliance_pct"`
	MTTACompliancePct    float64 `json:"mtta_compliance_pct"`
	ResolutionSLOPct     float64 `json:"resolution_slo_pct"`

	// SLO targets (hard-coded industry standards by severity)
	SLOTargets []SLOTarget `json:"slo_targets"`

	// MTTR trend vs target
	MTTRTrend []MTTRBucket `json:"mttr_trend"`

	// Resolution rate trend
	ResolutionTrend []ResolutionBucket `json:"resolution_trend"`
}

// SLOTarget is a row in the SLO targets table.
type SLOTarget struct {
	Severity        string  `json:"severity"`
	MTTRTargetHours float64 `json:"mttr_target_hours"`
	ActualMTTR      float64 `json:"actual_mttr_hours"`
	MTTATargetHours float64 `json:"mtta_target_hours"`
	ActualMTTA      float64 `json:"actual_mtta_hours"`
	CompliancePct   float64 `json:"compliance_pct"`
}

// ResolutionBucket is a day + resolution rate.
type ResolutionBucket struct {
	Date           string  `json:"date"`
	ResolutionRate float64 `json:"resolution_rate"`
}

// 

// intervalClause converts a time range string to a SQL interval literal.
func intervalClause(timeRange string) string {
	switch timeRange {
	case "24h":
		return "24 hours"
	case "7d":
		return "7 days"
	case "30d":
		return "30 days"
	case "90d":
		return "90 days"
	default:
		return "30 days"
	}
}

// 
// GetOverviewAnalytics
// 

func (s *AnalyticsService) GetOverviewAnalytics(ctx context.Context, timeRange string) (*OverviewAnalytics, error) {
	iv := intervalClause(timeRange)
	out := &OverviewAnalytics{
		TimeRange:           timeRange,
		AlertsBySeverity:    make(map[string]int),
		AlertsBySource:      make(map[string]int),
		IncidentsBySeverity: make(map[string]int),
		IncidentsByStatus:   make(map[string]int),
	}

	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval`, iv).Scan(&out.TotalAlerts)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE status IN ('open','acknowledged','investigating') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.OpenAlerts)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE severity='critical' AND status NOT IN ('resolved','closed') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.CriticalAlerts)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval`, iv).Scan(&out.TotalIncidents)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status IN ('open','investigating','identified','monitoring') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.OpenIncidents)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status IN ('resolved','closed') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.ResolvedIncidents)

	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600), 0)
		FROM incidents WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&out.MTTR)
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (acknowledged_at - started_at))/3600), 0)
		FROM incidents WHERE acknowledged_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&out.MTTA)

	if out.TotalIncidents > 0 {
		out.ResolutionRate = float64(out.ResolvedIncidents) / float64(out.TotalIncidents) * 100
	}
	if out.TotalAlerts > 0 && out.TotalIncidents > 0 {
		out.NoiseReduction = (1.0 - float64(out.TotalIncidents)/float64(out.TotalAlerts)) * 100
		if out.NoiseReduction < 0 {
			out.NoiseReduction = 0
		}
	}

	var autoCreated int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE auto_created=TRUE AND created_at >= NOW() - $1::interval`, iv).Scan(&autoCreated)
	if out.TotalIncidents > 0 {
		out.AutoCreatedRate = float64(autoCreated) / float64(out.TotalIncidents) * 100
	}

	// RCA completion rate from incidents table (rca_status column)
	var rcaTotal, rcaDone int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE rca_status NOT IN ('','none') AND created_at >= NOW() - $1::interval`, iv).Scan(&rcaTotal)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE rca_status='completed' AND created_at >= NOW() - $1::interval`, iv).Scan(&rcaDone)
	if rcaTotal > 0 {
		out.RCACompletionRate = float64(rcaDone) / float64(rcaTotal) * 100
	}

	// Daily alert trend
	rows, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at), 'YYYY-MM-DD'), COUNT(*)
		FROM alerts WHERE created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows != nil {
		for rows.Next() {
			var b DailyBucket
			rows.Scan(&b.Date, &b.Count)
			out.AlertTrend = append(out.AlertTrend, b)
		}
		rows.Close()
	}

	// Daily incident trend
	rows2, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at), 'YYYY-MM-DD'), COUNT(*)
		FROM incidents WHERE created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows2 != nil {
		for rows2.Next() {
			var b DailyBucket
			rows2.Scan(&b.Date, &b.Count)
			out.IncidentTrend = append(out.IncidentTrend, b)
		}
		rows2.Close()
	}

	// Severity + source distributions
	rows3, _ := s.db.QueryContext(ctx, `SELECT severity, COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY severity`, iv)
	if rows3 != nil {
		for rows3.Next() {
			var k string; var v int
			rows3.Scan(&k, &v)
			out.AlertsBySeverity[k] = v
		}
		rows3.Close()
	}
	rows4, _ := s.db.QueryContext(ctx, `SELECT COALESCE(source,'unknown'), COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, iv)
	if rows4 != nil {
		for rows4.Next() {
			var k string; var v int
			rows4.Scan(&k, &v)
			out.AlertsBySource[k] = v
		}
		rows4.Close()
	}
	rows5, _ := s.db.QueryContext(ctx, `SELECT severity, COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval GROUP BY severity`, iv)
	if rows5 != nil {
		for rows5.Next() {
			var k string; var v int
			rows5.Scan(&k, &v)
			out.IncidentsBySeverity[k] = v
		}
		rows5.Close()
	}
	rows6, _ := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval GROUP BY status`, iv)
	if rows6 != nil {
		for rows6.Next() {
			var k string; var v int
			rows6.Scan(&k, &v)
			out.IncidentsByStatus[k] = v
		}
		rows6.Close()
	}

	return out, nil
}

// 
// GetAlertDetailAnalytics
// 

func (s *AnalyticsService) GetAlertDetailAnalytics(ctx context.Context, timeRange string) (*AlertDetailAnalytics, error) {
	iv := intervalClause(timeRange)
	out := &AlertDetailAnalytics{
		TimeRange:      timeRange,
		BySeverity:     make(map[string]int),
		ByStatus:       make(map[string]int),
		BySource:       make(map[string]int),
		ByRegion:       make(map[string]int),
		MTTRBySeverity: make(map[string]float64),
	}

	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval`, iv).Scan(&out.TotalAlerts)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE severity='critical' AND created_at >= NOW() - $1::interval`, iv).Scan(&out.CriticalAlerts)
	s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(COALESCE(count,1)),0) FROM alerts WHERE created_at >= NOW() - $1::interval`, iv).Scan(&out.DedupCount)

	var resolved int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE status IN ('resolved','closed') AND created_at >= NOW() - $1::interval`, iv).Scan(&resolved)
	if out.TotalAlerts > 0 {
		out.ResolutionRate = float64(resolved) / float64(out.TotalAlerts) * 100
	}
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - created_at))/3600),0)
		FROM alerts WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&out.AvgMTTR)

	// Daily trend
	rows, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at),'YYYY-MM-DD'), COUNT(*)
		FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY 1 ORDER BY 1`, iv)
	if rows != nil {
		for rows.Next() {
			var b DailyBucket
			rows.Scan(&b.Date, &b.Count)
			out.Trend = append(out.Trend, b)
		}
		rows.Close()
	}

	// By severity/status/source/region
	for _, q := range []struct {
		dest *map[string]int
		sql  string
	}{
		{&out.BySeverity, `SELECT severity, COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY severity`},
		{&out.ByStatus, `SELECT status, COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY status`},
		{&out.BySource, `SELECT COALESCE(source,'unknown'), COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY 1 ORDER BY 2 DESC LIMIT 10`},
		{&out.ByRegion, `SELECT COALESCE(region,'unknown'), COUNT(*) FROM alerts WHERE created_at >= NOW() - $1::interval GROUP BY 1 ORDER BY 2 DESC LIMIT 8`},
	} {
		rows, _ := s.db.QueryContext(ctx, q.sql, iv)
		if rows != nil {
			for rows.Next() {
				var k string; var v int
				rows.Scan(&k, &v)
				(*q.dest)[k] = v
			}
			rows.Close()
		}
	}

	// Hourly distribution
	rows2, _ := s.db.QueryContext(ctx, `
		SELECT EXTRACT(HOUR FROM created_at)::int, COUNT(*)
		FROM alerts WHERE created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows2 != nil {
		for rows2.Next() {
			var h HourBucket
			rows2.Scan(&h.Hour, &h.Count)
			out.HourlyDistribution = append(out.HourlyDistribution, h)
		}
		rows2.Close()
	}

	// Top services
	rows3, _ := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(entity_id,''), NULLIF(source,''), 'unknown'), COUNT(*)
		FROM alerts WHERE created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, iv)
	if rows3 != nil {
		for rows3.Next() {
			var s ServiceAlertCount
			rows3.Scan(&s.Service, &s.Count)
			out.TopServices = append(out.TopServices, s)
		}
		rows3.Close()
	}

	// MTTR by severity
	rows4, _ := s.db.QueryContext(ctx, `
		SELECT severity, COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - created_at))/3600),0)
		FROM alerts WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval
		GROUP BY severity`, iv)
	if rows4 != nil {
		for rows4.Next() {
			var sev string; var v float64
			rows4.Scan(&sev, &v)
			out.MTTRBySeverity[sev] = v
		}
		rows4.Close()
	}

	return out, nil
}

// 
// GetIncidentDetailAnalytics
// 

func (s *AnalyticsService) GetIncidentDetailAnalytics(ctx context.Context, timeRange string) (*IncidentDetailAnalytics, error) {
	iv := intervalClause(timeRange)
	out := &IncidentDetailAnalytics{
		TimeRange:  timeRange,
		BySeverity: make(map[string]int),
		ByStatus:   make(map[string]int),
		ByPriority: make(map[string]int),
	}

	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval`, iv).Scan(&out.TotalIncidents)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status IN ('open','investigating','identified','monitoring') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.OpenIncidents)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status IN ('resolved','closed') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.ResolvedIncidents)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE auto_created=TRUE AND created_at >= NOW() - $1::interval`, iv).Scan(&out.AutoCreated)
	out.ManualCreated = out.TotalIncidents - out.AutoCreated

	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600),0)
		FROM incidents WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&out.MTTR)
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (acknowledged_at - started_at))/3600),0)
		FROM incidents WHERE acknowledged_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&out.MTTA)

	if out.TotalIncidents > 0 {
		out.ResolutionRate = float64(out.ResolvedIncidents) / float64(out.TotalIncidents) * 100
	}

	// Escalation rate: incidents that changed status to 'monitoring' or have correlation_confidence > 0.8
	var escalated int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE correlation_confidence > 0.8 AND created_at >= NOW() - $1::interval`, iv).Scan(&escalated)
	if out.TotalIncidents > 0 {
		out.EscalationRate = float64(escalated) / float64(out.TotalIncidents) * 100
	}

	// RCA completed count from incidents table
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE rca_status='completed' AND created_at >= NOW() - $1::interval`, iv).Scan(&out.RCACompletedCount)

	// Daily trend
	rows, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at),'YYYY-MM-DD'), COUNT(*)
		FROM incidents WHERE created_at >= NOW() - $1::interval GROUP BY 1 ORDER BY 1`, iv)
	if rows != nil {
		for rows.Next() {
			var b DailyBucket
			rows.Scan(&b.Date, &b.Count)
			out.Trend = append(out.Trend, b)
		}
		rows.Close()
	}

	// By severity/status/priority
	for _, q := range []struct {
		dest *map[string]int
		sql  string
	}{
		{&out.BySeverity, `SELECT severity, COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval GROUP BY severity`},
		{&out.ByStatus, `SELECT status, COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval GROUP BY status`},
		{&out.ByPriority, `SELECT COALESCE(priority,'unknown'), COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval GROUP BY 1`},
	} {
		rows, _ := s.db.QueryContext(ctx, q.sql, iv)
		if rows != nil {
			for rows.Next() {
				var k string; var v int
				rows.Scan(&k, &v)
				(*q.dest)[k] = v
			}
			rows.Close()
		}
	}

	// MTTR trend per day
	rows2, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at),'YYYY-MM-DD'),
		       COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600),0)
		FROM incidents WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows2 != nil {
		for rows2.Next() {
			var b MTTRBucket
			rows2.Scan(&b.Date, &b.MTTR)
			out.MTTRTrend = append(out.MTTRTrend, b)
		}
		rows2.Close()
	}

	// Top services involved in incidents (from source field)
	rows3, _ := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(source,''),'unknown'), COUNT(*)
		FROM incidents WHERE created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, iv)
	if rows3 != nil {
		for rows3.Next() {
			var s ServiceAlertCount
			rows3.Scan(&s.Service, &s.Count)
			out.TopServicesByIncidents = append(out.TopServicesByIncidents, s)
		}
		rows3.Close()
	}

	return out, nil
}

// 
// GetCorrelationAnalytics
// 

func (s *AnalyticsService) GetCorrelationAnalytics(ctx context.Context, timeRange string) (*CorrelationAnalytics, error) {
	iv := intervalClause(timeRange)
	out := &CorrelationAnalytics{
		TimeRange:            timeRange,
		DecisionDistribution: make(map[string]int),
	}

	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_correlation_results WHERE processed_at >= NOW() - $1::interval`, iv).Scan(&out.TotalProcessed)

	// Decision distribution
	rows, _ := s.db.QueryContext(ctx, `
		SELECT COALESCE(decision,'unknown'), COUNT(*)
		FROM pipeline_correlation_results WHERE processed_at >= NOW() - $1::interval
		GROUP BY 1`, iv)
	if rows != nil {
		for rows.Next() {
			var k string; var v int
			rows.Scan(&k, &v)
			out.DecisionDistribution[k] = v
			switch k {
			case "CreateIncident":
				out.IncidentsCreated += v
			case "MergeIntoExisting":
				out.AlertsMerged += v
			case "DecisionMonitor":
				out.MonitoringQueued += v
			}
		}
		rows.Close()
	}

	if out.TotalProcessed > 0 {
		out.NoiseReductionPct = float64(out.AlertsMerged) / float64(out.TotalProcessed) * 100
	}

	// Avg final score and avg "confidence" (mean of the four strategy scores)
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(final_score),0),
		       COALESCE(AVG((semantic_score + temporal_score + topology_score + rules_score) / 4.0), 0)
		FROM pipeline_correlation_results WHERE processed_at >= NOW() - $1::interval`, iv).
		Scan(&out.AvgScore, &out.AvgConfidence)

	// Strategy avg scores from individual float columns
	row := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(AVG(topology_score),0),
		  COALESCE(AVG(semantic_score),0),
		  COALESCE(AVG(temporal_score),0),
		  COALESCE(AVG(rules_score),0)
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - $1::interval`, iv)
	row.Scan(&out.StrategyScores.Topology, &out.StrategyScores.Semantic,
		&out.StrategyScores.Temporal, &out.StrategyScores.Rules)

	// Alerts per incident distribution
	type apiBucket struct {
		label string
		min   int
		max   int
	}
	buckets := []apiBucket{
		{"1", 1, 1},
		{"2-5", 2, 5},
		{"6-10", 6, 10},
		{"11-20", 11, 20},
		{"21+", 21, 999999},
	}
	for _, b := range buckets {
		var cnt int
		s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM (
			  SELECT incident_id, COUNT(*) AS c
			  FROM pipeline_correlation_results
			  WHERE processed_at >= NOW() - $1::interval AND incident_id IS NOT NULL
			  GROUP BY incident_id
			  HAVING COUNT(*) BETWEEN $2 AND $3
			) sub`, iv, b.min, b.max).Scan(&cnt)
		out.AlertsPerIncident = append(out.AlertsPerIncident, AlertsPerIncidentBucket{Bucket: b.label, Count: cnt})
	}

	// Daily processing trend
	rows2, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', processed_at),'YYYY-MM-DD'), COUNT(*)
		FROM pipeline_correlation_results WHERE processed_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows2 != nil {
		for rows2.Next() {
			var b DailyBucket
			rows2.Scan(&b.Date, &b.Count)
			out.Trend = append(out.Trend, b)
		}
		rows2.Close()
	}

	return out, nil
}

// 
// GetRCAAnalytics
// 

func (s *AnalyticsService) GetRCAAnalytics(ctx context.Context, timeRange string) (*RCAAnalytics, error) {
	iv := intervalClause(timeRange)
	out := &RCAAnalytics{
		TimeRange:         timeRange,
		PhaseDistribution: make(map[string]int),
	}

	// RCA data lives on the incidents table (rca_status, rca_confidence, ai_root_cause)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE rca_status NOT IN ('','none') AND created_at >= NOW() - $1::interval`, iv).Scan(&out.TotalInvestigations)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE rca_status='completed' AND created_at >= NOW() - $1::interval`, iv).Scan(&out.Completed)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE rca_status='failed' AND created_at >= NOW() - $1::interval`, iv).Scan(&out.Failed)
	out.InProgress = out.TotalInvestigations - out.Completed - out.Failed
	if out.InProgress < 0 {
		out.InProgress = 0
	}
	if out.TotalInvestigations > 0 {
		out.CompletionRate = float64(out.Completed) / float64(out.TotalInvestigations) * 100
	}

	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(rca_confidence),0)
		FROM incidents
		WHERE rca_status='completed' AND created_at >= NOW() - $1::interval`, iv).Scan(&out.AvgConfidence)

	// AvgInvestigationMin: approximated as time from created_at updated_at for completed
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (updated_at - created_at))/60),0)
		FROM incidents
		WHERE rca_status='completed' AND created_at >= NOW() - $1::interval`, iv).Scan(&out.AvgInvestigationMin)

	// Phase distribution
	rows, _ := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(rca_status,''),'unknown'), COUNT(*)
		FROM incidents WHERE created_at >= NOW() - $1::interval
		  AND rca_status NOT IN ('','none')
		GROUP BY 1`, iv)
	if rows != nil {
		for rows.Next() {
			var k string
			var v int
			rows.Scan(&k, &v)
			out.PhaseDistribution[k] = v
		}
		rows.Close()
	}

	// Confidence buckets
	confBuckets := []struct {
		label  string
		lo, hi float64
	}{
		{"0.0–0.2", 0, 0.2},
		{"0.2–0.4", 0.2, 0.4},
		{"0.4–0.6", 0.4, 0.6},
		{"0.6–0.8", 0.6, 0.8},
		{"0.8–1.0", 0.8, 1.01},
	}
	for _, b := range confBuckets {
		var cnt int
		s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM incidents
			WHERE rca_status='completed'
			  AND rca_confidence >= $2
			  AND rca_confidence < $3
			  AND created_at >= NOW() - $1::interval`, iv, b.lo, b.hi).Scan(&cnt)
		out.ConfidenceBuckets = append(out.ConfidenceBuckets, ConfidenceBucket{Range: b.label, Count: cnt})
	}

	// Recent completed investigations
	rows2, _ := s.db.QueryContext(ctx, `
		SELECT id::text, id::text, COALESCE(NULLIF(rca_status,''),'unknown'),
		       COALESCE(rca_confidence,0),
		       COALESCE(NULLIF(ai_root_cause,''),''),
		       created_at
		FROM incidents
		WHERE rca_status NOT IN ('','none') AND created_at >= NOW() - $1::interval
		ORDER BY created_at DESC LIMIT 15`, iv)
	if rows2 != nil {
		for rows2.Next() {
			var r RCASummary
			rows2.Scan(&r.ID, &r.IncidentID, &r.Phase, &r.Confidence, &r.RootCause, &r.CreatedAt)
			if len(r.RootCause) > 120 {
				r.RootCause = r.RootCause[:120] + "…"
			}
			out.RecentInvestigations = append(out.RecentInvestigations, r)
		}
		rows2.Close()
	}

	// Daily trend
	rows3, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at),'YYYY-MM-DD'), COUNT(*)
		FROM incidents WHERE rca_status NOT IN ('','none') AND created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows3 != nil {
		for rows3.Next() {
			var b DailyBucket
			rows3.Scan(&b.Date, &b.Count)
			out.Trend = append(out.Trend, b)
		}
		rows3.Close()
	}

	return out, nil
}

// 
// GetSLOAnalytics
// 

// SLO targets (industry-standard thresholds)
var sloTargets = []struct {
	severity   string
	mttrTarget float64 // hours
	mttaTarget float64 // hours
}{
	{"critical", 1.0, 0.25},
	{"high", 4.0, 0.5},
	{"medium", 24.0, 2.0},
	{"low", 72.0, 8.0},
}

func (s *AnalyticsService) GetSLOAnalytics(ctx context.Context, timeRange string) (*SLOAnalytics, error) {
	iv := intervalClause(timeRange)
	out := &SLOAnalytics{TimeRange: timeRange}

	var complianceSum float64
	var complianceCount int

	for _, t := range sloTargets {
		var actualMTTR, actualMTTA float64
		s.db.QueryRowContext(ctx, `
			SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600),0)
			FROM incidents WHERE severity=$1 AND resolved_at IS NOT NULL AND created_at >= NOW() - $2::interval`, t.severity, iv).Scan(&actualMTTR)
		s.db.QueryRowContext(ctx, `
			SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (acknowledged_at - started_at))/3600),0)
			FROM incidents WHERE severity=$1 AND acknowledged_at IS NOT NULL AND created_at >= NOW() - $2::interval`, t.severity, iv).Scan(&actualMTTA)

		compliance := 100.0
		if actualMTTR > 0 && actualMTTR > t.mttrTarget {
			compliance = t.mttrTarget / actualMTTR * 100
		}
		if compliance > 100 {
			compliance = 100
		}

		out.SLOTargets = append(out.SLOTargets, SLOTarget{
			Severity:        t.severity,
			MTTRTargetHours: t.mttrTarget,
			ActualMTTR:      actualMTTR,
			MTTATargetHours: t.mttaTarget,
			ActualMTTA:      actualMTTA,
			CompliancePct:   compliance,
		})
		complianceSum += compliance
		complianceCount++
	}

	if complianceCount > 0 {
		out.OverallCompliancePct = complianceSum / float64(complianceCount)
	}

	// MTTR compliance: % of resolved incidents that met their severity target
	var totalResolved, mttrCompliant int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&totalResolved)
	for _, t := range sloTargets {
		var cnt int
		s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM incidents
			WHERE severity=$1 AND resolved_at IS NOT NULL
			  AND EXTRACT(EPOCH FROM (resolved_at - started_at))/3600 <= $2
			  AND created_at >= NOW() - $3::interval`, t.severity, t.mttrTarget, iv).Scan(&cnt)
		mttrCompliant += cnt
	}
	if totalResolved > 0 {
		out.MTTRCompliancePct = float64(mttrCompliant) / float64(totalResolved) * 100
	}

	// MTTA compliance
	var mttaCompliant int
	for _, t := range sloTargets {
		var cnt int
		s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM incidents
			WHERE severity=$1 AND acknowledged_at IS NOT NULL
			  AND EXTRACT(EPOCH FROM (acknowledged_at - started_at))/3600 <= $2
			  AND created_at >= NOW() - $3::interval`, t.severity, t.mttaTarget, iv).Scan(&cnt)
		mttaCompliant += cnt
	}
	var totalAcked int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE acknowledged_at IS NOT NULL AND created_at >= NOW() - $1::interval`, iv).Scan(&totalAcked)
	if totalAcked > 0 {
		out.MTTACompliancePct = float64(mttaCompliant) / float64(totalAcked) * 100
	}

	// Resolution rate SLO (target = 95%)
	var totalInc, resolvedInc int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE created_at >= NOW() - $1::interval`, iv).Scan(&totalInc)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status IN ('resolved','closed') AND created_at >= NOW() - $1::interval`, iv).Scan(&resolvedInc)
	actualResRate := 0.0
	if totalInc > 0 {
		actualResRate = float64(resolvedInc) / float64(totalInc) * 100
	}
	out.ResolutionSLOPct = actualResRate // vs target 95%

	// MTTR trend
	rows, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at),'YYYY-MM-DD'),
		       COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600),0)
		FROM incidents WHERE resolved_at IS NOT NULL AND created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows != nil {
		for rows.Next() {
			var b MTTRBucket
			rows.Scan(&b.Date, &b.MTTR)
			out.MTTRTrend = append(out.MTTRTrend, b)
		}
		rows.Close()
	}

	// Resolution rate trend
	rows2, _ := s.db.QueryContext(ctx, `
		SELECT TO_CHAR(DATE_TRUNC('day', created_at),'YYYY-MM-DD'),
		       ROUND(100.0 * COUNT(*) FILTER (WHERE status IN ('resolved','closed')) / NULLIF(COUNT(*),0), 1)
		FROM incidents WHERE created_at >= NOW() - $1::interval
		GROUP BY 1 ORDER BY 1`, iv)
	if rows2 != nil {
		for rows2.Next() {
			var b ResolutionBucket
			rows2.Scan(&b.Date, &b.ResolutionRate)
			out.ResolutionTrend = append(out.ResolutionTrend, b)
		}
		rows2.Close()
	}

	return out, nil
}
