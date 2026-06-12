package sso

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	ErrLDAPConnectionFailed = errors.New("LDAP connection failed")
	ErrLDAPAuthFailed       = errors.New("LDAP authentication failed")
	ErrSAMLAuthFailed       = errors.New("SAML authentication failed")
	ErrOAuthFailed          = errors.New("OAuth authentication failed")
)

// SSOConfig holds SSO configuration
type SSOConfig struct {
	// LDAP Configuration
	LDAPEnabled    bool   `json:"ldap_enabled"`
	LDAPServer     string `json:"ldap_server"`
	LDAPPort       int    `json:"ldap_port"`
	LDAPBaseDN     string `json:"ldap_base_dn"`
	LDAPBindDN     string `json:"ldap_bind_dn"`
	LDAPBindPass   string `json:"-"`
	LDAPUserFilter string `json:"ldap_user_filter"`
	LDAPUseTLS     bool   `json:"ldap_use_tls"`

	// SAML Configuration
	SAMLEnabled     bool   `json:"saml_enabled"`
	SAMLEntityID    string `json:"saml_entity_id"`
	SAMLMetadataURL string `json:"saml_metadata_url"`
	SAMLCertificate string `json:"-"`
	SAMLPrivateKey  string `json:"-"`

	// OAuth Configuration
	OAuthEnabled      bool     `json:"oauth_enabled"`
	OAuthProvider     string   `json:"oauth_provider"`
	OAuthClientID     string   `json:"oauth_client_id"`
	OAuthClientSecret string   `json:"-"`
	OAuthRedirectURL  string   `json:"oauth_redirect_url"`
	OAuthScopes       []string `json:"oauth_scopes"`
}

// LDAPUser represents a user from LDAP
type LDAPUser struct {
	Username   string
	Email      string
	FullName   string
	Groups     []string
	Attributes map[string]string
}

// SSOService handles SSO authentication
type SSOService struct {
	config *SSOConfig
}

// NewSSOService creates a new SSO service
func NewSSOService(config *SSOConfig) *SSOService {
	return &SSOService{config: config}
}

// AuthenticateLDAP authenticates a user via LDAP
func (s *SSOService) AuthenticateLDAP(ctx context.Context, username, password string) (*LDAPUser, error) {
	if !s.config.LDAPEnabled {
		return nil, errors.New("LDAP authentication is not enabled")
	}

	// Connect to LDAP server
	serverAddr := fmt.Sprintf("%s:%d", s.config.LDAPServer, s.config.LDAPPort)

	var conn *ldap.Conn
	var err error

	if s.config.LDAPUseTLS {
		tlsConfig := &tls.Config{
			ServerName: s.config.LDAPServer,
		}
		conn, err = ldap.DialTLS("tcp", serverAddr, tlsConfig)
	} else {
		conn, err = ldap.Dial("tcp", serverAddr)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLDAPConnectionFailed, err)
	}
	defer conn.Close()

	// Bind with service account
	if s.config.LDAPBindDN != "" {
		err = conn.Bind(s.config.LDAPBindDN, s.config.LDAPBindPass)
		if err != nil {
			return nil, fmt.Errorf("LDAP bind failed: %v", err)
		}
	}

	// Search for user
	searchFilter := fmt.Sprintf(s.config.LDAPUserFilter, username)
	searchRequest := ldap.NewSearchRequest(
		s.config.LDAPBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0,
		0,
		false,
		searchFilter,
		[]string{"dn", "cn", "mail", "displayName", "memberOf", "sAMAccountName"},
		nil,
	)

	searchResult, err := conn.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("LDAP search failed: %v", err)
	}

	if len(searchResult.Entries) == 0 {
		return nil, ErrLDAPAuthFailed
	}

	userEntry := searchResult.Entries[0]
	userDN := userEntry.DN

	// Authenticate user
	err = conn.Bind(userDN, password)
	if err != nil {
		return nil, ErrLDAPAuthFailed
	}

	// Extract user information
	ldapUser := &LDAPUser{
		Username:   username,
		Email:      userEntry.GetAttributeValue("mail"),
		FullName:   userEntry.GetAttributeValue("displayName"),
		Groups:     userEntry.GetAttributeValues("memberOf"),
		Attributes: make(map[string]string),
	}

	// Extract additional attributes
	for _, attr := range userEntry.Attributes {
		if len(attr.Values) > 0 {
			ldapUser.Attributes[attr.Name] = attr.Values[0]
		}
	}

	return ldapUser, nil
}

