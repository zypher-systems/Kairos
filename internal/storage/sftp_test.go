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

// limitedCmds answers the hard-link request the way a server without the
// OpenSSH extension does. With replacing set, its plain rename replaces an
// existing file, which the protocol forbids and some servers do anyway.
type limitedCmds struct {
	sftp.FileCmder
	replacing bool
}

func (c limitedCmds) Filecmd(r *sftp.Request) error {
	switch {
	case r.Method == "Link":
		return sftp.ErrSSHFxOpUnsupported
	case r.Method == "Rename" && c.replacing:
		return c.FileCmder.(sftp.PosixRenameFileCmder).PosixRename(r)
	}
	return c.FileCmder.Filecmd(r)
}

// recordingWrites records the names opened for writing at the destination:
// an empty file there is one readers can see.
type recordingWrites struct {
	sftp.FileWriter
	names *[]string
}

func (w recordingWrites) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if !strings.HasSuffix(r.Filepath, ".kairos-part") {
		*w.names = append(*w.names, r.Filepath)
	}
	return w.FileWriter.Filewrite(r)
}

func openSFTPWithoutHardLinks(t *testing.T, replacing bool, writes *[]string) *SFTP {
	t.Helper()
	cli, srv := net.Pipe()
	h := sftp.InMemHandler()
	h.FileCmd = limitedCmds{h.FileCmd, replacing}
	h.FilePut = recordingWrites{h.FilePut, writes}
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
	var writes []string
	b := openSFTPWithoutHardLinks(t, false, &writes)
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
	if len(writes) != 0 {
		t.Fatalf("readers could see an empty file at %v", writes)
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

// A server whose rename replaces, without hard links, offers no safe way
// to land a file with overwrite off. The commit must fail, not replace.
func TestSFTPOverwriteOffFailsOnAReplacingServer(t *testing.T) {
	ctx := context.Background()
	var writes []string
	b := openSFTPWithoutHardLinks(t, true, &writes)
	w, err := b.Create(ctx, "/out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "data"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err == nil || !strings.Contains(err.Error(), "cannot land") {
		t.Fatalf("must fail and say why: %v", err)
	}
	if _, err := b.client.Lstat("/out.csv"); err == nil {
		t.Fatal("nothing may land")
	}
	if len(writes) != 0 {
		t.Fatalf("readers could see an empty file at %v", writes)
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

// Accounts chrooted on one host report the same paths for different files.
func TestSFTPCopyBetweenChrootedAccounts(t *testing.T) {
	ctx := context.Background()
	var writes []string
	src := openSFTPWithoutHardLinks(t, false, &writes)
	dst := openSFTPWithoutHardLinks(t, false, &writes)
	src.addr, dst.addr = "host:22", "host:22"
	w, err := src.Create(ctx, "/out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "payload"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyFile(ctx, src, "/out.csv", dst, "/out.csv", CopyOptions{}); err != nil {
		t.Fatalf("an empty namespace on the same host: %v", err)
	}
	// A different file already at that path is an overwrite, not a self-copy.
	w, err = dst.Create(ctx, "/out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "something else entirely"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyFile(ctx, src, "/out.csv", dst, "/out.csv", CopyOptions{Overwrite: true}); err != nil {
		t.Fatalf("a different file at the same path: %v", err)
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
