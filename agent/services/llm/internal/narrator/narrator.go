// Package narrator implements the incident narrator with Claude API + Ollama fallback.
//
// Priority chain:
//   1. Claude API (if CLAUDE_API_KEY is set) — highest quality narratives
//   2. Ollama (if OLLAMA_BASE_URL is set) — on-prem LLM, no internet required
//   3. Deterministic fallback — always works, structured but not fluent
//
// LLM role: NARRATOR ONLY — generates evidence-grounded summaries of WHAT happened.
// Recommendations come from the deterministic remediation engine, never from LLM output.
// Every sentence must cite a specific evidence item. Operators can audit against evidence.
package narrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	claudeAPIURL    = "https://api.anthropic.com/v1/messages"
	defaultClaude   = "claude-opus-4-8"
	defaultOllama   = "qwen2.5:7b"
	maxNarrativeLen = 1200
)

// Client narrates incidents using Claude API → Ollama → deterministic fallback.
type Client struct {
	claudeKey   string
	claudeModel string
	ollamaURL   string
	ollamaModel string
	http        *http.Client
}

// NewClient creates a narrator client.
// claudeKey: Anthropic API key (empty = skip Claude)
// claudeModel: model ID (empty = claude-opus-4-8)
// ollamaURL: base URL of Ollama (empty = skip Ollama)
// ollamaModel: model to use in Ollama (empty = qwen2.5:7b)
func NewClient(claudeKey, claudeModel, ollamaURL, ollamaModel string) *Client {
	if claudeModel == "" {
		claudeModel = defaultClaude
	}
	if ollamaModel == "" {
		ollamaModel = defaultOllama
	}
	return &Client{
		claudeKey:   claudeKey,
		claudeModel: claudeModel,
		ollamaURL:   ollamaURL,
		ollamaModel: ollamaModel,
		http:        &http.Client{Timeout: 60 * time.Second},
	}
}

// NarrativeRequest is the full incident context fed to the narrator.
type NarrativeRequest struct {
	IncidentID       string
	ClusterID        string
	Title            string
	DetectedAt       time.Time
	EvidenceGrade    string
	Confidence       float64
	RootCause        *RootCauseSummary
	Signals          []SignalSummary
	Actions          []ActionSummary
	SLOImpact        *SLOSummary
	SecurityFindings []SecurityFinding
}

type RootCauseSummary struct {
	EntityKind     string
	EntityName     string
	EntityNS       string
	Confidence     float64
	ConfidenceBand string
	FailureMode    string
}
type SignalSummary struct {
	Source       string
	Type         string
	Description  string
	Strength     float64
	ResourceKind string
	ResourceName string
	Namespace    string
	OccurredAt   time.Time
}
type ActionSummary struct{ Type, Target, Command, Rationale string; Confidence float64 }
type SLOSummary struct{ Service, SLOName string; BurnRate, BudgetPct float64 }
type SecurityFinding struct{ RuleID, Severity, Title string }

