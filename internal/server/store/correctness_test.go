package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func localJob(name string) *store.Job {
	return &store.Job{
		Name:              name,
		ScheduleType:      "interval",
		IntervalSeconds:   60,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		TimeoutSeconds:    60,
		ConcurrencyPolicy: "allow",
		Enabled:           true,
	}
}

func TestClaimFenceRejectsForeignAgent(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()

	a1, _, err := st.CreateAgent(ctx, "one", map[string]string{"role": "w"})
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := st.CreateAgent(ctx, "two", map[string]string{"role": "w"})
	if err != nil {
		t.Fatal(err)
	}
	j := localJob("fence")
	j.TargetType = "agent"
	j.TargetSelector = []byte(`{"agent_id":"` + a1.ID.String() + `"}`)
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}

	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claim, _, ok, err := st.ClaimNextForAgent(ctx, a1.ID, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	held, err := st.RunClaimedBy(ctx, run.ID, a2.ID.String())
	if err != nil || held {
		t.Fatalf("foreign agent must not hold claim: held=%v err=%v", held, err)
	}
	if err := st.ExtendLease(ctx, run.ID, a2.ID.String(), store.EpochUnknown, 30*time.Second); err != store.ErrStaleClaim {
		t.Fatalf("extend foreign: %v", err)
	}
	if _, err := st.CompleteClaim(ctx, run.ID, a2.ID.String(), claim.ClaimVersion, store.RunSucceeded, nil, ""); err != store.ErrStaleClaim {
		t.Fatalf("complete foreign: %v", err)
	}
	if _, err := st.CompleteClaim(ctx, run.ID, a1.ID.String(), claim.ClaimVersion, store.RunSucceeded, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRequeueBumpsEpochAndStalesOldComplete(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()

	a1, _, err := st.CreateAgent(ctx, "w", nil)
	if err != nil {
		t.Fatal(err)
	}
	j := localJob("lease")
	j.TargetType = "agent"
	j.TargetSelector = []byte(`{"agent_id":"` + a1.ID.String() + `"}`)
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now()); err != nil {
		t.Fatal(err)
	}
	claim, runID, ok, err := st.ClaimNextForAgent(ctx, a1.ID, time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	time.Sleep(5 * time.Millisecond)
	n, err := st.RequeueExpiredClaims(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("requeued %d want 1", n)
	}
	if _, err := st.CompleteClaim(ctx, runID, a1.ID.String(), claim.ClaimVersion, store.RunSucceeded, nil, ""); err != store.ErrStaleClaim {
		t.Fatalf("old complete must be stale: %v", err)
	}
	cancels, err := st.CanceledRunsForAgent(ctx, a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cancels) != 1 {
		t.Fatalf("want cancel signal, got %v", cancels)
	}
}

func TestLogCap(t *testing.T) {
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
	chunk := strings.Repeat("x", store.MaxLogChunkBytes+10)
	if err := st.AppendLog(ctx, run.ID, 1, "stdout", chunk); err != nil {
		t.Fatal(err)
	}
	logs, err := st.Logs(ctx, run.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || len(logs[0].Data) != store.MaxLogChunkBytes {
		t.Fatalf("chunk not capped: len=%d", len(logs[0].Data))
	}
}

func TestReplaceEdgesRejectsCycle(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	wf := &store.Workflow{Name: "cyc", Enabled: true}
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	ja, err := st.CreateJob(ctx, localJob("a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := st.CreateJob(ctx, localJob("b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = st.ReplaceWorkflowEdges(ctx, wf.ID, []store.WorkflowEdge{
		{FromJobID: ja.ID, ToJobID: jb.ID, Condition: "always"},
		{FromJobID: jb.ID, ToJobID: ja.ID, Condition: "always"},
	})
	if err != store.ErrDAGCycle {
		t.Fatalf("got %v want cycle", err)
	}
}

func TestDispatcherOrphanSweepIncludesSSH(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	job, err := st.CreateJob(ctx, localJob("sshish"), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='running', target_type='ssh' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.ForceRequeueDispatcherOrphaned(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunQueued {
		t.Fatalf("status %s want queued", got.Status)
	}
}

func TestWorkflowJoinWaitsForAllParents(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	wf := &store.Workflow{Name: "diamond", Enabled: true}
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	ja, err := st.CreateJob(ctx, localJob("A"), nil)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := st.CreateJob(ctx, localJob("B"), nil)
	if err != nil {
		t.Fatal(err)
	}
	jc, err := st.CreateJob(ctx, localJob("C"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceWorkflowEdges(ctx, wf.ID, []store.WorkflowEdge{
		{FromJobID: ja.ID, ToJobID: jc.ID, Condition: "always"},
		{FromJobID: jb.ID, ToJobID: jc.ID, Condition: "always"},
	}); err != nil {
		t.Fatal(err)
	}
	execID, err := st.CreateWorkflowExecution(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	ra, err := st.EnqueueWithPolicyExec(ctx, ja, store.TriggerWorkflow, time.Now(), execID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='succeeded', finished_at=now() WHERE id=$1`, ra.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := st.WorkflowJoinReady(ctx, execID, wf.ID, jc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("join must wait for B")
	}
	rb, err := st.EnqueueWithPolicyExec(ctx, jb, store.TriggerWorkflow, time.Now(), execID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='succeeded', finished_at=now() WHERE id=$1`, rb.ID); err != nil {
		t.Fatal(err)
	}
	ready, err = st.WorkflowJoinReady(ctx, execID, wf.ID, jc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("join should fire after both parents")
	}
}
