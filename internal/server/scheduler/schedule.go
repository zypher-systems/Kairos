// Package scheduler computes next-run times and drives the due-job engine.
package scheduler

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// Schedule values. TypeManual means no schedule of its own: the job or
// workflow runs by hand, from a file trigger, or as part of a workflow.
const (
	TypeCron     = "cron"
	TypeInterval = "interval"
	TypeOneoff   = "oneoff"
	TypeManual   = "manual"
)

// Schedule is the part of a job or workflow the scheduler reads.
type Schedule struct {
	Type            string
	CronExpr        string
	IntervalSeconds int
	OneoffAt        *time.Time
	Timezone        string
	NextRunAt       *time.Time
	Enabled         bool
}

// JobSchedule extracts a job's schedule.
func JobSchedule(j *store.Job) Schedule {
	return Schedule{
		Type: j.ScheduleType, CronExpr: j.CronExpr, IntervalSeconds: j.IntervalSeconds,
		OneoffAt: j.OneoffAt, Timezone: j.Timezone, NextRunAt: j.NextRunAt, Enabled: j.Enabled,
	}
}

// WorkflowSchedule extracts a workflow's schedule.
func WorkflowSchedule(w *store.Workflow) Schedule {
	return Schedule{
		Type: w.ScheduleType, CronExpr: w.CronExpr, IntervalSeconds: w.IntervalSeconds,
		OneoffAt: w.OneoffAt, Timezone: w.Timezone, NextRunAt: w.NextRunAt, Enabled: w.Enabled,
	}
}

// IsManual reports a schedule type with no timed firing.
func IsManual(scheduleType string) bool {
	return scheduleType == TypeManual || scheduleType == ""
}

var caches = sync.Map{} // cron.expr -> cron.Schedule (immutable, parse once)

// ParseCronExpr validates a 5-field cron expression and caches its schedule.
func ParseCronExpr(expr string) (cron.Schedule, error) {
	if v, ok := caches.Load(expr); ok {
		return v.(cron.Schedule), nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	s, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	caches.Store(expr, s)
	return s, nil
}

// ValidateJob checks schedule-specific fields and timezone.
func ValidateJob(j *store.Job) error {
	return ValidateSchedule(JobSchedule(j))
}

// ValidateSchedule checks schedule-specific fields and timezone.
func ValidateSchedule(sc Schedule) error {
	if _, err := time.LoadLocation(sc.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q", sc.Timezone)
	}
	switch sc.Type {
	case TypeCron:
		if sc.CronExpr == "" {
			return errors.New("cron_expr is required for cron schedules")
		}
		if _, err := ParseCronExpr(sc.CronExpr); err != nil {
			return err
		}
	case TypeInterval:
		if sc.IntervalSeconds < 5 {
			return errors.New("interval_seconds must be >= 5")
		}
	case TypeOneoff:
		if sc.OneoffAt == nil {
			return errors.New("oneoff_at is required for oneoff schedules")
		}
	case TypeManual, "":
	default:
		return errors.New("unsupported schedule_type")
	}
	return nil
}

// NextRun computes the next run time strictly after `after` for a job, using
// the job's stored next_run_at as the anchor where applicable. Missed spans
// (server downtime) coalesce into a single catch-up run.
// Returns ok=false when the schedule has no next run (e.g., oneoff already fired).
func NextRun(j *store.Job, after time.Time) (time.Time, bool, error) {
	return NextFor(JobSchedule(j), after)
}

// NextFor is NextRun for any schedule.
func NextFor(sc Schedule, after time.Time) (time.Time, bool, error) {
	if IsManual(sc.Type) {
		return time.Time{}, false, nil
	}
	loc, err := time.LoadLocation(sc.Timezone)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("invalid timezone %q", sc.Timezone)
	}
	base := after
	if sc.NextRunAt != nil && sc.NextRunAt.Before(after) {
		base = *sc.NextRunAt
	}
	j := sc

	switch j.Type {
	case TypeCron:
		s, err := ParseCronExpr(j.CronExpr)
		if err != nil {
			return time.Time{}, false, err
		}
		next := s.Next(base.In(loc))
		guard := 0
		for !next.After(after) && guard < 100000 {
			next = s.Next(next)
			guard++
		}
		if guard >= 100000 {
			return time.Time{}, false, errors.New("cron schedule fails to advance")
		}
		return next, true, nil
	case TypeInterval:
		d := time.Duration(j.IntervalSeconds) * time.Second
		next := base.Add(d)
		for !next.After(after) {
			next = next.Add(d)
		}
		return next, true, nil
	case TypeOneoff:
		one := *j.OneoffAt
		if one.After(after) {
			return one, true, nil
		}
		if j.NextRunAt != nil && j.NextRunAt.After(after) {
			return *j.NextRunAt, true, nil
		}
		return time.Time{}, false, nil
	default:
		return time.Time{}, false, fmt.Errorf("unsupported schedule_type %q", j.Type)
	}
}

// InitialNextRun computes the very first next_run_at when a job is created
// or updated (used instead of the stored anchor).
func InitialNextRun(j *store.Job, now time.Time) (time.Time, bool, error) {
	return InitialNextFor(JobSchedule(j), now)
}

// InitialNextFor is InitialNextRun for any schedule.
func InitialNextFor(sc Schedule, now time.Time) (time.Time, bool, error) {
	if !sc.Enabled || IsManual(sc.Type) {
		return time.Time{}, false, nil
	}
	na := now
	if sc.NextRunAt != nil {
		na = *sc.NextRunAt
	}
	return NextFor(sc, na)
}
