package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DeduplicationHandler manages deduplication rules
type DeduplicationHandler struct {
	db *sql.DB
}

// NewDeduplicationHandler creates a new deduplication handler
func NewDeduplicationHandler(db *sql.DB) *DeduplicationHandler {
	return &DeduplicationHandler{db: db}
}

type DeduplicationRule struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	Field             string     `json:"field"`
	MatchType         string     `json:"match_type"`
	Pattern           string     `json:"pattern"`
	TimeWindowSeconds int        `json:"time_window_seconds"`
	Enabled           bool       `json:"enabled"`
	Priority          int        `json:"priority"`
	DedupCount        int        `json:"dedup_count"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// RegisterRoutes registers deduplication routes
func (h *DeduplicationHandler) RegisterRoutes(router *gin.RouterGroup) {
	dedup := router.Group("/deduplication")
	{
		dedup.GET("/rules", h.ListRules)
		dedup.POST("/rules", h.CreateRule)
		dedup.PUT("/rules/:id", h.UpdateRule)
		dedup.PATCH("/rules/:id", h.PatchRule)
		dedup.DELETE("/rules/:id", h.DeleteRule)
	}
}

func (h *DeduplicationHandler) ListRules(c *gin.Context) {
	rows, err := h.db.QueryContext(c, `
		SELECT id, name, description, field, match_type, pattern,
		       time_window_seconds, enabled, priority, dedup_count, created_at, updated_at
		FROM deduplication_rules
		ORDER BY priority DESC, created_at DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	var rules []DeduplicationRule
	for rows.Next() {
		var r DeduplicationRule
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Field, &r.MatchType,
			&r.Pattern, &r.TimeWindowSeconds, &r.Enabled, &r.Priority, &r.DedupCount,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		rules = append(rules, r)
	}

	if rules == nil {
		rules = []DeduplicationRule{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rules, "total": len(rules)})
}

func (h *DeduplicationHandler) CreateRule(c *gin.Context) {
	var rule DeduplicationRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	rule.ID = uuid.New()
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = time.Now()
	if rule.MatchType == "" {
		rule.MatchType = "exact"
	}
	if rule.TimeWindowSeconds == 0 {
		rule.TimeWindowSeconds = 300
	}

	_, err := h.db.ExecContext(c, `
		INSERT INTO deduplication_rules
			(id, name, description, field, match_type, pattern, time_window_seconds, enabled, priority, dedup_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, rule.ID, rule.Name, rule.Description, rule.Field, rule.MatchType,
		rule.Pattern, rule.TimeWindowSeconds, rule.Enabled, rule.Priority, 0,
		rule.CreatedAt, rule.UpdatedAt)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": rule})
}

func (h *DeduplicationHandler) UpdateRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rule id"})
		return
	}

	var rule DeduplicationRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	rule.UpdatedAt = time.Now()
	res, err := h.db.ExecContext(c, `
		UPDATE deduplication_rules
		SET name=$1, description=$2, field=$3, match_type=$4, pattern=$5,
		    time_window_seconds=$6, enabled=$7, priority=$8, updated_at=$9
		WHERE id=$10
	`, rule.Name, rule.Description, rule.Field, rule.MatchType, rule.Pattern,
		rule.TimeWindowSeconds, rule.Enabled, rule.Priority, rule.UpdatedAt, id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}

	rule.ID = id
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rule})
}

func (h *DeduplicationHandler) PatchRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rule id"})
		return
	}

	var patch map[string]interface{}
	if err := json.NewDecoder(c.Request.Body).Decode(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if enabled, ok := patch["enabled"].(bool); ok {
		_, err = h.db.ExecContext(c,
			`UPDATE deduplication_rules SET enabled=$1, updated_at=$2 WHERE id=$3`,
			enabled, time.Now(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *DeduplicationHandler) DeleteRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rule id"})
		return
	}

	res, err := h.db.ExecContext(c, `DELETE FROM deduplication_rules WHERE id=$1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
