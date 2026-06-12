// Package risk implements the predictive change risk scoring engine.
// Scores any incoming Kubernetes change for its likelihood of causing
// a production incident — before it's applied. This is what the admission
// webhook uses, and what the GitOps correlation layer exposes to operators.
//
// No equivalent exists in Dynatrace, Datadog, or any market tool:
// they all detect incidents AFTER deployment. This scores BEFORE.
package risk

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Level classifies the overall risk of a change.
type Level string

const (
	LevelLow      Level = "low"
	LevelMedium   Level = "medium"
	LevelHigh     Level = "high"
	LevelCritical Level = "critical"
)

// ChangeType classifies what kind of change is being made.
type ChangeType string

const (
	ChangeImageUpdate   ChangeType = "image_update"
	ChangeScale         ChangeType = "scale"
	ChangeConfigUpdate  ChangeType = "config_update"
	ChangeSecretRotate  ChangeType = "secret_rotate"
	ChangeResourceLimits ChangeType = "resource_limits"
	ChangeNetworkPolicy ChangeType = "network_policy"
	ChangeRBACChange    ChangeType = "rbac_change"
	ChangeDelete        ChangeType = "delete"
	ChangeNew           ChangeType = "new_resource"
)

// ChangeInput describes an incoming cluster change to be scored.
type ChangeInput struct {
	ClusterID    string
	ResourceKind string
	Namespace    string
	Name         string
	ChangeType   ChangeType
	Actor        string    // kubectl user, ArgoCD, Helm, GitHub Actions
	Timestamp    time.Time

	// Optional enrichment fields — improve accuracy when present
	OldImageTag      string
	NewImageTag      string
	ReplicasBefore   int
	ReplicasAfter    int
	FieldsChanged    []string // JSON paths that changed
	NamespaceLabels  map[string]string
}

// Factor is one contributing reason to the overall risk score.
type Factor struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Weight      float64 `json:"weight"`   // contribution to total score (0-1)
	Score       float64 `json:"score"`    // raw factor score (0-1)
}

// Score is the complete risk assessment for a change.
type Score struct {
	Raw     float64 `json:"raw_score"`  // 0-1
	Level   Level   `json:"level"`
	Factors []Factor `json:"factors"`
	Summary string  `json:"summary"`

	// Enrichment: similar past incidents if history is provided
	SimilarIncidents []HistoricalIncident `json:"similar_incidents,omitempty"`
}

// HistoricalIncident is a past incident used to contextualize the risk score.
type HistoricalIncident struct {
	ID          string    `json:"id"`
	ResourceKind string   `json:"resource_kind"`
	Namespace   string    `json:"namespace"`
	FailureMode string    `json:"failure_mode"`
	OccurredAt  time.Time `json:"occurred_at"`
	Similarity  float64   `json:"similarity"` // 0-1
}

// IncidentRecord is one historical data point for the engine's memory.
type IncidentRecord struct {
	ClusterID    string
	ResourceKind string
	Namespace    string
	ChangeType   ChangeType
	FailureMode  string
	OccurredAt   time.Time
	Resolved     bool
}

// Engine scores incoming changes for incident likelihood.
// Feed it historical incidents via RecordIncident so its accuracy improves.
type Engine struct {
	history []IncidentRecord
}

// NewEngine creates an empty risk engine.
func NewEngine() *Engine {
	return &Engine{}
}

// RecordIncident adds a historical incident to the engine's memory.
// The engine uses this to calibrate namespace-level and change-type risk.
func (e *Engine) RecordIncident(r IncidentRecord) {
	e.history = append(e.history, r)
}

