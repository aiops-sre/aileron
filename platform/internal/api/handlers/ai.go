package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/oidc"
)

// AIHandler handles AI chat operations
type AIHandler struct {
	oidcService *oidc.OIDCProviderService
	db               *sql.DB
	advancedAnalyzer *AIAdvancedAnalyzer
	modelsCache      []oidc.ModelInfo
	cacheExpiry      time.Time
	mu               sync.RWMutex
}

// NewAIHandler creates a new AI handler
func NewAIHandler(oidcService *oidc.OIDCProviderService, db *sql.DB) *AIHandler {
	return &AIHandler{
		oidcService: oidcService,
		db:               db,
		advancedAnalyzer: NewAIAdvancedAnalyzer(db),
	}
}

// ChatRequest represents an incoming chat request from the frontend
type ChatRequest struct {
	Model     string                  `json:"model" binding:"required"`
	Messages  []oidc.ChatMessage `json:"messages" binding:"required"`
	SessionID *string                 `json:"session_id,omitempty"`
	MaxTokens int                     `json:"max_tokens,omitempty"`
}

// ChatResponse represents the response sent to the frontend
type ChatResponse struct {
	Success   bool                    `json:"success"`
	Message   string                  `json:"message,omitempty"`
	Response  *oidc.ChatResponse `json:"response,omitempty"`
	SessionID string                  `json:"session_id,omitempty"`
	Error     string                  `json:"error,omitempty"`
}

// ModelsResponse represents the available models response
type ModelsResponse struct {
	Success bool                  `json:"success"`
	Models  []oidc.ModelInfo `json:"models,omitempty"`
	Error   string                `json:"error,omitempty"`
}

// RegisterRoutes registers AI-related routes
func (h *AIHandler) RegisterRoutes(router *gin.RouterGroup) {
	ai := router.Group("/ai")
	{
		// Core AI endpoints
		ai.POST("/chat", h.Chat)
		ai.GET("/models", h.ListModels)
		ai.GET("/health", h.HealthCheck)
		ai.GET("/sessions", h.ListSessions)
		ai.POST("/sessions", h.CreateSession)
		ai.PATCH("/sessions/:session_id", h.UpdateSession)
		ai.GET("/sessions/:session_id/messages", h.GetSessionMessages)
		ai.DELETE("/sessions/:session_id", h.DeleteSession)

		// AI Tools endpoints (MCP-like)
		ai.GET("/tools", h.ListTools)
		ai.POST("/tools/execute", h.ExecuteTool)
		ai.GET("/capabilities", h.GetCapabilities)

		// Advanced AI Analysis endpoints
		ai.GET("/analyze/:alert_id", h.AdvancedAnalysis)
		ai.GET("/correlate/:alert_id", h.DetectCorrelations)
		ai.GET("/predict/:alert_id", h.PredictEscalation)
		ai.GET("/runbooks/:alert_id", h.GetRunbooks)
		ai.GET("/clusters", h.GetAlertClusters)
		ai.GET("/fatigue", h.AnalyzeFatigue)
		ai.POST("/rca", h.GenerateRCA)
	}
}

// Chat handles chat requests to OIDCProvider AI
// @Summary Chat with AI
// @Description Send a chat message to the configured AI model
// @Tags AI
// @Accept json
// @Produce json
// @Param request body ChatRequest true "Chat request"
// @Success 200 {object} ChatResponse
// @Failure 400 {object} ChatResponse
// @Failure 500 {object} ChatResponse
// @Router /api/v1/ai/chat [post]
func (h *AIHandler) Chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ChatResponse{
			Success: false,
			Error:   "Invalid request: " + err.Error(),
		})
		return
	}

	// Get user from context (set by auth middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ChatResponse{
			Success: false,
			Error:   "User not authenticated",
		})
		return
	}

	// Convert user_id to string (it may be uuid.UUID or string)
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, ChatResponse{
			Success: false,
			Error:   "Invalid user_id type",
		})
		return
	}

	// Create or get session ID
	sessionID := uuid.New().String()
	if req.SessionID != nil && *req.SessionID != "" {
		sessionID = *req.SessionID
	}

	// Enhance messages with AlertHub context
	enhancedMessages := h.enhanceMessagesWithContext(req.Messages, userIDStr)

	// Check for OIDCProvider token from browser
	oidcToken := c.GetHeader("X-OIDCProvider-Token")
	if oidcToken != "" {
		// Use token provided by user's browser/device
		h.oidcService.SetUserToken(oidcToken)

		// NEW: Get user's VPN IP from database for proxy requests
		var userVPNIP string
		err := h.db.QueryRow("SELECT vpn_ip FROM users WHERE id = $1", userIDStr).Scan(&userVPNIP)
		if err == nil && userVPNIP != "" {
			h.oidcService.SetUserVPNIP(userVPNIP)
		}
	}

	// Create OIDCProvider request
	oidcReq := oidc.ChatRequest{
		Model:     req.Model,
		Messages:  enhancedMessages,
		MaxTokens: req.MaxTokens,
	}

	// Allow up to 3 minutes for LLM inference — OIDCProvider Claude/Gemini calls can take 30-90s
	// for longer responses. The nginx ingress has proxy_read_timeout: 3600.
	oidcCtx, cancelFG := context.WithTimeout(c.Request.Context(), 180*time.Second)
	defer cancelFG()
	response, err := h.oidcService.Chat(oidcCtx, oidcReq)
	if err != nil {
		// Log the error so it's visible in pod logs
		log.Printf("OIDCProvider chat error (model=%s): %v", req.Model, err)
		// Fall back to mock only when OIDCProvider is unavailable (not on timeout from user-selected model)
		response = h.getMockResponseWithContext(enhancedMessages, userIDStr)
	}

	// Store conversation in database
	if err := h.storeConversation(sessionID, userIDStr, req.Model, req.Messages, response); err != nil {
		// Log error but don't fail the request
		c.Error(err)
	}

	c.JSON(http.StatusOK, ChatResponse{
		Success:   true,
		Message:   "Chat successful",
		Response:  response,
		SessionID: sessionID,
	})
}

