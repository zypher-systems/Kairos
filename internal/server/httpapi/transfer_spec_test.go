package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

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

// A token the server could not check is not a bad token: the agent must see
// an error it can retry, not a refusal.
func TestAgentAuthSeparatesFailureFromRefusal(t *testing.T) {
	st := testutil.Store(t)
	a := &API{store: st}
	reached := false
	h := a.AgentAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	do := func(ctx context.Context) int {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/watch-event", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer no-such-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := do(context.Background()); got != http.StatusUnauthorized {
		t.Fatalf("an unknown token: %d", got)
	}
	failed, cancel := context.WithCancel(context.Background())
	cancel()
	if got := do(failed); got != http.StatusInternalServerError {
		t.Fatalf("a failed lookup: %d", got)
	}
	if reached {
		t.Fatal("neither request may get through")
	}
}

func TestWatchAssignedSeparatesFailureFromRefusal(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	a := &API{store: st}
	ag, _, err := st.CreateAgent(ctx, "edge", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := st.CreateAgent(ctx, "other", nil)
	if err != nil {
		t.Fatal(err)
	}
	loc := &store.StorageLocation{Name: "edge-disk", Kind: "agent", AgentID: &ag.ID, BasePath: "/data"}
	if err := st.CreateStorageLocation(ctx, loc); err != nil {
		t.Fatal(err)
	}
	job := store.Job{WatchEnabled: true, WatchTarget: "location", WatchLocationID: &loc.ID}

	if ok, err := a.watchAssigned(ctx, job, ag.ID); !ok || err != nil {
		t.Fatalf("the owning agent: %v %v", ok, err)
	}
	if ok, err := a.watchAssigned(ctx, job, other.ID); ok || err != nil {
		t.Fatalf("another agent is a refusal: %v %v", ok, err)
	}
	failed, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.watchAssigned(failed, job, ag.ID); err == nil {
		t.Fatal("a failed lookup must be an error, not a refusal")
	}
	missing := uuid.New()
	job.WatchLocationID = &missing
	if ok, err := a.watchAssigned(ctx, job, ag.ID); ok || err != nil {
		t.Fatalf("a deleted location is a refusal: %v %v", ok, err)
	}
}