// MapLDAPGroupsToRole maps LDAP groups to application roles
func (s *SSOService) MapLDAPGroupsToRole(groups []string) string {
	// Define group to role mapping
	groupRoleMap := map[string]string{
		"CN=AlertHub-Admins,OU=Groups,DC=apple,DC=com":    "admin",
		"CN=AlertHub-Managers,OU=Groups,DC=apple,DC=com":  "manager",
		"CN=AlertHub-Engineers,OU=Groups,DC=apple,DC=com": "engineer",
		"CN=AlertHub-Viewers,OU=Groups,DC=apple,DC=com":   "viewer",
	}

	// Check groups in priority order
	for _, group := range groups {
		if role, exists := groupRoleMap[group]; exists {
			return role
		}
	}

	// Default to viewer role
	return "viewer"
}

// SAMLAuthRequest represents a SAML authentication request
type SAMLAuthRequest struct {
	SAMLResponse string
	RelayState   string
}

// SAMLUser represents a user from SAML
type SAMLUser struct {
	NameID     string
	Email      string
	FullName   string
	Groups     []string
	Attributes map[string]string
}

// SAMLResponse represents a simplified SAML response structure
type SAMLResponse struct {
	XMLName   xml.Name `xml:"Response"`
	Assertion SAMLAssertion
}

type SAMLAssertion struct {
	XMLName            xml.Name `xml:"Assertion"`
	Subject            SAMLSubject
	AttributeStatement SAMLAttributeStatement
}

type SAMLSubject struct {
	XMLName xml.Name `xml:"Subject"`
	NameID  SAMLNameID
}

type SAMLNameID struct {
	XMLName xml.Name `xml:"NameID"`
	Value   string   `xml:",chardata"`
}

type SAMLAttributeStatement struct {
	XMLName    xml.Name `xml:"AttributeStatement"`
	Attributes []SAMLAttribute
}

type SAMLAttribute struct {
	XMLName        xml.Name `xml:"Attribute"`
	Name           string   `xml:"Name,attr"`
	AttributeValue []string `xml:"AttributeValue"`
}

// AuthenticateSAML authenticates a user via SAML
func (s *SSOService) AuthenticateSAML(ctx context.Context, samlResponse string) (*SAMLUser, error) {
	if !s.config.SAMLEnabled {
		return nil, errors.New("SAML authentication is not enabled")
	}

	// Decode base64 SAML response (if needed)
	// In production, you'd use github.com/crewjam/saml or similar library
	// This is a simplified implementation

	var samlResp SAMLResponse
	err := xml.Unmarshal([]byte(samlResponse), &samlResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SAML response: %v", err)
	}

	// Extract user information
	samlUser := &SAMLUser{
		NameID:     samlResp.Assertion.Subject.NameID.Value,
		Attributes: make(map[string]string),
	}

	// Parse attributes
	for _, attr := range samlResp.Assertion.AttributeStatement.Attributes {
		if len(attr.AttributeValue) > 0 {
			switch strings.ToLower(attr.Name) {
			case "email", "emailaddress", "mail":
				samlUser.Email = attr.AttributeValue[0]
			case "name", "displayname", "fullname":
				samlUser.FullName = attr.AttributeValue[0]
			case "groups", "memberof":
				samlUser.Groups = attr.AttributeValue
			default:
				samlUser.Attributes[attr.Name] = attr.AttributeValue[0]
			}
		}
	}

	// Validate required fields
	if samlUser.Email == "" {
		return nil, errors.New("SAML response missing email attribute")
	}

	return samlUser, nil
}

// OAuthConfig holds OAuth provider configuration
type OAuthConfig struct {
	Provider     string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
}

// OAuthUser represents a user from OAuth
type OAuthUser struct {
	ID       string
	Email    string
	Name     string
	Picture  string
	Verified bool
	Provider string
}

// GetOAuthAuthURL generates OAuth authorization URL
func (s *SSOService) GetOAuthAuthURL(state string) (string, error) {
	if !s.config.OAuthEnabled {
		return "", errors.New("OAuth is not enabled")
	}

	// Build OAuth URL based on provider
	switch s.config.OAuthProvider {
	case "google":
		return fmt.Sprintf(
			"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
			s.config.OAuthClientID,
			s.config.OAuthRedirectURL,
			"openid email profile",
			state,
		), nil
	case "okta":
		// Okta OAuth URL
		return "", errors.New("Okta OAuth not yet implemented")
	case "azure":
		// Azure AD OAuth URL
		return "", errors.New("Azure AD OAuth not yet implemented")
	default:
		return "", fmt.Errorf("unsupported OAuth provider: %s", s.config.OAuthProvider)
	}
}

