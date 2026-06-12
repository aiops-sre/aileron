package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/analytics"
)

// AnalyticsHandler handles analytics and dashboard endpoints
type AnalyticsHandler struct {
	analyticsService *analytics.AnalyticsService
}

// NewAnalyticsHandler creates a new analytics handler
func NewAnalyticsHandler(analyticsService *analytics.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{
		analyticsService: analyticsService,
	}
}

// GetDashboardMetrics retrieves comprehensive dashboard metrics
func (h *AnalyticsHandler) GetDashboardMetrics(c *gin.Context) {
	metrics, err := h.analyticsService.GetDashboardMetrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve dashboard metrics",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    metrics,
	})
}

// GetAlertAnalytics retrieves alert analytics
func (h *AnalyticsHandler) GetAlertAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "24h")

	analytics, err := h.analyticsService.GetAlertAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve alert analytics",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    analytics,
	})
}

// GetIncidentAnalytics retrieves incident analytics
func (h *AnalyticsHandler) GetIncidentAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "24h")

	analytics, err := h.analyticsService.GetIncidentAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve incident analytics",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    analytics,
	})
}

// ExportMetrics exports metrics in Prometheus format
func (h *AnalyticsHandler) ExportMetrics(c *gin.Context) {
	metrics, err := h.analyticsService.ExportMetrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to export metrics",
			"error":   "internal error",
		})
		return
	}

	c.Header("Content-Type", "text/plain")
	c.String(http.StatusOK, metrics)
}

// GenerateReport generates a custom report
func (h *AnalyticsHandler) GenerateReport(c *gin.Context) {
	_, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not authenticated",
		})
		return
	}

	var req struct {
		ReportType string `json:"report_type" binding:"required"`
		StartDate  string `json:"start_date" binding:"required"`
		EndDate    string `json:"end_date" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
			"error":   "internal error",
		})
		return
	}

	// Parse dates
	startDate, err := parseDateTime(req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid start_date format",
		})
		return
	}

	endDate, err := parseDateTime(req.EndDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid end_date format",
		})
		return
	}

	report, err := h.analyticsService.GenerateReport(c.Request.Context(), req.ReportType, startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate report",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    report,
	})
}

// GetOverviewAnalytics returns the comprehensive overview tab payload.
func (h *AnalyticsHandler) GetOverviewAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "30d")
	data, err := h.analyticsService.GetOverviewAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetAlertDetailAnalytics returns the alert analytics tab payload.
func (h *AnalyticsHandler) GetAlertDetailAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "7d")
	data, err := h.analyticsService.GetAlertDetailAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetIncidentDetailAnalytics returns the incident analytics tab payload.
func (h *AnalyticsHandler) GetIncidentDetailAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "7d")
	data, err := h.analyticsService.GetIncidentDetailAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetCorrelationAnalytics returns the correlation intelligence tab payload.
func (h *AnalyticsHandler) GetCorrelationAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "7d")
	data, err := h.analyticsService.GetCorrelationAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetRCAAnalytics returns the RCA insights tab payload.
func (h *AnalyticsHandler) GetRCAAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "30d")
	data, err := h.analyticsService.GetRCAAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetSLOAnalytics returns the SLO performance tab payload.
func (h *AnalyticsHandler) GetSLOAnalytics(c *gin.Context) {
	timeRange := c.DefaultQuery("time_range", "30d")
	data, err := h.analyticsService.GetSLOAnalytics(c.Request.Context(), timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// RegisterRoutes registers analytics routes
func (h *AnalyticsHandler) RegisterRoutes(router *gin.RouterGroup) {
	a := router.Group("/analytics")
	{
		// Legacy
		a.GET("/dashboard", h.GetDashboardMetrics)
		a.GET("/alerts", h.GetAlertAnalytics)
		a.GET("/incidents", h.GetIncidentAnalytics)
		a.GET("/metrics", h.ExportMetrics)
		a.POST("/reports", h.GenerateReport)
		// Advanced per-tab endpoints
		a.GET("/overview", h.GetOverviewAnalytics)
		a.GET("/alerts/detail", h.GetAlertDetailAnalytics)
		a.GET("/incidents/detail", h.GetIncidentDetailAnalytics)
		a.GET("/correlation", h.GetCorrelationAnalytics)
		a.GET("/rca", h.GetRCAAnalytics)
		a.GET("/slo", h.GetSLOAnalytics)
	}

	// Legacy dashboard endpoint
	router.Group("/dashboard").GET("/stats", h.GetDashboardMetrics)
}

// Helper function to parse datetime strings
func parseDateTime(dateStr string) (time.Time, error) {
	// Try multiple formats
	formats := []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
}
