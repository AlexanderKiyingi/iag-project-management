CREATE TABLE IF NOT EXISTS pm_search_index (
    id                       BIGSERIAL PRIMARY KEY,
    workspace_owner_user_id  TEXT        NOT NULL,
    entity_type              TEXT        NOT NULL,
    entity_id                TEXT        NOT NULL,
    project_id               TEXT,
    title                    TEXT        NOT NULL DEFAULT '',
    body                     TEXT        NOT NULL DEFAULT '',
    tags                     TEXT        NOT NULL DEFAULT '',
    document                 TSVECTOR    NOT NULL,
    indexed_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_owner_user_id, entity_type, entity_id)
);

CREATE INDEX IF NOT EXISTS pm_search_index_tsv_idx
    ON pm_search_index USING GIN (document);

CREATE INDEX IF NOT EXISTS pm_search_index_workspace_idx
    ON pm_search_index (workspace_owner_user_id, entity_type);
