package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QueueClaim is a leased assignment of a run to a specific worker.
type QueueClaim struct {
	ID             uuid.UUID `json:"id"`
	RunID          uuid.UUID `json:"run_id"`
	TargetType     string    `json:"target_type"`
	TargetID       string    `json:"target_id"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	ClaimVersion   int       `json:"claim_version"`
	ClaimedAt      time.Time `json:"claimed_at"`
}

// ClaimNextForAgent atomically assigns the oldest queued run that routes to
// this agent (direct id or label selector), leases it, and marks it claimed.
// Returns (claim, runID, false, nil) when no work is available.
func (s *Store) ClaimNextForAgent(ctx context.Context, agentID uuid.UUID, lease time.Duration) (QueueClaim, uuid.UUID, bool, error) {
	var c QueueClaim
	var runID uuid.UUID

	row := s.pool.QueryRow(ctx, `
		WITH next AS (
			SELECT r.id
			FROM runs r
			LEFT JOIN queue_claims qc ON qc.run_id = r.id
			WHERE r.status = 'queued'
			  AND r.not_before <= now()
			  AND qc.run_id IS NULL
			  AND (
			    (r.target_type = 'agent' AND r.target_id = $1::text)
			    OR
			    (r.target_type = 'selector' AND EXISTS (
			      SELECT 1 FROM jobs j, agents a
			      WHERE j.id = r.job_id AND a.id = $1::uuid AND a.labels @> j.target_selector::jsonb
			    ))
			  )
			ORDER BY r.queued_at ASC
			LIMIT 1
			FOR UPDATE OF r SKIP LOCKED
		),
		ins AS (
			INSERT INTO queue_claims (run_id, target_type, target_id, lease_expires_at, claim_version)
			SELECT n.id, 'agent', $1::text, now() + $2::interval, r.claim_epoch
			FROM next n JOIN runs r ON r.id = n.id
			ON CONFLICT (run_id) DO NOTHING
			RETURNING id, run_id, target_type, target_id, lease_expires_at, claim_version, claimed_at
		),
		upd AS (
			-- An agent starts the run the moment the claim response lands,
			-- so the claim is also the start: started_at feeds late-run
			-- SLAs, durations and the running gauge.
			UPDATE runs SET status = 'running', claimed_at = now(), started_at = now(), target_id = $1::text
			WHERE id IN (SELECT run_id FROM ins)
		)
		SELECT id, run_id, target_type, target_id, lease_expires_at, claim_version, claimed_at
		FROM ins`,
		agentID.String(), lease.String(),
	)
	err := row.Scan(&c.ID, &c.RunID, &c.TargetType, &c.TargetID, &c.LeaseExpiresAt, &c.ClaimVersion, &c.ClaimedAt)
	if err == pgx.ErrNoRows {
		return QueueClaim{}, uuid.Nil, false, nil
	}
	if err != nil {
		return QueueClaim{}, uuid.Nil, false, err
	}
	runID = c.RunID
	return c, runID, true, nil
}

// ExtendLease pushes the lease out for a running execution owned by targetID.
// epoch is the claim_version the worker was handed; a worker that was
// superseded (lease expired, run requeued and reclaimed) gets ErrStaleClaim
// even when the new holder shares its target id, as two replicas running
// 'local' jobs do. A negative epoch (EpochUnknown) skips that check for
// agents from before 1.15, which renew without one.
func (s *Store) ExtendLease(ctx context.Context, runID uuid.UUID, targetID string, epoch int, lease time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE queue_claims SET lease_expires_at = now() + $2::interval
		WHERE run_id = $1 AND target_id = $3 AND ($4 < 0 OR claim_version = $4)`,
		runID, lease.String(), targetID, epoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleClaim
	}
	return nil
}

// EpochUnknown is the claim epoch of a worker that did not send one.
const EpochUnknown = -1

