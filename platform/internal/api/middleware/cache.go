package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/cache"
)

// CacheMiddleware provides intelligent caching for API responses
func CacheMiddleware(redisCache *cache.RedisCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip caching for:
		// - Non-GET requests
		// - WebSocket upgrades
		// - Admin operations
		// - Authentication endpoints
		if c.Request.Method != "GET" ||
			c.GetHeader("Upgrade") == "websocket" ||
			strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin") ||
			strings.HasPrefix(c.Request.URL.Path, "/api/v1/auth") ||
			// AI model responses depend on the OIDCProvider token — skip cache so each
			// request can reach OIDCProvider with the user's current token.
			(strings.HasPrefix(c.Request.URL.Path, "/api/v1/ai/models") && c.GetHeader("X-OIDCProvider-Token") != "") {
			c.Next()
			return
		}

		// Generate cache key from request
		cacheKey := generateCacheKey(c)

		// Try to get cached response
		var cachedResponse CachedResponse
		err := redisCache.Get(cacheKey, &cachedResponse)
		if err == nil {
			// Cache hit - return cached response
			c.Header("X-Cache", "HIT")
			c.Header("X-Cache-Key", cacheKey)

			// Set cached headers
			for key, value := range cachedResponse.Headers {
				c.Header(key, value)
			}

			c.Data(cachedResponse.StatusCode, cachedResponse.ContentType, cachedResponse.Body)
			c.Abort()
			return
		}

		// Cache miss - create response writer wrapper
		c.Header("X-Cache", "MISS")
		c.Header("X-Cache-Key", cacheKey)

		// Create response recorder
		writer := &responseWriter{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
		}
		c.Writer = writer

		// Process request
		c.Next()

		// Cache successful GET responses
		if c.Writer.Status() == http.StatusOK && c.Request.Method == "GET" {
			// Determine TTL based on endpoint
			ttl := getCacheTTL(c.Request.URL.Path)

			// Store in cache
			cachedResp := CachedResponse{
				StatusCode:  c.Writer.Status(),
				ContentType: c.Writer.Header().Get("Content-Type"),
				Body:        writer.body.Bytes(),
				Headers:     make(map[string]string),
			}

			// Store important headers
			for key := range c.Writer.Header() {
				if key != "Set-Cookie" { // Don't cache cookies
					cachedResp.Headers[key] = c.Writer.Header().Get(key)
				}
			}

			// Save to Redis
			redisCache.Set(cacheKey, cachedResp, ttl)
		}
	}
}

// CachedResponse stores HTTP response in cache
type CachedResponse struct {
	StatusCode  int               `json:"status_code"`
	ContentType string            `json:"content_type"`
	Body        []byte            `json:"body"`
	Headers     map[string]string `json:"headers"`
}

// responseWriter wraps gin.ResponseWriter to capture response
type responseWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// generateCacheKey creates unique cache key for request
func generateCacheKey(c *gin.Context) string {
	// Include: method, path, query params, user ID (if authenticated)
	keyParts := []string{
		c.Request.Method,
		c.Request.URL.Path,
		c.Request.URL.RawQuery,
	}

	// Include user ID for personalized responses
	if userID, exists := c.Get("user_id"); exists {
		keyParts = append(keyParts, fmt.Sprintf("user:%v", userID))
	}

	// Create hash of key parts
	keyString := strings.Join(keyParts, "|")
	hash := sha256.Sum256([]byte(keyString))
	return fmt.Sprintf("api:cache:%s", hex.EncodeToString(hash[:]))
}

// getCacheTTL returns appropriate TTL based on endpoint
func getCacheTTL(path string) time.Duration {
	switch {
	// Static/reference data - cache for 1 hour
	case strings.HasPrefix(path, "/api/v1/config"):
		return 1 * time.Hour
	case strings.HasPrefix(path, "/api/v1/roles"):
		return 30 * time.Minute
	case strings.HasPrefix(path, "/api/v1/integrations"):
		return 15 * time.Minute

	// AI models - short cache (5 min) since results depend on OIDCProvider token validity
	case strings.HasPrefix(path, "/api/v1/ai/models"):
		return 5 * time.Minute
	case strings.HasPrefix(path, "/api/v1/ai/providers"):
		return 1 * time.Hour

	// Analytics and stats - cache for 5 minutes
	case strings.HasPrefix(path, "/api/v1/analytics"):
		return 5 * time.Minute
	case strings.HasPrefix(path, "/api/v1/incidents/stats"):
		return 5 * time.Minute

	// Alerts - cache for 30 seconds (frequently changing)
	case strings.HasPrefix(path, "/api/v1/alerts"):
		return 30 * time.Second

	// Incidents - cache for 1 minute
	case strings.HasPrefix(path, "/api/v1/incidents"):
		return 1 * time.Minute

	// User data - cache for 5 minutes
	case strings.HasPrefix(path, "/api/v1/users"):
		return 5 * time.Minute

	// Workflows and correlation - cache for 10 minutes
	case strings.HasPrefix(path, "/api/v1/workflows"):
		return 10 * time.Minute
	case strings.HasPrefix(path, "/api/v1/correlation"):
		return 10 * time.Minute

	// AI sessions - cache for 5 minutes
	case strings.HasPrefix(path, "/api/v1/ai/sessions"):
		return 5 * time.Minute

	// Default - cache for 2 minutes
	default:
		return 2 * time.Minute
	}
}

// InvalidateCachePattern invalidates cache entries matching pattern
func InvalidateCachePattern(redisCache *cache.RedisCache, pattern string) error {
	if redisCache == nil {
		return nil
	}
	return redisCache.DeletePattern(fmt.Sprintf("api:cache:*%s*", pattern))
}

// InvalidateCacheMiddleware clears cache on POST/PUT/DELETE requests
func InvalidateCacheMiddleware(redisCache *cache.RedisCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Process request first
		c.Next()

		// Invalidate cache on data modifications (successful requests only)
		if redisCache != nil && c.Writer.Status() < 400 {
			switch c.Request.Method {
			case "POST", "PUT", "DELETE", "PATCH":
				// Invalidate related cache entries
				path := c.Request.URL.Path

				// Extract resource type from path
				parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
				if len(parts) > 0 {
					resource := parts[0]

					// Invalidate all cache entries for this resource
					pattern := fmt.Sprintf("*/%s*", resource)
					InvalidateCachePattern(redisCache, pattern)

					c.Header("X-Cache-Invalidated", resource)
				}
			}
		}
	}
}

// CacheStatsMiddleware adds cache statistics to response headers
func CacheStatsMiddleware(redisCache *cache.RedisCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		if redisCache != nil {
			stats, err := redisCache.GetStats()
			if err == nil {
				// Add basic stats to headers
				c.Header("X-Redis-Status", "connected")

				// Parse hit rate from stats if available
				if info, ok := stats["info"]; ok {
					if strings.Contains(info, "instantaneous_ops_per_sec") {
						c.Header("X-Redis-Ops", "active")
					}
				}
			}
		}
		c.Next()
	}
}
