// Package postmortem auto-generates structured incident postmortems.
//
// After an incident closes, KubeSense synthesizes a full postmortem from:
//   - The RCA investigation result (root cause, evidence chain)
//   - The timeline of events (from Kafka topics)
//   - Remediation actions taken and their outcomes
//   - SLO impact and error budget consumed
//   - Contributing factors from change correlation
//
// Output is a structured document in Markdown format (Confluence/Notion/GitHub ready)
// plus a machine-readable JSON version for analytics.
//
// Market gap: PagerDuty and Incident.io have postmortem templates. None auto-populate
// them from causal evidence chains with specific timestamps and evidence grades.
package postmortem

import (
	"fmt"
	"strings"
	"time"
)

// Severity maps to the incident impact level.
type Severity string

const (
	SEV1 Severity = "SEV1" // Complete outage
	SEV2 Severity = "SEV2" // Partial outage / degraded
	SEV3 Severity = "SEV3" // Minor impact
	SEV4 Severity = "SEV4" // Near miss / potential impact
)

// Input is everything needed to generate a postmortem.
type Input struct {
	// Identity
	IncidentID  string
	Title       string
	Severity    Severity
	ClusterID   string
	Namespace   string

	// Timeline
	DetectedAt  time.Time
	AcknowledgedAt time.Time
	MitigatedAt time.Time
	ResolvedAt  time.Time

	// Root cause (from RCA engine)
	RootCause       string  // human-readable description
	RootCauseEntity string  // "Deployment/payments/checkout"
	FailureMode     string  // "OOMKilled" | "CrashLoopBackOff" | etc.
	EvidenceGrade   string  // A/B/C/D/F
	Confidence      float64

	// Evidence chain
	EvidenceItems []EvidenceItem

	// Contributing changes (from change correlator)
	ContributingChanges []ContributingChange

	// Actions taken
	ActionsTaken []ActionTaken

	// Impact
	SLOImpact       *SLOImpact
	AffectedServices []string
	CustomerImpact  string

	// People
	DetectedBy    string
	IncidentCommander string
	Responders    []string
}

// EvidenceItem is one piece of evidence from the investigation.
type EvidenceItem struct {
	Source      string
	Description string
	OccurredAt  time.Time
	Strength    float64
}

// ContributingChange is a deployment / config change that contributed to the incident.
type ContributingChange struct {
	ChangeType    string
	ResourceKind  string
	ResourceName  string
	Namespace     string
	Actor         string
	OccurredAt    time.Time
	CorrelationScore float64
	GitCommitSHA  string
	PRTitle       string
	PRAuthor      string
}

// ActionTaken is one remediation step performed during the incident.
type ActionTaken struct {
	Description  string
	Command      string
	PerformedAt  time.Time
	PerformedBy  string
	WasEffective bool
	Notes        string
}

// SLOImpact describes the SLO error budget consumed.
type SLOImpact struct {
	SLOName         string
	Service         string
	BudgetConsumed  float64 // percentage
	DowntimeMinutes float64
}

// Postmortem is the generated document.
type Postmortem struct {
	IncidentID   string    `json:"incident_id"`
	GeneratedAt  time.Time `json:"generated_at"`
	Markdown     string    `json:"markdown"`
	TimeToDetect time.Duration `json:"time_to_detect"`
	TimeToAck    time.Duration `json:"time_to_acknowledge"`
	TimeToMitigate time.Duration `json:"time_to_mitigate"`
	TimeToResolve  time.Duration `json:"time_to_resolve"`
	ActionItems  []ActionItem `json:"action_items"`
}

// ActionItem is a follow-up task generated from the postmortem.
type ActionItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"` // P1 / P2 / P3
	Category    string `json:"category"` // prevention / detection / mitigation / process
	Owner       string `json:"owner,omitempty"`
}

// Generator creates postmortems from incident data.
type Generator struct{}

// NewGenerator creates a postmortem generator.
func NewGenerator() *Generator { return &Generator{} }

