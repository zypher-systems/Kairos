package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/zypher-systems/kairos/internal/server/sshx"
)

// SFTP is a directory on an SSH host.
type SFTP struct {
	root   string
	conn   *ssh.Client
	client *sftp.Client
	// LearnedFingerprint is set when the host had no pinned key yet.
	LearnedFingerprint string
}

// OpenSFTP dials the host in the spec and starts the SFTP subsystem.
func OpenSFTP(ctx context.Context, s Spec) (*SFTP, error) {
	port := s.Port
	if port == 0 {
		port = 22
	}
	conn, res, err := sshx.Dial(sshx.HostConfig{
		Host:               s.Host,
		Port:               port,
		Username:           s.Username,
		PrivateKeyPEM:      s.PrivateKeyPEM,
		Password:           s.Password,
		HostKeyFingerprint: s.HostKeyFingerprint,
		VerifyHostKey:      s.VerifyHostKey,
		Timeout:            15 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s@%s:%d: %w", s.Username, s.Host, port, err)
	}
	client, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("sftp subsystem: %w", err)
	}
	b := &SFTP{root: strings.TrimSpace(s.Root), conn: conn, client: client}
	if s.HostKeyFingerprint == "" && res.Fingerprint != "" {
		b.LearnedFingerprint = res.Fingerprint
	}
	return b, nil
}

func (b *SFTP) Kind() Kind { return KindSFTP }

func (b *SFTP) abs(p string) (string, error) {
	rel, err := cleanRel(b.root, p)
	if err != nil {
		return "", err
	}
	return joinRoot(b.root, rel), nil
}

func (b *SFTP) rel(abs string) string {
	if b.root == "" {
		return abs
	}
	return strings.TrimPrefix(strings.TrimPrefix(abs, strings.TrimRight(b.root, "/")), "/")
}

func (b *SFTP) entry(abs string, st fs.FileInfo) Entry {
	return Entry{Path: b.rel(abs), Size: st.Size(), ModTime: st.ModTime(), IsDir: st.IsDir()}
}

func (b *SFTP) List(ctx context.Context, pattern string) ([]Entry, error) {
	absPattern, err := b.abs(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := b.client.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("bad pattern: %w", err)
	}
	var out []Entry
	for _, m := range matches {
		st, err := b.client.Stat(m)
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, b.entry(m, st))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (b *SFTP) Stat(ctx context.Context, p string) (Entry, error) {
	abs, err := b.abs(p)
	if err != nil {
		return Entry{}, err
	}
	st, err := b.client.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return b.entry(abs, st), nil
}

func (b *SFTP) Open(ctx context.Context, p string) (io.ReadCloser, Entry, error) {
	abs, err := b.abs(p)
	if err != nil {
		return nil, Entry{}, err
	}
	fh, err := b.client.Open(abs)
	if errors.Is(err, os.ErrNotExist) {
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
	return fh, b.entry(abs, st), nil
}

func (b *SFTP) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	abs, err := b.abs(p)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		if _, err := b.client.Stat(abs); err == nil {
			return nil, ErrExists
		}
	}
	if err := b.mkdirAll(path.Dir(abs)); err != nil {
		return nil, err
	}
	tmp := abs + ".kairos-part"
	fh, err := b.client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", tmp, err)
	}
	return &sftpWriter{b: b, fh: fh, tmp: tmp, final: abs, overwrite: overwrite}, nil
}

type sftpWriter struct {
	b         *SFTP
	fh        *sftp.File
	tmp       string
	final     string
	overwrite bool
	done      bool
}

func (w *sftpWriter) Write(p []byte) (int, error) { return w.fh.Write(p) }

func (w *sftpWriter) Commit(modTime time.Time) error {
	if w.done {
		return nil
	}
	w.done = true
	if err := w.fh.Close(); err != nil {
		w.b.client.Remove(w.tmp)
		return fmt.Errorf("close %s: %w", w.tmp, err)
	}
	if !modTime.IsZero() {
		_ = w.b.client.Chtimes(w.tmp, time.Now(), modTime)
	}
	if w.overwrite {
		// Most SFTP servers refuse rename onto an existing file;
		// posix-rename replaces atomically where supported.
		if err := w.b.client.PosixRename(w.tmp, w.final); err == nil {
			return nil
		}
		_ = w.b.client.Remove(w.final)
	}
	if err := w.b.client.Rename(w.tmp, w.final); err != nil {
		w.b.client.Remove(w.tmp)
		return fmt.Errorf("rename %s into place: %w", w.tmp, err)
	}
	return nil
}

func (w *sftpWriter) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	w.fh.Close()
	return w.b.client.Remove(w.tmp)
}

func (b *SFTP) Remove(ctx context.Context, p string) error {
	abs, err := b.abs(p)
	if err != nil {
		return err
	}
	err = b.client.Remove(abs)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (b *SFTP) Close() error {
	err := b.client.Close()
	if cerr := b.conn.Close(); err == nil {
		err = cerr
	}
	return err
}

// mkdirAll creates remote directories recursively.
func (b *SFTP) mkdirAll(dir string) error {
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	cur := ""
	if strings.HasPrefix(dir, "/") {
		cur = "/"
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		cur = path.Join(cur, p)
		if _, err := b.client.Stat(cur); err != nil {
			if err := b.client.Mkdir(cur); err != nil {
				return fmt.Errorf("mkdir %s: %w", cur, err)
			}
		}
	}
	return nil
}
