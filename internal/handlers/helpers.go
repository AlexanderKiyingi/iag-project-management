package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
)

func userID(c *gin.Context) (string, bool) {
	id, ok := middleware.UserID(c)
	return id.String(), ok
}

func requireUserID(c *gin.Context) (string, bool) {
	uid, ok := userID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
	}
	return uid, ok
}

func respondWorkspace(c *gin.Context, ws store.Workspace) {
	var data any
	_ = json.Unmarshal(ws.Document, &data)
	c.JSON(http.StatusOK, gin.H{"data": data, "version": ws.Version})
}

func mutate(c *gin.Context, svc *workspace.Service, fn func(*models.Document) error) {
	uid, ok := userID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	ws, err := svc.Mutate(c.Request.Context(), uid, fn)
	if err != nil {
		if errors.Is(err, store.ErrVersionConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "version conflict"})
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
		return
	}
	respondWorkspace(c, ws)
}

func writeMutationError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrVersionConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "version conflict"})
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
}
