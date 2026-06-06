package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pmdb "github.com/iag/project-management/backend/db"
)

var ErrVersionConflict = errors.New("workspace version conflict")

type Workspace struct {
	ID          uuid.UUID
	OwnerUserID string
	Document    json.RawMessage
	Version     int64
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Pool exposes the underlying pgx pool for adapters (audit, search, outbox)
// that need direct database access alongside the workspace store.
func (r *Repository) Pool() *pgxpool.Pool {
	return r.pool
}

func (r *Repository) GetOrCreate(ctx context.Context, ownerUserID string) (Workspace, error) {
	ws, err := r.get(ctx, ownerUserID)
	if err == nil {
		return ws, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, err
	}
	doc := pmdb.DefaultWorkspaceJSON()
	if !json.Valid(doc) {
		return Workspace{}, fmt.Errorf("default workspace json invalid")
	}
	var id uuid.UUID
	var version int64
	err = r.pool.QueryRow(ctx, `
		INSERT INTO pm_workspaces (owner_user_id, document)
		VALUES ($1, $2::jsonb)
		ON CONFLICT (owner_user_id) DO UPDATE SET owner_user_id = EXCLUDED.owner_user_id
		RETURNING id, version`,
		ownerUserID, doc,
	).Scan(&id, &version)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{
		ID:          id,
		OwnerUserID: ownerUserID,
		Document:    json.RawMessage(doc),
		Version:     version,
	}, nil
}

// GetByOwner loads a workspace by owner user id.
func (r *Repository) GetByOwner(ctx context.Context, ownerUserID string) (Workspace, error) {
	return r.get(ctx, ownerUserID)
}

func (r *Repository) get(ctx context.Context, ownerUserID string) (Workspace, error) {
	var ws Workspace
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_user_id, document, version
		FROM pm_workspaces WHERE owner_user_id = $1`,
		ownerUserID,
	).Scan(&ws.ID, &ws.OwnerUserID, &ws.Document, &ws.Version)
	return ws, err
}

func (r *Repository) Update(ctx context.Context, ownerUserID string, document json.RawMessage, expectedVersion int64) (Workspace, error) {
	if !json.Valid(document) {
		return Workspace{}, fmt.Errorf("document must be valid json")
	}
	var ws Workspace
	err := r.pool.QueryRow(ctx, `
		UPDATE pm_workspaces
		SET document = $2::jsonb,
		    version = version + 1,
		    updated_at = NOW()
		WHERE owner_user_id = $1 AND version = $3
		RETURNING id, owner_user_id, document, version`,
		ownerUserID, document, expectedVersion,
	).Scan(&ws.ID, &ws.OwnerUserID, &ws.Document, &ws.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrVersionConflict
	}
	return ws, err
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// ListWorkspaces returns every persisted workspace (used by scheduled reminder jobs).
func (r *Repository) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, owner_user_id, document, version
		FROM pm_workspaces
		ORDER BY owner_user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Workspace
	for rows.Next() {
		var ws Workspace
		if err := rows.Scan(&ws.ID, &ws.OwnerUserID, &ws.Document, &ws.Version); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}
