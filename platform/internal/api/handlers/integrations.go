package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/services/integrations"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IntegrationHandler handles integration management endpoints
type IntegrationHandler struct {
	db                 *sql.DB
	integrationService *integrations.IntegrationService
}

// NewIntegrationHandler creates a new integration handler
func NewIntegrationHandler(db *sql.DB) *IntegrationHandler {
	return &IntegrationHandler{
		db:                 db,
		integrationService: integrations.NewIntegrationService(),
	}
}

// Integration represents a generic integration
type Integration struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Enabled   bool                   `json:"enabled"`
	Status    string                 `json:"status"`
	LastSync  *time.Time             `json:"lastSync,omitempty"`
	Config    map[string]interface{} `json:"config"`
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`
}

// AuthProvider represents an authentication provider
type AuthProvider struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Type     string                 `json:"type"`
	Provider string                 `json:"provider"`
	Enabled  bool                   `json:"enabled"`
	Config   map[string]interface{} `json:"config"`
}

// ListIntegrations returns all integrations
func (h *IntegrationHandler) ListIntegrations(c *gin.Context) {
	query := `
		SELECT id, name, type, enabled, status, last_sync, config, created_at, updated_at 
		FROM integrations 
		ORDER BY created_at DESC
	`

	rows, err := h.db.QueryContext(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to fetch integrations",
		})
		return
	}
	defer rows.Close()

	var integrations []Integration
	for rows.Next() {
		var integration Integration
		var configJSON []byte
		var lastSync sql.NullTime

		err := rows.Scan(
			&integration.ID, &integration.Name, &integration.Type,
			&integration.Enabled, &integration.Status, &lastSync,
			&configJSON, &integration.CreatedAt, &integration.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if lastSync.Valid {
			integration.LastSync = &lastSync.Time
		}

		json.Unmarshal(configJSON, &integration.Config)
		integrations = append(integrations, integration)
	}

	if integrations == nil {
		integrations = []Integration{}
	}

	c.JSON(http.StatusOK, integrations)
}

// GetIntegration returns a specific integration
func (h *IntegrationHandler) GetIntegration(c *gin.Context) {
	id := c.Param("id")

	var integration Integration
	var configJSON []byte
	var lastSync sql.NullTime

	query := `
		SELECT id, name, type, enabled, status, last_sync, config, created_at, updated_at 
		FROM integrations 
		WHERE id = $1
	`

	err := h.db.QueryRowContext(c.Request.Context(), query, id).Scan(
		&integration.ID, &integration.Name, &integration.Type,
		&integration.Enabled, &integration.Status, &lastSync,
		&configJSON, &integration.CreatedAt, &integration.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Integration not found",
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to fetch integration",
		})
		return
	}

	if lastSync.Valid {
		integration.LastSync = &lastSync.Time
	}

	json.Unmarshal(configJSON, &integration.Config)
	c.JSON(http.StatusOK, integration)
}

// CreateIntegration creates a new integration
func (h *IntegrationHandler) CreateIntegration(c *gin.Context) {
	var integration Integration
	if err := c.ShouldBindJSON(&integration); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	integration.ID = uuid.New().String()
	integration.Status = "disconnected"
	integration.CreatedAt = time.Now()
	integration.UpdatedAt = time.Now()

	configJSON, _ := json.Marshal(integration.Config)

	query := `
		INSERT INTO integrations (id, name, type, enabled, status, config, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := h.db.ExecContext(c.Request.Context(), query,
		integration.ID, integration.Name, integration.Type,
		integration.Enabled, integration.Status, configJSON,
		integration.CreatedAt, integration.UpdatedAt,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to create integration",
		})
		return
	}

	c.JSON(http.StatusCreated, integration)
}

