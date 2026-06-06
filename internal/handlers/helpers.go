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
	"github.com/iag/project-management/backend/internal/visibility"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func userID(c *gin.Context) (string, bool) {
	id, ok := middleware.UserID(c)
	return id.String(), ok
}

func requireUserID(c *gin.Context) (string, bool) {
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
	}
	return uid, ok
}

func respondWorkspace(c *gin.Context, ws store.Workspace) {
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var doc models.Document
	if err := json.Unmarshal(ws.Document, &doc); err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "decode workspace")
		return
	}
	visibility.Apply(&doc, ws.OwnerUserID, uid)
	c.JSON(http.StatusOK, gin.H{"data": doc, "version": ws.Version})
}

func mutate(c *gin.Context, svc *workspace.Service, fn func(*models.Document) error) {
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	ws, err := svc.Mutate(c.Request.Context(), uid, fn)
	if err != nil {
		if errors.Is(err, store.ErrVersionConflict) {
			apierr.JSONStatus(c, http.StatusConflict, "version conflict")
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			apierr.JSONStatus(c, http.StatusNotFound, err.Error())
			return
		}
		apierr.JSONStatus(c, http.StatusInternalServerError, "operation failed")
		return
	}
	respondWorkspace(c, ws)
}

func writeMutationError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrVersionConflict) {
		apierr.JSONStatus(c, http.StatusConflict, "version conflict")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		apierr.JSONStatus(c, http.StatusNotFound, err.Error())
		return
	}
	apierr.JSONStatus(c, http.StatusInternalServerError, "operation failed")
}