// AuthenticateOAuth authenticates a user via OAuth
func (s *SSOService) AuthenticateOAuth(ctx context.Context, code string) (*OAuthUser, error) {
	if !s.config.OAuthEnabled {
		return nil, errors.New("OAuth authentication is not enabled")
	}

	// Get OAuth configuration based on provider
	var oauthConfig *oauth2.Config
	var userInfoURL string

	switch s.config.OAuthProvider {
	case "google":
		oauthConfig = &oauth2.Config{
			ClientID:     s.config.OAuthClientID,
			ClientSecret: s.config.OAuthClientSecret,
			RedirectURL:  s.config.OAuthRedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}
		userInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"

	case "github":
		oauthConfig = &oauth2.Config{
			ClientID:     s.config.OAuthClientID,
			ClientSecret: s.config.OAuthClientSecret,
			RedirectURL:  s.config.OAuthRedirectURL,
			Scopes:       []string{"user:email", "read:user"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://github.com/login/oauth/authorize",
				TokenURL: "https://github.com/login/oauth/access_token",
			},
		}
		userInfoURL = "https://api.github.com/user"

	case "azure":
		// Azure AD / Microsoft OAuth
		tenantID := "common" // or specific tenant ID
		oauthConfig = &oauth2.Config{
			ClientID:     s.config.OAuthClientID,
			ClientSecret: s.config.OAuthClientSecret,
			RedirectURL:  s.config.OAuthRedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenantID),
				TokenURL: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
			},
		}
		userInfoURL = "https://graph.microsoft.com/v1.0/me"

	default:
		return nil, fmt.Errorf("unsupported OAuth provider: %s", s.config.OAuthProvider)
	}

	// Exchange authorization code for token
	token, err := oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("OAuth token exchange failed: %v", err)
	}

	// Get user info
	client := oauthConfig.Client(ctx, token)
	resp, err := client.Get(userInfoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read user info response: %v", err)
	}

	// Parse user info based on provider
	oauthUser := &OAuthUser{
		Provider: s.config.OAuthProvider,
	}

	var userInfo map[string]interface{}
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %v", err)
	}

	// Extract common fields (varies by provider)
	if id, ok := userInfo["id"].(string); ok {
		oauthUser.ID = id
	} else if sub, ok := userInfo["sub"].(string); ok {
		oauthUser.ID = sub
	}

	if email, ok := userInfo["email"].(string); ok {
		oauthUser.Email = email
	}

	if name, ok := userInfo["name"].(string); ok {
		oauthUser.Name = name
	}

	if picture, ok := userInfo["picture"].(string); ok {
		oauthUser.Picture = picture
	} else if avatarURL, ok := userInfo["avatar_url"].(string); ok {
		oauthUser.Picture = avatarURL
	}

	if verified, ok := userInfo["email_verified"].(bool); ok {
		oauthUser.Verified = verified
	} else {
		oauthUser.Verified = true // Assume verified if not specified
	}

	return oauthUser, nil
}

// SyncUserFromSSO synchronizes user from SSO provider to local database
type UserSyncFunc func(ctx context.Context, ssoUser interface{}) (uuid.UUID, error)

// SSOAuthResult represents the result of SSO authentication
type SSOAuthResult struct {
	UserID      uuid.UUID
	Username    string
	Email       string
	FullName    string
	Role        string
	IsNewUser   bool
	SSOProvider string
	SSOUserID   string
}

