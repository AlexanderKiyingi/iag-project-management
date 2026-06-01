package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/search"
)

type Search struct {
	Svc *search.Service
}

func (h *Search) Register(rg *gin.RouterGroup) {
	rg.GET("/search", auth.RequireWorkspaceRead(), h.query)
}

func (h *Search) query(c *gin.Context) {
	if h.Svc == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "search disabled"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q is required"})
		return
	}
	entityType := strings.TrimSpace(c.Query("type"))
	switch entityType {
	case "", search.TypeTask, search.TypeProject, search.TypeGoal, search.TypeMessage:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported type"})
		return
	}
	hits, total, err := h.Svc.Query(c.Request.Context(), search.QueryInput{
		OwnerUserID: uid,
		Q:           q,
		EntityType:  entityType,
		Limit:       parsePositiveQuery(c, "limit", 50, 200),
		Offset:      parseNonNegativeQuery(c, "offset", 0),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
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
