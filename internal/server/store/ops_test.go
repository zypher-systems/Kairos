package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func outboxChannel(t *testing.T, st *store.Store) *store.NotificationChannel {
	t.Helper()
	ch := &store.NotificationChannel{
		Name:            "hook",
		Type:            "webhook",
		Config:          map[string]string{"url": "https://example.com/hook"},
		Enabled:         true,
		NotifyOnFailure: true,
	}
	if err := st.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestOutboxClaimLocksAcrossWorkers(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ch := outboxChannel(t, st)
	if _, err := st.Pool().Exec(ctx, `INSERT INTO notify_outbox (channel_id, payload) VALUES ($1, '{"event":"run_failed"}')`, ch.ID); err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: n=%d err=%v", len(first), err)
	}
	// A second worker (another replica) sees nothing while the lock holds.
	second, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil || len(second) != 0 {
		t.Fatalf("locked row must not be claimed twice: n=%d err=%v", len(second), err)
	}
	// A failure releases the lock and schedules the retry.
	if err := st.FailOutbox(ctx, first[0].ID, 1, "boom"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE notify_outbox SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	again, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil || len(again) != 1 || again[0].Attempt != 1 {
		t.Fatalf("after failure the row must be claimable again: %+v err=%v", again, err)
	}
}

func TestOutboxDeadLetterKeptAndRetryable(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ch := outboxChannel(t, st)
	var id string
	if err := st.Pool().QueryRow(ctx, `INSERT INTO notify_outbox (channel_id, payload) VALUES ($1, '{"event":"run_failed"}') RETURNING id`, ch.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.ClaimOutboxDue(ctx, 1, time.Minute)
	if len(rows) != 1 {
		t.Fatal("claim")
	}
	if err := st.FailOutbox(ctx, rows[0].ID, 8, "still down"); err != nil {
		t.Fatal(err)
	}
	dead, err := st.ListDeadOutbox(ctx, 10)
	if err != nil || len(dead) != 1 || dead[0].DeadAt == nil || dead[0].LastError != "still down" {
		t.Fatalf("dead letter must be kept: %+v err=%v", dead, err)
	}
	pending, deadN, err := st.OutboxCounts(ctx)
	if err != nil || pending != 0 || deadN != 1 {
		t.Fatalf("counts pending=%d dead=%d err=%v", pending, deadN, err)
	}
	if got, _ := st.ClaimOutboxDue(ctx, 10, time.Minute); len(got) != 0 {
		t.Fatal("dead letters are not delivered on their own")
	}
	if err := st.RetryDeadOutbox(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ClaimOutboxDue(ctx, 10, time.Minute); len(got) != 1 || got[0].Attempt != 0 {
		t.Fatalf("retried dead letter must be due at once: %+v", got)
	}
	if err := st.DeleteDeadOutbox(ctx, rows[0].ID); err == nil {
		t.Fatal("a live row is not a dead letter")
	}
}

func TestPruneRunsKeepsActiveRuns(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	job, err := st.CreateJob(ctx, localJob("prune"), nil)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Both are ancient; only the finished one may go.
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET created_at = now() - interval '400 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status = 'succeeded', finished_at = now() WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	n, err := st.PruneRuns(ctx, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d want 1", n)
	}
	if _, err := st.GetRun(ctx, queued.ID); err != nil {
		t.Fatalf("queued run must survive pruning: %v", err)
	}
	// Per-job cap also skips active runs.
	for i := 0; i < 3; i++ {
		r, _ := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
		_, _ = st.Pool().Exec(ctx, `UPDATE runs SET status = 'failed', finished_at = now() WHERE id = $1`, r.ID)
	}
	if _, err := st.PruneRuns(ctx, 1, 3650); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRun(ctx, queued.ID); err != nil {
		t.Fatalf("queued run must survive the per-job cap: %v", err)
	}
}

func TestRevealJobSecretsFailsOnBadKey(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	j := localJob("secret")
	j.EnvSecrets = map[string]string{"TOKEN": "hunter2!"}
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := store.New(st.Pool(), "a-different-secret-that-cannot-open-it")
	got, err := other.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.RevealJobSecrets(&got); err == nil {
		t.Fatal("a secret sealed with another key must not reveal as empty")
	}
	mine, _ := st.GetJob(ctx, job.ID)
	if err := st.RevealJobSecrets(&mine); err != nil || mine.EnvSecrets["TOKEN"] != "hunter2!" {
		t.Fatalf("own key must reveal: %v %q", err, mine.EnvSecrets["TOKEN"])
	}
}
