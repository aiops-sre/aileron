package handlers

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/cache"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
)

// UserHandler handles user management endpoints
type UserHandler struct {
	rbacService *rbac.RBACService
	db          *sql.DB
	redis       *cache.RedisCache
}

// NewUserHandler creates a new user handler
func NewUserHandler(rbacService *rbac.RBACService, db *sql.DB, redisCache ...*cache.RedisCache) *UserHandler {
	h := &UserHandler{
		rbacService: rbacService,
		db:          db,
	}
	if len(redisCache) > 0 {
		h.redis = redisCache[0]
	}
	return h
}

// CreateUserRequest represents user creation request
type CreateUserRequest struct {
	Username    string                 `json:"username" binding:"required"`
	Email       string                 `json:"email" binding:"required,email"`
	Password    string                 `json:"password" binding:"required,min=8"`
	FullName    string                 `json:"full_name" binding:"required"`
	RoleID      uuid.UUID              `json:"role_id" binding:"required"`
	Phone       string                 `json:"phone"`
	Timezone    string                 `json:"timezone"`
	Preferences map[string]interface{} `json:"preferences"`
}

// UpdateUserRequest represents user update request
type UpdateUserRequest struct {
	FullName    string                 `json:"full_name"`
	RoleID      *uuid.UUID             `json:"role_id"`
	IsActive    *bool                  `json:"is_active"`
	Phone       string                 `json:"phone"`
	Timezone    string                 `json:"timezone"`
	AvatarURL   string                 `json:"avatar_url"`
	Preferences map[string]interface{} `json:"preferences"`
}

// ListUsers handles listing all users
func (h *UserHandler) ListUsers(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "users.view"); err != nil {
		log.Printf("ListUsers - Permission denied for user")
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	// Get pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	log.Printf("ListUsers - Fetching users with limit=%d, offset=%d", limit, offset)

	// Get users
	users, total, err := h.rbacService.ListUsers(c.Request.Context(), limit, offset)
	if err != nil {
		log.Printf("ERROR: ListUsers - Failed to fetch users: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch users: " + err.Error(),
		})
		return
	}

	log.Printf("ListUsers - Successfully fetched %d users (total: %d)", len(users), total)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"users": users,
			"pagination": gin.H{
				"total": total,
				"page":  page,
				"limit": limit,
				"pages": (total + limit - 1) / limit,
			},
		},
	})
}

