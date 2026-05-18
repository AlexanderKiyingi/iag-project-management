package files

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool      *pgxpool.Pool
	uploadDir string
}

func NewStore(pool *pgxpool.Pool, uploadDir string) *Store {
	if uploadDir == "" {
		uploadDir = "./data/pm-uploads"
	}
	return &Store{pool: pool, uploadDir: uploadDir}
}

type BlobMeta struct {
	ID         uuid.UUID
	StorageKey string
	SizeBytes  int64
}

// PersistInline stores base64 data URL or raw base64 payload to disk and records metadata.
func (s *Store) PersistInline(ctx context.Context, workspaceID uuid.UUID, ownerUserID, filename, dataURL string) (BlobMeta, error) {
	payload, contentType, err := decodeDataURL(dataURL)
	if err != nil {
		return BlobMeta{}, err
	}
	id := uuid.New()
	key := filepath.Join(ownerUserID, id.String())
	abs := filepath.Join(s.uploadDir, key)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return BlobMeta{}, err
	}
	if err := os.WriteFile(abs, payload, 0o640); err != nil {
		return BlobMeta{}, err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO pm_file_blobs (id, workspace_id, owner_user_id, filename, content_type, size_bytes, storage_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, workspaceID, ownerUserID, filename, contentType, len(payload), key,
	)
	if err != nil {
		_ = os.Remove(abs)
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
}

// GetBlob loads blob metadata; caller must verify workspace access.
func (s *Store) GetBlob(ctx context.Context, id uuid.UUID) (BlobRecord, error) {
	var rec BlobRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, workspace_id, filename, content_type, storage_key
		FROM pm_file_blobs WHERE id = $1`,
		id,
	).Scan(&rec.ID, &rec.WorkspaceID, &rec.Filename, &rec.ContentType, &rec.StorageKey)
	if err != nil {
		return BlobRecord{}, err
	}
	return rec, nil
}

func (s *Store) ReadBlob(rec BlobRecord) ([]byte, error) {
	abs := filepath.Join(s.uploadDir, rec.StorageKey)
	return os.ReadFile(abs)
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
