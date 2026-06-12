package notifications

import (
	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"time"

	"github.com/google/uuid"
)

var (
	ErrChannelNotFound    = errors.New("notification channel not found")
	ErrInvalidChannelType = errors.New("invalid channel type")
	ErrSendFailed         = errors.New("failed to send notification")
)

// NotificationChannel represents a notification channel
type NotificationChannel struct {
	ID        uuid.UUID              `json:"id"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"` // email, slack, pagerduty, webhook, sms
	Config    map[string]interface{} `json:"config"`
	IsActive  bool                   `json:"is_active"`
	CreatedBy uuid.UUID              `json:"created_by"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// NotificationRule represents notification routing rules
type NotificationRule struct {
	ID         uuid.UUID              `json:"id"`
	Name       string                 `json:"name"`
	Conditions map[string]interface{} `json:"conditions"`
	Channels   []uuid.UUID            `json:"channels"`
	IsActive   bool                   `json:"is_active"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// Notification represents a notification to be sent
type Notification struct {
	ID         uuid.UUID              `json:"id"`
	ChannelID  uuid.UUID              `json:"channel_id"`
	AlertID    *uuid.UUID             `json:"alert_id,omitempty"`
	IncidentID *uuid.UUID             `json:"incident_id,omitempty"`
	Recipient  string                 `json:"recipient"`
	Subject    string                 `json:"subject"`
	Message    string                 `json:"message"`
	Priority   string                 `json:"priority"`
	Metadata   map[string]interface{} `json:"metadata"`
	Status     string                 `json:"status"`
	ErrorMsg   string                 `json:"error_message,omitempty"`
	SentAt     time.Time              `json:"sent_at"`
}

// NotificationService handles notifications
type NotificationService struct {
	db *sql.DB
}

// NewNotificationService creates a new notification service
func NewNotificationService(db *sql.DB) *NotificationService {
	return &NotificationService{db: db}
}

// SendNotification sends a notification through specified channel
func (s *NotificationService) SendNotification(ctx context.Context, notification *Notification) error {
	// Get channel
	channel, err := s.GetChannel(ctx, notification.ChannelID)
	if err != nil {
		return err
	}

	if !channel.IsActive {
		return errors.New("channel is not active")
	}

	// Send based on channel type
	var sendErr error
	switch channel.Type {
	case "slack":
		sendErr = s.sendSlack(ctx, channel, notification)
	case "email":
		sendErr = s.sendEmail(ctx, channel, notification)
	case "pagerduty":
		sendErr = s.sendPagerDuty(ctx, channel, notification)
	case "webhook":
		sendErr = s.sendWebhook(ctx, channel, notification)
	case "sms":
		sendErr = s.sendSMS(ctx, channel, notification)
	default:
		sendErr = ErrInvalidChannelType
	}

	// Log notification
	notification.ID = uuid.New()
	notification.SentAt = time.Now()
	if sendErr != nil {
		notification.Status = "failed"
		notification.ErrorMsg = sendErr.Error()
	} else {
		notification.Status = "sent"
	}

	s.logNotification(ctx, notification)

	return sendErr
}

// SendAlertNotification sends notification for an alert
func (s *NotificationService) SendAlertNotification(ctx context.Context, alertID uuid.UUID, severity, title, description string) error {
	// Get applicable channels based on rules
	channels, err := s.getChannelsForAlert(ctx, severity)
	if err != nil {
		return err
	}

	// Send to each channel
	for _, channelID := range channels {
		notification := &Notification{
			ChannelID: channelID,
			AlertID:   &alertID,
			Subject:   fmt.Sprintf("[%s] %s", severity, title),
			Message:   description,
			Priority:  severity,
		}

		go s.SendNotification(ctx, notification)
	}

	return nil
}

// SendIncidentNotification sends notification for a newly created incident.
// Includes severity, title, alert count, and a direct link to the incident.
func (s *NotificationService) SendIncidentNotification(ctx context.Context, incidentID uuid.UUID, incidentNumber string, severity, title, description string) error {
	channels, err := s.getChannelsForIncident(ctx, severity)
	if err != nil {
		return err
	}

	appURL := os.Getenv("APP_BASE_URL")
	incidentURL := ""
	if appURL != "" {
		incidentURL = fmt.Sprintf("%s/incidents?id=%s", appURL, incidentID.String())
	}

	for _, channelID := range channels {
		notification := &Notification{
			ChannelID:  channelID,
			IncidentID: &incidentID,
			Subject:    fmt.Sprintf("[INCIDENT #%s] [%s] %s", incidentNumber, severity, title),
			Message: func() string {
				if incidentURL != "" {
					return fmt.Sprintf("%s\n\n<%s|View Incident #%s →>", description, incidentURL, incidentNumber)
				}
				return description
			}(),
			Priority: severity,
		}
		go s.SendNotification(ctx, notification)
	}

	return nil
}

// SendRCAUpdateNotification fires a follow-up Slack message after the RCA engine
// completes (either CACIE preliminary or OIE final result).
// This is the most operationally useful notification — operators see the root cause
// directly in Slack without opening the UI.
func (s *NotificationService) SendRCAUpdateNotification(ctx context.Context, incidentID uuid.UUID, incidentNumber, severity, title, rootCause string, confidence float64, band, source string) error {
	channels, err := s.getChannelsForIncident(ctx, severity)
	if err != nil || len(channels) == 0 {
		return nil // silent — channels not configured for this severity
	}

	appURL := os.Getenv("APP_BASE_URL")
	incidentURL := ""
	if appURL != "" {
		incidentURL = fmt.Sprintf("%s/incidents?id=%s&tab=rca", appURL, incidentID.String())
	}

	confidencePct := int(confidence * 100)
	bandEmoji := "🔵"
	if band == "CONFIRMED" {
		bandEmoji = "🟢"
	} else if band == "LIKELY" {
		bandEmoji = "🟡"
	} else if band == "POSSIBLE" {
		bandEmoji = "🟠"
	} else if confidence == 0 {
		bandEmoji = "🔴"
	}

	subject := fmt.Sprintf("%s RCA Ready — #%s [%s]", bandEmoji, incidentNumber, severity)
	msgBody := fmt.Sprintf("*%s*\n\n*Root Cause (%s, %d%% confidence):*\n%s", title, source, confidencePct, rootCause)
	if incidentURL != "" {
		msgBody += fmt.Sprintf("\n\n<%s|View RCA Details →>", incidentURL)
	}

	for _, channelID := range channels {
		notification := &Notification{
			ChannelID:  channelID,
			IncidentID: &incidentID,
			Subject:    subject,
			Message:    msgBody,
			Priority:   severity,
		}
		go s.SendNotification(ctx, notification)
	}
	return nil
}

// Channel-specific send functions

func (s *NotificationService) sendSlack(ctx context.Context, channel *NotificationChannel, notification *Notification) error {
	webhookURL, ok := channel.Config["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return errors.New("slack channel missing webhook_url in config")
	}
	slackChannel, _ := channel.Config["channel"].(string)

	payload := map[string]interface{}{
		"text": notification.Subject,
		"blocks": []map[string]interface{}{
			{
				"type": "header",
				"text": map[string]string{
					"type": "plain_text",
					"text": notification.Subject,
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": notification.Message,
				},
			},
			{
				"type": "context",
				"elements": []map[string]string{{
					"type": "mrkdwn",
					"text": fmt.Sprintf("Priority: *%s* · AlertHub", notification.Priority),
				}},
			},
		},
	}
	if slackChannel != "" {
		payload["channel"] = slackChannel
	}

	jsonData, _ := json.Marshal(payload)
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrSendFailed
	}

	return nil
}

