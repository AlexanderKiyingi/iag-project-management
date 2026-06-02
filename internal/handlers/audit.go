package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/project-management/backend/internal/audit"
	"github.com/iag/project-management/backend/internal/auth"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

type Audit struct {
	Pool *pgxpool.Pool
}

func (h *Audit) Register(rg *gin.RouterGroup) {
	rg.GET("/audit/requests", auth.RequirePerm("pm.admin"), h.listRequests)
}

// listRequests returns the request-level audit. Admin-only because the
// table can include IPs and user agents — not data unprivileged callers
// should see.
func (h *Audit) listRequests(c *gin.Context) {
	if h.Pool == nil {
		apierr.JSONStatus(c, http.StatusNotImplemented, "audit disabled")
		return
	}
	f := audit.ListFilter{
		WorkspaceOwnerUserID: strings.TrimSpace(c.Query("workspaceOwnerUserId")),
		ActorUserID:          strings.TrimSpace(c.Query("actor")),
		Path:                 strings.TrimSpace(c.Query("path")),
		Limit:                parsePositiveQuery(c, "limit", 100, 500),
		Offset:               parseNonNegativeQuery(c, "offset", 0),
	}
	if v := strings.TrimSpace(c.Query("from")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = &t
		}
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = &t
		}
	}
	rows, err := audit.List(c.Request.Context(), h.Pool, f)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "audit query failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "limit": f.Limit, "offset": f.Offset})
}
