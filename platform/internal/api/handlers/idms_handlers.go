package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/auth/oidc"
	"github.com/aileron-platform/aileron/platform/internal/cache"
	jwtpkg "github.com/aileron-platform/aileron/platform/internal/services/jwt"
	"github.com/aileron-platform/aileron/platform/internal/services/oauth"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
	dsldap "github.com/aileron-platform/aileron/platform/internal/services/dsldap"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// oauth2MemEntry is a TTL-based in-memory store entry.
type oauth2MemEntry struct {
	data      map[string]string
	expiresAt time.Time
}

// oauth2MemStore is a thread-safe in-memory store with TTL expiry.
type oauth2MemStore struct {
	mu      sync.Mutex
	entries map[string]oauth2MemEntry
}

func newOAuth2MemStore() *oauth2MemStore {
	s := &oauth2MemStore{entries: make(map[string]oauth2MemEntry)}
	go s.cleanup()
	return s
}

func (s *oauth2MemStore) set(key string, data map[string]string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = oauth2MemEntry{data: data, expiresAt: time.Now().Add(ttl)}
}

func (s *oauth2MemStore) get(key string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		delete(s.entries, key)
		return nil, false
	}
	return e.data, true
}

func (s *oauth2MemStore) delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

func (s *oauth2MemStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, e := range s.entries {
			if now.After(e.expiresAt) {
				delete(s.entries, k)
			}
		}
		s.mu.Unlock()
	}
}

// IDMSHandler handles IDMS OAuth2 authorization code flow and Floodgate token exchange.
type IDMSHandler struct {
	config      *idms.Config
	provisioner *idms.UserProvisioner
	jwtService  *jwtpkg.JWTService
	rbacService *rbac.RBACService
	db          *sql.DB
	oauthClient *oauth.OAuthClient
	appBaseURL  string
	callbackURL string // full redirect URI registered in IDMS
	redis       *cache.RedisCache
	stateStore  *oauth2MemStore // in-memory fallback when Redis unavailable
	codeStore   *oauth2MemStore
	ldapSvc     *dsldap.Service // optional; used to fetch groups when IDMS token omits them
}

// SetLDAPService wires in the DS-LDAP group-lookup client.
func (h *IDMSHandler) SetLDAPService(svc *dsldap.Service) { h.ldapSvc = svc }

// NewIDMSHandler creates a new IDMS OAuth2 handler.
// callbackURL is the redirect URI registered in IDMS (e.g. https://host/api/v1/auth).
func NewIDMSHandler(config *idms.Config, provisioner *idms.UserProvisioner, jwtService *jwtpkg.JWTService, rbacService *rbac.RBACService, db *sql.DB, oauthClient *oauth.OAuthClient, appBaseURL string, callbackURL string, redisCache *cache.RedisCache) *IDMSHandler {
	if callbackURL == "" {
		callbackURL = appBaseURL + "/api/v1/auth/oidc/callback"
	}
	return &IDMSHandler{
		config:      config,
		provisioner: provisioner,
		jwtService:  jwtService,
		rbacService: rbacService,
		db:          db,
		oauthClient: oauthClient,
		appBaseURL:  appBaseURL,
		callbackURL: callbackURL,
		redis:       redisCache,
		stateStore:  newOAuth2MemStore(),
		codeStore:   newOAuth2MemStore(),
	}
}

// storeOAuthState stores OAuth state in Redis (primary) with in-memory fallback.
func (h *IDMSHandler) storeOAuthState(key string, data map[string]string, ttl time.Duration) {
	if h.redis != nil {
		if err := h.redis.Set("idms:state:"+key, data, ttl); err != nil {
			log.Printf("Redis state store failed (state will be pod-local only): %v", err)
		}
	}
	h.stateStore.set(key, data, ttl)
}

func (h *IDMSHandler) loadOAuthState(key string) (map[string]string, bool) {
	if h.redis != nil {
		var data map[string]string
		if err := h.redis.Get("idms:state:"+key, &data); err == nil && data != nil {
			return data, true
		}
	}
	return h.stateStore.get(key)
}

