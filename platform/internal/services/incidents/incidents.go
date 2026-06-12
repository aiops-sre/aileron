package incidents

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/aileron-platform/aileron/platform/internal/services/ai"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
)

var (
	ErrIncidentNotFound = errors.New("incident not found")
	ErrInvalidSeverity  = errors.New("invalid severity")
	ErrInvalidStatus    = errors.New("invalid status")
)

// Incident represents an incident
type Incident struct {
	ID                   uuid.UUID       `json:"id"`
	IncidentNumber       string          `json:"incident_number"`
	Title                string          `json:"title"`
	Description          string          `json:"description"`
	Severity             string          `json:"severity"`
	Status               string          `json:"status"`
	Priority             string          `json:"priority"`
	Impact               string          `json:"impact"`
	Urgency              string          `json:"urgency"`
	AssignedTo           *uuid.UUID      `json:"assigned_to,omitempty"`
	AssignedToName       string          `json:"assigned_to_name,omitempty"`
	CreatedBy            *uuid.UUID      `json:"created_by,omitempty"`
	CreatedByName        string          `json:"created_by_name,omitempty"`
	TeamID               *uuid.UUID      `json:"team_id,omitempty"`
	AlertIDs             []uuid.UUID     `json:"alert_ids"`
	AlertCount           int             `json:"alert_count"`
	RelatedIncidentIDs   []uuid.UUID     `json:"related_incident_ids"`
	Timeline             []TimelineEvent `json:"timeline"`
	AIInsights           *ai.RCAResponse `json:"ai_insights,omitempty"`
	AIRootCause          string          `json:"ai_root_cause,omitempty"`
	AIRecommendations    []string        `json:"ai_recommendations,omitempty"`
	ResolutionNotes      string          `json:"resolution_notes,omitempty"`
	PostMortemURL        string          `json:"post_mortem_url,omitempty"`
	AutoCreated          bool            `json:"auto_created"`
	CorrelationID        string          `json:"correlation_id,omitempty"`
	CorrelationConfidence float64        `json:"correlation_confidence,omitempty"`
	// RCA enrichment
	RCAInvestigationID   string          `json:"rca_investigation_id,omitempty"`
	RCAStatus            string          `json:"rca_status"`
	RCAConfidence        float64         `json:"rca_confidence,omitempty"`
	// Blast radius and topology
	BlastRadius          []string            `json:"blast_radius,omitempty"`
	// BlastRadiusDetails carries the structured topology blast radius populated by
	// RecursiveTopoRCA — each entry includes entity type, infra level, and propagation
	// score so the UI can render the BM → VM → K8s node → pod hierarchy.
	BlastRadiusDetails   json.RawMessage     `json:"blast_radius_details,omitempty"`
	AffectedServicesNames []string       `json:"affected_services_names,omitempty"`
	TopologyPath         string          `json:"topology_path,omitempty"`
	CorrelationMethod    string          `json:"correlation_method,omitempty"`
	DominantStrategy     string          `json:"dominant_strategy,omitempty"`
	// V2 Correlation Engine fields
	OntologyDomain      string          `json:"ontology_domain,omitempty"`
	TopoRootEntityID    string          `json:"topo_root_entity_id,omitempty"`
	CausalChain         json.RawMessage `json:"causal_chain,omitempty"`
	RCAHypotheses       json.RawMessage `json:"rca_hypotheses,omitempty"`
	EvolutionGeneration int             `json:"evolution_generation,omitempty"`
	MergeSourceIDs      []string        `json:"merge_source_ids,omitempty"`
	StartedAt            time.Time       `json:"started_at"`
	DetectedAt           *time.Time      `json:"detected_at,omitempty"`
	AcknowledgedAt       *time.Time      `json:"acknowledged_at,omitempty"`
	ResolvedAt           *time.Time      `json:"resolved_at,omitempty"`
	ClosedAt             *time.Time      `json:"closed_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// TimelineEvent represents an incident timeline event
type TimelineEvent struct {
	ID          uuid.UUID              `json:"id"`
	IncidentID  uuid.UUID              `json:"incident_id"`
	UserID      *uuid.UUID             `json:"user_id,omitempty"`
	UserName    string                 `json:"user_name,omitempty"`
	EventType   string                 `json:"event_type"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
}

// IncidentService handles incident operations
type IncidentService struct {
	db          *sql.DB
	rbacService *rbac.RBACService
	aiService   *ai.AIService
	rcaURL      string // RCA orchestrator base URL; empty = disabled
}

// NewIncidentService creates a new incident service
func NewIncidentService(db *sql.DB, rbacService *rbac.RBACService, aiService *ai.AIService) *IncidentService {
	return &IncidentService{
		db:          db,
		rbacService: rbacService,
		aiService:   aiService,
	}
}

// SetRCAURL wires the RCA orchestrator URL for triggering investigations.
func (s *IncidentService) SetRCAURL(url string) { s.rcaURL = url }

