package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// LLMEnricher calls the in-cluster Ollama LLM to generate a root-cause
// analysis narrative for auto-created incidents. Best-effort: if the LLM
// is unreachable or times out the caller receives an empty string.
//
// Per-role model routing (LLMFit/Aurora pattern):
//   LLM_TRIAGE_MODEL   — fast model for alert triage (default: same as LLM_MODEL)
//   LLM_RCA_MODEL      — quality model for RCA synthesis (default: same as LLM_MODEL)
//   LLM_NARRATIVE_MODEL — model for human-readable narrative generation (default: same)
type LLMEnricher struct {
	serviceURL     string
	model          string // default model
	triageModel    string // fast: alert classification
	rcaModel       string // quality: root-cause analysis
	narrativeModel string // narrative generation
	client         *http.Client
}

// ModelRole selects the appropriate model for the task.
type ModelRole string

const (
	ModelRoleTriage    ModelRole = "triage"
	ModelRoleRCA       ModelRole = "rca"
	ModelRoleNarrative ModelRole = "narrative"
)

// NewLLMEnricher creates an enricher pointed at the cluster-local Ollama.
func NewLLMEnricher() *LLMEnricher {
	url := os.Getenv("LLM_SERVICE_URL")
	if url == "" {
		ns := os.Getenv("POD_NAMESPACE")
		if ns == "" {
			ns = "aileron"
		}
		url = fmt.Sprintf("http://ollama.%s.svc.cluster.local:11434", ns)
	}
	defaultModel := os.Getenv("LLM_MODEL")
	if defaultModel == "" {
		defaultModel = "qwen2.5:3b"
	}
	// Per-role overrides fall back to the default model when not set.
	triageModel := os.Getenv("LLM_TRIAGE_MODEL")
	if triageModel == "" {
		triageModel = defaultModel
	}
	rcaModel := os.Getenv("LLM_RCA_MODEL")
	if rcaModel == "" {
		rcaModel = defaultModel
	}
	narrativeModel := os.Getenv("LLM_NARRATIVE_MODEL")
	if narrativeModel == "" {
		narrativeModel = defaultModel
	}
	e := &LLMEnricher{
		serviceURL:     url,
		model:          defaultModel,
		triageModel:    triageModel,
		rcaModel:       rcaModel,
		narrativeModel: narrativeModel,
		client:         &http.Client{Timeout: 10 * time.Second},
	}
	log.Printf("LLMEnricher initialized: default=%s triage=%s rca=%s narrative=%s url=%s",
		e.model, e.triageModel, e.rcaModel, e.narrativeModel, e.serviceURL)
	return e
}

// modelFor returns the model name for the given role.
func (e *LLMEnricher) modelFor(role ModelRole) string {
	switch role {
	case ModelRoleTriage:
		return e.triageModel
	case ModelRoleRCA:
		return e.rcaModel
	case ModelRoleNarrative:
		return e.narrativeModel
	default:
		return e.model
	}
}

// GenerateRCA asks the LLM for a concise root-cause analysis paragraph.
// Uses the RCA role model (quality-optimized).
// Accepts optional structured findings for higher-accuracy prompts (K8sGPT pattern).
// Returns empty string on any error so callers can proceed without RCA.
func (e *LLMEnricher) GenerateRCA(
	ctx context.Context,
	alertTitle, severity string,
	matchedNode, matchedNodeType, rootCause, topologyPath string,
	strategyScores map[string]float64,
) string {
	return e.generateRCAWithFindings(ctx, alertTitle, severity, matchedNode, matchedNodeType, rootCause, topologyPath, strategyScores, nil)
}

// GenerateRCAWithFindings is like GenerateRCA but injects pre-analyzed structured findings
// (from Analyze()) into the prompt for significantly improved accuracy (K8sGPT pattern).
func (e *LLMEnricher) GenerateRCAWithFindings(
	ctx context.Context,
	alertTitle, severity string,
	matchedNode, matchedNodeType, rootCause, topologyPath string,
	strategyScores map[string]float64,
	findings []Finding,
) string {
	return e.generateRCAWithFindings(ctx, alertTitle, severity, matchedNode, matchedNodeType, rootCause, topologyPath, strategyScores, findings)
}

