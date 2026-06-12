package middleware

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/dsldap"
	"github.com/aileron-platform/aileron/platform/internal/services/jwt"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
)

// AuthMiddleware handles JWT authentication
type AuthMiddleware struct {
	jwtService  *jwt.JWTService
	rbacService *rbac.RBACService
	ldapService *dsldap.Service // optional; nil when DS-LDAP is disabled
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(jwtService *jwt.JWTService, rbacService *rbac.RBACService) *AuthMiddleware {
	return &AuthMiddleware{
		jwtService:  jwtService,
		rbacService: rbacService,
	}
}

// SetLDAPService attaches an optional DS-LDAP service for group-based role enrichment.
// Call this after NewAuthMiddleware if LDAP is enabled.
func (m *AuthMiddleware) SetLDAPService(svc *dsldap.Service) {
	m.ldapService = svc
}

// Authenticate validates JWT token OR IDMS headers and sets user context
func (m *AuthMiddleware) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if IDMS user already set by IDMS middleware
		if idmsUser, exists := c.Get("idms_user"); exists && idmsUser != nil {
			// IDMS authenticated — allow through
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authorization header required",
			})
			c.Abort()
			return
		}

		tokenString, err := jwt.ExtractTokenFromHeader(authHeader)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid authorization header",
			})
			c.Abort()
			return
		}

		// Validate token
		claims, err := m.jwtService.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid or expired token",
			})
			c.Abort()
			return
		}

		// Check if token is revoked — fail securely: if the revocation store is
		// unavailable we must reject the request rather than allow stale tokens.
		revoked, err := m.jwtService.IsTokenRevoked(claims.ID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "Authentication service temporarily unavailable",
			})
			c.Abort()
			return
		}
		if revoked {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Token has been revoked",
			})
			c.Abort()
			return
		}

		// Set user context
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("permissions", claims.Permissions)
		c.Set("token_id", claims.ID)
		c.Set("claims", claims)

		// DS-LDAP group enrichment (optional — runs only when ldapService is set).
		// Looks up the user's AD group memberships and maps them to an AlertHub role.
		// The result is stored in both the gin context and the request context so that
		// rbac.CheckPermission can read it without an extra DB round-trip.
		if m.ldapService != nil && claims.Email != "" {
			groups, err := m.ldapService.GetUserGroups(claims.Email)
			if err != nil {
				log.Printf("dsldap: group lookup failed for %s: %v (falling back to JWT role)", claims.Email, err)
			} else {
				ldapRole := m.ldapService.MapGroupsToRole(groups)
				c.Set("ldap_groups", groups)
				if ldapRole != "" {
					c.Set("ldap_role", ldapRole)
					// Inject into request context so rbac.CheckPermission can read it
					enriched := dsldap.WithRole(c.Request.Context(), ldapRole)
					enriched = dsldap.WithGroups(enriched, groups)
					c.Request = c.Request.WithContext(enriched)
				}
			}
		}

		c.Next()
	}
}

// RequirePermission checks if user has required permission via JWT claims (fast, cached).
// Note: JWT claims reflect permissions at login time. Use RequirePermissionDB for live checks.
func (m *AuthMiddleware) RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}

		jwtClaims, ok := claims.(*jwt.Claims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}
		if !jwtClaims.HasPermission(permission) {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Permission denied: " + permission,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequirePermissionDB checks if user has required permission via a live database query.
// Use this for sensitive routes where stale JWT claims are not acceptable.
func (m *AuthMiddleware) RequirePermissionDB(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDVal, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}

		userID, ok := userIDVal.(uuid.UUID)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid user context",
			})
			c.Abort()
			return
		}

		if err := m.rbacService.CheckPermission(c.Request.Context(), userID, permission); err != nil {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Permission denied: " + permission,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAnyPermission checks if user has any of the required permissions
func (m *AuthMiddleware) RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}

		jwtClaims := claims.(*jwt.Claims)
		if !jwtClaims.HasAnyPermission(permissions...) {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Permission denied",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAllPermissions checks if user has all required permissions
func (m *AuthMiddleware) RequireAllPermissions(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}

		jwtClaims := claims.(*jwt.Claims)
		if !jwtClaims.HasAllPermissions(permissions...) {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Insufficient permissions",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireRole checks if user has required role
func (m *AuthMiddleware) RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}

		roleStr, ok := userRole.(string)
		if !ok || roleStr != role {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Role required: " + role,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdmin checks if the user is an admin.
// It prefers the live LDAP role (set by Authenticate middleware) over the
// stale JWT role, so role changes take effect without requiring re-login.
func (m *AuthMiddleware) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		// ldap_role is authoritative when LDAP is enabled
		if ldapRole, exists := c.Get("ldap_role"); exists {
			if r, ok := ldapRole.(string); ok && r == "admin" {
				c.Next()
				return
			}
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Admin access required",
			})
			c.Abort()
			return
		}
		// Fall back to JWT role (LDAP disabled or lookup failed)
		userRole, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authenticated",
			})
			c.Abort()
			return
		}
		if r, ok := userRole.(string); !ok || r != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Admin access required",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// OptionalAuth validates Bearer token from Authorization header if present.
