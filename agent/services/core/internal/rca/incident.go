// Package rca — incident lifecycle state machine.
//
// Implements RCA-Operator's Detecting → Active → Resolved lifecycle backed by
// PostgreSQL instead of Kubernetes CRDs. The lifecycle goroutine runs every 30s
// and advances incidents through phases based on timing and signal activity.
//
// Phases:
//   Detecting  — incident fingerprint first observed; waiting for stabilization
//   Active     — confirmed active after stabilization window (default 30s)
//   Resolved   — no confirming signals for HealthyResolveWindow (5 min)
package rca

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Lifecycle timing constants (matching RCA-Operator defaults).
const (
	StabilizationWindow = 30 * time.Second // Detecting → Active
	HealthyResolveWindow = 5 * time.Minute  // Active → Resolved (no new signals)
	MaxTimelineEntries   = 50
)

// IncidentPhase is the lifecycle phase of an incident.
type IncidentPhase string

const (
	PhaseDetecting IncidentPhase = "Detecting"
	PhaseActive    IncidentPhase = "Active"
	PhaseResolved  IncidentPhase = "Resolved"
)

// Incident is the in-memory + DB record for a correlated incident.
type Incident struct {
	ID               string        `json:"id"`
	ClusterID        string        `json:"cluster_id"`
	Fingerprint      string        `json:"fingerprint"`
	IncidentType     string        `json:"incident_type"`
	Severity         string        `json:"severity"`
	Phase            IncidentPhase `json:"phase"`
	Summary          string        `json:"summary"`
	Namespace        string        `json:"namespace"`
	ResourceKind     string        `json:"resource_kind"`
	ResourceName     string        `json:"resource_name"`
	RuleName         string        `json:"rule_name"`
	FirstObservedAt  time.Time     `json:"first_observed_at"`
	ActiveAt         *time.Time    `json:"active_at,omitempty"`
	LastObservedAt   time.Time     `json:"last_observed_at"`
	ResolvedAt       *time.Time    `json:"resolved_at,omitempty"`
	SignalCount      int           `json:"signal_count"`
	CorrelatedSignals []string     `json:"correlated_signals"`
	Timeline         []TimelineEntry `json:"timeline,omitempty"`
}

// TimelineEntry is one event in an incident's history.
type TimelineEntry struct {
	Time  time.Time `json:"time"`
	Event string    `json:"event"`
}

// ─── Fingerprinting (RCA-Operator SHA-1 algorithm) ────────────────────────────

// IncidentFingerprint produces a stable incident identity from scope information.
// Matches RCA-Operator's scope-based fingerprint: "Level|namespace|kind|name"
func IncidentFingerprint(namespace, resourceKind, resourceName, scope string) string {
	var parts []string
	switch scope {
	case "sameNode":
		parts = []string{"Node", resourceName}
	case "sameNamespace":
		parts = []string{"Namespace", namespace}
	case "samePod":
		parts = []string{"Pod", namespace, strings.ToLower(resourceKind), resourceName}
	default:
		parts = []string{"Pod", namespace, strings.ToLower(resourceKind), resourceName}
	}
	raw := strings.Join(parts, "|")
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:6]) // 12 hex chars — same as RCA-Operator
}

// ─── Incident lifecycle manager ───────────────────────────────────────────────

// LifecycleManager manages the Detecting → Active → Resolved state machine.
// It persists incidents to PostgreSQL and fires metrics on transitions.
type LifecycleManager struct {
	db      *sql.DB
	mu      sync.RWMutex
	active  map[string]*Incident // fingerprint → incident
	metrics *RCAMetrics
}

// NewLifecycleManager creates a manager backed by the given DB.
func NewLifecycleManager(db *sql.DB, m *RCAMetrics) *LifecycleManager {
	return &LifecycleManager{
		db:      db,
		active:  make(map[string]*Incident),
		metrics: m,
	}
}

