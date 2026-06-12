package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/lib/pq"
)

var (
	ErrResourceExhausted   = errors.New("resource exhausted")
	ErrConnectionPoolFull  = errors.New("connection pool full")
	ErrMemoryLimitExceeded = errors.New("memory limit exceeded")
	ErrRateLimitExceeded   = errors.New("rate limit exceeded")
	ErrCircuitBreakerOpen  = errors.New("circuit breaker open")
)

// ResourceConfig defines resource management configuration
type ResourceConfig struct {
	// Database connection pool
	DBMaxOpenConns    int           `json:"db_max_open_conns" default:"25"`
	DBMaxIdleConns    int           `json:"db_max_idle_conns" default:"10"`
	DBConnMaxLifetime time.Duration `json:"db_conn_max_lifetime" default:"1h"`
	DBConnMaxIdleTime time.Duration `json:"db_conn_max_idle_time" default:"10m"`

	// Memory management
	MaxMemoryMB     int64 `json:"max_memory_mb" default:"1024"`
	MemoryWarningMB int64 `json:"memory_warning_mb" default:"768"`
	GCPercent       int   `json:"gc_percent" default:"100"`

	// Rate limiting (requests per second)
	AlertCreationRPS int `json:"alert_creation_rps" default:"100"`
	CorrelationRPS   int `json:"correlation_rps" default:"50"`
	APIRequestsRPS   int `json:"api_requests_rps" default:"1000"`

	// Circuit breakers
	CircuitBreakerConfig CircuitBreakerConfig `json:"circuit_breaker"`

	// Query limits
	MaxQueryResults      int           `json:"max_query_results" default:"1000"`
	MaxQueryTimeout      time.Duration `json:"max_query_timeout" default:"30s"`
	MaxConcurrentQueries int           `json:"max_concurrent_queries" default:"10"`

	// Enterprise scale settings
	MaxAlertsPerHour          int `json:"max_alerts_per_hour" default:"10000"`
	MaxConcurrentCorrelations int `json:"max_concurrent_correlations" default:"20"`
}

// CircuitBreakerConfig defines circuit breaker configuration
type CircuitBreakerConfig struct {
	FailureThreshold int           `json:"failure_threshold" default:"5"`
	SuccessThreshold int           `json:"success_threshold" default:"3"`
	Timeout          time.Duration `json:"timeout" default:"60s"`
	MaxRequests      int           `json:"max_requests" default:"10"`
}

// ResourceManager manages system resources and enforces limits
type ResourceManager struct {
	config         ResourceConfig
	db             *sql.DB
	rateLimiters   map[string]*RateLimiter
	circuitBreaker *CircuitBreaker
	queryLimiter   *QueryLimiter
	memoryMonitor  *MemoryMonitor
	mu             sync.RWMutex

	// Enterprise metrics
	metrics *ResourceMetrics
}

// ResourceMetrics tracks resource usage metrics
type ResourceMetrics struct {
	DBConnectionsActive   int              `json:"db_connections_active"`
	DBConnectionsIdle     int              `json:"db_connections_idle"`
	MemoryUsageMB         int64            `json:"memory_usage_mb"`
	MemoryUsagePercent    float64          `json:"memory_usage_percent"`
	AlertsProcessedPerMin int64            `json:"alerts_processed_per_minute"`
	QueriesActive         int              `json:"queries_active"`
	RateLimitHits         map[string]int64 `json:"rate_limit_hits"`
	CircuitBreakerState   string           `json:"circuit_breaker_state"`
	LastGCTime            time.Time        `json:"last_gc_time"`
	GCCount               int64            `json:"gc_count"`
}

// NewResourceManager creates a new enterprise resource manager
func NewResourceManager(config ResourceConfig) *ResourceManager {
	rm := &ResourceManager{
		config:         config,
		rateLimiters:   make(map[string]*RateLimiter),
		circuitBreaker: NewCircuitBreaker(config.CircuitBreakerConfig),
		queryLimiter:   NewQueryLimiter(config.MaxConcurrentQueries),
		memoryMonitor:  NewMemoryMonitor(config.MaxMemoryMB),
		metrics: &ResourceMetrics{
			RateLimitHits: make(map[string]int64),
		},
	}

	// Initialize rate limiters
	rm.rateLimiters["alert_creation"] = NewRateLimiter(config.AlertCreationRPS)
	rm.rateLimiters["correlation"] = NewRateLimiter(config.CorrelationRPS)
	rm.rateLimiters["api_requests"] = NewRateLimiter(config.APIRequestsRPS)

	// Start monitoring goroutines
	go rm.monitorResources()
	go rm.performPeriodicCleanup()

	return rm
}

