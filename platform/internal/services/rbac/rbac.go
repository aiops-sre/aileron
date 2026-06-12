package rbac

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/aileron-platform/aileron/platform/internal/services/dsldap"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserInactive       = errors.New("user account is inactive")
	ErrUserLocked         = errors.New("user account is locked")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrRoleNotFound       = errors.New("role not found")
)

// User represents a system user
type User struct {
	ID                  uuid.UUID              `json:"id"`
	Username            string                 `json:"username"`
	Email               string                 `json:"email"`
	PasswordHash        string                 `json:"-"`
	FullName            string                 `json:"full_name"`
	RoleID              uuid.UUID              `json:"role_id"`
	RoleName            string                 `json:"role_name,omitempty"`
	IsActive            bool                   `json:"is_active"`
	IsVerified          bool                   `json:"is_verified"`
	MFAEnabled          bool                   `json:"mfa_enabled"`
	LastLogin           *time.Time             `json:"last_login,omitempty"`
	LoginCount          int                    `json:"login_count"`
	FailedLoginAttempts int                    `json:"-"`
	LockedUntil         *time.Time             `json:"-"`
	AvatarURL           string                 `json:"avatar_url,omitempty"`
	Phone               string                 `json:"phone,omitempty"`
	Timezone            string                 `json:"timezone"`
	Preferences         map[string]interface{} `json:"preferences"`
	Permissions         []string               `json:"permissions,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

// Role represents a user role
type Role struct {
	ID           uuid.UUID    `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	IsSystemRole bool         `json:"is_system_role"`
	Permissions  []Permission `json:"permissions,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// Permission represents a granular permission
type Permission struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Resource    string    `json:"resource"`
	Action      string    `json:"action"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Session represents a user session
type Session struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	TokenHash        string    `json:"-"`
	RefreshTokenHash string    `json:"-"`
	IPAddress        string    `json:"ip_address"`
	UserAgent        string    `json:"user_agent"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// RBACService handles authentication and authorization
type RBACService struct {
	db *sql.DB
}

// NewRBACService creates a new RBAC service
func NewRBACService(db *sql.DB) *RBACService {
	return &RBACService{db: db}
}

// Authenticate validates user credentials and returns user with permissions
func (s *RBACService) Authenticate(ctx context.Context, username, password string) (*User, error) {
	user := &User{}
	var preferencesJSON []byte

	query := `
		SELECT u.id, u.username, u.email, u.password_hash, u.full_name, u.role_id,
		       u.is_active, u.is_verified, u.mfa_enabled, u.last_login, u.login_count,
		       u.failed_login_attempts, u.locked_until, COALESCE(u.avatar_url, ''), COALESCE(u.phone, ''), COALESCE(u.timezone, 'UTC'),
		       COALESCE(u.preferences::text, '{}'), u.created_at, u.updated_at, COALESCE(r.name, u.role, 'viewer') as role_name
		FROM users u
		LEFT JOIN roles r ON u.role_id = r.id
		WHERE u.username = $1 OR u.email = $1
	`

	err := s.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.FullName,
		&user.RoleID, &user.IsActive, &user.IsVerified, &user.MFAEnabled,
		&user.LastLogin, &user.LoginCount, &user.FailedLoginAttempts,
		&user.LockedUntil, &user.AvatarURL, &user.Phone, &user.Timezone,
		&preferencesJSON, &user.CreatedAt, &user.UpdatedAt, &user.RoleName,
	)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal preferences JSON
	if len(preferencesJSON) > 0 {
		var prefs map[string]interface{}
		if err := json.Unmarshal(preferencesJSON, &prefs); err == nil {
			user.Preferences = prefs
		}
	}
	if user.Preferences == nil {
		user.Preferences = make(map[string]interface{})
	}

	// Check if user is active
	if !user.IsActive {
		return nil, ErrUserInactive
	}

	// Check if user is locked
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, ErrUserLocked
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.incrementFailedLogins(ctx, user.ID)
		return nil, ErrInvalidCredentials
	}

	// Reset failed login attempts and update last login
	s.updateSuccessfulLogin(ctx, user.ID)

	// Load user permissions
	permissions, err := s.GetUserPermissions(ctx, user.ID)
	if err == nil {
		user.Permissions = permissions
	}

	return user, nil
}

// GetUserPermissions retrieves all permissions for a user.
// Falls back to the u.role text column when role_id is NULL.
func (s *RBACService) GetUserPermissions(ctx context.Context, userID uuid.UUID) ([]string, error) {
	query := `
		SELECT DISTINCT p.name
		FROM users u
		LEFT JOIN roles r ON u.role_id = r.id
		                  OR (u.role_id IS NULL AND LOWER(r.name) = LOWER(u.role))
		LEFT JOIN role_permissions rp ON r.id = rp.role_id
		JOIN permissions p ON rp.permission_id = p.id
		WHERE u.id = $1
	`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var permissions []string
	for rows.Next() {
		var perm string
		if err := rows.Scan(&perm); err != nil {
			return nil, err
		}
		permissions = append(permissions, perm)
	}

	return permissions, rows.Err()
}

// HasPermission checks if a user has a specific permission.
// Falls back to the u.role text column when role_id is NULL (e.g. MAS-provisioned users).
func (s *RBACService) HasPermission(ctx context.Context, userID uuid.UUID, permission string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1
			FROM users u
			LEFT JOIN roles r ON u.role_id = r.id
			                  OR (u.role_id IS NULL AND LOWER(r.name) = LOWER(u.role))
			LEFT JOIN role_permissions rp ON r.id = rp.role_id
			JOIN permissions p ON rp.permission_id = p.id
			WHERE u.id = $1 AND p.name = $2
		)
	`

	var exists bool
	err := s.db.QueryRowContext(ctx, query, userID, permission).Scan(&exists)
	return exists, err
}

