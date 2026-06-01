// Package search provides server-side full-text search over the PM
// workspace document, backed by Postgres FTS (tsvector + GIN). The
// indexer is called from the workspace service after every successful
// mutation: it diffs the current document into pm_search_index so the
// query endpoint can return ranked, paginated results without making
// the frontend hold a 50k-task workspace in memory.
package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/project-management/backend/internal/models"
)

// Supported entity types — kept tight so the query endpoint can
// validate `type=` against this allow-list.
const (
	TypeTask    = "task"
	TypeProject = "project"
	TypeGoal    = "goal"
	TypeMessage = "message"
)

// Hit is one returned search match.
type Hit struct {
	EntityType string  `json:"entityType"`
	EntityID   string  `json:"entityId"`
	ProjectID  string  `json:"projectId,omitempty"`
	Title      string  `json:"title"`
	Snippet    string  `json:"snippet,omitempty"`
	Rank       float64 `json:"rank"`
}

// Service is the storage-side of search.
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Reindex rebuilds the search index for one workspace from its current
// document state. Cheap-enough to call on every successful mutation
// because the document is bounded; large-workspace optimization can
// switch to incremental diffs later without changing the consumer
// surface.
func (s *Service) Reindex(ctx context.Context, ownerUserID string, doc models.Document) error {
	if s == nil || s.pool == nil {
		return errors.New("search disabled")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM pm_search_index WHERE workspace_owner_user_id = $1`, ownerUserID); err != nil {
		return fmt.Errorf("clear: %w", err)
	}

	rows := buildRows(ownerUserID, doc)
	if len(rows) == 0 {
		return tx.Commit(ctx)
	}

	const stmt = `
		INSERT INTO pm_search_index
			(workspace_owner_user_id, entity_type, entity_id, project_id,
			 title, body, tags, document)
		VALUES ($1,$2,$3,$4,$5,$6,$7,
		        setweight(to_tsvector('simple', COALESCE($5,'')), 'A') ||
		        setweight(to_tsvector('simple', COALESCE($7,'')), 'B') ||
		        setweight(to_tsvector('simple', COALESCE($6,'')), 'C'))
	`
	for _, r := range rows {
		if _, err := tx.Exec(ctx, stmt,
			ownerUserID, r.entityType, r.entityID, nullable(r.projectID),
			r.title, r.body, r.tags); err != nil {
			return fmt.Errorf("insert %s/%s: %w", r.entityType, r.entityID, err)
		}
	}
	return tx.Commit(ctx)
}

// Query searches across one workspace's index. EntityType filter is
// optional; an empty string searches every type.
type QueryInput struct {
	OwnerUserID string
	Q           string
	EntityType  string
	Limit       int
	Offset      int
}

func (s *Service) Query(ctx context.Context, in QueryInput) ([]Hit, int, error) {
	if s == nil || s.pool == nil {
		return nil, 0, errors.New("search disabled")
	}
	q := strings.TrimSpace(in.Q)
	if q == "" {
		return nil, 0, nil
	}
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{in.OwnerUserID, q}
	clauses := "WHERE workspace_owner_user_id = $1 AND document @@ plainto_tsquery('simple', $2)"
	if in.EntityType != "" {
		args = append(args, in.EntityType)
		clauses += " AND entity_type = $3"
	}
	args = append(args, limit, in.Offset)
	limitIdx := len(args) - 1
	offsetIdx := len(args)
	sql := fmt.Sprintf(`
		SELECT entity_type, entity_id, COALESCE(project_id,'') AS project_id,
		       title,
		       ts_headline('simple', COALESCE(body, ''), plainto_tsquery('simple', $2),
		                   'MaxFragments=1, StartSel=<mark>, StopSel=</mark>') AS snippet,
		       ts_rank(document, plainto_tsquery('simple', $2)) AS rank
		FROM pm_search_index
		%s
		ORDER BY rank DESC, indexed_at DESC
		LIMIT $%d OFFSET $%d
	`, clauses, limitIdx, offsetIdx)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.EntityType, &h.EntityID, &h.ProjectID, &h.Title, &h.Snippet, &h.Rank); err != nil {
			return nil, 0, err
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Cheap total: re-count without LIMIT/OFFSET. For workspaces with
	// huge result sets this is still a tsvector scan but acceptable
	// given the GIN index.
	countArgs := []any{in.OwnerUserID, q}
	countClauses := "WHERE workspace_owner_user_id = $1 AND document @@ plainto_tsquery('simple', $2)"
	if in.EntityType != "" {
		countArgs = append(countArgs, in.EntityType)
		countClauses += " AND entity_type = $3"
	}
	var total int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM pm_search_index "+countClauses, countArgs...).Scan(&total); err != nil {
		return hits, len(hits), nil
	}
	return hits, total, nil
}

// ----- helpers -----

type row struct {
	entityType, entityID, projectID, title, body, tags string
}

func buildRows(ownerUserID string, doc models.Document) []row {
	_ = ownerUserID
	out := make([]row, 0, len(doc.Tasks)+len(doc.Goals)+len(doc.Projects)+len(doc.Messages))
	for _, t := range doc.Tasks {
		if t.DeletedAt != "" {
			continue
		}
		project := t.Project
		if project == "" && len(t.Projects) > 0 {
			project = t.Projects[0]
		}
		out = append(out, row{
			entityType: TypeTask,
			entityID:   formatTaskID(t.ID),
			projectID:  project,
			title:      t.Name,
			body:       t.Desc,
			tags:       strings.Join(t.Tags, " "),
		})
	}
	for id, p := range doc.Projects {
		summary := ""
		if len(p.StatusHistory) > 0 {
			summary = p.StatusHistory[len(p.StatusHistory)-1].Summary
		}
		out = append(out, row{
			entityType: TypeProject,
			entityID:   id,
			projectID:  id,
			title:      p.Name,
			body:       summary,
			tags:       p.Code,
		})
	}
	for _, g := range doc.Goals {
		out = append(out, row{
			entityType: TypeGoal,
			entityID:   formatGoalID(g.ID),
			title:      g.Name,
			body:       summarizeKeyResults(g.KeyResults),
			tags:       g.Team,
		})
	}
	for _, m := range doc.Messages {
		if m.Deleted || m.Text == "" {
			continue
		}
		out = append(out, row{
			entityType: TypeMessage,
			entityID:   formatMessageID(m.ID),
			title:      truncate(m.Text, 80),
			body:       m.Text,
		})
	}
	return out
}

func summarizeKeyResults(krs []models.KeyResult) string {
	if len(krs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(krs))
	for _, kr := range krs {
		parts = append(parts, kr.Name)
	}
	return strings.Join(parts, " · ")
}

func formatTaskID(id int) string    { return fmt.Sprintf("%d", id) }
func formatGoalID(id int) string    { return fmt.Sprintf("%d", id) }
func formatMessageID(id int) string { return fmt.Sprintf("%d", id) }

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// ----- delete-by-workspace, used when a workspace is archived -----

func (s *Service) Purge(ctx context.Context, ownerUserID string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `DELETE FROM pm_search_index WHERE workspace_owner_user_id = $1`, ownerUserID)
	return err
}
