-- Migration: Add MAS Authentication Support
-- Version: 1.0.0
-- Description: Adds tables and columns to support MAS authentication alongside existing JWT auth

-- Add MAS-specific columns to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS mas_enabled BOOLEAN DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS mas_username VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_mas_sync TIMESTAMP;

-- Create index for MAS username lookups
CREATE INDEX IF NOT EXISTS idx_users_mas_username ON users(mas_username) WHERE mas_username IS NOT NULL;

-- Create table for MAS group to role mappings
CREATE TABLE IF NOT EXISTS mas_group_mappings (
    id SERIAL PRIMARY KEY,
    mas_group VARCHAR(255) UNIQUE NOT NULL,
    alerthub_role VARCHAR(50) NOT NULL,
    priority INT DEFAULT 0,
    auto_provision BOOLEAN DEFAULT true,
    description TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Create index for group lookups
CREATE INDEX IF NOT EXISTS idx_mas_group_mappings_group ON mas_group_mappings(mas_group);

-- Insert default group mappings
INSERT INTO mas_group_mappings (mas_group, alerthub_role, priority, auto_provision, description) VALUES
    ('interactive-apps-systems', 'admin', 100, true, 'Full admin access to AlertHub'),
    ('IASYS-SRE', 'sre', 90, true, 'SRE team with elevated access'),
    ('Interactive-SRE', 'sre', 90, true, 'SRE team with elevated access (new naming)'),
    ('interactive-monitoring', 'operator', 50, true, 'Monitoring operators'),
    ('interactive-all', 'viewer', 10, true, 'Basic viewer access for all team members')
ON CONFLICT (mas_group) DO NOTHING;

-- Create table for user MAS groups (tracks which groups each user belongs to)
CREATE TABLE IF NOT EXISTS user_mas_groups (
    user_id INT REFERENCES users(id) ON DELETE CASCADE,
    mas_group VARCHAR(255) NOT NULL,
    synced_at TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (user_id, mas_group)
);

-- Create index for group membership lookups
CREATE INDEX IF NOT EXISTS idx_user_mas_groups_user_id ON user_mas_groups(user_id);
CREATE INDEX IF NOT EXISTS idx_user_mas_groups_mas_group ON user_mas_groups(mas_group);

-- Create table for MAS authentication logs (audit trail)
CREATE TABLE IF NOT EXISTS mas_auth_logs (
    id SERIAL PRIMARY KEY,
    username VARCHAR(255) NOT NULL,
    email VARCHAR(255),
    groups TEXT[], -- Array of group names
    mapped_roles TEXT[], -- Array of mapped role names
    ip_address INET,
    user_agent TEXT,
    auth_result VARCHAR(50) NOT NULL, -- 'success', 'failure', 'denied'
    denial_reason TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create indexes for audit log queries
CREATE INDEX IF NOT EXISTS idx_mas_auth_logs_username ON mas_auth_logs(username);
CREATE INDEX IF NOT EXISTS idx_mas_auth_logs_created_at ON mas_auth_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mas_auth_logs_auth_result ON mas_auth_logs(auth_result);

-- Create table for MAS configuration
CREATE TABLE IF NOT EXISTS mas_config (
    key VARCHAR(255) PRIMARY KEY,
    value TEXT NOT NULL,
    description TEXT,
    updated_at TIMESTAMP DEFAULT NOW(),
    updated_by VARCHAR(255)
);

-- Insert default MAS configuration
INSERT INTO mas_config (key, value, description) VALUES
    ('enabled', 'false', 'Enable/disable MAS authentication'),
    ('auto_provision', 'true', 'Automatically provision users from MAS'),
    ('default_role', 'viewer', 'Default role for auto-provisioned users'),
    ('strict_mode', 'false', 'Require MAS authentication even when JWT is available'),
    ('sync_interval', '300', 'Seconds between group sync operations')
ON CONFLICT (key) DO NOTHING;

-- Add trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply trigger to relevant tables
DROP TRIGGER IF EXISTS update_mas_group_mappings_updated_at ON mas_group_mappings;
CREATE TRIGGER update_mas_group_mappings_updated_at BEFORE UPDATE ON mas_group_mappings
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_mas_config_updated_at ON mas_config;
CREATE TRIGGER update_mas_config_updated_at BEFORE UPDATE ON mas_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create view for easy user MAS info lookup
CREATE OR REPLACE VIEW v_users_mas_info AS
SELECT 
    u.id,
    u.username,
    u.email,
    u.role,
    u.mas_enabled,
    u.mas_username,
    u.last_mas_sync,
    ARRAY_AGG(DISTINCT umg.mas_group) FILTER (WHERE umg.mas_group IS NOT NULL) as mas_groups,
    ARRAY_AGG(DISTINCT mgm.alerthub_role) FILTER (WHERE mgm.alerthub_role IS NOT NULL) as mapped_roles,
    u.created_at,
    u.updated_at
FROM users u
LEFT JOIN user_mas_groups umg ON u.id = umg.user_id
LEFT JOIN mas_group_mappings mgm ON umg.mas_group = mgm.mas_group
WHERE u.mas_enabled = true
GROUP BY u.id, u.username, u.email, u.role, u.mas_enabled, u.mas_username, u.last_mas_sync, u.created_at, u.updated_at;

-- Grant permissions (adjust as needed for your setup)
-- GRANT SELECT, INSERT, UPDATE, DELETE ON mas_group_mappings TO alerthub_app;
-- GRANT SELECT, INSERT, UPDATE, DELETE ON user_mas_groups TO alerthub_app;
-- GRANT SELECT, INSERT ON mas_auth_logs TO alerthub_app;
-- GRANT SELECT, UPDATE ON mas_config TO alerthub_app;
-- GRANT SELECT ON v_users_mas_info TO alerthub_app;

-- Add comments for documentation
COMMENT ON TABLE mas_group_mappings IS 'Defines how MAS groups map to AlertHub roles';
COMMENT ON TABLE user_mas_groups IS 'Tracks which MAS groups each user belongs to';
COMMENT ON TABLE mas_auth_logs IS 'Audit log for MAS authentication attempts';
COMMENT ON TABLE mas_config IS 'Configuration settings for MAS authentication';
COMMENT ON VIEW v_users_mas_info IS 'Convenient view of users with their MAS groups and mapped roles';

COMMENT ON COLUMN users.mas_enabled IS 'Whether user can authenticate via MAS';
COMMENT ON COLUMN users.mas_username IS 'Username in MAS system (may differ from local username)';
COMMENT ON COLUMN users.last_mas_sync IS 'Last time user groups were synced from MAS';

-- Migration complete
-- To rollback, run: migrations/mas_auth_rollback.sql
