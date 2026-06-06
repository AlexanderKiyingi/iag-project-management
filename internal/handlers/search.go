package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/search"
	"github.com/iag/project-management/backend/internal/visibility"
	"github.com/iag/project-management/backend/internal/workspace"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

type Search struct {
	Svc   *search.Service
	WsSvc *workspace.Service
}

func (h *Search) Register(rg *gin.RouterGroup) {
	rg.GET("/search", auth.RequireWorkspaceRead(), h.query)
}

func (h *Search) query(c *gin.Context) {
	if h.Svc == nil {
		apierr.JSONStatus(c, http.StatusNotImplemented, "search disabled")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "q is required")
		return
	}
	entityType := strings.TrimSpace(c.Query("type"))
	switch entityType {
	case "", search.TypeTask, search.TypeProject, search.TypeGoal, search.TypeMessage:
	default:
		apierr.JSONStatus(c, http.StatusBadRequest, "unsupported type")
		return
	}

	ownerID := uid
	var hidden map[string]struct{}
	if h.WsSvc != nil && h.WsSvc.Repo != nil {
		if ws, err := h.WsSvc.Repo.ResolveForUser(c.Request.Context(), uid); err == nil {
			ownerID = ws.OwnerUserID
			var doc models.Document
			if json.Unmarshal(ws.Document, &doc) == nil {
				hidden = visibility.HiddenProjectIDs(doc, ws.OwnerUserID, uid)
			}
		}
	}

	hits, total, err := h.Svc.Query(c.Request.Context(), search.QueryInput{
		OwnerUserID: ownerID,
		Q:           q,
		EntityType:  entityType,
		Limit:       parsePositiveQuery(c, "limit", 50, 200),
		Offset:      parseNonNegativeQuery(c, "offset", 0),
	})
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "search failed")
		return
	}
	hits = visibility.FilterSearchHits(hits, hidden)
	if len(hidden) > 0 {
		total = len(hits)
	}
	c.JSON(http.StatusOK, gin.H{
		"items":  hits,
		"total":  total,
		"q":      q,
		"type":   entityType,
		"limit":  parsePositiveQuery(c, "limit", 50, 200),
		"offset": parseNonNegativeQuery(c, "offset", 0),
	})
}
