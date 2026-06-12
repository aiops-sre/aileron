CREATE TABLE IF NOT EXISTS kafka_message_retries (
    message_key  VARCHAR(500) NOT NULL PRIMARY KEY,
    retry_count  INTEGER      NOT NULL DEFAULT 1,
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_kafka_retries_updated ON kafka_message_retries (updated_at);
