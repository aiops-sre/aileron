package incidents

// evolution_engine.go
//
// IncidentEvolutionEngine runs a continuous 30-second background loop that
// evaluates open incidents for merge, split, severity escalation, and stale decay.
// Only one pod should run the loop at a time (distributed lock via Redis SETNX).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// EvolutionEventType describes the kind of evolution that occurred.
type EvolutionEventType string

const (
	EvolutionMerge       EvolutionEventType = "merge"
	EvolutionSplit       EvolutionEventType = "split"
	EvolutionEscalate    EvolutionEventType = "severity_escalate"
	EvolutionDeescalate  EvolutionEventType = "severity_deescalate"
	EvolutionReParent    EvolutionEventType = "rca_reparent"
	EvolutionAutoClose   EvolutionEventType = "auto_close"
	EvolutionBlastExpand EvolutionEventType = "blast_radius_expand"
	EvolutionRCAUpdate   EvolutionEventType = "rca_update"
)

// EvolutionEvent records a single evolution decision for audit and application.
type EvolutionEvent struct {
	Type           EvolutionEventType
	IncidentID     uuid.UUID
	TargetID       *uuid.UUID  // for merge: the surviving incident
	PreviousValue  interface{}
	NewValue       interface{}
	Confidence     float64
	Reasoning      string
	TriggeredAt    time.Time
	Version        int  // optimistic lock: version at read time; 0 = skip check
	TargetVersion  int  // version of TargetID incident for merge survivor CAS
}

// IncidentEvolutionEngine drives continuous incident lifecycle management.
type IncidentEvolutionEngine struct {
	db             *sql.DB
	rdb            *redis.Client
	svc            *IncidentService
	interval       time.Duration
	lockKey        string
	lockTTL        time.Duration
	mu             sync.Mutex
	recentMergesMu sync.Mutex
	recentMerges   map[uuid.UUID]time.Time // convergence guard: survivor ID merge time
}

func NewIncidentEvolutionEngine(db *sql.DB, rdb *redis.Client, svc *IncidentService) *IncidentEvolutionEngine {
	return &IncidentEvolutionEngine{
		db:           db,
		rdb:          rdb,
		svc:          svc,
		interval:     30 * time.Second,
		lockKey:      "lock:incident:evolution",
		lockTTL:      25 * time.Second,
		recentMerges: make(map[uuid.UUID]time.Time),
	}
}

// Run starts the continuous evolution loop. Call in a goroutine.
func (e *IncidentEvolutionEngine) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.acquireLock(ctx) {
				continue
			}
			if err := e.runCycle(ctx); err != nil {
				log.Printf("[EvolutionEngine] cycle error: %v", err)
			}
			e.releaseLock(ctx)
		}
	}
}

func (e *IncidentEvolutionEngine) runCycle(ctx context.Context) error {
	incidents, err := e.loadOpenIncidents(ctx)
	if err != nil {
		return err
	}
	if len(incidents) == 0 {
		return nil
	}

	var events []EvolutionEvent

	mergeEvents := e.evaluateMerges(ctx, incidents)
	events = append(events, mergeEvents...)

	if len(mergeEvents) > 0 {
		incidents, _ = e.loadOpenIncidents(ctx)
	}

	events = append(events, e.evaluateSplits(ctx, incidents)...)
	events = append(events, e.evaluateSeverityEscalation(ctx, incidents)...)
	events = append(events, e.evaluateStaleDecay(ctx, incidents)...)

	for _, ev := range events {
		if err := e.applyEvent(ctx, ev); err != nil {
			log.Printf("[EvolutionEngine] apply %s on %s: %v", ev.Type, ev.IncidentID, err)
		}
	}

	if len(events) > 0 {
		log.Printf("[EvolutionEngine] cycle: %d incidents %d evolution events", len(incidents), len(events))
	}
	return nil
}

// Merge Evaluation 

