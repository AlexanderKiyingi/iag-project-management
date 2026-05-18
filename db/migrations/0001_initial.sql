BEGIN;

CREATE TABLE IF NOT EXISTS pm_workspaces (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id   TEXT NOT NULL,
    document        JSONB NOT NULL DEFAULT '{}'::jsonb,
    version         BIGINT NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT pm_workspaces_owner_user_id_key UNIQUE (owner_user_id)
);

CREATE INDEX IF NOT EXISTS pm_workspaces_owner_user_id_idx ON pm_workspaces (owner_user_id);

COMMIT;
