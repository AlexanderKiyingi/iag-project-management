CREATE TABLE IF NOT EXISTS pm_request_audit (
    id                       BIGSERIAL PRIMARY KEY,
    occurred_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    workspace_owner_user_id  TEXT,
    actor_user_id            TEXT,
    actor_display            TEXT,
    method                   TEXT NOT NULL,
    path                     TEXT NOT NULL,
    status                   INT  NOT NULL,
    duration_ms              INT  NOT NULL DEFAULT 0,
    ip                       TEXT,
    user_agent               TEXT,
    request_id               TEXT
);

CREATE INDEX IF NOT EXISTS pm_request_audit_workspace_time_idx
    ON pm_request_audit (workspace_owner_user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS pm_request_audit_actor_time_idx
    ON pm_request_audit (actor_user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS pm_request_audit_path_time_idx
    ON pm_request_audit (path, occurred_at DESC);
