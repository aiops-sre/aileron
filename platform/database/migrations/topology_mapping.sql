-- Migration: Add service topology and dependency mapping tables
-- Version: topology_v1.0.0

-- Services table for service topology
CREATE TABLE IF NOT EXISTS services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255),
    type VARCHAR(50) NOT NULL,
    status VARCHAR(50) DEFAULT 'unknown',
    description TEXT,
    version VARCHAR(100),
    environment VARCHAR(100),
    owner VARCHAR(255),
    owner_email VARCHAR(255),
    repository VARCHAR(500),
    documentation TEXT,
    runbook_url VARCHAR(500),
    dashboard_url VARCHAR(500),
    tags JSONB DEFAULT '[]',
    labels JSONB DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    health_endpoint VARCHAR(500),
    sla JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name, environment),
    CHECK (type IN ('application', 'database', 'load_balancer', 'cache', 'queue', 'storage', 'network', 'infrastructure', 'api', 'microservice')),
    CHECK (status IN ('healthy', 'degraded', 'down', 'maintenance', 'unknown'))
);

-- Service dependencies table
CREATE TABLE IF NOT EXISTS service_dependencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    to_service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    dependency_type VARCHAR(50) NOT NULL DEFAULT 'soft',
    description TEXT,
    protocol VARCHAR(50),
    port INTEGER,
    endpoint VARCHAR(500),
    health_endpoint VARCHAR(500),
    retry_policy JSONB,
    timeout_seconds INTEGER DEFAULT 30,
    circuit_breaker BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(from_service_id, to_service_id),
    CHECK (dependency_type IN ('hard', 'soft', 'optional')),
    CHECK (from_service_id != to_service_id) -- Prevent self-dependency
);

-- Service status history table
CREATE TABLE IF NOT EXISTS service_status_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL,
    reason TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (status IN ('healthy', 'degraded', 'down', 'maintenance', 'unknown'))
);

-- Service metrics table for real-time metrics
CREATE TABLE IF NOT EXISTS service_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    cpu_percent DECIMAL(5,2),
    memory_percent DECIMAL(5,2),
    disk_percent DECIMAL(5,2),
    network_mbps DECIMAL(10,2),
    response_time_ms INTEGER,
    throughput_rps INTEGER,
    error_rate_percent DECIMAL(5,2),
    availability_percent DECIMAL(5,2),
    custom_metrics JSONB DEFAULT '{}',
    recorded_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (cpu_percent >= 0 AND cpu_percent <= 100),
    CHECK (memory_percent >= 0 AND memory_percent <= 100),
    CHECK (disk_percent >= 0 AND disk_percent <= 100),
    CHECK (error_rate_percent >= 0 AND error_rate_percent <= 100),
    CHECK (availability_percent >= 0 AND availability_percent <= 100)
);

-- Service discovery sources table
CREATE TABLE IF NOT EXISTS service_discovery_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    configuration JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN DEFAULT TRUE,
    last_scan TIMESTAMP WITH TIME ZONE,
    services_discovered INTEGER DEFAULT 0,
    scan_interval_minutes INTEGER DEFAULT 60,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name),
    CHECK (type IN ('kubernetes', 'docker', 'dns', 'cloud', 'manual'))
);

-- Service topology snapshots for versioning
CREATE TABLE IF NOT EXISTS topology_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255),
    description TEXT,
    services_data JSONB NOT NULL,
    dependencies_data JSONB NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(name)
);

-- Service health checks table
CREATE TABLE IF NOT EXISTS service_health_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    check_type VARCHAR(50) NOT NULL,
    endpoint VARCHAR(500) NOT NULL,
    method VARCHAR(10) DEFAULT 'GET',
    headers JSONB DEFAULT '{}',
    expected_status INTEGER DEFAULT 200,
    timeout_seconds INTEGER DEFAULT 30,
    interval_seconds INTEGER DEFAULT 60,
    enabled BOOLEAN DEFAULT TRUE,
    last_check TIMESTAMP WITH TIME ZONE,
    last_status VARCHAR(50),
    consecutive_failures INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    CHECK (check_type IN ('http', 'tcp', 'ping', 'custom')),
    CHECK (method IN ('GET', 'POST', 'PUT', 'DELETE', 'HEAD', 'OPTIONS'))
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_services_type ON services(type);
CREATE INDEX IF NOT EXISTS idx_services_status ON services(status);
CREATE INDEX IF NOT EXISTS idx_services_environment ON services(environment);
CREATE INDEX IF NOT EXISTS idx_services_tags ON services USING GIN(tags);
CREATE INDEX IF NOT EXISTS idx_services_labels ON services USING GIN(labels);

CREATE INDEX IF NOT EXISTS idx_service_dependencies_from_service ON service_dependencies(from_service_id);
CREATE INDEX IF NOT EXISTS idx_service_dependencies_to_service ON service_dependencies(to_service_id);
CREATE INDEX IF NOT EXISTS idx_service_dependencies_type ON service_dependencies(dependency_type);

