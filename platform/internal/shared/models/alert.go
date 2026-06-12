package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Alert represents the single, unified alert data model used across all services
// This is the authoritative Alert struct that resolves all data model conflicts
type Alert struct {
	// Core identification fields
	ID          uuid.UUID `json:"id" db:"id"`
	Title       string    `json:"title" db:"title"`
	Description string    `json:"description" db:"description"`
	Severity    string    `json:"severity" db:"severity"`
	Status      string    `json:"status" db:"status"`

	// Source information
	Source    string `json:"source" db:"source"`
	SourceID  string `json:"source_id" db:"source_id"`
	SourceURL string `json:"source_url" db:"source_url"`

	// Categorization and metadata
	Tags        []string               `json:"tags" db:"tags"`
	Labels      map[string]string      `json:"labels" db:"labels"`
	Metadata    map[string]interface{} `json:"metadata" db:"metadata"`
	Fingerprint string                 `json:"fingerprint" db:"fingerprint"`

	// Assignment and ownership
	AssignedTo     *uuid.UUID `json:"assigned_to,omitempty" db:"assigned_to"`
	AssignedToName string     `json:"assigned_to_name,omitempty" db:"-"`
	AssignedUserID *uuid.UUID `json:"assigned_user_id,omitempty" db:"assigned_user_id"` // For compatibility
	CreatedBy      *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedByName  string     `json:"created_by_name,omitempty" db:"-"`
	TeamID         *uuid.UUID `json:"team_id,omitempty" db:"team_id"`
	PrimaryTeamID  *uuid.UUID `json:"primary_team_id,omitempty" db:"primary_team_id"` // For compatibility

	// AI Analysis and correlation
	AIAnalysis          *AIAnalysis `json:"ai_analysis,omitempty" db:"ai_analysis"`
	AIClassification    string      `json:"ai_classification,omitempty" db:"ai_classification"`
	AIConfidence        float64     `json:"ai_confidence,omitempty" db:"ai_confidence"`
	CorrelationID       string      `json:"correlation_id,omitempty" db:"correlation_id"`
	IsCorrelated        bool        `json:"is_correlated" db:"is_correlated"`
	CorrelationParentID *uuid.UUID  `json:"correlation_parent_id,omitempty" db:"correlation_parent_id"`
	CorrelationScore    *float64    `json:"correlation_score,omitempty" db:"correlation_score"`
	LinkedIncidentID    *uuid.UUID  `json:"linked_incident_id,omitempty" db:"linked_incident_id"`

	// Deduplication and count
	Count       int       `json:"count" db:"count"`
	FirstSeenAt time.Time `json:"first_seen_at" db:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at" db:"last_seen_at"`

	// Lifecycle management
	AcknowledgedAt     *time.Time `json:"acknowledged_at,omitempty" db:"acknowledged_at"`
	AcknowledgedBy     *uuid.UUID `json:"acknowledged_by,omitempty" db:"acknowledged_by"`
	AcknowledgedByName string     `json:"acknowledged_by_name,omitempty" db:"-"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty" db:"resolved_at"`
	ResolvedBy         *uuid.UUID `json:"resolved_by,omitempty" db:"resolved_by"`
	ResolvedByName     string     `json:"resolved_by_name,omitempty" db:"-"`
	ResolutionNotes    string     `json:"resolution_notes,omitempty" db:"resolution_notes"`
	ResolutionType     string     `json:"resolution_type,omitempty" db:"resolution_type"`

	// Enterprise features
	IsAlertActive        bool          `json:"is_alert_active" db:"is_alert_active"`
	LinkedAlerts         []LinkedAlert `json:"linked_alerts,omitempty" db:"linked_alerts"`
	LinkedTo             *LinkedAlert  `json:"linked_to,omitempty" db:"linked_to"`
	MaintenanceStatus    int           `json:"maintenance_status" db:"maintenance_status"`
	MaintenanceStartTime *time.Time    `json:"maintenance_start_time,omitempty" db:"maintenance_start_time"`
	MaintenanceEndTime   *time.Time    `json:"maintenance_end_time,omitempty" db:"maintenance_end_time"`

	// SLA tracking
	SLAMetResponseTime   bool       `json:"sla_met_response_time" db:"sla_met_response_time"`
	SLAMetResolutionTime bool       `json:"sla_met_resolution_time" db:"sla_met_resolution_time"`
	RecentEmailTimestamp *time.Time `json:"recent_email_timestamp,omitempty" db:"recent_email_timestamp"`
	FirstEmailTimestamp  *time.Time `json:"first_email_timestamp,omitempty" db:"first_email_timestamp"`

	// Timestamps
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// AUTONOMOUS AIOPS ENHANCEMENTS
	EntityType       string  `json:"entity_type,omitempty" db:"entity_type"`     // BM/KVM/VM/K8s/Pod/App
	EntityID         string  `json:"entity_id,omitempty" db:"entity_id"`         // Unique entity identifier
	Region           string  `json:"region,omitempty" db:"region"`               // reno/maiden
	TopologyPath     string  `json:"topology_path,omitempty" db:"topology_path"` // /region/cluster/node/pod
	BlastRadiusScore float64 `json:"blast_radius_score,omitempty" db:"blast_radius_score"`

	// Autonomous analysis results
	AutonomousAnalysis *AutonomousAnalysis `json:"autonomous_analysis,omitempty" db:"autonomous_analysis"`
	RCAConfidence      float64             `json:"rca_confidence,omitempty" db:"rca_confidence"`
	RootCauseEntity    string              `json:"root_cause_entity,omitempty" db:"root_cause_entity"`
	AgentProcessed     bool                `json:"agent_processed" db:"agent_processed"`
	AgentDecision      string              `json:"agent_decision,omitempty" db:"agent_decision"`
}

