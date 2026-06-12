-- Dynatrace Topology Integration Tables
-- Stores real topology data from Dynatrace API for enhanced correlation

-- Main topology entities table
CREATE TABLE IF NOT EXISTS dynatrace_topology (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id VARCHAR(255) UNIQUE NOT NULL,
    entity_type VARCHAR(100) NOT NULL,
    display_name VARCHAR(500) NOT NULL,
    topology_data JSONB NOT NULL,
    last_updated TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW()
);

-- Index for fast entity lookups
CREATE INDEX IF NOT EXISTS idx_dynatrace_topology_entity_id ON dynatrace_topology(entity_id);
CREATE INDEX IF NOT EXISTS idx_dynatrace_topology_entity_type ON dynatrace_topology(entity_type);
CREATE INDEX IF NOT EXISTS idx_dynatrace_topology_last_updated ON dynatrace_topology(last_updated);

-- Topology relationships table (denormalized for fast queries)
CREATE TABLE IF NOT EXISTS dynatrace_relationships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_entity_id VARCHAR(255) NOT NULL,
    to_entity_id VARCHAR(255) NOT NULL,
    relationship_type VARCHAR(100) NOT NULL, -- CALLS, RUNS_ON, INSTANCE_OF, etc.
    direction VARCHAR(20) NOT NULL, -- INBOUND, OUTBOUND
    properties JSONB DEFAULT '{}',
    last_updated TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW(),
    
    UNIQUE(from_entity_id, to_entity_id, relationship_type, direction)
);

-- Indexes for relationship queries
CREATE INDEX IF NOT EXISTS idx_dynatrace_relationships_from_entity ON dynatrace_relationships(from_entity_id);
CREATE INDEX IF NOT EXISTS idx_dynatrace_relationships_to_entity ON dynatrace_relationships(to_entity_id);
CREATE INDEX IF NOT EXISTS idx_dynatrace_relationships_type ON dynatrace_relationships(relationship_type);

-- Dynatrace configuration table
CREATE TABLE IF NOT EXISTS dynatrace_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_url VARCHAR(500) NOT NULL,
    api_token_encrypted TEXT NOT NULL,
    environment_name VARCHAR(200) DEFAULT 'production',
    sync_enabled BOOLEAN DEFAULT true,
    sync_interval_minutes INTEGER DEFAULT 15,
    last_sync_time TIMESTAMP,
    last_sync_status VARCHAR(50), -- success, error, in_progress
    entity_types TEXT[] DEFAULT ARRAY['SERVICE', 'PROCESS_GROUP', 'HOST', 'APPLICATION'],
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Management zones table for organization
CREATE TABLE IF NOT EXISTS dynatrace_management_zones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    zone_id VARCHAR(255) UNIQUE NOT NULL,
    zone_name VARCHAR(300) NOT NULL,
    description TEXT,
    entity_count INTEGER DEFAULT 0,
    last_updated TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW()
);

-- Enhanced alert correlation table to include Dynatrace topology context
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS dynatrace_entity_id VARCHAR(255);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS management_zone VARCHAR(300);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS topology_context JSONB;

-- Index for Dynatrace entity correlation
CREATE INDEX IF NOT EXISTS idx_alerts_dynatrace_entity ON alerts(dynatrace_entity_id);
CREATE INDEX IF NOT EXISTS idx_alerts_management_zone ON alerts(management_zone);

-- Enhanced correlation results to include topology information
ALTER TABLE alert_correlations ADD COLUMN IF NOT EXISTS topology_correlation_score DECIMAL(5,4);
ALTER TABLE alert_correlations ADD COLUMN IF NOT EXISTS related_entities JSONB;
ALTER TABLE alert_correlations ADD COLUMN IF NOT EXISTS management_zone_match BOOLEAN DEFAULT FALSE;

-- View for alert topology correlation analysis
CREATE OR REPLACE VIEW alert_topology_correlation AS
SELECT 
    a.id as alert_id,
    a.title,
    a.severity,
    a.dynatrace_entity_id,
    a.management_zone,
    dt.entity_type,
    dt.display_name as entity_name,
    ac.topology_correlation_score,
    ac.related_entities,
    ac.confidence_score,
    COUNT(related_alerts.id) as related_alert_count
