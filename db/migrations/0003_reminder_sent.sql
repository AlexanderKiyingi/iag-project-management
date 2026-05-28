BEGIN;

CREATE TABLE IF NOT EXISTS pm_reminder_sent (
    reminder_key TEXT PRIMARY KEY,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMIT;
