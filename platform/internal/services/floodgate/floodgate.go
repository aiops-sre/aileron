package floodgate

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	FloodgateBaseURL      = ""
	ClaudeAPIPath         = "/api/anthropic/v1/messages"
	GeminiAPIPathTemplate = "/api/gemini/v1/publishers/google/models/%s:generateContent"
	ModelsAPIPath         = "/api/openai/v1/models"
	AppleConnectPath      = "/usr/local/bin/appleconnect"
	ClientID              = "hvys3fcwcteqrvw3qzkvtk86viuoqv"
)

// FloodgateService handles interactions with Apple's Floodgate GenAI service
type FloodgateService struct {
	httpClient *http.Client
	tokenCache *tokenCache
	userToken  string
	userVPNIP  string // NEW: Store user's VPN IP for proxy requests
	maxRetries int
	retryDelay time.Duration
}

// tokenCache stores OAuth token with expiration
type tokenCache struct {
	token     string
	expiresAt time.Time
}

// NewFloodgateService creates a new Floodgate service instance
func NewFloodgateService() *FloodgateService {
	// Check for mTLS certificates (K8s production mode like Interlinked)
	certPath := "/narrative/kube-actor/cert.pem"
	keyPath := "/narrative/kube-actor/private.pem"

	transport := &http.Transport{}

	// Try to load mTLS certificates if available (K8s production).
	// When mTLS is active, enforce server cert verification unconditionally —
	// INTERNAL_TLS_INSECURE must not bypass a properly-signed mTLS connection.
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err == nil {
		log.Printf("Floodgate: Using mTLS certificates from K8s (like Interlinked)")
		transport.TLSClientConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	} else {
		log.Printf("Floodgate: mTLS certificates not found, will use user OAuth tokens")
		// No mTLS — allow opt-in TLS bypass for dev/non-prod environments only.
		//nolint:gosec
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true",
		}
	}

	return &FloodgateService{
		httpClient: &http.Client{
			Timeout:   180 * time.Second,
			Transport: transport,
		},
		tokenCache: &tokenCache{},
		maxRetries: 3,
		retryDelay: 2 * time.Second,
	}
}

// SetUserToken sets a token provided by the user's browser/device
func (s *FloodgateService) SetUserToken(token string) {
	s.userToken = token
	// Cache the token
	s.tokenCache.token = token
	s.tokenCache.expiresAt = time.Now().Add(50 * time.Minute)
}

// SetUserVPNIP sets the user's VPN IP for proxy requests
// This is required by Floodgate when using multi-audience tokens
func (s *FloodgateService) SetUserVPNIP(ip string) {
	s.userVPNIP = ip
}

// getToken retrieves OAuth token from appleconnect or user-provided token
func (s *FloodgateService) getToken() (string, error) {
	// First check if user provided token
	if s.userToken != "" {
		return s.userToken, nil
	}

	// Check cache first (tokens valid for ~1 hour)
	if s.tokenCache.token != "" && time.Now().Before(s.tokenCache.expiresAt) {
		return s.tokenCache.token, nil
	}

	// Check if appleconnect exists
	if _, err := exec.LookPath(AppleConnectPath); err != nil {
		// appleconnect not available (e.g., in Docker), return demo mode indication
		return "", fmt.Errorf("appleconnect not available in this environment (Docker container). User should provide token from their Mac")
	}

	cmd := exec.Command(
		AppleConnectPath,
		"getToken",
		"-C", ClientID,
		"--token-type=oauth",
		"--interactivity-type=none",
		"-E", "prod",
		"-G", "pkce",
		"-o", "openid,dsid,accountname,profile,groups",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to execute appleconnect: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "oauth-id-token") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) >= 2 {
				token := strings.TrimSpace(parts[1])
				// Cache token for 50 minutes (assuming 1 hour validity)
				s.tokenCache.token = token
				s.tokenCache.expiresAt = time.Now().Add(50 * time.Minute)
				return token, nil
			}
		}
	}

	return "", fmt.Errorf("oauth-id-token not found in appleconnect output")
}

