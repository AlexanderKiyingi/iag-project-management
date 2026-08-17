package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformdb "github.com/alvor-technologies/iag-platform-go/db"
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
	// Sizing comes from the shared platform package so every service is tuned
	// through the same variables. The previous MaxConns default of 50 is kept
	// deliberately rather than dropping to the package default of 10: cutting a
	// busy service's pool fivefold is a capacity decision to make on measured
	// evidence, not a side effect of a refactor.
	pcfg := platformdb.ConfigFromEnv("iag_pm, public")
	pcfg.URL = url
	if pcfg.MaxConns == 0 {
		pcfg.MaxConns = 50
	}
	cfg, err = platformdb.BuildPoolConfig(pcfg)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

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
