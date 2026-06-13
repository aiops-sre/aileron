package oauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthClient handles Corporate OAuth 2.0 authentication
type OAuthClient struct {
	config     *OAuthConfig
	httpClient *http.Client
	tokenCache sync.Map // Cache tokens per user
	mu         sync.Mutex
	// JWKS cache — RS256 public keys fetched from OIDC, refreshed every hour.
	jwksCache *jwksCacheEntry
}

// jwksCacheEntry holds fetched RSA public keys indexed by kid.
type jwksCacheEntry struct {
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
}

// OAuthConfig holds OAuth 2.0 configuration
type OAuthConfig struct {
	// OIDC Configuration
	OIDCBaseURL  string //  (prod) or https://oidcac-uat.example.com (uat)
	ClientID     string // Your app's client ID (SRE Command Center: 961469)
	ClientSecret string // Your app's client secret

	// Multi-Audience Configuration
	Audiences      []string // ["sre-command-center", "sear-oidc"]
	RequiredScopes []string // ["dsid", "offline_access", "groups"]
	RequiredGroups []string // ["aileron-operators", "oidc-google-models-access"]

	// OIDCProvider Configuration
	OIDCProviderAppID   string // "928148"
	OIDCProviderBaseURL string // ""

	// Token Configuration
	TokenCacheTTL    time.Duration // How long to cache tokens
	RefreshThreshold time.Duration // Refresh tokens when this close to expiry
}

// TokenResponse represents OAuth token response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	IdToken      string `json:"id_token,omitempty"` // OIDCProvider uses the id_token as the Bearer credential
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
	IssuedAt     time.Time
}

// TokenCacheEntry stores cached token with metadata
type TokenCacheEntry struct {
	Token     *TokenResponse
	UserID    string
	ExpiresAt time.Time
}

