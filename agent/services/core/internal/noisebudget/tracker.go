// Package noisebudget tracks alert volume and suppresses duplicate noise.
//
// Alert fatigue is the #1 SRE burnout driver. Teams that receive 100+ alerts/day
// stop reading them. KubeSense's noise budget gives each team a weekly alert
// quota. When a team exceeds their budget, downstream alerts triggered by the
// same root cause are automatically silenced until the root cause is resolved.
//
// Key capabilities:
//   - Per-team, per-namespace weekly alert budget
//   - Causal deduplication: child alerts suppressed when parent is known
//   - Repeat alert detection: same root cause, already being investigated
//   - Noise score per team: 0-100 (lower = healthier signal/noise ratio)
//   - Trend analysis: is this team's alert volume growing?
//
// Market gap: AlertManager has inhibition rules but they're static and manual.
// KubeSense deduplicates causally — using the live RCA graph.
package noisebudget

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Alert is one incoming alert observation.
type Alert struct {
	ID          string
	Name        string       // alert rule name
	Severity    string       // critical / warning / info
	Team        string
	Namespace   string
	ClusterID   string
	ResourceKind string
	ResourceName string
	Labels      map[string]string
	FiredAt     time.Time
	ResolvedAt  time.Time  // zero if still firing
	RootCauseID string     // set if this alert is a child of another known incident
	Suppressed  bool       // true if noise budget or causal dedup suppressed it
	SuppressReason string
}

// BudgetConfig defines the alert budget for a team or namespace.
type BudgetConfig struct {
	Team           string
	Namespace      string
	WeeklyBudget   int     // max unique alerts per week
	HourlyBudget   int     // max alerts per hour (burst protection)
	DedupeWindow   time.Duration // suppress repeats within this window
}

// DefaultBudgetConfig returns sensible defaults for new teams.
var DefaultBudgetConfig = BudgetConfig{
	WeeklyBudget: 50,
	HourlyBudget: 10,
	DedupeWindow: 30 * time.Minute,
}

// BudgetStatus is the current state of a team's alert budget.
type BudgetStatus struct {
	Team             string    `json:"team"`
	Namespace        string    `json:"namespace,omitempty"`
	WeeklyBudget     int       `json:"weekly_budget"`
	WeeklyUsed       int       `json:"weekly_used"`
	WeeklyRemaining  int       `json:"weekly_remaining"`
	WeeklyPct        float64   `json:"weekly_pct"`
	HourlyUsed       int       `json:"hourly_used"`
	SuppressedCount  int       `json:"suppressed_count"`
	TopNoiseSource   string    `json:"top_noise_source"`
	NoiseScore       float64   `json:"noise_score"`     // 0-100
	NoiseLevel       string    `json:"noise_level"`     // healthy / warning / critical
	Trend            string    `json:"trend"`
	TrendPct         float64   `json:"trend_pct"`
	ActiveRootCauses []string  `json:"active_root_causes"`
}

// SuppressResult tells the caller whether to suppress an alert.
type SuppressResult struct {
	ShouldSuppress bool
	Reason         string
}

// Tracker manages alert budgets and causal deduplication.
type Tracker struct {
	mu      sync.RWMutex
	alerts  []Alert
	configs map[string]BudgetConfig // key: team or namespace
	maxAge  time.Duration
}

// NewTracker creates a noise budget tracker.
func NewTracker(maxAge time.Duration) *Tracker {
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	return &Tracker{
		maxAge:  maxAge,
		configs: make(map[string]BudgetConfig),
	}
}

// SetBudget configures the alert budget for a team or namespace.
func (t *Tracker) SetBudget(cfg BudgetConfig) {
	key := budgetKey(cfg.Team, cfg.Namespace)
	t.mu.Lock()
	t.configs[key] = cfg
	t.mu.Unlock()
}

