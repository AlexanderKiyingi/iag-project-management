package files

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	pmdb "github.com/iag/project-management/backend/db"
	pgdb "github.com/iag/project-management/backend/internal/db"
	"github.com/iag/project-management/backend/internal/migrate"
)

func TestNewS3Selection(t *testing.T) {
	// Any missing required field means no bucket: the caller falls back to disk.
	for name, args := range map[string][5]string{
		"empty":       {},
		"no bucket":   {"https://acct.r2.cloudflarestorage.com", "auto", "", "k", "s"},
		"no secret":   {"https://acct.r2.cloudflarestorage.com", "auto", "b", "k", ""},
		"no key":      {"https://acct.r2.cloudflarestorage.com", "auto", "b", "", "s"},
		"no endpoint": {"", "auto", "b", "k", "s"},
	} {
		if s := NewS3(args[0], args[1], args[2], args[3], args[4], true); s != nil {
			t.Errorf("%s: want nil store, got %T", name, s)
		}
		if b := NewStore(nil, t.TempDir(), NewS3(args[0], args[1], args[2], args[3], args[4], true)).Backend(); b != BackendDisk {
			t.Errorf("%s: backend = %q, want disk", name, b)
		}
	}
	s3 := NewS3("https://acct.r2.cloudflarestorage.com", "auto", "b", "k", "s", true)
	if s3 == nil {
		t.Fatal("complete config: want a store")
	}
	if b := NewStore(nil, "", s3).Backend(); b != BackendS3 {
		t.Fatalf("backend = %q, want s3", b)
	}
}

// testPool migrates a throwaway database (PM_TEST_DSN) and returns a pool plus
// a workspace id to hang blobs off.
func testPool(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("PM_TEST_DSN")
	if dsn == "" {
		t.Skip("PM_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgdb.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := migrate.Up(ctx, pool, pmdb.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var ws uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO pm_workspaces (owner_user_id) VALUES ($1) RETURNING id`,
		"files-test-"+uuid.NewString()).Scan(&ws); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return pool, ws
}

// testS3 returns the bucket named by PM_TEST_S3_* (MinIO, SeaweedFS, ...).
func testS3(t *testing.T) *Store {
	t.Helper()
	ep := os.Getenv("PM_TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("PM_TEST_S3_ENDPOINT not set")
	}
	s3 := NewS3(ep, "auto", os.Getenv("PM_TEST_S3_BUCKET"),
		os.Getenv("PM_TEST_S3_ACCESS_KEY_ID"), os.Getenv("PM_TEST_S3_SECRET_ACCESS_KEY"), false)
	if s3 == nil {
		t.Fatal("PM_TEST_S3_* incomplete")
	}
	return &Store{s3: s3}
}

func dataURL(b string) string {
	return "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(b))
}

func TestDiskRoundTrip(t *testing.T) {
	pool, ws := testPool(t)
	ctx := context.Background()
	s := NewStore(pool, t.TempDir(), nil)
	meta, err := s.PersistInline(ctx, ws, "u1", "a.txt", dataURL("on disk"))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	rec, err := s.GetBlob(ctx, meta.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Backend != BackendDisk {
		t.Fatalf("backend = %q, want disk", rec.Backend)
	}
	got, err := s.ReadBlob(rec)
	if err != nil || string(got) != "on disk" {
		t.Fatalf("read = %q, %v", got, err)
	}
}

// A store with a bucket writes there, still reads rows written to disk before
// the bucket existed, and Backfill moves those rows across.
func TestS3RoundTripLegacyDiskAndBackfill(t *testing.T) {
	pool, ws := testPool(t)
	ctx := context.Background()
	dir := t.TempDir()
	s := testS3(t)
	s.pool, s.uploadDir = pool, dir

	// A row as written before this change: no storage_backend in the INSERT.
	legacyID := uuid.New()
	legacyKey := filepath.Join("u1", legacyID.String())
	if err := os.MkdirAll(filepath.Join(dir, "u1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, legacyKey), []byte("legacy"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pm_file_blobs (id, workspace_id, owner_user_id, filename, content_type, size_bytes, storage_key)
		VALUES ($1, $2, 'u1', 'old.txt', 'text/plain', 6, $3)`, legacyID, ws, legacyKey); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	// A disk row whose file is gone (e.g. lost on an earlier redeploy).
	goneID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO pm_file_blobs (id, workspace_id, owner_user_id, filename, content_type, size_bytes, storage_key)
		VALUES ($1, $2, 'u1', 'gone.txt', 'text/plain', 1, $3)`, goneID, ws, "u1/"+goneID.String()); err != nil {
		t.Fatalf("gone insert: %v", err)
	}

	meta, err := s.PersistInline(ctx, ws, "u1", "new.txt", dataURL("in the bucket"))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, meta.StorageKey)); !os.IsNotExist(err) {
		t.Fatalf("s3 upload also wrote to disk: %v", err)
	}
	rec, err := s.GetBlob(ctx, meta.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Backend != BackendS3 {
		t.Fatalf("backend = %q, want s3", rec.Backend)
	}
	if got, err := s.ReadBlob(rec); err != nil || string(got) != "in the bucket" {
		t.Fatalf("s3 read = %q, %v", got, err)
	}

	old, err := s.GetBlob(ctx, legacyID)
	if err != nil {
		t.Fatalf("get legacy: %v", err)
	}
	if old.Backend != BackendDisk {
		t.Fatalf("legacy backend = %q, want disk", old.Backend)
	}
	if got, err := s.ReadBlob(old); err != nil || string(got) != "legacy" {
		t.Fatalf("legacy read = %q, %v", got, err)
	}

	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, f) }
	res, err := s.Backfill(ctx, true, logf)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Pending < 2 || res.Moved != 0 || res.Missing < 1 {
		t.Fatalf("dry run = %+v", res)
	}
	if old, _ = s.GetBlob(ctx, legacyID); old.Backend != BackendDisk {
		t.Fatal("dry run flipped a row")
	}

	if res, err = s.Backfill(ctx, false, logf); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if res.Moved < 1 || res.Missing < 1 {
		t.Fatalf("backfill = %+v", res)
	}
	// The disk copy is gone; the read must now come from the bucket.
	if err := os.Remove(filepath.Join(dir, legacyKey)); err != nil {
		t.Fatal(err)
	}
	old, err = s.GetBlob(ctx, legacyID)
	if err != nil || old.Backend != BackendS3 {
		t.Fatalf("after backfill backend = %q, %v", old.Backend, err)
	}
	if got, err := s.ReadBlob(old); err != nil || string(got) != "legacy" {
		t.Fatalf("backfilled read = %q, %v", got, err)
	}
	if gone, _ := s.GetBlob(ctx, goneID); gone.Backend != BackendDisk {
		t.Fatal("missing file was flipped to s3")
	}
	if !strings.Contains(strings.Join(logs, "\n"), "missing on disk") {
		t.Fatalf("missing file not reported: %v", logs)
	}

	// Re-run is a no-op for what already moved.
	again, err := s.Backfill(ctx, false, logf)
	if err != nil || again.Moved != 0 {
		t.Fatalf("re-run = %+v, %v", again, err)
	}
}
