package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Local is a directory on this machine: a disk, or an NFS/SMB share
// mounted here. With an empty root it addresses the whole filesystem.
type Local struct {
	root string
}

// NewLocal opens a local backend. A non-empty root must exist.
func NewLocal(root string) (*Local, error) {
	root = strings.TrimSpace(root)
	if root != "" {
		root = filepath.Clean(root)
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("root %q must be an absolute path", root)
		}
		st, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("root %s: %w", root, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("root %s is not a directory", root)
		}
	}
	return &Local{root: root}, nil
}

func (l *Local) Kind() Kind { return KindLocal }

// abs resolves p inside the root.
func (l *Local) abs(p string) (string, error) {
	rel, err := cleanRel(l.root, p)
	if err != nil {
		return "", err
	}
	if l.root == "" {
		return filepath.FromSlash(rel), nil
	}
	return filepath.Join(l.root, filepath.FromSlash(rel)), nil
}

// rel turns an absolute path back into the backend's path form.
func (l *Local) rel(abs string) string {
	if l.root == "" {
		return filepath.ToSlash(abs)
	}
	r, err := filepath.Rel(l.root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

func (l *Local) entry(abs string, st fs.FileInfo) Entry {
	return Entry{Path: l.rel(abs), Size: st.Size(), ModTime: st.ModTime(), IsDir: st.IsDir()}
}

func (l *Local) List(ctx context.Context, pattern string) ([]Entry, error) {
	absPattern, err := l.abs(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("bad pattern: %w", err)
	}
	var out []Entry
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, l.entry(m, st))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (l *Local) Stat(ctx context.Context, p string) (Entry, error) {
	abs, err := l.abs(p)
	if err != nil {
		return Entry{}, err
	}
	st, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return l.entry(abs, st), nil
}

func (l *Local) Open(ctx context.Context, p string) (io.ReadCloser, Entry, error) {
	abs, err := l.abs(p)
	if err != nil {
		return nil, Entry{}, err
	}
	fh, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Entry{}, ErrNotFound
	}
	if err != nil {
		return nil, Entry{}, err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, Entry{}, err
	}
	if st.IsDir() {
		fh.Close()
		return nil, Entry{}, fmt.Errorf("%s is a directory", p)
	}
	return fh, l.entry(abs, st), nil
}

func (l *Local) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	abs, err := l.abs(p)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		if _, err := os.Stat(abs); err == nil {
			return nil, ErrExists
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	tmp := abs + ".kairos-part"
	fh, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &localWriter{fh: fh, tmp: tmp, final: abs}, nil
}

type localWriter struct {
	fh    *os.File
	tmp   string
	final string
	done  bool
}

func (w *localWriter) Write(p []byte) (int, error) { return w.fh.Write(p) }

func (w *localWriter) Commit(modTime time.Time) error {
	if w.done {
		return nil
	}
	w.done = true
	if err := w.fh.Sync(); err != nil {
		w.fh.Close()
		os.Remove(w.tmp)
		return err
	}
	if err := w.fh.Close(); err != nil {
		os.Remove(w.tmp)
		return err
	}
	if !modTime.IsZero() {
		_ = os.Chtimes(w.tmp, time.Now(), modTime)
	}
	if err := os.Rename(w.tmp, w.final); err != nil {
		os.Remove(w.tmp)
		return err
	}
	return nil
}

func (w *localWriter) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	w.fh.Close()
	return os.Remove(w.tmp)
}

func (l *Local) Remove(ctx context.Context, p string) error {
	abs, err := l.abs(p)
	if err != nil {
		return err
	}
	err = os.Remove(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (l *Local) Close() error { return nil }

// ensure path is used (kept for parity with other backends' helpers).
var _ = path.Base
