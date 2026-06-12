// Package toil implements SRE toil detection and quantification.
//
// Toil is repetitive, manual, automatable operational work that scales with
// service growth. Google's SRE book defines toil as work that is:
//   - Manual, repetitive, automatable, tactical, lacks enduring value,
//     scales linearly with service growth.
//
// KubeSense tracks every kubectl command run, every restart action, every
// manual scale operation — and computes toil per team per week in engineer-hours.
// It then suggests automation that would eliminate the toil.
//
// Market gap: No observability platform quantifies SRE toil with this granularity.
// The closest is Google's internal tooling, never open or Kubernetes-specific.
package toil

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ActionCategory classifies the type of toil-generating action.
type ActionCategory string

const (
	CategoryManualRestart    ActionCategory = "manual_restart"
	CategoryManualScale      ActionCategory = "manual_scale"
	CategoryManualRollback   ActionCategory = "manual_rollback"
	CategoryManualDebug      ActionCategory = "manual_debug"
	CategoryAlertAck         ActionCategory = "alert_ack"
	CategoryConfigHotfix     ActionCategory = "config_hotfix"
	CategoryCertRenewal      ActionCategory = "cert_renewal"
	CategoryLogSearch        ActionCategory = "log_search"
	CategoryCapacityAdjust   ActionCategory = "capacity_adjust"
)

// ToilAction is one observed manual operation by an SRE.
type ToilAction struct {
	ID           string
	Category     ActionCategory
	Actor        string         // engineer who performed it
	Team         string
	ClusterID    string
	Namespace    string
	ResourceKind string
	ResourceName string
	Command      string         // the actual kubectl command or operation
	DurationMins float64        // how long it took
	PerformedAt  time.Time
	Trigger      string         // what caused this: "alert", "customer report", "proactive"
	IsAutomatable bool          // could this have been done by a controller/script?
	AutomationHint string       // what automation would eliminate it
}

// ToilSummary is the aggregated toil report for a team or namespace.
type ToilSummary struct {
	Team             string             `json:"team"`
	Namespace        string             `json:"namespace,omitempty"`
	WindowDays       int                `json:"window_days"`
	TotalActions     int                `json:"total_actions"`
	TotalHours       float64            `json:"total_hours"`
	AutomatableHours float64            `json:"automatable_hours"`
	AutomatablePct   float64            `json:"automatable_pct"`
	WeeklyHours      float64            `json:"weekly_hours"`
	ToilScore        float64            `json:"toil_score"`      // 0-100, higher = more toil
	ToilLevel        string             `json:"toil_level"`      // healthy / warning / critical
	ByCategory       []CategorySummary  `json:"by_category"`
	TopPatterns      []ToilPattern      `json:"top_patterns"`
	Automations      []AutomationHint   `json:"suggested_automations"`
	Trend            string             `json:"trend"`           // increasing / stable / decreasing
	TrendPct         float64            `json:"trend_pct"`       // week-over-week change
}

// CategorySummary breaks down toil by action type.
type CategorySummary struct {
	Category      ActionCategory `json:"category"`
	Count         int            `json:"count"`
	Hours         float64        `json:"hours"`
	Pct           float64        `json:"pct_of_total"`
	IsAutomatable bool           `json:"is_automatable"`
}

// ToilPattern is a repeated action signature (same command / same resource).
type ToilPattern struct {
	Signature     string         `json:"signature"`       // normalized action description
	Count         int            `json:"count"`           // occurrences in window
	TotalHours    float64        `json:"total_hours"`
	Actors        []string       `json:"actors"`          // unique engineers involved
	ResourceKind  string         `json:"resource_kind"`
	Namespace     string         `json:"namespace"`
	IsAutomatable bool           `json:"is_automatable"`
	Automation    string         `json:"automation_hint"`
}

// AutomationHint is a specific recommendation to eliminate a toil pattern.
type AutomationHint struct {
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	EstimatedSaving float64 `json:"estimated_weekly_hours_saved"`
	Implementation  string  `json:"implementation"`   // kubectl, HPA, PrometheusRule, etc.
	Effort          string  `json:"effort"`           // low / medium / high
	Pattern         string  `json:"pattern"`
}

// Detector tracks toil actions and generates summaries.
type Detector struct {
	mu      sync.RWMutex
	actions []ToilAction
	maxAge  time.Duration
}

// NewDetector creates a toil detector with configurable history window.
func NewDetector(maxAge time.Duration) *Detector {
	if maxAge <= 0 {
		maxAge = 90 * 24 * time.Hour // 90 days default
	}
	return &Detector{maxAge: maxAge}
}

// Record ingests one toil action.
// Called from: API resolution endpoint, kubectl audit webhook, ArgoCD event handler.
func (d *Detector) Record(a ToilAction) {
	if a.PerformedAt.IsZero() {
		a.PerformedAt = time.Now()
	}
	if a.DurationMins == 0 {
		a.DurationMins = defaultDuration(a.Category)
	}
	a.IsAutomatable = isAutomatable(a)
	a.AutomationHint = automationHint(a)

	d.mu.Lock()
	d.actions = append(d.actions, a)
	d.prune()
	d.mu.Unlock()
}

