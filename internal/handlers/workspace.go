package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Workspace struct {
	Svc      *workspace.Service
	Platform *middleware.PlatformAuth
}

func (h *Workspace) Register(rg *gin.RouterGroup) {
	rg.GET("/workspace", auth.RequireWorkspaceRead(), h.get)
	rg.PUT("/workspace", auth.RequireWorkspaceWrite(), h.put)
	rg.GET("/ws/workspace", h.ws)
}

func (h *Workspace) get(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	var doc models.Document
	if err := json.Unmarshal(ws.Document, &doc); err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "decode workspace")
		return
	}
	applyProjectVisibility(&doc, uid)
	c.JSON(http.StatusOK, gin.H{"data": doc, "version": ws.Version})
}

// applyProjectVisibility removes private projects (and tasks anchored
// to them) from the view returned to a caller who is not a member of
// those projects. The legacy Project.Visibility="workspace" stays
// visible to everyone; an empty Visibility is treated as workspace too.
// The workspace owner sees every project regardless of MemberIDs.
func applyProjectVisibility(doc *models.Document, viewer string) {
	if doc == nil || len(doc.Projects) == 0 {
		return
	}
	hidden := map[string]struct{}{}
	for id, p := range doc.Projects {
		if p.Visibility != models.ProjectVisibilityMembersOnly {
			continue
		}
		if memberIDsContain(p.MemberIDs, viewer) {
			continue
		}
		hidden[id] = struct{}{}
	}
	if len(hidden) == 0 {
		return
	}
	for id := range hidden {
		delete(doc.Projects, id)
	}
	if len(doc.Tasks) > 0 {
		kept := doc.Tasks[:0]
		for _, t := range doc.Tasks {
			if taskVisibleAfterHiding(t, hidden) {
				kept = append(kept, t)
			}
		}
		doc.Tasks = kept
	}
}

// taskVisibleAfterHiding reports whether a task remains visible given a
// set of project IDs the viewer cannot see. A multi-homed task stays
// visible as long as at least one of its projects is not hidden. A task
// with no projects at all stays visible (legacy back-compat).
func taskVisibleAfterHiding(t models.Task, hidden map[string]struct{}) bool {
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

func memberIDsContain(ids []string, viewer string) bool {
	for _, id := range ids {
		if id == viewer {
			return true
		}
	}
	return false
}

type putBody struct {
	Data    json.RawMessage `json:"data"`
	Version *int64          `json:"version"`
}

func (h *Workspace) put(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var body putBody
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Data) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	expected := int64(0)
	if v := c.GetHeader("If-Match"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			expected = n
		}
	}
	if body.Version != nil {
		expected = *body.Version
	}
	if expected == 0 {
		existing, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
		if err != nil {
			apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
			return
		}
		expected = existing.Version
	}

	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	updated, err := h.Svc.Repo.Update(c.Request.Context(), ws.OwnerUserID, body.Data, expected)
	if errors.Is(err, store.ErrVersionConflict) {
		apierr.JSONStatus(c, http.StatusConflict, "version conflict")
		return
	}
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "save workspace")
		return
	}
	h.Svc.BroadcastWorkspace(c.Request.Context(), updated)
	respondWorkspace(c, updated)
}

func (h *Workspace) ws(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		apierr.JSONStatus(c, http.StatusUnauthorized, "token required")
		return
	}
	if h.Platform == nil {
		apierr.JSONStatus(c, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	userID, _, err := h.Platform.VerifyBearerToken(token)
	if err != nil {
		apierr.JSONStatus(c, http.StatusUnauthorized, "invalid token")
		return
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	uid := userID.String()
	if h.Svc.Hub != nil {
		h.Svc.Hub.Register(uid, conn)
		defer h.Svc.Hub.Unregister(uid, conn)
	}

	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err == nil {
		push, _ := json.Marshal(map[string]any{
			"type": "workspace", "data": json.RawMessage(ws.Document), "version": ws.Version,
		})
		_ = conn.WriteMessage(websocket.TextMessage, push)
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
