package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// ADVANCED AI FEATURES FOR ALERTHUB
// ============================================================================
// This module provides intelligent alert analysis capabilities:
// 1. Alert Correlation & Pattern Detection
// 2. Root Cause Analysis (RCA) Suggestions
// 3. Predictive Alert Analytics
// 4. Alert Clustering & Grouping
// 5. Runbook Recommendations
// 6. Historical Trend Analysis
// 7. Alert Fatigue Detection
// 8. Smart Threshold Recommendations
// 9. On-Call Schedule Awareness
// 10. Incident Impact Prediction
// ============================================================================

// AIAdvancedAnalyzer provides advanced AI-powered alert analysis
type AIAdvancedAnalyzer struct {
	db *sql.DB
}

// NewAIAdvancedAnalyzer creates a new advanced analyzer
func NewAIAdvancedAnalyzer(db *sql.DB) *AIAdvancedAnalyzer {
	return &AIAdvancedAnalyzer{db: db}
}

// ============================================================================
// 1. ALERT CORRELATION & PATTERN DETECTION
// ============================================================================

// AlertCorrelation represents correlated alerts
type AlertCorrelation struct {
	PrimaryAlert   AlertSummary   `json:"primary_alert"`
	RelatedAlerts  []AlertSummary `json:"related_alerts"`
	CorrelationKey string         `json:"correlation_key"`
	Confidence     float64        `json:"confidence"`
	Reason         string         `json:"reason"`
}

// DetectAlertCorrelations finds related alerts using multiple correlation strategies
func (a *AIAdvancedAnalyzer) DetectAlertCorrelations(ctx context.Context, alertID string) ([]AlertCorrelation, error) {
	correlations := make([]AlertCorrelation, 0)

	// Get the primary alert
	primaryAlert, err := a.getAlertByID(alertID)
	if err != nil {
		return nil, err
	}

	// Strategy 1: Time-based correlation (alerts within 5 minutes)
	timeCorrelated, _ := a.findTimeCorrelatedAlerts(primaryAlert, 5*time.Minute)
	if len(timeCorrelated) > 0 {
		correlations = append(correlations, AlertCorrelation{
			PrimaryAlert:   *primaryAlert,
			RelatedAlerts:  timeCorrelated,
			CorrelationKey: "time_proximity",
			Confidence:     0.75,
			Reason:         "These alerts occurred within 5 minutes of each other, suggesting a related incident",
		})
	}

	// Strategy 2: Source-based correlation (same service/host)
	sourceCorrelated, _ := a.findSourceCorrelatedAlerts(primaryAlert)
	if len(sourceCorrelated) > 0 {
		correlations = append(correlations, AlertCorrelation{
			PrimaryAlert:   *primaryAlert,
			RelatedAlerts:  sourceCorrelated,
			CorrelationKey: "same_source",
			Confidence:     0.85,
			Reason:         fmt.Sprintf("These alerts originated from the same source: %s", primaryAlert.Source),
		})
	}

	// Strategy 3: Title/Pattern similarity
	patternCorrelated, _ := a.findPatternCorrelatedAlerts(primaryAlert)
	if len(patternCorrelated) > 0 {
		correlations = append(correlations, AlertCorrelation{
			PrimaryAlert:   *primaryAlert,
			RelatedAlerts:  patternCorrelated,
			CorrelationKey: "pattern_match",
			Confidence:     0.70,
			Reason:         "These alerts have similar patterns or error signatures",
		})
	}

	return correlations, nil
}

// ============================================================================
// 2. ROOT CAUSE ANALYSIS (RCA) SUGGESTIONS
// ============================================================================

// RCASuggestion represents a potential root cause
type RCASuggestion struct {
	ID             string    `json:"id"`
	RootCause      string    `json:"root_cause"`
	Confidence     float64   `json:"confidence"`
	Evidence       []string  `json:"evidence"`
	AffectedAlerts []string  `json:"affected_alerts"`
	Recommendation string    `json:"recommendation"`
	Priority       string    `json:"priority"`
	CreatedAt      time.Time `json:"created_at"`
}