// UpdateIntegration updates an existing integration
func (h *IntegrationHandler) UpdateIntegration(c *gin.Context) {
	id := c.Param("id")

	var integration Integration
	if err := c.ShouldBindJSON(&integration); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	integration.UpdatedAt = time.Now()
	configJSON, _ := json.Marshal(integration.Config)

	query := `
		UPDATE integrations 
		SET name = $1, enabled = $2, config = $3, updated_at = $4
		WHERE id = $5
	`

	result, err := h.db.ExecContext(c.Request.Context(), query,
		integration.Name, integration.Enabled, configJSON,
		integration.UpdatedAt, id,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to update integration",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Integration not found",
		})
		return
	}

	integration.ID = id
	c.JSON(http.StatusOK, integration)
}

// DeleteIntegration deletes an integration
func (h *IntegrationHandler) DeleteIntegration(c *gin.Context) {
	id := c.Param("id")

	query := `DELETE FROM integrations WHERE id = $1`
	result, err := h.db.ExecContext(c.Request.Context(), query, id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to delete integration",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Integration not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Integration deleted successfully",
	})
}

// ToggleIntegration toggles integration enabled status
func (h *IntegrationHandler) ToggleIntegration(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	query := `UPDATE integrations SET enabled = $1, updated_at = $2 WHERE id = $3`
	result, err := h.db.ExecContext(c.Request.Context(), query, req.Enabled, time.Now(), id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to toggle integration",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Integration not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Integration toggled successfully",
		"enabled": req.Enabled,
	})
}

// TestIntegration tests an integration connection
func (h *IntegrationHandler) TestIntegration(c *gin.Context) {
	id := c.Param("id")

	// Get integration details
	var integrationType string
	var configJSON []byte
	query := `SELECT type, config FROM integrations WHERE id = $1`
	err := h.db.QueryRowContext(c.Request.Context(), query, id).Scan(&integrationType, &configJSON)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Integration not found",
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to fetch integration",
		})
		return
	}

	var config map[string]interface{}
	json.Unmarshal(configJSON, &config)

	// Test connection using integration service
	result, err := h.integrationService.TestConnection(c.Request.Context(), integrationType, config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": fmt.Sprintf("Test failed: %v", err),
		})
		return
	}

	// Update integration status based on test result
	status := "connected"
	if !result.Success {
		status = "error"
	}

	updateQuery := `UPDATE integrations SET status = $1, last_sync = $2, updated_at = $3 WHERE id = $4`
	h.db.ExecContext(c.Request.Context(), updateQuery, status, time.Now(), time.Now(), id)

	if !result.Success {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": result.Error,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": result.Message,
		"latency": result.Latency,
		"status":  status,
	})
}

// TestIntegrationConfig tests integration configuration before saving
func (h *IntegrationHandler) TestIntegrationConfig(c *gin.Context) {
	var req struct {
		Type   string                 `json:"type"`
		Config map[string]interface{} `json:"config"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	// Test connection using integration service
	result, err := h.integrationService.TestConnection(c.Request.Context(), req.Type, req.Config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": fmt.Sprintf("Test failed: %v", err),
		})
		return
	}

	if !result.Success {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": result.Error,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": result.Message,
		"latency": result.Latency,
	})
}

// SyncIntegration manually triggers a sync
func (h *IntegrationHandler) SyncIntegration(c *gin.Context) {
	id := c.Param("id")

	// Update last_sync timestamp
	query := `UPDATE integrations SET last_sync = $1, updated_at = $2 WHERE id = $3`
	result, err := h.db.ExecContext(c.Request.Context(), query, time.Now(), time.Now(), id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to sync integration",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Integration not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Sync triggered successfully",
	})
}

// Auth Provider Handlers

// ListAuthProviders returns all auth providers
func (h *IntegrationHandler) ListAuthProviders(c *gin.Context) {
	query := `
		SELECT id, name, type, provider, enabled, config 
		FROM auth_providers 
		ORDER BY created_at DESC
	`

	rows, err := h.db.QueryContext(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to fetch auth providers",
		})
		return
	}
	defer rows.Close()

	var providers []AuthProvider
	for rows.Next() {
		var provider AuthProvider
		var configJSON []byte

		err := rows.Scan(
			&provider.ID, &provider.Name, &provider.Type,
			&provider.Provider, &provider.Enabled, &configJSON,
		)
		if err != nil {
			continue
		}

		json.Unmarshal(configJSON, &provider.Config)
		providers = append(providers, provider)
	}

	if providers == nil {
		providers = []AuthProvider{}
	}

	c.JSON(http.StatusOK, providers)
}

// CreateAuthProvider creates a new auth provider
func (h *IntegrationHandler) CreateAuthProvider(c *gin.Context) {
	var provider AuthProvider
	if err := c.ShouldBindJSON(&provider); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	provider.ID = uuid.New().String()
	configJSON, _ := json.Marshal(provider.Config)

	query := `
		INSERT INTO auth_providers (id, name, type, provider, enabled, config, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := h.db.ExecContext(c.Request.Context(), query,
		provider.ID, provider.Name, provider.Type,
		provider.Provider, provider.Enabled, configJSON,
		time.Now(), time.Now(),
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to create auth provider",
		})
		return
	}

	c.JSON(http.StatusCreated, provider)
}