// CheckPermission checks permission and returns error if denied.
// Priority order:
//  1. DB permission query (role_id join via HasPermission)
//  2. DS-LDAP role injected into context by auth middleware (most authoritative for MAS users)
//  3. role text column fallback (for MAS-provisioned users without role_id)
func (s *RBACService) CheckPermission(ctx context.Context, userID uuid.UUID, permission string) error {
	// 1. Try DB-backed permission check (works when role_id is set)
	has, err := s.HasPermission(ctx, userID, permission)
	if err == nil && has {
		return nil
	}

	// 2. DS-LDAP role from request context (injected by Authenticate middleware)
	if ldapRole := dsldap.RoleFromContext(ctx); ldapRole != "" {
		if checkRolePermission(ldapRole, permission) {
			return nil
		}
	}

	// 3. Fallback: role text column for MAS-provisioned users (role_id = NULL)
	var roleText string
	dbErr := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(role, '') FROM users WHERE id = $1 AND is_active = true",
		userID,
	).Scan(&roleText)
	if dbErr != nil {
		return ErrPermissionDenied
	}
	if checkRolePermission(strings.ToLower(strings.TrimSpace(roleText)), permission) {
		return nil
	}

	// 4. Check role name via role_id — catches admin users whose role text column is stale
	var roleNameByID string
	_ = s.db.QueryRowContext(ctx,
		"SELECT COALESCE(r.name, '') FROM users u LEFT JOIN roles r ON u.role_id = r.id WHERE u.id = $1 AND u.is_active = true",
		userID,
	).Scan(&roleNameByID)
	if checkRolePermission(strings.ToLower(strings.TrimSpace(roleNameByID)), permission) {
		return nil
	}

	// 5. Check live LDAP group role mappings (Admin LDAP Mappings table)
	var ldapMappedRole string
	_ = s.db.QueryRowContext(ctx, `
		SELECT r.name
		FROM user_mas_groups umg
		JOIN ldap_group_role_mappings m ON umg.mas_group = m.ldap_group
		JOIN roles r ON m.role_id = r.id
		WHERE umg.user_id = $1
		ORDER BY
			CASE LOWER(r.name)
				WHEN 'admin'    THEN 1
				WHEN 'operator' THEN 2
				WHEN 'sre'      THEN 2
				ELSE 9
			END ASC
		LIMIT 1
	`, userID).Scan(&ldapMappedRole)
	if ldapMappedRole != "" && checkRolePermission(strings.ToLower(strings.TrimSpace(ldapMappedRole)), permission) {
		return nil
	}

	return ErrPermissionDenied
}

