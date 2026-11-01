package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// tree is what Local needs from a filesystem. *os.Root provides it for a
// location with a root; wholeFS provides it for the rootless legacy form.
type tree interface {
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	Open(name string) (*os.File, error)
	OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error)
	MkdirAll(name string, perm fs.FileMode) error
	Remove(name string) error
	Rename(oldname, newname string) error
	Link(oldname, newname string) error
	Chtimes(name string, atime, mtime time.Time) error
}

// wholeFS is the machine's filesystem, for a Local without a root.
type wholeFS struct{}

func (wholeFS) Stat(name string) (fs.FileInfo, error)  { return os.Stat(name) }
func (wholeFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (wholeFS) Open(name string) (*os.File, error)     { return os.Open(name) }
func (wholeFS) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
func (wholeFS) MkdirAll(name string, perm fs.FileMode) error { return os.MkdirAll(name, perm) }
func (wholeFS) Remove(name string) error                     { return os.Remove(name) }
func (wholeFS) Rename(oldname, newname string) error         { return os.Rename(oldname, newname) }
func (wholeFS) Link(oldname, newname string) error           { return os.Link(oldname, newname) }
func (wholeFS) Chtimes(name string, atime, mtime time.Time) error {
	return os.Chtimes(name, atime, mtime)
}

// Local is a directory on this machine: a disk, or an NFS/SMB share
// mounted here. With an empty root it addresses the whole filesystem.
//
// A root is held open as an *os.Root, so no path can leave it, not even
// through a symlink swapped in during a transfer. Links that stay inside
// the root work when they are relative. An absolute link is refused even
// when it points inside: os.Root does not follow them.
type Local struct {
	root string
	dir  tree
}

// NewLocal opens a local backend. A non-empty root must exist. Close
// releases the root.
func NewLocal(root string) (*Local, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return &Local{dir: wholeFS{}}, nil
	}
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
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("root %s: %w", root, err)
	}
	return &Local{root: root, dir: dir}, nil
}

func (l *Local) Kind() Kind { return KindLocal }

// clean resolves p to the name the tree knows it by: relative to the root,
// or as given without one.
func (l *Local) clean(p string) (string, error) {
	rel, err := cleanRel(l.root, p)
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(rel), nil
}

// sameFile reports whether p here and otherPath on other are one file.
// Comparing the files rather than the paths also catches symlinks, hard
// links and bind mounts. Anything that cannot be read is not a match: a
// missing destination is the normal case, and an unreadable source fails
// on open.
func (l *Local) sameFile(p string, other *Local, otherPath string) bool {
	a, err := l.clean(p)
	if err != nil {
		return false
	}
	b, err := other.clean(otherPath)
	if err != nil {
		return false
	}
	ast, err := l.dir.Stat(a)
	if err != nil {
		return false
	}
	bst, err := other.dir.Stat(b)
	return err == nil && os.SameFile(ast, bst)
}

func entryOf(name string, st fs.FileInfo) Entry {
	return Entry{Path: filepath.ToSlash(name), Size: st.Size(), ModTime: st.ModTime(), IsDir: st.IsDir()}
}

// glob lists the names matching pattern. A match behind a symlink out of
// the root is not listed.
func (l *Local) glob(pattern string) ([]string, error) {
	if r, ok := l.dir.(*os.Root); ok {
		return fs.Glob(r.FS(), filepath.ToSlash(pattern))
	}
	return filepath.Glob(pattern)
}

func (l *Local) List(ctx context.Context, pattern string) ([]Entry, error) {
	name, err := l.clean(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := l.glob(name)
	if err != nil {
		return nil, fmt.Errorf("bad pattern: %w", err)
	}
	var out []Entry
	for _, m := range matches {
		st, err := l.dir.Stat(filepath.FromSlash(m))
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, entryOf(m, st))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (l *Local) Stat(ctx context.Context, p string) (Entry, error) {
	name, err := l.clean(p)
	if err != nil {
		return Entry{}, err
	}
	st, err := l.dir.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return entryOf(name, st), nil
}

func (l *Local) Open(ctx context.Context, p string) (io.ReadCloser, Entry, error) {
	name, err := l.clean(p)
	if err != nil {
		return nil, Entry{}, err
	}
	fh, err := l.dir.Open(name)
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
	return fh, entryOf(name, st), nil
}

func (l *Local) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	name, err := l.clean(p)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		if _, err := l.dir.Lstat(name); err == nil {
			return nil, ErrExists
		}
	}
	if err := l.dir.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return nil, err
	}
	tmp := partName(name)
	fh, err := l.dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	return &localWriter{dir: l.dir, fh: fh, tmp: tmp, final: name, overwrite: overwrite}, nil
}

type localWriter struct {
	dir       tree
	fh        *os.File
	tmp       string
	final     string
	overwrite bool
	done      bool
}

func (w *localWriter) Write(p []byte) (int, error) { return w.fh.Write(p) }

func (w *localWriter) Commit(modTime time.Time) error {
	if w.done {
		return nil
	}
	w.done = true
	if err := w.fh.Sync(); err != nil {
		w.fh.Close()
		w.dir.Remove(w.tmp)
		return err
	}
	if err := w.fh.Close(); err != nil {
		w.dir.Remove(w.tmp)
		return err
	}
	if !modTime.IsZero() {
		_ = w.dir.Chtimes(w.tmp, time.Now(), modTime)
	}
	if err := w.land(); err != nil {
		w.dir.Remove(w.tmp)
		return err
	}
	return nil
}

// land moves the finished temporary file to its final name. With overwrite
// off it must not replace a file that appeared during the transfer, so it
// cannot simply rename: a hard link fails on an existing name. On a
// filesystem without hard links the name is created empty and exclusively
// first, and the rename replaces that.
func (w *localWriter) land() error {
	if w.overwrite {
		return w.dir.Rename(w.tmp, w.final)
	}
	err := w.dir.Link(w.tmp, w.final)
	if err == nil {
		w.dir.Remove(w.tmp)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return ErrExists
	}
	fh, err := w.dir.OpenFile(w.final, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	fh.Close()
	if err := w.dir.Rename(w.tmp, w.final); err != nil {
		w.dir.Remove(w.final)
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
	return w.dir.Remove(w.tmp)
}

func (l *Local) Remove(ctx context.Context, p string) error {
	name, err := l.clean(p)
	if err != nil {
		return err
	}
	err = l.dir.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (l *Local) Close() error {
	if r, ok := l.dir.(*os.Root); ok {
		return r.Close()
	}
	return nil
}