// CreateIncident creates a new incident
func (s *IncidentService) CreateIncident(ctx context.Context, incident *Incident, userID uuid.UUID) error {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.create"); err != nil {
		return err
	}

	incident.ID = uuid.New()
	incident.CreatedBy = &userID
	incident.CreatedAt = time.Now()
	incident.UpdatedAt = time.Now()
	incident.StartedAt = time.Now()
	incident.Status = "open"

	// Marshal JSON fields
	alertIDsJSON, _ := json.Marshal(incident.AlertIDs)
	relatedIDsJSON, _ := json.Marshal(incident.RelatedIncidentIDs)
	timelineJSON, _ := json.Marshal(incident.Timeline)

	query := `
		INSERT INTO incidents (
			id, title, description, severity, status, priority, impact, urgency,
			assigned_to, created_by, team_id, alert_ids, related_incident_ids, timeline,
			started_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING incident_number
	`

	err := s.db.QueryRowContext(ctx, query,
		incident.ID, incident.Title, incident.Description, incident.Severity, incident.Status,
		incident.Priority, incident.Impact, incident.Urgency, incident.AssignedTo, incident.CreatedBy,
		incident.TeamID, alertIDsJSON, relatedIDsJSON, timelineJSON,
		incident.StartedAt, incident.CreatedAt, incident.UpdatedAt,
	).Scan(&incident.IncidentNumber)

	if err != nil {
		return err
	}

	// Add creation event to timeline
	s.AddTimelineEvent(ctx, incident.ID, userID, "created", "Incident Created", "Incident was created", nil)

	// Trigger AI analysis asynchronously
	go s.analyzeIncidentAsync(incident)

	return nil
}

// InternalCreateIncident creates an incident without RBAC check (for automated/webhook flows)
func (s *IncidentService) InternalCreateIncident(ctx context.Context, incident *Incident) error {
	incident.ID = uuid.New()
	incident.CreatedAt = time.Now()
	incident.UpdatedAt = time.Now()
	incident.StartedAt = time.Now()
	if incident.Status == "" {
		incident.Status = "open"
	}

	alertIDsJSON, _ := json.Marshal(incident.AlertIDs)
	relatedIDsJSON, _ := json.Marshal(incident.RelatedIncidentIDs)
	timelineJSON, _ := json.Marshal(incident.Timeline)
	blastRadiusJSON, _ := json.Marshal(incident.BlastRadius)

	rcaStatus := incident.RCAStatus
	if rcaStatus == "" {
		rcaStatus = "none"
	}

	query := `
		INSERT INTO incidents (
			id, title, description, severity, status, priority, impact, urgency,
			assigned_to, created_by, team_id, alert_ids, related_incident_ids, timeline,
			auto_created, ai_root_cause, rca_status, blast_radius, affected_services_names,
			topology_path, correlation_method, dominant_strategy, started_at, created_at, updated_at,
			correlation_id, correlation_confidence
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		          $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25,
		          NULLIF($26, ''), $27)
		RETURNING incident_number
	`

	err := s.db.QueryRowContext(ctx, query,
		incident.ID, incident.Title, incident.Description, incident.Severity, incident.Status,
		incident.Priority, incident.Impact, incident.Urgency, incident.AssignedTo, incident.CreatedBy,
		incident.TeamID, alertIDsJSON, relatedIDsJSON, timelineJSON,
		true, incident.AIRootCause, rcaStatus, blastRadiusJSON, pq.Array(incident.AffectedServicesNames),
		incident.TopologyPath, incident.CorrelationMethod, incident.DominantStrategy,
		incident.StartedAt, incident.CreatedAt, incident.UpdatedAt,
		incident.CorrelationID, incident.CorrelationConfidence,
	).Scan(&incident.IncidentNumber)
	if err != nil {
		return err
	}

	// Add a system timeline event so operators can see the auto-creation origin
	s.AddTimelineEvent(ctx, incident.ID, uuid.Nil, "auto_created",
		"Incident Auto-Created", "Incident was automatically created by the correlation pipeline", nil)

	// RCA is triggered by the pipeline's enrichIncidentV2 with full go_context after topology
	// engines run — calling analyzeIncidentAsync here would race with enrichIncidentV2 and
	// always win (fires immediately) with empty go_context, producing "unknown entity" RCA.

	return nil
}

