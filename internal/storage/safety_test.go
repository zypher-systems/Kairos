package storage

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var zeroTime time.Time

func mustLocal(t *testing.T, root string) *Local {
	t.Helper()
	b, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A copy onto the file it reads from must fail: a move would otherwise
// report success and then delete the only copy.
func TestCopyRefusesTheSameFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	other := t.TempDir()
	write(t, filepath.Join(root, "a.csv"), "only copy")
	write(t, filepath.Join(root, "in", "b.csv"), "only copy")
	if err := os.Symlink("in", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "a.csv"), filepath.Join(root, "hard.csv")); err != nil {
		t.Fatal(err)
	}
	same := mustLocal(t, root)
	sub := mustLocal(t, filepath.Join(root, "in"))
	cases := []struct {
		name         string
		src          Backend
		srcPath      string
		dst          Backend
		dstPath      string
		overwrite    bool
		wantSameFile bool
	}{
		{"same path", same, "a.csv", same, "a.csv", true, true},
		{"dot slash", same, "a.csv", same, DestPath("a.csv", "a.csv", "./"), true, true},
		{"empty destination", same, "a.csv", same, DestPath("a.csv", "a.csv", ""), true, true},
		{"redundant segments", same, "in/b.csv", same, "in/./b.csv", true, true},
		{"second location over the same directory", same, "in/b.csv", sub, "b.csv", true, true},
		{"symlinked directory", same, "in/b.csv", same, "alias/b.csv", true, true},
		{"hard link", same, "a.csv", same, "hard.csv", true, true},
		{"different directory", same, "a.csv", mustLocal(t, other), "a.csv", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CopyFile(ctx, c.src, c.srcPath, c.dst, c.dstPath, CopyOptions{Overwrite: c.overwrite, Verify: true})
			if c.wantSameFile {
				if err == nil || !strings.Contains(err.Error(), "same file") {
					t.Fatalf("copy onto itself must be refused, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	if got := read(t, filepath.Join(root, "a.csv")); got != "only copy" {
		t.Fatalf("source changed: %q", got)
	}
}

// Remote backends compare by the name the server knows a file by, whatever
// root each side was opened with.
func TestRemoteIdentity(t *testing.T) {
	nfsA := &NFS{root: "in", server: "nas", export: "/vol"}
	nfsB := &NFS{root: "", server: "nas", export: "/vol"}
	nfsOther := &NFS{root: "in", server: "nas2", export: "/vol"}
	s3A := &S3{root: "in", bucket: "b", endpoint: "minio:9000"}
	s3B := &S3{root: "", bucket: "b", endpoint: "minio:9000"}
	s3Other := &S3{root: "in", bucket: "c", endpoint: "minio:9000"}
	smbA := &SMB{root: "in", server: "fs", shareName: "data"}
	smbB := &SMB{root: "", server: "FS", shareName: "DATA"}
	smbOther := &SMB{root: "in", server: "fs", shareName: "other"}
	cases := []struct {
		name  string
		a     identifier
		aPath string
		b     identifier
		bPath string
		want  bool
	}{
		{"nfs overlapping roots", nfsA, "a.csv", nfsB, "in/a.csv", true},
		{"nfs other server", nfsA, "a.csv", nfsOther, "a.csv", false},
		{"s3 overlapping roots", s3A, "a.csv", s3B, "in/a.csv", true},
		{"s3 other bucket", s3A, "a.csv", s3Other, "a.csv", false},
		{"smb is case-insensitive", smbA, "A.csv", smbB, "in/a.CSV", true},
		{"smb other share", smbA, "a.csv", smbOther, "a.csv", false},
		{"different kinds never match", nfsA, "a.csv", s3A, "a.csv", false},
		{"nfs host case", nfsA, "a.csv", &NFS{root: "in", server: "NAS", export: "/vol"}, "a.csv", true},
		{"nfs export case stays", nfsA, "a.csv", &NFS{root: "in", server: "nas", export: "/VOL"}, "a.csv", false},
		{"nfs trailing slash on the export", nfsA, "a.csv", &NFS{root: "in", server: "nas", export: "/vol/"}, "a.csv", true},
		{"s3 host case", s3A, "a.csv", &S3{root: "in", bucket: "b", endpoint: "MinIO:9000"}, "a.csv", true},
		{"s3 trailing slash", s3A, "a.csv", &S3{root: "in", bucket: "b", endpoint: "minio:9000/"}, "a.csv", true},
		{"s3 bucket case stays", s3A, "a.csv", &S3{root: "in", bucket: "B", endpoint: "minio:9000"}, "a.csv", false},
		{"s3 key case stays", s3A, "a.csv", s3A, "A.csv", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := c.a.identity(c.aPath)
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.b.identity(c.bPath)
			if err != nil {
				t.Fatal(err)
			}
			if (a == b) != c.want {
				t.Fatalf("identity %q vs %q: want equal=%v", a, b, c.want)
			}
		})
	}
}

// Two writers on one destination must not share a temporary file: each
// commit has to land exactly what that writer wrote.
func TestConcurrentWritersKeepTheirOwnBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	b := mustLocal(t, root)
	first, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write(bytes.Repeat([]byte("A"), 1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write(bytes.Repeat([]byte("B"), 16)); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "out.csv")); got != strings.Repeat("A", 1024) {
		t.Fatalf("first commit landed %d bytes of mixed content", len(got))
	}
	if err := second.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "out.csv")); got != strings.Repeat("B", 16) {
		t.Fatalf("second commit landed %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestAbortRemovesOnlyItsOwnTemporaryFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	b := mustLocal(t, root)
	keep, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	drop, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := drop.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := keep.Write([]byte("kept")); err != nil {
		t.Fatal(err)
	}
	if err := keep.Commit(zeroTime); err != nil {
		t.Fatalf("an aborted writer took the other writer's file: %v", err)
	}
	if got := read(t, filepath.Join(root, "out.csv")); got != "kept" {
		t.Fatalf("landed %q", got)
	}
}

// noHardLinks is a filesystem that refuses link(2), as some FUSE and
// network mounts do. It records any non-temporary name opened for writing:
// that would be an empty file readers can see.
type noHardLinks struct {
	tree
	opened *[]string
}

func (noHardLinks) Link(string, string) error { return syscall.EPERM }

func (n noHardLinks) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	if !strings.HasSuffix(name, ".kairos-part") {
		*n.opened = append(*n.opened, name)
	}
	return n.tree.OpenFile(name, flag, perm)
}

