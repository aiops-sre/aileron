// Package eventstore persists IntelligenceEvents to PostgreSQL tables so the
// KubeSense RCA engine can query them for evidence during investigation.
//
// The RCA engine's db_adapter queries:
//   - kubesense_health_events for health.* and change.* events
//
// Without this persister, those tables are always empty and the RCA engine
// produces zero hypotheses for every investigation.
package eventstore

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"time"

	"github.com/aileron-platform/aileron/agent/pkg/events"
)

// EventPersister writes IntelligenceEvents to the kubesense_health_events table.
// It implements ingestion.EventProcessor so it can be added to the collector pipeline.
type EventPersister struct {
	db *sql.DB
}

// NewEventPersister creates a persister backed by the given Postgres DB.
func NewEventPersister(db *sql.DB) *EventPersister {
	if err := EnsureSchema(context.Background(), db); err != nil {
		log.Printf("eventstore: schema init warning: %v", err)
	}
	return &EventPersister{db: db}
}

// Process persists a single IntelligenceEvent to the appropriate table.
// Health and storage events → kubesense_health_events.
// Change events → both kubesense_health_events (for event replay) AND
// kubesense_changes (for RCA engine causal correlation queries).
// Other event types are silently skipped (topology events go to Neo4j).
func (p *EventPersister) Process(ctx context.Context, ev *events.IntelligenceEvent) error {
	if p.db == nil {
		return nil
	}
	evType := string(ev.Type)

	if !shouldPersist(evType) {
		return nil
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO kubesense_health_events
		    (id, cluster_id, event_type, severity, resource_kind, namespace, resource_name,
		     resource_uid, occurred_at, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (id) DO NOTHING
	`,
		ev.ID,
		ev.ClusterID,
		evType,
		string(ev.Severity),
		ev.Resource.Kind,
		ev.Resource.Namespace,
		ev.Resource.Name,
		ev.Resource.UID,
		ev.Timestamp,
	)
	if err != nil && !strings.Contains(err.Error(), "duplicate") {
		log.Printf("eventstore: insert health_events %s/%s: %v", evType, ev.ID, err)
	}

	// Change events are also written to kubesense_changes so the RCA engine
	// can correlate changes with incidents via its causal scoring model.
	if strings.HasPrefix(evType, "change.") {
		actor := ""
		if ev.Annotations != nil {
			actor = ev.Annotations["actor"]
		}
		_, cerr := p.db.ExecContext(ctx, `
			INSERT INTO kubesense_changes
			    (id, cluster_id, change_type, resource_kind, namespace, resource_name, actor, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO NOTHING
		`,
			ev.ID,
			ev.ClusterID,
			evType,
			ev.Resource.Kind,
			ev.Resource.Namespace,
			ev.Resource.Name,
			actor,
			ev.Timestamp,
		)
		if cerr != nil && !strings.Contains(cerr.Error(), "duplicate") {
			log.Printf("eventstore: insert changes %s/%s: %v", evType, ev.ID, cerr)
		}
	}

	return nil
}

func shouldPersist(evType string) bool {
	return strings.HasPrefix(evType, "health.") ||
		strings.HasPrefix(evType, "change.") ||
		strings.HasPrefix(evType, "storage.") ||
		strings.HasPrefix(evType, "forecast.") ||
		strings.HasPrefix(evType, "apm.")
}

// EnsureSchema creates the kubesense_health_events and kubesense_changes tables.
// Idempotent — safe to run on every startup.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS kubesense_health_events (
			id            VARCHAR(64) PRIMARY KEY,
			cluster_id    VARCHAR(128) NOT NULL,
			event_type    VARCHAR(100) NOT NULL,
			severity      VARCHAR(20)  NOT NULL,
			resource_kind VARCHAR(64),
			namespace     VARCHAR(255),
			resource_name VARCHAR(255),
			resource_uid  VARCHAR(64),
			occurred_at   TIMESTAMP NOT NULL,
			received_at   TIMESTAMP DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_ks_he_cluster_type  ON kubesense_health_events(cluster_id, event_type);
		CREATE INDEX IF NOT EXISTS idx_ks_he_ns_name       ON kubesense_health_events(namespace, resource_name) WHERE namespace IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_ks_he_occurred_desc ON kubesense_health_events(occurred_at DESC);

		CREATE TABLE IF NOT EXISTS kubesense_changes (
			id            VARCHAR(64) PRIMARY KEY,
			cluster_id    VARCHAR(128) NOT NULL,
			change_type   VARCHAR(100) NOT NULL,
			resource_kind VARCHAR(64),
			namespace     VARCHAR(255),
			resource_name VARCHAR(255),
			actor         VARCHAR(255),
			occurred_at   TIMESTAMP NOT NULL,
			received_at   TIMESTAMP DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_ks_ch_cluster_time ON kubesense_changes(cluster_id, occurred_at DESC);
		CREATE INDEX IF NOT EXISTS idx_ks_ch_ns_resource  ON kubesense_changes(namespace, resource_name) WHERE namespace IS NOT NULL;
	`)
	if err != nil {
		return err
	}
	db.ExecContext(ctx, `DELETE FROM kubesense_health_events WHERE received_at < NOW() - INTERVAL '14 days'`)
	db.ExecContext(ctx, `DELETE FROM kubesense_changes WHERE received_at < NOW() - INTERVAL '30 days'`)
	return nil
}

// Prune removes events older than maxAge from kubesense_health_events.
// Call periodically (e.g. every 6 hours) to prevent unbounded growth.
func Prune(ctx context.Context, db *sql.DB, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	result, err := db.ExecContext(ctx, `DELETE FROM kubesense_health_events WHERE received_at < $1`, cutoff)
	if err != nil {
		log.Printf("eventstore: prune error: %v", err)
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		log.Printf("eventstore: pruned %d events older than %v", n, maxAge)
	}
}
