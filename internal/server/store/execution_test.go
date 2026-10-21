package store_test

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestExtendLeaseFencedByEpoch(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	job, err := st.CreateJob(ctx, localJob("fence-epoch"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claim, _, ok, err := st.ClaimNextDispatcher(ctx, time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := st.ExtendLease(ctx, run.ID, claim.TargetID, claim.ClaimVersion, 30*time.Second); err != nil {
		t.Fatalf("own epoch must renew: %v", err)
	}
	if err := st.ExtendLease(ctx, run.ID, claim.TargetID, claim.ClaimVersion+7, 30*time.Second); err != store.ErrStaleClaim {
		t.Fatalf("foreign epoch must be stale: %v", err)
	}

	// Lease lapses, the run is requeued and a second worker with the same
	// target id ('local') claims it. The first worker's renew must fail.
	if _, err := st.Pool().Exec(ctx, `UPDATE queue_claims SET lease_expires_at = now() - interval '1 second' WHERE run_id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := st.RequeueExpiredClaims(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("requeue n=%d err=%v", n, err)
	}
	claim2, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("reclaim: ok=%v err=%v", ok, err)
	}
	if claim2.TargetID != claim.TargetID {
		t.Fatalf("both workers share target id, got %q and %q", claim.TargetID, claim2.TargetID)
	}
	if err := st.ExtendLease(ctx, run.ID, claim.TargetID, claim.ClaimVersion, 30*time.Second); err != store.ErrStaleClaim {
		t.Fatalf("superseded worker must be fenced: %v", err)
	}
	if err := st.ExtendLease(ctx, run.ID, claim2.TargetID, claim2.ClaimVersion, 30*time.Second); err != nil {
		t.Fatalf("new holder must renew: %v", err)
	}
}

func TestMarkRunRunningRefusesCanceled(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	job, err := st.CreateJob(ctx, localJob("cancel-before-start"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := st.CancelRuns(ctx, []uuid.UUID{run.ID}); err != nil {
		t.Fatal(err)
	}
	started, err := st.MarkRunRunning(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started {
		t.Fatal("a canceled run must not start")
	}
	got, _ := st.GetRun(ctx, run.ID)
	if got.Status != store.RunCanceled {
		t.Fatalf("status %s", got.Status)
	}
}

func TestReleaseClaimRequeuesSameAttempt(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	job, err := st.CreateJob(ctx, localJob("release"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claim, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if started, err := st.MarkRunRunning(ctx, run.ID); err != nil || !started {
		t.Fatalf("start: %v %v", started, err)
	}
	if err := st.ReleaseClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetRun(ctx, run.ID)
	if got.Status != store.RunQueued || got.Attempt != 1 || got.StartedAt != nil {
		t.Fatalf("released run %+v", got)
	}
	if got.ClaimEpoch != run.ClaimEpoch+1 {
		t.Fatalf("epoch must bump: %d -> %d", run.ClaimEpoch, got.ClaimEpoch)
	}
	if _, err := st.CompleteClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion, store.RunSucceeded, nil, ""); err != store.ErrStaleClaim {
		t.Fatalf("late result from the releasing worker must be stale: %v", err)
	}
	if err := st.ReleaseClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion); err != store.ErrStaleClaim {
		t.Fatalf("double release must be stale: %v", err)
	}
	if _, _, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second); err != nil || !ok {
		t.Fatalf("released run must be claimable again: ok=%v err=%v", ok, err)
	}
}

func TestCompleteClaimRetriesBeforeFanOut(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	wf := &store.Workflow{Name: "retry-chain", Enabled: true}
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	ja := localJob("A")
	ja.RetryCount = 2
	ja.RetryBaseDelayMs = 60_000
	a, err := st.CreateJob(ctx, ja, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateJob(ctx, localJob("B"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceWorkflowEdges(ctx, wf.ID, []store.WorkflowEdge{
		{FromJobID: a.ID, ToJobID: b.ID, Condition: "failure"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueWithPolicy(ctx, a, store.TriggerManual, time.Now()); err != nil {
		t.Fatal(err)
	}

	failOnce := func() store.CompleteResult {
		t.Helper()
		claim, run, ok, err := st.ClaimNextDispatcher(ctx, 30*time.Second)
		if err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		code := 1
		res, err := st.CompleteClaim(ctx, run.ID, claim.TargetID, claim.ClaimVersion, store.RunFailed, &code, "boom")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	makeDue := func() {
		t.Helper()
		if _, err := st.Pool().Exec(ctx, `UPDATE runs SET not_before = now() WHERE status = 'queued'`); err != nil {
			t.Fatal(err)
		}
	}

	res := failOnce()
	if res.Retry == nil || res.Retry.Attempt != 2 || res.Retry.Trigger != store.TriggerRetry {
		t.Fatalf("first failure must queue attempt 2: %+v", res.Retry)
	}
	if !res.Retry.NotBefore.After(time.Now().Add(30 * time.Second)) {
		t.Fatalf("retry must carry its backoff: not_before=%s", res.Retry.NotBefore)
	}
	if len(res.Downstream) != 0 {
		t.Fatalf("no fan-out before the final attempt: %v", res.Downstream)
	}
	if runs, _ := st.ListRuns(ctx, store.RunFilter{JobID: &b.ID, Limit: 10}); len(runs) != 0 {
		t.Fatalf("B must not run yet: %v", runs)
	}
	var outbox int
	_ = st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM notify_outbox`).Scan(&outbox)
	if outbox != 0 {
		t.Fatalf("no failure notification before the final attempt, got %d", outbox)
	}

	makeDue()
	res = failOnce()
	if res.Retry == nil || res.Retry.Attempt != 3 {
		t.Fatalf("second failure must queue attempt 3: %+v", res.Retry)
	}

	makeDue()
	res = failOnce()
	if res.Retry != nil {
		t.Fatalf("attempt 3 is final, got retry %+v", res.Retry)
	}
	if len(res.Downstream) != 1 || res.Downstream[0].JobID != b.ID {
		t.Fatalf("final failure must fan out to B: %v", res.Downstream)
	}
	if res.Downstream[0].WorkflowExecutionID == nil {
		t.Fatal("child must carry the execution id")
	}
}

func TestJoinRequiresEveryEdgeCondition(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	wf := &store.Workflow{Name: "strict-join", Enabled: true}
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	ja, _ := st.CreateJob(ctx, localJob("A"), nil)
	jb, _ := st.CreateJob(ctx, localJob("B"), nil)
	jc, _ := st.CreateJob(ctx, localJob("C"), nil)
	if err := st.ReplaceWorkflowEdges(ctx, wf.ID, []store.WorkflowEdge{
		{FromJobID: ja.ID, ToJobID: jc.ID, Condition: "success"},
		{FromJobID: jb.ID, ToJobID: jc.ID, Condition: "success"},
	}); err != nil {
		t.Fatal(err)
	}
	execID, err := st.CreateWorkflowExecution(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := st.EnqueueWithPolicyExec(ctx, ja, store.TriggerWorkflow, time.Now(), execID)
	rb, _ := st.EnqueueWithPolicyExec(ctx, jb, store.TriggerWorkflow, time.Now(), execID)
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='failed', finished_at=now() WHERE id=$1`, ra.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='succeeded', finished_at=now() WHERE id=$1`, rb.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := st.WorkflowJoinReady(ctx, execID, wf.ID, jc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("C must not run: A's edge wants success and A failed")
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='succeeded' WHERE id=$1`, ra.ID); err != nil {
		t.Fatal(err)
	}
	ready, err = st.WorkflowJoinReady(ctx, execID, wf.ID, jc.ID)
	if err != nil || !ready {
		t.Fatalf("both parents succeeded: ready=%v err=%v", ready, err)
	}
}

func TestAppendLogsStoresBinaryAndSplitRunes(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	job, err := st.CreateJob(ctx, localJob("logs"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("日", store.MaxLogChunkBytes/3+10) // over the chunk cap, 3 bytes per rune
	n, err := st.AppendLogs(ctx, run.ID, []store.LogChunk{
		{Seq: 1, Stream: "stdout", Data: "caf\xc3"},          // split é
		{Seq: 2, Stream: "stdout", Data: "\xa9 nul\x00byte"}, // other half + NUL
		{Seq: 3, Stream: "stdout", Data: "\xff\xfe binary"},
		{Seq: 4, Stream: "stdout", Data: big},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("accepted %d", n)
	}
	chunks, err := st.Logs(ctx, run.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 4 {
		t.Fatalf("stored %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if !utf8.ValidString(c.Data) {
			t.Fatalf("seq %d stored invalid UTF-8: %q", c.Seq, c.Data)
		}
		if strings.IndexByte(c.Data, 0) >= 0 {
			t.Fatalf("seq %d kept a NUL byte", c.Seq)
		}
		if len(c.Data) > store.MaxLogChunkBytes {
			t.Fatalf("seq %d over the chunk cap: %d", c.Seq, len(c.Data))
		}
	}
	if chunks[1].Data != "� nulbyte" {
		t.Fatalf("seq 2 = %q", chunks[1].Data)
	}
}

func TestReplacePolicyKeepsAgentClaimForKill(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ag, _, err := st.CreateAgent(ctx, "w", nil)
	if err != nil {
		t.Fatal(err)
	}
	j := localJob("replace")
	j.TargetType = "agent"
	j.TargetSelector = []byte(`{"agent_id":"` + ag.ID.String() + `"}`)
	j.ConcurrencyPolicy = "replace"
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := st.ClaimNextForAgent(ctx, ag.ID, 30*time.Second); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	got, _ := st.GetRun(ctx, first.ID)
	if got.Status != store.RunRunning || got.StartedAt == nil {
		t.Fatalf("an agent claim is a start: %+v", got)
	}
	if _, err := st.EnqueueWithPolicy(ctx, job, store.TriggerSchedule, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetRun(ctx, first.ID)
	if got.Status != store.RunCanceled {
		t.Fatalf("replace must cancel the running run: %s", got.Status)
	}
	kills, err := st.CanceledRunsForAgent(ctx, ag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kills) != 1 || kills[0] != first.ID.String() {
		t.Fatalf("agent must be told to kill the replaced run: %v", kills)
	}
}
