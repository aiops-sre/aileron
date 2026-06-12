package postmortems

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PostmortemService auto-generates structured postmortems when incidents resolve.
// Implements the Aurora postmortem automation pattern: structured template with
// timeline, impact, RCA, contributing factors, remediation, and lessons learned.
type PostmortemService struct {
	db         *sql.DB
	ollamaURL  string
	ollamaModel string
	httpClient *http.Client
}

// PostmortemContent is the structured postmortem document.
type PostmortemContent struct {
	Title               string          `json:"title"`
	Severity            string          `json:"severity"`
	Status              string          `json:"status"`
	Duration            string          `json:"duration"`
	ImpactSummary       string          `json:"impact_summary"`
	RootCause           string          `json:"root_cause"`
	RCAConfidence       float64         `json:"rca_confidence"`
	ContributingFactors []string        `json:"contributing_factors"`
	Timeline            []TimelineEntry `json:"timeline"`
	Remediation         string          `json:"remediation"`
	LessonsLearned      []string        `json:"lessons_learned"`
	ActionItems         []ActionItem    `json:"action_items"`
	GeneratedAt         time.Time       `json:"generated_at"`
	GeneratedBy         string          `json:"generated_by"` // "llm" | "template"
}

type TimelineEntry struct {
	Timestamp   string `json:"timestamp"`
	Description string `json:"description"`
	Type        string `json:"type"` // "alert", "escalation", "investigation", "mitigation", "resolution"
}

type ActionItem struct {
	Description string `json:"description"`
	Priority    string `json:"priority"` // "high", "medium", "low"
	Type        string `json:"type"`     // "prevent", "detect", "respond"
}

func NewPostmortemService(db *sql.DB) *PostmortemService {
	url := os.Getenv("LLM_SERVICE_URL")
	if url == "" {
		ns := os.Getenv("POD_NAMESPACE")
		if ns == "" {
			ns = "aileron"
		}
		url = fmt.Sprintf("http://ollama.%s.svc.cluster.local:11434", ns)
	}
	model := os.Getenv("LLM_RCA_MODEL")
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	if model == "" {
		model = "qwen2.5:3b"
	}
	return &PostmortemService{
		db:          db,
		ollamaURL:   url,
		ollamaModel: model,
		httpClient:  &http.Client{Timeout: 20 * time.Second},
	}
}

