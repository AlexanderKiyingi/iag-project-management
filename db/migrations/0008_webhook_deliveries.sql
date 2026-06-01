CREATE TABLE IF NOT EXISTS pm_webhook_deliveries (
    id                       BIGSERIAL PRIMARY KEY,
    workspace_owner_user_id  TEXT        NOT NULL,
    subscription_id          INT         NOT NULL,
    url                      TEXT        NOT NULL,
    secret                   TEXT        NOT NULL,
    event_type               TEXT        NOT NULL,
    payload                  JSONB       NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at             TIMESTAMPTZ,
    attempts                 INT         NOT NULL DEFAULT 0,
    last_status              INT,
    last_error               TEXT
);

CREATE INDEX IF NOT EXISTS pm_webhook_deliveries_pending_idx
    ON pm_webhook_deliveries (available_at)
    WHERE delivered_at IS NULL;