// Score computes the risk score for an incoming change.
func (e *Engine) Score(c ChangeInput) Score {
	var factors []Factor

	// ── Factor 1: Resource type inherent risk ─────────────────────────────
	// Based on blast radius: node failures affect all pods on it;
	// configmap changes can silently break all consuming pods, etc.
	rtScore := resourceTypeRisk(c.ResourceKind)
	factors = append(factors, Factor{
		Name:        "resource_type",
		Description: c.ResourceKind + " changes have " + resourceTypeDescription(c.ResourceKind),
		Weight:      0.25,
		Score:       rtScore,
	})

	// ── Factor 2: Change type risk ─────────────────────────────────────────
	ctScore := changeTypeRisk(c.ChangeType)
	factors = append(factors, Factor{
		Name:        "change_type",
		Description: string(c.ChangeType) + " has " + changeTypeDescription(c.ChangeType),
		Weight:      0.20,
		Score:       ctScore,
	})

	// ── Factor 3: Namespace production tier ───────────────────────────────
	tierScore := namespaceTierRisk(c.Namespace, c.NamespaceLabels)
	factors = append(factors, Factor{
		Name:        "namespace_tier",
		Description: "Namespace " + c.Namespace + " is in the " + namespaceTierLabel(c.Namespace, c.NamespaceLabels) + " tier",
		Weight:      0.20,
		Score:       tierScore,
	})

	// ── Factor 4: Time of day / business hours ────────────────────────────
	todScore := timeOfDayRisk(c.Timestamp)
	factors = append(factors, Factor{
		Name:        "time_of_day",
		Description: timeOfDayDescription(c.Timestamp),
		Weight:      0.10,
		Score:       todScore,
	})

	// ── Factor 5: Scale change magnitude ──────────────────────────────────
	if c.ChangeType == ChangeScale && (c.ReplicasBefore > 0 || c.ReplicasAfter > 0) {
		scaleScore := scaleChangeRisk(c.ReplicasBefore, c.ReplicasAfter)
		factors = append(factors, Factor{
			Name:        "scale_change_magnitude",
			Description: scaleDescription(c.ReplicasBefore, c.ReplicasAfter),
			Weight:      0.10,
			Score:       scaleScore,
		})
	}

	// ── Factor 6: Image tag patterns ─────────────────────────────────────
	if c.ChangeType == ChangeImageUpdate && c.NewImageTag != "" {
		imgScore := imageTagRisk(c.OldImageTag, c.NewImageTag)
		factors = append(factors, Factor{
			Name:        "image_tag_pattern",
			Description: imageTagDescription(c.OldImageTag, c.NewImageTag),
			Weight:      0.10,
			Score:       imgScore,
		})
	}

	// ── Factor 7: Historical incident rate in this namespace ──────────────
	histScore := e.historicalIncidentRate(c.ClusterID, c.Namespace, c.ResourceKind)
	factors = append(factors, Factor{
		Name:        "historical_incident_rate",
		Description: e.historicalDescription(c.ClusterID, c.Namespace),
		Weight:      0.15,
		Score:       histScore,
	})

	// Normalize weights to exactly 1.0 given variable factor set
	totalWeight := 0.0
	for _, f := range factors {
		totalWeight += f.Weight
	}
	raw := 0.0
	for _, f := range factors {
		raw += (f.Weight / totalWeight) * f.Score
	}

	raw = math.Min(1.0, math.Max(0.0, raw))

	score := Score{
		Raw:     raw,
		Level:   scoreToLevel(raw),
		Factors: factors,
		Summary: buildSummary(raw, c, factors),
	}

	// Attach similar historical incidents for operator context
	score.SimilarIncidents = e.findSimilarIncidents(c)
	return score
}

// ─── Factor computation ────────────────────────────────────────────────────────