// Narrative is the LLM output.
type Narrative struct {
	IncidentID   string    `json:"incident_id"`
	Text         string    `json:"text"`
	Model        string    `json:"model"`
	GeneratedAt  time.Time `json:"generated_at"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
}

// Generate produces a narrative using the best available backend.
func (c *Client) Generate(ctx context.Context, req NarrativeRequest) (*Narrative, error) {
	sys := buildSystemPrompt()
	user := buildUserPrompt(req)

	// 1. Try Claude API
	if c.claudeKey != "" {
		if n, err := c.callClaude(ctx, sys, user, req.IncidentID); err == nil {
			return n, nil
		}
	}

	// 2. Try Ollama
	if c.ollamaURL != "" {
		if n, err := c.callOllama(ctx, sys, user, req.IncidentID); err == nil {
			return n, nil
		}
	}

	// 3. Deterministic fallback — never fails
	return fallbackNarrative(req), nil
}

// ─── Claude API ───────────────────────────────────────────────────────────────

func (c *Client) callClaude(ctx context.Context, system, user, incidentID string) (*Narrative, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":      c.claudeModel,
		"max_tokens": maxNarrativeLen,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", claudeAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.claudeKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var ar struct {
		Content []struct{ Type, Text string } `json:"content"`
		Usage   struct{ InputTokens, OutputTokens int `json:"input_tokens,output_tokens"` } `json:"usage"`
	}
	if err := json.Unmarshal(raw, &ar); err != nil || len(ar.Content) == 0 {
		return nil, fmt.Errorf("claude: bad response")
	}
	text := ""
	for _, c := range ar.Content {
		if c.Type == "text" {
			text = strings.TrimSpace(c.Text)
			break
		}
	}
	if text == "" {
		return nil, fmt.Errorf("claude: empty response")
	}
	return &Narrative{
		IncidentID:   incidentID,
		Text:         text,
		Model:        c.claudeModel,
		GeneratedAt:  time.Now(),
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
	}, nil
}

// ─── Ollama ───────────────────────────────────────────────────────────────────

func (c *Client) callOllama(ctx context.Context, system, user, incidentID string) (*Narrative, error) {
	fullPrompt := system + "\n\n" + user
	body, _ := json.Marshal(map[string]interface{}{
		"model":  c.ollamaModel,
		"prompt": fullPrompt,
		"stream": false,
	})
	url := strings.TrimRight(c.ollamaURL, "/") + "/api/generate"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var or struct {
		Response string `json:"response"`
		Done     bool   `json:"done"`
	}
	if err := json.Unmarshal(raw, &or); err != nil || !or.Done {
		return nil, fmt.Errorf("ollama: bad response")
	}
	text := strings.TrimSpace(or.Response)
	if text == "" {
		return nil, fmt.Errorf("ollama: empty response")
	}
	return &Narrative{
		IncidentID:  incidentID,
		Text:        text,
		Model:       "ollama/" + c.ollamaModel,
		GeneratedAt: time.Now(),
	}, nil
}

// ─── Deterministic fallback ───────────────────────────────────────────────────

func fallbackNarrative(req NarrativeRequest) *Narrative {
	var b strings.Builder
	fmt.Fprintf(&b, "Incident %s detected at %s in cluster %s. ",
		req.IncidentID, req.DetectedAt.Format("15:04:05 UTC"), req.ClusterID)
	if req.RootCause != nil {
		rc := req.RootCause
		fmt.Fprintf(&b, "Root cause: %s %s/%s — %s (confidence %.0f%%). ",
			rc.EntityKind, rc.EntityNS, rc.EntityName, rc.FailureMode, rc.Confidence*100)
	}
	fmt.Fprintf(&b, "%d evidence signals. Grade: %s. ", len(req.Signals), req.EvidenceGrade)
	if req.SLOImpact != nil {
		fmt.Fprintf(&b, "SLO %q burn rate %.1fx, budget %.1f%% remaining. ",
			req.SLOImpact.SLOName, req.SLOImpact.BurnRate, req.SLOImpact.BudgetPct)
	}
	if req.EvidenceGrade == "F" || req.EvidenceGrade == "D" {
		b.WriteString("Evidence incomplete — manual inspection required.")
	} else {
		b.WriteString("See recommended actions from the remediation engine.")
	}
	return &Narrative{IncidentID: req.IncidentID, Text: b.String(), Model: "fallback", GeneratedAt: time.Now()}
}

// ─── Prompts ──────────────────────────────────────────────────────────────────

func buildSystemPrompt() string {
	return `You are KubeSense's incident narrator. Narrate WHAT happened — never decide what to do.
Rules:
1. Only describe what the evidence shows. Never invent details.
2. Reference specific signals: "Signal #N shows..."
3. Under 250 words. Plain SRE language.
4. Structure: (1) What happened (2) Where it started (3) How it spread (4) Evidence confidence note.
5. If confidence is LOW, say evidence is incomplete.
6. Do NOT suggest actions — those come from the deterministic engine.`
}

func buildUserPrompt(req NarrativeRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INCIDENT: %s | Cluster: %s | Detected: %s\n",
		req.IncidentID, req.ClusterID, req.DetectedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Title: %s\nGrade: %s | Confidence: %.0f%%\n\n",
		req.Title, req.EvidenceGrade, req.Confidence*100)
	if req.RootCause != nil {
		rc := req.RootCause
		fmt.Fprintf(&b, "ROOT CAUSE: %s %s/%s — %s (%.0f%% %s)\n\n",
			rc.EntityKind, rc.EntityNS, rc.EntityName, rc.FailureMode, rc.Confidence*100, rc.ConfidenceBand)
	}
	fmt.Fprintf(&b, "SIGNALS (%d):\n", len(req.Signals))
	for i, s := range req.Signals {
		fmt.Fprintf(&b, "  [%d] %s | %s/%s/%s | %.0f%% | %s\n",
			i+1, s.Source, s.ResourceKind, s.Namespace, s.ResourceName, s.Strength*100, s.Description)
	}
	if req.SLOImpact != nil {
		slo := req.SLOImpact
		fmt.Fprintf(&b, "\nSLO IMPACT: %s %s — burn %.1fx budget %.1f%% remaining\n",
			slo.Service, slo.SLOName, slo.BurnRate, slo.BudgetPct)
	}
	if len(req.SecurityFindings) > 0 {
		fmt.Fprintf(&b, "\nSECURITY:\n")
		for _, sf := range req.SecurityFindings {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", sf.Severity, sf.RuleID, sf.Title)
		}
	}
	b.WriteString("\nNarrate this incident following your rules.")
	return b.String()
}
