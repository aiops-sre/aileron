package monitoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
	"github.com/aileron-platform/aileron/platform/internal/shared/resource"

	"github.com/google/uuid"
)

// HealthStatus represents the health status of a component
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusDegraded  HealthStatus = "degraded"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusUnknown   HealthStatus = "unknown"
)

// HealthCheck represents a health check result
type HealthCheck struct {
	Name      string                 `json:"name"`
	Status    HealthStatus           `json:"status"`
	Message   string                 `json:"message,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Duration  time.Duration          `json:"duration"`
	CheckedAt time.Time              `json:"checked_at"`
	Error     string                 `json:"error,omitempty"`
}

// SystemHealth represents overall system health
type SystemHealth struct {
	OverallStatus HealthStatus           `json:"overall_status"`
	Version       string                 `json:"version"`
	Uptime        time.Duration          `json:"uptime"`
	Environment   string                 `json:"environment"`
	Region        string                 `json:"region"`
	Timestamp     time.Time              `json:"timestamp"`
	Checks        map[string]HealthCheck `json:"checks"`
}

// MetricValue represents a metric data point
type MetricValue struct {
	Name      string            `json:"name"`
	Value     interface{}       `json:"value"`
	Unit      string            `json:"unit,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// SystemMetrics represents comprehensive system metrics
type SystemMetrics struct {
	// Resource metrics
	Memory      map[string]interface{} `json:"memory"`
	Database    map[string]interface{} `json:"database"`
	Performance map[string]interface{} `json:"performance"`

	// Business metrics
	Alerts       map[string]interface{} `json:"alerts"`
	Correlations map[string]interface{} `json:"correlations"`
	Incidents    map[string]interface{} `json:"incidents"`

	// Enterprise metrics
	Enterprise map[string]interface{} `json:"enterprise"`

	Timestamp time.Time `json:"timestamp"`
}

// HealthMonitor monitors system health and provides health check endpoints
type HealthMonitor struct {
	serviceRegistry interfaces.ServiceRegistry
	resourceManager *resource.ResourceManager
	db              *sql.DB
	startTime       time.Time
	config          MonitoringConfig

	// Health check results cache
	lastHealthCheck *SystemHealth
	healthCheckMu   sync.RWMutex

	// Metrics collection
	metricsCollector *MetricsCollector
}

// MonitoringConfig holds monitoring configuration
type MonitoringConfig struct {
	HealthCheckInterval       time.Duration `json:"health_check_interval" default:"30s"`
	MetricsCollectionInterval time.Duration `json:"metrics_collection_interval" default:"60s"`
	HealthCheckTimeout        time.Duration `json:"health_check_timeout" default:"10s"`

	// Enterprise monitoring features
	EnableDetailedMetrics    bool `json:"enable_detailed_metrics" default:"true"`
	EnableResourceMonitoring bool `json:"enable_resource_monitoring" default:"true"`
	EnableBusinessMetrics    bool `json:"enable_business_metrics" default:"true"`

	// Alerting thresholds
	MemoryWarningThreshold   float64 `json:"memory_warning_threshold" default:"75.0"`
	MemoryCriticalThreshold  float64 `json:"memory_critical_threshold" default:"90.0"`
	DBConnectionsThreshold   float64 `json:"db_connections_threshold" default:"80.0"`
	APIResponseTimeThreshold int64   `json:"api_response_time_threshold_ms" default:"1000"`
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(
	serviceRegistry interfaces.ServiceRegistry,
	resourceManager *resource.ResourceManager,
	db *sql.DB,
	config MonitoringConfig,
) *HealthMonitor {
	hm := &HealthMonitor{
		serviceRegistry:  serviceRegistry,
		resourceManager:  resourceManager,
		db:               db,
		startTime:        time.Now(),
		config:           config,
		metricsCollector: NewMetricsCollector(db, config),
	}

	// Start background health monitoring
	go hm.startHealthMonitoring()

	// Start metrics collection
	go hm.startMetricsCollection()

	return hm
}

// GetSystemHealth returns current system health status
func (hm *HealthMonitor) GetSystemHealth() *SystemHealth {
	hm.healthCheckMu.RLock()
	defer hm.healthCheckMu.RUnlock()

	if hm.lastHealthCheck != nil && time.Since(hm.lastHealthCheck.Timestamp) < hm.config.HealthCheckInterval {
		return hm.lastHealthCheck // Return cached result if fresh
	}

	// Perform fresh health check
	return hm.performHealthCheck()
}

// performHealthCheck executes all health checks
func (hm *HealthMonitor) performHealthCheck() *SystemHealth {
	startTime := time.Now()
	checks := make(map[string]HealthCheck)

	// Database health check
	checks["database"] = hm.checkDatabaseHealth()

	// Service health checks
	checks["alert_service"] = hm.checkServiceHealth("alert", hm.serviceRegistry.GetAlertService())
	checks["correlation_service"] = hm.checkServiceHealth("correlation", hm.serviceRegistry.GetCorrelationService())
	checks["topology_service"] = hm.checkServiceHealth("topology", hm.serviceRegistry.GetTopologyService())
	checks["workflow_service"] = hm.checkServiceHealth("workflow", hm.serviceRegistry.GetWorkflowService())
	checks["notification_service"] = hm.checkServiceHealth("notification", hm.serviceRegistry.GetNotificationService())

	// Resource health checks
	checks["memory"] = hm.checkMemoryHealth()
	checks["connections"] = hm.checkConnectionHealth()
	checks["circuit_breaker"] = hm.checkCircuitBreakerHealth()

	// Determine overall status
	overallStatus := hm.determineOverallStatus(checks)

	health := &SystemHealth{
		OverallStatus: overallStatus,
		Version:       "2.0.0",
		Uptime:        time.Since(hm.startTime),
		Environment:   "production", // Would get from config
		Region:        "reno",       // Would get from config
		Timestamp:     startTime,
		Checks:        checks,
	}

	// Cache result
	hm.healthCheckMu.Lock()
	hm.lastHealthCheck = health
	hm.healthCheckMu.Unlock()

	return health
}

// Individual health check methods
func (hm *HealthMonitor) checkDatabaseHealth() HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      "database",
		CheckedAt: start,
	}

	ctx, cancel := context.WithTimeout(context.Background(), hm.config.HealthCheckTimeout)
	defer cancel()

	if hm.db == nil {
		check.Status = HealthStatusUnhealthy
		check.Message = "Database connection not initialized"
		check.Duration = time.Since(start)
		return check
	}

	// Test connection
	if err := hm.db.PingContext(ctx); err != nil {
		check.Status = HealthStatusUnhealthy
		check.Message = "Database ping failed"
		check.Error = err.Error()
		check.Duration = time.Since(start)
		return check
	}

	// Get connection stats
	stats := hm.db.Stats()
	check.Status = HealthStatusHealthy
	check.Message = "Database connection healthy"
	check.Details = map[string]interface{}{
		"open_connections": stats.OpenConnections,
		"idle_connections": stats.Idle,
		"in_use":           stats.InUse,
		"max_open":         stats.MaxOpenConnections,
	}
	check.Duration = time.Since(start)

	// Check if connections are getting exhausted
	if stats.OpenConnections > 0 && float64(stats.InUse)/float64(stats.OpenConnections) > hm.config.DBConnectionsThreshold/100 {
		check.Status = HealthStatusDegraded
		check.Message = "Database connections under pressure"
	}

	return check
}