// ConfigureDatabase configures database connection pool with enterprise settings
func (rm *ResourceManager) ConfigureDatabase(db *sql.DB) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Set connection pool parameters for enterprise scale
	db.SetMaxOpenConns(rm.config.DBMaxOpenConns)
	db.SetMaxIdleConns(rm.config.DBMaxIdleConns)
	db.SetConnMaxLifetime(rm.config.DBConnMaxLifetime)
	db.SetConnMaxIdleTime(rm.config.DBConnMaxIdleTime)

	rm.db = db

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	return nil
}

// CheckAlertCreationLimit checks if alert creation is within limits
func (rm *ResourceManager) CheckAlertCreationLimit(ctx context.Context) error {
	if !rm.rateLimiters["alert_creation"].Allow() {
		rm.metrics.RateLimitHits["alert_creation"]++
		return ErrRateLimitExceeded
	}

	return rm.memoryMonitor.CheckMemoryUsage()
}

// CheckCorrelationLimit checks if correlation processing is within limits
func (rm *ResourceManager) CheckCorrelationLimit(ctx context.Context) error {
	if !rm.rateLimiters["correlation"].Allow() {
		rm.metrics.RateLimitHits["correlation"]++
		return ErrRateLimitExceeded
	}

	return rm.queryLimiter.AcquireSlot(ctx)
}

// CheckAPIRequestLimit checks if API requests are within limits
func (rm *ResourceManager) CheckAPIRequestLimit(ctx context.Context) error {
	if !rm.rateLimiters["api_requests"].Allow() {
		rm.metrics.RateLimitHits["api_requests"]++
		return ErrRateLimitExceeded
	}

	return rm.memoryMonitor.CheckMemoryUsage()
}

// ExecuteWithCircuitBreaker executes a function with circuit breaker protection
func (rm *ResourceManager) ExecuteWithCircuitBreaker(ctx context.Context, operation string, fn func() error) error {
	return rm.circuitBreaker.Execute(ctx, operation, fn)
}

// RateLimiter implements token bucket rate limiting
type RateLimiter struct {
	tokens       chan struct{}
	refillTicker *time.Ticker
	mu           sync.Mutex
	closed       bool
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(rps int) *RateLimiter {
	rl := &RateLimiter{
		tokens: make(chan struct{}, rps),
	}

	// Fill initial tokens
	for i := 0; i < rps; i++ {
		select {
		case rl.tokens <- struct{}{}:
		default:
		}
	}

	// Refill tokens every second
	rl.refillTicker = time.NewTicker(time.Second / time.Duration(rps))
	go func() {
		for {
			select {
			case <-rl.refillTicker.C:
				select {
				case rl.tokens <- struct{}{}:
				default: // Bucket full
				}
			}
		}
	}()

	return rl
}

// Allow checks if a request should be allowed
func (rl *RateLimiter) Allow() bool {
	select {
	case <-rl.tokens:
		return true
	default:
		return false
	}
}

// Close stops the rate limiter
func (rl *RateLimiter) Close() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if !rl.closed {
		rl.refillTicker.Stop()
		close(rl.tokens)
		rl.closed = true
	}
}

// CircuitBreaker implements circuit breaker pattern for external services
type CircuitBreaker struct {
	config      CircuitBreakerConfig
	state       CircuitBreakerState
	failures    map[string]int
	lastAttempt map[string]time.Time
	mu          sync.RWMutex
}

type CircuitBreakerState int

const (
	CircuitBreakerClosed CircuitBreakerState = iota
	CircuitBreakerOpen
	CircuitBreakerHalfOpen
)

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		config:      config,
		state:       CircuitBreakerClosed,
		failures:    make(map[string]int),
		lastAttempt: make(map[string]time.Time),
	}
}

// Execute executes a function with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, operation string, fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check circuit state
	switch cb.state {
	case CircuitBreakerOpen:
		if time.Since(cb.lastAttempt[operation]) > cb.config.Timeout {
			cb.state = CircuitBreakerHalfOpen
		} else {
			return ErrCircuitBreakerOpen
		}
	}

	// Execute function
	cb.lastAttempt[operation] = time.Now()
	err := fn()

	if err != nil {
		cb.failures[operation]++
		if cb.failures[operation] >= cb.config.FailureThreshold {
			cb.state = CircuitBreakerOpen
		}
		return err
	}

	// Success
	delete(cb.failures, operation)
	if cb.state == CircuitBreakerHalfOpen {
		cb.state = CircuitBreakerClosed
	}

	return nil
}