var resourceTypeWeights = map[string]float64{
	"Node":                    0.95,
	"PersistentVolume":        0.85,
	"PersistentVolumeClaim":   0.80,
	"StatefulSet":             0.75,
	"ConfigMap":               0.70,
	"Secret":                  0.72,
	"ServiceAccount":          0.65,
	"ClusterRole":             0.80,
	"ClusterRoleBinding":      0.80,
	"Role":                    0.60,
	"RoleBinding":             0.60,
	"NetworkPolicy":           0.70,
	"DaemonSet":               0.65,
	"Deployment":              0.50,
	"ReplicaSet":              0.40,
	"Service":                 0.45,
	"Ingress":                 0.55,
	"HorizontalPodAutoscaler": 0.40,
	"Job":                     0.30,
	"CronJob":                 0.30,
	"Pod":                     0.25,
	"Namespace":               0.60,
}

func resourceTypeRisk(kind string) float64 {
	if w, ok := resourceTypeWeights[kind]; ok {
		return w
	}
	return 0.40
}

func resourceTypeDescription(kind string) string {
	switch kind {
	case "Node":
		return "cluster-wide blast radius (all pods on node affected)"
	case "ConfigMap", "Secret":
		return "silent propagation risk (all consuming pods restart on hot-reload)"
	case "StatefulSet":
		return "ordered rollout risk (persistent state may be corrupted)"
	case "ClusterRole", "ClusterRoleBinding":
		return "cluster-wide RBAC impact"
	case "NetworkPolicy":
		return "connectivity blast radius"
	default:
		return "standard blast radius"
	}
}

var changeTypeWeights = map[ChangeType]float64{
	ChangeDelete:        0.95,
	ChangeRBACChange:    0.85,
	ChangeNetworkPolicy: 0.80,
	ChangeSecretRotate:  0.70,
	ChangeConfigUpdate:  0.65,
	ChangeResourceLimits: 0.55,
	ChangeImageUpdate:   0.50,
	ChangeScale:         0.35,
	ChangeNew:           0.25,
}

func changeTypeRisk(ct ChangeType) float64 {
	if w, ok := changeTypeWeights[ct]; ok {
		return w
	}
	return 0.40
}

func changeTypeDescription(ct ChangeType) string {
	switch ct {
	case ChangeDelete:
		return "irreversible blast radius"
	case ChangeRBACChange:
		return "security surface change"
	case ChangeSecretRotate:
		return "credential rotation risk (consumers may cache old value)"
	case ChangeConfigUpdate:
		return "configuration propagation risk"
	case ChangeImageUpdate:
		return "runtime behavior change risk"
	case ChangeScale:
		return "capacity adjustment risk"
	default:
		return "standard change risk"
	}
}

// productionNamespacePrefixes are patterns that indicate a namespace
// is serving live traffic. Adjust for your organization's conventions.
var productionNamespacePrefixes = []string{
	"prod", "production", "live", "release",
	"critical", "main", "default",
}

var stagingNamespacePrefixes = []string{
	"stage", "staging", "uat", "pre-prod", "preprod", "integration",
}

func namespaceTierRisk(ns string, labels map[string]string) float64 {
	ns = strings.ToLower(ns)
	// Check explicit tier labels first (most authoritative)
	if labels != nil {
		switch strings.ToLower(labels["tier"]) {
		case "production", "prod":
			return 0.90
		case "staging", "uat":
			return 0.55
		case "development", "dev", "test":
			return 0.20
		}
		switch strings.ToLower(labels["environment"]) {
		case "production", "prod":
			return 0.90
		case "staging", "uat":
			return 0.55
		case "development", "dev", "test":
			return 0.20
		}
	}
	for _, prefix := range productionNamespacePrefixes {
		if strings.HasPrefix(ns, prefix) || strings.Contains(ns, prefix) {
			return 0.90
		}
	}
	for _, prefix := range stagingNamespacePrefixes {
		if strings.HasPrefix(ns, prefix) || strings.Contains(ns, prefix) {
			return 0.55
		}
	}
	// kube-system and similar are high risk
	if ns == "kube-system" || ns == "kube-public" || ns == "cert-manager" || ns == "istio-system" {
		return 0.85
	}
	return 0.30 // assume dev/test by default
}

