package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/iag/project-management/backend/internal/models"
)

// FindOwnerByProjectRef scans workspaces for a project id or code match.
func (r *Repository) FindOwnerByProjectRef(ctx context.Context, projectRef string) (string, bool) {
	projectRef = strings.TrimSpace(projectRef)
	if projectRef == "" {
		return "", false
	}
	list, err := r.ListWorkspaces(ctx)
	if err != nil {
		return "", false
	}
	for _, ws := range list {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			continue
		}
		for id, p := range doc.Projects {
			if id == projectRef || strings.EqualFold(p.Code, projectRef) || strings.EqualFold(p.ID, projectRef) {
				return ws.OwnerUserID, true
			}
		}
	}
	return "", false
}
