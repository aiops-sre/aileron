package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/jwt"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
	"github.com/aileron-platform/aileron/platform/internal/services/sso"
)

// AuthHandler handles authentication endpoints
type AuthHandler struct {
	rbacService *rbac.RBACService
	jwtService  *jwt.JWTService
	ssoManager  *sso.SSOManager
	db          *sql.DB
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(rbacService *rbac.RBACService, jwtService *jwt.JWTService, ssoManager *sso.SSOManager, db *sql.DB) *AuthHandler {
	return &AuthHandler{
		rbacService: rbacService,
		jwtService:  jwtService,
		ssoManager:  ssoManager,
		db:          db,
	}
}

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Remember bool   `json:"remember"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Success bool       `json:"success"`
	Message string     `json:"message,omitempty"`
	Data    *LoginData `json:"data,omitempty"`
}

// LoginData contains login response data
type LoginData struct {
	User   *rbac.User     `json:"user"`
	Tokens *jwt.TokenPair `json:"tokens"`
}

// Login handles user login
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	// Authenticate user
	user, err := h.rbacService.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		// Log failed attempt
		h.logAuditEvent(c, nil, "login_failed", "auth", nil, map[string]interface{}{
			"username": req.Username,
			"error":    err.Error(),
		})

		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid credentials",
		})
		return
	}

	// Generate JWT tokens
	tokens, err := h.jwtService.GenerateTokenPair(
		user.ID,
		user.Username,
		user.Email,
		user.RoleName,
		user.Permissions,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate tokens",
		})
		return
	}

	// Store session
	err = h.createSession(c, user.ID, tokens.AccessToken, tokens.RefreshToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create session",
		})
		return
	}

	// Log successful login
	h.logAuditEvent(c, &user.ID, "login_success", "auth", nil, map[string]interface{}{
		"username": user.Username,
	})

	// Remove sensitive data
	user.PasswordHash = ""

	c.JSON(http.StatusOK, LoginResponse{
		Success: true,
		Message: "Login successful",
		Data: &LoginData{
			User:   user,
			Tokens: tokens,
		},
	})
}

// LDAPLoginRequest represents LDAP login request
type LDAPLoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginLDAP handles LDAP authentication
func (h *AuthHandler) LoginLDAP(c *gin.Context) {
	var req LDAPLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	// Get LDAP provider
	provider, err := h.ssoManager.GetProvider("ldap")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "LDAP authentication not available",
		})
		return
	}

	// Authenticate via LDAP
	credentials := map[string]string{
		"username": req.Username,
		"password": req.Password,
	}
	result, err := provider.Authenticate(c.Request.Context(), credentials)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "LDAP authentication failed",
		})
		return
	}

	ldapUser, ok := result.(*sso.LDAPUser)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Invalid LDAP response",
		})
		return
	}

	// Map LDAP groups to role
	ssoService := sso.NewSSOService(&sso.SSOConfig{})
	roleName := ssoService.MapLDAPGroupsToRole(ldapUser.Groups)

	// Sync user to local database
	user, err := h.syncLDAPUser(c, ldapUser, roleName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to sync user",
		})
		return
	}

	// Generate tokens
	tokens, err := h.jwtService.GenerateTokenPair(
		user.ID,
		user.Username,
		user.Email,
		user.RoleName,
		user.Permissions,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate tokens",
		})
		return
	}

	// Create session
	if err := h.createSession(c, user.ID, tokens.AccessToken, tokens.RefreshToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create session"})
		return
	}

	// Log successful LDAP login
	h.logAuditEvent(c, &user.ID, "ldap_login_success", "auth", nil, map[string]interface{}{
		"username": user.Username,
		"groups":   ldapUser.Groups,
	})

	c.JSON(http.StatusOK, LoginResponse{
		Success: true,
		Message: "LDAP login successful",
		Data: &LoginData{
			User:   user,
			Tokens: tokens,
		},
	})
}

// RefreshTokenRequest represents refresh token request
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RefreshToken handles token refresh
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	// Validate refresh token
	claims, err := h.jwtService.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid refresh token",
		})
		return
	}

	// Get user to fetch latest permissions
	user, err := h.rbacService.GetUser(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Generate new token pair
	tokens, err := h.jwtService.RefreshAccessToken(req.RefreshToken, user.Permissions)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to refresh token",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tokens,
	})
}

// Logout handles user logout
func (h *AuthHandler) Logout(c *gin.Context) {
	// Get user from context (set by auth middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not authenticated",
		})
		return
	}

	// Get token ID from context
	tokenID, _ := c.Get("token_id")

	// Revoke token
	if tokenID != nil {
		h.jwtService.RevokeToken(tokenID.(string))
	}

	// Delete session
	h.deleteSession(c, userID.(uuid.UUID))

	// Log logout
	uid := userID.(uuid.UUID)
	h.logAuditEvent(c, &uid, "logout", "auth", nil, nil)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Logged out successfully",
	})
}

// GetCurrentUser returns current authenticated user
func (h *AuthHandler) GetCurrentUser(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not authenticated",
		})
		return
	}

	user, err := h.rbacService.GetUser(c.Request.Context(), userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    user,
	})
}

// ChangePasswordRequest represents password change request
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// ChangePassword handles password change
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not authenticated",
		})
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	// Change password
	err := h.rbacService.ChangePassword(c.Request.Context(), userID.(uuid.UUID), req.OldPassword, req.NewPassword)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Failed to change password: " + err.Error(),
		})
		return
	}

	// Log password change
	uid := userID.(uuid.UUID)
	h.logAuditEvent(c, &uid, "password_changed", "user", &uid, nil)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Password changed successfully",
	})
}

// Helper functions

func (h *AuthHandler) createSession(c *gin.Context, userID uuid.UUID, accessToken, refreshToken string) error {
	// Hash tokens with SHA-256 before storage (never store raw tokens in DB)
	hashToken := func(t string) string {
		h := sha256.Sum256([]byte(t))
		return hex.EncodeToString(h[:])
	}

	query := `
		INSERT INTO user_sessions (id, user_id, token_hash, refresh_token_hash, ip_address, user_agent, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := h.db.ExecContext(c.Request.Context(), query,
		uuid.New(),
		userID,
		hashToken(accessToken),
		hashToken(refreshToken),
		c.ClientIP(),
		c.Request.UserAgent(),
		time.Now().Add(24*time.Hour),
		time.Now(),
	)

	return err
}

func (h *AuthHandler) deleteSession(c *gin.Context, userID uuid.UUID) error {
	query := "DELETE FROM user_sessions WHERE user_id = $1"
	_, err := h.db.ExecContext(c.Request.Context(), query, userID)
	return err
}

func (h *AuthHandler) syncLDAPUser(c *gin.Context, ldapUser *sso.LDAPUser, roleName string) (*rbac.User, error) {
	// Check if user exists
	var existingUserID uuid.UUID
	err := h.db.QueryRowContext(c.Request.Context(),
		"SELECT id FROM users WHERE email = $1",
		ldapUser.Email,
	).Scan(&existingUserID)

	if err == sql.ErrNoRows {
		// Create new user
		// Get role ID
		var roleID uuid.UUID
		err = h.db.QueryRowContext(c.Request.Context(),
			"SELECT id FROM roles WHERE name = $1",
			roleName,
		).Scan(&roleID)

		if err != nil {
			return nil, err
		}

		user := &rbac.User{
			Username:    ldapUser.Username,
			Email:       ldapUser.Email,
			FullName:    ldapUser.FullName,
			RoleID:      roleID,
			IsActive:    true,
			IsVerified:  true,
			Timezone:    "UTC",
			Preferences: make(map[string]interface{}),
		}

		// Create user with random password (won't be used for LDAP users)
		err = h.rbacService.CreateUser(c.Request.Context(), user, uuid.New().String())
		if err != nil {
			return nil, err
		}

		return h.rbacService.GetUser(c.Request.Context(), user.ID)
	}

	if err != nil {
		return nil, err
	}

	// User exists, return it
	return h.rbacService.GetUser(c.Request.Context(), existingUserID)
}

func (h *AuthHandler) logAuditEvent(c *gin.Context, userID *uuid.UUID, action, resourceType string, resourceID *uuid.UUID, details map[string]interface{}) {
	detailsJSON, _ := json.Marshal(details)

	query := `
		INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	h.db.ExecContext(c.Request.Context(), query,
		uuid.New(),
		userID,
		action,
		resourceType,
		resourceID,
		detailsJSON,
		c.ClientIP(),
		c.Request.UserAgent(),
		time.Now(),
	)
}

// GetSSOProviders returns list of enabled SSO providers
func (h *AuthHandler) GetSSOProviders(c *gin.Context) {
	// Initialize empty slice (not nil) to ensure JSON returns [] not null
	enabledProviders := make([]string, 0)

	// Check SAML from database
	var samlEnabled bool
	err := h.db.QueryRow("SELECT enabled FROM saml_config WHERE enabled = true LIMIT 1").Scan(&samlEnabled)
	if err == nil && samlEnabled {
		enabledProviders = append(enabledProviders, "saml")
	}

	// Check LDAP (from SSO manager for now)
	staticProviders := h.ssoManager.ListEnabledProviders()
	for _, provider := range staticProviders {
		if provider == "ldap" || provider == "oauth" {
			enabledProviders = append(enabledProviders, provider)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"providers": enabledProviders,
		},
	})
}

// ValidateToken validates a JWT token
func (h *AuthHandler) ValidateToken(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	tokenString, err := jwt.ExtractTokenFromHeader(authHeader)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid authorization header",
		})
		return
	}

	claims, err := h.jwtService.ValidateToken(tokenString)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid token",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"valid":       true,
			"user_id":     claims.UserID,
			"username":    claims.Username,
			"role":        claims.Role,
			"permissions": claims.Permissions,
			"expires_at":  claims.ExpiresAt,
		},
	})
}

// SAMLLoginRequest represents SAML login request
type SAMLLoginRequest struct {
	SAMLResponse string `json:"saml_response" binding:"required"`
	RelayState   string `json:"relay_state"`
}

// LoginSAML handles SAML authentication
func (h *AuthHandler) LoginSAML(c *gin.Context) {
	var req SAMLLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	// Get SAML provider
	provider, err := h.ssoManager.GetProvider("saml")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "SAML authentication not available",
		})
		return
	}

	// Authenticate via SAML
	credentials := map[string]string{
		"saml_response": req.SAMLResponse,
	}
	result, err := provider.Authenticate(c.Request.Context(), credentials)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "SAML authentication failed: " + err.Error(),
		})
		return
	}

	samlUser, ok := result.(*sso.SAMLUser)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Invalid SAML response",
		})
		return
	}

	// Sync user to local database
	user, err := h.syncSAMLUser(c, samlUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to sync user: " + err.Error(),
		})
		return
	}

	// Generate tokens
	tokens, err := h.jwtService.GenerateTokenPair(
		user.ID,
		user.Username,
		user.Email,
		user.RoleName,
		user.Permissions,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate tokens",
		})
		return
	}

	// Create session
	if err := h.createSession(c, user.ID, tokens.AccessToken, tokens.RefreshToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create session"})
		return
	}

	// Log successful SAML login
	h.logAuditEvent(c, &user.ID, "saml_login_success", "auth", nil, map[string]interface{}{
		"email":  user.Email,
		"groups": samlUser.Groups,
	})

	c.JSON(http.StatusOK, LoginResponse{
		Success: true,
		Message: "SAML login successful",
		Data: &LoginData{
			User:   user,
			Tokens: tokens,
		},
	})
}

// GetSAMLMetadata returns SAML service provider metadata
func (h *AuthHandler) GetSAMLMetadata(c *gin.Context) {
	// Return SAML SP metadata XML
	// In production, use github.com/crewjam/saml to generate this
	metadata := `<?xml version="1.0"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata"
                  entityID="https://alerthub.example.com/saml">
  <SPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <AssertionConsumerService
      Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"
      Location="https://alerthub.example.com/api/v1/auth/saml/acs"
      index="1"/>
  </SPSSODescriptor>
</EntityDescriptor>`

	c.Header("Content-Type", "application/xml")
	c.String(http.StatusOK, metadata)
}

// SAMLCallback handles SAML assertion consumer service (ACS) callback
func (h *AuthHandler) SAMLCallback(c *gin.Context) {
	// Get SAML response from form POST
	samlResponse := c.PostForm("SAMLResponse")
	relayState := c.PostForm("RelayState")

	if samlResponse == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Missing SAML response",
		})
		return
	}

	// Get SAML provider
	provider, err := h.ssoManager.GetProvider("saml")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "SAML authentication not available",
		})
		return
	}

	// Authenticate via SAML
	credentials := map[string]string{
		"saml_response": samlResponse,
	}
	result, err := provider.Authenticate(c.Request.Context(), credentials)
	if err != nil {
		c.HTML(http.StatusUnauthorized, "error.html", gin.H{
			"error": "SAML authentication failed: " + err.Error(),
		})
		return
	}

	samlUser, ok := result.(*sso.SAMLUser)
	if !ok {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{
			"error": "Invalid SAML response",
		})
		return
	}

	// Sync user to local database
	user, err := h.syncSAMLUser(c, samlUser)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{
			"error": "Failed to sync user",
		})
		return
	}

	// Generate tokens
	tokens, err := h.jwtService.GenerateTokenPair(
		user.ID,
		user.Username,
		user.Email,
		user.RoleName,
		user.Permissions,
	)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{
			"error": "Failed to generate tokens",
		})
		return
	}

	// Create session
	if err := h.createSession(c, user.ID, tokens.AccessToken, tokens.RefreshToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create session"})
		return
	}

	// Log successful SAML login
	h.logAuditEvent(c, &user.ID, "saml_login_success", "auth", nil, map[string]interface{}{
		"email":  user.Email,
		"groups": samlUser.Groups,
	})

	// Redirect to dashboard with token — secure=true in all deployments (TLS terminated at ingress)
	c.SetCookie("access_token", tokens.AccessToken, 86400, "/", "", true, true)
	// Validate RelayState is a safe relative path — no host component and no
	// protocol-relative URLs (e.g. //evil.com) which some browsers normalise to https.
	redirectURL := "/"
	if relayState != "" {
		if parsed, err := url.Parse(relayState); err == nil &&
			parsed.Host == "" && parsed.Scheme == "" &&
			strings.HasPrefix(relayState, "/") && !strings.HasPrefix(relayState, "//") {
			redirectURL = relayState
		}
	}
	c.Redirect(http.StatusFound, redirectURL)
}

func (h *AuthHandler) syncSAMLUser(c *gin.Context, samlUser *sso.SAMLUser) (*rbac.User, error) {
	// Check if user exists
	var existingUserID uuid.UUID
	err := h.db.QueryRowContext(c.Request.Context(),
		"SELECT id FROM users WHERE email = $1",
		samlUser.Email,
	).Scan(&existingUserID)

	if err == sql.ErrNoRows {
		// Create new user
		// Determine role from SAML groups
		ssoService := sso.NewSSOService(&sso.SSOConfig{})
		roleName := ssoService.MapLDAPGroupsToRole(samlUser.Groups) // Reuse group mapping

		// Get role ID
		var roleID uuid.UUID
		err = h.db.QueryRowContext(c.Request.Context(),
			"SELECT id FROM roles WHERE name = $1",
			roleName,
		).Scan(&roleID)

		if err != nil {
			// Default to viewer role if mapping fails
			err = h.db.QueryRowContext(c.Request.Context(),
				"SELECT id FROM roles WHERE name = 'viewer'",
			).Scan(&roleID)
			if err != nil {
				return nil, err
			}
		}

		user := &rbac.User{
			Username:    samlUser.Email, // Use email as username
			Email:       samlUser.Email,
			FullName:    samlUser.FullName,
			RoleID:      roleID,
			IsActive:    true,
			IsVerified:  true,
			Timezone:    "UTC",
			Preferences: make(map[string]interface{}),
		}

		// Create user with random password (won't be used for SAML users)
		err = h.rbacService.CreateUser(c.Request.Context(), user, uuid.New().String())
		if err != nil {
			return nil, err
		}

		return h.rbacService.GetUser(c.Request.Context(), user.ID)
	}

	if err != nil {
		return nil, err
	}

	// User exists, update last login
	_, err = h.db.ExecContext(c.Request.Context(),
		"UPDATE users SET last_login = NOW(), updated_at = NOW() WHERE id = $1",
		existingUserID,
	)

	// Return existing user
	return h.rbacService.GetUser(c.Request.Context(), existingUserID)
}

// RegisterRoutes registers authentication routes
func (h *AuthHandler) RegisterRoutes(router *gin.RouterGroup, authMiddleware func() gin.HandlerFunc) {
	auth := router.Group("/auth")
	{
		// Public routes
		auth.POST("/login", h.Login) // manual-login fallback
		auth.POST("/refresh", h.RefreshToken)
		auth.POST("/validate", h.ValidateToken)

		// Protected routes - require authentication
		authMiddlewareFunc := authMiddleware()
		auth.POST("/logout", authMiddlewareFunc, h.Logout)
		auth.POST("/change-password", authMiddlewareFunc, h.ChangePassword)
		auth.GET("/me", authMiddlewareFunc, h.GetCurrentUser)
	}
}