// AuthenticateWithSSO is a unified SSO authentication method
func (s *SSOService) AuthenticateWithSSO(ctx context.Context, provider, credential string, syncFunc UserSyncFunc) (*SSOAuthResult, error) {
	switch provider {
	case "ldap":
		// credential format: "username:password"
		// Parse and authenticate
		return nil, errors.New("use AuthenticateLDAP directly")

	case "saml":
		// credential is SAML response
		samlUser, err := s.AuthenticateSAML(ctx, credential)
		if err != nil {
			return nil, err
		}

		// Sync user to database
		userID, err := syncFunc(ctx, samlUser)
		if err != nil {
			return nil, err
		}

		return &SSOAuthResult{
			UserID:      userID,
			Email:       samlUser.Email,
			FullName:    samlUser.FullName,
			SSOProvider: "saml",
			SSOUserID:   samlUser.NameID,
		}, nil

	case "oauth":
		// credential is OAuth code
		oauthUser, err := s.AuthenticateOAuth(ctx, credential)
		if err != nil {
			return nil, err
		}

		// Sync user to database
		userID, err := syncFunc(ctx, oauthUser)
		if err != nil {
			return nil, err
		}

		return &SSOAuthResult{
			UserID:      userID,
			Email:       oauthUser.Email,
			FullName:    oauthUser.Name,
			SSOProvider: s.config.OAuthProvider,
			SSOUserID:   oauthUser.ID,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported SSO provider: %s", provider)
	}
}

// ValidateLDAPConnection tests LDAP connection
func (s *SSOService) ValidateLDAPConnection() error {
	if !s.config.LDAPEnabled {
		return errors.New("LDAP is not enabled")
	}

	serverAddr := fmt.Sprintf("%s:%d", s.config.LDAPServer, s.config.LDAPPort)

	var conn *ldap.Conn
	var err error

	if s.config.LDAPUseTLS {
		tlsConfig := &tls.Config{
			ServerName: s.config.LDAPServer,
		}
		conn, err = ldap.DialTLS("tcp", serverAddr, tlsConfig)
	} else {
		conn, err = ldap.Dial("tcp", serverAddr)
	}

	if err != nil {
		return fmt.Errorf("%w: %v", ErrLDAPConnectionFailed, err)
	}
	defer conn.Close()

	// Test bind
	if s.config.LDAPBindDN != "" {
		err = conn.Bind(s.config.LDAPBindDN, s.config.LDAPBindPass)
		if err != nil {
			return fmt.Errorf("LDAP bind test failed: %v", err)
		}
	}

	return nil
}

// SSOProvider interface for different SSO implementations
type SSOProvider interface {
	Authenticate(ctx context.Context, credentials map[string]string) (interface{}, error)
	GetUserInfo(ctx context.Context, token string) (interface{}, error)
	ValidateConfig() error
}

// LDAPProvider implements LDAP authentication
type LDAPProvider struct {
	config *SSOConfig
}

// NewLDAPProvider creates a new LDAP provider
func NewLDAPProvider(config *SSOConfig) *LDAPProvider {
	return &LDAPProvider{config: config}
}

// Authenticate authenticates via LDAP
func (p *LDAPProvider) Authenticate(ctx context.Context, credentials map[string]string) (interface{}, error) {
	username := credentials["username"]
	password := credentials["password"]

	ssoService := NewSSOService(p.config)
	return ssoService.AuthenticateLDAP(ctx, username, password)
}

// GetUserInfo retrieves user info (not applicable for LDAP)
func (p *LDAPProvider) GetUserInfo(ctx context.Context, token string) (interface{}, error) {
	return nil, errors.New("GetUserInfo not supported for LDAP")
}

// ValidateConfig validates LDAP configuration
func (p *LDAPProvider) ValidateConfig() error {
	if p.config.LDAPServer == "" {
		return errors.New("LDAP server is required")
	}
	if p.config.LDAPPort == 0 {
		return errors.New("LDAP port is required")
	}
	if p.config.LDAPBaseDN == "" {
		return errors.New("LDAP base DN is required")
	}
	return nil
}

// SAMLProvider implements SAML authentication
type SAMLProvider struct {
	config *SSOConfig
}

// NewSAMLProvider creates a new SAML provider
func NewSAMLProvider(config *SSOConfig) *SAMLProvider {
	return &SAMLProvider{config: config}
}

// Authenticate authenticates via SAML
func (p *SAMLProvider) Authenticate(ctx context.Context, credentials map[string]string) (interface{}, error) {
	samlResponse := credentials["saml_response"]

	ssoService := NewSSOService(p.config)
	return ssoService.AuthenticateSAML(ctx, samlResponse)
}

// GetUserInfo retrieves user info from SAML assertion
func (p *SAMLProvider) GetUserInfo(ctx context.Context, token string) (interface{}, error) {
	// For SAML, the user info is typically included in the assertion itself
	// This method would parse a stored assertion token
	ssoService := NewSSOService(p.config)
	return ssoService.AuthenticateSAML(ctx, token)
}

// ValidateConfig validates SAML configuration
func (p *SAMLProvider) ValidateConfig() error {
	if p.config.SAMLEntityID == "" {
		return errors.New("SAML entity ID is required")
	}
	if p.config.SAMLMetadataURL == "" {
		return errors.New("SAML metadata URL is required")
	}
	return nil
}

// OAuthProvider implements OAuth authentication
type OAuthProvider struct {
	config *SSOConfig
}

// NewOAuthProvider creates a new OAuth provider
func NewOAuthProvider(config *SSOConfig) *OAuthProvider {
	return &OAuthProvider{config: config}
}

// Authenticate authenticates via OAuth
func (p *OAuthProvider) Authenticate(ctx context.Context, credentials map[string]string) (interface{}, error) {
	code := credentials["code"]

	ssoService := NewSSOService(p.config)
	return ssoService.AuthenticateOAuth(ctx, code)
}

// GetUserInfo retrieves user info from OAuth provider
func (p *OAuthProvider) GetUserInfo(ctx context.Context, accessToken string) (interface{}, error) {
	var userInfoURL string

	switch p.config.OAuthProvider {
	case "google":
		userInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
	case "github":
		userInfoURL = "https://api.github.com/user"
	case "azure":
		userInfoURL = "https://graph.microsoft.com/v1.0/me"
	default:
		return nil, fmt.Errorf("unsupported OAuth provider: %s", p.config.OAuthProvider)
	}

	// Create HTTP request with bearer token
	req, err := http.NewRequest("GET", userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var userInfo map[string]interface{}
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, err
	}

	// Convert to OAuthUser
	oauthUser := &OAuthUser{
		Provider: p.config.OAuthProvider,
	}

	if id, ok := userInfo["id"].(string); ok {
		oauthUser.ID = id
	} else if sub, ok := userInfo["sub"].(string); ok {
		oauthUser.ID = sub
	}

	if email, ok := userInfo["email"].(string); ok {
		oauthUser.Email = email
	}

	if name, ok := userInfo["name"].(string); ok {
		oauthUser.Name = name
	}

	if picture, ok := userInfo["picture"].(string); ok {
		oauthUser.Picture = picture
	} else if avatarURL, ok := userInfo["avatar_url"].(string); ok {
		oauthUser.Picture = avatarURL
	}

	if verified, ok := userInfo["email_verified"].(bool); ok {
		oauthUser.Verified = verified
	} else {
		oauthUser.Verified = true
	}

	return oauthUser, nil
}

// ValidateConfig validates OAuth configuration
func (p *OAuthProvider) ValidateConfig() error {
	if p.config.OAuthClientID == "" {
		return errors.New("OAuth client ID is required")
	}
	if p.config.OAuthClientSecret == "" {
		return errors.New("OAuth client secret is required")
	}
	if p.config.OAuthRedirectURL == "" {
		return errors.New("OAuth redirect URL is required")
	}
	return nil
}

// SSOManager manages multiple SSO providers
type SSOManager struct {
	providers map[string]SSOProvider
	config    *SSOConfig
}

// NewSSOManager creates a new SSO manager
func NewSSOManager(config *SSOConfig) *SSOManager {
	manager := &SSOManager{
		providers: make(map[string]SSOProvider),
		config:    config,
	}

	// Register enabled providers
	if config.LDAPEnabled {
		manager.providers["ldap"] = NewLDAPProvider(config)
	}
	if config.SAMLEnabled {
		manager.providers["saml"] = NewSAMLProvider(config)
	}
	if config.OAuthEnabled {
		manager.providers["oauth"] = NewOAuthProvider(config)
	}

	return manager
}

// GetProvider returns a specific SSO provider
func (m *SSOManager) GetProvider(name string) (SSOProvider, error) {
	provider, exists := m.providers[name]
	if !exists {
		return nil, fmt.Errorf("SSO provider '%s' not found or not enabled", name)
	}
	return provider, nil
}

// ListEnabledProviders returns list of enabled SSO providers
func (m *SSOManager) ListEnabledProviders() []string {
	var providers []string
	for name := range m.providers {
		providers = append(providers, name)
	}
	return providers
}

// SSOAuditLog represents an SSO authentication audit log
type SSOAuditLog struct {
	ID        uuid.UUID
	Provider  string
	Username  string
	Email     string
	Success   bool
	ErrorMsg  string
	IPAddress string
	UserAgent string
	Timestamp time.Time
}

// LogSSOAttempt logs an SSO authentication attempt
func (m *SSOManager) LogSSOAttempt(provider, username, email string, success bool, errorMsg, ipAddress, userAgent string) *SSOAuditLog {
	return &SSOAuditLog{
		ID:        uuid.New(),
		Provider:  provider,
		Username:  username,
		Email:     email,
		Success:   success,
		ErrorMsg:  errorMsg,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Timestamp: time.Now(),
	}
}
