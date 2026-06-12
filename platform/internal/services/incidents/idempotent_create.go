package incidents

// idempotent_create.go
//
// Distributed idempotency guard for incident creation.
// Prevents race conditions when multiple goroutines process correlated alerts
// simultaneously and all attempt to create the same incident.
//
// Uses Redis SETNX: the first caller acquires the token and creates the incident.
// Subsequent callers receive the incident ID created by the winner.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	idempotencyKeyPrefix = "idem:incident:"
	idempotencyTTL       = 10 * time.Minute
)

// IdempotentCreateResult tells the caller what happened on an acquisition attempt.
type IdempotentCreateResult struct {
	IncidentID uuid.UUID
	Created    bool   // false = an existing incident was returned from Redis
	Token      string
}

// BuildIdempotencyToken computes a stable token for an incident creation request.
// Same alertEntityID + cluster + domain + severity within a 5-minute window
// produces the same token, preventing duplicate incidents from concurrent alerts.
func BuildIdempotencyToken(alertEntityID, cluster, domain, severity string, t time.Time) string {
	window := t.Truncate(5 * time.Minute)
	raw := fmt.Sprintf("%s|%s|%s|%s|%d", alertEntityID, cluster, domain, severity, window.Unix())
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:16]) // 32-char hex = 128 bits
}

// AcquireIdempotencyToken tries to claim the token for incident creation.
//
// Returns (true, nil, nil) if the token was claimed — caller should create the incident.
// Returns (false, &existingID, nil) if another caller already created it.
// Returns (false, nil, nil) if the token state is ambiguous — caller may proceed.
func AcquireIdempotencyToken(ctx context.Context, rdb *redis.Client, token string) (bool, *uuid.UUID, error) {
	key := idempotencyKeyPrefix + token

	ok, err := rdb.SetNX(ctx, key, "pending", idempotencyTTL).Result()
	if err != nil {
		return false, nil, fmt.Errorf("idempotency check: %w", err)
	}

	if ok {
		// Token claimed — caller should proceed with creation
		return true, nil, nil
	}

	// Key exists — retrieve the stored incident ID
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		// Key expired between SetNX and Get — allow creation
		return false, nil, nil
	}

	if val == "pending" {
		// Another goroutine is mid-creation — wait briefly and retry once
		time.Sleep(100 * time.Millisecond)
		val, err = rdb.Get(ctx, key).Result()
		if err != nil || val == "pending" {
			// Still pending — proceed anyway (11-point dedup cascade is the real guard)
			return false, nil, nil
		}
	}

	id, err := uuid.Parse(val)
	if err != nil {
		return false, nil, nil
	}
	return false, &id, nil
}

// ConfirmIdempotencyToken records the created incident ID against the token.
// Call this immediately after successful incident creation.
func ConfirmIdempotencyToken(ctx context.Context, rdb *redis.Client, token string, incidentID uuid.UUID) {
	key := idempotencyKeyPrefix + token
	rdb.Set(ctx, key, incidentID.String(), idempotencyTTL)
}

// ReleaseIdempotencyToken removes a token if incident creation failed,
// allowing a retry by the next caller.
func ReleaseIdempotencyToken(ctx context.Context, rdb *redis.Client, token string) {
	key := idempotencyKeyPrefix + token
	rdb.Del(ctx, key)
}