// ListModels lists available AI models with caching for fast loading
// @Summary List AI models
// @Description Get list of available AI models from OIDCProvider with caching
// @Tags AI
// @Produce json
// @Success 200 {object} ModelsResponse
// @Failure 500 {object} ModelsResponse
// @Router /api/v1/ai/models [get]
func (h *AIHandler) ListModels(c *gin.Context) {
	// Check for OIDCProvider token first — bypass cache when user provides their token
	oidcToken := c.GetHeader("X-OIDCProvider-Token")
	if oidcToken == "" {
		// Try to get from OAuth Authorization header
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			oidcToken = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	// Only serve from cache when no user token is provided (anonymous/fallback requests)
	if oidcToken == "" {
		h.mu.RLock()
		if time.Now().Before(h.cacheExpiry) && len(h.modelsCache) > 0 {
			models := h.modelsCache
			h.mu.RUnlock()
			c.JSON(http.StatusOK, ModelsResponse{
				Success: true,
				Models:  models,
			})
			return
		}
		h.mu.RUnlock()
	}

	var models []oidc.ModelInfo

	if oidcToken != "" {
		// Use token provided by user's browser/device
		h.oidcService.SetUserToken(oidcToken)

		// Get user's VPN IP from database for proxy requests
		userID, exists := c.Get("user_id")
		if exists {
			var userIDStr string
			switch v := userID.(type) {
			case uuid.UUID:
				userIDStr = v.String()
			case string:
				userIDStr = v
			}

			if userIDStr != "" {
				var userVPNIP string
				err := h.db.QueryRow("SELECT vpn_ip FROM users WHERE id = $1", userIDStr).Scan(&userVPNIP)
				if err == nil && userVPNIP != "" {
					h.oidcService.SetUserVPNIP(userVPNIP)
				}
			}
		}

		// Try to get models from OIDCProvider
		oidcModels, err := h.oidcService.ListModels(c.Request.Context())
		if err == nil {
			models = oidcModels
		} else {
			log.Printf("OIDCProvider ListModels failed (token_prefix=%s...): %v", func() string {
				if len(oidcToken) > 20 { return oidcToken[:20] }
				return oidcToken
			}(), err)
		}
	}

	// Always provide AlertHub Intelligence as fallback/default
	if len(models) == 0 {
		models = []oidc.ModelInfo{
			{
				ID:        "alerthub-intelligence",
				Name:      "AlertHub Intelligence",
				Provider:  "AlertHub AI",
				CreatedAt: time.Now().Format(time.RFC3339),
			},
			{
				ID:        "alerthub-correlation",
				Name:      "AlertHub Correlation AI",
				Provider:  "AlertHub AI",
				CreatedAt: time.Now().Format(time.RFC3339),
			},
		}
	} else {
		// Add AlertHub models to OIDCProvider models
		models = append(models, oidc.ModelInfo{
			ID:        "alerthub-intelligence",
			Name:      "AlertHub Intelligence",
			Provider:  "AlertHub AI",
			CreatedAt: time.Now().Format(time.RFC3339),
		})
	}

	// Cache the models for fast subsequent requests (5 minute cache)
	h.mu.Lock()
	h.modelsCache = models
	h.cacheExpiry = time.Now().Add(5 * time.Minute)
	h.mu.Unlock()

	c.JSON(http.StatusOK, ModelsResponse{
		Success: true,
		Models:  models,
	})
}

// HealthCheck checks AI service health
// @Summary Health check
// @Description Check if OIDCProvider service is accessible
// @Tags AI
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/v1/ai/health [get]
func (h *AIHandler) HealthCheck(c *gin.Context) {
	// Check for OIDCProvider token from browser (CRITICAL FIX)
	oidcToken := c.GetHeader("X-OIDCProvider-Token")
	if oidcToken != "" {
		// Use token provided by user's browser/device
		h.oidcService.SetUserToken(oidcToken)
	}

	err := h.oidcService.HealthCheck(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   err.Error(),
			"status":  "unhealthy",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"status":  "healthy",
		"service": "OIDCProvider GenAI",
	})
}

// ListSessions lists user's chat sessions
func (h *AIHandler) ListSessions(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	// Convert user_id to string
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	sessions, err := h.getUserSessions(userIDStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch sessions: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"sessions": sessions,
	})
}

// GetSessionMessages retrieves messages for a specific session
func (h *AIHandler) GetSessionMessages(c *gin.Context) {
	sessionID := c.Param("session_id")
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	// Convert user_id to string
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	messages, err := h.getSessionMessages(sessionID, userIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"error":   "Session not found",
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   "Failed to fetch messages: " + err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"messages": messages,
	})
}

// DeleteSession deletes a chat session
func (h *AIHandler) DeleteSession(c *gin.Context) {
	sessionID := c.Param("session_id")
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	// Convert user_id to string
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	err := h.deleteSession(sessionID, userIDStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to delete session: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Session deleted successfully",
	})
}