func (h *IDMSHandler) deleteOAuthState(key string) {
	if h.redis != nil {
		_ = h.redis.Delete("idms:state:" + key)
	}
	h.stateStore.delete(key)
}

// storeOAuthCode stores one-time exchange code in Redis (primary) with in-memory fallback.
func (h *IDMSHandler) storeOAuthCode(key string, data map[string]string, ttl time.Duration) {
	if h.redis != nil {
		if err := h.redis.Set("idms:code:"+key, data, ttl); err != nil {
			log.Printf("Redis code store failed, using memory: %v", err)
		}
	}
	h.codeStore.set(key, data, ttl)
}

func (h *IDMSHandler) loadOAuthCode(key string) (map[string]string, bool) {
	if h.redis != nil {
		var data map[string]string
		if err := h.redis.Get("idms:code:"+key, &data); err == nil && data != nil {
			return data, true
		}
	}
	return h.codeStore.get(key)
}

func (h *IDMSHandler) deleteOAuthCode(key string) {
	if h.redis != nil {
		_ = h.redis.Delete("idms:code:" + key)
	}
	h.codeStore.delete(key)
}

// GetIDMSSettings returns IDMS/auth settings from database (public endpoint).
// GET /api/v1/auth/oidc/settings
func (h *IDMSHandler) GetIDMSSettings(c *gin.Context) {
	settings := make(map[string]string)

	rows, err := h.db.Query("SELECT key, value FROM mas_settings")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch settings",
		})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err == nil {
			settings[key] = value
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    settings,
	})
}

// IDMSLogin initiates IDMS OAuth2 authorization code flow.
// GET /api/v1/auth/oidc
func (h *IDMSHandler) IDMSLogin(c *gin.Context) {
	if h.oauthClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "OAuth client not configured — set OAUTH_CLIENT_ID and OAUTH_CLIENT_SECRET",
		})
		return
	}

	if !h.oauthClient.IsConfigured() {
		log.Printf("IDMS Login: OAUTH_CLIENT_SECRET is not set — cannot initiate OAuth2 flow")
		c.Redirect(http.StatusFound, "/manual-login?error=oauth_not_configured&error_description="+
			url.QueryEscape("OAUTH_CLIENT_SECRET is not set. Contact your administrator."))
		return
	}

	redirectTo := c.Query("redirect")
	if redirectTo == "" || !strings.HasPrefix(redirectTo, "/") || strings.HasPrefix(redirectTo, "//") {
		redirectTo = "/dashboard"
	}

	state := uuid.New().String()
	h.storeOAuthState(state, map[string]string{"redirect": redirectTo}, 10*time.Minute)

	redirectURI := h.callbackURL
	authURL, err := h.oauthClient.GetAuthorizationURL(redirectURI, state)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to build IDMS authorization URL: " + err.Error(),
		})
		return
	}

	log.Printf("IDMS OAuth2 login initiated, redirecting to IDMS")
	c.Redirect(http.StatusFound, authURL)
}

