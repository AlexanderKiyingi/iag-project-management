-- Self-heal: legacy Railway DBs have 0001 recorded in schema_migrations
-- but pm_workspaces does not exist because pgx's default extended
-- protocol parsed only the first statement of 0001's multi-statement
-- body (see migrate.go switch to QueryExecModeSimpleProtocol). On
-- healthy DBs these are no-ops.
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

ALTER TABLE pm_workspaces
    ADD COLUMN IF NOT EXISTS org_id TEXT;

CREATE INDEX IF NOT EXISTS pm_workspaces_org_id_idx ON pm_workspaces (org_id);

CREATE TABLE IF NOT EXISTS pm_workspace_members (
    workspace_id UUID NOT NULL REFERENCES pm_workspaces (id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT 'member',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX IF NOT EXISTS pm_workspace_members_user_id_idx ON pm_workspace_members (user_id);

CREATE TABLE IF NOT EXISTS pm_file_blobs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL REFERENCES pm_workspaces (id) ON DELETE CASCADE,
    owner_user_id  TEXT NOT NULL,
    filename       TEXT NOT NULL,
    content_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes     BIGINT NOT NULL,
    storage_key    TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS pm_file_blobs_workspace_id_idx ON pm_file_blobs (workspace_id);
