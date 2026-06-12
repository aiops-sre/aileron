package workflows

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HTTPActionExecutor executes HTTP requests
type HTTPActionExecutor struct{}

func (e *HTTPActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	url, ok := action.Parameters["url"].(string)
	if !ok {
		return nil, fmt.Errorf("url parameter is required")
	}

	method := "GET"
	if m, ok := action.Parameters["method"].(string); ok {
		method = m
	}

	var body []byte
	if bodyParam, ok := action.Parameters["body"]; ok {
		if bodyStr, ok := bodyParam.(string); ok {
			body = []byte(bodyStr)
		} else {
			body, _ = json.Marshal(bodyParam)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	if headers, ok := action.Parameters["headers"].(map[string]interface{}); ok {
		for key, value := range headers {
			if valueStr, ok := value.(string); ok {
				req.Header.Set(key, valueStr)
			}
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	result := map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
	}

	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	return result, nil
}

func (e *HTTPActionExecutor) Validate(action *WorkflowAction) error {
	if _, ok := action.Parameters["url"]; !ok {
		return fmt.Errorf("url parameter is required")
	}
	return nil
}

// EmailActionExecutor sends email notifications
type EmailActionExecutor struct{}

func (e *EmailActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	to, ok := action.Parameters["to"].(string)
	if !ok {
		return nil, fmt.Errorf("to parameter is required")
	}

	subject, ok := action.Parameters["subject"].(string)
	if !ok {
		return nil, fmt.Errorf("subject parameter is required")
	}

	body, ok := action.Parameters["body"].(string)
	if !ok {
		return nil, fmt.Errorf("body parameter is required")
	}

	// Get SMTP configuration from context or parameters
	smtpHost := "localhost"
	smtpPort := 587
	username := ""
	password := ""

	if host, ok := action.Parameters["smtp_host"].(string); ok {
		smtpHost = host
	}
	if port, ok := action.Parameters["smtp_port"].(float64); ok {
		smtpPort = int(port)
	}
	if user, ok := action.Parameters["smtp_username"].(string); ok {
		username = user
	}
	if pass, ok := action.Parameters["smtp_password"].(string); ok {
		password = pass
	}

	// Simple email sending (in production, use a proper email library)
	auth := smtp.PlainAuth("", username, password, smtpHost)
	message := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s", to, subject, body)

	err := smtp.SendMail(fmt.Sprintf("%s:%d", smtpHost, smtpPort), auth, username, []string{to}, []byte(message))
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"sent": true,
		"to":   to,
	}, nil
}

func (e *EmailActionExecutor) Validate(action *WorkflowAction) error {
	required := []string{"to", "subject", "body"}
	for _, param := range required {
		if _, ok := action.Parameters[param]; !ok {
			return fmt.Errorf("%s parameter is required", param)
		}
	}
	return nil
}

// SlackActionExecutor sends Slack notifications
type SlackActionExecutor struct{}

func (e *SlackActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	webhookURL, ok := action.Parameters["webhook_url"].(string)
	if !ok {
		return nil, fmt.Errorf("webhook_url parameter is required")
	}

	message, ok := action.Parameters["message"].(string)
	if !ok {
		return nil, fmt.Errorf("message parameter is required")
	}

	payload := map[string]interface{}{
		"text": message,
	}

	if channel, ok := action.Parameters["channel"].(string); ok {
		payload["channel"] = channel
	}

	payloadBytes, _ := json.Marshal(payload)
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Slack API returned status %d", resp.StatusCode)
	}

	return map[string]interface{}{
		"sent": true,
	}, nil
}

func (e *SlackActionExecutor) Validate(action *WorkflowAction) error {
	required := []string{"webhook_url", "message"}
	for _, param := range required {
		if _, ok := action.Parameters[param]; !ok {
			return fmt.Errorf("%s parameter is required", param)
		}
	}
	return nil
}

// PagerDutyActionExecutor creates PagerDuty incidents
type PagerDutyActionExecutor struct{}

func (e *PagerDutyActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	integrationKey, ok := action.Parameters["integration_key"].(string)
	if !ok {
		return nil, fmt.Errorf("integration_key parameter is required")
	}

	summary, ok := action.Parameters["summary"].(string)
	if !ok {
		return nil, fmt.Errorf("summary parameter is required")
	}

	payload := map[string]interface{}{
		"routing_key":  integrationKey,
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  summary,
			"source":   "AlertHub",
			"severity": "error",
		},
	}

	if severity, ok := action.Parameters["severity"].(string); ok {
		payload["payload"].(map[string]interface{})["severity"] = severity
	}

	payloadBytes, _ := json.Marshal(payload)
	resp, err := http.Post("https://events.pagerduty.com/v2/enqueue", "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 202 {
		return nil, fmt.Errorf("PagerDuty API returned status %d", resp.StatusCode)
	}

	return map[string]interface{}{
		"sent": true,
	}, nil
}