// CreateSession creates a new AI chat session explicitly
func (h *AIHandler) CreateSession(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	var req struct {
		Title string `json:"title"`
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body — use defaults
		req.Title = "New Chat"
	}
	if req.Title == "" {
		req.Title = "New Chat"
	}
	if req.Model == "" {
		req.Model = "alerthub-intelligence"
	}

	sessionID := uuid.New().String()
	var createdAt time.Time
	err := h.db.QueryRowContext(c.Request.Context(), `
		INSERT INTO ai_chat_sessions (id, user_id, model, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING created_at
	`, sessionID, userIDStr, req.Model, req.Title).Scan(&createdAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to create session: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"session": gin.H{
			"id":         sessionID,
			"title":      req.Title,
			"model":      req.Model,
			"created_at": createdAt,
		},
	})
}

// UpdateSession updates the title of an existing AI chat session
func (h *AIHandler) UpdateSession(c *gin.Context) {
	sessionID := c.Param("session_id")
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	var req struct {
		Title string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "title is required",
		})
		return
	}

	result, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE ai_chat_sessions
		SET title = $1, updated_at = NOW()
		WHERE id = $2 AND user_id = $3
	`, req.Title, sessionID, userIDStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to update session: " + err.Error(),
		})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Session not found or access denied",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Database helper functions

type ChatSession struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Model     string    `json:"model"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatMessageDB struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	TokensUsed int       `json:"tokens_used,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (h *AIHandler) storeConversation(sessionID, userID, model string, messages []oidc.ChatMessage, response *oidc.ChatResponse) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Create or update session
	title := "New Chat"
	if len(messages) > 0 && len(messages[0].Content) > 50 {
		title = messages[0].Content[:50] + "..."
	} else if len(messages) > 0 {
		title = messages[0].Content
	}

	_, err = tx.Exec(`
		INSERT INTO ai_chat_sessions (id, user_id, model, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET updated_at = NOW(), model = $3
	`, sessionID, userID, model, title)
	if err != nil {
		return err
	}

	// Store user messages
	for _, msg := range messages {
		_, err = tx.Exec(`
			INSERT INTO ai_chat_messages (id, session_id, role, content, created_at)
			VALUES ($1, $2, $3, $4, NOW())
		`, uuid.New().String(), sessionID, msg.Role, msg.Content)
		if err != nil {
			return err
		}
	}

	// Store AI response
	_, err = tx.Exec(`
		INSERT INTO ai_chat_messages (id, session_id, role, content, tokens_used, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
	`, uuid.New().String(), sessionID, response.Role, response.Message, response.Usage.TotalTokens)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (h *AIHandler) getUserSessions(userID string) ([]ChatSession, error) {
	rows, err := h.db.Query(`
		SELECT id, user_id, model, title, created_at, updated_at
		FROM ai_chat_sessions
		WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT 50
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]ChatSession, 0)
	for rows.Next() {
		var session ChatSession
		err := rows.Scan(&session.ID, &session.UserID, &session.Model, &session.Title, &session.CreatedAt, &session.UpdatedAt)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}

	return sessions, rows.Err()
}

func (h *AIHandler) getSessionMessages(sessionID, userID string) ([]ChatMessageDB, error) {
	// Verify session belongs to user
	var count int
	err := h.db.QueryRow(`
		SELECT COUNT(*) FROM ai_chat_sessions WHERE id = $1 AND user_id = $2
	`, sessionID, userID).Scan(&count)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, sql.ErrNoRows
	}

	rows, err := h.db.Query(`
		SELECT id, session_id, role, content, COALESCE(tokens_used, 0), created_at
		FROM ai_chat_messages
		WHERE session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]ChatMessageDB, 0)
	for rows.Next() {
		var msg ChatMessageDB
		err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.TokensUsed, &msg.CreatedAt)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

func (h *AIHandler) deleteSession(sessionID, userID string) error {
	result, err := h.db.Exec(`
		DELETE FROM ai_chat_sessions WHERE id = $1 AND user_id = $2
	`, sessionID, userID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// AlertSummary represents a simplified alert for AI context
type AlertSummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	Status      string    `json:"status"`
	Source      string    `json:"source"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AlertStats represents alert statistics
type AlertStats struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

// IncidentSummary represents a simplified incident
type IncidentSummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	Status      string    `json:"status"`
	Priority    string    `json:"priority,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// IncidentStats holds incident counts by status/severity
type IncidentStats struct {
	Total       int `json:"total"`
	Open        int `json:"open"`
	Investigating int `json:"investigating"`
	Critical    int `json:"critical"`
	High        int `json:"high"`
}

// K8sClusterInfo holds cluster summary
type K8sClusterInfo struct {
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
	Region      string `json:"region,omitempty"`
	NodeCount   int    `json:"node_count"`
	PodCount    int    `json:"pod_count"`
	SyncStatus  string `json:"sync_status"`
}

// TopologyOverview holds entity counts per source
type TopologyOverview struct {
	TotalEntities   int            `json:"total_entities"`
	BySource        map[string]int `json:"by_source"`
	ByType          map[string]int `json:"by_type"`
	UnhealthyCount  int            `json:"unhealthy_count"`
}

// enhanceMessagesWithContext adds AlertHub data context to user messages
func (h *AIHandler) enhanceMessagesWithContext(messages []oidc.ChatMessage, userID string) []oidc.ChatMessage {
	// Get the last user message to understand context
	var lastUserMessage string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserMessage = strings.ToLower(messages[i].Content)
			break
		}
	}

	// Inject context for any operational or system query — broad trigger so AI always
	// has live data when the user is asking about the environment.
	needsContext := strings.Contains(lastUserMessage, "alert") ||
		strings.Contains(lastUserMessage, "incident") ||
		strings.Contains(lastUserMessage, "topology") ||
		strings.Contains(lastUserMessage, "kubernetes") ||
		strings.Contains(lastUserMessage, "k8s") ||
		strings.Contains(lastUserMessage, "cluster") ||
		strings.Contains(lastUserMessage, "infrastructure") ||
		strings.Contains(lastUserMessage, "infra") ||
		strings.Contains(lastUserMessage, "capacity") ||
		strings.Contains(lastUserMessage, "node") ||
		strings.Contains(lastUserMessage, "pod") ||
		strings.Contains(lastUserMessage, "service") ||
		strings.Contains(lastUserMessage, "health") ||
		strings.Contains(lastUserMessage, "status") ||
		strings.Contains(lastUserMessage, "analyze") ||
		strings.Contains(lastUserMessage, "analysis") ||
		strings.Contains(lastUserMessage, "summary") ||
		strings.Contains(lastUserMessage, "overview") ||
		strings.Contains(lastUserMessage, "recent") ||
		strings.Contains(lastUserMessage, "critical") ||
		strings.Contains(lastUserMessage, "system") ||
		strings.Contains(lastUserMessage, "show") ||
		strings.Contains(lastUserMessage, "list") ||
		strings.Contains(lastUserMessage, "what") ||
		strings.Contains(lastUserMessage, "how many") ||
		strings.Contains(lastUserMessage, "correlat") ||
		strings.Contains(lastUserMessage, "workflow") ||
		strings.Contains(lastUserMessage, "performance") ||
		strings.Contains(lastUserMessage, "predict") ||
		strings.Contains(lastUserMessage, "fatigue") ||
		strings.Contains(lastUserMessage, "rca") ||
		strings.Contains(lastUserMessage, "root cause")

	if !needsContext {
		return messages
	}

	// Fetch actual AlertHub data
	ahContext := h.getAlertHubContext(userID)
	if ahContext == "" {
		return messages
	}

	// Comprehensive system prompt with live data
	systemPrompt := fmt.Sprintf(`You are AlertHub AI — an expert SRE assistant with DIRECT, REAL-TIME access to this AlertHub system's operational data.

LIVE SYSTEM DATA (fetched now from the database):
%s

Guidelines:
- Use the data above to give specific, actionable answers
- Reference actual counts, names, severities, and statuses from the data
- For incidents and alerts, prioritize critical/high severity items
- For infrastructure, highlight any unhealthy or degraded components
- For Kubernetes, flag clusters with sync issues or high pod counts
- NEVER claim you don't have access to data — the live snapshot is above
- Be concise and structured; use bullet points and headers where helpful
- Suggest next steps based on the actual state of the system

Respond to the user's question using this live system data.`, ahContext)

	// Prepend system message before all user/assistant turns
	enhancedMessages := make([]oidc.ChatMessage, 0, len(messages)+1)
	enhancedMessages = append(enhancedMessages, oidc.ChatMessage{
		Role:    "system",
		Content: systemPrompt,
	})
	enhancedMessages = append(enhancedMessages, messages...)

	log.Printf("Enhanced context: Added %d bytes of system prompt with AlertHub data\n", len(systemPrompt))
	return enhancedMessages
}