// OnEvent is called when a new correlated event arrives.
// It either creates a new incident (Detecting) or refreshes an existing one.
func (lm *LifecycleManager) OnEvent(ctx context.Context, ev BufferEntry, result CorrelationResult) {
	fp := IncidentFingerprint(ev.Scope.Namespace, ev.Scope.ResourceKind, ev.Scope.ResourceName, result.Scope)

	lm.mu.Lock()
	inc, exists := lm.active[fp]
	if !exists {
		inc = &Incident{
			ID:              newIncidentID(fp),
			ClusterID:       ev.Scope.ClusterID,
			Fingerprint:     fp,
			IncidentType:    result.IncidentType,
			Severity:        result.Severity,
			Phase:           PhaseDetecting,
			Summary:         result.Summary,
			Namespace:       ev.Scope.Namespace,
			ResourceKind:    ev.Scope.ResourceKind,
			ResourceName:    ev.Scope.ResourceName,
			RuleName:        result.RuleName,
			FirstObservedAt: time.Now(),
			LastObservedAt:  time.Now(),
			SignalCount:     1,
		}
		inc.Timeline = append(inc.Timeline, TimelineEntry{
			Time:  time.Now(),
			Event: fmt.Sprintf("Incident detected: %s by rule %s", result.IncidentType, result.RuleName),
		})
		lm.active[fp] = inc
		lm.mu.Unlock()

		lm.persistIncident(ctx, inc)
		if lm.metrics != nil {
			lm.metrics.IncidentsDetecting.WithLabelValues(inc.IncidentType, inc.Severity).Inc()
			lm.metrics.ActiveIncidents.WithLabelValues(inc.IncidentType, inc.Severity).Inc()
		}
		return
	}

	// Update existing incident — refresh last seen, increment signal count
	inc.LastObservedAt = time.Now()
	inc.SignalCount++
	if len(inc.CorrelatedSignals) < 20 {
		inc.CorrelatedSignals = append(inc.CorrelatedSignals, ev.EventType)
	}
	if len(inc.Timeline) < MaxTimelineEntries {
		inc.Timeline = append(inc.Timeline, TimelineEntry{
			Time:  time.Now(),
			Event: fmt.Sprintf("Confirming signal: %s (total: %d)", ev.EventType, inc.SignalCount),
		})
	}
	phase := inc.Phase
	lm.mu.Unlock()

	_ = phase
	lm.updateLastSeen(ctx, inc)
}

// Run executes the lifecycle tick loop; blocks until ctx is cancelled.
func (lm *LifecycleManager) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// Recover in-flight incidents from DB on startup
	lm.loadFromDB(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lm.tick(ctx)
		}
	}
}

// ListActive returns a copy of all non-Resolved incidents.
func (lm *LifecycleManager) ListActive() []*Incident {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	out := make([]*Incident, 0, len(lm.active))
	for _, inc := range lm.active {
		cp := *inc
		out = append(out, &cp)
	}
	return out
}

// ListByCluster returns active incidents for a specific cluster.
func (lm *LifecycleManager) ListByCluster(clusterID string) []*Incident {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	var out []*Incident
	for _, inc := range lm.active {
		if inc.ClusterID == clusterID {
			cp := *inc
			out = append(out, &cp)
		}
	}
	return out
}

// ─── Lifecycle tick ───────────────────────────────────────────────────────────

func (lm *LifecycleManager) tick(ctx context.Context) {
	now := time.Now()

	lm.mu.Lock()
	toProcess := make([]*Incident, 0, len(lm.active))
	for _, inc := range lm.active {
		toProcess = append(toProcess, inc)
	}
	lm.mu.Unlock()

	for _, inc := range toProcess {
		switch inc.Phase {
		case PhaseDetecting:
			lm.tickDetecting(ctx, inc, now)
		case PhaseActive:
			lm.tickActive(ctx, inc, now)
		case PhaseResolved:
			lm.cleanupResolved(inc)
		}
	}
}

func (lm *LifecycleManager) tickDetecting(ctx context.Context, inc *Incident, now time.Time) {
	elapsed := now.Sub(inc.FirstObservedAt)
	if elapsed < StabilizationWindow {
		return // still in stabilization window
	}
	// Check if we've had a signal within the window
	sinceLastSeen := now.Sub(inc.LastObservedAt)
	if sinceLastSeen > StabilizationWindow {
		// No recent signal — resolve before activation
		lm.transitionTo(ctx, inc, PhaseResolved, "Incident cleared before stabilization window")
		return
	}
	// Has signals and stabilization period passed — activate
	lm.transitionTo(ctx, inc, PhaseActive, "Incident confirmed active after stabilization")
}

