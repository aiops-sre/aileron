package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	appinv "github.com/aileron-platform/aileron/platform/services/oie/internal/application/investigation"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/ports/http/dto"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// Handler provides HTTP endpoints for the Investigation Manager.
type Handler struct {
	manager appinv.Service
	repo    domain.Repository
	logger  *slog.Logger
}

// NewHandler constructs an HTTP handler with the given service dependencies.
func NewHandler(manager appinv.Service, repo domain.Repository, logger *slog.Logger) *Handler {
	if manager == nil {
		panic("http handler: manager is required")
	}
	if repo == nil {
		panic("http handler: repo is required")
	}
	return &Handler{manager: manager, repo: repo, logger: logger}
}

// RegisterRoutes wires all investigation routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	inv := rg.Group("/investigations")
	inv.POST("", h.StartInvestigation)
	inv.GET("", h.ListActiveInvestigations)
	inv.GET("/:id", h.GetInvestigation)
	inv.POST("/:id/cancel", h.CancelInvestigation)
	inv.GET("/:id/events", h.GetInvestigationEvents)
	inv.GET("/:id/stream", h.StreamInvestigationProgress)

	rg.GET("/incidents/:incident_id/investigation", h.GetInvestigationByIncident)
	rg.GET("/incidents/:incident_id/investigation/stream", h.StreamIncidentInvestigation)
}

// StartInvestigation handles POST /investigations.
func (h *Handler) StartInvestigation(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.start")
	defer span.End()

	var req dto.StartInvestigationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	span.SetAttributes(
		attribute.String("incident.id", req.IncidentID.String()),
		attribute.String("severity", req.Severity),
	)

	resp, err := h.manager.Start(ctx, &appinv.StartRequest{
		IncidentID:        req.IncidentID,
		IncidentNumber:    req.IncidentNumber,
		IdempotencyKey:    req.IdempotencyKey,
		Severity:          req.Severity,
		IncidentStartedAt: req.IncidentStartedAt,
		RootEntityID:      req.RootEntityID,
		RootEntityType:    req.RootEntityType,
		FailureClass:      req.FailureClass,
		Domain:            req.Domain,
		PlaybookID:        req.PlaybookID,
		TopologyPath:      req.TopologyPath,
		CorrelationID:     req.CorrelationID,
	})
	if err != nil {
		switch err.(type) {
		case domain.ErrInvalidInput:
			h.renderError(c, http.StatusBadRequest, "invalid_input", err.Error())
		default:
			h.logger.ErrorContext(ctx, "failed to start investigation", "error", err)
			h.renderError(c, http.StatusInternalServerError, "internal_error", "failed to start investigation")
		}
		return
	}

	statusCode := http.StatusCreated
	if resp.AlreadyExisted {
		statusCode = http.StatusOK
	}
	metrics.HTTPRequestsTotal.WithLabelValues("POST", "/investigations", fmt.Sprintf("%d", statusCode)).Inc()
	c.JSON(statusCode, dto.StartInvestigationResponse{
		InvestigationID: resp.InvestigationID,
		Status:          string(resp.Status),
		AlreadyExisted:  resp.AlreadyExisted,
	})
}

// GetInvestigation handles GET /investigations/:id.
func (h *Handler) GetInvestigation(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.get")
	defer span.End()

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_id", "investigation id must be a valid UUID")
		return
	}
	span.SetAttributes(attribute.String("investigation.id", id.String()))

	inv, err := h.manager.GetByID(ctx, id)
	if err != nil {
		switch err.(type) {
		case domain.ErrInvestigationNotFound:
			h.renderError(c, http.StatusNotFound, "not_found", fmt.Sprintf("investigation %s not found", id))
		default:
			h.logger.ErrorContext(ctx, "failed to get investigation", "id", id, "error", err)
			h.renderError(c, http.StatusInternalServerError, "internal_error", "failed to retrieve investigation")
		}
		return
	}

	statusCode := http.StatusOK
	if !inv.Status.IsTerminal() {
		statusCode = http.StatusAccepted
	}
	metrics.HTTPRequestsTotal.WithLabelValues("GET", "/investigations/:id", fmt.Sprintf("%d", statusCode)).Inc()
	c.JSON(statusCode, dto.FromDomain(inv))
}

