package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/cache"

	"github.com/gin-gonic/gin"
)

// RateLimiter handles rate limiting using Redis
type RateLimiter struct {
	redisCache *cache.RedisCache
}

// RateLimitConfig defines rate limiting configuration
type RateLimitConfig struct {
	RequestsPerMinute int           `json:"requests_per_minute" default:"100"`
	WindowSize        time.Duration `json:"window_size" default:"60s"`
	BurstLimit        int           `json:"burst_limit" default:"20"`
	BypassRoles       []string      `json:"bypass_roles"`
}

// RateLimitResult contains rate limit check results
type RateLimitResult struct {
	Allowed      bool   `json:"allowed"`
	RequestsLeft int    `json:"requests_left"`
	ResetTime    int64  `json:"reset_time"`
	RetryAfter   int    `json:"retry_after_seconds"`
	LimitType    string `json:"limit_type"`
	Identifier   string `json:"identifier"`
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(redisCache *cache.RedisCache) *RateLimiter {
	return &RateLimiter{
		redisCache: redisCache,
	}
}

// GetDefaultRateLimitConfig returns default rate limiting configuration
func GetDefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		RequestsPerMinute: 100,
		WindowSize:        time.Minute,
		BurstLimit:        20,
		BypassRoles:       []string{"admin", "system"},
	}
}

// RateLimitMiddleware creates a rate limiting middleware with configuration
func (rl *RateLimiter) RateLimitMiddleware(config *RateLimitConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if user role bypasses rate limiting
		if role, exists := c.Get("role"); exists {
			userRole := role.(string)
			for _, bypassRole := range config.BypassRoles {
				if userRole == bypassRole {
					c.Next()
					return
				}
			}
		}

		// Determine identifier for rate limiting
		identifier := rl.getIdentifier(c)

		// Check rate limit
		result, err := rl.checkRateLimit(identifier, config)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Rate limiting service unavailable",
				"error":   err.Error(),
			})
			c.Abort()
			return
		}

		// Add rate limit headers
		c.Header("X-RateLimit-Limit", strconv.Itoa(config.RequestsPerMinute))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(result.RequestsLeft))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(result.ResetTime, 10))

		if !result.Allowed {
			c.Header("Retry-After", strconv.Itoa(result.RetryAfter))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success":         false,
				"message":         "Rate limit exceeded",
				"rate_limit_info": result,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// checkRateLimit checks if request is within rate limit
func (rl *RateLimiter) checkRateLimit(identifier string, config *RateLimitConfig) (*RateLimitResult, error) {
	ctx := context.Background()
	now := time.Now()
	window := now.Truncate(config.WindowSize).Unix()

	// Keys for rate limiting
	key := fmt.Sprintf("ratelimit:%s:%d", identifier, window)
	burstKey := fmt.Sprintf("ratelimit:burst:%s", identifier)

	// Check burst limit first (for short-term spikes)
	burstAllowed, err := rl.checkBurstLimit(burstKey, config.BurstLimit)
	if err != nil {
		return nil, err
	}

	if !burstAllowed {
		resetTime := now.Add(time.Minute).Unix()
		return &RateLimitResult{
			Allowed:      false,
			RequestsLeft: 0,
			ResetTime:    resetTime,
			RetryAfter:   60,
			LimitType:    "burst",
			Identifier:   identifier,
		}, nil
	}

	// Check regular rate limit
	script := `
		local key = KEYS[1]
		local limit = tonumber(ARGV[1])
		local window = tonumber(ARGV[2])
		local current = redis.call('GET', key)
		
		if current == false then
			current = 0
		else
			current = tonumber(current)
		end
		
		if current < limit then
			redis.call('INCR', key)
			redis.call('EXPIRE', key, window)
			return {1, limit - current - 1}
		else
			return {0, 0}
		end
	`

	result, err := rl.redisCache.EvalLuaScript(ctx, script, []string{key}, config.RequestsPerMinute, int(config.WindowSize.Seconds()))
	if err != nil {
		return nil, err
	}

	results, ok := result.([]interface{})
	if !ok || len(results) < 2 {
		return nil, fmt.Errorf("unexpected rate limit script result type")
	}

	allowedInt, ok1 := results[0].(int64)
	remainingInt, ok2 := results[1].(int64)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("unexpected rate limit script result values")
	}

	allowed := allowedInt == 1
	remaining := int(remainingInt)

	resetTime := now.Add(config.WindowSize).Unix()
	retryAfter := int(config.WindowSize.Seconds())

	return &RateLimitResult{
		Allowed:      allowed,
		RequestsLeft: remaining,
		ResetTime:    resetTime,
		RetryAfter:   retryAfter,
		LimitType:    "standard",
		Identifier:   identifier,
	}, nil
}