// noNoReplace is a filesystem with neither hard links nor a rename that
// refuses an existing name: it cannot land a file safely with overwrite off.
type noNoReplace struct{ noHardLinks }

func (noNoReplace) Open(string) (*os.File, error) { return nil, syscall.EINVAL }

// A file that appears at the destination mid-transfer must survive when
// overwrite is off, however the backend lands the file.
func TestOverwriteOffHoldsAtCommit(t *testing.T) {
	ctx := context.Background()
	var placeholders []string
	defer func() {
		if len(placeholders) != 0 {
			t.Errorf("readers could see an empty file at %v", placeholders)
		}
	}()
	for _, c := range []struct {
		name string
		wrap func(*Local)
	}{
		{"hard links", func(*Local) {}},
		{"no hard links", func(l *Local) { l.dir = noHardLinks{l.dir, &placeholders} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			b := mustLocal(t, root)
			c.wrap(b)
			w, err := b.Create(ctx, "out.csv", false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("late")); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, "out.csv"), "first")
			if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
				t.Fatalf("commit over a new file must fail with ErrExists: %v", err)
			}
			if got := read(t, filepath.Join(root, "out.csv")); got != "first" {
				t.Fatalf("destination replaced with overwrite off: %q", got)
			}
			if left, _ := filepath.Glob(filepath.Join(root, "*.kairos-part")); len(left) != 0 {
				t.Fatalf("temp files left behind: %v", left)
			}

			// With nothing in the way the same path lands normally.
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
			if got := read(t, filepath.Join(root, "fresh.csv")); got != "new" {
				t.Fatalf("landed %q", got)
			}
			if left, _ := filepath.Glob(filepath.Join(root, "*.kairos-part")); len(left) != 0 {
				t.Fatalf("temp files left behind: %v", left)
			}
		})
	}
}