// MarkRunRunning moves a dispatcher-claimed run to running. It reports false
// when the run is no longer claimed (canceled between claim and start), in
// which case the caller must not execute it.
func (s *Store) MarkRunRunning(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs SET status = 'running', started_at = now()
		WHERE id = $1 AND status = 'claimed'`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ReleaseClaim hands a claimed or running run back to the queue: the worker
// is shutting down and did not finish. The epoch is bumped so a late result
// from the releasing worker cannot land, and the run keeps its attempt.
func (s *Store) ReleaseClaim(ctx context.Context, runID uuid.UUID, targetID string, epoch int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		DELETE FROM queue_claims
		WHERE run_id = $1 AND target_id = $2 AND ($3 < 0 OR claim_version = $3)`,
		runID, targetID, epoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleClaim
	}
	if _, err := tx.Exec(ctx, `
		UPDATE runs SET status = 'queued', claimed_at = NULL, started_at = NULL,
		                claim_epoch = claim_epoch + 1
		WHERE id = $1 AND status IN ('claimed', 'running')`, runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CompleteResult is what CompleteClaim did in its transaction.
type CompleteResult struct {
	// Run is the finished run as stored (canceled wins over the reported status).
	Run Run
	// Downstream are workflow children enqueued by this completion.
	Downstream []Run
	// Retry is the next attempt, enqueued when the run failed and the job
	// allows another try. Workflow fan-out and failure notifications wait
	// for the final attempt.
	Retry *Run
}

// RunClaimedBy reports whether targetID currently holds the claim for runID.
func (s *Store) RunClaimedBy(ctx context.Context, runID uuid.UUID, targetID string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM queue_claims WHERE run_id = $1 AND target_id = $2`,
		runID, targetID).Scan(&n)
	return n > 0, err
}

// CompleteClaim finishes a run only if the caller still holds the matching
// claim (target + epoch). A superseded worker after lease requeue is a no-op
// that returns ErrStaleClaim. A run canceled while executing keeps canceled.
// The retry, downstream workflow jobs and notification outbox rows are
// written in the same transaction so a crash cannot lose any of them.
func (s *Store) CompleteClaim(ctx context.Context, runID uuid.UUID, targetID string, epoch int, status string, exitCode *int, errMsg string) (CompleteResult, error) {
	var res CompleteResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		DELETE FROM queue_claims
		WHERE run_id = $1 AND target_id = $2 AND claim_version = $3`,
		runID, targetID, epoch)
	if err != nil {
		return res, err
	}
	if tag.RowsAffected() == 0 {
		return res, ErrStaleClaim
	}
	if _, err := tx.Exec(ctx, `
		UPDATE runs SET status = $2, exit_code = $3, error = $4, finished_at = now()
		WHERE id = $1 AND status <> $5`, runID, status, exitCode, errMsg, RunCanceled); err != nil {
		return res, err
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+RunCols+` FROM runs WHERE id = $1`, runID))
	if err != nil {
		return res, err
	}
	res.Run = run
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = $1`, run.JobID))
	if err != nil {
		return res, err
	}
	_, _ = tx.Exec(ctx, `DELETE FROM run_cancel_signals WHERE run_id = $1`, runID)

	// A failed attempt with tries left is not final: queue the next attempt
	// here, with its backoff, and hold fan-out and notifications until the
	// last attempt settles.
	if run.Status == RunFailed && retryAllowed(job, run) {
		notBefore := time.Now().Add(RetryBackoff(job, run.Attempt))
		retry, err := s.EnqueueRunTxExec(ctx, tx, job, TriggerRetry, run.ScheduleTime, notBefore, run.Attempt+1, run.WorkflowExecutionID)
		if err != nil {
			return res, err
		}
		if err := tx.Commit(ctx); err != nil {
			return res, err
		}
		res.Retry = &retry
		return res, nil
	}

	downstream, err := s.fanoutWorkflowTx(ctx, tx, run, job)
	if err != nil {
		return res, err
	}
	if err := s.enqueueNotifyOutboxTx(ctx, tx, run, job); err != nil {
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.Downstream = downstream
	return res, nil
}

// retryAllowed reports whether a failed run has attempts left.
func retryAllowed(job Job, failed Run) bool {
	return job.RetryCount > 0 && failed.Attempt < job.RetryCount+1
}

// RequeueExpiredClaims resets runs whose lease expired back to queued so
// another worker can pick them up. The original agent is signalled to kill
// the still-running process, and claim_epoch is incremented so a late
// complete from the superseded worker cannot write the result. Dispatcher
// workers (local, ssh) fence themselves: a renew that finds no claim, or a
// lease that could not be renewed before it expired, kills the process.
func (s *Store) RequeueExpiredClaims(ctx context.Context, now time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO run_cancel_signals (run_id, target_id)
		SELECT qc.run_id, qc.target_id
		FROM queue_claims qc
		WHERE qc.lease_expires_at < $1 AND qc.target_type = 'agent'
		ON CONFLICT DO NOTHING`, now); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE runs SET status = 'queued', claimed_at = NULL, started_at = NULL,
		                claim_epoch = claim_epoch + 1
		WHERE status IN ('claimed', 'running')
		  AND EXISTS (
			SELECT 1 FROM queue_claims qc
			WHERE qc.run_id = runs.id AND qc.lease_expires_at < $1
		  )`, now)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM queue_claims WHERE lease_expires_at < $1`, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PruneStaleCanceledClaims drops claims of canceled runs that finished long