// getAlertHubContext fetches current AlertHub data for AI context
func (h *AIHandler) getAlertHubContext(userID string) string {
	var contextParts []string

	// --- Alerts ---
	alerts, err := h.getRecentAlerts(10)
	if err != nil {
		log.Printf("AI Context: Failed to fetch recent alerts: %v\n", err)
	}
	if len(alerts) > 0 {
		alertSummary := make([]map[string]string, 0, len(alerts))
		for _, a := range alerts {
			alertSummary = append(alertSummary, map[string]string{
				"id": a.ID, "title": a.Title, "severity": a.Severity,
				"status": a.Status, "source": a.Source,
			})
		}
		alertData, _ := json.Marshal(alertSummary)
		contextParts = append(contextParts, fmt.Sprintf("=== RECENT ALERTS (active, up to 10) ===\n%s", string(alertData)))
	}

	stats, err := h.getAlertStats()
	if err != nil {
		log.Printf("AI Context: Failed to fetch alert stats: %v\n", err)
	} else {
		contextParts = append(contextParts, fmt.Sprintf(
			"=== ALERT COUNTS ===\nTotal=%d  Critical=%d  High=%d  Medium=%d  Low=%d",
			stats.Total, stats.Critical, stats.High, stats.Medium, stats.Low))
	}

	// --- Incidents ---
	incidents, err := h.getRecentIncidents(8)
	if err != nil {
		log.Printf("AI Context: Failed to fetch incidents: %v\n", err)
	}
	if len(incidents) > 0 {
		incSummary := make([]map[string]string, 0, len(incidents))
		for _, i := range incidents {
			m := map[string]string{
				"id": i.ID, "title": i.Title,
				"severity": i.Severity, "status": i.Status,
			}
			if i.Priority != "" {
				m["priority"] = i.Priority
			}
			incSummary = append(incSummary, m)
		}
		incData, _ := json.Marshal(incSummary)
		contextParts = append(contextParts, fmt.Sprintf("=== OPEN INCIDENTS (up to 8) ===\n%s", string(incData)))
	}

	incStats, err := h.getIncidentStats()
	if err != nil {
		log.Printf("AI Context: Failed to fetch incident stats: %v\n", err)
	} else if incStats.Total > 0 {
		contextParts = append(contextParts, fmt.Sprintf(
			"=== INCIDENT COUNTS ===\nTotal=%d  Open=%d  Investigating=%d  Critical=%d  High=%d",
			incStats.Total, incStats.Open, incStats.Investigating, incStats.Critical, incStats.High))
	}

	// --- Kubernetes Clusters ---
	k8sClusters, err := h.getK8sClusters()
	if err != nil {
		log.Printf("AI Context: Failed to fetch K8s clusters: %v\n", err)
	}
	if len(k8sClusters) > 0 {
		clusterData, _ := json.Marshal(k8sClusters)
		contextParts = append(contextParts, fmt.Sprintf("=== KUBERNETES CLUSTERS ===\n%s", string(clusterData)))
	}

	// --- Topology / Infrastructure ---
	topoOverview, err := h.getTopologyOverview()
	if err != nil {
		log.Printf("AI Context: Failed to fetch topology overview: %v\n", err)
	} else if topoOverview != nil && topoOverview.TotalEntities > 0 {
		topoData, _ := json.Marshal(topoOverview)
		contextParts = append(contextParts, fmt.Sprintf("=== TOPOLOGY / INFRASTRUCTURE ===\n%s", string(topoData)))
	}

	if len(contextParts) == 0 {
		log.Printf("AI Context: No context data available\n")
		return ""
	}

	contextData := "AlertHub Live System Snapshot:\n\n" + strings.Join(contextParts, "\n\n")
	log.Printf("AI Context: Generated %d bytes (%d sections)\n", len(contextData), len(contextParts))
	return contextData
}