func (s *NotificationService) sendEmail(ctx context.Context, channel *NotificationChannel, notification *Notification) error {
	// Get SMTP configuration from channel config or environment variables
	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	fromEmail := os.Getenv("SMTP_FROM")

	// Allow channel-specific override
	if host, ok := channel.Config["smtp_host"].(string); ok && host != "" {
		smtpHost = host
	}
	if port, ok := channel.Config["smtp_port"].(string); ok && port != "" {
		smtpPort = port
	}
	if user, ok := channel.Config["smtp_user"].(string); ok && user != "" {
		smtpUser = user
	}
	if pass, ok := channel.Config["smtp_pass"].(string); ok && pass != "" {
		smtpPass = pass
	}
	if from, ok := channel.Config["from_email"].(string); ok && from != "" {
		fromEmail = from
	}

	// Get recipient from notification or channel config
	recipient := notification.Recipient
	if recipient == "" {
		if to, ok := channel.Config["to_email"].(string); ok {
			recipient = to
		}
	}

	if smtpHost == "" || smtpPort == "" || recipient == "" {
		return errors.New("SMTP configuration incomplete")
	}

	// Compose email message
	subject := notification.Subject
	body := notification.Message

	message := fmt.Sprintf("From: %s\r\n", fromEmail)
	message += fmt.Sprintf("To: %s\r\n", recipient)
	message += fmt.Sprintf("Subject: %s\r\n", subject)
	message += "MIME-Version: 1.0\r\n"
	message += "Content-Type: text/html; charset=UTF-8\r\n"
	message += "\r\n"
	message += fmt.Sprintf("<html><body><h2>%s</h2><p>%s</p></body></html>", subject, body)

	// Setup authentication
	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	// Send email
	addr := fmt.Sprintf("%s:%s", smtpHost, smtpPort)

	// Try TLS connection first
	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         smtpHost,
	}

	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		// Fallback to non-TLS
		return smtp.SendMail(addr, auth, fromEmail, []string{recipient}, []byte(message))
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if auth != nil {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}

	if err = client.Mail(fromEmail); err != nil {
		return err
	}

	if err = client.Rcpt(recipient); err != nil {
		return err
	}

	w, err := client.Data()
	if err != nil {
		return err
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	return client.Quit()
}

