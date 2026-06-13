package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	dsldap "github.com/aileron-platform/aileron/platform/internal/services/dsldap"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
)

// RoleHandler handles role and permission management
type RoleHandler struct {
	rbacService *rbac.RBACService
	db          *sql.DB
	ldapSvc     *dsldap.Service // optional; triggers mapping reload after changes
}

// NewRoleHandler creates a new role handler
func NewRoleHandler(rbacService *rbac.RBACService, db *sql.DB) *RoleHandler {
	return &RoleHandler{
		rbacService: rbacService,
		db:          db,
	}
}

// SetLDAPService attaches the LDAP service so mapping changes trigger live reloads.
func (h *RoleHandler) SetLDAPService(svc *dsldap.Service) {
	h.ldapSvc = svc
}

// CreateRoleRequest represents role creation request
type CreateRoleRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// UpdateRoleRequest represents role update request
type UpdateRoleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreatePermissionRequest represents permission creation request
type CreatePermissionRequest struct {
	Name        string `json:"name" binding:"required"`
	Resource    string `json:"resource" binding:"required"`
	Action      string `json:"action" binding:"required"`
	Description string `json:"description"`
}

// UpsertLDAPMappingRequest represents a LDAP group role mapping upsert
type UpsertLDAPMappingRequest struct {
	LDAPGroup string    `json:"ldap_group" binding:"required"`
	RoleID    uuid.UUID `json:"role_id" binding:"required"`
}

// AssignPermissionsRequest represents permission assignment request
type AssignPermissionsRequest struct {
	PermissionIDs []uuid.UUID `json:"permission_ids" binding:"required"`
}

// ListRoles handles listing all roles
func (h *RoleHandler) ListRoles(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "roles.view"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	roles, err := h.rbacService.ListRoles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch roles",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"roles": roles,
		},
	})
}

// GetRole handles getting a single role
func (h *RoleHandler) GetRole(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "roles.view"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid role ID",
		})
		return
	}

	role, err := h.rbacService.GetRole(c.Request.Context(), roleID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Role not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    role,
	})
}

// CreateRole handles role creation
func (h *RoleHandler) CreateRole(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "roles.create"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	var req CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	role := &rbac.Role{
		Name:         req.Name,
		Description:  req.Description,
		IsSystemRole: false,
	}

	err := h.rbacService.CreateRole(c.Request.Context(), role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create role",
		})
		return
	}

	// Log role creation
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "role_created", "role", &role.ID, map[string]interface{}{
		"role_name": role.Name,
	})

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Role created successfully",
		"data":    role,
	})
}

// ListPermissions handles listing all permissions
func (h *RoleHandler) ListPermissions(c *gin.Context) {
	permissions, err := h.rbacService.ListPermissions(c.Request.Context())
	if err != nil {
		log.Printf("ListPermissions error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch permissions",
		})
		return
	}

	// Group permissions by resource
	grouped := make(map[string][]rbac.Permission)
	for _, perm := range permissions {
		grouped[perm.Resource] = append(grouped[perm.Resource], perm)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"permissions": permissions,
			"grouped":     grouped,
		},
	})
}

// GetRolePermissions handles getting permissions for a role
func (h *RoleHandler) GetRolePermissions(c *gin.Context) {
	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid role ID",
		})
		return
	}

	permissions, err := h.rbacService.GetRolePermissions(c.Request.Context(), roleID)
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

// AssignPermissions handles assigning permissions to a role (replaces existing permissions)
func (h *RoleHandler) AssignPermissions(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "roles.update"); err != nil {
		log.Printf("Permission denied for user")
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		log.Printf("Invalid role ID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid role ID",
		})
		return
	}

	var req AssignPermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	log.Printf("Replacing permissions for role %s with %d permissions", roleID, len(req.PermissionIDs))

	// Replace all permissions for this role
	err = h.rbacService.ReplaceRolePermissions(c.Request.Context(), roleID, req.PermissionIDs)
	if err != nil {
		log.Printf("Failed to replace permissions: %v", err)
		log.Printf("UpdateRolePermissions error role=%s: %v", roleID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update permissions",
		})
		return
	}

	// Log permission assignment
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "permissions_updated", "role", &roleID, map[string]interface{}{
		"permission_ids": req.PermissionIDs,
		"count":          len(req.PermissionIDs),
	})

	log.Printf("Successfully updated permissions for role %s", roleID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Permissions updated successfully",
	})
}

