package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

var (
	ErrAlertNotFound   = errors.New("alert not found")
	ErrInvalidSeverity = errors.New("invalid severity")
	ErrInvalidStatus   = errors.New("invalid status")
	ErrUnauthorized    = errors.New("unauthorized")
)

// Use the unified Alert model from shared package
type Alert = models.Alert
type LinkedAlert = models.LinkedAlert

// AlertService handles alert operations and implements interfaces.AlertService
type AlertService struct {
	db                 *sql.DB
	rbacService        interfaces.RBACService
	aiService          interfaces.AIService
	correlationService interfaces.CorrelationService
	registry           interfaces.ServiceRegistry
}

// NewAlertService creates a new alert service
func NewAlertService(registry interfaces.ServiceRegistry) *AlertService {
	db, ok := registry.GetDatabase().(*sql.DB)
	if !ok {
		panic("invalid database type")
	}

	return &AlertService{
		db:                 db,
		rbacService:        registry.GetRBACService(),
		aiService:          registry.GetAIService(),
		correlationService: registry.GetCorrelationService(),
		registry:           registry,
	}
}

func (s *AlertService) checkPerm(ctx context.Context, userID uuid.UUID, perm string) error {
	if s.rbacService == nil {
		return nil
	}
	return s.checkPerm(ctx, userID, perm)
}

// CreateAlert creates a new alert
func (s *AlertService) CreateAlert(ctx context.Context, alert *Alert, userID uuid.UUID) error {
	// Check permission (skip if it's a zero UUID - webhook/system user)
	if userID != uuid.Nil && userID.String() != "00000000-0000-0000-0000-000000000001" {
		if err := s.checkPerm(ctx, userID, "alerts.create"); err != nil {
			return err
		}
	}

	alert.ID = uuid.New()
	// Only set created_by if it's a real user (not webhook/system user)
	if userID != uuid.Nil && userID.String() != "00000000-0000-0000-0000-000000000001" {
		alert.CreatedBy = &userID
	}
	alert.CreatedAt = time.Now()
	alert.UpdatedAt = time.Now()
	alert.FirstSeenAt = time.Now()
	alert.LastSeenAt = time.Now()
	alert.Count = 1
	alert.IsAlertActive = true  // New alerts are active by default
	alert.MaintenanceStatus = 0 // Not in maintenance by default

	// Only generate a weak source:title:severity fingerprint when the normalizer hasn't
	// already set a richer one (e.g. per-ProblemID hash for Dynatrace alerts).
	if alert.Fingerprint == "" {
		alert.Fingerprint = s.generateFingerprint(alert)
	}

	// Marshal JSON fields — log on failure; zero-value JSON is safe to store
	tagsJSON, err := json.Marshal(alert.Tags)
	if err != nil {
		log.Printf("alerts: failed to marshal tags for alert %s: %v", alert.ID, err)
		tagsJSON = []byte("[]")
	}
	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		log.Printf("alerts: failed to marshal labels for alert %s: %v", alert.ID, err)
		labelsJSON = []byte("{}")
	}
	metadataJSON, err := json.Marshal(alert.Metadata)
	if err != nil {
		log.Printf("alerts: failed to marshal metadata for alert %s: %v", alert.ID, err)
		metadataJSON = []byte("{}")
	}
	linkedAlertsJSON, err := json.Marshal(alert.LinkedAlerts)
	if err != nil {
		log.Printf("alerts: failed to marshal linked_alerts for alert %s: %v", alert.ID, err)
		linkedAlertsJSON = []byte("[]")
	}
	linkedToJSON, err := json.Marshal(alert.LinkedTo)
	if err != nil {
		log.Printf("alerts: failed to marshal linked_to for alert %s: %v", alert.ID, err)
		linkedToJSON = []byte("[]")
	}

	// ON CONFLICT: when a webhook re-fires the same source+source_id, update the
	// existing row rather than inserting a duplicate.  The RETURNING clause gives
	// us the canonical ID (which may be the pre-existing row's ID on conflict).
	query := `
		INSERT INTO alerts (
			id, title, description, severity, status, source, source_id, source_url,
			tags, labels, metadata, assigned_to, created_by, team_id, fingerprint,
			count, first_seen_at, last_seen_at, is_alert_active, maintenance_status,
			linked_alerts, linked_to, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		ON CONFLICT (source, source_id) WHERE source_id IS NOT NULL AND source_id != ''
		DO UPDATE SET
			title        = EXCLUDED.title,
			description  = EXCLUDED.description,
			severity     = EXCLUDED.severity,
			-- Only reopen a resolved alert when the incoming event is a fresh OPEN, not another RESOLVED.
			status       = CASE WHEN alerts.status = 'resolved' AND EXCLUDED.status != 'resolved' THEN 'open' ELSE EXCLUDED.status END,
			-- Always stamp resolved_at when resolving; always clear it when reopening so the
			-- next resolution cycle gets a fresh timestamp (avoids stale T1 on second occurrence).
			resolved_at  = CASE
				WHEN EXCLUDED.status = 'resolved' THEN NOW()
				WHEN alerts.status = 'resolved' AND EXCLUDED.status != 'resolved' THEN NULL
				ELSE alerts.resolved_at
			END,
			labels       = EXCLUDED.labels,
			tags         = EXCLUDED.tags,
			metadata     = EXCLUDED.metadata,
			last_seen_at = EXCLUDED.last_seen_at,
			count        = COALESCE(alerts.count, 0) + 1,
			updated_at   = EXCLUDED.updated_at
		RETURNING id
	`

	var returnedID uuid.UUID
	if err := s.db.QueryRowContext(ctx, query,
		alert.ID, alert.Title, alert.Description, alert.Severity, alert.Status,
		alert.Source, alert.SourceID, alert.SourceURL, tagsJSON, labelsJSON, metadataJSON,
		alert.AssignedTo, alert.CreatedBy, alert.TeamID, alert.Fingerprint,
		alert.Count, alert.FirstSeenAt, alert.LastSeenAt, alert.IsAlertActive, alert.MaintenanceStatus,
		linkedAlertsJSON, linkedToJSON, alert.CreatedAt, alert.UpdatedAt,
	).Scan(&returnedID); err != nil {
		return err
	}
	// Overwrite with the canonical DB row ID (differs from alert.ID when a conflict occurred).
	alert.ID = returnedID

	// Trigger AI analysis asynchronously
	go s.analyzeAlertAsync(alert)

	return nil
}

