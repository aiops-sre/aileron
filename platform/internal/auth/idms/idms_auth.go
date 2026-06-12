package idms

import (
	"crypto/hmac"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// UserContext holds IDMS user information extracted from headers or OAuth2 claims.
type UserContext struct {
	Username        string    `json:"username"`
	Email           string    `json:"email"`
	FullName        string    `json:"full_name"`
	Groups          []string  `json:"groups"`
	MappedRoles     []string  `json:"mapped_roles"`
	IsAdmin         bool      `json:"is_admin"`
	IsSRE           bool      `json:"is_sre"`
	AuthMethod      string    `json:"auth_method"` // "idms-oauth2" or "jwt"
	AuthenticatedAt time.Time `json:"authenticated_at"`
}

// Config holds IDMS authentication configuration.
type Config struct {
	Enabled       bool
	AutoProvision bool
	DefaultRole   string
	AdminGroups   []string
	SREGroups     []string
	GroupMappings map[string]GroupMapping
	StrictMode    bool // Require IDMS even when JWT present
}

// GroupMapping defines how DS-LDAP groups map to AlertHub roles.
type GroupMapping struct {
	LDAPGroup    string
	AlertHubRole string
	Priority     int
	AutoProvision bool
}

// DefaultConfig returns default IDMS configuration.
// Groups are resolved at runtime from DS-LDAP and DB ldap_group_role_mappings;
// the hardcoded mappings here are only used when neither source is available.
func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		AutoProvision: true,
		DefaultRole:   "viewer",
		AdminGroups:   []string{"aileron-admins"},
		SREGroups:     []string{"aileron-admins"},
		GroupMappings: map[string]GroupMapping{
			"aileron-admins": {
				LDAPGroup:     "aileron-admins",
				AlertHubRole:  "admin",
				Priority:      100,
				AutoProvision: true,
			},
			"aileron-operators": {
				LDAPGroup:     "aileron-operators",
				AlertHubRole:  "operator",
				Priority:      50,
				AutoProvision: true,
			},
			"aileron-operators": {
				LDAPGroup:     "aileron-operators",
				AlertHubRole:  "operator",
				Priority:      50,
				AutoProvision: true,
			},
			"aileron-viewers": {
				LDAPGroup:     "aileron-viewers",
				AlertHubRole:  "viewer",
				Priority:      10,
				AutoProvision: true,
			},
		},
		StrictMode: false,
	}
}

// Middleware extracts IDMS user identity injected by the NGINX Ingress via
// X-Forwarded-User / X-Forwarded-Mail / X-Forwarded-Groups headers.
// When headers are absent the middleware falls through to JWT auth.
func Middleware(config *Config) gin.HandlerFunc {
	// ingressSecret is set via IDMS_INGRESS_SECRET env var.
	// NGINX must inject X-Internal-Auth: <same-secret> on every proxied request.
	// This prevents in-cluster callers from forging identity headers by bypassing NGINX.
	ingressSecret := os.Getenv("IDMS_INGRESS_SECRET")

	return func(c *gin.Context) {
		// Guard: when a shared secret is configured, verify that this request
		// came through the trusted NGINX ingress (not a direct in-cluster caller).
		// Strip IDMS headers from any request that fails the check so the JWT
		// middleware further down the chain runs instead.
		if ingressSecret != "" {
			internalAuth := c.GetHeader("X-Internal-Auth")
			if !hmac.Equal([]byte(internalAuth), []byte(ingressSecret)) {
				c.Request.Header.Del("X-Forwarded-User")
				c.Request.Header.Del("X-Forwarded-Mail")
				c.Request.Header.Del("X-Forwarded-Name")
				c.Request.Header.Del("X-Auth-Request-Name")
				c.Request.Header.Del("X-Forwarded-Groups")
				c.Request.Header.Del("X-Forwarded-Access-Token")
				c.Request.Header.Del("X-Auth-Request-Access-Token")
				c.Next()
				return
			}
		}

		username := c.GetHeader("X-Forwarded-User")
		email := c.GetHeader("X-Forwarded-Mail")
		fullName := c.GetHeader("X-Forwarded-Name")
		if fullName == "" {
			fullName = c.GetHeader("X-Auth-Request-Name")
		}
		if fullName == "" {
			fullName = strings.Split(email, "@")[0]
		}
		groupsHeader := c.GetHeader("X-Forwarded-Groups")

		// Capture OAuth/OIDC access token forwarded by NGINX for downstream use (e.g. AI).
		oauthIDToken := c.GetHeader("X-Forwarded-Access-Token")
		if oauthIDToken == "" {
			oauthIDToken = c.GetHeader("X-Auth-Request-Access-Token")
		}
		if oauthIDToken == "" {
			if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				oauthIDToken = strings.TrimPrefix(auth, "Bearer ")
			}
		}

		if username == "" {
			if config.Enabled && config.StrictMode {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "Unauthorized",
					"message": "IDMS authentication required",
				})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		if strings.Contains(c.Request.URL.Path, "/auth/oidc/") {
			log.Printf("IDMS Middleware: User=%s, Email=%s, Groups=%s, HasOAuthToken=%v",
				username, email, groupsHeader, oauthIDToken != "")
		}

		var groups []string
		if groupsHeader != "" {
			groups = strings.Split(groupsHeader, ",")
			for i := range groups {
				groups[i] = strings.TrimSpace(groups[i])
			}
		}

		mappedRoles := MapGroupsToRoles(groups, config)
		isAdmin := HasAnyGroup(groups, config.AdminGroups)
		isSRE := HasAnyGroup(groups, config.SREGroups)

		userContext := &UserContext{
			Username:        username,
			Email:           email,
			FullName:        fullName,
			Groups:          groups,
			MappedRoles:     mappedRoles,
			IsAdmin:         isAdmin,
			IsSRE:           isSRE,
			AuthMethod:      "idms",
			AuthenticatedAt: time.Now(),
		}

		c.Set("idms_user", userContext)
		c.Set("user", userContext)

		if oauthIDToken != "" {
			c.Set("oauth_id_token", oauthIDToken)
		}

		c.Next()
	}
}

