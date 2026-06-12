-- Add topology_path and correlation_id to investigations for entity context derivation
ALTER TABLE investigations
  ADD COLUMN IF NOT EXISTS topology_path  VARCHAR(500) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255) NOT NULL DEFAULT '';
