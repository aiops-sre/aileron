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
	KentaurusAPIURL     = ""
	KentaurusConsumerID = "928952"
	WhisperURL          = "/v1/my-secrets/kentaurus-token?buckets=Help-Central"
	// Kentaurus tokens have a 100-minute TTL; refresh 10 minutes before expiry
	tokenTTL           = 100 * time.Minute
	tokenRefreshBuffer = 10 * time.Minute
	// Fallback fixed TTL when the token format doesn't contain an embedded timestamp
	tokenFallbackTTL = 90 * time.Minute
)

// KentaurusClient handles Kentaurus API interactions
type KentaurusClient struct {
	httpClient    *http.Client
	whisperClient *http.Client // separate client with mTLS cert for Whisper
	mu            sync.RWMutex
	cachedToken   string
	tokenExpiry   time.Time
	tokenSource   string // "whisper" or "idms"
}

// NewKentaurusClient creates a new Kentaurus API client
func NewKentaurusClient() *KentaurusClient {
	c := &KentaurusClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
			},
		},
	}
	c.whisperClient = c.buildWhisperClient()
	return c
}

// buildWhisperClient builds an http.Client with the mTLS cert for Whisper.
// Cert paths come from env vars (mounted from K8s secret); falls back to
// well-known in-pod paths used by the init container.
func (c *KentaurusClient) buildWhisperClient() *http.Client {
	certPath := os.Getenv("WHISPER_CLIENT_CERT")
	keyPath := os.Getenv("WHISPER_CLIENT_KEY")
	if certPath == "" {
		certPath = "/secrets/whisper/tls.crt"
	}
	if keyPath == "" {
		keyPath = "/secrets/whisper/tls.key"
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		// Cert not available — whisperClient will be nil, fall back to IDMS
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

// parseTokenExpiry extracts the generation timestamp from the Kentaurus token format
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

// getToken returns a valid Kentaurus auth token.
// Prefers Whisper (auto-rotated); falls back to IDMS token generation.
func (c *KentaurusClient) getToken(ctx context.Context) (string, error) {
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

	if c.whisperClient != nil {
		token, err = c.fetchTokenFromWhisper(ctx)
		if token != "" {
			source = "whisper"
		}
	}
	if token == "" {
		token, err = c.fetchTokenFromIDMS(ctx)
		if token != "" {
			source = "idms"
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

// fetchTokenFromWhisper reads the auto-rotated token from Apple Whisper secrets.
func (c *KentaurusClient) fetchTokenFromWhisper(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", WhisperURL, nil)
	if err != nil {
		return "", fmt.Errorf("whisper request build failed: %w", err)
	}

	resp, err := c.whisperClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("whisper read failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("whisper parse failed: %w", err)
	}
	if result.Secret == "" {
		return "", fmt.Errorf("whisper returned empty secret")
	}

	// Whisper stores the token base64-encoded
	decoded, err := base64.StdEncoding.DecodeString(result.Secret)
	if err != nil {
		// Not base64 — use as-is
		return result.Secret, nil
	}
	return string(decoded), nil
}

// fetchTokenFromIDMS generates a short-lived token via Apple IDMS app-to-app auth.
func (c *KentaurusClient) fetchTokenFromIDMS(ctx context.Context) (string, error) {
	appID := os.Getenv("OIDC_APP_ID")
	appPassword := os.Getenv("IDMS_APP_PASSWORD")
	if appID == "" {
		appID = KentaurusConsumerID
	}
	if appPassword == "" {
		return "", fmt.Errorf("neither Whisper cert nor IDMS_APP_PASSWORD is configured")
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
		"https://idmsservice.example.com/auth/apptoapp/token/generate",
		bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("IDMS request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("cache-control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("IDMS request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("IDMS returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("IDMS parse failed: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("IDMS returned empty token")
	}
	return result.Token, nil
}

// CreateIncidentRequest represents the request to create an HCL incident
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

// QueryIncidentsRequest represents the request to query HCL incidents
type QueryIncidentsRequest struct {
	Module      string `json:"module"`
	Number      string `json:"number"`
	Query       string `json:"query"`
	Count       int    `json:"count"`
	Offset      int    `json:"offset"`
	SortOrder   string `json:"sortOrder"`
	SortByField string `json:"sortByField,omitempty"`
}

// UpdateIncidentRequest represents the request to update an HCL incident
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

// ReopenIncidentRequest represents the request to reopen an HCL incident
type ReopenIncidentRequest struct {
	Module            string `json:"module"`
	Number            string `json:"number"`
	DsPrsID           string `json:"dsPrsId"`
	Action            string `json:"action"`
	Comment           string `json:"comment"`
	AssignmentGroupID string `json:"assignmentGroupId,omitempty"`
	AssignedPersonID  string `json:"assignedPersonId,omitempty"`
}

// KentaurusResponse represents the Kentaurus API response
type KentaurusResponse struct {
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

func (c *KentaurusClient) CreateIncident(ctx context.Context, req *CreateIncidentRequest) (*KentaurusResponse, error) {
	return c.makeRequest(ctx, "POST", "/createRecords", req)
}

func (c *KentaurusClient) QueryIncidents(ctx context.Context, req *QueryIncidentsRequest) (*KentaurusResponse, error) {
	return c.makeRequest(ctx, "POST", "/queryRecords", req)
}

func (c *KentaurusClient) UpdateIncident(ctx context.Context, req *UpdateIncidentRequest) (*KentaurusResponse, error) {
	return c.makeRequest(ctx, "POST", "/updateRecords", req)
}

func (c *KentaurusClient) ReopenIncident(ctx context.Context, req *ReopenIncidentRequest) (*KentaurusResponse, error) {
	return c.makeRequest(ctx, "POST", "/updateRecords", req)
}

func (c *KentaurusClient) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*KentaurusResponse, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	url := KentaurusAPIURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP_HEADER_KENTAURUS_CONSUMER_ID", KentaurusConsumerID)
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
	// If the previous token came from Whisper, fall back to IDMS directly to avoid
	// using a Whisper token that Kentaurus may have already invalidated.
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
		if prevSource == "whisper" {
			// Whisper token was rejected — try IDMS directly
			freshToken, tokenErr = c.fetchTokenFromIDMS(ctx)
			if tokenErr == nil && freshToken != "" {
				c.mu.Lock()
				c.cachedToken = freshToken
				c.tokenSource = "idms"
				c.tokenExpiry = parseTokenExpiry(freshToken)
				c.mu.Unlock()
			}
		}
		if freshToken == "" {
			// Either prevSource was idms, or IDMS also failed — try getToken (full chain)
			freshToken, tokenErr = c.getToken(ctx)
		}
		if tokenErr != nil {
			return nil, fmt.Errorf("re-authentication failed: %w", tokenErr)
		}

		req2, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonBody))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("HTTP_HEADER_KENTAURUS_CONSUMER_ID", KentaurusConsumerID)
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
		return nil, fmt.Errorf("kentaurus API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var kentaurusResp KentaurusResponse
	if err := json.Unmarshal(respBody, &kentaurusResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Kentaurus returns "warning" when it clamps input parameters (e.g. count > max).
	// Treat "warning" as success — the response still contains valid data.
	state := kentaurusResp.Result.Status.State
	if state != "success" && state != "warning" {
		return nil, fmt.Errorf("kentaurus API error: %s - %s",
			kentaurusResp.Result.Status.State,
			kentaurusResp.Result.Status.Message)
	}

	return &kentaurusResp, nil
}
