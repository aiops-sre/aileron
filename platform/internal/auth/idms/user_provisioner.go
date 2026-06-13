package oidc

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UserProvisioner handles automatic user provisioning from OIDC OAuth2.
//
// Role resolution priority (same for new and existing users):
//  1. DB-configured LDAP group mappings  — explicit, auditable, Admin-UI configurable
//  2. ALERTHUB_ADMIN_GROUPS env var       — operator bootstrap without UI access
//  3. ALERTHUB_OPERATOR_GROUPS env var    — same for operator tier
//  4. Existing DB role                    — preserve; never silently downgrade
//  5. config.DefaultRole                  — last resort
//
// Groups are evaluated across ALL of the user's OIDC groups; the most-privileged
// role that any single group confers is used (principle of highest earned privilege).
type UserProvisioner struct {
	db           *sql.DB
	config       *Config
	adminGroups  []string // from OIDC_ADMIN_GROUPS / ALERTHUB_ADMIN_GROUPS
	opGroups     []string // from OIDC_OPERATOR_GROUPS / ALERTHUB_OPERATOR_GROUPS
	viewerGroups []string // from OIDC_VIEWER_GROUPS / ALERTHUB_VIEWER_GROUPS
}

// NewUserProvisioner creates a new user provisioner.
// Reads group lists from environment variables (set in alerthub-ldap-config):
//
//	OIDC_ADMIN_GROUPS    — comma-separated LDAP groups that confer admin role
//	OIDC_OPERATOR_GROUPS — comma-separated LDAP groups that confer operator role
//	OIDC_VIEWER_GROUPS   — comma-separated LDAP groups that confer viewer role
//
// ALERTHUB_ADMIN_GROUPS / ALERTHUB_OPERATOR_GROUPS are accepted as explicit overrides.
func NewUserProvisioner(db *sql.DB, config *Config) *UserProvisioner {
	p := &UserProvisioner{db: db, config: config}

	adminEnv := firstNonEmpty(os.Getenv("ALERTHUB_ADMIN_GROUPS"), os.Getenv("OIDC_ADMIN_GROUPS"))
	opEnv := firstNonEmpty(os.Getenv("ALERTHUB_OPERATOR_GROUPS"), os.Getenv("OIDC_OPERATOR_GROUPS"))
	viewerEnv := firstNonEmpty(os.Getenv("ALERTHUB_VIEWER_GROUPS"), os.Getenv("OIDC_VIEWER_GROUPS"))

	p.adminGroups = splitGroups(adminEnv)
	p.opGroups = splitGroups(opEnv)
	p.viewerGroups = splitGroups(viewerEnv)

	if len(p.adminGroups) > 0 {
		log.Printf("auth: admin groups configured: %v", p.adminGroups)
	}
	return p
}

// ProvisionUser creates or updates a user based on OIDC identity.
func (p *UserProvisioner) ProvisionUser(user *UserContext) error {
	if !p.config.AutoProvision {
		return nil
	}

	exists, userID, err := p.userExists(user.Username)
	if err != nil {
		return fmt.Errorf("failed to check user existence: %w", err)
	}

	if exists {
		return p.updateUser(userID, user)
	}
	return p.createUser(user)
}

// resolveRole determines the most-privileged role for the user following the
// priority chain described on UserProvisioner. existingRole is the role currently
// in the DB ("" for brand-new users).
func (p *UserProvisioner) resolveRole(user *UserContext, existingRole string) (roleName string, roleID uuid.UUID) {
	// 1. DB-configured LDAP group mappings 
	if len(user.Groups) > 0 {
		if name, id := p.lookupRoleFromGroups(user.Groups); name != "" {
			return name, id
		}
	}

	// 2. ALERTHUB_ADMIN_GROUPS / OIDC_ADMIN_GROUPS env var 
	if len(p.adminGroups) > 0 && groupIntersects(user.Groups, p.adminGroups) {
		return "admin", uuid.Nil
	}

	// 3. ALERTHUB_OPERATOR_GROUPS / OIDC_OPERATOR_GROUPS env var 
	if len(p.opGroups) > 0 && groupIntersects(user.Groups, p.opGroups) {
		return "operator", uuid.Nil
	}

	// 4. Preserve existing DB role (never silently downgrade) 
	if existingRole != "" && existingRole != p.config.DefaultRole {
		return existingRole, uuid.Nil
	}

	// 5. First-user bootstrap: if no admins exist, make this user admin 
	// Handles the chicken-and-egg: admin access is needed to configure LDAP mappings,
	// but LDAP mappings are how admin is granted.
	if len(p.adminGroups) == 0 {
		var adminCount int
		_ = p.db.QueryRow(`
			SELECT COUNT(*) FROM users u
			LEFT JOIN roles r ON u.role_id = r.id
			WHERE u.is_active = true
			  AND (LOWER(r.name) = 'admin' OR LOWER(TRIM(u.role)) = 'admin')
		`).Scan(&adminCount)
		if adminCount == 0 {
			log.Printf("auth: no admins in system and no ALERTHUB_ADMIN_GROUPS configured — "+
				"elevating %s to admin (first-admin bootstrap). "+
				"Set ALERTHUB_ADMIN_GROUPS env var to make this explicit.", user.Username)
			return "admin", uuid.Nil
		}
	}

	// 6. Default role 
	if existingRole != "" {
		return existingRole, uuid.Nil
	}
	return p.config.DefaultRole, uuid.Nil
}

