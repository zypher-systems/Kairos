package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Event kinds for SLA alerts (notify only).
const (
	EventRunMissed   = "run_missed"
	EventRunLate     = "run_late"
	EventRunPending  = "run_pending"
	EventRunRejected = "run_rejected"
)

// SLAMiss is a scheduled occurrence that never started within grace.
type SLAMiss struct {
	Job          Job
	Run          *Run
	OccurrenceAt time.Time
}

// SLALate is a run whose duration exceeded the job's late SLA.
type SLALate struct {
	Job Job
	Run Run
}

// DueMissedJobs are enabled jobs whose next_run_at is past grace with no alert yet.
func (s *Store) DueMissedJobs(ctx context.Context, now time.Time) ([]SLAMiss, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobCols+`
		FROM jobs
		WHERE enabled
		  AND sla_missed_grace_seconds > 0
		  AND next_run_at IS NOT NULL
		  AND next_run_at + make_interval(secs => sla_missed_grace_seconds) < $1
		  AND (sla_missed_alerted_for IS NULL OR sla_missed_alerted_for IS DISTINCT FROM next_run_at)`,
		now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, err
	}
	out := make([]SLAMiss, 0, len(jobs))
	for _, j := range jobs {
		occ := *j.NextRunAt
		out = append(out, SLAMiss{Job: j, OccurrenceAt: occ})
	}
	return out, nil
}

// DueMissedQueuedRuns are schedule-triggered runs that never left queued/claimed.
func (s *Store) DueMissedQueuedRuns(ctx context.Context, now time.Time) ([]SLAMiss, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+runColsPrefixed+`
		FROM runs r
		JOIN jobs j ON j.id = r.job_id
		WHERE j.sla_missed_grace_seconds > 0
		  AND r.trigger = $2
		  AND r.started_at IS NULL
		  AND r.status IN ($3, $4)
		  AND r.sla_missed_alerted_at IS NULL
		  AND r.schedule_time + make_interval(secs => j.sla_missed_grace_seconds) < $1`,
		now, TriggerSchedule, RunQueued, RunClaimed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		r, err := ScanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SLAMiss, 0, len(runs))
	for i := range runs {
		j, err := s.GetJob(ctx, runs[i].JobID)
		if err != nil {
			return nil, err
		}
		out = append(out, SLAMiss{Job: j, Run: &runs[i], OccurrenceAt: runs[i].ScheduleTime})
	}
	return out, nil
}

// DueLateRuns are runs whose elapsed time meets the job's late SLA (notify only).
func (s *Store) DueLateRuns(ctx context.Context, now time.Time) ([]SLALate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+runColsPrefixed+`
		FROM runs r
		JOIN jobs j ON j.id = r.job_id
		WHERE j.sla_late_seconds > 0
		  AND r.started_at IS NOT NULL
		  AND r.sla_late_alerted_at IS NULL
		  AND EXTRACT(epoch FROM (COALESCE(r.finished_at, $1) - r.started_at)) >= j.sla_late_seconds`,
		now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		r, err := ScanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SLALate, 0, len(runs))
	for i := range runs {
		j, err := s.GetJob(ctx, runs[i].JobID)
		if err != nil {
			return nil, err
		}
		out = append(out, SLALate{Job: j, Run: runs[i]})
	}
	return out, nil
}

// MarkJobMissedAlerted records that this next_run_at already produced a miss alert.
func (s *Store) MarkJobMissedAlerted(ctx context.Context, jobID uuid.UUID, occurrence time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET sla_missed_alerted_for = $2 WHERE id = $1`, jobID, occurrence)
	return err
}

// MarkRunMissedAlerted records a queued-not-started miss so it is not re-fired.
func (s *Store) MarkRunMissedAlerted(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET sla_missed_alerted_at = now() WHERE id = $1`, runID)
	return err
}

// MarkRunLateAlerted records a late-run notify; the run is not canceled.
func (s *Store) MarkRunLateAlerted(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET sla_late_alerted_at = now() WHERE id = $1`, runID)
	return err
}

// EnqueueSLANotify writes outbox rows for a missed or late event.
func (s *Store) EnqueueSLANotify(ctx context.Context, kind string, job Job, run *Run) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := enqueueEventNotifyTx(ctx, tx, kind, job, run); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