func (h *AIHandler) getRecentAlerts(limit int) ([]AlertSummary, error) {
	// First try with status filter
	rows, err := h.db.Query(`
		SELECT id, title, severity, status, source, description, created_at, updated_at
		FROM alerts
		WHERE status NOT IN ('resolved', 'closed')
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)

	if err != nil {
		log.Printf("Query with status filter failed: %v\n", err)
		// Try without status filter
		rows, err = h.db.Query(`
			SELECT id, title, severity, status, source, description, created_at, updated_at
			FROM alerts
			ORDER BY created_at DESC
			LIMIT $1
		`, limit)
		if err != nil {
			log.Printf("Query without filter also failed: %v\n", err)
			return nil, err
		}
	}
	defer rows.Close()

	alerts := make([]AlertSummary, 0)
	for rows.Next() {
		var alert AlertSummary
		var description sql.NullString
		err := rows.Scan(&alert.ID, &alert.Title, &alert.Severity, &alert.Status,
			&alert.Source, &description, &alert.CreatedAt, &alert.UpdatedAt)
		if err != nil {
			log.Printf("Failed to scan alert row: %v\n", err)
			continue
		}
		if description.Valid {
			// Limit description length
			if len(description.String) > 200 {
				alert.Description = description.String[:200] + "..."
			} else {
				alert.Description = description.String
			}
		}
		alerts = append(alerts, alert)
		log.Printf("Found alert: id=%s, title=%s, status=%s, severity=%s\n",
			alert.ID, alert.Title, alert.Status, alert.Severity)
	}

	log.Printf("Total alerts retrieved: %d\n", len(alerts))
	return alerts, nil
}

func (h *AIHandler) getAlertStats() (*AlertStats, error) {
	stats := &AlertStats{}

	// Try with status filter first
	err := h.db.QueryRow(`
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE severity = 'critical') as critical,
			COUNT(*) FILTER (WHERE severity = 'high') as high,
			COUNT(*) FILTER (WHERE severity = 'medium') as medium,
			COUNT(*) FILTER (WHERE severity = 'low') as low
		FROM alerts
		WHERE status NOT IN ('resolved', 'closed')
	`).Scan(&stats.Total, &stats.Critical, &stats.High, &stats.Medium, &stats.Low)

	if err != nil {
		log.Printf("Stats query with filter failed: %v, trying without filter\n", err)
		// Try without status filter
		err = h.db.QueryRow(`
			SELECT
				COUNT(*) as total,
				COUNT(*) FILTER (WHERE severity = 'critical') as critical,
				COUNT(*) FILTER (WHERE severity = 'high') as high,
				COUNT(*) FILTER (WHERE severity = 'medium') as medium,
				COUNT(*) FILTER (WHERE severity = 'low') as low
			FROM alerts
		`).Scan(&stats.Total, &stats.Critical, &stats.High, &stats.Medium, &stats.Low)
	}

	if err == nil {
		log.Printf("Alert Stats: Total=%d, Critical=%d, High=%d, Medium=%d, Low=%d\n",
			stats.Total, stats.Critical, stats.High, stats.Medium, stats.Low)
	}

	return stats, err
}

func (h *AIHandler) getRecentIncidents(limit int) ([]IncidentSummary, error) {
	rows, err := h.db.Query(`
		SELECT id, title, severity, status, COALESCE(priority,''), COALESCE(description,''), created_at
		FROM incidents
		WHERE status NOT IN ('resolved','closed')
		ORDER BY
			CASE severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,
			created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		// Fallback without priority (older schema)
		rows, err = h.db.Query(`
			SELECT id, title, severity, status, '', COALESCE(description,''), created_at
			FROM incidents
			WHERE status != 'resolved'
			ORDER BY created_at DESC
			LIMIT $1
		`, limit)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	incidents := make([]IncidentSummary, 0)
	for rows.Next() {
		var i IncidentSummary
		var desc string
		if err := rows.Scan(&i.ID, &i.Title, &i.Severity, &i.Status, &i.Priority, &desc, &i.CreatedAt); err != nil {
			continue
		}
		if len(desc) > 200 {
			i.Description = desc[:200] + "..."
		} else {
			i.Description = desc
		}
		incidents = append(incidents, i)
	}
	return incidents, nil
}

func (h *AIHandler) getIncidentStats() (*IncidentStats, error) {
	stats := &IncidentStats{}
	err := h.db.QueryRow(`
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'open') as open,
			COUNT(*) FILTER (WHERE status = 'investigating') as investigating,
			COUNT(*) FILTER (WHERE severity = 'critical') as critical,
			COUNT(*) FILTER (WHERE severity = 'high') as high
		FROM incidents
		WHERE status NOT IN ('resolved','closed')
	`).Scan(&stats.Total, &stats.Open, &stats.Investigating, &stats.Critical, &stats.High)
	return stats, err
}

