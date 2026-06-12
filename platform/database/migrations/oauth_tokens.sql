-- OAuth Token Storage Migration
-- Adds OAuth ID token storage and AI Assistant connection settings to users table

-- Add OAuth token columns to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_id_token TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_token_updated_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS ai_auto_connect BOOLEAN DEFAULT false;

-- Create index for OAuth token queries
CREATE INDEX IF NOT EXISTS idx_users_oauth_token ON users(oauth_id_token) WHERE oauth_id_token IS NOT NULL;

-- Create table for storing OAuth token refresh history
CREATE TABLE IF NOT EXISTS oauth_token_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  action VARCHAR(50) NOT NULL, -- 'stored', 'refreshed', 'validated', 'cleared'
  token_hash VARCHAR(255),
  ai_provider VARCHAR(100),
  status VARCHAR(20) NOT NULL DEFAULT 'success', -- 'success', 'failed'
  error_message TEXT,
  ip_address INET,
  user_agent TEXT,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Create index for audit queries
CREATE INDEX IF NOT EXISTS idx_oauth_token_audit_user_id ON oauth_token_audit(user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_token_audit_created_at ON oauth_token_audit(created_at);

-- Create table for OAuth connection settings
CREATE TABLE IF NOT EXISTS oauth_settings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  ai_provider VARCHAR(100) DEFAULT 'work_ai',
  auto_connect BOOLEAN DEFAULT false,
  connection_status VARCHAR(50) DEFAULT 'disconnected', -- 'disconnected', 'connected', 'token_stored', 'error'
  last_connected_at TIMESTAMP,
  last_error_at TIMESTAMP,
  last_error_message TEXT,
  reconnect_attempts INT DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Create index for settings queries
CREATE INDEX IF NOT EXISTS idx_oauth_settings_auto_connect ON oauth_settings(auto_connect) WHERE auto_connect = true;
CREATE INDEX IF NOT EXISTS idx_oauth_settings_connection_status ON oauth_settings(connection_status);

-- Trigger to update oauth_settings updated_at timestamp
CREATE OR REPLACE FUNCTION update_oauth_settings_timestamp()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER oauth_settings_updated_at_trigger
BEFORE UPDATE ON oauth_settings
FOR EACH ROW
EXECUTE FUNCTION update_oauth_settings_timestamp();

-- Add OAuth columns to MAS user table if it exists
ALTER TABLE IF EXISTS mas_users ADD COLUMN IF NOT EXISTS oauth_id_token TEXT;
ALTER TABLE IF EXISTS mas_users ADD COLUMN IF NOT EXISTS oauth_token_updated_at TIMESTAMP;
ALTER TABLE IF EXISTS mas_users ADD COLUMN IF NOT EXISTS ai_auto_connect BOOLEAN DEFAULT false;

-- Create or update view for user OAuth status
CREATE OR REPLACE VIEW user_oauth_status AS
SELECT
  u.id,
  u.username,
  u.email,
  COALESCE(os.ai_provider, 'work_ai') as ai_provider,
  COALESCE(u.oauth_id_token IS NOT NULL, false) as has_token,
  COALESCE(os.auto_connect, false) as auto_connect,
  COALESCE(os.connection_status, 'disconnected') as status,
  os.last_connected_at,
  os.reconnect_attempts
FROM users u
LEFT JOIN oauth_settings os ON u.id = os.user_id;
