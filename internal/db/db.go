package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		return nil, errors.New("DATABASE_URL is empty")
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = intEnv("DB_MAX_CONNS", 50)
	cfg.MinConns = intEnv("DB_MIN_CONNS", 2)
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second

	// Isolate on the shared Railway database in this service's own schema, pinned
	// in code rather than depending on a ?search_path= param in DATABASE_URL
	// (which, if dropped, would silently land tables in public). Overrides any
	// value parsed from the DSN; falls back to public for not-yet-relocated tables.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "iag_pm, public"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

func intEnv(key string, fallback int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return int32(n)
}
