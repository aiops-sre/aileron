-- Add missing columns to incidents table
-- These were referenced in InternalCreateIncident/GetIncident but never migrated,
-- causing all auto-created incidents to silently fail with
-- "pq: column timeline of relation incidents does not exist"

ALTER TABLE incidents ADD COLUMN IF NOT EXISTS timeline JSONB DEFAULT '[]'::jsonb;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS closed_at TIMESTAMP;
