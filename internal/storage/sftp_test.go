package storage

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// openTestSFTP drives the backend through a real SFTP session against the
// in-process server, which serves the local filesystem.
func openTestSFTP(t *testing.T) *SFTP {
	t.Helper()
	cli, srv := net.Pipe()
	server, err := sftp.NewServer(srv)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(cli, cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return &SFTP{client: client}
}

// noHardLinkCmds answers the hard-link request the way a server without the
// OpenSSH extension does.
type noHardLinkCmds struct{ sftp.FileCmder }

func (c noHardLinkCmds) Filecmd(r *sftp.Request) error {
	if r.Method == "Link" {
		return sftp.ErrSSHFxOpUnsupported
	}
	return c.FileCmder.Filecmd(r)
}

func openSFTPWithoutHardLinks(t *testing.T) *SFTP {
	t.Helper()
	cli, srv := net.Pipe()
	h := sftp.InMemHandler()
	h.FileCmd = noHardLinkCmds{h.FileCmd}
	server := sftp.NewRequestServer(srv, h)
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(cli, cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return &SFTP{client: client}
}

func TestSFTPOverwriteOffWithoutHardLinks(t *testing.T) {
	ctx := context.Background()
	b := openSFTPWithoutHardLinks(t)
	content := func(p string) string {
		t.Helper()
		f, err := b.client.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	put := func(p, body string, overwrite bool) Writer {
		t.Helper()
		w, err := b.Create(ctx, p, overwrite)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
		return w
	}

	if err := put("/fresh.csv", "new", false).Commit(zeroTime); err != nil {
		t.Fatalf("a free name must land: %v", err)
	}
	if got := content("/fresh.csv"); got != "new" {
		t.Fatalf("landed %q", got)
	}

	w := put("/out.csv", "late", false)
	if err := put("/out.csv", "first", true).Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
		t.Fatalf("commit over a new file must fail with ErrExists: %v", err)
	}
	if got := content("/out.csv"); got != "first" {
		t.Fatalf("destination replaced with overwrite off: %q", got)
	}
	entries, err := b.client.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".kairos-part") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSFTPOverwriteOffHoldsAtCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openTestSFTP(t)
	final := filepath.Join(dir, "out.csv")
	w, err := b.Create(ctx, final, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "late"); err != nil {
		t.Fatal(err)
	}
	write(t, final, "first")
	if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
		t.Fatalf("commit over a new file must fail with ErrExists: %v", err)
	}
	if got := read(t, final); got != "first" {
		t.Fatalf("destination replaced with overwrite off: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestSFTPConcurrentWritersKeepTheirOwnBytes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := openTestSFTP(t)
	final := filepath.Join(dir, "out.csv")
	first, err := b.Create(ctx, final, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Create(ctx, final, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(first, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(second, "second"); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, final); got != "first" {
		t.Fatalf("first commit landed %q", got)
	}
	if err := second.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, final); got != "second" {
		t.Fatalf("second commit landed %q", got)
	}
}

func TestSFTPCopyRefusesTheSameFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.csv")
	write(t, p, "only copy")
	b := openTestSFTP(t)
	if _, err := CopyFile(ctx, b, p, b, dir+"/./a.csv", CopyOptions{Overwrite: true}); err == nil {
		t.Fatal("copy onto itself must be refused")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