// GetIncident retrieves an incident by ID
func (s *IncidentService) GetIncident(ctx context.Context, incidentID, userID uuid.UUID) (*Incident, error) {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.view"); err != nil {
		return nil, err
	}

	incident := &Incident{}

	query := `
		SELECT i.id, i.incident_number, i.title, i.description, i.severity, i.status,
		       COALESCE(i.priority, ''), COALESCE(i.impact, ''), COALESCE(i.urgency, ''),
		       i.assigned_to, i.created_by, i.team_id,
		       i.alert_ids, i.related_incident_ids, i.timeline, i.ai_insights,
		       i.ai_root_cause, i.ai_recommendations, i.resolution_notes, i.post_mortem_url,
		       COALESCE(i.started_at, i.created_at), i.detected_at, i.acknowledged_at, i.resolved_at, i.closed_at,
		       i.created_at, i.updated_at,
		       COALESCE(i.rca_status, 'none'), COALESCE(i.rca_investigation_id, ''),
		       COALESCE(i.rca_confidence, 0),
		       COALESCE(i.blast_radius, '[]'::jsonb),
		       COALESCE(i.affected_services_names, ARRAY[]::text[]),
		       COALESCE(i.topology_path, ''), COALESCE(i.correlation_method, ''),
		       COALESCE(i.dominant_strategy, ''), COALESCE(i.correlation_confidence, 0),
		       COALESCE(i.auto_created, false),
		       COALESCE(u1.full_name, '') as assigned_to_name,
		       COALESCE(u2.full_name, '') as created_by_name,
		       COALESCE(i.correlation_id, ''),
		       COALESCE(i.ontology_domain, ''),
		       COALESCE(i.topo_root_entity_id, ''),
		       i.causal_chain,
		       i.rca_hypotheses,
		       COALESCE(i.evolution_generation, 0),
		       COALESCE(i.merge_source_ids, ARRAY[]::uuid[]),
		       COALESCE(i.blast_radius_details, '[]'::jsonb)
		FROM incidents i
		LEFT JOIN users u1 ON i.assigned_to = u1.id
		LEFT JOIN users u2 ON i.created_by = u2.id
		WHERE i.id = $1
	`

	var alertIDsJSON, relatedIDsJSON, timelineJSON, aiInsightsJSON, aiRecommendationsJSON, blastRadiusJSON []byte
	var causalChainJSON, rcaHypothesesJSON, blastRadiusDetailsJSON []byte
	var mergeSourceIDsRaw []string

	err := s.db.QueryRowContext(ctx, query, incidentID).Scan(
		&incident.ID, &incident.IncidentNumber, &incident.Title, &incident.Description,
		&incident.Severity, &incident.Status, &incident.Priority, &incident.Impact, &incident.Urgency,
		&incident.AssignedTo, &incident.CreatedBy, &incident.TeamID, &alertIDsJSON, &relatedIDsJSON,
		&timelineJSON, &aiInsightsJSON, &incident.AIRootCause, &aiRecommendationsJSON,
		&incident.ResolutionNotes, &incident.PostMortemURL, &incident.StartedAt, &incident.DetectedAt,
		&incident.AcknowledgedAt, &incident.ResolvedAt, &incident.ClosedAt,
		&incident.CreatedAt, &incident.UpdatedAt,
		&incident.RCAStatus, &incident.RCAInvestigationID, &incident.RCAConfidence,
		&blastRadiusJSON, pq.Array(&incident.AffectedServicesNames),
		&incident.TopologyPath, &incident.CorrelationMethod, &incident.DominantStrategy,
		&incident.CorrelationConfidence, &incident.AutoCreated,
		&incident.AssignedToName, &incident.CreatedByName,
		&incident.CorrelationID,
		&incident.OntologyDomain, &incident.TopoRootEntityID,
		&causalChainJSON, &rcaHypothesesJSON,
		&incident.EvolutionGeneration, pq.Array(&mergeSourceIDsRaw),
		&blastRadiusDetailsJSON,
	)

	if err == sql.ErrNoRows {
		return nil, ErrIncidentNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	json.Unmarshal(alertIDsJSON, &incident.AlertIDs)
	json.Unmarshal(relatedIDsJSON, &incident.RelatedIncidentIDs)
	json.Unmarshal(timelineJSON, &incident.Timeline)
	if aiInsightsJSON != nil {
		json.Unmarshal(aiInsightsJSON, &incident.AIInsights)
	}
	if aiRecommendationsJSON != nil {
		json.Unmarshal(aiRecommendationsJSON, &incident.AIRecommendations)
	}
	if blastRadiusJSON != nil {
		json.Unmarshal(blastRadiusJSON, &incident.BlastRadius)
	}
	if blastRadiusDetailsJSON != nil {
		incident.BlastRadiusDetails = json.RawMessage(blastRadiusDetailsJSON)
	}
	if causalChainJSON != nil {
		incident.CausalChain = json.RawMessage(causalChainJSON)
	}
	if rcaHypothesesJSON != nil {
		incident.RCAHypotheses = json.RawMessage(rcaHypothesesJSON)
	}
	incident.MergeSourceIDs = mergeSourceIDsRaw

	return incident, nil
}

