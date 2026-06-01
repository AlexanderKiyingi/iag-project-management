package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/iag/project-management/backend/internal/automation"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/realtime"
	"github.com/iag/project-management/backend/internal/store"
)

// Indexer is the search-index hook called after every successful
// mutation. Satisfied by *search.Service; declared as an interface
// here so workspace doesn't import search and create a cycle.
type Indexer interface {
	Reindex(ctx context.Context, ownerUserID string, doc models.Document) error
}

type Service struct {
	Repo       *store.Repository
	Hub        *realtime.Hub
	Redis      *realtime.RedisBridge
	Events     *events.Bus
	Search     Indexer
	// RuleNotify is invoked post-persist for each notify action that
	// fired during the mutation. Wired up in main.go to bus.PublishPMAlert
	// so the resulting iag.commercial event lands in the central inbox.
	// nil here is fine — engine simply skips notify callbacks.
	RuleNotify automation.NotifyHook
}

func (s *Service) Mutate(ctx context.Context, userID string, fn func(*models.Document) error) (store.Workspace, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		ws, err := s.Repo.ResolveForUser(ctx, userID)
		if err != nil {
			return store.Workspace{}, err
		}
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			return store.Workspace{}, err
		}
		prev := cloneDocument(doc)
		if err := fn(&doc); err != nil {
			return store.Workspace{}, err
		}
		// Run the automation engine between the user's mutation and
		// the persist step so rule-driven changes land atomically.
		// Notifications are deferred to a post-persist callback list
		// so we don't publish a notify event for a state that ends up
		// rolled back on version conflict.
		notifyCallbacks := automation.DiffAndRun(&prev, &doc, userID)
		raw, err := json.Marshal(doc)
		if err != nil {
			return store.Workspace{}, err
		}
		updated, err := s.Repo.Update(ctx, ws.OwnerUserID, raw, ws.Version)
		if errors.Is(err, store.ErrVersionConflict) {
			lastErr = err
			continue
		}
		if err != nil {
			return store.Workspace{}, err
		}
		s.BroadcastWorkspace(ctx, updated)
		if s.Search != nil {
			if err := s.Search.Reindex(ctx, ws.OwnerUserID, doc); err != nil {
				slog.Warn("search reindex failed", "owner", ws.OwnerUserID, "err", err)
			}
		}
		for _, cb := range notifyCallbacks {
			cb(s.RuleNotify)
		}
		return updated, nil
	}
	if lastErr != nil {
		return store.Workspace{}, lastErr
	}
	return store.Workspace{}, store.ErrVersionConflict
}

// cloneDocument deep-copies a Document via JSON so the automation
// engine can compare pre and post states without false aliasing on
// the inner slices.
func cloneDocument(d models.Document) models.Document {
	raw, err := json.Marshal(d)
	if err != nil {
		return models.Document{}
	}
	var out models.Document
	_ = json.Unmarshal(raw, &out)
	return out
}

// BroadcastWorkspace notifies the owner and all workspace members (shared tenancy).
func (s *Service) BroadcastWorkspace(ctx context.Context, ws store.Workspace) {
	audience, err := s.Repo.WorkspaceAudience(ctx, ws.ID, ws.OwnerUserID)
	if err != nil || len(audience) == 0 {
		audience = []string{ws.OwnerUserID}
	}
	push := realtime.WorkspacePush{Type: "workspace", Data: ws.Document, Version: ws.Version}
	for _, uid := range audience {
		if s.Redis != nil {
			_ = s.Redis.Publish(ctx, uid, push)
			continue
		}
		if s.Hub != nil {
			s.Hub.Broadcast(uid, push)
		}
	}
}

func (s *Service) LoadDocument(ctx context.Context, userID string) (models.Document, store.Workspace, error) {
	ws, err := s.Repo.ResolveForUser(ctx, userID)
	if err != nil {
		return models.Document{}, store.Workspace{}, err
	}
	var doc models.Document
	if err := json.Unmarshal(ws.Document, &doc); err != nil {
		return models.Document{}, store.Workspace{}, err
	}
	return doc, ws, nil
}
