-- Create integrations table
CREATE TABLE IF NOT EXISTS integrations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    enabled BOOLEAN DEFAULT false,
    status VARCHAR(50) DEFAULT 'disconnected',
    last_sync TIMESTAMP,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create index for faster queries
CREATE INDEX IF NOT EXISTS idx_integrations_type ON integrations(type);
CREATE INDEX IF NOT EXISTS idx_integrations_enabled ON integrations(enabled);
CREATE INDEX IF NOT EXISTS idx_integrations_status ON integrations(status);

-- Create auth_providers table
CREATE TABLE IF NOT EXISTS auth_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL, -- 'oauth' or 'saml'
    provider VARCHAR(50) NOT NULL, -- 'generic', 'google', 'microsoft', 'okta', 'auth0'
    enabled BOOLEAN DEFAULT false,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create index for faster queries
CREATE INDEX IF NOT EXISTS idx_auth_providers_type ON auth_providers(type);
CREATE INDEX IF NOT EXISTS idx_auth_providers_enabled ON auth_providers(enabled);

-- Add comments for documentation
COMMENT ON TABLE integrations IS 'Stores integration configurations for monitoring tools and services';
COMMENT ON TABLE auth_providers IS 'Stores OAuth and SAML authentication provider configurations';

COMMENT ON COLUMN integrations.type IS 'Integration type: prometheus, dynatrace, grafana, webhook, slack, pagerduty, jira, self';
COMMENT ON COLUMN integrations.status IS 'Connection status: connected, disconnected, error';
COMMENT ON COLUMN integrations.config IS 'JSON configuration specific to integration type';

COMMENT ON COLUMN auth_providers.type IS 'Authentication type: oauth, saml';
COMMENT ON COLUMN auth_providers.provider IS 'Provider preset: generic, google, microsoft, okta, auth0';
COMMENT ON COLUMN auth_providers.config IS 'JSON configuration for the auth provider';