func (e *LLMEnricher) generateRCAWithFindings(
	ctx context.Context,
	alertTitle, severity string,
	matchedNode, matchedNodeType, rootCause, topologyPath string,
	strategyScores map[string]float64,
	findings []Finding,
) string {
	// ── Input anonymization (K8sGPT + Aurora NeMo Guardrails) ────────────────
	// Strip internal IPs, UIDs, hostnames, and credentials before LLM call.
	alertTitle, _ = DefaultLLMGuard.Anonymize(alertTitle)
	matchedNode, _ = DefaultLLMGuard.Anonymize(matchedNode)
	rootCause, _ = DefaultLLMGuard.Anonymize(rootCause)
	topologyPath, _ = DefaultLLMGuard.Anonymize(topologyPath)
	if len(findings) > 0 {
		findings = DefaultLLMGuard.AnonymizeFindings(findings)
	}
	// Block if alert title contains prompt-injection patterns.
	if reason := DefaultLLMGuard.ValidateInput(alertTitle); reason != "" {
		log.Printf("LLMEnricher: input blocked (%s) for alert '%s'", reason, alertTitle)
		return ""
	}

	var sb strings.Builder
	// Strict context isolation: the prompt must only reference data passed
	// as explicit parameters — prevents cross-incident hallucination.
	sb.WriteString("You are an SRE on-call assistant. Analyze ONLY this specific incident.\n")
	sb.WriteString("Write a concise 2-3 sentence root cause analysis in the format:\n")
	sb.WriteString("Error: <error class>\n")
	sb.WriteString("Solution: <recommended remediation step>\n\n")
	sb.WriteString(fmt.Sprintf("Alert: %s\nSeverity: %s\n", alertTitle, severity))

	// Inject structured findings for precise error taxonomy (K8sGPT pattern).
	if len(findings) > 0 {
		sb.WriteString("\nStructured findings:\n")
		sb.WriteString(FormatFindingsForLLM(findings))
		sb.WriteString("\n")
	}

	if matchedNode != "" {
		nodeType := strings.ReplaceAll(matchedNodeType, "_", " ")
		sb.WriteString(fmt.Sprintf("Affected component: %s (type: %s)\n", matchedNode, nodeType))
	}
	if rootCause != "" && rootCause != matchedNode {
		sb.WriteString(fmt.Sprintf("Root cause entity: %s\n", rootCause))
	}
	if topologyPath != "" {
		sb.WriteString(fmt.Sprintf("Infrastructure path: %s\n", topologyPath))
	}

	var topStrategy string
	var topScore float64
	for k, v := range strategyScores {
		if v > topScore {
			topScore = v
			topStrategy = k
		}
	}
	if topStrategy != "" && topScore > 0 {
		sb.WriteString(fmt.Sprintf("Correlation method: %s (%.0f%% confidence)\n", topStrategy, topScore*100))
	}

	sb.WriteString("\nRCA (2-3 sentences, Error: / Solution: format, specific and technical):")

	model := e.modelFor(ModelRoleRCA)
	body, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": sb.String(),
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.1,
			"num_predict": 200,
		},
	})

	llmCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(llmCtx, http.MethodPost, e.serviceURL+"/api/generate", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("LLMEnricher.GenerateRCA: request build failed model=%s: %v", model, err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		log.Printf("LLMEnricher.GenerateRCA: request failed model=%s url=%s: %v", model, e.serviceURL, err)
		return ""
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("LLMEnricher.GenerateRCA: body read failed model=%s: %v", model, err)
		return ""
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("LLMEnricher.GenerateRCA: non-200 status=%d model=%s body=%.100s",
			resp.StatusCode, model, string(raw))
		return ""
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		log.Printf("LLMEnricher.GenerateRCA: json decode failed model=%s: %v", model, err)
		return ""
	}

	rca := strings.TrimSpace(result.Response)
	// ── Output validation (Aurora NeMo pattern) ───────────────────────────────
	if reason := DefaultLLMGuard.ValidateOutput(rca); reason != "" {
		log.Printf("LLMEnricher: output rejected (%s) model=%s", reason, model)
		return ""
	}
	if rca != "" {
		log.Printf("LLM RCA generated model=%s alert='%s' (%d chars)", model, alertTitle, len(rca))
	} else {
		log.Printf("LLMEnricher.GenerateRCA: empty response model=%s", model)
	}
	return rca
}
