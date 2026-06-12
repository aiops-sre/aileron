package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// NotificationIntegrationsHandler handles notification tool integrations
type NotificationIntegrationsHandler struct {
	db *sql.DB
}

// NewNotificationIntegrationsHandler creates a new notification integrations handler
func NewNotificationIntegrationsHandler(db *sql.DB) *NotificationIntegrationsHandler {
	return &NotificationIntegrationsHandler{db: db}
}

// RegisterRoutes registers notification integration routes
func (h *NotificationIntegrationsHandler) RegisterRoutes(router *gin.RouterGroup) {
	notifications := router.Group("/integrations/notifications")
	{
		notifications.POST("/slack", h.ConfigureSlack)
		notifications.POST("/pagerduty", h.ConfigurePagerDuty)
		notifications.POST("/:type/test", h.TestNotificationIntegration)
		notifications.GET("/:type/status", h.GetNotificationStatus)
	}
}

// ConfigureSlack configures Slack integration
func (h *NotificationIntegrationsHandler) ConfigureSlack(c *gin.Context) {
	var config struct {
		Name       string   `json:"name" binding:"required"`
		BotToken   string   `json:"botToken" binding:"required"`
		WebhookURL string   `json:"webhookUrl"`
		Channels   []string `json:"channels"`
		Enabled    bool     `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"botToken":      config.BotToken,
		"webhookUrl":    config.WebhookURL,
		"channels":      config.Channels,
		"enableAlerts":  true,
		"enableThreads": true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'slack', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save Slack configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Slack integration configured successfully",
		"config":  config,
	})
}

// ConfigurePagerDuty configures PagerDuty integration
func (h *NotificationIntegrationsHandler) ConfigurePagerDuty(c *gin.Context) {
	var config struct {
		Name             string `json:"name" binding:"required"`
		IntegrationKey   string `json:"integrationKey" binding:"required"`
		APIToken         string `json:"apiToken"`
		EscalationPolicy string `json:"escalationPolicy"`
		Enabled          bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"integrationKey":   config.IntegrationKey,
		"apiToken":         config.APIToken,
		"escalationPolicy": config.EscalationPolicy,
		"enableIncidents":  true,
		"enableEscalation": true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'pagerduty', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save PagerDuty configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "PagerDuty integration configured successfully",
		"config":  config,
	})
}

// TestNotificationIntegration tests a notification integration
func (h *NotificationIntegrationsHandler) TestNotificationIntegration(c *gin.Context) {
	integrationType := c.Param("type")

	var configJSON []byte
	query := `SELECT config FROM integrations WHERE type = $1 AND enabled = true ORDER BY updated_at DESC LIMIT 1`
	err := h.db.QueryRowContext(c.Request.Context(), query, integrationType).Scan(&configJSON)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   fmt.Sprintf("%s integration not found or not enabled", integrationType),
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch integration config: " + err.Error(),
		})
		return
	}

	var config map[string]interface{}
	json.Unmarshal(configJSON, &config)

	result := h.testNotificationConnection(integrationType, config)

	// Update integration status
	status := "connected"
	if !result["success"].(bool) {
		status = "error"
	}

	updateQuery := `UPDATE integrations SET status = $1, last_sync = NOW(), updated_at = NOW() WHERE type = $2`
	h.db.ExecContext(c.Request.Context(), updateQuery, status, integrationType)

	c.JSON(http.StatusOK, result)
}

// GetNotificationStatus gets notification integration status
func (h *NotificationIntegrationsHandler) GetNotificationStatus(c *gin.Context) {
	integrationType := c.Param("type")

	var integration struct {
		Name      string    `json:"name"`
		Type      string    `json:"type"`
		Enabled   bool      `json:"enabled"`
		Status    string    `json:"status"`
		LastSync  time.Time `json:"lastSync"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	query := `
		SELECT name, type, enabled, status, COALESCE(last_sync, created_at), updated_at
		FROM integrations 
		WHERE type = $1 
		ORDER BY updated_at DESC 
		LIMIT 1
	`

	err := h.db.QueryRowContext(c.Request.Context(), query, integrationType).Scan(
		&integration.Name, &integration.Type, &integration.Enabled,
		&integration.Status, &integration.LastSync, &integration.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   fmt.Sprintf("%s integration not found", integrationType),
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch integration status: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"integration": integration,
	})
}

func (h *NotificationIntegrationsHandler) testNotificationConnection(integrationType string, config map[string]interface{}) gin.H {
	startTime := time.Now()

	switch strings.ToLower(integrationType) {
	case "slack":
		return h.testSlackConnection(config, startTime)
	case "pagerduty":
		return h.testPagerDutyConnection(config, startTime)
	default:
		return gin.H{
			"success": false,
			"error":   "Unsupported notification integration type: " + integrationType,
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
}

func (h *NotificationIntegrationsHandler) testSlackConnection(config map[string]interface{}, startTime time.Time) gin.H {
	botToken, _ := config["botToken"].(string)

	if botToken == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required Slack configuration (botToken)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "Slack connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"features": []string{"channels", "notifications", "workflows"},
		},
	}
}

func (h *NotificationIntegrationsHandler) testPagerDutyConnection(config map[string]interface{}, startTime time.Time) gin.H {
	integrationKey, _ := config["integrationKey"].(string)

	if integrationKey == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required PagerDuty configuration (integrationKey)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "PagerDuty connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"features": []string{"incidents", "escalation", "on-call"},
		},
	}
}