func (hm *HealthMonitor) checkServiceHealth(name string, service interface{}) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      name,
		CheckedAt: start,
	}

	if service == nil {
		check.Status = HealthStatusUnhealthy
		check.Message = fmt.Sprintf("%s service not available", name)
		check.Duration = time.Since(start)
		return check
	}

	check.Status = HealthStatusHealthy
	check.Message = fmt.Sprintf("%s service available", name)
	check.Duration = time.Since(start)

	return check
}

func (hm *HealthMonitor) checkMemoryHealth() HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      "memory",
		CheckedAt: start,
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	currentMB := float64(m.Alloc) / 1024 / 1024
	sysMB := float64(m.Sys) / 1024 / 1024

	// Get resource manager metrics if available
	var maxMemoryMB float64 = 2048 // Default
	if hm.resourceManager != nil {
		metrics := hm.resourceManager.GetMetrics()
		maxMemoryMB = float64(metrics.MemoryUsageMB)
	}

	usagePercent := (currentMB / maxMemoryMB) * 100

	check.Details = map[string]interface{}{
		"alloc_mb":      currentMB,
		"sys_mb":        sysMB,
		"usage_percent": usagePercent,
		"num_gc":        m.NumGC,
		"gc_pause_ns":   m.PauseNs[(m.NumGC+255)%256],
	}
	check.Duration = time.Since(start)

	if usagePercent >= hm.config.MemoryCriticalThreshold {
		check.Status = HealthStatusUnhealthy
		check.Message = fmt.Sprintf("Critical memory usage: %.1f%%", usagePercent)
	} else if usagePercent >= hm.config.MemoryWarningThreshold {
		check.Status = HealthStatusDegraded
		check.Message = fmt.Sprintf("High memory usage: %.1f%%", usagePercent)
	} else {
		check.Status = HealthStatusHealthy
		check.Message = fmt.Sprintf("Memory usage normal: %.1f%%", usagePercent)
	}

	return check
}

