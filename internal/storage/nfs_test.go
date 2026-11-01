package storage

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	nfsserver "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

// The NFS backend is exercised against an in-process NFSv3 server (the
// same protocol a NAS speaks), so no kernel mount or privileges are
// needed here either.
func startNFSServer(t *testing.T, dir string) (host string) {
	t.Helper()
	return serveNFS(t, osfs.New(dir))
}

func serveNFS(t *testing.T, fs billy.Filesystem) (host string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := nfshelper.NewNullAuthHandler(fs)
	go func() { _ = nfsserver.Serve(l, nfshelper.NewCachingHandler(handler, 1024)) }()
	t.Cleanup(func() { l.Close() })
	return l.Addr().String()
}

func TestNFSBackendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "in"), 0o755)
	os.WriteFile(filepath.Join(dir, "in", "a.csv"), []byte("1,2,3"), 0o644)
	addr := startNFSServer(t, dir)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	b, err := OpenNFS(context.Background(), Spec{Kind: KindNFS, Host: host, Port: port, Export: "/", UID: 1000, GID: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	got, err := b.List(context.Background(), "in/*.csv")
	if err != nil || len(got) != 1 || got[0].Path != "in/a.csv" {
		t.Fatalf("list: %+v %v", got, err)
	}
	res, err := CopyFile(context.Background(), b, "in/a.csv", b, "out/b.csv", CopyOptions{Verify: true, PreserveMTime: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out", "b.csv"))
	if err != nil || string(data) != "1,2,3" || res.Bytes != 5 {
		t.Fatalf("copy landed wrong: %q %v %+v", data, err, res)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "out", "*.kairos-part")); len(entries) != 0 {
		t.Fatal("temp file left behind")
	}
	if err := b.Remove(context.Background(), "in/a.csv"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(context.Background(), "in/a.csv"); err != ErrNotFound {
		t.Fatalf("removed file must be gone: %v", err)
	}
	if _, err := b.Stat(context.Background(), "../etc/passwd"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escape must be refused: %v", err)
	}
}

func openTestNFS(t *testing.T, dir string) *NFS {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(startNFSServer(t, dir))
	port, _ := strconv.Atoi(portStr)
	b, err := OpenNFS(context.Background(), Spec{Kind: KindNFS, Host: host, Port: port, Export: "/", UID: 1000, GID: 1000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestNFSOverwriteOffHoldsAtCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openTestNFS(t, dir)
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("late")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "out.csv"), "first")
	if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
		t.Fatalf("commit over a new file must fail with ErrExists: %v", err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "first" {
		t.Fatalf("destination replaced with overwrite off: %q", got)
	}

	// With nothing in the way the file lands as a regular file.
	w, err = b.Create(ctx, "fresh.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(dir, "fresh.csv"))
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("landed %v %v", st, err)
	}
	if got := read(t, filepath.Join(dir, "fresh.csv")); got != "new" {
		t.Fatalf("landed %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestNFSConcurrentWritersKeepTheirOwnBytes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openTestNFS(t, dir)
	first, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "first" {
		t.Fatalf("first commit landed %q", got)
	}
	if err := second.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "second" {
		t.Fatalf("second commit landed %q", got)
	}
}

// noSymlinkFS is an export that refuses symlinks, as some NAS shares do.
type noSymlinkFS struct{ billy.Filesystem }

func (noSymlinkFS) Symlink(string, string) error { return syscall.EPERM }

// Without symlinks nothing can claim the name, and an NFS rename replaces,
// so overwrite off must fail the commit rather than rename and hope.
func TestNFSOverwriteOffFailsWhenItCannotClaimTheName(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	host, portStr, _ := net.SplitHostPort(serveNFS(t, noSymlinkFS{osfs.New(dir)}))
	port, _ := strconv.Atoi(portStr)
	b, err := OpenNFS(ctx, Spec{Kind: KindNFS, Host: host, Port: port, Export: "/", UID: 1000, GID: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err == nil || !strings.Contains(err.Error(), "cannot land") {
		t.Fatalf("must fail and say why: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "out.csv")); !os.IsNotExist(err) {
		t.Fatalf("nothing may land: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}

	// Overwrite on needs no claim.
	w, err = b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
}
