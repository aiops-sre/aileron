package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ============================================================================
// OIDC OAUTH 2.0 AUTHORIZATION CODE FLOW
// Full implementation based on Aileron OIDC documentation
// ============================================================================

// AuthorizationRequest represents OAuth authorization request
type AuthorizationRequest struct {
	ClientID            string   `json:"client_id"`
	RedirectURI         string   `json:"redirect_uri"`
	ResponseType        string   `json:"response_type"`                   // "code"
	Scope               []string `json:"scope"`                           // ["openid", "api", "dsid", "accountname", "email", "groups"]
	Audience            string   `json:"audience"`                        // "hvys3fcwcteqrvw3qzkvtk86viuoqv"
	State               string   `json:"state"`                           // CSRF protection
	CodeChallenge       string   `json:"code_challenge,omitempty"`        // PKCE
	CodeChallengeMethod string   `json:"code_challenge_method,omitempty"` // "S256"
}

// AuthorizationResponse represents callback response from OIDC
type AuthorizationResponse struct {
	Code             string `json:"code"`
	State            string `json:"state"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// TokenRequest represents token exchange request
type TokenRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	GrantType    string `json:"grant_type"`              // "authorization_code"
	Code         string `json:"code"`                    // Authorization code from callback
	RedirectURI  string `json:"redirect_uri"`            // Must match authorization request
	CodeVerifier string `json:"code_verifier,omitempty"` // PKCE
}

// UserClaims represents user information from ID token
type UserClaims struct {
	Subject     string   `json:"sub"` // DSID
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Picture     string   `json:"picture"` // Profile photo URL from OIDC profile scope
	AccountName string   `json:"accountname"`
	Groups      []string `json:"groups"`
	DSID        string   `json:"dsid"`
	Issuer      string   `json:"iss"`
	Audience    string   `json:"aud"`
	ExpiresAt   int64    `json:"exp"`
	IssuedAt    int64    `json:"iat"`
}

// SessionData represents user session information
type SessionData struct {
	UserID       string                 `json:"user_id"` // DSID
	Email        string                 `json:"email"`
	AccountName  string                 `json:"account_name"`
	Groups       []string               `json:"groups"`
	AccessToken  string                 `json:"access_token"`
	RefreshToken string                 `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time              `json:"expires_at"`
	CreatedAt    time.Time              `json:"created_at"`
	LastUsed     time.Time              `json:"last_used"`
	UserAgent    string                 `json:"user_agent"`
	ClientIP     string                 `json:"client_ip"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// RBAC represents role-based access control configuration
type RBAC struct {
	RequiredGroups []string            `json:"required_groups"`
	AdminGroups    []string            `json:"admin_groups"`
	RoleMapping    map[string]string   `json:"role_mapping"` // group -> role
	Permissions    map[string][]string `json:"permissions"`  // role -> permissions
}

// IsConfigured returns true when the client has a non-empty client secret.
func (c *OAuthClient) IsConfigured() bool {
	return c.config.ClientSecret != ""
}

// GetClientID returns the configured OAuth client ID (safe to log).
func (c *OAuthClient) GetClientID() string {
	return c.config.ClientID
}

// GetAuthorizationURL generates OAuth authorization URL
func (c *OAuthClient) GetAuthorizationURL(redirectURI, state string) (string, error) {
	authURL, err := url.Parse(fmt.Sprintf("%s/protocol/openid-connect/auth", c.config.OIDCBaseURL))
	if err != nil {
		return "", fmt.Errorf("failed to parse auth URL: %w", err)
	}

	// Required scopes for OIDC Provider access (space-separated per RFC 6749 §3.3)
	// "profile" added to get picture/name claims from userinfo endpoint
	scopes := []string{"openid", "api", "dsid", "accountname", "email", "groups", "profile", "offline_access"}

	params := url.Values{}
	params.Set("client_id", c.config.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", strings.Join(scopes, " "))
	params.Set("state", state)
	// Include OIDC Provider OIDC client as audience so the refresh token carries OIDC Provider
	// consent. SEAR-OIDC Provider approved this client, so OIDC will honour the audience.
	params.Set("audience", "hvys3fcwcteqrvw3qzkvtk86viuoqv")

	authURL.RawQuery = params.Encode()
	return authURL.String(), nil
}

// ExchangeCodeForTokens exchanges authorization code for access and ID tokens
func (c *OAuthClient) ExchangeCodeForTokens(ctx context.Context, code, redirectURI string) (*TokenResponse, *UserClaims, error) {
	tokenURL := fmt.Sprintf("%s/auth/oauth2/token", c.config.OIDCBaseURL)

	// OAuth2 token exchange uses application/x-www-form-urlencoded (RFC 6749)
	data := url.Values{}
	data.Set("client_id", c.config.ClientID)
	data.Set("client_secret", c.config.ClientSecret)
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	// Request OIDC Provider audience in the code exchange so the returned id_token is
	// valid for OIDC Provider without a separate token exchange round-trip.
	data.Set("audience", "hvys3fcwcteqrvw3qzkvtk86viuoqv")

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token,omitempty"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Parse ID token for user claims (without signature validation for now)
	userClaims, err := c.parseIDToken(tokenResp.IDToken)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse ID token: %w", err)
	}

	// Create token response
	token := &TokenResponse{
		AccessToken:  tokenResp.AccessToken,
		IdToken:      tokenResp.IDToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scope:        tokenResp.Scope,
		IssuedAt:     time.Now(),
	}

	// If the ID token didn't carry a picture claim, try the userinfo endpoint.
	// Aileron OIDC sometimes includes richer profile data there.
	if userClaims.Picture == "" && tokenResp.AccessToken != "" {
		if info, err := c.FetchUserInfo(ctx, tokenResp.AccessToken); err == nil && info != nil && info.Picture != "" {
			userClaims.Picture = info.Picture
		}
	}

	return token, userClaims, nil
}

// FetchUserInfo calls the OIDC userinfo endpoint with the given access token
// and returns any additional user claims (e.g. picture URL) not present in the ID token.
func (c *OAuthClient) FetchUserInfo(ctx context.Context, accessToken string) (*UserClaims, error) {
	// Try the standard OIDC userinfo path; OIDC uses the same base as the token endpoint.
	userInfoURL := fmt.Sprintf("%s/auth/oauth2/userinfo", c.config.OIDCBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo returned %d", resp.StatusCode)
	}

	var claims map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		return nil, err
	}
	// Log all claim keys so we know what OIDC actually returns (helps debug missing picture).
	keys := make([]string, 0, len(claims))
	for k := range claims {
		keys = append(keys, k)
	}
	log.Printf("userinfo claims keys: %v", keys)
	// Log the raw groups claim so we can debug role-resolution issues.
	if rawGroups, ok := claims["groups"]; ok {
		log.Printf("userinfo groups claim for %v: %#v", claims["accountname"], rawGroups)
	} else {
		log.Printf("userinfo groups claim absent for %v", claims["accountname"])
	}
	return c.extractUserClaims(jwt.MapClaims(claims)), nil
}

// parseIDToken parses and validates ID token claims
func (c *OAuthClient) parseIDToken(idToken string) (*UserClaims, error) {
	// Parse JWT token (without signature validation for now)
	// In production, you should validate the signature against OIDC public keys
	token, err := jwt.Parse(idToken, func(token *jwt.Token) (interface{}, error) {
		// For now, skip signature validation
		// In production, fetch and use OIDC public keys
		return []byte("dummy-key"), nil
	})

	if err != nil {
		// Parse claims anyway for development
		parser := jwt.NewParser()
		claims := jwt.MapClaims{}
		_, _, err := parser.ParseUnverified(idToken, claims)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ID token: %w", err)
		}

		return c.extractUserClaims(claims), nil
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		return c.extractUserClaims(claims), nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// extractUserClaims extracts user information from JWT claims
func (c *OAuthClient) extractUserClaims(claims jwt.MapClaims) *UserClaims {
	userClaims := &UserClaims{}

	if sub, ok := claims["sub"].(string); ok {
		userClaims.Subject = sub
		userClaims.DSID = sub
	}

	if email, ok := claims["email"].(string); ok {
		userClaims.Email = email
	}

	if name, ok := claims["name"].(string); ok {
		userClaims.Name = name
	}

	if picture, ok := claims["picture"].(string); ok {
		userClaims.Picture = picture
	}
	// Try alternate field names Aileron OIDC may use for the profile photo.
	if userClaims.Picture == "" {
		for _, key := range []string{"photo", "photo_url", "avatar_url", "profile_image_url", "image", "thumbnail"} {
			if v, ok := claims[key].(string); ok && v != "" {
				userClaims.Picture = v
				break
			}
		}
	}

	if accountName, ok := claims["accountname"].(string); ok {
		userClaims.AccountName = accountName
	}

	if groups, ok := claims["groups"].([]interface{}); ok {
		userClaims.Groups = make([]string, len(groups))
		for i, group := range groups {
			if groupStr, ok := group.(string); ok {
				userClaims.Groups[i] = groupStr
			}
		}
	}

	if iss, ok := claims["iss"].(string); ok {
		userClaims.Issuer = iss
	}

	if aud, ok := claims["aud"].(string); ok {
		userClaims.Audience = aud
	}

	if exp, ok := claims["exp"].(float64); ok {
		userClaims.ExpiresAt = int64(exp)
	}

	if iat, ok := claims["iat"].(float64); ok {
		userClaims.IssuedAt = int64(iat)
	}

	return userClaims
}

// CreateUserSession creates secure user session
func (c *OAuthClient) CreateUserSession(userClaims *UserClaims, tokens *TokenResponse, clientIP, userAgent string) *SessionData {
	session := &SessionData{
		UserID:       userClaims.DSID,
		Email:        userClaims.Email,
		AccountName:  userClaims.AccountName,
		Groups:       userClaims.Groups,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
		CreatedAt:    time.Now(),
		LastUsed:     time.Now(),
		UserAgent:    userAgent,
		ClientIP:     clientIP,
		Metadata:     make(map[string]interface{}),
	}

	// Cache session with user ID
	c.cacheToken(userClaims.DSID, tokens)

	return session
}

// ValidateUserAccess validates user access based on groups and RBAC
func (c *OAuthClient) ValidateUserAccess(userGroups []string, rbac *RBAC) (bool, string, []string) {
	// Check if user has any required groups
	hasRequiredGroup := false
	userRole := "user" // Default role
	var userPermissions []string

	for _, userGroup := range userGroups {
		// Check required groups
		for _, requiredGroup := range rbac.RequiredGroups {
			if userGroup == requiredGroup {
				hasRequiredGroup = true
				break
			}
		}

		// Check admin groups
		for _, adminGroup := range rbac.AdminGroups {
			if userGroup == adminGroup {
				userRole = "admin"
				break
			}
		}

		// Map group to role
		if role, exists := rbac.RoleMapping[userGroup]; exists {
			userRole = role
		}
	}

	// Get permissions for user role
	if permissions, exists := rbac.Permissions[userRole]; exists {
		userPermissions = permissions
	}

	return hasRequiredGroup, userRole, userPermissions
}

// GenerateState generates secure random state for CSRF protection
func (c *OAuthClient) GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// GetRBACConfig returns RBAC configuration for AlertHub
func (c *OAuthClient) GetRBACConfig() *RBAC {
	return &RBAC{
		RequiredGroups: []string{
			"aileron-operators",
			"sre-team-access",
			"alerthub-users",
		},
		AdminGroups: []string{
			"sre-admin",
			"alerthub-admin",
			"aileron-admins",
		},
		RoleMapping: map[string]string{
			"aileron-operators":     "engineer",
			"oidc-google-models-access": "ai-user",
			"oidc-anthropic-access":     "ai-user",
			"sre-admin":                      "admin",
			"alerthub-admin":                 "admin",
			"sre-oncall":                     "oncall",
		},
		Permissions: map[string][]string{
			"admin": {
				"alerts.view", "alerts.create", "alerts.update", "alerts.delete",
				"incidents.view", "incidents.create", "incidents.update", "incidents.resolve",
				"users.view", "users.create", "users.update", "users.delete",
				"system.configure", "analytics.view", "ai.use", "audit.view",
			},
			"engineer": {
				"alerts.view", "alerts.update", "alerts.resolve",
				"incidents.view", "incidents.create", "incidents.update", "incidents.resolve",
				"analytics.view", "ai.use",
			},
			"ai-user": {
				"alerts.view", "incidents.view", "ai.use", "analytics.view",
			},
			"oncall": {
				"alerts.view", "alerts.update", "alerts.resolve",
				"incidents.view", "incidents.update", "incidents.resolve",
			},
			"user": {
				"alerts.view", "incidents.view", "analytics.view",
			},
		},
	}
}

// ============================================================================
// FLOODGATE INTEGRATION (Enhanced)
// ============================================================================

// OIDC ProviderTokenRequest requests OIDC Provider-specific token
type OIDC ProviderTokenRequest struct {
	UserToken string `json:"user_token"`
	UserIP    string `json:"user_ip"`
	UserAgent string `json:"user_agent"`
}

// GetOIDC ProviderToken gets or exchanges token for OIDC Provider API access
func (c *OAuthClient) GetOIDC ProviderToken(ctx context.Context, userID, userIP string) (*TokenResponse, error) {
	// Check if we have a cached multi-audience token
	if cached := c.getCachedToken(userID); cached != nil {
		// Validate token has OIDC Provider audience
		if c.hasOIDC ProviderAudience(cached.Token.AccessToken) {
			return cached.Token, nil
		}
	}

	// Get user's app token
	userToken := c.getCachedToken(userID)
	if userToken == nil {
		return nil, fmt.Errorf("user not authenticated")
	}

	// Exchange for OIDC Provider token
	oidcToken, err := c.ExchangeTokenForOIDC Provider(ctx, userToken.Token.AccessToken, userID)
	if err != nil {
		return nil, fmt.Errorf("OIDC Provider token exchange failed: %w", err)
	}

	return oidcToken, nil
}

// hasOIDC ProviderAudience checks if token has OIDC Provider audience
func (c *OAuthClient) hasOIDC ProviderAudience(accessToken string) bool {
	// Parse JWT to check audience claim
	parser := jwt.NewParser()
	claims := jwt.MapClaims{}
	_, _, err := parser.ParseUnverified(accessToken, claims)
	if err != nil {
		return false
	}

	// Check audience claim
	if aud, ok := claims["aud"].(string); ok {
		return strings.Contains(aud, "sear-oidc")
	}

	if auds, ok := claims["aud"].([]interface{}); ok {
		for _, aud := range auds {
			if audStr, ok := aud.(string); ok && strings.Contains(audStr, "sear-oidc") {
				return true
			}
		}
	}

	return false
}

// ============================================================================
// SESSION MANAGEMENT
// ============================================================================

// SessionManager manages user sessions with Redis backend
type SessionManager struct {
	redis          RedisClient
	sessionTTL     time.Duration
	cookieName     string
	secureCookie   bool
	httpOnlyCookie bool
}

// RedisClient interface for session storage
type RedisClient interface {
	Set(key string, value interface{}, expiration time.Duration) error
	Get(key string) (string, error)
	Del(key string) error
}

// NewSessionManager creates new session manager
func NewSessionManager(redis RedisClient) *SessionManager {
	return &SessionManager{
		redis:          redis,
		sessionTTL:     8 * time.Hour, // 8-hour sessions
		cookieName:     "alerthub_session",
		secureCookie:   true, // HTTPS only
		httpOnlyCookie: true, // Not accessible to JavaScript
	}
}

// CreateSession creates secure session cookie
func (sm *SessionManager) CreateSession(w http.ResponseWriter, session *SessionData) (string, error) {
	// Generate secure session ID
	sessionID, err := sm.generateSecureSessionID()
	if err != nil {
		return "", err
	}

	// Store session data in Redis
	sessionJSON, _ := json.Marshal(session)
	sessionKey := fmt.Sprintf("session:%s", sessionID)

	if err := sm.redis.Set(sessionKey, string(sessionJSON), sm.sessionTTL); err != nil {
		return "", fmt.Errorf("failed to store session: %w", err)
	}

	// Set secure cookie
	cookie := &http.Cookie{
		Name:     sm.cookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   int(sm.sessionTTL.Seconds()),
		HttpOnly: sm.httpOnlyCookie,
		Secure:   sm.secureCookie,
		SameSite: http.SameSiteStrictMode,
	}

	http.SetCookie(w, cookie)
	return sessionID, nil
}

// GetSession retrieves session data from cookie
func (sm *SessionManager) GetSession(r *http.Request) (*SessionData, error) {
	cookie, err := r.Cookie(sm.cookieName)
	if err != nil {
		return nil, fmt.Errorf("session cookie not found")
	}

	// Get session data from Redis
	sessionKey := fmt.Sprintf("session:%s", cookie.Value)
	sessionJSON, err := sm.redis.Get(sessionKey)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}

	var session SessionData
	if err := json.Unmarshal([]byte(sessionJSON), &session); err != nil {
		return nil, fmt.Errorf("invalid session data")
	}

	// Update last used time
	session.LastUsed = time.Now()
	updatedJSON, _ := json.Marshal(session)
	sm.redis.Set(sessionKey, string(updatedJSON), sm.sessionTTL)

	return &session, nil
}

// DestroySession removes session and cookie
func (sm *SessionManager) DestroySession(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(sm.cookieName)
	if err != nil {
		return nil // No cookie to destroy
	}

	// Remove from Redis
	sessionKey := fmt.Sprintf("session:%s", cookie.Value)
	sm.redis.Del(sessionKey)

	// Clear cookie
	clearCookie := &http.Cookie{
		Name:     sm.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: sm.httpOnlyCookie,
		Secure:   sm.secureCookie,
	}

	http.SetCookie(w, clearCookie)
	return nil
}

func (sm *SessionManager) generateSecureSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// ============================================================================
// MIDDLEWARE FOR HTTP HANDLERS
// ============================================================================

// AuthMiddleware provides OAuth authentication middleware
func (c *OAuthClient) AuthMiddleware(sessionManager *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get session
			session, err := sessionManager.GetSession(r)
			if err != nil {
				// Redirect to login
				loginURL := fmt.Sprintf("/login?redirect=%s", url.QueryEscape(r.URL.String()))
				http.Redirect(w, r, loginURL, http.StatusTemporaryRedirect)
				return
			}

			// Add user info to request context
			ctx := r.Context()
			ctx = context.WithValue(ctx, "user_id", session.UserID)
			ctx = context.WithValue(ctx, "user_email", session.Email)
			ctx = context.WithValue(ctx, "user_groups", session.Groups)
			ctx = context.WithValue(ctx, "session", session)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RBACMiddleware provides role-based access control
func (c *OAuthClient) RBACMiddleware(requiredPermission string) func(http.Handler) http.Handler {
	rbac := c.GetRBACConfig()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user groups from context
			userGroups, ok := r.Context().Value("user_groups").([]string)
			if !ok {
				http.Error(w, "Access denied: no user groups", http.StatusForbidden)
				return
			}

			// Check RBAC
			hasAccess, userRole, permissions := c.ValidateUserAccess(userGroups, rbac)
			if !hasAccess {
				http.Error(w, "Access denied: insufficient groups", http.StatusForbidden)
				return
			}

			// Check specific permission
			hasPermission := false
			for _, permission := range permissions {
				if permission == requiredPermission {
					hasPermission = true
					break
				}
			}

			if !hasPermission {
				http.Error(w, fmt.Sprintf("Access denied: missing permission '%s'", requiredPermission), http.StatusForbidden)
				return
			}

			// Add role info to context
			ctx := r.Context()
			ctx = context.WithValue(ctx, "user_role", userRole)
			ctx = context.WithValue(ctx, "user_permissions", permissions)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ============================================================================
// HEALTH CHECK AND DIAGNOSTICS
// ============================================================================

// HealthCheck validates OAuth client configuration and connectivity
func (c *OAuthClient) HealthCheck(ctx context.Context) map[string]interface{} {
	health := map[string]interface{}{
		"oauth_configured":     c.config.ClientID != "",
		"oidc_base_url":        c.config.OIDCBaseURL,
		"oidc_configured": c.config.OIDC ProviderAppID != "",
		"cache_enabled":        true,
		"multi_audience":       len(c.config.Audiences) > 1,
		"required_groups":      c.config.RequiredGroups,
	}

	// Test OIDC connectivity
	if c.config.OIDCBaseURL != "" {
		testURL := fmt.Sprintf("%s/.well-known/openid_configuration", c.config.OIDCBaseURL)
		resp, err := http.Get(testURL)
		if err == nil && resp.StatusCode == 200 {
			health["oidc_connectivity"] = "healthy"
			resp.Body.Close()
		} else {
			health["oidc_connectivity"] = "unhealthy"
		}
	}

	// Cache statistics
	cacheCount := 0
	c.tokenCache.Range(func(key, value interface{}) bool {
		cacheCount++
		return true
	})
	health["cached_tokens"] = cacheCount

	return health
}