func (s *NotificationService) sendPagerDuty(ctx context.Context, channel *NotificationChannel, notification *Notification) error {
	// routing_key is the authentication for PagerDuty Events API v2.
	// It can be stored under either "routing_key" (preferred) or "service_key" (legacy).
	routingKey, _ := channel.Config["routing_key"].(string)
	if routingKey == "" {
		routingKey, _ = channel.Config["service_key"].(string)
	}
	if routingKey == "" {
		return errors.New("pagerduty channel missing routing_key in config")
	}

	payload := map[string]interface{}{
		"routing_key":  routingKey,
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  notification.Subject,
			"severity": notification.Priority,
			"source":   "alerthub",
			"custom_details": map[string]interface{}{
				"message": notification.Message,
			},
		},
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://events.pagerduty.com/v2/enqueue", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return ErrSendFailed
	}

	return nil
}

func (s *NotificationService) sendWebhook(ctx context.Context, channel *NotificationChannel, notification *Notification) error {
	webhookURL := channel.Config["url"].(string)

	payload := map[string]interface{}{
		"subject":  notification.Subject,
		"message":  notification.Message,
		"priority": notification.Priority,
		"metadata": notification.Metadata,
	}

	jsonData, _ := json.Marshal(payload)
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrSendFailed
	}

	return nil
}

func (s *NotificationService) sendSMS(ctx context.Context, channel *NotificationChannel, notification *Notification) error {
	// Get Twilio configuration from channel config or environment variables
	accountSID := os.Getenv("TWILIO_ACCOUNT_SID")
	authToken := os.Getenv("TWILIO_AUTH_TOKEN")
	fromNumber := os.Getenv("TWILIO_FROM_NUMBER")

	// Allow channel-specific override
	if sid, ok := channel.Config["account_sid"].(string); ok && sid != "" {
		accountSID = sid
	}
	if token, ok := channel.Config["auth_token"].(string); ok && token != "" {
		authToken = token
	}
	if from, ok := channel.Config["from_number"].(string); ok && from != "" {
		fromNumber = from
	}

	// Get recipient from notification or channel config
	toNumber := notification.Recipient
	if toNumber == "" {
		if to, ok := channel.Config["to_number"].(string); ok {
			toNumber = to
		}
	}

	if accountSID == "" || authToken == "" || fromNumber == "" || toNumber == "" {
		return errors.New("Twilio configuration incomplete")
	}

	// Prepare message - SMS has character limits
	message := notification.Message
	if len(message) > 1600 {
		message = message[:1597] + "..."
	}

	// Build Twilio API request
	twilioURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", accountSID)

	data := fmt.Sprintf("From=%s&To=%s&Body=%s", fromNumber, toNumber, message)

	req, err := http.NewRequest("POST", twilioURL, bytes.NewBufferString(data))
	if err != nil {
		return err
	}

	req.SetBasicAuth(accountSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Twilio API error: status %d", resp.StatusCode)
	}

	return nil
}

