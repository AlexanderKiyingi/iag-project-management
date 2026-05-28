package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/alvor-technologies/iag-authclient"
	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"
	pmdb "github.com/iag/project-management/backend/db"
	"github.com/iag/project-management/backend/internal/config"
	pgdb "github.com/iag/project-management/backend/internal/db"
	pmconsumer "github.com/iag/project-management/backend/internal/consumer"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/files"
	"github.com/iag/project-management/backend/internal/jobs"
	"github.com/iag/project-management/backend/internal/migrate"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/realtime"
	"github.com/iag/project-management/backend/internal/router"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
)

func main() {
	configureLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	if os.Getenv("DATABASE_URL") == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := pgdb.Connect(connectCtx, "")
	cancel()
	if err != nil {
		slog.Error("connect postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := autoMigrate(context.Background(), pool); err != nil {
			slog.Error("auto-migrate", "err", err)
			os.Exit(1)
		}
	}

	repo := store.NewRepository(pool)
	hub := realtime.NewHub()

	var redisBridge *realtime.RedisBridge
	if redisURL := strings.TrimSpace(os.Getenv("REDIS_URL")); redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			slog.Warn("REDIS_URL invalid", "err", err)
		} else {
			rdb := redis.NewClient(opt)
			if err := rdb.Ping(context.Background()).Err(); err != nil {
				slog.Warn("redis unavailable", "err", err)
			} else {
				redisBridge = realtime.NewRedisBridge(rdb, hub)
				subCtx, subCancel := context.WithCancel(context.Background())
				go redisBridge.RunSubscriber(subCtx)
				defer subCancel()
				defer func() { _ = rdb.Close() }()
				slog.Info("redis pub/sub enabled for workspace ws")
			}
		}
	}

	verifier := authclient.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer, cfg.Audience)
	initCtx, refreshCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := verifier.Refresh(initCtx); err != nil {
		refreshCancel()
		slog.Error("jwks refresh", "err", err)
		os.Exit(1)
	}
	refreshCancel()
	go jwksRefreshLoop(ctx, verifier)

	platformAuth := middleware.NewPlatformAuth(middleware.PlatformAuthOptions{
		Verifier: verifier,
	})

	if cfg.ServiceClientSecret != "" {
		go registerPermissionsLoop(ctx, cfg)
	} else {
		slog.Warn("SERVICE_CLIENT_SECRET unset — skipping permissions registration")
	}

	eventBus := events.New(events.Config{
		Brokers: cfg.KafkaBrokers,
		Enabled: cfg.EventBusEnabled,
	})
	defer func() { _ = eventBus.Close() }()
	if eventBus.Enabled() {
		slog.Info("event bus enabled", "brokers", cfg.KafkaBrokers)
	}

	go remindersLoop(ctx, repo, eventBus, cfg.RemindersInterval)
	go archiveLoop(ctx, repo, cfg.ArchiveInterval)

	if cfg.ConsumerEnabled {
		wsSvc := &workspace.Service{Repo: repo, Hub: hub, Redis: redisBridge, Events: eventBus}
		consumer, closeDLQ, err := pmconsumer.New(pmconsumer.Options{
			Brokers:  cfg.KafkaBrokers,
			GroupID:  cfg.ConsumerGroupID,
			DLQTopic: cfg.ConsumerDLQTopic,
			Pool:     pool,
			Handler:  &pmconsumer.Handler{Svc: wsSvc, Repo: repo},
		})
		if err != nil {
			slog.Error("consumer init", "err", err)
			os.Exit(1)
		}
		defer closeDLQ()
		defer func() { _ = consumer.Close() }()
		go func() {
			if err := consumer.Run(ctx); err != nil {
				slog.Warn("consumer stopped", "err", err)
			}
		}()
		slog.Info("commercial consumer enabled", "group", cfg.ConsumerGroupID, "dlq", cfg.ConsumerDLQTopic)
	}

	fileStore := files.NewStore(pool, cfg.UploadDir)

	engine := router.New(router.Options{
		Cfg:          cfg,
		PlatformAuth: platformAuth,
		Repo:         repo,
		Hub:          hub,
		RedisBridge:  redisBridge,
		Events:       eventBus,
		FileStore:    fileStore,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		slog.Info("API listening", "addr", cfg.Addr, "audience", cfg.Audience)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		slog.Info("shutdown", "signal", sig.String())
	case err := <-listenErr:
		slog.Error("listener died", "err", err)
		os.Exit(1)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
}

func registerPermissionsLoop(ctx context.Context, cfg config.Config) {
	saClient := platformserviceauth.NewClient(platformserviceauth.Options{
		TokenURL:     cfg.AuthTokenURL,
		ClientID:     cfg.ServiceClientID,
		ClientSecret: cfg.ServiceClientSecret,
		Audience:     "iag.authentication",
	})
	descriptors := models.PermissionDescriptors()
	perms := make([]platformserviceauth.Permission, 0, len(descriptors))
	for _, d := range descriptors {
		perms = append(perms, platformserviceauth.Permission{
			Name:        d.Name,
			Description: d.Description,
		})
	}

	backoff := time.Second
	const maxBackoff = 5 * time.Minute
	for {
		regCtx, c := context.WithTimeout(ctx, 10*time.Second)
		err := platformserviceauth.RegisterPermissions(regCtx, saClient, cfg.JWTIssuer, "project-management", perms)
		c()
		if err == nil {
			slog.Info("permissions registered with auth service")
			return
		}
		slog.Warn("permissions registration failed; retrying", "err", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func configureLogger() {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	var handler slog.Handler
	if strings.ToLower(os.Getenv("LOG_FORMAT")) == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))
}

func jwksRefreshLoop(ctx context.Context, v *authclient.Verifier) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := v.Refresh(refreshCtx); err != nil {
				slog.Warn("jwks refresh", "err", err)
			}
			cancel()
		}
	}
}

// archiveLoop runs jobs.RunArchive on the configured interval until ctx is
// cancelled. interval<=0 disables the loop. Bounded by a 10-minute per-tick
// timeout (archive scans every workspace, so slower than reminders).
func archiveLoop(ctx context.Context, repo *store.Repository, interval time.Duration) {
	if interval <= 0 {
		slog.Info("archive loop disabled (interval <= 0)")
		return
	}
	slog.Info("archive loop started", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	cfg := jobs.DefaultArchiveConfig()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			if _, err := jobs.RunArchive(jobCtx, repo, cfg); err != nil {
				slog.Warn("archive loop", "err", err)
			}
			cancel()
		}
	}
}

// remindersLoop runs jobs.RunReminders on the configured interval until ctx is
// cancelled. interval<=0 disables the loop. Each tick is bounded by a 5-minute
// timeout so a slow run cannot block shutdown indefinitely.
func remindersLoop(ctx context.Context, repo *store.Repository, bus *events.Bus, interval time.Duration) {
	if interval <= 0 {
		slog.Info("reminders loop disabled (interval <= 0)")
		return
	}
	slog.Info("reminders loop started", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if _, err := jobs.RunReminders(jobCtx, repo, bus); err != nil {
				slog.Warn("reminders loop", "err", err)
			}
			cancel()
		}
	}
}

func autoMigrate(parent context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	applied, err := migrate.Up(ctx, pool, pmdb.Migrations())
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if len(applied) == 0 {
		slog.Info("schema already up to date")
	} else {
		slog.Info("migrations applied", "versions", applied)
	}
	return nil
}