// IDMSCallback handles the IDMS OAuth2 authorization code callback.
// Registered at both GET /api/v1/auth (backward-compat) and GET /api/v1/auth/oidc/callback.
func (h *IDMSHandler) IDMSCallback(c *gin.Context) {
	errorParam := c.Query("error")
	if errorParam != "" {
		desc := c.Query("error_description")
		log.Printf("IDMS OAuth error: %s - %s", errorParam, desc)
		redirectURL := "/manual-login?error=" + url.QueryEscape(errorParam)
		if desc != "" {
			redirectURL += "&error_description=" + url.QueryEscape(desc)
		}
		c.Redirect(http.StatusFound, redirectURL)
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		log.Printf("IDMS callback: missing code or state — restarting OAuth flow")
		c.Redirect(http.StatusFound, "/api/v1/auth/oidc?redirect=%2Fdashboard")
		return
	}

	stateData, ok := h.loadOAuthState(state)
	if !ok {
		log.Printf("IDMS callback: invalid or expired state %s — restarting OAuth flow", state)
		c.Redirect(http.StatusFound, h.appBaseURL+"/api/v1/auth/oidc?redirect=%2Fdashboard")
		return
	}
	h.deleteOAuthState(state)

	redirectTo := stateData["redirect"]
	if redirectTo == "" {
		redirectTo = "/dashboard"
	}

	redirectURI := h.callbackURL
	tokens, userClaims, err := h.oauthClient.ExchangeCodeForTokens(c.Request.Context(), code, redirectURI)
	if err != nil {
		log.Printf("IDMS token exchange failed: %v", err)
		c.Redirect(http.StatusFound, "/api/v1/auth/oidc?redirect="+url.QueryEscape(redirectTo))
		return
	}

	idmsUser := &idms.UserContext{
		Username:        userClaims.AccountName,
		Email:           userClaims.Email,
		FullName:        userClaims.Name,
		Groups:          userClaims.Groups,
		AuthMethod:      "idms-oauth2",
		AuthenticatedAt: time.Now(),
	}
	if idmsUser.Username == "" {
		idmsUser.Username = userClaims.Email
	}

	// Strip empty-string group entries that IDMS sometimes emits.
	var cleanGroups []string
	for _, g := range idmsUser.Groups {
		if strings.TrimSpace(g) != "" {
			cleanGroups = append(cleanGroups, g)
		}
	}
	log.Printf("auth: IDMS groups for %s: raw=%v clean=%v", idmsUser.Username, idmsUser.Groups, cleanGroups)
	idmsUser.Groups = cleanGroups

	// IDMS tokens often omit the groups claim; fall back to DS-LDAP.
	if len(idmsUser.Groups) == 0 && h.ldapSvc != nil {
		if ldapGroups, err := h.ldapSvc.GetUserGroups(idmsUser.Email); err == nil && len(ldapGroups) > 0 {
			idmsUser.Groups = ldapGroups
			log.Printf("auth: fetched %d groups from DS-LDAP for %s: %v", len(ldapGroups), idmsUser.Username, ldapGroups)
		} else if err != nil {
			log.Printf("auth: DS-LDAP group lookup failed for %s (non-fatal): %v", idmsUser.Username, err)
		} else {
			log.Printf("auth: DS-LDAP returned 0 groups for %s — role will use DB fallback", idmsUser.Username)
		}
	}

	if err := h.provisioner.ProvisionUser(idmsUser); err != nil {
		log.Printf("IDMS user provisioning failed for %s: %v", idmsUser.Username, err)
		c.Redirect(http.StatusFound, "/manual-login?error=provision_failed&error_description="+
			url.QueryEscape("Failed to provision user account. Please contact your administrator."))
		return
	}

	var userID uuid.UUID
	var email, roleName string
	// Priority: LDAP group mapping (DB-configured) > role_id join > role text column > 'viewer'.
	err = h.db.QueryRow(`
		SELECT u.id, u.email,
			COALESCE(
				(SELECT r2.name
				 FROM user_mas_groups umg
				 JOIN ldap_group_role_mappings m ON m.ldap_group = umg.mas_group
				 JOIN roles r2 ON r2.id = m.role_id
				 WHERE umg.user_id = u.id
				 ORDER BY CASE LOWER(r2.name)
					WHEN 'admin'    THEN 1
					WHEN 'operator' THEN 2
					WHEN 'sre'      THEN 2
					WHEN 'engineer' THEN 3
					ELSE 9
				 END ASC
				 LIMIT 1),
				r.name,
				NULLIF(TRIM(u.role), ''),
				'viewer'
			) AS role_name
		FROM users u
		LEFT JOIN roles r ON u.role_id = r.id
		WHERE u.username = $1 OR u.email = $2
		LIMIT 1
	`, idmsUser.Username, idmsUser.Email).Scan(&userID, &email, &roleName)
	if err != nil {
		log.Printf("IDMS callback: user not found after provision for %s: %v", idmsUser.Username, err)
		c.Redirect(http.StatusFound, "/api/v1/auth/oidc?redirect="+url.QueryEscape(redirectTo))
		return
	}

	jwtTokens, err := h.jwtService.GenerateTokenPair(userID, idmsUser.Username, email, roleName, []string{"alerts.view"})
	if err != nil {
		log.Printf("IDMS callback: JWT generation failed for %s: %v", idmsUser.Username, err)
		c.Redirect(http.StatusFound, "/api/v1/auth/oidc?redirect="+url.QueryEscape(redirectTo))
		return
	}

	idmsToken := ""
	idmsRefreshToken := ""
	if tokens != nil {
		idmsToken = tokens.AccessToken
		idmsRefreshToken = tokens.RefreshToken
	}
	log.Printf("IDMS tokens for %s: access_len=%d refresh_len=%d", idmsUser.Username, len(idmsToken), len(idmsRefreshToken))

	if h.redis != nil {
		if idmsToken != "" {
			tokenTTL := 55 * time.Minute
			if tokens.ExpiresIn > 0 {
				tokenTTL = time.Duration(tokens.ExpiresIn-30) * time.Second
			}
			if err := h.redis.Set("idms:token:"+userID.String(), idmsToken, tokenTTL); err != nil {
				log.Printf("Failed to cache IDMS token for user %s: %v", userID, err)
			}
		}
		// Store IDMS refresh token for Floodgate exchange — consent for audience=sear-floodgate
		// is carried by the refresh token and produces a valid Floodgate id_token.
		if idmsRefreshToken != "" {
			if err := h.redis.Set("idms:refresh:"+userID.String(), idmsRefreshToken, 7*24*time.Hour); err != nil {
				log.Printf("Failed to cache IDMS refresh token for user %s: %v", userID, err)
			}
		}
		if userClaims != nil && userClaims.Picture != "" {
			if err := h.redis.Set("user:photo:"+userID.String(), userClaims.Picture, 24*time.Hour); err != nil {
				log.Printf("Failed to cache profile picture for user %s: %v", userID, err)
			} else {
				log.Printf("Profile picture cached for %s", idmsUser.Username)
			}
		}
	}

	// Exchange IDMS refresh token (or access token) for a Floodgate id_token.
	// The id_token from the initial code exchange is scoped to the alerthub client only;
	// a separate exchange targeting audience=sear-floodgate is required.
	floodgateToken := ""
	floodgateExpiresIn := 0
	if h.oauthClient != nil {
		var fgTok *oauth.TokenResponse
		var fgErr error

		if idmsRefreshToken != "" {
			fgTok, fgErr = h.oauthClient.ExchangeRefreshForFloodgate(c.Request.Context(), idmsRefreshToken)
			if fgErr != nil {
				log.Printf("Floodgate refresh exchange failed for %s: %v — trying access token exchange", idmsUser.Username, fgErr)
			}
		}

		if fgTok == nil && idmsToken != "" {
			fgTok, fgErr = h.oauthClient.ExchangeTokenForFloodgate(c.Request.Context(), idmsToken, userID.String())
			if fgErr != nil {
				log.Printf("Floodgate token exchange failed for %s (non-fatal): %v", idmsUser.Username, fgErr)
			}
		}

		if fgTok != nil && (fgTok.IdToken != "" || fgTok.AccessToken != "") {
			floodgateToken = fgTok.IdToken
			if floodgateToken == "" {
				floodgateToken = fgTok.AccessToken
			}
			floodgateExpiresIn = fgTok.ExpiresIn
			log.Printf("Floodgate token obtained for %s (expires_in=%ds, has_id_token=%v)", idmsUser.Username, fgTok.ExpiresIn, fgTok.IdToken != "")
		}

		if floodgateToken != "" && h.redis != nil {
			fgTTL := 55 * time.Minute
			if floodgateExpiresIn > 0 {
				fgTTL = time.Duration(floodgateExpiresIn-30) * time.Second
			}
			_ = h.redis.Set("floodgate:token:"+userID.String(), floodgateToken, fgTTL)
		}
	}

	exchangeCode := uuid.New().String()
	codeData := map[string]string{
		"access_token":         jwtTokens.AccessToken,
		"refresh_token":        jwtTokens.RefreshToken,
		"user_id":              userID.String(),
		"email":                email,
		"full_name":            idmsUser.FullName,
		"role_name":            roleName,
		"redirect":             redirectTo,
		"idms_token":           idmsToken,
		"floodgate_token":      floodgateToken,
		"floodgate_expires_in": strconv.Itoa(floodgateExpiresIn),
	}
	h.storeOAuthCode(exchangeCode, codeData, 2*time.Minute)

	log.Printf("IDMS OAuth2 login successful for %s (role: %s)", idmsUser.Username, roleName)
	callbackURL := "/oauth/callback?exchange_code=" + url.QueryEscape(exchangeCode) +
		"&exchange_endpoint=" + url.QueryEscape("/api/v1/auth/oidc/exchange") +
		"&redirect=" + url.QueryEscape(redirectTo)
	c.Redirect(http.StatusFound, callbackURL)
}

