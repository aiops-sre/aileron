-- Migration: Add Floodgate Hybrid Authentication columns
-- Description: Adds oauth_source and vpn_ip columns to users table for hybrid token acquisition tracking
-- Date: 2026-02-26

-- Add new columns for hybrid authentication
ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_source VARCHAR(50);
ALTER TABLE users ADD COLUMN IF NOT EXISTS vpn_ip VARCHAR(45);

-- Add comment for documentation
COMMENT ON COLUMN users.oauth_source IS 'Source of OAuth token: mas-proxy, headers, floodgate-cli, database-cache';
COMMENT ON COLUMN users.vpn_ip IS 'User VPN IP address for Floodgate proxy requests (required by Floodgate API)';

-- Add index for performance (optional but recommended)
CREATE INDEX IF NOT EXISTS idx_users_oauth_source 
ON users(oauth_source) 
WHERE oauth_source IS NOT NULL;

-- Add index for token expiry queries
CREATE INDEX IF NOT EXISTS idx_users_oauth_token_updated 
ON users(oauth_token_updated_at) 
WHERE oauth_id_token IS NOT NULL;

-- Verify columns were added
SELECT 
    column_name, 
    data_type, 
    character_maximum_length,
    is_nullable
FROM information_schema.columns 
WHERE table_name = 'users' 
AND column_name IN ('oauth_source', 'vpn_ip', 'oauth_id_token', 'oauth_token_updated_at')
ORDER BY column_name;

-- Display migration success message
DO $$
BEGIN
    RAISE NOTICE '✅ Floodgate hybrid authentication migration completed successfully';
    RAISE NOTICE 'Added columns: oauth_source, vpn_ip';
    RAISE NOTICE 'Added indexes: idx_users_oauth_source, idx_users_oauth_token_updated';
END $$;