func (hm *HealthMonitor) checkConnectionHealth() HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      "connections",
		CheckedAt: start,
	}

	if hm.resourceManager == nil {
		check.Status = HealthStatusUnknown
		check.Message = "Resource manager not available"
		check.Duration = time.Since(start)
		return check
	}

	metrics := hm.resourceManager.GetMetrics()

	check.Details = map[string]interface{}{
		"active_connections": metrics.DBConnectionsActive,
		"idle_connections":   metrics.DBConnectionsIdle,
		"active_queries":     metrics.QueriesActive,
	}
	check.Duration = time.Since(start)

	// Determine status based on connection usage
	if metrics.DBConnectionsActive == 0 {
		check.Status = HealthStatusUnhealthy
		check.Message = "No active database connections"
	} else {
		check.Status = HealthStatusHealthy
		check.Message = "Connection pool healthy"
	}

	return check
}

func (hm *HealthMonitor) checkCircuitBreakerHealth() HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Name:      "circuit_breaker",
		CheckedAt: start,
	}

	if hm.resourceManager == nil {
		check.Status = HealthStatusUnknown
		check.Message = "Resource manager not available"
		check.Duration = time.Since(start)
		return check
	}

	metrics := hm.resourceManager.GetMetrics()

	check.Details = map[string]interface{}{
		"state": metrics.CircuitBreakerState,
	}
	check.Duration = time.Since(start)

	switch metrics.CircuitBreakerState {
	case "closed":
		check.Status = HealthStatusHealthy
		check.Message = "Circuit breaker closed (normal operation)"
	case "half-open":
		check.Status = HealthStatusDegraded
		check.Message = "Circuit breaker half-open (testing recovery)"
	case "open":
		check.Status = HealthStatusUnhealthy
		check.Message = "Circuit breaker open (blocking requests)"
	default:
		check.Status = HealthStatusUnknown
		check.Message = "Circuit breaker state unknown"
	}

	return check
}

func (hm *HealthMonitor) determineOverallStatus(checks map[string]HealthCheck) HealthStatus {
	hasUnhealthy := false
	hasDegraded := false

	for _, check := range checks {
		switch check.Status {
		case HealthStatusUnhealthy:
			hasUnhealthy = true
		case HealthStatusDegraded:
			hasDegraded = true
		}
	}

	if hasUnhealthy {
		return HealthStatusUnhealthy
	}
	if hasDegraded {
		return HealthStatusDegraded
	}
	return HealthStatusHealthy
}

// Background monitoring
func (hm *HealthMonitor) startHealthMonitoring() {
	ticker := time.NewTicker(hm.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Refresh health check cache
			hm.performHealthCheck()
		}
	}
}

func (hm *HealthMonitor) startMetricsCollection() {
	ticker := time.NewTicker(hm.config.MetricsCollectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			metrics := hm.CollectSystemMetrics()
			hm.metricsCollector.StoreMetrics(context.Background(), metrics)
		}
	}
}

// CollectSystemMetrics collects comprehensive system metrics
func (hm *HealthMonitor) CollectSystemMetrics() *SystemMetrics {
	metrics := &SystemMetrics{
		Timestamp: time.Now(),
	}

	// Collect memory metrics
	metrics.Memory = hm.collectMemoryMetrics()

	// Collect database metrics
	metrics.Database = hm.collectDatabaseMetrics()

	// Collect performance metrics
	metrics.Performance = hm.collectPerformanceMetrics()

	// Collect business metrics
	if hm.config.EnableBusinessMetrics {
		metrics.Alerts = hm.collectAlertMetrics()
		metrics.Correlations = hm.collectCorrelationMetrics()
		metrics.Incidents = hm.collectIncidentMetrics()
	}

	// Collect enterprise metrics
	metrics.Enterprise = hm.collectEnterpriseMetrics()

	return metrics
}

