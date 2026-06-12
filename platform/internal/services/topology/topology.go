package topology

import (
	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

var (
	ErrServiceNotFound    = errors.New("service not found")
	ErrDependencyExists   = errors.New("dependency already exists")
	ErrCircularDependency = errors.New("circular dependency detected")
	ErrInvalidServiceType = errors.New("invalid service type")
)

// ServiceType represents different types of services
type ServiceType string

const (
	ServiceTypeApplication    ServiceType = "application"
	ServiceTypeDatabase       ServiceType = "database"
	ServiceTypeLoadBalancer   ServiceType = "load_balancer"
	ServiceTypeCache          ServiceType = "cache"
	ServiceTypeQueue          ServiceType = "queue"
	ServiceTypeStorage        ServiceType = "storage"
	ServiceTypeNetwork        ServiceType = "network"
	ServiceTypeInfrastructure ServiceType = "infrastructure"
	ServiceTypeAPI            ServiceType = "api"
	ServiceTypeMicroservice   ServiceType = "microservice"
)

// ServiceStatus represents the current status of a service
type ServiceStatus string

const (
	StatusHealthy     ServiceStatus = "healthy"
	StatusDegraded    ServiceStatus = "degraded"
	StatusDown        ServiceStatus = "down"
	StatusMaintenance ServiceStatus = "maintenance"
	StatusUnknown     ServiceStatus = "unknown"
)

// DependencyType represents the type of dependency relationship
type DependencyType string

const (
	DependencyHard     DependencyType = "hard"     // Service cannot function without dependency
	DependencySoft     DependencyType = "soft"     // Service can function with degraded performance
	DependencyOptional DependencyType = "optional" // Service can fully function without dependency
)

// TopologyService handles service topology and dependency mapping
type TopologyService struct {
	db *sql.DB
}

// NewTopologyService creates a new topology service
func NewTopologyService(db *sql.DB) *TopologyService {
	return &TopologyService{
		db: db,
	}
}

// Service represents a service in the topology
type Service struct {
	ID             uuid.UUID              `json:"id"`
	Name           string                 `json:"name"`
	DisplayName    string                 `json:"display_name"`
	Type           ServiceType            `json:"type"`
	Status         ServiceStatus          `json:"status"`
	Description    string                 `json:"description"`
	Version        string                 `json:"version"`
	Environment    string                 `json:"environment"`
	Owner          string                 `json:"owner"`
	OwnerEmail     string                 `json:"owner_email"`
	Repository     string                 `json:"repository,omitempty"`
	Documentation  string                 `json:"documentation,omitempty"`
	RunbookURL     string                 `json:"runbook_url,omitempty"`
	DashboardURL   string                 `json:"dashboard_url,omitempty"`
	Tags           []string               `json:"tags"`
	Labels         map[string]string      `json:"labels"`
	Metadata       map[string]interface{} `json:"metadata"`
	HealthEndpoint string                 `json:"health_endpoint,omitempty"`
	Dependencies   []ServiceDependency    `json:"dependencies,omitempty"`
	Dependents     []ServiceDependency    `json:"dependents,omitempty"`
	SLA            *ServiceSLA            `json:"sla,omitempty"`
	Metrics        *ServiceMetrics        `json:"metrics,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// ServiceDependency represents a dependency relationship between services
type ServiceDependency struct {
	ID              uuid.UUID      `json:"id"`
	FromServiceID   uuid.UUID      `json:"from_service_id"`
	ToServiceID     uuid.UUID      `json:"to_service_id"`
	FromServiceName string         `json:"from_service_name"`
	ToServiceName   string         `json:"to_service_name"`
	DependencyType  DependencyType `json:"dependency_type"`
	Description     string         `json:"description"`
	Protocol        string         `json:"protocol,omitempty"`
	Port            int            `json:"port,omitempty"`
	Endpoint        string         `json:"endpoint,omitempty"`
	HealthEndpoint  string         `json:"health_endpoint,omitempty"`
	RetryPolicy     *RetryPolicy   `json:"retry_policy,omitempty"`
	TimeoutSeconds  int            `json:"timeout_seconds"`
	CircuitBreaker  bool           `json:"circuit_breaker"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// ServiceSLA represents service level agreement
type ServiceSLA struct {
	Availability float64 `json:"availability"` // e.g., 99.9
	ResponseTime int     `json:"response_time_ms"`
	Throughput   int     `json:"throughput_rps"`
	ErrorRate    float64 `json:"error_rate_percent"`
}

// ServiceMetrics represents current service metrics
type ServiceMetrics struct {
	CPU          float64   `json:"cpu_percent"`
	Memory       float64   `json:"memory_percent"`
	Disk         float64   `json:"disk_percent"`
	Network      float64   `json:"network_mbps"`
	ResponseTime int       `json:"response_time_ms"`
	Throughput   int       `json:"throughput_rps"`
	ErrorRate    float64   `json:"error_rate_percent"`
	Availability float64   `json:"availability_percent"`
	LastUpdated  time.Time `json:"last_updated"`
}

// RetryPolicy represents retry configuration for dependencies
type RetryPolicy struct {
	MaxRetries  int           `json:"max_retries"`
	BackoffType string        `json:"backoff_type"` // "fixed", "exponential", "linear"
	Delay       time.Duration `json:"delay"`
}

// TopologyGraph represents the complete service topology
type TopologyGraph struct {
	Services     map[uuid.UUID]*Service           `json:"services"`
	Dependencies map[uuid.UUID]*ServiceDependency `json:"dependencies"`
	Layers       map[string][]uuid.UUID           `json:"layers"`
	Metadata     map[string]interface{}           `json:"metadata"`
	GeneratedAt  time.Time                        `json:"generated_at"`
}

// ImpactAnalysis represents the impact of a service being down
type ImpactAnalysis struct {
	ServiceID           uuid.UUID              `json:"service_id"`
	ServiceName         string                 `json:"service_name"`
	DirectlyAffected    []uuid.UUID            `json:"directly_affected"`
	IndirectlyAffected  []uuid.UUID            `json:"indirectly_affected"`
	CriticalPath        []uuid.UUID            `json:"critical_path"`
	EstimatedUsers      int                    `json:"estimated_affected_users"`
	EstimatedRevenue    float64                `json:"estimated_revenue_impact"`
	BusinessCriticality string                 `json:"business_criticality"`
	RecoveryTime        time.Duration          `json:"estimated_recovery_time"`
	Recommendations     []string               `json:"recommendations"`
	AlternativeServices []uuid.UUID            `json:"alternative_services"`
	Metadata            map[string]interface{} `json:"metadata"`
	AnalysisTime        time.Time              `json:"analysis_time"`
}

// CreateService creates a new service in the topology
func (ts *TopologyService) CreateService(ctx context.Context, service *Service) error {
	service.ID = uuid.New()
	service.CreatedAt = time.Now()
	service.UpdatedAt = time.Now()

	if service.Status == "" {
		service.Status = StatusUnknown
	}

	// Marshal JSON fields
	tagsJSON, _ := json.Marshal(service.Tags)
	labelsJSON, _ := json.Marshal(service.Labels)
	metadataJSON, _ := json.Marshal(service.Metadata)
	slaJSON, _ := json.Marshal(service.SLA)

	query := `
		INSERT INTO services (
			id, name, display_name, type, status, description, version, environment,
			owner, owner_email, repository, documentation, runbook_url, dashboard_url,
			tags, labels, metadata, health_endpoint, sla, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`

	_, err := ts.db.ExecContext(ctx, query,
		service.ID, service.Name, service.DisplayName, service.Type, service.Status,
		service.Description, service.Version, service.Environment, service.Owner, service.OwnerEmail,
		service.Repository, service.Documentation, service.RunbookURL, service.DashboardURL,
		tagsJSON, labelsJSON, metadataJSON, service.HealthEndpoint, slaJSON,
		service.CreatedAt, service.UpdatedAt,
	)

	return err
}

// GetService retrieves a service by ID
func (ts *TopologyService) GetService(ctx context.Context, serviceID uuid.UUID) (*Service, error) {
	service := &Service{}

	query := `
		SELECT id, name, display_name, type, status, description, version, environment,
		       owner, owner_email, repository, documentation, runbook_url, dashboard_url,
		       tags, labels, metadata, health_endpoint, sla, created_at, updated_at
		FROM services
		WHERE id = $1
	`

	var tagsJSON, labelsJSON, metadataJSON, slaJSON []byte

	err := ts.db.QueryRowContext(ctx, query, serviceID).Scan(
		&service.ID, &service.Name, &service.DisplayName, &service.Type, &service.Status,
		&service.Description, &service.Version, &service.Environment, &service.Owner, &service.OwnerEmail,
		&service.Repository, &service.Documentation, &service.RunbookURL, &service.DashboardURL,
		&tagsJSON, &labelsJSON, &metadataJSON, &service.HealthEndpoint, &slaJSON,
		&service.CreatedAt, &service.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrServiceNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	json.Unmarshal(tagsJSON, &service.Tags)
	json.Unmarshal(labelsJSON, &service.Labels)
	json.Unmarshal(metadataJSON, &service.Metadata)
	if slaJSON != nil {
		json.Unmarshal(slaJSON, &service.SLA)
	}

	// Load dependencies
	service.Dependencies, _ = ts.GetServiceDependencies(ctx, serviceID)
	service.Dependents, _ = ts.GetServiceDependents(ctx, serviceID)

	return service, nil
}

// CreateDependency creates a dependency relationship between services
func (ts *TopologyService) CreateDependency(ctx context.Context, dep *ServiceDependency) error {
	// Check for circular dependencies
	if err := ts.checkCircularDependency(ctx, dep.FromServiceID, dep.ToServiceID); err != nil {
		return err
	}

	dep.ID = uuid.New()
	dep.CreatedAt = time.Now()
	dep.UpdatedAt = time.Now()

	retryPolicyJSON, _ := json.Marshal(dep.RetryPolicy)

	query := `
		INSERT INTO service_dependencies (
			id, from_service_id, to_service_id, dependency_type, description,
			protocol, port, endpoint, health_endpoint, retry_policy, 
			timeout_seconds, circuit_breaker, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	_, err := ts.db.ExecContext(ctx, query,
		dep.ID, dep.FromServiceID, dep.ToServiceID, dep.DependencyType, dep.Description,
		dep.Protocol, dep.Port, dep.Endpoint, dep.HealthEndpoint, retryPolicyJSON,
		dep.TimeoutSeconds, dep.CircuitBreaker, dep.CreatedAt, dep.UpdatedAt,
	)

	return err
}

// GetServiceDependencies retrieves all dependencies for a service
func (ts *TopologyService) GetServiceDependencies(ctx context.Context, serviceID uuid.UUID) ([]ServiceDependency, error) {
	query := `
		SELECT sd.id, sd.from_service_id, sd.to_service_id, sd.dependency_type, sd.description,
		       sd.protocol, sd.port, sd.endpoint, sd.health_endpoint, sd.retry_policy,
		       sd.timeout_seconds, sd.circuit_breaker, sd.created_at, sd.updated_at,
		       s1.name as from_service_name, s2.name as to_service_name
		FROM service_dependencies sd
		JOIN services s1 ON sd.from_service_id = s1.id
		JOIN services s2 ON sd.to_service_id = s2.id
		WHERE sd.from_service_id = $1
	`

	rows, err := ts.db.QueryContext(ctx, query, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dependencies []ServiceDependency
	for rows.Next() {
		var dep ServiceDependency
		var retryPolicyJSON []byte

		err := rows.Scan(
			&dep.ID, &dep.FromServiceID, &dep.ToServiceID, &dep.DependencyType, &dep.Description,
			&dep.Protocol, &dep.Port, &dep.Endpoint, &dep.HealthEndpoint, &retryPolicyJSON,
			&dep.TimeoutSeconds, &dep.CircuitBreaker, &dep.CreatedAt, &dep.UpdatedAt,
			&dep.FromServiceName, &dep.ToServiceName,
		)
		if err != nil {
			continue
		}

		if retryPolicyJSON != nil {
			json.Unmarshal(retryPolicyJSON, &dep.RetryPolicy)
		}

		dependencies = append(dependencies, dep)
	}

	return dependencies, nil
}

// GetServiceDependents retrieves all services that depend on this service
func (ts *TopologyService) GetServiceDependents(ctx context.Context, serviceID uuid.UUID) ([]ServiceDependency, error) {
	query := `
		SELECT sd.id, sd.from_service_id, sd.to_service_id, sd.dependency_type, sd.description,
		       sd.protocol, sd.port, sd.endpoint, sd.health_endpoint, sd.retry_policy,
		       sd.timeout_seconds, sd.circuit_breaker, sd.created_at, sd.updated_at,
		       s1.name as from_service_name, s2.name as to_service_name
		FROM service_dependencies sd
		JOIN services s1 ON sd.from_service_id = s1.id
		JOIN services s2 ON sd.to_service_id = s2.id
		WHERE sd.to_service_id = $1
	`

	rows, err := ts.db.QueryContext(ctx, query, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dependents []ServiceDependency
	for rows.Next() {
		var dep ServiceDependency
		var retryPolicyJSON []byte

		err := rows.Scan(
			&dep.ID, &dep.FromServiceID, &dep.ToServiceID, &dep.DependencyType, &dep.Description,
			&dep.Protocol, &dep.Port, &dep.Endpoint, &dep.HealthEndpoint, &retryPolicyJSON,
			&dep.TimeoutSeconds, &dep.CircuitBreaker, &dep.CreatedAt, &dep.UpdatedAt,
			&dep.FromServiceName, &dep.ToServiceName,
		)
		if err != nil {
			continue
		}

		if retryPolicyJSON != nil {
			json.Unmarshal(retryPolicyJSON, &dep.RetryPolicy)
		}

		dependents = append(dependents, dep)
	}

	return dependents, nil
}

// GenerateTopologyGraph generates a complete topology graph
func (ts *TopologyService) GenerateTopologyGraph(ctx context.Context) (*TopologyGraph, error) {
	// Get all services
	services, err := ts.getAllServices(ctx)
	if err != nil {
		return nil, err
	}

	// Get all dependencies
	dependencies, err := ts.getAllDependencies(ctx)
	if err != nil {
		return nil, err
	}

	// Build graph
	graph := &TopologyGraph{
		Services:     make(map[uuid.UUID]*Service),
		Dependencies: make(map[uuid.UUID]*ServiceDependency),
		Layers:       make(map[string][]uuid.UUID),
		Metadata:     make(map[string]interface{}),
		GeneratedAt:  time.Now(),
	}

	// Populate services
	for _, service := range services {
		graph.Services[service.ID] = service
	}

	// Populate dependencies
	for _, dep := range dependencies {
		graph.Dependencies[dep.ID] = dep
	}

	// Generate layers based on service types and dependencies
	graph.Layers = ts.generateLayers(services, dependencies)

	// Add metadata
	graph.Metadata["total_services"] = len(services)
	graph.Metadata["total_dependencies"] = len(dependencies)
	graph.Metadata["service_types"] = ts.getServiceTypeStats(services)

	return graph, nil
}

// AnalyzeImpact analyzes the impact of a service being down
func (ts *TopologyService) AnalyzeImpact(ctx context.Context, serviceID uuid.UUID) (*ImpactAnalysis, error) {
	service, err := ts.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	analysis := &ImpactAnalysis{
		ServiceID:           serviceID,
		ServiceName:         service.Name,
		DirectlyAffected:    make([]uuid.UUID, 0),
		IndirectlyAffected:  make([]uuid.UUID, 0),
		CriticalPath:        make([]uuid.UUID, 0),
		AlternativeServices: make([]uuid.UUID, 0),
		Recommendations:     make([]string, 0),
		Metadata:            make(map[string]interface{}),
		AnalysisTime:        time.Now(),
	}

	// Find directly affected services (services that depend on this one)
	dependents, _ := ts.GetServiceDependents(ctx, serviceID)
	for _, dep := range dependents {
		analysis.DirectlyAffected = append(analysis.DirectlyAffected, dep.FromServiceID)

		if dep.DependencyType == DependencyHard {
			analysis.CriticalPath = append(analysis.CriticalPath, dep.FromServiceID)
		}
	}

	// Find indirectly affected services (cascade effect)
	visited := make(map[uuid.UUID]bool)
	ts.findCascadeImpact(ctx, analysis.DirectlyAffected, &analysis.IndirectlyAffected, visited)

	// Estimate business impact
	analysis.EstimatedUsers = ts.estimateAffectedUsers(service, analysis.DirectlyAffected)
	analysis.EstimatedRevenue = ts.estimateRevenueImpact(service, analysis.DirectlyAffected)
	analysis.BusinessCriticality = ts.assessBusinessCriticality(service)
	analysis.RecoveryTime = ts.estimateRecoveryTime(service)

	// Generate recommendations
	analysis.Recommendations = ts.generateImpactRecommendations(service, analysis)

	return analysis, nil
}

// Helper methods

func (ts *TopologyService) getAllServices(ctx context.Context) ([]*Service, error) {
	query := `
		SELECT id, name, display_name, type, status, description, version, environment,
		       owner, owner_email, repository, documentation, runbook_url, dashboard_url,
		       tags, labels, metadata, health_endpoint, sla, created_at, updated_at
		FROM services
		ORDER BY name
	`

	rows, err := ts.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []*Service
	for rows.Next() {
		service := &Service{}
		var tagsJSON, labelsJSON, metadataJSON, slaJSON []byte

		err := rows.Scan(
			&service.ID, &service.Name, &service.DisplayName, &service.Type, &service.Status,
			&service.Description, &service.Version, &service.Environment, &service.Owner, &service.OwnerEmail,
			&service.Repository, &service.Documentation, &service.RunbookURL, &service.DashboardURL,
			&tagsJSON, &labelsJSON, &metadataJSON, &service.HealthEndpoint, &slaJSON,
			&service.CreatedAt, &service.UpdatedAt,
		)
		if err != nil {
			continue
		}

		// Unmarshal JSON fields
		json.Unmarshal(tagsJSON, &service.Tags)
		json.Unmarshal(labelsJSON, &service.Labels)
		json.Unmarshal(metadataJSON, &service.Metadata)
		if slaJSON != nil {
			json.Unmarshal(slaJSON, &service.SLA)
		}

		services = append(services, service)
	}

	return services, nil
}

func (ts *TopologyService) getAllDependencies(ctx context.Context) ([]*ServiceDependency, error) {
	query := `
		SELECT sd.id, sd.from_service_id, sd.to_service_id, sd.dependency_type, sd.description,
		       sd.protocol, sd.port, sd.endpoint, sd.health_endpoint, sd.retry_policy,
		       sd.timeout_seconds, sd.circuit_breaker, sd.created_at, sd.updated_at,
		       s1.name as from_service_name, s2.name as to_service_name
		FROM service_dependencies sd
		JOIN services s1 ON sd.from_service_id = s1.id
		JOIN services s2 ON sd.to_service_id = s2.id
	`

	rows, err := ts.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dependencies []*ServiceDependency
	for rows.Next() {
		dep := &ServiceDependency{}
		var retryPolicyJSON []byte

		err := rows.Scan(
			&dep.ID, &dep.FromServiceID, &dep.ToServiceID, &dep.DependencyType, &dep.Description,
			&dep.Protocol, &dep.Port, &dep.Endpoint, &dep.HealthEndpoint, &retryPolicyJSON,
			&dep.TimeoutSeconds, &dep.CircuitBreaker, &dep.CreatedAt, &dep.UpdatedAt,
			&dep.FromServiceName, &dep.ToServiceName,
		)
		if err != nil {
			continue
		}

		if retryPolicyJSON != nil {
			json.Unmarshal(retryPolicyJSON, &dep.RetryPolicy)
		}

		dependencies = append(dependencies, dep)
	}

	return dependencies, nil
}

func (ts *TopologyService) checkCircularDependency(ctx context.Context, fromService, toService uuid.UUID) error {
	// Simple circular dependency check - in production, use more sophisticated graph algorithms
	visited := make(map[uuid.UUID]bool)
	return ts.dfsCircularCheck(ctx, toService, fromService, visited)
}

func (ts *TopologyService) dfsCircularCheck(ctx context.Context, current, target uuid.UUID, visited map[uuid.UUID]bool) error {
	if current == target {
		return ErrCircularDependency
	}

	if visited[current] {
		return nil
	}

	visited[current] = true

	// Get dependencies of current service
	dependencies, err := ts.GetServiceDependencies(ctx, current)
	if err != nil {
		return err
	}

	for _, dep := range dependencies {
		if err := ts.dfsCircularCheck(ctx, dep.ToServiceID, target, visited); err != nil {
			return err
		}
	}

	return nil
}

func (ts *TopologyService) generateLayers(services []*Service, _ []*ServiceDependency) map[string][]uuid.UUID {
	layers := make(map[string][]uuid.UUID)

	// Group by service type
	for _, service := range services {
		layerName := string(service.Type)
		if layers[layerName] == nil {
			layers[layerName] = make([]uuid.UUID, 0)
		}
		layers[layerName] = append(layers[layerName], service.ID)
	}

	return layers
}

func (ts *TopologyService) getServiceTypeStats(services []*Service) map[string]int {
	stats := make(map[string]int)
	for _, service := range services {
		stats[string(service.Type)]++
	}
	return stats
}

func (ts *TopologyService) findCascadeImpact(ctx context.Context, directlyAffected []uuid.UUID, indirectlyAffected *[]uuid.UUID, visited map[uuid.UUID]bool) {
	for _, serviceID := range directlyAffected {
		if visited[serviceID] {
			continue
		}
		visited[serviceID] = true

		// Get services that depend on this affected service
		dependents, _ := ts.GetServiceDependents(ctx, serviceID)
		for _, dep := range dependents {
			if !visited[dep.FromServiceID] {
				*indirectlyAffected = append(*indirectlyAffected, dep.FromServiceID)
				// Recursively find more affected services
				ts.findCascadeImpact(ctx, []uuid.UUID{dep.FromServiceID}, indirectlyAffected, visited)
			}
		}
	}
}

func (ts *TopologyService) estimateAffectedUsers(service *Service, affected []uuid.UUID) int {
	// Simple estimation - in production, this would be more sophisticated
	baseUsers := 1000
	if service.Type == ServiceTypeApplication || service.Type == ServiceTypeAPI {
		baseUsers = 10000
	}

	// Add impact from affected services
	return baseUsers + (len(affected) * 500)
}

func (ts *TopologyService) estimateRevenueImpact(service *Service, affected []uuid.UUID) float64 {
	// Simple estimation - in production, this would use actual business metrics
	baseImpact := 1000.0
	if service.Type == ServiceTypeApplication {
		baseImpact = 10000.0
	}

	return baseImpact + (float64(len(affected)) * 500.0)
}

func (ts *TopologyService) assessBusinessCriticality(service *Service) string {
	// Assess based on service type and metadata
	if service.Type == ServiceTypeApplication || service.Type == ServiceTypeAPI {
		return "high"
	}
	if service.Type == ServiceTypeDatabase || service.Type == ServiceTypeLoadBalancer {
		return "critical"
	}
	return "medium"
}

func (ts *TopologyService) estimateRecoveryTime(service *Service) time.Duration {
	// Estimate based on service type and configuration
	switch service.Type {
	case ServiceTypeApplication:
		return 15 * time.Minute
	case ServiceTypeDatabase:
		return 30 * time.Minute
	case ServiceTypeInfrastructure:
		return 45 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func (ts *TopologyService) generateImpactRecommendations(service *Service, analysis *ImpactAnalysis) []string {
	recommendations := []string{
		fmt.Sprintf("Prioritize recovery of %s due to %s business criticality", service.Name, analysis.BusinessCriticality),
	}

	if len(analysis.DirectlyAffected) > 0 {
		recommendations = append(recommendations, fmt.Sprintf("Monitor %d directly affected services for cascading failures", len(analysis.DirectlyAffected)))
	}

	if len(analysis.CriticalPath) > 0 {
		recommendations = append(recommendations, "Focus on services in critical dependency path first")
	}

	if service.RunbookURL != "" {
		recommendations = append(recommendations, fmt.Sprintf("Follow recovery procedures: %s", service.RunbookURL))
	}

	return recommendations
}

// UpdateServiceStatus updates the status of a service
func (ts *TopologyService) UpdateServiceStatus(ctx context.Context, serviceID uuid.UUID, status ServiceStatus, reason string) error {
	query := `
		UPDATE services 
		SET status = $1, updated_at = $2
		WHERE id = $3
	`

	_, err := ts.db.ExecContext(ctx, query, status, time.Now(), serviceID)
	if err != nil {
		return err
	}

	// Log status change
	ts.logStatusChange(ctx, serviceID, status, reason)

	return nil
}

func (ts *TopologyService) logStatusChange(ctx context.Context, serviceID uuid.UUID, status ServiceStatus, reason string) {
	query := `
		INSERT INTO service_status_history (id, service_id, status, reason, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	ts.db.ExecContext(ctx, query, uuid.New(), serviceID, status, reason, time.Now())
}

// DiscoverServices automatically discovers services from various sources
func (ts *TopologyService) DiscoverServices(ctx context.Context, discoveryConfig *DiscoveryConfig) ([]*Service, error) {
	var discoveredServices []*Service

	// Kubernetes discovery
	if discoveryConfig.Kubernetes.Enabled {
		k8sServices, err := ts.discoverKubernetesServices(ctx, &discoveryConfig.Kubernetes)
		if err == nil {
			discoveredServices = append(discoveredServices, k8sServices...)
		}
	}

	// Docker discovery
	if discoveryConfig.Docker.Enabled {
		dockerServices, err := ts.discoverDockerServices(ctx, &discoveryConfig.Docker)
		if err == nil {
			discoveredServices = append(discoveredServices, dockerServices...)
		}
	}

	// DNS discovery
	if discoveryConfig.DNS.Enabled {
		dnsServices, err := ts.discoverDNSServices(ctx, &discoveryConfig.DNS)
		if err == nil {
			discoveredServices = append(discoveredServices, dnsServices...)
		}
	}

	return discoveredServices, nil
}

// DiscoveryConfig represents service discovery configuration
type DiscoveryConfig struct {
	Kubernetes KubernetesDiscovery `json:"kubernetes"`
	Docker     DockerDiscovery     `json:"docker"`
	DNS        DNSDiscovery        `json:"dns"`
	Cloud      CloudDiscovery      `json:"cloud"`
}

type KubernetesDiscovery struct {
	Enabled       bool     `json:"enabled"`
	Kubeconfig    string   `json:"kubeconfig"`
	Namespaces    []string `json:"namespaces"`
	LabelSelector string   `json:"label_selector"`
}

type DockerDiscovery struct {
	Enabled      bool     `json:"enabled"`
	DockerSocket string   `json:"docker_socket"`
	Networks     []string `json:"networks"`
	LabelFilter  string   `json:"label_filter"`
}

type DNSDiscovery struct {
	Enabled bool     `json:"enabled"`
	Zones   []string `json:"zones"`
	Servers []string `json:"servers"`
}

type CloudDiscovery struct {
	Enabled   bool              `json:"enabled"`
	Providers map[string]string `json:"providers"` // provider -> config
}

// Placeholder discovery methods (would need full implementation)
func (ts *TopologyService) discoverKubernetesServices(_ context.Context, _ *KubernetesDiscovery) ([]*Service, error) {
	// Would integrate with Kubernetes API
	return []*Service{}, nil
}

func (ts *TopologyService) discoverDockerServices(_ context.Context, _ *DockerDiscovery) ([]*Service, error) {
	// Would integrate with Docker API
	return []*Service{}, nil
}

func (ts *TopologyService) discoverDNSServices(_ context.Context, _ *DNSDiscovery) ([]*Service, error) {
	// Would perform DNS discovery
	return []*Service{}, nil
}

// Interface implementation methods for interfaces.TopologyService

// GetServiceDependenciesByName returns service dependency names (interface method)
func (ts *TopologyService) GetServiceDependenciesByName(ctx context.Context, serviceName string) ([]string, error) {
	// Get service ID by name first
	var serviceID uuid.UUID
	err := ts.db.QueryRowContext(ctx, "SELECT id FROM services WHERE name = $1", serviceName).Scan(&serviceID)
	if err != nil {
		if err == sql.ErrNoRows {
			return []string{}, nil // Service not found, return empty dependencies
		}
		return nil, err
	}

	// Get dependencies using existing method
	dependencies, err := ts.GetServiceDependencies(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	// Extract dependency names
	var dependencyNames []string
	for _, dep := range dependencies {
		dependencyNames = append(dependencyNames, dep.ToServiceName)
	}

	return dependencyNames, nil
}

// GetInfrastructureMapping returns infrastructure topology for an entity
func (ts *TopologyService) GetInfrastructureMapping(ctx context.Context, entityID string) (*interfaces.InfrastructureMapping, error) {
	// For now, try to find a service with this entity ID
	// In production, this would integrate with Dynatrace Smartscape or K8s API

	var serviceID uuid.UUID
	var serviceName, serviceType string
	query := `
		SELECT id, name, type FROM services
		WHERE id::text = $1 OR name = $1
		LIMIT 1
	`

	err := ts.db.QueryRowContext(ctx, query, entityID).Scan(&serviceID, &serviceName, &serviceType)
	if err != nil {
		if err == sql.ErrNoRows {
			// Return empty mapping for unknown entities
			return &interfaces.InfrastructureMapping{
				EntityID:     entityID,
				EntityType:   "unknown",
				Dependencies: []string{},
				Metadata:     make(map[string]string),
			}, nil
		}
		return nil, err
	}

	// Get dependencies for this service using the UUID method
	serviceDependencies, err := ts.GetServiceDependencies(ctx, serviceID)
	if err != nil {
		serviceDependencies = []ServiceDependency{} // Continue with empty dependencies on error
	}

	// Convert ServiceDependency slice to string slice
	var dependencyNames []string
	for _, dep := range serviceDependencies {
		dependencyNames = append(dependencyNames, dep.ToServiceName)
	}

	return &interfaces.InfrastructureMapping{
		EntityID:     entityID,
		EntityType:   serviceType,
		Dependencies: dependencyNames,
		Metadata: map[string]string{
			"service_name":  serviceName,
			"discovered_at": time.Now().Format(time.RFC3339),
		},
	}, nil
}

// DiscoverTopology performs topology discovery from a specific source
func (ts *TopologyService) DiscoverTopology(ctx context.Context, source string) error {
	log.Printf("Starting topology discovery from source: %s", source)

	switch source {
	case "kubernetes", "k8s":
		return ts.discoverFromKubernetes(ctx)
	case "dynatrace":
		return ts.discoverFromDynatrace(ctx)
	case "prometheus":
		return ts.discoverFromPrometheus(ctx)
	case "docker":
		return ts.discoverFromDocker(ctx)
	default:
		return fmt.Errorf("unsupported discovery source: %s", source)
	}
}

// Discovery implementation methods
func (ts *TopologyService) discoverFromKubernetes(ctx context.Context) error {
	// This would integrate with Kubernetes API to discover:
	// - Services, Pods, Nodes
	// - Service dependencies via labels/annotations
	// - Resource relationships

	log.Println("Kubernetes topology discovery - placeholder implementation")

	// For now, create a sample K8s service
	sampleService := &Service{
		Name:        "kubernetes-api-server",
		DisplayName: "Kubernetes API Server",
		Type:        ServiceTypeInfrastructure,
		Status:      StatusHealthy,
		Description: "Auto-discovered Kubernetes API server",
		Environment: "production",
		Owner:       "platform-team",
		Tags:        []string{"kubernetes", "infrastructure", "auto-discovered"},
		Labels: map[string]string{
			"discovery_source": "kubernetes",
			"component":        "api-server",
		},
		Metadata: map[string]interface{}{
			"auto_discovered": true,
			"discovery_time":  time.Now().Format(time.RFC3339),
			"source":          "kubernetes",
		},
	}

	return ts.CreateService(ctx, sampleService)
}

func (ts *TopologyService) discoverFromDynatrace(ctx context.Context) error {
	// This would integrate with Dynatrace Smartscape API to discover:
	// - Applications, Services, Processes
	// - Infrastructure (Hosts, Process Groups)
	// - Dependencies via Smartscape topology

	log.Println("Dynatrace topology discovery - placeholder implementation")

	// Sample Dynatrace service
	sampleService := &Service{
		Name:        "payment-service",
		DisplayName: "Payment Processing Service",
		Type:        ServiceTypeApplication,
		Status:      StatusHealthy,
		Description: "Auto-discovered from Dynatrace",
		Environment: "production",
		Owner:       "payments-team",
		Tags:        []string{"dynatrace", "payment", "critical", "auto-discovered"},
		Labels: map[string]string{
			"discovery_source": "dynatrace",
			"entity_type":      "service",
		},
		Metadata: map[string]interface{}{
			"auto_discovered": true,
			"discovery_time":  time.Now().Format(time.RFC3339),
			"source":          "dynatrace",
		},
	}

	return ts.CreateService(ctx, sampleService)
}

func (ts *TopologyService) discoverFromPrometheus(ctx context.Context) error {
	// This would integrate with Prometheus service discovery to find:
	// - Monitoring targets
	// - Service instances
	// - Labels and annotations

	log.Println("Prometheus topology discovery - placeholder implementation")
	return nil
}

func (ts *TopologyService) discoverFromDocker(ctx context.Context) error {
	// This would integrate with Docker API to discover:
	// - Running containers
	// - Docker networks
	// - Container dependencies

	log.Println("Docker topology discovery - placeholder implementation")
	return nil
}

// HealthCheck implements the interfaces.TopologyService HealthCheck method
func (ts *TopologyService) HealthCheck() error {
	// Test database connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := ts.db.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	// Test basic query
	var count int
	err = ts.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM services").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to query services table: %w", err)
	}

	return nil
}

// Name returns the service name for health checking
func (ts *TopologyService) Name() string {
	return "TopologyService"
}

// UpdateTopologyRelationship updates or creates a topology relationship
func (ts *TopologyService) UpdateTopologyRelationship(ctx context.Context, rel *interfaces.TopologyRelationship) error {
	// Convert interface type to internal type and create/update dependency
	dep := &ServiceDependency{
		FromServiceID:  uuid.MustParse(rel.SourceID),
		ToServiceID:    uuid.MustParse(rel.TargetID),
		DependencyType: DependencyType(rel.Type),
		Description:    fmt.Sprintf("Topology relationship: %s", rel.Type),
		Protocol:       "http", // default
		TimeoutSeconds: 30,     // default
	}

	// Try to get service names
	var fromServiceName, toServiceName string
	ts.db.QueryRowContext(ctx, "SELECT name FROM services WHERE id = $1", dep.FromServiceID).Scan(&fromServiceName)
	ts.db.QueryRowContext(ctx, "SELECT name FROM services WHERE id = $1", dep.ToServiceID).Scan(&toServiceName)

	dep.FromServiceName = fromServiceName
	dep.ToServiceName = toServiceName

	// Check if relationship already exists
	var existingID uuid.UUID
	query := `SELECT id FROM service_dependencies WHERE from_service_id = $1 AND to_service_id = $2`
	err := ts.db.QueryRowContext(ctx, query, dep.FromServiceID, dep.ToServiceID).Scan(&existingID)

	if err == sql.ErrNoRows {
		// Create new relationship
		return ts.CreateDependency(ctx, dep)
	} else if err != nil {
		return err
	} else {
		// Update existing relationship
		updateQuery := `
			UPDATE service_dependencies
			SET dependency_type = $1, description = $2, updated_at = $3
			WHERE id = $4
		`
		_, err = ts.db.ExecContext(ctx, updateQuery, dep.DependencyType, dep.Description, time.Now(), existingID)
		return err
	}
}

// GetInfrastructureCorrelations returns infrastructure correlations for an entity
func (ts *TopologyService) GetInfrastructureCorrelations(ctx context.Context, entityID string) ([]*interfaces.InfrastructureCorrelation, error) {
	// Query infrastructure_correlations table created in the database migration
	query := `
		SELECT id, source_entity_id, target_entity_id, entity_type, correlation_type,
		       relationship_strength, dependency_direction, infrastructure_layer,
		       cluster_id, namespace_name, service_name, correlation_data,
		       discovered_method, confidence_score, is_active, metadata,
		       created_at, updated_at
		FROM infrastructure_correlations
		WHERE (source_entity_id = $1 OR target_entity_id = $1) AND is_active = true
		ORDER BY confidence_score DESC, created_at DESC
		LIMIT 50
	`

	rows, err := ts.db.QueryContext(ctx, query, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var correlations []*interfaces.InfrastructureCorrelation
	for rows.Next() {
		var corr interfaces.InfrastructureCorrelation
		var correlationDataJSON, metadataJSON []byte
		var clusterID, namespaceName, serviceName sql.NullString

		err := rows.Scan(
			&corr.ID, &corr.SourceEntityID, &corr.TargetEntityID, &corr.EntityType, &corr.CorrelationType,
			&corr.RelationshipStrength, &corr.DependencyDirection, &corr.InfrastructureLayer,
			&clusterID, &namespaceName, &serviceName, &correlationDataJSON,
			&corr.DiscoveredMethod, &corr.ConfidenceScore, &corr.IsActive, &metadataJSON,
			&corr.CreatedAt, &corr.UpdatedAt,
		)
		if err != nil {
			continue
		}

		// Handle nullable fields
		if clusterID.Valid {
			corr.ClusterID = clusterID.String
		}
		if namespaceName.Valid {
			corr.NamespaceName = namespaceName.String
		}
		if serviceName.Valid {
			corr.ServiceName = serviceName.String
		}

		// Unmarshal JSON fields
		if correlationDataJSON != nil {
			json.Unmarshal(correlationDataJSON, &corr.CorrelationData)
		}
		if metadataJSON != nil {
			json.Unmarshal(metadataJSON, &corr.Metadata)
		}

		correlations = append(correlations, &corr)
	}

	return correlations, rows.Err()
}

// GetServiceDependenciesByString is an adapter method for the interface that takes string instead of UUID
func (ts *TopologyService) GetServiceDependenciesByString(ctx context.Context, serviceID string) ([]interfaces.ServiceDependency, error) {
	// Try to parse as UUID first
	if id, err := uuid.Parse(serviceID); err == nil {
		deps, err := ts.GetServiceDependencies(ctx, id)
		if err != nil {
			return nil, err
		}

		// Convert to interface type
		var result []interfaces.ServiceDependency
		for _, dep := range deps {
			result = append(result, interfaces.ServiceDependency{
				ServiceID:        dep.ToServiceID.String(),
				ServiceName:      dep.ToServiceName,
				DependentService: dep.FromServiceName,
				DependencyType:   string(dep.DependencyType),
				Criticality:      ts.mapDependencyTypeToCriticality(dep.DependencyType),
				HealthStatus:     "unknown", // Would need to query service status
				LastChecked:      dep.UpdatedAt.Unix(),
			})
		}
		return result, nil
	}

	// Treat as service name and lookup ID
	var serviceUUID uuid.UUID
	err := ts.db.QueryRowContext(ctx, "SELECT id FROM services WHERE name = $1", serviceID).Scan(&serviceUUID)
	if err != nil {
		return nil, err
	}

	return ts.GetServiceDependenciesByString(ctx, serviceUUID.String())
}

// Helper method to map dependency type to criticality
func (ts *TopologyService) mapDependencyTypeToCriticality(depType DependencyType) string {
	switch depType {
	case DependencyHard:
		return "critical"
	case DependencySoft:
		return "medium"
	case DependencyOptional:
		return "low"
	default:
		return "unknown"
	}
}

// DiscoverTopologyWithResult implements the interface method that returns TopologyMap
func (ts *TopologyService) DiscoverTopologyWithResult(ctx context.Context, source string) (*interfaces.TopologyMap, error) {
	// First perform the discovery
	err := ts.DiscoverTopology(ctx, source)
	if err != nil {
		return nil, err
	}

	// Then generate and return the topology map
	graph, err := ts.GenerateTopologyGraph(ctx)
	if err != nil {
		return nil, err
	}

	// Convert TopologyGraph to interfaces.TopologyMap
	entities := make([]interfaces.TopologyEntity, 0, len(graph.Services))
	for _, service := range graph.Services {
		entity := interfaces.TopologyEntity{
			ID:   service.ID.String(),
			Type: string(service.Type),
			Name: service.Name,
			Properties: map[string]interface{}{
				"display_name": service.DisplayName,
				"environment":  service.Environment,
				"owner":        service.Owner,
				"version":      service.Version,
			},
			Status:      string(service.Status),
			HealthScore: ts.calculateHealthScore(service),
			Metadata:    service.Metadata,
		}
		entities = append(entities, entity)
	}

	relationships := make([]interfaces.TopologyRelationship, 0, len(graph.Dependencies))
	for _, dep := range graph.Dependencies {
		relationship := interfaces.TopologyRelationship{
			ID:       dep.ID.String(),
			SourceID: dep.FromServiceID.String(),
			TargetID: dep.ToServiceID.String(),
			Type:     string(dep.DependencyType),
			Properties: map[string]interface{}{
				"protocol":        dep.Protocol,
				"port":            dep.Port,
				"timeout_seconds": dep.TimeoutSeconds,
			},
			Strength:  ts.calculateRelationshipStrength(dep),
			Direction: "downstream", // from source to target
			Metadata: map[string]interface{}{
				"description":     dep.Description,
				"circuit_breaker": dep.CircuitBreaker,
			},
			CreatedAt: dep.CreatedAt.Unix(),
			UpdatedAt: dep.UpdatedAt.Unix(),
		}
		relationships = append(relationships, relationship)
	}

	return &interfaces.TopologyMap{
		ID:            uuid.New().String(),
		Source:        source,
		Entities:      entities,
		Relationships: relationships,
		Metadata:      graph.Metadata,
		LastUpdated:   graph.GeneratedAt.Unix(),
	}, nil
}

// Helper methods
func (ts *TopologyService) calculateHealthScore(service *Service) float64 {
	switch service.Status {
	case StatusHealthy:
		return 1.0
	case StatusDegraded:
		return 0.7
	case StatusDown:
		return 0.0
	case StatusMaintenance:
		return 0.5
	default:
		return 0.5
	}
}

func (ts *TopologyService) calculateRelationshipStrength(dep *ServiceDependency) float64 {
	switch dep.DependencyType {
	case DependencyHard:
		return 1.0
	case DependencySoft:
		return 0.7
	case DependencyOptional:
		return 0.3
	default:
		return 0.5
	}
}

// AnalyzeImpactForIncident analyzes impact when given an incident ID instead of service ID
func (ts *TopologyService) AnalyzeImpactForIncident(ctx context.Context, incidentID uuid.UUID) (*interfaces.ImpactAnalysis, error) {
	// Get incident details to find affected services
	var alertIDs []string
	var incidentTitle string

	query := `SELECT alert_ids, title FROM incidents WHERE id = $1`
	var alertIDsJSON []byte
	err := ts.db.QueryRowContext(ctx, query, incidentID).Scan(&alertIDsJSON, &incidentTitle)
	if err != nil {
		return nil, fmt.Errorf("failed to get incident details: %w", err)
	}

	json.Unmarshal(alertIDsJSON, &alertIDs)

	// Get services affected by alerts in the incident
	var affectedServices []uuid.UUID
	for _, alertIDStr := range alertIDs {
		alertID, err := uuid.Parse(alertIDStr)
		if err != nil {
			continue
		}

		// Get service info from alert metadata
		var metadataJSON []byte
		query := `SELECT metadata FROM alerts WHERE id = $1`
		err = ts.db.QueryRowContext(ctx, query, alertID).Scan(&metadataJSON)
		if err != nil {
			continue
		}

		var metadata map[string]interface{}
		json.Unmarshal(metadataJSON, &metadata)

		if serviceID, ok := metadata["service_id"].(string); ok {
			if id, err := uuid.Parse(serviceID); err == nil {
				affectedServices = append(affectedServices, id)
			}
		}
	}

	// If no services found from alerts, use a default approach
	if len(affectedServices) == 0 {
		// Create a mock impact analysis
		return &interfaces.ImpactAnalysis{
			IncidentID:         incidentID,
			AffectedServices:   []string{},
			Dependencies:       []interfaces.ServiceDependency{},
			BlastRadius:        []string{},
			ImpactScore:        0.5,
			EstimatedDowntime:  int64(30 * time.Minute),
			BusinessImpact:     "medium",
			RiskLevel:          "medium",
			RecommendedActions: []string{"Monitor situation", "Investigate root cause"},
			AnalyzedAt:         time.Now().Unix(),
			Metadata: map[string]interface{}{
				"incident_title":    incidentTitle,
				"analysis_method":   "basic",
				"services_analyzed": 0,
			},
		}, nil
	}

	// Analyze impact for the first affected service (primary)
	primaryServiceID := affectedServices[0]
	analysis, err := ts.AnalyzeImpact(ctx, primaryServiceID)
	if err != nil {
		return nil, err
	}

	// Convert to interface type
	var affectedServiceNames []string
	for _, serviceID := range analysis.DirectlyAffected {
		if service, err := ts.GetService(ctx, serviceID); err == nil {
			affectedServiceNames = append(affectedServiceNames, service.Name)
		}
	}

	var dependencies []interfaces.ServiceDependency
	if primaryService, err := ts.GetService(ctx, primaryServiceID); err == nil {
		for _, dep := range primaryService.Dependencies {
			dependencies = append(dependencies, interfaces.ServiceDependency{
				ServiceID:        dep.ToServiceID.String(),
				ServiceName:      dep.ToServiceName,
				DependentService: dep.FromServiceName,
				DependencyType:   string(dep.DependencyType),
				Criticality:      ts.mapDependencyTypeToCriticality(dep.DependencyType),
				HealthStatus:     "unknown",
				LastChecked:      dep.UpdatedAt.Unix(),
			})
		}
	}

	return &interfaces.ImpactAnalysis{
		IncidentID:         incidentID,
		AffectedServices:   affectedServiceNames,
		Dependencies:       dependencies,
		BlastRadius:        affectedServiceNames,
		ImpactScore:        float64(len(affectedServiceNames)) / 10.0,
		EstimatedDowntime:  int64(analysis.RecoveryTime),
		BusinessImpact:     analysis.BusinessCriticality,
		RiskLevel:          analysis.BusinessCriticality,
		RecommendedActions: analysis.Recommendations,
		AnalyzedAt:         analysis.AnalysisTime.Unix(),
		Metadata: map[string]interface{}{
			"primary_service":     analysis.ServiceName,
			"directly_affected":   len(analysis.DirectlyAffected),
			"indirectly_affected": len(analysis.IndirectlyAffected),
			"estimated_users":     analysis.EstimatedUsers,
			"estimated_revenue":   analysis.EstimatedRevenue,
		},
	}, nil
}
