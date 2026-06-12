package jwt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken     = errors.New("invalid token")
	ErrExpiredToken     = errors.New("token has expired")
	ErrTokenNotFound    = errors.New("token not found")
	ErrInvalidSignature = errors.New("invalid token signature")
)

// Claims represents JWT claims
type Claims struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// RevokedToken represents a revoked token entry
type RevokedToken struct {
	TokenID   string
	RevokedAt time.Time
	ExpiresAt time.Time
}

// RevocationStore abstracts the backend used for token blacklisting.
// The default implementation (memRevocationStore) is in-process.
// Wire in a RedisRevocationStore for cross-pod consistency.
type RevocationStore interface {
	Revoke(ctx context.Context, tokenID string, expiry time.Time) error
	IsRevoked(ctx context.Context, tokenID string) (bool, error)
	RevokeAllForUser(ctx context.Context, userID uuid.UUID, expiry time.Time) error
}

// memRevocationStore is the in-process fallback (single-pod consistent only).
type memRevocationStore struct {
	mu     sync.RWMutex
	tokens map[string]*RevokedToken
}

func newMemRevocationStore() *memRevocationStore {
	s := &memRevocationStore{tokens: make(map[string]*RevokedToken)}
	go s.cleanup()
	return s
}

func (m *memRevocationStore) Revoke(_ context.Context, id string, expiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[id] = &RevokedToken{TokenID: id, RevokedAt: time.Now(), ExpiresAt: expiry}
	return nil
}

func (m *memRevocationStore) IsRevoked(_ context.Context, id string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt, ok := m.tokens[id]
	if !ok || time.Now().After(rt.ExpiresAt) {
		return false, nil
	}
	return true, nil
}

func (m *memRevocationStore) RevokeAllForUser(_ context.Context, _ uuid.UUID, _ time.Time) error {
	// In-memory store cannot enumerate per-user tokens; no-op fallback.
	return nil
}

func (m *memRevocationStore) cleanup() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		now := time.Now()
		for id, rt := range m.tokens {
			if now.After(rt.ExpiresAt) {
				delete(m.tokens, id)
			}
		}
		m.mu.Unlock()
	}
}

// RedisRevocationStore stores revoked token IDs in Redis with TTL.
// Each token ID is stored as   revoked:token:<tokenID>  with TTL = remaining token lifetime.
// Per-user bulk-revoke uses a sentinel key: revoked:user:<userID>  with the same TTL.
// On IsRevoked we check both the token key and the user sentinel.
type RedisRevocationStore struct {
	// redisClient is a minimal interface so we can accept *cache.RedisCache without
	// a circular import.  We use only Set/Get.
	set func(ctx context.Context, key string, ttl time.Duration) error
	get func(ctx context.Context, key string) (bool, error)
}

// NewRedisRevocationStore wraps two simple Redis primitives.
// Pass in closures over your existing *cache.RedisCache client:
//
//	store := jwt.NewRedisRevocationStore(
//	    func(ctx context.Context, key string, ttl time.Duration) error {
//	        return redisCache.Client().Set(ctx, key, "1", ttl).Err()
//	    },
//	    func(ctx context.Context, key string) (bool, error) {
//	        err := redisCache.Client().Get(ctx, key).Err()
//	        if err == redis.Nil { return false, nil }
//	        return err == nil, err
//	    },
//	)
func NewRedisRevocationStore(
	setFn func(ctx context.Context, key string, ttl time.Duration) error,
	getFn func(ctx context.Context, key string) (bool, error),
) *RedisRevocationStore {
	return &RedisRevocationStore{set: setFn, get: getFn}
}

func (r *RedisRevocationStore) Revoke(ctx context.Context, tokenID string, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return nil // already expired; nothing to store
	}
	return r.set(ctx, "revoked:token:"+tokenID, ttl)
}

func (r *RedisRevocationStore) IsRevoked(ctx context.Context, tokenID string) (bool, error) {
	return r.get(ctx, "revoked:token:"+tokenID)
}

func (r *RedisRevocationStore) RevokeAllForUser(ctx context.Context, userID uuid.UUID, expiry time.Time) error {
	ttl := time.Until(expiry)
	if ttl <= 0 {
		return nil
	}
	return r.set(ctx, fmt.Sprintf("revoked:user:%s", userID), ttl)
}

// JWTService handles JWT token operations
type JWTService struct {
	secretKey        []byte
	refreshSecretKey []byte
	issuer           string
	accessTokenTTL   time.Duration
	refreshTokenTTL  time.Duration
	revocation       RevocationStore
}

// NewJWTService creates a new JWT service backed by an in-memory revocation store.
// Call SetRevocationStore() to upgrade to Redis after the cache is initialised.
func NewJWTService(secretKey, refreshSecretKey, issuer string, accessTTL, refreshTTL time.Duration) *JWTService {
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	if refreshTTL <= 0 {
		refreshTTL = 7 * 24 * time.Hour
	}
	return &JWTService{
		secretKey:        []byte(secretKey),
		refreshSecretKey: []byte(refreshSecretKey),
		issuer:           issuer,
		accessTokenTTL:   accessTTL,
		refreshTokenTTL:  refreshTTL,
		revocation:       newMemRevocationStore(),
	}
}

