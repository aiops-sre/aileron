package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/incidents"
)

// HCLIncidentHandler handles External incident operations via IncidentManager API
type HCLIncidentHandler struct {
	incident_managerClient *incidents.IncidentManagerClient
}

// NewHCLIncidentHandler creates a new External incident handler
func NewHCLIncidentHandler() *HCLIncidentHandler {
	return &HCLIncidentHandler{
		incident_managerClient: incidents.NewIncidentManagerClient(),
	}
}

// CreateHCLIncident creates a new External incident via IncidentManager API
func (h *HCLIncidentHandler) CreateHCLIncident(c *gin.Context) {
	var req incidents.CreateIncidentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	resp, err := h.incident_managerClient.CreateIncident(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create External incident: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "External incident created successfully",
		"data":    resp.Result.Data,
	})
}

// hclQueryFrontendRequest is the payload the frontend IncidentManagerService sends
type hclQueryFrontendRequest struct {
	Module  string `json:"module"`
	Number  string `json:"number"`
	Query   string `json:"query"`
	Count   int    `json:"count"`
	Offset  int    `json:"offset"`
	Filters *struct {
		AssignmentGroup string `json:"assignment_group"`
		UBusinessOrg    string `json:"u_business_org"`
	} `json:"filters"`
	Pagination *struct {
		Offset int `json:"offset"`
		Count  int `json:"count"`
	} `json:"pagination"`
	DateRange *struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"dateRange"`
	SortOrder   string `json:"sortOrder"`
	SortByField string `json:"sortByField"`
}

// QueryHCLIncidents queries External incidents via IncidentManager API
func (h *HCLIncidentHandler) QueryHCLIncidents(c *gin.Context) {
	var body hclQueryFrontendRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Build the IncidentManager query string.
	// Always scope to aileron-admins — append any extra filters from the request.
	query := body.Query
	if query == "" {
		parts := []string{"assignment_group.name=aileron-admins"}

		if body.DateRange != nil && body.DateRange.Start != "" {
			start, err := time.Parse(time.RFC3339Nano, body.DateRange.Start)
			if err != nil {
				start, err = time.Parse(time.RFC3339, body.DateRange.Start)
			}
			if err == nil {
				parts = append(parts, fmt.Sprintf("sys_created_on>%s", start.Format("2006-01-02")))
			}
		}
		if body.DateRange != nil && body.DateRange.End != "" {
			end, err := time.Parse(time.RFC3339Nano, body.DateRange.End)
			if err != nil {
				end, err = time.Parse(time.RFC3339, body.DateRange.End)
			}
			if err == nil {
				parts = append(parts, fmt.Sprintf("sys_created_on<%s", end.Format("2006-01-02")))
			}
		}

		if len(parts) == 1 {
			// no date range — default to last 30 days
			parts = append(parts, "sys_created_on>javascript:gs.beginningOfLast30Days()")
		}

		// Encode sort directly in the query — more reliable than the sortByField parameter
		// when combined with offset-based pagination.
		parts = append(parts, "ORDERBYDESCsys_created_on")

		query = strings.Join(parts, "^")
	}

	// Resolve count/offset from either flat fields or nested pagination
	count := body.Count
	offset := body.Offset
	if body.Pagination != nil {
		if body.Pagination.Count > 0 {
			count = body.Pagination.Count
		}
		offset = body.Pagination.Offset
	}
	if count == 0 {
		count = 100
	}
	// IncidentManager hard-caps count at 100; sending more triggers a warning state
	if count > 100 {
		count = 100
	}

	sortOrder := body.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	req := &incidents.QueryIncidentsRequest{
		Module:      "incident",
		Number:      body.Number,
		Query:       query,
		Count:       count,
		Offset:      offset,
		SortOrder:   sortOrder,
		SortByField: body.SortByField,
	}

	resp, err := h.incident_managerClient.QueryIncidents(c.Request.Context(), req)
	if err != nil {
		log.Printf("[HCL] IncidentManager query failed: %v", err)
		// Degrade gracefully so the UI still shows local incidents
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "External incidents unavailable: " + err.Error(),
			"result": gin.H{
				"data": []interface{}{},
				"meta": gin.H{"queryResultCount": 0, "resultCount": 0},
			},
		})
		return
	}

	log.Printf("[HCL] IncidentManager query success: resultCount=%d queryResultCount=%d",
		resp.Result.Meta.ResultCount, resp.Result.Meta.QueryResultCount)
	c.JSON(http.StatusOK, resp)
}

// GetHCLIncident gets a specific External incident by number via IncidentManager API
func (h *HCLIncidentHandler) GetHCLIncident(c *gin.Context) {
	number := c.Param("number")
	if number == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Incident number is required",
		})
		return
	}

	req := &incidents.QueryIncidentsRequest{
		Module: "incident",
		Number: number,
		Query:  "",
		Count:  1,
		Offset: 0,
	}

	resp, err := h.incident_managerClient.QueryIncidents(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get External incident: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// UpdateHCLIncident updates an External incident via IncidentManager API
func (h *HCLIncidentHandler) UpdateHCLIncident(c *gin.Context) {
	ticketID := c.Param("ticketId")
	if ticketID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Ticket ID is required",
		})
		return
	}

	var req incidents.UpdateIncidentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	req.TicketID = ticketID

	resp, err := h.incident_managerClient.UpdateIncident(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update External incident: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "External incident updated successfully",
		"data":    resp.Result.Data,
	})
}

// ReopenHCLIncident reopens an External incident via IncidentManager API
func (h *HCLIncidentHandler) ReopenHCLIncident(c *gin.Context) {
	number := c.Param("number")
	if number == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Incident number is required",
		})
		return
	}

	var req incidents.ReopenIncidentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	req.Number = number
	req.Action = "reopen"
	req.Module = "incident"

	resp, err := h.incident_managerClient.ReopenIncident(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to reopen External incident: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "External incident reopened successfully",
		"data":    resp.Result.Data,
	})
}

// RegisterRoutes registers External incident routes
func (h *HCLIncidentHandler) RegisterRoutes(router *gin.RouterGroup) {
	hcl := router.Group("/incidents/hcl")
	{
		hcl.POST("/create", h.CreateHCLIncident)
		hcl.POST("/query", h.QueryHCLIncidents)
		hcl.GET("/:number", h.GetHCLIncident)
		hcl.PUT("/:ticketId", h.UpdateHCLIncident)
		hcl.POST("/:number/reopen", h.ReopenHCLIncident)
	}
}
