// Package change implements GitOps change intelligence.
// Correlates Kubernetes resource changes with upstream Git commits, ArgoCD sync
// operations, and Helm releases. When an incident occurs, operators immediately
// see: "PR #1234 by alice@example.com merged 8 minutes ago — 87% correlation."
//
// Market gap: Change tracking tools (OpsLevel, Cortex) track changes but don't
// correlate them with incidents using causal evidence scoring. KubeSense does both.
package change

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Source classifies where a change came from.
type Source string

const (
	SourceArgoCD   Source = "argocd"
	SourceFlux     Source = "flux"
	SourceHelm     Source = "helm"
	SourceKubectl  Source = "kubectl"
	SourceGitHub   Source = "github"
	SourceGitLab   Source = "gitlab"
	SourcePipeline Source = "ci_pipeline"
	SourceUnknown  Source = "unknown"
)

// GitCommit holds metadata from a git commit or pull request merge.
type GitCommit struct {
	SHA          string
	ShortSHA     string
	PRNumber     int
	PRTitle      string
	Author       string // email or username
	AuthorName   string
	MergedAt     time.Time
	Repository   string
	Branch       string
	FilesChanged []string
	AddedLines   int
	RemovedLines int
}

// K8sChange is a Kubernetes resource change event from the Kafka stream.
type K8sChange struct {
	ClusterID       string
	Source          Source
	ResourceKind    string
	Namespace       string
	Name            string
	Actor           string
	OccurredAt      time.Time
	ResourceVersion string

	// Enrichment set by the correlator
	GitCommit   *GitCommit
	HelmRelease string
	HelmVersion string
	ArgoAppName string
}

// CorrelatedChange links a K8s change to its upstream git/CI source.
type CorrelatedChange struct {
	K8sChange
	CorrelationScore float64 `json:"correlation_score"` // 0-1
	CorrelationBasis string  `json:"correlation_basis"`
	TimeLagSeconds   int64   `json:"time_lag_seconds"`
}

// IncidentChanges is the output for a given incident window.
type IncidentChanges struct {
	IncidentID  string             `json:"incident_id"`
	WindowStart time.Time          `json:"window_start"`
	WindowEnd   time.Time          `json:"window_end"`
	Changes     []CorrelatedChange `json:"changes"`
	TopChange   *CorrelatedChange  `json:"top_change,omitempty"`
	ChangeCount int                `json:"change_count"`
}

// Correlator maintains an in-memory buffer of recent changes and
// correlates them against incident time windows.
type Correlator struct {
	mu      sync.RWMutex
	changes []K8sChange
	commits []GitCommit
	maxAge  time.Duration
}

// NewCorrelator creates a correlator with a configurable change buffer window.
func NewCorrelator(maxAge time.Duration) *Correlator {
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	return &Correlator{maxAge: maxAge}
}

// RecordK8sChange ingests a Kubernetes resource change from the Kafka stream.
func (c *Correlator) RecordK8sChange(ch K8sChange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.changes = append(c.changes, ch)
	c.pruneOldChanges()
}

// RecordGitCommit ingests a Git commit or PR merge from a webhook payload.
func (c *Correlator) RecordGitCommit(commit GitCommit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits = append(c.commits, commit)
}

// GetForIncident returns all changes within the lookback window before
// the incident time, scored by causal correlation strength.
func (c *Correlator) GetForIncident(
	incidentID string,
	incidentTime time.Time,
	lookback time.Duration,
) *IncidentChanges {
	if lookback <= 0 {
		lookback = 6 * time.Hour
	}
	windowStart := incidentTime.Add(-lookback)

	c.mu.RLock()
	defer c.mu.RUnlock()

	var correlated []CorrelatedChange
	for _, ch := range c.changes {
		if ch.OccurredAt.Before(windowStart) || ch.OccurredAt.After(incidentTime) {
			continue
		}
		lag := int64(incidentTime.Sub(ch.OccurredAt).Seconds())
		score, basis := scoreChange(ch, incidentTime)

		// Attempt git commit enrichment
		enriched := ch
		for i := range c.commits {
			if matchCommitToChange(c.commits[i], ch) {
				enriched.GitCommit = &c.commits[i]
				score = adjustScoreForGit(score, c.commits[i])
				break
			}
		}

		correlated = append(correlated, CorrelatedChange{
			K8sChange:        enriched,
			CorrelationScore: score,
			CorrelationBasis: basis,
			TimeLagSeconds:   lag,
		})
	}

	// Sort by score descending (insertion sort — small lists)
	for i := 1; i < len(correlated); i++ {
		for j := i; j > 0 && correlated[j].CorrelationScore > correlated[j-1].CorrelationScore; j-- {
			correlated[j], correlated[j-1] = correlated[j-1], correlated[j]
		}
	}

	result := &IncidentChanges{
		IncidentID:  incidentID,
		WindowStart: windowStart,
		WindowEnd:   incidentTime,
		Changes:     correlated,
		ChangeCount: len(correlated),
	}
	if len(correlated) > 0 {
		top := correlated[0]
		result.TopChange = &top
	}
	return result
}

