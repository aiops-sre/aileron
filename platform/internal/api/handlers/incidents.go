package handlers

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/services/incidents"
)

// IncidentHandler handles incident endpoints
type IncidentHandler struct {
	incidentService *incidents.IncidentService
	db              *sql.DB
	feedbackSvc     *correlation.CorrelationFeedbackService // optional; set via SetFeedbackService
	// oieBaseURL is the OIE service endpoint. When set, GetIncident enriches the
	// response with OIE's investigation result (real evidence-based RCA).
	oieBaseURL      string
	notificationSvc incidentNotifier // optional; fires Slack RCA updates
}

// incidentNotifier is the narrow interface for sending RCA update notifications.
type incidentNotifier interface {
	SendRCAUpdateNotification(ctx context.Context, incidentID uuid.UUID, incidentNumber, severity, title, rootCause string, confidence float64, band, source string) error
}

// SetNotificationService wires a notification service for RCA completion Slack updates.
func (h *IncidentHandler) SetNotificationService(n incidentNotifier) {
	h.notificationSvc = n
}

// NewIncidentHandler creates a new incident handler
func NewIncidentHandler(incidentService *incidents.IncidentService, db *sql.DB) *IncidentHandler {
	return &IncidentHandler{
		incidentService: incidentService,
		db:              db,
	}
}

// SetOIEURL wires the OIE investigation service URL so GetIncident can enrich
// responses with real evidence-based RCA results.
func (h *IncidentHandler) SetOIEURL(url string) {
	h.oieBaseURL = url
}

// SetFeedbackService wires the correlation feedback service for the per-incident feedback endpoint.
func (h *IncidentHandler) SetFeedbackService(svc *correlation.CorrelationFeedbackService) {
	h.feedbackSvc = svc
}

// ListIncidents retrieves incidents
func (h *IncidentHandler) ListIncidents(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not authenticated",
		})
		return
	}

	// Parse query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	filters := incidents.IncidentFilters{
		Limit:  limit,
		Offset: offset,
	}

	// Parse status filter (comma-separated)
	if statusParam := c.Query("status"); statusParam != "" {
		filters.Status = strings.Split(statusParam, ",")
	}

	// Parse severity filter
	if severityParam := c.Query("severity"); severityParam != "" {
		filters.Severity = strings.Split(severityParam, ",")
	}

	// Parse search
	if search := c.Query("search"); search != "" {
		filters.Search = search
	}

	// Get incidents
	incidentList, total, err := h.incidentService.ListIncidents(c.Request.Context(), userID.(uuid.UUID), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve incidents",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"incidents": incidentList,
			"total":     total,
			"limit":     limit,
			"offset":    offset,
		},
	})
}

// GetIncident retrieves a single incident
func (h *IncidentHandler) GetIncident(c *gin.Context) {
	userID, _ := c.Get("user_id")
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	incident, err := h.incidentService.GetIncident(c.Request.Context(), incidentID, userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Incident not found"})
		return
	}

	// Enrich with OIE investigation result if available.
	// OIE provides evidence-based RCA that supersedes the LLM-only narrative from alert_pipeline.
	var oieInvestigation map[string]interface{}
	if h.oieBaseURL != "" {
		oieURL := fmt.Sprintf("%s/api/v1/incidents/%s/investigation", h.oieBaseURL, incidentID)
		if req, reqErr := http.NewRequestWithContext(c.Request.Context(), "GET", oieURL, nil); reqErr == nil {
			client := &http.Client{Timeout: 3 * time.Second}
			if resp, doErr := client.Do(req); doErr == nil && resp.StatusCode == 200 {
				defer resp.Body.Close()
				_ = json.NewDecoder(resp.Body).Decode(&oieInvestigation)

				// If OIE has a completed investigation, persist real confidence back to incidents
				// so the UI shows evidence-weighted confidence, not the static 0.70 default.
				if status, _ := oieInvestigation["status"].(string); status == "COMPLETED" {
					if rca, ok := oieInvestigation["rca"].(map[string]interface{}); ok {
						if conf, ok := rca["confidence"].(float64); ok && conf > 0 {
							summary, _ := rca["summary"].(string)
							h.db.ExecContext(c.Request.Context(), `
								UPDATE incidents
								SET rca_confidence = $1,
								    rca_status = 'completed',
								    ai_root_cause = COALESCE(NULLIF($2,''), ai_root_cause),
								    updated_at = NOW()
								WHERE id = $3
								  AND rca_status NOT IN ('failed')
							`, conf, summary, incidentID)
						}
					}
				}
			}
		}
	}

	response := gin.H{
		"success": true,
		"data":    incident,
	}
	if oieInvestigation != nil {
		response["investigation"] = oieInvestigation
	}

	c.JSON(http.StatusOK, response)
}

