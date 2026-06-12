-- Generic OAuth 2.0 provider configuration
CREATE TABLE IF NOT EXISTS oauth2_providers (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(64)  UNIQUE NOT NULL,           -- slug used in URLs, e.g. "google"
    display_name        VARCHAR(128) NOT NULL,                  -- shown on login button
    client_id           VARCHAR(256) NOT NULL,
    client_secret       VARCHAR(512) NOT NULL,
    auth_url            VARCHAR(512) NOT NULL,                  -- authorization endpoint
    token_url           VARCHAR(512) NOT NULL,                  -- token endpoint
    userinfo_url        VARCHAR(512) NOT NULL DEFAULT '',       -- OIDC userinfo endpoint
    scopes              TEXT[]       NOT NULL DEFAULT '{}',
    icon_url            VARCHAR(512) NOT NULL DEFAULT '',       -- optional branding icon
    enabled             BOOLEAN      NOT NULL DEFAULT true,
    auto_provision      BOOLEAN      NOT NULL DEFAULT true,     -- create users on first login
    default_role        VARCHAR(64)  NOT NULL DEFAULT 'viewer', -- role assigned to new users
    email_domain_filter VARCHAR(256) NOT NULL DEFAULT '',       -- e.g. "example.com" to restrict logins
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
