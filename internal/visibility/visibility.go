// Package visibility enforces members-only project access for workspace readers.
package visibility

import (
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/search"
)

// Apply removes private projects and dependent entities from doc for viewer.
// The workspace owner always sees the full document.
func Apply(doc *models.Document, ownerUserID, viewerUserID string) {
	if doc == nil || len(doc.Projects) == 0 {
		return
	}
	if viewerUserID != "" && viewerUserID == ownerUserID {
		return
	}
	hidden := HiddenProjectIDs(*doc, ownerUserID, viewerUserID)
	if len(hidden) == 0 {
		return
	}
	for id := range hidden {
		delete(doc.Projects, id)
	}
	if len(doc.Tasks) > 0 {
		kept := doc.Tasks[:0]
		for _, t := range doc.Tasks {
			if TaskVisible(t, hidden) {
				kept = append(kept, t)
			}
		}
		doc.Tasks = kept
	}
	filterSprints(doc, hidden)
}

// HiddenProjectIDs returns project IDs the viewer may not see.
func HiddenProjectIDs(doc models.Document, ownerUserID, viewerUserID string) map[string]struct{} {
	hidden := map[string]struct{}{}
	if viewerUserID != "" && viewerUserID == ownerUserID {
		return hidden
	}
	for id, p := range doc.Projects {
		if p.Visibility != models.ProjectVisibilityMembersOnly {
			continue
		}
		if memberContains(p.MemberIDs, viewerUserID) {
			continue
		}
		hidden[id] = struct{}{}
	}
	return hidden
}

// TaskVisible reports whether a task remains visible when hidden projects are stripped.
func TaskVisible(t models.Task, hidden map[string]struct{}) bool {
	if len(hidden) == 0 {
		return true
	}
	if len(t.Projects) == 0 {
		if t.Project == "" {
			return true
		}
		_, drop := hidden[t.Project]
		return !drop
	}
	for _, p := range t.Projects {
		if _, drop := hidden[p]; !drop {
			return true
		}
	}
	return false
}

// FilterSearchHits drops hits anchored to hidden projects.
func FilterSearchHits(hits []search.Hit, hidden map[string]struct{}) []search.Hit {
	if len(hidden) == 0 {
		return hits
	}
	out := make([]search.Hit, 0, len(hits))
	for _, h := range hits {
		if h.ProjectID == "" {
			out = append(out, h)
			continue
		}
		if _, drop := hidden[h.ProjectID]; !drop {
			out = append(out, h)
		}
	}
	return out
}

func memberContains(ids []string, viewer string) bool {
	for _, id := range ids {
		if id == viewer {
			return true
		}
	}
	return false
}

func filterSprints(doc *models.Document, hidden map[string]struct{}) {
	if len(hidden) == 0 || len(doc.Sprints) == 0 {
		return
	}
	kept := doc.Sprints[:0]
	for _, s := range doc.Sprints {
		if s.ProjectID == "" {
			kept = append(kept, s)
			continue
		}
		if _, drop := hidden[s.ProjectID]; !drop {
			kept = append(kept, s)
		}
	}
	doc.Sprints = kept
}