// CreateIncident creates a new incident
func (h *IncidentHandler) CreateIncident(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var incident incidents.Incident
	if err := c.ShouldBindJSON(&incident); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
			"error":   "internal error",
		})
		return
	}

	err := h.incidentService.CreateIncident(c.Request.Context(), &incident, userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create incident",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Incident created",
		"data":    incident,
	})
}

// UpdateIncident updates an incident
func (h *IncidentHandler) UpdateIncident(c *gin.Context) {
	userID, _ := c.Get("user_id")
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid incident ID",
		})
		return
	}

	var incident incidents.Incident
	if err := c.ShouldBindJSON(&incident); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
			"error":   "internal error",
		})
		return
	}

	incident.ID = incidentID
	err = h.incidentService.UpdateIncident(c.Request.Context(), &incident, userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update incident",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Incident updated",
		"data":    incident,
	})
}

// ResolveIncident resolves an incident
func (h *IncidentHandler) ResolveIncident(c *gin.Context) {
	userID, _ := c.Get("user_id")
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid incident ID",
		})
		return
	}

	var req struct {
		Notes string `json:"notes"`
	}
	c.ShouldBindJSON(&req)

	err = h.incidentService.ResolveIncident(c.Request.Context(), incidentID, userID.(uuid.UUID), req.Notes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to resolve incident",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Incident resolved",
	})
}

// AssignIncident assigns an incident
func (h *IncidentHandler) AssignIncident(c *gin.Context) {
	userID, _ := c.Get("user_id")
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid incident ID",
		})
		return
	}

	var req struct {
		AssignTo uuid.UUID `json:"assign_to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	err = h.incidentService.AssignIncident(c.Request.Context(), incidentID, req.AssignTo, userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to assign incident",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Incident assigned",
	})
}

// GetIncidentTimeline retrieves incident timeline
func (h *IncidentHandler) GetIncidentTimeline(c *gin.Context) {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Authentication required"})
		return
	}
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid incident ID",
		})
		return
	}

	timeline, err := h.incidentService.GetIncidentTimeline(c.Request.Context(), incidentID, userIDVal.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve timeline",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    timeline,
	})
}

// AddTimelineEvent adds an event to incident timeline
func (h *IncidentHandler) AddTimelineEvent(c *gin.Context) {
	userID, _ := c.Get("user_id")
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid incident ID",
		})
		return
	}

	var req struct {
		EventType   string                 `json:"event_type" binding:"required"`
		Title       string                 `json:"title" binding:"required"`
		Description string                 `json:"description"`
		Metadata    map[string]interface{} `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
			"error":   "internal error",
		})
		return
	}

	err = h.incidentService.AddTimelineEvent(c.Request.Context(), incidentID, userID.(uuid.UUID),
		req.EventType, req.Title, req.Description, req.Metadata)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to add timeline event",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Timeline event added",
	})
}

// PerformRCA performs root cause analysis on an incident
func (h *IncidentHandler) PerformRCA(c *gin.Context) {
	userID, _ := c.Get("user_id")
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid incident ID",
		})
		return
	}

	rca, err := h.incidentService.PerformRCA(c.Request.Context(), incidentID, userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to perform RCA",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    rca,
	})
}