// Evaluate decides whether an incoming alert should be suppressed.
// Call this before routing an alert to PagerDuty/Slack/etc.
func (t *Tracker) Evaluate(alert Alert) SuppressResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	cfg := t.budgetFor(alert.Team, alert.Namespace)
	now := time.Now()

	// Rule 1: Causal deduplication — this alert is caused by a known active incident
	if alert.RootCauseID != "" {
		for _, a := range t.alerts {
			if a.ID == alert.RootCauseID && a.ResolvedAt.IsZero() {
				alert.Suppressed = true
				alert.SuppressReason = fmt.Sprintf("causal dedup: caused by active incident %s", alert.RootCauseID)
				t.alerts = append(t.alerts, alert)
				return SuppressResult{true, alert.SuppressReason}
			}
		}
	}

	// Rule 2: Repeat alert — same name + namespace within dedupe window
	dedupeWindow := cfg.DedupeWindow
	if dedupeWindow <= 0 {
		dedupeWindow = DefaultBudgetConfig.DedupeWindow
	}
	for _, a := range t.alerts {
		if a.Name == alert.Name && a.Namespace == alert.Namespace &&
			a.ResolvedAt.IsZero() &&
			now.Sub(a.FiredAt) < dedupeWindow {
			alert.Suppressed = true
			alert.SuppressReason = fmt.Sprintf("repeat dedup: same alert fired %s ago, still active",
				humanDuration(now.Sub(a.FiredAt)))
			t.alerts = append(t.alerts, alert)
			return SuppressResult{true, alert.SuppressReason}
		}
	}

	// Rule 3: Hourly burst budget
	hourlyBudget := cfg.HourlyBudget
	if hourlyBudget <= 0 {
		hourlyBudget = DefaultBudgetConfig.HourlyBudget
	}
	hourlyCount := 0
	for _, a := range t.alerts {
		if a.Team == alert.Team && a.Namespace == alert.Namespace &&
			!a.Suppressed && now.Sub(a.FiredAt) < time.Hour {
			hourlyCount++
		}
	}
	if hourlyCount >= hourlyBudget {
		alert.Suppressed = true
		alert.SuppressReason = fmt.Sprintf("hourly budget: %d/%d alerts this hour for %s/%s",
			hourlyCount, hourlyBudget, alert.Team, alert.Namespace)
		t.alerts = append(t.alerts, alert)
		return SuppressResult{true, alert.SuppressReason}
	}

	// Rule 4: Weekly budget exhausted
	weeklyBudget := cfg.WeeklyBudget
	if weeklyBudget <= 0 {
		weeklyBudget = DefaultBudgetConfig.WeeklyBudget
	}
	weeklyCount := 0
	weekAgo := now.Add(-7 * 24 * time.Hour)
	for _, a := range t.alerts {
		if a.Team == alert.Team && !a.Suppressed && a.FiredAt.After(weekAgo) {
			weeklyCount++
		}
	}
	if weeklyCount >= weeklyBudget {
		alert.Suppressed = true
		alert.SuppressReason = fmt.Sprintf("weekly budget exhausted: %d/%d alerts this week for team %s",
			weeklyCount, weeklyBudget, alert.Team)
		t.alerts = append(t.alerts, alert)
		return SuppressResult{true, alert.SuppressReason}
	}

	// Alert passes all filters
	t.alerts = append(t.alerts, alert)
	t.prune()
	return SuppressResult{false, ""}
}

// ResolveAlert marks an alert as resolved, unblocking suppressed children.
func (t *Tracker) ResolveAlert(alertID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.alerts {
		if t.alerts[i].ID == alertID {
			t.alerts[i].ResolvedAt = time.Now()
			return
		}
	}
}