// RemovePermission handles removing a permission from a role
func (h *RoleHandler) RemovePermission(c *gin.Context) {
	// Check permission
	if err := h.checkPermission(c, "roles.update"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Permission denied",
		})
		return
	}

	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid role ID",
		})
		return
	}

	permID, err := uuid.Parse(c.Param("permissionId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid permission ID",
		})
		return
	}

	err = h.rbacService.RemovePermissionFromRole(c.Request.Context(), roleID, permID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to remove permission",
		})
		return
	}

	// Log permission removal
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "permission_removed", "role", &roleID, map[string]interface{}{
		"permission_id": permID,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Permission removed successfully",
	})
}

// CheckUserPermission checks if a user has a specific permission
func (h *RoleHandler) CheckUserPermission(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	permission := c.Query("permission")
	if permission == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Permission parameter is required",
		})
		return
	}

	hasPermission, err := h.rbacService.HasPermission(c.Request.Context(), userID, permission)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to check permission",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"has_permission": hasPermission,
			"permission":     permission,
		},
	})
}

// RegisterRoutes registers role management routes
func (h *RoleHandler) RegisterRoutes(router *gin.RouterGroup) {
	roles := router.Group("/roles")
	{
		roles.GET("", h.ListRoles)
		roles.POST("", h.CreateRole)
		roles.GET("/:id", h.GetRole)
		roles.PUT("/:id", h.UpdateRole)
		roles.DELETE("/:id", h.DeleteRole)
		roles.GET("/:id/permissions", h.GetRolePermissions)
		roles.POST("/:id/permissions", h.AssignPermissions)
		roles.DELETE("/:id/permissions/:permissionId", h.RemovePermission)
	}

	permissions := router.Group("/permissions")
	{
		permissions.GET("", h.ListPermissions)
		permissions.POST("", h.CreatePermission)
		permissions.DELETE("/:id", h.DeletePermission)
	}

	// LDAP group role mappings (self-configurable without restart)
	ldap := router.Group("/ldap-mappings")
	{
		ldap.GET("", h.ListLDAPMappings)
		ldap.POST("", h.UpsertLDAPMapping)
		ldap.DELETE("/:id", h.DeleteLDAPMapping)
	}

	// LDAP group autocomplete search
	router.GET("/admin/ldap/groups/search", h.SearchLDAPGroups)

	// User permission check endpoint
	router.GET("/users/:id/check-permission", h.CheckUserPermission)
	// Assign role to user
	router.POST("/users/:id/role", h.AssignRoleToUser)
}

// UpdateRole updates a role's name and description
func (h *RoleHandler) UpdateRole(c *gin.Context) {
	if err := h.checkPermission(c, "roles.update"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid role ID"})
		return
	}
	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}
	if err := h.rbacService.UpdateRole(c.Request.Context(), roleID, req.Name, req.Description); err != nil {
		log.Printf("UpdateRole error id=%s: %v", roleID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "role_updated", "role", &roleID, map[string]interface{}{"name": req.Name})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Role updated"})
}

// DeleteRole removes a non-system role
func (h *RoleHandler) DeleteRole(c *gin.Context) {
	if err := h.checkPermission(c, "roles.delete"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	roleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid role ID"})
		return
	}
	if err := h.rbacService.DeleteRole(c.Request.Context(), roleID); err != nil {
		if err.Error() == "cannot delete system role" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot delete system role"})
			return
		}
		log.Printf("DeleteRole error id=%s: %v", roleID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "role_deleted", "role", &roleID, nil)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Role deleted"})
}

