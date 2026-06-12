package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client makes HTTP calls to a locally-deployed Ollama service.
type Client struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewClient constructs an Ollama client.
// baseURL example: "http://ollama.aileron.svc.cluster.local:11434"
// model example: "qwen2.5:3b"
func NewClient(baseURL, model string) *Client {
	return &Client{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.model }

// generateRequest is the Ollama /api/generate request body.
type generateRequest struct {
	Model       string          `json:"model"`
	Prompt      string          `json:"prompt"`
	Stream      bool            `json:"stream"`
	Format      string          `json:"format,omitempty"`
	Options     *ollamaOptions  `json:"options,omitempty"`
}

// ollamaOptions controls generation behaviour.
// temperature=0.1 minimises hallucination for factual SRE narratives.
// top_p=0.9 keeps output on-distribution.
type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
	Seed        int     `json:"seed"` // deterministic across equal inputs
}

// generateResponse is the Ollama /api/generate response body.
type generateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

// Generate sends a prompt to Ollama and returns the completion.
// Returns an error if Ollama is unavailable or the request times out.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	if c.baseURL == "" {
		return "", fmt.Errorf("ollama base URL not configured")
	}

	reqBody, _ := json.Marshal(generateRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		// Low temperature = deterministic, fact-grounded output.
		// Default Ollama temperature is 0.7 which causes hallucination for RCA.
		Options: &ollamaOptions{
			Temperature: 0.1,
			TopP:        0.90,
			TopK:        40,
			Seed:        42,
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/api/generate",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return "", fmt.Errorf("building ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing ollama response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("ollama error: %s", result.Error)
	}

	return result.Response, nil
}

// Ping checks whether Ollama is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama ping returned HTTP %d", resp.StatusCode)
	}
	return nil
}
