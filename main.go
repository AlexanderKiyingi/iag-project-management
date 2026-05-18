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
	pmdb "github.com/iag/project-management/backend/db"
	"github.com/iag/project-management/backend/internal/config"
	pgdb "github.com/iag/project-management/backend/internal/db"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/files"
	"github.com/iag/project-management/backend/internal/migrate"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/realtime"
	"github.com/iag/project-management/backend/internal/router"
	"github.com/iag/project-management/backend/internal/store"
)

func main() {
	configureLogger()

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

	var verifier *authclient.Verifier
	if cfg.AuthMode == "jwt" {
		verifier = authclient.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer)
		initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := verifier.Refresh(initCtx); err != nil {
			cancel()
			slog.Error("jwks refresh", "err", err)
			os.Exit(1)
		}
		cancel()
		go jwksRefreshLoop(verifier)
	}

	platformAuth := middleware.NewPlatformAuth(middleware.PlatformAuthOptions{
		Mode:          cfg.AuthMode,
		GatewaySecret: cfg.GatewaySecret,
		Verifier:      verifier,
	})

	eventBus := events.New(events.Config{
		Brokers: cfg.KafkaBrokers,
		Enabled: cfg.EventBusEnabled,
	})
	defer func() { _ = eventBus.Close() }()
	if eventBus.Enabled() {
		slog.Info("event bus enabled", "brokers", cfg.KafkaBrokers)
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
		slog.Info("API listening", "addr", cfg.Addr, "authMode", cfg.AuthMode)
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

func jwksRefreshLoop(v *authclient.Verifier) {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		if err := v.Refresh(context.Background()); err != nil {
			slog.Warn("jwks refresh", "err", err)
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
