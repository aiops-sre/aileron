// Package rca — Change Correlation RCA (BigPanda-style).
//
// ChangeCorrelator identifies the most likely causal deployment change for an
// incident by querying kubesense_changes within a lookback window and scoring
// each change using recency and overlap signals. The algorithm mirrors BigPanda's
// change correlation engine: recent, targeted changes score highest.
package rca

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	defaultChangeCorrelatorLookback = 2 * time.Hour
	changeConfidenceThreshold       = 0.3
	recencyHalfLifeMinutes          = 30.0 // exp decay half-life for recency score
)

// ─── ChangeCorrelator ─────────────────────────────────────────────────────────

// ChangeCorrelator queries kubesense_changes to find the most likely causal
// deployment or configuration change for a given incident.
type ChangeCorrelator struct {
	db             *sql.DB
	lookbackWindow time.Duration
}

// NewChangeCorrelator creates a ChangeCorrelator with the default 2-hour
// lookback window. Use WithLookback to override.
func NewChangeCorrelator(db *sql.DB) *ChangeCorrelator {
	return &ChangeCorrelator{
		db:             db,
		lookbackWindow: defaultChangeCorrelatorLookback,
	}
}

// WithLookback returns a copy of the correlator with the given lookback window.
func (c *ChangeCorrelator) WithLookback(d time.Duration) *ChangeCorrelator {
	return &ChangeCorrelator{db: c.db, lookbackWindow: d}
}

// ─── ChangeCandidate ──────────────────────────────────────────────────────────

// ChangeCandidate is a change record that may have caused an incident.
type ChangeCandidate struct {
	ChangeType   string
	ResourceKind string
	Namespace    string
	ResourceName string
	Actor        string
	OccurredAt   time.Time
	Confidence   float64
	CommitHash   string
}

// ─── FindCausalChange ─────────────────────────────────────────────────────────

// FindCausalChange queries kubesense_changes for changes that precede
// incidentStart within the lookback window, then scores them by overlap and
// recency. Returns the highest-confidence candidate, or nil if none clears
// the 0.3 confidence threshold.
//
// Score = overlapScore × recencyScore
//
//	recencyScore = exp(-dt_minutes / 30.0)
//	                where dt = incidentStart - change.occurred_at
//	overlapScore = 1.0  exact resource match (same namespace + name)
//	             = 0.7  same namespace only
//	             = 0.4  same cluster only
func (c *ChangeCorrelator) FindCausalChange(
	ctx context.Context,
	clusterID string,
	entityIDs []string,
	incidentStart time.Time,
) *ChangeCandidate {
	if c.db == nil {
		return nil
	}

	windowStart := incidentStart.Add(-c.lookbackWindow)

	rows, err := c.db.QueryContext(ctx, `
		SELECT change_type, resource_kind, namespace, resource_name,
		       actor, occurred_at, COALESCE(commit_hash, '')
		FROM kubesense_changes
		WHERE cluster_id = $1
		  AND occurred_at BETWEEN $2 AND $3
		ORDER BY occurred_at DESC
		LIMIT 200
	`, clusterID, windowStart, incidentStart)
	if err != nil {
		return nil
	}
	defer rows.Close()

	// Parse entity IDs into namespace/name lookup sets for fast matching.
	namespaceSet, exactSet := buildEntitySets(entityIDs)

	var best *ChangeCandidate
	bestScore := -1.0

	for rows.Next() {
		var (
			changeType   string
			resourceKind string
			namespace    string
			resourceName string
			actor        string
			occurredAt   time.Time
			commitHash   string
		)
		if err := rows.Scan(&changeType, &resourceKind, &namespace, &resourceName,
			&actor, &occurredAt, &commitHash); err != nil {
			continue
		}

		// Compute recency score: exponential decay over dt minutes.
		dt := incidentStart.Sub(occurredAt).Minutes()
		if dt < 0 {
			continue // change happened after incident start — skip
		}
		recencyScore := math.Exp(-dt / recencyHalfLifeMinutes)

		// Compute overlap score based on how precisely the change matches.
		overlap := computeOverlap(namespace, resourceName, namespaceSet, exactSet)

		score := overlap * recencyScore
		if score > bestScore {
			bestScore = score
			best = &ChangeCandidate{
				ChangeType:   changeType,
				ResourceKind: resourceKind,
				Namespace:    namespace,
				ResourceName: resourceName,
				Actor:        actor,
				OccurredAt:   occurredAt,
				Confidence:   score,
				CommitHash:   commitHash,
			}
		}
	}

	if best == nil || best.Confidence < changeConfidenceThreshold {
		return nil
	}
	return best
}

