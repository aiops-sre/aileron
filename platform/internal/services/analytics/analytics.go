package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AnalyticsService handles analytics and reporting
type AnalyticsService struct {
	db *sql.DB
}

// NewAnalyticsService creates a new analytics service
func NewAnalyticsService(db *sql.DB) *AnalyticsService {
	return &AnalyticsService{db: db}
}

// DashboardMetrics represents dashboard metrics
type DashboardMetrics struct {
	TotalAlerts         int                    `json:"total_alerts"`
	OpenAlerts          int                    `json:"open_alerts"`
	CriticalAlerts      int                    `json:"critical_alerts"`
	TotalIncidents      int                    `json:"total_incidents"`
	OpenIncidents       int                    `json:"open_incidents"`
	MTTR                float64                `json:"mttr_hours"`
	AlertsBySource      map[string]int         `json:"alerts_by_source"`
	AlertsBySeverity    map[string]int         `json:"alerts_by_severity"`
	IncidentsBySeverity map[string]int         `json:"incidents_by_severity"`
	AlertTrend          []TrendPoint           `json:"alert_trend"`
	IncidentTrend       []TrendPoint           `json:"incident_trend"`
	TopAlertSources     []SourceMetric         `json:"top_alert_sources"`
	RecentActivity      []ActivityEvent        `json:"recent_activity"`
	AIAnalysisStats     map[string]interface{} `json:"ai_analysis_stats"`
	NotificationStats   map[string]int         `json:"notification_stats"`
	MaintenanceWindows  int                    `json:"active_maintenance_windows"`
	OnCallEngineers     int                    `json:"oncall_engineers"`
}

// TrendPoint represents a point in time series
type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     int       `json:"value"`
}

// SourceMetric represents metrics by source
type SourceMetric struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

