package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/metrics"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

// Engine wakes on a tick, locks due jobs, enqueues their runs, and advances
// their next_run_at — all in one transaction per batch.
type Engine struct {
	store *store.Store
	hub   *ws.Hub
	tick  time.Duration
	batch int
}

// New builds a scheduler engine.
func New(st *store.Store, hub *ws.Hub, tick time.Duration) *Engine {
	if tick <= 0 {
		tick = time.Second
	}
	return &Engine{store: st, hub: hub, tick: tick, batch: 250}
}

// lastTick is the wall time of the last successful tick on this replica;
// zero when it is not the leader. /healthz reads it.
var lastTick atomic.Value // time.Time

// LastTick returns when the scheduler last completed a tick here.
func LastTick() time.Time {
	t, _ := lastTick.Load().(time.Time)
	return t
}

// Run blocks until ctx is done, processing due jobs on each tick.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(e.tick)
	defer t.Stop()
	slog.Info("scheduler started", "tick", e.tick.String())
	lastTick.Store(time.Now())
	defer func() {
		lastTick.Store(time.Time{})
		metrics.SchedulerLastOK.Set(0)
	}()
	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler stopped")
			return
		case <-t.C:
			if err := e.processTick(ctx); err != nil {
				metrics.SchedulerTicks.Inc("error")
				slog.Warn("scheduler tick failed", "err", err)
				continue
			}
			metrics.SchedulerTicks.Inc("ok")
			now := time.Now()
			lastTick.Store(now)
			metrics.SchedulerLastOK.Set(float64(now.Unix()))
		}
	}
}

type queuedRun struct {
	run  store.Run
	name string
}

func (e *Engine) processTick(ctx context.Context) error {
	now := time.Now()
	tx, err := e.store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	jobs, err := e.store.DueJobsTx(ctx, tx, now, e.batch)
	if err != nil {
		return err
	}
	var queued []queuedRun
	for i := range jobs {
		job := &jobs[i]
		// Each job gets a savepoint: one job whose enqueue fails rolls back
		// alone, and the rest of the batch still fires. Without it the whole
		// batch would retry every tick and nothing would be scheduled.
		if _, err := tx.Exec(ctx, `SAVEPOINT job`); err != nil {
			return err
		}
		run, err := e.scheduleOne(ctx, tx, job, now)
		if err != nil {
			slog.Error("scheduling job failed; it will be retried next tick", "job", job.ID, "name", job.Name, "err", err)
			if _, rbErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT job`); rbErr != nil {
				return rbErr
			}
			continue
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT job`); err != nil {
			return err
		}
		if run != nil {
			queued = append(queued, queuedRun{run: *run, name: job.Name})
		}
	}

	// Workflows with their own schedule: one execution, every root job.
	wfs, err := e.store.DueWorkflowsTx(ctx, tx, now, e.batch)
	if err != nil {
		return err
	}
	for i := range wfs {
		wf := &wfs[i]
		if _, err := tx.Exec(ctx, `SAVEPOINT wf`); err != nil {
			return err
		}
		runs, err := e.scheduleWorkflow(ctx, tx, wf, now)
		if err != nil {
			slog.Error("scheduling workflow failed; it will be retried next tick", "workflow", wf.ID, "name", wf.Name, "err", err)
			if _, rbErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT wf`); rbErr != nil {
				return rbErr
			}
			continue
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT wf`); err != nil {
			return err
		}
		for _, r := range runs {
			queued = append(queued, queuedRun{run: r, name: wf.Name})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	metrics.SchedulerEnqueue.Add(int64(len(queued)))
	if e.hub != nil {
		for _, q := range queued {
			e.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
				"id":          q.run.ID,
				"job_id":      q.run.JobID,
				"job_name":    q.name,
				"status":      q.run.Status,
				"target_type": q.run.TargetType,
				"trigger":     q.run.Trigger,
			})
		}
	}
	slog.Debug("scheduler tick", "due", len(jobs))
	return nil
}

// TickOnce processes one due batch. Tests call this; Run loops it.
func (e *Engine) TickOnce(ctx context.Context) error {
	return e.processTick(ctx)
}