// CreatePermission creates a new custom permission
func (h *RoleHandler) CreatePermission(c *gin.Context) {
	if err := h.checkPermission(c, "roles.create"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	var req CreatePermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	perm, err := h.rbacService.CreatePermission(c.Request.Context(), req.Name, req.Resource, req.Action, req.Description)
	if err != nil {
		log.Printf("CreatePermission error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "permission_created", "permission", &perm.ID, map[string]interface{}{"name": req.Name})
	c.JSON(http.StatusCreated, gin.H{"success": true, "message": "Permission created", "data": perm})
}

// DeletePermission removes a custom permission
func (h *RoleHandler) DeletePermission(c *gin.Context) {
	if err := h.checkPermission(c, "roles.delete"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	permID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid permission ID"})
		return
	}
	if err := h.rbacService.DeletePermission(c.Request.Context(), permID); err != nil {
		log.Printf("DeletePermission error id=%s: %v", permID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	userID := h.getUserID(c)
	h.logAuditEvent(c, userID, "permission_deleted", "permission", &permID, nil)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Permission deleted"})
}

// AssignRoleToUser assigns a role (by name) to a user
func (h *RoleHandler) AssignRoleToUser(c *gin.Context) {
	if err := h.checkPermission(c, "users.update"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid user ID"})
		return
	}
	var req struct {
		RoleName string `json:"role_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "role_name is required"})
		return
	}
	if err := h.rbacService.AssignRoleToUser(c.Request.Context(), userID, req.RoleName); err != nil {
		log.Printf("AssignRoleToUser error user=%s role=%s: %v", userID, req.RoleName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	actorID := h.getUserID(c)
	h.logAuditEvent(c, actorID, "role_assigned", "user", &userID, map[string]interface{}{"role": req.RoleName})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Role assigned"})
}

// ListLDAPMappings returns all LDAP group role mappings
func (h *RoleHandler) ListLDAPMappings(c *gin.Context) {
	if err := h.checkPermission(c, "roles.view"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	mappings, err := h.rbacService.GetLDAPGroupMappings(c.Request.Context())
	if err != nil {
		log.Printf("ListLDAPMappings error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"mappings": mappings}})
}

// UpsertLDAPMapping creates or updates a LDAP group role mapping
func (h *RoleHandler) UpsertLDAPMapping(c *gin.Context) {
	if err := h.checkPermission(c, "roles.update"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	var req UpsertLDAPMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	actorID := h.getUserID(c)
	if err := h.rbacService.UpsertLDAPGroupMapping(c.Request.Context(), req.LDAPGroup, req.RoleID, actorID); err != nil {
		log.Printf("UpsertLDAPMapping error group=%s: %v", req.LDAPGroup, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	h.logAuditEvent(c, actorID, "ldap_mapping_upserted", "ldap_mapping", nil, map[string]interface{}{
		"ldap_group": req.LDAPGroup, "role_id": req.RoleID,
	})
	// Reload LDAP in-memory mappings immediately (no restart needed)
	if h.ldapSvc != nil {
		h.ldapSvc.ReloadMappings()
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "LDAP mapping saved"})
}

// DeleteLDAPMapping removes a LDAP group role mapping
func (h *RoleHandler) DeleteLDAPMapping(c *gin.Context) {
	if err := h.checkPermission(c, "roles.update"); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Permission denied"})
		return
	}
	mappingID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid mapping ID"})
		return
	}
	if err := h.rbacService.DeleteLDAPGroupMapping(c.Request.Context(), mappingID); err != nil {
		log.Printf("DeleteLDAPMapping error id=%s: %v", mappingID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal server error"})
		return
	}
	actorID := h.getUserID(c)
	h.logAuditEvent(c, actorID, "ldap_mapping_deleted", "ldap_mapping", &mappingID, nil)
	// Reload LDAP in-memory mappings immediately (no restart needed)
	if h.ldapSvc != nil {
		h.ldapSvc.ReloadMappings()
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "LDAP mapping deleted"})
}

// SearchLDAPGroups handles GET /api/v1/admin/ldap/groups/search?q=<prefix>
// It returns up to 20 group CNs from LDAP that contain the query prefix.
// Returns an empty array (not an error) when the LDAP service is unavailable
// or the prefix is too short, so the UI degrades gracefully.
func (h *RoleHandler) SearchLDAPGroups(c *gin.Context) {
	q := c.Query("q")
	if h.ldapSvc == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{"groups": []string{}},
		})
		return
	}

	groups, err := h.ldapSvc.SearchGroups(q)
	if err != nil {
		log.Printf("WARN: LDAP group search failed: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{"groups": []string{}},
		})
		return
	}
	if groups == nil {
		groups = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"groups": groups},
	})
}

// Helper functions

func (h *RoleHandler) checkPermission(c *gin.Context, permission string) error {
	userID := h.getUserID(c)
	if userID == nil {
		return rbac.ErrPermissionDenied
	}

	return h.rbacService.CheckPermission(c.Request.Context(), *userID, permission)
}

func (h *RoleHandler) getUserID(c *gin.Context) *uuid.UUID {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		return nil
	}

	userID := userIDVal.(uuid.UUID)
	return &userID
}

func (h *RoleHandler) logAuditEvent(c *gin.Context, userID *uuid.UUID, action, resourceType string, resourceID *uuid.UUID, details map[string]interface{}) {
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
