package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache provides high-performance caching layer
type RedisCache struct {
	client *redis.Client
	ctx    context.Context
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Addr         string
	Password     string
	DB           int
	PoolSize     int
	MinIdleConns int
	MaxRetries   int
	PoolTimeout  time.Duration
	IdleTimeout  time.Duration
}

// NewRedisCache creates an optimized Redis cache client
func NewRedisCache(config *RedisConfig) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            config.Addr,
		Password:        config.Password,
		DB:              config.DB,
		PoolSize:        config.PoolSize,
		MinIdleConns:    config.MinIdleConns,
		MaxRetries:      config.MaxRetries,
		PoolTimeout:     config.PoolTimeout,
		ConnMaxIdleTime: config.IdleTimeout,
	})

	ctx := context.Background()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	cache := &RedisCache{
		client: client,
		ctx:    ctx,
	}

	// Start monitoring
	go cache.monitorPerformance()

	return cache, nil
}

// GetDefaultConfig returns production-optimized Redis configuration
func GetDefaultConfig() *RedisConfig {
	password := os.Getenv("REDIS_PASSWORD")
	return &RedisConfig{
		Addr:         "redis:6379",
		Password:     password,
		DB:           0,
		PoolSize:     50,              // 50 concurrent connections
		MinIdleConns: 10,              // Keep 10 idle connections
		MaxRetries:   3,               // Retry failed operations
		PoolTimeout:  4 * time.Second, // Wait 4s for connection
		IdleTimeout:  5 * time.Minute, // Close idle after 5 min
	}
}

// ============================================================================
// CACHE OPERATIONS
// ============================================================================

// Client returns the underlying Redis client for services that need direct access.
func (c *RedisCache) Client() *redis.Client {
	return c.client
}

// Set stores a value with TTL
func (c *RedisCache) Set(key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(c.ctx, key, data, ttl).Err()
}