func (lm *LifecycleManager) tickActive(ctx context.Context, inc *Incident, now time.Time) {
	idle := now.Sub(inc.LastObservedAt)
	if idle < HealthyResolveWindow {
		return // still receiving signals
	}
	// No signals for HealthyResolveWindow — resolve
	lm.transitionTo(ctx, inc, PhaseResolved,
		fmt.Sprintf("No confirming signals for %.0f minutes", HealthyResolveWindow.Minutes()))
}

func (lm *LifecycleManager) cleanupResolved(inc *Incident) {
	// Remove resolved incidents from in-memory map after 15 minutes
	if inc.ResolvedAt != nil && time.Since(*inc.ResolvedAt) > 15*time.Minute {
		lm.mu.Lock()
		delete(lm.active, inc.Fingerprint)
		lm.mu.Unlock()
	}
}

func (lm *LifecycleManager) transitionTo(ctx context.Context, inc *Incident, phase IncidentPhase, reason string) {
	lm.mu.Lock()
	prevPhase := inc.Phase
	inc.Phase = phase
	now := time.Now()
	if phase == PhaseActive {
		inc.ActiveAt = &now
	} else if phase == PhaseResolved {
		inc.ResolvedAt = &now
	}
	inc.Timeline = appendTimeline(inc.Timeline, TimelineEntry{
		Time:  now,
		Event: fmt.Sprintf("%s → %s: %s", prevPhase, phase, reason),
	})
	lm.mu.Unlock()

	lm.persistIncident(ctx, inc)

	if lm.metrics != nil {
		switch phase {
		case PhaseActive:
			duration := now.Sub(inc.FirstObservedAt).Seconds()
			lm.metrics.IncidentsActivated.WithLabelValues(inc.IncidentType, inc.Severity).Inc()
			lm.metrics.TransitionSeconds.WithLabelValues("detecting", "active").Observe(duration)
		case PhaseResolved:
			from := "detecting"
			if prevPhase == PhaseActive {
				from = "active"
			}
			var duration float64
			if prevPhase == PhaseActive && inc.ActiveAt != nil {
				duration = now.Sub(*inc.ActiveAt).Seconds()
			} else {
				duration = now.Sub(inc.FirstObservedAt).Seconds()
			}
			lm.metrics.IncidentsResolved.WithLabelValues(inc.IncidentType, inc.Severity).Inc()
			lm.metrics.TransitionSeconds.WithLabelValues(from, "resolved").Observe(duration)
			lm.metrics.ActiveIncidents.WithLabelValues(inc.IncidentType, inc.Severity).Dec()
		}
	}
}

// ─── Persistence ──────────────────────────────────────────────────────────────

func (lm *LifecycleManager) persistIncident(ctx context.Context, inc *Incident) {
	if lm.db == nil {
		return
	}
	signalsJSON, _ := json.Marshal(inc.CorrelatedSignals)
	timelineJSON, _ := json.Marshal(inc.Timeline)
	_, _ = lm.db.ExecContext(ctx, `
		INSERT INTO kubesense_incidents
		    (id, cluster_id, fingerprint, incident_type, severity, phase, summary,
		     namespace, resource_kind, resource_name, rule_name,
		     first_observed_at, active_at, last_observed_at, resolved_at,
		     signal_count, correlated_signals, timeline, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NOW())
		ON CONFLICT (id) DO UPDATE SET
		    phase              = EXCLUDED.phase,
		    active_at          = EXCLUDED.active_at,
		    last_observed_at   = EXCLUDED.last_observed_at,
		    resolved_at        = EXCLUDED.resolved_at,
		    signal_count       = EXCLUDED.signal_count,
		    correlated_signals = EXCLUDED.correlated_signals,
		    timeline           = EXCLUDED.timeline,
		    updated_at         = NOW()
	`, inc.ID, inc.ClusterID, inc.Fingerprint, inc.IncidentType, inc.Severity,
		string(inc.Phase), inc.Summary, inc.Namespace, inc.ResourceKind, inc.ResourceName,
		inc.RuleName, inc.FirstObservedAt, inc.ActiveAt, inc.LastObservedAt, inc.ResolvedAt,
		inc.SignalCount, signalsJSON, timelineJSON)
}

