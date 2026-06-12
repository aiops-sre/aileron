package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/workflows"
)

// workflowTemplate is the in-memory representation of a built-in template.
type workflowTemplate struct {
	ID          uuid.UUID              `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Category    string                 `json:"category"`
	Tags        []string               `json:"tags"`
	UsageCount  int                    `json:"usage_count"`
	Triggers    []map[string]interface{} `json:"triggers"`
	Steps       []map[string]interface{} `json:"steps"`
	Parameters  []map[string]interface{} `json:"parameters"`
}

// builtinTemplates is the authoritative list of workflow templates.
// IDs are stable so the frontend can store them.
var builtinTemplates = []workflowTemplate{
	{
		ID:          uuid.MustParse("11111111-0000-0000-0000-000000000001"),
		Name:        "Alert Escalation Workflow",
		Description: "Escalates critical alerts to on-call team after a specified timeout",
		Category:    "alerting",
		Tags:        []string{"alerting", "escalation", "pagerduty"},
		UsageCount:  25,
		Triggers: []map[string]interface{}{
			{"type": "alert", "conditions": []map[string]interface{}{
				{"field": "severity", "operator": "equals", "value": "critical"},
			}},
		},
		Steps: []map[string]interface{}{
			{"id": "wait", "name": "Wait for acknowledgment", "type": "action",
				"action": map[string]interface{}{"type": "wait", "parameters": map[string]interface{}{"duration": "5m"}}},
			{"id": "check_status", "name": "Check alert status", "type": "condition",
				"condition": map[string]interface{}{"expression": "trigger.status == \"open\""}},
			{"id": "page_oncall", "name": "Page on-call team", "type": "action",
				"action": map[string]interface{}{"type": "pagerduty",
					"parameters": map[string]interface{}{"summary": "Critical alert: {{trigger.title}}", "severity": "critical"}}},
		},
		Parameters: []map[string]interface{}{
			{"name": "escalation_timeout", "type": "duration", "default": "5m", "description": "Time to wait before escalation"},
			{"name": "pagerduty_key", "type": "string", "required": true, "description": "PagerDuty integration key"},
		},
	},
	{
		ID:          uuid.MustParse("11111111-0000-0000-0000-000000000002"),
		Name:        "Incident Creation Workflow",
		Description: "Automatically creates incidents for high-severity alerts from critical services",
		Category:    "incident-management",
		Tags:        []string{"incident-management", "automation", "slack"},
		UsageCount:  18,
		Triggers: []map[string]interface{}{
			{"type": "alert", "conditions": []map[string]interface{}{
				{"field": "severity", "operator": "in", "value": []string{"critical", "high"}},
			}},
		},
		Steps: []map[string]interface{}{
			{"id": "notify_slack", "name": "Notify Slack", "type": "action",
				"action": map[string]interface{}{"type": "slack",
					"parameters": map[string]interface{}{"message": "Incident created: {{trigger.title}}"}}},
		},
		Parameters: []map[string]interface{}{
			{"name": "slack_webhook", "type": "string", "required": true, "description": "Slack webhook URL"},
			{"name": "incident_channel", "type": "string", "default": "#incidents", "description": "Slack channel"},
		},
	},
	{
		ID:          uuid.MustParse("11111111-0000-0000-0000-000000000003"),
		Name:        "Alert Correlation Workflow",
		Description: "Groups similar alerts and suppresses duplicates",
		Category:    "correlation",
		Tags:        []string{"correlation", "deduplication", "automation"},
		UsageCount:  12,
		Triggers: []map[string]interface{}{
			{"type": "alert"},
		},
		Steps: []map[string]interface{}{
			{"id": "correlate", "name": "Correlate alert", "type": "action",
				"action": map[string]interface{}{"type": "correlate", "parameters": map[string]interface{}{}}},
		},
		Parameters: []map[string]interface{}{},
	},
}

// WorkflowHandler handles workflow-related endpoints
type WorkflowHandler struct {
	workflowEngine *workflows.WorkflowEngine
	db             *sql.DB
}

// NewWorkflowHandler creates a new workflow handler
func NewWorkflowHandler(workflowEngine *workflows.WorkflowEngine, db *sql.DB) *WorkflowHandler {
	return &WorkflowHandler{
		workflowEngine: workflowEngine,
		db:             db,
	}
}

// RegisterRoutes registers workflow routes
func (h *WorkflowHandler) RegisterRoutes(router *gin.RouterGroup) {
	workflow := router.Group("/workflows")
	{
		workflow.GET("", h.ListWorkflows)
		workflow.POST("", h.CreateWorkflow)
		workflow.GET("/:id", h.GetWorkflow)
		workflow.PUT("/:id", h.UpdateWorkflow)
		workflow.DELETE("/:id", h.DeleteWorkflow)
		workflow.POST("/:id/execute", h.ExecuteWorkflow)
		workflow.POST("/:id/enable", h.EnableWorkflow)
		workflow.POST("/:id/disable", h.DisableWorkflow)

		workflow.GET("/:id/executions", h.ListWorkflowExecutions)
		workflow.GET("/:id/executions/:execution_id", h.GetWorkflowExecution)
		workflow.POST("/:id/executions/:execution_id/cancel", h.CancelWorkflowExecution)

		workflow.GET("/templates", h.ListWorkflowTemplates)
		workflow.GET("/templates/:id", h.GetWorkflowTemplate)
		workflow.POST("/templates/:id/create", h.CreateFromTemplate)
	}
}

// ListWorkflows lists all workflows
func (h *WorkflowHandler) ListWorkflows(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	enabled := c.Query("enabled")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	query := `SELECT w.id, w.name, w.description, w.enabled, w.triggers, w.created_at, w.updated_at,
	                 u.username as created_by,
	                 (SELECT COUNT(*) FROM workflow_executions we WHERE we.workflow_id = w.id) as executions,
	                 (SELECT MAX(we2.started_at) FROM workflow_executions we2 WHERE we2.workflow_id = w.id) as last_run
	          FROM workflows w LEFT JOIN users u ON w.created_by = u.id`
	args := []interface{}{}
	argIdx := 1
	if enabled != "" {
		query += ` WHERE w.enabled = $` + strconv.Itoa(argIdx)
		args = append(args, enabled == "true")
		argIdx++
	}
	query += ` ORDER BY w.created_at DESC LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	var total int
	countQ := "SELECT COUNT(*) FROM workflows"
	if enabled != "" {
		countQ += " WHERE enabled = $1"
		h.db.QueryRowContext(c.Request.Context(), countQ, enabled == "true").Scan(&total)
	} else {
		h.db.QueryRowContext(c.Request.Context(), countQ).Scan(&total)
	}

	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer rows.Close()

	type WorkflowRow struct {
		ID          string     `json:"id"`
		Name        string     `json:"name"`
		Description *string    `json:"description"`
		Enabled     bool       `json:"enabled"`
		Triggers    []byte     `json:"-"`
		CreatedAt   time.Time  `json:"created_at"`
		UpdatedAt   time.Time  `json:"updated_at"`
		CreatedBy   *string    `json:"created_by"`
		Executions  int        `json:"executions"`
		LastRun     *time.Time `json:"last_run"`
	}

	list := make([]map[string]interface{}, 0)
	for rows.Next() {
		var w WorkflowRow
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Enabled, &w.Triggers,
			&w.CreatedAt, &w.UpdatedAt, &w.CreatedBy, &w.Executions, &w.LastRun); err != nil {
			continue
		}
		var triggers interface{}
		if len(w.Triggers) > 0 {
			json.Unmarshal(w.Triggers, &triggers)
		}
		row := map[string]interface{}{
			"id": w.ID, "name": w.Name, "description": w.Description,
			"enabled": w.Enabled, "triggers": triggers,
			"created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
			"created_by": w.CreatedBy, "executions": w.Executions, "last_run": w.LastRun,
		}
		list = append(list, row)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"workflows": list, "total": total, "page": page, "limit": limit},
	})
}

