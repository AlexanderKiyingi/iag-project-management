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
	doc, err := h.Svc.DocumentForViewer(ws, uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "decode workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": doc, "version": ws.Version})
}

func workspaceRolesChanged(before, after []models.WorkspaceRole) bool {
	if len(before) != len(after) {
		return true
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	return string(b) != string(a)
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
	var incoming models.Document
	if err := json.Unmarshal(body.Data, &incoming); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid workspace document")
		return
	}
	var existingDoc models.Document
	if err := json.Unmarshal(ws.Document, &existingDoc); err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "decode workspace")
		return
	}
	if workspaceRolesChanged(existingDoc.Roles, incoming.Roles) && !auth.HasPerm(c, "pm.admin") {
		apierr.JSONStatus(c, http.StatusForbidden, "pm.admin required to change workspace roles")
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
	userID, claims, err := h.Platform.VerifyBearerToken(token)
	if err != nil {
		apierr.JSONStatus(c, http.StatusUnauthorized, "invalid token")
		return
	}
	if !auth.HasPermClaims(claims, claims.Permissions, "pm.view_workspace") &&
		!auth.HasPermClaims(claims, claims.Permissions, "pm.mutate_workspace") {
		apierr.JSONStatus(c, http.StatusForbidden, "permission denied")
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
		if push, pushErr := h.Svc.FilteredPush(ws, uid); pushErr == nil {
			raw, _ := json.Marshal(push)
			_ = conn.WriteMessage(websocket.TextMessage, raw)
		}
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