func (lm *LifecycleManager) updateLastSeen(ctx context.Context, inc *Incident) {
	if lm.db == nil {
		return
	}
	signalsJSON, _ := json.Marshal(inc.CorrelatedSignals)
	timelineJSON, _ := json.Marshal(inc.Timeline)
	_, _ = lm.db.ExecContext(ctx, `
		UPDATE kubesense_incidents
		SET last_observed_at = $2, signal_count = $3,
		    correlated_signals = $4, timeline = $5, updated_at = NOW()
		WHERE id = $1
	`, inc.ID, inc.LastObservedAt, inc.SignalCount, signalsJSON, timelineJSON)
}

func (lm *LifecycleManager) loadFromDB(ctx context.Context) {
	if lm.db == nil {
		return
	}
	rows, err := lm.db.QueryContext(ctx, `
		SELECT id, cluster_id, fingerprint, incident_type, severity, phase, summary,
		       namespace, resource_kind, resource_name, rule_name,
		       first_observed_at, active_at, last_observed_at, resolved_at,
		       signal_count, correlated_signals, timeline
		FROM kubesense_incidents
		WHERE phase != 'Resolved' OR resolved_at > NOW() - INTERVAL '15 minutes'
		LIMIT 500
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	lm.mu.Lock()
	defer lm.mu.Unlock()
	for rows.Next() {
		var inc Incident
		var signalsJSON, timelineJSON []byte
		var phase string
		if err := rows.Scan(&inc.ID, &inc.ClusterID, &inc.Fingerprint, &inc.IncidentType,
			&inc.Severity, &phase, &inc.Summary, &inc.Namespace, &inc.ResourceKind,
			&inc.ResourceName, &inc.RuleName, &inc.FirstObservedAt, &inc.ActiveAt,
			&inc.LastObservedAt, &inc.ResolvedAt, &inc.SignalCount, &signalsJSON, &timelineJSON); err != nil {
			continue
		}
		inc.Phase = IncidentPhase(phase)
		_ = json.Unmarshal(signalsJSON, &inc.CorrelatedSignals)
		_ = json.Unmarshal(timelineJSON, &inc.Timeline)
		lm.active[inc.Fingerprint] = &inc
	}
}

// ─── PostgreSQL schema for incidents ──────────────────────────────────────────

// EnsureIncidentSchema creates the kubesense_incidents table if missing.
func EnsureIncidentSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS kubesense_incidents (
			id                 VARCHAR(64)   PRIMARY KEY,
			cluster_id         VARCHAR(128)  NOT NULL,
			fingerprint        VARCHAR(64)   NOT NULL,
			incident_type      VARCHAR(100)  NOT NULL,
			severity           VARCHAR(10)   NOT NULL,
			phase              VARCHAR(20)   NOT NULL DEFAULT 'Detecting',
			summary            TEXT,
			namespace          VARCHAR(255),
			resource_kind      VARCHAR(64),
			resource_name      VARCHAR(255),
			rule_name          VARCHAR(255),
			first_observed_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			active_at          TIMESTAMPTZ,
			last_observed_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			resolved_at        TIMESTAMPTZ,
			signal_count       INTEGER       NOT NULL DEFAULT 1,
			correlated_signals JSONB         NOT NULL DEFAULT '[]',
			timeline           JSONB         NOT NULL DEFAULT '[]',
			created_at         TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			updated_at         TIMESTAMPTZ   NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_ks_inc_cluster_phase ON kubesense_incidents(cluster_id, phase);
		CREATE INDEX IF NOT EXISTS idx_ks_inc_fingerprint   ON kubesense_incidents(fingerprint);
		CREATE INDEX IF NOT EXISTS idx_ks_inc_updated       ON kubesense_incidents(updated_at DESC);
	`)
	return err
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newIncidentID(fingerprint string) string {
	sum := sha1.Sum([]byte(fingerprint + fmt.Sprintf("%d", time.Now().UnixNano())))
	return "inc-" + hex.EncodeToString(sum[:8])
}

func appendTimeline(tl []TimelineEntry, entry TimelineEntry) []TimelineEntry {
	tl = append(tl, entry)
	if len(tl) > MaxTimelineEntries {
		tl = tl[len(tl)-MaxTimelineEntries:]
	}
	return tl
}