// CancelInvestigation handles POST /investigations/:id/cancel.
func (h *Handler) CancelInvestigation(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.cancel")
	defer span.End()

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_id", "investigation id must be a valid UUID")
		return
	}

	var req dto.CancelInvestigationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	if err := h.manager.Cancel(ctx, id, req.Reason); err != nil {
		switch err.(type) {
		case domain.ErrInvestigationNotFound:
			h.renderError(c, http.StatusNotFound, "not_found", fmt.Sprintf("investigation %s not found", id))
		default:
			h.logger.ErrorContext(ctx, "failed to cancel investigation", "id", id, "error", err)
			h.renderError(c, http.StatusInternalServerError, "internal_error", "failed to cancel investigation")
		}
		return
	}

	metrics.HTTPRequestsTotal.WithLabelValues("POST", "/investigations/:id/cancel", "200").Inc()
	c.JSON(http.StatusOK, gin.H{"cancelled": true})
}

// GetInvestigationByIncident handles GET /incidents/:incident_id/investigation.
func (h *Handler) GetInvestigationByIncident(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.get_by_incident")
	defer span.End()

	incidentID, err := uuid.Parse(c.Param("incident_id"))
	if err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_id", "incident id must be a valid UUID")
		return
	}

	inv, err := h.manager.GetByIncidentID(ctx, incidentID)
	if err != nil {
		switch err.(type) {
		case domain.ErrInvestigationNotFound:
			h.renderError(c, http.StatusNotFound, "not_found",
				fmt.Sprintf("no investigation found for incident %s", incidentID))
		default:
			h.logger.ErrorContext(ctx, "failed to get investigation by incident",
				"incident_id", incidentID, "error", err)
			h.renderError(c, http.StatusInternalServerError, "internal_error", "failed to retrieve investigation")
		}
		return
	}
	c.JSON(http.StatusOK, dto.FromDomain(inv))
}

// GetInvestigationEvents handles GET /investigations/:id/events?since_seq=N.
func (h *Handler) GetInvestigationEvents(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.get_events")
	defer span.End()

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_id", "investigation id must be a valid UUID")
		return
	}

	var afterSeq int64
	if seqStr := c.Query("since_seq"); seqStr != "" {
		if _, err := fmt.Sscanf(seqStr, "%d", &afterSeq); err != nil {
			h.renderError(c, http.StatusBadRequest, "invalid_seq", "since_seq must be an integer")
			return
		}
	}

	events, err := h.repo.GetEvents(ctx, id, afterSeq)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get investigation events", "id", id, "error", err)
		h.renderError(c, http.StatusInternalServerError, "internal_error", "failed to retrieve events")
		return
	}

	results := make([]dto.InvestigationEventResponse, 0, len(events))
	for _, ev := range events {
		var payload any
		_ = json.Unmarshal(ev.Payload, &payload)
		results = append(results, dto.InvestigationEventResponse{
			SequenceNum: ev.SequenceNum,
			EventType:   ev.EventType,
			Payload:     payload,
			CreatedAt:   ev.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"events": results, "count": len(results)})
}

// ListActiveInvestigations handles GET /investigations.
func (h *Handler) ListActiveInvestigations(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.list")
	defer span.End()

	invs, err := h.manager.ListActive(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list active investigations", "error", err)
		h.renderError(c, http.StatusInternalServerError, "internal_error", "failed to list investigations")
		return
	}

	results := make([]*dto.InvestigationResponse, 0, len(invs))
	for _, inv := range invs {
		results = append(results, dto.FromDomain(inv))
	}
	c.JSON(http.StatusOK, gin.H{"investigations": results, "count": len(results)})
}

// Healthz is the liveness probe handler.
func (h *Handler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC()})
}

// Readyz is the readiness probe handler.
func (h *Handler) Readyz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

func (h *Handler) renderError(c *gin.Context, status int, code string, message string) {
	metrics.HTTPRequestsTotal.WithLabelValues(c.Request.Method, c.FullPath(), fmt.Sprintf("%d", status)).Inc()
	c.JSON(status, dto.ErrorResponse{Error: message, Code: code})
}

