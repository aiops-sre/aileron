package models

import (
	"time"

	"github.com/google/uuid"
)

// Incident represents an incident in the system
type Incident struct {
	ID                 uuid.UUID   `json:"id" db:"id"`
	IncidentNumber     int         `json:"incident_number" db:"incident_number"`
	Title              string      `json:"title" db:"title"`
	Description        string      `json:"description" db:"description"`
	Severity           string      `json:"severity" db:"severity"`
	Status             string      `json:"status" db:"status"`
	Priority           string      `json:"priority" db:"priority"`
	Impact             string      `json:"impact" db:"impact"`
	Urgency            string      `json:"urgency" db:"urgency"`
	AssignedTo         *uuid.UUID  `json:"assigned_to" db:"assigned_to"`
	CreatedBy          *uuid.UUID  `json:"created_by" db:"created_by"`
	TeamID             *uuid.UUID  `json:"team_id" db:"team_id"`
	AlertIDs           []uuid.UUID `json:"alert_ids" db:"alert_ids"`
	RelatedIncidentIDs []uuid.UUID `json:"related_incident_ids" db:"related_incident_ids"`
	Timeline           interface{} `json:"timeline" db:"timeline"`
	AIInsights         interface{} `json:"ai_insights" db:"ai_insights"`
	AIRootCause        string      `json:"ai_root_cause" db:"ai_root_cause"`
	AIRecommendations  interface{} `json:"ai_recommendations" db:"ai_recommendations"`
	ResolutionNotes    string      `json:"resolution_notes" db:"resolution_notes"`
	PostMortemURL      string      `json:"post_mortem_url" db:"post_mortem_url"`
	StartedAt          time.Time   `json:"started_at" db:"started_at"`
	DetectedAt         *time.Time  `json:"detected_at" db:"detected_at"`
	AcknowledgedAt     *time.Time  `json:"acknowledged_at" db:"acknowledged_at"`
	ResolvedAt         *time.Time  `json:"resolved_at" db:"resolved_at"`
	ClosedAt           *time.Time  `json:"closed_at" db:"closed_at"`
	CreatedAt          time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at" db:"updated_at"`
}

// CorrelationResult represents the result of alert correlation
type CorrelationResult struct {
	AlertID             uuid.UUID              `json:"alert_id"`
	CorrelationScore    float64                `json:"correlation_score"`
	CorrelatedIncidents []CorrelatedIncident   `json:"correlated_incidents"`
	CorrelatedAlerts    []CorrelatedAlert      `json:"correlated_alerts"`
	Strategy            string                 `json:"strategy"`
	Confidence          float64                `json:"confidence"`
	RecommendedActions  []string               `json:"recommended_actions"`
	Metadata            map[string]interface{} `json:"metadata"`
	ProcessedAt         time.Time              `json:"processed_at"`
}

// CorrelatedIncident represents a correlated incident
type CorrelatedIncident struct {
	IncidentID       uuid.UUID `json:"incident_id"`
	CorrelationScore float64   `json:"correlation_score"`
	Reason           string    `json:"reason"`
	Strategy         string    `json:"strategy"`
}

// CorrelatedAlert represents a correlated alert
type CorrelatedAlert struct {
	AlertID          uuid.UUID `json:"alert_id"`
	CorrelationScore float64   `json:"correlation_score"`
	Reason           string    `json:"reason"`
	Strategy         string    `json:"strategy"`
}

// TopologyMap represents the infrastructure topology
type TopologyMap struct {
	ID            uuid.UUID              `json:"id"`
	Source        string                 `json:"source"`
	Entities      []TopologyEntity       `json:"entities"`
	Relationships []TopologyRelationship `json:"relationships"`
	Metadata      map[string]interface{} `json:"metadata"`
	LastUpdated   time.Time              `json:"last_updated"`
}

// TopologyEntity represents an entity in the topology
type TopologyEntity struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"` // service, pod, node, application, etc.
	Name        string                 `json:"name"`
	Properties  map[string]interface{} `json:"properties"`
	Status      string                 `json:"status"`
	HealthScore float64                `json:"health_score"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// TopologyRelationship represents a relationship between entities
type TopologyRelationship struct {
	ID         string                 `json:"id"`
	SourceID   string                 `json:"source_id"`
	TargetID   string                 `json:"target_id"`
	Type       string                 `json:"type"` // depends_on, communicates_with, contains, etc.
	Properties map[string]interface{} `json:"properties"`
	Strength   float64                `json:"strength"`
	Direction  string                 `json:"direction"` // upstream, downstream, bidirectional
	Metadata   map[string]interface{} `json:"metadata"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// ImpactAnalysis represents the impact analysis result
