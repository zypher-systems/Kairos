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
	addr   string
	user   string
	conn   *ssh.Client
	client *sftp.Client
	// keepsExisting is set once this server's plain rename has been shown
	// to refuse an existing name.
	keepsExisting bool
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
	b := &SFTP{root: strings.TrimSpace(s.Root), addr: fmt.Sprintf("%s:%d", s.Host, port), user: s.Username, conn: conn, client: client}
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

// identity asks the server for the real path, which sees through symlinks
// and anchors a path relative to the login directory. The user is left out
// on purpose: two accounts on one host usually reach the same files, and
// sameTree settles the ones that do not.
func (b *SFTP) identity(p string) (string, error) {
	real, err := b.realPath(p)
	if err != nil {
		return "", err
	}
	return "sftp://" + strings.ToLower(b.addr) + "/" + strings.TrimPrefix(real, "/"), nil
}

func (b *SFTP) realPath(p string) (string, error) {
	abs, err := b.abs(p)
	if err != nil {
		return "", err
	}
	if real, err := b.client.RealPath(abs); err == nil {
		return real, nil
	}
	return abs, nil
}

// sameTree reports whether this session and other, which give one path on
// one server for the file at p, see the same files. One account always does.
// Two accounts may be chrooted apart, so a scratch file beside p decides: if
// this session cannot see what other just made, the trees differ. Anything
// that leaves the question open counts as the same tree: that refuses a
// transfer, where the wrong answer would delete a file.
func (b *SFTP) sameTree(other *SFTP, p string) bool {
	if b.user == other.user {
		return true
	}
	real, err := other.realPath(p)
	if err != nil {
		return true
	}
	mark := partName(real)
	fh, err := other.client.OpenFile(mark, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		// This session holds a file in that directory, so if the other
		// has no such directory they are not looking at one tree.
		_, serr := other.client.Lstat(path.Dir(real))
		return !errors.Is(serr, os.ErrNotExist)
	}
	defer other.client.Remove(mark)
	if err := fh.Close(); err != nil {
		return true
	}
	_, err = b.client.Lstat(mark)
	return !errors.Is(err, os.ErrNotExist)
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
	tmp := partName(abs)
	fh, err := b.client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
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
	if !w.overwrite {
		if err := w.landExclusive(); err != nil {
			w.b.client.Remove(w.tmp)
			return err
		}
		return nil
	}
	// Most SFTP servers refuse rename onto an existing file; posix-rename
	// replaces atomically where supported.
	if err := w.b.client.PosixRename(w.tmp, w.final); err == nil {
		return nil
	}
	_ = w.b.client.Remove(w.final)
	if err := w.b.client.Rename(w.tmp, w.final); err != nil {
		w.b.client.Remove(w.tmp)
		return fmt.Errorf("rename %s into place: %w", w.tmp, err)
	}
	return nil
}

// landExclusive lands the file without replacing anything. A hard link does
// that atomically. Without the hard-link extension the protocol's plain
// rename does too, but only on servers that follow the protocol, so that
// has to be shown first; otherwise there is no safe way to land the file,
// and the commit fails.
func (w *sftpWriter) landExclusive() error {
	c := w.b.client
	if err := c.Link(w.tmp, w.final); err == nil {
		_ = c.Remove(w.tmp)
		return nil
	}
	if _, err := c.Lstat(w.final); err == nil {
		return ErrExists
	}
	if !w.renameKeepsExisting() {
		return fmt.Errorf("%w: it has no hard links, and its rename could not be shown to keep existing files", errCannotLand)
	}
	if err := c.Rename(w.tmp, w.final); err != nil {
		if _, serr := c.Lstat(w.final); serr == nil {
			return ErrExists
		}
		return fmt.Errorf("rename %s into place: %w", w.tmp, err)
	}
	return nil
}

// renameKeepsExisting tries the server's plain rename on scratch files beside
// the destination. SFTP has one error code for every failure, so a refusal
// alone proves nothing: a passing fault looks the same. The rename must fail
// onto a taken name, work onto a free one, then fail onto the taken name
// again. Anything less counts as no.
func (w *sftpWriter) renameKeepsExisting() bool {
	if w.b.keepsExisting {
		return true
	}
	c := w.b.client
	from, taken, free := partName(w.final), partName(w.final), partName(w.final)
	defer c.Remove(free)
	for _, p := range []string{from, taken} {
		fh, err := c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return false
		}
		defer c.Remove(p)
		if err := fh.Close(); err != nil {
			return false
		}
	}
	w.b.keepsExisting = c.Rename(from, taken) != nil &&
		c.Rename(from, free) == nil &&
		c.Rename(free, taken) != nil
	return w.b.keepsExisting
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