// CreateWorkflow creates a new workflow
func (h *WorkflowHandler) CreateWorkflow(c *gin.Context) {
	var req struct {
		Name        string                      `json:"name" binding:"required"`
		Description string                      `json:"description"`
		Triggers    []workflows.WorkflowTrigger `json:"triggers" binding:"required"`
		Steps       []workflows.WorkflowStep    `json:"steps" binding:"required"`
		Enabled     bool                        `json:"enabled"`
		Tags        []string                    `json:"tags"`
		Metadata    map[string]interface{}      `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user ID from auth middleware context
	rawUserID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	userID, ok := rawUserID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user session"})
		return
	}

	workflow := &workflows.Workflow{
		Name:        req.Name,
		Description: req.Description,
		Triggers:    req.Triggers,
		Steps:       req.Steps,
		Enabled:     req.Enabled,
		Tags:        req.Tags,
		Metadata:    req.Metadata,
		CreatedBy:   userID,
	}

	err := h.workflowEngine.CreateWorkflow(c.Request.Context(), workflow)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    workflow,
	})
}

// GetWorkflow retrieves a specific workflow
func (h *WorkflowHandler) GetWorkflow(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	workflow, err := h.workflowEngine.GetWorkflow(c.Request.Context(), workflowID)
	if err != nil {
		if err == workflows.ErrWorkflowNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Workflow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    workflow,
	})
}

// UpdateWorkflow updates an existing workflow
func (h *WorkflowHandler) UpdateWorkflow(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	var req struct {
		Name        string                      `json:"name"`
		Description string                      `json:"description"`
		Triggers    []workflows.WorkflowTrigger `json:"triggers"`
		Steps       []workflows.WorkflowStep    `json:"steps"`
		Enabled     bool                        `json:"enabled"`
		Tags        []string                    `json:"tags"`
		Metadata    map[string]interface{}      `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	triggersJSON, _ := json.Marshal(req.Triggers)
	stepsJSON, _ := json.Marshal(req.Steps)
	metadataJSON, _ := json.Marshal(req.Metadata)
	tagsJSON, _ := json.Marshal(req.Tags)
	now := time.Now()

	var updatedWorkflow map[string]interface{}
	err = h.db.QueryRowContext(c.Request.Context(), `
		UPDATE workflows
		SET name=$1, description=$2, triggers=$3, steps=$4, enabled=$5,
		    metadata=$6, tags=$7, updated_at=$8, version=version+1
		WHERE id=$9
		RETURNING id, name, description, enabled, created_at, updated_at, version
	`, req.Name, req.Description, triggersJSON, stepsJSON, req.Enabled,
		metadataJSON, tagsJSON, now, workflowID).Scan(
		new(string), new(string), new(*string), new(bool), new(time.Time), new(time.Time), new(int),
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	updatedWorkflow = map[string]interface{}{
		"id": workflowID, "name": req.Name, "description": req.Description,
		"enabled": req.Enabled, "updated_at": now,
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": updatedWorkflow})
}

// DeleteWorkflow deletes a workflow
func (h *WorkflowHandler) DeleteWorkflow(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	res, err := h.db.ExecContext(c.Request.Context(), `DELETE FROM workflows WHERE id=$1`, workflowID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Workflow deleted successfully"})
}

// ExecuteWorkflow manually executes a workflow
func (h *WorkflowHandler) ExecuteWorkflow(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	var req struct {
		TriggerEvent map[string]interface{} `json:"trigger_event"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.TriggerEvent == nil {
		req.TriggerEvent = map[string]interface{}{
			"type":         "manual",
			"triggered_by": "user",
			"timestamp":    "2024-01-01T00:00:00Z",
		}
	}

	execution, err := h.workflowEngine.ExecuteWorkflow(c.Request.Context(), workflowID, req.TriggerEvent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    execution,
	})
}

// EnableWorkflow enables a workflow
func (h *WorkflowHandler) EnableWorkflow(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	if err := h.setWorkflowEnabled(c, workflowID, true); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Workflow enabled successfully"})
}

// DisableWorkflow disables a workflow
func (h *WorkflowHandler) DisableWorkflow(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	if err := h.setWorkflowEnabled(c, workflowID, false); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Workflow disabled successfully"})
}

// ListWorkflowExecutions lists executions for a workflow
func (h *WorkflowHandler) ListWorkflowExecutions(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.Query("status")

	offset := (page - 1) * limit

	query := `SELECT id, workflow_id, status, COALESCE(trigger_event, trigger_data, 'null'::jsonb),
	                 started_at, completed_at, COALESCE(error, error_message, '')
	          FROM workflow_executions
	          WHERE workflow_id = $1`
	args := []interface{}{workflowID}
	argIdx := 2

	if status != "" {
		query += ` AND status = $` + strconv.Itoa(argIdx)
		args = append(args, status)
		argIdx++
	}
	query += ` ORDER BY started_at DESC LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	var total int
	countArgs := []interface{}{workflowID}
	countQ := `SELECT COUNT(*) FROM workflow_executions WHERE workflow_id = $1`
	if status != "" {
		countQ += ` AND status = $2`
		countArgs = append(countArgs, status)
	}
	h.db.QueryRowContext(c.Request.Context(), countQ, countArgs...).Scan(&total)

	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	executions := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, wfID uuid.UUID
		var st string
		var triggerJSON []byte
		var startedAt time.Time
		var completedAt *time.Time
		var errMsg string
		if err := rows.Scan(&id, &wfID, &st, &triggerJSON, &startedAt, &completedAt, &errMsg); err != nil {
			continue
		}
		var triggerEvent interface{}
		json.Unmarshal(triggerJSON, &triggerEvent)
		row := map[string]interface{}{
			"id": id, "workflow_id": wfID, "status": st,
			"trigger_event": triggerEvent, "started_at": startedAt,
		}
		if completedAt != nil {
			row["completed_at"] = completedAt
		}
		if errMsg != "" {
			row["error"] = errMsg
		}
		executions = append(executions, row)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"executions": executions,
			"total":      len(executions),
			"page":       page,
			"limit":      limit,
		},
	})
}

// GetWorkflowExecution retrieves a specific workflow execution
func (h *WorkflowHandler) GetWorkflowExecution(c *gin.Context) {
	workflowIDStr := c.Param("id")
	executionIDStr := c.Param("execution_id")

	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	executionID, err := uuid.Parse(executionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid execution ID"})
		return
	}

	// Check in-memory first (running execution), fall back to DB
	if status, err := h.workflowEngine.GetWorkflowStatus(c.Request.Context(), executionID.String()); err == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
		return
	}

	var st string
	var triggerJSON, stepResultsJSON, logsJSON []byte
	var startedAt time.Time
	var completedAt *time.Time
	var errMsg string
	err = h.db.QueryRowContext(c.Request.Context(), `
		SELECT status,
		       COALESCE(trigger_event, trigger_data, 'null'::jsonb),
		       COALESCE(step_results, 'null'::jsonb),
		       COALESCE(logs, '[]'::jsonb),
		       started_at, completed_at,
		       COALESCE(error, error_message, '')
		FROM workflow_executions
		WHERE id=$1 AND workflow_id=$2`, executionID, workflowID).
		Scan(&st, &triggerJSON, &stepResultsJSON, &logsJSON, &startedAt, &completedAt, &errMsg)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "execution not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var triggerEvent, stepResults, logs interface{}
	json.Unmarshal(triggerJSON, &triggerEvent)
	json.Unmarshal(stepResultsJSON, &stepResults)
	json.Unmarshal(logsJSON, &logs)

	execution := map[string]interface{}{
		"id": executionID, "workflow_id": workflowID, "status": st,
		"trigger_event": triggerEvent, "step_results": stepResults, "logs": logs,
		"started_at": startedAt,
	}
	if completedAt != nil {
		execution["completed_at"] = completedAt
	}
	if errMsg != "" {
		execution["error"] = errMsg
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": execution})
}

// CancelWorkflowExecution cancels a running workflow execution
func (h *WorkflowHandler) CancelWorkflowExecution(c *gin.Context) {
	workflowIDStr := c.Param("id")
	executionIDStr := c.Param("execution_id")

	workflowID, err := uuid.Parse(workflowIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workflow ID"})
		return
	}

	executionID, err := uuid.Parse(executionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid execution ID"})
		return
	}

	// Try to cancel via engine (handles in-memory running executions)
	err = h.workflowEngine.CancelWorkflow(c.Request.Context(), executionID.String())
	if err != nil {
		// Not in-memory (already completed, or stored only in DB). Update DB directly
		// if status is still 'running'.
		res, dbErr := h.db.ExecContext(c.Request.Context(), `
			UPDATE workflow_executions SET status='cancelled', completed_at=NOW(),
			       error=COALESCE(error,'Cancelled by user'), error_message=COALESCE(error_message,'Cancelled by user')
			WHERE id=$1 AND workflow_id=$2 AND status='running'`, executionID, workflowID)
		if dbErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": dbErr.Error()})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "execution not found or not cancellable"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Workflow execution cancelled successfully",
	})
}

// ListWorkflowTemplates lists available workflow templates
func (h *WorkflowHandler) ListWorkflowTemplates(c *gin.Context) {
	category := c.Query("category")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))


	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	result := make([]workflowTemplate, 0, len(builtinTemplates))
	for _, t := range builtinTemplates {
		if category == "" || t.Category == category {
			result = append(result, t)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"templates": result,
			"total":     len(result),
			"page":      page,
			"limit":     limit,
		},
	})
}