CREATE INDEX IF NOT EXISTS idx_service_status_history_service_id ON service_status_history(service_id);
CREATE INDEX IF NOT EXISTS idx_service_status_history_created_at ON service_status_history(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_service_metrics_service_id ON service_metrics(service_id);
CREATE INDEX IF NOT EXISTS idx_service_metrics_recorded_at ON service_metrics(recorded_at DESC);

CREATE INDEX IF NOT EXISTS idx_service_discovery_sources_type ON service_discovery_sources(type);
CREATE INDEX IF NOT EXISTS idx_service_discovery_sources_enabled ON service_discovery_sources(enabled) WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_service_health_checks_service_id ON service_health_checks(service_id);
CREATE INDEX IF NOT EXISTS idx_service_health_checks_enabled ON service_health_checks(enabled) WHERE enabled = TRUE;

-- Insert sample services for demonstration
INSERT INTO services (name, display_name, type, status, description, environment, owner, tags, labels) VALUES
('alerthub-backend', 'AlertHub Backend API', 'application', 'healthy', 'Main AlertHub backend service', 'production', 'SRE Team', 
 '["backend", "api", "go"]', '{"tier": "critical", "team": "sre"}'),

('alerthub-frontend', 'AlertHub Frontend', 'application', 'healthy', 'AlertHub React frontend', 'production', 'SRE Team',
 '["frontend", "web", "react"]', '{"tier": "critical", "team": "sre"}'),

('postgresql-primary', 'PostgreSQL Primary', 'database', 'healthy', 'Primary PostgreSQL database', 'production', 'DBA Team',
 '["database", "postgresql", "primary"]', '{"tier": "critical", "team": "dba"}'),

('redis-cache', 'Redis Cache', 'cache', 'healthy', 'Redis caching layer', 'production', 'SRE Team',
 '["cache", "redis", "memory"]', '{"tier": "high", "team": "sre"}'),

('nginx-lb', 'Nginx Load Balancer', 'load_balancer', 'healthy', 'Nginx load balancer', 'production', 'SRE Team',
 '["loadbalancer", "nginx", "ingress"]', '{"tier": "critical", "team": "sre"}'),

('prometheus-monitoring', 'Prometheus', 'monitoring', 'healthy', 'Prometheus monitoring system', 'production', 'SRE Team',
 '["monitoring", "prometheus", "metrics"]', '{"tier": "high", "team": "sre"}')

ON CONFLICT (name, environment) DO NOTHING;

-- Insert sample dependencies
INSERT INTO service_dependencies (from_service_id, to_service_id, dependency_type, description, protocol, port) VALUES
((SELECT id FROM services WHERE name = 'alerthub-backend'), (SELECT id FROM services WHERE name = 'postgresql-primary'), 'hard', 'Database connection', 'tcp', 5432),
((SELECT id FROM services WHERE name = 'alerthub-backend'), (SELECT id FROM services WHERE name = 'redis-cache'), 'soft', 'Caching layer', 'tcp', 6379),
((SELECT id FROM services WHERE name = 'alerthub-frontend'), (SELECT id FROM services WHERE name = 'alerthub-backend'), 'hard', 'API calls', 'http', 3000),
((SELECT id FROM services WHERE name = 'nginx-lb'), (SELECT id FROM services WHERE name = 'alerthub-frontend'), 'hard', 'Web serving', 'http', 80),
((SELECT id FROM services WHERE name = 'nginx-lb'), (SELECT id FROM services WHERE name = 'alerthub-backend'), 'hard', 'API serving', 'http', 3000)
ON CONFLICT (from_service_id, to_service_id) DO NOTHING;

-- Insert sample health checks
INSERT INTO service_health_checks (service_id, check_type, endpoint, method, expected_status, interval_seconds) VALUES
((SELECT id FROM services WHERE name = 'alerthub-backend'), 'http', '/health', 'GET', 200, 30),
((SELECT id FROM services WHERE name = 'alerthub-frontend'), 'http', '/', 'GET', 200, 60),
((SELECT id FROM services WHERE name = 'postgresql-primary'), 'tcp', 'localhost:5432', 'GET', 200, 60),
((SELECT id FROM services WHERE name = 'redis-cache'), 'tcp', 'localhost:6379', 'GET', 200, 60)
ON CONFLICT DO NOTHING;

-- Create trigger to update updated_at timestamp
CREATE TRIGGER update_services_updated_at 
    BEFORE UPDATE ON services
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_service_dependencies_updated_at 
    BEFORE UPDATE ON service_dependencies
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_service_discovery_sources_updated_at 
    BEFORE UPDATE ON service_discovery_sources
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_service_health_checks_updated_at 
    BEFORE UPDATE ON service_health_checks
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();