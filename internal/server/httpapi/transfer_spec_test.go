package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/storage"
	"github.com/zypher-systems/kairos/internal/testutil"
)

// A job saved before such routes were refused must fail when it is claimed,
// not hand the agent a server path to open on its own disk.
func TestAgentSpecRefusesServerDirectories(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ag, _, err := st.CreateAgent(ctx, "edge", nil)
	if err != nil {
		t.Fatal(err)
	}
	mount := &store.StorageLocation{Name: "server-share", Kind: "mount", BasePath: "/mnt/in"}
	onAgent := &store.StorageLocation{Name: "edge-disk", Kind: "agent", AgentID: &ag.ID, BasePath: "/data"}
	bucket := &store.StorageLocation{Name: "lake", Kind: "s3", Bucket: "in", Region: "eu-west-1"}
	for _, l := range []*store.StorageLocation{mount, onAgent, bucket} {
		if err := st.CreateStorageLocation(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	end := func(l *store.StorageLocation) store.TransferEndpoint {
		return store.TransferEndpoint{Kind: "location", LocationID: &l.ID, Path: "in/"}
	}
	for name, cfg := range map[string]store.TransferConfig{
		"source": {Source: end(mount), Dest: end(onAgent)},
		"dest":   {Source: end(onAgent), Dest: end(mount)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildAgentTransferSpec(ctx, st, store.Job{TransferConfig: cfg}, store.Run{})
			if err == nil || !strings.Contains(err.Error(), "server-share is on the server") {
				t.Fatalf("got %v", err)
			}
		})
	}

	trigger := store.Job{
		WatchEnabled: true, WatchTarget: "local",
		TransferConfig: store.TransferConfig{Source: end(onAgent), Dest: end(onAgent), FromTrigger: true},
	}
	if _, err := buildAgentTransferSpec(ctx, st, trigger, store.Run{}); err == nil || !strings.Contains(err.Error(), "server filesystem is on the server") {
		t.Fatalf("server filesystem trigger: %v", err)
	}

	// Sides the agent opens itself still go through.
	ts, err := buildAgentTransferSpec(ctx, st, store.Job{TransferConfig: store.TransferConfig{Source: end(bucket), Dest: end(onAgent)}}, store.Run{})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Source.Storage.Kind != storage.KindS3 || ts.Dest.Storage.Kind != storage.KindLocal || ts.Dest.Storage.Root != "/data" {
		t.Fatalf("spec %+v", ts)
	}
	legacy := store.TransferConfig{
		Source: store.TransferEndpoint{Kind: "agent", AgentID: &ag.ID, Path: "/data/a.csv"},
		Dest:   store.TransferEndpoint{Kind: "library", Path: "a.csv"},
	}
	if _, err := buildAgentTransferSpec(ctx, st, store.Job{TransferConfig: legacy}, store.Run{}); err != nil {
		t.Fatalf("legacy agent path: %v", err)
	}
}
