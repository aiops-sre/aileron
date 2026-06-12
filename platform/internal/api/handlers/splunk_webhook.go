package handlers

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/alerts"
	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/services/normalization"
)

// SplunkWebhookHandler handles Splunk alert webhook integrations.
// Splunk sends alerts via Webhook Alert Action (Settings Searches, Reports & Alerts Alert Actions).
type SplunkWebhookHandler struct {
	alertService      *alerts.AlertService
	correlationEngine *correlation.CorrelationEngine
	kafkaProducer     KafkaProducer
	pipelineProcessor PipelineProcessor
	normRegistry      *normalization.Registry
}

// SplunkWebhookPayload is the envelope Splunk posts for every triggered alert.
type SplunkWebhookPayload struct {
	Result      SplunkResult           `json:"result"`
	Sid         string                 `json:"sid"`
	ResultsLink string                 `json:"results_link"`
	SearchName  string                 `json:"search_name"`
	Owner       string                 `json:"owner"`
	App         string                 `json:"app"`
	Severity    string                 `json:"severity"`
	Status      string                 `json:"status"`
	Message     string                 `json:"message"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// SplunkResult contains the matched search result that triggered the alert.
type SplunkResult struct {
	Raw         string            `json:"_raw"`
	Host        string            `json:"host"`
	Source      string            `json:"source"`
	SourceType  string            `json:"sourcetype"`
	Index       string            `json:"index"`
	Time        string            `json:"_time"`
	Severity    string            `json:"severity"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Fields      map[string]string `json:"-"`
}

func NewSplunkWebhookHandler(alertService *alerts.AlertService, correlationEngine *correlation.CorrelationEngine) *SplunkWebhookHandler {
	return &SplunkWebhookHandler{
		alertService:      alertService,
		correlationEngine: correlationEngine,
		normRegistry:      normalization.NewRegistry(),
	}
}

func (h *SplunkWebhookHandler) SetKafkaProducer(p KafkaProducer) { h.kafkaProducer = p }
func (h *SplunkWebhookHandler) SetPipelineProcessor(p PipelineProcessor) {
	h.pipelineProcessor = p
}

// validateSplunkAuth verifies the X-Splunk-Request-Authentication-Token header when
// SPLUNK_WEBHOOK_TOKEN is set. If the env var is empty and we are in production,
// every request is rejected — the token MUST be configured.
func validateSplunkAuth(c *gin.Context) bool {
	secret := os.Getenv("SPLUNK_WEBHOOK_TOKEN")
	if secret == "" {
		if os.Getenv("ENV") == "production" {
			log.Printf("SECURITY: SPLUNK_WEBHOOK_TOKEN not set in production — rejecting all Splunk webhooks")
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Splunk webhook token not configured",
			})
			return false
		}
		// Non-production: allow without auth but log a warning
		log.Printf("Splunk webhook: SPLUNK_WEBHOOK_TOKEN not set — accepting unauthenticated request (non-prod only)")
		return true
	}

	// Splunk's standard auth token header
	token := c.GetHeader("X-Splunk-Request-Authentication-Token")
	if token == "" {
		// Some Splunk versions use Authorization: Splunk <token>
		auth := c.GetHeader("Authorization")
		if len(auth) > 7 && auth[:7] == "Splunk " {
			token = auth[7:]
		}
	}

	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Missing Splunk authentication token",
		})
		return false
	}

	// Constant-time token comparison — prevents timing attacks.
	// Splunk sends the raw token value; we compare directly in constant time.
	if !hmac.Equal([]byte(token), []byte(secret)) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Splunk authentication token",
		})
		return false
	}
	return true
}

// HandleSplunkWebhook accepts POST payloads from Splunk's Webhook Alert Action.
// Endpoint: POST /api/v1/webhooks/splunk
func (h *SplunkWebhookHandler) HandleSplunkWebhook(c *gin.Context) {
	if !validateSplunkAuth(c) {
		return
	}

	// Read body once for both JSON binding and any future HMAC verification.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20)) // 1 MB limit
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Failed to read request body"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	var rawPayload map[string]interface{}
	if err := c.ShouldBindJSON(&rawPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid Splunk webhook payload",
			"error":   "internal error",
		})
		return
	}

	normalized, err := h.normRegistry.Normalize("splunk", rawPayload)
	if err != nil || normalized == nil {
		normalized, _ = h.normRegistry.Normalize("generic", rawPayload)
	}
	alert := normalization.ToAlert(normalized)

	// Log source IP for security audit trail
	sourceIP := c.ClientIP()
	searchName := ""
	if sn, ok := rawPayload["search_name"].(string); ok {
		searchName = sn
	}
	log.Printf("Splunk webhook: alert=%s search=%q severity=%s source_ip=%s",
		alert.ID, searchName, alert.Severity, sourceIP)

	// Kafka-first: publish to raw-alerts so the consumer persists + correlates.
	if h.kafkaProducer != nil {
		msg := map[string]interface{}{
			"id":          alert.ID.String(),
			"alert_id":    alert.ID.String(),
			"title":       alert.Title,
			"description": alert.Description,
			"severity":    alert.Severity,
			"status":      alert.Status,
			"source":      alert.Source,
			"source_id":   alert.SourceID,
			"labels":      alert.Labels,
			"metadata":    alert.Metadata,
			"timestamp":   alert.CreatedAt,
		}
		// Add idempotency key so retries don't create duplicate alerts
		if sid, ok := rawPayload["sid"].(string); ok && sid != "" {
			msg["idempotency_key"] = "splunk:" + hex.EncodeToString([]byte(sid))
		}
		if err := h.kafkaProducer.PublishToRawAlerts(msg); err != nil {
			fmt.Printf("Splunk Kafka publish failed for alert %s: %v — falling back to direct DB\n", alert.ID, err)
		} else {
			if h.pipelineProcessor != nil {
				h.pipelineProcessor.EnqueueAlert(alert)
			}
			c.JSON(http.StatusOK, gin.H{
				"success":     true,
				"message":     "Splunk webhook queued via Kafka",
				"alert_id":    alert.ID,
				"search_name": searchName,
				"severity":    alert.Severity,
			})
			return
		}
	}

	// Direct path (Kafka unavailable).
	systemUserID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	if err := h.alertService.CreateAlert(c.Request.Context(), alert, systemUserID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create alert from Splunk webhook",
			"error":   "internal error",
		})
		return
	}

	if h.pipelineProcessor != nil {
		h.pipelineProcessor.EnqueueAlert(alert)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"message":     "Splunk webhook processed successfully",
		"alert_id":    alert.ID,
		"search_name": searchName,
		"severity":    alert.Severity,
	})
}

// RegisterRoutes wires Splunk webhook under /splunk prefix.
func (h *SplunkWebhookHandler) RegisterRoutes(router *gin.RouterGroup) {
	splunk := router.Group("/splunk")
	{
		splunk.POST("/webhook", h.HandleSplunkWebhook)
		splunk.GET("/webhook", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"status":  "ready",
				"message": "Splunk webhook endpoint active — POST alerts here",
				"url":     "/api/v1/splunk/webhook",
			})
		})
	}
}