// ─── EnrichIncidentWithRCA ────────────────────────────────────────────────────

// EnrichIncidentWithRCA finds the causal change for a group and, if one is
// found with sufficient confidence, annotates the group with change-correlation
// metadata. Returns the (possibly enriched) group pointer.
func (c *ChangeCorrelator) EnrichIncidentWithRCA(
	ctx context.Context,
	clusterID string,
	group *TopoIncidentGroup,
) *TopoIncidentGroup {
	if group == nil {
		return nil
	}

	// Collect entity IDs from group members for overlap matching.
	entityIDs := make([]string, 0, len(group.Members))
	for _, m := range group.Members {
		entityIDs = append(entityIDs, m.EntityID)
		// Also add namespace/kind/name form for broader matching.
		if m.Namespace != "" && m.Name != "" {
			entityIDs = append(entityIDs, fmt.Sprintf("%s/%s/%s", m.EntityKind, m.Namespace, m.Name))
		}
	}

	incidentStart := group.EarliestEventTime
	if incidentStart.IsZero() {
		incidentStart = findEarliestTime(group.Members)
	}
	if incidentStart.IsZero() {
		incidentStart = time.Now()
	}

	candidate := c.FindCausalChange(ctx, clusterID, entityIDs, incidentStart)
	if candidate == nil {
		return group
	}

	group.CorrelationMethod = "change_correlation"
	group.ChangeInfo = candidate
	return group
}

// ─── Scoring helpers ──────────────────────────────────────────────────────────

// computeOverlap returns the overlap score between a change record and the
// set of entities in the incident.
//
//   - 1.0: exact match — the change namespace+name appears in exactSet
//   - 0.7: namespace match — the change namespace appears in namespaceSet
//   - 0.4: cluster match — any change in the cluster (fallback)
func computeOverlap(namespace, resourceName string, namespaceSet, exactSet map[string]bool) float64 {
	key := namespace + "/" + resourceName
	if exactSet[key] {
		return 1.0
	}
	if namespaceSet[namespace] {
		return 0.7
	}
	return 0.4
}

// buildEntitySets parses a slice of entityIDs (in "kind/namespace/name" or
// "namespace/name" or bare "name" forms) into:
//   - namespaceSet: set of namespaces present in the incident
//   - exactSet: set of "namespace/name" keys present in the incident
func buildEntitySets(entityIDs []string) (namespaceSet, exactSet map[string]bool) {
	namespaceSet = make(map[string]bool)
	exactSet = make(map[string]bool)

	for _, id := range entityIDs {
		parts := strings.Split(id, "/")
		switch len(parts) {
		case 3:
			// "kind/namespace/name"
			ns, name := parts[1], parts[2]
			if ns != "" {
				namespaceSet[ns] = true
				if name != "" {
					exactSet[ns+"/"+name] = true
				}
			}
		case 2:
			// "namespace/name"
			ns, name := parts[0], parts[1]
			if ns != "" {
				namespaceSet[ns] = true
				if name != "" {
					exactSet[ns+"/"+name] = true
				}
			}
		}
	}
	return namespaceSet, exactSet
}