func (e *IncidentEvolutionEngine) evaluateMerges(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent
	merged := map[uuid.UUID]bool{}

	// Convergence guard: collect recently merged survivor IDs and prune stale entries.
	e.recentMergesMu.Lock()
	recentSurvivors := make(map[uuid.UUID]bool, len(e.recentMerges))
	for id, t := range e.recentMerges {
		if time.Since(t) < 5*time.Minute {
			recentSurvivors[id] = true
		} else {
			delete(e.recentMerges, id)
		}
	}
	e.recentMergesMu.Unlock()

	for i := 0; i < len(incidents); i++ {
		if merged[incidents[i].ID] || recentSurvivors[incidents[i].ID] {
			continue
		}
		for j := i + 1; j < len(incidents); j++ {
			if merged[incidents[j].ID] || recentSurvivors[incidents[j].ID] {
				continue
			}
			a, b := incidents[i], incidents[j]

			sameRoot := a.RootEntityID != "" && a.RootEntityID == b.RootEntityID
			overlapScore := topologyPathOverlap(a.TopologyPath, b.TopologyPath)
			timeDiff := absTimeDuration(a.CreatedAt.Sub(b.CreatedAt))
			bothConfident := a.RCAConfidence >= 0.65 && b.RCAConfidence >= 0.65

			if (sameRoot || overlapScore >= 0.70) && timeDiff < 2*time.Hour && bothConfident {
				survivorID, absorbedID := a.ID, b.ID
				if b.AlertCount > a.AlertCount {
					survivorID, absorbedID = b.ID, a.ID
				}

				mergeConf := (overlapScore + a.RCAConfidence + b.RCAConfidence) / 3
				if sameRoot {
					mergeConf = math.Min(1.0, mergeConf+0.10)
				}

				events = append(events, EvolutionEvent{
					Type:          EvolutionMerge,
					IncidentID:    absorbedID,
					TargetID:      &survivorID,
					Confidence:    mergeConf,
					Reasoning: fmt.Sprintf("merge: overlap=%.2f, same_root=%v, time_diff=%s",
						overlapScore, sameRoot, timeDiff.Round(time.Second)),
					TriggeredAt:   time.Now(),
					Version:       func() int { if absorbedID == a.ID { return a.Version }; return b.Version }(),
					TargetVersion: func() int { if survivorID == a.ID { return a.Version }; return b.Version }(),
				})
				merged[absorbedID] = true
			}
		}
	}
	return events
}

// Split Evaluation 

// splitGroupRecord describes one cluster group when evaluating an incident split.
type splitGroupRecord struct {
	Cluster string
	Alerts  []alertSummary
	Ratio   float64
}

func (e *IncidentEvolutionEngine) evaluateSplits(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent

	for _, inc := range incidents {
		if inc.AlertCount < 4 {
			continue
		}
		alerts, err := e.loadIncidentAlerts(ctx, inc.ID)
		if err != nil || len(alerts) < 4 {
			continue
		}

		clusterGroups := map[string][]alertSummary{}
		for _, a := range alerts {
			cluster := a.Cluster
			if cluster == "" {
				cluster = "unknown"
			}
			clusterGroups[cluster] = append(clusterGroups[cluster], a)
		}
		if len(clusterGroups) < 2 {
			continue
		}

		var groups []splitGroupRecord
		for cluster, as := range clusterGroups {
			groups = append(groups, splitGroupRecord{cluster, as, float64(len(as)) / float64(len(alerts))})
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].Ratio > groups[j].Ratio })

		if len(groups) >= 2 {
			g1, g2 := groups[0], groups[1]
			topoOverlap := topologyGroupOverlap(g1.Alerts, g2.Alerts)

			if g1.Ratio >= 0.30 && g2.Ratio >= 0.30 && topoOverlap < 0.10 {
				events = append(events, EvolutionEvent{
					Type:       EvolutionSplit,
					IncidentID: inc.ID,
					Confidence: 1.0 - topoOverlap,
					Reasoning: fmt.Sprintf("split: cluster %s (%.0f%%) vs %s (%.0f%%) topo_overlap=%.2f",
						g1.Cluster, g1.Ratio*100, g2.Cluster, g2.Ratio*100, topoOverlap),
					NewValue:    groups,
					TriggeredAt: time.Now(),
				})
			}
		}
	}
	return events
}

// Severity Escalation 

func (e *IncidentEvolutionEngine) evaluateSeverityEscalation(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent
	for _, inc := range incidents {
		newSeverity := computeEvolvingSeverity(inc)
		if severityRank(newSeverity) > severityRank(inc.Severity) {
			events = append(events, EvolutionEvent{
				Type:          EvolutionEscalate,
				IncidentID:    inc.ID,
				PreviousValue: inc.Severity,
				NewValue:      newSeverity,
				Confidence:    0.90,
				Reasoning: fmt.Sprintf("escalate %s%s: blast_radius=%d, alert_count=%d",
					inc.Severity, newSeverity, inc.BlastRadiusCount, inc.AlertCount),
				TriggeredAt: time.Now(),
				Version:     inc.Version,
			})
		}
	}
	return events
}