// GetIncidentAlerts retrieves the full alert details for all alerts linked to an incident.
func (s *IncidentService) GetIncidentAlerts(ctx context.Context, incidentID uuid.UUID) ([]map[string]interface{}, error) {
	query := `
		SELECT a.id, a.title, a.description, a.severity, a.status, a.source,
		       a.source_id, a.labels, a.metadata, a.tags, a.fingerprint,
		       a.created_at, a.updated_at,
		       COALESCE(a.correlation_id, '') as correlation_id,
		       COALESCE(a.correlation_confidence::text, '0') as correlation_confidence
		FROM alerts a
		JOIN incidents i ON a.id = ANY(
		    SELECT elem::uuid
		    FROM jsonb_array_elements_text(i.alert_ids) AS elem
		)
		WHERE i.id = $1
		ORDER BY a.created_at ASC
	`

	rows, err := s.db.QueryContext(ctx, query, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []map[string]interface{}
	for rows.Next() {
		var id, sourceID, source, title, description, severity, status, fingerprint, correlationID, correlationConf string
		var labelsJSON, metadataJSON, tagsJSON []byte
		var createdAt, updatedAt time.Time

		if err := rows.Scan(
			&id, &title, &description, &severity, &status, &source,
			&sourceID, &labelsJSON, &metadataJSON, &tagsJSON, &fingerprint,
			&createdAt, &updatedAt, &correlationID, &correlationConf,
		); err != nil {
			continue
		}

		alert := map[string]interface{}{
			"id":                     id,
			"title":                  title,
			"description":            description,
			"severity":               severity,
			"status":                 status,
			"source":                 source,
			"source_id":              sourceID,
			"fingerprint":            fingerprint,
			"correlation_id":         correlationID,
			"correlation_confidence": correlationConf,
			"created_at":             createdAt,
			"updated_at":             updatedAt,
		}

		var labels map[string]string
		var metadata, tags map[string]interface{}
		json.Unmarshal(labelsJSON, &labels)
		json.Unmarshal(metadataJSON, &metadata)
		json.Unmarshal(tagsJSON, &tags)
		alert["labels"] = labels
		alert["metadata"] = metadata
		alert["tags"] = tags

		alerts = append(alerts, alert)
	}

	if alerts == nil {
		alerts = []map[string]interface{}{}
	}
	return alerts, rows.Err()
}

// ListIncidents retrieves incidents with filtering
func (s *IncidentService) ListIncidents(ctx context.Context, userID uuid.UUID, filters IncidentFilters) ([]*Incident, int, error) {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.view"); err != nil {
		return nil, 0, err
	}

	// Build query
	query := `
		SELECT i.id, i.incident_number, i.title, i.severity, i.status,
		       COALESCE(i.priority, ''),
		       i.assigned_to, i.created_by,
		       COALESCE(i.started_at, i.created_at), i.resolved_at, i.created_at,
		       COALESCE(u1.full_name, '') as assigned_to_name,
		       COALESCE(u2.full_name, '') as created_by_name,
		       COALESCE(i.auto_created, false),
		       COALESCE(i.rca_status, 'none'),
		       COALESCE(i.rca_investigation_id, ''),
		       COALESCE(i.rca_confidence, 0),
		       COALESCE(i.blast_radius, '[]'::jsonb),
		       COALESCE(i.affected_services_names, ARRAY[]::text[]),
		       COALESCE(i.topology_path, ''),
		       COALESCE(i.correlation_method, ''),
		       COALESCE(i.dominant_strategy, ''),
		       COALESCE(i.correlation_confidence, 0),
		       jsonb_array_length(COALESCE(i.alert_ids, '[]'::jsonb)) as alert_count,
		       COALESCE(i.description, ''),
		       COALESCE(i.correlation_id, ''),
		       COALESCE(i.ontology_domain, ''),
		       COALESCE(i.topo_root_entity_id, ''),
		       COALESCE(i.evolution_generation, 0),
		       COALESCE(i.ai_root_cause, ''),
		       COALESCE(i.blast_radius_details, '[]'::jsonb)
		FROM incidents i
		LEFT JOIN users u1 ON i.assigned_to = u1.id
		LEFT JOIN users u2 ON i.created_by = u2.id
		WHERE 1=1
	`

	args := []interface{}{}
	argCount := 1

	// Apply filters
	if len(filters.Severity) > 0 {
		query += fmt.Sprintf(" AND i.severity = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Severity))
		argCount++
	}

	if len(filters.Status) > 0 {
		query += fmt.Sprintf(" AND i.status = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Status))
		argCount++
	}

	if filters.Search != "" {
		query += fmt.Sprintf(" AND (i.title ILIKE $%d OR i.description ILIKE $%d OR i.correlation_id ILIKE $%d OR i.incident_number::text ILIKE $%d)", argCount, argCount, argCount, argCount)
		args = append(args, "%"+filters.Search+"%")
		argCount++
	}

	query += fmt.Sprintf(" ORDER BY i.created_at DESC LIMIT $%d OFFSET $%d", argCount, argCount+1)
	args = append(args, filters.Limit, filters.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var incidents []*Incident
	for rows.Next() {
		incident := &Incident{}
		var blastRadiusJSON, blastRadiusDetailsJSON []byte
		err := rows.Scan(
			&incident.ID, &incident.IncidentNumber, &incident.Title, &incident.Severity,
			&incident.Status, &incident.Priority, &incident.AssignedTo, &incident.CreatedBy,
			&incident.StartedAt, &incident.ResolvedAt, &incident.CreatedAt,
			&incident.AssignedToName, &incident.CreatedByName,
			&incident.AutoCreated, &incident.RCAStatus, &incident.RCAInvestigationID,
			&incident.RCAConfidence, &blastRadiusJSON, pq.Array(&incident.AffectedServicesNames),
			&incident.TopologyPath, &incident.CorrelationMethod, &incident.DominantStrategy,
			&incident.CorrelationConfidence, &incident.AlertCount,
			&incident.Description, &incident.CorrelationID,
			&incident.OntologyDomain, &incident.TopoRootEntityID, &incident.EvolutionGeneration,
			&incident.AIRootCause, &blastRadiusDetailsJSON,
		)
		if err != nil {
			return nil, 0, err
		}
		if blastRadiusJSON != nil {
			json.Unmarshal(blastRadiusJSON, &incident.BlastRadius)
		}
		if blastRadiusDetailsJSON != nil {
			incident.BlastRadiusDetails = json.RawMessage(blastRadiusDetailsJSON)
		}
		incidents = append(incidents, incident)
	}

	// Get total count respecting the same filters
	countQuery := "SELECT COUNT(*) FROM incidents i WHERE 1=1"
	countArgs := []interface{}{}
	cIdx := 1
	if len(filters.Severity) > 0 {
		countQuery += fmt.Sprintf(" AND i.severity = ANY($%d)", cIdx)
		countArgs = append(countArgs, pq.Array(filters.Severity))
		cIdx++
	}
	if len(filters.Status) > 0 {
		countQuery += fmt.Sprintf(" AND i.status = ANY($%d)", cIdx)
		countArgs = append(countArgs, pq.Array(filters.Status))
		cIdx++
	}
	if filters.Search != "" {
		countQuery += fmt.Sprintf(" AND (i.title ILIKE $%d OR i.description ILIKE $%d OR i.correlation_id ILIKE $%d OR i.incident_number::text ILIKE $%d)", cIdx, cIdx, cIdx, cIdx)
		countArgs = append(countArgs, "%"+filters.Search+"%")
	}
	var total int
	s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)

	return incidents, total, rows.Err()
}

// UpdateIncident updates an incident
func (s *IncidentService) UpdateIncident(ctx context.Context, incident *Incident, userID uuid.UUID) error {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.update"); err != nil {
		return err
	}

	incident.UpdatedAt = time.Now()

	query := `
		UPDATE incidents
		SET title = $1, description = $2, severity = $3, status = $4, priority = $5,
		    impact = $6, urgency = $7, assigned_to = $8, resolution_notes = $9,
		    post_mortem_url = $10, updated_at = $11
		WHERE id = $12
	`

	result, err := s.db.ExecContext(ctx, query,
		incident.Title, incident.Description, incident.Severity, incident.Status, incident.Priority,
		incident.Impact, incident.Urgency, incident.AssignedTo, incident.ResolutionNotes,
		incident.PostMortemURL, incident.UpdatedAt, incident.ID,
	)

	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIncidentNotFound
	}

	// Add update event to timeline
	s.AddTimelineEvent(ctx, incident.ID, userID, "updated", "Incident Updated", "Incident details were updated", nil)

	return nil
}

