package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/alerts"
	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
)

// EnhancedWebhookHandler extends WebhookHandler with autonomous correlation capabilities
type EnhancedWebhookHandler struct {
	*WebhookHandler
	correlationEngine    *correlation.CorrelationEngine
	autonomousServiceURL string
	httpClient           *http.Client
}

// NewEnhancedWebhookHandler creates enhanced webhook handler with autonomous correlation
func NewEnhancedWebhookHandler(alertService *alerts.AlertService, correlationEngine *correlation.CorrelationEngine) *EnhancedWebhookHandler {
	baseHandler := NewWebhookHandler(alertService, nil)
	return &EnhancedWebhookHandler{
		WebhookHandler:    baseHandler,
		correlationEngine: correlationEngine,
		autonomousServiceURL: func() string {
			if url := os.Getenv("AUTONOMOUS_CORRELATION_URL"); url != "" {
				return url
			}
			return "http://autonomous-ai-correlation.sre-hub-alerthub.svc.cluster.local"
		}(),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// DynatraceWebhookWithAutonomousCorrelation enhances existing Dynatrace webhook with autonomous correlation
func (h *EnhancedWebhookHandler) DynatraceWebhookWithAutonomousCorrelation(c *gin.Context) {
	// Get raw payload for autonomous processing
	bodyBytes, _ := c.GetRawData()

	// Restore the body for normal processing
	c.Request.Body = &readCloser{bytes.NewReader(bodyBytes)}

	// First, process with existing Dynatrace webhook logic
	h.WebhookHandler.DynatraceWebhook(c)

	// If webhook processing was successful, trigger autonomous correlation in background
	if c.Writer.Status() == http.StatusOK {
		// Process autonomously in background (non-blocking)
		go func() {
			err := h.processAutonomousCorrelation(bodyBytes)
			if err != nil {
				log.Printf("Background autonomous correlation failed: %v", err)
			}
		}()
	}
}

// Process autonomous correlation for newly created alert
func (h *EnhancedWebhookHandler) processAutonomousCorrelation(webhookPayload []byte) error {
	var dynatracePayload map[string]interface{}
	if err := json.Unmarshal(webhookPayload, &dynatracePayload); err != nil {
		return fmt.Errorf("failed to unmarshal Dynatrace payload: %w", err)
	}

	// Extract alert information using your proven methodology
	problemID := getStringField(dynatracePayload, "ProblemID", "problemId", "id")

	if problemID == "" {
		return fmt.Errorf("no problem ID found in webhook")
	}

	// Get the alert that was just created by your webhook handler
	alert, err := h.alertService.GetAlertBySourceID(context.Background(), "dynatrace", problemID)
	if err != nil {
		return fmt.Errorf("could not find created alert: %w", err)
	}

	// Call autonomous correlation service
	correlationRequest := map[string]interface{}{
		"alert_id": alert.ID.String(),
		"alert_data": map[string]interface{}{
			"id":           alert.ID.String(),
			"title":        alert.Title,
			"description":  alert.Description,
			"severity":     alert.Severity,
			"source":       alert.Source,
			"source_id":    alert.SourceID,
			"service_name": extractServiceName(alert),
			"timestamp":    time.Now().UTC(),
			"metadata":     alert.Metadata,
		},
	}

	// Call autonomous correlation service
	requestBody, _ := json.Marshal(correlationRequest)

	resp, err := h.httpClient.Post(
		h.autonomousServiceURL+"/api/correlation/autonomous",
		"application/json",
		bytes.NewBuffer(requestBody),
	)

	if err != nil {
		return fmt.Errorf("autonomous service call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var correlationResponse map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&correlationResponse); err == nil {
			log.Printf("Autonomous correlation completed for Dynatrace problem %s: correlate=%v, confidence=%.2f",
				problemID,
				correlationResponse["should_correlate"],
				correlationResponse["confidence"])

			// If correlation found, update incident (your existing logic)
			if correlate, ok := correlationResponse["should_correlate"].(bool); ok && correlate {
				if incidentID, exists := correlationResponse["incident_id"].(string); exists && incidentID != "" {
					// Update your existing incident with this correlated alert
					log.Printf("Alert %s autonomously correlated to incident %s", alert.ID.String(), incidentID)

					// This would integrate with your incident management system
					// For now, just log the successful autonomous decision
				}
			}
		}
	}

	return nil
}

// Get autonomous correlation statistics for your SRE Command Center
func (h *EnhancedWebhookHandler) GetAutonomousStats(c *gin.Context) {
	resp, err := h.httpClient.Get(h.autonomousServiceURL + "/api/stats/dashboard")
	if err != nil {
		log.Printf("Failed to get autonomous stats: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Autonomous service unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Autonomous service error"})
		return
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse autonomous stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

func (h *EnhancedWebhookHandler) GetAutonomousStatus(c *gin.Context) {
	resp, err := h.httpClient.Get(h.autonomousServiceURL + "/api/integration/status")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"autonomous_engine": "unavailable",
			"error":             err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)

	c.JSON(http.StatusOK, gin.H{
		"success":                true,
		"autonomous_integration": status,
	})
}

// Enhanced RegisterRoutes that includes autonomous correlation endpoints
func (h *EnhancedWebhookHandler) RegisterEnhancedRoutes(router *gin.RouterGroup) {
	// Register all existing webhook routes
	h.WebhookHandler.RegisterRoutes(router)

	webhooks := router.Group("/webhooks")
	{
		// Override Dynatrace webhook with enhanced version
		webhooks.POST("/dynatrace", h.DynatraceWebhookWithAutonomousCorrelation)

		// NEW: Autonomous correlation endpoints for your SRE Command Center
		webhooks.GET("/autonomous/stats", h.GetAutonomousStats)
		webhooks.GET("/autonomous/status", h.GetAutonomousStatus)
	}
}

// Helper function to extract service name from your alert structure
func extractServiceName(alert *alerts.Alert) string {
	// Check metadata first (your Dynatrace approach)
	if alert.Metadata != nil {
		if serviceName, ok := alert.Metadata["service_name"].(string); ok && serviceName != "" {
			return serviceName
		}

		// Check for entity name in Dynatrace metadata
		if entityName, ok := alert.Metadata["EntityName"].(string); ok && entityName != "" {
			return entityName
		}
	}

	// Extract from title using patterns
	title := strings.ToLower(alert.Title)

	// Your common service patterns
	servicePatterns := []string{
		"web-api", "user-service", "payment-service", "auth-service",
		"database", "postgres", "mysql", "redis", "cache",
		"frontend", "backend", "api-gateway", "load-balancer",
		"kafka", "rabbitmq", "queue", "elasticsearch", "kibana",
	}

	for _, pattern := range servicePatterns {
		if strings.Contains(title, pattern) {
			return pattern
		}
	}

	return "unknown"
}

// readCloser implements io.ReadCloser for restoring request body
type readCloser struct {
	*bytes.Reader
}

func (rc *readCloser) Close() error {
	return nil
}
