package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/alerts"
	"github.com/aileron-platform/aileron/platform/internal/services/maintenance"
)

// AlertResolvedNotifier is implemented by pipeline.AlertPipelineService.
// Using an interface keeps the alerts package free of a circular import on pipeline.
type AlertResolvedNotifier interface {
	HandleAlertResolved(ctx context.Context, alertID uuid.UUID, incidentID uuid.UUID)
}

// AlertHandler handles alert endpoints
type AlertHandler struct {
	alertService       *alerts.AlertService
	maintenanceService *maintenance.MaintenanceService
	db                 *sql.DB
	pipelineNotifier   AlertResolvedNotifier
}

// NewAlertHandler creates a new alert handler
func NewAlertHandler(alertService *alerts.AlertService, maintenanceService *maintenance.MaintenanceService, db *sql.DB) *AlertHandler {
	return &AlertHandler{
		alertService:       alertService,
		maintenanceService: maintenanceService,
		db:                 db,
	}
}

// SetPipelineNotifier wires the pipeline service so alert resolves can trigger incident auto-resolve.
func (h *AlertHandler) SetPipelineNotifier(n AlertResolvedNotifier) {
	h.pipelineNotifier = n
}

// ListAlerts retrieves alerts
func (h *AlertHandler) ListAlerts(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not authenticated",
		})
		return
	}

	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}

	// Parse and bound query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 {
		limit = 1
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	sortBy := c.DefaultQuery("sort", "created_at")
	sortOrder := c.DefaultQuery("order", "DESC")

	filters := alerts.AlertFilters{
		Limit:     limit,
		Offset:    offset,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}

	// Parse additional filters if provided
	if severity := c.Query("severity"); severity != "" {
		filters.Severity = []string{severity}
	}
	if status := c.Query("status"); status != "" {
		filters.Status = []string{status}
	}
	if source := c.Query("source"); source != "" {
		filters.Source = []string{source}
	}
	if search := c.Query("search"); search != "" {
		if len(search) > 500 {
			search = search[:500]
		}
		filters.Search = search
	}
	if timeRange := c.Query("time_range"); timeRange != "" {
		now := time.Now()
		var start time.Time
		switch timeRange {
		case "15m":
			start = now.Add(-15 * time.Minute)
		case "1h":
			start = now.Add(-1 * time.Hour)
		case "6h":
			start = now.Add(-6 * time.Hour)
		case "24h":
			start = now.Add(-24 * time.Hour)
		case "7d":
			start = now.Add(-7 * 24 * time.Hour)
		}
		if !start.IsZero() {
			filters.StartDate = &start
			filters.EndDate = &now
		}
	}

	// Get alerts
	alertList, total, err := h.alertService.ListAlerts(c.Request.Context(), uid, filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve alerts",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"alerts": alertList,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
	})
}

// GetAlert retrieves a single alert
func (h *AlertHandler) GetAlert(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid alert ID",
		})
		return
	}

	alert, err := h.alertService.GetAlert(c.Request.Context(), alertID, uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Alert not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    alert,
	})
}

// AcknowledgeAlert acknowledges an alert
func (h *AlertHandler) AcknowledgeAlert(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid alert ID",
		})
		return
	}

	err = h.alertService.AcknowledgeAlert(c.Request.Context(), alertID, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to acknowledge alert",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Alert acknowledged",
	})
}

// ResolveAlert resolves an alert
func (h *AlertHandler) ResolveAlert(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid alert ID",
		})
		return
	}

	var req struct {
		Notes string `json:"notes"`
	}
	c.ShouldBindJSON(&req)

	err = h.alertService.ResolveAlert(c.Request.Context(), alertID, uid, req.Notes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to resolve alert",
		})
		return
	}

	// Notify pipeline so it can auto-resolve the incident if all alerts are now resolved.
	if h.pipelineNotifier != nil && h.db != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var incidentID uuid.UUID
			if err := h.db.QueryRowContext(ctx,
				`SELECT incident_id FROM alerts WHERE id = $1 AND incident_id IS NOT NULL`, alertID,
			).Scan(&incidentID); err == nil && incidentID != uuid.Nil {
				h.pipelineNotifier.HandleAlertResolved(ctx, alertID, incidentID)
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Alert resolved",
	})
}

