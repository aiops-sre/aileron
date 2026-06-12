-- Add prom-dashboard-inspired features to alerts table
-- Migration: alert_prom_features
-- Date: 2026-01-30

-- Add linked alerts support (JSONB for flexibility)
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS linked_alerts JSONB DEFAULT NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS linked_to JSONB DEFAULT NULL;

-- Add maintenance mode support
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_status INTEGER DEFAULT 0;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_start_time TIMESTAMP DEFAULT NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_end_time TIMESTAMP DEFAULT NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_comment TEXT DEFAULT NULL;

-- Add live status tracking
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS is_alert_active BOOLEAN DEFAULT TRUE;

-- Add SLA tracking
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS sla_met_responsetime BOOLEAN DEFAULT FALSE;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS sla_met_resolutiontime BOOLEAN DEFAULT FALSE;

-- Add email tracking (for alert management)
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS first_email_timestamp TIMESTAMP DEFAULT NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS recent_email_timestamp TIMESTAMP DEFAULT NULL;

-- Create indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_alerts_is_active ON alerts(is_alert_active);
CREATE INDEX IF NOT EXISTS idx_alerts_maintenance ON alerts(maintenance_status) WHERE maintenance_status = 1;
CREATE INDEX IF NOT EXISTS idx_alerts_linked ON alerts USING GIN (linked_alerts) WHERE linked_alerts IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_sla_response ON alerts(sla_met_responsetime) WHERE sla_met_responsetime = FALSE;
CREATE INDEX IF NOT EXISTS idx_alerts_sla_resolution ON alerts(sla_met_resolutiontime) WHERE sla_met_resolutiontime = FALSE;

-- Create maintenance_windows table for comprehensive maintenance tracking
CREATE TABLE IF NOT EXISTS maintenance_windows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    url_name TEXT,
    alertname TEXT,
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP,
    maintenance_status INTEGER DEFAULT 1,
    created_by UUID REFERENCES users(id),
    comment TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_alert ON maintenance_windows(alert_id);
CREATE INDEX IF NOT EXISTS idx_maintenance_status ON maintenance_windows(maintenance_status);
CREATE INDEX IF NOT EXISTS idx_maintenance_active ON maintenance_windows(maintenance_status, end_time) 
    WHERE maintenance_status = 1 AND (end_time IS NULL OR end_time > NOW());

-- Add comments/notes tracking table
CREATE TABLE IF NOT EXISTS alert_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id),
    comment TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_comments_alert ON alert_comments(alert_id);
CREATE INDEX IF NOT EXISTS idx_alert_comments_created ON alert_comments(created_at DESC);

-- Function to auto-calculate SLA compliance
CREATE OR REPLACE FUNCTION calculate_sla_compliance()
RETURNS TRIGGER AS $$
DECLARE
    response_sla_minutes INTEGER := 15; -- 15 minutes response SLA
    resolution_sla_minutes INTEGER := 240; -- 4 hours resolution SLA
    response_time_minutes INTEGER;
    resolution_time_minutes INTEGER;
BEGIN
    -- Calculate response time (time to acknowledge)
    IF NEW.acknowledged_at IS NOT NULL AND NEW.created_at IS NOT NULL THEN
        response_time_minutes := EXTRACT(EPOCH FROM (NEW.acknowledged_at - NEW.created_at)) / 60;
        NEW.sla_met_responsetime := response_time_minutes <= response_sla_minutes;
    END IF;

    -- Calculate resolution time
    IF NEW.resolved_at IS NOT NULL AND NEW.created_at IS NOT NULL THEN
        resolution_time_minutes := EXTRACT(EPOCH FROM (NEW.resolved_at - NEW.created_at)) / 60;
        NEW.sla_met_resolutiontime := resolution_time_minutes <= resolution_sla_minutes;
    END IF;

    -- Update is_alert_active based on status
    IF NEW.status = 'resolved' THEN
        NEW.is_alert_active := FALSE;
    ELSE
        NEW.is_alert_active := TRUE;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for SLA calculation
DROP TRIGGER IF EXISTS trigger_calculate_sla ON alerts;
CREATE TRIGGER trigger_calculate_sla
    BEFORE INSERT OR UPDATE ON alerts
    FOR EACH ROW
    EXECUTE FUNCTION calculate_sla_compliance();

-- Update existing alerts to set default values
UPDATE alerts SET is_alert_active = TRUE WHERE is_alert_active IS NULL;
UPDATE alerts SET maintenance_status = 0 WHERE maintenance_status IS NULL;
UPDATE alerts SET sla_met_responsetime = FALSE WHERE sla_met_responsetime IS NULL;
UPDATE alerts SET sla_met_resolutiontime = FALSE WHERE sla_met_resolutiontime IS NULL;

-- Recalculate SLA for existing alerts
UPDATE alerts SET 
    sla_met_responsetime = CASE 
        WHEN acknowledged_at IS NOT NULL THEN 
            EXTRACT(EPOCH FROM (acknowledged_at - created_at)) / 60 <= 15
        ELSE FALSE
    END,
    sla_met_resolutiontime = CASE 
        WHEN resolved_at IS NOT NULL THEN 
            EXTRACT(EPOCH FROM (resolved_at - created_at)) / 60 <= 240
        ELSE FALSE
    END,
    is_alert_active = CASE 
        WHEN status = 'resolved' THEN FALSE
        ELSE TRUE
    END
WHERE id IN (SELECT id FROM alerts);

COMMENT ON COLUMN alerts.linked_alerts IS 'JSONB array of linked alert references: [{"alert_id": "uuid", "alert_type": "Node", "link_type": "related"}]';
COMMENT ON COLUMN alerts.linked_to IS 'JSONB parent alert reference: {"alert_id": "uuid", "alert_type": "Kubernetes"}';
COMMENT ON COLUMN alerts.maintenance_status IS '0=active monitoring, 1=in maintenance window';
COMMENT ON COLUMN alerts.is_alert_active IS 'TRUE if alert is currently firing, FALSE if resolved';
COMMENT ON COLUMN alerts.sla_met_responsetime IS 'TRUE if acknowledged within SLA (default: 15 minutes)';
COMMENT ON COLUMN alerts.sla_met_resolutiontime IS 'TRUE if resolved within SLA (default: 4 hours)';