// ActivityEvent represents a recent activity
type ActivityEvent struct {
	ID          uuid.UUID `json:"id"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Severity    string    `json:"severity"`
	Timestamp   time.Time `json:"timestamp"`
}

// GetDashboardMetrics retrieves comprehensive dashboard metrics
func (s *AnalyticsService) GetDashboardMetrics(ctx context.Context) (*DashboardMetrics, error) {
	metrics := &DashboardMetrics{
		AlertsBySource:      make(map[string]int),
		AlertsBySeverity:    make(map[string]int),
		IncidentsBySeverity: make(map[string]int),
		AIAnalysisStats:     make(map[string]interface{}),
		NotificationStats:   make(map[string]int),
	}

	// Total alerts
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts").Scan(&metrics.TotalAlerts)

	// Open alerts
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE status IN ('open', 'acknowledged')").Scan(&metrics.OpenAlerts)

	// Critical alerts
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE severity = 'critical' AND status != 'resolved'").Scan(&metrics.CriticalAlerts)

	// Total incidents
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents").Scan(&metrics.TotalIncidents)

	// Open incidents
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents WHERE status IN ('open', 'investigating')").Scan(&metrics.OpenIncidents)

	// MTTR
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600), 0)
		FROM incidents
		WHERE resolved_at IS NOT NULL
	`).Scan(&metrics.MTTR)

	// Alerts by source
	rows, _ := s.db.QueryContext(ctx, "SELECT source, COUNT(*) FROM alerts GROUP BY source ORDER BY COUNT(*) DESC LIMIT 10")
	for rows.Next() {
		var source string
		var count int
		rows.Scan(&source, &count)
		metrics.AlertsBySource[source] = count
		metrics.TopAlertSources = append(metrics.TopAlertSources, SourceMetric{Source: source, Count: count})
	}
	rows.Close()

	// Alerts by severity
	rows2, _ := s.db.QueryContext(ctx, "SELECT severity, COUNT(*) FROM alerts GROUP BY severity")
	for rows2.Next() {
		var severity string
		var count int
		rows2.Scan(&severity, &count)
		metrics.AlertsBySeverity[severity] = count
	}
	rows2.Close()

	// Incidents by severity
	rows3, _ := s.db.QueryContext(ctx, "SELECT severity, COUNT(*) FROM incidents GROUP BY severity")
	for rows3.Next() {
		var severity string
		var count int
		rows3.Scan(&severity, &count)
		metrics.IncidentsBySeverity[severity] = count
	}
	rows3.Close()

	// Alert trend (last 24 hours)
	rows4, _ := s.db.QueryContext(ctx, `
		SELECT DATE_TRUNC('hour', created_at) as hour, COUNT(*)
		FROM alerts
		WHERE created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY hour
		ORDER BY hour
	`)
	for rows4.Next() {
		var timestamp time.Time
		var count int
		rows4.Scan(&timestamp, &count)
		metrics.AlertTrend = append(metrics.AlertTrend, TrendPoint{Timestamp: timestamp, Value: count})
	}
	rows4.Close()

	// Incident trend
	rows5, _ := s.db.QueryContext(ctx, `
		SELECT DATE_TRUNC('hour', created_at) as hour, COUNT(*)
		FROM incidents
		WHERE created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY hour
		ORDER BY hour
	`)
	for rows5.Next() {
		var timestamp time.Time
		var count int
		rows5.Scan(&timestamp, &count)
		metrics.IncidentTrend = append(metrics.IncidentTrend, TrendPoint{Timestamp: timestamp, Value: count})
	}
	rows5.Close()

	// Recent activity
	rows6, _ := s.db.QueryContext(ctx, `
		SELECT id, title, severity, created_at, 'alert' as type
		FROM alerts
		WHERE created_at >= NOW() - INTERVAL '1 hour'
		UNION ALL
		SELECT id, title, severity, created_at, 'incident' as type
		FROM incidents
		WHERE created_at >= NOW() - INTERVAL '1 hour'
		ORDER BY created_at DESC
		LIMIT 10
	`)
	for rows6.Next() {
		var activity ActivityEvent
		rows6.Scan(&activity.ID, &activity.Title, &activity.Severity, &activity.Timestamp, &activity.Type)
		metrics.RecentActivity = append(metrics.RecentActivity, activity)
	}
	rows6.Close()

	// AI analysis stats
	var aiAnalyzed, aiClassified int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE ai_analysis IS NOT NULL").Scan(&aiAnalyzed)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE ai_classification IS NOT NULL").Scan(&aiClassified)
	metrics.AIAnalysisStats["analyzed"] = aiAnalyzed
	metrics.AIAnalysisStats["classified"] = aiClassified

	// Notification stats (handle table not existing)
	rows7, err := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM notification_log GROUP BY status")
	if err == nil && rows7 != nil {
		for rows7.Next() {
			var status string
			var count int
			rows7.Scan(&status, &count)
			metrics.NotificationStats[status] = count
		}
		rows7.Close()
	}

	// Active maintenance windows (handle table not existing)
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM maintenance_windows WHERE status = 'active'").Scan(&metrics.MaintenanceWindows)
	if err != nil {
		// If table doesn't exist, set to 0
		metrics.MaintenanceWindows = 0
	}

	// OnCall engineers (if table doesn't exist, set to 0)
	// This might not exist in all deployments
	if metrics.OnCallEngineers == 0 {
		metrics.OnCallEngineers = 0
	}

	return metrics, nil
}

// AlertAnalytics represents alert analytics
type AlertAnalytics struct {
	TimeRange             string         `json:"time_range"`
	TotalAlerts           int            `json:"total_alerts"`
	BySeverity            map[string]int `json:"by_severity"`
	ByStatus              map[string]int `json:"by_status"`
	BySource              map[string]int `json:"by_source"`
	Trend                 []TrendPoint   `json:"trend"`
	TopTags               []TagMetric    `json:"top_tags"`
	AverageResolutionTime float64        `json:"avg_resolution_time_hours"`
	ResolutionRate        float64        `json:"resolution_rate"`
}