// GetAlert retrieves an alert by ID
func (s *AlertService) GetAlert(ctx context.Context, alertID, userID uuid.UUID) (*Alert, error) {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.view"); err != nil {
		return nil, err
	}

	alert := &Alert{}

	query := `
		SELECT a.id, a.title, a.description, a.severity, a.status, a.source, a.source_id, a.source_url,
		       a.tags, a.labels, a.metadata, a.assigned_to, a.created_by, a.team_id,
		       a.ai_analysis, a.ai_classification, a.ai_confidence, a.fingerprint, a.count,
		       a.first_seen_at, a.last_seen_at, a.acknowledged_at, a.acknowledged_by,
		       a.resolved_at, a.resolved_by, a.resolution_notes, a.created_at, a.updated_at,
		       u1.full_name as assigned_to_name, u2.full_name as created_by_name
		FROM alerts a
		LEFT JOIN users u1 ON a.assigned_to = u1.id
		LEFT JOIN users u2 ON a.created_by = u2.id
		WHERE a.id = $1
	`

	var tagsJSON, labelsJSON, metadataJSON, aiAnalysisJSON []byte

	err := s.db.QueryRowContext(ctx, query, alertID).Scan(
		&alert.ID, &alert.Title, &alert.Description, &alert.Severity, &alert.Status,
		&alert.Source, &alert.SourceID, &alert.SourceURL, &tagsJSON, &labelsJSON, &metadataJSON,
		&alert.AssignedTo, &alert.CreatedBy, &alert.TeamID, &aiAnalysisJSON,
		&alert.AIClassification, &alert.AIConfidence, &alert.Fingerprint, &alert.Count,
		&alert.FirstSeenAt, &alert.LastSeenAt, &alert.AcknowledgedAt, &alert.AcknowledgedBy,
		&alert.ResolvedAt, &alert.ResolvedBy, &alert.ResolutionNotes, &alert.CreatedAt, &alert.UpdatedAt,
		&alert.AssignedToName, &alert.CreatedByName,
	)

	if err == sql.ErrNoRows {
		return nil, ErrAlertNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	json.Unmarshal(tagsJSON, &alert.Tags)
	json.Unmarshal(labelsJSON, &alert.Labels)
	json.Unmarshal(metadataJSON, &alert.Metadata)
	if aiAnalysisJSON != nil {
		json.Unmarshal(aiAnalysisJSON, &alert.AIAnalysis)
	}

	return alert, nil
}