// checkRolePermission returns true if the named role grants the requested permission.
// This is used both by the LDAP context check and the role-text fallback.
func checkRolePermission(role, permission string) bool {
	switch role {
	case "admin", "superadmin", "super_admin", "administrator", "owner":
		return true
	case "operator", "sre", "engineer", "oncall", "on-call", "responder":
		return operatorPermissions[permission]
	case "viewer", "readonly", "read-only", "read_only", "observer":
		return viewerPermissions[permission]
	}
	return false
}

// operatorPermissions is the fixed permission set for the 'operator' role.
var operatorPermissions = map[string]bool{
	"alerts.view": true, "alerts.create": true, "alerts.update": true,
	"alerts.assign": true, "alerts.resolve": true, "alerts.acknowledge": true,
	"incidents.view": true, "incidents.create": true,
	"incidents.update": true, "incidents.resolve": true,
	"maintenance.view": true, "maintenance.create": true, "maintenance.update": true,
	"workflows.view": true, "workflows.execute": true,
	"correlations.view": true, "aiops.view": true,
	"topology.view": true, "oncall.view": true,
	"analytics.view": true, "integrations.view": true,
	"ai.use": true, "audit.view": true, "system.view": true,
}

// viewerPermissions is the fixed permission set for the 'viewer' role.
var viewerPermissions = map[string]bool{
	"alerts.view": true, "incidents.view": true,
	"maintenance.view": true, "workflows.view": true,
	"correlations.view": true, "aiops.view": true,
	"topology.view": true, "oncall.view": true,
	"analytics.view": true, "integrations.view": true,
	"system.view": true,
}