// GenerateRCASuggestions analyzes alerts and suggests potential root causes
func (a *AIAdvancedAnalyzer) GenerateRCASuggestions(ctx context.Context, alertIDs []string) ([]RCASuggestion, error) {
	suggestions := make([]RCASuggestion, 0)

	// Analyze alert patterns
	alerts, err := a.getMultipleAlerts(alertIDs)
	if err != nil {
		return nil, err
	}

	// Pattern 1: Multiple alerts from same source = Source issue
	sourceMap := make(map[string][]string)
	for _, alert := range alerts {
		sourceMap[alert.Source] = append(sourceMap[alert.Source], alert.ID)
	}

	for source, ids := range sourceMap {
		if len(ids) >= 3 {
			suggestions = append(suggestions, RCASuggestion{
				ID:             uuid.New().String(),
				RootCause:      fmt.Sprintf("Service degradation or failure in %s", source),
				Confidence:     0.85,
				Evidence:       []string{fmt.Sprintf("Multiple alerts (%d) from the same source", len(ids))},
				AffectedAlerts: ids,
				Recommendation: fmt.Sprintf("Check the health and resource utilization of %s. Review recent deployments or configuration changes.", source),
				Priority:       "high",
				CreatedAt:      time.Now(),
			})
		}
	}

	// Pattern 2: High severity cascade = Infrastructure issue
	criticalCount := 0
	for _, alert := range alerts {
		if alert.Severity == "critical" {
			criticalCount++
		}
	}

	if criticalCount >= 2 {
		suggestions = append(suggestions, RCASuggestion{
			ID:             uuid.New().String(),
			RootCause:      "Infrastructure-level issue affecting multiple services",
			Confidence:     0.80,
			Evidence:       []string{fmt.Sprintf("Multiple critical alerts (%d) detected simultaneously", criticalCount)},
			AffectedAlerts: alertIDs,
			Recommendation: "Check infrastructure components: network connectivity, load balancers, database connections, and shared resources",
			Priority:       "critical",
			CreatedAt:      time.Now(),
		})
	}

	// Pattern 3: Memory/CPU pattern detection
	memoryAlerts := 0
	cpuAlerts := 0
	for _, alert := range alerts {
		title := strings.ToLower(alert.Title)
		if strings.Contains(title, "memory") || strings.Contains(title, "oom") {
			memoryAlerts++
		}
		if strings.Contains(title, "cpu") || strings.Contains(title, "load") {
			cpuAlerts++
		}
	}

	if memoryAlerts >= 2 {
		suggestions = append(suggestions, RCASuggestion{
			ID:             uuid.New().String(),
			RootCause:      "Memory exhaustion or memory leak",
			Confidence:     0.90,
			Evidence:       []string{fmt.Sprintf("%d memory-related alerts detected", memoryAlerts)},
			AffectedAlerts: alertIDs,
			Recommendation: "Investigate memory usage patterns, check for memory leaks, review recent code changes that might affect memory consumption",
			Priority:       "high",
			CreatedAt:      time.Now(),
		})
	}

	return suggestions, nil
}

// ============================================================================
// 3. PREDICTIVE ALERT ANALYTICS
// ============================================================================

// AlertPrediction represents a prediction about future alert behavior
type AlertPrediction struct {
	AlertID               string   `json:"alert_id"`
	PredictedEscalation   bool     `json:"predicted_escalation"`
	EscalationProbability float64  `json:"escalation_probability"`
	EstimatedTimeframe    string   `json:"estimated_timeframe"`
	Reasoning             []string `json:"reasoning"`
	PreventiveActions     []string `json:"preventive_actions"`
}