// ListAlerts retrieves alerts with filtering and pagination
func (s *AlertService) ListAlerts(ctx context.Context, userID uuid.UUID, filters AlertFilters) ([]*Alert, int, error) {
	// Skip permission check for now - allow everyone to view alerts
	// if err := s.checkPerm(ctx, userID, "alerts.view"); err != nil {
	// 	return nil, 0, err
	// }

	query, args := s.buildListQuery(filters, userID)

	whereClause, countArgs := s.buildWhereClause(filters, userID)
	countQuery := "SELECT COUNT(*) FROM alerts WHERE 1=1" + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		log.Printf("warn: count query failed: %v", err)
	}

	// Execute query
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var alerts []*Alert
	for rows.Next() {
		alert := &Alert{}
		var tagsJSON, metadataJSON, acknowledgedByName, linkedAlertsJSON, linkedToJSON string
		var resolvedByName string
		var corrID string
		var corrConf float64
		var incidentID sql.NullString

		err := rows.Scan(
			&alert.ID, &alert.Title, &alert.Description, &alert.Severity, &alert.Status,
			&alert.Source, &alert.SourceID, &alert.SourceURL,
			&tagsJSON, &metadataJSON,
			&alert.AssignedTo, &alert.CreatedBy,
			&alert.AIClassification, &alert.AIConfidence, &alert.Count,
			&alert.FirstSeenAt, &alert.LastSeenAt, &alert.CreatedAt, &alert.UpdatedAt,
			&alert.AssignedToName, &alert.CreatedByName,
			&alert.AcknowledgedBy, &acknowledgedByName,
			&alert.IsAlertActive, &alert.MaintenanceStatus,
			&alert.SLAMetResponseTime, &alert.SLAMetResolutionTime,
			&linkedAlertsJSON, &linkedToJSON,
			&alert.ResolutionType, &alert.ResolvedBy, &resolvedByName,
			&corrID, &corrConf, &incidentID,
		)
		if err != nil {
			return nil, 0, err
		}

		// Parse metadata
		if metadataJSON != "" && metadataJSON != "{}" {
			json.Unmarshal([]byte(metadataJSON), &alert.Metadata)
		}

		// Store acknowledged_by_name directly on Alert struct for easy frontend access
		if acknowledgedByName != "" {
			alert.AcknowledgedByName = acknowledgedByName
			// Also store in metadata as fallback
			if alert.Metadata == nil {
				alert.Metadata = make(map[string]interface{})
			}
			alert.Metadata["acknowledged_by_name"] = acknowledgedByName
		}

		// Store resolved_by_name and resolution_type in metadata for frontend
		if resolvedByName != "" {
			if alert.Metadata == nil {
				alert.Metadata = make(map[string]interface{})
			}
			alert.Metadata["resolved_by_name"] = resolvedByName
			alert.ResolvedByName = resolvedByName
		}
		if alert.ResolutionType != "" {
			if alert.Metadata == nil {
				alert.Metadata = make(map[string]interface{})
			}
			alert.Metadata["resolution_type"] = alert.ResolutionType
		}

		// Set correlation and incident fields from new columns
		alert.CorrelationID = corrID
		if corrConf > 0 {
			alert.CorrelationScore = &corrConf
		}
		if incidentID.Valid && incidentID.String != "" {
			if parsed, err := uuid.Parse(incidentID.String); err == nil {
				alert.LinkedIncidentID = &parsed
			}
		}

		if tagsJSON != "" && tagsJSON != "[]" {
			json.Unmarshal([]byte(tagsJSON), &alert.Tags)
		}
		if alert.Tags == nil {
			alert.Tags = []string{}
		}

		// Parse linked alerts
		if linkedAlertsJSON != "" && linkedAlertsJSON != "null" {
			json.Unmarshal([]byte(linkedAlertsJSON), &alert.LinkedAlerts)
		}
		if linkedToJSON != "" && linkedToJSON != "null" {
			json.Unmarshal([]byte(linkedToJSON), &alert.LinkedTo)
		}

		alerts = append(alerts, alert)
	}

	return alerts, total, rows.Err()
}

// UpdateAlert updates an alert
func (s *AlertService) UpdateAlert(ctx context.Context, alert *Alert, userID uuid.UUID) error {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.update"); err != nil {
		return err
	}

	alert.UpdatedAt = time.Now()

	tagsJSON, _ := json.Marshal(alert.Tags)
	labelsJSON, _ := json.Marshal(alert.Labels)
	metadataJSON, _ := json.Marshal(alert.Metadata)

	query := `
		UPDATE alerts
		SET title = $1, description = $2, severity = $3, status = $4,
		    tags = $5, labels = $6, metadata = $7, assigned_to = $8,
		    updated_at = $9
		WHERE id = $10
	`

	result, err := s.db.ExecContext(ctx, query,
		alert.Title, alert.Description, alert.Severity, alert.Status,
		tagsJSON, labelsJSON, metadataJSON, alert.AssignedTo, alert.UpdatedAt, alert.ID,
	)

	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	// Log to alert history
	s.logAlertHistory(ctx, alert.ID, userID, "updated", nil, nil)

	return nil
}