// AssignAlert assigns an alert
func (h *AlertHandler) AssignAlert(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid alert ID",
		})
		return
	}

	var req struct {
		AssignTo uuid.UUID `json:"assign_to"` // Preferred parameter
		UserID   uuid.UUID `json:"user_id"`   // Support legacy frontend calls
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	// Use whichever field is provided
	assignToUser := req.AssignTo
	if assignToUser == uuid.Nil && req.UserID != uuid.Nil {
		assignToUser = req.UserID
	}

	if assignToUser == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Either assign_to or user_id must be provided",
		})
		return
	}

	err = h.alertService.AssignAlert(c.Request.Context(), alertID, assignToUser, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to assign alert",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Alert assigned",
	})
}

// StartMaintenanceWindow starts a maintenance window for an alert
func (h *AlertHandler) StartMaintenanceWindow(c *gin.Context) {
	userID, _ := c.Get("user_id")
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid alert ID",
		})
		return
	}

	var req struct {
		StartTime time.Time  `json:"start_time"`
		EndTime   *time.Time `json:"end_time"`
		Comment   string     `json:"comment"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}
	window := &maintenance.MaintenanceWindow{
		AlertID:   &alertID,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Comment:   req.Comment,
		CreatedBy: &uid,
	}

	err = h.maintenanceService.CreateMaintenanceWindow(c.Request.Context(), window)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create maintenance window",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Maintenance window created",
		"data":    window,
	})
}

// EndMaintenanceWindow ends a maintenance window
func (h *AlertHandler) EndMaintenanceWindow(c *gin.Context) {
	userID, _ := c.Get("user_id")
	windowID, err := uuid.Parse(c.Param("window_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid window ID",
		})
		return
	}

	err = h.maintenanceService.EndMaintenanceWindow(c.Request.Context(), windowID, func() uuid.UUID {
		if uid, ok := userID.(uuid.UUID); ok {
			return uid
		}
		return uuid.Nil
	}())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to end maintenance window",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Maintenance window ended",
	})
}

// GetMaintenanceWindows retrieves maintenance windows for an alert
func (h *AlertHandler) GetMaintenanceWindows(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid alert ID",
		})
		return
	}

	windows, err := h.maintenanceService.GetMaintenanceWindowsForAlert(c.Request.Context(), alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve maintenance windows",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    windows,
	})
}

// ListActiveMaintenanceWindows retrieves all active maintenance windows
func (h *AlertHandler) ListActiveMaintenanceWindows(c *gin.Context) {
	windows, err := h.maintenanceService.GetActiveMaintenanceWindows(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve maintenance windows",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    windows,
	})
}

// BulkAcknowledgeAlerts acknowledges multiple alerts at once
func (h *AlertHandler) BulkAcknowledgeAlerts(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}

	var req struct {
		AlertIDs []string `json:"alert_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	successCount := 0

	for _, idStr := range req.AlertIDs {
		alertID, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}

		if err := h.alertService.AcknowledgeAlert(c.Request.Context(), alertID, uid); err == nil {
			successCount++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("%d alerts acknowledged", successCount),
		"data": gin.H{
			"acknowledged": successCount,
			"total":        len(req.AlertIDs),
		},
	})
}