// SummarizeTeam returns the toil summary for a team over the given window.
func (d *Detector) SummarizeTeam(team string, windowDays int) *ToilSummary {
	d.mu.RLock()
	defer d.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -windowDays)
	var actions []ToilAction
	for _, a := range d.actions {
		if a.Team == team && a.PerformedAt.After(since) {
			actions = append(actions, a)
		}
	}
	return d.buildSummary(team, "", windowDays, actions)
}

// SummarizeNamespace returns the toil summary for a namespace.
func (d *Detector) SummarizeNamespace(ns string, windowDays int) *ToilSummary {
	d.mu.RLock()
	defer d.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -windowDays)
	var actions []ToilAction
	for _, a := range d.actions {
		if a.Namespace == ns && a.PerformedAt.After(since) {
			actions = append(actions, a)
		}
	}
	return d.buildSummary("", ns, windowDays, actions)
}

// TopToilSources returns the namespaces/teams generating the most toil,
// ranked by weekly engineer-hours descending.
func (d *Detector) TopToilSources(windowDays int, limit int) []ToilSummary {
	d.mu.RLock()
	defer d.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -windowDays)
	byNS := map[string][]ToilAction{}
	for _, a := range d.actions {
		if a.PerformedAt.After(since) {
			byNS[a.Namespace] = append(byNS[a.Namespace], a)
		}
	}

	var summaries []ToilSummary
	for ns, acts := range byNS {
		s := d.buildSummary("", ns, windowDays, acts)
		summaries = append(summaries, *s)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].WeeklyHours > summaries[j].WeeklyHours
	})
	if len(summaries) > limit {
		return summaries[:limit]
	}
	return summaries
}

// ─── Summary builder ──────────────────────────────────────────────────────────

