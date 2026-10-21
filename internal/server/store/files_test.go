package store_test

import (
	"context"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestListFilesNewestFirstSameName(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	older := &store.FileEntry{Name: "report.log", SizeBytes: 1, SHA256: "a", StoragePath: "files/a"}
	if err := st.CreateFile(ctx, older); err != nil {
		t.Fatal(err)
	}
	newer := &store.FileEntry{Name: "report.log", SizeBytes: 2, SHA256: "b", StoragePath: "files/b"}
	if err := st.CreateFile(ctx, newer); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len %d", len(list))
	}
	if list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("newest first: got %s then %s", list[0].ID, list[1].ID)
	}
	if list[0].Name != "report.log" || list[1].Name != "report.log" {
		t.Fatalf("names %q %q", list[0].Name, list[1].Name)
	}
}