// TagMetric represents tag metrics
type TagMetric struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// GetAlertAnalytics retrieves alert analytics
func (s *AnalyticsService) GetAlertAnalytics(ctx context.Context, timeRange string) (*AlertAnalytics, error) {
	analytics := &AlertAnalytics{
		TimeRange:  timeRange,
		BySeverity: make(map[string]int),
		ByStatus:   make(map[string]int),
		BySource:   make(map[string]int),
	}

	// Determine time filter
	var timeFilter string
	switch timeRange {
	case "24h":
		timeFilter = "created_at >= NOW() - INTERVAL '24 hours'"
	case "7d":
		timeFilter = "created_at >= NOW() - INTERVAL '7 days'"
	case "30d":
		timeFilter = "created_at >= NOW() - INTERVAL '30 days'"
	default:
		timeFilter = "1=1"
	}

	// Total alerts
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE "+timeFilter).Scan(&analytics.TotalAlerts)

	// By severity
	rows, _ := s.db.QueryContext(ctx, "SELECT severity, COUNT(*) FROM alerts WHERE "+timeFilter+" GROUP BY severity")
	for rows.Next() {
		var severity string
		var count int
		rows.Scan(&severity, &count)
		analytics.BySeverity[severity] = count
	}
	rows.Close()

	// By status
	rows2, _ := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM alerts WHERE "+timeFilter+" GROUP BY status")
	for rows2.Next() {
		var status string
		var count int
		rows2.Scan(&status, &count)
		analytics.ByStatus[status] = count
	}
	rows2.Close()

	// By source
	rows3, _ := s.db.QueryContext(ctx, "SELECT source, COUNT(*) FROM alerts WHERE "+timeFilter+" GROUP BY source ORDER BY COUNT(*) DESC LIMIT 10")
	for rows3.Next() {
		var source string
		var count int
		rows3.Scan(&source, &count)
		analytics.BySource[source] = count
	}
	rows3.Close()

	// Average resolution time
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - created_at))/3600), 0)
		FROM alerts
		WHERE resolved_at IS NOT NULL AND `+timeFilter,
	).Scan(&analytics.AverageResolutionTime)

	// Resolution rate
	var resolved, total int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE status = 'resolved' AND "+timeFilter).Scan(&resolved)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE "+timeFilter).Scan(&total)
	if total > 0 {
		analytics.ResolutionRate = float64(resolved) / float64(total) * 100
	}

	return analytics, nil
}

// IncidentAnalytics represents incident analytics
type IncidentAnalytics struct {
	TimeRange      string         `json:"time_range"`
	TotalIncidents int            `json:"total_incidents"`
	BySeverity     map[string]int `json:"by_severity"`
	ByStatus       map[string]int `json:"by_status"`
	Trend          []TrendPoint   `json:"trend"`
	MTTR           float64        `json:"mttr_hours"`
	MTTAcknowledge float64        `json:"mtta_hours"`
	ResolutionRate float64        `json:"resolution_rate"`
}

// GetIncidentAnalytics retrieves incident analytics
func (s *AnalyticsService) GetIncidentAnalytics(ctx context.Context, timeRange string) (*IncidentAnalytics, error) {
	analytics := &IncidentAnalytics{
		TimeRange:  timeRange,
		BySeverity: make(map[string]int),
		ByStatus:   make(map[string]int),
	}

	var timeFilter string
	switch timeRange {
	case "24h":
		timeFilter = "created_at >= NOW() - INTERVAL '24 hours'"
	case "7d":
		timeFilter = "created_at >= NOW() - INTERVAL '7 days'"
	case "30d":
		timeFilter = "created_at >= NOW() - INTERVAL '30 days'"
	default:
		timeFilter = "1=1"
	}

	// Total incidents
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents WHERE "+timeFilter).Scan(&analytics.TotalIncidents)

	// By severity
	rows, _ := s.db.QueryContext(ctx, "SELECT severity, COUNT(*) FROM incidents WHERE "+timeFilter+" GROUP BY severity")
	for rows.Next() {
		var severity string
		var count int
		rows.Scan(&severity, &count)
		analytics.BySeverity[severity] = count
	}
	rows.Close()

	// By status
	rows2, _ := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM incidents WHERE "+timeFilter+" GROUP BY status")
	for rows2.Next() {
		var status string
		var count int
		rows2.Scan(&status, &count)
		analytics.ByStatus[status] = count
	}
	rows2.Close()

	// MTTR
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600), 0)
		FROM incidents
		WHERE resolved_at IS NOT NULL AND `+timeFilter,
	).Scan(&analytics.MTTR)

	// MTTA (Mean Time To Acknowledge)
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (acknowledged_at - started_at))/3600), 0)
		FROM incidents
		WHERE acknowledged_at IS NOT NULL AND `+timeFilter,
	).Scan(&analytics.MTTAcknowledge)

	return analytics, nil
}

// GenerateReport generates a custom report
func (s *AnalyticsService) GenerateReport(ctx context.Context, reportType string, startDate, endDate time.Time) (map[string]interface{}, error) {
	report := make(map[string]interface{})

	report["report_type"] = reportType
	report["start_date"] = startDate
	report["end_date"] = endDate
	report["generated_at"] = time.Now()

	switch reportType {
	case "alert_summary":
		report["data"] = s.generateAlertSummary(ctx, startDate, endDate)
	case "incident_summary":
		report["data"] = s.generateIncidentSummary(ctx, startDate, endDate)
	case "performance":
		report["data"] = s.generatePerformanceReport(ctx, startDate, endDate)
	case "compliance":
		report["data"] = s.generateComplianceReport(ctx, startDate, endDate)
	}

	return report, nil
}

func (s *AnalyticsService) generateAlertSummary(ctx context.Context, start, end time.Time) map[string]interface{} {
	summary := make(map[string]interface{})

	var total, resolved int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE created_at BETWEEN $1 AND $2", start, end).Scan(&total)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE created_at BETWEEN $1 AND $2 AND status = 'resolved'", start, end).Scan(&resolved)

	summary["total"] = total
	summary["resolved"] = resolved
	summary["resolution_rate"] = float64(resolved) / float64(total) * 100

	return summary
}

func (s *AnalyticsService) generateIncidentSummary(ctx context.Context, start, end time.Time) map[string]interface{} {
	summary := make(map[string]interface{})

	var total, resolved int
	var mttr float64

	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents WHERE created_at BETWEEN $1 AND $2", start, end).Scan(&total)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents WHERE created_at BETWEEN $1 AND $2 AND status = 'resolved'", start, end).Scan(&resolved)
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600), 0)
		FROM incidents
		WHERE resolved_at IS NOT NULL AND created_at BETWEEN $1 AND $2
	`, start, end).Scan(&mttr)

	summary["total"] = total
	summary["resolved"] = resolved
	summary["mttr_hours"] = mttr

	return summary
}