// ResolveIncident resolves an incident
func (s *IncidentService) ResolveIncident(ctx context.Context, incidentID, userID uuid.UUID, notes string) error {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.resolve"); err != nil {
		return err
	}

	now := time.Now()
	query := `
		UPDATE incidents
		SET status = 'resolved', resolved_at = $1, resolution_notes = $2, updated_at = $3
		WHERE id = $4
	`

	result, err := s.db.ExecContext(ctx, query, now, notes, now, incidentID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIncidentNotFound
	}

	// Add resolution event to timeline
	s.AddTimelineEvent(ctx, incidentID, userID, "resolved", "Incident Resolved", notes, nil)

	return nil
}

// AddTimelineEvent adds an event to incident timeline
func (s *IncidentService) AddTimelineEvent(ctx context.Context, incidentID, userID uuid.UUID, eventType, title, description string, metadata map[string]interface{}) error {
	eventID := uuid.New()
	metadataJSON, _ := json.Marshal(metadata)

	// Pass NULL for system/pipeline events (uuid.Nil) to avoid FK violation on users.id.
	var userIDArg interface{}
	if userID != uuid.Nil {
		userIDArg = userID
	}

	query := `
		INSERT INTO incident_timeline (id, incident_id, user_id, event_type, title, description, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := s.db.ExecContext(ctx, query,
		eventID, incidentID, userIDArg, eventType, title, description, metadataJSON, time.Now(),
	)

	return err
}

// GetIncidentTimeline retrieves incident timeline, merging real events with synthetic ones from timestamps.
func (s *IncidentService) GetIncidentTimeline(ctx context.Context, incidentID, userID uuid.UUID) ([]TimelineEvent, error) {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.view"); err != nil {
		return nil, err
	}

	query := `
		SELECT t.id, t.incident_id, t.user_id, t.event_type, t.title, t.description,
		       t.metadata, t.created_at, u.full_name as user_name
		FROM incident_timeline t
		LEFT JOIN users u ON t.user_id = u.id
		WHERE t.incident_id = $1
		ORDER BY t.created_at ASC
	`

	rows, err := s.db.QueryContext(ctx, query, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var realEvents []TimelineEvent
	coveredTypes := map[string]bool{}
	for rows.Next() {
		event := TimelineEvent{}
		var metadataJSON []byte
		err := rows.Scan(
			&event.ID, &event.IncidentID, &event.UserID, &event.EventType,
			&event.Title, &event.Description, &metadataJSON, &event.CreatedAt, &event.UserName,
		)
		if err != nil {
			return nil, err
		}
		if metadataJSON != nil {
			json.Unmarshal(metadataJSON, &event.Metadata)
		}
		realEvents = append(realEvents, event)
		coveredTypes[event.EventType] = true
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}

	// Synthesize milestone events from incident timestamps for any type not already stored.
	var inc struct {
		CreatedAt      time.Time
		StartedAt      time.Time
		DetectedAt     *time.Time
		AcknowledgedAt *time.Time
		ResolvedAt     *time.Time
		Title          string
		Severity       string
		CorrelationID  string
	}
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(created_at, NOW()), COALESCE(started_at, created_at),
		       detected_at, acknowledged_at, resolved_at,
		       title, severity, COALESCE(correlation_id, '')
		FROM incidents WHERE id = $1`, incidentID).Scan(
		&inc.CreatedAt, &inc.StartedAt, &inc.DetectedAt,
		&inc.AcknowledgedAt, &inc.ResolvedAt,
		&inc.Title, &inc.Severity, &inc.CorrelationID,
	)

	synth := func(evType, title, desc string, ts time.Time) TimelineEvent {
		return TimelineEvent{
			ID:          uuid.New(),
			IncidentID:  incidentID,
			EventType:   evType,
			Title:       title,
			Description: desc,
			CreatedAt:   ts,
		}
	}

	var synthetic []TimelineEvent
	if !coveredTypes["incident_detected"] && !inc.StartedAt.IsZero() {
		desc := fmt.Sprintf("Incident detected with severity %s", inc.Severity)
		if inc.CorrelationID != "" {
			desc += fmt.Sprintf(" (correlation: %s)", inc.CorrelationID)
		}
		synthetic = append(synthetic, synth("incident_detected", "Incident Detected", desc, inc.StartedAt))
	}
	if !coveredTypes["auto_created"] && !inc.CreatedAt.IsZero() {
		synthetic = append(synthetic, synth("auto_created", "Incident Created", "Incident record created by correlation pipeline", inc.CreatedAt))
	}
	if inc.DetectedAt != nil && !coveredTypes["alert_received"] {
		synthetic = append(synthetic, synth("alert_received", "First Alert Received", "Initial alert triggered incident creation", *inc.DetectedAt))
	}
	if inc.AcknowledgedAt != nil && !coveredTypes["acknowledged"] {
		synthetic = append(synthetic, synth("acknowledged", "Incident Acknowledged", "Incident was acknowledged", *inc.AcknowledgedAt))
	}
	if inc.ResolvedAt != nil && !coveredTypes["resolved"] {
		synthetic = append(synthetic, synth("resolved", "Incident Resolved", "Incident was marked as resolved", *inc.ResolvedAt))
	}

	// Synthesize per-alert events (first_seen, last_seen for linked alerts).
	alertRows, _ := s.db.QueryContext(ctx, `
		SELECT title, severity, source, COALESCE(first_seen_at, created_at), last_seen_at, created_at
		FROM alerts WHERE incident_id = $1 ORDER BY COALESCE(first_seen_at, created_at) ASC
	`, incidentID)
	if alertRows != nil {
		defer alertRows.Close()
		for alertRows.Next() {
			var aTitle, aSev, aSrc string
			var aFirst, aCreated time.Time
			var aLast *time.Time
			if err := alertRows.Scan(&aTitle, &aSev, &aSrc, &aFirst, &aLast, &aCreated); err != nil {
				continue
			}
			synthetic = append(synthetic, synth(
				"alert_fired",
				fmt.Sprintf("Alert: %s", aTitle),
				fmt.Sprintf("[%s] %s from %s", strings.ToUpper(aSev), aTitle, aSrc),
				aFirst,
			))
			if aLast != nil && aLast.After(aFirst.Add(time.Minute)) {
				synthetic = append(synthetic, synth(
					"alert_updated",
					fmt.Sprintf("Alert Updated: %s", aTitle),
					"Alert last seen at this time",
					*aLast,
				))
			}
		}
	}

	all := append(synthetic, realEvents...)
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.Before(all[j].CreatedAt) })
	return all, nil
}

