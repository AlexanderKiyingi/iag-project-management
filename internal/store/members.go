package store

import (
	"context"

	"github.com/google/uuid"
)

// WorkspaceAudience returns user IDs that should receive workspace push events (owner + members).
func (r *Repository) WorkspaceAudience(ctx context.Context, workspaceID uuid.UUID, ownerUserID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id FROM pm_workspace_members WHERE workspace_id = $1`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := map[string]struct{}{ownerUserID: {}}
	out := []string{ownerUserID}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out, rows.Err()
}
