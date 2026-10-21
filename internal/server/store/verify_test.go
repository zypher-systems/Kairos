package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestCompleteClaimFansOutDownstream(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	wf := &store.Workflow{Name: "chain", Enabled: true}
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	ja, err := st.CreateJob(ctx, localJob("parent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := st.CreateJob(ctx, localJob("child"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceWorkflowEdges(ctx, wf.ID, []store.WorkflowEdge{
		{FromJobID: ja.ID, ToJobID: jb.ID, Condition: "success"},
	}); err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, ja, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claim, claimed, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if claimed.ID != run.ID {
		t.Fatalf("claimed %s want %s", claimed.ID, run.ID)
	}
	res, err := st.CompleteClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion, store.RunSucceeded, intPtr(0), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Downstream) != 1 || res.Downstream[0].JobID != jb.ID {
		t.Fatalf("downstream=%v", res.Downstream)
	}
	runs, err := st.ListRuns(ctx, store.RunFilter{JobID: &jb.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != store.RunQueued {
		t.Fatalf("child runs=%v", runs)
	}
}

func TestCompleteClaimWritesNotifyOutbox(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ch := &store.NotificationChannel{
		Name:            "hook",
		Type:            "webhook",
		Config:          map[string]string{"url": "https://example.com/hook"},
		Enabled:         true,
		NotifyOnSuccess: true,
		NotifyOnFailure: true,
	}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	j, err := st.CreateJob(ctx, localJob("notify-me"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, j, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claim, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := st.CompleteClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion, store.RunSucceeded, intPtr(0), ""); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ClaimOutboxDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("outbox rows=%d", len(rows))
	}
	if rows[0].ChannelID != ch.ID {
		t.Fatalf("channel %s want %s", rows[0].ChannelID, ch.ID)
	}
}

func TestNotifyOutboxHonorsJobChannelIDs(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	keep := &store.NotificationChannel{
		Name: "keep", Type: "webhook",
		Config:          map[string]string{"url": "https://example.com/keep"},
		Enabled:         true,
		NotifyOnSuccess: true,
		NotifyOnFailure: true,
	}
	drop := &store.NotificationChannel{
		Name: "drop", Type: "webhook",
		Config:          map[string]string{"url": "https://example.com/drop"},
		Enabled:         true,
		NotifyOnSuccess: true,
		NotifyOnFailure: true,
	}
	if err := st.CreateChannel(ctx, keep); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateChannel(ctx, drop); err != nil {
		t.Fatal(err)
	}
	spec := localJob("routed")
	spec.NotifyChannelIDs = []uuid.UUID{keep.ID}
	j, err := st.CreateJob(ctx, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, j, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claim, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := st.CompleteClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion, store.RunSucceeded, intPtr(0), ""); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ClaimOutboxDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChannelID != keep.ID {
		t.Fatalf("outbox=%v want only %s", rows, keep.ID)
	}
}

func TestPruneRunsKeepsNewest(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	j, err := st.CreateJob(ctx, localJob("prune"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		r, err := st.EnqueueWithPolicy(ctx, j, store.TriggerManual, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		// Only finished runs are pruned.
		if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status = 'succeeded', finished_at = now() WHERE id = $1`, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.PruneRuns(ctx, 2, 3650)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("pruned %d want >= 3", n)
	}
	runs, err := st.ListRuns(ctx, store.RunFilter{JobID: &j.ID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("kept %d want 2", len(runs))
	}
}

func intPtr(n int) *int { return &n }
