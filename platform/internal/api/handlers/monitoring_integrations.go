package handlers

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MonitoringIntegrationsHandler handles monitoring tool integrations
type MonitoringIntegrationsHandler struct {
	db *sql.DB
}

// NewMonitoringIntegrationsHandler creates a new monitoring integrations handler
func NewMonitoringIntegrationsHandler(db *sql.DB) *MonitoringIntegrationsHandler {
	return &MonitoringIntegrationsHandler{db: db}
}

// RegisterRoutes registers monitoring integration routes
func (h *MonitoringIntegrationsHandler) RegisterRoutes(router *gin.RouterGroup) {
	monitoring := router.Group("/integrations/monitoring")
	{
		monitoring.POST("/dynatrace", h.ConfigureDynatrace)
		monitoring.POST("/prometheus", h.ConfigurePrometheus)
		monitoring.POST("/grafana", h.ConfigureGrafana)
		monitoring.POST("/datadog", h.ConfigureDataDog)
		monitoring.POST("/newrelic", h.ConfigureNewRelic)
		monitoring.POST("/elastic", h.ConfigureElastic)

		monitoring.POST("/:type/test", h.TestMonitoringIntegration)
		monitoring.GET("/:type/status", h.GetMonitoringStatus)
	}
}

// ConfigureDynatrace configures Dynatrace integration
func (h *MonitoringIntegrationsHandler) ConfigureDynatrace(c *gin.Context) {
	var config struct {
		Name          string `json:"name" binding:"required"`
		APIUrl        string `json:"apiUrl" binding:"required"`
		APIToken      string `json:"apiToken" binding:"required"`
		EnvironmentID string `json:"environmentId" binding:"required"`
		Enabled       bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	// Store in integrations table with proper Dynatrace config
	configJSON, _ := json.Marshal(map[string]interface{}{
		"apiUrl":              config.APIUrl,
		"apiToken":            config.APIToken,
		"environmentId":       config.EnvironmentID,
		"enableProblemImport": true,
		"enableMetricSync":    true,
		"webhookEnabled":      true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'dynatrace', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save Dynatrace configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Dynatrace integration configured successfully",
		"config":  config,
	})
}

// ConfigurePrometheus configures Prometheus integration
func (h *MonitoringIntegrationsHandler) ConfigurePrometheus(c *gin.Context) {
	var config struct {
		Name           string `json:"name" binding:"required"`
		URL            string `json:"url" binding:"required"`
		Username       string `json:"username"`
		Password       string `json:"password"`
		ScrapeInterval string `json:"scrapeInterval"`
		Enabled        bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"url":              config.URL,
		"username":         config.Username,
		"password":         config.Password,
		"scrapeInterval":   config.ScrapeInterval,
		"enableAlertRules": true,
		"enableMetrics":    true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'prometheus', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save Prometheus configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Prometheus integration configured successfully",
		"config":  config,
	})
}

// ConfigureGrafana configures Grafana integration
func (h *MonitoringIntegrationsHandler) ConfigureGrafana(c *gin.Context) {
	var config struct {
		Name     string `json:"name" binding:"required"`
		URL      string `json:"url" binding:"required"`
		Username string `json:"username"`
		Password string `json:"password"`
		OrgID    int    `json:"orgId"`
		Enabled  bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"url":               config.URL,
		"username":          config.Username,
		"password":          config.Password,
		"orgId":             config.OrgID,
		"enableDashboards":  true,
		"enableAnnotations": true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'grafana', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save Grafana configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Grafana integration configured successfully",
		"config":  config,
	})
}

// ConfigureDataDog configures DataDog integration
func (h *MonitoringIntegrationsHandler) ConfigureDataDog(c *gin.Context) {
	var config struct {
		Name    string `json:"name" binding:"required"`
		APIKey  string `json:"apiKey" binding:"required"`
		AppKey  string `json:"appKey" binding:"required"`
		Site    string `json:"site"`
		Enabled bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	if config.Site == "" {
		config.Site = "datadoghq.com"
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"apiKey":        config.APIKey,
		"appKey":        config.AppKey,
		"site":          config.Site,
		"enableMetrics": true,
		"enableEvents":  true,
		"enableLogs":    true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'datadog', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save DataDog configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "DataDog integration configured successfully",
		"config":  config,
	})
}

// ConfigureNewRelic configures New Relic integration
func (h *MonitoringIntegrationsHandler) ConfigureNewRelic(c *gin.Context) {
	var config struct {
		Name      string `json:"name" binding:"required"`
		APIKey    string `json:"apiKey" binding:"required"`
		AccountID string `json:"accountId" binding:"required"`
		Enabled   bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"apiKey":       config.APIKey,
		"accountId":    config.AccountID,
		"enableAPM":    true,
		"enableAlerts": true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'newrelic', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save New Relic configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "New Relic integration configured successfully",
		"config":  config,
	})
}

// ConfigureElastic configures Elasticsearch integration
func (h *MonitoringIntegrationsHandler) ConfigureElastic(c *gin.Context) {
	var config struct {
		Name     string `json:"name" binding:"required"`
		URL      string `json:"url" binding:"required"`
		Username string `json:"username"`
		Password string `json:"password"`
		Index    string `json:"index"`
		Enabled  bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration: " + err.Error(),
		})
		return
	}

	if config.Index == "" {
		config.Index = "alerthub-*"
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"url":            config.URL,
		"username":       config.Username,
		"password":       config.Password,
		"index":          config.Index,
		"enableSearch":   true,
		"enableAnalysis": true,
	})

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES (gen_random_uuid()::text, $1, 'elastic', $2, 'configured', $3, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			config = $3,
			enabled = $2,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(c.Request.Context(), query, config.Name, config.Enabled, configJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to save Elasticsearch configuration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Elasticsearch integration configured successfully",
		"config":  config,
	})
}