func (h *AIHandler) getK8sClusters() ([]K8sClusterInfo, error) {
	rows, err := h.db.Query(`
		SELECT name, COALESCE(environment,''), COALESCE(region,''),
		       COALESCE(node_count,0), COALESCE(pod_count,0), COALESCE(sync_status,'unknown')
		FROM k8s_cluster_configs
		WHERE enabled = true
		ORDER BY name
		LIMIT 20
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	clusters := make([]K8sClusterInfo, 0)
	for rows.Next() {
		var c K8sClusterInfo
		if err := rows.Scan(&c.Name, &c.Environment, &c.Region, &c.NodeCount, &c.PodCount, &c.SyncStatus); err != nil {
			continue
		}
		clusters = append(clusters, c)
	}
	return clusters, nil
}

func (h *AIHandler) getTopologyOverview() (*TopologyOverview, error) {
	overview := &TopologyOverview{
		BySource: make(map[string]int),
		ByType:   make(map[string]int),
	}

	// Total + unhealthy count
	err := h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE health_status NOT IN ('healthy','unknown') AND health_status IS NOT NULL)
		FROM topology_entities
	`).Scan(&overview.TotalEntities, &overview.UnhealthyCount)
	if err != nil {
		return nil, err
	}

	// Breakdown by source
	sourceRows, err := h.db.Query(`
		SELECT source, COUNT(*) FROM topology_entities GROUP BY source ORDER BY COUNT(*) DESC LIMIT 10
	`)
	if err == nil {
		defer sourceRows.Close()
		for sourceRows.Next() {
			var src string
			var cnt int
			if err := sourceRows.Scan(&src, &cnt); err == nil {
				overview.BySource[src] = cnt
			}
		}
	}

	// Breakdown by entity type (top 10)
	typeRows, err := h.db.Query(`
		SELECT entity_type, COUNT(*) FROM topology_entities GROUP BY entity_type ORDER BY COUNT(*) DESC LIMIT 10
	`)
	if err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var etype string
			var cnt int
			if err := typeRows.Scan(&etype, &cnt); err == nil {
				overview.ByType[etype] = cnt
			}
		}
	}

	return overview, nil
}

// getMockResponseWithContext provides context-aware mock responses
func (h *AIHandler) getMockResponseWithContext(messages []oidc.ChatMessage, userID string) *oidc.ChatResponse {
	var lastUserMessage string
	var systemContext string

	// Extract user message and system context
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserMessage = strings.ToLower(messages[i].Content)
		}
		if messages[i].Role == "system" {
			systemContext = messages[i].Content
		}
	}

	log.Printf("Mock AI: User asked: '%s'\n", lastUserMessage)
	log.Printf("Mock AI: System context length: %d bytes\n", len(systemContext))

	var response string

	// If we have system context with actual data, use it
	if systemContext != "" && (strings.Contains(systemContext, "RECENT ALERTS") ||
		strings.Contains(systemContext, "ALERT COUNTS") ||
		strings.Contains(systemContext, "OPEN INCIDENTS") ||
		strings.Contains(systemContext, "KUBERNETES CLUSTERS") ||
		strings.Contains(systemContext, "TOPOLOGY")) {
		log.Printf("Mock AI: Using AlertHub context data\n")
		// Parse and analyze the actual alert data
		if strings.Contains(lastUserMessage, "analyze") || strings.Contains(lastUserMessage, "recent") {
			response = h.generateAlertAnalysis(systemContext)
		} else if strings.Contains(lastUserMessage, "critical") {
			response = h.generateCriticalAlertsSummary(systemContext)
		} else {
			response = h.generateGeneralInsights(systemContext)
		}
	} else {
		log.Printf("Mock AI: No AlertHub context available, using generic response\n")
		// Fallback to generic responses
		mockResponses := map[string]string{
			"hello":   "Hello! I'm your AlertHub AI assistant with access to your system data. I can help you analyze alerts, incidents, and system metrics. What would you like to know?",
			"help":    "I can help you with:\n- Analyzing current alerts and incidents\n- Identifying critical issues\n- Spotting trends and patterns\n- Providing actionable recommendations\n- Troubleshooting guidance\n\nWhat would you like me to analyze?",
			"alert":   "I'm checking your AlertHub database for alerts... \n\nNote: It appears there may be no active alerts in your system currently, or I'm unable to connect to the database. Please ensure:\n1. The database contains alert data\n2. Alerts are in 'firing' or 'acknowledged' status\n3. Database connection is working",
			"analyze": "I'm attempting to analyze your alerts... \n\n**Status**: It appears there are currently no active alerts in your AlertHub system, or I'm unable to retrieve them. This could mean:\n- Your system is healthy with no active alerts\n- Alerts may have been resolved\n- Database connectivity issues\n\nWould you like me to help with something else?",
		}

		response = "I'm your AlertHub AI assistant. I can analyze your alerts, incidents, and system data in real-time. What would you like me to help with?"
		for keyword, mockResp := range mockResponses {
			if strings.Contains(lastUserMessage, keyword) {
				response = mockResp
				break
			}
		}
	}

	log.Printf("Mock AI: Generated response (%d chars)\n", len(response))

	return &oidc.ChatResponse{
		ID:      uuid.New().String(),
		Model:   "alerthub-ai",
		Message: oidc.ChatMessage{Role: "assistant", Content: response},
		Usage: oidc.TokenUsage{
			PromptTokens:     150,
			CompletionTokens: 200,
			TotalTokens:      350,
		},
	}
}

