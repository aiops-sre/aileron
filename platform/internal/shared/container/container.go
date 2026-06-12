package container

import (
	"context"
	"fmt"
	"sync"

	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
)

// Container is a dependency injection container that manages service instances
type Container struct {
	mu                  sync.RWMutex
	correlationEngine   interfaces.CorrelationEngine
	incidentManager     interfaces.IncidentManager
	alertManager        interfaces.AlertManager
	topologyService     interfaces.TopologyService
	aiService           interfaces.AIService
	notificationService interfaces.NotificationService
	userService         interfaces.UserService
	databaseManager     interfaces.DatabaseManager
	cache               interfaces.Cache
	metricsCollector    interfaces.MetricsCollector
	logger              interfaces.Logger
	serviceRegistry     interfaces.ServiceRegistry
	configManager       interfaces.ConfigManager
	healthCheckers      []interfaces.HealthChecker
}

// NewContainer creates a new dependency injection container
func NewContainer() *Container {
	return &Container{
		healthCheckers: make([]interfaces.HealthChecker, 0),
	}
}

// SetCorrelationEngine sets the correlation engine implementation
func (c *Container) SetCorrelationEngine(engine interfaces.CorrelationEngine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.correlationEngine = engine
	c.addHealthChecker(engine)
}

// GetCorrelationEngine returns the correlation engine implementation
func (c *Container) GetCorrelationEngine() interfaces.CorrelationEngine {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.correlationEngine
}

// SetIncidentManager sets the incident manager implementation
func (c *Container) SetIncidentManager(manager interfaces.IncidentManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.incidentManager = manager
	c.addHealthChecker(manager)
}

// GetIncidentManager returns the incident manager implementation
func (c *Container) GetIncidentManager() interfaces.IncidentManager {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.incidentManager
}

// SetAlertManager sets the alert manager implementation
func (c *Container) SetAlertManager(manager interfaces.AlertManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alertManager = manager
	c.addHealthChecker(manager)
}

// GetAlertManager returns the alert manager implementation
func (c *Container) GetAlertManager() interfaces.AlertManager {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.alertManager
}

// SetTopologyService sets the topology service implementation
func (c *Container) SetTopologyService(service interfaces.TopologyService) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topologyService = service
	c.addHealthChecker(service)
}

// GetTopologyService returns the topology service implementation
func (c *Container) GetTopologyService() interfaces.TopologyService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.topologyService
}

// SetAIService sets the AI service implementation
func (c *Container) SetAIService(service interfaces.AIService) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aiService = service
	c.addHealthChecker(service)
}

// GetAIService returns the AI service implementation
func (c *Container) GetAIService() interfaces.AIService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.aiService
}

// SetNotificationService sets the notification service implementation
func (c *Container) SetNotificationService(service interfaces.NotificationService) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notificationService = service
	c.addHealthChecker(service)
}

// GetNotificationService returns the notification service implementation
func (c *Container) GetNotificationService() interfaces.NotificationService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.notificationService
}

// SetUserService sets the user service implementation
func (c *Container) SetUserService(service interfaces.UserService) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userService = service
	c.addHealthChecker(service)
}

// GetUserService returns the user service implementation
func (c *Container) GetUserService() interfaces.UserService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.userService
}

// SetDatabaseManager sets the database manager implementation
func (c *Container) SetDatabaseManager(manager interfaces.DatabaseManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.databaseManager = manager
	c.addHealthChecker(manager)
}

// GetDatabaseManager returns the database manager implementation
func (c *Container) GetDatabaseManager() interfaces.DatabaseManager {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.databaseManager
}

// SetCache sets the cache implementation
func (c *Container) SetCache(cache interfaces.Cache) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = cache
	c.addHealthChecker(cache)
}

// GetCache returns the cache implementation
func (c *Container) GetCache() interfaces.Cache {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache
}

// SetMetricsCollector sets the metrics collector implementation
func (c *Container) SetMetricsCollector(collector interfaces.MetricsCollector) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metricsCollector = collector
	c.addHealthChecker(collector)
}

// GetMetricsCollector returns the metrics collector implementation
func (c *Container) GetMetricsCollector() interfaces.MetricsCollector {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metricsCollector
}