// Generate builds a complete postmortem from the incident input.
func (g *Generator) Generate(in Input) *Postmortem {
	pm := &Postmortem{
		IncidentID:  in.IncidentID,
		GeneratedAt: time.Now(),
	}

	// Compute durations
	if !in.DetectedAt.IsZero() && !in.ResolvedAt.IsZero() {
		pm.TimeToResolve = in.ResolvedAt.Sub(in.DetectedAt)
	}
	if !in.DetectedAt.IsZero() && !in.AcknowledgedAt.IsZero() {
		pm.TimeToAck = in.AcknowledgedAt.Sub(in.DetectedAt)
	}
	if !in.DetectedAt.IsZero() && !in.MitigatedAt.IsZero() {
		pm.TimeToMitigate = in.MitigatedAt.Sub(in.DetectedAt)
	}

	// Generate action items from the evidence
	pm.ActionItems = g.generateActionItems(in)

	// Build markdown
	pm.Markdown = g.buildMarkdown(in, pm)

	return pm
}

// ─── Markdown builder ─────────────────────────────────────────────────────────

func (g *Generator) buildMarkdown(in Input, pm *Postmortem) string {
	var b strings.Builder

	// Header
	fmt.Fprintf(&b, "# Postmortem: %s\n\n", in.Title)
	fmt.Fprintf(&b, "> **Incident ID:** `%s` | **Severity:** %s | **Status:** Resolved\n\n",
		in.IncidentID, string(in.Severity))
	fmt.Fprintf(&b, "_Auto-generated by KubeSense · Evidence grade: **%s** · Confidence: **%.0f%%**_\n\n",
		in.EvidenceGrade, in.Confidence*100)
	fmt.Fprintf(&b, "---\n\n")

	// Summary
	fmt.Fprintf(&b, "## Summary\n\n")
	if in.CustomerImpact != "" {
		fmt.Fprintf(&b, "%s\n\n", in.CustomerImpact)
	}
	fmt.Fprintf(&b, "**Root cause:** %s\n\n", in.RootCause)
	if in.FailureMode != "" {
		fmt.Fprintf(&b, "**Failure mode:** `%s`", in.FailureMode)
		if in.RootCauseEntity != "" {
			fmt.Fprintf(&b, " on `%s`", in.RootCauseEntity)
		}
		fmt.Fprintf(&b, "\n\n")
	}

	// Impact
	fmt.Fprintf(&b, "## Impact\n\n")
	if in.SLOImpact != nil {
		fmt.Fprintf(&b, "| SLO | Service | Budget consumed | Downtime |\n")
		fmt.Fprintf(&b, "|-----|---------|-----------------|----------|\n")
		fmt.Fprintf(&b, "| %s | %s | %.1f%% | %.0f min |\n\n",
			in.SLOImpact.SLOName, in.SLOImpact.Service,
			in.SLOImpact.BudgetConsumed, in.SLOImpact.DowntimeMinutes)
	}
	if len(in.AffectedServices) > 0 {
		fmt.Fprintf(&b, "**Affected services:** %s\n\n", strings.Join(in.AffectedServices, ", "))
	}

	// Timeline
	fmt.Fprintf(&b, "## Timeline\n\n")
	fmt.Fprintf(&b, "| Time | Event |\n|------|-------|\n")
	if !in.DetectedAt.IsZero() {
		fmt.Fprintf(&b, "| `%s` | 🔴 Incident detected |\n", fmtTime(in.DetectedAt))
	}
	if !in.AcknowledgedAt.IsZero() {
		fmt.Fprintf(&b, "| `%s` | 👤 Acknowledged by %s (**TTAck: %s**) |\n",
			fmtTime(in.AcknowledgedAt), in.IncidentCommander, fmtDuration(pm.TimeToAck))
	}
	// Contributing changes in timeline
	for _, ch := range in.ContributingChanges {
		marker := "⚠️"
		if ch.CorrelationScore >= 0.80 {
			marker = "🔴"
		}
		line := fmt.Sprintf("%s `%s` updated `%s/%s` (correlation: %.0f%%)",
			marker, ch.Actor, ch.ResourceKind, ch.ResourceName, ch.CorrelationScore*100)
		if ch.PRTitle != "" {
			line += fmt.Sprintf(" — PR: _%s_", ch.PRTitle)
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", fmtTime(ch.OccurredAt), line)
	}
	// Evidence events in timeline
	for _, ev := range in.EvidenceItems {
		if ev.Strength >= 0.70 {
			fmt.Fprintf(&b, "| `%s` | 📋 %s |\n", fmtTime(ev.OccurredAt), ev.Description)
		}
	}
	// Actions taken
	for _, act := range in.ActionsTaken {
		result := "✅"
		if !act.WasEffective {
			result = "❌"
		}
		fmt.Fprintf(&b, "| `%s` | %s %s (`%s` by %s) |\n",
			fmtTime(act.PerformedAt), result, act.Description, act.Command, act.PerformedBy)
	}
	if !in.MitigatedAt.IsZero() {
		fmt.Fprintf(&b, "| `%s` | 🟡 Incident mitigated (**TTMit: %s**) |\n",
			fmtTime(in.MitigatedAt), fmtDuration(pm.TimeToMitigate))
	}
	if !in.ResolvedAt.IsZero() {
		fmt.Fprintf(&b, "| `%s` | 🟢 Incident resolved (**TTR: %s**) |\n",
			fmtTime(in.ResolvedAt), fmtDuration(pm.TimeToResolve))
	}
	fmt.Fprintf(&b, "\n")

	// Root cause analysis
	fmt.Fprintf(&b, "## Root Cause Analysis\n\n")
	fmt.Fprintf(&b, "**KubeSense RCA result** (Evidence grade: %s, Confidence: %.0f%%)\n\n",
		in.EvidenceGrade, in.Confidence*100)
	fmt.Fprintf(&b, "%s\n\n", in.RootCause)

	if len(in.EvidenceItems) > 0 {
		fmt.Fprintf(&b, "### Evidence Chain\n\n")
		for i, ev := range in.EvidenceItems {
			strength := "●●●"
			if ev.Strength < 0.70 {
				strength = "●●○"
			}
			if ev.Strength < 0.40 {
				strength = "●○○"
			}
			fmt.Fprintf(&b, "%d. **[%s]** `%s` — %s _(strength: %s)_\n",
				i+1, ev.Source, fmtTime(ev.OccurredAt), ev.Description, strength)
		}
		fmt.Fprintf(&b, "\n")
	}

	// Contributing changes
	if len(in.ContributingChanges) > 0 {
		fmt.Fprintf(&b, "## Contributing Changes\n\n")
		fmt.Fprintf(&b, "| Resource | Change | Actor | Time before incident | Correlation |\n")
		fmt.Fprintf(&b, "|----------|--------|-------|----------------------|-------------|\n")
		for _, ch := range in.ContributingChanges {
			lag := "—"
			if !in.DetectedAt.IsZero() && !ch.OccurredAt.IsZero() {
				lag = fmtDuration(in.DetectedAt.Sub(ch.OccurredAt))
			}
			actor := ch.Actor
			if ch.PRAuthor != "" {
				actor = fmt.Sprintf("%s (PR: %s)", ch.PRAuthor, ch.PRTitle)
			}
			fmt.Fprintf(&b, "| `%s/%s` | %s | %s | %s | **%.0f%%** |\n",
				ch.ResourceKind, ch.ResourceName,
				ch.ChangeType, actor, lag, ch.CorrelationScore*100)
		}
		fmt.Fprintf(&b, "\n")
	}

	// Key metrics
	fmt.Fprintf(&b, "## Key Metrics\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|--------|-------|\n")
	fmt.Fprintf(&b, "| Time to detect | %s |\n", fmtDuration(pm.TimeToDetect))
	fmt.Fprintf(&b, "| Time to acknowledge | %s |\n", fmtDuration(pm.TimeToAck))
	fmt.Fprintf(&b, "| Time to mitigate | %s |\n", fmtDuration(pm.TimeToMitigate))
	fmt.Fprintf(&b, "| Time to resolve | %s |\n", fmtDuration(pm.TimeToResolve))
	fmt.Fprintf(&b, "| Evidence grade | %s |\n", in.EvidenceGrade)
	fmt.Fprintf(&b, "| Responders | %d |\n\n", len(in.Responders))

	// Responders
	if len(in.Responders) > 0 {
		fmt.Fprintf(&b, "**Responders:** %s\n\n", strings.Join(in.Responders, ", "))
	}

	// Action items
	if len(pm.ActionItems) > 0 {
		fmt.Fprintf(&b, "## Action Items\n\n")
		fmt.Fprintf(&b, "| Priority | Category | Action | Owner |\n")
		fmt.Fprintf(&b, "|----------|----------|--------|-------|\n")
		for _, ai := range pm.ActionItems {
			fmt.Fprintf(&b, "| **%s** | %s | %s | %s |\n",
				ai.Priority, ai.Category, ai.Title, ai.Owner)
		}
		fmt.Fprintf(&b, "\n")
	}

	// Footer
	fmt.Fprintf(&b, "---\n\n")
	fmt.Fprintf(&b, "_Generated by KubeSense on %s · Cluster: `%s`_\n",
		time.Now().Format("2006-01-02 15:04 UTC"), in.ClusterID)

	return b.String()
}

// ─── Action item generation ───────────────────────────────────────────────────

func (g *Generator) generateActionItems(in Input) []ActionItem {
	var items []ActionItem

	// Prevention: from contributing changes
	for _, ch := range in.ContributingChanges {
		if ch.CorrelationScore >= 0.70 {
			items = append(items, ActionItem{
				Title:       fmt.Sprintf("Add pre-deploy validation for %s/%s", ch.ResourceKind, ch.ResourceName),
				Description: fmt.Sprintf("This %s change had %.0f%% correlation with the incident. Add admission webhook rules to catch similar patterns.", ch.ChangeType, ch.CorrelationScore*100),
				Priority:    "P1",
				Category:    "prevention",
			})
			break // one prevention item per incident
		}
	}

	// Detection: based on TTDetect
	if in.AcknowledgedAt.Sub(in.DetectedAt) > 10*time.Minute {
		items = append(items, ActionItem{
			Title:       "Improve detection time for " + in.FailureMode,
			Description: fmt.Sprintf("Detection took %s. Add alerting rule for early warning signals before full failure.", fmtDuration(in.AcknowledgedAt.Sub(in.DetectedAt))),
			Priority:    "P2",
			Category:    "detection",
		})
	}

	// Mitigation: from failure mode
	switch in.FailureMode {
	case "OOMKilled":
		items = append(items, ActionItem{
			Title:       "Configure memory limits and VPA for " + in.RootCauseEntity,
			Description: "OOMKilled indicates memory limits are too low or there is a memory leak. Set correct limits and enable VPA for automatic adjustment.",
			Priority:    "P1",
			Category:    "prevention",
		})
	case "CrashLoopBackOff":
		items = append(items, ActionItem{
			Title:       "Add liveness probe to " + in.RootCauseEntity,
			Description: "CrashLoopBackOff indicates the container is failing on startup. A proper liveness probe with a restart policy prevents cascading failure.",
			Priority:    "P1",
			Category:    "prevention",
		})
	case "ImagePullBackOff":
		items = append(items, ActionItem{
			Title:       "Verify image pull secrets and registry availability",
			Description: "ImagePullBackOff indicates authentication or registry connectivity issues. Verify imagePullSecrets are present and the registry is reachable.",
			Priority:    "P2",
			Category:    "mitigation",
		})
	}

	// Process: SLO impact
	if in.SLOImpact != nil && in.SLOImpact.BudgetConsumed > 10 {
		items = append(items, ActionItem{
			Title:       fmt.Sprintf("Review SLO target for %s after %.1f%% budget consumed", in.SLOImpact.Service, in.SLOImpact.BudgetConsumed),
			Description: "Large error budget consumption. Consider whether the SLO target is realistic or whether reliability investments are needed.",
			Priority:    "P2",
			Category:    "process",
		})
	}

	// Process: Runbook
	items = append(items, ActionItem{
		Title:       fmt.Sprintf("Add KubeSense playbook for %s on %s", in.FailureMode, in.RootCauseEntity),
		Description: "Ensure this incident's resolution is recorded in KubeSense so future similar incidents auto-generate the playbook.",
		Priority:    "P3",
		Category:    "process",
	})

	return items
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