func (h *AIHandler) generateAlertAnalysis(context string) string {
	// Extract key metrics from context
	totalAlerts := h.extractMetric(context, "Total Active:")
	criticalAlerts := h.extractMetric(context, "Critical:")

	analysis := fmt.Sprintf(`## Alert System Analysis

**Current Status:**
- **Total Active Alerts:** %s
- **Critical Alerts:** %s

**Key Findings:**`, totalAlerts, criticalAlerts)

	if strings.Contains(context, `"severity": "critical"`) {
		analysis += "\n- **CRITICAL ALERTS DETECTED** - Immediate attention required"
	}

	// Extract alert titles from JSON
	alertTitles := h.extractAlertTitles(context)
	if len(alertTitles) > 0 {
		analysis += "\n\n**Recent Alerts:**"
		for i, title := range alertTitles {
			if i >= 5 {
				break
			}
			analysis += fmt.Sprintf("\n%d. %s", i+1, title)
		}
	}

	analysis += "\n\n**Recommendations:**\n1. Address critical severity alerts first\n2. Look for patterns in recurring alerts\n3. Check alert correlations for potential incidents\n4. Review acknowledgment and resolution times"

	return analysis
}

func (h *AIHandler) generateCriticalAlertsSummary(context string) string {
	criticalCount := h.extractMetric(context, "Critical:")

	summary := fmt.Sprintf(`## Critical Alerts Summary

**Critical Alert Count:** %s

`, criticalCount)

	// Extract critical alerts from context
	if strings.Contains(context, `"severity": "critical"`) {
		summary += "**Critical Alerts Requiring Attention:**\n"
		titles := h.extractAlertTitles(context)
		for i, title := range titles {
			if i >= 5 {
				break
			}
			summary += fmt.Sprintf("- %s\n", title)
		}
	} else {
		summary += "No critical alerts currently active.\n"
	}

	summary += "\n**Next Steps:**\n1. Investigate root causes\n2. Coordinate with on-call team\n3. Document resolution steps\n4. Update runbooks if needed"

	return summary
}

func (h *AIHandler) generateGeneralInsights(context string) string {
	total := h.extractMetric(context, "Total Active:")

	return fmt.Sprintf(`## System Health Overview

**Alert Status:**
- Active Alerts: %s
- System Status: Monitoring

I have access to your current alerts and incidents. You can ask me to:
- Analyze recent alert patterns
- Identify critical issues
- Provide troubleshooting guidance
- Suggest optimization strategies

What would you like to explore?`, total)
}

func (h *AIHandler) extractMetric(context, label string) string {
	idx := strings.Index(context, label)
	if idx == -1 {
		return "0"
	}

	startIdx := idx + len(label)
	endIdx := strings.IndexAny(context[startIdx:], "\n,")
	if endIdx == -1 {
		return strings.TrimSpace(context[startIdx:])
	}

	return strings.TrimSpace(context[startIdx : startIdx+endIdx])
}

func (h *AIHandler) extractAlertTitles(context string) []string {
	titles := make([]string, 0)
	lines := strings.Split(context, "\n")

	for _, line := range lines {
		if strings.Contains(line, `"title":`) {
			// Extract title value
			startIdx := strings.Index(line, `"title": "`)
			if startIdx != -1 {
				startIdx += len(`"title": "`)
				endIdx := strings.Index(line[startIdx:], `"`)
				if endIdx != -1 {
					title := line[startIdx : startIdx+endIdx]
					titles = append(titles, title)
				}
			}
		}
	}

	return titles
}

// ListTools returns available AI tools for the user
// @Summary List AI tools
// @Description Get list of AI tools available to the current user based on permissions
// @Tags AI
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/tools [get]
func (h *AIHandler) ListTools(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	// Convert user_id to string
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	executor := NewAIToolExecutor(h.db, userIDStr)
	tools := executor.GetAvailableTools()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"tools":   tools,
		"count":   len(tools),
	})
}