// GetTokenDirect retrieves OAuth token directly via appleconnect command
// This is used during MAS login to automatically capture the token
// It includes retry logic similar to kubot for better reliability
func (s *FloodgateService) GetTokenDirect() (string, error) {
	return s.getTokenWithRetry()
}

// getTokenWithRetry implements retry logic similar to kubot
// Retries up to 3 times with exponential backoff (2, 4, 8 seconds)
func (s *FloodgateService) getTokenWithRetry() (string, error) {
	maxRetries := 3

	for retry := 0; retry <= maxRetries; retry++ {
		token, err := s.executeAppleConnect()
		if err == nil && token != "" {
			// Success! Cache the token
			s.tokenCache.token = token
			s.tokenCache.expiresAt = time.Now().Add(50 * time.Minute)
			return token, nil
		}

		if retry < maxRetries {
			// Exponential backoff: 2, 4, 8 seconds
			waitTime := time.Duration(1<<uint(retry)) * 2 * time.Second
			log.Printf("OAuth token retrieval failed (attempt %d/%d), retrying in %v: %v",
				retry+1, maxRetries+1, waitTime, err)
			time.Sleep(waitTime)
		}
	}

	return "", fmt.Errorf("failed to retrieve OAuth token after %d attempts", maxRetries+1)
}

// executeAppleConnect runs the appleconnect command once
func (s *FloodgateService) executeAppleConnect() (string, error) {
	// Check if appleconnect exists
	if _, err := exec.LookPath(AppleConnectPath); err != nil {
		return "", fmt.Errorf("appleconnect not available: %w", err)
	}

	cmd := exec.Command(
		AppleConnectPath,
		"getToken",
		"-C", ClientID,
		"--token-type=oauth",
		"--interactivity-type=none",
		"-E", "prod",
		"-G", "pkce",
		"-o", "openid,dsid,accountname,profile,groups",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("appleconnect command failed: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "oauth-id-token") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) >= 2 {
				token := strings.TrimSpace(parts[1])
				if token != "" {
					return token, nil
				}
			}
		}
	}

	return "", fmt.Errorf("oauth-id-token not found in appleconnect output")
}

// ChatRequest represents a chat request
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

// ChatMessage represents a single message in the conversation
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse represents the AI response
type ChatResponse struct {
	ID      string     `json:"id"`
	Model   string     `json:"model"`
	Message string     `json:"message"`
	Role    string     `json:"role"`
	Usage   TokenUsage `json:"usage,omitempty"`
}

