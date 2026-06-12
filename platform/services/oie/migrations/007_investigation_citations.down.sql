-- Rollback migration 007
ALTER TABLE investigations DROP COLUMN IF EXISTS citations_json;
