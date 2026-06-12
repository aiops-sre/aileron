package interfaces

import (
	"context"

	"github.com/aileron-platform/aileron/platform/internal/shared/models"

	"github.com/google/uuid"
)

// CorrelationStrategy defines the interface for correlation algorithms
type CorrelationStrategy interface {
	Name() string
	Calculate(ctx context.Context, alert *models.Alert, incidents []models.Incident) (float64, error)
	Confidence() float64
	RequiredFields() []string
}

// CorrelationEngine defines the main correlation engine interface
type CorrelationEngine interface {
	Correlate(ctx context.Context, alert *models.Alert) (*models.CorrelationResult, error)
	AddStrategy(strategy CorrelationStrategy) error
	RemoveStrategy(name string) error
	SetThreshold(threshold float64)
	GetThreshold() float64
	GetStrategies() []string
	HealthCheck() error
}

// IncidentManager defines the interface for incident management
type IncidentManager interface {
	Create(ctx context.Context, incident *models.Incident) (*models.Incident, error)
	Update(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error
	Get(ctx context.Context, id uuid.UUID) (*models.Incident, error)
	List(ctx context.Context, filters map[string]interface{}) ([]*models.Incident, error)
	Delete(ctx context.Context, id uuid.UUID) error
	GetByAlert(ctx context.Context, alertID uuid.UUID) (*models.Incident, error)
	LinkAlert(ctx context.Context, incidentID, alertID uuid.UUID) error
	UnlinkAlert(ctx context.Context, incidentID, alertID uuid.UUID) error
	HealthCheck() error
}

// AlertManager defines the interface for alert management
type AlertManager interface {
	Create(ctx context.Context, alert *models.Alert) (*models.Alert, error)
	Update(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error
	Get(ctx context.Context, id uuid.UUID) (*models.Alert, error)
	GetRecent(ctx context.Context, limit int) ([]*models.Alert, error)
	List(ctx context.Context, filters map[string]interface{}) ([]*models.Alert, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Acknowledge(ctx context.Context, id uuid.UUID, userID uuid.UUID, notes string) error
	Resolve(ctx context.Context, id uuid.UUID, userID uuid.UUID, notes string) error
	HealthCheck() error
}

// TopologyService defines the interface for topology discovery and analysis
type TopologyService interface {
	DiscoverTopology(ctx context.Context, source string) (*models.TopologyMap, error)
	AnalyzeImpact(ctx context.Context, incidentID uuid.UUID) (*models.ImpactAnalysis, error)
	GetServiceDependencies(ctx context.Context, serviceID string) ([]models.ServiceDependency, error)
	UpdateTopologyRelationship(ctx context.Context, rel *models.TopologyRelationship) error
	GetInfrastructureCorrelations(ctx context.Context, entityID string) ([]*models.InfrastructureCorrelation, error)
	HealthCheck() error
}

// AIService defines the interface for AI-powered features
type AIService interface {
	AnalyzeAlert(ctx context.Context, request *AlertAnalysisRequest) (*AlertAnalysisResponse, error)
	AnalyzeAlertModel(ctx context.Context, alert *models.Alert) (*models.AIAnalysis, error)
	PredictIncidentSeverity(ctx context.Context, alert *models.Alert) (string, float64, error)
	GenerateRecommendations(ctx context.Context, incident *models.Incident) ([]string, error)
	TrainModel(ctx context.Context, modelType string, data interface{}) error
	HealthCheck() error
}

// NotificationService defines the interface for notifications
type NotificationService interface {
	SendAlert(ctx context.Context, alert *models.Alert, recipients []string) error
	SendIncidentUpdate(ctx context.Context, incident *models.Incident, message string) error
	SendEscalation(ctx context.Context, alert *models.Alert, level int) error
	ConfigureChannel(ctx context.Context, channel *models.NotificationChannel) error
	HealthCheck() error
}

// UserService defines the interface for user management
type UserService interface {
	GetUser(ctx context.Context, id uuid.UUID) (*models.User, error)
	GetUsersByTeam(ctx context.Context, teamID uuid.UUID) ([]*models.User, error)
	GetOnCallUsers(ctx context.Context, serviceID string) ([]*models.User, error)
	UpdateUserExpertise(ctx context.Context, userID uuid.UUID, expertise *models.UserExpertise) error
	HealthCheck() error
}

// DatabaseManager defines the interface for database operations
type DatabaseManager interface {
	GetConnection() interface{}
	HealthCheck() error
	BeginTransaction(ctx context.Context) (Transaction, error)
	ExecuteQuery(ctx context.Context, query string, args ...interface{}) error
	ExecuteQueryWithResult(ctx context.Context, query string, args ...interface{}) (interface{}, error)
}

// Transaction defines the interface for database transactions
type Transaction interface {
	Commit() error
	Rollback() error
	ExecContext(ctx context.Context, query string, args ...interface{}) error
}

// Cache defines the interface for caching operations
type Cache interface {
	Get(key string) (interface{}, bool)
	Set(key string, value interface{}, ttl int) error
	Delete(key string) error
	Clear() error
	HealthCheck() error
}

// MetricsCollector defines the interface for metrics collection
type MetricsCollector interface {
	IncrementCounter(name string, tags map[string]string) error
	RecordGauge(name string, value float64, tags map[string]string) error
	RecordTimer(name string, duration int64, tags map[string]string) error
	RecordHistogram(name string, value float64, tags map[string]string) error
	HealthCheck() error
}

// Logger defines the interface for structured logging
type Logger interface {
	Debug(msg string, fields ...map[string]interface{})
	Info(msg string, fields ...map[string]interface{})
	Warn(msg string, fields ...map[string]interface{})
	Error(msg string, err error, fields ...map[string]interface{})
	Fatal(msg string, err error, fields ...map[string]interface{})
	WithFields(fields map[string]interface{}) Logger
	WithContext(ctx context.Context) Logger
}

// HealthChecker defines a common interface for health checking
type HealthChecker interface {
	HealthCheck() error
	Name() string
}

// ServiceRegistry defines the interface for service discovery and registration
type ServiceRegistry interface {
	Register(serviceName, address string, tags []string) error
	Deregister(serviceName, address string) error
	Discover(serviceName string) ([]string, error)
	Watch(serviceName string, callback func([]string)) error
	HealthCheck() error
	// Service management methods
	RegisterService(name string, service interface{}) error
	GetService(name string) (interface{}, error)
	GetDatabase() interface{}
	GetRBACService() RBACService
	GetAIService() AIService
	GetAlertService() AlertService
	GetIncidentService() IncidentService
	GetCorrelationService() CorrelationService
	GetWorkflowService() WorkflowService
	GetTopologyService() TopologyService
	GetNotificationService() NotificationService
}

// ConfigManager defines the interface for configuration management
type ConfigManager interface {
	Get(key string) (interface{}, error)
	Set(key string, value interface{}) error
	GetString(key string) string
	GetInt(key string) int
	GetBool(key string) bool
	GetFloat64(key string) float64
	Watch(key string, callback func(interface{})) error
	HealthCheck() error
}

// RBACService defines the interface for role-based access control
type RBACService interface {
	ValidatePermission(ctx context.Context, userID uuid.UUID, permission string) (bool, error)
	CheckPermission(ctx context.Context, userID uuid.UUID, permission string) error
	GetUserRoles(ctx context.Context, userID uuid.UUID) ([]string, error)
	GetRolePermissions(ctx context.Context, role string) ([]string, error)
	AssignRole(ctx context.Context, userID uuid.UUID, role string) error
	RevokeRole(ctx context.Context, userID uuid.UUID, role string) error
	CreateRole(ctx context.Context, role *models.Role) error
	UpdateRole(ctx context.Context, roleID uuid.UUID, updates map[string]interface{}) error
	DeleteRole(ctx context.Context, roleID uuid.UUID) error
	HealthCheck() error
}

// CorrelationService defines the interface for alert correlation
type CorrelationService interface {
	Correlate(ctx context.Context, alert *models.Alert) (*CorrelationResult, error)
	CorrelateAlert(ctx context.Context, alert *models.Alert) (*CorrelationResult, error)
	GetCorrelationRules(ctx context.Context) ([]*models.CorrelationRule, error)
	CreateCorrelationRule(ctx context.Context, rule *models.CorrelationRule) error
	UpdateCorrelationRule(ctx context.Context, ruleID uuid.UUID, updates map[string]interface{}) error
	DeleteCorrelationRule(ctx context.Context, ruleID uuid.UUID) error
	AnalyzeCorrelation(ctx context.Context, alertID uuid.UUID) (*CorrelationResult, error)
	GetCorrelationResult(ctx context.Context, alertID uuid.UUID) (*CorrelationResult, error)
	HealthCheck() error
}

// AlertService defines the interface for alert management
type AlertService interface {
	Create(ctx context.Context, alert *models.Alert) (*models.Alert, error)
	Update(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error
	Get(ctx context.Context, id uuid.UUID) (*models.Alert, error)
	GetRecent(ctx context.Context, limit int) ([]*models.Alert, error)
	List(ctx context.Context, filters map[string]interface{}) ([]*models.Alert, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Acknowledge(ctx context.Context, id uuid.UUID, userID uuid.UUID, notes string) error
	Resolve(ctx context.Context, id uuid.UUID, userID uuid.UUID, notes string) error
	AnalyzeAlert(ctx context.Context, alertID uuid.UUID) (*AlertAnalysisResponse, error)
	CorrelateAlert(ctx context.Context, alertID uuid.UUID) (*CorrelationResult, error)
	GetAlertStats(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	HealthCheck() error
}

// IncidentService defines the interface for incident management
type IncidentService interface {
	Create(ctx context.Context, incident *models.Incident) (*models.Incident, error)
	Update(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error
	Get(ctx context.Context, id uuid.UUID) (*models.Incident, error)
	List(ctx context.Context, filters map[string]interface{}) ([]*models.Incident, error)
	Delete(ctx context.Context, id uuid.UUID) error
	GetByAlert(ctx context.Context, alertID uuid.UUID) (*models.Incident, error)
	LinkAlert(ctx context.Context, incidentID, alertID uuid.UUID) error
	UnlinkAlert(ctx context.Context, incidentID, alertID uuid.UUID) error
	AssignToUser(ctx context.Context, incidentID, userID uuid.UUID) error
	EscalateIncident(ctx context.Context, incidentID uuid.UUID, level int) error
	HealthCheck() error
}

// WorkflowService defines the interface for workflow management
type WorkflowService interface {
	Create(ctx context.Context, workflow *models.Workflow) (*models.Workflow, error)
	Update(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error
	Get(ctx context.Context, id uuid.UUID) (*models.Workflow, error)
	List(ctx context.Context, filters map[string]interface{}) ([]*models.Workflow, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Execute(ctx context.Context, workflowID uuid.UUID, params map[string]interface{}) (*WorkflowResult, error)
	ExecuteWorkflowByID(ctx context.Context, workflowID string, inputs map[string]interface{}) (*WorkflowResult, error)
	GetExecutionStatus(ctx context.Context, executionID uuid.UUID) (*WorkflowStatus, error)
	GetWorkflowStatus(ctx context.Context, executionID string) (*WorkflowStatus, error)
	GetExecutionHistory(ctx context.Context, workflowID uuid.UUID, limit int) ([]*WorkflowResult, error)
	HealthCheck() error
}

// Enhanced ServiceRegistry interface with additional methods
type EnhancedServiceRegistry interface {
	ServiceRegistry
	RegisterService(name string, service interface{}) error
	GetService(name string) (interface{}, error)
	GetDatabase() interface{}
	GetRBACService() RBACService
	GetAIService() AIService
	GetAlertService() AlertService
	GetIncidentService() IncidentService
	GetCorrelationService() CorrelationService
	GetWorkflowService() WorkflowService
}

// Update ServiceRegistry to include the missing methods
type ExtendedServiceRegistry interface {
	Register(serviceName, address string, tags []string) error
	Deregister(serviceName, address string) error
	Discover(serviceName string) ([]string, error)
	Watch(serviceName string, callback func([]string)) error
	HealthCheck() error
	// Extended methods
	RegisterService(name string, service interface{}) error
	GetService(name string) (interface{}, error)
	GetDatabase() interface{}
	GetRBACService() RBACService
	GetAIService() AIService
	GetAlertService() AlertService
	GetIncidentService() IncidentService
	GetCorrelationService() CorrelationService
	GetWorkflowService() WorkflowService
}

// Response and Result Types
type AlertAnalysisResponse struct {
	AlertID         uuid.UUID              `json:"alert_id"`
	Severity        string                 `json:"severity"`
	Classification  string                 `json:"classification"`
	Confidence      float64                `json:"confidence"`
	Recommendations []string               `json:"recommendations"`
	SimilarAlerts   []*models.Alert        `json:"similar_alerts"`
	Analysis        map[string]interface{} `json:"analysis"`
	ProcessedAt     int64                  `json:"processed_at"`
}

type CorrelationResult struct {
	CorrelationID     uuid.UUID              `json:"correlation_id"`
	AlertID           uuid.UUID              `json:"alert_id"`
	IncidentID        *uuid.UUID             `json:"incident_id,omitempty"`
	CorrelatedWith    []uuid.UUID            `json:"correlated_with"`
	SimilarAlerts     []uuid.UUID            `json:"similar_alerts"`
	IsDuplicate       bool                   `json:"is_duplicate"`
	DuplicateOf       *uuid.UUID             `json:"duplicate_of,omitempty"`
	Confidence        float64                `json:"confidence"`
	ConfidenceScore   float64                `json:"confidence_score"`
	Strategy          string                 `json:"strategy"`
	CorrelationType   string                 `json:"correlation_type"`
	RecommendedAction string                 `json:"recommended_action"`
	Reasoning         string                 `json:"reasoning"`
	Metadata          map[string]interface{} `json:"metadata"`
	CreatedAt         int64                  `json:"created_at"`
}

// AlertAnalysisRequest represents a request for AI alert analysis
type AlertAnalysisRequest struct {
	AlertID     uuid.UUID              `json:"alert_id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity"`
	Source      string                 `json:"source"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type WorkflowResult struct {
	ExecutionID string                 `json:"execution_id"`
	WorkflowID  string                 `json:"workflow_id"`
	Status      string                 `json:"status"`
	Result      map[string]interface{} `json:"result"`
	Results     map[string]interface{} `json:"results"` // Legacy field for compatibility
	Error       *string                `json:"error,omitempty"`
	StartedAt   string                 `json:"started_at"`
	CompletedAt string                 `json:"completed_at,omitempty"`
	Duration    *int64                 `json:"duration,omitempty"`
	StepResults []WorkflowStepResult   `json:"step_results"`
}

type WorkflowStatus struct {
	ExecutionID  string                 `json:"execution_id"`
	WorkflowID   string                 `json:"workflow_id"`
	Status       string                 `json:"status"`
	CurrentStep  string                 `json:"current_step"`
	Progress     float64                `json:"progress"`
	ErrorMessage string                 `json:"error_message,omitempty"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// WorkflowStatusType represents workflow status as string constants
type WorkflowStatusType string

const (
	WorkflowStatusTypePending   WorkflowStatusType = "pending"
	WorkflowStatusTypeRunning   WorkflowStatusType = "running"
	WorkflowStatusTypeCompleted WorkflowStatusType = "completed"
	WorkflowStatusTypeFailed    WorkflowStatusType = "failed"
	WorkflowStatusTypeCancelled WorkflowStatusType = "cancelled"
	WorkflowStatusTypeTimeout   WorkflowStatusType = "timeout"
)

type WorkflowStepResult struct {
	StepID      string                 `json:"step_id"`
	StepName    string                 `json:"step_name"`
	Status      WorkflowStatus         `json:"status"`
	Result      map[string]interface{} `json:"result"`
	Error       *string                `json:"error,omitempty"`
	StartedAt   int64                  `json:"started_at"`
	CompletedAt *int64                 `json:"completed_at,omitempty"`
	Duration    *int64                 `json:"duration,omitempty"`
}

// Topology and Infrastructure Types
type InfrastructureMapping struct {
	ID           uuid.UUID              `json:"id"`
	EntityType   string                 `json:"entity_type"`
	EntityID     string                 `json:"entity_id"`
	EntityName   string                 `json:"entity_name"`
	MappedTo     []string               `json:"mapped_to"`
	Dependencies []string               `json:"dependencies"`
	Attributes   map[string]interface{} `json:"attributes"`
	Metadata     map[string]string      `json:"metadata"`
	CreatedAt    int64                  `json:"created_at"`
	UpdatedAt    int64                  `json:"updated_at"`
}

type TopologyRelationship struct {
	ID           string                 `json:"id"`
	SourceType   string                 `json:"source_type"`
	SourceID     string                 `json:"source_id"`
	TargetType   string                 `json:"target_type"`
	TargetID     string                 `json:"target_id"`
	RelationType string                 `json:"relation_type"`
	Type         string                 `json:"type"` // Legacy field for compatibility
	Attributes   map[string]interface{} `json:"attributes"`
	Properties   map[string]interface{} `json:"properties"`
	Metadata     map[string]interface{} `json:"metadata"`
	Confidence   float64                `json:"confidence"`
	Strength     float64                `json:"strength"`
	Direction    string                 `json:"direction"`
	CreatedAt    int64                  `json:"created_at"`
	UpdatedAt    int64                  `json:"updated_at"`
}

type InfrastructureCorrelation struct {
	ID                   uuid.UUID              `json:"id"`
	EntityID             string                 `json:"entity_id"`
	SourceEntityID       string                 `json:"source_entity_id"`
	TargetEntityID       string                 `json:"target_entity_id"`
	EntityType           string                 `json:"entity_type"`
	RelationshipStrength float64                `json:"relationship_strength"`
	DependencyDirection  string                 `json:"dependency_direction"`
	InfrastructureLayer  string                 `json:"infrastructure_layer"`
	DiscoveredMethod     string                 `json:"discovered_method"`
	ConfidenceScore      float64                `json:"confidence_score"`
	IsActive             bool                   `json:"is_active"`
	ClusterID            string                 `json:"cluster_id"`
	NamespaceName        string                 `json:"namespace_name"`
	ServiceName          string                 `json:"service_name"`
	CorrelationData      map[string]interface{} `json:"correlation_data"`
	CorrelatedWith       []string               `json:"correlated_with"`
	CorrelationType      string                 `json:"correlation_type"`
	Confidence           float64                `json:"confidence"`
	Metadata             map[string]interface{} `json:"metadata"`
	CreatedAt            int64                  `json:"created_at"`
	UpdatedAt            int64                  `json:"updated_at"`
}

type ServiceDependency struct {
	ID               string                 `json:"id"`
	ServiceID        string                 `json:"service_id"`
	ServiceName      string                 `json:"service_name"`
	DependsOn        []string               `json:"depends_on"`
	DependentService string                 `json:"dependent_service"`
	DependencyType   string                 `json:"dependency_type"`
	Critical         bool                   `json:"critical"`
	Criticality      string                 `json:"criticality"`
	HealthStatus     string                 `json:"health_status"`
	LastChecked      int64                  `json:"last_checked"`
	Attributes       map[string]interface{} `json:"attributes"`
	CreatedAt        int64                  `json:"created_at"`
	UpdatedAt        int64                  `json:"updated_at"`
}

type TopologyMap struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Source        string                 `json:"source"`
	Entities      []TopologyEntity       `json:"entities"`
	Relationships []TopologyRelationship `json:"relationships"`
	Metadata      map[string]interface{} `json:"metadata"`
	LastUpdated   int64                  `json:"last_updated"`
	CreatedAt     int64                  `json:"created_at"`
	UpdatedAt     int64                  `json:"updated_at"`
}

type TopologyEntity struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Attributes  map[string]interface{} `json:"attributes"`
	Properties  map[string]interface{} `json:"properties"`
	Status      string                 `json:"status"`
	HealthScore float64                `json:"health_score"`
	Metadata    map[string]interface{} `json:"metadata"`
	Position    *TopologyPosition      `json:"position,omitempty"`
}

type TopologyPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z,omitempty"`
}

type ImpactAnalysis struct {
	ID                 uuid.UUID              `json:"id"`
	IncidentID         uuid.UUID              `json:"incident_id"`
	AffectedServices   []string               `json:"affected_services"`
	AffectedUsers      []string               `json:"affected_users"`
	Dependencies       []ServiceDependency    `json:"dependencies"`
	BlastRadius        []string               `json:"blast_radius"`
	BusinessImpact     string                 `json:"business_impact"`
	TechnicalImpact    string                 `json:"technical_impact"`
	ImpactScore        float64                `json:"impact_score"`
	EstimatedDowntime  int64                  `json:"estimated_downtime"`
	RiskLevel          string                 `json:"risk_level"`
	Recommendations    []string               `json:"recommendations"`
	RecommendedActions []string               `json:"recommended_actions"`
	Metadata           map[string]interface{} `json:"metadata"`
	AnalyzedAt         int64                  `json:"analyzed_at"`
}

// Incident type alias to resolve missing interfaces.Incident
type Incident = models.Incident