// GenerateForIncident generates a postmortem for a resolved incident.
// Safe to call multiple times — will update if one already exists.
func (s *PostmortemService) GenerateForIncident(ctx context.Context, incidentID uuid.UUID) error {
	if s.db == nil {
		return nil
	}

	// Fetch incident data.
	var title, severity, status, rcaStatus string
	var rcaConf float64
	var rcaText, topologyPath string
	var createdAt, resolvedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT title, severity, status, COALESCE(rca_status,'none'),
		       COALESCE(rca_confidence, 0), COALESCE(ai_root_cause, ''),
		       COALESCE(topology_path, ''), created_at,
		       COALESCE(resolved_at, NOW())
		FROM incidents WHERE id = $1
	`, incidentID).Scan(&title, &severity, &status, &rcaStatus, &rcaConf, &rcaText, &topologyPath, &createdAt, &resolvedAt)
	if err != nil {
		return fmt.Errorf("fetch incident %s: %w", incidentID, err)
	}

	// Build timeline from timeline events if available.
	timeline := s.buildTimeline(ctx, incidentID, createdAt, resolvedAt)

	// Fetch correlated alerts for contributing factors.
	contributing := s.fetchContributingFactors(ctx, incidentID)

	duration := resolvedAt.Sub(createdAt)
	durationStr := formatDuration(duration)

	// Build postmortem content — try LLM first, fall back to template.
	content := s.generate(ctx, title, severity, rcaText, rcaConf, topologyPath, timeline, contributing, durationStr)

	data, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("marshal postmortem: %w", err)
	}

	// Upsert into post_mortems table (full content stored in timeline JSONB).
	contributingJSON, _ := json.Marshal(content.ContributingFactors)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO post_mortems
			(id, incident_id, title, summary, impact, root_cause, timeline, created_at,
			 generated_by, rca_confidence, contributing_factors)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NOW(), $8, $9, $10)
		ON CONFLICT (incident_id) DO UPDATE SET
			title               = EXCLUDED.title,
			summary             = EXCLUDED.summary,
			impact              = EXCLUDED.impact,
			root_cause          = EXCLUDED.root_cause,
			timeline            = EXCLUDED.timeline,
			generated_by        = EXCLUDED.generated_by,
			rca_confidence      = EXCLUDED.rca_confidence,
			contributing_factors = EXCLUDED.contributing_factors
	`,
		uuid.New(), incidentID,
		content.Title,
		content.ImpactSummary,
		content.ImpactSummary,
		content.RootCause,
		data,
		content.GeneratedBy,
		content.RCAConfidence,
		string(contributingJSON),
	)
	if err != nil {
		// Table may not have incident_id unique constraint — fall back to plain INSERT.
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO post_mortems
				(id, incident_id, title, summary, impact, root_cause, timeline, created_at,
				 generated_by, rca_confidence, contributing_factors)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NOW(), $8, $9, $10)
		`, uuid.New(), incidentID, content.Title, content.ImpactSummary,
			content.ImpactSummary, content.RootCause, data,
			content.GeneratedBy, content.RCAConfidence, string(contributingJSON))
	}
	if err != nil {
		return fmt.Errorf("upsert postmortem for incident %s: %w", incidentID, err)
	}
	log.Printf("Postmortem generated for incident %s (duration=%s rca_confidence=%.0f%% by=%s)",
		incidentID, durationStr, rcaConf*100, content.GeneratedBy)
	return nil
}

func (s *PostmortemService) generate(
	ctx context.Context,
	title, severity, rcaText string,
	rcaConf float64,
	topologyPath string,
	timeline []TimelineEntry,
	contributing []string,
	durationStr string,
) *PostmortemContent {
	content := &PostmortemContent{
		Title:          fmt.Sprintf("Postmortem: %s", title),
		Severity:       severity,
		Duration:       durationStr,
		RootCause:      rcaText,
		RCAConfidence:  rcaConf,
		Timeline:       timeline,
		GeneratedAt:    time.Now().UTC(),
		GeneratedBy:    "template",
	}
	if len(contributing) > 0 {
		content.ContributingFactors = contributing
	}

	// Try LLM generation if RCA confidence is sufficient.
	if rcaConf >= 0.60 && s.ollamaURL != "" && rcaText != "" {
		llmContent := s.generateWithLLM(ctx, title, severity, rcaText, durationStr, contributing)
		if llmContent != "" {
			// Parse LLM sections into structured fields.
			content.ImpactSummary, content.Remediation, content.LessonsLearned, content.ActionItems =
				parseLLMPostmortem(llmContent)
			content.GeneratedBy = "llm"
			return content
		}
	}

	// Deterministic template fallback.
	content.ImpactSummary = fmt.Sprintf(
		"A %s severity incident occurred (%s) requiring %s to resolve. "+
			"RCA confidence: %.0f%%.",
		severity, title, durationStr, rcaConf*100,
	)
	content.Remediation = rcaText
	content.LessonsLearned = []string{
		"Review monitoring thresholds for early detection.",
		"Verify RCA accuracy with on-call team.",
	}
	content.ActionItems = []ActionItem{
		{Description: "Review and confirm root cause analysis", Priority: "high", Type: "respond"},
		{Description: "Add runbook for this failure pattern", Priority: "medium", Type: "respond"},
	}
	return content
}

func (s *PostmortemService) generateWithLLM(
	ctx context.Context,
	title, severity, rcaText, duration string,
	contributing []string,
) string {
	var sb strings.Builder
	sb.WriteString("You are an SRE writing a concise incident postmortem.\n\n")
	sb.WriteString(fmt.Sprintf("Incident: %s\nSeverity: %s\nDuration: %s\n", title, severity, duration))
	sb.WriteString(fmt.Sprintf("Root Cause: %s\n", rcaText))
	if len(contributing) > 0 {
		sb.WriteString("Contributing factors:\n")
		for _, f := range contributing {
			sb.WriteString("• " + f + "\n")
		}
	}
	sb.WriteString(`