// CreateUser creates a new user
func (s *RBACService) CreateUser(ctx context.Context, user *User, password string) error {
	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	user.ID = uuid.New()
	user.PasswordHash = string(hashedPassword)
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()

	// Marshal preferences to JSON
	var prefsJSON []byte
	if user.Preferences != nil {
		prefsJSON, _ = json.Marshal(user.Preferences)
	} else {
		prefsJSON = []byte("{}")
	}

	query := `
		INSERT INTO users (id, username, email, password_hash, full_name, role_id,
		                   is_active, timezone, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err = s.db.ExecContext(ctx, query,
		user.ID, user.Username, user.Email, user.PasswordHash, user.FullName,
		user.RoleID, user.IsActive, user.Timezone, string(prefsJSON),
		user.CreatedAt, user.UpdatedAt,
	)

	return err
}

// GetUser retrieves a user by ID
func (s *RBACService) GetUser(ctx context.Context, userID uuid.UUID) (*User, error) {
	user := &User{}
	var preferencesJSON []byte

	query := `
		SELECT u.id, u.username, u.email, COALESCE(u.full_name, ''), u.role_id, COALESCE(r.name, u.role, 'viewer') as role_name,
		       u.is_active, u.is_verified, u.mfa_enabled, u.last_login, u.login_count,
		       COALESCE(u.avatar_url, ''), COALESCE(u.phone, ''), COALESCE(u.timezone, 'UTC'),
		       COALESCE(u.preferences::text, '{}'), u.created_at, u.updated_at
		FROM users u
		LEFT JOIN roles r ON u.role_id = r.id
		WHERE u.id = $1
	`

	err := s.db.QueryRowContext(ctx, query, userID).Scan(
		&user.ID, &user.Username, &user.Email, &user.FullName, &user.RoleID, &user.RoleName,
		&user.IsActive, &user.IsVerified, &user.MFAEnabled, &user.LastLogin, &user.LoginCount,
		&user.AvatarURL, &user.Phone, &user.Timezone, &preferencesJSON,
		&user.CreatedAt, &user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal preferences JSON
	if len(preferencesJSON) > 0 {
		var prefs map[string]interface{}
		if err := json.Unmarshal(preferencesJSON, &prefs); err == nil {
			user.Preferences = prefs
		}
	}
	if user.Preferences == nil {
		user.Preferences = make(map[string]interface{})
	}

	// Load permissions
	permissions, err := s.GetUserPermissions(ctx, userID)
	if err == nil {
		user.Permissions = permissions
	}

	return user, nil
}

// ListUsers retrieves all users (with pagination)
func (s *RBACService) ListUsers(ctx context.Context, limit, offset int) ([]*User, int, error) {
	// Get total count
	var total int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT u.id, u.username, u.email, COALESCE(u.full_name, ''), u.role_id, COALESCE(r.name, u.role, 'viewer') as role_name,
		       u.is_active, COALESCE(u.is_verified, false), u.last_login, COALESCE(u.login_count, 0),
		       COALESCE(u.avatar_url, ''), COALESCE(u.timezone, 'UTC'), u.created_at, u.updated_at
		FROM users u
		LEFT JOIN roles r ON u.role_id = r.id
		                  OR (u.role_id IS NULL AND LOWER(r.name) = LOWER(u.role))
		ORDER BY u.created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user := &User{}
		err := rows.Scan(
			&user.ID, &user.Username, &user.Email, &user.FullName, &user.RoleID, &user.RoleName,
			&user.IsActive, &user.IsVerified, &user.LastLogin, &user.LoginCount,
			&user.AvatarURL, &user.Timezone, &user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}

	return users, total, rows.Err()
}

// UpdateUser updates user information
func (s *RBACService) UpdateUser(ctx context.Context, user *User) error {
	user.UpdatedAt = time.Now()

	// Marshal preferences to JSON
	var prefsJSON []byte
	if user.Preferences != nil {
		prefsJSON, _ = json.Marshal(user.Preferences)
	} else {
		prefsJSON = []byte("{}")
	}

	// Get role name if role_id changed
	var roleName string
	if user.RoleID != uuid.Nil {
		s.db.QueryRowContext(ctx, "SELECT name FROM roles WHERE id = $1", user.RoleID).Scan(&roleName)
	}

	// Update both role_id and role fields for MAS compatibility
	query := `
		UPDATE users
		SET full_name = $1, role_id = $2, role = $3, is_active = $4, avatar_url = $5,
		    phone = $6, timezone = $7, preferences = $8, updated_at = $9
		WHERE id = $10
	`

	result, err := s.db.ExecContext(ctx, query,
		user.FullName, user.RoleID, roleName, user.IsActive, user.AvatarURL,
		user.Phone, user.Timezone, string(prefsJSON), user.UpdatedAt, user.ID,
	)

	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}

// DeleteUser soft deletes a user
func (s *RBACService) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	query := "UPDATE users SET is_active = false, updated_at = $1 WHERE id = $2"
	result, err := s.db.ExecContext(ctx, query, time.Now(), userID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}

// GetRole retrieves a role by ID
func (s *RBACService) GetRole(ctx context.Context, roleID uuid.UUID) (*Role, error) {
	role := &Role{}

	query := `
		SELECT id, name, COALESCE(description, ''), is_system_role, created_at, updated_at
		FROM roles
		WHERE id = $1
	`

	err := s.db.QueryRowContext(ctx, query, roleID).Scan(
		&role.ID, &role.Name, &role.Description, &role.IsSystemRole,
		&role.CreatedAt, &role.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, err
	}

	// Load permissions
	permissions, err := s.GetRolePermissions(ctx, roleID)
	if err == nil {
		role.Permissions = permissions
	}

	return role, nil
}

// ListRoles retrieves all roles
func (s *RBACService) ListRoles(ctx context.Context) ([]*Role, error) {
	query := `
		SELECT id, name, COALESCE(description, ''), is_system_role, created_at, updated_at
		FROM roles
		ORDER BY name
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		log.Printf("ERROR: ListRoles - Query failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	var roles []*Role
	for rows.Next() {
		role := &Role{}
		err := rows.Scan(
			&role.ID, &role.Name, &role.Description, &role.IsSystemRole,
			&role.CreatedAt, &role.UpdatedAt,
		)
		if err != nil {
			log.Printf("ERROR: ListRoles - Scan failed: %v", err)
			return nil, err
		}

		// Load permissions for each role
		permissions, err := s.GetRolePermissions(ctx, role.ID)
		if err == nil {
			role.Permissions = permissions
			log.Printf("DEBUG: ListRoles - Loaded %d permissions for role: %s (ID: %s)", len(permissions), role.Name, role.ID)
		} else {
			log.Printf("ERROR: ListRoles - Failed to load permissions for role %s: %v", role.Name, err)
		}

		roles = append(roles, role)
	}

	log.Printf("DEBUG: ListRoles - Returning %d roles", len(roles))
	return roles, rows.Err()
}

// GetRolePermissions retrieves all permissions for a role
func (s *RBACService) GetRolePermissions(ctx context.Context, roleID uuid.UUID) ([]Permission, error) {
	query := `
		SELECT p.id, p.name, p.resource, p.action, COALESCE(p.description, ''), p.created_at
		FROM permissions p
		JOIN role_permissions rp ON p.id = rp.permission_id
		WHERE rp.role_id = $1
		ORDER BY p.resource, p.action
	`

	log.Printf("DEBUG: GetRolePermissions - Querying for role: %s", roleID)
	rows, err := s.db.QueryContext(ctx, query, roleID)
	if err != nil {
		log.Printf("ERROR: GetRolePermissions - Query failed for role %s: %v", roleID, err)
		return nil, err
	}
	defer rows.Close()

	var permissions []Permission
	for rows.Next() {
		perm := Permission{}
		err := rows.Scan(&perm.ID, &perm.Name, &perm.Resource, &perm.Action, &perm.Description, &perm.CreatedAt)
		if err != nil {
			log.Printf("ERROR: GetRolePermissions - Scan failed: %v", err)
			return nil, err
		}
		log.Printf("DEBUG: GetRolePermissions - Found permission: %s (%s.%s)", perm.Name, perm.Resource, perm.Action)
		permissions = append(permissions, perm)
	}

	log.Printf("DEBUG: GetRolePermissions - Returning %d permissions for role: %s", len(permissions), roleID)
	return permissions, rows.Err()
}

// CreateRole creates a new role
func (s *RBACService) CreateRole(ctx context.Context, role *Role) error {
	role.ID = uuid.New()
	role.CreatedAt = time.Now()
	role.UpdatedAt = time.Now()

	query := `
		INSERT INTO roles (id, name, description, is_system_role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	_, err := s.db.ExecContext(ctx, query,
		role.ID, role.Name, role.Description, role.IsSystemRole,
		role.CreatedAt, role.UpdatedAt,
	)

	return err
}

// AssignPermissionToRole assigns a permission to a role
func (s *RBACService) AssignPermissionToRole(ctx context.Context, roleID, permissionID uuid.UUID) error {
	query := `
		INSERT INTO role_permissions (role_id, permission_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (role_id, permission_id) DO NOTHING
	`

	_, err := s.db.ExecContext(ctx, query, roleID, permissionID, time.Now())
	return err
}

// RemovePermissionFromRole removes a permission from a role
func (s *RBACService) RemovePermissionFromRole(ctx context.Context, roleID, permissionID uuid.UUID) error {
	query := "DELETE FROM role_permissions WHERE role_id = $1 AND permission_id = $2"
	_, err := s.db.ExecContext(ctx, query, roleID, permissionID)
	return err
}

// ReplaceRolePermissions replaces all permissions for a role
func (s *RBACService) ReplaceRolePermissions(ctx context.Context, roleID uuid.UUID, permissionIDs []uuid.UUID) error {
	log.Printf("DEBUG: ReplaceRolePermissions - Starting for role: %s with %d permissions", roleID.String(), len(permissionIDs))

	// Start a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("ERROR: Failed to begin transaction: %v", err)
		return err
	}
	defer tx.Rollback()

	// Delete existing permissions
	result, err := tx.ExecContext(ctx, "DELETE FROM role_permissions WHERE role_id = $1", roleID)
	if err != nil {
		log.Printf("ERROR: Failed to delete existing permissions: %v", err)
		return err
	}
	rowsDeleted, _ := result.RowsAffected()
	log.Printf("DEBUG: Deleted %d existing permissions for role: %s", rowsDeleted, roleID)

	// Insert new permissions
	for i, permID := range permissionIDs {
		log.Printf("DEBUG: Inserting permission %d/%d: %s for role: %s", i+1, len(permissionIDs), permID.String(), roleID)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO role_permissions (role_id, permission_id, created_at)
			VALUES ($1, $2, $3)
		`, roleID, permID, time.Now())
		if err != nil {
			log.Printf("ERROR: Failed to insert permission %s: %v", permID, err)
			return err
		}
	}

	err = tx.Commit()
	if err != nil {
		log.Printf("ERROR: Failed to commit transaction: %v", err)
		return err
	}

	log.Printf("DEBUG: Successfully committed %d permissions for role: %s", len(permissionIDs), roleID.String())
	return nil
}