// scheduleOne fires one due job inside the batch transaction. A job whose
// schedule cannot be advanced (a timezone that no longer loads, a cron
// expression that never fires again) is disabled with a log line instead
// of being silently left with no next run.
func (e *Engine) scheduleOne(ctx context.Context, tx pgx.Tx, job *store.Job, now time.Time) (*store.Run, error) {
	var nextPtr *time.Time
	next, ok, err := NextRun(job, now)
	if err != nil {
		slog.Error("cannot compute next run; disabling job", "job", job.ID, "name", job.Name, "err", err)
		if _, err := tx.Exec(ctx, `UPDATE jobs SET enabled = false, next_run_at = NULL, updated_at = now() WHERE id = $1`, job.ID); err != nil {
			return nil, err
		}
		return nil, nil
	} else if ok {
		nextPtr = &next
	}
	if skip, err := e.calendarSkip(ctx, job, now); err != nil {
		return nil, err
	} else if skip {
		run, err := e.store.RecordSkippedReasonTx(ctx, tx, *job, "calendar")
		if err != nil {
			return nil, err
		}
		if err := e.store.SetJobScheduleStateTx(ctx, tx, job.ID, nextPtr, now); err != nil {
			return nil, err
		}
		slog.Debug("calendar skip", "job", job.ID, "name", job.Name, "next", nextPtr)
		return &run, nil
	}
	run, err := e.store.EnqueueWithPolicyTx(ctx, tx, *job, store.TriggerSchedule, now)
	if err != nil {
		return nil, err
	}
	if err := e.store.SetJobScheduleStateTx(ctx, tx, job.ID, nextPtr, now); err != nil {
		return nil, err
	}
	slog.Debug("scheduled run", "job", job.ID, "name", job.Name, "next", nextPtr, "status", run.Status)
	return &run, nil
}

func (e *Engine) calendarSkip(ctx context.Context, job *store.Job, now time.Time) (bool, error) {
	return e.calendarSkipFor(ctx, job.CalendarID, job.ScheduleType, job.Timezone, job.NextRunAt, now)
}

// calendarSkipFor reports whether a skip calendar covers the occurrence
// that is firing now.
func (e *Engine) calendarSkipFor(ctx context.Context, calendarID *uuid.UUID, scheduleType, timezone string, nextRunAt *time.Time, now time.Time) (bool, error) {
	if calendarID == nil {
		return false, nil
	}
	cal, err := e.store.GetCalendar(ctx, *calendarID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	fireAt := now
	if nextRunAt != nil {
		fireAt = *nextRunAt
	}
	return SkipFire(scheduleType, timezone, cal.Dates, fireAt), nil
}

// scheduleWorkflow fires one due workflow: advance its schedule, honour a
// skip calendar, and start an execution with every root job.
func (e *Engine) scheduleWorkflow(ctx context.Context, tx pgx.Tx, wf *store.Workflow, now time.Time) ([]store.Run, error) {
	var nextPtr *time.Time
	next, ok, err := NextFor(WorkflowSchedule(wf), now)
	if err != nil {
		slog.Error("cannot compute next run; disabling workflow", "workflow", wf.ID, "name", wf.Name, "err", err)
		return nil, e.store.DisableWorkflowTx(ctx, tx, wf.ID)
	} else if ok {
		nextPtr = &next
	}
	if skip, err := e.calendarSkipFor(ctx, wf.CalendarID, wf.ScheduleType, wf.Timezone, wf.NextRunAt, now); err != nil {
		return nil, err
	} else if skip {
		slog.Info("calendar skip", "workflow", wf.ID, "name", wf.Name, "next", nextPtr)
		return nil, e.store.SetWorkflowScheduleStateTx(ctx, tx, wf.ID, nextPtr, now)
	}
	_, runs, err := e.store.StartWorkflowTx(ctx, tx, *wf, store.TriggerSchedule, now, nil)
	if err != nil {
		return nil, err
	}
	if err := e.store.SetWorkflowScheduleStateTx(ctx, tx, wf.ID, nextPtr, now); err != nil {
		return nil, err
	}
	slog.Debug("scheduled workflow", "workflow", wf.ID, "name", wf.Name, "roots", len(runs), "next", nextPtr)
	return runs, nil
}