// BulkResolveAlerts resolves multiple alerts at once
func (h *AlertHandler) BulkResolveAlerts(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}

	var req struct {
		AlertIDs []string `json:"alert_ids" binding:"required"`
		Notes    string   `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	successCount := 0
	var resolvedIDs []uuid.UUID

	for _, idStr := range req.AlertIDs {
		alertID, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}

		if err := h.alertService.ResolveAlert(c.Request.Context(), alertID, uid, req.Notes); err == nil {
			successCount++
			resolvedIDs = append(resolvedIDs, alertID)
		}
	}

	// Trigger incident auto-resolve check for each resolved alert (async).
	if h.pipelineNotifier != nil && h.db != nil && len(resolvedIDs) > 0 {
		go func(ids []uuid.UUID) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, alertID := range ids {
				var incidentID uuid.UUID
				if err := h.db.QueryRowContext(ctx,
					`SELECT incident_id FROM alerts WHERE id = $1 AND incident_id IS NOT NULL`, alertID,
				).Scan(&incidentID); err == nil && incidentID != uuid.Nil {
					h.pipelineNotifier.HandleAlertResolved(ctx, alertID, incidentID)
				}
			}
		}(resolvedIDs)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("%d alerts resolved", successCount),
		"data": gin.H{
			"resolved": successCount,
			"total":    len(req.AlertIDs),
		},
	})
}

// RegisterRoutes registers alert routes
func (h *AlertHandler) RegisterRoutes(router *gin.RouterGroup) {
	alerts := router.Group("/alerts")
	{
		alerts.GET("", h.ListAlerts)
		alerts.GET("/counts", h.GetAlertCounts)
		alerts.GET("/:id", h.GetAlert)
		alerts.POST("/:id/acknowledge", h.AcknowledgeAlert)
		alerts.POST("/:id/resolve", h.ResolveAlert)
		alerts.POST("/:id/assign", h.AssignAlert)

		// Batch operations for performance
		alerts.POST("/bulk/acknowledge", h.BulkAcknowledgeAlerts)
		alerts.POST("/bulk/resolve", h.BulkResolveAlerts)

		// Maintenance endpoints (prom-dashboard feature)
		alerts.POST("/:id/maintenance/start", h.StartMaintenanceWindow)
		alerts.POST("/maintenance/:window_id/end", h.EndMaintenanceWindow)
		alerts.GET("/:id/maintenance", h.GetMaintenanceWindows)
		alerts.GET("/maintenance/active", h.ListActiveMaintenanceWindows)

		// Alert comments endpoints
		alerts.GET("/:id/comments", h.GetAlertComments)
		alerts.POST("/:id/comments", h.AddAlertComment)
		alerts.DELETE("/:id/comments/:comment_id", h.DeleteAlertComment)

		// NEW: Autonomous AIOps endpoints
		alerts.POST("/:id/trigger-rca", h.TriggerRCAAnalysis)
		alerts.POST("/:id/auto-remediate", h.TriggerAutoRemediation)
		alerts.GET("/:id/autonomous-analysis", h.GetAutonomousAnalysis)
		alerts.GET("/autonomous/stats", h.GetAutonomousStats)
		alerts.POST("/:id/process-autonomous", h.ProcessAutonomously)
	}
}

// TriggerRCAAnalysis triggers autonomous RCA analysis for an alert
func (h *AlertHandler) TriggerRCAAnalysis(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid alert ID"})
		return
	}

	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		uid = uuid.Nil
	}

	// Get alert
	alert, err := h.alertService.GetAlert(c.Request.Context(), alertID, uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Alert not found"})
		return
	}

	// Trigger autonomous RCA analysis
	go h.triggerAsyncRCAAnalysis(alert)

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "RCA analysis triggered",
		"alert_id": alertID,
	})
}

// TriggerAutoRemediation triggers automatic remediation for an alert
func (h *AlertHandler) TriggerAutoRemediation(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid alert ID"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "Auto-remediation triggered (feature in development)",
		"alert_id": alertID,
	})
}

// GetAutonomousAnalysis gets autonomous analysis results for an alert
func (h *AlertHandler) GetAutonomousAnalysis(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid alert ID"})
		return
	}

	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		uid = uuid.Nil
	}

	alert, err := h.alertService.GetAlert(c.Request.Context(), alertID, uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Alert not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"agent_processed":     alert.AgentProcessed,
			"agent_decision":      alert.AgentDecision,
			"rca_confidence":      alert.RCAConfidence,
			"root_cause_entity":   alert.RootCauseEntity,
			"autonomous_analysis": alert.AutonomousAnalysis,
		},
	})
}

// GetAlertCounts returns status/severity breakdown counts in a single query.
// Used by the stats strip on the Alerts page — avoids counting from a single page of results.
func (h *AlertHandler) GetAlertCounts(c *gin.Context) {
	userID, _ := c.Get("user_id")
	if _, ok := userID.(uuid.UUID); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user session"})
		return
	}

	row := h.db.QueryRowContext(c.Request.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE status = 'open')                                      AS open,
			COUNT(*) FILTER (WHERE status = 'acknowledged')                              AS acknowledged,
			COUNT(*) FILTER (WHERE status = 'investigating')                             AS investigating,
			COUNT(*) FILTER (WHERE status = 'resolved')                                  AS resolved,
			COUNT(*) FILTER (WHERE status = 'closed')                                    AS closed,
			COUNT(*) FILTER (WHERE severity = 'critical'
			                   AND status NOT IN ('resolved','closed','suppressed'))      AS critical_open,
			COUNT(*)                                                                      AS total
		FROM alerts
	`)

	var counts struct {
		Open         int `json:"open"`
		Acknowledged int `json:"acknowledged"`
		Investigating int `json:"investigating"`
		Resolved     int `json:"resolved"`
		Closed       int `json:"closed"`
		CriticalOpen int `json:"critical_open"`
		Total        int `json:"total"`
	}
	if err := row.Scan(
		&counts.Open, &counts.Acknowledged, &counts.Investigating,
		&counts.Resolved, &counts.Closed, &counts.CriticalOpen, &counts.Total,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to count alerts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": counts})
}