// Storage with no atomic way to keep an existing name must fail the commit:
// neither a blind rename nor an empty placeholder is safe.
func TestOverwriteOffFailsWhenItCannotBeHonoured(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	b := mustLocal(t, root)
	var placeholders []string
	b.dir = noNoReplace{noHardLinks{b.dir, &placeholders}}
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
	if _, err := os.Lstat(filepath.Join(root, "out.csv")); !os.IsNotExist(err) {
		t.Fatalf("nothing may land: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "*.kairos-part")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestCopyNamesAnOverwriteRefusalAtCommit(t *testing.T) {
	ctx := context.Background()
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	write(t, filepath.Join(srcRoot, "a.csv"), "new")
	dst := &raceDest{Local: mustLocal(t, dstRoot), appear: filepath.Join(dstRoot, "a.csv")}
	_, err := CopyFile(ctx, mustLocal(t, srcRoot), "a.csv", dst, "a.csv", CopyOptions{})
	if err == nil || !strings.Contains(err.Error(), "exists and overwrite is off") {
		t.Fatalf("got %v", err)
	}
	if got := read(t, filepath.Join(dstRoot, "a.csv")); got != "winner" {
		t.Fatalf("destination replaced: %q", got)
	}
}

// raceDest lets another writer win the destination name after Create.
type raceDest struct {
	*Local
	appear string
}

func (r *raceDest) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	w, err := r.Local.Create(ctx, p, overwrite)
	if err == nil {
		err = os.WriteFile(r.appear, []byte("winner"), 0o644)
	}
	return w, err
}

// Symlinks inside a location must not lead out of its root, for any
// operation, and the listing must not surface them.
func TestLocalSymlinksCannotLeaveTheRoot(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.csv")
	write(t, secret, "secret")
	root := t.TempDir()
	write(t, filepath.Join(root, "real.csv"), "inside")
	write(t, filepath.Join(root, "dir", "nested.csv"), "nested")
	for link, target := range map[string]string{
		"file-abs.csv": secret,
		"file-rel.csv": filepath.Join("..", filepath.Base(outside), "secret.csv"),
		"dir-abs":      outside,
		"ok-rel.csv":   "real.csv",
		"ok-dir":       "dir",
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	b := mustLocal(t, root)

	for _, p := range []string{"file-abs.csv", "file-rel.csv", "dir-abs/secret.csv"} {
		if _, err := b.Stat(ctx, p); err == nil {
			t.Errorf("Stat %s must be refused", p)
		}
		if rc, _, err := b.Open(ctx, p); err == nil {
			rc.Close()
			t.Errorf("Open %s must be refused", p)
		}
		if err := b.Remove(ctx, "dir-abs/secret.csv"); err == nil {
			t.Errorf("Remove through %s must be refused", p)
		}
	}
	if w, err := b.Create(ctx, "dir-abs/planted.csv", true); err == nil {
		w.Abort()
		t.Error("Create through a symlink out of the root must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.csv")); err == nil {
		t.Error("a file was written outside the root")
	}
	if got := read(t, secret); got != "secret" {
		t.Errorf("outside file changed: %q", got)
	}

	got, err := b.List(ctx, "*.csv")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Path)
	}
	if want := "ok-rel.csv,real.csv"; strings.Join(names, ",") != want {
		t.Errorf("listing = %v, want %s", names, want)
	}
	if got, err := b.List(ctx, "dir-abs/*.csv"); err != nil || len(got) != 0 {
		t.Errorf("listing through a symlink out of the root: %+v %v", got, err)
	}

	// Links that stay inside the root keep working.
	if rc, _, err := b.Open(ctx, "ok-rel.csv"); err != nil {
		t.Errorf("relative link inside the root: %v", err)
	} else {
		rc.Close()
	}
	if got, err := b.List(ctx, "ok-dir/*.csv"); err != nil || len(got) != 1 || got[0].Path != "ok-dir/nested.csv" {
		t.Errorf("link to a directory inside the root: %+v %v", got, err)
	}
}

// A rootless spec is the legacy "whole filesystem" form; it stays that way.
func TestRootlessLocalStillAddressesAbsolutePaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "a.csv")
	write(t, p, "x")
	b := mustLocal(t, "")
	got, err := b.List(ctx, filepath.Join(dir, "sub", "*.csv"))
	if err != nil || len(got) != 1 || got[0].Path != p {
		t.Fatalf("list: %+v %v", got, err)
	}
	w, err := b.Create(ctx, filepath.Join(dir, "new", "b.csv"), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "new", "b.csv")); got != "y" {
		t.Fatalf("landed %q", got)
	}
}

func TestBackslashTraversalIsRefused(t *testing.T) {
	smb := &SMB{root: "in"}
	nfs := &NFS{root: "in"}
	s3 := &S3{root: "in"}
	for _, p := range []string{
		`..\secret.txt`,
		`..\..\secret.txt`,
		`sub\..\..\secret.txt`,
		`sub/..\..\secret.txt`,
		`\..\secret.txt`,
	} {
		if got, err := smb.smbPath(p); err == nil {
			t.Errorf("smb accepted %q as %q", p, got)
		}
		if got, err := nfs.abs(p); err == nil {
			t.Errorf("nfs accepted %q as %q", p, got)
		}
		if got, err := s3.key(p); err == nil {
			t.Errorf("s3 accepted %q as %q", p, got)
		}
	}
	// A dotted name is not a parent reference.
	for _, p := range []string{`a..b.csv`, `..hidden`, `sub\a..csv`} {
		if _, err := smb.smbPath(p); err != nil {
			t.Errorf("smb refused %q: %v", p, err)
		}
	}
}

func TestPartNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := range 64 {
		n := partName("out.csv")
		if seen[n] {
			t.Fatalf("repeat after %d: %s", i, n)
		}
		seen[n] = true
		if !strings.HasPrefix(n, "out.csv.") || !strings.HasSuffix(n, ".kairos-part") {
			t.Fatalf("name %q keeps neither the target nor the suffix", n)
		}
	}
}