// AutonomousAnalysis represents AI analysis results from autonomous agents
type AutonomousAnalysis struct {
	ProcessedByAgent bool      `json:"processed_by_agent"`
	AgentDecision    string    `json:"agent_decision"` // "correlate", "create_incident", "suppress", "auto_resolve"
	ConfidenceScore  float64   `json:"confidence_score"`
	ReasoningSteps   []string  `json:"reasoning_steps"`
	SuggestedActions []string  `json:"suggested_actions"`
	ProcessedAt      time.Time `json:"processed_at"`

	// Topology context
	UpstreamDeps     []string `json:"upstream_deps,omitempty"`
	DownstreamImpact []string `json:"downstream_impact,omitempty"`
	ServiceOwner     string   `json:"service_owner,omitempty"`
	Environment      string   `json:"environment,omitempty"`
}

// LinkedAlert represents a linked alert reference for enterprise correlation
type LinkedAlert struct {
	AlertID   uuid.UUID `json:"alert_id" db:"alert_id"`
	AlertType string    `json:"alert_type,omitempty" db:"alert_type"`
	LinkType  string    `json:"link_type,omitempty" db:"link_type"` // "related", "duplicate", "parent", "child"
	Title     string    `json:"title,omitempty" db:"title"`         // For compatibility with service model
}