func computeEvolvingSeverity(inc incidentRecord) string {
	if inc.BlastRadiusCount >= 15 || inc.AlertCount >= 20 {
		return "critical"
	}
	if inc.BlastRadiusCount >= 8 || inc.AlertCount >= 10 {
		if inc.Severity == "medium" || inc.Severity == "low" {
			return "high"
		}
	}
	if inc.BlastRadiusCount >= 4 || inc.AlertCount >= 5 {
		if inc.Severity == "low" {
			return "medium"
		}
	}
	return inc.Severity
}

// Stale Decay 

func (e *IncidentEvolutionEngine) evaluateStaleDecay(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent
	now := time.Now()

	for _, inc := range incidents {
		sinceLastAlert := now.Sub(inc.LastAlertAt)
		if sinceLastAlert > 8*time.Hour && inc.UnresolvedAlerts == 0 {
			events = append(events, EvolutionEvent{
				Type:        EvolutionAutoClose,
				IncidentID:  inc.ID,
				Confidence:  0.85,
				Reasoning:   fmt.Sprintf("stale: no new alerts for %.1fh, all alerts resolved", sinceLastAlert.Hours()),
				TriggeredAt: now,
				Version:     inc.Version,
			})
		}
	}
	return events
}

// Apply Events 

func (e *IncidentEvolutionEngine) applyEvent(ctx context.Context, ev EvolutionEvent) error {
	switch ev.Type {
	case EvolutionMerge:
		return e.applyMerge(ctx, ev)
	case EvolutionSplit:
		return e.applySplit(ctx, ev)
	case EvolutionEscalate, EvolutionDeescalate:
		return e.applySeverityChange(ctx, ev)
	case EvolutionAutoClose:
		return e.applyAutoClose(ctx, ev)
	case EvolutionBlastExpand:
		return e.applyBlastExpansion(ctx, ev)
	case EvolutionRCAUpdate:
		return e.applyRCAUpdate(ctx, ev)
	}
	return nil
}

