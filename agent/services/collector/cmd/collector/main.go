// kubesense-collector — central event ingestion from all cluster agents.
//
// Subscribes to all kubesense.events.* Kafka topics.
// One consumer group (kubesense-collector) across all replicas ensures
// each event is processed exactly once.
//
// Processors run in parallel per event:
//   1. Registry  — updates kubesense_clusters in PostgreSQL
//   2. Topology  — upserts/deletes nodes in Neo4j
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/aileron-platform/aileron/agent/services/collector/internal/eventstore"
	"github.com/aileron-platform/aileron/agent/services/collector/internal/ingestion"
	"github.com/aileron-platform/aileron/agent/services/collector/internal/registry"
	"github.com/aileron-platform/aileron/agent/services/collector/internal/topology"
)

func main() {
	kafkaBrokers := envOrDefault("KAFKA_BROKERS",
		"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092")
	dbURL     := envOrDefault("DATABASE_URL", "")
	neo4jURL  := envOrDefault("NEO4J_URL", "neo4j://kubesense-neo4j.aileron-agent.svc.cluster.local:7687")
	neo4jUser := envOrDefault("NEO4J_USER", "neo4j")
	neo4jPass := envOrDefault("NEO4J_PASSWORD", "")

	// Convert bolt/neo4j URL to HTTP for the REST API
	neo4jHTTP := boltToHTTP(neo4jURL)
	log.Printf("kubesense-collector starting: kafka=%s neo4j=%s", kafkaBrokers, neo4jHTTP)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── PostgreSQL ──────────────────────────────────────────────────────────
	var db *sql.DB
	var clusterRegistry *registry.Store
	if dbURL != "" {
		var openErr error
		db, openErr = sql.Open("postgres", dbURL)
		if openErr != nil {
			log.Printf("collector: postgres connect failed: %v — cluster registry disabled", openErr)
			db = nil
		} else {
			db.SetMaxOpenConns(10)
			db.SetConnMaxLifetime(5 * time.Minute)
			if err := registry.EnsureSchema(ctx, db); err != nil {
				log.Printf("collector: schema init failed: %v", err)
			} else {
				clusterRegistry = registry.NewStore(db)
				log.Printf("collector: PostgreSQL cluster registry ready")

				// Background stale-cluster sweeper
				go func() {
					tick := time.NewTicker(2 * time.Minute)
					defer tick.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-tick.C:
							registry.MarkStale(ctx, db)
						}
					}
				}()
			}
		}
	} else {
		log.Printf("collector: DATABASE_URL not set — cluster registry disabled")
	}

	// ── Neo4j via HTTP API (no Go driver dependency) ─────────────────────────
	var topoWriter *topology.Writer
	if neo4jPass != "" {
		w := topology.NewWriter(neo4jHTTP, neo4jUser, neo4jPass)
		if err := w.Verify(ctx); err != nil {
			log.Printf("collector: Neo4j unreachable: %v — topology writer disabled", err)
		} else {
			if err := topology.EnsureConstraints(ctx, w); err != nil {
				log.Printf("collector: Neo4j constraints: %v", err)
			}
			topoWriter = w
			log.Printf("collector: Neo4j topology writer ready")
		}
	} else {
		log.Printf("collector: NEO4J_PASSWORD not set — topology writer disabled")
	}

	// ── Build processor chain ────────────────────────────────────────────────
	var processors []ingestion.EventProcessor
	if clusterRegistry != nil {
		processors = append(processors, clusterRegistry)
	}
	if topoWriter != nil {
		processors = append(processors, topoWriter)
	}
	// EventPersister — writes health/change/storage events to kubesense_health_events
	// so the KubeSense RCA engine can query them during investigation.
	if db != nil {
		ep := eventstore.NewEventPersister(db)
		processors = append(processors, ep)
		log.Printf("collector: event persister registered (kubesense_health_events)")
		// Background pruner — keeps table under control (14-day retention)
		go func() {
			tick := time.NewTicker(6 * time.Hour)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					eventstore.Prune(ctx, db, 14*24*time.Hour)
				}
			}
		}()
	}
	if len(processors) == 0 {
		log.Printf("collector: WARNING — no processors active; events consumed but not stored")
	}

	// ── Kafka consumer ───────────────────────────────────────────────────────
	brokers := strings.Split(kafkaBrokers, ",")
	consumer := ingestion.NewConsumer(brokers, processors...)
	consumer.Start(ctx) // blocks until ctx cancelled

	log.Println("kubesense-collector: shutdown complete")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// boltToHTTP converts bolt:// or neo4j:// URL to HTTP for the REST API.
func boltToHTTP(u string) string {
	for _, prefix := range []string{"neo4j://", "bolt://"} {
		if len(u) > len(prefix) && u[:len(prefix)] == prefix {
			host := u[len(prefix):]
			// replace :7687 with :7474
			if idx := strings.LastIndex(host, ":"); idx >= 0 {
				host = host[:idx] + ":7474"
			}
			return "http://" + host
		}
	}
	return u
}
