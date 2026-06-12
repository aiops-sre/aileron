package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Scope constants — coarse-grained permissions for API keys.
const (
	ScopeAlertsRead     = "alerts:read"
	ScopeAlertsWrite    = "alerts:write"
	ScopeIncidentsRead  = "incidents:read"
	ScopeIncidentsWrite = "incidents:write"
	ScopeWebhooksIngest = "webhooks:ingest"
	ScopeAnalyticsRead  = "analytics:read"
	ScopeAdmin          = "admin"

	keyByteLen   = 32           // 256-bit raw key 64-char hex
	keyPrefix    = "sk-ah-"     // AlertHub API key prefix (sk = secret key)
)

// AllScopes is the complete list used for validation.
var AllScopes = []string{
	ScopeAlertsRead, ScopeAlertsWrite,
	ScopeIncidentsRead, ScopeIncidentsWrite,
	ScopeWebhooksIngest, ScopeAnalyticsRead,
	ScopeAdmin,
}

// APIKey is the domain model — plaintext is never persisted, only returned once on creation.
type APIKey struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Name        string     `json:"name"`
	KeyPrefix   string     `json:"key_prefix"`   // first 12 chars shown in UI
	Scopes      []string   `json:"scopes"`
	TierName    string     `json:"tier_name"`
	Description string     `json:"description"`
	IsActive    bool       `json:"is_active"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP  string     `json:"last_used_ip,omitempty"`
	TotalReqs   int64      `json:"total_requests"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateResult wraps the created key + one-time plaintext.
type CreateResult struct {
	APIKey    *APIKey `json:"api_key"`
	Plaintext string  `json:"plaintext"` // ONLY returned on creation — never again
}

// Service provides API key lifecycle management.
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// CreateAPIKey generates a new API key for a user.
func (s *Service) CreateAPIKey(
	ctx context.Context,
	userID uuid.UUID,
	name, description string,
	scopes []string,
	tierName string,
	expiresAt *time.Time,
) (*CreateResult, error) {
	if err := validateScopes(scopes); err != nil {
		return nil, err
	}
	if tierName == "" {
		tierName = "standard"
	}

	raw, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}

	plaintext := keyPrefix + raw
	hash := hashKey(plaintext)
	prefix := plaintext[:min(len(plaintext), 14)] + "..." // sk-ah-XXXXXX...

	id := uuid.New()
	now := time.Now()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO api_keys
		    (id, user_id, name, key_prefix, key_hash, scopes, tier_name,
		     description, is_active, expires_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true,$9,$10,$10)`,
		id, userID, name, prefix, hash, scopeArray(scopes), tierName,
		description, expiresAt, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert api_key: %w", err)
	}

	key := &APIKey{
		ID: id, UserID: userID, Name: name, KeyPrefix: prefix,
		Scopes: scopes, TierName: tierName, Description: description,
		IsActive: true, ExpiresAt: expiresAt, CreatedAt: now,
	}
	return &CreateResult{APIKey: key, Plaintext: plaintext}, nil
}

// ValidateKey looks up a key by its hash and returns the API key record.
// Returns (nil, nil) when the key does not exist or is revoked/expired.
func (s *Service) ValidateKey(ctx context.Context, plaintext string) (*APIKey, error) {
	if !strings.HasPrefix(plaintext, keyPrefix) {
		return nil, nil
	}
	hash := hashKey(plaintext)

	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, key_prefix, scopes, tier_name,
		       description, expires_at, last_used_at, last_used_ip,
		       total_requests, created_at
		FROM api_keys
		WHERE key_hash = $1 AND is_active = true AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())`,
		hash,
	)

	var k APIKey
	var scopeStr string
	var lastUsedIP sql.NullString
	err := row.Scan(
		&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &scopeStr, &k.TierName,
		&k.Description, &k.ExpiresAt, &k.LastUsedAt, &lastUsedIP,
		&k.TotalReqs, &k.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.IsActive = true
	k.LastUsedIP = lastUsedIP.String
	k.Scopes = parseScopeArray(scopeStr)
	return &k, nil
}

// UpdateLastUsed bumps last_used_at, total_requests, and last_used_ip asynchronously.
func (s *Service) UpdateLastUsed(keyID uuid.UUID, ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx, `
		UPDATE api_keys
		SET last_used_at = NOW(), last_used_ip = $2,
		    total_requests = total_requests + 1, updated_at = NOW()
		WHERE id = $1`, keyID, ip)
}

