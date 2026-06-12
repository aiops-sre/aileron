package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/apikeys"
)

// APIKeyAuth extends the existing auth middleware to accept enterprise API keys
// (prefix "sk-ah-") in addition to JWT tokens. When an API key is detected
// it is validated against the api_keys table; the resolved user_id, api_key_id,
// and scopes are injected into the gin context exactly like JWT claims.
//
// This middleware does NOT touch the webhook_api_keys table or validateAPIKey()
// used by inbound webhook sources (Dynatrace etc.) — those remain unchanged.
type APIKeyAuth struct {
	svc *apikeys.Service
}

func NewAPIKeyAuth(svc *apikeys.Service) *APIKeyAuth {
	return &APIKeyAuth{svc: svc}
}

// Middleware returns a gin.HandlerFunc. It runs before (or instead of) JWT auth.
// If the Bearer token starts with "sk-ah-", it is treated as an enterprise API
// key. Otherwise the middleware is a no-op and falls through to JWT auth.
func (a *APIKeyAuth) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			c.Next()
			return
		}

		token := strings.TrimSpace(parts[1])
		if !strings.HasPrefix(token, "sk-ah-") {
			// Not an API key — let JWT middleware handle it
			c.Next()
			return
		}

		key, err := a.svc.ValidateKey(c.Request.Context(), token)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "API key validation failed",
			})
			c.Abort()
			return
		}
		if key == nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid or expired API key",
			})
			c.Abort()
			return
		}

		// Set context vars matching what JWT auth sets so downstream handlers
		// don't need to know which auth mechanism was used.
		c.Set("user_id", key.UserID)
		c.Set("api_key_id", key.ID)
		c.Set("api_key_scopes", key.Scopes)
		c.Set("auth_method", "api_key")

		// Async usage bump — fire-and-forget
		go a.svc.UpdateLastUsed(key.ID, c.ClientIP())

		c.Next()
	}
}

// RequireScope returns a handler that aborts with 403 if the authenticated
// entity (JWT user or API key) does not hold the required scope. JWT users
// with role "admin" pass unconditionally.
func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// JWT admin bypasses scope checks
		if role, exists := c.Get("role"); exists {
			if r, ok := role.(string); ok && r == "admin" {
				c.Next()
				return
			}
		}

		// Check API key scopes
		if scopes, exists := c.Get("api_key_scopes"); exists {
			if sl, ok := scopes.([]string); ok {
				for _, s := range sl {
					if s == apikeys.ScopeAdmin || s == scope {
						c.Next()
						return
					}
				}
			}
		}

		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Scope required: " + scope,
		})
		c.Abort()
	}
}
