package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// MonitoringHandler provides real operational metrics for the monitoring dashboard.
type MonitoringHandler struct {
	db          *sql.DB
	neo4jDriver neo4j.DriverWithContext // may be nil if Neo4j is unavailable
	redisClient *goredis.Client         // may be nil if Redis is unavailable
}

// NewMonitoringHandler creates a new MonitoringHandler.
func NewMonitoringHandler(db *sql.DB, neo4jDriver neo4j.DriverWithContext, redisClient *goredis.Client) *MonitoringHandler {
	return &MonitoringHandler{
		db:          db,
		neo4jDriver: neo4jDriver,
		redisClient: redisClient,
	}
}

// GetMetrics returns real-time operational metrics for all major subsystems.
// GET /api/v1/monitoring/metrics
func (h *MonitoringHandler) GetMetrics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result := gin.H{}
	componentHealth := gin.H{}

	// Alert ingestion counts 
	var alertsLast1h, alertsLast24h int
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - INTERVAL '1 hour'`).Scan(&alertsLast1h)
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - INTERVAL '24 hours'`).Scan(&alertsLast24h)
	result["alerts_last_1h"] = alertsLast1h
	result["alerts_last_24h"] = alertsLast24h

	// Active incidents 
	var activeIncidents int
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status NOT IN ('resolved','closed')`).Scan(&activeIncidents)
	result["active_incidents"] = activeIncidents

	// Correlation success rate (last 24h) 
	var correlated, totalRecent int
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE created_at >= NOW() - INTERVAL '24 hours'`).Scan(&totalRecent)
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE incident_id IS NOT NULL AND created_at >= NOW() - INTERVAL '24 hours'`).Scan(&correlated)
	correlationRate := 0.0
	if totalRecent > 0 {
		correlationRate = float64(correlated) / float64(totalRecent)
	}
	result["correlation_rate_24h"] = correlationRate

	// Pipeline latency proxy (avg ms between alert created_at and incident updated_at) 
	var avgLatencyMs float64
	err := h.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (i.updated_at - a.created_at)) * 1000), 0)
		FROM alerts a
		JOIN incidents i ON i.id = a.incident_id
		WHERE a.created_at >= NOW() - INTERVAL '24 hours'
		  AND i.updated_at >= a.created_at
	`).Scan(&avgLatencyMs)
	if err != nil {
		avgLatencyMs = 0
	}
	result["pipeline_latency_avg_ms"] = int64(avgLatencyMs)

	// Neo4j node count 
	neo4jNodeCount := 0
	if h.neo4jDriver != nil {
		neoCtx, neoCancel := context.WithTimeout(ctx, 3*time.Second)
		defer neoCancel()
		session := h.neo4jDriver.NewSession(neoCtx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
		res, nErr := session.Run(neoCtx, "MATCH (n) RETURN count(n) AS cnt", nil)
		if nErr == nil && res.Next(neoCtx) {
			if v, ok := res.Record().Get("cnt"); ok && v != nil {
				switch n := v.(type) {
				case int64:
					neo4jNodeCount = int(n)
				}
			}
		}
		session.Close(neoCtx)
		if nErr == nil {
			componentHealth["neo4j"] = "healthy"
		} else {
			componentHealth["neo4j"] = "unhealthy"
		}
	} else {
		componentHealth["neo4j"] = "unavailable"
	}
	result["neo4j_node_count"] = neo4jNodeCount

	// Redis ping latency 
	redisPingMs := int64(0)
	if h.redisClient != nil {
		redisCtx, redisCancel := context.WithTimeout(ctx, 2*time.Second)
		defer redisCancel()
		start := time.Now()
		pingErr := h.redisClient.Ping(redisCtx).Err()
		redisPingMs = time.Since(start).Milliseconds()
		if pingErr == nil {
			componentHealth["redis"] = "healthy"
		} else {
			componentHealth["redis"] = "unhealthy"
		}
	} else {
		componentHealth["redis"] = "unavailable"
	}
	result["redis_ping_ms"] = redisPingMs

	// Kafka consumer lag — skip with null (no direct client available) 
	result["kafka_consumer_lag"] = nil

	// Topology last sync from Redis 
	topologyLastSync := ""
	if h.redisClient != nil {
		ts, tsErr := h.redisClient.Get(ctx, "topology:last_refresh_ts").Result()
		if tsErr == nil {
			topologyLastSync = ts
		}
	}
	result["topology_last_sync"] = topologyLastSync

	// Postgres health 
	dbCtx, dbCancel := context.WithTimeout(ctx, 2*time.Second)
	defer dbCancel()
	if err := h.db.PingContext(dbCtx); err == nil {
		componentHealth["postgres"] = "healthy"
	} else {
		componentHealth["postgres"] = "unhealthy"
	}

	// Kafka health (mark unknown — no direct broker client in this handler) 
	componentHealth["kafka"] = "unknown"

	result["component_health"] = componentHealth

	c.JSON(http.StatusOK, result)
}