// UpdateAuthProvider updates an auth provider
func (h *IntegrationHandler) UpdateAuthProvider(c *gin.Context) {
	id := c.Param("id")

	var provider AuthProvider
	if err := c.ShouldBindJSON(&provider); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	configJSON, _ := json.Marshal(provider.Config)

	query := `
		UPDATE auth_providers 
		SET name = $1, enabled = $2, config = $3, updated_at = $4
		WHERE id = $5
	`

	result, err := h.db.ExecContext(c.Request.Context(), query,
		provider.Name, provider.Enabled, configJSON, time.Now(), id,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to update auth provider",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Auth provider not found",
		})
		return
	}

	provider.ID = id
	c.JSON(http.StatusOK, provider)
}

// DeleteAuthProvider deletes an auth provider
func (h *IntegrationHandler) DeleteAuthProvider(c *gin.Context) {
	id := c.Param("id")

	query := `DELETE FROM auth_providers WHERE id = $1`
	result, err := h.db.ExecContext(c.Request.Context(), query, id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to delete auth provider",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Auth provider not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Auth provider deleted successfully",
	})
}

// ToggleAuthProvider toggles auth provider
func (h *IntegrationHandler) ToggleAuthProvider(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "Invalid request body",
		})
		return
	}

	query := `UPDATE auth_providers SET enabled = $1, updated_at = $2 WHERE id = $3`
	result, err := h.db.ExecContext(c.Request.Context(), query, req.Enabled, time.Now(), id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"detail": "Failed to toggle auth provider",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"detail": "Auth provider not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Auth provider toggled successfully",
		"enabled": req.Enabled,
	})
}

// RegisterRoutes registers integration routes
func (h *IntegrationHandler) RegisterRoutes(router *gin.RouterGroup) {
	integrations := router.Group("/integrations")
	{
		// Auth Providers — registered before /:id to avoid wildcard conflicts
		integrations.GET("/auth-providers", h.ListAuthProviders)
		integrations.POST("/auth-providers", h.CreateAuthProvider)
		integrations.PUT("/auth-providers/:id", h.UpdateAuthProvider)
		integrations.DELETE("/auth-providers/:id", h.DeleteAuthProvider)
		integrations.PUT("/auth-providers/:id/toggle", h.ToggleAuthProvider)

		// Test config (static path before wildcard)
		integrations.POST("/test-config", h.TestIntegrationConfig)

		// Generic Integration CRUD
		integrations.GET("", h.ListIntegrations)
		integrations.GET("/:id", h.GetIntegration)
		integrations.POST("", h.CreateIntegration)
		integrations.PUT("/:id", h.UpdateIntegration)
		integrations.DELETE("/:id", h.DeleteIntegration)
		integrations.PUT("/:id/toggle", h.ToggleIntegration)
		integrations.POST("/:id/test", h.TestIntegration)
		integrations.POST("/:id/sync", h.SyncIntegration)
	}
}
