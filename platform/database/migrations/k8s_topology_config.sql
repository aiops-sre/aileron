-- K8s Cluster Configuration Database Schema
-- This migration adds support for Kubernetes topology discovery using service account configs

-- Create the k8s cluster configurations table
CREATE TABLE IF NOT EXISTS k8s_cluster_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    environment VARCHAR(100) NOT NULL DEFAULT 'production',
    region VARCHAR(100) NOT NULL DEFAULT 'default',
    api_server_url VARCHAR(500) NOT NULL,
    service_account_token TEXT NOT NULL,
    ca_cert_data TEXT,
    namespace VARCHAR(100) DEFAULT 'default',
    enabled BOOLEAN DEFAULT TRUE,
    last_sync TIMESTAMP WITH TIME ZONE,
    sync_status VARCHAR(50) DEFAULT 'pending',
    sync_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_name ON k8s_cluster_configs(name);
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_enabled ON k8s_cluster_configs(enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_environment ON k8s_cluster_configs(environment);
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_sync_status ON k8s_cluster_configs(sync_status);
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_last_sync ON k8s_cluster_configs(last_sync DESC);

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_k8s_cluster_configs_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_k8s_cluster_configs_updated_at 
    BEFORE UPDATE ON k8s_cluster_configs
    FOR EACH ROW EXECUTE FUNCTION update_k8s_cluster_configs_updated_at();

-- Insert sample configurations (commented out - add manually for security)
-- These should be added through the admin UI with real service account tokens

-- Example for a production cluster:
-- INSERT INTO k8s_cluster_configs (
--     name, environment, region, api_server_url, 
--     service_account_token, ca_cert_data, namespace
-- ) VALUES (
--     'prod-cluster-1',
--     'production', 
--     'us-east-1',
--     'https://k8s-api.prod.company.com:6443',
--     'eyJhbGciOiJSUzI1NiIsImtp...', -- Real service account token
--     '-----BEGIN CERTIFICATE-----...', -- Real CA certificate
--     'monitoring'
-- ) ON CONFLICT (name) DO NOTHING;

-- Example for a staging cluster:
-- INSERT INTO k8s_cluster_configs (
--     name, environment, region, api_server_url,
--     service_account_token, ca_cert_data, namespace
-- ) VALUES (
--     'staging-cluster-1',
--     'staging',
--     'us-west-2', 
--     'https://k8s-api.staging.company.com:6443',
--     'eyJhbGciOiJSUzI1NiIsImtp...',
--     '-----BEGIN CERTIFICATE-----...',
--     'default'
-- ) ON CONFLICT (name) DO NOTHING;

-- Table for storing discovered K8s topology data
CREATE TABLE IF NOT EXISTS k8s_topology_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_name VARCHAR(255) NOT NULL REFERENCES k8s_cluster_configs(name) ON DELETE CASCADE,
    environment VARCHAR(100) NOT NULL,
    region VARCHAR(100) NOT NULL,
    topology_data JSONB NOT NULL,
    discovery_summary JSONB NOT NULL DEFAULT '{}',
    health_score DECIMAL(5,4) DEFAULT 0.0,
    total_nodes INTEGER DEFAULT 0,
    ready_nodes INTEGER DEFAULT 0,
    total_pods INTEGER DEFAULT 0,
    running_pods INTEGER DEFAULT 0,
    total_services INTEGER DEFAULT 0,
    discovered_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for topology snapshots
CREATE INDEX IF NOT EXISTS idx_k8s_topology_snapshots_cluster ON k8s_topology_snapshots(cluster_name);
CREATE INDEX IF NOT EXISTS idx_k8s_topology_snapshots_env ON k8s_topology_snapshots(environment);
CREATE INDEX IF NOT EXISTS idx_k8s_topology_snapshots_discovered_at ON k8s_topology_snapshots(discovered_at DESC);
CREATE INDEX IF NOT EXISTS idx_k8s_topology_snapshots_health ON k8s_topology_snapshots(health_score DESC);

-- GIN index for searching topology data
CREATE INDEX IF NOT EXISTS idx_k8s_topology_data_gin ON k8s_topology_snapshots USING gin(topology_data);

-- Table for K8s service accounts and RBAC configurations
CREATE TABLE IF NOT EXISTS k8s_service_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    cluster_name VARCHAR(255) NOT NULL,
    namespace VARCHAR(100) NOT NULL DEFAULT 'default',
    token TEXT NOT NULL,
    permissions JSONB DEFAULT '[]',
    expires_at TIMESTAMP WITH TIME ZONE,
    active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name, cluster_name, namespace)
);

-- Indexes for service accounts
CREATE INDEX IF NOT EXISTS idx_k8s_service_accounts_cluster ON k8s_service_accounts(cluster_name);
CREATE INDEX IF NOT EXISTS idx_k8s_service_accounts_active ON k8s_service_accounts(active) WHERE active = TRUE;
CREATE INDEX IF NOT EXISTS idx_k8s_service_accounts_expires ON k8s_service_accounts(expires_at);

-- Create trigger for service accounts
CREATE TRIGGER update_k8s_service_accounts_updated_at 
    BEFORE UPDATE ON k8s_service_accounts
    FOR EACH ROW EXECUTE FUNCTION update_k8s_cluster_configs_updated_at();

-- Add helpful comments
COMMENT ON TABLE k8s_cluster_configs IS 'Configuration for Kubernetes clusters for topology discovery';
COMMENT ON COLUMN k8s_cluster_configs.service_account_token IS 'Service account JWT token for read-only access to cluster';
COMMENT ON COLUMN k8s_cluster_configs.ca_cert_data IS 'Base64 encoded CA certificate for cluster verification';
COMMENT ON COLUMN k8s_cluster_configs.sync_status IS 'Status of last sync: pending, success, error';

COMMENT ON TABLE k8s_topology_snapshots IS 'Snapshots of discovered Kubernetes topology data';
COMMENT ON COLUMN k8s_topology_snapshots.topology_data IS 'Complete JSON representation of cluster topology';
COMMENT ON COLUMN k8s_topology_snapshots.health_score IS 'Overall cluster health score (0.0-1.0)';

COMMENT ON TABLE k8s_service_accounts IS 'Service account configurations for Kubernetes access';