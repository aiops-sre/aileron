package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/apikeys"
	"github.com/aileron-platform/aileron/platform/internal/services/webhook_manager"
)

// EnterpriseAPIHandler handles enterprise API key and webhook subscription management.
// These endpoints are distinct from the inbound webhook_api_keys table used by
// Dynatrace/Prometheus/Grafana source authentication, which remains untouched.
type EnterpriseAPIHandler struct {
	db     *sql.DB
	keySvc *apikeys.Service
	whmgr  *webhook_manager.Manager
}

func NewEnterpriseAPIHandler(
	db *sql.DB,
	keySvc *apikeys.Service,
	whmgr *webhook_manager.Manager,
) *EnterpriseAPIHandler {
	return &EnterpriseAPIHandler{db: db, keySvc: keySvc, whmgr: whmgr}
}

// API Key endpoints 

// ListAPIKeys GET /api/v1/enterprise/api-keys
func (h *EnterpriseAPIHandler) ListAPIKeys(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	keys, err := h.keySvc.ListForUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to list keys"})
		return
	}
	if keys == nil {
		keys = []*apikeys.APIKey{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"keys": keys}})
}

// CreateAPIKey POST /api/v1/enterprise/api-keys
func (h *EnterpriseAPIHandler) CreateAPIKey(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}

	var body struct {
		Name        string     `json:"name" binding:"required"`
		Description string     `json:"description"`
		Scopes      []string   `json:"scopes"`
		TierName    string     `json:"tier_name"`
		ExpiresAt   *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "name is required"})
		return
	}
	if len(body.Scopes) == 0 {
		body.Scopes = []string{apikeys.ScopeAlertsRead}
	}

	result, err := h.keySvc.CreateAPIKey(
		c.Request.Context(), userID,
		body.Name, body.Description,
		body.Scopes, body.TierName, body.ExpiresAt,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": result})
}

// RevokeAPIKey DELETE /api/v1/enterprise/api-keys/:id
func (h *EnterpriseAPIHandler) RevokeAPIKey(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid key id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.Reason == "" {
		body.Reason = "user_revoked"
	}
	if err := h.keySvc.Revoke(c.Request.Context(), keyID, userID, body.Reason); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "key revoked"})
}

// RotateAPIKey POST /api/v1/enterprise/api-keys/:id/rotate
func (h *EnterpriseAPIHandler) RotateAPIKey(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid key id"})
		return
	}
	result, err := h.keySvc.Rotate(c.Request.Context(), keyID, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": result})
}

// GetAPIKeyUsage GET /api/v1/enterprise/api-keys/:id/usage
func (h *EnterpriseAPIHandler) GetAPIKeyUsage(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid key id"})
		return
	}
	stats, err := h.keySvc.GetUsageStats(c.Request.Context(), keyID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"stats": stats}})
}

// ListScopes GET /api/v1/enterprise/api-keys/scopes
func (h *EnterpriseAPIHandler) ListScopes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"scopes": apikeys.AllScopes},
	})
}

// Webhook subscription endpoints 

// ListWebhookSubscriptions GET /api/v1/enterprise/webhooks
func (h *EnterpriseAPIHandler) ListWebhookSubscriptions(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id, name, target_url,
		       COALESCE(array_to_string(event_types,'|'), '') AS event_types,
		       COALESCE(array_to_string(severity_filter,'|'), '') AS severity_filter,
		       COALESCE(array_to_string(source_filter,'|'), '') AS source_filter,
		       is_active, verify_ssl, total_deliveries, failed_deliveries,
		       last_delivery_at, last_success_at, last_failure_at,
		       consecutive_failures, paused_until, description, created_at
		FROM webhook_subscriptions
		WHERE user_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to list subscriptions"})
		return
	}
	defer rows.Close()

	var subs []gin.H
	for rows.Next() {
		var (
			id, name, targetURL, description            string
			eventTypesStr, sevFilterStr, srcFilterStr   string
			isActive, verifySSL                         bool
			totalDeliveries, failedDeliveries           int64
			consecutiveFailures                         int
			lastDelivery, lastSuccess, lastFailure, pausedUntil sql.NullTime
			createdAt                                   time.Time
		)
		if err := rows.Scan(
			&id, &name, &targetURL,
			&eventTypesStr, &sevFilterStr, &srcFilterStr,
			&isActive, &verifySSL,
			&totalDeliveries, &failedDeliveries,
			&lastDelivery, &lastSuccess, &lastFailure,
			&consecutiveFailures, &pausedUntil,
			&description, &createdAt,
		); err != nil {
			continue
		}
		sub := gin.H{
			"id": id, "name": name, "target_url": targetURL,
			"event_types":          splitPipe(eventTypesStr),
			"severity_filter":      splitPipe(sevFilterStr),
			"source_filter":        splitPipe(srcFilterStr),
			"is_active":            isActive,
			"verify_ssl":           verifySSL,
			"total_deliveries":     totalDeliveries,
			"failed_deliveries":    failedDeliveries,
			"consecutive_failures": consecutiveFailures,
			"description":          description,
			"created_at":           createdAt.Format(time.RFC3339),
		}
		if lastDelivery.Valid {
			sub["last_delivery_at"] = lastDelivery.Time.Format(time.RFC3339)
		}
		if lastSuccess.Valid {
			sub["last_success_at"] = lastSuccess.Time.Format(time.RFC3339)
		}
		if lastFailure.Valid {
			sub["last_failure_at"] = lastFailure.Time.Format(time.RFC3339)
		}
		if pausedUntil.Valid {
			sub["paused_until"] = pausedUntil.Time.Format(time.RFC3339)
		}
		subs = append(subs, sub)
	}
	if subs == nil {
		subs = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"subscriptions": subs}})
}