// GetIncidentStats retrieves incident statistics
func (h *IncidentHandler) GetIncidentStats(c *gin.Context) {
	userID, _ := c.Get("user_id")

	stats, err := h.incidentService.GetIncidentStats(c.Request.Context(), userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve incident stats",
			"error":   "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// AutoCloseResolved sweeps all open incidents and resolves those whose every
// alert is already resolved.
func (h *IncidentHandler) AutoCloseResolved(c *gin.Context) {
	closed, err := h.incidentService.AutoCloseResolvedIncidents(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Auto-close sweep failed",
			"error":   "internal error",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"closed":  closed,
		"message": fmt.Sprintf("Auto-closed %d incident(s) whose alerts were all resolved", closed),
	})
}

// OIEResultCallback receives completed investigation data from the OIE service and
// updates the incident with evidence-based RCA. Called by OIE after investigation completes.
// This endpoint ensures incidents in rca_status='investigating' get updated automatically
// when OIE finishes, without requiring the operator to open the incident.
func (h *IncidentHandler) OIEResultCallback(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var req struct {
		InvestigationID      string  `json:"investigation_id"`
		Confidence           float64 `json:"confidence"`
		ConfidenceBand       string  `json:"confidence_band"`
		RootCause            string  `json:"root_cause"`
		EvidenceCount        int     `json:"evidence_count"`
		HypothesesGenerated  int     `json:"hypotheses_generated"`
		HypothesesRejected   int     `json:"hypotheses_rejected"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}
	if req.Confidence <= 0 || req.RootCause == "" {
		// OIE returned insufficient result — transition investigating→failed so
		// the incident doesn't stay stuck. Don't overwrite a better existing cause.
		if h.db != nil {
			h.db.ExecContext(c.Request.Context(), `
				UPDATE incidents
				SET rca_status = CASE WHEN rca_status = 'investigating' THEN 'failed' ELSE rca_status END,
				    ai_root_cause = CASE
				        WHEN rca_status = 'investigating'
				          AND (ai_root_cause LIKE 'RCA in progress%' OR ai_root_cause LIKE '%OIE evidence%')
				        THEN 'OIE investigation completed with insufficient evidence to determine root cause.'
				        ELSE ai_root_cause
				    END,
				    updated_at = NOW()
				WHERE id = $1
			`, incidentID)
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "no update — insufficient result; marked failed"})
		return
	}

	if h.db != nil {
		h.db.ExecContext(c.Request.Context(), `
			UPDATE incidents
			SET rca_confidence     = GREATEST(COALESCE(rca_confidence, 0), $1),
			    rca_status         = CASE WHEN rca_status IN ('investigating','queued','none') THEN 'completed'
			                              ELSE rca_status END,
			    ai_root_cause      = COALESCE(NULLIF(TRIM($2), ''), ai_root_cause),
			    rca_investigation_id = COALESCE(NULLIF($3, ''), rca_investigation_id),
			    updated_at         = NOW()
			WHERE id = $4
		`,
			req.Confidence,
			req.RootCause,
			req.InvestigationID,
			incidentID,
		)
		// Send Slack RCA update notification (OIE final result).
		if h.notificationSvc != nil {
			go func() {
				// Fetch incident number, severity, title for the notification.
				var incNum, sev, title string
				_ = h.db.QueryRowContext(c.Request.Context(),
					`SELECT COALESCE(incident_number,''), COALESCE(severity,''), COALESCE(title,'') FROM incidents WHERE id=$1`,
					incidentID).Scan(&incNum, &sev, &title)
				if incNum != "" {
					h.notificationSvc.SendRCAUpdateNotification(
						c.Request.Context(), incidentID, incNum, sev, title,
						req.RootCause, req.Confidence,
						req.ConfidenceBand, "OIE",
					)
				}
			}()
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RCACallback receives completion data from the RCA orchestrator and updates the incident.
func (h *IncidentHandler) RCACallback(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var req struct {
		InvestigationID string  `json:"investigation_id"`
		RootCause       string  `json:"root_cause"`
		Confidence      float64 `json:"confidence"`
		Status          string  `json:"status"` // "completed" | "failed"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	rcaStatus := req.Status
	if rcaStatus == "" {
		rcaStatus = "completed"
	}
	// Guard: if orchestrator claims "completed" but sent no root cause, it means the
	// LLM was unreachable or returned empty content. Store "failed" so the UI shows
	// "RCA Failed" instead of "RCA Done" with no content.
	if rcaStatus == "completed" && strings.TrimSpace(req.RootCause) == "" {
		rcaStatus = "failed"
	}
	// Default confidence when orchestrator omits the field (zero value).
	// A completed RCA with non-empty root cause earns at least 0.70 confidence.
	confidence := req.Confidence
	if confidence == 0 && rcaStatus == "completed" {
		confidence = 0.70
	}

	if h.db != nil {
		_, err = h.db.ExecContext(c.Request.Context(), `
			UPDATE incidents
			SET rca_investigation_id = $1,
			    rca_status            = $2,
			    rca_confidence         = $3,
			    ai_root_cause          = CASE WHEN $4 != '' THEN $4 ELSE ai_root_cause END,
			    updated_at             = $5
			WHERE id = $6
		`, req.InvestigationID, rcaStatus, confidence, req.RootCause, time.Now(), incidentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "DB update failed"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "RCA result stored"})
}

// GetIncidentAlerts returns the full alert details for all alerts in an incident.
func (h *IncidentHandler) GetIncidentAlerts(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	alerts, err := h.incidentService.GetIncidentAlerts(c.Request.Context(), incidentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"alerts": alerts,
			"total":  len(alerts),
		},
	})
}

// FloodgateRCA runs an RCA investigation using a Floodgate-proxied Claude model.
// POST /api/v1/incidents/:id/rca/floodgate
// Body: { "model": "claude-sonnet-4-6" | "claude-opus-4-7", "token": "<oauth-id-token>" }
func (h *IncidentHandler) FloodgateRCA(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var req struct {
		Model string `json:"model" binding:"required"`
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "model and token are required"})
		return
	}

	// Validate model selection
	allowedModels := map[string]string{
		"claude-sonnet-4-6": "claude-sonnet-4-6-20250514",
		"claude-opus-4-7":   "claude-opus-4-7-20251101",
	}
	claudeModel, ok := allowedModels[req.Model]
	if !ok {
		// Allow full model IDs to pass through
		claudeModel = req.Model
	}

	// Fetch the incident directly from DB (no RBAC — user already authenticated via JWT)
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Database unavailable"})
		return
	}

	var title, description, severity, topologyPath, aiRootCause string
	err = h.db.QueryRowContext(c.Request.Context(), `
		SELECT title, COALESCE(description,''), COALESCE(severity,'medium'),
		       COALESCE(topology_path,''), COALESCE(ai_root_cause,'')
		FROM incidents WHERE id = $1
	`, incidentID).Scan(&title, &description, &severity, &topologyPath, &aiRootCause)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Incident not found"})
		return
	}

	// Build RCA prompt
	var promptBuf strings.Builder
	promptBuf.WriteString("You are a senior SRE performing root cause analysis. ")
	promptBuf.WriteString("Given the following incident details, provide a clear, concise root cause analysis in 3-5 sentences. ")
	promptBuf.WriteString("Be specific and technical. Include the likely root cause, contributing factors, and immediate remediation steps.\n\n")
	promptBuf.WriteString(fmt.Sprintf("Incident Title: %s\n", title))
	promptBuf.WriteString(fmt.Sprintf("Severity: %s\n", severity))
	if description != "" {
		promptBuf.WriteString(fmt.Sprintf("Description: %s\n", description))
	}
	if topologyPath != "" {
		promptBuf.WriteString(fmt.Sprintf("Topology/Affected Path: %s\n", topologyPath))
	}
	promptBuf.WriteString("\nProvide:\n1. Root cause (2-3 sentences)\n2. Contributing factors (1-2 sentences)\n3. Immediate remediation steps (numbered list)")

	// Call Floodgate with the user-provided token
	result, err := callFloodgateClaude(c.Request.Context(), req.Token, claudeModel, promptBuf.String())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": fmt.Sprintf("Floodgate error: %v", err)})
		return
	}

	// Persist the result
	_, dbErr := h.db.ExecContext(c.Request.Context(), `
		UPDATE incidents
		SET ai_root_cause = $1,
		    rca_status = 'completed',
		    rca_confidence = GREATEST(COALESCE(rca_confidence, 0), 0.72),
		    updated_at = NOW()
		WHERE id = $2
	`, result, incidentID)
	if dbErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to save RCA result"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"root_cause": result,
		"model":      req.Model,
	})
}

// callFloodgateClaude sends a prompt to Claude via the Floodgate proxy using the given user token.
func callFloodgateClaude(ctx context.Context, token, model, prompt string) (string, error) {
	type claudeContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type claudeMsg struct {
		Role    string          `json:"role"`
		Content []claudeContent `json:"content"`
	}
	type claudeReq struct {
		Model     string      `json:"model"`
		MaxTokens int         `json:"max_tokens"`
		System    string      `json:"system"`
		Messages  []claudeMsg `json:"messages"`
	}
	type claudeRespContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type claudeResp struct {
		Content []claudeRespContent `json:"content"`
	}

	body := claudeReq{
		Model:     model,
		MaxTokens: 2048,
		System:    "You are a senior SRE and reliability engineer with deep expertise in Kubernetes, distributed systems, and observability.",
		Messages: []claudeMsg{
			{Role: "user", Content: []claudeContent{{Type: "text", Text: prompt}}},
		},
	}

	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"/api/anthropic/v1/messages",
		bytes.NewBuffer(payload),
	)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("User-Agent", "AlertHub/1.0")

	client := &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Floodgate returned %d: %s", resp.StatusCode, string(raw))
	}

	var cr claudeResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("failed to parse Floodgate response: %w", err)
	}

	var text string
	for _, block := range cr.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return strings.TrimSpace(text), nil
}

// FloodgateTestToken validates a Floodgate OAuth token with a minimal Claude API call.
// POST /api/v1/incidents/floodgate-token-test
// Body: { "token": "<oauth-id-token>" }
func (h *IncidentHandler) FloodgateTestToken(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "token is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	_, err := callFloodgateClaude(ctx, req.Token, "claude-sonnet-4-6-20250514", "Reply with exactly: ok")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"valid":   false,
			"message": fmt.Sprintf("Token validation failed: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"valid":   true,
		"message": "Token is valid — Floodgate Claude is reachable",
	})
}

// GetInvestigationDAG returns the generated investigation playbook for an incident.
// The playbook is produced by enrichIncidentV2 once RCA engines complete; it may
// not be available immediately after incident creation.
func (h *IncidentHandler) GetInvestigationDAG(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var domain, rootEntity string
	var stepsJSON, stepStatesJSON []byte
	var generatedAt, updatedAt time.Time

	err = h.db.QueryRowContext(c.Request.Context(), `
		SELECT domain, root_entity, steps,
		       COALESCE(step_states, '{}'),
		       generated_at, updated_at
		FROM incident_investigation_dags
		WHERE incident_id = $1`,
		incidentID,
	).Scan(&domain, &rootEntity, &stepsJSON, &stepStatesJSON, &generatedAt, &updatedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusOK, gin.H{
			"success":     true,
			"available":   false,
			"incident_id": incidentID,
			"message":     "Investigation guide not yet available — RCA engines may still be running",
		})
		return
	}
	if err != nil {
		log.Printf("GetInvestigationDAG: db error incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
		return
	}

	// Ensure stepsJSON is never null in the response — use empty array as fallback
	if len(stepsJSON) == 0 {
		stepsJSON = []byte("[]")
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"available":    true,
		"incident_id":  incidentID,
		"domain":       domain,
		"root_entity":  rootEntity,
		"steps":        json.RawMessage(stepsJSON),
		"step_states":  json.RawMessage(stepStatesJSON),
		"generated_at": generatedAt,
		"updated_at":   updatedAt,
	})
}

// UpdateInvestigationStep marks a single investigation step as
// pending / in_progress / done / skipped.
func (h *IncidentHandler) UpdateInvestigationStep(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}
	stepID := c.Param("step_id")
	if stepID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "step_id is required"})
		return
	}

	var body struct {
		State string `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "state is required"})
		return
	}
	validStates := map[string]bool{
		"pending": true, "in_progress": true, "done": true, "skipped": true,
	}
	if !validStates[body.State] {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "state must be: pending | in_progress | done | skipped",
		})
		return
	}

	// ARRAY[$1] is the jsonb_set path: a single-element text array containing the step ID.
	// The value is a JSON string literal (e.g. "done") — must be quoted.
	result, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE incident_investigation_dags
		SET step_states = jsonb_set(
			COALESCE(step_states, '{}'),
			ARRAY[$1],
			$2::jsonb
		)
		WHERE incident_id = $3`,
		stepID,
		fmt.Sprintf(`"%s"`, body.State),
		incidentID,
	)
	if err != nil {
		log.Printf("UpdateInvestigationStep: db error incident=%s step=%s: %v",
			incidentID, stepID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "No investigation DAG found for this incident",
		})
		return
	}
	c.Status(http.StatusNoContent)
}

// GetCorrelationExplanation returns the correlation evidence breakdown for every
// alert in an incident. Each record is the ExplainabilityReport stored by
// enrichIncidentV2 in pipeline_correlation_results.explanation_json.
func (h *IncidentHandler) GetCorrelationExplanation(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	// DISTINCT ON (a.id) keeps only the most recent enriched row per alert.
	// The initial sparse row (explanation_json populated from V1 path) is overwritten
	// by the V2 enrichment pass, but both rows exist until cleanup.
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT DISTINCT ON (a.id)
			a.id::text            AS alert_id,
			a.title               AS alert_title,
			a.severity,
			pcr.final_score,
			pcr.decision,
			pcr.dominant_strategy,
			pcr.explanation_json,
			pcr.processed_at
		FROM alerts a
		JOIN pipeline_correlation_results pcr ON pcr.alert_id = a.id
		WHERE a.incident_id = $1
		  AND pcr.explanation_json IS NOT NULL
		ORDER BY a.id, pcr.processed_at DESC`,
		incidentID,
	)
	if err != nil {
		log.Printf("GetCorrelationExplanation: db error incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
		return
	}
	defer rows.Close()

	type alertExplanation struct {
		AlertID     string          `json:"alert_id"`
		Title       string          `json:"alert_title"`
		Severity    string          `json:"severity"`
		FinalScore  float64         `json:"final_score"`
		Decision    string          `json:"decision"`
		Strategy    string          `json:"dominant_strategy"`
		Explanation json.RawMessage `json:"explanation"`
		ProcessedAt time.Time       `json:"processed_at"`
	}

	results := make([]alertExplanation, 0) // never return null
	for rows.Next() {
		var ae alertExplanation
		var expJSON []byte
		if err := rows.Scan(
			&ae.AlertID, &ae.Title, &ae.Severity,
			&ae.FinalScore, &ae.Decision, &ae.Strategy,
			&expJSON, &ae.ProcessedAt,
		); err != nil {
			log.Printf("GetCorrelationExplanation: scan error incident=%s: %v", incidentID, err)
			continue
		}
		ae.Explanation = json.RawMessage(expJSON)
		results = append(results, ae)
	}
	if err := rows.Err(); err != nil {
		log.Printf("GetCorrelationExplanation: rows error incident=%s: %v", incidentID, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"incident_id":  incidentID,
		"explanations": results,
		"total":        len(results),
	})
}