// GetChannel retrieves a notification channel
func (s *NotificationService) GetChannel(ctx context.Context, channelID uuid.UUID) (*NotificationChannel, error) {
	channel := &NotificationChannel{}

	var configJSON []byte
	query := "SELECT id, name, type, config, is_active, created_by, created_at, updated_at FROM notification_channels WHERE id = $1"

	err := s.db.QueryRowContext(ctx, query, channelID).Scan(
		&channel.ID, &channel.Name, &channel.Type, &configJSON, &channel.IsActive,
		&channel.CreatedBy, &channel.CreatedAt, &channel.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrChannelNotFound
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal(configJSON, &channel.Config)

	return channel, nil
}

func (s *NotificationService) getChannelsForAlert(ctx context.Context, severity string) ([]uuid.UUID, error) {
	// Get channels based on notification rules
	query := `
		SELECT DISTINCT unnest(channels) as channel_id
		FROM notification_rules
		WHERE is_active = true
		AND conditions->>'severity' = $1
	`

	rows, err := s.db.QueryContext(ctx, query, severity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []uuid.UUID
	for rows.Next() {
		var channelID uuid.UUID
		rows.Scan(&channelID)
		channels = append(channels, channelID)
	}

	return channels, nil
}

func (s *NotificationService) getChannelsForIncident(ctx context.Context, severity string) ([]uuid.UUID, error) {
	return s.getChannelsForAlert(ctx, severity)
}

func (s *NotificationService) logNotification(ctx context.Context, notification *Notification) {
	query := `
		INSERT INTO notification_log (id, channel_id, alert_id, incident_id, recipient, status, error_message, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	s.db.ExecContext(ctx, query,
		notification.ID, notification.ChannelID, notification.AlertID, notification.IncidentID,
		notification.Recipient, notification.Status, notification.ErrorMsg, notification.SentAt,
	)
}

// CreateChannel creates a new notification channel
func (s *NotificationService) CreateChannel(ctx context.Context, channel *NotificationChannel) error {
	channel.ID = uuid.New()
	channel.CreatedAt = time.Now()
	channel.UpdatedAt = time.Now()

	configJSON, _ := json.Marshal(channel.Config)

	query := `
		INSERT INTO notification_channels (id, name, type, config, is_active, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := s.db.ExecContext(ctx, query,
		channel.ID, channel.Name, channel.Type, configJSON, channel.IsActive,
		channel.CreatedBy, channel.CreatedAt, channel.UpdatedAt,
	)

	return err
}

// ListChannels retrieves all notification channels
func (s *NotificationService) ListChannels(ctx context.Context) ([]*NotificationChannel, error) {
	query := "SELECT id, name, type, config, is_active, created_by, created_at, updated_at FROM notification_channels ORDER BY name"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*NotificationChannel
	for rows.Next() {
		channel := &NotificationChannel{}
		var configJSON []byte

		err := rows.Scan(
			&channel.ID, &channel.Name, &channel.Type, &configJSON, &channel.IsActive,
			&channel.CreatedBy, &channel.CreatedAt, &channel.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(configJSON, &channel.Config)
		channels = append(channels, channel)
	}

	return channels, rows.Err()
}

// TestChannel tests a notification channel
func (s *NotificationService) TestChannel(ctx context.Context, channelID uuid.UUID) error {
	_, err := s.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}

	testNotification := &Notification{
		ChannelID: channelID,
		Subject:   "AlertHub Test Notification",
		Message:   "This is a test notification from AlertHub. If you receive this, the channel is configured correctly.",
		Priority:  "info",
	}

	return s.SendNotification(ctx, testNotification)
}

// Interface implementation methods for interfaces.NotificationService

// SendAlert sends an alert notification (interface method)
func (s *NotificationService) SendAlert(ctx context.Context, alert *models.Alert, channels []string) error {
	// Convert channel names to UUIDs if needed, or use the existing channel lookup
	for _, channelName := range channels {
		// Find channel by name
		var channelID uuid.UUID
		err := s.db.QueryRowContext(ctx, "SELECT id FROM notification_channels WHERE name = $1 AND is_active = true", channelName).Scan(&channelID)
		if err != nil {
			continue // Skip unavailable channels
		}

		notification := &Notification{
			ChannelID: channelID,
			AlertID:   &alert.ID,
			Subject:   fmt.Sprintf("[%s] %s", alert.Severity, alert.Title),
			Message:   alert.Description,
			Priority:  alert.Severity,
			Metadata: map[string]interface{}{
				"alert_id": alert.ID.String(),
				"source":   alert.Source,
				"tags":     alert.Tags,
			},
		}

		// Send notification asynchronously
		go func(n *Notification) {
			if err := s.SendNotification(ctx, n); err != nil {
				fmt.Printf("Failed to send alert notification: %v\n", err)
			}
		}(notification)
	}

	return nil
}

// SendIncidentAlert sends incident notifications using the interface (interface method)
func (s *NotificationService) SendIncidentAlert(ctx context.Context, incident *interfaces.Incident, notificationType string) error {
	// Get applicable channels based on severity
	channels, err := s.getChannelsForIncident(ctx, incident.Severity)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("[INCIDENT] [%s] %s", incident.Severity, incident.Title)
	if notificationType != "" {
		subject = fmt.Sprintf("[INCIDENT-%s] [%s] %s", notificationType, incident.Severity, incident.Title)
	}

	for _, channelID := range channels {
		notification := &Notification{
			ChannelID:  channelID,
			IncidentID: &incident.ID,
			Subject:    subject,
			Message:    incident.Description,
			Priority:   incident.Severity,
			Metadata: map[string]interface{}{
				"incident_id":       incident.ID.String(),
				"notification_type": notificationType,
				"status":            incident.Status,
				"alert_count":       len(incident.AlertIDs),
			},
		}

		// Send notification asynchronously
		go func(n *Notification) {
			if err := s.SendNotification(ctx, n); err != nil {
				fmt.Printf("Failed to send incident notification: %v\n", err)
			}
		}(notification)
	}

	return nil
}

// GetNotificationHistory returns notification history for analytics
func (s *NotificationService) GetNotificationHistory(ctx context.Context, filters map[string]interface{}) ([]*Notification, error) {
	query := `
		SELECT id, channel_id, alert_id, incident_id, recipient, subject,
		       message, priority, status, error_message, sent_at
		FROM notification_log
		WHERE 1=1
	`

	args := []interface{}{}
	argCount := 1

	// Apply filters
	if alertID, ok := filters["alert_id"].(string); ok && alertID != "" {
		query += fmt.Sprintf(" AND alert_id = $%d", argCount)
		args = append(args, alertID)
		argCount++
	}

	if incidentID, ok := filters["incident_id"].(string); ok && incidentID != "" {
		query += fmt.Sprintf(" AND incident_id = $%d", argCount)
		args = append(args, incidentID)
		argCount++
	}

	if status, ok := filters["status"].(string); ok && status != "" {
		query += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
		argCount++
	}

	query += " ORDER BY sent_at DESC LIMIT 100"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []*Notification
	for rows.Next() {
		notification := &Notification{}
		err := rows.Scan(
			&notification.ID, &notification.ChannelID, &notification.AlertID, &notification.IncidentID,
			&notification.Recipient, &notification.Subject, &notification.Message, &notification.Priority,
			&notification.Status, &notification.ErrorMsg, &notification.SentAt,
		)
		if err != nil {
			continue
		}

		notifications = append(notifications, notification)
	}

	return notifications, nil
}

// HealthCheck verifies the notification service can reach the database.
func (s *NotificationService) HealthCheck() error {
	var count int
	return s.db.QueryRow("SELECT COUNT(*) FROM notification_channels WHERE is_active = true").Scan(&count)
}

// GetNotificationStats returns notification statistics
func (s *NotificationService) GetNotificationStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total notifications in last 24 hours
	var total int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM notification_log
		WHERE sent_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&total)
	stats["total_24h"] = total

	// Success rate
	var successful int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM notification_log
		WHERE sent_at >= NOW() - INTERVAL '24 hours' AND status = 'sent'
	`).Scan(&successful)

	if total > 0 {
		stats["success_rate"] = float64(successful) / float64(total) * 100
	} else {
		stats["success_rate"] = 100.0
	}

	// By channel type
	rows, _ := s.db.QueryContext(ctx, `
		SELECT nc.type, COUNT(*) as count
		FROM notification_log nl
		JOIN notification_channels nc ON nl.channel_id = nc.id
		WHERE nl.sent_at >= NOW() - INTERVAL '24 hours'
		GROUP BY nc.type
	`)
	defer rows.Close()

	byChannelType := make(map[string]int)
	for rows.Next() {
		var channelType string
		var count int
		rows.Scan(&channelType, &count)
		byChannelType[channelType] = count
	}
	stats["by_channel_type"] = byChannelType

	return stats, nil
}
