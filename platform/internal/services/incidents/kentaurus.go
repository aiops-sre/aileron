package incidents

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	IncidentManagerAPIURL     = ""
	IncidentManagerConsumerID = "928952"
	SecretsManagerURL          = "/v1/my-secrets/incident_manager-token?buckets=Help-Central"
	// IncidentManager tokens have a 100-minute TTL; refresh 10 minutes before expiry
	tokenTTL           = 100 * time.Minute
	tokenRefreshBuffer = 10 * time.Minute
	// Fallback fixed TTL when the token format doesn't contain an embedded timestamp
	tokenFallbackTTL = 90 * time.Minute
)

// IncidentManagerClient handles IncidentManager API interactions
type IncidentManagerClient struct {
	httpClient    *http.Client
	secrets_managerClient *http.Client // separate client with mTLS cert for SecretsManager
	mu            sync.RWMutex
	cachedToken   string
	tokenExpiry   time.Time
	tokenSource   string // "secrets_manager" or "oidc"
}

// NewIncidentManagerClient creates a new IncidentManager API client
func NewIncidentManagerClient() *IncidentManagerClient {
	c := &IncidentManagerClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
			},
		},
	}
	c.secrets_managerClient = c.buildSecretsManagerClient()
	return c
}

// buildSecretsManagerClient builds an http.Client with the mTLS cert for SecretsManager.
// Cert paths come from env vars (mounted from K8s secret); falls back to
// well-known in-pod paths used by the init container.
func (c *IncidentManagerClient) buildSecretsManagerClient() *http.Client {
	certPath := os.Getenv("WHISPER_CLIENT_CERT")
	keyPath := os.Getenv("WHISPER_CLIENT_KEY")
	if certPath == "" {
		certPath = "/secrets/secrets_manager/tls.crt"
	}
	if keyPath == "" {
		keyPath = "/secrets/secrets_manager/tls.key"
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		// Cert not available — secrets_managerClient will be nil, fall back to OIDC
		return nil
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      x509.NewCertPool(),
	}

	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
}

// parseTokenExpiry extracts the generation timestamp from the IncidentManager token format
// (<base64>=:<sessionid>:<epochmillis>:<version>) and returns expiry = generation + TTL - buffer.
// Falls back to now + fallbackTTL if the token doesn't match the expected format.
func parseTokenExpiry(token string) time.Time {
	parts := strings.SplitN(token, ":", 4)
	if len(parts) >= 3 {
		epochMs, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil && epochMs > 0 {
			generatedAt := time.UnixMilli(epochMs)
			return generatedAt.Add(tokenTTL - tokenRefreshBuffer)
		}
	}
	return time.Now().Add(tokenFallbackTTL)
}

// getToken returns a valid IncidentManager auth token.
// Prefers SecretsManager (auto-rotated); falls back to OIDC token generation.
func (c *IncidentManagerClient) getToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		tok := c.cachedToken
		c.mu.RUnlock()
		return tok, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	var (
		token  string
		source string
		err    error
	)

	if c.secrets_managerClient != nil {
		token, err = c.fetchTokenFromSecretsManager(ctx)
		if token != "" {
			source = "secrets_manager"
		}
	}
	if token == "" {
		token, err = c.fetchTokenFromOIDC(ctx)
		if token != "" {
			source = "oidc"
		}
	}
	if err != nil {
		return "", err
	}

	c.cachedToken = token
	c.tokenSource = source
	c.tokenExpiry = parseTokenExpiry(token)
	return token, nil
}