Write a structured postmortem with these sections (2-3 sentences each):
IMPACT: What was affected and how many users/services were impacted.
REMEDIATION: What was done to fix it.
LESSONS: 3 bullet points — what can be improved.
ACTIONS: 2 action items with priority (high/medium) and type (prevent/detect/respond).
`)

	body, _ := json.Marshal(map[string]interface{}{
		"model":  s.ollamaModel,
		"prompt": sb.String(),
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.2,
			"num_predict": 350,
		},
	})

	llmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(llmCtx, http.MethodPost, s.ollamaURL+"/api/generate", bytes.NewBuffer(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("postmortem LLM call failed: %v", err)
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result struct{ Response string `json:"response"` }
	if err := json.Unmarshal(raw, &result); err != nil {
		return ""
	}
	return strings.TrimSpace(result.Response)
}

func (s *PostmortemService) buildTimeline(ctx context.Context, incidentID uuid.UUID, createdAt, resolvedAt time.Time) []TimelineEntry {
	var entries []TimelineEntry
	entries = append(entries, TimelineEntry{
		Timestamp:   createdAt.Format(time.RFC3339),
		Description: "Incident created",
		Type:        "alert",
	})

	rows, err := s.db.QueryContext(ctx, `
		SELECT event_type, description, created_at
		FROM incident_timeline_events
		WHERE incident_id = $1
		ORDER BY created_at ASC
		LIMIT 20
	`, incidentID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var evType, desc string
			var ts time.Time
			if err := rows.Scan(&evType, &desc, &ts); err != nil {
				continue
			}
			entries = append(entries, TimelineEntry{
				Timestamp:   ts.Format(time.RFC3339),
				Description: desc,
				Type:        timelineEventType(evType),
			})
		}
	}

	entries = append(entries, TimelineEntry{
		Timestamp:   resolvedAt.Format(time.RFC3339),
		Description: "Incident resolved",
		Type:        "resolution",
	})
	return entries
}

func (s *PostmortemService) fetchContributingFactors(ctx context.Context, incidentID uuid.UUID) []string {
	var factors []string
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT source, root_cause_domain
		FROM rca_decisions
		WHERE incident_id = $1 AND final_confidence > 0.3
		ORDER BY final_confidence DESC
		LIMIT 5
	`, incidentID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var source, domain string
			if err := rows.Scan(&source, &domain); err != nil {
				continue
			}
			if domain != "" {
				factors = append(factors, fmt.Sprintf("%s (%s)", domain, source))
			}
		}
	}
	return factors
}

// GetForIncident returns the postmortem for an incident, or nil if none exists.
func (s *PostmortemService) GetForIncident(ctx context.Context, incidentID uuid.UUID) (*PostmortemContent, error) {
	var timelineJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT timeline FROM post_mortems WHERE incident_id = $1 ORDER BY created_at DESC LIMIT 1
	`, incidentID).Scan(&timelineJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetch postmortem: %w", err)
	}
	var content PostmortemContent
	if err := json.Unmarshal(timelineJSON, &content); err != nil {
		return nil, fmt.Errorf("parse postmortem: %w", err)
	}
	return &content, nil
}

// parseLLMPostmortem extracts structured sections from the LLM response.
func parseLLMPostmortem(text string) (impact, remediation string, lessons []string, actions []ActionItem) {
	lines := strings.Split(text, "\n")
	section := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "IMPACT:"):
			section = "impact"
			impact = strings.TrimPrefix(line[7:], " ")
		case strings.HasPrefix(upper, "REMEDIATION:"):
			section = "remediation"
			remediation = strings.TrimPrefix(line[12:], " ")
		case strings.HasPrefix(upper, "LESSONS:"):
			section = "lessons"
		case strings.HasPrefix(upper, "ACTIONS:"):
			section = "actions"
		default:
			switch section {
			case "impact":
				impact += " " + line
			case "remediation":
				remediation += " " + line
			case "lessons":
				if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
					lessons = append(lessons, strings.TrimLeft(line, "•-* "))
				}
			case "actions":
				if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
					priority := "medium"
					aType := "respond"
					if strings.Contains(strings.ToLower(line), "high") {
						priority = "high"
					}
					if strings.Contains(strings.ToLower(line), "prevent") {
						aType = "prevent"
					} else if strings.Contains(strings.ToLower(line), "detect") {
						aType = "detect"
					}
					actions = append(actions, ActionItem{
						Description: strings.TrimLeft(line, "•-* "),
						Priority:    priority,
						Type:        aType,
					})
				}
			}
		}
	}
	return
}

func timelineEventType(evType string) string {
	switch evType {
	case "alert_resolved", "auto_resolved", "incident_resolved":
		return "resolution"
	case "rca_triggered", "investigation_started", "oie_result":
		return "investigation"
	case "alert_created", "alert_escalated":
		return "alert"
	case "escalation":
		return "escalation"
	default:
		return "event"
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