// checkBurstLimit checks burst protection (sliding window)
func (rl *RateLimiter) checkBurstLimit(key string, limit int) (bool, error) {
	ctx := context.Background()
	now := time.Now().Unix()

	// Sliding window for burst detection (last 10 seconds)
	script := `
		local key = KEYS[1]
		local limit = tonumber(ARGV[1])
		local now = tonumber(ARGV[2])
		local window = 10
		
		-- Remove old entries
		redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
		
		-- Count current entries
		local current = redis.call('ZCARD', key)
		
		if current < limit then
			-- Add current request
			redis.call('ZADD', key, now, now)
			redis.call('EXPIRE', key, window)
			return 1
		else
			return 0
		end
	`

	result, err := rl.redisCache.EvalLuaScript(ctx, script, []string{key}, limit, now)
	if err != nil {
		return false, err
	}

	allowed, ok := result.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected burst limit script result type")
	}
	return allowed == 1, nil
}

// getIdentifier determines the rate limit identifier for a request.
// Priority: User ID (most specific) > API Key > real client IP.
// We use Gin's c.ClientIP() which respects TrustedProxies config — never
// trust a raw X-Forwarded-For header here as it can be spoofed by clients.
func (rl *RateLimiter) getIdentifier(c *gin.Context) string {
	// 1. User ID (most specific — authenticated users get a tighter, per-user bucket)
	if userID, exists := c.Get("user_id"); exists {
		return fmt.Sprintf("user:%s", userID)
	}

	// 2. API key presence (pre-auth identifier)
	if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
		return fmt.Sprintf("apikey:%s", apiKey)
	}

	// 3. Real IP — use Gin's ClientIP() which honours TrustedProxies, not raw headers.
	return fmt.Sprintf("ip:%s", c.ClientIP())
}

// EndpointSpecificRateLimit creates rate limiting for specific endpoints
func (rl *RateLimiter) EndpointSpecificRateLimit(configs map[string]*RateLimitConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		endpoint := c.FullPath()

		// Check if there's a specific config for this endpoint
		config, exists := configs[endpoint]
		if !exists {
			// Use default config
			config = GetDefaultRateLimitConfig()
		}

		// Apply rate limiting
		rateLimitHandler := rl.RateLimitMiddleware(config)
		rateLimitHandler(c)
	}
}

// ====================================================================
// ADAPTIVE RATE LIMITING - Adjusts based on system load
// ====================================================================

type AdaptiveRateLimiter struct {
	baseLimiter    *RateLimiter
	systemMonitor  SystemLoadMonitor
	adaptiveConfig *AdaptiveConfig
}

type AdaptiveConfig struct {
	BaseLimit        int     `json:"base_limit"`
	MaxLimit         int     `json:"max_limit"`
	MinLimit         int     `json:"min_limit"`
	CPUThreshold     float64 `json:"cpu_threshold"`
	MemoryThreshold  float64 `json:"memory_threshold"`
	AdjustmentFactor float64 `json:"adjustment_factor"`
}

type SystemLoadMonitor interface {
	GetCPUUsage() float64
	GetMemoryUsage() float64
}

// NewAdaptiveRateLimiter creates an adaptive rate limiter
func NewAdaptiveRateLimiter(baseLimiter *RateLimiter, monitor SystemLoadMonitor) *AdaptiveRateLimiter {
	return &AdaptiveRateLimiter{
		baseLimiter:   baseLimiter,
		systemMonitor: monitor,
		adaptiveConfig: &AdaptiveConfig{
			BaseLimit:        100,
			MaxLimit:         200,
			MinLimit:         20,
			CPUThreshold:     75.0,
			MemoryThreshold:  80.0,
			AdjustmentFactor: 0.5,
		},
	}
}