func namespaceTierLabel(ns string, labels map[string]string) string {
	risk := namespaceTierRisk(ns, labels)
	switch {
	case risk >= 0.80:
		return "production"
	case risk >= 0.50:
		return "staging"
	default:
		return "development"
	}
}

// timeOfDayRisk returns higher risk during business hours and peak traffic windows.
// Lower risk during maintenance windows (nights and weekends).
func timeOfDayRisk(t time.Time) float64 {
	// Convert to UTC weekday and hour for consistent evaluation
	hour := t.UTC().Hour()
	weekday := t.UTC().Weekday()

	// Weekend deployments have lower baseline traffic but higher operational risk
	// (fewer engineers available to respond to incidents)
	if weekday == time.Saturday || weekday == time.Sunday {
		return 0.60
	}
	// Business hours (8am-6pm UTC): highest traffic, highest blast radius
	if hour >= 8 && hour < 18 {
		return 0.80
	}
	// Evening hours: moderate traffic
	if hour >= 18 && hour < 22 {
		return 0.55
	}
	// Night/early morning: lowest traffic, safest deployment window
	return 0.25
}

func timeOfDayDescription(t time.Time) string {
	risk := timeOfDayRisk(t)
	switch {
	case risk >= 0.75:
		return "deployment during peak business hours — highest incident blast radius"
	case risk >= 0.50:
		return "deployment during evening hours — moderate traffic"
	case risk >= 0.40:
		return "weekend deployment — lower traffic but reduced on-call coverage"
	default:
		return "deployment during low-traffic window — lowest blast radius"
	}
}

func scaleChangeRisk(before, after int) float64 {
	if before == 0 && after == 0 {
		return 0.10
	}
	if before == 0 {
		return 0.20 // scale-up from zero: low risk
	}
	ratio := float64(after) / float64(before)
	// Scaling to zero: very high risk (service outage)
	if after == 0 {
		return 0.95
	}
	// Large scale-down: high risk
	if ratio < 0.5 {
		return 0.75
	}
	// Moderate scale-down
	if ratio < 0.8 {
		return 0.45
	}
	// Scale-up: generally safe
	if ratio > 1.0 {
		return 0.20
	}
	return 0.30
}

func scaleDescription(before, after int) string {
	if after == 0 {
		return "scale to zero — service will become unavailable"
	}
	if before == 0 {
		return "initial scale-up from zero"
	}
	return ""
}

func imageTagRisk(oldTag, newTag string) float64 {
	// latest tag: high risk (non-deterministic)
	if newTag == "latest" || newTag == "main" || newTag == "master" {
		return 0.90
	}
	// SHA digest: lowest risk (fully pinned)
	if strings.HasPrefix(newTag, "sha256:") {
		return 0.10
	}
	// Downgrade (tag looks older, e.g. v1.2.0 → v1.1.0): elevated risk
	if oldTag != "" && strings.Compare(newTag, oldTag) < 0 {
		return 0.70
	}
	// Major version jump (v1.x → v2.x): high risk
	if oldTag != "" && majorVersion(newTag) > majorVersion(oldTag) {
		return 0.80
	}
	// Semver minor/patch: normal risk
	return 0.40
}

func imageTagDescription(oldTag, newTag string) string {
	if newTag == "latest" {
		return "using mutable :latest tag — image content is non-deterministic"
	}
	if strings.HasPrefix(newTag, "sha256:") {
		return "pinned to SHA digest — fully reproducible"
	}
	if majorVersion(newTag) > majorVersion(oldTag) {
		return "major version bump — breaking changes possible"
	}
	return "standard semver tag update"
}

