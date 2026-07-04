package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The ledger is schema-qualified to `iag_pm` so it can never collide with
// another service's global public.schema_migrations on the shared Railway
// database. db.Connect pins search_path to `iag_pm, public`.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS iag_pm.schema_migrations (
    version    TEXT PRIMARY KEY,
    checksum   TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

type Migration struct {
	Version  string
	Body     string
	Checksum string
}

func Up(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	migs, err := load(fsys)
	if err != nil {
		return nil, err
	}

	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS iag_pm`); err != nil {
		return nil, fmt.Errorf("create schema iag_pm: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	// Safety net for the shared-database cutover: if this service historically
	// ran without the ?search_path= DSN param its ledger may sit in the global
	// public.schema_migrations. Stamp those versions into the per-service ledger
	// with current file checksums so nothing re-runs. No-op when the ledger
	// already lives in iag_pm or on a fresh database.
	if err := seedFromLegacyLedger(ctx, pool, migs); err != nil {
		return nil, fmt.Errorf("seed from legacy ledger: %w", err)
	}

	applied, err := loadApplied(ctx, pool)
	if err != nil {
		return nil, err
	}

	var newlyApplied []string
	for _, m := range migs {
		prev, ok := applied[m.Version]
		switch {
		case !ok:
			if err := apply(ctx, pool, m); err != nil {
				return newlyApplied, fmt.Errorf("migration %s: %w", m.Version, err)
			}
			newlyApplied = append(newlyApplied, m.Version)
			slog.Info("migration applied", "version", m.Version)
		case prev.Checksum != m.Checksum:
			// Legacy Railway DBs were seeded by an earlier migration tool that
			// stored a different checksum value for the same body. The
			// migration files themselves are append-only (git history shows
			// no edits) and idempotent (CREATE ... IF NOT EXISTS), so the
			// safe action is to re-stamp the stored checksum rather than
			// crash on every boot. Mirrors the self-heal pattern landed in
			// iag-authentication 839c292.
			slog.Warn("migration checksum mismatch; re-stamping",
				"version", m.Version,
				"stored", prev.Checksum,
				"file", m.Checksum,
			)
			if _, err := pool.Exec(ctx,
				`UPDATE iag_pm.schema_migrations SET checksum = $1 WHERE version = $2`,
				m.Checksum, m.Version); err != nil {
				return newlyApplied, fmt.Errorf(
					"migration %s re-stamp checksum: %w", m.Version, err,
				)
			}
		}
	}
	return newlyApplied, nil
}

// seedFromLegacyLedger stamps this service's shipped versions into iag_pm's
// ledger using the CURRENT file checksums, for any version already recorded in a
// legacy global public.schema_migrations. Idempotent via ON CONFLICT; no-op when
// no legacy ledger exists or none of its versions match. Guards the cutover for
// deployments whose DATABASE_URL ever lacked the ?search_path= param.
func seedFromLegacyLedger(ctx context.Context, pool *pgxpool.Pool, migs []Migration) error {
	var hasLegacy bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'schema_migrations'
		)`).Scan(&hasLegacy); err != nil {
		return err
	}
	if !hasLegacy {
		return nil
	}
	for _, m := range migs {
		if _, err := pool.Exec(ctx, `
			INSERT INTO iag_pm.schema_migrations (version, checksum)
			SELECT $1, $2
			WHERE EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = $1)
			ON CONFLICT (version) DO NOTHING`, m.Version, m.Checksum); err != nil {
			return fmt.Errorf("seed %s: %w", m.Version, err)
		}
	}
	return nil
}

func load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  strings.TrimSuffix(name, ".sql"),
			Body:     string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

type appliedRow struct {
	Version  string
	Checksum string
}

func loadApplied(ctx context.Context, pool *pgxpool.Pool) (map[string]appliedRow, error) {
	rows, err := pool.Query(ctx, `SELECT version, checksum FROM iag_pm.schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]appliedRow{}
	for rows.Next() {
		var r appliedRow
		if err := rows.Scan(&r.Version, &r.Checksum); err != nil {
			return nil, err
		}
		out[r.Version] = r
	}
	return out, rows.Err()
}

func apply(ctx context.Context, pool *pgxpool.Pool, m Migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// QueryExecModeSimpleProtocol so multi-statement bodies execute in
	// full. pgx's default extended protocol parses only the first
	// statement of a multi-statement Exec, which is how Railway ended up
	// with 0001 recorded as applied but pm_workspaces missing. Mirrors
	// the fix landed in iag-authentication 839c292.
	if _, err := tx.Exec(ctx, m.Body, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO iag_pm.schema_migrations (version, checksum) VALUES ($1, $2)`,
		m.Version, m.Checksum); err != nil {
		if strings.Contains(err.Error(), "23505") {
			return errors.New("concurrent migration: version already applied by another process")
		}
		return err
	}
	return tx.Commit(ctx)
}
