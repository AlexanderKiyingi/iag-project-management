package router

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/config"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/files"
	"github.com/iag/project-management/backend/internal/handlers"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/realtime"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
)

type Options struct {
	Cfg          config.Config
	PlatformAuth *middleware.PlatformAuth
	Repo         *store.Repository
	Hub          *realtime.Hub
	RedisBridge  *realtime.RedisBridge
	Events       *events.Bus
	FileStore    *files.Store
}

func New(opts Options) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware(opts.Cfg.CORSOrigin))
	r.Use(securityHeaders())

	health := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	r.GET("/healthz", health)
	r.GET("/health", health)
	r.GET("/ready", func(c *gin.Context) {
		if err := opts.Repo.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "database": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "database": true})
	})

	if opts.PlatformAuth != nil {
		r.Use(opts.PlatformAuth.AttachPrincipal())
	}

	api := r.Group("/api/v1")
	svc := &workspace.Service{
		Repo:   opts.Repo,
		Hub:    opts.Hub,
		Redis:  opts.RedisBridge,
		Events: opts.Events,
	}
	(&handlers.Workspace{Svc: svc, Platform: opts.PlatformAuth}).Register(api)
	(&handlers.Entities{Svc: svc, Files: opts.FileStore}).Register(api)
	(&handlers.PlatformStatus{Cfg: opts.Cfg, Repo: opts.Repo}).Register(api)

	return r
}

func corsMiddleware(allowed string) gin.HandlerFunc {
	allowedOrigins := splitAllowedOrigins(allowed)
	allowAny := allowed == "*"
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowAny || (origin != "" && originAllowed(origin, allowedOrigins)) {
			if origin != "" {
				c.Header("Access-Control-Allow-Origin", origin)
			} else if allowAny {
				c.Header("Access-Control-Allow-Origin", "*")
			}
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, If-Match, X-Workspace-User")
		c.Header("Access-Control-Expose-Headers", "ETag")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func splitAllowedOrigins(allowed string) []string {
	if allowed == "" || allowed == "*" {
		return nil
	}
	parts := strings.Split(allowed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func originAllowed(origin string, allowed []string) bool {
	for _, candidate := range allowed {
		if origin == candidate {
			return true
		}
	}
	return false
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Next()
	}
}

func requestTimeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if timeout <= 0 || strings.HasPrefix(c.Request.URL.Path, "/api/v1/ws/") {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func getRequestTimeout() time.Duration {
	timeout := os.Getenv("REQUEST_TIMEOUT")
	if timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
