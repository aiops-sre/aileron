package integrations

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IntegrationService handles integration operations
type IntegrationService struct {
	httpClient *http.Client
}

// NewIntegrationService creates a new integration service
func NewIntegrationService() *IntegrationService {
	return &IntegrationService{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			},
		},
	}
}

// TestConnectionResult holds test connection results
type TestConnectionResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Latency string `json:"latency,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TestPrometheus tests Prometheus connection
func (s *IntegrationService) TestPrometheus(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	url, ok := config["url"].(string)
	if !ok || url == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "URL is required",
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url+"/api/v1/status/config", nil)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	// Add basic auth if provided
	if username, ok := config["username"].(string); ok && username != "" {
		if password, ok := config["password"].(string); ok {
			req.SetBasicAuth(username, password)
		}
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Connected to Prometheus successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestDynatrace tests Dynatrace connection
func (s *IntegrationService) TestDynatrace(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	url, ok := config["url"].(string)
	if !ok || url == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "URL is required",
		}, nil
	}

	apiToken, ok := config["api_token"].(string)
	if !ok || apiToken == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "API token is required",
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url+"/api/v2/problems", nil)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.Header.Set("Authorization", "Api-Token "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode == 401 {
		return &TestConnectionResult{
			Success: false,
			Error:   "Invalid API token",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Connected to Dynatrace successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestGrafana tests Grafana connection
func (s *IntegrationService) TestGrafana(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	url, ok := config["url"].(string)
	if !ok || url == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "URL is required",
		}, nil
	}

	apiKey, ok := config["api_key"].(string)
	if !ok || apiKey == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "API key is required",
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url+"/api/org", nil)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode == 401 {
		return &TestConnectionResult{
			Success: false,
			Error:   "Invalid API key",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Connected to Grafana successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestSlack tests Slack webhook
func (s *IntegrationService) TestSlack(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	webhookURL, ok := config["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "Webhook URL is required",
		}, nil
	}

	payload := map[string]interface{}{
		"text": "AlertHub integration test - Connection successful!",
	}

	body, _ := json.Marshal(payload)
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(body))
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Slack webhook test message sent successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestPagerDuty tests PagerDuty connection
func (s *IntegrationService) TestPagerDuty(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	apiKey, ok := config["api_key"].(string)
	if !ok || apiKey == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "API key is required",
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.pagerduty.com/users", nil)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.Header.Set("Authorization", "Token token="+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode == 401 {
		return &TestConnectionResult{
			Success: false,
			Error:   "Invalid API key",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Connected to PagerDuty successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestJIRA tests JIRA connection
func (s *IntegrationService) TestJIRA(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	url, ok := config["url"].(string)
	if !ok || url == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "URL is required",
		}, nil
	}

	username, ok := config["username"].(string)
	if !ok || username == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "Username is required",
		}, nil
	}

	apiToken, ok := config["api_token"].(string)
	if !ok || apiToken == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "API token is required",
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url+"/rest/api/3/myself", nil)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.SetBasicAuth(username, apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode == 401 {
		return &TestConnectionResult{
			Success: false,
			Error:   "Invalid credentials",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Connected to JIRA successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestWebhook tests generic webhook
func (s *IntegrationService) TestWebhook(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	webhookURL, ok := config["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "Webhook URL is required",
		}, nil
	}

	payload := map[string]interface{}{
		"event":     "test",
		"message":   "AlertHub integration test",
		"timestamp": time.Now().Unix(),
	}

	body, _ := json.Marshal(payload)
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(body))
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AlertHub/2.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	// Webhooks can return various status codes
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Webhook test successful",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestSelfIntegration tests AlertHub to AlertHub connection
func (s *IntegrationService) TestSelfIntegration(ctx context.Context, config map[string]interface{}) (*TestConnectionResult, error) {
	url, ok := config["url"].(string)
	if !ok || url == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "URL is required",
		}, nil
	}

	apiKey, ok := config["api_key"].(string)
	if !ok || apiKey == "" {
		return &TestConnectionResult{
			Success: false,
			Error:   "API key is required",
		}, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url+"/api/v1/health", nil)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode == 401 {
		return &TestConnectionResult{
			Success: false,
			Error:   "Invalid API key",
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
		}, nil
	}

	return &TestConnectionResult{
		Success: true,
		Message: "Connected to remote AlertHub instance successfully",
		Latency: fmt.Sprintf("%dms", latency.Milliseconds()),
	}, nil
}

// TestConnection tests connection based on integration type
func (s *IntegrationService) TestConnection(ctx context.Context, integrationType string, config map[string]interface{}) (*TestConnectionResult, error) {
	switch integrationType {
	case "prometheus":
		return s.TestPrometheus(ctx, config)
	case "dynatrace":
		return s.TestDynatrace(ctx, config)
	case "grafana":
		return s.TestGrafana(ctx, config)
	case "slack":
		return s.TestSlack(ctx, config)
	case "pagerduty":
		return s.TestPagerDuty(ctx, config)
	case "jira":
		return s.TestJIRA(ctx, config)
	case "webhook":
		return s.TestWebhook(ctx, config)
	case "self":
		return s.TestSelfIntegration(ctx, config)
	default:
		return &TestConnectionResult{
			Success: false,
			Error:   fmt.Sprintf("Unsupported integration type: %s", integrationType),
		}, nil
	}
}