// CreateUser handles user creation
func (h *UserHandler) CreateUser(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "users.create"); err != nil {
		log.Printf("CreateUser - Permission denied")
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("ERROR: CreateUser - Invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	log.Printf("CreateUser - Creating user: %s (email: %s, role_id: %s)", req.Username, req.Email, req.RoleID)

	// Create user
	user := &rbac.User{
		Username:    req.Username,
		Email:       req.Email,
		FullName:    req.FullName,
		RoleID:      req.RoleID,
		Phone:       req.Phone,
		Timezone:    req.Timezone,
		IsActive:    true,
		IsVerified:  false,
		Preferences: req.Preferences,
	}

	if user.Timezone == "" {
		user.Timezone = "UTC"
	}

	err := h.rbacService.CreateUser(c.Request.Context(), user, req.Password)
	if err != nil {
		log.Printf("ERROR: CreateUser - Failed to create user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}

	log.Printf("CreateUser - User created successfully: %s (id: %s)", user.Username, user.ID)

	// Log user creation
	creatorID := h.getUserID(c)
	h.logAuditEvent(c, creatorID, "user_created", "user", &user.ID, map[string]interface{}{
		"username": user.Username,
		"email":    user.Email,
		"role_id":  user.RoleID,
	})

	// Remove password hash before returning
	user.PasswordHash = ""

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "User created successfully",
		"data":    user,
	})
}

// GetUser handles getting a single user
func (h *UserHandler) GetUser(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "users.view"); err != nil {
		log.Printf("GetUser - Permission denied")
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		log.Printf("ERROR: GetUser - Invalid user ID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	log.Printf("GetUser - Fetching user: %s", userID)

	user, err := h.rbacService.GetUser(c.Request.Context(), userID)
	if err != nil {
		log.Printf("ERROR: GetUser - User not found: %v", err)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	log.Printf("GetUser - Successfully fetched user: %s", user.Username)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    user,
	})
}

// UpdateUser handles user updates
func (h *UserHandler) UpdateUser(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "users.update"); err != nil {
		log.Printf("UpdateUser - Permission denied")
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		log.Printf("ERROR: UpdateUser - Invalid user ID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("ERROR: UpdateUser - Invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	log.Printf("UpdateUser - Updating user: %s", userID)

	// Get existing user
	user, err := h.rbacService.GetUser(c.Request.Context(), userID)
	if err != nil {
		log.Printf("ERROR: UpdateUser - User not found: %v", err)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Update fields
	if req.FullName != "" {
		user.FullName = req.FullName
	}
	if req.RoleID != nil {
		user.RoleID = *req.RoleID
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}
	if req.Phone != "" {
		user.Phone = req.Phone
	}
	if req.Timezone != "" {
		user.Timezone = req.Timezone
	}
	if req.AvatarURL != "" {
		user.AvatarURL = req.AvatarURL
	}
	if req.Preferences != nil {
		user.Preferences = req.Preferences
	}

	// Update user
	err = h.rbacService.UpdateUser(c.Request.Context(), user)
	if err != nil {
		log.Printf("ERROR: UpdateUser - Failed to update user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update user: " + err.Error(),
		})
		return
	}

	log.Printf("UpdateUser - User updated successfully: %s", user.Username)

	// Log update
	updaterID := h.getUserID(c)
	h.logAuditEvent(c, updaterID, "user_updated", "user", &userID, map[string]interface{}{
		"updated_fields": req,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User updated successfully",
		"data":    user,
	})
}

// DeleteUser handles user deletion
func (h *UserHandler) DeleteUser(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "users.delete"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	// Prevent self-deletion
	currentUserID := h.getUserID(c)
	if currentUserID != nil && *currentUserID == userID {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Cannot delete your own account",
		})
		return
	}

	// Delete user (soft delete)
	err = h.rbacService.DeleteUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete user",
		})
		return
	}

	// Log deletion
	h.logAuditEvent(c, currentUserID, "user_deleted", "user", &userID, nil)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User deleted successfully",
	})
}

// GetUserPermissions returns permissions for a user — requires auth; non-admins can only query themselves
func (h *UserHandler) GetUserPermissions(c *gin.Context) {
	callerID := h.getUserID(c)
	if callerID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Unauthorized"})
		return
	}

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	// Non-admin callers can only view their own permissions
	callerRole, _ := c.Get("role")
	if *callerID != userID && callerRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Forbidden"})
		return
	}

	permissions, err := h.rbacService.GetUserPermissions(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch permissions",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"permissions": permissions,
		},
	})
}

// GetUserSettings returns the authenticated user's settings
func (h *UserHandler) GetUserSettings(c *gin.Context) {
	userID := h.getUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Unauthorized",
		})
		return
	}

	log.Printf("GetUserSettings - Fetching settings for user: %s", userID)

	user, err := h.rbacService.GetUser(c.Request.Context(), *userID)
	if err != nil {
		log.Printf("ERROR: GetUserSettings - User not found: %v", err)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	log.Printf("GetUserSettings - Successfully fetched settings for: %s", user.Username)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"settings": user.Preferences,
		},
	})
}

// UpdateUserSettingsRequest represents settings update request
type UpdateUserSettingsRequest struct {
	Settings map[string]interface{} `json:"settings" binding:"required"`
}

// UpdateUserSettings updates the authenticated user's settings
func (h *UserHandler) UpdateUserSettings(c *gin.Context) {
	userID := h.getUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Unauthorized",
		})
		return
	}

	var req UpdateUserSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("ERROR: UpdateUserSettings - Invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	log.Printf("UpdateUserSettings - Updating settings for user: %s", userID)

	// Get existing user
	user, err := h.rbacService.GetUser(c.Request.Context(), *userID)
	if err != nil {
		log.Printf("ERROR: UpdateUserSettings - User not found: %v", err)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Update preferences
	user.Preferences = req.Settings

	// Update user in database
	err = h.rbacService.UpdateUser(c.Request.Context(), user)
	if err != nil {
		log.Printf("ERROR: UpdateUserSettings - Failed to update settings: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update settings: " + err.Error(),
		})
		return
	}

	log.Printf("UpdateUserSettings - Settings updated successfully for: %s", user.Username)

	// Log settings update
	h.logAuditEvent(c, userID, "settings_updated", "user", userID, map[string]interface{}{
		"settings_keys": getMapKeys(req.Settings),
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Settings updated successfully",
		"data": gin.H{
			"settings": user.Preferences,
		},
	})
}

