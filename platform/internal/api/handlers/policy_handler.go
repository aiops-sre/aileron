package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/policy"
)

// PolicyHandler provides CRUD for intelligence_policies and exposes the policy
// evaluation endpoint for debugging (Sympozium SympoziumPolicy pattern).
type PolicyHandler struct {
	db     *sql.DB
	engine *policy.PolicyEngine
}

func NewPolicyHandler(db *sql.DB, engine *policy.PolicyEngine) *PolicyHandler {
	return &PolicyHandler{db: db, engine: engine}
}

type policyAPIRow struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	PolicyType    string    `json:"policy_type"`
	ConditionJSON string    `json:"condition"`
	Action        string    `json:"action"`
	Enabled       bool      `json:"enabled"`
	Priority      int       `json:"priority"`
	CreatedAt     time.Time `json:"created_at"`
}

// ListPolicies handles GET /api/v1/intelligence-policies.
func (h *PolicyHandler) ListPolicies(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id::text, name, COALESCE(description,''), policy_type, condition_json,
		       COALESCE(action,'suppress'), enabled, COALESCE(priority,50), created_at
		FROM intelligence_policies
		ORDER BY priority DESC, created_at
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []policyAPIRow
	for rows.Next() {
		var r policyAPIRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.PolicyType, &r.ConditionJSON,
			&r.Action, &r.Enabled, &r.Priority, &r.CreatedAt); err != nil {
			continue
		}
		items = append(items, r)
	}
	if items == nil {
		items = []policyAPIRow{}
	}
	c.JSON(http.StatusOK, gin.H{"policies": items, "count": len(items)})
}

// CreatePolicy handles POST /api/v1/intelligence-policies.
func (h *PolicyHandler) CreatePolicy(c *gin.Context) {
	var body struct {
		Name          string `json:"name" binding:"required"`
		Description   string `json:"description"`
		PolicyType    string `json:"policy_type" binding:"required"`
		ConditionJSON string `json:"condition" binding:"required"`
		Action        string `json:"action"`
		Priority      int    `json:"priority"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Action == "" {
		body.Action = "suppress"
	}
	if body.Priority == 0 {
		body.Priority = 50
	}
	id := uuid.New()
	_, err := h.db.ExecContext(c.Request.Context(), `
		INSERT INTO intelligence_policies
			(id, name, description, policy_type, condition_json, action, enabled, priority, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, true, $7, NOW())
	`, id, body.Name, body.Description, body.PolicyType, body.ConditionJSON, body.Action, body.Priority)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.engine != nil {
		h.engine.Invalidate()
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// TogglePolicy handles PATCH /api/v1/intelligence-policies/:id/toggle.
func (h *PolicyHandler) TogglePolicy(c *gin.Context) {
	pid := c.Param("id")
	res, err := h.db.ExecContext(c.Request.Context(),
		`UPDATE intelligence_policies SET enabled = NOT enabled WHERE id::text = $1`, pid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "policy not found"})
		return
	}
	if h.engine != nil {
		h.engine.Invalidate()
	}
	c.JSON(http.StatusOK, gin.H{"status": "toggled"})
}

// DeletePolicy handles DELETE /api/v1/intelligence-policies/:id.
func (h *PolicyHandler) DeletePolicy(c *gin.Context) {
	pid := c.Param("id")
	res, err := h.db.ExecContext(c.Request.Context(),
		`DELETE FROM intelligence_policies WHERE id::text = $1`, pid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "policy not found"})
		return
	}
	if h.engine != nil {
		h.engine.Invalidate()
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// EvaluatePolicy handles POST /api/v1/intelligence-policies/evaluate (debug endpoint).
func (h *PolicyHandler) EvaluatePolicy(c *gin.Context) {
	if h.engine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "policy engine not initialized"})
		return
	}
	var body struct {
		Source     string            `json:"source"`
		Title      string            `json:"title"`
		Severity   string            `json:"severity"`
		Namespace  string            `json:"namespace"`
		EntityType string            `json:"entity_type"`
		Labels     map[string]string `json:"labels"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	decision := h.engine.EvaluateAlert(
		c.Request.Context(),
		body.Source, body.Title, body.Severity,
		body.Namespace, body.EntityType, body.Labels,
	)
	c.JSON(http.StatusOK, gin.H{
		"action":      decision.Action,
		"policy_name": decision.PolicyName,
		"policy_id":   decision.PolicyID,
	})
}
