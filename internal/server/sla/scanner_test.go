package sla

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantSLA(t *testing.T, features ...string) { t.Helper() }

func slaJob(name string) *store.Job {
	return &store.Job{
		Name:              name,
		ScheduleType:      "interval",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		TimeoutSeconds:    3600,
		ConcurrencyPolicy: "allow",
		Enabled:           true,
	}
}

func TestMissedRunFiresOnceAfterGrace(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantSLA(t, "missed_run")
	ch := &store.NotificationChannel{
		Name:            "ops",
		Type:            "webhook",
		Config:          map[string]string{"url": "http://127.0.0.1:9/hook"},
		Enabled:         true,
		NotifyOnMissed:  true,
		NotifyOnFailure: true,
	}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	j := slaJob("miss-me")
	j.SLAMissedGraceSeconds = 30
	due := time.Now().Add(-time.Minute)
	job, err := st.CreateJob(ctx, j, &due)
	if err != nil {
		t.Fatal(err)
	}
	sc := New(st, time.Hour)
	sc.ScanAt(ctx, time.Now())
	rows, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 missed notify, got %d", len(rows))
	}
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != store.EventRunMissed {
		t.Fatalf("event %v", payload["event"])
	}
	if payload["job_id"] != job.ID.String() {
		t.Fatalf("job_id %v", payload["job_id"])
	}
	for _, r := range rows {
		_ = st.CompleteOutbox(ctx, r.ID)
	}
	sc.ScanAt(ctx, time.Now())
	again, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("duplicate missed notify: %d", len(again))
	}
}

func TestLateRunNotifiesWithoutCancel(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	grantSLA(t, "late_run")
	ch := &store.NotificationChannel{
		Name:         "ops",
		Type:         "webhook",
		Config:       map[string]string{"url": "http://127.0.0.1:9/hook"},
		Enabled:      true,
		NotifyOnLate: true,
	}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	j := slaJob("slow")
	j.SLALateSeconds = 10
	job, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, job, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `UPDATE runs SET status='running', started_at=now() WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	sc := New(st, time.Hour)
	sc.ScanAt(ctx, time.Now().Add(15*time.Second))
	rows, err := st.ClaimOutboxDue(ctx, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 late notify, got %d", len(rows))
	}
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != store.EventRunLate {
		t.Fatalf("event %v", payload["event"])
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunRunning {
		t.Fatalf("late-run must not cancel, status %s", got.Status)
	}
}
