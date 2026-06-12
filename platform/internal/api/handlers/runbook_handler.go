package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RunbookHandler manages the investigation_runbooks catalog (HolmesGPT SkillCatalog pattern).
// Runbooks are domain-specific investigation guides injected into OIE investigations
// as context evidence when the failure_class or entity_type matches.
type RunbookHandler struct {
	db *sql.DB
}

func NewRunbookHandler(db *sql.DB) *RunbookHandler {
	return &RunbookHandler{db: db}
}

type runbookRow struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Domain       string    `json:"domain"`       // "k8s", "netapp", "cloudstack", "database", ""=any
	EntityType   string    `json:"entity_type"`  // "pod", "node", "pvc", ""=any
	FailureClass string    `json:"failure_class"` // "CrashLoopBackOff", "OOMKilled", ""=any
	Content      string    `json:"content"`
	Source       string    `json:"source"` // "system", "operator", "confluence"
	CreatedAt    time.Time `json:"created_at"`
}

// ListRunbooks handles GET /api/v1/runbooks.
func (h *RunbookHandler) ListRunbooks(c *gin.Context) {
	domain := c.Query("domain")
	entityType := c.Query("entity_type")
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id::text, name, domain, entity_type, failure_class, content, source, created_at
		FROM investigation_runbooks
		WHERE ($1 = '' OR domain = $1 OR domain = '')
		  AND ($2 = '' OR entity_type = $2 OR entity_type = '')
		ORDER BY domain, entity_type, failure_class
		LIMIT 50
	`, domain, entityType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []runbookRow
	for rows.Next() {
		var r runbookRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Domain, &r.EntityType, &r.FailureClass,
			&r.Content, &r.Source, &r.CreatedAt); err != nil {
			continue
		}
		items = append(items, r)
	}
	if items == nil {
		items = []runbookRow{}
	}
	c.JSON(http.StatusOK, gin.H{"runbooks": items, "count": len(items)})
}

// CreateRunbook handles POST /api/v1/runbooks.
func (h *RunbookHandler) CreateRunbook(c *gin.Context) {
	var body struct {
		Name         string `json:"name" binding:"required"`
		Domain       string `json:"domain"`
		EntityType   string `json:"entity_type"`
		FailureClass string `json:"failure_class"`
		Content      string `json:"content" binding:"required"`
		Source       string `json:"source"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Source == "" {
		body.Source = "operator"
	}
	id := uuid.New()
	_, err := h.db.ExecContext(c.Request.Context(), `
		INSERT INTO investigation_runbooks
			(id, name, domain, entity_type, failure_class, content, source, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	`, id, body.Name, body.Domain, body.EntityType, body.FailureClass, body.Content, body.Source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": body.Name})
}

// UpdateRunbook handles PUT /api/v1/runbooks/:id.
func (h *RunbookHandler) UpdateRunbook(c *gin.Context) {
	rid := c.Param("id")
	var body struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.db.ExecContext(c.Request.Context(),
		`UPDATE investigation_runbooks SET content = $1 WHERE id::text = $2`,
		body.Content, rid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "runbook not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// DeleteRunbook handles DELETE /api/v1/runbooks/:id.
func (h *RunbookHandler) DeleteRunbook(c *gin.Context) {
	rid := c.Param("id")
	res, err := h.db.ExecContext(c.Request.Context(),
		`DELETE FROM investigation_runbooks WHERE id::text = $1 AND source != 'system'`, rid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "runbook not found or system runbooks cannot be deleted"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