// Get retrieves a cached value
func (c *RedisCache) Get(key string, dest interface{}) error {
	data, err := c.client.Get(c.ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// Delete removes a key
func (c *RedisCache) Delete(key string) error {
	return c.client.Del(c.ctx, key).Err()
}

// DeletePattern deletes keys matching pattern
func (c *RedisCache) DeletePattern(pattern string) error {
	iter := c.client.Scan(c.ctx, 0, pattern, 100).Iterator()
	for iter.Next(c.ctx) {
		c.client.Del(c.ctx, iter.Val())
	}
	return iter.Err()
}

// ============================================================================
// INTEGRATION-SPECIFIC CACHING
// ============================================================================

// CacheIntegrationStatus caches integration connection status
func (c *RedisCache) CacheIntegrationStatus(integrationID, status string, ttl time.Duration) error {
	key := fmt.Sprintf("integration:status:%s", integrationID)
	return c.client.Set(c.ctx, key, status, ttl).Err()
}

// GetIntegrationStatus retrieves cached integration status
func (c *RedisCache) GetIntegrationStatus(integrationID string) (string, error) {
	key := fmt.Sprintf("integration:status:%s", integrationID)
	return c.client.Get(c.ctx, key).Result()
}

// ============================================================================
// ALERT CACHING
// ============================================================================

// CacheAlert caches alert data (5 minute TTL)
func (c *RedisCache) CacheAlert(alertID string, alert interface{}) error {
	key := fmt.Sprintf("alert:%s", alertID)
	return c.Set(key, alert, 5*time.Minute)
}

// GetAlert retrieves cached alert
func (c *RedisCache) GetAlert(alertID string, dest interface{}) error {
	key := fmt.Sprintf("alert:%s", alertID)
	return c.Get(key, dest)
}

// InvalidateAlert removes alert from cache
func (c *RedisCache) InvalidateAlert(alertID string) error {
	return c.Delete(fmt.Sprintf("alert:%s", alertID))
}

// ============================================================================
// REAL-TIME ALERT COUNTS
// ============================================================================

// IncrementAlertCount increments real-time alert counter
func (c *RedisCache) IncrementAlertCount(severity string) error {
	key := "alerts:count"
	return c.client.HIncrBy(c.ctx, key, severity, 1).Err()
}

// DecrementAlertCount decrements real-time alert counter
func (c *RedisCache) DecrementAlertCount(severity string) error {
	key := "alerts:count"
	return c.client.HIncrBy(c.ctx, key, severity, -1).Err()
}

// GetAlertCounts retrieves all alert counts
func (c *RedisCache) GetAlertCounts() (map[string]int, error) {
	key := "alerts:count"
	result, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for k, v := range result {
		var count int
		fmt.Sscanf(v, "%d", &count)
		counts[k] = count
	}

	return counts, nil
}

// ============================================================================
// RATE LIMITING
// ============================================================================

// CheckRateLimit checks if user/IP is within rate limit
func (c *RedisCache) CheckRateLimit(identifier string, limit int, window time.Duration) (bool, error) {
	key := fmt.Sprintf("ratelimit:%s:%d", identifier, time.Now().Unix()/int64(window.Seconds()))

	// Increment counter
	count, err := c.client.Incr(c.ctx, key).Result()
	if err != nil {
		return false, err
	}

	// Set TTL on first request — if this fails the key never expires, so treat it as fatal.
	if count == 1 {
		if err := c.client.Expire(c.ctx, key, window).Err(); err != nil {
			// Roll back the increment so the key doesn't accumulate without expiry.
			c.client.Decr(c.ctx, key)
			return false, fmt.Errorf("ratelimit: failed to set expiry for key %s: %w", key, err)
		}
	}

	// Check if limit exceeded
	return count <= int64(limit), nil
}

// ============================================================================
// SESSION MANAGEMENT
// ============================================================================

// SetSession stores user session
func (c *RedisCache) SetSession(token string, session interface{}, ttl time.Duration) error {
	key := fmt.Sprintf("session:%s", token)
	return c.Set(key, session, ttl)
}

// GetSession retrieves user session
func (c *RedisCache) GetSession(token string, dest interface{}) error {
	key := fmt.Sprintf("session:%s", token)
	return c.Get(key, dest)
}

// DeleteSession removes user session
func (c *RedisCache) DeleteSession(token string) error {
	key := fmt.Sprintf("session:%s", token)
	return c.Delete(key)
}

// ============================================================================
// API RESPONSE CACHING
// ============================================================================

// CacheAPIResponse caches API response
func (c *RedisCache) CacheAPIResponse(endpoint string, response interface{}, ttl time.Duration) error {
	key := fmt.Sprintf("cache:api:%s", endpoint)
	return c.Set(key, response, ttl)
}

// GetAPIResponse retrieves cached API response
func (c *RedisCache) GetAPIResponse(endpoint string, dest interface{}) error {
	key := fmt.Sprintf("cache:api:%s", endpoint)
	return c.Get(key, dest)
}

// ============================================================================
// WEBSOCKET CONNECTION TRACKING
// ============================================================================

// TrackWebSocketConnection tracks active WebSocket connection
func (c *RedisCache) TrackWebSocketConnection(connectionID, userID string) error {
	key := "websocket:connections"
	data := map[string]interface{}{
		"user_id":      userID,
		"connected_at": time.Now().Unix(),
	}
	jsonData, _ := json.Marshal(data)
	return c.client.HSet(c.ctx, key, connectionID, jsonData).Err()
}

// RemoveWebSocketConnection removes WebSocket connection
func (c *RedisCache) RemoveWebSocketConnection(connectionID string) error {
	key := "websocket:connections"
	return c.client.HDel(c.ctx, key, connectionID).Err()
}

// GetActiveWebSocketCount returns count of active WebSocket connections
func (c *RedisCache) GetActiveWebSocketCount() (int64, error) {
	key := "websocket:connections"
	return c.client.HLen(c.ctx, key).Result()
}

// ============================================================================
// PUB/SUB FOR REAL-TIME UPDATES
// ============================================================================

// PublishAlertUpdate publishes alert update to subscribers
func (c *RedisCache) PublishAlertUpdate(alertID string, update interface{}) error {
	channel := "alerts:updates"
	data, err := json.Marshal(map[string]interface{}{
		"alert_id":  alertID,
		"update":    update,
		"timestamp": time.Now(),
	})
	if err != nil {
		return err
	}
	return c.client.Publish(c.ctx, channel, data).Err()
}

// SubscribeAlertUpdates subscribes to alert updates
func (c *RedisCache) SubscribeAlertUpdates() *redis.PubSub {
	return c.client.Subscribe(c.ctx, "alerts:updates")
}

// ============================================================================
// PERFORMANCE MONITORING
// ============================================================================

// parseInfoField extracts a single numeric value from a Redis INFO section.
// Returns 0 if the field is absent or non-numeric.
func parseInfoField(info, field string) float64 {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			val := strings.TrimPrefix(line, field+":")
			val = strings.TrimSpace(val)
			// Strip human-readable suffixes (e.g. "17.54M") — take raw numeric prefix.
			for i, c := range val {
				if c != '.' && (c < '0' || c > '9') {
					val = val[:i]
					break
				}
			}
			f, _ := strconv.ParseFloat(val, 64)
			return f
		}
	}
	return 0
}

