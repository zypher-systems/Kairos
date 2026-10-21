package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FileEntry is one staged payload in the managed file library.
type FileEntry struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	StoragePath string    `json:"-"` // server-internal; never serialized
	CreatedAt   time.Time `json:"created_at"`
}

// CreateFile records an uploaded library file.
func (s *Store) CreateFile(ctx context.Context, f *FileEntry) error {
	if f.Name == "" {
		return errors.New("file name is required")
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO files (name, size_bytes, sha256, storage_path)
		VALUES ($1,$2,$3,$4)
		RETURNING id, created_at`,
		f.Name, f.SizeBytes, f.SHA256, f.StoragePath,
	).Scan(&f.ID, &f.CreatedAt)
}

// GetFile fetches one entry including its storage path.
func (s *Store) GetFile(ctx context.Context, id uuid.UUID) (FileEntry, error) {
	var f FileEntry
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, size_bytes, sha256, storage_path, created_at
		FROM files WHERE id = $1`, id,
	).Scan(&f.ID, &f.Name, &f.SizeBytes, &f.SHA256, &f.StoragePath, &f.CreatedAt)
	return f, err
}

// ListFiles returns the library, newest first.
func (s *Store) ListFiles(ctx context.Context) ([]FileEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, size_bytes, sha256, storage_path, created_at
		FROM files ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileEntry
	for rows.Next() {
		var f FileEntry
		if err := rows.Scan(&f.ID, &f.Name, &f.SizeBytes, &f.SHA256, &f.StoragePath, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFile removes the row and returns its storage path so the caller can
// unlink the blob.
func (s *Store) DeleteFile(ctx context.Context, id uuid.UUID) (string, error) {
	var path string
	err := s.pool.QueryRow(ctx, `DELETE FROM files WHERE id = $1 RETURNING storage_path`, id).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", pgx.ErrNoRows
	}
	return path, err
}

// PruneFilesOlderThan deletes library entries older than maxAgeDays and
// returns their storage paths so the caller can unlink blobs.
func (s *Store) PruneFilesOlderThan(ctx context.Context, maxAgeDays int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		DELETE FROM files
		WHERE created_at < now() - make_interval(days => $1)
		RETURNING storage_path`, maxAgeDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
