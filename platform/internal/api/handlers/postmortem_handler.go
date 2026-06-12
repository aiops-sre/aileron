package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/postmortems"
)

// PostmortemHandler serves GET and POST (re-generate) for incident postmortems.
type PostmortemHandler struct {
	svc *postmortems.PostmortemService
}

func NewPostmortemHandler(svc *postmortems.PostmortemService) *PostmortemHandler {
	return &PostmortemHandler{svc: svc}
}

// GetPostmortem handles GET /incidents/:id/postmortem.
func (h *PostmortemHandler) GetPostmortem(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid incident ID"})
		return
	}
	pm, err := h.svc.GetForIncident(c.Request.Context(), incidentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pm == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no postmortem available — incident may not be resolved yet"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"postmortem": pm})
}

// GeneratePostmortem handles POST /incidents/:id/postmortem/generate.
// Re-generates the postmortem for a resolved incident on demand.
func (h *PostmortemHandler) GeneratePostmortem(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid incident ID"})
		return
	}
	if err := h.svc.GenerateForIncident(c.Request.Context(), incidentID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pm, _ := h.svc.GetForIncident(c.Request.Context(), incidentID)
	c.JSON(http.StatusOK, gin.H{"postmortem": pm, "message": "postmortem generated"})
}
