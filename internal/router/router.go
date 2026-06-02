package router

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/audit"
	"github.com/iag/project-management/backend/internal/automation"
	"github.com/iag/project-management/backend/internal/config"
	"github.com/iag/project-management/backend/internal/docs"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/files"
	"github.com/iag/project-management/backend/internal/handlers"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/realtime"
	"github.com/iag/project-management/backend/internal/search"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/usersclient"
	"github.com/iag/project-management/backend/internal/webhooks"
	"github.com/iag/project-management/backend/internal/workspace"
)

type Options struct {
	Cfg           config.Config
	PlatformAuth  *middleware.PlatformAuth
	Repo          *store.Repository
	Hub           *realtime.Hub
	RedisBridge   *realtime.RedisBridge
	Events        *events.Bus
	FileStore     *files.Store
	AuditRecorder audit.Recorder
	Search        *search.Service
	RuleNotify    automation.NotifyHook
	Webhooks      *webhooks.Store
}

func New(opts Options) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestTimeout(getRequestTimeout()))
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
	docs.Register(r)

	// Public form intake — unauthenticated; mounted before the
	// PlatformAuth middleware kicks in so external stakeholders can
	// submit without a Bearer token.
	pubForms := &handlers.PublicForms{
		Repo: opts.Repo,
		Svc: &workspace.Service{
			Repo: opts.Repo, Hub: opts.Hub, Redis: opts.RedisBridge,
			Events: opts.Events, Search: opts.Search, RuleNotify: opts.RuleNotify,
		},
	}
	pubForms.Register(r)

	// Public share-token endpoints — read-only views of single tasks
	// and projects, gated by a JWT issued by iag-authentication with
	// audience iag.share. JWKS fetched lazily from the auth service.
	(&handlers.PublicShare{
		Repo:     opts.Repo,
		JWKSURL:  opts.Cfg.JWKSURL,
		Issuer:   opts.Cfg.JWTIssuer,
		Audience: "iag.share",
	}).Register(r)

	if opts.PlatformAuth != nil {
		r.Use(opts.PlatformAuth.AttachPrincipal())
	}
	if opts.AuditRecorder != nil {
		r.Use(audit.NewMiddleware(opts.AuditRecorder))
	}

	api := r.Group("/api/v1")
	svc := &workspace.Service{
		Repo:            opts.Repo,
		Hub:             opts.Hub,
		Redis:           opts.RedisBridge,
		Events:          opts.Events,
		Search:          opts.Search,
		RuleNotify:      opts.RuleNotify,
		WebhookEnqueuer: webhookAdapter{store: opts.Webhooks},
	}
	(&handlers.Workspace{Svc: svc, Platform: opts.PlatformAuth}).Register(api)
	(&handlers.Entities{Svc: svc, Files: opts.FileStore, Users: usersclient.New(opts.Cfg.UsersAPIURL)}).Register(api)
	(&handlers.PlatformStatus{Cfg: opts.Cfg, Repo: opts.Repo}).Register(api)
	(&handlers.Audit{Pool: opts.Repo.Pool()}).Register(api)
	(&handlers.Search{Svc: opts.Search}).Register(api)

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
		// JSON-only API: strictest CSP — nothing loads, nothing frames.
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=(), interest-cohort=()")
		c.Header("X-XSS-Protection", "1; mode=block")
		// HSTS only over TLS so local dev (http://localhost) doesn't lock
		// the browser into HTTPS for the dev domain. Behind Railway/nginx
		// the request arrives as plaintext with X-Forwarded-Proto=https.
		if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}
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

// webhookAdapter satisfies workspace.WebhookEnqueuer by wrapping the
// concrete *webhooks.Store. Keeping the indirection lets the workspace
// package stay free of webhook imports.
type webhookAdapter struct {
	store *webhooks.Store
}

func (a webhookAdapter) Enqueue(ctx context.Context, ownerUserID string, eventType string, payload any, subs []models.WebhookSubscription) error {
	if a.store == nil {
		return nil
	}
	return a.store.Enqueue(ctx, ownerUserID, webhooks.Event{
		Type:      eventType,
		Workspace: ownerUserID,
		Time:      models.ISONow(),
		Data:      payload,
	}, subs)
}
