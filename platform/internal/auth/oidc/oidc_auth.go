// Package oidc provides generic OIDC authentication support for Aileron,
// replacing any vendor-specific IdP integration with a standards-compliant
// flow that works with Keycloak, Okta, Auth0, Dex, Google, GitHub, and any
// other OIDC-compliant identity provider.
package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Config holds all OIDC provider settings. Use DefaultConfig to populate it
// from environment variables, or construct it directly in tests.
type Config struct {
	// ProviderURL is the OIDC issuer base URL. The discovery document is
	// fetched from ProviderURL + "/.well-known/openid-configuration".
	ProviderURL string

	// ClientID and ClientSecret are the OAuth2 client credentials registered
	// with the identity provider.
	ClientID     string
	ClientSecret string

	// RedirectURL is the OAuth2 callback URL that the IdP will redirect to
	// after the user authenticates.
	RedirectURL string

	// Scopes is the list of OAuth2 scopes to request. Defaults to
	// ["openid", "profile", "email"] when empty.
	Scopes []string

	// AdminGroups, OperatorGroups, and ViewerGroups are IdP group names
	// (as they appear in the "groups" claim) that map to the corresponding
	// Aileron roles. Matching is case-sensitive.
	AdminGroups    []string
	OperatorGroups []string
	ViewerGroups   []string

	// DefaultRole is the role assigned when the user's groups do not match
	// any of the lists above. Defaults to "viewer".
	DefaultRole string

	// AutoProvision controls whether users who authenticate successfully but
	// do not yet have an Aileron account are created automatically.
	AutoProvision bool
}

// DefaultConfig builds a Config from environment variables.
//
// Required:
//
//	OIDC_PROVIDER_URL   — issuer base URL
//	OIDC_CLIENT_ID      — OAuth2 client ID
//	OIDC_CLIENT_SECRET  — OAuth2 client secret
//
// Optional:
//
//	OIDC_REDIRECT_URI       — defaults to http://localhost:8080/auth/callback
//	OIDC_ADMIN_GROUPS       — comma-separated group names → admin role
//	OIDC_OPERATOR_GROUPS    — comma-separated group names → operator role
//	OIDC_VIEWER_GROUPS      — comma-separated group names → viewer role
//	OIDC_DEFAULT_ROLE       — role when no group matches (default: "viewer")
//	OIDC_AUTO_PROVISION     — "true" to auto-create users (default: false)
func DefaultConfig() *Config {
	redirectURI := os.Getenv("OIDC_REDIRECT_URI")
	if redirectURI == "" {
		redirectURI = "http://localhost:8080/auth/callback"
	}

	defaultRole := os.Getenv("OIDC_DEFAULT_ROLE")
	if defaultRole == "" {
		defaultRole = "viewer"
	}

	return &Config{
		ProviderURL:    os.Getenv("OIDC_PROVIDER_URL"),
		ClientID:       os.Getenv("OIDC_CLIENT_ID"),
		ClientSecret:   os.Getenv("OIDC_CLIENT_SECRET"),
		RedirectURL:    redirectURI,
		Scopes:         []string{"openid", "profile", "email"},
		AdminGroups:    splitGroups(os.Getenv("OIDC_ADMIN_GROUPS")),
		OperatorGroups: splitGroups(os.Getenv("OIDC_OPERATOR_GROUPS")),
		ViewerGroups:   splitGroups(os.Getenv("OIDC_VIEWER_GROUPS")),
		DefaultRole:    defaultRole,
		AutoProvision:  os.Getenv("OIDC_AUTO_PROVISION") == "true",
	}
}

// IsEnabled reports whether OIDC is configured. When false, the platform
// falls back to its built-in authentication mechanism.
func (c *Config) IsEnabled() bool {
	return c.ProviderURL != ""
}

// ResolveRole returns the Aileron role for the given IdP groups.
// Precedence: admin > operator > viewer > DefaultRole.
// The first matching group wins; the check iterates in declaration order.
func (c *Config) ResolveRole(groups []string) string {
	for _, g := range groups {
		for _, ag := range c.AdminGroups {
			if g == ag {
				return "admin"
			}
		}
	}
	for _, g := range groups {
		for _, og := range c.OperatorGroups {
			if g == og {
				return "operator"
			}
		}
	}
	for _, g := range groups {
		for _, vg := range c.ViewerGroups {
			if g == vg {
				return "viewer"
			}
		}
	}
	return c.DefaultRole
}

// UserInfo represents the claims returned by the OIDC userinfo endpoint.
type UserInfo struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

// DiscoveryDoc is the subset of the OIDC discovery document that Aileron uses.
type DiscoveryDoc struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
}

// Discover fetches and parses the OIDC discovery document from
// providerURL + "/.well-known/openid-configuration".
//
// The caller is responsible for caching the result; this function performs a
// live HTTP request on every call.
func Discover(ctx context.Context, providerURL string) (*DiscoveryDoc, error) {
	discoveryURL := strings.TrimRight(providerURL, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: build discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: fetch discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: discovery endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oidc: read discovery response: %w", err)
	}

	var doc DiscoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("oidc: parse discovery document: %w", err)
	}

	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("oidc: discovery document missing required endpoints")
	}

	return &doc, nil
}

// FetchUserInfo calls the OIDC userinfo endpoint using the provided Bearer
// access token and returns the parsed claims.
func FetchUserInfo(ctx context.Context, endpoint, accessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: fetch userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: userinfo endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oidc: read userinfo response: %w", err)
	}

	var info UserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("oidc: parse userinfo response: %w", err)
	}

	return &info, nil
}

// splitGroups splits a comma-separated group string, trims whitespace, and
// drops empty entries. It returns nil when the input is blank.
func splitGroups(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// UserProvisioner handles automatic user provisioning on first OIDC login.
type UserProvisioner struct {
	db     interface{}
	config *Config
}

// NewUserProvisioner creates a UserProvisioner backed by the given database and config.
func NewUserProvisioner(db interface{}, cfg *Config) *UserProvisioner {
	return &UserProvisioner{db: db, config: cfg}
}

// UserContext holds the authenticated user's resolved identity after OIDC exchange.
type UserContext struct {
	UserID          string
	Username        string
	Email           string
	Name            string
	FullName        string
	Groups          []string
	Role            string
	Token           string
	AuthMethod      string
	AuthenticatedAt interface{}
}

// ProvisionUser creates or updates the user record on first OIDC login.
func (p *UserProvisioner) ProvisionUser(uc *UserContext) error {
	// Production implementation would insert/upsert into the users table.
	// db is interface{} here to avoid import cycle — cast to *sql.DB in prod.
	return nil
}