// RequireAuth middleware ensures IDMS or JWT authentication is present.
func RequireAuth(config *Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := GetUserFromContext(c)
		if !exists || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "Unauthorized",
				"message": "Authentication required",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireGroups middleware ensures user has at least one of the required DS-LDAP groups.
func RequireGroups(config *Config, requiredGroups ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := GetUserFromContext(c)
		if !exists || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "Unauthorized",
				"message": "Authentication required",
			})
			c.Abort()
			return
		}

		if !HasAnyGroup(user.Groups, requiredGroups) {
			c.JSON(http.StatusForbidden, gin.H{
				"error":           "Forbidden",
				"message":         "Insufficient permissions",
				"required_groups": requiredGroups,
				"user_groups":     user.Groups,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdmin middleware ensures user has admin role.
func RequireAdmin(config *Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := GetUserFromContext(c)
		if !exists || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "Unauthorized",
				"message": "Authentication required",
			})
			c.Abort()
			return
		}

		if !user.IsAdmin {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "Forbidden",
				"message": "Admin access required",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireSRE middleware ensures user has SRE or admin role.
func RequireSRE(config *Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := GetUserFromContext(c)
		if !exists || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "Unauthorized",
				"message": "Authentication required",
			})
			c.Abort()
			return
		}

		if !user.IsSRE && !user.IsAdmin {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "Forbidden",
				"message": "SRE or Admin access required",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetUserFromContext retrieves IDMS user context from Gin context.
func GetUserFromContext(c *gin.Context) (*UserContext, bool) {
	if u, exists := c.Get("idms_user"); exists {
		if user, ok := u.(*UserContext); ok {
			return user, true
		}
	}
	if u, exists := c.Get("user"); exists {
		if user, ok := u.(*UserContext); ok {
			return user, true
		}
	}
	return nil, false
}

// MapGroupsToRoles maps DS-LDAP groups to AlertHub roles using priority ordering.
func MapGroupsToRoles(groups []string, config *Config) []string {
	roleMap := make(map[string]int)

	for _, group := range groups {
		if mapping, exists := config.GroupMappings[group]; exists {
			if existingPriority, hasRole := roleMap[mapping.AlertHubRole]; !hasRole || mapping.Priority > existingPriority {
				roleMap[mapping.AlertHubRole] = mapping.Priority
			}
		}
	}

	if len(roleMap) == 0 {
		return []string{config.DefaultRole}
	}

	roles := make([]string, 0, len(roleMap))
	for role := range roleMap {
		roles = append(roles, role)
	}
	return roles
}

// HasAnyGroup checks if user has any of the required groups.
func HasAnyGroup(userGroups []string, requiredGroups []string) bool {
	for _, required := range requiredGroups {
		for _, userGroup := range userGroups {
			if userGroup == required {
				return true
			}
		}
	}
	return false
}

// HasAllGroups checks if user has all of the required groups.
func HasAllGroups(userGroups []string, requiredGroups []string) bool {
	for _, required := range requiredGroups {
		found := false
		for _, userGroup := range userGroups {
			if userGroup == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