func (p *UserProvisioner) resolveRoleID(roleName string) (uuid.UUID, string) {
	var id uuid.UUID
	err := p.db.QueryRow(`SELECT id FROM roles WHERE LOWER(name) = LOWER($1) LIMIT 1`, roleName).Scan(&id)
	if err == nil {
		return id, roleName
	}
	var fallback string
	err2 := p.db.QueryRow(`SELECT id, name FROM roles WHERE LOWER(name) = 'viewer' LIMIT 1`).Scan(&id, &fallback)
	if err2 == nil {
		log.Printf("auth: role %q not found, fell back to %q", roleName, fallback)
		return id, fallback
	}
	err3 := p.db.QueryRow(`SELECT id, name FROM roles ORDER BY name LIMIT 1`).Scan(&id, &fallback)
	if err3 == nil {
		log.Printf("auth: role %q not found, fell back to first available %q", roleName, fallback)
		return id, fallback
	}
	return uuid.Nil, roleName
}

func (p *UserProvisioner) createUser(user *UserContext) error {
	roleName, roleID := p.resolveRole(user, "")
	if roleID == uuid.Nil {
		roleID, roleName = p.resolveRoleID(roleName)
	}

	userID := uuid.New()
	// Note: DB columns mas_enabled/mas_username/last_mas_sync retain legacy names
	// to avoid a migration; they track OIDC-provisioned users.
	_, err := p.db.Exec(`
		INSERT INTO users (id, username, email, full_name, role_id, role, mas_enabled, mas_username,
		                   last_mas_sync, is_active, is_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, '00000000-0000-0000-0000-000000000000'::uuid),
		        $6, true, $7, $8, true, true, NOW(), NOW())
	`, userID, user.Username, user.Email, user.FullName, roleID, roleName, user.Username, time.Now())
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	log.Printf("auth: provisioned new user %s (role=%s)", user.Username, roleName)
	p.syncGroups(userID, user.Groups)
	return nil
}

func (p *UserProvisioner) updateUser(userID uuid.UUID, user *UserContext) error {
	var existingRole string
	_ = p.db.QueryRow(`
		SELECT COALESCE(r.name, NULLIF(TRIM(u.role), ''), '')
		FROM users u LEFT JOIN roles r ON u.role_id = r.id
		WHERE u.id = $1
	`, userID).Scan(&existingRole)

	roleName, roleID := p.resolveRole(user, existingRole)
	if roleID == uuid.Nil {
		roleID, roleName = p.resolveRoleID(roleName)
	}

	var err error
	if roleID != uuid.Nil {
		_, err = p.db.Exec(`
			UPDATE users
			SET email = $1, full_name = COALESCE(NULLIF($2, ''), full_name), role = $3, role_id = $4,
			    mas_enabled = true, mas_username = $5, last_mas_sync = $6, updated_at = NOW()
			WHERE id = $7
		`, user.Email, user.FullName, roleName, roleID, user.Username, time.Now(), userID)
	} else {
		_, err = p.db.Exec(`
			UPDATE users
			SET email = $1, full_name = COALESCE(NULLIF($2, ''), full_name), role = $3,
			    mas_enabled = true, mas_username = $4, last_mas_sync = $5, updated_at = NOW()
			WHERE id = $6
		`, user.Email, user.FullName, roleName, user.Username, time.Now(), userID)
	}
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	if existingRole != roleName {
		log.Printf("auth: role updated for %s: %q %q", user.Username, existingRole, roleName)
	}

	p.syncGroups(userID, user.Groups)
	return nil
}

// syncGroups replaces the user's OIDC group membership.
// The underlying table (user_mas_groups / mas_group column) retains legacy names
// to avoid a DB migration.
func (p *UserProvisioner) syncGroups(userID uuid.UUID, groups []string) {
	p.db.Exec(`DELETE FROM user_mas_groups WHERE user_id = $1`, userID)
	for _, g := range groups {
		p.db.Exec(`
			INSERT INTO user_mas_groups (user_id, mas_group, synced_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (user_id, mas_group) DO UPDATE SET synced_at = NOW()
		`, userID, strings.TrimSpace(g))
	}
}