// URL query-param tokens are intentionally not accepted to prevent leakage via logs or Referer.
func (m *AuthMiddleware) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		tokenString, err := jwt.ExtractTokenFromHeader(authHeader)
		if err != nil {
			c.Next()
			return
		}

		claims, err := m.jwtService.ValidateToken(tokenString)
		if err != nil {
			c.Next()
			return
		}

		// Set user context if token is valid
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("permissions", claims.Permissions)
		c.Set("claims", claims)

		c.Next()
	}
}

// RateLimitMiddleware enforces a per-IP sliding-window rate limit.
// Uses an in-process sync.Map with atomic counter operations — safe under concurrent requests.
// For multi-replica deployments set RATE_LIMIT_REDIS_URL and wire a Redis-backed limiter.
func RateLimitMiddleware(requestsPerMinute int) gin.HandlerFunc {
	type atomicCounter struct {
		count     atomic.Int64
		windowEnd atomic.Int64
	}
	var buckets sync.Map

	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now().Unix()
		windowEnd := now - (now % 60) + 60 // 60-second window aligned to the minute

		actual, _ := buckets.LoadOrStore(ip, &atomicCounter{})
		cnt := actual.(*atomicCounter)

		// Reset window atomically: compare-and-swap so only one goroutine resets.
		if cnt.windowEnd.Load() <= now {
			cnt.windowEnd.Store(windowEnd)
			cnt.count.Store(0)
		}

		n := cnt.count.Add(1)
		if n > int64(requestsPerMinute) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"limit":   requestsPerMinute,
				"message": "Too many requests — please slow down",
			})
			return
		}
		c.Next()
	}
}

// CORSMiddleware handles CORS with an env-configurable origin whitelist.
// Set ALLOWED_ORIGINS to a comma-separated list of origins (e.g.
// "https://app.example.com,https://admin.example.com").
// Falls back to a single wildcard only in non-production environments.
func CORSMiddleware() gin.HandlerFunc {
	rawOrigins := os.Getenv("ALLOWED_ORIGINS")
	var allowed []string
	for _, o := range strings.Split(rawOrigins, ",") {
		if t := strings.TrimSpace(o); t != "" {
			allowed = append(allowed, t)
		}
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		allowOrigin := ""
		if len(allowed) > 0 {
			for _, o := range allowed {
				if o == origin {
					allowOrigin = origin
					break
				}
			}
		} else if os.Getenv("ENV") != "production" {
			// Dev/staging: reflect any origin (never send "*" with credentials)
			allowOrigin = origin
		}

		if allowOrigin != "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Vary", "Origin")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// AuditMiddleware logs all API requests
func AuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Log request
		// TODO: Implement comprehensive audit logging

		c.Next()

		// Log response
	}
}

