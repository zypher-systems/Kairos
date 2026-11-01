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

// raceFS lets a test act at the moments between the client's requests. Use
// a pointer: the NFS server compares filesystems with reflect.DeepEqual,
// which is never true of a struct holding funcs.
type raceFS struct {
	billy.Filesystem
	afterSymlink func()
	failRename   func()
	// lostReply makes SYMLINK work and answer with a failure, which is what
	// the client sees when its resend reaches a server that already did it.
	lostReply    bool
	failReadlink bool
	removed      []string
}

func (f *raceFS) Symlink(target, link string) error {
	err := f.Filesystem.Symlink(target, link)
	if f.afterSymlink != nil {
		f.afterSymlink()
	}
	if err == nil && f.lostReply {
		return os.ErrExist
	}
	return err
}

func (f *raceFS) Readlink(link string) (string, error) {
	if f.failReadlink {
		return "", syscall.EIO
	}
	return f.Filesystem.Readlink(link)
}

func (f *raceFS) Remove(name string) error {
	f.removed = append(f.removed, name)
	return f.Filesystem.Remove(name)
}

func (f *raceFS) Rename(from, to string) error {
	if f.failRename != nil && strings.HasSuffix(from, ".kairos-part") {
		f.failRename()
		return syscall.EIO
	}
	return f.Filesystem.Rename(from, to)
}

func openNFSOn(t *testing.T, fs billy.Filesystem) *NFS {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(serveNFS(t, fs))
	port, _ := strconv.Atoi(portStr)
	b, err := OpenNFS(context.Background(), Spec{Kind: KindNFS, Host: host, Port: port, Export: "/", UID: 1000, GID: 1000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// replaceFinal lands another writer's file over whatever is at the name, as
// an overwrite-on transfer or another NFS client would.
func replaceFinal(t *testing.T, dir string) func() {
	return func() {
		t.Helper()
		other := filepath.Join(dir, "other-writer")
		write(t, other, "first")
		if err := os.Rename(other, filepath.Join(dir, "out.csv")); err != nil {
			t.Fatal(err)
		}
	}
}

// A file that takes the name after the claim must not be replaced by the
// rename that follows it.
func TestNFSOverwriteOffChecksItsClaimBeforeRenaming(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openNFSOn(t, &raceFS{Filesystem: osfs.New(dir), afterSymlink: replaceFinal(t, dir)})
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("late")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v", err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "first" {
		t.Fatalf("another writer's file was replaced: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// A resent SYMLINK can answer "exists" for a link this writer made. The name
// is already held, so the transfer carries on.
func TestNFSKeepsAClaimItAlreadyHolds(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openNFSOn(t, &raceFS{Filesystem: osfs.New(dir), lostReply: true})
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatalf("the claim was this writer's: %v", err)
	}
	st, err := os.Lstat(filepath.Join(dir, "out.csv"))
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("landed %v %v", st, err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "data" {
		t.Fatalf("landed %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// Another writer's claim is a refusal, and this writer's file goes.
func TestNFSAnotherWritersClaimIsARefusal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openNFSOn(t, &raceFS{Filesystem: osfs.New(dir)})
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "elsewhere.kairos-part"), "their data")
	if err := os.Symlink("elsewhere.kairos-part", filepath.Join(dir, "out.csv")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v", err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "their data" {
		t.Fatalf("landed %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.kairos-part")); len(left) != 1 {
		t.Fatalf("only the other writer's temp file may stay: %v", left)
	}
}

// When the link cannot be read, whose it is stays open. Deleting the
// temporary file would strand a link to nothing, and removing the link could
// remove someone else's file, so both stay and the commit fails.
func TestNFSUnreadableClaimLeavesEverything(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openNFSOn(t, &raceFS{Filesystem: osfs.New(dir), failReadlink: true})
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	err = w.Commit(zeroTime)
	if err == nil || errors.Is(err, ErrExists) {
		t.Fatalf("the name is not known to be taken, and not known to be free: %v", err)
	}
	if got := read(t, filepath.Join(dir, "out.csv")); got != "data" {
		t.Fatalf("the link must still reach the whole file, got %q", got)
	}
}

// After a failed rename the final name is left alone. Checking it first
// would not help: a file can land between that check and the unlink.
func TestNFSFailedRenameLeavesTheFinalName(t *testing.T) {
	ctx := context.Background()

	t.Run("another file took the name", func(t *testing.T) {
		dir := t.TempDir()
		fs := &raceFS{Filesystem: osfs.New(dir), failRename: replaceFinal(t, dir)}
		b := openNFSOn(t, fs)
		w, err := b.Create(ctx, "out.csv", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(zeroTime); err == nil {
			t.Fatal("the rename failed; the commit must too")
		}
		if got := read(t, filepath.Join(dir, "out.csv")); got != "first" {
			t.Fatalf("another writer's file was removed: %q", got)
		}
	})

	t.Run("the claim is still ours", func(t *testing.T) {
		dir := t.TempDir()
		fs := &raceFS{Filesystem: osfs.New(dir), failRename: func() {}}
		b := openNFSOn(t, fs)
		w, err := b.Create(ctx, "out.csv", false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("data")); err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(zeroTime); err == nil {
			t.Fatal("the rename failed; the commit must too")
		}
		for _, name := range fs.removed {
			if name == "out.csv" {
				t.Fatal("the final name was unlinked")
			}
		}
		if got := read(t, filepath.Join(dir, "out.csv")); got != "data" {
			t.Fatalf("the link must still reach the whole file, got %q", got)
		}
	})
}
