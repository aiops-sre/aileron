package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AuditLog represents an audit log entry
type AuditLog struct {
	ID           uuid.UUID              `json:"id"`
	UserID       *uuid.UUID             `json:"user_id,omitempty"`
	Username     string                 `json:"username,omitempty"`
	Action       string                 `json:"action"`
	ResourceType string                 `json:"resource_type"`
	ResourceID   *uuid.UUID             `json:"resource_id,omitempty"`
	OldValue     map[string]interface{} `json:"old_value,omitempty"`
	NewValue     map[string]interface{} `json:"new_value,omitempty"`
	Details      map[string]interface{} `json:"details,omitempty"`
	IPAddress    string                 `json:"ip_address"`
	UserAgent    string                 `json:"user_agent"`
	Status       string                 `json:"status"`
	ErrorMessage string                 `json:"error_message,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
}

// AuditService handles audit logging
type AuditService struct {
	db *sql.DB
}

// NewAuditService creates a new audit service
func NewAuditService(db *sql.DB) *AuditService {
	return &AuditService{db: db}
}

// Log creates an audit log entry
func (s *AuditService) Log(ctx context.Context, log *AuditLog) error {
	log.ID = uuid.New()
	log.CreatedAt = time.Now()

	oldValueJSON, _ := json.Marshal(log.OldValue)
	newValueJSON, _ := json.Marshal(log.NewValue)
	detailsJSON, _ := json.Marshal(log.Details)

	query := `
		INSERT INTO audit_logs (
			id, user_id, action, resource_type, resource_id, old_value, new_value,
			details, ip_address, user_agent, status, error_message, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err := s.db.ExecContext(ctx, query,
		log.ID, log.UserID, log.Action, log.ResourceType, log.ResourceID,
		oldValueJSON, newValueJSON, detailsJSON, log.IPAddress, log.UserAgent,
		log.Status, log.ErrorMessage, log.CreatedAt,
	)

	return err
}

// ListAuditLogs retrieves audit logs with filtering
func (s *AuditService) ListAuditLogs(ctx context.Context, filters AuditFilters) ([]*AuditLog, int, error) {
	query := `
		SELECT a.id, a.user_id, a.action, COALESCE(a.resource_type, ''), a.resource_id,
		       a.old_value, a.new_value, a.details, COALESCE(a.ip_address, ''), COALESCE(a.user_agent, ''),
		       COALESCE(a.status, 'success'), COALESCE(a.error_message, ''), a.created_at, COALESCE(u.username, 'System')
		FROM audit_logs a
		LEFT JOIN users u ON a.user_id = u.id
		WHERE 1=1
	`

	args := []interface{}{}
	argCount := 1

	// Apply filters
	if filters.UserID != nil {
		query += " AND a.user_id = $" + fmt.Sprintf("%d", argCount)
		args = append(args, *filters.UserID)
		argCount++
	}

	if filters.Action != "" {
		query += " AND a.action = $" + fmt.Sprintf("%d", argCount)
		args = append(args, filters.Action)
		argCount++
	}

	if filters.ResourceType != "" {
		query += " AND a.resource_type = $" + fmt.Sprintf("%d", argCount)
		args = append(args, filters.ResourceType)
		argCount++
	}

	if filters.StartDate != nil {
		query += " AND a.created_at >= $" + fmt.Sprintf("%d", argCount)
		args = append(args, *filters.StartDate)
		argCount++
	}

	if filters.EndDate != nil {
		query += " AND a.created_at <= $" + fmt.Sprintf("%d", argCount)
		args = append(args, *filters.EndDate)
		argCount++
	}

	query += " ORDER BY a.created_at DESC LIMIT $" + fmt.Sprintf("%d", argCount) + " OFFSET $" + fmt.Sprintf("%d", argCount+1)
	args = append(args, filters.Limit, filters.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []*AuditLog
	for rows.Next() {
		log := &AuditLog{}
		var oldValueJSON, newValueJSON, detailsJSON []byte

		err := rows.Scan(
			&log.ID, &log.UserID, &log.Action, &log.ResourceType, &log.ResourceID,
			&oldValueJSON, &newValueJSON, &detailsJSON, &log.IPAddress, &log.UserAgent,
			&log.Status, &log.ErrorMessage, &log.CreatedAt, &log.Username,
		)
		if err != nil {
			return nil, 0, err
		}

		if oldValueJSON != nil {
			json.Unmarshal(oldValueJSON, &log.OldValue)
		}
		if newValueJSON != nil {
			json.Unmarshal(newValueJSON, &log.NewValue)
		}
		if detailsJSON != nil {
			json.Unmarshal(detailsJSON, &log.Details)
		}

		logs = append(logs, log)
	}

	// Get total count
	var total int
	countQuery := "SELECT COUNT(*) FROM audit_logs WHERE 1=1"
	s.db.QueryRowContext(ctx, countQuery).Scan(&total)

	return logs, total, rows.Err()
}

// GetUserActivity retrieves activity for a specific user
func (s *AuditService) GetUserActivity(ctx context.Context, userID uuid.UUID, limit int) ([]*AuditLog, error) {
	query := `
		SELECT id, user_id, action, resource_type, resource_id, details,
		       ip_address, user_agent, status, created_at
		FROM audit_logs
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := s.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*AuditLog
	for rows.Next() {
		log := &AuditLog{}
		var detailsJSON []byte

		err := rows.Scan(
			&log.ID, &log.UserID, &log.Action, &log.ResourceType, &log.ResourceID,
			&detailsJSON, &log.IPAddress, &log.UserAgent, &log.Status, &log.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if detailsJSON != nil {
			json.Unmarshal(detailsJSON, &log.Details)
		}

		logs = append(logs, log)
	}

	return logs, rows.Err()
}

// GetAuditStats retrieves audit statistics
func (s *AuditService) GetAuditStats(ctx context.Context, timeRange string) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	var timeFilter string
	switch timeRange {
	case "24h":
		timeFilter = "created_at >= NOW() - INTERVAL '24 hours'"
	case "7d":
		timeFilter = "created_at >= NOW() - INTERVAL '7 days'"
	case "30d":
		timeFilter = "created_at >= NOW() - INTERVAL '30 days'"
	default:
		timeFilter = "1=1"
	}

	// Total logs
	var total int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE "+timeFilter).Scan(&total)
	stats["total"] = total

	// By action
	rows, _ := s.db.QueryContext(ctx, "SELECT action, COUNT(*) FROM audit_logs WHERE "+timeFilter+" GROUP BY action ORDER BY COUNT(*) DESC LIMIT 10")
	byAction := make(map[string]int)
	for rows.Next() {
		var action string
		var count int
		rows.Scan(&action, &count)
		byAction[action] = count
	}
	rows.Close()
	stats["by_action"] = byAction

	// By resource type
	rows2, _ := s.db.QueryContext(ctx, "SELECT resource_type, COUNT(*) FROM audit_logs WHERE "+timeFilter+" GROUP BY resource_type")
	byResource := make(map[string]int)
	for rows2.Next() {
		var resourceType string
		var count int
		rows2.Scan(&resourceType, &count)
		byResource[resourceType] = count
	}
	rows2.Close()
	stats["by_resource"] = byResource

	// Failed actions
	var failed int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE status = 'failed' AND "+timeFilter).Scan(&failed)
	stats["failed"] = failed

	return stats, nil
}

// AuditFilters represents audit log filtering options
type AuditFilters struct {
	UserID       *uuid.UUID
	Action       string
	ResourceType string
	Status       string
	StartDate    *time.Time
	EndDate      *time.Time
	Limit        int
	Offset       int
}

// DeleteOldLogs deletes audit logs older than retention period
func (s *AuditService) DeleteOldLogs(ctx context.Context, retentionDays int) (int64, error) {
	query := "DELETE FROM audit_logs WHERE created_at < NOW() - INTERVAL '$1 days'"
	result, err := s.db.ExecContext(ctx, query, retentionDays)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}
