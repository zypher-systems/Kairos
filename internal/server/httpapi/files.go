package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// maxUploadBytes caps one library upload.
const maxUploadBytes = 2 << 30 // 2 GiB

// maxViewBytes caps an in-browser text preview.
const maxViewBytes = 2 << 20 // 2 MiB

func (a *API) filesDir() string {
	return filepath.Join(a.cfg.DataDir, "files")
}

// handleUploadFile streams a multipart upload into the library.
func (a *API) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid multipart form (2 GiB max)")
		return
	}
	part, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "form field \"file\" is required")
		return
	}
	defer part.Close()

	if err := os.MkdirAll(a.filesDir(), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	rel := filepath.Join("files", uuid.NewString())
	dst := filepath.Join(a.cfg.DataDir, rel)
	fh, err := os.Create(dst)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(fh, h), part)
	closeErr := fh.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		writeErr(w, http.StatusInternalServerError, "failed to store upload")
		return
	}

	name := filepath.Base(header.Filename)
	if name == "" || name == "." {
		name = "upload"
	}
	entry := store.FileEntry{
		Name:        name,
		SizeBytes:   n,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
		StoragePath: rel,
	}
	if err := a.store.CreateFile(r.Context(), &entry); err != nil {
		_ = os.Remove(dst)
		slog.Error("register upload", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to register file")
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (a *API) handleListFiles(w http.ResponseWriter, r *http.Request) {
	files, err := a.store.ListFiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list files")
		return
	}
	if files == nil {
		files = []store.FileEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (a *API) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	f, err := a.store.GetFile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	p := filepath.Join(a.cfg.DataDir, f.StoragePath)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.Name))
	http.ServeFile(w, r, p)
}

// handleViewFile serves a text preview for a library file. Binary blobs and
// files over 2 MiB stay download-only.
func (a *API) handleViewFile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	f, err := a.store.GetFile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	if f.SizeBytes > maxViewBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "file is too large to view (2 MiB max)")
		return
	}
	p := filepath.Join(a.cfg.DataDir, f.StoragePath)
	fh, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	defer fh.Close()

	data, err := io.ReadAll(io.LimitReader(fh, maxViewBytes+1))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	if int64(len(data)) > maxViewBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "file is too large to view (2 MiB max)")
		return
	}
	if bytes.IndexByte(data, 0) >= 0 {
		writeErr(w, http.StatusUnsupportedMediaType, "file is not text")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", f.Name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *API) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	rel, err := a.store.DeleteFile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete file")
		return
	}
	if err := os.Remove(filepath.Join(a.cfg.DataDir, rel)); err != nil {
		slog.Warn("unlink file blob", "path", rel, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}
