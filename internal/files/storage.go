package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/objectstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Values of pm_file_blobs.storage_backend.
const (
	BackendDisk = "disk"
	BackendS3   = "s3"
)

// Store writes new blobs to the bucket when one is configured, else to
// uploadDir. Reads follow each row's storage_backend, so rows written to disk
// before the bucket existed stay readable from disk (while it is still there).
type Store struct {
	pool      *pgxpool.Pool
	uploadDir string
	s3        objectstore.Store // nil = local disk only
}

func NewStore(pool *pgxpool.Pool, uploadDir string, s3 objectstore.Store) *Store {
	if uploadDir == "" {
		uploadDir = "./data/pm-uploads"
	}
	return &Store{pool: pool, uploadDir: uploadDir, s3: s3}
}

// NewS3 returns the bucket store, or nil when S3_* is incomplete. The untyped
// nil matters: a typed nil *S3Store would make the interface non-nil.
func NewS3(endpoint, region, bucket, accessKey, secretKey string, useSSL bool) objectstore.Store {
	if s := objectstore.NewS3Store(endpoint, region, bucket, accessKey, secretKey, useSSL); s != nil {
		return s
	}
	return nil
}

// Backend reports where new uploads go.
func (s *Store) Backend() string {
	if s.s3 != nil {
		return BackendS3
	}
	return BackendDisk
}

type BlobMeta struct {
	ID         uuid.UUID
	StorageKey string
	SizeBytes  int64
}

// PersistInline stores base64 data URL or raw base64 payload (bucket or disk) and records metadata.
func (s *Store) PersistInline(ctx context.Context, workspaceID uuid.UUID, ownerUserID, filename, dataURL string) (BlobMeta, error) {
	payload, contentType, err := decodeDataURL(dataURL)
	if err != nil {
		return BlobMeta{}, err
	}
	id := uuid.New()
	key := filepath.Join(ownerUserID, id.String())
	backend := s.Backend()
	var cleanup func()
	if backend == BackendS3 {
		if _, err := s.s3.Put(key, contentType, bytes.NewReader(payload)); err != nil {
			return BlobMeta{}, err
		}
		cleanup = func() { _ = s.s3.Delete(key) }
	} else {
		abs := s.DiskPath(key)
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return BlobMeta{}, err
		}
		if err := os.WriteFile(abs, payload, 0o640); err != nil {
			return BlobMeta{}, err
		}
		cleanup = func() { _ = os.Remove(abs) }
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO pm_file_blobs (id, workspace_id, owner_user_id, filename, content_type, size_bytes, storage_key, storage_backend)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, workspaceID, ownerUserID, filename, contentType, len(payload), key, backend,
	)
	if err != nil {
		cleanup()
		return BlobMeta{}, err
	}
	return BlobMeta{ID: id, StorageKey: key, SizeBytes: int64(len(payload))}, nil
}

type BlobRecord struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Filename    string
	ContentType string
	StorageKey  string
	Backend     string // BackendDisk or BackendS3
}

// GetBlob loads blob metadata; caller must verify workspace access.
func (s *Store) GetBlob(ctx context.Context, id uuid.UUID) (BlobRecord, error) {
	var rec BlobRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, workspace_id, filename, content_type, storage_key, storage_backend
		FROM pm_file_blobs WHERE id = $1`,
		id,
	).Scan(&rec.ID, &rec.WorkspaceID, &rec.Filename, &rec.ContentType, &rec.StorageKey, &rec.Backend)
	if err != nil {
		return BlobRecord{}, err
	}
	return rec, nil
}

func (s *Store) ReadBlob(rec BlobRecord) ([]byte, error) {
	if rec.Backend == BackendS3 {
		if s.s3 == nil {
			return nil, fmt.Errorf("blob %s is in object storage but S3_* is not configured", rec.ID)
		}
		rc, err := s.s3.Open(rec.StorageKey)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return os.ReadFile(s.DiskPath(rec.StorageKey))
}

// DiskPath is where a disk-backed blob's bytes live.
func (s *Store) DiskPath(key string) string {
	return filepath.Join(s.uploadDir, key)
}

func decodeDataURL(dataURL string) ([]byte, string, error) {
	if dataURL == "" {
		return nil, "application/octet-stream", fmt.Errorf("empty data")
	}
	if strings.HasPrefix(dataURL, "data:") {
		parts := strings.SplitN(dataURL, ",", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid data url")
		}
		meta := parts[0]
		ct := "application/octet-stream"
		if i := strings.Index(meta, ":"); i >= 0 {
			rest := meta[i+1:]
			if j := strings.Index(rest, ";"); j >= 0 {
				ct = rest[:j]
			} else {
				ct = rest
			}
		}
		raw, err := base64.StdEncoding.DecodeString(parts[1])
		return raw, ct, err
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL)
	return raw, "application/octet-stream", err
}

// BackfillResult counts what Backfill did with the disk-backed rows.
type BackfillResult struct {
	Pending int // disk rows found
	Moved   int // uploaded and flipped to s3
	Missing int // row exists but the file is not on this disk; left as disk
}

// Backfill copies every disk-backed blob into the bucket under the same key and
// flips its row to s3. Upload first, then flip, so a row never points at a
// bucket object that is not there; a re-run resumes where a failure stopped.
// Disk files are left in place. dryRun reports without uploading or updating.
func (s *Store) Backfill(ctx context.Context, dryRun bool, logf func(format string, args ...any)) (BackfillResult, error) {
	var res BackfillResult
	if s.s3 == nil && !dryRun {
		return res, fmt.Errorf("object storage is not configured")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, content_type, storage_key FROM pm_file_blobs
		WHERE storage_backend = $1 ORDER BY created_at`, BackendDisk)
	if err != nil {
		return res, err
	}
	type pending struct {
		id      uuid.UUID
		ct, key string
	}
	var work []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.ct, &p.key); err != nil {
			rows.Close()
			return res, err
		}
		work = append(work, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	res.Pending = len(work)

	for _, p := range work {
		payload, err := os.ReadFile(s.DiskPath(p.key))
		if errors.Is(err, fs.ErrNotExist) {
			res.Missing++
			logf("missing on disk, left as disk: %s (%s)", p.id, p.key)
			continue
		}
		if err != nil {
			return res, fmt.Errorf("read %s: %w", p.key, err)
		}
		if dryRun {
			logf("would upload %s (%d B) -> %s", p.id, len(payload), p.key)
			continue
		}
		if _, err := s.s3.Put(p.key, p.ct, bytes.NewReader(payload)); err != nil {
			return res, fmt.Errorf("upload %s: %w", p.key, err)
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE pm_file_blobs SET storage_backend = $1
			WHERE id = $2 AND storage_backend = $3`, BackendS3, p.id, BackendDisk); err != nil {
			return res, fmt.Errorf("flip %s: %w", p.id, err)
		}
		res.Moved++
	}
	return res, nil
}
