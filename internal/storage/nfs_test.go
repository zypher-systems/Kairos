package storage

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/osfs"
	nfsserver "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

// The NFS backend is exercised against an in-process NFSv3 server (the
// same protocol a NAS speaks), so no kernel mount or privileges are
// needed here either.
func startNFSServer(t *testing.T, dir string) (host string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := nfshelper.NewNullAuthHandler(osfs.New(dir))
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
