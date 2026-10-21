package scheduler

import (
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func TestNextRunCron(t *testing.T) {
	j := &store.Job{
		ScheduleType: TypeCron,
		CronExpr:     "*/5 * * * *",
		Timezone:     "UTC",
	}
	after := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	next, ok, err := NextRun(j, after)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := time.Date(2026, 9, 4, 0, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestNextRunCronCoalescesDowntime(t *testing.T) {
	past := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	j := &store.Job{
		ScheduleType: TypeCron,
		CronExpr:     "* * * * *",
		Timezone:     "UTC",
		NextRunAt:    &past,
	}
	now := time.Now()
	next, ok, err := NextRun(j, now)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if next.Before(now) {
		t.Fatalf("catch-up run in past: %v (now=%v)", next, now)
	}
	if next.After(now.Add(2 * time.Minute)) {
		t.Fatalf("catch-up run too far: %v", next)
	}
}

func TestNextRunCronTimezone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata missing")
	}
	j := &store.Job{
		ScheduleType: TypeCron,
		CronExpr:     "0 9 * * *",
		Timezone:     "America/New_York",
	}
	after := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC) // 20:00 ET previous day
	next, ok, err := NextRun(j, after)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := time.Date(2026, 9, 4, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestNextRunInterval(t *testing.T) {
	now := time.Now().UTC()
	j := &store.Job{
		ScheduleType:    TypeInterval,
		IntervalSeconds: 300,
		Timezone:        "UTC",
	}
	next, ok, err := NextRun(j, now)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	delta := next.Sub(now)
	if delta != 5*time.Minute {
		t.Fatalf("delta=%v want 5m", delta)
	}
}

func TestNextRunIntervalCoalescesMissed(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	j := &store.Job{
		ScheduleType:    TypeInterval,
		IntervalSeconds: 60,
		Timezone:        "UTC",
		NextRunAt:       &past,
	}
	next, ok, err := NextRun(j, now)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if next.Before(now) {
		t.Fatalf("next in past: %v", next)
	}
	if next.Sub(now) > time.Minute {
		t.Fatalf("expected catch-up within 60s, got %v", next.Sub(now))
	}
}

func TestNextRunOneoff(t *testing.T) {
	future := time.Now().UTC().Add(10 * time.Minute)
	j := &store.Job{
		ScheduleType: TypeOneoff,
		OneoffAt:     &future,
		Timezone:     "UTC",
	}
	next, ok, err := NextRun(j, time.Now())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !next.Equal(future) {
		t.Fatalf("next=%v want=%v", next, future)
	}

	past := time.Now().UTC().Add(-time.Hour)
	j.OneoffAt = &past
	j.NextRunAt = &past
	_, ok, err = NextRun(j, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("oneoff in past must report no next run")
	}
}

func TestValidateJob(t *testing.T) {
	bad := &store.Job{ScheduleType: TypeCron, CronExpr: "not a cron", Timezone: "UTC"}
	if err := ValidateJob(bad); err == nil {
		t.Fatal("expected invalid cron error")
	}
	badTZ := &store.Job{ScheduleType: TypeCron, CronExpr: "* * * * *", Timezone: "Mars/Olympus"}
	if err := ValidateJob(badTZ); err == nil {
		t.Fatal("expected invalid timezone error")
	}
	fast := &store.Job{ScheduleType: TypeInterval, IntervalSeconds: 1, Timezone: "UTC"}
	if err := ValidateJob(fast); err == nil {
		t.Fatal("expected interval minimum error")
	}
	good := &store.Job{ScheduleType: TypeCron, CronExpr: "@daily", Timezone: "UTC"}
	if err := ValidateJob(good); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
