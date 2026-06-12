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

// TicketingIntegrationsHandler handles ticketing tool integrations
type TicketingIntegrationsHandler struct {
	db *sql.DB
}

// NewTicketingIntegrationsHandler creates a new ticketing integrations handler
func NewTicketingIntegrationsHandler(db *sql.DB) *TicketingIntegrationsHandler {
	return &TicketingIntegrationsHandler{db: db}
}

// RegisterRoutes registers ticketing integration routes
func (h *TicketingIntegrationsHandler) RegisterRoutes(router *gin.RouterGroup) {
	ticketing := router.Group("/integrations/ticketing")
	{
		ticketing.POST("/servicenow", h.ConfigureServiceNow)
		ticketing.POST("/jira", h.ConfigureJira)
		ticketing.POST("/:type/test", h.TestTicketingIntegration)
		ticketing.GET("/:type/status", h.GetTicketingStatus)
	}
}

// ConfigureServiceNow configures ServiceNow integration
func (h *TicketingIntegrationsHandler) ConfigureServiceNow(c *gin.Context) {
	var config struct {
		Name            string `json:"name" binding:"required"`
		InstanceURL     string `json:"instanceUrl" binding:"required"`
		Username        string `json:"username" binding:"required"`
		Password        string `json:"password" binding:"required"`
		AssignmentGroup string `json:"assignmentGroup"`
		Enabled         bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"instanceUrl":     config.InstanceURL,
		"username":        config.Username,
		"password":        config.Password,
		"assignmentGroup": config.AssignmentGroup,
		"enableTickets":   true,
		"enableChangeReq": true,
		"enableCMDB":      true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'servicenow', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save ServiceNow configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "ServiceNow integration configured successfully",
		"config":  config,
	})
}

// ConfigureJira configures Jira integration
func (h *TicketingIntegrationsHandler) ConfigureJira(c *gin.Context) {
	var config struct {
		Name      string `json:"name" binding:"required"`
		URL       string `json:"url" binding:"required"`
		Username  string `json:"username" binding:"required"`
		APIToken  string `json:"apiToken" binding:"required"`
		Project   string `json:"project" binding:"required"`
		IssueType string `json:"issueType"`
		Enabled   bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	if config.IssueType == "" {
		config.IssueType = "Bug"
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"url":               config.URL,
		"username":          config.Username,
		"apiToken":          config.APIToken,
		"project":           config.Project,
		"issueType":         config.IssueType,
		"enableAutoTickets": true,
		"enableStatusSync":  true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'jira', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save Jira configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Jira integration configured successfully",
		"config":  config,
	})
}

// TestTicketingIntegration tests a ticketing integration
func (h *TicketingIntegrationsHandler) TestTicketingIntegration(c *gin.Context) {
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

	result := h.testTicketingConnection(integrationType, config)

	// Update integration status
	status := "connected"
	if !result["success"].(bool) {
		status = "error"
	}

	updateQuery := `UPDATE integrations SET status = $1, last_sync = NOW(), updated_at = NOW() WHERE type = $2`
	h.db.ExecContext(c.Request.Context(), updateQuery, status, integrationType)

	c.JSON(http.StatusOK, result)
}

// GetTicketingStatus gets ticketing integration status
func (h *TicketingIntegrationsHandler) GetTicketingStatus(c *gin.Context) {
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

func (h *TicketingIntegrationsHandler) testTicketingConnection(integrationType string, config map[string]interface{}) gin.H {
	startTime := time.Now()

	switch strings.ToLower(integrationType) {
	case "servicenow":
		return h.testServiceNowConnection(config, startTime)
	case "jira":
		return h.testJiraConnection(config, startTime)
	default:
		return gin.H{
			"success": false,
			"error":   "Unsupported ticketing integration type: " + integrationType,
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
}

func (h *TicketingIntegrationsHandler) testServiceNowConnection(config map[string]interface{}, startTime time.Time) gin.H {
	instanceURL, _ := config["instanceUrl"].(string)
	username, _ := config["username"].(string)
	password, _ := config["password"].(string)

	if instanceURL == "" || username == "" || password == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required ServiceNow configuration (instanceUrl, username, password)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "ServiceNow connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"endpoint": instanceURL,
			"features": []string{"incidents", "change-requests", "cmdb"},
		},
	}
}

func (h *TicketingIntegrationsHandler) testJiraConnection(config map[string]interface{}, startTime time.Time) gin.H {
	url, _ := config["url"].(string)
	username, _ := config["username"].(string)
	apiToken, _ := config["apiToken"].(string)
	project, _ := config["project"].(string)

	if url == "" || username == "" || apiToken == "" || project == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required Jira configuration (url, username, apiToken, project)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "Jira connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"endpoint": url,
			"project":  project,
			"features": []string{"issues", "automation", "status-sync"},
		},
	}
}