func (s *AnalyticsService) generatePerformanceReport(ctx context.Context, start, end time.Time) map[string]interface{} {
	report := make(map[string]interface{})

	// API performance metrics would go here
	report["api_requests"] = 0
	report["avg_response_time_ms"] = 0
	report["error_rate"] = 0

	return report
}

func (s *AnalyticsService) generateComplianceReport(ctx context.Context, start, end time.Time) map[string]interface{} {
	report := make(map[string]interface{})

	// Compliance metrics
	var totalAuditLogs int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE created_at BETWEEN $1 AND $2", start, end).Scan(&totalAuditLogs)

	report["audit_logs"] = totalAuditLogs
	report["period_start"] = start
	report["period_end"] = end

	return report
}

// ExportMetrics exports metrics in Prometheus format
func (s *AnalyticsService) ExportMetrics(ctx context.Context) (string, error) {
	metrics := ""

	// Alert metrics
	var totalAlerts, openAlerts, criticalAlerts int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts").Scan(&totalAlerts)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE status IN ('open', 'acknowledged')").Scan(&openAlerts)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts WHERE severity = 'critical'").Scan(&criticalAlerts)

	metrics += fmt.Sprintf("alerthub_alerts_total %d\n", totalAlerts)
	metrics += fmt.Sprintf("alerthub_alerts_open %d\n", openAlerts)
	metrics += fmt.Sprintf("alerthub_alerts_critical %d\n", criticalAlerts)

	// Incident metrics
	var totalIncidents, openIncidents int
	var mttr float64
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents").Scan(&totalIncidents)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents WHERE status IN ('open', 'investigating')").Scan(&openIncidents)
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))), 0)
		FROM incidents WHERE resolved_at IS NOT NULL
	`).Scan(&mttr)

	metrics += fmt.Sprintf("alerthub_incidents_total %d\n", totalIncidents)
	metrics += fmt.Sprintf("alerthub_incidents_open %d\n", openIncidents)
	metrics += fmt.Sprintf("alerthub_mttr_seconds %.2f\n", mttr)

	return metrics, nil
}