// TokenUsage represents token usage statistics
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Claude-specific structures
type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
	System    string          `json:"system,omitempty"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content []claudeContent `json:"content"`
}

type claudeContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeResponse struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []claudeResponseContent `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type claudeResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Gemini-specific structures
type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	ResponseID string            `json:"responseId"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

// Chat sends a chat request to the specified model
func (s *FloodgateService) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if strings.HasPrefix(req.Model, "aws:") || strings.Contains(req.Model, "claude") {
		return s.chatClaude(ctx, req)
	} else if strings.Contains(req.Model, "gemini") {
		return s.chatGemini(ctx, req)
	}

	return nil, fmt.Errorf("unsupported model: %s", req.Model)
}

// chatClaude sends request to Claude via Floodgate
func (s *FloodgateService) chatClaude(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	token, err := s.getToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	// Convert to Claude format
	claudeReq := claudeRequest{
		Model:     strings.TrimPrefix(req.Model, "aws:"),
		MaxTokens: req.MaxTokens,
		Messages:  make([]claudeMessage, 0, len(req.Messages)),
	}

	if claudeReq.MaxTokens == 0 {
		claudeReq.MaxTokens = 8192
	}

	// Extract system message if present
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			claudeReq.System = msg.Content
		} else {
			claudeReq.Messages = append(claudeReq.Messages, claudeMessage{
				Role: msg.Role,
				Content: []claudeContent{
					{Type: "text", Text: msg.Content},
				},
			})
		}
	}

	jsonData, err := json.Marshal(claudeReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := FloodgateBaseURL + ClaudeAPIPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("User-Agent", "AlertHub/1.0")

	// NEW: Add X-Forwarded-For for proxy requests
	if s.userVPNIP != "" {
		httpReq.Header.Set("X-Forwarded-For", s.userVPNIP)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract text from content blocks
	var responseText string
	for _, content := range claudeResp.Content {
		if content.Type == "text" {
			responseText += content.Text
		}
	}

	return &ChatResponse{
		ID:      claudeResp.ID,
		Model:   req.Model,
		Message: responseText,
		Role:    claudeResp.Role,
		Usage: TokenUsage{
			PromptTokens:     claudeResp.Usage.InputTokens,
			CompletionTokens: claudeResp.Usage.OutputTokens,
			TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
		},
	}, nil
}

// chatGemini sends request to Gemini via Floodgate
func (s *FloodgateService) chatGemini(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	token, err := s.getToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	// Convert to Gemini format
	geminiReq := geminiRequest{
		Contents: make([]geminiContent, 0, len(req.Messages)),
	}

	// Extract system message if present
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			geminiReq.SystemInstruction = &geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			}
		} else {
			role := msg.Role
			if role == "assistant" {
				role = "model"
			}
			geminiReq.Contents = append(geminiReq.Contents, geminiContent{
				Role:  role,
				Parts: []geminiPart{{Text: msg.Content}},
			})
		}
	}

	jsonData, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := FloodgateBaseURL + fmt.Sprintf(GeminiAPIPathTemplate, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", "AlertHub/1.0")

	// NEW: Add X-Forwarded-For for proxy requests
	if s.userVPNIP != "" {
		httpReq.Header.Set("X-Forwarded-For", s.userVPNIP)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract text from first candidate
	var responseText string
	if len(geminiResp.Candidates) > 0 {
		for _, part := range geminiResp.Candidates[0].Content.Parts {
			responseText += part.Text
		}
	}

	return &ChatResponse{
		ID:      geminiResp.ResponseID,
		Model:   req.Model,
		Message: responseText,
		Role:    "assistant",
	}, nil
}

// ModelInfo represents available model information
type ModelInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	CreatedAt int64  `json:"created_at"`
}

// ListModels retrieves available models from Floodgate
func (s *FloodgateService) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Check if using mTLS (certificates loaded in transport)
	transport := s.httpClient.Transport.(*http.Transport)
	usingMTLS := len(transport.TLSClientConfig.Certificates) > 0

	url := FloodgateBaseURL + ModelsAPIPath
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Only set Authorization header if NOT using mTLS
	if !usingMTLS {
		token, err := s.getToken()
		if err != nil {
			return nil, fmt.Errorf("failed to get token: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	} else {
		log.Printf("Using mTLS authentication for Floodgate models")
	}

	// NEW: Add X-Forwarded-For for proxy requests (required by Floodgate)
	if s.userVPNIP != "" {
		httpReq.Header.Set("X-Forwarded-For", s.userVPNIP)
		log.Printf("Floodgate request with proxy headers (IP: %s)", s.userVPNIP)
	}

	// Identify as AlertHub proxy
	httpReq.Header.Set("User-Agent", "AlertHub/1.0")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var modelsResp struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
			Created int64  `json:"created"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(modelsResp.Data))
	for _, model := range modelsResp.Data {
		provider := "Unknown"
		name := model.ID

		if strings.HasPrefix(model.ID, "aws:") {
			provider = "AWS Claude"
			name = strings.TrimPrefix(model.ID, "aws:")
		} else if strings.HasPrefix(model.ID, "gcp:") {
			provider = "Google Gemini"
			name = strings.TrimPrefix(model.ID, "gcp:")
		}

		models = append(models, ModelInfo{
			ID:        model.ID,
			Name:      name,
			Provider:  provider,
			CreatedAt: model.Created,
		})
	}

	return models, nil
}

// HealthCheck verifies Floodgate connectivity
func (s *FloodgateService) HealthCheck(ctx context.Context) error {
	_, err := s.getToken()
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	return nil
}
