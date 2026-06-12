package registry

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
)

var (
	ErrServiceNotFound      = errors.New("service not found")
	ErrServiceAlreadyExists = errors.New("service already registered")
	ErrInvalidServiceType   = errors.New("invalid service type")
)

// ServiceRegistry implements the service registry interface
type ServiceRegistry struct {
	services map[string]interface{}
	mu       sync.RWMutex
	db       *sql.DB
}

// NewServiceRegistry creates a new service registry
func NewServiceRegistry(db *sql.DB) *ServiceRegistry {
	return &ServiceRegistry{
		services: make(map[string]interface{}),
		db:       db,
	}
}

// RegisterService registers a service implementation
func (r *ServiceRegistry) RegisterService(name string, service interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[name]; exists {
		return ErrServiceAlreadyExists
	}

	r.services[name] = service
	return nil
}

// GetService retrieves a service by name
func (r *ServiceRegistry) GetService(name string) (interface{}, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services[name]
	if !exists {
		return nil, ErrServiceNotFound
	}

	return service, nil
}

// GetAlertService returns the alert service
func (r *ServiceRegistry) GetAlertService() interfaces.AlertService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["alert"]
	if !exists {
		return nil
	}

	alertService, ok := service.(interfaces.AlertService)
	if !ok {
		return nil
	}

	return alertService
}

// GetCorrelationService returns the correlation service
func (r *ServiceRegistry) GetCorrelationService() interfaces.CorrelationService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["correlation"]
	if !exists {
		return nil
	}

	correlationService, ok := service.(interfaces.CorrelationService)
	if !ok {
		return nil
	}

	return correlationService
}

// GetAIService returns the AI service
func (r *ServiceRegistry) GetAIService() interfaces.AIService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["ai"]
	if !exists {
		return nil
	}

	aiService, ok := service.(interfaces.AIService)
	if !ok {
		return nil
	}

	return aiService
}

// GetRBACService returns the RBAC service
func (r *ServiceRegistry) GetRBACService() interfaces.RBACService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["rbac"]
	if !exists {
		return nil
	}

	rbacService, ok := service.(interfaces.RBACService)
	if !ok {
		return nil
	}

	return rbacService
}

// GetNotificationService returns the notification service
func (r *ServiceRegistry) GetNotificationService() interfaces.NotificationService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["notification"]
	if !exists {
		return nil
	}

	notificationService, ok := service.(interfaces.NotificationService)
	if !ok {
		return nil
	}

	return notificationService
}

// GetIncidentService returns the incident service
func (r *ServiceRegistry) GetIncidentService() interfaces.IncidentService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["incident"]
	if !exists {
		return nil
	}

	incidentService, ok := service.(interfaces.IncidentService)
	if !ok {
		return nil
	}

	return incidentService
}

// GetTopologyService returns the topology service
func (r *ServiceRegistry) GetTopologyService() interfaces.TopologyService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["topology"]
	if !exists {
		return nil
	}

	topologyService, ok := service.(interfaces.TopologyService)
	if !ok {
		return nil
	}

	return topologyService
}

// GetWorkflowService returns the workflow service
func (r *ServiceRegistry) GetWorkflowService() interfaces.WorkflowService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services["workflow"]
	if !exists {
		return nil
	}

	workflowService, ok := service.(interfaces.WorkflowService)
	if !ok {
		return nil
	}

	return workflowService
}

// IsServiceRegistered checks if a service is registered
func (r *ServiceRegistry) IsServiceRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.services[name]
	return exists
}

// UnregisterService removes a service from the registry
func (r *ServiceRegistry) UnregisterService(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[name]; !exists {
		return ErrServiceNotFound
	}

	delete(r.services, name)
	return nil
}

// ListServices returns all registered service names
func (r *ServiceRegistry) ListServices() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	services := make([]string, 0, len(r.services))
	for name := range r.services {
		services = append(services, name)
	}

	return services
}

// GetDatabase returns the database connection
func (r *ServiceRegistry) GetDatabase() interface{} {
	return r.db
}

// GetDatabaseConnection returns the typed database connection
func (r *ServiceRegistry) GetDatabaseConnection() *sql.DB {
	return r.db
}

// HealthCheck performs a health check on the registry and database
func (r *ServiceRegistry) HealthCheck() error {
	// Basic health check - ensure database is available
	if r.db != nil {
		return r.db.Ping()
	}
	return nil
}

// HealthCheckServices performs a health check on all registered services
func (r *ServiceRegistry) HealthCheckServices() map[string]error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make(map[string]error)

	for name := range r.services {
		// For now, just mark all services as healthy
		// In the future, services could implement a HealthChecker interface
		results[name] = nil
	}

	return results
}

// Implement basic ServiceRegistry interface methods
func (r *ServiceRegistry) Register(serviceName, address string, tags []string) error {
	// For in-memory registry, we'll just store the service info
	r.mu.Lock()
	defer r.mu.Unlock()

	serviceInfo := map[string]interface{}{
		"address": address,
		"tags":    tags,
	}
	r.services[serviceName] = serviceInfo
	return nil
}

func (r *ServiceRegistry) Deregister(serviceName, address string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.services, serviceName)
	return nil
}

func (r *ServiceRegistry) Discover(serviceName string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services[serviceName]
	if !exists {
		return nil, ErrServiceNotFound
	}

	// For in-memory registry, return the address if available
	if serviceMap, ok := service.(map[string]interface{}); ok {
		if address, ok := serviceMap["address"].(string); ok {
			return []string{address}, nil
		}
	}

	return []string{}, nil
}

func (r *ServiceRegistry) Watch(serviceName string, callback func([]string)) error {
	// For in-memory registry, this is a no-op
	// In a real implementation, this would watch for service changes
	return nil
}