// CreateWebhookSubscription POST /api/v1/enterprise/webhooks
func (h *EnterpriseAPIHandler) CreateWebhookSubscription(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}

	var body struct {
		Name           string   `json:"name" binding:"required"`
		TargetURL      string   `json:"target_url" binding:"required"`
		EventTypes     []string `json:"event_types"`
		Description    string   `json:"description"`
		SeverityFilter []string `json:"severity_filter"`
		SourceFilter   []string `json:"source_filter"`
		VerifySSL      *bool    `json:"verify_ssl"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	verifySSL := true
	if body.VerifySSL != nil {
		verifySSL = *body.VerifySSL
	}
	if body.EventTypes == nil {
		body.EventTypes = []string{}
	}
	if body.SeverityFilter == nil {
		body.SeverityFilter = []string{}
	}
	if body.SourceFilter == nil {
		body.SourceFilter = []string{}
	}

	secret, secretHash, err := genWebhookSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to generate secret"})
		return
	}

	var subID uuid.UUID
	err = h.db.QueryRowContext(c.Request.Context(), `
		INSERT INTO webhook_subscriptions
		    (user_id, name, target_url, event_types, signing_secret,
		     severity_filter, source_filter, verify_ssl, description)
		VALUES ($1,$2,$3,$4::text[],$5,$6::text[],$7::text[],$8,$9)
		RETURNING id`,
		userID, body.Name, body.TargetURL,
		toPostgresArray(body.EventTypes), secretHash,
		toPostgresArray(body.SeverityFilter),
		toPostgresArray(body.SourceFilter),
		verifySSL, body.Description,
	).Scan(&subID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to create subscription"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":             subID,
			"name":           body.Name,
			"target_url":     body.TargetURL,
			"event_types":    body.EventTypes,
			"description":    body.Description,
			"verify_ssl":     verifySSL,
			"signing_secret": secret, // shown ONCE — store in secrets manager
			"created_at":     time.Now().Format(time.RFC3339),
		},
	})
}

// DeleteWebhookSubscription DELETE /api/v1/enterprise/webhooks/:id
func (h *EnterpriseAPIHandler) DeleteWebhookSubscription(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	subID := c.Param("id")
	res, err := h.db.ExecContext(c.Request.Context(),
		`DELETE FROM webhook_subscriptions WHERE id = $1 AND user_id = $2`, subID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to delete subscription"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "subscription not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "subscription deleted"})
}

// PauseWebhookSubscription POST /api/v1/enterprise/webhooks/:id/pause
func (h *EnterpriseAPIHandler) PauseWebhookSubscription(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	subID := c.Param("id")
	var body struct {
		Minutes int `json:"minutes"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.Minutes <= 0 {
		body.Minutes = 60
	}
	_, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE webhook_subscriptions
		SET paused_until = NOW() + ($1 * INTERVAL '1 minute')
		WHERE id = $2 AND user_id = $3`, body.Minutes, subID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to pause"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"message":      "subscription paused",
		"paused_until": time.Now().Add(time.Duration(body.Minutes) * time.Minute).Format(time.RFC3339),
	})
}

// ResumeWebhookSubscription POST /api/v1/enterprise/webhooks/:id/resume
func (h *EnterpriseAPIHandler) ResumeWebhookSubscription(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	subID := c.Param("id")
	_, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE webhook_subscriptions
		SET paused_until = NULL, consecutive_failures = 0
		WHERE id = $1 AND user_id = $2`, subID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to resume"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "subscription resumed"})
}

