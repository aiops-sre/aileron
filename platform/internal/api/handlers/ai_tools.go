package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AITool represents an AI-executable tool with security controls
type AITool struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	RequiredPerms []string `json:"required_permissions"`
	Category      string   `json:"category"`
}

// AIToolResult represents the result of tool execution
type AIToolResult struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	AuditID   string      `json:"audit_id,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// AIToolExecutor handles secure tool execution
type AIToolExecutor struct {
	db     *sql.DB
	userID string
	perms  map[string]bool
}

// NewAIToolExecutor creates a new tool executor with user context
func NewAIToolExecutor(db *sql.DB, userID string) *AIToolExecutor {
	executor := &AIToolExecutor{
		db:     db,
		userID: userID,
		perms:  make(map[string]bool),
	}
	executor.loadUserPermissions()
	return executor
}

// loadUserPermissions loads user's permissions from database
func (e *AIToolExecutor) loadUserPermissions() {
	rows, err := e.db.Query(`
		SELECT DISTINCT p.name
		FROM users u
		JOIN user_roles ur ON u.id = ur.user_id
		JOIN role_permissions rp ON ur.role_id = rp.role_id
		JOIN permissions p ON rp.permission_id = p.id
		WHERE u.id = $1
	`, e.userID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var perm string
		if err := rows.Scan(&perm); err == nil {
			e.perms[perm] = true
		}
	}
}

// HasPermission checks if user has required permission
func (e *AIToolExecutor) HasPermission(perm string) bool {
	return e.perms[perm] || e.perms["admin"]
}

// GetAvailableTools returns tools available to the user
func (e *AIToolExecutor) GetAvailableTools() []AITool {
	allTools := []AITool{
		{
			Name:          "acknowledge_alert",
			Description:   "Acknowledge one or more alerts to indicate you're working on them",
			RequiredPerms: []string{"alert:update"},
			Category:      "Alert Management",
		},
		{
			Name:          "resolve_alert",
			Description:   "Mark alerts as resolved after fixing the underlying issue",
			RequiredPerms: []string{"alert:update"},
			Category:      "Alert Management",
		},
		{
			Name:          "assign_alert",
			Description:   "Assign alerts to specific team members for investigation",
			RequiredPerms: []string{"alert:update"},
			Category:      "Alert Management",
		},
		{
			Name:          "create_incident",
			Description:   "Create an incident from related alerts",
			RequiredPerms: []string{"incident:create"},
			Category:      "Incident Management",
		},
		{
			Name:          "correlate_alerts",
			Description:   "Find related alerts that might indicate a larger issue",
			RequiredPerms: []string{"alert:read"},
			Category:      "Analytics",
		},
		{
			Name:          "get_alert_trends",
			Description:   "Analyze alert patterns and trends over time",
			RequiredPerms: []string{"alert:read"},
			Category:      "Analytics",
		},
		{
			Name:          "predict_incidents",
			Description:   "Predict potential incidents based on current alert patterns",
			RequiredPerms: []string{"alert:read"},
			Category:      "Analytics",
		},
		{
			Name:          "get_runbook",
			Description:   "Retrieve remediation runbook for specific alert types",
			RequiredPerms: []string{"alert:read"},
			Category:      "Knowledge Base",
		},
		{
			Name:          "calculate_impact",
			Description:   "Assess the business impact of current alerts",
			RequiredPerms: []string{"alert:read"},
			Category:      "Analytics",
		},
		{
			Name:          "get_oncall_schedule",
			Description:   "View current on-call rotation and escalation contacts",
			RequiredPerms: []string{"oncall:read"},
			Category:      "Team Management",
		},
		{
			Name:          "execute_remediation",
			Description:   "Execute automated remediation for known issues (requires approval)",
			RequiredPerms: []string{"remediation:execute", "admin"},
			Category:      "Automation",
		},
		{
			Name:          "get_alert_history",
			Description:   "View historical data for similar alerts",
			RequiredPerms: []string{"alert:read"},
			Category:      "Analytics",
		},
	}

	// Filter tools based on user permissions
	availableTools := make([]AITool, 0)
	for _, tool := range allTools {
		hasAllPerms := true
		for _, perm := range tool.RequiredPerms {
			if !e.HasPermission(perm) {
				hasAllPerms = false
				break
			}
		}
		if hasAllPerms {
			availableTools = append(availableTools, tool)
		}
	}

	return availableTools
}

// ExecuteTool executes a tool with security and audit logging
func (e *AIToolExecutor) ExecuteTool(ctx context.Context, toolName string, params map[string]interface{}) *AIToolResult {
	result := &AIToolResult{
		Timestamp: time.Now(),
	}

	// Audit log the attempt
	auditID := e.auditToolExecution(toolName, params, "attempted")
	result.AuditID = auditID

	// Execute based on tool name
	switch toolName {
	case "acknowledge_alert":
		result = e.acknowledgeAlert(params)
	case "resolve_alert":
		result = e.resolveAlert(params)
	case "assign_alert":
		result = e.assignAlert(params)
	case "create_incident":
		result = e.createIncident(params)
	case "correlate_alerts":
		result = e.correlateAlerts(params)
	case "get_alert_trends":
		result = e.getAlertTrends(params)
	case "predict_incidents":
		result = e.predictIncidents(params)
	case "get_runbook":
		result = e.getRunbook(params)
	case "calculate_impact":
		result = e.calculateImpact(params)
	case "get_oncall_schedule":
		result = e.getOnCallSchedule(params)
	case "execute_remediation":
		result = e.executeRemediation(params)
	case "get_alert_history":
		result = e.getAlertHistory(params)
	default:
		result.Success = false
		result.Error = fmt.Sprintf("Unknown tool: %s", toolName)
	}

	// Audit log the result
	status := "failed"
	if result.Success {
		status = "completed"
	}
	e.auditToolExecution(toolName, params, status)

	return result
}

func (e *AIToolExecutor) auditToolExecution(toolName string, params map[string]interface{}, status string) string {
	auditID := uuid.New().String()
	paramsJSON, _ := json.Marshal(params)

	_, err := e.db.Exec(`
		INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`, auditID, e.userID, fmt.Sprintf("ai_tool_%s", status), "ai_tool", toolName, string(paramsJSON))

	if err != nil {
		// Log error but don't fail
		return ""
	}
	return auditID
}

func (e *AIToolExecutor) acknowledgeAlert(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:update") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:update required",
			Timestamp: time.Now(),
		}
	}

	alertIDs, ok := params["alert_ids"].([]interface{})
	if !ok {
		return &AIToolResult{
			Success:   false,
			Error:     "Invalid alert_ids parameter",
			Timestamp: time.Now(),
		}
	}

	acknowledgedCount := 0
	for _, alertID := range alertIDs {
		_, err := e.db.Exec(`
			UPDATE alerts 
			SET status = 'acknowledged', 
			    acknowledged_by = $1,
			    acknowledged_at = NOW(),
			    updated_at = NOW()
			WHERE id = $2 AND status = 'firing'
		`, e.userID, alertID)
		if err == nil {
			acknowledgedCount++
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"acknowledged_count": acknowledgedCount,
			"alert_ids":          alertIDs,
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) resolveAlert(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:update") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:update required",
			Timestamp: time.Now(),
		}
	}

	alertIDs, ok := params["alert_ids"].([]interface{})
	if !ok {
		return &AIToolResult{
			Success:   false,
			Error:     "Invalid alert_ids parameter",
			Timestamp: time.Now(),
		}
	}

	resolution := ""
	if resParam, ok := params["resolution"].(string); ok {
		resolution = resParam
	}

	resolvedCount := 0
	for _, alertID := range alertIDs {
		_, err := e.db.Exec(`
			UPDATE alerts 
			SET status = 'resolved',
			    resolved_by = $1,
			    resolved_at = NOW(),
			    resolution = $2,
			    updated_at = NOW()
			WHERE id = $3 AND status IN ('firing', 'acknowledged')
		`, e.userID, resolution, alertID)
		if err == nil {
			resolvedCount++
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"resolved_count": resolvedCount,
			"alert_ids":      alertIDs,
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) assignAlert(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:update") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:update required",
			Timestamp: time.Now(),
		}
	}

	alertID, _ := params["alert_id"].(string)
	assigneeID, _ := params["assignee_id"].(string)

	_, err := e.db.Exec(`
		UPDATE alerts 
		SET assigned_to = $1, updated_at = NOW()
		WHERE id = $2
	`, assigneeID, alertID)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to assign alert: %v", err),
			Timestamp: time.Now(),
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"alert_id":    alertID,
			"assignee_id": assigneeID,
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) createIncident(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("incident:create") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: incident:create required",
			Timestamp: time.Now(),
		}
	}

	title, _ := params["title"].(string)
	description, _ := params["description"].(string)
	severity, _ := params["severity"].(string)

	incidentID := uuid.New().String()
	_, err := e.db.Exec(`
		INSERT INTO incidents (id, title, description, severity, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'investigating', $5, NOW(), NOW())
	`, incidentID, title, description, severity, e.userID)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to create incident: %v", err),
			Timestamp: time.Now(),
		}
	}

	// Link alerts to incident if provided
	if alertIDs, ok := params["alert_ids"].([]interface{}); ok {
		for _, alertID := range alertIDs {
			if _, err := e.db.Exec(`UPDATE alerts SET incident_id = $1 WHERE id = $2`, incidentID, alertID); err != nil {
				log.Printf("ai_tools: failed to link alert %v to incident %s: %v", alertID, incidentID, err)
			}
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"incident_id": incidentID,
			"title":       title,
			"severity":    severity,
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) correlateAlerts(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:read required",
			Timestamp: time.Now(),
		}
	}

	timeWindow := "1 hour"
	if tw, ok := params["time_window"].(string); ok {
		timeWindow = tw
	}

	rows, err := e.db.Query(`
		WITH alert_groups AS (
			SELECT 
				a1.id as alert1_id,
				a1.title as alert1_title,
				a2.id as alert2_id,
				a2.title as alert2_title,
				a1.source,
				a1.severity,
				EXTRACT(EPOCH FROM (a2.created_at - a1.created_at)) as time_diff
			FROM alerts a1
			JOIN alerts a2 ON a1.source = a2.source 
				AND a1.id != a2.id
				AND a2.created_at BETWEEN a1.created_at - INTERVAL $1 AND a1.created_at + INTERVAL $1
			WHERE a1.status IN ('firing', 'acknowledged')
				AND a2.status IN ('firing', 'acknowledged')
		)
		SELECT source, severity, COUNT(*) as correlation_count, 
		       array_agg(DISTINCT alert1_id) as alert_ids
		FROM alert_groups
		GROUP BY source, severity
		HAVING COUNT(*) >= 2
		ORDER BY correlation_count DESC
		LIMIT 10
	`, timeWindow)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to correlate alerts: %v", err),
			Timestamp: time.Now(),
		}
	}
	defer rows.Close()

	correlations := make([]map[string]interface{}, 0)
	for rows.Next() {
		var source, severity string
		var count int
		var alertIDs string
		if err := rows.Scan(&source, &severity, &count, &alertIDs); err == nil {
			correlations = append(correlations, map[string]interface{}{
				"source":    source,
				"severity":  severity,
				"count":     count,
				"alert_ids": alertIDs,
			})
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"correlations": correlations,
			"time_window":  timeWindow,
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) getAlertTrends(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:read required",
			Timestamp: time.Now(),
		}
	}

	days := 7
	if d, ok := params["days"].(float64); ok {
		days = int(d)
	}

	rows, err := e.db.Query(`
		SELECT 
			DATE(created_at) as date,
			severity,
			COUNT(*) as count
		FROM alerts
		WHERE created_at >= NOW() - ($1 * INTERVAL '1 day')
		GROUP BY DATE(created_at), severity
		ORDER BY date DESC, severity
		LIMIT 1000
	`, days)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get alert trends: %v", err),
			Timestamp: time.Now(),
		}
	}
	defer rows.Close()

	trends := make([]map[string]interface{}, 0)
	for rows.Next() {
		var date time.Time
		var severity string
		var count int
		if err := rows.Scan(&date, &severity, &count); err == nil {
			trends = append(trends, map[string]interface{}{
				"date":     date.Format("2006-01-02"),
				"severity": severity,
				"count":    count,
			})
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"trends": trends,
			"period": fmt.Sprintf("%d days", days),
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) predictIncidents(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:read required",
			Timestamp: time.Now(),
		}
	}

	// Simplified prediction based on alert velocity and patterns
	rows, err := e.db.Query(`
		SELECT 
			source,
			severity,
			COUNT(*) as alert_count,
			COUNT(*) FILTER (WHERE created_at >= NOW() - INTERVAL '1 hour') as recent_count,
			MIN(created_at) as first_alert,
			MAX(created_at) as last_alert
		FROM alerts
		WHERE status IN ('firing', 'acknowledged')
			AND created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY source, severity
		HAVING COUNT(*) >= 3
		ORDER BY recent_count DESC, alert_count DESC
		LIMIT 5
	`)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to predict incidents: %v", err),
			Timestamp: time.Now(),
		}
	}
	defer rows.Close()

	predictions := make([]map[string]interface{}, 0)
	for rows.Next() {
		var source, severity string
		var alertCount, recentCount int
		var firstAlert, lastAlert time.Time
		if err := rows.Scan(&source, &severity, &alertCount, &recentCount, &firstAlert, &lastAlert); err == nil {
			// Calculate risk score
			riskScore := float64(recentCount) * 2.0
			if severity == "critical" {
				riskScore *= 3.0
			} else if severity == "high" {
				riskScore *= 2.0
			}

			predictions = append(predictions, map[string]interface{}{
				"source":         source,
				"severity":       severity,
				"alert_count":    alertCount,
				"recent_count":   recentCount,
				"risk_score":     riskScore,
				"likelihood":     getRiskLevel(riskScore),
				"recommendation": fmt.Sprintf("Monitor %s alerts closely - showing escalating pattern", source),
			})
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"predictions": predictions,
			"analysis":    "Based on alert velocity and pattern analysis",
		},
		Timestamp: time.Now(),
	}
}

func getRiskLevel(score float64) string {
	if score >= 15 {
		return "High"
	} else if score >= 8 {
		return "Medium"
	}
	return "Low"
}

func (e *AIToolExecutor) getRunbook(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:read required",
			Timestamp: time.Now(),
		}
	}

	alertType, _ := params["alert_type"].(string)

	// Mock runbook data - in production, this would query a runbooks database
	runbooks := map[string]map[string]interface{}{
		"cpu_high": {
			"title": "High CPU Usage Remediation",
			"steps": []string{
				"1. Check top processes: `top -o %CPU`",
				"2. Identify resource-intensive processes",
				"3. Check for memory leaks or infinite loops",
				"4. Scale horizontally if legitimate load",
				"5. Restart affected services if necessary",
			},
			"escalation": "Contact SRE team if issue persists > 15 minutes",
		},
		"memory_leak": {
			"title": "Memory Leak Investigation",
			"steps": []string{
				"1. Monitor memory usage: `free -m` or `top`",
				"2. Check application logs for OOM errors",
				"3. Analyze heap dumps if available",
				"4. Review recent code deployments",
				"5. Restart service with memory profiling enabled",
			},
			"escalation": "Page application team if service degraded",
		},
		"disk_space": {
			"title": "Disk Space Management",
			"steps": []string{
				"1. Check disk usage: `df -h`",
				"2. Find large files: `du -sh /* | sort -hr | head -10`",
				"3. Clean up logs: check /var/log",
				"4. Remove old backups if safe",
				"5. Expand volume if cleanup insufficient",
			},
			"escalation": "Infrastructure team for volume expansion",
		},
	}

	// Match alert type to runbook
	var runbook map[string]interface{}
	for key, rb := range runbooks {
		if strings.Contains(strings.ToLower(alertType), key) {
			runbook = rb
			break
		}
	}

	if runbook == nil {
		runbook = map[string]interface{}{
			"title": "General Alert Response",
			"steps": []string{
				"1. Acknowledge the alert",
				"2. Review alert details and metrics",
				"3. Check related services and dependencies",
				"4. Investigate root cause",
				"5. Apply appropriate remediation",
				"6. Document findings",
			},
			"escalation": "Follow standard escalation policy",
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"runbook":    runbook,
			"alert_type": alertType,
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) calculateImpact(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:read required",
			Timestamp: time.Now(),
		}
	}

	// Calculate business impact based on alert data
	var totalAlerts, criticalAlerts, affectedServices int
	err := e.db.QueryRow(`
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE severity = 'critical') as critical,
			COUNT(DISTINCT source) as affected_services
		FROM alerts
		WHERE status IN ('firing', 'acknowledged')
	`).Scan(&totalAlerts, &criticalAlerts, &affectedServices)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to calculate impact: %v", err),
			Timestamp: time.Now(),
		}
	}

	impactLevel := "Low"
	if criticalAlerts >= 5 || affectedServices >= 3 {
		impactLevel = "High"
	} else if criticalAlerts >= 2 || affectedServices >= 2 {
		impactLevel = "Medium"
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"total_alerts":      totalAlerts,
			"critical_alerts":   criticalAlerts,
			"affected_services": affectedServices,
			"impact_level":      impactLevel,
			"recommendation":    getImpactRecommendation(impactLevel, criticalAlerts),
		},
		Timestamp: time.Now(),
	}
}

func getImpactRecommendation(level string, critical int) string {
	switch level {
	case "High":
		return fmt.Sprintf("URGENT: %d critical alerts affecting multiple services. Consider declaring incident.", critical)
	case "Medium":
		return "Moderate impact detected. Monitor closely and prepare for escalation."
	default:
		return "Low impact. Continue normal monitoring procedures."
	}
}

func (e *AIToolExecutor) getOnCallSchedule(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("oncall:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: oncall:read required",
			Timestamp: time.Now(),
		}
	}

	rows, err := e.db.Query(`
		SELECT u.id, u.name, u.email, os.role, os.start_time, os.end_time
		FROM oncall_schedules os
		JOIN users u ON os.user_id = u.id
		WHERE os.start_time <= NOW() AND os.end_time >= NOW()
		ORDER BY os.priority ASC
	`)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get on-call schedule: %v", err),
			Timestamp: time.Now(),
		}
	}
	defer rows.Close()

	schedule := make([]map[string]interface{}, 0)
	for rows.Next() {
		var userID, name, email, role string
		var startTime, endTime time.Time
		if err := rows.Scan(&userID, &name, &email, &role, &startTime, &endTime); err == nil {
			schedule = append(schedule, map[string]interface{}{
				"user_id":    userID,
				"name":       name,
				"email":      email,
				"role":       role,
				"start_time": startTime,
				"end_time":   endTime,
			})
		}
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"current_oncall": schedule,
			"timestamp":      time.Now(),
		},
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) executeRemediation(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("remediation:execute") || !e.HasPermission("admin") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: remediation:execute and admin required",
			Timestamp: time.Now(),
		}
	}

	// This is a sensitive operation - require explicit approval
	approved, _ := params["approved"].(bool)
	if !approved {
		return &AIToolResult{
			Success: false,
			Data: map[string]interface{}{
				"requires_approval": true,
				"message":           "Automated remediation requires explicit approval for security",
			},
			Timestamp: time.Now(),
		}
	}

	remediationType, _ := params["type"].(string)

	// Execute safe, pre-approved remediations only
	result := map[string]interface{}{
		"type":    remediationType,
		"status":  "simulated", // In production, this would trigger actual automation
		"message": fmt.Sprintf("Remediation '%s' would be executed with user approval", remediationType),
	}

	return &AIToolResult{
		Success:   true,
		Data:      result,
		Timestamp: time.Now(),
	}
}

func (e *AIToolExecutor) getAlertHistory(params map[string]interface{}) *AIToolResult {
	if !e.HasPermission("alert:read") {
		return &AIToolResult{
			Success:   false,
			Error:     "Permission denied: alert:read required",
			Timestamp: time.Now(),
		}
	}

	alertTitle, _ := params["alert_title"].(string)
	days := 30
	if d, ok := params["days"].(float64); ok {
		days = int(d)
	}

	rows, err := e.db.Query(`
		SELECT id, title, severity, status, created_at, resolved_at,
		       EXTRACT(EPOCH FROM (COALESCE(resolved_at, NOW()) - created_at)) as duration
		FROM alerts
		WHERE title ILIKE $1
			AND created_at >= NOW() - ($2 * INTERVAL '1 day')
		ORDER BY created_at DESC
		LIMIT 20
	`, "%"+alertTitle+"%", days)

	if err != nil {
		return &AIToolResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get alert history: %v", err),
			Timestamp: time.Now(),
		}
	}
	defer rows.Close()

	history := make([]map[string]interface{}, 0)
	var totalDuration, count float64
	for rows.Next() {
		var id, title, severity, status string
		var createdAt time.Time
		var resolvedAt sql.NullTime
		var duration float64
		if err := rows.Scan(&id, &title, &severity, &status, &createdAt, &resolvedAt, &duration); err == nil {
			history = append(history, map[string]interface{}{
				"id":         id,
				"title":      title,
				"severity":   severity,
				"status":     status,
				"created_at": createdAt,
				"duration":   duration,
			})
			if status == "resolved" {
				totalDuration += duration
				count++
			}
		}
	}

	avgResolution := 0.0
	if count > 0 {
		avgResolution = totalDuration / count
	}

	return &AIToolResult{
		Success: true,
		Data: map[string]interface{}{
			"history":             history,
			"count":               len(history),
			"avg_resolution_time": avgResolution,
			"period":              fmt.Sprintf("%d days", days),
		},
		Timestamp: time.Now(),
	}
}