// GetState returns the current circuit breaker state
func (cb *CircuitBreaker) GetState() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case CircuitBreakerClosed:
		return "closed"
	case CircuitBreakerOpen:
		return "open"
	case CircuitBreakerHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// QueryLimiter manages concurrent query limits
type QueryLimiter struct {
	semaphore chan struct{}
	active    int
	mu        sync.Mutex
}

// NewQueryLimiter creates a new query limiter
func NewQueryLimiter(maxConcurrent int) *QueryLimiter {
	return &QueryLimiter{
		semaphore: make(chan struct{}, maxConcurrent),
	}
}

// AcquireSlot acquires a query slot
func (ql *QueryLimiter) AcquireSlot(ctx context.Context) error {
	select {
	case ql.semaphore <- struct{}{}:
		ql.mu.Lock()
		ql.active++
		ql.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrResourceExhausted
	}
}

// ReleaseSlot releases a query slot
func (ql *QueryLimiter) ReleaseSlot() {
	select {
	case <-ql.semaphore:
		ql.mu.Lock()
		ql.active--
		ql.mu.Unlock()
	default:
	}
}

// GetActiveQueries returns the number of active queries
func (ql *QueryLimiter) GetActiveQueries() int {
	ql.mu.Lock()
	defer ql.mu.Unlock()
	return ql.active
}

// MemoryMonitor monitors and enforces memory limits
type MemoryMonitor struct {
	maxMemoryMB int64
	warningMB   int64
	lastGCTime  time.Time
	gcCount     int64
	mu          sync.RWMutex
}

// NewMemoryMonitor creates a new memory monitor
func NewMemoryMonitor(maxMemoryMB int64) *MemoryMonitor {
	return &MemoryMonitor{
		maxMemoryMB: maxMemoryMB,
		warningMB:   maxMemoryMB * 75 / 100, // Warning at 75%
	}
}

// CheckMemoryUsage checks current memory usage and enforces limits
func (mm *MemoryMonitor) CheckMemoryUsage() error {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	currentMB := int64(m.Alloc) / 1024 / 1024

	mm.mu.Lock()
	defer mm.mu.Unlock()

	if currentMB > mm.maxMemoryMB {
		// Force garbage collection
		runtime.GC()
		mm.lastGCTime = time.Now()
		mm.gcCount++

		// Check again after GC
		runtime.ReadMemStats(&m)
		currentMB = int64(m.Alloc) / 1024 / 1024

		if currentMB > mm.maxMemoryMB {
			return ErrMemoryLimitExceeded
		}
	} else if currentMB > mm.warningMB {
		// Trigger garbage collection as a warning measure
		go func() {
			runtime.GC()
			mm.mu.Lock()
			mm.lastGCTime = time.Now()
			mm.gcCount++
			mm.mu.Unlock()
		}()
	}

	return nil
}

// GetMemoryUsage returns current memory usage statistics
func (mm *MemoryMonitor) GetMemoryUsage() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	mm.mu.RLock()
	defer mm.mu.RUnlock()

	currentMB := int64(m.Alloc) / 1024 / 1024
	usagePercent := float64(currentMB) / float64(mm.maxMemoryMB) * 100

	return map[string]interface{}{
		"current_mb":    currentMB,
		"max_mb":        mm.maxMemoryMB,
		"usage_percent": usagePercent,
		"warning_mb":    mm.warningMB,
		"last_gc_time":  mm.lastGCTime.Format(time.RFC3339),
		"gc_count":      mm.gcCount,
		"heap_objects":  m.HeapObjects,
		"sys_memory_mb": int64(m.Sys) / 1024 / 1024,
	}
}

// ConnectionPool manages database connections with enterprise features
type ConnectionPool struct {
	db      *sql.DB
	config  ResourceConfig
	monitor *ConnectionMonitor
}

// ConnectionMonitor tracks connection pool metrics
type ConnectionMonitor struct {
	activeConnections  int
	idleConnections    int
	totalConnections   int
	connectionsFailed  int64
	connectionsCreated int64
	queriesExecuted    int64
	avgQueryDuration   time.Duration
	mu                 sync.RWMutex
}

// NewConnectionPool creates an enterprise-grade connection pool
func NewConnectionPool(dbURL string, config ResourceConfig) (*ConnectionPool, error) {
	// Parse database URL and add enterprise connection settings
	connector, err := pq.NewConnector(dbURL)
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(connector)

	// Apply enterprise connection pool settings
	db.SetMaxOpenConns(config.DBMaxOpenConns)
	db.SetMaxIdleConns(config.DBMaxIdleConns)
	db.SetConnMaxLifetime(config.DBConnMaxLifetime)
	db.SetConnMaxIdleTime(config.DBConnMaxIdleTime)

	monitor := &ConnectionMonitor{}

	pool := &ConnectionPool{
		db:      db,
		config:  config,
		monitor: monitor,
	}

	// Test initial connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Start connection monitoring
	go pool.startConnectionMonitoring()

	return pool, nil
}