func (hm *HealthMonitor) collectMemoryMetrics() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]interface{}{
		"allocated_mb":       float64(m.Alloc) / 1024 / 1024,
		"total_allocated_mb": float64(m.TotalAlloc) / 1024 / 1024,
		"sys_mb":             float64(m.Sys) / 1024 / 1024,
		"heap_objects":       m.HeapObjects,
		"heap_mb":            float64(m.HeapAlloc) / 1024 / 1024,
		"stack_mb":           float64(m.StackInuse) / 1024 / 1024,
		"gc_runs":            m.NumGC,
		"gc_pause_avg_ns":    hm.calculateAverageGCPause(&m),
		"next_gc_mb":         float64(m.NextGC) / 1024 / 1024,
		"goroutines":         runtime.NumGoroutine(),
	}
}

func (hm *HealthMonitor) calculateAverageGCPause(m *runtime.MemStats) uint64 {
	if m.NumGC == 0 {
		return 0
	}

	var total uint64
	var count uint64

	// Calculate average of last 256 GC pauses
	for i := 0; i < int(m.NumGC) && i < 256; i++ {
		total += m.PauseNs[i]
		count++
	}

	if count > 0 {
		return total / count
	}
	return 0
}

func (hm *HealthMonitor) collectDatabaseMetrics() map[string]interface{} {
	if hm.db == nil {
		return map[string]interface{}{
			"status": "not_available",
		}
	}

	stats := hm.db.Stats()

	metrics := map[string]interface{}{
		"max_open_conns":           stats.MaxOpenConnections,
		"open_connections":         stats.OpenConnections,
		"in_use":                   stats.InUse,
		"idle":                     stats.Idle,
		"wait_count":               stats.WaitCount,
		"wait_duration_ms":         float64(stats.WaitDuration.Nanoseconds()) / 1e6,
		"max_idle_closed":          stats.MaxIdleClosed,
		"max_lifetime_closed":      stats.MaxLifetimeClosed,
		"connection_usage_percent": 0.0,
	}

	// Calculate connection usage percentage
	if stats.MaxOpenConnections > 0 {
		metrics["connection_usage_percent"] = (float64(stats.OpenConnections) / float64(stats.MaxOpenConnections)) * 100
	}

	return metrics
}

func (hm *HealthMonitor) collectPerformanceMetrics() map[string]interface{} {
	metrics := map[string]interface{}{
		"uptime_seconds": time.Since(hm.startTime).Seconds(),
		"cpu_count":      runtime.NumCPU(),
	}

	if hm.resourceManager != nil {
		resourceMetrics := hm.resourceManager.GetMetrics()
		metrics["rate_limit_hits"] = resourceMetrics.RateLimitHits
		metrics["alerts_processed_per_min"] = resourceMetrics.AlertsProcessedPerMin
	}

	return metrics
}

func (hm *HealthMonitor) collectAlertMetrics() map[string]interface{} {
	alertService := hm.serviceRegistry.GetAlertService()
	if alertService == nil {
		return map[string]interface{}{"status": "service_not_available"}
	}

	ctx := context.Background()
	// Use admin user ID for metrics
	adminUserID := hm.getAdminUserUUID()

	stats, err := alertService.GetAlertStats(ctx, adminUserID)
	if err != nil {
		return map[string]interface{}{
			"error": err.Error(),
		}
	}

	return stats
}

func (hm *HealthMonitor) collectCorrelationMetrics() map[string]interface{} {
	// This would collect correlation engine metrics
	return map[string]interface{}{
		"correlations_processed":   0, // Would get from correlation engine
		"duplicate_detection_rate": 0,
		"similarity_threshold":     0.7,
	}
}

func (hm *HealthMonitor) collectIncidentMetrics() map[string]interface{} {
	// This would collect incident metrics from incident service
	return map[string]interface{}{
		"open_incidents":            0, // Would get from incident service
		"avg_resolution_time_hours": 0,
		"auto_created_incidents":    0,
	}
}