// AIAnalysis represents AI analysis of an alert (unified from service model)
type AIAnalysis struct {
	RootCause         string                 `json:"root_cause,omitempty"`
	Impact            string                 `json:"impact,omitempty"`
	Recommendations   []string               `json:"recommendations,omitempty"`
	SimilarIncidents  []string               `json:"similar_incidents,omitempty"`
	PredictedDuration time.Duration          `json:"predicted_duration,omitempty"`
	Confidence        float64                `json:"confidence"`
	AnalyzedAt        time.Time              `json:"analyzed_at"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// AlertStatus represents valid alert statuses
type AlertStatus string

const (
	AlertStatusOpen          AlertStatus = "open"
	AlertStatusAcknowledged  AlertStatus = "acknowledged"
	AlertStatusInvestigating AlertStatus = "investigating"
	AlertStatusResolved      AlertStatus = "resolved"
	AlertStatusClosed        AlertStatus = "closed"
	AlertStatusSuppressed    AlertStatus = "suppressed"
)

// AlertSeverity represents valid alert severities
type AlertSeverity string

const (
	AlertSeverityCritical AlertSeverity = "critical"
	AlertSeverityHigh     AlertSeverity = "high"
	AlertSeverityMedium   AlertSeverity = "medium"
	AlertSeverityLow      AlertSeverity = "low"
	AlertSeverityInfo     AlertSeverity = "info"
)

// AlertSource represents common alert sources
type AlertSource string

const (
	AlertSourceDynatrace  AlertSource = "dynatrace"
	AlertSourcePrometheus AlertSource = "prometheus"
	AlertSourceGrafana    AlertSource = "grafana"
	AlertSourceDatadog    AlertSource = "datadog"
	AlertSourceKubernetes AlertSource = "kubernetes"
	AlertSourceCloudWatch AlertSource = "cloudwatch"
	AlertSourceWebhook    AlertSource = "webhook"
	AlertSourceSystem     AlertSource = "system"
	AlertSourceManual     AlertSource = "manual"
	AlertSourceGeneric    AlertSource = "generic"
)

// Validate validates the alert for all required fields and business rules
func (a *Alert) Validate() error {
	if a.ID == uuid.Nil {
		return errors.New("alert ID is required")
	}

	if a.Title == "" {
		return errors.New("alert title is required")
	}

	if !a.ValidateSeverity() {
		return errors.New("invalid severity level")
	}

	if !a.ValidateStatus() {
		return errors.New("invalid status")
	}

	if a.Source == "" {
		return errors.New("alert source is required")
	}

	// Validate correlation fields if present
	if a.CorrelationScore != nil && (*a.CorrelationScore < 0.0 || *a.CorrelationScore > 1.0) {
		return errors.New("correlation score must be between 0.0 and 1.0")
	}

	if a.AIConfidence < 0.0 || a.AIConfidence > 1.0 {
		return errors.New("AI confidence must be between 0.0 and 1.0")
	}

	return nil
}

// ValidateStatus validates if the alert status is valid
func (a *Alert) ValidateStatus() bool {
	validStatuses := []AlertStatus{
		AlertStatusOpen,
		AlertStatusAcknowledged,
		AlertStatusInvestigating,
		AlertStatusResolved,
		AlertStatusClosed,
		AlertStatusSuppressed,
	}

	for _, status := range validStatuses {
		if a.Status == string(status) {
			return true
		}
	}
	return false
}

// ValidateSeverity validates if the alert severity is valid
func (a *Alert) ValidateSeverity() bool {
	validSeverities := []AlertSeverity{
		AlertSeverityCritical,
		AlertSeverityHigh,
		AlertSeverityMedium,
		AlertSeverityLow,
		AlertSeverityInfo,
	}

	for _, severity := range validSeverities {
		if a.Severity == string(severity) {
			return true
		}
	}
	return false
}

// IsActive returns true if the alert is in an active state
func (a *Alert) IsActive() bool {
	return a.Status == string(AlertStatusOpen) ||
		a.Status == string(AlertStatusAcknowledged) ||
		a.Status == string(AlertStatusInvestigating)
}

// IsResolved returns true if the alert has been resolved
func (a *Alert) IsResolved() bool {
	return a.Status == string(AlertStatusResolved) || a.Status == string(AlertStatusClosed)
}

// GetAge returns the age of the alert since creation
func (a *Alert) GetAge() time.Duration {
	return time.Since(a.CreatedAt)
}

// GetTimeToAcknowledge returns time between creation and acknowledgment
func (a *Alert) GetTimeToAcknowledge() *time.Duration {
	if a.AcknowledgedAt == nil {
		return nil
	}
	duration := a.AcknowledgedAt.Sub(a.CreatedAt)
	return &duration
}

// GetTimeToResolve returns time between creation and resolution
func (a *Alert) GetTimeToResolve() *time.Duration {
	if a.ResolvedAt == nil {
		return nil
	}
	duration := a.ResolvedAt.Sub(a.CreatedAt)
	return &duration
}

// HasTag checks if the alert has a specific tag
func (a *Alert) HasTag(tag string) bool {
	for _, t := range a.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// GetLabel returns the value of a specific label
func (a *Alert) GetLabel(key string) (string, bool) {
	if a.Labels == nil {
		return "", false
	}
	value, exists := a.Labels[key]
	return value, exists
}

// SetLabel sets a label on the alert
func (a *Alert) SetLabel(key, value string) {
	if a.Labels == nil {
		a.Labels = make(map[string]string)
	}
	a.Labels[key] = value
}

// GetMetadataValue returns a metadata value
func (a *Alert) GetMetadataValue(key string) (interface{}, bool) {
	if a.Metadata == nil {
		return nil, false
	}
	value, exists := a.Metadata[key]
	return value, exists
}

// SetMetadataValue sets a metadata value on the alert
func (a *Alert) SetMetadataValue(key string, value interface{}) {
	if a.Metadata == nil {
		a.Metadata = make(map[string]interface{})
	}
	a.Metadata[key] = value
}

// UpdateActiveStatus updates the IsAlertActive field based on current status
func (a *Alert) UpdateActiveStatus() {
	a.IsAlertActive = a.IsActive()
}

// AddLinkedAlert adds a linked alert relationship
func (a *Alert) AddLinkedAlert(alertID uuid.UUID, linkType, title string) {
	if a.LinkedAlerts == nil {
		a.LinkedAlerts = make([]LinkedAlert, 0)
	}

	// Check if already linked
	for _, linked := range a.LinkedAlerts {
		if linked.AlertID == alertID {
			return // Already linked
		}
	}

	a.LinkedAlerts = append(a.LinkedAlerts, LinkedAlert{
		AlertID:  alertID,
		LinkType: linkType,
		Title:    title,
	})
}

// RemoveLinkedAlert removes a linked alert relationship
func (a *Alert) RemoveLinkedAlert(alertID uuid.UUID) {
	if a.LinkedAlerts == nil {
		return
	}

	for i, linked := range a.LinkedAlerts {
		if linked.AlertID == alertID {
			a.LinkedAlerts = append(a.LinkedAlerts[:i], a.LinkedAlerts[i+1:]...)
			return
		}
	}
}

// ToCorrelationAlert converts to a simplified correlation alert format
func (a *Alert) ToCorrelationAlert() *CorrelationAlert {
	return &CorrelationAlert{
		ID:          a.ID,
		Title:       a.Title,
		Description: a.Description,
		Severity:    a.Severity,
		Source:      a.Source,
		SourceID:    a.SourceID,
		Tags:        a.Tags,
		Labels:      a.Labels,
		Metadata:    a.Metadata,
		Fingerprint: a.Fingerprint,
		CreatedAt:   a.CreatedAt,
	}
}

// CorrelationAlert represents a simplified alert for correlation processing
type CorrelationAlert struct {
	ID          uuid.UUID              `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity"`
	Source      string                 `json:"source"`
	SourceID    string                 `json:"source_id"`
	Tags        []string               `json:"tags"`
	Labels      map[string]string      `json:"labels"`
	Metadata    map[string]interface{} `json:"metadata"`
	Fingerprint string                 `json:"fingerprint"`
	CreatedAt   time.Time              `json:"created_at"`
}