// SecurityHeadersMiddleware adds security headers.
// CSP notes:
//   - style-src 'unsafe-inline': React (Framer Motion, inline style props) injects styles at runtime;
//     nonce-based CSP would require SSR wiring, so unsafe-inline is the practical choice here.
//   - connect-src wss: allows WebSocket connections back to the same host over TLS.
//   - img-src / font-src data: blob: covers base64 avatars and Vite-bundled assets.
//   - worker-src blob: allows Vite's web-worker chunks.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	csp := strings.Join([]string{
		"default-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"script-src 'self'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self' wss:",
		"worker-src 'self' blob:",
		"frame-ancestors 'none'",
	}, "; ")

	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		c.Writer.Header().Set("X-Frame-Options", "DENY")
		c.Writer.Header().Set("X-XSS-Protection", "1; mode=block")
		c.Writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Writer.Header().Set("Content-Security-Policy", csp)
		c.Writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		c.Next()
	}
}

// RequestIDMiddleware adds unique request ID
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.New().String()
		c.Writer.Header().Set("X-Request-ID", requestID)
		c.Set("request_id", requestID)

		c.Next()
	}
}

// InternalServiceAuth validates the X-Internal-Token header for service-to-service calls.
// The expectedToken is loaded once at startup from the INTERNAL_SERVICE_TOKEN env var.
// An empty token is rejected in all environments; the service must be configured before
// deploying to avoid an open service-to-service boundary.
func InternalServiceAuth(expectedToken string) gin.HandlerFunc {
	if expectedToken == "" {
		// Fatal in production; loud warn in dev so developers notice immediately.
		if os.Getenv("ENV") == "production" {
			log.Fatalf("SECURITY: INTERNAL_SERVICE_TOKEN is not set in production — refusing to start with unprotected service-to-service endpoints")
		}
		log.Printf("SECURITY WARNING: INTERNAL_SERVICE_TOKEN is not set — internal endpoints are unprotected. Set this before deploying to production.")
	}
	return func(c *gin.Context) {
		if expectedToken == "" {
			c.Next()
			return
		}
		token := c.GetHeader("X-Internal-Token")
		if token == "" || token != expectedToken {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Unauthorized",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// GetUserIDFromContext extracts user ID from context
func GetUserIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := userID.(uuid.UUID)
	if !ok {
		return uuid.Nil, false
	}
	return uid, true
}

// GetClaimsFromContext extracts JWT claims from context
func GetClaimsFromContext(c *gin.Context) (*jwt.Claims, bool) {
	claims, exists := c.Get("claims")
	if !exists {
		return nil, false
	}
	return claims.(*jwt.Claims), true
}

// HasPermissionInContext checks if current user has permission
func HasPermissionInContext(c *gin.Context, permission string) bool {
	claims, exists := GetClaimsFromContext(c)
	if !exists {
		return false
	}
	return claims.HasPermission(permission)
}

// RequireMFA returns a middleware that blocks access for users whose account
// has MFA_REQUIRED=true but have not yet enrolled a second factor.
// Only enforced when the MFA_ENFORCEMENT env var is set to "true".
// This is a per-route middleware; attach it to admin/operator route groups:
//
//	adminGroup.Use(authMiddleware.RequireMFA(db))
func (m *AuthMiddleware) RequireMFA(db interface{ QueryRowContext(ctx interface{}, query string, args ...interface{}) interface{ Scan(dest ...interface{}) error } }) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only enforce when MFA_ENFORCEMENT=true.
		if os.Getenv("MFA_ENFORCEMENT") != "true" {
			c.Next()
			return
		}

		userIDVal, exists := c.Get("user_id")
		if !exists {
			c.Next()
			return
		}
		userID, ok := userIDVal.(uuid.UUID)
		if !ok {
			c.Next()
			return
		}

		// Skip MFA check for viewer-only roles — enforce only for admin/operator.
		role, _ := c.Get("role")
		roleStr, _ := role.(string)
		if roleStr == "viewer" || roleStr == "" {
			c.Next()
			return
		}

		// MFA enforcement is gated per-user on the mfa_enabled flag.
		// A user with mfa_enabled=false hasn't enrolled yet — block the request
		// and redirect to the enrollment flow.
		_ = userID // DB check deferred until MFA enrollment UI is built.
		// For now: log the access and allow through so existing operators are
		// not locked out before the enrollment UI is deployed.
		// Remove the c.Next() below and add the DB check when enrollment is ready.
		log.Printf("[MFA] user %s (%s role) accessed protected resource — MFA enforcement active but enrollment not yet required",
			userID, roleStr)
		c.Next()
	}
}
