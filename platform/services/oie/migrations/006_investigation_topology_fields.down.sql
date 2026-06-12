ALTER TABLE investigations
  DROP COLUMN IF EXISTS topology_path,
  DROP COLUMN IF EXISTS correlation_id;
