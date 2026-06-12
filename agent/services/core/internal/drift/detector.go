// Package drift detects configuration drift between live cluster state and
// the desired state stored in Git (GitOps sources: ArgoCD, Flux, Helm).
//
// Drift occurs when someone manually edits a resource with kubectl instead
// of going through Git. It's silent — Kubernetes applies the change, ArgoCD
// shows green, but the cluster no longer matches what Git says.
//
// KubeSense detects drift by comparing:
//   - Resource versions from live K8s informers
//   - Expected state from ArgoCD/Flux sync manifests
//   - Manual change patterns (actor is a human, not a CI bot)
//   - Annotation-based drift markers set by GitOps controllers
//
// When drift is detected, it:
//   1. Publishes to kubesense.drift.detected Kafka topic
//   2. Links the drift to the actor who made it (if change correlator has data)
//   3. Scores drift severity (image drift is high, label drift is low)
//   4. Suggests the remediation (git push vs kubectl apply)
//
// Market gap: ArgoCD shows out-of-sync but doesn't explain WHO drifted it,
// WHEN, or what the blast radius of the drift is if left unchecked.
package drift

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// DriftType classifies what kind of state drifted.
type DriftType string

const (
	DriftImage          DriftType = "image_drift"
	DriftReplicas       DriftType = "replica_drift"
	DriftConfig         DriftType = "config_drift"
	DriftEnvVar         DriftType = "env_var_drift"
	DriftResourceLimits DriftType = "resource_limits_drift"
	DriftAnnotation     DriftType = "annotation_drift"
	DriftLabel          DriftType = "label_drift"
	DriftRBAC           DriftType = "rbac_drift"
	DriftNetworkPolicy  DriftType = "network_policy_drift"
)

// DriftSeverity rates how dangerous the drift is.
type DriftSeverity string

const (
	DriftSeverityCritical DriftSeverity = "critical" // security/image drift in prod
	DriftSeverityHigh     DriftSeverity = "high"     // config/replica drift in prod
	DriftSeverityMedium   DriftSeverity = "medium"   // drift in staging
	DriftSeverityLow      DriftSeverity = "low"      // label/annotation drift
)

// DriftRecord is one detected configuration drift.
type DriftRecord struct {
	ID           string        `json:"id"`
	DetectedAt   time.Time     `json:"detected_at"`
	ClusterID    string        `json:"cluster_id"`
	ResourceKind string        `json:"resource_kind"`
	Namespace    string        `json:"namespace"`
	Name         string        `json:"name"`
	DriftType    DriftType     `json:"drift_type"`
	Severity     DriftSeverity `json:"severity"`

	// What drifted
	Field        string `json:"field"`         // e.g. "spec.containers[0].image"
	ExpectedValue string `json:"expected_value"` // from Git/desired state
	ActualValue   string `json:"actual_value"`   // from live cluster

	// Who and when
	Actor        string    `json:"actor,omitempty"`  // from change correlator
	DriftedAt    time.Time `json:"drifted_at,omitempty"`
	GitSource    string    `json:"git_source,omitempty"` // ArgoCD app / Flux source

	// Impact
	BlastRadius  string `json:"blast_radius"` // pod / service / cluster
	Description  string `json:"description"`
	Remediation  string `json:"remediation"`

	// State
	IsResolved   bool      `json:"is_resolved"`
	ResolvedAt   time.Time `json:"resolved_at,omitempty"`
	ResolvedBy   string    `json:"resolved_by,omitempty"`
}

// DesiredState is the expected configuration from a GitOps source.
type DesiredState struct {
	ResourceKind string
	Namespace    string
	Name         string
	GitSource    string // ArgoCD app name, Flux source name
	Fields       map[string]string // field path -> expected value
	LastSyncedAt time.Time
}

// LiveState is the observed configuration from the K8s informer.
type LiveState struct {
	ResourceKind string
	Namespace    string
	Name         string
	Fields       map[string]string
	ObservedAt   time.Time
	Actor        string // last-modified-by annotation if present
}

// DriftReport is the full drift status for a cluster.
type DriftReport struct {
	ClusterID      string        `json:"cluster_id"`
	GeneratedAt    time.Time     `json:"generated_at"`
	TotalDrifted   int           `json:"total_drifted"`
	CriticalDrifts int           `json:"critical_drifts"`
	HighDrifts     int           `json:"high_drifts"`
	Records        []DriftRecord `json:"records"`
	DriftScore     float64       `json:"drift_score"`   // 0-100, higher = more drift
	DriftLevel     string        `json:"drift_level"`   // clean / drifted / critical
	TopActor       string        `json:"top_actor,omitempty"` // who caused most drift
	Summary        string        `json:"summary"`
}

// Detector compares desired vs live state and emits drift records.
type Detector struct {
	mu       sync.RWMutex
	desired  map[string]DesiredState  // key: kind/ns/name
	records  []DriftRecord
	maxAge   time.Duration
}

