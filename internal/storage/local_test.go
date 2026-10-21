package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalRootScoping(t *testing.T) {
	root := t.TempDir()
	b, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(context.Background(), "../etc/passwd"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escape must be refused: %v", err)
	}
	if _, err := NewLocal("relative/dir"); err == nil {
		t.Fatal("relative roots are refused")
	}
	if err := os.WriteFile(filepath.Join(root, "a.csv"), []byte("1,2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.csv"), []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := b.List(context.Background(), "*.csv")
	if err != nil || len(got) != 1 || got[0].Path != "a.csv" {
		t.Fatalf("list: %+v %v", got, err)
	}
	got, err = b.List(context.Background(), "sub/*.csv")
	if err != nil || len(got) != 1 || got[0].Path != "sub/b.csv" {
		t.Fatalf("nested list: %+v %v", got, err)
	}
}

func TestCopyFileCommitsAtomicallyAndVerifies(t *testing.T) {
	ctx := context.Background()
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	src, _ := NewLocal(srcRoot)
	dst, _ := NewLocal(dstRoot)
	payload := strings.Repeat("kairos ", 10000)
	old := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(srcRoot, "in/report.csv"), nil, 0o644); err == nil {
		t.Fatal("unexpected")
	}
	os.MkdirAll(filepath.Join(srcRoot, "in"), 0o755)
	os.WriteFile(filepath.Join(srcRoot, "in/report.csv"), []byte(payload), 0o644)
	os.Chtimes(filepath.Join(srcRoot, "in/report.csv"), old, old)

	res, err := CopyFile(ctx, src, "in/report.csv", dst, "out/report.csv", CopyOptions{PreserveMTime: true, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != int64(len(payload)) || res.SHA256 == "" {
		t.Fatalf("result %+v", res)
	}
	st, err := os.Stat(filepath.Join(dstRoot, "out/report.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(old) {
		t.Fatalf("mtime not preserved: %s", st.ModTime())
	}
	if entries, _ := filepath.Glob(filepath.Join(dstRoot, "out", "*.kairos-part")); len(entries) != 0 {
		t.Fatal("temp file left behind")
	}
	// Overwrite off refuses; on replaces.
	if _, err := CopyFile(ctx, src, "in/report.csv", dst, "out/report.csv", CopyOptions{}); err == nil {
		t.Fatal("overwrite off must refuse")
	}
	if _, err := CopyFile(ctx, src, "in/report.csv", dst, "out/report.csv", CopyOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
}

func TestDestPath(t *testing.T) {
	cases := []struct{ pattern, match, dest, want string }{
		{"in/a.csv", "in/a.csv", "out/b.csv", "out/b.csv"},
		{"in/*.csv", "in/a.csv", "out", "out/a.csv"},
		{"in/a.csv", "in/a.csv", "out/", "out/a.csv"},
		{"in/*.csv", "in/a.csv", "", "a.csv"},
	}
	for _, c := range cases {
		if got := DestPath(c.pattern, c.match, c.dest); got != c.want {
			t.Fatalf("DestPath(%q,%q,%q)=%q want %q", c.pattern, c.match, c.dest, got, c.want)
		}
	}
}