// GetAutonomousStats returns autonomous processing statistics
func (h *AlertHandler) GetAutonomousStats(c *gin.Context) {
	stats := make(map[string]interface{})

	// Count agent-processed alerts
	var agentProcessedCount int
	h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM alerts WHERE agent_processed = true").Scan(&agentProcessedCount)
	stats["agent_processed_count"] = agentProcessedCount

	// Calculate correlation accuracy
	var totalCorrelations, correctCorrelations int
	h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM alert_correlations").Scan(&totalCorrelations)
	h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM alert_correlations WHERE confidence_score > 0.8").Scan(&correctCorrelations)

	correlationAccuracy := 0.0
	if totalCorrelations > 0 {
		correlationAccuracy = float64(correctCorrelations) / float64(totalCorrelations)
	}
	stats["correlation_accuracy"] = correlationAccuracy

	// Auto-resolution rate
	var autoResolvedCount, totalResolvedCount int
	h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM alerts WHERE resolution_type = 'auto'").Scan(&autoResolvedCount)
	h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM alerts WHERE status = 'resolved'").Scan(&totalResolvedCount)

	autoResolvedRate := 0.0
	if totalResolvedCount > 0 {
		autoResolvedRate = float64(autoResolvedCount) / float64(totalResolvedCount)
	}
	stats["auto_resolved_rate"] = autoResolvedRate

	// Total alerts
	var totalAlerts int
	h.db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM alerts").Scan(&totalAlerts)
	stats["total_alerts"] = totalAlerts

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// ProcessAutonomously processes an alert through the autonomous agent pipeline
func (h *AlertHandler) ProcessAutonomously(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid alert ID"})
		return
	}

	userID, _ := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !ok {
		uid = uuid.Nil
	}

	// Get alert
	alert, err := h.alertService.GetAlert(c.Request.Context(), alertID, uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Alert not found"})
		return
	}

	// Trigger autonomous processing
	go h.triggerAutonomousProcessing(alert)

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "Autonomous processing triggered",
		"alert_id": alertID,
	})
}

// Helper methods for autonomous processing
func (h *AlertHandler) triggerAsyncRCAAnalysis(alert *alerts.Alert) {
	// Implementation for RCA analysis integration
	// This would integrate with existing AI services
	fmt.Printf("Triggering RCA analysis for alert %s\n", alert.ID)
}

func (h *AlertHandler) triggerAutonomousProcessing(alert *alerts.Alert) {
	// Implementation for autonomous agent pipeline
	// This would enhance the existing correlation flow
	fmt.Printf("Triggering autonomous processing for alert %s\n", alert.ID)
}

// GetAlertComments retrieves comments for an alert
func (h *AlertHandler) GetAlertComments(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid alert ID"})
		return
	}

	query := `
		SELECT id, alert_id, user_id, username, comment, created_at
		FROM alert_comments
		WHERE alert_id = $1
		ORDER BY created_at DESC
	`

	rows, err := h.db.QueryContext(c.Request.Context(), query, alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch comments"})
		return
	}
	defer rows.Close()

	var comments []map[string]interface{}
	for rows.Next() {
		var id, alertID, userID uuid.UUID
		var username, comment string
		var createdAt time.Time

		if err := rows.Scan(&id, &alertID, &userID, &username, &comment, &createdAt); err != nil {
			continue
		}
		comments = append(comments, map[string]interface{}{
			"id":         id.String(),
			"alert_id":   alertID.String(),
			"user_id":    userID.String(),
			"username":   username,
			"comment":    comment,
			"created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"comments": comments}})
}

// AddAlertComment adds a comment to an alert
func (h *AlertHandler) AddAlertComment(c *gin.Context) {
	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid alert ID"})
		return
	}

	userID, _ := c.Get("user_id")
	username, _ := c.Get("username")

	var req struct {
		Comment string `json:"comment" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Comment is required"})
		return
	}

	query := `
		INSERT INTO alert_comments (id, alert_id, user_id, username, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	_, err = h.db.ExecContext(c.Request.Context(), query,
		uuid.New(), alertID, userID, username, req.Comment, time.Now())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to add comment"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "message": "Comment added"})
}

// DeleteAlertComment deletes a comment
func (h *AlertHandler) DeleteAlertComment(c *gin.Context) {
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid comment ID"})
		return
	}

	query := `DELETE FROM alert_comments WHERE id = $1`
	_, err = h.db.ExecContext(c.Request.Context(), query, commentID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to delete comment"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Comment deleted"})
}