func (e *PagerDutyActionExecutor) Validate(action *WorkflowAction) error {
	required := []string{"integration_key", "summary"}
	for _, param := range required {
		if _, ok := action.Parameters[param]; !ok {
			return fmt.Errorf("%s parameter is required", param)
		}
	}
	return nil
}

// WebhookActionExecutor executes generic webhooks
type WebhookActionExecutor struct{}

func (e *WebhookActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	// Similar to HTTPActionExecutor but with webhook-specific logic
	return (&HTTPActionExecutor{}).Execute(ctx, action, context)
}

func (e *WebhookActionExecutor) Validate(action *WorkflowAction) error {
	return (&HTTPActionExecutor{}).Validate(action)
}

// CreateIncidentActionExecutor creates incidents
type CreateIncidentActionExecutor struct {
	db *sql.DB
}

func (e *CreateIncidentActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	title, ok := action.Parameters["title"].(string)
	if !ok {
		return nil, fmt.Errorf("title parameter is required")
	}

	description, _ := action.Parameters["description"].(string)
	severity, _ := action.Parameters["severity"].(string)
	if severity == "" {
		severity = "medium"
	}

	// Create incident in database
	incidentID := uuid.New()
	query := `
		INSERT INTO incidents (id, title, description, severity, status, started_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'open', NOW(), NOW(), NOW())
	`

	_, err := e.db.ExecContext(ctx, query, incidentID, title, description, severity)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"incident_id": incidentID.String(),
		"title":       title,
		"severity":    severity,
	}, nil
}

func (e *CreateIncidentActionExecutor) Validate(action *WorkflowAction) error {
	if _, ok := action.Parameters["title"]; !ok {
		return fmt.Errorf("title parameter is required")
	}
	return nil
}

// UpdateAlertActionExecutor updates alert properties
type UpdateAlertActionExecutor struct {
	db *sql.DB
}

func (e *UpdateAlertActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	alertIDStr, ok := action.Parameters["alert_id"].(string)
	if !ok {
		// Try to get from trigger context
		if triggerData, ok := context["trigger"].(map[string]interface{}); ok {
			if id, ok := triggerData["alert_id"].(string); ok {
				alertIDStr = id
			}
		}
	}

	if alertIDStr == "" {
		return nil, fmt.Errorf("alert_id parameter is required")
	}

	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid alert_id: %v", err)
	}

	// Update alert properties
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if status, ok := action.Parameters["status"].(string); ok {
		updates = append(updates, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, status)
		argIndex++
	}

	if severity, ok := action.Parameters["severity"].(string); ok {
		updates = append(updates, fmt.Sprintf("severity = $%d", argIndex))
		args = append(args, severity)
		argIndex++
	}

	if len(updates) == 0 {
		return nil, fmt.Errorf("no valid update parameters provided")
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	args = append(args, alertID)

	query := fmt.Sprintf("UPDATE alerts SET %s WHERE id = $%d",
		strings.Join(updates, ", "), argIndex)

	_, err = e.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"alert_id": alertIDStr,
		"updated":  true,
	}, nil
}

func (e *UpdateAlertActionExecutor) Validate(action *WorkflowAction) error {
	// Either alert_id must be provided or it should be available in context
	validParams := []string{"status", "severity", "title", "description"}
	hasValidParam := false
	for _, param := range validParams {
		if _, ok := action.Parameters[param]; ok {
			hasValidParam = true
			break
		}
	}
	if !hasValidParam {
		return fmt.Errorf("at least one update parameter is required")
	}
	return nil
}

// AssignAlertActionExecutor assigns alerts to users
type AssignAlertActionExecutor struct {
	db *sql.DB
}