func (p *UserProvisioner) userExists(username string) (bool, uuid.UUID, error) {
	var userID uuid.UUID
	err := p.db.QueryRow(`
		SELECT id FROM users WHERE mas_username = $1 OR username = $1
	`, username).Scan(&userID)
	if err == sql.ErrNoRows {
		return false, uuid.Nil, nil
	}
	if err != nil {
		return false, uuid.Nil, err
	}
	return true, userID, nil
}

// lookupRoleFromGroups queries ldap_group_role_mappings for ALL of the user's groups
// and returns the single most-privileged role that any group confers.
func (p *UserProvisioner) lookupRoleFromGroups(groups []string) (string, uuid.UUID) {
	if len(groups) == 0 {
		return "", uuid.Nil
	}
	placeholders := make([]string, len(groups))
	args := make([]interface{}, len(groups))
	for i, g := range groups {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = strings.TrimSpace(g)
	}
	query := fmt.Sprintf(`
		SELECT r.name, r.id
		FROM ldap_group_role_mappings m
		JOIN roles r ON m.role_id = r.id
		WHERE m.ldap_group IN (%s)
		ORDER BY
			CASE LOWER(r.name)
				WHEN 'admin'       THEN 1
				WHEN 'superadmin'  THEN 1
				WHEN 'operator'    THEN 2
				WHEN 'sre'         THEN 2
				WHEN 'engineer'    THEN 3
				WHEN 'viewer'      THEN 4
				ELSE 9
			END ASC
		LIMIT 1
	`, strings.Join(placeholders, ","))
	var roleName string
	var roleID uuid.UUID
	_ = p.db.QueryRow(query, args...).Scan(&roleName, &roleID)
	return roleName, roleID
}

// SyncAllUsers refreshes the last-seen timestamp for all OIDC-provisioned users.
func (p *UserProvisioner) SyncAllUsers() (int, error) {
	rows, err := p.db.Query(`
		SELECT id, mas_username FROM users WHERE mas_enabled = true AND mas_username IS NOT NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to query OIDC users: %w", err)
	}
	defer rows.Close()

	synced := 0
	for rows.Next() {
		var userID int
		var oidcUsername string
		if err := rows.Scan(&userID, &oidcUsername); err != nil {
			continue
		}
		if _, err = p.db.Exec(`UPDATE users SET last_mas_sync = NOW() WHERE id = $1`, userID); err == nil {
			synced++
		}
	}
	return synced, nil
}

// GetUserByOIDCUsername retrieves a user record with their current OIDC groups.
func (p *UserProvisioner) GetUserByOIDCUsername(username string) (map[string]interface{}, error) {
	row := p.db.QueryRow(`
		SELECT id, username, email, role, mas_enabled, mas_username, last_mas_sync, created_at, updated_at
		FROM users WHERE mas_username = $1 OR username = $1
	`, username)

	var id int
	var uname, email, role string
	var oidcEnabled bool
	var oidcUsername sql.NullString
	var lastSync sql.NullTime
	var createdAt, updatedAt time.Time

	err := row.Scan(&id, &uname, &email, &role, &oidcEnabled, &oidcUsername, &lastSync, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	user := map[string]interface{}{
		"id": id, "username": uname, "email": email, "role": role,
		"oidc_enabled": oidcEnabled, "oidc_username": oidcUsername.String,
		"last_oidc_sync": lastSync.Time, "created_at": createdAt, "updated_at": updatedAt,
	}

	groupRows, err := p.db.Query(`SELECT mas_group, synced_at FROM user_mas_groups WHERE user_id = $1`, id)
	if err != nil {
		return user, nil
	}
	defer groupRows.Close()

	var groups []map[string]interface{}
	for groupRows.Next() {
		var group string
		var syncedAt time.Time
		if err := groupRows.Scan(&group, &syncedAt); err == nil {
			groups = append(groups, map[string]interface{}{"group": group, "synced_at": syncedAt})
		}
	}
	user["oidc_groups"] = groups
	return user, nil
}

// GetEnvGroupConfig returns ALERTHUB_ADMIN_GROUPS and ALERTHUB_OPERATOR_GROUPS
// as a diagnostic aid for the Admin LDAP Mappings panel.
func (p *UserProvisioner) GetEnvGroupConfig() map[string][]string {
	return map[string][]string{
		"admin_groups":    p.adminGroups,
		"operator_groups": p.opGroups,
	}
}

// helpers 

func splitGroups(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func groupIntersects(userGroups, configGroups []string) bool {
	for _, ug := range userGroups {
		ul := strings.ToLower(strings.TrimSpace(ug))
		for _, cg := range configGroups {
			if ul == strings.ToLower(strings.TrimSpace(cg)) {
				return true
			}
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