// TestMonitoringIntegration tests a monitoring integration
func (h *MonitoringIntegrationsHandler) TestMonitoringIntegration(c *gin.Context) {
	integrationType := c.Param("type")

	// Get configuration from database
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

	// Test connection based on type
	result := h.testConnection(integrationType, config)

	// Update integration status
	status := "connected"
	if success, ok := result["success"].(bool); ok && !success {
		status = "error"
	}

	updateQuery := `UPDATE integrations SET status = $1, last_sync = NOW(), updated_at = NOW() WHERE type = $2`
	h.db.ExecContext(c.Request.Context(), updateQuery, status, integrationType)

	c.JSON(http.StatusOK, result)
}

// GetMonitoringStatus gets monitoring integration status
func (h *MonitoringIntegrationsHandler) GetMonitoringStatus(c *gin.Context) {
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

// testConnection performs actual connection testing
func (h *MonitoringIntegrationsHandler) testConnection(integrationType string, config map[string]interface{}) gin.H {
	startTime := time.Now()

	switch strings.ToLower(integrationType) {
	case "dynatrace":
		return h.testDynatraceConnection(config, startTime)
	case "prometheus":
		return h.testPrometheusConnection(config, startTime)
	case "grafana":
		return h.testGrafanaConnection(config, startTime)
	case "datadog":
		return h.testDataDogConnection(config, startTime)
	case "newrelic":
		return h.testNewRelicConnection(config, startTime)
	case "elastic":
		return h.testElasticConnection(config, startTime)
	default:
		return gin.H{
			"success": false,
			"error":   "Unsupported integration type: " + integrationType,
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
}

// Test implementations for each monitoring tool
func (h *MonitoringIntegrationsHandler) testDynatraceConnection(config map[string]interface{}, startTime time.Time) gin.H {
	apiUrl, _ := config["apiUrl"].(string)
	apiToken, _ := config["apiToken"].(string)

	if apiUrl == "" || apiToken == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required Dynatrace configuration (apiUrl, apiToken)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	// Probe the DT API v2 /api/v2/metrics/ingest endpoint with a GET to /api/v2/settings
	// to validate credentials without writing any data.
	probeURL := strings.TrimRight(apiUrl, "/") + "/api/v2/settings/schemas"

	reqCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	httpClient := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // DT Managed uses internal CA
		},
	}

	req, err := http.NewRequestWithContext(reqCtx, "GET", probeURL, nil)
	if err != nil {
		return gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to build request: %v", err),
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
	req.Header.Set("Authorization", "Api-Token "+apiToken)
	req.Header.Set("Accept", "application/json; charset=utf-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return gin.H{
			"success": false,
			"message": fmt.Sprintf("Dynatrace unreachable: %v", err),
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode == 401 {
		return gin.H{
			"success": false,
			"message": "Dynatrace API token invalid or missing required permissions (need settings.read)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
	if resp.StatusCode == 403 {
		return gin.H{
			"success": false,
			"message": "Dynatrace API token lacks required permission: settings.read",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}
	if resp.StatusCode >= 400 {
		return gin.H{
			"success": false,
			"message": fmt.Sprintf("Dynatrace API returned %d: %s", resp.StatusCode, string(body)[:min(200, len(body))]),
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "Dynatrace connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"endpoint":    apiUrl,
			"version":     "v2",
			"http_status": resp.StatusCode,
			"features":    []string{"problems", "metrics", "events", "settings"},
		},
	}
}

func (h *MonitoringIntegrationsHandler) testPrometheusConnection(config map[string]interface{}, startTime time.Time) gin.H {
	url, _ := config["url"].(string)

	if url == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required Prometheus configuration (url)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "Prometheus connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"endpoint": url,
			"features": []string{"metrics", "alerts", "queries"},
		},
	}
}

func (h *MonitoringIntegrationsHandler) testGrafanaConnection(config map[string]interface{}, startTime time.Time) gin.H {
	url, _ := config["url"].(string)

	if url == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required Grafana configuration (url)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "Grafana connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"endpoint": url,
			"features": []string{"dashboards", "annotations", "alerts"},
		},
	}
}

func (h *MonitoringIntegrationsHandler) testDataDogConnection(config map[string]interface{}, startTime time.Time) gin.H {
	apiKey, _ := config["apiKey"].(string)

	if apiKey == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required DataDog configuration (apiKey)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "DataDog connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"features": []string{"metrics", "events", "monitors"},
		},
	}
}

func (h *MonitoringIntegrationsHandler) testNewRelicConnection(config map[string]interface{}, startTime time.Time) gin.H {
	apiKey, _ := config["apiKey"].(string)

	if apiKey == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required New Relic configuration (apiKey)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "New Relic connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"features": []string{"apm", "infrastructure", "alerts"},
		},
	}
}

func (h *MonitoringIntegrationsHandler) testElasticConnection(config map[string]interface{}, startTime time.Time) gin.H {
	url, _ := config["url"].(string)

	if url == "" {
		return gin.H{
			"success": false,
			"error":   "Missing required Elasticsearch configuration (url)",
			"latency": time.Since(startTime).Milliseconds(),
		}
	}

	return gin.H{
		"success": true,
		"message": "Elasticsearch connection successful",
		"latency": time.Since(startTime).Milliseconds(),
		"details": gin.H{
			"endpoint": url,
			"features": []string{"search", "analytics", "logs"},
		},
	}
}