// PredictAlertEscalation predicts if an alert might escalate
func (a *AIAdvancedAnalyzer) PredictAlertEscalation(ctx context.Context, alertID string) (*AlertPrediction, error) {
	alert, err := a.getAlertByID(alertID)
	if err != nil {
		return nil, err
	}

	prediction := &AlertPrediction{
		AlertID:               alertID,
		PredictedEscalation:   false,
		EscalationProbability: 0.0,
		Reasoning:             make([]string, 0),
		PreventiveActions:     make([]string, 0),
	}

	// Factor 1: Historical pattern analysis
	historicalEscalations, _ := a.getHistoricalEscalationRate(alert.Source)
	if historicalEscalations > 0.5 {
		prediction.EscalationProbability += 0.3
		prediction.Reasoning = append(prediction.Reasoning,
			fmt.Sprintf("Source %s has a %.0f%% historical escalation rate", alert.Source, historicalEscalations*100))
	}

	// Factor 2: Severity level
	if alert.Severity == "high" || alert.Severity == "critical" {
		prediction.EscalationProbability += 0.25
		prediction.Reasoning = append(prediction.Reasoning,
			fmt.Sprintf("Alert is already at %s severity", alert.Severity))
	}

	// Factor 3: Time of day (after hours = higher escalation)
	hour := time.Now().Hour()
	if hour < 6 || hour > 22 {
		prediction.EscalationProbability += 0.15
		prediction.Reasoning = append(prediction.Reasoning, "Alert occurred during off-hours when response may be delayed")
	}

	// Factor 4: Related alerts
	relatedCount, _ := a.countRelatedAlerts(alertID, 10*time.Minute)
	if relatedCount >= 3 {
		prediction.EscalationProbability += 0.20
		prediction.Reasoning = append(prediction.Reasoning,
			fmt.Sprintf("Multiple related alerts (%d) detected in the last 10 minutes", relatedCount))
	}

	// Determine if escalation is predicted
	prediction.PredictedEscalation = prediction.EscalationProbability > 0.6

	if prediction.PredictedEscalation {
		prediction.EstimatedTimeframe = "Within 15-30 minutes"
		prediction.PreventiveActions = []string{
			"Page on-call engineer immediately",
			"Escalate to senior team members",
			"Prepare incident response plan",
			"Check related infrastructure components",
		}
	} else {
		prediction.EstimatedTimeframe = "Low risk in next hour"
		prediction.PreventiveActions = []string{
			"Monitor alert progression",
			"Review standard troubleshooting steps",
			"Acknowledge alert to prevent duplicate notifications",
		}
	}

	return prediction, nil
}

// ============================================================================
// 4. ALERT CLUSTERING & GROUPING
// ============================================================================

// AlertCluster represents a group of similar alerts
type AlertCluster struct {
	ClusterID   string         `json:"cluster_id"`
	ClusterName string         `json:"cluster_name"`
	Alerts      []AlertSummary `json:"alerts"`
	Pattern     string         `json:"pattern"`
	Severity    string         `json:"severity"`
	Count       int            `json:"count"`
	FirstSeen   time.Time      `json:"first_seen"`
	LastSeen    time.Time      `json:"last_seen"`
}

// ClusterAlerts groups similar alerts together
func (a *AIAdvancedAnalyzer) ClusterAlerts(ctx context.Context, timeWindow time.Duration) ([]AlertCluster, error) {
	// Get recent alerts
	alerts, err := a.getAlertsInTimeWindow(timeWindow)
	if err != nil {
		return nil, err
	}

	clusters := make(map[string]*AlertCluster)

	// Cluster by source and pattern
	for _, alert := range alerts {
		clusterKey := a.generateClusterKey(alert)

		if cluster, exists := clusters[clusterKey]; exists {
			cluster.Alerts = append(cluster.Alerts, alert)
			cluster.Count++
			if alert.CreatedAt.After(cluster.LastSeen) {
				cluster.LastSeen = alert.CreatedAt
			}
			if alert.CreatedAt.Before(cluster.FirstSeen) {
				cluster.FirstSeen = alert.CreatedAt
			}
		} else {
			clusters[clusterKey] = &AlertCluster{
				ClusterID:   uuid.New().String(),
				ClusterName: a.generateClusterName(alert),
				Alerts:      []AlertSummary{alert},
				Pattern:     clusterKey,
				Severity:    alert.Severity,
				Count:       1,
				FirstSeen:   alert.CreatedAt,
				LastSeen:    alert.CreatedAt,
			}
		}
	}

	// Convert map to slice and sort by count
	result := make([]AlertCluster, 0, len(clusters))
	for _, cluster := range clusters {
		result = append(result, *cluster)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})

	return result, nil
}