func (d *Detector) buildSummary(team, ns string, windowDays int, actions []ToilAction) *ToilSummary {
	s := &ToilSummary{
		Team:       team,
		Namespace:  ns,
		WindowDays: windowDays,
	}
	if len(actions) == 0 {
		s.ToilLevel = "healthy"
		return s
	}

	totalHours := 0.0
	automatable := 0.0
	byCat := map[ActionCategory]*CategorySummary{}
	patternMap := map[string]*ToilPattern{}
	actorSet := map[string]bool{}

	for _, a := range actions {
		hrs := a.DurationMins / 60.0
		totalHours += hrs
		actorSet[a.Actor] = true

		// Category breakdown
		if byCat[a.Category] == nil {
			byCat[a.Category] = &CategorySummary{Category: a.Category, IsAutomatable: isAutomatable(a)}
		}
		byCat[a.Category].Count++
		byCat[a.Category].Hours += hrs

		if a.IsAutomatable {
			automatable += hrs
		}

		// Pattern detection: normalize signature
		sig := normalizeSignature(a)
		if patternMap[sig] == nil {
			patternMap[sig] = &ToilPattern{
				Signature:     sig,
				ResourceKind:  a.ResourceKind,
				Namespace:     a.Namespace,
				IsAutomatable: a.IsAutomatable,
				Automation:    a.AutomationHint,
			}
		}
		p := patternMap[sig]
		p.Count++
		p.TotalHours += hrs
		hasActor := false
		for _, ac := range p.Actors {
			if ac == a.Actor {
				hasActor = true
			}
		}
		if !hasActor && a.Actor != "" {
			p.Actors = append(p.Actors, a.Actor)
		}
	}

	s.TotalActions = len(actions)
	s.TotalHours = math.Round(totalHours*10) / 10
	s.AutomatableHours = math.Round(automatable*10) / 10
	if totalHours > 0 {
		s.AutomatablePct = math.Round(automatable/totalHours*1000) / 10
	}
	s.WeeklyHours = math.Round(totalHours/float64(windowDays)*7*10) / 10

	// Category summaries sorted by hours desc
	for _, cs := range byCat {
		if totalHours > 0 {
			cs.Pct = math.Round(cs.Hours/totalHours*1000) / 10
		}
		s.ByCategory = append(s.ByCategory, *cs)
	}
	sort.Slice(s.ByCategory, func(i, j int) bool {
		return s.ByCategory[i].Hours > s.ByCategory[j].Hours
	})

	// Top patterns (min 2 occurrences, sorted by total hours)
	for _, p := range patternMap {
		if p.Count >= 2 {
			s.TopPatterns = append(s.TopPatterns, *p)
		}
	}
	sort.Slice(s.TopPatterns, func(i, j int) bool {
		return s.TopPatterns[i].TotalHours > s.TopPatterns[j].TotalHours
	})
	if len(s.TopPatterns) > 10 {
		s.TopPatterns = s.TopPatterns[:10]
	}

	// Automation hints: top 5 automatable patterns
	for _, p := range s.TopPatterns {
		if p.IsAutomatable && p.Automation != "" {
			savings := p.TotalHours / float64(windowDays) * 7
			s.Automations = append(s.Automations, AutomationHint{
				Title:           buildAutomationTitle(p),
				Description:     fmt.Sprintf("Pattern '%s' occurred %d times — %.1fh total", p.Signature, p.Count, p.TotalHours),
				EstimatedSaving: math.Round(savings*10) / 10,
				Implementation:  p.Automation,
				Effort:          automationEffort(p),
				Pattern:         p.Signature,
			})
		}
	}
	sort.Slice(s.Automations, func(i, j int) bool {
		return s.Automations[i].EstimatedSaving > s.Automations[j].EstimatedSaving
	})
	if len(s.Automations) > 5 {
		s.Automations = s.Automations[:5]
	}

	// Toil score and level
	s.ToilScore = toil_score(s.WeeklyHours, s.AutomatablePct)
	s.ToilLevel = toil_level(s.ToilScore)

	// Trend: compare first vs second half of window
	half := time.Now().AddDate(0, 0, -windowDays/2)
	first, second := 0.0, 0.0
	for _, a := range actions {
		hrs := a.DurationMins / 60.0
		if a.PerformedAt.Before(half) {
			first += hrs
		} else {
			second += hrs
		}
	}
	if first > 0 {
		changePct := (second - first) / first * 100
		s.TrendPct = math.Round(changePct*10) / 10
		switch {
		case changePct > 15:
			s.Trend = "increasing"
		case changePct < -15:
			s.Trend = "decreasing"
		default:
			s.Trend = "stable"
		}
	}

	return s
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func normalizeSignature(a ToilAction) string {
	// Normalize to detect patterns across different resources/actors
	kind := a.ResourceKind
	if kind == "" {
		kind = "resource"
	}
	return fmt.Sprintf("%s:%s", string(a.Category), kind)
}

func isAutomatable(a ToilAction) bool {
	switch a.Category {
	case CategoryManualRestart, CategoryManualScale, CategoryAlertAck,
		CategoryCapacityAdjust, CategoryCertRenewal:
		return true
	case CategoryManualRollback:
		return true // can be automated with progressive delivery
	default:
		return false
	}
}

func automationHint(a ToilAction) string {
	switch a.Category {
	case CategoryManualRestart:
		return "Add liveness probe + restartPolicy=Always; controller auto-restarts unhealthy pods"
	case CategoryManualScale:
		return "Configure HorizontalPodAutoscaler targeting CPU/memory/custom metrics"
	case CategoryManualRollback:
		return "Use Argo Rollouts or Flagger for progressive delivery with automatic rollback"
	case CategoryAlertAck:
		return "Add inhibition rules for downstream alerts caused by the same root cause"
	case CategoryCapacityAdjust:
		return "Configure VPA (VerticalPodAutoscaler) for automatic resource right-sizing"
	case CategoryCertRenewal:
		return "Deploy cert-manager with automatic certificate renewal"
	case CategoryConfigHotfix:
		return "Use external-secrets-operator or Vault agent injection for dynamic config"
	default:
		return ""
	}
}

func defaultDuration(cat ActionCategory) float64 {
	// P50 time-cost estimates from SRE research for common operations
	switch cat {
	case CategoryManualRestart:
		return 5
	case CategoryManualScale:
		return 8
	case CategoryManualRollback:
		return 20
	case CategoryManualDebug:
		return 45
	case CategoryAlertAck:
		return 3
	case CategoryConfigHotfix:
		return 30
	case CategoryCertRenewal:
		return 15
	case CategoryLogSearch:
		return 20
	case CategoryCapacityAdjust:
		return 10
	default:
		return 10
	}
}

func buildAutomationTitle(p ToilPattern) string {
	parts := strings.SplitN(p.Signature, ":", 2)
	cat := parts[0]
	if len(parts) > 1 {
		return fmt.Sprintf("Automate %s restarts for %s", parts[1], cat)
	}
	return "Automate " + cat
}

func automationEffort(p ToilPattern) string {
	switch {
	case p.TotalHours > 10:
		return "high" // worth a sprint
	case p.TotalHours > 3:
		return "medium"
	default:
		return "low"
	}
}

func toil_score(weeklyHours, automatablePct float64) float64 {
	// Score = weighted combination of volume and automatable fraction
	// 0 = no toil, 100 = severe toil
	volumeScore := math.Min(weeklyHours/20.0, 1.0) * 60 // 20h/week = max volume score
	automScore := automatablePct / 100.0 * 40
	return math.Round((volumeScore + automScore) * 10) / 10
}

func toil_level(score float64) string {
	switch {
	case score >= 70:
		return "critical"
	case score >= 40:
		return "warning"
	default:
		return "healthy"
	}
}

func (d *Detector) prune() {
	cutoff := time.Now().Add(-d.maxAge)
	i := 0
	for i < len(d.actions) && d.actions[i].PerformedAt.Before(cutoff) {
		i++
	}
	d.actions = d.actions[i:]
}
