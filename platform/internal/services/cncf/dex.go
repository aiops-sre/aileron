package cncf

import (
	"fmt"
	"os"
	"strings"
)

// DexConfig holds the configuration required to integrate with a Dex OIDC provider.
type DexConfig struct {
	// IssuerURL is the base URL of the Dex instance (e.g. "https://dex.example.com").
	IssuerURL string
	// ClientID is the OAuth2 client identifier registered in Dex.
	ClientID string
	// ClientSecret is the OAuth2 client secret registered in Dex.
	ClientSecret string
	// RedirectURI is the callback URI that Dex will redirect to after authentication.
	RedirectURI string
	// GroupsScope requests the "groups" scope, which causes Dex to include group
	// memberships in the ID token claims. Requires a connector that supports groups.
	GroupsScope bool
	// InsecureSkipVerify disables TLS certificate verification for the Dex issuer.
	// Only set true in development environments.
	InsecureSkipVerify bool
}

// DexConfigFromEnv constructs a DexConfig by reading environment variables.
// Variable precedence:
//
//	IssuerURL    — DEX_ISSUER_URL, fallback OIDC_PROVIDER_URL
//	ClientID     — DEX_CLIENT_ID
//	ClientSecret — DEX_CLIENT_SECRET
//	RedirectURI  — DEX_REDIRECT_URI
//	GroupsScope  — DEX_GROUPS_SCOPE ("true" / "1" / "yes" enables the scope)
func DexConfigFromEnv() *DexConfig {
	issuer := os.Getenv("DEX_ISSUER_URL")
	if issuer == "" {
		issuer = os.Getenv("OIDC_PROVIDER_URL")
	}

	groupsScope := false
	if v := os.Getenv("DEX_GROUPS_SCOPE"); v != "" {
		lower := strings.ToLower(strings.TrimSpace(v))
		groupsScope = lower == "true" || lower == "1" || lower == "yes"
	}

	return &DexConfig{
		IssuerURL:    issuer,
		ClientID:     os.Getenv("DEX_CLIENT_ID"),
		ClientSecret: os.Getenv("DEX_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("DEX_REDIRECT_URI"),
		GroupsScope:  groupsScope,
	}
}

// IsConfigured returns true when the minimum required Dex configuration is present.
// An empty IssuerURL means Dex integration is disabled.
func (c *DexConfig) IsConfigured() bool {
	return c.IssuerURL != ""
}

// DexStaticClientYAML returns a YAML snippet suitable for inclusion in the
// staticClients section of a Dex configuration file.
//
// Example output:
//
//	- id: my-app
//	  secret: s3cret
//	  redirectURIs:
//	    - https://app.example.com/callback
//	  name: my-app
//	  public: false
func DexStaticClientYAML(clientID, clientSecret, redirectURI string) string {
	return fmt.Sprintf(`- id: %s
  secret: %s
  redirectURIs:
    - %s
  name: %s
  public: false
`, clientID, clientSecret, redirectURI, clientID)
}

// AuthProviderRegistry holds the list of OIDC/OAuth2 providers that the Aileron
// platform can integrate with via Dex connectors or direct OIDC configuration.
type AuthProviderRegistry struct {
	providers []string
}

// NewAuthProviderRegistry constructs an AuthProviderRegistry pre-populated with
// all supported providers.
func NewAuthProviderRegistry() *AuthProviderRegistry {
	return &AuthProviderRegistry{
		providers: []string{
			"dex",
			"pinniped",
			"zitadel",
			"keycloak",
			"authelia",
			"casdoor",
			"github",
			"google",
			"microsoft",
			"okta",
			"auth0",
		},
	}
}

// ListProviders returns a copy of the list of all supported OIDC/OAuth2 provider names.
func (r *AuthProviderRegistry) ListProviders() []string {
	out := make([]string, len(r.providers))
	copy(out, r.providers)
	return out
}