// RefreshFloodgateToken silently exchanges the stored IDMS token for a fresh Floodgate token.
// Called by the frontend when the Floodgate token is about to expire.
// GET /api/v1/auth/oidc/floodgate-refresh
func (h *IDMSHandler) RefreshFloodgateToken(c *gin.Context) {
	userIDRaw, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Not authenticated"})
		return
	}
	userIDStr := fmt.Sprintf("%v", userIDRaw)

	if h.redis == nil || h.oauthClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Token refresh unavailable"})
		return
	}

	var cachedFG string
	if err := h.redis.Get("floodgate:token:"+userIDStr, &cachedFG); err == nil && cachedFG != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"floodgate_token": cachedFG,
				"source":          "cache",
			},
		})
		return
	}

	var idmsRefresh string
	_ = h.redis.Get("idms:refresh:"+userIDStr, &idmsRefresh)

	var idmsToken string
	_ = h.redis.Get("idms:token:"+userIDStr, &idmsToken)

	if idmsRefresh == "" && idmsToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "IDMS session expired — please re-authenticate",
		})
		return
	}

	var fgTok *oauth.TokenResponse
	var err error

	if idmsRefresh != "" {
		fgTok, err = h.oauthClient.ExchangeRefreshForFloodgate(c.Request.Context(), idmsRefresh)
		if err != nil {
			log.Printf("Floodgate refresh exchange failed for user %s: %v — trying access token", userIDStr, err)
		}
	}
	if fgTok == nil && idmsToken != "" {
		fgTok, err = h.oauthClient.ExchangeTokenForFloodgate(c.Request.Context(), idmsToken, userIDStr)
	}
	if err != nil || fgTok == nil {
		log.Printf("Floodgate refresh failed for user %s: %v", userIDStr, err)
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Floodgate token exchange failed — user may not have Floodgate access",
		})
		return
	}

	fgTTL := 55 * time.Minute
	if fgTok.ExpiresIn > 0 {
		fgTTL = time.Duration(fgTok.ExpiresIn-30) * time.Second
	}
	fgCredential := fgTok.IdToken
	if fgCredential == "" {
		fgCredential = fgTok.AccessToken
	}
	if err := h.redis.Set("floodgate:token:"+userIDStr, fgCredential, fgTTL); err != nil {
		log.Printf("Failed to cache refreshed Floodgate token for user %s: %v", userIDStr, err)
	}

	log.Printf("Floodgate token refreshed for user %s (expires_in=%ds, has_id_token=%v)", userIDStr, fgTok.ExpiresIn, fgTok.IdToken != "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"floodgate_token": fgCredential,
			"expires_in":      fgTok.ExpiresIn,
			"source":          "exchange",
		},
	})
}

// IDMSExchange redeems a one-time exchange code for JWT tokens.
// GET /api/v1/auth/oidc/exchange
func (h *IDMSHandler) IDMSExchange(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Missing code parameter"})
		return
	}

	data, ok := h.loadOAuthCode(code)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid or expired exchange code"})
		return
	}
	h.deleteOAuthCode(code)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tokens": gin.H{
				"access_token":  data["access_token"],
				"refresh_token": data["refresh_token"],
			},
			"user": gin.H{
				"id":        data["user_id"],
				"email":     data["email"],
				"full_name": data["full_name"],
				"role_name": data["role_name"],
			},
			"redirect":             data["redirect"],
			"idms_token":           data["idms_token"],
			"floodgate_token":      data["floodgate_token"],
			"floodgate_expires_in": func() int {
				if v, err := strconv.Atoi(data["floodgate_expires_in"]); err == nil && v > 0 {
					return v
				}
				return 3300
			}(),
		},
	})
}
