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
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	var data any
	_ = json.Unmarshal(ws.Document, &data)
	c.JSON(http.StatusOK, gin.H{"data": data, "version": ws.Version})
}

type putBody struct {
	Data    json.RawMessage `json:"data"`
	Version *int64          `json:"version"`
}

func (h *Workspace) put(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	var body putBody
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Data) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
			return
		}
		expected = existing.Version
	}

	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	updated, err := h.Svc.Repo.Update(c.Request.Context(), ws.OwnerUserID, body.Data, expected)
	if errors.Is(err, store.ErrVersionConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "version conflict"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save workspace"})
		return
	}
	h.Svc.BroadcastWorkspace(c.Request.Context(), updated)
	respondWorkspace(c, updated)
}

func (h *Workspace) ws(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token required"})
		return
	}
	if h.Platform == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth not configured"})
		return
	}
	userID, _, err := h.Platform.VerifyBearerToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
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
