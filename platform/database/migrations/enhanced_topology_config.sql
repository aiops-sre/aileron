-- Enhanced topology configuration schema
-- Supports simple K8s readonly cluster configs and detailed CloudStack APIs

-- Drop and recreate topology_config table with better structure
DROP TABLE IF EXISTS topology_config CASCADE;

CREATE TABLE topology_config (
    id TEXT PRIMARY KEY DEFAULT 'default',
    config JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Enhanced K8s cluster configuration table for readonly configs
CREATE TABLE IF NOT EXISTS k8s_cluster_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255),
    api_server VARCHAR(500) NOT NULL,
    region VARCHAR(100) DEFAULT 'reno',
    environment VARCHAR(100) DEFAULT 'production',
    version VARCHAR(50),
    node_count INTEGER DEFAULT 0,
    namespace_count INTEGER DEFAULT 0,
    pod_count INTEGER DEFAULT 0,
    status VARCHAR(50) DEFAULT 'unknown',
    readonly_mode BOOLEAN DEFAULT true,
    discovery_enabled BOOLEAN DEFAULT true,
    labels JSONB DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    last_discovery TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    UNIQUE(cluster_name, region)
);

-- CloudStack configuration table for detailed API configs
CREATE TABLE IF NOT EXISTS cloudstack_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255),
    endpoint VARCHAR(500) NOT NULL,
    api_key VARCHAR(500) NOT NULL,
    secret_key VARCHAR(500) NOT NULL,
    region VARCHAR(100) DEFAULT 'reno',
    zone VARCHAR(100),
    status VARCHAR(50) DEFAULT 'unknown',
    host_count INTEGER DEFAULT 0,
    vm_count INTEGER DEFAULT 0,
    last_discovery TIMESTAMP,
    discovery_enabled BOOLEAN DEFAULT true,
    connection_timeout INTEGER DEFAULT 30,
    labels JSONB DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    UNIQUE(cluster_name, region)
);

-- Insert default configuration with enhanced structure
INSERT INTO topology_config (id, config, created_at, updated_at)
VALUES (
    'default',
    '{
        "version": "2.0",
        "discovery_settings": {
            "discovery_interval": "5m",
            "real_time_discovery": true,
            "parallel_discovery": true,
            "connection_timeout": 30,
            "retry_attempts": 3
        },
        "cloudstack": [],
        "k8s_clusters": [],
        "component_types": {
            "applications": true,
            "services": true,
            "databases": true,
            "containers": true,
            "networks": true,
            "storage": true
        },
        "regions": ["reno", "maiden"],
        "auto_discovery": {
            "enabled": true,
            "scan_interval": "15m"
        }
    }'::jsonb,
    NOW(),
    NOW()
)
ON CONFLICT (id) DO UPDATE SET 
    config = EXCLUDED.config,
    updated_at = NOW();

-- Sample K8s cluster configurations (readonly)
INSERT INTO k8s_cluster_configs (cluster_name, display_name, api_server, region, version, readonly_mode) VALUES
('mps-sandbox-rno', 'MPS Sandbox Reno', 'https://k8s-api-reno.internal:6443', 'reno', '1.28.2', true),
('prod-cluster-rno', 'Production Reno', 'https://k8s-prod-reno.internal:6443', 'reno', '1.28.2', true),
('dev-cluster-rno', 'Development Reno', 'https://k8s-dev-reno.internal:6443', 'reno', '1.27.8', true),
('prod-cluster-maiden', 'Production Maiden', 'https://k8s-prod-maiden.internal:6443', 'maiden', '1.28.2', true),
('dev-cluster-maiden', 'Development Maiden', 'https://k8s-dev-maiden.internal:6443', 'maiden', '1.27.8', true)
ON CONFLICT (cluster_name, region) DO NOTHING;

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_region ON k8s_cluster_configs(region);
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_status ON k8s_cluster_configs(status);
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_discovery_enabled ON k8s_cluster_configs(discovery_enabled) WHERE discovery_enabled = true;

CREATE INDEX IF NOT EXISTS idx_cloudstack_configs_region ON cloudstack_configs(region);
CREATE INDEX IF NOT EXISTS idx_cloudstack_configs_status ON cloudstack_configs(status);
CREATE INDEX IF NOT EXISTS idx_cloudstack_configs_discovery_enabled ON cloudstack_configs(discovery_enabled) WHERE discovery_enabled = true;

-- Update triggers
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_k8s_cluster_configs_updated_at BEFORE UPDATE ON k8s_cluster_configs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_cloudstack_configs_updated_at BEFORE UPDATE ON cloudstack_configs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_topology_config_updated_at BEFORE UPDATE ON topology_config FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Comments
COMMENT ON TABLE k8s_cluster_configs IS 'Kubernetes cluster configurations for topology discovery (readonly mode)';
COMMENT ON TABLE cloudstack_configs IS 'CloudStack API configurations for infrastructure discovery';
COMMENT ON COLUMN k8s_cluster_configs.readonly_mode IS 'If true, only displays cluster info without requiring API tokens';
COMMENT ON COLUMN cloudstack_configs.api_key IS 'CloudStack API key for authentication';
COMMENT ON COLUMN cloudstack_configs.secret_key IS 'CloudStack secret key for HMAC signature';