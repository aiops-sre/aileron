package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

// AnalyticsExtendedHandler adds correlation, Redis, and infrastructure-correlation
// analytics endpoints that require direct DB + Redis access.
type AnalyticsExtendedHandler struct {
	db          *sql.DB
	redisClient *goredis.Client // may be nil
}

// NewAnalyticsExtendedHandler creates the extended analytics handler.
func NewAnalyticsExtendedHandler(db *sql.DB, redisClient *goredis.Client) *AnalyticsExtendedHandler {
	return &AnalyticsExtendedHandler{db: db, redisClient: redisClient}
}

// GetCorrelations returns correlation strategy breakdown for the last 24 h.
// GET /api/v1/analytics/correlations
func (h *AnalyticsExtendedHandler) GetCorrelations(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	// Strategy totals
	strategyMap := map[string]int{
		"temporal": 0,
		"topology": 0,
		"semantic": 0,
		"rules":    0,
	}
	total := 0

	rows, err := h.db.QueryContext(ctx, `
		SELECT COALESCE(dominant_strategy, 'unknown'), COUNT(*)
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - INTERVAL '24 hours'
		GROUP BY dominant_strategy
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var strategy string
			var cnt int
			if rows.Scan(&strategy, &cnt) == nil {
				strategyMap[strategy] += cnt
				total += cnt
			}
		}
	}
	// If pipeline_correlation_results doesn't exist / is empty, fall through with zeros.

	// By-hour breakdown (last 24 h)
	byHour := []map[string]interface{}{}
	hourRows, hErr := h.db.QueryContext(ctx, `
		SELECT
			date_trunc('hour', processed_at) AS hr,
			COALESCE(dominant_strategy, 'unknown') AS strategy,
			COUNT(*) AS cnt
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - INTERVAL '24 hours'
		GROUP BY hr, strategy
		ORDER BY hr
	`)
	if hErr == nil {
		defer hourRows.Close()
		for hourRows.Next() {
			var hr time.Time
			var strategy string
			var cnt int
			if hourRows.Scan(&hr, &strategy, &cnt) == nil {
				byHour = append(byHour, map[string]interface{}{
					"hour":     hr.Format(time.RFC3339),
					"strategy": strategy,
					"count":    cnt,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data": gin.H{
			"temporal": strategyMap["temporal"],
			"topology": strategyMap["topology"],
			"semantic": strategyMap["semantic"],
			"rules":    strategyMap["rules"],
			"total":    total,
			"by_hour":  byHour,
		},
	})
}

// GetRedisStats returns Redis memory usage and key counts by prefix.
// GET /api/v1/analytics/redis
func (h *AnalyticsExtendedHandler) GetRedisStats(c *gin.Context) {
	if h.redisClient == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"ping_ms":       nil,
				"topology_keys": 0,
				"alert_keys":    0,
				"incident_keys": 0,
				"memory_used_mb": 0,
				"error":         "Redis not configured",
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Ping latency
	start := time.Now()
	pingErr := h.redisClient.Ping(ctx).Err()
	pingMs := time.Since(start).Milliseconds()
	if pingErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"ping_ms":       nil,
				"error":         pingErr.Error(),
			},
		})
		return
	}

	countKeys := func(pattern string) int {
		var cursor uint64
		total := 0
		for {
			keys, nextCursor, err := h.redisClient.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				break
			}
			total += len(keys)
			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
		return total
	}

	topologyKeys := countKeys("topology:*")
	alertKeys := countKeys("alert:*")
	incidentKeys := countKeys("incident:*")

	// Memory usage via INFO memory
	memoryUsedMB := float64(0)
	info, infoErr := h.redisClient.Info(ctx, "memory").Result()
	if infoErr == nil {
		// Parse "used_memory_human" or "used_memory" from INFO output
		for _, line := range splitLines(info) {
			if len(line) > 12 && line[:12] == "used_memory:" {
				var bytes int64
				if _, scanErr := scanInt(line[12:], &bytes); scanErr == nil {
					memoryUsedMB = float64(bytes) / (1024 * 1024)
				}
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"ping_ms":        pingMs,
			"topology_keys":  topologyKeys,
			"alert_keys":     alertKeys,
			"incident_keys":  incidentKeys,
			"memory_used_mb": memoryUsedMB,
		},
	})
}

// GetInfrastructureCorrelations returns auto-correlation breakdown by strategy for the last 7 days.
// GET /api/v1/analytics/infrastructure-correlations
func (h *AnalyticsExtendedHandler) GetInfrastructureCorrelations(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	byStrategy := map[string]int{}
	totalAuto := 0
	totalManual := 0

	// Count auto-correlated incidents by correlation method / dominant strategy
	rows, err := h.db.QueryContext(ctx, `
		SELECT
			COALESCE(correlation_method, COALESCE(dominant_strategy, 'unknown')) AS strategy,
			COUNT(*) AS cnt
		FROM incidents
		WHERE auto_created = true
		  AND created_at >= NOW() - INTERVAL '7 days'
		GROUP BY 1
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var strategy string
			var cnt int
			if rows.Scan(&strategy, &cnt) == nil {
				byStrategy[strategy] += cnt
				totalAuto += cnt
			}
		}
	}

	// Count manual incidents (auto_created = false or NULL)
	manualErr := h.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incidents
		WHERE (auto_created IS NULL OR auto_created = false)
		  AND created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&totalManual)
	if manualErr != nil {
		totalManual = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"by_strategy":          byStrategy,
			"total_auto_correlated": totalAuto,
			"total_manual":         totalManual,
		},
	})
}

// RegisterRoutes registers the extended analytics routes under the protected group.
func (h *AnalyticsExtendedHandler) RegisterRoutes(router *gin.RouterGroup) {
	analytics := router.Group("/analytics")
	{
		analytics.GET("/correlations", h.GetCorrelations)
		analytics.GET("/redis", h.GetRedisStats)
		analytics.GET("/infrastructure-correlations", h.GetInfrastructureCorrelations)
	}
}

// helpers 

func splitLines(s string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func scanInt(s string, out *int64) (int, error) {
	s = trimSpace(s)
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	var n int64
	found := false
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int64(ch-'0')
			found = true
		} else if found {
			break
		}
	}
	if !found {
		return 0, sql.ErrNoRows
	}
	if neg {
		n = -n
	}
	*out = n
	return 1, nil
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