// GetWorkflowTemplate retrieves a specific workflow template
func (h *WorkflowHandler) GetWorkflowTemplate(c *gin.Context) {
	templateIDStr := c.Param("id")
	templateID, err := uuid.Parse(templateIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid template ID"})
		return
	}

	for _, t := range builtinTemplates {
		if t.ID == templateID {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": t})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
}

// CreateFromTemplate creates a new workflow from a template
func (h *WorkflowHandler) CreateFromTemplate(c *gin.Context) {
	templateIDStr := c.Param("id")
	templateID, err := uuid.Parse(templateIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid template ID"})
		return
	}

	var req struct {
		Name       string                 `json:"name" binding:"required"`
		Parameters map[string]interface{} `json:"parameters"`
		Enabled    bool                   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rawUserID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	createdBy, ok := rawUserID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user session"})
		return
	}

	// Find the template by ID
	var tmpl *workflowTemplate
	for i := range builtinTemplates {
		if builtinTemplates[i].ID == templateID {
			tmpl = &builtinTemplates[i]
			break
		}
	}
	if tmpl == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	// Decode template triggers and steps
	triggersJSON, _ := json.Marshal(tmpl.Triggers)
	stepsJSON, _ := json.Marshal(tmpl.Steps)

	var triggers []workflows.WorkflowTrigger
	var steps []workflows.WorkflowStep
	json.Unmarshal(triggersJSON, &triggers)
	json.Unmarshal(stepsJSON, &steps)

	wf := &workflows.Workflow{
		Name:        req.Name,
		Description: tmpl.Description,
		Triggers:    triggers,
		Steps:       steps,
		Enabled:     req.Enabled,
		Tags:        tmpl.Tags,
		Metadata:    map[string]interface{}{"template_id": templateID, "parameters": req.Parameters},
		CreatedBy:   createdBy,
	}

	if err := h.workflowEngine.CreateWorkflow(c.Request.Context(), wf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    wf,
	})
}

// setWorkflowEnabled is a shared helper for Enable/Disable endpoints.
func (h *WorkflowHandler) setWorkflowEnabled(c *gin.Context, workflowID uuid.UUID, enabled bool) error {
	res, err := h.db.ExecContext(c.Request.Context(),
		`UPDATE workflows SET enabled=$1, updated_at=NOW() WHERE id=$2`, enabled, workflowID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		return sql.ErrNoRows
	}
	return nil
}