// AdaptiveRateLimitMiddleware adjusts rate limits based on system load
func (arl *AdaptiveRateLimiter) AdaptiveRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Calculate current system load
		cpuUsage := arl.systemMonitor.GetCPUUsage()
		memoryUsage := arl.systemMonitor.GetMemoryUsage()

		// Adjust rate limit based on load
		adjustedLimit := arl.calculateAdjustedLimit(cpuUsage, memoryUsage)

		// Create dynamic config
		config := &RateLimitConfig{
			RequestsPerMinute: adjustedLimit,
			WindowSize:        time.Minute,
			BurstLimit:        adjustedLimit / 5,
			BypassRoles:       []string{"admin", "system"},
		}

		// Add system load headers for debugging
		c.Header("X-System-CPU", fmt.Sprintf("%.1f", cpuUsage))
		c.Header("X-System-Memory", fmt.Sprintf("%.1f", memoryUsage))
		c.Header("X-Adjusted-Limit", strconv.Itoa(adjustedLimit))

		// Apply adaptive rate limiting
		rateLimitHandler := arl.baseLimiter.RateLimitMiddleware(config)
		rateLimitHandler(c)
	}
}

// calculateAdjustedLimit calculates adjusted rate limit based on system load
func (arl *AdaptiveRateLimiter) calculateAdjustedLimit(cpuUsage, memoryUsage float64) int {
	baseLimit := float64(arl.adaptiveConfig.BaseLimit)

	// Calculate load factor (0.0 to 1.0+)
	loadFactor := 0.0

	if cpuUsage > arl.adaptiveConfig.CPUThreshold {
		loadFactor += (cpuUsage - arl.adaptiveConfig.CPUThreshold) / 100.0
	}

	if memoryUsage > arl.adaptiveConfig.MemoryThreshold {
		loadFactor += (memoryUsage - arl.adaptiveConfig.MemoryThreshold) / 100.0
	}

	// Adjust limit (higher load = lower limit)
	adjustedLimit := baseLimit * (1.0 - loadFactor*arl.adaptiveConfig.AdjustmentFactor)

	// Ensure within bounds
	finalLimit := int(adjustedLimit)
	if finalLimit > arl.adaptiveConfig.MaxLimit {
		finalLimit = arl.adaptiveConfig.MaxLimit
	} else if finalLimit < arl.adaptiveConfig.MinLimit {
		finalLimit = arl.adaptiveConfig.MinLimit
	}

	return finalLimit
}

// ====================================================================
// RATE LIMIT METRICS AND MONITORING
// ====================================================================

// MetricsCollector interface for tracking rate limiting metrics
type MetricsCollector interface {
	IncrementCounter(metric string, labels map[string]string)
	RecordHistogram(metric string, value float64, labels map[string]string)
	SetGauge(metric string, value float64, labels map[string]string)
}

// RateLimitMetrics tracks rate limiting statistics
type RateLimitMetrics struct {
	redisCache       *cache.RedisCache
	metricsCollector MetricsCollector
}

// NewRateLimitMetrics creates a new rate limit metrics tracker
func NewRateLimitMetrics(redisCache *cache.RedisCache, metricsCollector MetricsCollector) *RateLimitMetrics {
	return &RateLimitMetrics{
		redisCache:       redisCache,
		metricsCollector: metricsCollector,
	}
}

// TrackRateLimitEvent tracks rate limiting events for monitoring
func (rlm *RateLimitMetrics) TrackRateLimitEvent(result *RateLimitResult, endpoint, method string) {
	if rlm.metricsCollector == nil {
		return // Gracefully handle nil metrics collector
	}

	labels := map[string]string{
		"endpoint":   endpoint,
		"method":     method,
		"limit_type": result.LimitType,
		"identifier": result.Identifier,
	}

	if result.Allowed {
		rlm.metricsCollector.IncrementCounter("rate_limit_requests_allowed_total", labels)
	} else {
		rlm.metricsCollector.IncrementCounter("rate_limit_requests_blocked_total", labels)
	}

	rlm.metricsCollector.SetGauge("rate_limit_requests_remaining", float64(result.RequestsLeft), labels)
}

// GetRateLimitStats returns current rate limiting statistics
func (rlm *RateLimitMetrics) GetRateLimitStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Get rate limit hit counts from Redis
	// This would query Redis for rate limiting statistics

	stats["total_requests"] = 0
	stats["blocked_requests"] = 0
	stats["allowed_requests"] = 0
	stats["current_limits"] = make(map[string]int)

	return stats, nil
}