// Helper functions

func (s *RBACService) incrementFailedLogins(ctx context.Context, userID uuid.UUID) {
	query := `
		UPDATE users
		SET failed_login_attempts = failed_login_attempts + 1,
		    locked_until = CASE 
		        WHEN failed_login_attempts >= 4 THEN NOW() + INTERVAL '30 minutes'
		        ELSE locked_until
		    END
		WHERE id = $1
	`
	if _, err := s.db.ExecContext(ctx, query, userID); err != nil {
		log.Printf("rbac: failed to increment failed logins for user %s: %v", userID, err)
	}
}

func (s *RBACService) updateSuccessfulLogin(ctx context.Context, userID uuid.UUID) {
	query := `
		UPDATE users
		SET last_login = $1,
		    login_count = login_count + 1,
		    failed_login_attempts = 0,
		    locked_until = NULL
		WHERE id = $2
	`
	if _, err := s.db.ExecContext(ctx, query, time.Now(), userID); err != nil {
		log.Printf("rbac: failed to update successful login for user %s: %v", userID, err)
	}
}

// ChangePassword changes a user's password
func (s *RBACService) ChangePassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string) error {
	// Get current password hash
	var currentHash string
	err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id = $1", userID).Scan(&currentHash)
	if err != nil {
		return err
	}

	// Verify old password
	if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(oldPassword)); err != nil {
		return ErrInvalidCredentials
	}

	// Hash new password
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// Update password
	query := "UPDATE users SET password_hash = $1, updated_at = $2 WHERE id = $3"
	_, err = s.db.ExecContext(ctx, query, string(newHash), time.Now(), userID)
	return err
}