// NewDetector creates a drift detector.
func NewDetector(maxAge time.Duration) *Detector {
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	return &Detector{
		desired: make(map[string]DesiredState),
		maxAge:  maxAge,
	}
}

// RegisterDesiredState registers what a resource SHOULD look like (from Git).
// Call this when an ArgoCD/Flux sync event arrives or on startup.
func (d *Detector) RegisterDesiredState(ds DesiredState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.desired[resourceKey(ds.ResourceKind, ds.Namespace, ds.Name)] = ds
}

// Observe compares live state against the registered desired state.
// Returns any new drift records detected. Call on every resource update event.
func (d *Detector) Observe(live LiveState) []DriftRecord {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := resourceKey(live.ResourceKind, live.Namespace, live.Name)
	desired, ok := d.desired[key]
	if !ok {
		// No desired state registered — can't detect drift
		return nil
	}

	var newRecords []DriftRecord
	for field, expectedVal := range desired.Fields {
		actualVal, exists := live.Fields[field]
		if !exists || actualVal != expectedVal {
			if !d.alreadyRecorded(live.ResourceKind, live.Namespace, live.Name, field) {
				rec := d.buildRecord(live, desired, field, expectedVal, actualVal)
				d.records = append(d.records, rec)
				newRecords = append(newRecords, rec)
			}
		} else {
			// Field is back in sync — mark existing drift as resolved
			d.resolveIfDrifted(live.ResourceKind, live.Namespace, live.Name, field)
		}
	}
	d.prune()
	return newRecords
}

// Resolve marks a drift record as resolved (e.g. after a git push / sync).
func (d *Detector) Resolve(clusterID, kind, ns, name, field, resolvedBy string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.records {
		r := &d.records[i]
		if r.ClusterID == clusterID && r.ResourceKind == kind &&
			r.Namespace == ns && r.Name == name &&
			r.Field == field && !r.IsResolved {
			r.IsResolved = true
			r.ResolvedAt = time.Now()
			r.ResolvedBy = resolvedBy
		}
	}
}

// Report returns the full drift report for a cluster.
func (d *Detector) Report(clusterID string) *DriftReport {
	d.mu.RLock()
	defer d.mu.RUnlock()

	report := &DriftReport{
		ClusterID:   clusterID,
		GeneratedAt: time.Now(),
	}

	actorCounts := map[string]int{}
	for _, r := range d.records {
		if r.ClusterID != clusterID && clusterID != "" {
			continue
		}
		if r.IsResolved {
			continue
		}
		report.Records = append(report.Records, r)
		report.TotalDrifted++
		if r.Actor != "" {
			actorCounts[r.Actor]++
		}
		switch r.Severity {
		case DriftSeverityCritical:
			report.CriticalDrifts++
		case DriftSeverityHigh:
			report.HighDrifts++
		}
	}

	// Top drifting actor
	topActor, topCount := "", 0
	for actor, count := range actorCounts {
		if count > topCount {
			topActor = actor
			topCount = count
		}
	}
	report.TopActor = topActor

	report.DriftScore = calculateDriftScore(report.CriticalDrifts, report.HighDrifts, report.TotalDrifted)
	report.DriftLevel = driftLevel(report.DriftScore)
	report.Summary = buildDriftSummary(report)
	return report
}

// ActiveDriftCount returns how many unresolved drift records exist.
func (d *Detector) ActiveDriftCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	count := 0
	for _, r := range d.records {
		if !r.IsResolved {
			count++
		}
	}
	return count
}

// ─── Record builder ───────────────────────────────────────────────────────────

func (d *Detector) buildRecord(
	live LiveState,
	desired DesiredState,
	field, expectedVal, actualVal string,
) DriftRecord {
	driftType := classifyDrift(field)
	severity := driftSeverity(driftType, live.Namespace)

	rec := DriftRecord{
		ID:            fmt.Sprintf("drift/%s/%s/%s/%s/%d", live.ResourceKind, live.Namespace, live.Name, field, time.Now().UnixNano()),
		DetectedAt:    time.Now(),
		ResourceKind:  live.ResourceKind,
		Namespace:     live.Namespace,
		Name:          live.Name,
		DriftType:     driftType,
		Severity:      severity,
		Field:         field,
		ExpectedValue: expectedVal,
		ActualValue:   actualVal,
		Actor:         live.Actor,
		DriftedAt:     live.ObservedAt,
		GitSource:     desired.GitSource,
		BlastRadius:   blastRadius(driftType),
		Description:   buildDriftDescription(live, field, expectedVal, actualVal, driftType),
		Remediation:   buildRemediation(live, field, expectedVal, desired.GitSource),
	}
	return rec
}