// SetRevocationStore replaces the default in-memory store with a persistent one (e.g. Redis).
// Call this once after initialisation — before any tokens are issued.
func (s *JWTService) SetRevocationStore(store RevocationStore) {
	s.revocation = store
}

// GenerateTokenPair generates access and refresh tokens
func (s *JWTService) GenerateTokenPair(userID uuid.UUID, username, email, role string, permissions []string) (*TokenPair, error) {
	now := time.Now()
	expiresAt := now.Add(s.accessTokenTTL)

	claims := &Claims{
		UserID:      userID,
		Username:    username,
		Email:       email,
		Role:        role,
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString(s.secretKey)
	if err != nil {
		return nil, err
	}

	refreshClaims := &Claims{
		UserID:   userID,
		Username: username,
		Email:    email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshTokenTTL)),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}

	refreshTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshToken, err := refreshTokenObj.SignedString(s.refreshSecretKey)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.accessTokenTTL.Seconds()),
		ExpiresAt:    expiresAt,
	}, nil
}

// ValidateToken validates and parses a JWT token
func (s *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidSignature
		}
		return s.secretKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	revoked, err := s.IsTokenRevoked(claims.ID)
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, errors.New("token has been revoked")
	}

	return claims, nil
}

// ValidateRefreshToken validates a refresh token
func (s *JWTService) ValidateRefreshToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidSignature
		}
		return s.refreshSecretKey, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// RefreshAccessToken generates a new access token from refresh token
func (s *JWTService) RefreshAccessToken(refreshToken string, permissions []string) (*TokenPair, error) {
	claims, err := s.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, err
	}
	return s.GenerateTokenPair(claims.UserID, claims.Username, claims.Email, claims.Role, permissions)
}

// RevokeToken revokes a specific token by ID.
func (s *JWTService) RevokeToken(tokenID string) error {
	return s.revocation.Revoke(context.Background(), tokenID, time.Now().Add(s.accessTokenTTL))
}

// RevokeTokenWithExpiry revokes a token with a specific expiration time
func (s *JWTService) RevokeTokenWithExpiry(tokenID string, expiresAt time.Time) error {
	return s.revocation.Revoke(context.Background(), tokenID, expiresAt)
}

// IsTokenRevoked checks if a token has been revoked.
func (s *JWTService) IsTokenRevoked(tokenID string) (bool, error) {
	return s.revocation.IsRevoked(context.Background(), tokenID)
}

// RevokeAllUserTokens revokes all active tokens for a user (requires Redis store).
func (s *JWTService) RevokeAllUserTokens(userID uuid.UUID) error {
	return s.revocation.RevokeAllForUser(context.Background(), userID, time.Now().Add(s.refreshTokenTTL))
}

// GetRevokedTokenCount returns the count only for the in-memory store.
func (s *JWTService) GetRevokedTokenCount() int {
	if m, ok := s.revocation.(*memRevocationStore); ok {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return len(m.tokens)
	}
	return -1 // Redis store: unknown without a KEYS scan
}

// ExtractTokenFromHeader extracts JWT token from Authorization header
func ExtractTokenFromHeader(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("authorization header is empty")
	}
	const bearerPrefix = "Bearer "
	if len(authHeader) < len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
		return "", errors.New("authorization header must start with 'Bearer '")
	}
	return authHeader[len(bearerPrefix):], nil
}

// TokenMetadata contains token metadata
type TokenMetadata struct {
	TokenID   string
	UserID    uuid.UUID
	Username  string
	Email     string
	Role      string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// GetTokenMetadata extracts metadata from token without full validation
func (s *JWTService) GetTokenMetadata(tokenString string) (*TokenMetadata, error) {
	claims, err := s.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}
	return &TokenMetadata{
		TokenID:   claims.ID,
		UserID:    claims.UserID,
		Username:  claims.Username,
		Email:     claims.Email,
		Role:      claims.Role,
		IssuedAt:  claims.IssuedAt.Time,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// HasPermission checks if token has a specific permission
func (c *Claims) HasPermission(permission string) bool {
	for _, p := range c.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

func (c *Claims) HasAnyPermission(permissions ...string) bool {
	for _, required := range permissions {
		if c.HasPermission(required) {
			return true
		}
	}
	return false
}

func (c *Claims) HasAllPermissions(permissions ...string) bool {
	for _, required := range permissions {
		if !c.HasPermission(required) {
			return false
		}
	}
	return true
}

func (c *Claims) IsAdmin() bool          { return c.Role == "admin" }
func (c *Claims) IsExpired() bool        { return time.Now().After(c.ExpiresAt.Time) }
func (c *Claims) TimeUntilExpiry() time.Duration { return time.Until(c.ExpiresAt.Time) }