func (e *IncidentEvolutionEngine) applyMerge(ctx context.Context, ev EvolutionEvent) error {
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// CAS check: mark absorbed incident as merged only if version matches.
	res, err := tx.ExecContext(ctx,
		`UPDATE alerts SET incident_id = $1 WHERE incident_id = $2`,
		ev.TargetID, ev.IncidentID)
	if err != nil {
		return err
	}

	var survivorResult sql.Result
	survivorResult, err = tx.ExecContext(ctx, `
		UPDATE incidents
		SET alert_ids = (
			SELECT jsonb_agg(DISTINCT elem) FROM (
				SELECT jsonb_array_elements_text(alert_ids) AS elem FROM incidents WHERE id = $1
				UNION ALL
				SELECT jsonb_array_elements_text(alert_ids) AS elem FROM incidents WHERE id = $2
			) t
		),
		version = version + 1,
		updated_at = NOW()
		WHERE id = $1 AND ($3 = 0 OR version = $3)`, ev.TargetID, ev.IncidentID, ev.TargetVersion)
	if err != nil {
		return err
	}
	if n, _ := survivorResult.RowsAffected(); n == 0 {
		log.Printf("[EvolutionEngine] applyMerge: version conflict on survivor %s (expected %d), skipping", ev.TargetID, ev.TargetVersion)
		return nil
	}
	_ = res

	absorbedResult, err := tx.ExecContext(ctx,
		`UPDATE incidents SET status = 'merged', resolved_at = NOW(), updated_at = NOW(), version = version + 1
		 WHERE id = $1 AND ($2 = 0 OR version = $2)`,
		ev.IncidentID, ev.Version)
	if err != nil {
		return err
	}
	if n, _ := absorbedResult.RowsAffected(); n == 0 {
		log.Printf("[EvolutionEngine] applyMerge: version conflict on absorbed %s (expected %d), skipping", ev.IncidentID, ev.Version)
		return nil
	}

	metaJSON, _ := json.Marshal(map[string]interface{}{
		"absorbed_id": ev.IncidentID,
		"confidence":  ev.Confidence,
		"reasoning":   ev.Reasoning,
	})
	_, err = tx.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, metadata, created_at)
		VALUES ($1, $2, 'evolution_merge', 'Incident Merged', $3, $4, NOW())`,
		uuid.New(), ev.TargetID,
		fmt.Sprintf("Absorbed incident %s (confidence: %.2f)", ev.IncidentID, ev.Confidence),
		metaJSON)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Record the survivor in the convergence guard to prevent immediate re-merge.
	if ev.TargetID != nil {
		e.recentMergesMu.Lock()
		e.recentMerges[*ev.TargetID] = time.Now()
		e.recentMergesMu.Unlock()
	}
	return nil
}

func (e *IncidentEvolutionEngine) applySplit(ctx context.Context, ev EvolutionEvent) error {
	groups, ok := ev.NewValue.([]splitGroupRecord)
	if !ok || len(groups) < 2 {
		log.Printf("[EvolutionEngine] applySplit: invalid NewValue type for incident %s", ev.IncidentID)
		return nil
	}

	// Load source incident metadata to copy to the new incident.
	var title, severity, topologyPath string
	var alertIDsRaw []byte
	err := e.db.QueryRowContext(ctx,
		`SELECT title, severity, COALESCE(topology_path,''), COALESCE(alert_ids,'[]'::jsonb)
		 FROM incidents WHERE id = $1`,
		ev.IncidentID).Scan(&title, &severity, &topologyPath, &alertIDsRaw)
	if err != nil {
		return fmt.Errorf("applySplit: load source: %w", err)
	}

	// The second group (smaller) becomes a new incident.
	splitGroup := groups[1]
	splitAlertIDs := make([]string, len(splitGroup.Alerts))
	for i, a := range splitGroup.Alerts {
		splitAlertIDs[i] = a.ID.String()
	}
	splitAlertIDsJSON, _ := json.Marshal(splitAlertIDs)

	newIncidentID := uuid.New()
	newTitle := fmt.Sprintf("%s [split: %s]", title, splitGroup.Cluster)

	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Create the new incident for the split cluster group.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO incidents (id, title, severity, status, alert_ids, topology_path,
		                       auto_created, rca_status, started_at, created_at, updated_at)
		VALUES ($1, $2, $3, 'open', $4, $5, true, 'none', NOW(), NOW(), NOW())`,
		newIncidentID, newTitle, severity, splitAlertIDsJSON, topologyPath)
	if err != nil {
		return fmt.Errorf("applySplit: create new incident: %w", err)
	}

	// 2. Move split-group alerts to the new incident.
	for _, a := range splitGroup.Alerts {
		if _, err := tx.ExecContext(ctx,
			`UPDATE alerts SET incident_id = $1 WHERE id = $2 AND incident_id = $3`,
			newIncidentID, a.ID, ev.IncidentID); err != nil {
			return fmt.Errorf("applySplit: migrate alert %s: %w", a.ID, err)
		}
	}

	// 3. Remove split-group alert IDs from the source incident's alert_ids JSONB array.
	_, err = tx.ExecContext(ctx, `
		UPDATE incidents
		SET alert_ids = (
			SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
			FROM jsonb_array_elements_text(COALESCE(alert_ids, '[]'::jsonb)) AS elem
			WHERE NOT ($2::jsonb @> to_jsonb(elem))
		),
		updated_at = NOW()
		WHERE id = $1`,
		ev.IncidentID, splitAlertIDsJSON)
	if err != nil {
		return fmt.Errorf("applySplit: remove alert_ids from source: %w", err)
	}

	// 4. Timeline entry on source incident.
	splitMeta, _ := json.Marshal(map[string]interface{}{
		"new_incident_id": newIncidentID,
		"cluster":         splitGroup.Cluster,
		"alert_count":     len(splitGroup.Alerts),
		"confidence":      ev.Confidence,
		"reasoning":       ev.Reasoning,
	})
	_, err = tx.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, metadata, created_at)
		VALUES ($1, $2, 'evolution_split', 'Incident Split', $3, $4, NOW())`,
		uuid.New(), ev.IncidentID,
		fmt.Sprintf("Split %d alerts for cluster %s into new incident %s (confidence: %.2f)",
			len(splitGroup.Alerts), splitGroup.Cluster, newIncidentID, ev.Confidence),
		splitMeta)
	if err != nil {
		return err
	}

	// 5. Timeline entry on new incident.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, created_at)
		VALUES ($1, $2, 'evolution_split_created', 'Created from Incident Split', $3, NOW())`,
		uuid.New(), newIncidentID,
		fmt.Sprintf("Created by splitting cluster %s alerts from incident %s", splitGroup.Cluster, ev.IncidentID))
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Printf("[EvolutionEngine] split incident %s new %s (cluster: %s, %d alerts, confidence: %.2f)",
		ev.IncidentID, newIncidentID, splitGroup.Cluster, len(splitGroup.Alerts), ev.Confidence)
	return nil
}