FROM alerts a
LEFT JOIN dynatrace_topology dt ON a.dynatrace_entity_id = dt.entity_id
LEFT JOIN alert_correlations ac ON a.id = ac.alert_id
LEFT JOIN alerts related_alerts ON related_alerts.management_zone = a.management_zone 
    AND related_alerts.id != a.id
    AND related_alerts.created_at > (a.created_at - INTERVAL '2 hours')
WHERE a.source = 'dynatrace'
GROUP BY a.id, a.title, a.severity, a.dynatrace_entity_id, a.management_zone, 
         dt.entity_type, dt.display_name, ac.topology_correlation_score, 
         ac.related_entities, ac.confidence_score;

-- Function to get related entities for an alert
CREATE OR REPLACE FUNCTION get_related_entities_for_alert(alert_entity_id VARCHAR(255))
RETURNS TABLE(
    entity_id VARCHAR(255),
    entity_type VARCHAR(100),
    display_name VARCHAR(500),
    relationship_type VARCHAR(100),
    direction VARCHAR(20)
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        CASE 
            WHEN dr.direction = 'OUTBOUND' THEN dr.to_entity_id
            ELSE dr.from_entity_id
        END as entity_id,
        dt.entity_type,
        dt.display_name,
        dr.relationship_type,
        dr.direction
    FROM dynatrace_relationships dr
    JOIN dynatrace_topology dt ON (
        CASE 
            WHEN dr.direction = 'OUTBOUND' THEN dt.entity_id = dr.to_entity_id
            ELSE dt.entity_id = dr.from_entity_id
        END
    )
    WHERE (
        (dr.direction = 'OUTBOUND' AND dr.from_entity_id = alert_entity_id)
        OR 
        (dr.direction = 'INBOUND' AND dr.to_entity_id = alert_entity_id)
    )
    AND dr.last_updated > NOW() - INTERVAL '1 hour';
END;
$$ LANGUAGE plpgsql;

-- Function to calculate topology-based correlation score
CREATE OR REPLACE FUNCTION calculate_topology_correlation_score(
    entity1_id VARCHAR(255),
    entity2_id VARCHAR(255)
)
RETURNS DECIMAL(5,4) AS $$
DECLARE
    score DECIMAL(5,4) := 0.0;
    same_zone BOOLEAN := FALSE;
    direct_relationship BOOLEAN := FALSE;
    relationship_count INTEGER := 0;
BEGIN
    -- Check if same management zone
    SELECT COUNT(*) > 0 INTO same_zone
    FROM dynatrace_topology dt1, dynatrace_topology dt2
    WHERE dt1.entity_id = entity1_id 
    AND dt2.entity_id = entity2_id
    AND dt1.topology_data->>'managementZone' = dt2.topology_data->>'managementZone'
    AND dt1.topology_data->>'managementZone' != '';
    
    IF same_zone THEN
        score := score + 0.3;
    END IF;
    
    -- Check for direct relationships
    SELECT COUNT(*) > 0 INTO direct_relationship
    FROM dynatrace_relationships
    WHERE (from_entity_id = entity1_id AND to_entity_id = entity2_id)
    OR (from_entity_id = entity2_id AND to_entity_id = entity1_id);
    
    IF direct_relationship THEN
        score := score + 0.5;
    END IF;
    
    -- Count mutual relationships (2nd degree connections)
    SELECT COUNT(DISTINCT dr1.to_entity_id) INTO relationship_count
    FROM dynatrace_relationships dr1
    JOIN dynatrace_relationships dr2 ON dr1.to_entity_id = dr2.from_entity_id
    WHERE dr1.from_entity_id = entity1_id
    AND dr2.to_entity_id = entity2_id;
    
    IF relationship_count > 0 THEN
        score := score + (relationship_count * 0.1);
    END IF;
    
    -- Cap at 1.0
    IF score > 1.0 THEN
        score := 1.0;
    END IF;
    
    RETURN score;
END;
$$ LANGUAGE plpgsql;

-- Trigger to automatically extract Dynatrace entity information from alerts
CREATE OR REPLACE FUNCTION extract_dynatrace_entity_from_alert()
RETURNS TRIGGER AS $$
BEGIN
    -- Extract entity ID from metadata
    IF NEW.source = 'dynatrace' AND NEW.metadata IS NOT NULL THEN
        -- Try different possible locations for entity ID
        NEW.dynatrace_entity_id := COALESCE(
            NEW.metadata->>'entityId',
            NEW.metadata->>'entity_id',
            NEW.metadata->>'impactedEntity',
            NEW.metadata->'AffectedEntities'->0->>'entityId'
        );
        
        -- Extract management zone if available
        NEW.management_zone := COALESCE(
            NEW.metadata->>'managementZone',
            NEW.metadata->'managementZones'->0->>'name'
        );
        
        -- Store topology context for faster correlation
        IF NEW.dynatrace_entity_id IS NOT NULL THEN
            NEW.topology_context := jsonb_build_object(
                'entity_id', NEW.dynatrace_entity_id,
                'management_zone', NEW.management_zone,
                'extracted_at', NOW()
            );
        END IF;
    END IF;
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger on alerts table
DROP TRIGGER IF EXISTS trigger_extract_dynatrace_entity ON alerts;
CREATE TRIGGER trigger_extract_dynatrace_entity
    BEFORE INSERT OR UPDATE ON alerts
    FOR EACH ROW
    EXECUTE FUNCTION extract_dynatrace_entity_from_alert();

-- Sample queries for testing topology correlation

-- Find all alerts for entities in the same management zone
-- SELECT * FROM alert_topology_correlation 
-- WHERE management_zone = 'Production-Web-Services' 
-- AND created_at > NOW() - INTERVAL '24 hours';

-- Find alerts for related entities (direct relationships)
-- SELECT a1.title as alert1, a2.title as alert2, dr.relationship_type
-- FROM alerts a1
-- JOIN dynatrace_relationships dr ON a1.dynatrace_entity_id = dr.from_entity_id
-- JOIN alerts a2 ON dr.to_entity_id = a2.dynatrace_entity_id
-- WHERE a1.created_at > NOW() - INTERVAL '2 hours'
-- AND a2.created_at > NOW() - INTERVAL '2 hours';

-- Get topology correlation scores for recent alerts
-- SELECT 
--     a1.title,
--     a2.title,
--     calculate_topology_correlation_score(a1.dynatrace_entity_id, a2.dynatrace_entity_id) as correlation_score
-- FROM alerts a1
-- CROSS JOIN alerts a2
-- WHERE a1.id != a2.id
-- AND a1.source = 'dynatrace' AND a2.source = 'dynatrace'
-- AND a1.created_at > NOW() - INTERVAL '1 hour'
-- AND a2.created_at > NOW() - INTERVAL '1 hour'
-- AND calculate_topology_correlation_score(a1.dynatrace_entity_id, a2.dynatrace_entity_id) > 0.5;

COMMENT ON TABLE dynatrace_topology IS 'Stores Dynatrace entity topology data for correlation analysis';
COMMENT ON TABLE dynatrace_relationships IS 'Stores relationships between Dynatrace entities';
COMMENT ON TABLE dynatrace_config IS 'Configuration for Dynatrace API integration';
COMMENT ON FUNCTION calculate_topology_correlation_score IS 'Calculates correlation score between two Dynatrace entities based on topology';
COMMENT ON VIEW alert_topology_correlation IS 'View showing alerts with their topology correlation context';

-- Insert sample configuration (update with your Dynatrace details)
-- INSERT INTO dynatrace_config (tenant_url, api_token_encrypted, environment_name) 
-- VALUES ('https://your-tenant.live.dynatrace.com', 'encrypted_api_token_here', 'production')
-- ON CONFLICT (id) DO NOTHING;