// ListForUser returns all keys for a user (no plaintext).
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]*APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, key_prefix, scopes, tier_name,
		       description, is_active, expires_at, last_used_at,
		       total_requests, created_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		var k APIKey
		var scopeStr string
		if err := rows.Scan(
			&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &scopeStr, &k.TierName,
			&k.Description, &k.IsActive, &k.ExpiresAt, &k.LastUsedAt,
			&k.TotalReqs, &k.CreatedAt,
		); err != nil {
			return nil, err
		}
		k.Scopes = parseScopeArray(scopeStr)
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

// Revoke soft-deletes a key. Users can only revoke their own keys; admins can revoke any.
func (s *Service) Revoke(ctx context.Context, keyID, userID uuid.UUID, reason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE api_keys
		SET is_active = false, revoked_at = NOW(), revoked_reason = $3,
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND is_active = true`,
		keyID, userID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("key not found or already revoked")
	}
	return nil
}

// Rotate creates a new key with identical metadata and revokes the old one atomically.
func (s *Service) Rotate(ctx context.Context, oldKeyID, userID uuid.UUID) (*CreateResult, error) {
	old, err := s.getByID(ctx, oldKeyID, userID)
	if err != nil {
		return nil, err
	}
	result, err := s.CreateAPIKey(ctx, userID, old.Name+" (rotated)", old.Description,
		old.Scopes, old.TierName, old.ExpiresAt)
	if err != nil {
		return nil, err
	}
	// Mark old key rotated
	_, _ = s.db.ExecContext(ctx, `
		UPDATE api_keys SET is_active = false, revoked_at = NOW(),
		    revoked_reason = 'rotated', updated_at = NOW()
		WHERE id = $1`, oldKeyID)
	return result, nil
}

// GetUsageStats returns daily request counts for a key over the last 7 days.
func (s *Service) GetUsageStats(ctx context.Context, keyID, userID uuid.UUID) ([]map[string]interface{}, error) {
	// Verify ownership
	if _, err := s.getByID(ctx, keyID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DATE_TRUNC('day', used_at) AS day,
		       COUNT(*)                  AS total,
		       COUNT(*) FILTER (WHERE status_code >= 400) AS errors,
		       AVG(latency_ms)           AS avg_latency_ms
		FROM api_key_usage
		WHERE api_key_id = $1 AND used_at > NOW() - INTERVAL '7 days'
		GROUP BY 1 ORDER BY 1 DESC`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []map[string]interface{}
	for rows.Next() {
		var day time.Time
		var total, errors int64
		var avgLatency sql.NullFloat64
		if err := rows.Scan(&day, &total, &errors, &avgLatency); err != nil {
			continue
		}
		stats = append(stats, map[string]interface{}{
			"day": day.Format("2006-01-02"), "requests": total,
			"errors": errors, "avg_latency_ms": avgLatency.Float64,
		})
	}
	return stats, rows.Err()
}

// HasScope returns true if the key grants the requested scope.
// "admin" scope implies all scopes.
func (k *APIKey) HasScope(scope string) bool {
	for _, s := range k.Scopes {
		if s == ScopeAdmin || s == scope {
			return true
		}
	}
	return false
}

// ---- helpers ----------------------------------------------------------------

func generateKey() (string, error) {
	b := make([]byte, keyByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

func validateScopes(scopes []string) error {
	valid := make(map[string]bool, len(AllScopes))
	for _, s := range AllScopes {
		valid[s] = true
	}
	for _, s := range scopes {
		if !valid[s] {
			return fmt.Errorf("unknown scope: %q", s)
		}
	}
	if len(scopes) == 0 {
		return fmt.Errorf("at least one scope is required")
	}
	return nil
}

func scopeArray(scopes []string) string {
	return "{" + strings.Join(scopes, ",") + "}"
}

func parseScopeArray(raw string) []string {
	raw = strings.Trim(raw, "{}")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func (s *Service) getByID(ctx context.Context, keyID, userID uuid.UUID) (*APIKey, error) {
	var k APIKey
	var scopeStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, key_prefix, scopes, tier_name,
		       description, is_active, expires_at, total_requests, created_at
		FROM api_keys WHERE id = $1 AND user_id = $2`, keyID, userID).
		Scan(&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &scopeStr, &k.TierName,
			&k.Description, &k.IsActive, &k.ExpiresAt, &k.TotalReqs, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("api key not found")
	}
	if err != nil {
		return nil, err
	}
	k.Scopes = parseScopeArray(scopeStr)
	return &k, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