// GetRCADecisions returns ranked RCA hypotheses for an incident from the rca_decisions table.
func (h *IncidentHandler) GetRCADecisions(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var rootEntityLabel, rootEntityType, confidenceBand string
	var confidence float64
	var hypsJSON []byte
	var createdAt time.Time
	var operatorVerdict, actualRootCause sql.NullString

	err = h.db.QueryRowContext(c.Request.Context(), `
		SELECT root_entity_label, root_entity_type, confidence, confidence_band,
		       hypotheses, created_at, operator_verdict, actual_root_cause
		FROM rca_decisions
		WHERE incident_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, incidentID,
	).Scan(&rootEntityLabel, &rootEntityType, &confidence, &confidenceBand,
		&hypsJSON, &createdAt, &operatorVerdict, &actualRootCause)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"available": false,
			"message":   "RCA not yet computed — engines may still be running",
		})
		return
	}
	if err != nil {
		log.Printf("GetRCADecisions: db error incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"available":         true,
		"incident_id":       incidentID,
		"root_entity_label": rootEntityLabel,
		"root_entity_type":  rootEntityType,
		"confidence":        confidence,
		"confidence_band":   confidenceBand,
		"hypotheses":        json.RawMessage(hypsJSON),
		"generated_at":      createdAt,
		"operator_verdict":  operatorVerdict.String,
		"actual_root_cause": actualRootCause.String,
	})
}

// SubmitRCAVerdict lets operators confirm or correct the RCA result.
func (h *IncidentHandler) SubmitRCAVerdict(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var body struct {
		Verdict         string `json:"verdict" binding:"required"`
		ActualRootCause string `json:"actual_root_cause"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "verdict is required"})
		return
	}
	valid := map[string]bool{"CONFIRMED": true, "WRONG": true, "PARTIAL": true}
	if !valid[body.Verdict] {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "verdict must be CONFIRMED | WRONG | PARTIAL",
		})
		return
	}

	res, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE rca_decisions
		SET operator_verdict  = $1,
		    actual_root_cause = $2,
		    verdict_at        = NOW()
		WHERE incident_id = $3`, body.Verdict, body.ActualRootCause, incidentID)
	if err != nil {
		log.Printf("SubmitRCAVerdict: db error incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "No RCA decision found for this incident"})
		return
	}
	c.Status(http.StatusNoContent)
}

// SubmitIncidentFeedback records operator correlation feedback for a specific incident.
func (h *IncidentHandler) SubmitIncidentFeedback(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid incident ID"})
		return
	}

	var body struct {
		FeedbackType string `json:"feedback_type" binding:"required"`
		Notes        string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "feedback_type is required"})
		return
	}
	valid := map[string]bool{
		"confirmed": true, "false_positive": true,
		"split": true, "missed_correlation": true,
	}
	if !valid[body.FeedbackType] {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "feedback_type must be: confirmed | false_positive | split | missed_correlation",
		})
		return
	}

	if h.feedbackSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Feedback service not available"})
		return
	}

	// Resolve the correlation_id for this incident so the feedback service can
	// update the right correlation decision row.
	var correlationID, dominantStrategy string
	var alertID uuid.UUID
	var strategyScoresJSON []byte
	_ = h.db.QueryRowContext(c.Request.Context(), `
		SELECT pcr.correlation_id, pcr.alert_id,
		       pcr.dominant_strategy, pcr.strategy_scores
		FROM pipeline_correlation_results pcr
		JOIN alerts a ON a.id = pcr.alert_id
		WHERE a.incident_id = $1
		ORDER BY pcr.processed_at DESC
		LIMIT 1`, incidentID,
	).Scan(&correlationID, &alertID, &dominantStrategy, &strategyScoresJSON)

	userIDVal, _ := c.Get("user_id")
	operatorID, _ := userIDVal.(uuid.UUID)

	var stratScores map[string]float64
	json.Unmarshal(strategyScoresJSON, &stratScores)

	fb := &correlation.CorrelationFeedback{
		AlertID:          alertID,
		IncidentID:       &incidentID,
		CorrelationID:    correlationID,
		FeedbackType:     correlation.FeedbackType(body.FeedbackType),
		DominantStrategy: dominantStrategy,
		StrategyScores:   stratScores,
		Notes:            body.Notes,
		OperatorID:       &operatorID,
	}
	if err := h.feedbackSvc.RecordFeedback(c.Request.Context(), fb); err != nil {
		log.Printf("SubmitIncidentFeedback: record failed incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to record feedback"})
		return
	}
	c.Status(http.StatusNoContent)
}

// RegisterRoutes registers incident routes
func (h *IncidentHandler) RegisterRoutes(router *gin.RouterGroup) {
	incidents := router.Group("/incidents")
	{
		incidents.GET("", h.ListIncidents)
		incidents.POST("", h.CreateIncident)
		incidents.GET("/stats", h.GetIncidentStats)
		incidents.POST("/floodgate-token-test", h.FloodgateTestToken)
		incidents.POST("/auto-close", h.AutoCloseResolved)
		incidents.GET("/:id", h.GetIncident)
		incidents.PUT("/:id", h.UpdateIncident)
		incidents.POST("/:id/resolve", h.ResolveIncident)
		incidents.POST("/:id/assign", h.AssignIncident)
		incidents.GET("/:id/timeline", h.GetIncidentTimeline)
		incidents.POST("/:id/timeline", h.AddTimelineEvent)
		incidents.POST("/:id/rca", h.PerformRCA)
		incidents.POST("/:id/rca/floodgate", h.FloodgateRCA)
		incidents.GET("/:id/alerts", h.GetIncidentAlerts)
		incidents.GET("/:id/investigation", h.GetInvestigationDAG)
		incidents.PATCH("/:id/investigation/steps/:step_id", h.UpdateInvestigationStep)
		incidents.GET("/:id/explanation", h.GetCorrelationExplanation)
		incidents.GET("/:id/rca-decisions", h.GetRCADecisions)
		incidents.POST("/:id/rca-decisions/verdict", h.SubmitRCAVerdict)
		incidents.POST("/:id/feedback", h.SubmitIncidentFeedback)
		// NOTE: /:id/rca-callback is registered on the public v1 group with InternalServiceAuth in cmd/main.go
	}
}
