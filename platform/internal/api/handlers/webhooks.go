package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/alerts"
	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/services/incidents"
	"github.com/aileron-platform/aileron/platform/internal/services/normalization"
)

// WebhookHandler handles webhook ingestion from various sources with auto-correlation
type WebhookHandler struct {
	alertService      *alerts.AlertService
	correlationEngine *correlation.CorrelationEngine
	incidentService   *incidents.IncidentService
	db                *sql.DB
	kafkaProducer     KafkaProducer     // Kafka producer for Kafka-first pattern
	pipelineProcessor PipelineProcessor // parallel correlation pipeline
	normRegistry      *normalization.Registry
}

// KafkaProducer interface for publishing alerts to Kafka
type KafkaProducer interface {
	PublishToRawAlerts(alert interface{}) error
}

// PipelineProcessor is the interface implemented by AlertPipelineService.
// It enqueues an alert for async parallel correlation and auto-incident creation.
type PipelineProcessor interface {
	EnqueueAlert(alert *alerts.Alert)
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(alertService *alerts.AlertService, db *sql.DB) *WebhookHandler {
	return &WebhookHandler{
		alertService: alertService,
		db:           db,
		normRegistry: normalization.NewRegistry(),
	}
}

// SetNormalizerRegistry replaces the default normalizer registry.
// Useful for testing or for registering custom normalizers for new sources.
func (h *WebhookHandler) SetNormalizerRegistry(r *normalization.Registry) {
	h.normRegistry = r
}

// SetCorrelationEngine sets the correlation engine for auto-correlation
func (h *WebhookHandler) SetCorrelationEngine(engine *correlation.CorrelationEngine) {
	h.correlationEngine = engine
}

// SetIncidentService sets the incident service for auto-incident creation
func (h *WebhookHandler) SetIncidentService(service *incidents.IncidentService) {
	h.incidentService = service
}

// SetKafkaProducer sets the Kafka producer for Kafka-first architecture
func (h *WebhookHandler) SetKafkaProducer(producer KafkaProducer) {
	h.kafkaProducer = producer
	log.Printf("Webhook handler: Kafka-first integration %s", map[bool]string{true: "ENABLED", false: "DISABLED"}[producer != nil])
}

// SetPipelineProcessor wires the parallel correlation pipeline for all webhooks.
func (h *WebhookHandler) SetPipelineProcessor(p PipelineProcessor) {
	h.pipelineProcessor = p
	log.Printf("Webhook handler: parallel pipeline processor wired")
}

// GenericAlertRequest represents a generic alert ingestion request
type GenericAlertRequest struct {
	Title       string                 `json:"title" binding:"required"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity" binding:"required"`
	Source      string                 `json:"source"`
	SourceID    string                 `json:"source_id"`
	SourceURL   string                 `json:"source_url"`
	Tags        []string               `json:"tags"`
	Labels      map[string]string      `json:"labels"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// IngestAlert handles generic alert ingestion (API key authenticated)
func (h *WebhookHandler) IngestAlert(c *gin.Context) {
	// API key must be in the X-API-Key header only (not query params — prevents key leakage in logs/proxies)
	apiKey := c.GetHeader("X-API-Key")

	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "API key required",
		})
		return
	}

	// Validate API key and get associated user
	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid API key",
		})
		return
	}

	var req GenericAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request body",
		})
		return
	}

	// Enforce input length limits
	if len(req.Title) > 500 {
		req.Title = req.Title[:500]
	}
	if len(req.Description) > 10000 {
		req.Description = req.Description[:10000]
	}

	// Create alert
	alert := &alerts.Alert{
		Title:       req.Title,
		Description: req.Description,
		Severity:    req.Severity,
		Status:      "open",
		Source:      req.Source,
		SourceID:    req.SourceID,
		SourceURL:   req.SourceURL,
		Tags:        req.Tags,
		Labels:      req.Labels,
		Metadata:    req.Metadata,
	}

	if alert.Source == "" {
		alert.Source = "api"
	}

	err = h.alertService.CreateAlert(c.Request.Context(), alert, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create alert",
		})
		return
	}

	// PRODUCTION FEATURE: Auto-correlation and incident creation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.processAlertForCorrelationCtx(ctx, alert, userID)
	}()

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Alert created successfully with auto-correlation",
		"data": gin.H{
			"alert_id": alert.ID,
			"status":   "created",
		},
	})
}

// PrometheusWebhook handles Prometheus Alertmanager webhooks
func (h *WebhookHandler) PrometheusWebhook(c *gin.Context) {
	apiKey := c.GetHeader("Authorization")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	alertMaps := normalization.PrometheusPayloadToAlerts(payload)
	for _, alertRaw := range alertMaps {
		normalized, err := h.normRegistry.Normalize("prometheus", alertRaw)
		if err != nil || normalized == nil {
			log.Printf("Prometheus normalization failed: %v", err)
			continue
		}
		alert := normalization.ToAlert(normalized)
		// Guard: don't create a new row for a resolved alert — mirror DT webhook behaviour.
		// Resolve the existing open alert (if any) and route to the pipeline for incident auto-close.
		if alert.Status == "resolved" {
			if alert.SourceID != "" {
				// Best-effort resolve — ignore the error: ResolveAlertBySourceID returns
				// ErrAlertNotFound when the alert is already resolved (0 rows matched by
				// WHERE status != 'resolved').  We still need to enqueue in that case so
				// the pipeline can close an incident that wasn't closed on the first attempt.
				_ = h.alertService.ResolveAlertBySourceID(c.Request.Context(), alert.Source, alert.SourceID, "Auto-resolved by Prometheus webhook")
				if existing, gErr := h.alertService.GetAlertBySourceID(c.Request.Context(), alert.Source, alert.SourceID); gErr == nil && existing != nil {
					existing.Status = "resolved"
					if h.pipelineProcessor != nil {
						h.pipelineProcessor.EnqueueAlert(existing)
					}
				} else {
					log.Printf("Prometheus: RESOLVED event for source_id=%s — no alert found, ignoring", alert.SourceID)
				}
			}
			continue
		}
		if err := h.alertService.CreateAlert(c.Request.Context(), alert, userID); err != nil {
			log.Printf("Prometheus: failed to create alert source_id=%s: %v", alert.SourceID, err)
			continue
		}
		if h.pipelineProcessor != nil {
			h.pipelineProcessor.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// GrafanaWebhook handles Grafana webhooks (legacy panel alerts and v9+ unified alerting)
func (h *WebhookHandler) GrafanaWebhook(c *gin.Context) {
	apiKey := c.GetHeader("Authorization")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	alertMaps := normalization.GrafanaPayloadToAlerts(payload)
	for _, alertRaw := range alertMaps {
		normalized, err := h.normRegistry.Normalize("grafana", alertRaw)
		if err != nil || normalized == nil {
			log.Printf("Grafana normalization failed: %v", err)
			continue
		}
		alert := normalization.ToAlert(normalized)
		// Guard: don't create a new row for a resolved alert — mirror DT webhook behaviour.
		if alert.Status == "resolved" {
			if alert.SourceID != "" {
				_ = h.alertService.ResolveAlertBySourceID(c.Request.Context(), alert.Source, alert.SourceID, "Auto-resolved by Grafana webhook")
				if existing, gErr := h.alertService.GetAlertBySourceID(c.Request.Context(), alert.Source, alert.SourceID); gErr == nil && existing != nil {
					existing.Status = "resolved"
					if h.pipelineProcessor != nil {
						h.pipelineProcessor.EnqueueAlert(existing)
					}
				} else {
					log.Printf("Grafana: RESOLVED event for source_id=%s — no alert found, ignoring", alert.SourceID)
				}
			}
			continue
		}
		if err := h.alertService.CreateAlert(c.Request.Context(), alert, userID); err != nil {
			log.Printf("Grafana: failed to create alert source_id=%s: %v", alert.SourceID, err)
			continue
		}
		if h.pipelineProcessor != nil {
			h.pipelineProcessor.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// DynatraceWebhook handles Dynatrace problem notifications with enhanced async processing
func (h *WebhookHandler) DynatraceWebhook(c *gin.Context) {
	start := time.Now()

	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Read raw body for HMAC verification before JSON parsing
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Verify HMAC-SHA256 signature if DYNATRACE_WEBHOOK_SECRET is configured
	if secret := os.Getenv("DYNATRACE_WEBHOOK_SECRET"); secret != "" {
		sig := c.GetHeader("X-Dynatrace-Signature")
		if sig == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing webhook signature"})
			return
		}
		// Normalize: strip sha256= prefix and lowercase for comparison.
		sig = strings.TrimPrefix(sig, "sha256=")
		sig = strings.TrimPrefix(sig, "SHA256=")
		sig = strings.ToLower(sig)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(rawBody)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(expected)) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"})
			return
		}
	}

	// Accept any JSON payload from Dynatrace (be flexible with format)
	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		log.Printf("Dynatrace webhook payload error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid payload",
			"details": err.Error(),
		})
		return
	}

	// Extract fields with fallbacks for different Dynatrace formats
	problemID := getStringField(payload, "ProblemID", "problemId", "id")
	problemTitle := getStringField(payload, "ProblemTitle", "problemTitle", "title")
	problemDetails := getStringField(payload, "ProblemDetailsText", "ProblemDetails", "problemDetails", "description")
	state := getStringField(payload, "State", "state", "status")
	_ = getStringField(payload, "ImpactLevel", "impactLevel", "severity") // consumed by normalizer

	// Extract priority/impact/urgency for logging purposes only.
	_ = getStringField(payload, "ProblemSeverity", "problemSeverity")
	_ = getStringField(payload, "impact", "ProblemImpact")
	_ = getStringField(payload, "urgency")
	_ = getStringField(payload, "priority")

	mappedStatus := h.mapDynatraceStatus(state)

	// DT occasionally sends RESOLVED events with an empty or missing State field.
	// Detect resolution from the description prefix so these events don't create
	// spurious open alerts (observed with P-26057264: State="" but ProblemDetailsText
	// started with "RESOLVED Problem P-26057264").
	if mappedStatus != "resolved" && strings.HasPrefix(strings.TrimSpace(problemDetails), "RESOLVED Problem") {
		mappedStatus = "resolved"
		log.Printf("Dynatrace: overriding status to resolved from description prefix (State=%q) for problem %s", state, problemID)
	}

	// When DT sends an empty ProblemID (health-check / synthetic-monitor alerts, or
	// custom HTTP integrations with a minimal payload template), derive a stable
	// synthetic ID from the normalizer fingerprint.  This ensures:
	//   1. Repeated OPEN firings of the same problem deduplicate into one DB row.
	//   2. The paired RESOLVED event finds the existing row and closes the incident.
	// Without this, every firing creates a new row and resolutions are ignored.
	if problemID == "" && h.normRegistry != nil {
		if preNorm, preErr := h.normRegistry.Normalize("dynatrace", payload); preErr == nil && preNorm != nil && preNorm.Fingerprint != "" {
			problemID = "fp:" + preNorm.Fingerprint
		}
	}

	log.Printf("Dynatrace webhook: ProblemID=%s, State=%s, Title=%s", problemID, state, problemTitle)

	// Attempt exact-match dedup using the (possibly synthetic) problem ID.
	var existingAlert *alerts.Alert
	if problemID != "" {
		existingAlert, err = h.alertService.GetAlertBySourceID(c.Request.Context(), "dynatrace", problemID)
	}

	// Check if we found an alert (err might be ErrAlertNotFound or nil)
	if err == nil && existingAlert != nil {
		// Alert exists - update it directly (no queue for updates)
		log.Printf("Updating existing alert %s for problem %s: %s", existingAlert.ID, problemID, mappedStatus)

		if mappedStatus == "resolved" {
			err = h.alertService.ResolveAlertBySourceID(c.Request.Context(), "dynatrace", problemID, "Auto-resolved by Dynatrace webhook")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve alert", "details": err.Error()})
				return
			}
			// Update in-memory state so the pipeline sees status=resolved and auto-closes
			// the linked incident.  Without this the pipeline enqueues an "open" alert and
			// handleResolvedAlert is never triggered, leaving the incident open for the next
			// problem event to incorrectly merge into.
			existingAlert.Status = "resolved"
			go h.processAlertForCorrelation(existingAlert, userID)
			c.JSON(http.StatusOK, gin.H{
				"status": "success", "message": "Alert resolved", "alert_id": existingAlert.ID, "action": "updated",
				"processing_ms": time.Since(start).Milliseconds(),
			})
			return
		} else {
			// Update other status changes — re-normalize to refresh labels so the
			// pipeline has correct cluster/workload context (avoids null-label flapping).
			existingAlert.Status = mappedStatus
			existingAlert.Description = problemDetails
			existingAlert.Metadata = payload
			if refreshed, rErr := h.normRegistry.Normalize("dynatrace", payload); rErr == nil && refreshed != nil {
				existingAlert.Labels = refreshed.Labels
			}
			err = h.alertService.UpdateAlertBySourceID(c.Request.Context(), existingAlert)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update alert", "details": err.Error()})
				return
			}
			// Re-enqueue updated alert so pipeline records a fresh correlation result.
			go h.processAlertForCorrelation(existingAlert, userID)
			c.JSON(http.StatusOK, gin.H{
				"status": "success", "message": "Alert updated", "alert_id": existingAlert.ID, "action": "updated",
				"processing_ms": time.Since(start).Milliseconds(),
			})
			return
		}
	}

	// Alert doesn't exist - create new one.
	// Guard: if Dynatrace is telling us something resolved that we have no record of,
	// try a description-based fallback before giving up — the original OPEN alert may
	// have been ingested before source_id tracking was in place (empty source_id).
	if mappedStatus == "resolved" {
		if h.db != nil && problemID != "" {
			descPattern := "%Problem " + problemID + "%"
			var fallbackID uuid.UUID
			qErr := h.db.QueryRowContext(c.Request.Context(), `
				SELECT id
				FROM alerts
				WHERE source = 'dynatrace'
				  AND (source_id IS NULL OR source_id = '')
				  AND status != 'resolved'
				  AND description ILIKE $1
				ORDER BY created_at DESC
				LIMIT 1
			`, descPattern).Scan(&fallbackID)
			if qErr == nil && fallbackID != uuid.Nil {
				_, _ = h.db.ExecContext(c.Request.Context(), `
					UPDATE alerts SET status='resolved', resolved_at=NOW(), source_id=$1 WHERE id=$2
				`, problemID, fallbackID)
				log.Printf("Dynatrace RESOLVED: resolved orphaned alert %s via description fallback for problem %s", fallbackID, problemID)
				if h.pipelineProcessor != nil {
					resolved := &alerts.Alert{ID: fallbackID, Status: "resolved"}
					go h.processAlertForCorrelation(resolved, userID)
				}
				c.JSON(http.StatusAccepted, gin.H{"status": "resolved", "alert_id": fallbackID, "action": "fallback_resolved"})
				return
			}
		}
		log.Printf("Dynatrace RESOLVED event for unknown problem %s — no open alert found, ignoring", problemID)
		c.JSON(http.StatusAccepted, gin.H{
			"status":  "ignored",
			"message": "no open alert found for this problem",
		})
		return
	}

	log.Printf("Creating new alert for Dynatrace problem: %s", problemID)

	// Normalise the payload — extracts labels, entities, RCE, severity from all
	// Dynatrace payload locations (ProblemDetails text, customProperties, entities).
	normalized, normErr := h.normRegistry.Normalize("dynatrace", payload)
	if normErr != nil || normalized == nil {
		log.Printf("Dynatrace normalization failed (%v), using raw fields", normErr)
		normalized, _ = h.normRegistry.Normalize("generic", payload)
	}

	// Build the alert directly from the normalizer output — no CanonicalAlert intermediary.
	alert := normalization.ToAlert(normalized)
	alert.Status = mappedStatus
	// Ensure source_id is the authoritative problem ID (real or synthetic "fp:…").
	// ToAlert sets it from normalized.SourceID which may be empty for blank-ProblemID payloads.
	if problemID != "" {
		alert.SourceID = problemID
	}
	if alert.Title == "" {
		alert.Title = "Dynatrace Problem: " + problemID
	}
	if alert.Description == "" {
		alert.Description = "Problem notification from Dynatrace"
	}

	// Kafka-first: publish to Kafka so the consumer handles DB writes and correlation
	// at its own pace — this protects the DB during burst/bulk alert storms.
	if h.kafkaProducer != nil {
		if err := h.kafkaProducer.PublishToRawAlerts(map[string]interface{}{
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
		}); err != nil {
			log.Printf("Kafka publish failed, falling back to direct processing: %v", err)
			// fall through to direct path below
		} else {
			log.Printf("Alert published to Kafka topic raw-alerts: source_id=%s", problemID)
			c.JSON(http.StatusAccepted, gin.H{
				"status":        "accepted",
				"alert_id":      alert.ID,
				"source_id":     problemID,
				"processing":    "kafka",
				"processing_ms": time.Since(start).Milliseconds(),
			})
			return
		}
	}

	// Direct fallback (Kafka unavailable): persist to DB then enqueue to pipeline.
	err = h.alertService.CreateAlert(c.Request.Context(), alert, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create alert", "details": err.Error()})
		return
	}
	go h.processAlertForCorrelation(alert, userID)

	c.JSON(http.StatusOK, gin.H{
		"status":        "success",
		"message":       "Alert created from Dynatrace problem",
		"alert_id":      alert.ID,
		"action":        "created",
		"processing_ms": time.Since(start).Milliseconds(),
	})
}

// Helper function to get string field with multiple possible keys
func getStringField(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := data[key]; ok {
			if str, ok := val.(string); ok {
				// Trim whitespace from extracted strings
				return strings.TrimSpace(str)
			}
		}
	}
	return ""
}

// PagerDutyWebhook handles PagerDuty incident webhooks
func (h *WebhookHandler) PagerDutyWebhook(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload PagerDutyPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	for _, message := range payload.Messages {
		incident := message.Incident

		serviceName := "unknown"
		if service, ok := incident.Service["summary"].(string); ok {
			serviceName = service
		}

		alert := &alerts.Alert{
			Title:       incident.Title,
			Description: incident.Description,
			Severity:    h.mapPagerDutyUrgency(incident.Urgency),
			Status:      h.mapPagerDutyStatus(incident.Status),
			Source:      "pagerduty",
			SourceID:    incident.ID,
			SourceURL:   incident.HTMLURL,
			Tags:        []string{serviceName},
			Metadata: map[string]interface{}{
				"incident_number":   incident.IncidentNumber,
				"service":           incident.Service,
				"escalation_policy": incident.EscalationPolicy,
			},
		}

		if err := h.alertService.CreateAlert(c.Request.Context(), alert, userID); err != nil {
			log.Printf("PagerDuty webhook: failed to create alert for incident %s: %v", incident.ID, err)
			continue
		}
		if h.pipelineProcessor != nil {
			h.pipelineProcessor.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// Helper functions

func (h *WebhookHandler) validateAPIKey(c *gin.Context, apiKey string) (uuid.UUID, error) {
	if h.db == nil {
		return uuid.Nil, fmt.Errorf("API key validation not configured")
	}

	// Strip "Bearer " prefix if present
	key := strings.TrimPrefix(apiKey, "Bearer ")
	key = strings.TrimSpace(key)
	if key == "" {
		return uuid.Nil, fmt.Errorf("empty API key")
	}

	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	var userID uuid.UUID
	err := h.db.QueryRowContext(c.Request.Context(),
		`UPDATE webhook_api_keys
		 SET last_used_at = NOW()
		 WHERE key_hash = $1 AND enabled = TRUE
		 RETURNING user_id`,
		keyHash,
	).Scan(&userID)
	if err == sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("invalid API key")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("API key lookup failed: %w", err)
	}
	return userID, nil
}

func (h *WebhookHandler) mapDynatraceStatus(state string) string {
	if state == "RESOLVED" {
		return "resolved"
	}
	return "open"
}

func (h *WebhookHandler) mapPagerDutyUrgency(urgency string) string {
	if urgency == "high" {
		return "critical"
	}
	return "high"
}

func (h *WebhookHandler) mapPagerDutyStatus(status string) string {
	switch status {
	case "triggered":
		return "open"
	case "acknowledged":
		return "acknowledged"
	case "resolved":
		return "resolved"
	default:
		return "open"
	}
}

// Payload structures

type PagerDutyPayload struct {
	Messages []PagerDutyMessage `json:"messages"`
}

type PagerDutyMessage struct {
	Event    string            `json:"event"`
	Incident PagerDutyIncident `json:"incident"`
}

type PagerDutyIncident struct {
	ID               string                 `json:"id"`
	IncidentNumber   int                    `json:"incident_number"`
	Title            string                 `json:"title"`
	Description      string                 `json:"description"`
	Status           string                 `json:"status"`
	Urgency          string                 `json:"urgency"`
	HTMLURL          string                 `json:"html_url"`
	Service          map[string]interface{} `json:"service"`
	EscalationPolicy map[string]interface{} `json:"escalation_policy"`
}

// GetDynatraceAlerts returns alerts sourced from Dynatrace.
func (h *WebhookHandler) GetDynatraceAlerts(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Authentication required"})
		return
	}

	filters := alerts.AlertFilters{
		Source:    []string{"dynatrace"},
		Limit:     100,
		Offset:    0,
		SortBy:    "created_at",
		SortOrder: "DESC",
	}
	if severity := c.Query("severity"); severity != "" {
		filters.Severity = []string{severity}
	}
	if status := c.Query("status"); status != "" {
		filters.Status = []string{status}
	}

	alertList, total, err := h.alertService.ListAlerts(c.Request.Context(), userID.(uuid.UUID), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to retrieve Dynatrace alerts", "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"alerts": alertList, "total": total, "source": "dynatrace"},
	})
}

// webhookBodyLimitMiddleware rejects requests whose body exceeds maxBytes.
// Using both ContentLength check (fast path) and MaxBytesReader (safe path for chunked transfers).
func webhookBodyLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "payload too large"})
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// RegisterRoutes registers webhook routes
func (h *WebhookHandler) RegisterRoutes(router *gin.RouterGroup) {
	webhooks := router.Group("/webhooks")
	// Limit webhook payloads to 1 MB — prevents memory exhaustion from oversized payloads
	webhooks.Use(webhookBodyLimitMiddleware(1 << 20))
	{
		// Generic ingestion endpoint
		webhooks.POST("/ingest", h.IngestAlert)

		// Source-specific endpoints
		webhooks.POST("/prometheus", h.PrometheusWebhook)
		webhooks.POST("/grafana", h.GrafanaWebhook)
		webhooks.POST("/dynatrace", h.DynatraceWebhook)
		webhooks.POST("/pagerduty", h.PagerDutyWebhook)
		webhooks.POST("/splunk", h.SplunkWebhook)
		webhooks.POST("/datadog", h.DatadogWebhook)
		webhooks.POST("/newrelic", h.NewRelicWebhook)
		webhooks.POST("/cloudwatch", h.CloudWatchWebhook)
		webhooks.POST("/generic", h.IngestAlert)
	}

	// /dynatrace group: webhook alias + alert listing (merged from DynatraceWebhookHandler)
	dt := router.Group("/dynatrace")
	{
		dt.POST("/webhook", h.DynatraceWebhook)
		dt.GET("/alerts", h.GetDynatraceAlerts)
	}

	// Public ingestion endpoint (for testing)
	router.POST("/alerts/ingest", h.IngestAlert)
}

// Additional webhook handlers (placeholders)

func (h *WebhookHandler) SplunkWebhook(c *gin.Context) {
	apiKey := c.GetHeader("Authorization")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if _, err := h.validateAPIKey(c, apiKey); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *WebhookHandler) DatadogWebhook(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	for _, alertRaw := range normalization.DatadogPayloadToAlerts(payload) {
		normalized, err := h.normRegistry.Normalize("datadog", alertRaw)
		if err != nil || normalized == nil {
			log.Printf("Datadog normalization failed: %v", err)
			continue
		}
		alert := normalization.ToAlert(normalized)
		if err := h.alertService.CreateAlert(c.Request.Context(), alert, userID); err != nil {
			log.Printf("Datadog: failed to create alert source_id=%s: %v", alert.SourceID, err)
			continue
		}
		if h.pipelineProcessor != nil {
			h.pipelineProcessor.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *WebhookHandler) NewRelicWebhook(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	for _, alertRaw := range normalization.NewRelicPayloadToAlerts(payload) {
		normalized, err := h.normRegistry.Normalize("newrelic", alertRaw)
		if err != nil || normalized == nil {
			log.Printf("NewRelic normalization failed: %v", err)
			continue
		}
		alert := normalization.ToAlert(normalized)
		if err := h.alertService.CreateAlert(c.Request.Context(), alert, userID); err != nil {
			log.Printf("NewRelic: failed to create alert source_id=%s: %v", alert.SourceID, err)
			continue
		}
		if h.pipelineProcessor != nil {
			h.pipelineProcessor.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *WebhookHandler) CloudWatchWebhook(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID, err := h.validateAPIKey(c, apiKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	for _, alertRaw := range normalization.CloudWatchPayloadToAlerts(payload) {
		normalized, err := h.normRegistry.Normalize("cloudwatch", alertRaw)
		if err != nil || normalized == nil {
			log.Printf("CloudWatch normalization failed: %v", err)
			continue
		}
		alert := normalization.ToAlert(normalized)
		if err := h.alertService.CreateAlert(c.Request.Context(), alert, userID); err != nil {
			log.Printf("CloudWatch: failed to create alert source_id=%s: %v", alert.SourceID, err)
			continue
		}
		if h.pipelineProcessor != nil {
			h.pipelineProcessor.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// processAlertForCorrelation handles automatic correlation and incident creation in background
func (h *WebhookHandler) processAlertForCorrelation(alert *alerts.Alert, userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h.processAlertForCorrelationCtx(ctx, alert, userID)
}

// processAlertForCorrelationCtx is the context-aware implementation.
func (h *WebhookHandler) processAlertForCorrelationCtx(ctx context.Context, alert *alerts.Alert, userID uuid.UUID) {
	// Enqueue to parallel pipeline (non-blocking, does not depend on basic engine)
	if h.pipelineProcessor != nil {
		h.pipelineProcessor.EnqueueAlert(alert)
	}

	if h.correlationEngine == nil {
		log.Printf("Correlation engine not available for alert %s", alert.ID)
		return
	}

	log.Printf("Starting auto-correlation for alert %s: %s", alert.ID, alert.Title)

	// Convert to correlation Alert format
	corrAlert := &correlation.Alert{
		ID:          alert.ID,
		Title:       alert.Title,
		Description: alert.Description,
		Severity:    alert.Severity,
		Source:      alert.Source,
		Tags:        alert.Tags,
		Labels:      alert.Labels,
		Metadata:    alert.Metadata,
		CreatedAt:   time.Now(),
	}

	// Run correlation analysis
	result, err := h.correlationEngine.CorrelateAlert(ctx, corrAlert)
	if err != nil {
		log.Printf("Correlation failed for alert %s: %v", alert.ID, err)
		return
	}

	log.Printf("Correlation completed for alert %s: confidence=%.2f, action=%s",
		alert.ID, result.ConfidenceScore, result.RecommendedAction)

	// Handle correlation results
	switch result.RecommendedAction {
	case "create_incident":
		log.Printf("Alert %s recommended for incident creation (confidence: %.2f)", alert.ID, result.ConfidenceScore)
		// Incident creation is handled by the pipeline processor
	case "merge_with_existing":
		log.Printf("Alert %s should be merged with existing alerts", alert.ID)
		// Future: Implement merging logic
	case "escalate":
		log.Printf("Alert %s requires escalation", alert.ID)
		// Future: Implement escalation logic
	}
}

