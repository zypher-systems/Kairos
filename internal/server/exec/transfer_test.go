package exec

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

// TestTransferMountToMountGlob moves every CSV from one mounted directory
// to another through the server, registers nothing on disk it should not,
// and removes the sources only after each copy is in place.
func TestTransferMountToMountGlob(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	in, out := t.TempDir(), t.TempDir()
	for _, n := range []string{"a.csv", "b.csv", "skip.txt"} {
		if err := os.WriteFile(filepath.Join(in, n), []byte("data-"+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := &store.StorageLocation{Name: "in", Kind: "mount", BasePath: in}
	dst := &store.StorageLocation{Name: "out", Kind: "mount", BasePath: out}
	for _, l := range []*store.StorageLocation{src, dst} {
		if err := st.CreateStorageLocation(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	job := store.Job{
		Name: "move-csv", ExecMode: "transfer", TargetType: "local",
		TransferConfig: store.TransferConfig{
			Source:       store.TransferEndpoint{Kind: "location", LocationID: &src.ID, Path: "*.csv"},
			Dest:         store.TransferEndpoint{Kind: "location", LocationID: &dst.ID, Path: "archive/"},
			DeleteSource: true, PreserveMTime: true, VerifyChecksum: true,
		},
	}
	created, err := st.CreateJob(ctx, &job, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, created, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sink := NewLogSink(ctx, st, run.ID, nil)
	defer sink.Close()
	r := NewTransferRunner(st, t.TempDir())
	code, msg := r.Execute(ctx, created, run, sink)
	if code != 0 || msg != "" {
		t.Fatalf("transfer failed: %d %s", code, msg)
	}
	for _, n := range []string{"a.csv", "b.csv"} {
		got, err := os.ReadFile(filepath.Join(out, "archive", n))
		if err != nil || string(got) != "data-"+n {
			t.Fatalf("%s not moved: %q %v", n, got, err)
		}
		if _, err := os.Stat(filepath.Join(in, n)); !os.IsNotExist(err) {
			t.Fatalf("%s source must be removed", n)
		}
	}
	if _, err := os.Stat(filepath.Join(in, "skip.txt")); err != nil {
		t.Fatal("non-matching file must stay")
	}
}

// TestTransferTriggerFilesIntoLibrary copies exactly the files a trigger
// matched and registers them in the library.
func TestTransferTriggerFilesIntoLibrary(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	in := t.TempDir()
	os.WriteFile(filepath.Join(in, "new.csv"), []byte("fresh"), 0o644)
	os.WriteFile(filepath.Join(in, "old.csv"), []byte("stale"), 0o644)
	src := &store.StorageLocation{Name: "drop", Kind: "mount", BasePath: in}
	if err := st.CreateStorageLocation(ctx, src); err != nil {
		t.Fatal(err)
	}
	job := store.Job{
		Name: "ingest", ExecMode: "transfer", TargetType: "local",
		WatchEnabled: true, WatchTarget: "location", WatchLocationID: &src.ID, WatchPath: "*.csv", WatchIntervalSec: 10,
		TransferConfig: store.TransferConfig{
			Source:      store.TransferEndpoint{Kind: "location", LocationID: &src.ID},
			Dest:        store.TransferEndpoint{Kind: "library"},
			FromTrigger: true,
		},
	}
	created, err := st.CreateJob(ctx, &job, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, created, store.TriggerFile, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRunTriggerFiles(ctx, run.ID, []string{"new.csv"}); err != nil {
		t.Fatal(err)
	}
	run, _ = st.GetRun(ctx, run.ID)
	dataDir := t.TempDir()
	sink := NewLogSink(ctx, st, run.ID, nil)
	defer sink.Close()
	code, msg := NewTransferRunner(st, dataDir).Execute(ctx, created, run, sink)
	if code != 0 || msg != "" {
		t.Fatalf("transfer failed: %d %s", code, msg)
	}
	files, err := st.ListFiles(ctx)
	if err != nil || len(files) != 1 || files[0].Name != "new.csv" || files[0].SizeBytes != 5 {
		t.Fatalf("library must hold exactly the triggered file: %+v %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, files[0].StoragePath)); err != nil {
		t.Fatalf("blob missing: %v", err)
	}
}