// PerformRCA triggers an RCA investigation via the Python orchestrator, or falls back to
// the Go AI service if the orchestrator URL is not configured.
func (s *IncidentService) PerformRCA(ctx context.Context, incidentID, userID uuid.UUID) (*ai.RCAResponse, error) {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "ai.use"); err != nil {
		return nil, err
	}

	// Get incident
	incident, err := s.GetIncident(ctx, incidentID, userID)
	if err != nil {
		return nil, err
	}

	// Prefer the dedicated Python RCA orchestrator.
	if s.rcaURL != "" {
		invID, err := s.triggerOrchestratorRCA(ctx, incident)
		if err != nil {
			return nil, fmt.Errorf("RCA orchestrator error: %w", err)
		}
		// Mark the incident as queued and return a lightweight response.
		s.db.ExecContext(ctx, `UPDATE incidents SET rca_status=$1, rca_investigation_id=$2, updated_at=$3 WHERE id=$4`,
			"queued", invID, time.Now(), incidentID)
		s.AddTimelineEvent(ctx, incidentID, userID, "rca_started", "RCA Investigation Started",
			fmt.Sprintf("Investigation %s queued in RCA orchestrator", invID), nil)
		return &ai.RCAResponse{
			RootCause:  "Investigation queued — check the RCA tab for live results",
			Confidence: 0,
		}, nil
	}

	// Fallback: use the local Go AI service.
	timeline, _ := s.GetIncidentTimeline(ctx, incidentID, userID)
	rcaReq := &ai.RCARequest{
		IncidentID:  incident.ID,
		Title:       incident.Title,
		Description: incident.Description,
		Timeline:    convertToAITimeline(timeline),
		Metadata:    map[string]interface{}{"severity": incident.Severity},
	}
	rca, err := s.aiService.PerformRCA(ctx, rcaReq)
	if err != nil {
		return nil, err
	}
	aiInsightsJSON, _ := json.Marshal(rca)
	aiRecommendationsJSON, _ := json.Marshal(rca.Recommendations)
	rcaStatus := "none"
	if rca.RootCause != "" {
		rcaStatus = "completed"
	}
	s.db.ExecContext(ctx, `UPDATE incidents SET ai_insights=$1, ai_root_cause=$2, ai_recommendations=$3, rca_status=$4, updated_at=$5 WHERE id=$6`,
		aiInsightsJSON, rca.RootCause, aiRecommendationsJSON, rcaStatus, time.Now(), incidentID)
	s.AddTimelineEvent(ctx, incidentID, userID, "ai_analysis", "AI Analysis Completed", rca.RootCause,
		map[string]interface{}{"confidence": rca.Confidence})
	return rca, nil
}