// GetWebhookDeliveries GET /api/v1/enterprise/webhooks/:id/deliveries
func (h *EnterpriseAPIHandler) GetWebhookDeliveries(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}
	subID := c.Param("id")

	var ownerID uuid.UUID
	err := h.db.QueryRowContext(c.Request.Context(),
		`SELECT user_id FROM webhook_subscriptions WHERE id = $1`, subID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "subscription not found"})
		return
	}
	if err != nil || ownerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "forbidden"})
		return
	}

	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id, event_type, status, attempt_count, max_attempts,
		       response_status, error_message, response_latency_ms,
		       delivered_at, created_at
		FROM webhook_deliveries
		WHERE subscription_id = $1
		ORDER BY created_at DESC
		LIMIT 50`, subID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to load deliveries"})
		return
	}
	defer rows.Close()

	var deliveries []gin.H
	for rows.Next() {
		var (
			id, eventType, status string
			errMsg                sql.NullString
			attemptCount, maxAttempts int
			respStatus, latencyMS sql.NullInt32
			deliveredAt, createdAt sql.NullTime
		)
		if err := rows.Scan(
			&id, &eventType, &status, &attemptCount, &maxAttempts,
			&respStatus, &errMsg, &latencyMS,
			&deliveredAt, &createdAt,
		); err != nil {
			continue
		}
		d := gin.H{
			"id": id, "event_type": eventType, "status": status,
			"attempt_count": attemptCount, "max_attempts": maxAttempts,
		}
		if respStatus.Valid {
			d["response_status"] = respStatus.Int32
		}
		if latencyMS.Valid {
			d["response_latency_ms"] = latencyMS.Int32
		}
		if errMsg.Valid && errMsg.String != "" {
			d["error_message"] = errMsg.String
		}
		if createdAt.Valid {
			d["created_at"] = createdAt.Time.Format(time.RFC3339)
		}
		if deliveredAt.Valid {
			d["delivered_at"] = deliveredAt.Time.Format(time.RFC3339)
		}
		deliveries = append(deliveries, d)
	}
	if deliveries == nil {
		deliveries = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deliveries": deliveries}})
}

// Event catalog 

// ListEventCatalog GET /api/v1/enterprise/events
func (h *EnterpriseAPIHandler) ListEventCatalog(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT event_type, category, display_name, description, since_version
		FROM event_catalog
		WHERE is_active = true AND deprecated = false
		ORDER BY category, event_type`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to load event catalog"})
		return
	}
	defer rows.Close()

	var events []gin.H
	for rows.Next() {
		var eventType, category, displayName, description, sinceVersion string
		if err := rows.Scan(&eventType, &category, &displayName, &description, &sinceVersion); err != nil {
			continue
		}
		events = append(events, gin.H{
			"event_type":    eventType,
			"category":      category,
			"display_name":  displayName,
			"description":   description,
			"since_version": sinceVersion,
		})
	}
	if events == nil {
		events = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"events": events}})
}

// Rate limit tiers 

// ListRateLimitTiers GET /api/v1/enterprise/rate-limits
func (h *EnterpriseAPIHandler) ListRateLimitTiers(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT name, display_name, requests_per_minute, requests_per_hour,
		       requests_per_day, burst_limit, ai_requests_per_min, webhook_ingress_per_min
		FROM rate_limit_tiers WHERE is_active = true ORDER BY requests_per_minute`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to load tiers"})
		return
	}
	defer rows.Close()

	var tiers []gin.H
	for rows.Next() {
		var name, displayName string
		var rpm, rph, rpd, burst, aiRpm, whRpm int
		if err := rows.Scan(&name, &displayName, &rpm, &rph, &rpd, &burst, &aiRpm, &whRpm); err != nil {
			continue
		}
		tiers = append(tiers, gin.H{
			"name": name, "display_name": displayName,
			"requests_per_minute": rpm, "requests_per_hour": rph,
			"requests_per_day": rpd, "burst_limit": burst,
			"ai_requests_per_min": aiRpm, "webhook_ingress_per_min": whRpm,
		})
	}
	if tiers == nil {
		tiers = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"tiers": tiers}})
}

// GetMyRateLimitStatus GET /api/v1/enterprise/rate-limits/me
func (h *EnterpriseAPIHandler) GetMyRateLimitStatus(c *gin.Context) {
	userID, ok := enterpriseCurrentUserID(c)
	if !ok {
		return
	}

	var tierName, displayName string
	var rpm, rph, rpd, burst int
	err := h.db.QueryRowContext(c.Request.Context(), `
		SELECT t.name, t.display_name, t.requests_per_minute,
		       t.requests_per_hour, t.requests_per_day, t.burst_limit
		FROM user_rate_limit_tiers u
		JOIN rate_limit_tiers t ON t.id = u.tier_id
		WHERE u.user_id = $1`, userID).
		Scan(&tierName, &displayName, &rpm, &rph, &rpd, &burst)
	if err == sql.ErrNoRows {
		tierName, displayName = "standard", "Standard"
		rpm, rph, rpd, burst = 300, 10000, 100000, 50
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to load tier"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tier_name":           tierName,
			"display_name":        displayName,
			"requests_per_minute": rpm,
			"requests_per_hour":   rph,
			"requests_per_day":    rpd,
			"burst_limit":         burst,
		},
	})
}

// helpers 

func enterpriseCurrentUserID(c *gin.Context) (uuid.UUID, bool) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "not authenticated"})
		c.Abort()
		return uuid.Nil, false
	}
	id, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "invalid user context"})
		c.Abort()
		return uuid.Nil, false
	}
	return id, true
}

func genWebhookSecret() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = "whsec_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	return plaintext, hash, nil
}

func toPostgresArray(ss []string) string {
	return "{" + strings.Join(ss, ",") + "}"
}

func splitPipe(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "|")
}
