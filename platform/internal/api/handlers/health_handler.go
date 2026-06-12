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

// HealthHandler provides the detailed /health/detailed endpoint.
type HealthHandler struct {
	db          *sql.DB
	neo4jDriver neo4j.DriverWithContext // may be nil
	redisClient *goredis.Client         // may be nil
}

// NewHealthHandler creates a HealthHandler with all component references.
func NewHealthHandler(db *sql.DB, neo4jDriver neo4j.DriverWithContext, redisClient *goredis.Client) *HealthHandler {
	return &HealthHandler{
		db:          db,
		neo4jDriver: neo4jDriver,
		redisClient: redisClient,
	}
}

// ComponentHealth holds the health status of one component.
type ComponentHealth struct {
	Status  string `json:"status"` // "healthy" | "degraded" | "unhealthy" | "unknown"
	Latency int64  `json:"latency_ms"`
	Detail  string `json:"detail,omitempty"`
}

// DetailedHealth is the response payload for /health/detailed.
type DetailedHealth struct {
	Overall    string                     `json:"overall"`
	Components map[string]ComponentHealth `json:"components"`
	CheckedAt  time.Time                  `json:"checked_at"`
}

// GetDetailedHealth performs live checks against all platform components
// and returns a structured health summary.
// GET /health/detailed  (no auth required — health checks are public)
func (h *HealthHandler) GetDetailedHealth(c *gin.Context) {
	checkedAt := time.Now()
	components := map[string]ComponentHealth{}

	// PostgreSQL 
	{
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		start := time.Now()
		err := h.db.QueryRowContext(ctx, "SELECT 1").Err()
		cancel()
		latency := time.Since(start).Milliseconds()
		if err == nil {
			components["postgres"] = ComponentHealth{Status: "healthy", Latency: latency}
		} else {
			components["postgres"] = ComponentHealth{Status: "unhealthy", Latency: latency, Detail: err.Error()}
		}
	}

	// Neo4j 
	if h.neo4jDriver != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		start := time.Now()
		session := h.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
		_, err := session.Run(ctx, "RETURN 1", nil)
		session.Close(ctx)
		cancel()
		latency := time.Since(start).Milliseconds()
		if err == nil {
			components["neo4j"] = ComponentHealth{Status: "healthy", Latency: latency}
		} else {
			components["neo4j"] = ComponentHealth{Status: "unhealthy", Latency: latency, Detail: err.Error()}
		}
	} else {
		components["neo4j"] = ComponentHealth{Status: "unknown", Detail: "driver not initialised"}
	}

	// Redis 
	if h.redisClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		start := time.Now()
		err := h.redisClient.Ping(ctx).Err()
		cancel()
		latency := time.Since(start).Milliseconds()
		if err == nil {
			components["redis"] = ComponentHealth{Status: "healthy", Latency: latency}
		} else {
			components["redis"] = ComponentHealth{Status: "unhealthy", Latency: latency, Detail: err.Error()}
		}
	} else {
		components["redis"] = ComponentHealth{Status: "unknown", Detail: "Redis not configured"}
	}

	// Topology freshness 
	{
		topoStatus := "healthy"
		topoDetail := ""
		if h.redisClient != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			ts, err := h.redisClient.Get(ctx, "topology:last_refresh_ts").Result()
			cancel()
			if err == nil && ts != "" {
				if t, pErr := time.Parse(time.RFC3339, ts); pErr == nil {
					age := time.Since(t)
					switch {
					case age > 30*time.Minute:
						topoStatus = "unhealthy"
						topoDetail = "topology not refreshed in >30 minutes"
					case age > 15*time.Minute:
						topoStatus = "degraded"
						topoDetail = "topology not refreshed in >15 minutes"
					}
				}
			} else {
				topoStatus = "unknown"
				topoDetail = "topology:last_refresh_ts key not found"
			}
		} else {
			topoStatus = "unknown"
			topoDetail = "Redis not available to check topology age"
		}
		components["topology"] = ComponentHealth{Status: topoStatus, Detail: topoDetail}
	}

	// Kafka — mark as "unknown" (no broker client here) 
	components["kafka"] = ComponentHealth{Status: "unknown", Detail: "no direct Kafka client in health handler"}

	// Pipeline activity 
	{
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var recentCount int
		err := h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pipeline_correlation_results WHERE processed_at > NOW() - INTERVAL '5 minutes'`).
			Scan(&recentCount)
		cancel()
		if err != nil {
			components["pipeline"] = ComponentHealth{Status: "unknown", Detail: err.Error()}
		} else if recentCount == 0 {
			components["pipeline"] = ComponentHealth{Status: "degraded", Detail: "no alerts processed in last 5 minutes"}
		} else {
			components["pipeline"] = ComponentHealth{Status: "healthy", Detail: ""}
		}
	}

	// Overall status 
	overall := "healthy"
	for _, ch := range components {
		if ch.Status == "unhealthy" {
			overall = "unhealthy"
			break
		}
		if ch.Status == "degraded" && overall == "healthy" {
			overall = "degraded"
		}
	}

	c.JSON(http.StatusOK, DetailedHealth{
		Overall:    overall,
		Components: components,
		CheckedAt:  checkedAt,
	})
}