type ImpactAnalysis struct {
	IncidentID         uuid.UUID              `json:"incident_id"`
	AffectedServices   []string               `json:"affected_services"`
	Dependencies       []ServiceDependency    `json:"dependencies"`
	BlastRadius        []string               `json:"blast_radius"`
	ImpactScore        float64                `json:"impact_score"`
	EstimatedDowntime  time.Duration          `json:"estimated_downtime"`
	BusinessImpact     string                 `json:"business_impact"`
	RiskLevel          string                 `json:"risk_level"`
	RecommendedActions []string               `json:"recommended_actions"`
	AnalyzedAt         time.Time              `json:"analyzed_at"`
	Metadata           map[string]interface{} `json:"metadata"`
}

// ServiceDependency represents a service dependency
type ServiceDependency struct {
	ServiceID        string    `json:"service_id"`
	ServiceName      string    `json:"service_name"`
	DependentService string    `json:"dependent_service"`
	DependencyType   string    `json:"dependency_type"`
	Criticality      string    `json:"criticality"`
	HealthStatus     string    `json:"health_status"`
	LastChecked      time.Time `json:"last_checked"`
}

// InfrastructureCorrelation represents infrastructure correlation data
type InfrastructureCorrelation struct {
	ID                   uuid.UUID              `json:"id"`
	SourceEntityID       string                 `json:"source_entity_id"`
	TargetEntityID       string                 `json:"target_entity_id"`
	EntityType           string                 `json:"entity_type"`
	CorrelationType      string                 `json:"correlation_type"`
	RelationshipStrength float64                `json:"relationship_strength"`
	DependencyDirection  string                 `json:"dependency_direction"`
	InfrastructureLayer  string                 `json:"infrastructure_layer"`
	ClusterID            string                 `json:"cluster_id"`
	NamespaceName        string                 `json:"namespace_name"`
	ServiceName          string                 `json:"service_name"`
	CorrelationData      map[string]interface{} `json:"correlation_data"`
	DiscoveredMethod     string                 `json:"discovered_method"`
	ConfidenceScore      float64                `json:"confidence_score"`
	LastValidatedAt      time.Time              `json:"last_validated_at"`
	ValidationCount      int                    `json:"validation_count"`
	IsActive             bool                   `json:"is_active"`
	Metadata             map[string]interface{} `json:"metadata"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
}

// NotificationChannel represents a notification channel
type NotificationChannel struct {
	ID        uuid.UUID              `json:"id"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Config    map[string]interface{} `json:"config"`
	IsActive  bool                   `json:"is_active"`
	CreatedBy *uuid.UUID             `json:"created_by"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// InfrastructureMapping represents infrastructure topology mapping
type InfrastructureMapping struct {
	EntityID     string            `json:"entity_id"`
	EntityType   string            `json:"entity_type"`
	Dependencies []string          `json:"dependencies"`
	Metadata     map[string]string `json:"metadata"`
}

// User represents a user in the system
type User struct {
	ID                  uuid.UUID              `json:"id" db:"id"`
	Username            string                 `json:"username" db:"username"`
	Email               string                 `json:"email" db:"email"`
	PasswordHash        string                 `json:"password_hash,omitempty" db:"password_hash"`
	FullName            string                 `json:"full_name" db:"full_name"`
	RoleID              *uuid.UUID             `json:"role_id" db:"role_id"`
	IsActive            bool                   `json:"is_active" db:"is_active"`
	IsVerified          bool                   `json:"is_verified" db:"is_verified"`
	MFAEnabled          bool                   `json:"mfa_enabled" db:"mfa_enabled"`
	MFASecret           string                 `json:"mfa_secret,omitempty" db:"mfa_secret"`
	LastLogin           *time.Time             `json:"last_login" db:"last_login"`
	LoginCount          int                    `json:"login_count" db:"login_count"`
	FailedLoginAttempts int                    `json:"failed_login_attempts" db:"failed_login_attempts"`
	LockedUntil         *time.Time             `json:"locked_until" db:"locked_until"`
	AvatarURL           string                 `json:"avatar_url" db:"avatar_url"`
	Phone               string                 `json:"phone" db:"phone"`
	Timezone            string                 `json:"timezone" db:"timezone"`
	Preferences         map[string]interface{} `json:"preferences" db:"preferences"`
	CreatedAt           time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at" db:"updated_at"`
}

