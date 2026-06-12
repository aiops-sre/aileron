// Package registry maintains the cluster registry in PostgreSQL.
// Tracks every cluster the collector has seen events from, with last heartbeat.
package registry

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/aileron-platform/aileron/agent/pkg/events"
)

// ClusterRecord is a row in the clusters table.
type ClusterRecord struct {
	ID            string
	LastHeartbeat time.Time
	LastAgentID   string
	AgentVersion  string
	NodeCount     int
	Status        string // active | stale | offline
}

// Store maintains the cluster registry.
type Store struct {
	db *sql.DB
}

// NewStore creates a registry store.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Process implements ingestion.EventProcessor.
// Updates the cluster's last_seen timestamp on every event.
func (s *Store) Process(ctx context.Context, ev *events.IntelligenceEvent) error {
	if ev.ClusterID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO kubesense_clusters
			(id, last_heartbeat, last_agent_id, agent_version, status, first_seen)
		VALUES ($1, NOW(), $2, $3, 'active', NOW())
		ON CONFLICT (id) DO UPDATE SET
			last_heartbeat = NOW(),
			last_agent_id  = EXCLUDED.last_agent_id,
			agent_version  = EXCLUDED.agent_version,
			status         = 'active'`,
		ev.ClusterID, ev.AgentID, ev.AgentVersion,
	)
	if err != nil {
		log.Printf("registry: upsert cluster=%s: %v", ev.ClusterID, err)
	}
	return nil
}

// EnsureSchema creates the clusters table if it doesn't exist.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS kubesense_clusters (
			id             TEXT        PRIMARY KEY,
			first_seen     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_heartbeat TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_agent_id  TEXT,
			agent_version  TEXT,
			node_count     INTEGER     NOT NULL DEFAULT 0,
			status         TEXT        NOT NULL DEFAULT 'active'
		)`)
	return err
}

// MarkStale sets clusters with no heartbeat in the last 5 minutes to stale.
func MarkStale(ctx context.Context, db *sql.DB) {
	_, _ = db.ExecContext(ctx, `
		UPDATE kubesense_clusters
		SET status = CASE
			WHEN last_heartbeat < NOW() - INTERVAL '15 minutes' THEN 'offline'
			WHEN last_heartbeat < NOW() - INTERVAL '5 minutes'  THEN 'stale'
			ELSE 'active'
		END
		WHERE status != 'offline' OR last_heartbeat > NOW() - INTERVAL '15 minutes'`)
}
