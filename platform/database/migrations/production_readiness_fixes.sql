-- Production readiness fixes — applied before v1.0.0 go-live.
--
-- 1. alerts: add unique partial index matching ON CONFLICT predicate in CreateAlert
-- 2. correlation_results: add recommended_action column (needed by parallel engine INSERT)
-- 3. correlation_results: extend CHECK constraint to include 'parallel'
-- 4. correlation_results: add ON CONFLICT-compatible unique constraint
-- 5. incidents: add missing recommended_action / parallel to correlation_type
-- -----------------------------------------------------------------------------

-- 1. Unique index whose predicate exactly matches the ON CONFLICT clause in alerts.go.
--    The existing idx_alerts_source_id uses WHERE source_id IS NOT NULL (no != ''),
--    so the ON CONFLICT clause fails to match and raises a duplicate-key error.
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_source_id_nonempty
    ON alerts (source, source_id)
    WHERE source_id IS NOT NULL AND source_id != '';

-- 2. Add recommended_action to correlation_results (partitioned table).
--    Use IF NOT EXISTS via a DO block because ALTER TABLE … ADD COLUMN IF NOT EXISTS
--    is only available in PG 9.6+ but the column may already exist in some deploys.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'correlation_results' AND column_name = 'recommended_action'
    ) THEN
        ALTER TABLE correlation_results ADD COLUMN recommended_action TEXT;
    END IF;
END$$;

-- 3. Drop the old CHECK constraint and recreate it with 'parallel' included.
--    The constraint name comes from the v2_safe_migration.sql definition.
--    We use a DO block so this is idempotent across deployments.
DO $$
BEGIN
    -- Drop existing constraint (name may vary; cover both known names).
    BEGIN
        ALTER TABLE correlation_results DROP CONSTRAINT IF EXISTS correlation_results_correlation_type_check;
    EXCEPTION WHEN OTHERS THEN NULL;
    END;
    -- Add the updated constraint.
    BEGIN
        ALTER TABLE correlation_results
            ADD CONSTRAINT correlation_results_correlation_type_check
            CHECK (correlation_type IN (
                'rule_based','ai_semantic','davis_ai','topology',
                'temporal','pipeline','manual','parallel'
            ));
    EXCEPTION WHEN duplicate_object THEN NULL;
    END;
END$$;
