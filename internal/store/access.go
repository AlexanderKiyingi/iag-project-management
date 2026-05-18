package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ResolveForUser returns the workspace owned by userID, or one shared via pm_workspace_members.
func (r *Repository) ResolveForUser(ctx context.Context, userID string) (Workspace, error) {
	ws, err := r.get(ctx, userID)
	if err == nil {
		return ws, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, err
	}

	var shared Workspace
	err = r.pool.QueryRow(ctx, `
		SELECT w.id, w.owner_user_id, w.document, w.version
		FROM pm_workspaces w
		INNER JOIN pm_workspace_members m ON m.workspace_id = w.id
		WHERE m.user_id = $1
		ORDER BY w.updated_at DESC
		LIMIT 1`,
		userID,
	).Scan(&shared.ID, &shared.OwnerUserID, &shared.Document, &shared.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r.GetOrCreate(ctx, userID)
		}
		return Workspace{}, err
	}
	return shared, nil
}

func (r *Repository) SetOrgID(ctx context.Context, ownerUserID, orgID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE pm_workspaces SET org_id = NULLIF($2, ''), updated_at = NOW()
		WHERE owner_user_id = $1`,
		ownerUserID, orgID,
	)
	return err
}

func (r *Repository) AddMember(ctx context.Context, workspaceID uuid.UUID, userID, role string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO pm_workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		workspaceID, userID, role,
	)
	return err
}
