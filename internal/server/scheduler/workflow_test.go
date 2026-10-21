package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func manualJob(name string) *store.Job {
	return &store.Job{
		Name: name, ScheduleType: "manual", Timezone: "UTC", TargetType: "local", ExecMode: "shell",
		ShellScript: "true", TimeoutSeconds: 60, ConcurrencyPolicy: "allow", Enabled: true,
	}
}

// A workflow with its own schedule fires every root job under one
// execution; jobs with the manual schedule type never fire on their own.
func TestScheduledWorkflowStartsAllRoots(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	a, err := st.CreateJob(ctx, manualJob("extract-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateJob(ctx, manualJob("extract-b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateJob(ctx, manualJob("merge"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.NextRunAt != nil {
		t.Fatalf("a manual job has no next run: %v", a.NextRunAt)
	}
	due := time.Now().Add(-time.Second)
	wf := &store.Workflow{Name: "nightly", Enabled: true, ScheduleType: "interval", IntervalSeconds: 3600, Timezone: "UTC", NextRunAt: &due}
	if err := st.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	edges := []store.WorkflowEdge{
		{FromJobID: a.ID, ToJobID: m.ID, Condition: "success"},
		{FromJobID: b.ID, ToJobID: m.ID, Condition: "success"},
	}
	if err := st.ReplaceWorkflowEdges(ctx, wf.ID, edges); err != nil {
		t.Fatal(err)
	}
	if err := st.SetJobsWorkflowFromEdges(ctx, wf.ID, edges); err != nil {
		t.Fatal(err)
	}

	eng := scheduler.New(st, nil, time.Second)
	if err := eng.TickOnce(ctx); err != nil {
		t.Fatal(err)
	}

	runs, err := st.ListRuns(ctx, store.RunFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("both roots and nothing else must be queued, got %d runs", len(runs))
	}
	var execID string
	for _, r := range runs {
		if r.JobID == m.ID {
			t.Fatal("merge is not a root and must wait for its parents")
		}
		if r.WorkflowExecutionID == nil {
			t.Fatal("root runs must carry the execution")
		}
		if r.Trigger != store.TriggerSchedule {
			t.Fatalf("trigger %s", r.Trigger)
		}
		if execID == "" {
			execID = r.WorkflowExecutionID.String()
		} else if execID != r.WorkflowExecutionID.String() {
			t.Fatal("both roots must share one execution")
		}
	}
	got, err := st.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) || got.LastRunAt == nil {
		t.Fatalf("schedule must advance: next=%v last=%v", got.NextRunAt, got.LastRunAt)
	}
	// A second tick fires nothing: the workflow is not due again.
	if err := eng.TickOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if runs, _ := st.ListRuns(ctx, store.RunFilter{Limit: 20}); len(runs) != 2 {
		t.Fatalf("no double firing, got %d", len(runs))
	}
}

func TestWorkflowScheduleValidation(t *testing.T) {
	bad := scheduler.Schedule{Type: "cron", CronExpr: "not a cron", Timezone: "UTC"}
	if err := scheduler.ValidateSchedule(bad); err == nil {
		t.Fatal("bad cron must be refused")
	}
	if err := scheduler.ValidateSchedule(scheduler.Schedule{Type: "manual", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := scheduler.InitialNextFor(scheduler.Schedule{Type: "manual", Timezone: "UTC", Enabled: true}, time.Now()); ok {
		t.Fatal("manual schedules have no next run")
	}
}