// Helper functions

func (h *UserHandler) checkPermission(c *gin.Context, permission string) error {
	userID := h.getUserID(c)
	if userID == nil {
		return rbac.ErrPermissionDenied
	}

	return h.rbacService.CheckPermission(c.Request.Context(), *userID, permission)
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (h *UserHandler) getUserID(c *gin.Context) *uuid.UUID {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		return nil
	}

	userID := userIDVal.(uuid.UUID)
	return &userID
}

func (h *UserHandler) logAuditEvent(c *gin.Context, userID *uuid.UUID, action, resourceType string, resourceID *uuid.UUID, details map[string]interface{}) {
	detailsJSON, _ := json.Marshal(details)

	// Get real client IP from X-Forwarded-For or X-Real-IP headers
	clientIP := c.GetHeader("X-Forwarded-For")
	if clientIP == "" {
		clientIP = c.GetHeader("X-Real-IP")
	}
	if clientIP == "" {
		clientIP = c.ClientIP()
	}

	query := `
		INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := h.db.ExecContext(c.Request.Context(), query,
		uuid.New(),
		userID,
		action,
		resourceType,
		resourceID,
		detailsJSON,
		clientIP,
		c.Request.UserAgent(),
		"success",
		time.Now(),
	)

	if err != nil {
		log.Printf("ERROR: Failed to log audit event: %v", err)
	}
}

// cssPhotoCacheEntry holds a photo URL with its expiry time.
type cssPhotoCacheEntry struct {
	photoURL string
	name     string
	expiry   time.Time
}

// photoCache caches CSS responses per user ID to avoid repeated outbound calls.
var photoCache sync.Map // map[userID]cssPhotoCacheEntry

// GetMyPhoto returns the caller's profile photo.
// Priority: OIDC picture URL (from OIDC profile scope) CSS search empty.
// GET /api/v1/users/me/photo
func (h *UserHandler) GetMyPhoto(c *gin.Context) {
	userID, _ := c.Get("user_id")
	cacheKey := ""
	if userID != nil {
		cacheKey = fmt.Sprintf("%v", userID)
	}

	// Return in-process cache if still fresh (30 min TTL)
	if cacheKey != "" {
		if raw, ok := photoCache.Load(cacheKey); ok {
			entry := raw.(cssPhotoCacheEntry)
			if time.Now().Before(entry.expiry) {
				c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
					"photo": entry.photoURL,
					"name":  entry.name,
				}})
				return
			}
			photoCache.Delete(cacheKey)
		}
	}

	// Priority 1: OIDC picture URL cached in Redis during MAS login 
	if h.redis != nil && cacheKey != "" {
		var pictureURL string
		if err := h.redis.Get("user:photo:"+cacheKey, &pictureURL); err == nil && pictureURL != "" {
			// Look up display name from DB
			var displayName string
			_ = h.db.QueryRowContext(c.Request.Context(), `SELECT full_name FROM users WHERE id = $1`, userID).Scan(&displayName)
			if cacheKey != "" {
				photoCache.Store(cacheKey, cssPhotoCacheEntry{
					photoURL: pictureURL,
					name:     displayName,
					expiry:   time.Now().Add(30 * time.Minute),
				})
			}
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": pictureURL, "name": displayName}})
			return
		}
	}

	// Priority 2: CSS search (returns name; photo URL may be in document) 
	oidcToken := ""
	if h.redis != nil && cacheKey != "" {
		var stored string
		if err := h.redis.Get("oidc:token:"+cacheKey, &stored); err == nil && stored != "" {
			oidcToken = stored
		}
	}
	if oidcToken == "" {
		oidcToken = c.GetHeader("X-OIDC-Token")
	}
	if oidcToken == "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": ""}})
		return
	}
	if len(oidcToken) < 7 || oidcToken[:7] != "Bearer " {
		oidcToken = "Bearer " + oidcToken
	}

	// Look up the user's DSO shortname to use as the CSS search query.
	var cssUsername string
	if userID != nil {
		_ = h.db.QueryRowContext(c.Request.Context(),
			`SELECT username FROM users WHERE id = $1`, userID).Scan(&cssUsername)
	}
	cssQuery := cssUsername
	if cssQuery == "" {
		cssQuery = "me"
	}
	log.Printf("CSS search query: %s for user %v", cssQuery, userID)

	cssURL := "https://directory.example.com/service/css/search?q=" + cssQuery
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, cssURL, nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": ""}})
		return
	}
	req.Header.Set("Authorization", oidcToken)
	req.Header.Set("Accept", "application/json")

	// Private/corporate CA may not be in the default trust store inside pods;
	// skip verification for internal oidc-int-rsvc calls only when INTERNAL_TLS_INSECURE=true.
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("CSS photo fetch failed for user %v: %v", userID, err)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": ""}})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("CSS response: status=%d, body_len=%d for user %v (query: %s)", resp.StatusCode, len(body), userID, cssQuery)
	if resp.StatusCode != http.StatusOK {
		log.Printf("CSS body sample: %s", string(body[:min(200, len(body))]))
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": ""}})
		return
	}

	// CSS returns: {"cssResponse":{"groupResults":[{"documents":[{...person fields...}]}]}}
	entityID, displayName := parseCSSPerson(body)
	log.Printf("CSS parsed entityID=%q displayName=%q for user %v", entityID, displayName, userID)

	// Try to fetch a photo using the entity_id (Aileron DSID) from the CSS response.
	// The photo endpoint pattern is: /service/css/picture/entity/<entity_id>
	photoURL := ""
	if entityID != "" && oidcToken != "" {
		photoURL = tryFetchCSSPhoto(c.Request.Context(), entityID, oidcToken)
		log.Printf("CSS photo for entity %s: %q", entityID, photoURL)
	}

	// Cache the result (even if photo is empty, cache prevents repeated calls).
	if cacheKey != "" {
		photoCache.Store(cacheKey, cssPhotoCacheEntry{
			photoURL: photoURL,
			name:     displayName,
			expiry:   time.Now().Add(30 * time.Minute),
		})
		if h.redis != nil && photoURL != "" {
			_ = h.redis.Set("user:photo:"+cacheKey, photoURL, 24*time.Hour)
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": photoURL, "name": displayName}})
}

// GetUserPhoto returns the profile photo and display name for any user by their ID.
// Uses the same CSS lookup as GetMyPhoto but with the target user's username.
// The caller's OIDC token is used to authenticate against the CSS search API.
// GET /api/v1/users/:id/photo
func (h *UserHandler) GetUserPhoto(c *gin.Context) {
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid user ID"})
		return
	}

	cacheKey := targetID.String()

	// In-process cache (30 min TTL)
	if raw, ok := photoCache.Load("u:" + cacheKey); ok {
		entry := raw.(cssPhotoCacheEntry)
		if time.Now().Before(entry.expiry) {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": entry.photoURL, "name": entry.name}})
			return
		}
		photoCache.Delete("u:" + cacheKey)
	}

	// Redis cache
	if h.redis != nil {
		var pictureURL string
		if err2 := h.redis.Get("user:photo:"+cacheKey, &pictureURL); err2 == nil && pictureURL != "" {
			var displayName string
			_ = h.db.QueryRowContext(c.Request.Context(), `SELECT COALESCE(NULLIF(full_name,''), username) FROM users WHERE id = $1`, targetID).Scan(&displayName)
			photoCache.Store("u:"+cacheKey, cssPhotoCacheEntry{photoURL: pictureURL, name: displayName, expiry: time.Now().Add(30 * time.Minute)})
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": pictureURL, "name": displayName}})
			return
		}
	}

	// Look up target user's username and full_name
	var cssUsername, fullName string
	_ = h.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(username,''), COALESCE(NULLIF(full_name,''), username) FROM users WHERE id = $1`, targetID,
	).Scan(&cssUsername, &fullName)
	if cssUsername == "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": ""}})
		return
	}

	// Get the caller's OIDC token
	callerID, _ := c.Get("user_id")
	oidcToken := ""
	if h.redis != nil && callerID != nil {
		var stored string
		if err2 := h.redis.Get("oidc:token:"+fmt.Sprintf("%v", callerID), &stored); err2 == nil {
			oidcToken = stored
		}
	}
	if oidcToken == "" {
		oidcToken = c.GetHeader("X-OIDC-Token")
	}
	if oidcToken == "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": fullName}})
		return
	}
	if len(oidcToken) < 7 || oidcToken[:7] != "Bearer " {
		oidcToken = "Bearer " + oidcToken
	}

	cssURL := "https://directory.example.com/service/css/search?q=" + cssUsername
	req, err2 := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, cssURL, nil)
	if err2 != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": fullName}})
		return
	}
	req.Header.Set("Authorization", oidcToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
		},
	}
	resp, err3 := client.Do(req)
	if err3 != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": "", "name": fullName}})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	entityID, cssName := parseCSSPerson(body)
	if cssName != "" {
		fullName = cssName
	}
	photoURL := ""
	if entityID != "" {
		photoURL = tryFetchCSSPhoto(c.Request.Context(), entityID, oidcToken)
	}

	photoCache.Store("u:"+cacheKey, cssPhotoCacheEntry{photoURL: photoURL, name: fullName, expiry: time.Now().Add(30 * time.Minute)})
	if h.redis != nil && photoURL != "" {
		_ = h.redis.Set("user:photo:"+cacheKey, photoURL, 24*time.Hour)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"photo": photoURL, "name": fullName}})
}
// Returns the photo as a base64 data URL if successful, or "" on any error.
func tryFetchCSSPhoto(ctx context.Context, entityID, token string) string {
	if len(token) < 7 || token[:7] != "Bearer " {
		token = "Bearer " + token
	}
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
		},
	}
	// Try the known Aileron CSS photo endpoint patterns.
	candidates := []string{
		"https://directory.example.com/service/css/picture/entity/" + entityID,
		"https://directory.example.com/service/css/photos/entity/" + entityID,
		"https://directory.example.com/service/css/photo/" + entityID,
	}
	for _, u := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", token)
		resp, err := httpClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		defer resp.Body.Close()
		imgBytes, err := io.ReadAll(resp.Body)
		if err != nil || len(imgBytes) < 100 {
			continue
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "image/jpeg"
		}
		return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(imgBytes)
	}
	return ""
}