// fetchTokenFromSecretsManager reads the auto-rotated token from Aileron SecretsManager secrets.
func (c *IncidentManagerClient) fetchTokenFromSecretsManager(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", SecretsManagerURL, nil)
	if err != nil {
		return "", fmt.Errorf("secrets_manager request build failed: %w", err)
	}

	resp, err := c.secrets_managerClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("secrets_manager request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("secrets_manager read failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("secrets_manager returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("secrets_manager parse failed: %w", err)
	}
	if result.Secret == "" {
		return "", fmt.Errorf("secrets_manager returned empty secret")
	}

	// SecretsManager stores the token base64-encoded
	decoded, err := base64.StdEncoding.DecodeString(result.Secret)
	if err != nil {
		// Not base64 — use as-is
		return result.Secret, nil
	}
	return string(decoded), nil
}

// fetchTokenFromOIDC generates a short-lived token via Aileron OIDC app-to-app auth.
func (c *IncidentManagerClient) fetchTokenFromOIDC(ctx context.Context) (string, error) {
	appID := os.Getenv("OIDC_APP_ID")
	appPassword := os.Getenv("OIDC_APP_PASSWORD")
	if appID == "" {
		appID = IncidentManagerConsumerID
	}
	if appPassword == "" {
		return "", fmt.Errorf("neither SecretsManager cert nor OIDC_APP_PASSWORD is configured")
	}

	payload := map[string]interface{}{
		"appId":          appID,
		"appPassword":    appPassword,
		"otherApp":       "150899",
		"context":        "#GrandPrix#",
		"oneTimeToken":   "false",
		"contextVersion": 3,
		"timeToLive":     6000000,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://oidcservice.example.com/auth/apptoapp/token/generate",
		bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("OIDC request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("cache-control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OIDC request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("OIDC returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("OIDC parse failed: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("OIDC returned empty token")
	}
	return result.Token, nil
}

// CreateIncidentRequest represents the request to create an External incident
type CreateIncidentRequest struct {
	Module               string `json:"module"`
	CallingApp           string `json:"callingApp"`
	Title                string `json:"title"`
	Description          string `json:"description"`
	Configuration        string `json:"configuration,omitempty"`
	Impact               string `json:"impact,omitempty"`
	Urgency              string `json:"urgency,omitempty"`
	Priority             string `json:"priority,omitempty"`
	AssignmentGroupID    string `json:"assignmentGroupId,omitempty"`
	AssignedPersonID     string `json:"assignedPersonId,omitempty"`
	BusinessOrganization string `json:"businessOrganization,omitempty"`
	Environment          string `json:"environment,omitempty"`
	ContactType          string `json:"contactType,omitempty"`
}

// QueryIncidentsRequest represents the request to query External incidents
type QueryIncidentsRequest struct {
	Module      string `json:"module"`
	Number      string `json:"number"`
	Query       string `json:"query"`
	Count       int    `json:"count"`
	Offset      int    `json:"offset"`
	SortOrder   string `json:"sortOrder"`
	SortByField string `json:"sortByField,omitempty"`
}

// UpdateIncidentRequest represents the request to update an External incident
type UpdateIncidentRequest struct {
	Module             string `json:"module"`
	TicketID           string `json:"ticketId"`
	DsPrsID            string `json:"dsPrsId"`
	Title              string `json:"title,omitempty"`
	Description        string `json:"description,omitempty"`
	TicketStatus       string `json:"ticketStatus,omitempty"`
	Impact             string `json:"impact,omitempty"`
	Urgency            string `json:"urgency,omitempty"`
	Priority           string `json:"priority,omitempty"`
	AssignmentGroupID  string `json:"assignmentGroupId,omitempty"`
	AssignedPersonID   string `json:"assignedPersonId,omitempty"`
	WorkLog            string `json:"workLog,omitempty"`
	AdditionalComments string `json:"additionalComments,omitempty"`
	Resolution         string `json:"resolution,omitempty"`
	ResolutionSummary  string `json:"resolutionSummary,omitempty"`
}

// ReopenIncidentRequest represents the request to reopen an External incident
type ReopenIncidentRequest struct {
	Module            string `json:"module"`
	Number            string `json:"number"`
	DsPrsID           string `json:"dsPrsId"`
	Action            string `json:"action"`
	Comment           string `json:"comment"`
	AssignmentGroupID string `json:"assignmentGroupId,omitempty"`
	AssignedPersonID  string `json:"assignedPersonId,omitempty"`
}

// IncidentManagerResponse represents the IncidentManager API response
type IncidentManagerResponse struct {
	Result struct {
		Status struct {
			HTTPStatusCode int    `json:"httpStatusCode"`
			State          string `json:"state"`
			Message        string `json:"message"`
			AdditionalInfo string `json:"additionalInfo"`
		} `json:"status"`
		Meta struct {
			UUID             string `json:"UUID"`
			Environment      string `json:"environment"`
			Module           string `json:"module,omitempty"`
			Query            string `json:"query,omitempty"`
			QueryResultCount int    `json:"queryResultCount,omitempty"`
			ResultCount      int    `json:"resultCount,omitempty"`
			Offset           int    `json:"offset,omitempty"`
			SortOrder        string `json:"sortOrder,omitempty"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	} `json:"result"`
}

func (c *IncidentManagerClient) CreateIncident(ctx context.Context, req *CreateIncidentRequest) (*IncidentManagerResponse, error) {
	return c.makeRequest(ctx, "POST", "/createRecords", req)
}

func (c *IncidentManagerClient) QueryIncidents(ctx context.Context, req *QueryIncidentsRequest) (*IncidentManagerResponse, error) {
	return c.makeRequest(ctx, "POST", "/queryRecords", req)
}

func (c *IncidentManagerClient) UpdateIncident(ctx context.Context, req *UpdateIncidentRequest) (*IncidentManagerResponse, error) {
	return c.makeRequest(ctx, "POST", "/updateRecords", req)
}

func (c *IncidentManagerClient) ReopenIncident(ctx context.Context, req *ReopenIncidentRequest) (*IncidentManagerResponse, error) {
	return c.makeRequest(ctx, "POST", "/updateRecords", req)
}

func (c *IncidentManagerClient) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*IncidentManagerResponse, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	url := IncidentManagerAPIURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP_HEADER_KENTAURUS_CONSUMER_ID", IncidentManagerConsumerID)
	req.Header.Set("HTTP_HEADER_KENTAURUS_AUTH_TOKEN", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// On 401 the token was rejected — clear cache and retry with a fresh token.
	// If the previous token came from SecretsManager, fall back to OIDC directly to avoid
	// using a SecretsManager token that IncidentManager may have already invalidated.
	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		prevSource := c.tokenSource
		c.cachedToken = ""
		c.tokenSource = ""
		c.mu.Unlock()

		var (
			freshToken string
			tokenErr   error
		)
		if prevSource == "secrets_manager" {
			// SecretsManager token was rejected — try OIDC directly
			freshToken, tokenErr = c.fetchTokenFromOIDC(ctx)
			if tokenErr == nil && freshToken != "" {
				c.mu.Lock()
				c.cachedToken = freshToken
				c.tokenSource = "oidc"
				c.tokenExpiry = parseTokenExpiry(freshToken)
				c.mu.Unlock()
			}
		}
		if freshToken == "" {
			// Either prevSource was oidc, or OIDC also failed — try getToken (full chain)
			freshToken, tokenErr = c.getToken(ctx)
		}
		if tokenErr != nil {
			return nil, fmt.Errorf("re-authentication failed: %w", tokenErr)
		}

		req2, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonBody))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("HTTP_HEADER_KENTAURUS_CONSUMER_ID", IncidentManagerConsumerID)
		req2.Header.Set("HTTP_HEADER_KENTAURUS_AUTH_TOKEN", freshToken)

		resp2, err2 := c.httpClient.Do(req2)
		if err2 != nil {
			return nil, fmt.Errorf("retry request failed: %w", err2)
		}
		defer resp2.Body.Close()
		respBody, _ = io.ReadAll(resp2.Body)
		resp = resp2
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("incident_manager API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var incident_managerResp IncidentManagerResponse
	if err := json.Unmarshal(respBody, &incident_managerResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// IncidentManager returns "warning" when it clamps input parameters (e.g. count > max).
	// Treat "warning" as success — the response still contains valid data.
	state := incident_managerResp.Result.Status.State
	if state != "success" && state != "warning" {
		return nil, fmt.Errorf("incident_manager API error: %s - %s",
			incident_managerResp.Result.Status.State,
			incident_managerResp.Result.Status.Message)
	}

	return &incident_managerResp, nil
}