// ago (their agents restarted and will never report back).
func (s *Store) PruneStaleCanceledClaims(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM queue_claims qc USING runs r
		WHERE qc.run_id = r.id AND r.status = 'canceled'
		  AND r.finished_at IS NOT NULL AND r.finished_at < now() - $1 * interval '1 millisecond'`,
		olderThan.Milliseconds(),
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CanceledRunsForAgent lists run ids claimed by this agent that were
// canceled server-side (delivered via heartbeat for process kill).
func (s *Store) CanceledRunsForAgent(ctx context.Context, agentID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT qc.run_id::text FROM queue_claims qc
		JOIN runs r ON r.id = qc.run_id
		WHERE qc.target_type = 'agent' AND qc.target_id = $1::text AND r.status = 'canceled'
		UNION
		SELECT run_id::text FROM run_cancel_signals WHERE target_id = $1::text`,
		agentID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ClaimNextDispatcher atomically assigns the oldest queued local or SSH run
// to the in-process dispatcher and leases it like an agent claim.
func (s *Store) ClaimNextDispatcher(ctx context.Context, lease time.Duration) (QueueClaim, Run, bool, error) {
	var c QueueClaim
	row := s.pool.QueryRow(ctx, `
		WITH next AS (
			SELECT r.id
			FROM runs r
			LEFT JOIN queue_claims qc ON qc.run_id = r.id
			WHERE r.status = 'queued'
			  AND r.not_before <= now()
			  AND qc.run_id IS NULL
			  AND r.target_type IN ('local','ssh')
			ORDER BY r.queued_at ASC
			LIMIT 1
			FOR UPDATE OF r SKIP LOCKED
		),
		ins AS (
			INSERT INTO queue_claims (run_id, target_type, target_id, lease_expires_at, claim_version)
			SELECT n.id, r.target_type, r.target_id, now() + $1::interval, r.claim_epoch
			FROM next n JOIN runs r ON r.id = n.id
			ON CONFLICT (run_id) DO NOTHING
			RETURNING id, run_id, target_type, target_id, lease_expires_at, claim_version, claimed_at
		),
		upd AS (
			UPDATE runs SET status = 'claimed', claimed_at = now()
			WHERE id IN (SELECT run_id FROM ins)
		)
		SELECT id, run_id, target_type, target_id, lease_expires_at, claim_version, claimed_at
		FROM ins`,
		lease.String(),
	)
	err := row.Scan(&c.ID, &c.RunID, &c.TargetType, &c.TargetID, &c.LeaseExpiresAt, &c.ClaimVersion, &c.ClaimedAt)
	if err == pgx.ErrNoRows {
		return QueueClaim{}, Run{}, false, nil
	}
	if err != nil {
		return QueueClaim{}, Run{}, false, err
	}
	run, err := s.GetRun(ctx, c.RunID)
	if err != nil {
		return QueueClaim{}, Run{}, false, err
	}
	return c, run, true, nil
}

// ForceRequeueDispatcherOrphaned resets local and SSH runs left in
// claimed/running without a live claim (server restart sweep).
func (s *Store) ForceRequeueDispatcherOrphaned(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status = 'queued', claimed_at = NULL, started_at = NULL,
		                claim_epoch = claim_epoch + 1
		WHERE status IN ('claimed','running') AND target_type IN ('local','ssh')
		  AND NOT EXISTS (SELECT 1 FROM queue_claims qc WHERE qc.run_id = runs.id)`)
	return err
}

// AgentHoldsFile reports whether the agent currently has a claimed/running
// transfer whose library endpoint is this file.
func (s *Store) AgentHoldsFile(ctx context.Context, agentID, fileID uuid.UUID) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM queue_claims qc
		JOIN runs r ON r.id = qc.run_id
		JOIN jobs j ON j.id = r.job_id
		WHERE qc.target_type = 'agent' AND qc.target_id = $1
		  AND r.status IN ('claimed','running')
		  AND (
		    j.transfer_config->'source'->>'file_id' = $2
		    OR j.transfer_config->'dest'->>'file_id' = $2
		  )`,
		agentID.String(), fileID.String(),
	).Scan(&n)
	return n > 0, err
}