func (e *IncidentEvolutionEngine) applySeverityChange(ctx context.Context, ev EvolutionEvent) error {
	res, err := e.db.ExecContext(ctx,
		`UPDATE incidents SET severity = $1, version = version + 1, updated_at = NOW()
		 WHERE id = $2 AND ($3 = 0 OR version = $3)`,
		ev.NewValue, ev.IncidentID, ev.Version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("[EvolutionEngine] applySeverityChange: version conflict on %s (expected %d), skipping", ev.IncidentID, ev.Version)
		return nil
	}
	_, err = e.db.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, created_at)
		VALUES ($1, $2, 'evolution_escalate', 'Severity Changed', $3, NOW())`,
		uuid.New(), ev.IncidentID,
		fmt.Sprintf("Severity %v %v: %s", ev.PreviousValue, ev.NewValue, ev.Reasoning))
	return err
}

func (e *IncidentEvolutionEngine) applyAutoClose(ctx context.Context, ev EvolutionEvent) error {
	res, err := e.db.ExecContext(ctx,
		`UPDATE incidents SET status = 'resolved', resolved_at = NOW(), version = version + 1, updated_at = NOW()
		 WHERE id = $1 AND ($2 = 0 OR version = $2)`,
		ev.IncidentID, ev.Version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("[EvolutionEngine] applyAutoClose: version conflict on %s (expected %d), skipping", ev.IncidentID, ev.Version)
		return nil
	}
	_, _ = e.db.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, created_at)
		VALUES ($1, $2, 'auto_resolved', 'Auto-Resolved (Stale)', $3, NOW())`,
		uuid.New(), ev.IncidentID, ev.Reasoning)
	return err
}

func (e *IncidentEvolutionEngine) applyBlastExpansion(ctx context.Context, ev EvolutionEvent) error {
	newBlast, ok := ev.NewValue.([]string)
	if !ok {
		return nil
	}
	data, _ := json.Marshal(newBlast)
	_, err := e.db.ExecContext(ctx,
		`UPDATE incidents SET blast_radius = $1, updated_at = NOW() WHERE id = $2`,
		data, ev.IncidentID)
	return err
}

func (e *IncidentEvolutionEngine) applyRCAUpdate(ctx context.Context, ev EvolutionEvent) error {
	vals, ok := ev.NewValue.(map[string]interface{})
	if !ok {
		return nil
	}
	davisJSON, _ := json.Marshal(vals)
	res, err := e.db.ExecContext(ctx, `
		UPDATE incidents
		SET davis_ai_analysis      = $1,
		    correlation_confidence = GREATEST(COALESCE(correlation_confidence, 0), $2),
		    version                = version + 1,
		    updated_at             = NOW()
		WHERE id = $3 AND ($4 = 0 OR version = $4)`,
		davisJSON, ev.Confidence, ev.IncidentID, ev.Version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("[EvolutionEngine] applyRCAUpdate: version conflict on %s (expected %d), skipping", ev.IncidentID, ev.Version)
	}
	return nil
}

// HandleRCAUpdate is called by CACIE (via the pipeline) to inform the evolution
// engine of a newly discovered root cause so the sameRoot merge check works.
func (e *IncidentEvolutionEngine) HandleRCAUpdate(ctx context.Context, incidentID uuid.UUID, rootEntityID, rootEntityLabel, domain string, confidence float64) {
	ev := EvolutionEvent{
		Type:       EvolutionRCAUpdate,
		IncidentID: incidentID,
		Confidence: confidence,
		NewValue: map[string]interface{}{
			"root_entity_id":    rootEntityID,
			"root_entity_label": rootEntityLabel,
			"domain":            domain,
		},
		TriggeredAt: time.Now(),
	}
	if err := e.applyRCAUpdate(ctx, ev); err != nil {
		log.Printf("[EvolutionEngine] HandleRCAUpdate for %s: %v", incidentID, err)
	}
}

