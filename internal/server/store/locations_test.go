package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestStorageLocationSealsSecretAndValidates(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	bad := &store.StorageLocation{Name: "x", Kind: "mount", BasePath: "relative"}
	if err := st.CreateStorageLocation(ctx, bad); err == nil {
		t.Fatal("mount needs an absolute base path")
	}
	bad = &store.StorageLocation{Name: "x", Kind: "s3", Bucket: "b"}
	if err := st.CreateStorageLocation(ctx, bad); err == nil {
		t.Fatal("aws s3 needs a region or an endpoint")
	}
	bad = &store.StorageLocation{Name: "x", Kind: "nfs", Host: "nas", Export: "volume1"}
	if err := st.CreateStorageLocation(ctx, bad); err == nil {
		t.Fatal("nfs export must be an absolute path")
	}
	bad = &store.StorageLocation{Name: "x", Kind: "smb", Host: "files", Share: "a/b", Username: "u"}
	if err := st.CreateStorageLocation(ctx, bad); err == nil {
		t.Fatal("smb share must be a bare name")
	}
	smb := &store.StorageLocation{Name: "finance", Kind: "smb", Host: "files", Share: "exports", Domain: "CORP", Username: "svc", SecretKey: "smb-pass-word", BasePath: "\\incoming\\2026"}
	if err := st.CreateStorageLocation(ctx, smb); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetStorageLocationForDelivery(ctx, smb.ID); got.SecretKey != "smb-pass-word" || got.BasePath != "incoming/2026" {
		t.Fatalf("smb location: %+v", got)
	}
	l := &store.StorageLocation{Name: "minio", Kind: "s3", Bucket: "reports", Endpoint: "minio:9000", AccessKey: "ak", SecretKey: "sk-secret-value", PathStyle: true}
	if err := st.CreateStorageLocation(ctx, l); err != nil {
		t.Fatal(err)
	}
	if l.SecretKey != "" || !l.HasSecretKey {
		t.Fatalf("secret must not be echoed: %+v", l)
	}
	var cipher string
	if err := st.Pool().QueryRow(ctx, `SELECT secret_cipher FROM storage_locations WHERE id = $1`, l.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if cipher == "" || cipher == "sk-secret-value" {
		t.Fatalf("secret must be sealed at rest: %q", cipher)
	}
	got, err := st.GetStorageLocation(ctx, l.ID)
	if err != nil || got.SecretKey != store.SecretMask {
		t.Fatalf("api view must mask: %+v %v", got, err)
	}
	live, err := st.GetStorageLocationForDelivery(ctx, l.ID)
	if err != nil || live.SecretKey != "sk-secret-value" {
		t.Fatalf("delivery view must reveal: %q %v", live.SecretKey, err)
	}
	// Update without a key keeps it; a duplicate name is refused.
	got.SecretKey = ""
	got.Endpoint = "minio2:9000"
	if err := st.UpdateStorageLocation(ctx, &got); err != nil {
		t.Fatal(err)
	}
	live, _ = st.GetStorageLocationForDelivery(ctx, l.ID)
	if live.SecretKey != "sk-secret-value" || live.Endpoint != "minio2:9000" {
		t.Fatalf("update must keep the key: %+v", live)
	}
	dup := &store.StorageLocation{Name: "minio", Kind: "mount", BasePath: "/tmp"}
	if err := st.CreateStorageLocation(ctx, dup); err == nil {
		t.Fatal("duplicate name must be refused")
	}
}

func TestTransferTargetDerivedFromEndpoints(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ag, _, err := st.CreateAgent(ctx, "edge", nil)
	if err != nil {
		t.Fatal(err)
	}
	bucket := &store.StorageLocation{Name: "lake", Kind: "s3", Bucket: "in", Region: "eu-west-1"}
	if err := st.CreateStorageLocation(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	onAgent := &store.StorageLocation{Name: "edge-disk", Kind: "agent", AgentID: &ag.ID, BasePath: "/data"}
	if err := st.CreateStorageLocation(ctx, onAgent); err != nil {
		t.Fatal(err)
	}
	j := localJob("move")
	j.ExecMode = "transfer"
	j.TargetType = "ssh" // wrong on purpose: the store derives the target
	j.TransferConfig = store.TransferConfig{
		Source: store.TransferEndpoint{Kind: "location", LocationID: &bucket.ID, Path: "*.csv"},
		Dest:   store.TransferEndpoint{Kind: "location", LocationID: &onAgent.ID, Path: "archive/"},
	}
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if job.TargetType != "agent" {
		t.Fatalf("an agent location makes the agent the target, got %s", job.TargetType)
	}
	var sel struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.Unmarshal(job.TargetSelector, &sel)
	if sel.AgentID != ag.ID.String() {
		t.Fatalf("selector %s", job.TargetSelector)
	}
	// Server-only endpoints run locally.
	job.TransferConfig.Dest = store.TransferEndpoint{Kind: "library", Path: "out.csv"}
	updated, err := st.UpdateJob(ctx, &job)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TargetType != "local" {
		t.Fatalf("s3 -> library runs on the server, got %s", updated.TargetType)
	}
	// from_trigger needs the trigger.
	job.TransferConfig.FromTrigger = true
	if _, err := st.UpdateJob(ctx, &job); err == nil {
		t.Fatal("from_trigger without a file trigger must be refused")
	}
}

// A directory on the server cannot be half of a transfer that runs on an
// agent: the agent would open that path on its own disk.
func TestTransferRefusesServerDirectoryOnAnAgentRoute(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ag, _, err := st.CreateAgent(ctx, "edge", nil)
	if err != nil {
		t.Fatal(err)
	}
	mount := &store.StorageLocation{Name: "server-share", Kind: "mount", BasePath: "/mnt/in"}
	onAgent := &store.StorageLocation{Name: "edge-disk", Kind: "agent", AgentID: &ag.ID, BasePath: "/data"}
	for _, l := range []*store.StorageLocation{mount, onAgent} {
		if err := st.CreateStorageLocation(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	mountEnd := store.TransferEndpoint{Kind: "location", LocationID: &mount.ID, Path: "in/"}
	agentEnd := store.TransferEndpoint{Kind: "location", LocationID: &onAgent.ID, Path: "in/"}
	legacyAgentEnd := store.TransferEndpoint{Kind: "agent", AgentID: &ag.ID, Path: "/data/in/"}
	library := store.TransferEndpoint{Kind: "library", Path: "out.csv"}

	transfer := func(cfg store.TransferConfig) *store.Job {
		j := localJob("move")
		j.ExecMode = "transfer"
		j.TransferConfig = cfg
		return j
	}
	refused := map[string]*store.Job{
		"mount to agent":        transfer(store.TransferConfig{Source: mountEnd, Dest: agentEnd}),
		"agent to mount":        transfer(store.TransferConfig{Source: agentEnd, Dest: mountEnd}),
		"mount to legacy agent": transfer(store.TransferConfig{Source: mountEnd, Dest: legacyAgentEnd}),
	}
	watched := transfer(store.TransferConfig{Source: agentEnd, Dest: mountEnd, FromTrigger: true})
	watched.WatchEnabled, watched.WatchTarget, watched.WatchLocationID = true, "location", &onAgent.ID
	watched.WatchPath, watched.WatchIntervalSec = "*.csv", 10
	refused["agent trigger to mount"] = watched
	watchServer := transfer(store.TransferConfig{Source: agentEnd, Dest: agentEnd, FromTrigger: true})
	watchServer.WatchEnabled, watchServer.WatchTarget = true, "local"
	watchServer.WatchPath, watchServer.WatchIntervalSec = "/mnt/in/*.csv", 10
	refused["server filesystem trigger to agent"] = watchServer
	for name, j := range refused {
		t.Run(name, func(t *testing.T) {
			_, err := st.CreateJob(ctx, j, nil)
			if err == nil || !strings.Contains(err.Error(), "on the server") {
				t.Fatalf("must be refused with the reason, got %v", err)
			}
		})
	}

	// The same locations stay usable on routes that can reach them.
	for name, cfg := range map[string]store.TransferConfig{
		"mount to library": {Source: mountEnd, Dest: library},
		"library to agent": {Source: library, Dest: agentEnd},
	} {
		t.Run(name, func(t *testing.T) {
			j := transfer(cfg)
			if cfg.Source.Kind == "library" {
				f := store.FileEntry{Name: "a.csv", StoragePath: "files/a"}
				if err := st.CreateFile(ctx, &f); err != nil {
					t.Fatal(err)
				}
				j.TransferConfig.Source.FileID = &f.ID
			}
			j.Name = name
			if _, err := st.CreateJob(ctx, j, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
