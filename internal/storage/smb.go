package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/hirochachacha/go-smb2"
)

// SMB is a directory on an SMB2/3 share (Windows file server, Samba, NAS),
// spoken directly from the process with NTLMv2. No mount, no privileges,
// so it runs on the server and on agents alike.
type SMB struct {
	root    string
	conn    net.Conn
	session *smb2.Session
	share   *smb2.Share
}

// OpenSMB connects to the share in the spec: Host (host[:port], default
// 445), Bucket carries the share name, Username/Password/Domain the
// account, Root the directory inside the share.
func OpenSMB(ctx context.Context, s Spec) (*SMB, error) {
	if s.Host == "" || s.Bucket == "" {
		return nil, errors.New("smb needs a server and a share name")
	}
	addr := s.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		port := s.Port
		if port == 0 {
			port = 445
		}
		addr = net.JoinHostPort(addr, fmt.Sprint(port))
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smb dial %s: %w", addr, err)
	}
	dialer := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{
		User:     s.Username,
		Password: s.Password,
		Domain:   s.Domain,
	}}
	session, err := dialer.DialContext(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smb session %s: %w", addr, err)
	}
	share, err := session.Mount(s.Bucket)
	if err != nil {
		session.Logoff()
		conn.Close()
		return nil, fmt.Errorf("smb share %s: %w", s.Bucket, err)
	}
	root := strings.Trim(strings.ReplaceAll(strings.TrimSpace(s.Root), "\\", "/"), "/")
	return &SMB{root: root, conn: conn, session: session, share: share}, nil
}

func (b *SMB) Kind() Kind { return KindSMB }

// smbPath resolves p inside the root using the share's separators.
func (b *SMB) smbPath(p string) (string, error) {
	rel, err := cleanRel("/"+b.root, p)
	if err != nil {
		return "", err
	}
	if b.root != "" {
		rel = b.root + "/" + rel
	}
	return strings.ReplaceAll(rel, "/", "\\"), nil
}

func (b *SMB) rel(smbp string) string {
	p := strings.ReplaceAll(smbp, "\\", "/")
	if b.root != "" {
		p = strings.TrimPrefix(p, b.root+"/")
	}
	return p
}

func (b *SMB) entry(smbp string, st fs.FileInfo) Entry {
	return Entry{Path: b.rel(smbp), Size: st.Size(), ModTime: st.ModTime(), IsDir: st.IsDir()}
}

func (b *SMB) List(ctx context.Context, pattern string) ([]Entry, error) {
	rel, err := cleanRel("/"+b.root, pattern)
	if err != nil {
		return nil, err
	}
	dir, base := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if HasGlob(dir) {
		return nil, errors.New("wildcards are only supported in the last path element")
	}
	smbDir, err := b.smbPath(dir)
	if err != nil {
		if dir == "" {
			smbDir = strings.ReplaceAll(b.root, "/", "\\")
		} else {
			return nil, err
		}
	}
	infos, err := b.share.ReadDir(smbDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, st := range infos {
		if st.IsDir() {
			continue
		}
		ok, err := path.Match(base, st.Name())
		if err != nil {
			return nil, fmt.Errorf("bad pattern: %w", err)
		}
		if !ok {
			continue
		}
		p := st.Name()
		if dir != "" {
			p = dir + "/" + p
		}
		out = append(out, Entry{Path: p, Size: st.Size(), ModTime: st.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (b *SMB) Stat(ctx context.Context, p string) (Entry, error) {
	sp, err := b.smbPath(p)
	if err != nil {
		return Entry{}, err
	}
	st, err := b.share.Stat(sp)
	if os.IsNotExist(err) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return b.entry(sp, st), nil
}

func (b *SMB) Open(ctx context.Context, p string) (io.ReadCloser, Entry, error) {
	sp, err := b.smbPath(p)
	if err != nil {
		return nil, Entry{}, err
	}
	f, err := b.share.Open(sp)
	if os.IsNotExist(err) {
		return nil, Entry{}, ErrNotFound
	}
	if err != nil {
		return nil, Entry{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Entry{}, err
	}
	return f, b.entry(sp, st), nil
}

func (b *SMB) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	sp, err := b.smbPath(p)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		if _, err := b.share.Stat(sp); err == nil {
			return nil, ErrExists
		}
	}
	if dir := path.Dir(strings.ReplaceAll(sp, "\\", "/")); dir != "." && dir != "" {
		_ = b.share.MkdirAll(strings.ReplaceAll(dir, "/", "\\"), 0o755)
	}
	tmp := sp + ".kairos-part"
	f, err := b.share.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", p, err)
	}
	return &smbWriter{b: b, f: f, tmp: tmp, final: sp}, nil
}

type smbWriter struct {
	b     *SMB
	f     *smb2.File
	tmp   string
	final string
	done  bool
}

func (w *smbWriter) Write(p []byte) (int, error) { return w.f.Write(p) }

func (w *smbWriter) Commit(modTime time.Time) error {
	if w.done {
		return nil
	}
	w.done = true
	if err := w.f.Close(); err != nil {
		w.b.share.Remove(w.tmp)
		return err
	}
	if !modTime.IsZero() {
		_ = w.b.share.Chtimes(w.tmp, time.Now(), modTime)
	}
	// SMB rename does not replace; clear the target first.
	_ = w.b.share.Remove(w.final)
	if err := w.b.share.Rename(w.tmp, w.final); err != nil {
		w.b.share.Remove(w.tmp)
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

func (w *smbWriter) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	w.f.Close()
	return w.b.share.Remove(w.tmp)
}

func (b *SMB) Remove(ctx context.Context, p string) error {
	sp, err := b.smbPath(p)
	if err != nil {
		return err
	}
	err = b.share.Remove(sp)
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}

func (b *SMB) Close() error {
	err := b.share.Umount()
	_ = b.session.Logoff()
	_ = b.conn.Close()
	return err
}
