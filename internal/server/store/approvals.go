package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotPending is returned when approve/reject/timeout CAS misses.
var ErrNotPending = errors.New("run is not pending approval")

// ApprovalTimeout is a pending run whose job timeout has elapsed.
type ApprovalTimeout struct {
	Job Job
	Run Run
}

func casPending(ctx context.Context, tx pgx.Tx, id uuid.UUID, setSQL string, args ...any) (Run, error) {
	params := append([]any{id, RunPendingApproval}, args...)
	row := tx.QueryRow(ctx, setSQL, params...)
	run, err := scanRun(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, ErrNotPending
		}
		return Run{}, err
	}
	return run, nil
}

// ApproveRun CAS-moves pending_approval → queued.
func (s *Store) ApproveRun(ctx context.Context, id, actor uuid.UUID) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	run, err := casPending(ctx, tx, id, `
		UPDATE runs SET status = 'queued', approved_by = $3, approved_at = now()
		WHERE id = $1 AND status = $2
		RETURNING `+RunCols, actor)
	if err != nil {
		if errors.Is(err, ErrNotPending) {
			if _, getErr := s.GetRun(ctx, id); errors.Is(getErr, pgx.ErrNoRows) {
				return Run{}, pgx.ErrNoRows
			}
		}
		return Run{}, err
	}
	return run, tx.Commit(ctx)
}

// RejectRun CAS-moves pending_approval → skipped with error rejected.
func (s *Store) RejectRun(ctx context.Context, id, actor uuid.UUID) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	run, err := casPending(ctx, tx, id, `
		UPDATE runs SET status = 'skipped', error = 'rejected', rejected_by = $3, finished_at = now()
		WHERE id = $1 AND status = $2
		RETURNING `+RunCols, actor)
	if err != nil {
		if errors.Is(err, ErrNotPending) {
			if _, getErr := s.GetRun(ctx, id); errors.Is(getErr, pgx.ErrNoRows) {
				return Run{}, pgx.ErrNoRows
			}
		}
		return Run{}, err
	}
	job, err := s.GetJob(ctx, run.JobID)
	if err != nil {
		return Run{}, err
	}
	if err := enqueueEventNotifyTx(ctx, tx, EventRunRejected, job, &run); err != nil {
		return Run{}, err
	}
	return run, tx.Commit(ctx)
}

// SkipApprovalTimeout CAS-moves pending_approval → skipped with error approval_timeout.
func (s *Store) SkipApprovalTimeout(ctx context.Context, id uuid.UUID) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	run, err := casPending(ctx, tx, id, `
		UPDATE runs SET status = 'skipped', error = 'approval_timeout', finished_at = now()
		WHERE id = $1 AND status = $2
		RETURNING `+RunCols)
	if err != nil {
		return Run{}, err
	}
	job, err := s.GetJob(ctx, run.JobID)
	if err != nil {
		return Run{}, err
	}
	if err := enqueueEventNotifyTx(ctx, tx, EventRunRejected, job, &run); err != nil {
		return Run{}, err
	}
	return run, tx.Commit(ctx)
}

// DueApprovalTimeouts lists pending runs whose per-job timeout has elapsed.
// Timeout 0 waits forever and is never returned here.
func (s *Store) DueApprovalTimeouts(ctx context.Context, now time.Time) ([]ApprovalTimeout, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+runColsPrefixed+`
		FROM runs r
		JOIN jobs j ON j.id = r.job_id
		WHERE r.status = $2
		  AND j.approval_timeout_seconds > 0
		  AND r.queued_at + make_interval(secs => j.approval_timeout_seconds) < $1`,
		now, RunPendingApproval)
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
	out := make([]ApprovalTimeout, 0, len(runs))
	for i := range runs {
		j, err := s.GetJob(ctx, runs[i].JobID)
		if err != nil {
			return nil, err
		}
		out = append(out, ApprovalTimeout{Job: j, Run: runs[i]})
	}
	return out, nil
}