// UserExpertise represents user expertise data
type UserExpertise struct {
	ID                       uuid.UUID              `json:"id"`
	UserID                   uuid.UUID              `json:"user_id"`
	ExpertiseType            string                 `json:"expertise_type"`
	ExpertiseArea            string                 `json:"expertise_area"`
	ExpertiseLevel           string                 `json:"expertise_level"`
	ConfidenceScore          float64                `json:"confidence_score"`
	Technologies             []string               `json:"technologies"`
	Services                 []string               `json:"services"`
	InfrastructureComponents []string               `json:"infrastructure_components"`
	ResolutionCount          int                    `json:"resolution_count"`
	AvgResolutionTimeMinutes int                    `json:"avg_resolution_time_minutes"`
	SuccessRate              float64                `json:"success_rate"`
	EscalationRate           float64                `json:"escalation_rate"`
	AutoDetected             bool                   `json:"auto_detected"`
	LastActivityDate         *time.Time             `json:"last_activity_date"`
	SkillValidationDate      *time.Time             `json:"skill_validation_date"`
	ValidatedBy              *uuid.UUID             `json:"validated_by"`
	PreferredAlertTypes      []string               `json:"preferred_alert_types"`
	AvailabilityHours        map[string]interface{} `json:"availability_hours"`
	MaxConcurrentAlerts      int                    `json:"max_concurrent_alerts"`
	NotificationPreferences  map[string]interface{} `json:"notification_preferences"`
	Metadata                 map[string]interface{} `json:"metadata"`
	CreatedAt                time.Time              `json:"created_at"`
	UpdatedAt                time.Time              `json:"updated_at"`
}

// Role represents a user role in the system
type Role struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	Name        string                 `json:"name" db:"name"`
	DisplayName string                 `json:"display_name" db:"display_name"`
	Description string                 `json:"description" db:"description"`
	Permissions []string               `json:"permissions" db:"permissions"`
	IsActive    bool                   `json:"is_active" db:"is_active"`
	IsDefault   bool                   `json:"is_default" db:"is_default"`
	Metadata    map[string]interface{} `json:"metadata" db:"metadata"`
	CreatedBy   *uuid.UUID             `json:"created_by" db:"created_by"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at" db:"updated_at"`
}

// CorrelationRule represents a correlation rule
type CorrelationRule struct {
	ID            uuid.UUID              `json:"id" db:"id"`
	Name          string                 `json:"name" db:"name"`
	Description   string                 `json:"description" db:"description"`
	RuleType      string                 `json:"rule_type" db:"rule_type"`
	Conditions    map[string]interface{} `json:"conditions" db:"conditions"`
	Actions       map[string]interface{} `json:"actions" db:"actions"`
	Priority      int                    `json:"priority" db:"priority"`
	IsActive      bool                   `json:"is_active" db:"is_active"`
	TimeWindow    int                    `json:"time_window" db:"time_window"`
	Threshold     float64                `json:"threshold" db:"threshold"`
	Tags          []string               `json:"tags" db:"tags"`
	AppliedCount  int                    `json:"applied_count" db:"applied_count"`
	LastTriggered *time.Time             `json:"last_triggered" db:"last_triggered"`
	Metadata      map[string]interface{} `json:"metadata" db:"metadata"`
	CreatedBy     *uuid.UUID             `json:"created_by" db:"created_by"`
	CreatedAt     time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at" db:"updated_at"`
}

// Workflow represents a workflow in the system
type Workflow struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	Name        string                 `json:"name" db:"name"`
	Description string                 `json:"description" db:"description"`
	Version     string                 `json:"version" db:"version"`
	Type        string                 `json:"type" db:"type"`
	Trigger     map[string]interface{} `json:"trigger" db:"trigger"`
	Steps       []WorkflowStep         `json:"steps" db:"steps"`
	Variables   map[string]interface{} `json:"variables" db:"variables"`
	IsActive    bool                   `json:"is_active" db:"is_active"`
	IsPublic    bool                   `json:"is_public" db:"is_public"`
	Tags        []string               `json:"tags" db:"tags"`
	Timeout     int                    `json:"timeout" db:"timeout"`
	RetryCount  int                    `json:"retry_count" db:"retry_count"`
	Metadata    map[string]interface{} `json:"metadata" db:"metadata"`
	CreatedBy   *uuid.UUID             `json:"created_by" db:"created_by"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at" db:"updated_at"`
}

// WorkflowStep represents a step in a workflow
type WorkflowStep struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	Action       string                 `json:"action"`
	Parameters   map[string]interface{} `json:"parameters"`
	Conditions   map[string]interface{} `json:"conditions"`
	OnSuccess    string                 `json:"on_success"`
	OnFailure    string                 `json:"on_failure"`
	Timeout      int                    `json:"timeout"`
	RetryCount   int                    `json:"retry_count"`
	RetryDelay   int                    `json:"retry_delay"`
	Dependencies []string               `json:"dependencies"`
	Metadata     map[string]interface{} `json:"metadata"`
}