func majorVersion(tag string) int {
	tag = strings.TrimPrefix(tag, "v")
	if parts := strings.SplitN(tag, ".", 2); len(parts) > 0 {
		n := 0
		for _, c := range parts[0] {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		return n
	}
	return 0
}

// ─── Historical context ────────────────────────────────────────────────────────

func (e *Engine) historicalIncidentRate(clusterID, namespace, kind string) float64 {
	if len(e.history) == 0 {
		return 0.30 // no history — conservative prior
	}
	cutoff := time.Now().AddDate(0, 0, -30) // 30-day window
	total, nsMatches, kindMatches := 0, 0, 0
	for _, h := range e.history {
		if h.ClusterID != clusterID || h.OccurredAt.Before(cutoff) {
			continue
		}
		total++
		if h.Namespace == namespace {
			nsMatches++
		}
		if h.ResourceKind == kind {
			kindMatches++
		}
	}
	if total == 0 {
		return 0.25
	}
	// Combine namespace and kind incident rates
	nsRate := float64(nsMatches) / float64(total)
	kindRate := float64(kindMatches) / float64(total)
	return math.Min(0.95, (nsRate*0.6+kindRate*0.4)*3.0)
}

func (e *Engine) historicalDescription(clusterID, namespace string) string {
	cutoff := time.Now().AddDate(0, 0, -30)
	count := 0
	for _, h := range e.history {
		if h.ClusterID == clusterID && h.Namespace == namespace &&
			h.OccurredAt.After(cutoff) {
			count++
		}
	}
	if count == 0 {
		return "no incidents in " + namespace + " in the past 30 days"
	}
	return fmt.Sprintf("%d incident(s) in namespace %s in the past 30 days", count, namespace)
}

func (e *Engine) findSimilarIncidents(c ChangeInput) []HistoricalIncident {
	var matches []HistoricalIncident
	cutoff := time.Now().AddDate(0, -6, 0) // 6-month history

	for _, h := range e.history {
		if h.OccurredAt.Before(cutoff) {
			continue
		}
		sim := incidentSimilarity(c, h)
		if sim >= 0.60 {
			matches = append(matches, HistoricalIncident{
				ResourceKind: h.ResourceKind,
				Namespace:    h.Namespace,
				FailureMode:  h.FailureMode,
				OccurredAt:   h.OccurredAt,
				Similarity:   sim,
			})
		}
	}

	// Return top 3 most similar
	if len(matches) > 3 {
		// Simple partial sort: bubble top 3
		for i := 0; i < 3 && i < len(matches); i++ {
			for j := i + 1; j < len(matches); j++ {
				if matches[j].Similarity > matches[i].Similarity {
					matches[i], matches[j] = matches[j], matches[i]
				}
			}
		}
		matches = matches[:3]
	}
	return matches
}

func incidentSimilarity(c ChangeInput, h IncidentRecord) float64 {
	score := 0.0
	total := 0.0

	total += 1.0
	if c.ResourceKind == h.ResourceKind {
		score += 1.0
	}
	total += 0.8
	if c.Namespace == h.Namespace {
		score += 0.8
	}
	total += 0.6
	if c.ChangeType == h.ChangeType {
		score += 0.6
	}
	return score / total
}

// ─── Scoring helpers ──────────────────────────────────────────────────────────

func scoreToLevel(raw float64) Level {
	switch {
	case raw >= 0.80:
		return LevelCritical
	case raw >= 0.60:
		return LevelHigh
	case raw >= 0.35:
		return LevelMedium
	default:
		return LevelLow
	}
}

func buildSummary(raw float64, c ChangeInput, factors []Factor) string {
	level := scoreToLevel(raw)
	topFactor := ""
	topScore := 0.0
	for _, f := range factors {
		if f.Score*f.Weight > topScore {
			topScore = f.Score * f.Weight
			topFactor = f.Description
		}
	}
	return fmt.Sprintf("%s risk (%.0f%%) — %s → %s/%s. Top driver: %s",
		strings.ToUpper(string(level)), raw*100,
		string(c.ChangeType), c.Namespace, c.Name, topFactor)
}