// SetLogger sets the logger implementation
func (c *Container) SetLogger(logger interfaces.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

// GetLogger returns the logger implementation
func (c *Container) GetLogger() interfaces.Logger {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.logger
}

// SetServiceRegistry sets the service registry implementation
func (c *Container) SetServiceRegistry(registry interfaces.ServiceRegistry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.serviceRegistry = registry
	c.addHealthChecker(registry)
}

// GetServiceRegistry returns the service registry implementation
func (c *Container) GetServiceRegistry() interfaces.ServiceRegistry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serviceRegistry
}

// SetConfigManager sets the config manager implementation
func (c *Container) SetConfigManager(manager interfaces.ConfigManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configManager = manager
	c.addHealthChecker(manager)
}

// GetConfigManager returns the config manager implementation
func (c *Container) GetConfigManager() interfaces.ConfigManager {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configManager
}

// addHealthChecker adds a health checker to the container if it implements the interface
func (c *Container) addHealthChecker(service interface{}) {
	if hc, ok := service.(interfaces.HealthChecker); ok {
		c.healthCheckers = append(c.healthCheckers, hc)
	}
}

// HealthCheck performs health checks on all registered services
func (c *Container) HealthCheck(ctx context.Context) map[string]error {
	c.mu.RLock()
	checkers := make([]interfaces.HealthChecker, len(c.healthCheckers))
	copy(checkers, c.healthCheckers)
	c.mu.RUnlock()

	results := make(map[string]error)

	// Channel to collect results
	type result struct {
		name string
		err  error
	}

	resultsChan := make(chan result, len(checkers))

	// Run health checks concurrently
	for _, checker := range checkers {
		go func(hc interfaces.HealthChecker) {
			err := hc.HealthCheck()
			resultsChan <- result{name: hc.Name(), err: err}
		}(checker)
	}

	// Collect results
	for i := 0; i < len(checkers); i++ {
		select {
		case res := <-resultsChan:
			results[res.name] = res.err
		case <-ctx.Done():
			return results // Return partial results if context is cancelled
		}
	}

	return results
}

// IsHealthy returns true if all services are healthy
func (c *Container) IsHealthy(ctx context.Context) bool {
	results := c.HealthCheck(ctx)
	for _, err := range results {
		if err != nil {
			return false
		}
	}
	return true
}

// ValidateConfiguration validates that all required services are configured
func (c *Container) ValidateConfiguration() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	missing := make([]string, 0)

	if c.alertManager == nil {
		missing = append(missing, "AlertManager")
	}
	if c.incidentManager == nil {
		missing = append(missing, "IncidentManager")
	}
	if c.correlationEngine == nil {
		missing = append(missing, "CorrelationEngine")
	}
	if c.databaseManager == nil {
		missing = append(missing, "DatabaseManager")
	}
	if c.logger == nil {
		missing = append(missing, "Logger")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required services: %v", missing)
	}

	return nil
}

// Shutdown gracefully shuts down all services that support it
func (c *Container) Shutdown(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Add shutdown logic for services that implement a Shutdown interface
	// This is a placeholder for future implementation
	return nil
}

// GetServiceNames returns the names of all registered services
func (c *Container) GetServiceNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := make([]string, 0)

	if c.alertManager != nil {
		names = append(names, "AlertManager")
	}
	if c.incidentManager != nil {
		names = append(names, "IncidentManager")
	}
	if c.correlationEngine != nil {
		names = append(names, "CorrelationEngine")
	}
	if c.topologyService != nil {
		names = append(names, "TopologyService")
	}
	if c.aiService != nil {
		names = append(names, "AIService")
	}
	if c.notificationService != nil {
		names = append(names, "NotificationService")
	}
	if c.userService != nil {
		names = append(names, "UserService")
	}
	if c.databaseManager != nil {
		names = append(names, "DatabaseManager")
	}
	if c.cache != nil {
		names = append(names, "Cache")
	}
	if c.metricsCollector != nil {
		names = append(names, "MetricsCollector")
	}
	if c.logger != nil {
		names = append(names, "Logger")
	}
	if c.serviceRegistry != nil {
		names = append(names, "ServiceRegistry")
	}
	if c.configManager != nil {
		names = append(names, "ConfigManager")
	}

	return names
}

// Global container instance
var globalContainer *Container
var containerOnce sync.Once

// GetGlobalContainer returns the global container instance (singleton)
func GetGlobalContainer() *Container {
	containerOnce.Do(func() {
		globalContainer = NewContainer()
	})
	return globalContainer
}

// SetGlobalContainer sets the global container instance
func SetGlobalContainer(container *Container) {
	globalContainer = container
}
