package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

// NFS is a directory on an NFSv3 export, spoken directly from the process:
// no kernel mount, no privileges, so it works in an unprivileged container
// and on agents. The server sees the AUTH_SYS identity in the spec (UID and
// GID), so exports must allow it (or map it with all_squash/anonuid).
type NFS struct {
	root   string
	mount  *nfs.Mount
	target *nfs.Target
}

// OpenNFS contacts the portmapper and mountd on Host and mounts Export.
func OpenNFS(ctx context.Context, s Spec) (*NFS, error) {
	if s.Host == "" || s.Export == "" {
		return nil, errors.New("nfs needs a server and an export path")
	}
	host := s.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	type result struct {
		b   *NFS
		err error
	}
	done := make(chan result, 1)
	go func() {
		var m *nfs.Mount
		if s.Port != 0 {
			// A fixed port serves both MOUNT and NFS (appliances and
			// userspace servers that run without a portmapper).
			client, err := rpc.DialTCP("tcp", net.JoinHostPort(host, fmt.Sprint(s.Port)), false)
			if err != nil {
				done <- result{nil, fmt.Errorf("nfs dial %s:%d: %w", host, s.Port, err)}
				return
			}
			m = &nfs.Mount{Client: client}
		} else {
			var err error
			m, err = nfs.DialMount(host, 0)
			if err != nil {
				done <- result{nil, fmt.Errorf("nfs dial %s (portmapper): %w", host, err)}
				return
			}
		}
		auth := rpc.NewAuthUnix("kairos", s.UID, s.GID)
		export := strings.TrimRight(s.Export, "/")
		if export == "" {
			export = "/"
		}
		t, err := m.Mount(export, auth.Auth())
		if err != nil {
			m.Close()
			done <- result{nil, fmt.Errorf("nfs mount %s:%s: %w", host, s.Export, err)}
			return
		}
		done <- result{&NFS{root: strings.Trim(strings.TrimSpace(s.Root), "/"), mount: m, target: t}, nil}
	}()
	select {
	case r := <-done:
		return r.b, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("nfs %s: connection timed out", host)
	}
}

func (b *NFS) Kind() Kind { return KindNFS }

// abs resolves p inside the root, as a path relative to the export.
func (b *NFS) abs(p string) (string, error) {
	rel, err := cleanRel("/"+b.root, p)
	if err != nil {
		return "", err
	}
	if b.root == "" {
		return rel, nil
	}
	return b.root + "/" + rel, nil
}

func (b *NFS) rel(abs string) string {
	if b.root == "" {
		return abs
	}
	return strings.TrimPrefix(abs, b.root+"/")
}

func nfsNotExist(err error) bool {
	return err != nil && (errors.Is(err, os.ErrNotExist) || os.IsNotExist(err))
}

func (b *NFS) List(ctx context.Context, pattern string) ([]Entry, error) {
	rel, err := cleanRel("/"+b.root, pattern)
	if err != nil {
		return nil, err
	}
	dir, base := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if HasGlob(dir) {
		return nil, errors.New("wildcards are only supported in the last path element")
	}
	absDir := b.root
	if dir != "" {
		absDir = path.Join(b.root, dir)
	}
	if absDir == "" {
		absDir = "."
	}
	entries, err := b.target.ReadDirPlus(absDir)
	if err != nil {
		if nfsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		name := e.Name()
		if name == "." || name == ".." || e.IsDir() {
			continue
		}
		ok, err := path.Match(base, name)
		if err != nil {
			return nil, fmt.Errorf("bad pattern: %w", err)
		}
		if !ok {
			continue
		}
		p := name
		if dir != "" {
			p = dir + "/" + name
		}
		out = append(out, Entry{Path: p, Size: e.Size(), ModTime: e.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (b *NFS) Stat(ctx context.Context, p string) (Entry, error) {
	abs, err := b.abs(p)
	if err != nil {
		return Entry{}, err
	}
	fi, _, err := b.target.Lookup(abs)
	if nfsNotExist(err) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return Entry{Path: b.rel(abs), Size: fi.Size(), ModTime: fi.ModTime(), IsDir: fi.IsDir()}, nil
}

func (b *NFS) Open(ctx context.Context, p string) (io.ReadCloser, Entry, error) {
	e, err := b.Stat(ctx, p)
	if err != nil {
		return nil, Entry{}, err
	}
	if e.IsDir {
		return nil, Entry{}, fmt.Errorf("%s is a directory", p)
	}
	abs, _ := b.abs(p)
	f, err := b.target.Open(abs)
	if err != nil {
		return nil, Entry{}, err
	}
	return f, e, nil
}

func (b *NFS) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	abs, err := b.abs(p)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		if _, _, err := b.target.Lookup(abs); err == nil {
			return nil, ErrExists
		}
	}
	if err := b.mkdirAll(path.Dir(abs)); err != nil {
		return nil, err
	}
	tmp := abs + ".kairos-part"
	_ = b.target.Remove(tmp)
	if _, err := b.target.Create(tmp, 0o644); err != nil {
		return nil, fmt.Errorf("create %s: %w", p, err)
	}
	f, err := b.target.OpenFile(tmp, 0o644)
	if err != nil {
		_ = b.target.Remove(tmp)
		return nil, fmt.Errorf("open %s for writing: %w", p, err)
	}
	return &nfsWriter{b: b, f: f, tmp: tmp, final: abs}, nil
}

type nfsWriter struct {
	b     *NFS
	f     *nfs.File
	tmp   string
	final string
	done  bool
}

func (w *nfsWriter) Write(p []byte) (int, error) { return w.f.Write(p) }

func (w *nfsWriter) Commit(modTime time.Time) error {
	if w.done {
		return nil
	}
	w.done = true
	if err := w.f.Close(); err != nil {
		w.b.target.Remove(w.tmp)
		return err
	}
	if !modTime.IsZero() {
		_ = w.b.target.Setattr(w.tmp, nfs.Sattr3{
			Mtime: nfs.SetTime{SetIt: nfs.SetToClientTime, Time: nfs.NFS3Time{Seconds: uint32(modTime.Unix()), Nseconds: uint32(modTime.Nanosecond())}},
		})
	}
	// NFS rename replaces an existing target atomically.
	if err := w.b.target.Rename(w.tmp, w.final); err != nil {
		w.b.target.Remove(w.tmp)
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

func (w *nfsWriter) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	w.f.Close()
	return w.b.target.Remove(w.tmp)
}

func (b *NFS) Remove(ctx context.Context, p string) error {
	abs, err := b.abs(p)
	if err != nil {
		return err
	}
	err = b.target.Remove(abs)
	if nfsNotExist(err) {
		return ErrNotFound
	}
	return err
}

func (b *NFS) Close() error {
	err := b.mount.Unmount()
	b.mount.Close()
	return err
}

// mkdirAll creates each missing directory below the export.
func (b *NFS) mkdirAll(dir string) error {
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	cur := ""
	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur = path.Join(cur, seg)
		if _, _, err := b.target.Lookup(cur); err == nil {
			continue
		}
		if _, err := b.target.Mkdir(cur, 0o755); err != nil {
			if _, _, again := b.target.Lookup(cur); again == nil {
				continue
			}
			return fmt.Errorf("mkdir %s: %w", cur, err)
		}
	}
	return nil
}