// Distributed Lock 

func (e *IncidentEvolutionEngine) acquireLock(ctx context.Context) bool {
	if e.rdb == nil {
		return true
	}
	ok, err := e.rdb.SetNX(ctx, e.lockKey, "1", e.lockTTL).Result()
	return err == nil && ok
}

func (e *IncidentEvolutionEngine) releaseLock(ctx context.Context) {
	if e.rdb == nil {
		return
	}
	e.rdb.Del(ctx, e.lockKey)
}

// Data helpers 

type incidentRecord struct {
	ID               uuid.UUID
	Severity         string
	Status           string
	TopologyPath     string
	RootEntityID     string
	RCAConfidence    float64
	AlertCount       int
	BlastRadiusCount int
	UnresolvedAlerts int
	CreatedAt        time.Time
	LastAlertAt      time.Time
	Version          int
}

type alertSummary struct {
	ID           uuid.UUID
	Cluster      string
	TopologyPath string
	RootEntity   string
}

func (e *IncidentEvolutionEngine) loadOpenIncidents(ctx context.Context) ([]incidentRecord, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT i.id, i.severity, i.status, i.topology_path,
		       COALESCE(i.davis_ai_analysis->>'root_entity_id', '') AS root_entity_id,
		       COALESCE(i.correlation_confidence, 0) AS rca_confidence,
		       jsonb_array_length(COALESCE(i.alert_ids,'[]'::jsonb)) AS alert_count,
		       jsonb_array_length(
		           CASE WHEN jsonb_typeof(COALESCE(i.blast_radius,'[]'::jsonb)) = 'array'
		                THEN COALESCE(i.blast_radius,'[]'::jsonb)
		                ELSE '[]'::jsonb END
		       ) AS blast_radius_count,
		       (SELECT COUNT(*) FROM alerts a WHERE a.incident_id = i.id AND a.status != 'resolved') AS unresolved,
		       i.created_at,
		       COALESCE((SELECT MAX(a.created_at) FROM alerts a WHERE a.incident_id = i.id), i.created_at) AS last_alert_at,
		       COALESCE(i.version, 0)
		FROM incidents i
		WHERE i.status IN ('open','investigating','acknowledged')
		  AND i.created_at >= NOW() - INTERVAL '24 hours'
		ORDER BY i.created_at DESC
		LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []incidentRecord
	for rows.Next() {
		var r incidentRecord
		if err := rows.Scan(&r.ID, &r.Severity, &r.Status, &r.TopologyPath,
			&r.RootEntityID, &r.RCAConfidence, &r.AlertCount, &r.BlastRadiusCount,
			&r.UnresolvedAlerts, &r.CreatedAt, &r.LastAlertAt, &r.Version); err != nil {
			continue
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (e *IncidentEvolutionEngine) loadIncidentAlerts(ctx context.Context, incidentID uuid.UUID) ([]alertSummary, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT id, COALESCE(labels->>'k8s.cluster.name','') AS cluster,
		        COALESCE(topology_path,''), COALESCE(root_cause_entity,'')
		 FROM alerts WHERE incident_id = $1`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []alertSummary
	for rows.Next() {
		var a alertSummary
		rows.Scan(&a.ID, &a.Cluster, &a.TopologyPath, &a.RootEntity)
		result = append(result, a)
	}
	return result, rows.Err()
}

// Topology overlap helpers 

func topologyPathOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	partsA := splitTopoPath(a)
	partsB := splitTopoPath(b)
	setB := map[string]bool{}
	for _, p := range partsB {
		setB[p] = true
	}
	shared := 0
	for _, p := range partsA {
		if setB[p] {
			shared++
		}
	}
	total := len(partsA) + len(partsB) - shared
	if total == 0 {
		return 0
	}
	return float64(shared) / float64(total)
}

func topologyGroupOverlap(groupA, groupB []alertSummary) float64 {
	total := 0.0
	count := 0
	for _, a := range groupA {
		for _, b := range groupB {
			total += topologyPathOverlap(a.TopologyPath, b.TopologyPath)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func splitTopoPath(p string) []string {
	var parts []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

func absTimeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