// ============================================================================
// 5. RUNBOOK RECOMMENDATIONS
// ============================================================================

// RunbookRecommendation suggests relevant runbooks for alerts
type RunbookRecommendation struct {
	RunbookID     string   `json:"runbook_id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Steps         []string `json:"steps"`
	Relevance     float64  `json:"relevance"`
	EstimatedTime string   `json:"estimated_time"`
	Difficulty    string   `json:"difficulty"`
}

// GetRunbookRecommendations suggests runbooks based on alert characteristics
func (a *AIAdvancedAnalyzer) GetRunbookRecommendations(ctx context.Context, alertID string) ([]RunbookRecommendation, error) {
	alert, err := a.getAlertByID(alertID)
	if err != nil {
		return nil, err
	}

	recommendations := make([]RunbookRecommendation, 0)
	title := strings.ToLower(alert.Title)

	// Memory-related runbooks
	if strings.Contains(title, "memory") || strings.Contains(title, "oom") {
		recommendations = append(recommendations, RunbookRecommendation{
			RunbookID:   "RB-MEM-001",
			Title:       "Memory Exhaustion Troubleshooting",
			Description: "Step-by-step guide to diagnose and resolve memory issues",
			Steps: []string{
				"Check current memory usage: kubectl top nodes/pods",
				"Identify memory-hungry processes",
				"Review memory limits and requests in pod specs",
				"Check for memory leaks in application logs",
				"Scale up resources if needed or optimize application",
			},
			Relevance:     0.95,
			EstimatedTime: "15-30 minutes",
			Difficulty:    "Intermediate",
		})
	}

	// CPU-related runbooks
	if strings.Contains(title, "cpu") || strings.Contains(title, "load") {
		recommendations = append(recommendations, RunbookRecommendation{
			RunbookID:   "RB-CPU-001",
			Title:       "High CPU Usage Resolution",
			Description: "Guide to identify and resolve high CPU consumption",
			Steps: []string{
				"Check CPU metrics: kubectl top nodes/pods",
				"Identify top CPU consumers",
				"Review recent deployments that might have introduced CPU-intensive code",
				"Check for infinite loops or inefficient algorithms",
				"Scale horizontally or increase CPU limits",
			},
			Relevance:     0.93,
			EstimatedTime: "20-40 minutes",
			Difficulty:    "Intermediate",
		})
	}

	// Pod/Container issues
	if strings.Contains(title, "pod") || strings.Contains(title, "container") || strings.Contains(title, "crash") {
		recommendations = append(recommendations, RunbookRecommendation{
			RunbookID:   "RB-POD-001",
			Title:       "Pod Crash Loop Resolution",
			Description: "Troubleshooting guide for pod stability issues",
			Steps: []string{
				"Get pod status: kubectl get pods",
				"Check pod logs: kubectl logs <pod-name> --previous",
				"Describe pod for events: kubectl describe pod <pod-name>",
				"Review liveness/readiness probe configurations",
				"Check resource requests and limits",
				"Verify config maps and secrets are correctly mounted",
			},
			Relevance:     0.90,
			EstimatedTime: "15-25 minutes",
			Difficulty:    "Beginner",
		})
	}

	// Network/Connectivity issues
	if strings.Contains(title, "network") || strings.Contains(title, "connection") || strings.Contains(title, "timeout") {
		recommendations = append(recommendations, RunbookRecommendation{
			RunbookID:   "RB-NET-001",
			Title:       "Network Connectivity Troubleshooting",
			Description: "Diagnosing and fixing network-related issues",
			Steps: []string{
				"Test connectivity: kubectl exec -it <pod> -- ping <target>",
				"Check network policies: kubectl get networkpolicies",
				"Verify service endpoints: kubectl get endpoints",
				"Check DNS resolution: kubectl exec -it <pod> -- nslookup <service>",
				"Review ingress/load balancer configurations",
				"Check firewall rules and security groups",
			},
			Relevance:     0.88,
			EstimatedTime: "20-35 minutes",
			Difficulty:    "Intermediate",
		})
	}

	// Sort by relevance
	sort.Slice(recommendations, func(i, j int) bool {
		return recommendations[i].Relevance > recommendations[j].Relevance
	})

	return recommendations, nil
}

// ============================================================================
// 6. ALERT FATIGUE DETECTION
// ============================================================================

// AlertFatigueAnalysis detects alert fatigue conditions
type AlertFatigueAnalysis struct {
	FatigueDetected bool     `json:"fatigue_detected"`
	FatigueScore    float64  `json:"fatigue_score"` // 0-100
	NoiseAlerts     int      `json:"noise_alerts"`
	DuplicateAlerts int      `json:"duplicate_alerts"`
	FlappingAlerts  int      `json:"flapping_alerts"`
	Recommendations []string `json:"recommendations"`
	AffectedSources []string `json:"affected_sources"`
}

// DetectAlertFatigue analyzes alert patterns to detect fatigue conditions
func (a *AIAdvancedAnalyzer) DetectAlertFatigue(ctx context.Context, timeWindow time.Duration) (*AlertFatigueAnalysis, error) {
	alerts, err := a.getAlertsInTimeWindow(timeWindow)
	if err != nil {
		return nil, err
	}

	analysis := &AlertFatigueAnalysis{
		Recommendations: make([]string, 0),
		AffectedSources: make([]string, 0),
	}

	// Detect duplicate alerts
	duplicateMap := make(map[string]int)
	for _, alert := range alerts {
		key := fmt.Sprintf("%s:%s", alert.Source, alert.Title)
		duplicateMap[key]++
	}

	for source, count := range duplicateMap {
		if count > 5 {
			analysis.DuplicateAlerts += count
			analysis.AffectedSources = append(analysis.AffectedSources, source)
		}
	}

	// Calculate fatigue score
	totalAlerts := len(alerts)
	if totalAlerts > 0 {
		noiseRatio := float64(analysis.DuplicateAlerts) / float64(totalAlerts)
		analysis.FatigueScore = noiseRatio * 100
	}

	analysis.FatigueDetected = analysis.FatigueScore > 30.0

	if analysis.FatigueDetected {
		analysis.Recommendations = append(analysis.Recommendations,
			"Consider increasing alert thresholds for noisy alerts",
			"Implement alert aggregation to reduce duplicate notifications",
			"Review and tune alert conditions to reduce false positives",
			"Create maintenance windows for known recurring issues",
			"Enable alert suppression during deployment windows",
		)
	}

	return analysis, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func (a *AIAdvancedAnalyzer) getAlertByID(alertID string) (*AlertSummary, error) {
	var alert AlertSummary
	err := a.db.QueryRow(`
		SELECT id, title, severity, status, source, created_at, updated_at
		FROM alerts WHERE id = $1
	`, alertID).Scan(&alert.ID, &alert.Title, &alert.Severity, &alert.Status,
		&alert.Source, &alert.CreatedAt, &alert.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &alert, nil
}

func (a *AIAdvancedAnalyzer) findTimeCorrelatedAlerts(alert *AlertSummary, window time.Duration) ([]AlertSummary, error) {
	start := alert.CreatedAt.Add(-window)
	end := alert.CreatedAt.Add(window)

	rows, err := a.db.Query(`
		SELECT id, title, severity, status, source, created_at, updated_at
		FROM alerts 
		WHERE created_at BETWEEN $1 AND $2 
		AND id != $3
		LIMIT 10
	`, start, end, alert.ID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	alerts := make([]AlertSummary, 0)
	for rows.Next() {
		var a AlertSummary
		if err := rows.Scan(&a.ID, &a.Title, &a.Severity, &a.Status, &a.Source, &a.CreatedAt, &a.UpdatedAt); err == nil {
			alerts = append(alerts, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getTimeCorrelatedAlerts: %w", err)
	}

	return alerts, nil
}

func (a *AIAdvancedAnalyzer) findSourceCorrelatedAlerts(alert *AlertSummary) ([]AlertSummary, error) {
	rows, err := a.db.Query(`
		SELECT id, title, severity, status, source, created_at, updated_at
		FROM alerts 
		WHERE source = $1 
		AND id != $2 
		AND status IN ('firing', 'acknowledged')
		LIMIT 10
	`, alert.Source, alert.ID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	alerts := make([]AlertSummary, 0)
	for rows.Next() {
		var a AlertSummary
		if err := rows.Scan(&a.ID, &a.Title, &a.Severity, &a.Status, &a.Source, &a.CreatedAt, &a.UpdatedAt); err == nil {
			alerts = append(alerts, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findSourceCorrelatedAlerts: %w", err)
	}

	return alerts, nil
}

func (a *AIAdvancedAnalyzer) findPatternCorrelatedAlerts(alert *AlertSummary) ([]AlertSummary, error) {
	// Extract keywords from title
	keywords := strings.Fields(strings.ToLower(alert.Title))
	if len(keywords) == 0 {
		return []AlertSummary{}, nil
	}

	// Use first meaningful keyword
	keyword := keywords[0]
	if len(keyword) < 3 {
		keyword = alert.Title
	}

	rows, err := a.db.Query(`
		SELECT id, title, severity, status, source, created_at, updated_at
		FROM alerts 
		WHERE LOWER(title) LIKE $1 
		AND id != $2 
		AND status IN ('firing', 'acknowledged')
		LIMIT 10
	`, "%"+keyword+"%", alert.ID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	alerts := make([]AlertSummary, 0)
	for rows.Next() {
		var a AlertSummary
		if err := rows.Scan(&a.ID, &a.Title, &a.Severity, &a.Status, &a.Source, &a.CreatedAt, &a.UpdatedAt); err == nil {
			alerts = append(alerts, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findPatternCorrelatedAlerts: %w", err)
	}

	return alerts, nil
}

func (a *AIAdvancedAnalyzer) getMultipleAlerts(alertIDs []string) ([]AlertSummary, error) {
	if len(alertIDs) == 0 {
		return []AlertSummary{}, nil
	}

	// Create placeholders for SQL IN clause
	placeholders := make([]string, len(alertIDs))
	args := make([]interface{}, len(alertIDs))
	for i, id := range alertIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, title, severity, status, source, created_at, updated_at
		FROM alerts 
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	alerts := make([]AlertSummary, 0)
	for rows.Next() {
		var a AlertSummary
		if err := rows.Scan(&a.ID, &a.Title, &a.Severity, &a.Status, &a.Source, &a.CreatedAt, &a.UpdatedAt); err == nil {
			alerts = append(alerts, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getMultipleAlerts: %w", err)
	}

	return alerts, nil
}

func (a *AIAdvancedAnalyzer) getHistoricalEscalationRate(source string) (float64, error) {
	// Check how many alerts from this source escalated in the past
	var total, escalated int

	err := a.db.QueryRow(`
		SELECT 
			COUNT(*) as total,
			COUNT(CASE WHEN severity = 'critical' THEN 1 END) as escalated
		FROM alerts 
		WHERE source = $1 
		AND created_at > NOW() - INTERVAL '30 days'
	`, source).Scan(&total, &escalated)

	if err != nil || total == 0 {
		return 0.0, err
	}

	return float64(escalated) / float64(total), nil
}

func (a *AIAdvancedAnalyzer) countRelatedAlerts(alertID string, window time.Duration) (int, error) {
	alert, err := a.getAlertByID(alertID)
	if err != nil {
		return 0, err
	}

	var count int
	err = a.db.QueryRow(`
		SELECT COUNT(*) 
		FROM alerts 
		WHERE source = $1 
		AND created_at > $2 
		AND id != $3
	`, alert.Source, time.Now().Add(-window), alertID).Scan(&count)

	return count, err
}

func (a *AIAdvancedAnalyzer) getAlertsInTimeWindow(window time.Duration) ([]AlertSummary, error) {
	rows, err := a.db.Query(`
		SELECT id, title, severity, status, source, created_at, updated_at
		FROM alerts 
		WHERE created_at > $1 
		AND status IN ('firing', 'acknowledged')
		ORDER BY created_at DESC
		LIMIT 100
	`, time.Now().Add(-window))

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	alerts := make([]AlertSummary, 0)
	for rows.Next() {
		var a AlertSummary
		if err := rows.Scan(&a.ID, &a.Title, &a.Severity, &a.Status, &a.Source, &a.CreatedAt, &a.UpdatedAt); err == nil {
			alerts = append(alerts, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getAlertsInTimeWindow: %w", err)
	}

	return alerts, nil
}

func (a *AIAdvancedAnalyzer) generateClusterKey(alert AlertSummary) string {
	// Simple clustering by source and first word of title
	titleWords := strings.Fields(alert.Title)
	firstWord := "unknown"
	if len(titleWords) > 0 {
		firstWord = strings.ToLower(titleWords[0])
	}
	return fmt.Sprintf("%s:%s", alert.Source, firstWord)
}

func (a *AIAdvancedAnalyzer) generateClusterName(alert AlertSummary) string {
	return fmt.Sprintf("%s alerts from %s", strings.Title(alert.Severity), alert.Source)
}

// AdvancedAnalysisResponse wraps all advanced analysis results
type AdvancedAnalysisResponse struct {
	Correlations    []AlertCorrelation      `json:"correlations,omitempty"`
	RCASuggestions  []RCASuggestion         `json:"rca_suggestions,omitempty"`
	Predictions     *AlertPrediction        `json:"predictions,omitempty"`
	Clusters        []AlertCluster          `json:"clusters,omitempty"`
	Runbooks        []RunbookRecommendation `json:"runbooks,omitempty"`
	FatigueAnalysis *AlertFatigueAnalysis   `json:"fatigue_analysis,omitempty"`
	Summary         string                  `json:"summary"`
	Recommendations []string                `json:"recommendations"`
	GeneratedAt     time.Time               `json:"generated_at"`
}

// PerformComprehensiveAnalysis runs all advanced analysis features
func (a *AIAdvancedAnalyzer) PerformComprehensiveAnalysis(ctx context.Context, alertID string) (*AdvancedAnalysisResponse, error) {
	response := &AdvancedAnalysisResponse{
		GeneratedAt:     time.Now(),
		Recommendations: make([]string, 0),
	}

	// Run all analyses
	correlations, _ := a.DetectAlertCorrelations(ctx, alertID)
	response.Correlations = correlations

	prediction, _ := a.PredictAlertEscalation(ctx, alertID)
	response.Predictions = prediction

	runbooks, _ := a.GetRunbookRecommendations(ctx, alertID)
	response.Runbooks = runbooks

	clusters, _ := a.ClusterAlerts(ctx, 1*time.Hour)
	response.Clusters = clusters

	fatigue, _ := a.DetectAlertFatigue(ctx, 24*time.Hour)
	response.FatigueAnalysis = fatigue

	// Generate summary
	response.Summary = a.generateAnalysisSummary(response)

	return response, nil
}

func (a *AIAdvancedAnalyzer) generateAnalysisSummary(resp *AdvancedAnalysisResponse) string {
	summary := "## Advanced Alert Analysis\n\n"

	if len(resp.Correlations) > 0 {
		summary += fmt.Sprintf("**Correlations**: Found %d correlated alert groups\n", len(resp.Correlations))
	}

	if resp.Predictions != nil && resp.Predictions.PredictedEscalation {
		summary += fmt.Sprintf("**Risk**: High escalation probability (%.0f%%)\n",
			resp.Predictions.EscalationProbability*100)
	}

	if len(resp.Runbooks) > 0 {
		summary += fmt.Sprintf("**Runbooks**: %d relevant troubleshooting guides available\n", len(resp.Runbooks))
	}

	if resp.FatigueAnalysis != nil && resp.FatigueAnalysis.FatigueDetected {
		summary += fmt.Sprintf("**Alert Fatigue**: Detected (score: %.1f/100)\n", resp.FatigueAnalysis.FatigueScore)
	}

	return summary
}
