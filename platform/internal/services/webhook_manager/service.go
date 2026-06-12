package webhook_manager

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// EventPayload is the envelope sent to every outbound webhook subscriber.
type EventPayload struct {
	ID          string          `json:"id"`           // unique delivery ID
	EventType   string          `json:"event_type"`
	Version     string          `json:"version"`      // "1.0"
	Source      string          `json:"source"`       // "alerthub"
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data"`
}

// Subscription mirrors the DB row we need for delivery.
type Subscription struct {
	ID            uuid.UUID
	TargetURL     string
	SigningSecret string // hex(SHA256(plaintext_secret)) stored in DB
	EventTypes    []string
	VerifySSL     bool
}

// Manager handles the full outbound webhook delivery lifecycle.
type Manager struct {
	db     *sql.DB
	client *http.Client
}

func NewManager(db *sql.DB) *Manager {
	return &Manager{
		db: db,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Dispatch enqueues a delivery for all active subscribers of the given event type.
// It is non-blocking — inserts pending rows, then returns. The retry worker delivers them.
func (m *Manager) Dispatch(ctx context.Context, eventType string, eventID uuid.UUID, data interface{}) {
	payload, err := buildPayload(eventType, eventID, data)
	if err != nil {
		log.Printf("webhook_manager: failed to build payload for %s: %v", eventType, err)
		return
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT id, target_url, signing_secret
		FROM webhook_subscriptions
		WHERE is_active = true
		  AND paused_until IS NULL OR paused_until < NOW()
		  AND (event_types = '{}' OR $1 = ANY(event_types))`,
		eventType)
	if err != nil {
		log.Printf("webhook_manager: query subscriptions: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.TargetURL, &sub.SigningSecret); err != nil {
			continue
		}
		idemKey := fmt.Sprintf("%s:%s:%s", eventType, eventID, sub.ID)
		_, _ = m.db.ExecContext(ctx, `
			INSERT INTO webhook_deliveries
			    (id, subscription_id, event_type, event_id, idempotency_key,
			     payload, status, next_attempt_at)
			VALUES ($1,$2,$3,$4,$5,$6,'pending', NOW())
			ON CONFLICT (idempotency_key) DO NOTHING`,
			uuid.New(), sub.ID, eventType, eventID, idemKey, payload)
	}
}

// RunRetryWorker processes pending deliveries in a loop. Call in a goroutine.
func (m *Manager) RunRetryWorker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.processBatch(ctx)
		}
	}
}

func (m *Manager) processBatch(ctx context.Context) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT d.id, d.subscription_id, d.event_type, d.payload,
		       d.attempt_count, d.max_attempts,
		       s.target_url, s.signing_secret, s.verify_ssl
		FROM webhook_deliveries d
		JOIN webhook_subscriptions s ON s.id = d.subscription_id
		WHERE d.status = 'pending'
		  AND d.next_attempt_at <= NOW()
		  AND d.attempt_count < d.max_attempts
		ORDER BY d.next_attempt_at
		LIMIT 50
		FOR UPDATE OF d SKIP LOCKED`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			deliveryID, subID    uuid.UUID
			eventType            string
			payload              []byte
			attempts, maxAttempts int
			targetURL, sigSecret string
			verifySSL            bool
		)
		if err := rows.Scan(&deliveryID, &subID, &eventType, &payload,
			&attempts, &maxAttempts, &targetURL, &sigSecret, &verifySSL); err != nil {
			continue
		}

		// Mark delivering to prevent concurrent workers picking the same row
		_, _ = m.db.ExecContext(ctx,
			`UPDATE webhook_deliveries SET status='delivering', last_attempt_at=NOW(),
			 attempt_count=attempt_count+1 WHERE id=$1`, deliveryID)

		go m.attempt(ctx, deliveryID, subID, targetURL, sigSecret, payload, attempts+1, maxAttempts)
	}
}

func (m *Manager) attempt(
	ctx context.Context,
	deliveryID, subID uuid.UUID,
	targetURL, sigSecret string,
	payload []byte,
	attempt, maxAttempts int,
) {
	sig := signPayload(payload, sigSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		m.markFailed(ctx, deliveryID, subID, attempt, maxAttempts, 0, "", err.Error(), sig)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AlertHub-Signature", "sha256="+sig)
	req.Header.Set("X-AlertHub-Delivery", deliveryID.String())
	req.Header.Set("X-AlertHub-Attempt", fmt.Sprintf("%d", attempt))
	req.Header.Set("User-Agent", "AlertHub-Webhook/1.0")

	t0 := time.Now()
	resp, err := m.client.Do(req)
	latencyMS := int(time.Since(t0).Milliseconds())

	if err != nil {
		m.markFailed(ctx, deliveryID, subID, attempt, maxAttempts, 0, "", err.Error(), sig)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		m.markDelivered(ctx, deliveryID, subID, resp.StatusCode, latencyMS)
		return
	}

	m.markFailed(ctx, deliveryID, subID, attempt, maxAttempts, resp.StatusCode, "", fmt.Sprintf("HTTP %d", resp.StatusCode), sig)
}

func (m *Manager) markDelivered(ctx context.Context, id, subID uuid.UUID, statusCode, latency int) {
	_, _ = m.db.ExecContext(ctx, `
		UPDATE webhook_deliveries
		SET status='delivered', response_status=$2, response_latency_ms=$3,
		    delivered_at=NOW()
		WHERE id=$1`, id, statusCode, latency)

	_, _ = m.db.ExecContext(ctx, `
		UPDATE webhook_subscriptions
		SET last_delivery_at=NOW(), last_success_at=NOW(),
		    last_response_code=$2, total_deliveries=total_deliveries+1,
		    consecutive_failures=0
		WHERE id=$1`, subID, statusCode)
}

func (m *Manager) markFailed(
	ctx context.Context, id, subID uuid.UUID,
	attempt, maxAttempts, statusCode int,
	body, errMsg, sig string,
) {
	var nextStatus string
	var nextAttemptAt *time.Time
	if attempt >= maxAttempts {
		nextStatus = "dead_lettered"
	} else {
		nextStatus = "pending"
		// Exponential backoff: 5s, 30s, 5min
		delays := []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}
		idx := attempt - 1
		if idx >= len(delays) {
			idx = len(delays) - 1
		}
		t := time.Now().Add(delays[idx])
		nextAttemptAt = &t
	}

	_, _ = m.db.ExecContext(ctx, `
		UPDATE webhook_deliveries
		SET status=$2, response_status=$3, response_body=$4,
		    error_message=$5, signature_header=$6,
		    next_attempt_at=COALESCE($7, next_attempt_at)
		WHERE id=$1`, id, nextStatus, statusCode, body, errMsg, "sha256="+sig, nextAttemptAt)

	_, _ = m.db.ExecContext(ctx, `
		UPDATE webhook_subscriptions
		SET last_delivery_at=NOW(), last_failure_at=NOW(),
		    last_response_code=$2, failed_deliveries=failed_deliveries+1,
		    consecutive_failures=consecutive_failures+1,
		    -- Circuit-open: pause for 30min after 10 consecutive failures
		    paused_until = CASE
		        WHEN consecutive_failures >= 9 THEN NOW() + INTERVAL '30 minutes'
		        ELSE paused_until END
		WHERE id=$1`, subID, statusCode)

	if nextStatus == "dead_lettered" {
		log.Printf("webhook_manager: delivery %s dead-lettered after %d attempts (sub=%s)", id, attempt, subID)
	}
}

// ---- helpers ----------------------------------------------------------------

func buildPayload(eventType string, eventID uuid.UUID, data interface{}) ([]byte, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	env := EventPayload{
		ID:         uuid.New().String(),
		EventType:  eventType,
		Version:    "1.0",
		Source:     "alerthub",
		OccurredAt: time.Now().UTC(),
		Data:       dataBytes,
	}
	return json.Marshal(env)
}

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
