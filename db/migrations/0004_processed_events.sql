BEGIN;

CREATE TABLE IF NOT EXISTS pm_processed_events (
    event_id     TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMIT;