// ListPermissions retrieves all available permissions
func (s *RBACService) ListPermissions(ctx context.Context) ([]Permission, error) {
	query := `
		SELECT id, name, resource, action, COALESCE(description, ''), created_at
		FROM permissions
		ORDER BY resource, action
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var permissions []Permission
	for rows.Next() {
		perm := Permission{}
		err := rows.Scan(&perm.ID, &perm.Name, &perm.Resource, &perm.Action, &perm.Description, &perm.CreatedAt)
		if err != nil {
			return nil, err
		}
		permissions = append(permissions, perm)
	}

	return permissions, rows.Err()
}

// UpdateRole updates a role's name and/or description.
// System roles can be updated by admins but not deleted.
func (s *RBACService) UpdateRole(ctx context.Context, roleID uuid.UUID, name, description string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE roles SET name = $1, description = $2, updated_at = NOW()
		WHERE id = $3
	`, name, description, roleID)
	return err
}

// DeleteRole removes a non-system role and its permission assignments.
// Returns an error if the role is marked is_system_role = true.
func (s *RBACService) DeleteRole(ctx context.Context, roleID uuid.UUID) error {
	var isSystem bool
	err := s.db.QueryRowContext(ctx, "SELECT is_system_role FROM roles WHERE id = $1", roleID).Scan(&isSystem)
	if err == sql.ErrNoRows {
		return ErrRoleNotFound
	}
	if err != nil {
		return err
	}
	if isSystem {
		return errors.New("cannot delete system role")
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM roles WHERE id = $1", roleID)
	return err
}

// CreatePermission inserts a new permission record and returns it.
func (s *RBACService) CreatePermission(ctx context.Context, name, resource, action, description string) (*Permission, error) {
	perm := &Permission{
		ID:          uuid.New(),
		Name:        name,
		Resource:    resource,
		Action:      action,
		Description: description,
		CreatedAt:   time.Now(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO permissions (id, name, resource, action, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, perm.ID, perm.Name, perm.Resource, perm.Action, perm.Description, perm.CreatedAt)
	if err != nil {
		return nil, err
	}
	return perm, nil
}

// DeletePermission removes a permission and all its role assignments.
func (s *RBACService) DeletePermission(ctx context.Context, permID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM permissions WHERE id = $1", permID)
	return err
}

// AssignRoleToUser sets a user's role by role name, updating both role_id and the role text column.
func (s *RBACService) AssignRoleToUser(ctx context.Context, userID uuid.UUID, roleName string) error {
	var roleID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM roles WHERE LOWER(name) = LOWER($1)", roleName,
	).Scan(&roleID)
	if err == sql.ErrNoRows {
		return ErrRoleNotFound
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		"UPDATE users SET role_id = $1, role = $2, updated_at = NOW() WHERE id = $3",
		roleID, roleName, userID,
	)
	return err
}

// GetLDAPGroupMappings returns all LDAP group role mappings from the database.
func (s *RBACService) GetLDAPGroupMappings(ctx context.Context) ([]LDAPGroupMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.ldap_group, m.role_id, r.name, m.created_at
		FROM ldap_group_role_mappings m
		JOIN roles r ON m.role_id = r.id
		ORDER BY m.ldap_group
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mappings []LDAPGroupMapping
	for rows.Next() {
		var m LDAPGroupMapping
		if err := rows.Scan(&m.ID, &m.LDAPGroup, &m.RoleID, &m.RoleName, &m.CreatedAt); err != nil {
			return nil, err
		}
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}

// UpsertLDAPGroupMapping inserts or replaces a LDAP group role mapping.
func (s *RBACService) UpsertLDAPGroupMapping(ctx context.Context, ldapGroup string, roleID uuid.UUID, createdBy *uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ldap_group_role_mappings (id, ldap_group, role_id, created_by, created_at, updated_at)
		VALUES (uuid_generate_v4(), $1, $2, $3, NOW(), NOW())
		ON CONFLICT (ldap_group) DO UPDATE
		  SET role_id = EXCLUDED.role_id, updated_at = NOW()
	`, ldapGroup, roleID, createdBy)
	return err
}

// DeleteLDAPGroupMapping removes a LDAP group role mapping.
func (s *RBACService) DeleteLDAPGroupMapping(ctx context.Context, mappingID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM ldap_group_role_mappings WHERE id = $1", mappingID)
	return err
}

// LDAPGroupMapping represents a mapping from an AD group to an AlertHub role.
type LDAPGroupMapping struct {
	ID        uuid.UUID  `json:"id"`
	LDAPGroup string     `json:"ldap_group"`
	RoleID    uuid.UUID  `json:"role_id"`
	RoleName  string     `json:"role_name"`
	CreatedAt time.Time  `json:"created_at"`
}