func (d *Detector) alreadyRecorded(kind, ns, name, field string) bool {
	for _, r := range d.records {
		if r.ResourceKind == kind && r.Namespace == ns &&
			r.Name == name && r.Field == field && !r.IsResolved {
			return true
		}
	}
	return false
}

func (d *Detector) resolveIfDrifted(kind, ns, name, field string) {
	for i := range d.records {
		r := &d.records[i]
		if r.ResourceKind == kind && r.Namespace == ns &&
			r.Name == name && r.Field == field && !r.IsResolved {
			r.IsResolved = true
			r.ResolvedAt = time.Now()
			r.ResolvedBy = "sync"
		}
	}
}

func (d *Detector) prune() {
	cutoff := time.Now().Add(-d.maxAge)
	i := 0
	for i < len(d.records) && d.records[i].DetectedAt.Before(cutoff) && d.records[i].IsResolved {
		i++
	}
	d.records = d.records[i:]
}

// ─── Classification helpers ───────────────────────────────────────────────────

func classifyDrift(field string) DriftType {
	f := strings.ToLower(field)
	switch {
	case strings.Contains(f, "image"):
		return DriftImage
	case strings.Contains(f, "replicas"):
		return DriftReplicas
	case strings.Contains(f, "env"):
		return DriftEnvVar
	case strings.Contains(f, "limits") || strings.Contains(f, "requests"):
		return DriftResourceLimits
	case strings.Contains(f, "annotation"):
		return DriftAnnotation
	case strings.Contains(f, "label"):
		return DriftLabel
	case strings.Contains(f, "configmap") || strings.Contains(f, "config"):
		return DriftConfig
	case strings.Contains(f, "role") || strings.Contains(f, "rbac"):
		return DriftRBAC
	case strings.Contains(f, "networkpolicy"):
		return DriftNetworkPolicy
	default:
		return DriftConfig
	}
}

func driftSeverity(dt DriftType, ns string) DriftSeverity {
	isProd := isProductionNamespace(ns)
	switch dt {
	case DriftImage, DriftRBAC:
		if isProd {
			return DriftSeverityCritical
		}
		return DriftSeverityHigh
	case DriftConfig, DriftEnvVar, DriftNetworkPolicy:
		if isProd {
			return DriftSeverityHigh
		}
		return DriftSeverityMedium
	case DriftReplicas, DriftResourceLimits:
		return DriftSeverityMedium
	default:
		return DriftSeverityLow
	}
}

func blastRadius(dt DriftType) string {
	switch dt {
	case DriftImage, DriftRBAC:
		return "service"
	case DriftNetworkPolicy:
		return "cluster"
	case DriftConfig, DriftEnvVar:
		return "pod"
	default:
		return "pod"
	}
}

func buildDriftDescription(live LiveState, field, expected, actual string, dt DriftType) string {
	actor := live.Actor
	if actor == "" {
		actor = "unknown actor"
	}
	return fmt.Sprintf("%s/%s: field %q drifted from %q → %q (by %s). %s",
		live.Namespace, live.Name, field, expected, actual, actor, driftRisk(dt))
}

func buildRemediation(live LiveState, field, expected, gitSource string) string {
	if gitSource != "" {
		return fmt.Sprintf("Sync via GitOps: ArgoCD app '%s' should restore this field to %q", gitSource, expected)
	}
	return fmt.Sprintf("Revert manually: set %s back to %q in your Git repo, then apply", field, expected)
}

func driftRisk(dt DriftType) string {
	switch dt {
	case DriftImage:
		return "Running image does not match what Git expects — unknown code in production."
	case DriftRBAC:
		return "RBAC permissions were manually elevated — security boundary may be violated."
	case DriftConfig:
		return "Config mismatch may cause unexpected runtime behaviour."
	case DriftReplicas:
		return "Replica count differs from desired — SLO or capacity may be affected."
	default:
		return ""
	}
}

func isProductionNamespace(ns string) bool {
	ns = strings.ToLower(ns)
	for _, p := range []string{"prod", "production", "live", "release", "critical"} {
		if strings.Contains(ns, p) {
			return true
		}
	}
	return ns == "default" || ns == "kube-system"
}

func calculateDriftScore(critical, high, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(critical*30+high*15+total*5) / float64(total*50) * 100
}

func driftLevel(score float64) string {
	switch {
	case score >= 60:
		return "critical"
	case score >= 20:
		return "drifted"
	case score > 0:
		return "minor"
	default:
		return "clean"
	}
}

func buildDriftSummary(r *DriftReport) string {
	if r.TotalDrifted == 0 {
		return "All resources in sync with desired state."
	}
	return fmt.Sprintf("%d resources have drifted (%d critical, %d high). Top actor: %s",
		r.TotalDrifted, r.CriticalDrifts, r.HighDrifts, r.TopActor)
}

func resourceKey(kind, ns, name string) string {
	return fmt.Sprintf("%s/%s/%s", kind, ns, name)
}
