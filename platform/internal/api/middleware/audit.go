package middleware

import (
	"database/sql"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AuditLogger writes a structured row to api_request_log for every
// authenticated API request. It is designed to be lightweight: the DB
// write is fire-and-forget so it never blocks the response path.
type AuditLogger struct {
	db *sql.DB
}

func NewAuditLogger(db *sql.DB) *AuditLogger {
	return &AuditLogger{db: db}
}

// Middleware captures the response status + latency and asynchronously
// inserts a row into api_request_log. Unauthenticated requests are logged
// with NULL user_id / api_key_id.
func (al *AuditLogger) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		latency := int(time.Since(start).Milliseconds())
		status := c.Writer.Status()

		// Collect identity context set by auth middleware
		var userID *uuid.UUID
		var apiKeyID *uuid.UUID

		if uid, exists := c.Get("user_id"); exists {
			if id, ok := uid.(uuid.UUID); ok && id != uuid.Nil {
				userID = &id
			}
		}
		if kid, exists := c.Get("api_key_id"); exists {
			if id, ok := kid.(uuid.UUID); ok && id != uuid.Nil {
				apiKeyID = &id
			}
		}

		reqID, _ := c.Get("request_id")
		reqIDStr, _ := reqID.(string)

		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		method := c.Request.Method
		ip := c.ClientIP()
		ua := c.Request.UserAgent()
		errMsg := c.Errors.ByType(gin.ErrorTypeAny).String()

		go func() {
			_, err := al.db.Exec(`
				INSERT INTO api_request_log
				    (request_id, user_id, api_key_id, method, path, query,
				     status_code, latency_ms, ip_address, user_agent, error_msg, logged_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::inet,$10,$11,NOW())`,
				reqIDStr, userID, apiKeyID, method, path, query,
				status, latency, ip, ua, nullableStr(errMsg),
			)
			if err != nil {
				log.Printf("audit: insert failed: %v", err)
			}
		}()
	}
}

func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