// NewOAuthClient creates a new OAuth client
func NewOAuthClient(config *OAuthConfig) *OAuthClient {
	if config.TokenCacheTTL == 0 {
		config.TokenCacheTTL = 50 * time.Minute // Default 50 min (tokens expire in 60 min)
	}
	if config.RefreshThreshold == 0 {
		config.RefreshThreshold = 5 * time.Minute // Refresh 5 min before expiry
	}

	return &OAuthClient{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// GetDefaultConfig returns production OAuth configuration
func GetDefaultConfig() *OAuthConfig {
	return &OAuthConfig{
		OIDCBaseURL:    "",
		ClientID:       "7jdvu5f1gxuuckpbdb5s7jw6tcwpf3", // OIDC OAuth Client ID (App ID: 961469)
		Audiences:      []string{"sre-command-center", "sear-oidc"},
		RequiredScopes: []string{"dsid", "offline_access", "groups"},
		RequiredGroups: []string{
			"aileron-operators",
			"oidc-google-models-access",
			"oidc-anthropic-access",
		},
		OIDCProviderAppID:   "928148",
		OIDCProviderBaseURL: "",
		TokenCacheTTL:    50 * time.Minute,
		RefreshThreshold: 5 * time.Minute,
	}
}

// ============================================================================
// TOKEN GENERATION WITH JWT ASSERTION
// ============================================================================

// GenerateToken generates access/refresh tokens using JWT assertion
func (c *OAuthClient) GenerateToken(ctx context.Context, assertion string, userID string) (*TokenResponse, error) {
	// Check cache first
	if cached := c.getCachedToken(userID); cached != nil {
		if time.Until(cached.ExpiresAt) > c.config.RefreshThreshold {
			return cached.Token, nil
		}
		// Token expiring soon, refresh it
		if cached.Token.RefreshToken != "" {
			return c.RefreshToken(ctx, cached.Token.RefreshToken, userID)
		}
	}

	tokenURL := fmt.Sprintf("%s/auth/oauth2/token", c.config.OIDCBaseURL)

	// Build multi-audience request
	audiences := strings.Join(c.config.Audiences, " ")
	scopes := strings.Join(c.config.RequiredScopes, " ")

	payload := map[string]string{
		"client_id":     c.config.ClientID,
		"client_secret": c.config.ClientSecret,
		"grant_type":    "urn:ietf:params:oauth:grant-type:jwt-bearer",
		"assertion":     assertion,
		"scope":         scopes,
		"audience":      audiences, // Multi-audience: both app and OIDCProvider
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed: %d - %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	tokenResp.IssuedAt = time.Now()

	// Cache the token
	c.cacheToken(userID, &tokenResp)

	return &tokenResp, nil
}

// RefreshToken refreshes an access token using refresh token
func (c *OAuthClient) RefreshToken(ctx context.Context, refreshToken string, userID string) (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("%s/auth/oauth2/token", c.config.OIDCBaseURL)

	// Build form-encoded request (different from initial token request)
	data := url.Values{}
	data.Set("client_id", c.config.ClientID)
	data.Set("client_secret", c.config.ClientSecret)
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Refresh token invalid/expired - remove from cache
		c.tokenCache.Delete(userID)
		return nil, fmt.Errorf("token refresh failed: %d - %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	tokenResp.IssuedAt = time.Now()

	// Update cache with refreshed token
	c.cacheToken(userID, &tokenResp)

	return &tokenResp, nil
}

// ============================================================================
// TOKEN EXCHANGE (Alternative approach)
// ============================================================================

// ExchangeTokenForOIDCProvider exchanges app token for OIDCProvider token using RFC 8693 token exchange.
// OIDC corporate OAuth token exchange API uses application/json (per Aileron OIDC docs).
func (c *OAuthClient) ExchangeTokenForOIDCProvider(ctx context.Context, appToken string, userID string) (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("%s/auth/oauth2/token", c.config.OIDCBaseURL)

	payload := map[string]string{
		"client_id":             c.config.ClientID,
		"client_secret":         c.config.ClientSecret,
		"grant_type":            "urn:ietf:params:oauth:grant-type:token-exchange",
		"scope":                 "dsid groups",
		"resource":              c.config.OIDCProviderBaseURL,
		"requested_token_type":  "urn:ietf:params:oauth:token-type:access_token",
		"subject_token_type":    "urn:ietf:params:oauth:token-type:access_token",
		"subject_token":         appToken,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal exchange request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create exchange request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %d - %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse exchange response: %w", err)
	}

	tokenResp.IssuedAt = time.Now()

	return &tokenResp, nil
}

// ExchangeRefreshForOIDCProvider uses the OIDC refresh token (which carries the original
// authorization consent including audience=hvys3fcwcteqrvw3qzkvtk86viuoqv) to obtain
// a OIDCProvider-scoped access token. The access_token from the initial code exchange has
// aud:"OIDC" which OIDCProvider rejects; the refresh token is not audience-restricted and
// can be used to get a token targeted at the OIDCProvider OIDC client.
func (c *OAuthClient) ExchangeRefreshForOIDCProvider(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("%s/auth/oauth2/token", c.config.OIDCBaseURL)

	data := url.Values{}
	data.Set("client_id", c.config.ClientID)
	data.Set("client_secret", c.config.ClientSecret)
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("audience", "hvys3fcwcteqrvw3qzkvtk86viuoqv") // OIDCProvider OIDC client ID
	data.Set("scope", "openid dsid accountname profile groups") // openid required for id_token in response

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh exchange failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh exchange failed: %d - %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}
	tokenResp.IssuedAt = time.Now()
	log.Printf("OIDCProvider exchange: has_access_token=%v has_id_token=%v expires_in=%d",
		tokenResp.AccessToken != "", tokenResp.IdToken != "", tokenResp.ExpiresIn)
	return &tokenResp, nil
}

// OIDCProviderRequest represents a request to OIDCProvider
type OIDCProviderRequest struct {
	Method        string
	Path          string // e.g., "/api/openai/v1/models"
	Headers       map[string]string
	Body          []byte
	UserIP        string // Required: User's VPN/17net/10net IP
	UserID        string // For token lookup
	MultiAudToken string // Optional: Pre-generated multi-audience token
}

// ProxyToOIDCProvider forwards a request to OIDCProvider with user identity
func (c *OAuthClient) ProxyToOIDCProvider(ctx context.Context, req *OIDCProviderRequest) (*http.Response, error) {
	if req.UserIP == "" {
		return nil, fmt.Errorf("user IP is required for OIDCProvider requests")
	}

	// Get or use multi-audience token
	var token string
	if req.MultiAudToken != "" {
		token = req.MultiAudToken
	} else {
		// Get cached token for user
		cached := c.getCachedToken(req.UserID)
		if cached == nil || cached.Token == nil {
			return nil, fmt.Errorf("no valid token for user %s", req.UserID)
		}
		token = cached.Token.AccessToken
	}

	// Build OIDCProvider URL
	oidcURL := fmt.Sprintf("%s%s", c.config.OIDCProviderBaseURL, req.Path)

	// Create HTTP request
	var bodyReader io.Reader
	if req.Body != nil {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, oidcURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDCProvider request: %w", err)
	}

	// Set required headers
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	httpReq.Header.Set("X-Forwarded-For", req.UserIP) // CRITICAL: User's IP
	httpReq.Header.Set("User-Agent", "AlertHub-SRE-Command-Center/1.0")

	// Add custom headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Forward to OIDCProvider
	return c.httpClient.Do(httpReq)
}

// ============================================================================
// TOKEN CACHING
// ============================================================================

func (c *OAuthClient) cacheToken(userID string, token *TokenResponse) {
	expiresAt := token.IssuedAt.Add(time.Duration(token.ExpiresIn) * time.Second)

	entry := &TokenCacheEntry{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}

	c.tokenCache.Store(userID, entry)
}

func (c *OAuthClient) getCachedToken(userID string) *TokenCacheEntry {
	if val, ok := c.tokenCache.Load(userID); ok {
		entry := val.(*TokenCacheEntry)

		// Check if token is still valid
		if time.Now().Before(entry.ExpiresAt) {
			return entry
		}

		// Token expired, remove from cache
		c.tokenCache.Delete(userID)
	}
	return nil
}

// ClearCache clears all cached tokens
func (c *OAuthClient) ClearCache() {
	c.tokenCache.Range(func(key, value interface{}) bool {
		c.tokenCache.Delete(key)
		return true
	})
}

// ClearUserToken clears cached token for specific user
func (c *OAuthClient) ClearUserToken(userID string) {
	c.tokenCache.Delete(userID)
}

// ============================================================================
// VALIDATION
// ============================================================================

// ValidateToken validates an OIDC JWT by verifying its RS256 signature against
// the OIDC JWKS endpoint, then checking exp/iss/aud claims.
// Replaces the previous stub that returned {"valid":true} for ANY token.
func (c *OAuthClient) ValidateToken(ctx context.Context, tokenString string) (map[string]interface{}, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	// Decode header to get kid and alg.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT header encoding")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("invalid JWT header JSON")
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm %q: only RS256 accepted", header.Alg)
	}

	// Fetch (or use cached) RSA public key for this kid.
	pubKey, err := c.getJWKSKey(ctx, header.Kid)
	if err != nil {
		return nil, fmt.Errorf("JWKS key lookup: %w", err)
	}

	// Verify RS256 signature.
	signingInput := parts[0] + "." + parts[1]
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT signature encoding")
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		return nil, fmt.Errorf("JWT signature verification failed")
	}

	// Decode and validate claims.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT claims encoding")
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid JWT claims JSON")
	}

	// exp — token must not be expired.
	exp, ok := claims["exp"].(float64)
	if !ok || time.Now().Unix() > int64(exp) {
		return nil, fmt.Errorf("JWT has expired")
	}

	// iss — must come from our OIDC instance.
	iss, _ := claims["iss"].(string)
	if !strings.HasPrefix(iss, c.config.OIDCBaseURL) {
		return nil, fmt.Errorf("JWT issuer %q not trusted (expected prefix %q)", iss, c.config.OIDCBaseURL)
	}

	// aud — at least one audience must match our registered audiences.
	if len(c.config.Audiences) > 0 {
		audOK := false
		switch v := claims["aud"].(type) {
		case string:
			for _, a := range c.config.Audiences {
				if v == a {
					audOK = true
					break
				}
			}
		case []interface{}:
			for _, item := range v {
				if s, ok2 := item.(string); ok2 {
					for _, a := range c.config.Audiences {
						if s == a {
							audOK = true
							break
						}
					}
				}
			}
		}
		if !audOK {
			return nil, fmt.Errorf("JWT audience not in allowed list")
		}
	}

	claims["valid"] = true
	return claims, nil
}

// getJWKSKey fetches and caches RSA public keys from the OIDC JWKS endpoint.
// Keys are cached for 1 hour; forced refresh on cache miss.
func (c *OAuthClient) getJWKSKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Serve from cache if still valid.
	if c.jwksCache != nil && time.Now().Before(c.jwksCache.expiresAt) {
		if key, ok := c.jwksCache.keys[kid]; ok {
			return key, nil
		}
	}

	// Fetch fresh JWKS from OIDC.
	jwksURL := fmt.Sprintf("%s/auth/oauth2/keys", c.config.OIDCBaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building JWKS request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("parsing JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		eInt := 0
		for _, b := range eBytes {
			eInt = eInt<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: eInt}
	}
	c.jwksCache = &jwksCacheEntry{keys: keys, expiresAt: time.Now().Add(time.Hour)}

	key, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("kid %q not found in JWKS (found %d keys)", kid, len(keys))
	}
	return key, nil
}
