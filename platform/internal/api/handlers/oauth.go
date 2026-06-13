package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/oauth"
)

// OAuthHandler handles Corporate OAuth 2.0 operations
type OAuthHandler struct {
	oauthClient *oauth.OAuthClient
}

// NewOAuthHandler creates a new OAuth handler
func NewOAuthHandler(oauthClient *oauth.OAuthClient) *OAuthHandler {
	return &OAuthHandler{
		oauthClient: oauthClient,
	}
}

// ============================================================================
// TOKEN GENERATION ENDPOINTS
// ============================================================================

// GenerateToken handles OAuth token generation with JWT assertion
// POST /api/v1/oauth/token
func (h *OAuthHandler) GenerateToken(c *gin.Context) {
	var req struct {
		Assertion string `json:"assertion" binding:"required"`
		UserID    string `json:"user_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Extract user ID from context if not provided
	if req.UserID == "" {
		if userID, exists := c.Get("user_id"); exists {
			req.UserID = fmt.Sprintf("%v", userID)
		} else {
			// Extract from JWT assertion (decode middle part)
			parts := strings.Split(req.Assertion, ".")
			if len(parts) >= 2 {
				payload, err := base64.RawURLEncoding.DecodeString(parts[1])
				if err == nil {
					var claims map[string]interface{}
					if json.Unmarshal(payload, &claims) == nil {
						if sub, ok := claims["sub"].(string); ok {
							req.UserID = sub
						}
					}
				}
			}
		}
	}

	if req.UserID == "" {
		req.UserID = "unknown"
	}

	// Generate multi-audience token
	token, err := h.oauthClient.GenerateToken(c.Request.Context(), req.Assertion, req.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Token generation failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"access_token":  token.AccessToken,
			"refresh_token": token.RefreshToken,
			"token_type":    token.TokenType,
			"expires_in":    token.ExpiresIn,
			"scope":         token.Scope,
		},
	})
}

// RefreshToken handles token refresh
// POST /api/v1/oauth/refresh
func (h *OAuthHandler) RefreshToken(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
		UserID       string `json:"user_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Extract user ID from context if not provided
	if req.UserID == "" {
		if userID, exists := c.Get("user_id"); exists {
			req.UserID = fmt.Sprintf("%v", userID)
		} else {
			req.UserID = "unknown"
		}
	}

	token, err := h.oauthClient.RefreshToken(c.Request.Context(), req.RefreshToken, req.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Token refresh failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"access_token": token.AccessToken,
			"token_type":   token.TokenType,
			"expires_in":   token.ExpiresIn,
			"scope":        token.Scope,
		},
	})
}

// ============================================================================
// FLOODGATE PROXY ENDPOINTS
// ============================================================================

// ProxyToOIDC Provider forwards requests to OIDC Provider with user identity
// ALL /api/v1/oidc/*
func (h *OAuthHandler) ProxyToOIDC Provider(c *gin.Context) {
	// Extract user information from context (set by MAS middleware)
	userID := ""
	userIP := ""

	if uid, exists := c.Get("user_id"); exists {
		userID = fmt.Sprintf("%v", uid)
	}

	// Get user's IP from forwarded headers
	userIP = c.GetHeader("X-Forwarded-For")
	if userIP == "" {
		userIP = c.GetHeader("X-Real-IP")
	}
	if userIP == "" {
		userIP = c.ClientIP()
	}

	// Extract first IP if multiple
	if strings.Contains(userIP, ",") {
		userIP = strings.TrimSpace(strings.Split(userIP, ",")[0])
	}

	if userIP == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "User IP required for OIDC Provider requests",
		})
		return
	}

	// Get OIDC Provider path from URL
	oidcPath := strings.TrimPrefix(c.Request.URL.Path, "/api/v1/oidc")
	if oidcPath == "" {
		oidcPath = "/"
	}

	// Add query parameters
	if c.Request.URL.RawQuery != "" {
		oidcPath += "?" + c.Request.URL.RawQuery
	}

	// Read request body
	var body []byte
	if c.Request.Body != nil {
		body, _ = io.ReadAll(c.Request.Body)
	}

	// Get multi-audience token (from header or context)
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Authorization token required",
		})
		return
	}

	// Create OIDC Provider request
	oidcReq := &oauth.OIDC ProviderRequest{
		Method:        c.Request.Method,
		Path:          oidcPath,
		Headers:       make(map[string]string),
		Body:          body,
		UserIP:        userIP,
		UserID:        userID,
		MultiAudToken: token,
	}

	// Copy relevant headers
	for key := range c.Request.Header {
		if key != "Authorization" && key != "Host" {
			oidcReq.Headers[key] = c.GetHeader(key)
		}
	}

	// Proxy to OIDC Provider
	resp, err := h.oauthClient.ProxyToOIDC Provider(c.Request.Context(), oidcReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "OIDC Provider request failed: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key := range resp.Header {
		c.Writer.Header().Set(key, resp.Header.Get(key))
	}

	// Copy response body
	responseBody, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), responseBody)
}

// ============================================================================
// TOKEN MANAGEMENT
// ============================================================================

// ClearTokenCache clears all cached tokens
// POST /api/v1/oauth/cache/clear
func (h *OAuthHandler) ClearTokenCache(c *gin.Context) {
	h.oauthClient.ClearCache()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token cache cleared",
	})
}

// ClearUserToken clears cached token for specific user
// DELETE /api/v1/oauth/cache/:user_id
func (h *OAuthHandler) ClearUserToken(c *gin.Context) {
	userID := c.Param("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "User ID required",
		})
		return
	}

	h.oauthClient.ClearUserToken(userID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Token cleared for user %s", userID),
	})
}

// ============================================================================
// ROUTE REGISTRATION
// ============================================================================

// RegisterRoutes registers OAuth routes
func (h *OAuthHandler) RegisterRoutes(protected *gin.RouterGroup) {
	oauth := protected.Group("/oauth")
	{
		// Token management
		oauth.POST("/token", h.GenerateToken)
		oauth.POST("/refresh", h.RefreshToken)

		// Cache management (admin only)
		oauth.POST("/cache/clear", h.ClearTokenCache)
		oauth.DELETE("/cache/:user_id", h.ClearUserToken)
	}

	// OIDC Provider proxy - all methods
	oidc := protected.Group("/oidc")
	{
		oidc.Any("/*path", h.ProxyToOIDC Provider)
	}
}