// StreamInvestigationProgress handles GET /investigations/:id/stream (SSE).
// Streams investigation lifecycle events as Server-Sent Events.
// Event types: investigation.started, investigation.evidence_gathered,
// investigation.hypothesis_evaluated, investigation.completed, investigation.failed.
// HolmesGPT pattern: operators see START_TOOL / TOOL_RESULT / ANSWER_END in real time.
func (h *Handler) StreamInvestigationProgress(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.stream")
	defer span.End()

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_id", "invalid investigation ID")
		return
	}
	span.SetAttributes(attribute.String("investigation.id", id.String()))

	// Verify investigation exists.
	inv, err := h.repo.GetByID(ctx, id)
	if err != nil {
		h.renderError(c, http.StatusNotFound, "not_found", "investigation not found")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	clientGone := c.Request.Context().Done()

	// Send the current state immediately.
	writeSSEEvent(c, "investigation.state", map[string]interface{}{
		"investigation_id": inv.ID,
		"incident_id":      inv.IncidentID,
		"status":           inv.Status,
		"severity":         inv.Severity,
		"playbook_id":      inv.PlaybookID,
	})

	if inv.Status.IsTerminal() {
		// Already done — send final event and close.
		writeSSEEvent(c, "investigation."+string(inv.Status), map[string]interface{}{
			"investigation_id": inv.ID,
			"status":           inv.Status,
		})
		c.Writer.Flush()
		return
	}

	// Poll for status changes with 2s interval, up to 120s (the max investigation budget).
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()

	lastStatus := inv.Status
	for {
		select {
		case <-clientGone:
			return
		case <-deadline.C:
			writeSSEEvent(c, "investigation.timeout", map[string]interface{}{
				"investigation_id": id,
				"message":          "stream timeout — poll /investigations/:id for final result",
			})
			c.Writer.Flush()
			return
		case <-ticker.C:
			current, err := h.repo.GetByID(ctx, id)
			if err != nil {
				return
			}
			if current.Status != lastStatus {
				eventType := "investigation." + string(current.Status)
				payload := map[string]interface{}{
					"investigation_id": current.ID,
					"incident_id":      current.IncidentID,
					"status":           current.Status,
					"severity":         current.Severity,
				}
				if current.Status.IsTerminal() {
					if current.Confidence != nil {
						payload["confidence"] = *current.Confidence
					}
					if current.ConfidenceBand != nil {
						payload["confidence_band"] = *current.ConfidenceBand
					}
					if current.RootCauseSummary != nil {
						payload["root_cause"] = *current.RootCauseSummary
					}
					payload["evidence_count"] = current.EvidenceGathered
					payload["hypotheses_generated"] = current.HypothesesGenerated
					payload["hypotheses_rejected"] = current.HypothesesRejected
				}
				writeSSEEvent(c, eventType, payload)
				c.Writer.Flush()
				lastStatus = current.Status
			}
			if current.Status.IsTerminal() {
				return
			}
		}
	}
}

// StreamIncidentInvestigation handles GET /incidents/:incident_id/investigation/stream (SSE).
// Convenience endpoint that looks up the investigation by incident ID and streams it.
func (h *Handler) StreamIncidentInvestigation(c *gin.Context) {
	ctx, span := tracing.Tracer().Start(c.Request.Context(), "http.investigation.stream_incident")
	defer span.End()

	incidentID, err := uuid.Parse(c.Param("incident_id"))
	if err != nil {
		h.renderError(c, http.StatusBadRequest, "invalid_id", "invalid incident ID")
		return
	}
	span.SetAttributes(attribute.String("incident.id", incidentID.String()))

	inv, err := h.repo.GetByIncidentID(ctx, incidentID)
	if err != nil {
		// No investigation yet — send a pending event so the frontend can show "queued".
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		writeSSEEvent(c, "investigation.none", map[string]interface{}{
			"incident_id": incidentID,
			"message":     "no investigation started for this incident yet",
		})
		c.Writer.Flush()
		return
	}

	// Reuse the existing stream handler with the investigation ID.
	c.Params = append(c.Params, gin.Param{Key: "id", Value: inv.ID.String()})
	h.StreamInvestigationProgress(c)
}

// writeSSEEvent writes a single SSE event to the response writer.
func writeSSEEvent(c *gin.Context, eventType string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, string(data))
}