// GetStatus returns the current budget status for a team.
func (t *Tracker) GetStatus(team, namespace string) *BudgetStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	cfg := t.budgetFor(team, namespace)
	now := time.Now()
	weekAgo := now.Add(-7 * 24 * time.Hour)
	hourAgo := now.Add(-time.Hour)

	weekly, hourly, suppressed := 0, 0, 0
	noiseCounts := map[string]int{}
	activeRoots := map[string]bool{}

	for _, a := range t.alerts {
		if a.Team != team && team != "" {
			continue
		}
		if a.Namespace != namespace && namespace != "" {
			continue
		}
		if a.FiredAt.After(weekAgo) && !a.Suppressed {
			weekly++
			noiseCounts[a.Name]++
			if a.ResolvedAt.IsZero() && a.RootCauseID != "" {
				activeRoots[a.RootCauseID] = true
			}
		}
		if a.FiredAt.After(hourAgo) && !a.Suppressed {
			hourly++
		}
		if a.Suppressed {
			suppressed++
		}
	}

	weeklyBudget := cfg.WeeklyBudget
	if weeklyBudget <= 0 {
		weeklyBudget = DefaultBudgetConfig.WeeklyBudget
	}

	topSource := ""
	topCount := 0
	for name, count := range noiseCounts {
		if count > topCount {
			topCount = count
			topSource = name
		}
	}

	remaining := weeklyBudget - weekly
	if remaining < 0 {
		remaining = 0
	}
	pct := 0.0
	if weeklyBudget > 0 {
		pct = float64(weekly) / float64(weeklyBudget) * 100
	}

	var roots []string
	for r := range activeRoots {
		roots = append(roots, r)
	}

	noiseScore := calculateNoiseScore(weekly, weeklyBudget, suppressed, hourly)

	return &BudgetStatus{
		Team:            team,
		Namespace:       namespace,
		WeeklyBudget:    weeklyBudget,
		WeeklyUsed:      weekly,
		WeeklyRemaining: remaining,
		WeeklyPct:       math.Round(pct*10) / 10,
		HourlyUsed:      hourly,
		SuppressedCount: suppressed,
		TopNoiseSource:  topSource,
		NoiseScore:      noiseScore,
		NoiseLevel:      noiseLevel(noiseScore),
		ActiveRootCauses: roots,
	}
}

// GetTopNoisySources returns namespaces generating the most alert volume.
func (t *Tracker) GetTopNoisySources(limit int) []BudgetStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	nsSet := map[string]bool{}
	for _, a := range t.alerts {
		nsSet[a.Namespace] = true
	}

	var statuses []BudgetStatus
	seen := map[string]bool{}
	for _, a := range t.alerts {
		key := a.Team + "/" + a.Namespace
		if seen[key] {
			continue
		}
		seen[key] = true
		s := t.GetStatus(a.Team, a.Namespace)
		statuses = append(statuses, *s)
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].WeeklyUsed > statuses[j].WeeklyUsed
	})
	if len(statuses) > limit {
		return statuses[:limit]
	}
	return statuses
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (t *Tracker) budgetFor(team, ns string) BudgetConfig {
	if cfg, ok := t.configs[budgetKey(team, ns)]; ok {
		return cfg
	}
	if cfg, ok := t.configs[budgetKey(team, "")]; ok {
		return cfg
	}
	return DefaultBudgetConfig
}

func (t *Tracker) prune() {
	cutoff := time.Now().Add(-t.maxAge)
	i := 0
	for i < len(t.alerts) && t.alerts[i].FiredAt.Before(cutoff) {
		i++
	}
	t.alerts = t.alerts[i:]
}

func budgetKey(team, ns string) string {
	return strings.ToLower(team + "/" + ns)
}

func calculateNoiseScore(weekly, budget, suppressed, hourly int) float64 {
	if budget <= 0 {
		budget = DefaultBudgetConfig.WeeklyBudget
	}
	volumeScore := math.Min(float64(weekly)/float64(budget), 1.5) * 50
	burstScore := math.Min(float64(hourly)/float64(DefaultBudgetConfig.HourlyBudget), 1.5) * 30
	suppressScore := math.Min(float64(suppressed)/float64(weekly+1), 1.0) * 20
	return math.Min(math.Round((volumeScore+burstScore+suppressScore)*10)/10, 100)
}

func noiseLevel(score float64) string {
	switch {
	case score >= 75:
		return "critical"
	case score >= 45:
		return "warning"
	default:
		return "healthy"
	}
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1fh", d.Hours())
	}
}
