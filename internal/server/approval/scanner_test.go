package approval

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantApproval(t *testing.T) { t.Helper() }

func gateJob(name string, timeout int) *store.Job {
	return &store.Job{
		Name:                   name,
		ScheduleType:           "interval",
		IntervalSeconds:        3600,
		Timezone:               "UTC",
		TargetType:             "local",
		ExecMode:               "shell",
		ShellScript:            "true",
		TimeoutSeconds:         3600,
		ConcurrencyPolicy:      "allow",
		Enabled:                true,
		RequireApproval:        true,
		ApprovalTimeoutSeconds: timeout,
	}
}

func TestTimeoutSkipsAfterElapsed(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	job, err := st.CreateJob(ctx, gateJob("timeout-me", 1), nil)
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
	New(st, time.Hour).ScanAt(ctx, time.Now().Add(2*time.Second))
	got, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunSkipped || got.Error != "approval_timeout" {
		t.Fatalf("timeout %s %q", got.Status, got.Error)
	}
}

func TestTimeoutZeroWaitsForever(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantApproval(t)
	job, err := st.CreateJob(ctx, gateJob("wait-forever", 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	New(st, time.Hour).ScanAt(ctx, time.Now().Add(24*time.Hour))
	got, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunPendingApproval {
		t.Fatalf("timeout 0 must stay pending, got %s", got.Status)
	}
}
