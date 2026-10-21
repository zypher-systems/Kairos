package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantApproval(t *testing.T) { t.Helper() }

func TestApprovalStillHoldsWithoutLicense(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	j := localJob("open")
	j.RequireApproval = true
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != store.RunPendingApproval {
		t.Fatalf("status %s; a lapsed approval gate must still hold", run.Status)
	}
	if _, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second); err != nil || ok {
		t.Fatalf("a held run must not be claimable: ok=%v err=%v", ok, err)
	}
}

func TestGatedEnqueuePendingUntilApprove(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	j := localJob("gated")
	j.RequireApproval = true
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != store.RunPendingApproval {
		t.Fatalf("status %s", run.Status)
	}
	_, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("pending run must not be claimable")
	}
	approved, err := st.ApproveRun(ctx, run.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != store.RunQueued {
		t.Fatalf("after approve %s", approved.Status)
	}
	_, claimed, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim after approve: ok=%v err=%v", ok, err)
	}
	if claimed.ID != run.ID {
		t.Fatalf("claimed %s", claimed.ID)
	}
}

func TestRejectSkipsAndIsNotClaimed(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	j := localJob("reject-me")
	j.RequireApproval = true
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.RejectRun(ctx, run.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunSkipped || got.Error != "rejected" {
		t.Fatalf("reject %s %q", got.Status, got.Error)
	}
	_, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("rejected run must not be claimed")
	}
}

func TestCancelPending(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	j := localJob("cancel-me")
	j.RequireApproval = true
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CancelRuns(ctx, []uuid.UUID{run.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunCanceled {
		t.Fatalf("status %s", got.Status)
	}
}

func TestPendingNotifyAndSilentApprove(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	ch := &store.NotificationChannel{
		Name:             "ops",
		Type:             "webhook",
		Config:           map[string]string{"url": "http://127.0.0.1:9/hook"},
		Enabled:          true,
		NotifyOnPending:  true,
		NotifyOnRejected: true,
	}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	j := localJob("notify-gate")
	j.RequireApproval = true
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 pending notify, got %d", len(rows))
	}
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != store.EventRunPending {
		t.Fatalf("event %v", payload["event"])
	}
	for _, r := range rows {
		_ = st.CompleteOutbox(ctx, r.ID)
	}
	if _, err := st.ApproveRun(ctx, run.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	again, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("approve must be silent, got %d", len(again))
	}
}

func TestApproveNotPendingIsError(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateJob(ctx, localJob("plain"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ApproveRun(ctx, run.ID, admin.ID)
	if !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("want ErrNotPending, got %v", err)
	}
}
