package rca

// db_adapter.go — implements ChangeQuerier and EventQuerier against PostgreSQL.
// The kubesense-core PostgreSQL database holds kubesense_health_events
// (deduplicated health issues and change events from the collector).

import (
	"context"
	"database/sql"
	"time"
)

// DBAdapter implements both ChangeQuerier and EventQuerier.
type DBAdapter struct {
	db *sql.DB
}

func NewDBAdapter(db *sql.DB) *DBAdapter {
	return &DBAdapter{db: db}
}

// GetRecent returns change events from the last `from–until` window.
// Queries kubesense_health_events (written by the collector) for change.* event types.
// Falls back gracefully to nil when no data exists.
func (a *DBAdapter) GetRecent(ctx context.Context, clusterID string, from, until time.Time) ([]ChangeRecord, error) {
	if a.db == nil {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT event_type,
		       COALESCE(resource_kind, ''),
		       COALESCE(namespace, ''),
		       COALESCE(resource_name, ''),
		       'kubesense-agent',
		       occurred_at
		FROM kubesense_health_events
		WHERE cluster_id = $1
		  AND event_type LIKE 'change.%'
		  AND occurred_at BETWEEN $2 AND $3
		ORDER BY occurred_at DESC
		LIMIT 50
	`, clusterID, from, until)
	if err != nil {
		return nil, nil // fail open — RCA continues without change context
	}
	defer rows.Close()

	var records []ChangeRecord
	for rows.Next() {
		var evType, kind, ns, name, actor string
		var occurredAt time.Time
		if err := rows.Scan(&evType, &kind, &ns, &name, &actor, &occurredAt); err != nil {
			continue
		}
		records = append(records, ChangeRecord{
			ChangeType:   evType,
			ResourceKind: kind,
			Namespace:    ns,
			ResourceName: name,
			Actor:        actor,
			OccurredAt:   occurredAt,
		})
	}
	return records, nil
}

// GetK8sEvents returns recent health events from kubesense_health_events.
// These are the deduplicated pod/node health issues written by the collector.
func (a *DBAdapter) GetK8sEvents(ctx context.Context, clusterID, namespace string, since time.Time) ([]Evidence, error) {
	if a.db == nil {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT event_type, severity, resource_kind, resource_name, occurred_at
		FROM kubesense_health_events
		WHERE cluster_id = $1
		  AND ($2 = '' OR namespace = $2)
		  AND occurred_at >= $3
		ORDER BY occurred_at DESC
		LIMIT 30
	`, clusterID, namespace, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var evidence []Evidence
	for rows.Next() {
		var evType, sev, kind, name string
		var occurredAt time.Time
		if err := rows.Scan(&evType, &sev, &kind, &name, &occurredAt); err != nil {
			continue
		}
		evidence = append(evidence, Evidence{
			Type:        "k8s_event",
			Source:      "kubesense_health",
			Description: evType + " on " + kind + "/" + name,
			Strength:    severityToStrength(sev),
			OccurredAt:  occurredAt,
			ResourceRef: ResourceRef{
				Kind:      kind,
				Namespace: namespace,
				Name:      name,
			},
		})
	}
	return evidence, nil
}
