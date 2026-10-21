package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantCalendars(t *testing.T) { t.Helper() }

func TestTickSkipsCalendarDate(t *testing.T) {
	grantCalendars(t)
	st := testutil.Store(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	today := past.UTC().Format("2006-01-02")
	cal := &store.Calendar{Name: "holidays", Dates: []string{today}}
	if err := st.CreateCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	j := &store.Job{
		Name:              "skip-me",
		ScheduleType:      "interval",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		Enabled:           true,
		ConcurrencyPolicy: "allow",
		CalendarID:        &cal.ID,
	}
	created, err := st.CreateJob(ctx, j, &past)
	if err != nil {
		t.Fatal(err)
	}
	eng := scheduler.New(st, nil, time.Second)
	if err := eng.TickOnce(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ListRuns(ctx, store.RunFilter{JobID: &created.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != store.RunSkipped || runs[0].Error != "calendar" {
		t.Fatalf("skip run %+v", runs)
	}
	reloaded, err := st.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.NextRunAt == nil || !reloaded.NextRunAt.After(past) {
		t.Fatalf("next_run_at should advance, got %v", reloaded.NextRunAt)
	}
}

func TestTickFiresWhenDateNotOnCalendar(t *testing.T) {
	grantCalendars(t)
	st := testutil.Store(t)
	ctx := context.Background()
	cal := &store.Calendar{Name: "holidays", Dates: []string{"1999-01-01"}}
	if err := st.CreateCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	j := &store.Job{
		Name:              "run-me",
		ScheduleType:      "interval",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		Enabled:           true,
		ConcurrencyPolicy: "allow",
		CalendarID:        &cal.ID,
	}
	past := time.Now().Add(-time.Minute)
	created, err := st.CreateJob(ctx, j, &past)
	if err != nil {
		t.Fatal(err)
	}
	eng := scheduler.New(st, nil, time.Second)
	if err := eng.TickOnce(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ListRuns(ctx, store.RunFilter{JobID: &created.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != store.RunQueued {
		t.Fatalf("want queued, got %+v", runs)
	}
}

func TestTickOneoffIgnoresCalendar(t *testing.T) {
	grantCalendars(t)
	st := testutil.Store(t)
	ctx := context.Background()
	today := time.Now().Add(-time.Minute).UTC().Format("2006-01-02")
	cal := &store.Calendar{Name: "holidays", Dates: []string{today}}
	if err := st.CreateCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	j := &store.Job{
		Name:              "once",
		ScheduleType:      "oneoff",
		OneoffAt:          &at,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		Enabled:           true,
		ConcurrencyPolicy: "allow",
		CalendarID:        &cal.ID,
	}
	created, err := st.CreateJob(ctx, j, &at)
	if err != nil {
		t.Fatal(err)
	}
	eng := scheduler.New(st, nil, time.Second)
	if err := eng.TickOnce(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ListRuns(ctx, store.RunFilter{JobID: &created.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != store.RunQueued {
		t.Fatalf("one-off should fire, got %+v", runs)
	}
}

func TestManualRunIgnoresCalendar(t *testing.T) {
	grantCalendars(t)
	st := testutil.Store(t)
	ctx := context.Background()
	today := time.Now().Add(-time.Minute).UTC().Format("2006-01-02")
	cal := &store.Calendar{Name: "holidays", Dates: []string{today}}
	if err := st.CreateCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	j := &store.Job{
		Name:              "manual",
		ScheduleType:      "interval",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		Enabled:           true,
		ConcurrencyPolicy: "allow",
		CalendarID:        &cal.ID,
	}
	created, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.EnqueueWithPolicy(ctx, created, store.TriggerManual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != store.RunQueued {
		t.Fatalf("manual %s", run.Status)
	}
}