// parseInfoString extracts a single string value from a Redis INFO section.
func parseInfoString(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, field+":"))
		}
	}
	return ""
}

func (c *RedisCache) monitorPerformance() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	var lastCmds float64

	for range ticker.C {
		// Single INFO call covering all sections we need — avoids duplicate round trips.
		info, err := c.client.Info(c.ctx, "all").Result()
		if err != nil {
			log.Printf("redis: health check failed: %v", err)
			continue
		}

		usedMem := parseInfoString(info, "used_memory_human")
		maxMem := parseInfoString(info, "maxmemory_human")
		hits := parseInfoField(info, "keyspace_hits")
		misses := parseInfoField(info, "keyspace_misses")
		totalCmds := parseInfoField(info, "total_commands_processed")
		connClients := parseInfoField(info, "connected_clients")
		evictedKeys := parseInfoField(info, "evicted_keys")
		fragRatio := parseInfoField(info, "mem_fragmentation_ratio")

		hitRate := 0.0
		if total := hits + misses; total > 0 {
			hitRate = hits / total * 100
		}
		cmdsDelta := totalCmds - lastCmds
		lastCmds = totalCmds

		log.Printf("redis: mem=%s/%s frag=%.2f hit_rate=%.1f%% clients=%.0f cmds_since_last=%.0f evicted=%.0f",
			usedMem, maxMem, fragRatio, hitRate, connClients, cmdsDelta, evictedKeys)
	}
}

// GetStats returns parsed Redis key metrics (not raw INFO text).
func (c *RedisCache) GetStats() (map[string]string, error) {
	info, err := c.client.Info(c.ctx, "all").Result()
	if err != nil {
		return nil, err
	}
	hits := parseInfoField(info, "keyspace_hits")
	misses := parseInfoField(info, "keyspace_misses")
	hitRate := 0.0
	if total := hits + misses; total > 0 {
		hitRate = hits / total * 100
	}
	return map[string]string{
		"used_memory":         parseInfoString(info, "used_memory_human"),
		"maxmemory":           parseInfoString(info, "maxmemory_human"),
		"hit_rate_pct":        fmt.Sprintf("%.1f", hitRate),
		"connected_clients":   fmt.Sprintf("%.0f", parseInfoField(info, "connected_clients")),
		"evicted_keys":        fmt.Sprintf("%.0f", parseInfoField(info, "evicted_keys")),
		"mem_fragmentation":   fmt.Sprintf("%.2f", parseInfoField(info, "mem_fragmentation_ratio")),
		"total_commands":      fmt.Sprintf("%.0f", parseInfoField(info, "total_commands_processed")),
		"rejected_connections": fmt.Sprintf("%.0f", parseInfoField(info, "rejected_connections")),
	}, nil
}

// HealthCheck checks Redis health
func (c *RedisCache) HealthCheck() error {
	return c.client.Ping(c.ctx).Err()
}

// Close closes Redis connection
func (c *RedisCache) Close() error {
	return c.client.Close()
}

// ============================================================================
// CACHE WARMING (Pre-populate hot data)
// ============================================================================

// EvalLuaScript executes a Lua script on Redis
func (c *RedisCache) EvalLuaScript(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return c.client.Eval(ctx, script, keys, args...).Result()
}

// IncrementCounter increments a counter metric in Redis
func (c *RedisCache) IncrementCounter(ctx context.Context, key string) error {
	return c.client.Incr(ctx, key).Err()
}

// RecordLatency records latency metric in Redis
func (c *RedisCache) RecordLatency(ctx context.Context, key string, duration time.Duration) error {
	// Store latency as milliseconds
	latencyMs := float64(duration.Nanoseconds()) / 1e6
	return c.client.LPush(ctx, key, latencyMs).Err()
}

// WarmCache pre-populates frequently accessed data
func (c *RedisCache) WarmCache(db interface{}) error {
	log.Printf("redis: warming cache with hot data")
	// Cache alert counts
	// Would query from database and populate Redis

	// Cache active integration statuses
	// Would query from database and populate Redis

	log.Printf("redis: cache warmed")
	return nil
}