// triggerOrchestratorRCA POSTs to the Python RCA orchestrator and returns the investigation ID.
func (s *IncidentService) triggerOrchestratorRCA(ctx context.Context, incident *Incident) (string, error) {
	namespace, cluster, service := "", "", ""
	if len(incident.AffectedServicesNames) > 0 {
		service = incident.AffectedServicesNames[0]
	}

	// Reconstruct go_context from stored DB fields so the V2 orchestrator gets topology and
	// hypothesis data even for manually-triggered RCA (the pipeline auto-trigger already
	// sends the full context via triggerRCA in enrichIncidentV2).
	goContext := s.buildGoContextFromIncident(incident)

	body := map[string]interface{}{
		"alert_id":    incident.ID.String(),
		"alert_title": incident.Title,
		"alert_body": map[string]interface{}{
			"description":  incident.Description,
			"severity":     incident.Severity,
			"topology":     incident.TopologyPath,
			"blast_radius": incident.BlastRadius,
		},
		"severity":    incident.Severity,
		"incident_id": incident.ID.String(),
		"namespace":   namespace,
		"cluster":     cluster,
		"service":     service,
		"go_context":  goContext,
	}
	payload, _ := json.Marshal(body)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/investigations", s.rcaURL),
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		InvestigationID string `json:"investigation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.InvestigationID == "" {
		return "", fmt.Errorf("unexpected response from orchestrator (status %d)", resp.StatusCode)
	}
	return result.InvestigationID, nil
}

// buildGoContextFromIncident reconstructs a go_context map from stored incident DB columns.
// This lets the V2 orchestrator start with known topology context for manual RCA re-triggers.
func (s *IncidentService) buildGoContextFromIncident(incident *Incident) map[string]interface{} {
	goCtx := map[string]interface{}{
		"domain":            incident.OntologyDomain,
		"root_entity_id":    incident.TopoRootEntityID,
		"root_entity_label": incident.TopoRootEntityID,
		"blast_radius_size": len(incident.BlastRadius),
		"topo_depth":        0,
		"hypotheses":        []interface{}{},
		"topo_reasoning":    []string{incident.TopologyPath},
	}

	// Extract root entity label from causal chain's first link (from_label = root)
	if len(incident.CausalChain) > 0 {
		var chain []map[string]interface{}
		if json.Unmarshal(incident.CausalChain, &chain) == nil && len(chain) > 0 {
			if fromLabel, ok := chain[0]["from_label"].(string); ok && fromLabel != "" {
				goCtx["root_entity_label"] = fromLabel
			}
			if fromType, ok := chain[0]["edge_type"].(string); ok {
				goCtx["root_entity_type"] = fromType
			}
			goCtx["topo_depth"] = len(chain)
		}
	}

	// Pass stored probabilistic hypotheses to the V2 scorer
	if len(incident.RCAHypotheses) > 0 {
		var hyps []interface{}
		if json.Unmarshal(incident.RCAHypotheses, &hyps) == nil && len(hyps) > 0 {
			goCtx["hypotheses"] = hyps
		}
	}

	return goCtx
}

// AssignIncident assigns an incident to a user
func (s *IncidentService) AssignIncident(ctx context.Context, incidentID, assignToUserID, assignedByUserID uuid.UUID) error {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, assignedByUserID, "incidents.assign"); err != nil {
		return err
	}

	query := "UPDATE incidents SET assigned_to = $1, updated_at = $2 WHERE id = $3"
	result, err := s.db.ExecContext(ctx, query, assignToUserID, time.Now(), incidentID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIncidentNotFound
	}

	// Add assignment event
	s.AddTimelineEvent(ctx, incidentID, assignedByUserID, "assigned", "Incident Assigned",
		fmt.Sprintf("Assigned to user %s", assignToUserID), nil)

	return nil
}

// GetIncidentStats retrieves incident statistics
func (s *IncidentService) GetIncidentStats(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	// Check permission
	if err := s.rbacService.CheckPermission(ctx, userID, "incidents.view"); err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})

	// Total incidents
	var total int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidents").Scan(&total)
	stats["total"] = total

	// By severity
	bySeverity := make(map[string]int)
	if rows, err := s.db.QueryContext(ctx, "SELECT severity, COUNT(*) FROM incidents GROUP BY severity"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var severity string
			var count int
			if err := rows.Scan(&severity, &count); err == nil {
				bySeverity[severity] = count
			}
		}
	}
	stats["by_severity"] = bySeverity

	// By status
	byStatus := make(map[string]int)
	if rows2, err := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM incidents GROUP BY status"); err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var status string
			var count int
			if err := rows2.Scan(&status, &count); err == nil {
				byStatus[status] = count
			}
		}
	}
	stats["by_status"] = byStatus

	// MTTR (Mean Time To Resolution)
	var mttr float64
	s.db.QueryRowContext(ctx, `
		SELECT AVG(EXTRACT(EPOCH FROM (resolved_at - started_at))/3600)
		FROM incidents
		WHERE resolved_at IS NOT NULL
	`).Scan(&mttr)
	stats["mttr_hours"] = mttr

	return stats, nil
}

// GetOpenIncidentByCorrelationID returns the first open incident with the given correlation_id, or nil if none exists.
func (s *IncidentService) GetOpenIncidentByCorrelationID(ctx context.Context, correlationID string) (*Incident, error) {
	if correlationID == "" {
		return nil, nil
	}
	var incident Incident
	var alertIDsJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, severity, status, correlation_id, alert_ids
		FROM incidents
		WHERE correlation_id = $1 AND status NOT IN ('resolved','closed')
		ORDER BY created_at DESC LIMIT 1
	`, correlationID).Scan(
		&incident.ID, &incident.Title, &incident.Severity,
		&incident.Status, &incident.CorrelationID, &alertIDsJSON,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	json.Unmarshal(alertIDsJSON, &incident.AlertIDs)
	return &incident, nil
}