// parseCSSPerson extracts entity_id and display name from the CSS search API response.
// CSS response shape: {"cssResponse":{"groupResults":[{"documents":[{...}]}]}}
func parseCSSPerson(body []byte) (entityID, name string) {
	var wrapper struct {
		CSSResponse struct {
			GroupResults []struct {
				Documents []struct {
					EntityID  string `json:"entity_id"`
					FullName  string `json:"person_full_name_formatted"`
					FirstName string `json:"person_first_name"`
					LastName  string `json:"person_last_name"`
				} `json:"documents"`
			} `json:"groupResults"`
		} `json:"cssResponse"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return "", ""
	}
	for _, group := range wrapper.CSSResponse.GroupResults {
		for _, doc := range group.Documents {
			n := doc.FullName
			if n == "" {
				n = strings.TrimSpace(doc.FirstName + " " + doc.LastName)
			}
			return doc.EntityID, n
		}
	}
	return "", ""
}

// RegisterRoutes registers user management routes
func (h *UserHandler) RegisterRoutes(router *gin.RouterGroup) {
	users := router.Group("/users")
	{
		users.GET("", h.ListUsers)
		users.POST("", h.CreateUser)
		users.GET("/:id", h.GetUser)
		users.PUT("/:id", h.UpdateUser)
		users.DELETE("/:id", h.DeleteUser)
		users.GET("/:id/permissions", h.GetUserPermissions)

		// Current-user endpoints (must be registered before /:id to avoid conflict)
		users.GET("/me/photo", h.GetMyPhoto)

		// Per-user photo (admin use — CSS lookup with caller's OIDC token)
		users.GET("/:id/photo", h.GetUserPhoto)

		// User settings endpoints (for authenticated user)
		users.GET("/settings", h.GetUserSettings)
		users.PUT("/settings", h.UpdateUserSettings)
	}
}
