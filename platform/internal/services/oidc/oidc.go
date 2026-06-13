// Package oidc provides the AI provider integration layer.
// Replaces Apple Floodgate with a generic OIDC-protected AI provider.
// Configure via OIDC_PROVIDER_URL or use local Ollama (OLLAMA_URL).
package oidc

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
	"bytes"
	"encoding/json"
)

// ModelInfo describes an available AI model.
type ModelInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	Description string   `json:"description,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	Permission  []string `json:"permission,omitempty"`
}

// ChatMessage is one turn in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is an AI chat request.
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	UserID    string        `json:"user_id,omitempty"`
}

// ChatResponse is the response from an AI model.
type ChatResponse struct {
	ID      string      `json:"id,omitempty"`
	Role    string      `json:"role,omitempty"`
	Message ChatMessage `json:"message"`
	Model   string      `json:"model"`
	Done    bool        `json:"done"`
	Usage   TokenUsage  `json:"usage,omitempty"`
}

// Config holds OIDC provider configuration.
type Config struct {
	ProviderURL    string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	AdminGroups    []string
	OperatorGroups []string
	ViewerGroups   []string
	AutoProvision  bool
	DefaultRole    string
}

// DefaultConfig reads config from environment variables.
func DefaultConfig() *Config {
	return &Config{
		ProviderURL:  os.Getenv("OIDC_PROVIDER_URL"),
		ClientID:     os.Getenv("OIDC_CLIENT_ID"),
		ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		DefaultRole:  envOrDefault("OIDC_DEFAULT_ROLE", "viewer"),
		AutoProvision: os.Getenv("OIDC_AUTO_PROVISION") != "false",
	}
}

// OIDCProviderService handles AI model access via OIDC-protected provider.
// Falls back to local Ollama when OIDC_PROVIDER_URL is not set.
type OIDCProviderService struct {
	providerURL  string
	ollamaURL    string
	clientID     string
	clientSecret string
	userToken    string
	userVPNIP    string
	httpClient   *http.Client
}

// New creates an OIDCProviderService.
func New() *OIDCProviderService {
	return &OIDCProviderService{
		providerURL:  os.Getenv("OIDC_PROVIDER_URL"),
		ollamaURL:    envOrDefault("OLLAMA_URL", "http://ollama:11434"),
		clientID:     os.Getenv("OIDC_CLIENT_ID"),
		clientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		httpClient:   &http.Client{Timeout: 60 * time.Second},
	}
}

// IsEnabled returns true when a provider URL is configured.
func (s *OIDCProviderService) IsEnabled() bool {
	return s.providerURL != "" || s.ollamaURL != ""
}

// SetUserToken sets the bearer token for authenticated calls.
func (s *OIDCProviderService) SetUserToken(token string) { s.userToken = token }

// SetUserVPNIP sets the user VPN IP — no-op in OSS build.
func (s *OIDCProviderService) SetUserVPNIP(ip string) { s.userVPNIP = ip }

// HealthCheck verifies the AI provider is reachable.
func (s *OIDCProviderService) HealthCheck(ctx context.Context) error {
	url := s.ollamaURL + "/api/tags"
	if s.providerURL != "" {
		url = s.providerURL + "/health"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil { return err }
	resp, err := s.httpClient.Do(req)
	if err != nil { return fmt.Errorf("ai provider unreachable: %w", err) }
	resp.Body.Close()
	if resp.StatusCode >= 500 { return fmt.Errorf("ai provider unhealthy: status %d", resp.StatusCode) }
	return nil
}

// ListModels returns available models.
func (s *OIDCProviderService) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if s.providerURL != "" {
		// OIDC-protected provider
		return []ModelInfo{
			{ID: "claude-3-5-haiku",   Name: "Claude 3.5 Haiku",   Provider: "anthropic"},
			{ID: "claude-3-5-sonnet",  Name: "Claude 3.5 Sonnet",  Provider: "anthropic"},
			{ID: "claude-opus-4",      Name: "Claude Opus 4",      Provider: "anthropic"},
		}, nil
	}
	// Local Ollama
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ollamaURL+"/api/tags", nil)
	if err != nil { return nil, err }
	resp, err := s.httpClient.Do(req)
	if err != nil { return nil, fmt.Errorf("ollama unavailable: %w", err) }
	defer resp.Body.Close()
	var result struct{ Models []struct{ Name string `json:"name"` } `json:"models"` }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil { return nil, err }
	var out []ModelInfo
	for _, m := range result.Models {
		out = append(out, ModelInfo{ID: "local:" + m.Name, Name: m.Name, Provider: "ollama"})
	}
	return out, nil
}

// Chat sends a chat request to the configured AI provider.
func (s *OIDCProviderService) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Use OIDC-protected provider if configured, else local Ollama
	if s.providerURL != "" {
		return s.chatViaProvider(ctx, req)
	}
	return s.chatViaOllama(ctx, req)
}

func (s *OIDCProviderService) chatViaOllama(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if len(model) > 6 && model[:6] == "local:" { model = model[6:] }
	body, _ := json.Marshal(map[string]interface{}{
		"model":    model,
		"messages": req.Messages,
		"stream":   false,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.ollamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil { return nil, err }
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil { return nil, fmt.Errorf("ollama chat: %w", err) }
	defer resp.Body.Close()
	var result struct {
		Message ChatMessage `json:"message"`
		Done    bool        `json:"done"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil { return nil, err }
	return &ChatResponse{Message: result.Message, Done: result.Done, Model: model}, nil
}

func (s *OIDCProviderService) chatViaProvider(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Generic OIDC-protected AI provider (Keycloak-gated Claude, Bedrock, Vertex AI, etc.)
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.providerURL+"/v1/chat", bytes.NewReader(body))
	if err != nil { return nil, err }
	httpReq.Header.Set("Content-Type", "application/json")
	if s.userToken != "" { httpReq.Header.Set("Authorization", "Bearer "+s.userToken) } else {
		httpReq.SetBasicAuth(s.clientID, s.clientSecret)
	}
	resp, err := s.httpClient.Do(httpReq)
	if err != nil { return nil, fmt.Errorf("oidc provider chat: %w", err) }
	defer resp.Body.Close()
	var result ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil { return nil, err }
	return &result, nil
}

// GetToken returns an access token for the given scope.
func (s *OIDCProviderService) GetToken(ctx context.Context, scope string) (string, error) {
	if s.userToken != "" { return s.userToken, nil }
	return "", fmt.Errorf("no token configured; set OIDC user token or configure OIDC_CLIENT_ID/SECRET")
}

// UserProvisioner auto-provisions users authenticated via OIDC.
type UserProvisioner struct {
	db     interface{}
	config interface{}
}

// NewUserProvisioner creates a UserProvisioner.
func NewUserProvisioner(db interface{}, cfg interface{}) *UserProvisioner {
	return &UserProvisioner{db: db, config: cfg}
}


func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" { return v }
	return def
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UserContext holds authenticated user information from OIDC claims.
type UserContext struct {
	UserID   string   `json:"user_id"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Groups   []string `json:"groups"`
	Role     string   `json:"role"`
	Token    string   `json:"-"`
}