// ─── Scoring ──────────────────────────────────────────────────────────────────

func scoreChange(ch K8sChange, incidentTime time.Time) (float64, string) {
	lag := incidentTime.Sub(ch.OccurredAt)
	if lag < 0 {
		return 0, "change after incident"
	}
	timeScore := timeProximityScore(lag)
	impactScore := changeImpactScore(ch.ResourceKind)
	score := timeScore*0.55 + impactScore*0.45
	basis := buildBasis(ch, lag)
	return score, basis
}

func timeProximityScore(lag time.Duration) float64 {
	m := lag.Minutes()
	switch {
	case m <= 5:
		return 0.95
	case m <= 15:
		return 0.85
	case m <= 30:
		return 0.75
	case m <= 60:
		return 0.60
	case m <= 120:
		return 0.45
	case m <= 360:
		return 0.25
	default:
		return 0.10
	}
}

func changeImpactScore(kind string) float64 {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
		return 0.85
	case "ConfigMap", "Secret":
		return 0.75
	case "Service", "Ingress":
		return 0.65
	case "PersistentVolumeClaim":
		return 0.60
	case "HorizontalPodAutoscaler":
		return 0.55
	case "Node":
		return 0.90
	default:
		return 0.40
	}
}

func buildBasis(ch K8sChange, lag time.Duration) string {
	parts := []string{
		fmt.Sprintf("%s change %s before incident", ch.ResourceKind, humanDuration(lag)),
	}
	if ch.Source != SourceUnknown && ch.Source != "" {
		parts = append(parts, "via "+string(ch.Source))
	}
	if ch.Actor != "" {
		parts = append(parts, "by "+ch.Actor)
	}
	return strings.Join(parts, " ")
}

func adjustScoreForGit(score float64, commit GitCommit) float64 {
	totalLines := commit.AddedLines + commit.RemovedLines
	if totalLines > 500 {
		score *= 1.15
	} else if totalLines > 100 {
		score *= 1.05
	}
	for _, f := range commit.FilesChanged {
		f = strings.ToLower(f)
		if strings.Contains(f, "values.yaml") || strings.Contains(f, "deployment.yaml") ||
			strings.Contains(f, "configmap") || strings.Contains(f, "secret") {
			score *= 1.10
			break
		}
	}
	if score > 0.98 {
		return 0.98
	}
	return score
}

// matchCommitToChange links a git commit to a K8s change via:
// 1. ArgoCD app name matching repo name
// 2. Helm release name containing repo name
// 3. Actor email matching PR author + time proximity
// 4. Automated CI: commit merge within 2 minutes of K8s change
func matchCommitToChange(commit GitCommit, ch K8sChange) bool {
	if ch.Source == SourceArgoCD && ch.ArgoAppName != "" && commit.Repository != "" {
		if strings.EqualFold(commit.Repository, ch.ArgoAppName) ||
			strings.Contains(strings.ToLower(ch.ArgoAppName), strings.ToLower(commit.Repository)) {
			return true
		}
	}
	if ch.HelmRelease != "" && commit.Repository != "" {
		if strings.Contains(strings.ToLower(ch.HelmRelease), strings.ToLower(commit.Repository)) {
			return true
		}
	}
	if commit.Author != "" && ch.Actor != "" {
		user := extractUsername(commit.Author)
		if strings.EqualFold(user, ch.Actor) || strings.EqualFold(commit.AuthorName, ch.Actor) {
			lag := ch.OccurredAt.Sub(commit.MergedAt)
			if lag >= 0 && lag < 30*time.Minute {
				return true
			}
		}
	}
	if !commit.MergedAt.IsZero() {
		lag := ch.OccurredAt.Sub(commit.MergedAt)
		if lag >= 0 && lag < 2*time.Minute {
			return true
		}
	}
	return false
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (c *Correlator) pruneOldChanges() {
	cutoff := time.Now().Add(-c.maxAge)
	i := 0
	for i < len(c.changes) && c.changes[i].OccurredAt.Before(cutoff) {
		i++
	}
	c.changes = c.changes[i:]
}

func extractUsername(email string) string {
	if idx := strings.Index(email, "@"); idx > 0 {
		return email[:idx]
	}
	return email
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "seconds"
	case d < 2*time.Minute:
		return "1 minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
}