// ExecuteTool executes an AI tool with security checks
// @Summary Execute AI tool
// @Description Execute a specific AI tool with provided parameters
// @Tags AI
// @Accept json
// @Produce json
// @Param request body map[string]interface{} true "Tool execution request"
// @Success 200 {object} AIToolResult
// @Router /api/v1/ai/tools/execute [post]
func (h *AIHandler) ExecuteTool(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	// Convert user_id to string
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	var req struct {
		Tool   string                 `json:"tool" binding:"required"`
		Params map[string]interface{} `json:"params"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	executor := NewAIToolExecutor(h.db, userIDStr)
	result := executor.ExecuteTool(c.Request.Context(), req.Tool, req.Params)

	statusCode := http.StatusOK
	if !result.Success {
		// Check if it's a permission error
		if strings.Contains(result.Error, "Permission denied") {
			statusCode = http.StatusForbidden
		} else {
			statusCode = http.StatusBadRequest
		}
	}

	c.JSON(statusCode, result)
}

// GetCapabilities returns AI system capabilities
// @Summary Get AI capabilities
// @Description Get comprehensive AI system capabilities and features
// @Tags AI
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/capabilities [get]
func (h *AIHandler) GetCapabilities(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "User not authenticated",
		})
		return
	}

	// Convert user_id to string
	var userIDStr string
	switch v := userID.(type) {
	case uuid.UUID:
		userIDStr = v.String()
	case string:
		userIDStr = v
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Invalid user_id type",
		})
		return
	}

	executor := NewAIToolExecutor(h.db, userIDStr)
	tools := executor.GetAvailableTools()

	// Group tools by category
	categories := make(map[string][]AITool)
	for _, tool := range tools {
		categories[tool.Category] = append(categories[tool.Category], tool)
	}

	capabilities := map[string]interface{}{
		"success": true,
		"version": "1.0",
		"features": map[string]interface{}{
			"chat": map[string]bool{
				"context_aware":   true,
				"real_time_data":  true,
				"multi_turn":      true,
				"session_history": true,
				"streaming":       false, // Future feature
			},
			"tools": map[string]interface{}{
				"enabled":          true,
				"count":            len(tools),
				"categories":       categories,
				"permission_based": true,
				"audit_logged":     true,
			},
			"analytics": map[string]bool{
				"alert_trends":        true,
				"incident_prediction": true,
				"correlation":         true,
				"impact_analysis":     true,
			},
			"security": map[string]interface{}{
				"rbac_enabled":      true,
				"audit_logging":     true,
				"data_isolation":    true,
				"approval_required": []string{"execute_remediation"},
			},
		},
		"data_sources": []string{
			"alerts",
			"incidents",
			"oncall_schedules",
			"audit_logs",
			"metrics",
		},
		"supported_queries": []string{
			"Analyze recent alerts",
			"Show critical alerts",
			"Predict potential incidents",
			"Get alert trends",
			"Find correlated alerts",
			"Show on-call schedule",
			"Calculate business impact",
		},
	}

	c.JSON(http.StatusOK, capabilities)
}

// ============================================================================
// ADVANCED AI ANALYSIS ENDPOINTS
// ============================================================================

// AdvancedAnalysis performs comprehensive analysis on an alert
// @Summary Advanced alert analysis
// @Description Get comprehensive AI analysis including correlations, predictions, and recommendations
// @Tags AI
// @Produce json
// @Param alert_id path string true "Alert ID"
// @Success 200 {object} AdvancedAnalysisResponse
// @Router /api/v1/ai/analyze/{alert_id} [get]
func (h *AIHandler) AdvancedAnalysis(c *gin.Context) {
	alertID := c.Param("alert_id")

	analysis, err := h.advancedAnalyzer.PerformComprehensiveAnalysis(c.Request.Context(), alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to perform analysis: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    analysis,
	})
}

// DetectCorrelations finds correlated alerts
// @Summary Detect alert correlations
// @Description Find alerts that are correlated with the specified alert
// @Tags AI
// @Produce json
// @Param alert_id path string true "Alert ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/correlate/{alert_id} [get]
func (h *AIHandler) DetectCorrelations(c *gin.Context) {
	alertID := c.Param("alert_id")

	correlations, err := h.advancedAnalyzer.DetectAlertCorrelations(c.Request.Context(), alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to detect correlations: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"correlations": correlations,
		"count":        len(correlations),
	})
}

// PredictEscalation predicts if an alert will escalate
// @Summary Predict alert escalation
// @Description Predict if an alert is likely to escalate based on historical patterns
// @Tags AI
// @Produce json
// @Param alert_id path string true "Alert ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/predict/{alert_id} [get]
func (h *AIHandler) PredictEscalation(c *gin.Context) {
	alertID := c.Param("alert_id")

	prediction, err := h.advancedAnalyzer.PredictAlertEscalation(c.Request.Context(), alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to predict escalation: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"prediction": prediction,
	})
}

// GetRunbooks gets runbook recommendations for an alert
// @Summary Get runbook recommendations
// @Description Get relevant runbook recommendations based on alert characteristics
// @Tags AI
// @Produce json
// @Param alert_id path string true "Alert ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/runbooks/{alert_id} [get]
func (h *AIHandler) GetRunbooks(c *gin.Context) {
	alertID := c.Param("alert_id")

	runbooks, err := h.advancedAnalyzer.GetRunbookRecommendations(c.Request.Context(), alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to get runbooks: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"runbooks": runbooks,
		"count":    len(runbooks),
	})
}

// GetAlertClusters gets alert clusters
// @Summary Get alert clusters
// @Description Get grouped alerts based on similarity patterns
// @Tags AI
// @Produce json
// @Param hours query int false "Time window in hours" default(1)
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/clusters [get]
func (h *AIHandler) GetAlertClusters(c *gin.Context) {
	hours := 1
	if hoursStr := c.Query("hours"); hoursStr != "" {
		fmt.Sscanf(hoursStr, "%d", &hours)
	}

	clusters, err := h.advancedAnalyzer.ClusterAlerts(c.Request.Context(), time.Duration(hours)*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to cluster alerts: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"clusters": clusters,
		"count":    len(clusters),
	})
}

// AnalyzeFatigue analyzes alert fatigue
// @Summary Analyze alert fatigue
// @Description Detect alert fatigue conditions and get recommendations
// @Tags AI
// @Produce json
// @Param hours query int false "Time window in hours" default(24)
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/fatigue [get]
func (h *AIHandler) AnalyzeFatigue(c *gin.Context) {
	hours := 24
	if hoursStr := c.Query("hours"); hoursStr != "" {
		fmt.Sscanf(hoursStr, "%d", &hours)
	}

	analysis, err := h.advancedAnalyzer.DetectAlertFatigue(c.Request.Context(), time.Duration(hours)*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to analyze fatigue: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"analysis": analysis,
	})
}

// GenerateRCA generates root cause analysis
// @Summary Generate RCA suggestions
// @Description Generate root cause analysis suggestions for multiple alerts
// @Tags AI
// @Accept json
// @Produce json
// @Param request body map[string]interface{} true "RCA request with alert_ids"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/ai/rca [post]
func (h *AIHandler) GenerateRCA(c *gin.Context) {
	var req struct {
		AlertIDs []string `json:"alert_ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	suggestions, err := h.advancedAnalyzer.GenerateRCASuggestions(c.Request.Context(), req.AlertIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to generate RCA: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"suggestions": suggestions,
		"count":       len(suggestions),
	})
}
