package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/config"
	"github.com/iag/project-management/backend/internal/store"
)

type PlatformStatus struct {
	Cfg  config.Config
	Repo *store.Repository
}

func (p *PlatformStatus) Register(rg *gin.RouterGroup) {
	rg.GET("/platform/status", auth.RequireStaff(), p.status)
}

func (p *PlatformStatus) status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	dbOK := p.Repo.Ping(ctx) == nil

	c.JSON(http.StatusOK, gin.H{
		"service":          "iag-project-management",
		"audience":         p.Cfg.Audience,
		"publicApiUrl":     p.Cfg.PublicAPIURL,
		"gatewayApiPrefix": p.Cfg.GatewayAPIPrefix,
		"databaseOk":       dbOK,
	})
}