// AddAlertToIncident appends an alert UUID to an incident's alert_ids array and links the alert back to the incident.
func (s *IncidentService) AddAlertToIncident(ctx context.Context, incidentID, alertID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE incidents
		SET alert_ids  = CASE
		                   WHEN alert_ids @> $1::jsonb THEN alert_ids
		                   ELSE alert_ids || $1::jsonb
		                 END,
		    updated_at = NOW()
		WHERE id = $2
	`, fmt.Sprintf(`["%s"]`, alertID), incidentID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE alerts SET incident_id = $1, updated_at = NOW() WHERE id = $2
	`, incidentID, alertID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// AutoCloseResolvedIncidents finds all open incidents whose every linked alert
// is resolved, then marks those incidents resolved with an auto-close note.
// Returns the number of incidents that were closed.
func (s *IncidentService) AutoCloseResolvedIncidents(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, nil
	}

	// Find open incidents where alert_count > 0 and ALL alerts are resolved.
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id
		FROM incidents i
		WHERE i.status IN ('open', 'investigating', 'acknowledged')
		  AND jsonb_array_length(COALESCE(i.alert_ids, '[]')) > 0
		  AND NOT EXISTS (
		      SELECT 1 FROM alerts a
		      WHERE a.incident_id = i.id
		        AND a.status != 'resolved'
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}

	now := time.Now()
	closed := 0
	for _, id := range ids {
		res, err := s.db.ExecContext(ctx, `
			UPDATE incidents
			SET status = 'resolved',
			    resolved_at = $1,
			    closed_at   = $1,
			    resolution_notes = 'Auto-resolved: all correlated alerts resolved',
			    updated_at  = $1
			WHERE id = $2
			  AND status IN ('open', 'investigating', 'acknowledged')
		`, now, id)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			closed++
			s.AddTimelineEvent(ctx, id, uuid.Nil, "auto_resolved",
				"Incident Auto-Resolved",
				"All correlated alerts are resolved. Incident closed automatically.", nil)
		}
	}

	return closed, nil
}

// IncidentFilters represents incident filtering options
type IncidentFilters struct {
	Severity   []string
	Status     []string
	Priority   []string
	AssignedTo *uuid.UUID
	CreatedBy  *uuid.UUID
	Search     string
	StartDate  *time.Time
	EndDate    *time.Time
	Limit      int
	Offset     int
}

// Helper functions

func (s *IncidentService) analyzeIncidentAsync(incident *Incident) {
	ctx := context.Background()

	if s.rcaURL != "" {
		invID, err := s.triggerOrchestratorRCA(ctx, incident)
		if err != nil {
			fmt.Printf("RCA orchestrator trigger failed for incident %s: %v\n", incident.ID, err)
			return
		}
		s.db.ExecContext(ctx, `UPDATE incidents SET rca_status='queued', rca_investigation_id=$1, updated_at=$2 WHERE id=$3`,
			invID, time.Now(), incident.ID)
		return
	}

	// Fallback: local Go AI service.
	rcaReq := &ai.RCARequest{
		IncidentID:  incident.ID,
		Title:       incident.Title,
		Description: incident.Description,
		Metadata:    map[string]interface{}{"severity": incident.Severity},
	}
	rca, err := s.aiService.PerformRCA(ctx, rcaReq)
	if err != nil {
		return
	}
	aiInsightsJSON, _ := json.Marshal(rca)
	aiRecommendationsJSON, _ := json.Marshal(rca.Recommendations)
	rcaStatus := "none"
	if rca.RootCause != "" {
		rcaStatus = "completed"
	}
	s.db.ExecContext(ctx, `UPDATE incidents SET ai_insights=$1, ai_root_cause=$2, ai_recommendations=$3, rca_status=$4, updated_at=$5 WHERE id=$6`,
		aiInsightsJSON, rca.RootCause, aiRecommendationsJSON, rcaStatus, time.Now(), incident.ID)
}

func convertToAITimeline(events []TimelineEvent) []ai.TimelineEvent {
	var aiEvents []ai.TimelineEvent
	for _, event := range events {
		aiEvents = append(aiEvents, ai.TimelineEvent{
			Timestamp:   event.CreatedAt,
			EventType:   event.EventType,
			Description: event.Description,
			Metadata:    event.Metadata,
		})
	}
	return aiEvents
}