// ProviderAlert represents an alert from an external provider
type ProviderAlert struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity"`
	Status      string                 `json:"status"`
	Source      string                 `json:"source"`
	SourceURL   string                 `json:"source_url,omitempty"`
	Tags        []string               `json:"tags"`
	Labels      map[string]string      `json:"labels"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// ToAlert converts a ProviderAlert to the unified Alert model
func (pa *ProviderAlert) ToAlert() *Alert {
	alert := &Alert{
		ID:          uuid.New(), // Generate new UUID
		Title:       pa.Title,
		Description: pa.Description,
		Severity:    pa.Severity,
		Status:      pa.Status,
		Source:      pa.Source,
		SourceID:    pa.ID, // Use provider ID as source_id
		SourceURL:   pa.SourceURL,
		Tags:        pa.Tags,
		Labels:      pa.Labels,
		Metadata:    pa.Metadata,
		Count:       1,
		CreatedAt:   pa.CreatedAt,
		UpdatedAt:   pa.UpdatedAt,
	}

	// Set timestamps
	if !pa.CreatedAt.IsZero() {
		alert.FirstSeenAt = pa.CreatedAt
		alert.LastSeenAt = pa.CreatedAt
	} else {
		now := time.Now()
		alert.CreatedAt = now
		alert.UpdatedAt = now
		alert.FirstSeenAt = now
		alert.LastSeenAt = now
	}

	// Set default status if empty
	if alert.Status == "" {
		alert.Status = string(AlertStatusOpen)
	}

	// Set active state
	alert.UpdateActiveStatus()

	return alert
}

// Constants for backward compatibility with existing services
const (
	// Alert statuses (for backward compatibility)
	StatusOpen          = "open"
	StatusAcknowledged  = "acknowledged"
	StatusInvestigating = "investigating"
	StatusResolved      = "resolved"
	StatusClosed        = "closed"

	// Alert severities (for backward compatibility)
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"

	// Alert sources (for backward compatibility)
	SourceDynatrace  = "dynatrace"
	SourcePrometheus = "prometheus"
	SourceGrafana    = "grafana"
	SourceKubernetes = "kubernetes"
	SourceCloudWatch = "cloudwatch"
	SourceGeneric    = "generic"
	SourceWebhook    = "webhook"
	SourceSystem     = "system"
	SourceManual     = "manual"
)