// ExecContext executes a query with resource monitoring
func (cp *ConnectionPool) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		cp.updateQueryMetrics(duration)
	}()

	// Check if we can acquire a connection
	if cp.monitor.activeConnections >= cp.config.DBMaxOpenConns {
		cp.monitor.connectionsFailed++
		return nil, ErrConnectionPoolFull
	}

	result, err := cp.db.ExecContext(ctx, query, args...)
	if err != nil {
		cp.monitor.connectionsFailed++
	} else {
		cp.monitor.queriesExecuted++
	}

	return result, err
}

// QueryContext executes a query with resource monitoring and limits
func (cp *ConnectionPool) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		cp.updateQueryMetrics(duration)
	}()

	// Add timeout to context if not already set
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cp.config.MaxQueryTimeout)
		defer cancel()
	}

	rows, err := cp.db.QueryContext(ctx, query, args...)
	if err != nil {
		cp.monitor.connectionsFailed++
	} else {
		cp.monitor.queriesExecuted++
	}

	return rows, err
}

func (cp *ConnectionPool) updateQueryMetrics(duration time.Duration) {
	cp.monitor.mu.Lock()
	defer cp.monitor.mu.Unlock()

	// Update average query duration (simple moving average)
	if cp.monitor.avgQueryDuration == 0 {
		cp.monitor.avgQueryDuration = duration
	} else {
		cp.monitor.avgQueryDuration = (cp.monitor.avgQueryDuration + duration) / 2
	}
}

func (cp *ConnectionPool) startConnectionMonitoring() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cp.updateConnectionStats()
		}
	}
}

func (cp *ConnectionPool) updateConnectionStats() {
	stats := cp.db.Stats()

	cp.monitor.mu.Lock()
	defer cp.monitor.mu.Unlock()

	cp.monitor.activeConnections = stats.OpenConnections
	cp.monitor.idleConnections = stats.Idle
	cp.monitor.totalConnections = stats.OpenConnections
}

// GetConnectionStats returns current connection statistics
func (cp *ConnectionPool) GetConnectionStats() map[string]interface{} {
	stats := cp.db.Stats()

	cp.monitor.mu.RLock()
	defer cp.monitor.mu.RUnlock()

	return map[string]interface{}{
		"max_open_conns":        stats.MaxOpenConnections,
		"open_connections":      stats.OpenConnections,
		"in_use":                stats.InUse,
		"idle":                  stats.Idle,
		"wait_count":            stats.WaitCount,
		"wait_duration_ns":      stats.WaitDuration.Nanoseconds(),
		"max_idle_closed":       stats.MaxIdleClosed,
		"max_idle_time_closed":  stats.MaxIdleTimeClosed,
		"max_lifetime_closed":   stats.MaxLifetimeClosed,
		"queries_executed":      cp.monitor.queriesExecuted,
		"connections_failed":    cp.monitor.connectionsFailed,
		"avg_query_duration_ms": float64(cp.monitor.avgQueryDuration.Nanoseconds()) / 1e6,
	}
}

// GetDB returns the underlying database connection
func (cp *ConnectionPool) GetDB() *sql.DB {
	return cp.db
}

// Resource monitoring and cleanup
func (rm *ResourceManager) monitorResources() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rm.updateMetrics()
		}
	}
}

func (rm *ResourceManager) updateMetrics() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.db != nil {
		stats := rm.db.Stats()
		rm.metrics.DBConnectionsActive = stats.OpenConnections
		rm.metrics.DBConnectionsIdle = stats.Idle
	}

	// Update memory metrics
	memUsage := rm.memoryMonitor.GetMemoryUsage()
	rm.metrics.MemoryUsageMB = memUsage["current_mb"].(int64)
	rm.metrics.MemoryUsagePercent = memUsage["usage_percent"].(float64)
	rm.metrics.LastGCTime = rm.memoryMonitor.lastGCTime
	rm.metrics.GCCount = rm.memoryMonitor.gcCount

	// Update circuit breaker state
	rm.metrics.CircuitBreakerState = rm.circuitBreaker.GetState()

	// Update query metrics
	rm.metrics.QueriesActive = rm.queryLimiter.GetActiveQueries()
}

