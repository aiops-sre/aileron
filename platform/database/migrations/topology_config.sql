-- Topology Configuration Storage
-- Stores K8s cluster configs, CloudStack endpoints, IPMI settings

CREATE TABLE IF NOT EXISTS topology_config (
    id TEXT PRIMARY KEY DEFAULT 'default',
    config JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Insert default config
INSERT INTO topology_config (id, config, created_at, updated_at)
VALUES (
    'default',
    '{
        "k8s_clusters": [],
        "cloudstack": [],
        "ipmi": [],
        "discovery_interval": "5m",
        "real_time_discovery": true,
        "component_types": {
            "applications": true,
            "services": true,
            "databases": true,
            "containers": true,
            "networks": false,
            "storage": false
        }
    }'::jsonb,
    NOW(),
    NOW()
)
ON CONFLICT (id) DO NOTHING;

-- Create index for faster lookups
CREATE INDEX IF NOT EXISTS idx_topology_config_updated_at ON topology_config(updated_at);

-- Comments
COMMENT ON TABLE topology_config IS 'Stores topology discovery configuration for K8s, CloudStack, IPMI, etc.';
COMMENT ON COLUMN topology_config.config IS 'JSONB configuration containing all infrastructure service connection details';
