package maintenance

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// MaintenanceWindow represents a maintenance window for alerts
type MaintenanceWindow struct {
	ID                uuid.UUID  `json:"id"`
	AlertID           *uuid.UUID `json:"alert_id,omitempty"`
	URLName           string     `json:"url_name,omitempty"`
	Alertname         string     `json:"alertname,omitempty"`
	StartTime         time.Time  `json:"start_time"`
	EndTime           *time.Time `json:"end_time,omitempty"`
	MaintenanceStatus int        `json:"maintenance_status"` // 0=completed, 1=active
	CreatedBy         *uuid.UUID `json:"created_by,omitempty"`
	Comment           string     `json:"comment,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// MaintenanceService handles maintenance operations
type MaintenanceService struct {
	db *sql.DB
}

// NewMaintenanceService creates a new maintenance service
func NewMaintenanceService(db *sql.DB) *MaintenanceService {
	return &MaintenanceService{db: db}
}

// CreateMaintenanceWindow creates a new maintenance window
func (s *MaintenanceService) CreateMaintenanceWindow(ctx context.Context, window *MaintenanceWindow) error {
	window.ID = uuid.New()
	window.CreatedAt = time.Now()
	window.UpdatedAt = time.Now()
	window.MaintenanceStatus = 1 // Active by default

	query := `
		INSERT INTO maintenance_windows (
			id, alert_id, url_name, alertname, start_time, end_time,
			maintenance_status, created_by, comment, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := s.db.ExecContext(ctx, query,
		window.ID, window.AlertID, window.URLName, window.Alertname,
		window.StartTime, window.EndTime, window.MaintenanceStatus,
		window.CreatedBy, window.Comment, window.CreatedAt, window.UpdatedAt,
	)

	if err != nil {
		return err
	}

	// Update alert maintenance status if alert_id is provided
	if window.AlertID != nil {
		s.updateAlertMaintenanceStatus(ctx, *window.AlertID, 1, &window.StartTime, window.EndTime)
	}

	return nil
}

// EndMaintenanceWindow ends a maintenance window
func (s *MaintenanceService) EndMaintenanceWindow(ctx context.Context, windowID uuid.UUID, userID uuid.UUID) error {
	now := time.Now()

	query := `
		UPDATE maintenance_windows
		SET maintenance_status = 0, end_time = $1, updated_at = $2
		WHERE id = $3
		RETURNING alert_id
	`

	var alertID *uuid.UUID
	err := s.db.QueryRowContext(ctx, query, now, now, windowID).Scan(&alertID)
	if err != nil {
		return err
	}

	// Update alert maintenance status if alert exists
	if alertID != nil {
		s.updateAlertMaintenanceStatus(ctx, *alertID, 0, nil, &now)
	}

	return nil
}

// GetActiveMaintenanceWindows retrieves all active maintenance windows
func (s *MaintenanceService) GetActiveMaintenanceWindows(ctx context.Context) ([]*MaintenanceWindow, error) {
	query := `
		SELECT id, alert_id, url_name, alertname, start_time, end_time,
		       maintenance_status, created_by, comment, created_at, updated_at
		FROM maintenance_windows
		WHERE maintenance_status = 1
		  AND (end_time IS NULL OR end_time > NOW())
		ORDER BY start_time DESC
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []*MaintenanceWindow
	for rows.Next() {
		window := &MaintenanceWindow{}
		err := rows.Scan(
			&window.ID, &window.AlertID, &window.URLName, &window.Alertname,
			&window.StartTime, &window.EndTime, &window.MaintenanceStatus,
			&window.CreatedBy, &window.Comment, &window.CreatedAt, &window.UpdatedAt,
		)
		if err != nil {
			continue
		}
		windows = append(windows, window)
	}

	return windows, rows.Err()
}

// GetMaintenanceWindowsForAlert retrieves maintenance windows for a specific alert
func (s *MaintenanceService) GetMaintenanceWindowsForAlert(ctx context.Context, alertID uuid.UUID) ([]*MaintenanceWindow, error) {
	query := `
		SELECT id, alert_id, url_name, alertname, start_time, end_time,
		       maintenance_status, created_by, comment, created_at, updated_at
		FROM maintenance_windows
		WHERE alert_id = $1
		ORDER BY start_time DESC
	`

	rows, err := s.db.QueryContext(ctx, query, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []*MaintenanceWindow
	for rows.Next() {
		window := &MaintenanceWindow{}
		err := rows.Scan(
			&window.ID, &window.AlertID, &window.URLName, &window.Alertname,
			&window.StartTime, &window.EndTime, &window.MaintenanceStatus,
			&window.CreatedBy, &window.Comment, &window.CreatedAt, &window.UpdatedAt,
		)
		if err != nil {
			continue
		}
		windows = append(windows, window)
	}

	return windows, rows.Err()
}

// IsAlertInMaintenance checks if an alert is currently in maintenance
func (s *MaintenanceService) IsAlertInMaintenance(ctx context.Context, alertID uuid.UUID) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM maintenance_windows
			WHERE alert_id = $1
			  AND maintenance_status = 1
			  AND (end_time IS NULL OR end_time > NOW())
		)
	`

	var inMaintenance bool
	err := s.db.QueryRowContext(ctx, query, alertID).Scan(&inMaintenance)
	return inMaintenance, err
}

// updateAlertMaintenanceStatus updates the maintenance status in the alerts table
func (s *MaintenanceService) updateAlertMaintenanceStatus(ctx context.Context, alertID uuid.UUID, status int, startTime *time.Time, endTime *time.Time) error {
	query := `
		UPDATE alerts
		SET maintenance_status = $1, maintenance_start_time = $2, maintenance_end_time = $3, updated_at = $4
		WHERE id = $5
	`

	_, err := s.db.ExecContext(ctx, query, status, startTime, endTime, time.Now(), alertID)
	return err
}

// AutoExpireMaintenanceWindows expires maintenance windows that have passed their end_time
// This should be run periodically (e.g., every minute via cron)
func (s *MaintenanceService) AutoExpireMaintenanceWindows(ctx context.Context) (int, error) {
	now := time.Now()

	// Get expired windows
	query := `
		SELECT id, alert_id FROM maintenance_windows
		WHERE maintenance_status = 1
		  AND end_time IS NOT NULL
		  AND end_time <= $1
	`

	rows, err := s.db.QueryContext(ctx, query, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	expired := []struct {
		WindowID uuid.UUID
		AlertID  *uuid.UUID
	}{}

	for rows.Next() {
		var e struct {
			WindowID uuid.UUID
			AlertID  *uuid.UUID
		}
		if err := rows.Scan(&e.WindowID, &e.AlertID); err != nil {
			continue
		}
		expired = append(expired, e)
	}

	// Update expired windows
	updateQuery := `
		UPDATE maintenance_windows
		SET maintenance_status = 0, updated_at = $1
		WHERE id = $2
	`

	for _, e := range expired {
		s.db.ExecContext(ctx, updateQuery, now, e.WindowID)

		// Update alert status
		if e.AlertID != nil {
			s.updateAlertMaintenanceStatus(ctx, *e.AlertID, 0, nil, &now)
		}
	}

	return len(expired), nil
}
