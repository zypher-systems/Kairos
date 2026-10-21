package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/ha"
	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestOnlyLeaderEnqueues(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	lead := ha.New(st.Pool(), "leader", 15*time.Second, 5*time.Second)
	follow := ha.New(st.Pool(), "follower", 15*time.Second, 5*time.Second)
	if ok, err := lead.TryAcquire(ctx); err != nil || !ok {
		t.Fatalf("leader acquire: ok=%v err=%v", ok, err)
	}
	if ok, err := follow.TryAcquire(ctx); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("follower stole the lease")
	}

	past := time.Now().Add(-time.Minute)
	created, err := st.CreateJob(ctx, &store.Job{
		Name:              "ha-once",
		ScheduleType:      "interval",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		Enabled:           true,
		ConcurrencyPolicy: "allow",
	}, &past)
	if err != nil {
		t.Fatal(err)
	}

	engLead := scheduler.New(st, nil, time.Second)
	engFollow := scheduler.New(st, nil, time.Second)
	tick := func(isLeader bool, eng *scheduler.Engine) {
		if !isLeader {
			return
		}
		if err := eng.TickOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tick(lead.IsLeader(), engLead)
	tick(follow.IsLeader(), engFollow)

	runs, err := st.ListRuns(ctx, store.RunFilter{JobID: &created.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run from the leader, got %d", len(runs))
	}
}