func (rm *ResourceManager) performPeriodicCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rm.performCleanup()
		}
	}
}

func (rm *ResourceManager) performCleanup() {
	// Reset rate limit hit counters periodically
	rm.mu.Lock()
	for key := range rm.metrics.RateLimitHits {
		rm.metrics.RateLimitHits[key] = rm.metrics.RateLimitHits[key] / 2 // Decay
	}
	rm.mu.Unlock()

	// Force garbage collection if memory usage is high
	memUsage := rm.memoryMonitor.GetMemoryUsage()
	if memUsage["usage_percent"].(float64) > 80 {
		runtime.GC()
		rm.memoryMonitor.gcCount++
		rm.memoryMonitor.lastGCTime = time.Now()
	}
}

// GetMetrics returns current resource metrics
func (rm *ResourceManager) GetMetrics() *ResourceMetrics {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Create a copy to avoid concurrent access issues
	metricsCopy := &ResourceMetrics{
		DBConnectionsActive:   rm.metrics.DBConnectionsActive,
		DBConnectionsIdle:     rm.metrics.DBConnectionsIdle,
		MemoryUsageMB:         rm.metrics.MemoryUsageMB,
		MemoryUsagePercent:    rm.metrics.MemoryUsagePercent,
		AlertsProcessedPerMin: rm.metrics.AlertsProcessedPerMin,
		QueriesActive:         rm.metrics.QueriesActive,
		RateLimitHits:         make(map[string]int64),
		CircuitBreakerState:   rm.metrics.CircuitBreakerState,
		LastGCTime:            rm.metrics.LastGCTime,
		GCCount:               rm.metrics.GCCount,
	}

	// Copy rate limit hits
	for k, v := range rm.metrics.RateLimitHits {
		metricsCopy.RateLimitHits[k] = v
	}

	return metricsCopy
}

// ValidateResourceConfiguration validates resource configuration for enterprise deployment
func ValidateResourceConfiguration(config ResourceConfig) error {
	if config.DBMaxOpenConns < 10 {
		return errors.New("db_max_open_conns too low for enterprise deployment (minimum 10)")
	}
	if config.DBMaxOpenConns > 100 {
		return errors.New("db_max_open_conns too high (maximum 100 for stability)")
	}
	if config.MaxMemoryMB < 512 {
		return errors.New("max_memory_mb too low for enterprise deployment (minimum 512MB)")
	}
	if config.APIRequestsRPS < 100 {
		return errors.New("api_requests_rps too low for enterprise scale (minimum 100 RPS)")
	}
	if config.MaxAlertsPerHour < 1000 {
		return errors.New("max_alerts_per_hour too low for 150-server environment (minimum 1000)")
	}

	return nil
}

// GetDefaultEnterpriseConfig returns recommended configuration for 150-server deployment
func GetDefaultEnterpriseConfig() ResourceConfig {
	return ResourceConfig{
		// Database: Optimized for high-throughput alert processing
		DBMaxOpenConns:    25,               // Balanced for 150 servers
		DBMaxIdleConns:    10,               // Keep connections warm
		DBConnMaxLifetime: 60 * time.Minute, // Rotate connections hourly
		DBConnMaxIdleTime: 10 * time.Minute, // Clean up idle connections

		// Memory: Conservative limits for stability
		MaxMemoryMB:     2048, // 2GB limit
		MemoryWarningMB: 1536, // Warning at 1.5GB
		GCPercent:       100,  // Default GC behavior

		// Rate limiting: Scaled for enterprise load
		AlertCreationRPS: 200,  // 200 alerts/sec for 150 servers
		CorrelationRPS:   100,  // 100 correlations/sec
		APIRequestsRPS:   1500, // 1500 API requests/sec

		// Circuit breakers: Conservative for reliability
		CircuitBreakerConfig: CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 3,
			Timeout:          60 * time.Second,
			MaxRequests:      10,
		},

		// Query limits: Prevent runaway queries
		MaxQueryResults:      5000, // Large enough for dashboards
		MaxQueryTimeout:      30 * time.Second,
		MaxConcurrentQueries: 15, // Balance between throughput and resource usage

		// Enterprise limits
		MaxAlertsPerHour:          15000, // 15k alerts/hour for 150 servers
		MaxConcurrentCorrelations: 25,    // 25 concurrent correlation processes
	}
}

// Close cleans up all resources
func (rm *ResourceManager) Close() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Close rate limiters
	for _, limiter := range rm.rateLimiters {
		limiter.Close()
	}

	// Close database connections
	if rm.db != nil {
		return rm.db.Close()
	}

	return nil
}