func (e *AssignAlertActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	alertIDStr, ok := action.Parameters["alert_id"].(string)
	if !ok {
		if triggerData, ok := context["trigger"].(map[string]interface{}); ok {
			if id, ok := triggerData["alert_id"].(string); ok {
				alertIDStr = id
			}
		}
	}

	userIDStr, ok := action.Parameters["user_id"].(string)
	if !ok {
		return nil, fmt.Errorf("user_id parameter is required")
	}

	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid alert_id: %v", err)
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid user_id: %v", err)
	}

	query := "UPDATE alerts SET assigned_to = $1, updated_at = $2 WHERE id = $3"
	_, err = e.db.ExecContext(ctx, query, userID, time.Now(), alertID)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"alert_id": alertIDStr,
		"user_id":  userIDStr,
		"assigned": true,
	}, nil
}

func (e *AssignAlertActionExecutor) Validate(action *WorkflowAction) error {
	if _, ok := action.Parameters["user_id"]; !ok {
		return fmt.Errorf("user_id parameter is required")
	}
	return nil
}

// ResolveAlertActionExecutor resolves alerts
type ResolveAlertActionExecutor struct {
	db *sql.DB
}

func (e *ResolveAlertActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	alertIDStr, ok := action.Parameters["alert_id"].(string)
	if !ok {
		if triggerData, ok := context["trigger"].(map[string]interface{}); ok {
			if id, ok := triggerData["alert_id"].(string); ok {
				alertIDStr = id
			}
		}
	}

	if alertIDStr == "" {
		return nil, fmt.Errorf("alert_id parameter is required")
	}

	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid alert_id: %v", err)
	}

	notes, _ := action.Parameters["notes"].(string)
	if notes == "" {
		notes = "Resolved by workflow"
	}

	query := `
		UPDATE alerts 
		SET status = 'resolved', resolved_at = $1, resolution_notes = $2, 
		    resolution_type = 'workflow', updated_at = $1
		WHERE id = $3
	`

	_, err = e.db.ExecContext(ctx, query, time.Now(), notes, alertID)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"alert_id": alertIDStr,
		"resolved": true,
		"notes":    notes,
	}, nil
}

func (e *ResolveAlertActionExecutor) Validate(action *WorkflowAction) error {
	// alert_id can come from context, so don't require it in parameters
	return nil
}

// WaitActionExecutor waits for a specified duration
type WaitActionExecutor struct{}

func (e *WaitActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	durationParam, ok := action.Parameters["duration"]
	if !ok {
		return nil, fmt.Errorf("duration parameter is required")
	}

	var duration time.Duration
	var err error

	switch v := durationParam.(type) {
	case string:
		duration, err = time.ParseDuration(v)
	case float64:
		duration = time.Duration(v) * time.Second
	case int:
		duration = time.Duration(v) * time.Second
	default:
		return nil, fmt.Errorf("invalid duration format")
	}

	if err != nil {
		return nil, fmt.Errorf("invalid duration: %v", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(duration):
		return map[string]interface{}{
			"waited": duration.String(),
		}, nil
	}
}

func (e *WaitActionExecutor) Validate(action *WorkflowAction) error {
	if _, ok := action.Parameters["duration"]; !ok {
		return fmt.Errorf("duration parameter is required")
	}
	return nil
}

// ConditionActionExecutor evaluates conditions
type ConditionActionExecutor struct{}

func (e *ConditionActionExecutor) Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error) {
	expression, ok := action.Parameters["expression"].(string)
	if !ok {
		return nil, fmt.Errorf("expression parameter is required")
	}

	// Simple condition evaluation - in production, use a proper expression engine
	result := e.evaluateCondition(expression, context)

	return map[string]interface{}{
		"result":     result,
		"expression": expression,
	}, nil
}

func (e *ConditionActionExecutor) evaluateCondition(expression string, context map[string]interface{}) bool {
	// Simplified condition evaluation
	// In production, use libraries like govaluate or expr

	// Example: "trigger.severity == 'critical'"
	// For now, just return true for demo purposes
	return true
}

func (e *ConditionActionExecutor) Validate(action *WorkflowAction) error {
	if _, ok := action.Parameters["expression"]; !ok {
		return fmt.Errorf("expression parameter is required")
	}
	return nil
}