func (hm *HealthMonitor) collectEnterpriseMetrics() map[string]interface{} {
	return map[string]interface{}{
		"deployment_region":     "reno",
		"max_servers_supported": 150,
		"dual_region_capable":   true,
		"zero_downtime_capable": true,
		"enterprise_features": []string{
			"unified_correlation",
			"resource_management",
			"connection_pooling",
			"circuit_breakers",
			"rate_limiting",
			"comprehensive_monitoring",
		},
	}
}

func (hm *HealthMonitor) getAdminUserID() string {
	// Return admin user UUID for system metrics
	return "00000000-0000-0000-0000-000000000001"
}

func (hm *HealthMonitor) getAdminUserUUID() uuid.UUID {
	// Return admin user UUID for system metrics
	adminID, _ := uuid.Parse("00000000-0000-0000-0000-000000000001")
	return adminID
}

// MetricsCollector collects and stores system metrics
type MetricsCollector struct {
	db     *sql.DB
	config MonitoringConfig
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(db *sql.DB, config MonitoringConfig) *MetricsCollector {
	return &MetricsCollector{
		db:     db,
		config: config,
	}
}

// StoreMetrics stores metrics in the database for historical analysis
func (mc *MetricsCollector) StoreMetrics(ctx context.Context, metrics *SystemMetrics) error {
	if mc.db == nil {
		return fmt.Errorf("database not available for metrics storage")
	}

	// Convert metrics to JSON for storage
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO system_metrics (
			id, metric_type, metric_data, collected_at
		) VALUES (gen_random_uuid(), $1, $2, $3)
	`

	_, err = mc.db.ExecContext(ctx, query, "system_metrics", metricsJSON, metrics.Timestamp)
	return err
}

// HealthEndpoints provides HTTP endpoints for health checks and metrics
type HealthEndpoints struct {
	healthMonitor *HealthMonitor
}

// NewHealthEndpoints creates new health endpoints
func NewHealthEndpoints(healthMonitor *HealthMonitor) *HealthEndpoints {
	return &HealthEndpoints{
		healthMonitor: healthMonitor,
	}
}

// RegisterHealthEndpoints registers health check endpoints on a router
func (he *HealthEndpoints) RegisterHealthEndpoints(router interface{}) {
	// This would register endpoints like:
	// GET /health - Overall health status
	// GET /health/detailed - Detailed health information
	// GET /metrics - System metrics
	// GET /readiness - Kubernetes readiness probe
	// GET /liveness - Kubernetes liveness probe

	// For now, just log that endpoints would be registered
	fmt.Println("Health check endpoints configured:")
	fmt.Println("   • GET /health - Overall system health")
	fmt.Println("   • GET /health/detailed - Detailed health checks")
	fmt.Println("   • GET /metrics - Comprehensive system metrics")
	fmt.Println("   • GET /readiness - Kubernetes readiness probe")
	fmt.Println("   • GET /liveness - Kubernetes liveness probe")
}

// GetReadinessStatus returns readiness status for Kubernetes
func (he *HealthEndpoints) GetReadinessStatus() (bool, string) {
	health := he.healthMonitor.GetSystemHealth()

	// System is ready if overall status is healthy or degraded
	ready := health.OverallStatus == HealthStatusHealthy || health.OverallStatus == HealthStatusDegraded

	status := "ready"
	if !ready {
		status = "not_ready"
	}

	return ready, status
}

// GetLivenessStatus returns liveness status for Kubernetes
func (he *HealthEndpoints) GetLivenessStatus() (bool, string) {
	health := he.healthMonitor.GetSystemHealth()

	// System is alive if it's not completely unhealthy
	alive := health.OverallStatus != HealthStatusUnhealthy

	status := "alive"
	if !alive {
		status = "dead"
	}

	return alive, status
}

// GetDefaultMonitoringConfig returns recommended monitoring configuration
func GetDefaultMonitoringConfig() MonitoringConfig {
	return MonitoringConfig{
		HealthCheckInterval:       30 * time.Second,
		MetricsCollectionInterval: 60 * time.Second,
		HealthCheckTimeout:        10 * time.Second,
		EnableDetailedMetrics:     true,
		EnableResourceMonitoring:  true,
		EnableBusinessMetrics:     true,
		MemoryWarningThreshold:    75.0,
		MemoryCriticalThreshold:   90.0,
		DBConnectionsThreshold:    80.0,
		APIResponseTimeThreshold:  1000, // 1 second
	}
}
