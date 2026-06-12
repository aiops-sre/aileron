CREATE SEQUENCE IF NOT EXISTS investigation_events_seq START 1;

CREATE TABLE IF NOT EXISTS investigation_events (
    id               UUID        NOT NULL DEFAULT gen_random_uuid(),
    investigation_id UUID        NOT NULL,
    sequence_num     BIGINT      NOT NULL DEFAULT nextval('investigation_events_seq'),
    event_type       VARCHAR(80) NOT NULL,
    payload          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id)
) PARTITION BY RANGE (created_at);

DO $$
DECLARE
    start_date DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_date   DATE := DATE_TRUNC('month', CURRENT_DATE) + INTERVAL '3 months';
    part_name  TEXT;
    part_start TEXT;
    part_end   TEXT;
BEGIN
    WHILE start_date < end_date LOOP
        part_name  := 'investigation_events_' || TO_CHAR(start_date, 'YYYY_MM');
        part_start := TO_CHAR(start_date, 'YYYY-MM-DD');
        part_end   := TO_CHAR(start_date + INTERVAL '1 month', 'YYYY-MM-DD');
        EXECUTE FORMAT(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF investigation_events FOR VALUES FROM (%L) TO (%L)',
            part_name, part_start, part_end
        );
        start_date := start_date + INTERVAL '1 month';
    END LOOP;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_investigation_events_seq ON investigation_events (sequence_num);
CREATE INDEX IF NOT EXISTS idx_investigation_events_inv_seq ON investigation_events (investigation_id, sequence_num ASC);

ALTER TABLE investigation_events SET (
    autovacuum_vacuum_scale_factor  = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);