// AssignAlert assigns an alert to a user
func (s *AlertService) AssignAlert(ctx context.Context, alertID, assignToUserID, assignedByUserID uuid.UUID) error {
	// Check permission
	if err := s.checkPerm(ctx, assignedByUserID, "alerts.assign"); err != nil {
		return err
	}

	query := "UPDATE alerts SET assigned_to = $1, updated_at = $2 WHERE id = $3"
	result, err := s.db.ExecContext(ctx, query, assignToUserID, time.Now(), alertID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	// Log assignment
	s.logAlertHistory(ctx, alertID, assignedByUserID, "assigned", nil, map[string]interface{}{
		"assigned_to": assignToUserID,
	})

	return nil
}

// ResolveAlert resolves an alert
func (s *AlertService) ResolveAlert(ctx context.Context, alertID, userID uuid.UUID, notes string) error {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.resolve"); err != nil {
		return err
	}

	now := time.Now()
	query := `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $1, resolved_by = $2, resolution_notes = $3,
		    resolution_type = 'manual', updated_at = $4
		WHERE id = $5
	`

	result, err := s.db.ExecContext(ctx, query, now, userID, notes, now, alertID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	// Log resolution
	s.logAlertHistory(ctx, alertID, userID, "resolved", nil, map[string]interface{}{
		"notes":           notes,
		"resolution_type": "manual",
	})

	return nil
}

// AcknowledgeAlert acknowledges an alert
func (s *AlertService) AcknowledgeAlert(ctx context.Context, alertID, userID uuid.UUID) error {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.update"); err != nil {
		return err
	}

	now := time.Now()
	query := `
		UPDATE alerts
		SET status = 'acknowledged', acknowledged_at = $1, acknowledged_by = $2, updated_at = $3
		WHERE id = $4
	`

	result, err := s.db.ExecContext(ctx, query, now, userID, now, alertID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	// Log acknowledgment
	s.logAlertHistory(ctx, alertID, userID, "acknowledged", nil, nil)

	return nil
}

// DeleteAlert deletes an alert
func (s *AlertService) DeleteAlert(ctx context.Context, alertID, userID uuid.UUID) error {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.delete"); err != nil {
		return err
	}

	query := "DELETE FROM alerts WHERE id = $1"
	result, err := s.db.ExecContext(ctx, query, alertID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	return nil
}

// GetAlertBySourceID retrieves an alert by source and source_id
// Prioritizes finding open/acknowledged alerts over resolved ones
func (s *AlertService) GetAlertBySourceID(ctx context.Context, source, sourceID string) (*Alert, error) {
	alert := &Alert{}

	// Simplified query to avoid JOIN issues
	query := `
		SELECT a.id, a.title, COALESCE(a.description, ''), a.severity, a.status, a.source, a.source_id, COALESCE(a.source_url, ''),
		       COALESCE(a.tags, '[]'), COALESCE(a.labels, '{}'), COALESCE(a.metadata, '{}'),
		       a.assigned_to, a.created_by, a.team_id,
		       a.ai_analysis, COALESCE(a.ai_classification, ''), COALESCE(a.ai_confidence, 0),
		       COALESCE(a.fingerprint, ''), COALESCE(a.count, 1),
		       COALESCE(a.first_seen_at, a.created_at), COALESCE(a.last_seen_at, a.created_at),
		       a.acknowledged_at, a.acknowledged_by,
		       a.resolved_at, a.resolved_by, COALESCE(a.resolution_notes, ''),
		       a.created_at, a.updated_at
		FROM alerts a
		WHERE a.source = $1 AND a.source_id = $2
		ORDER BY
			CASE
				WHEN a.status = 'open' THEN 1
				WHEN a.status = 'acknowledged' THEN 2
				ELSE 3
			END,
			a.created_at DESC
		LIMIT 1
	`

	var tagsJSON, labelsJSON, metadataJSON, aiAnalysisJSON sql.NullString

	err := s.db.QueryRowContext(ctx, query, source, sourceID).Scan(
		&alert.ID, &alert.Title, &alert.Description, &alert.Severity, &alert.Status,
		&alert.Source, &alert.SourceID, &alert.SourceURL, &tagsJSON, &labelsJSON, &metadataJSON,
		&alert.AssignedTo, &alert.CreatedBy, &alert.TeamID, &aiAnalysisJSON,
		&alert.AIClassification, &alert.AIConfidence, &alert.Fingerprint, &alert.Count,
		&alert.FirstSeenAt, &alert.LastSeenAt, &alert.AcknowledgedAt, &alert.AcknowledgedBy,
		&alert.ResolvedAt, &alert.ResolvedBy, &alert.ResolutionNotes, &alert.CreatedAt, &alert.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrAlertNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	if tagsJSON.Valid && tagsJSON.String != "" {
		json.Unmarshal([]byte(tagsJSON.String), &alert.Tags)
	}
	if labelsJSON.Valid && labelsJSON.String != "" {
		json.Unmarshal([]byte(labelsJSON.String), &alert.Labels)
	}
	if metadataJSON.Valid && metadataJSON.String != "" {
		json.Unmarshal([]byte(metadataJSON.String), &alert.Metadata)
	}
	if aiAnalysisJSON.Valid && aiAnalysisJSON.String != "" {
		json.Unmarshal([]byte(aiAnalysisJSON.String), &alert.AIAnalysis)
	}

	return alert, nil
}

// ResolveAlertBySourceID resolves an alert by source and source_id (for webhook auto-resolution)
func (s *AlertService) ResolveAlertBySourceID(ctx context.Context, source, sourceID, notes string) error {
	now := time.Now()
	query := `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $1, resolution_notes = $2, resolution_type = 'auto', updated_at = $3
		WHERE source = $4 AND source_id = $5 AND status != 'resolved'
	`

	result, err := s.db.ExecContext(ctx, query, now, notes, now, source, sourceID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	// Log resolution (fetch alert ID for logging)
	var alertID uuid.UUID
	s.db.QueryRowContext(ctx, "SELECT id FROM alerts WHERE source = $1 AND source_id = $2 ORDER BY created_at DESC LIMIT 1", source, sourceID).Scan(&alertID)
	if alertID != uuid.Nil {
		s.logAlertHistory(ctx, alertID, uuid.Nil, "auto_resolved", nil, map[string]interface{}{
			"notes":           notes,
			"source":          source,
			"resolution_type": "auto",
		})
	}

	return nil
}

// UpdateAlertBySourceID updates an alert using its Alert object
func (s *AlertService) UpdateAlertBySourceID(ctx context.Context, alert *Alert) error {
	alert.UpdatedAt = time.Now()

	tagsJSON, _ := json.Marshal(alert.Tags)
	labelsJSON, _ := json.Marshal(alert.Labels)
	metadataJSON, _ := json.Marshal(alert.Metadata)

	query := `
		UPDATE alerts
		SET title = $1, description = $2, severity = $3, status = $4,
		    tags = $5, labels = $6, metadata = $7, updated_at = $8
		WHERE id = $9
	`

	result, err := s.db.ExecContext(ctx, query,
		alert.Title, alert.Description, alert.Severity, alert.Status,
		tagsJSON, labelsJSON, metadataJSON, alert.UpdatedAt, alert.ID,
	)

	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAlertNotFound
	}

	// Log to alert history
	s.logAlertHistory(ctx, alert.ID, uuid.Nil, "updated_by_webhook", nil, map[string]interface{}{
		"status": alert.Status,
		"source": alert.Source,
	})

	return nil
}

// AnalyzeAlertWithAI triggers AI analysis for an alert
func (s *AlertService) AnalyzeAlertWithAI(ctx context.Context, alertID, userID uuid.UUID) (*interfaces.AlertAnalysisResponse, error) {
	// Check permission
	if err := s.checkPerm(ctx, userID, "ai.use"); err != nil {
		return nil, err
	}

	// Get alert
	alert, err := s.GetAlert(ctx, alertID, userID)
	if err != nil {
		return nil, err
	}

	// Prepare AI request using interface types
	aiReq := &interfaces.AlertAnalysisRequest{
		AlertID:     alert.ID,
		Title:       alert.Title,
		Description: alert.Description,
		Severity:    alert.Severity,
		Source:      alert.Source,
		Tags:        alert.Tags,
		Metadata:    alert.Metadata,
	}

	// Call AI service
	if s.aiService == nil {
		return nil, fmt.Errorf("AI service not available")
	}

	analysis, err := s.aiService.AnalyzeAlert(ctx, aiReq)
	if err != nil {
		return nil, err
	}

	// Store AI analysis
	analysisJSON, _ := json.Marshal(analysis)
	query := `
		UPDATE alerts
		SET ai_analysis = $1, ai_classification = $2, ai_confidence = $3, updated_at = $4
		WHERE id = $5
	`

	if _, err := s.db.ExecContext(ctx, query, analysisJSON, analysis.Classification, analysis.Confidence, time.Now(), alertID); err != nil {
		log.Printf("alerts: failed to store AI analysis for alert %s: %v", alertID, err)
	}

	// Log AI analysis
	s.logAlertHistory(ctx, alertID, userID, "ai_analyzed", nil, map[string]interface{}{
		"classification": analysis.Classification,
		"confidence":     analysis.Confidence,
	})

	return analysis, nil
}

// AlertFilters represents alert filtering options
type AlertFilters struct {
	Severity   []string
	Status     []string
	Source     []string
	AssignedTo *uuid.UUID
	CreatedBy  *uuid.UUID
	Tags       []string
	Search     string
	StartDate  *time.Time
	EndDate    *time.Time
	Limit      int
	Offset     int
	SortBy     string
	SortOrder  string
}

// Helper functions

func (s *AlertService) buildListQuery(filters AlertFilters, userID uuid.UUID) (string, []interface{}) {
	query := `
		SELECT a.id, a.title, COALESCE(a.description, ''), a.severity, a.status,
		       COALESCE(a.source, ''), COALESCE(a.source_id, ''), COALESCE(a.source_url, ''),
		       COALESCE(a.tags::text, '[]')::text,
		       COALESCE(a.metadata::text, '{}')::text,
		       a.assigned_to, a.created_by,
		       COALESCE(a.ai_classification, ''), COALESCE(a.ai_confidence, 0),
		       COALESCE(a.count, 1),
		       COALESCE(a.first_seen_at, a.created_at),
		       COALESCE(a.last_seen_at, a.created_at),
		       a.created_at, a.updated_at,
		       COALESCE((SELECT full_name FROM users WHERE id = a.assigned_to LIMIT 1), '') as assigned_to_name,
		       COALESCE((SELECT full_name FROM users WHERE id = a.created_by LIMIT 1), '') as created_by_name,
		       a.acknowledged_by,
		       COALESCE((SELECT full_name FROM users WHERE id = a.acknowledged_by LIMIT 1), '') as acknowledged_by_name,
		       COALESCE(a.is_alert_active, TRUE) as is_alert_active,
		       COALESCE(a.maintenance_status, 0) as maintenance_status,
		       COALESCE(a.sla_met_responsetime, FALSE) as sla_met_responsetime,
		       COALESCE(a.sla_met_resolutiontime, FALSE) as sla_met_resolutiontime,
		       COALESCE(a.linked_alerts::text, 'null') as linked_alerts,
		       COALESCE(a.linked_to::text, 'null') as linked_to,
		       COALESCE(a.resolution_type, '') as resolution_type,
		       a.resolved_by,
		       COALESCE((SELECT full_name FROM users WHERE id = a.resolved_by LIMIT 1), '') as resolved_by_name,
		       COALESCE(a.correlation_id, '') as correlation_id,
		       COALESCE(a.correlation_confidence::float8, 0) as correlation_confidence,
		       a.incident_id
		FROM alerts a
		WHERE 1=1
	`

	args := []interface{}{}
	argCount := 1

	if len(filters.Severity) > 0 {
		query += fmt.Sprintf(" AND a.severity = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Severity))
		argCount++
	}

	if len(filters.Status) > 0 {
		query += fmt.Sprintf(" AND a.status = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Status))
		argCount++
	}

	if len(filters.Source) > 0 {
		query += fmt.Sprintf(" AND a.source = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Source))
		argCount++
	}

	if filters.AssignedTo != nil {
		query += fmt.Sprintf(" AND a.assigned_to = $%d", argCount)
		args = append(args, *filters.AssignedTo)
		argCount++
	}

	if filters.Search != "" {
		query += fmt.Sprintf(" AND (a.title ILIKE $%d OR a.description ILIKE $%d OR a.source_id ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+filters.Search+"%")
		argCount++
	}

	if filters.StartDate != nil {
		query += fmt.Sprintf(" AND a.created_at >= $%d", argCount)
		args = append(args, *filters.StartDate)
		argCount++
	}

	if filters.EndDate != nil {
		query += fmt.Sprintf(" AND a.created_at <= $%d", argCount)
		args = append(args, *filters.EndDate)
		argCount++
	}

	allowedSortColumns := map[string]bool{
		"created_at": true, "updated_at": true, "severity": true,
		"status": true, "source": true, "title": true,
		"first_seen_at": true, "last_seen_at": true, "count": true,
	}
	sortBy := "created_at"
	if filters.SortBy != "" && allowedSortColumns[filters.SortBy] {
		sortBy = filters.SortBy
	}
	sortOrder := "DESC"
	if filters.SortOrder == "ASC" {
		sortOrder = "ASC"
	}
	query += fmt.Sprintf(" ORDER BY a.%s %s", sortBy, sortOrder)

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount, argCount+1)
		args = append(args, filters.Limit, filters.Offset)
	}

	return query, args
}

func (s *AlertService) buildWhereClause(filters AlertFilters, userID uuid.UUID) (string, []interface{}) {
	where := ""
	args := []interface{}{}
	argCount := 1

	if len(filters.Severity) > 0 {
		where += fmt.Sprintf(" AND severity = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Severity))
		argCount++
	}

	if len(filters.Status) > 0 {
		where += fmt.Sprintf(" AND status = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Status))
		argCount++
	}

	if len(filters.Source) > 0 {
		where += fmt.Sprintf(" AND source = ANY($%d)", argCount)
		args = append(args, pq.Array(filters.Source))
		argCount++
	}

	if filters.Search != "" {
		where += fmt.Sprintf(" AND (title ILIKE $%d OR description ILIKE $%d OR source_id ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+filters.Search+"%")
		argCount++
	}

	if filters.StartDate != nil {
		where += fmt.Sprintf(" AND created_at >= $%d", argCount)
		args = append(args, *filters.StartDate)
		argCount++
	}

	if filters.EndDate != nil {
		where += fmt.Sprintf(" AND created_at <= $%d", argCount)
		args = append(args, *filters.EndDate)
		argCount++
	}

	return where, args
}

func (s *AlertService) generateFingerprint(alert *Alert) string {
	// Generate unique fingerprint for deduplication
	// In production, use a proper hashing algorithm
	return fmt.Sprintf("%s:%s:%s", alert.Source, alert.Title, alert.Severity)
}

func (s *AlertService) logAlertHistory(ctx context.Context, alertID, userID uuid.UUID, action string, oldValue, newValue map[string]interface{}) {
	oldJSON, err := json.Marshal(oldValue)
	if err != nil {
		log.Printf("alerts: failed to marshal old_value for history alert %s action=%s: %v", alertID, action, err)
		oldJSON = []byte("{}")
	}
	newJSON, err := json.Marshal(newValue)
	if err != nil {
		log.Printf("alerts: failed to marshal new_value for history alert %s action=%s: %v", alertID, action, err)
		newJSON = []byte("{}")
	}

	query := `
		INSERT INTO alert_history (id, alert_id, user_id, action, old_value, new_value, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	var userIDArg interface{}
	if userID != uuid.Nil {
		userIDArg = userID
	}

	if _, err := s.db.ExecContext(ctx, query, uuid.New(), alertID, userIDArg, action, oldJSON, newJSON, time.Now()); err != nil {
		log.Printf("alerts: failed to log alert history for alert %s action=%s: %v", alertID, action, err)
	}
}

func (s *AlertService) analyzeAlertAsync(alert *Alert) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if s.aiService == nil {
		return // AI service not available
	}

	aiReq := &interfaces.AlertAnalysisRequest{
		AlertID:     alert.ID,
		Title:       alert.Title,
		Description: alert.Description,
		Severity:    alert.Severity,
		Source:      alert.Source,
		Tags:        alert.Tags,
		Metadata:    alert.Metadata,
	}

	analysis, err := s.aiService.AnalyzeAlert(ctx, aiReq)
	if err != nil {
		return
	}

	// Store analysis
	analysisJSON, _ := json.Marshal(analysis)
	query := `
		UPDATE alerts
		SET ai_analysis = $1, ai_classification = $2, ai_confidence = $3, updated_at = $4
		WHERE id = $5
	`

	if _, err := s.db.ExecContext(ctx, query, analysisJSON, analysis.Classification, analysis.Confidence, time.Now(), alert.ID); err != nil {
		log.Printf("alerts: failed to store async AI analysis for alert %s: %v", alert.ID, err)
	}
}


// callIncidentUpdateAPI adds an alert to an existing incident and links the alert back.
func (s *AlertService) callIncidentUpdateAPI(ctx context.Context, incidentID string, alertID string) error {
	newAlertJSON, _ := json.Marshal([]string{alertID})
	// Use @> containment guard to prevent the same alert ID being appended more than once.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE incidents
		SET alert_ids = COALESCE(alert_ids::jsonb, '[]'::jsonb) || $1::jsonb,
		    updated_at = NOW()
		WHERE id = $2
		  AND NOT (COALESCE(alert_ids::jsonb, '[]'::jsonb) @> $1::jsonb)
	`, newAlertJSON, incidentID); err != nil {
		return err
	}
	// Keep alert.incident_id in sync — without this the alert appears unlinked.
	_, err := s.db.ExecContext(ctx, `
		UPDATE alerts SET incident_id = $1, updated_at = NOW() WHERE id = $2
	`, incidentID, alertID)
	return err
}

// GetCorrelatedAlerts retrieves correlated alerts for a given alert
func (s *AlertService) GetCorrelatedAlerts(ctx context.Context, alertID, userID uuid.UUID) ([]*Alert, error) {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.view"); err != nil {
		return nil, err
	}

	// Get correlation result
	if s.correlationService == nil {
		return []*Alert{}, nil // No correlation service available
	}

	result, err := s.correlationService.GetCorrelationResult(ctx, alertID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return []*Alert{}, nil
	}

	// Get similar alerts
	if len(result.SimilarAlerts) == 0 {
		return []*Alert{}, nil
	}

	// Convert UUIDs to interface{} for the query
	alertIDs := make([]interface{}, len(result.SimilarAlerts))
	for i, id := range result.SimilarAlerts {
		alertIDs[i] = id
	}

	// Build query to fetch similar alerts
	query := `
		SELECT id, title, description, severity, status, source, created_at, updated_at
		FROM alerts
		WHERE id = ANY($1)
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(result.SimilarAlerts))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []*Alert
	for rows.Next() {
		alert := &Alert{}
		err := rows.Scan(
			&alert.ID, &alert.Title, &alert.Description, &alert.Severity,
			&alert.Status, &alert.Source, &alert.CreatedAt, &alert.UpdatedAt,
		)
		if err != nil {
			continue
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// GetAlertStats retrieves alert statistics
func (s *AlertService) GetAlertStats(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	// Check permission
	if err := s.checkPerm(ctx, userID, "alerts.view"); err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})

	// Total alerts
	var total int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts").Scan(&total)
	stats["total"] = total

	// By severity
	rows, _ := s.db.QueryContext(ctx, "SELECT severity, COUNT(*) FROM alerts GROUP BY severity")
	defer rows.Close()

	bySeverity := make(map[string]int)
	for rows.Next() {
		var severity string
		var count int
		rows.Scan(&severity, &count)
		bySeverity[severity] = count
	}
	stats["by_severity"] = bySeverity

	// By status
	rows2, _ := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM alerts GROUP BY status")
	defer rows2.Close()

	byStatus := make(map[string]int)
	for rows2.Next() {
		var status string
		var count int
		rows2.Scan(&status, &count)
		byStatus[status] = count
	}
	stats["by_status"] = byStatus

	return stats, nil
}

// Interface implementation methods for compatibility with unified handlers

// List method for interface compliance (used by unified_handlers.go)
func (s *AlertService) List(ctx context.Context, filters map[string]interface{}) ([]*Alert, error) {
	// Convert map filters to AlertFilters struct
	alertFilters := AlertFilters{}

	if limit, ok := filters["limit"].(int); ok {
		alertFilters.Limit = limit
	}
	if offset, ok := filters["offset"].(int); ok {
		alertFilters.Offset = offset
	}
	if search, ok := filters["search"].(string); ok {
		alertFilters.Search = search
	}
	if severity, ok := filters["severity"].([]string); ok {
		alertFilters.Severity = severity
	}
	if status, ok := filters["status"].([]string); ok {
		alertFilters.Status = status
	}

	// Use admin user for interface calls
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	alerts, _, err := s.ListAlerts(ctx, adminID, alertFilters)
	return alerts, err
}

// Get method for interface compliance
func (s *AlertService) Get(ctx context.Context, id uuid.UUID) (*Alert, error) {
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	return s.GetAlert(ctx, id, adminID)
}

// Create method for interface compliance
func (s *AlertService) Create(ctx context.Context, alert *Alert) (*Alert, error) {
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	err := s.CreateAlert(ctx, alert, adminID)
	return alert, err
}

// Update method for interface compliance
func (s *AlertService) Update(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error {
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	// Get existing alert
	alert, err := s.GetAlert(ctx, id, adminID)
	if err != nil {
		return err
	}

	// Apply updates
	if title, ok := updates["title"].(string); ok {
		alert.Title = title
	}
	if description, ok := updates["description"].(string); ok {
		alert.Description = description
	}
	if severity, ok := updates["severity"].(string); ok {
		alert.Severity = severity
	}
	if status, ok := updates["status"].(string); ok {
		alert.Status = status
	}
	if assignedTo, ok := updates["assigned_to"].(uuid.UUID); ok {
		alert.AssignedTo = &assignedTo
	}

	return s.UpdateAlert(ctx, alert, adminID)
}

// Delete method for interface compliance
func (s *AlertService) Delete(ctx context.Context, id uuid.UUID) error {
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	return s.DeleteAlert(ctx, id, adminID)
}

// Acknowledge method for interface compliance
func (s *AlertService) Acknowledge(ctx context.Context, id uuid.UUID, userID uuid.UUID, notes string) error {
	return s.AcknowledgeAlert(ctx, id, userID)
}

// Resolve method for interface compliance
func (s *AlertService) Resolve(ctx context.Context, id uuid.UUID, userID uuid.UUID, notes string) error {
	return s.ResolveAlert(ctx, id, userID, notes)
}

// AnalyzeAlert method for interface compliance
func (s *AlertService) AnalyzeAlert(ctx context.Context, id uuid.UUID) (*interfaces.AlertAnalysisResponse, error) {
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	return s.AnalyzeAlertWithAI(ctx, id, adminID)
}
