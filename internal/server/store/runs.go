package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Run statuses.
const (
	RunQueued          = "queued"
	RunClaimed         = "claimed"
	RunRunning         = "running"
	RunSucceeded       = "succeeded"
	RunFailed          = "failed"
	RunCanceled        = "canceled"
	RunSkipped         = "skipped"
	RunPendingApproval = "pending_approval"
)

// Run triggers.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	TriggerRetry    = "retry"
	TriggerWorkflow = "workflow"
	TriggerFile     = "file"
)

// Run is a single execution attempt of a job.
type Run struct {
	ID                  uuid.UUID  `json:"id"`
	JobID               uuid.UUID  `json:"job_id"`
	Attempt             int        `json:"attempt"`
	Trigger             string     `json:"trigger"`
	Status              string     `json:"status"`
	TargetType          string     `json:"target_type"`
	TargetID            string     `json:"target_id"`
	ScheduleTime        time.Time  `json:"schedule_time"`
	QueuedAt            time.Time  `json:"queued_at"`
	NotBefore           time.Time  `json:"not_before"`
	ClaimedAt           *time.Time `json:"claimed_at,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	ExitCode            *int       `json:"exit_code,omitempty"`
	Error               string     `json:"error"`
	CreatedAt           time.Time  `json:"created_at"`
	ClaimEpoch          int        `json:"claim_epoch"`
	WorkflowExecutionID *uuid.UUID `json:"workflow_execution_id,omitempty"`
	ApprovedBy          *uuid.UUID `json:"approved_by,omitempty"`
	ApprovedAt          *time.Time `json:"approved_at,omitempty"`
	RejectedBy          *uuid.UUID `json:"rejected_by,omitempty"`
	// TriggerFiles are the files a file trigger matched when it queued
	// this run (paths as the watched location lists them).
	TriggerFiles []string `json:"trigger_files,omitempty"`
}

const runCols = `
	id, job_id, attempt, trigger, status, target_type, target_id,
	schedule_time, queued_at, not_before, claimed_at, started_at, finished_at, exit_code,
	error, created_at, claim_epoch, workflow_execution_id,
	approved_by, approved_at, rejected_by, trigger_files`

const runColsPrefixed = `
	r.id, r.job_id, r.attempt, r.trigger, r.status, r.target_type, r.target_id,
	r.schedule_time, r.queued_at, r.not_before, r.claimed_at, r.started_at, r.finished_at, r.exit_code,
	r.error, r.created_at, r.claim_epoch, r.workflow_execution_id,
	r.approved_by, r.approved_at, r.rejected_by, r.trigger_files`

// RunCols is the exported column list for claim queries in other packages.
const RunCols = runCols

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(
		&r.ID, &r.JobID, &r.Attempt, &r.Trigger, &r.Status, &r.TargetType, &r.TargetID,
		&r.ScheduleTime, &r.QueuedAt, &r.NotBefore, &r.ClaimedAt, &r.StartedAt, &r.FinishedAt, &r.ExitCode,
		&r.Error, &r.CreatedAt, &r.ClaimEpoch, &r.WorkflowExecutionID,
		&r.ApprovedBy, &r.ApprovedAt, &r.RejectedBy, &r.TriggerFiles,
	)
	return r, err
}

// ScanRun scans the row currently positioned by rows.Next() into a Run.
func ScanRun(rows pgx.Rows) (Run, error) {
	var r Run
	err := rows.Scan(
		&r.ID, &r.JobID, &r.Attempt, &r.Trigger, &r.Status, &r.TargetType, &r.TargetID,
		&r.ScheduleTime, &r.QueuedAt, &r.NotBefore, &r.ClaimedAt, &r.StartedAt, &r.FinishedAt, &r.ExitCode,
		&r.Error, &r.CreatedAt, &r.ClaimEpoch, &r.WorkflowExecutionID,
		&r.ApprovedBy, &r.ApprovedAt, &r.RejectedBy, &r.TriggerFiles,
	)
	return r, err
}

func scanRuns(rows pgx.Rows) ([]Run, error) {
	var out []Run
	for rows.Next() {
		r, err := ScanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnqueueRunTxExec creates a queued run, optionally stamped with a workflow
// execution so fan-in can wait for every parent in the same execution.
func (s *Store) EnqueueRunTxExec(ctx context.Context, tx pgx.Tx, job Job, trigger string, scheduleTime time.Time, notBefore time.Time, attempt int, execID *uuid.UUID) (Run, error) {
	targetType := job.TargetType
	targetID := s.resolveTargetID(job)
	if notBefore.IsZero() || notBefore.Before(time.Now()) {
		notBefore = time.Now()
	}
	// A job marked require-approval waits for a person. Approve and reject
	// stay available; the run does not start unreviewed.
	status := RunQueued
	if job.RequireApproval {
		status = RunPendingApproval
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO runs (job_id, attempt, trigger, status, target_type, target_id, schedule_time, not_before, workflow_execution_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+RunCols,
		job.ID, attempt, trigger, status, targetType, targetID, scheduleTime, notBefore, execID,
	)
	run, err := scanRun(row)
	if err != nil {
		return Run{}, err
	}
	if run.Status == RunPendingApproval {
		if err := enqueueEventNotifyTx(ctx, tx, EventRunPending, job, &run); err != nil {
			return Run{}, err
		}
	}
	return run, nil
}

// EnqueueRunTx creates a queued run with no workflow execution.
func (s *Store) EnqueueRunTx(ctx context.Context, tx pgx.Tx, job Job, trigger string, scheduleTime time.Time, notBefore time.Time, attempt int) (Run, error) {
	return s.EnqueueRunTxExec(ctx, tx, job, trigger, scheduleTime, notBefore, attempt, nil)
}

func (s *Store) resolveTargetID(job Job) string {
	switch job.TargetType {
	case "ssh":
		if job.SSHHostID != nil {
			return job.SSHHostID.String()
		}
		return ""
	case "local":
		return "local"
	case "agent":
		var sel struct {
			AgentID string `json:"agent_id"`
		}
		if len(job.TargetSelector) > 0 {
			_ = json.Unmarshal(job.TargetSelector, &sel)
		}
		return sel.AgentID
	default:
		return "" // selector: resolved at claim time
	}
}

// EnqueueRun is the non-transactional variant.
func (s *Store) EnqueueRun(ctx context.Context, job Job, trigger string, scheduleTime time.Time, attempt int) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	r, err := s.EnqueueRunTx(ctx, tx, job, trigger, scheduleTime, time.Now(), attempt)
	if err != nil {
		return Run{}, err
	}
	return r, tx.Commit(ctx)
}

// ActiveRunsForJob lists in-flight runs of a job (concurrency policy).
func (s *Store) ActiveRunsForJob(ctx context.Context, jobID uuid.UUID) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+RunCols+` FROM runs
		WHERE job_id = $1 AND status IN ('queued','claimed','running','pending_approval')`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// CancelRuns marks runs canceled and drops their queue claims. Claims for
// agent targets survive cancellation: they carry the kill instruction to
// the agent via heartbeat and are removed when the agent reports back.
func (s *Store) CancelRuns(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, id := range ids {
		if err := cancelRunTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// cancelRunTx marks one run canceled. Dispatcher claims are dropped, which
// the worker notices at its next lease renew and kills the process. Agent
// claims stay: the heartbeat delivers the kill and the agent's completion
// removes the claim.
func cancelRunTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE runs SET status = 'canceled', finished_at = now()
		WHERE id = $1 AND status IN ('queued','claimed','running','pending_approval')`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		DELETE FROM queue_claims WHERE run_id = $1 AND target_type <> 'agent'`, id)
	return err
}

// EnqueueWithPolicy applies the concurrency policy for schedule/manual
// triggers: forbid → skipped record; replace → cancel actives first.
func (s *Store) EnqueueWithPolicy(ctx context.Context, job Job, trigger string, scheduleTime time.Time) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	r, err := s.EnqueueWithPolicyTx(ctx, tx, job, trigger, scheduleTime)
	if err != nil {
		return Run{}, err
	}
	return r, tx.Commit(ctx)
}

// ConcurrencyDecision is what EnqueueWithPolicy does given current actives.
type ConcurrencyDecision string

const (
	DecisionEnqueue ConcurrencyDecision = "enqueue"
	DecisionSkip    ConcurrencyDecision = "skip"
	DecisionReplace ConcurrencyDecision = "replace"
)

// DecideConcurrency is the pure policy used by EnqueueWithPolicyTx.
func DecideConcurrency(policy, trigger string, active int) ConcurrencyDecision {
	_ = trigger
	if policy == "" || policy == "allow" || active == 0 {
		return DecisionEnqueue
	}
	if policy == "forbid" {
		return DecisionSkip
	}
	if policy == "replace" {
		return DecisionReplace
	}
	return DecisionEnqueue
}

// EnqueueWithPolicyTx is the transactional variant used by the scheduler.
func (s *Store) EnqueueWithPolicyTx(ctx context.Context, tx pgx.Tx, job Job, trigger string, scheduleTime time.Time) (Run, error) {
	return s.enqueueWithPolicyTx(ctx, tx, job, trigger, scheduleTime, nil)
}

func (s *Store) enqueueWithPolicyTx(ctx context.Context, tx pgx.Tx, job Job, trigger string, scheduleTime time.Time, execID *uuid.UUID) (Run, error) {
	needActives := DecideConcurrency(job.ConcurrencyPolicy, trigger, 1) != DecisionEnqueue
	if needActives {
		rows, err := tx.Query(ctx, `
			SELECT `+RunCols+` FROM runs
			WHERE job_id = $1 AND status IN ('queued','claimed','running','pending_approval')
			FOR UPDATE`, job.ID)
		if err != nil {
			return Run{}, err
		}
		active, err := scanRuns(rows)
		rows.Close()
		if err != nil {
			return Run{}, err
		}
		switch DecideConcurrency(job.ConcurrencyPolicy, trigger, len(active)) {
		case DecisionSkip:
			return s.recordSkippedTx(ctx, tx, job)
		case DecisionReplace:
			for _, r := range active {
				if err := cancelRunTx(ctx, tx, r.ID); err != nil {
					return Run{}, err
				}
			}
		}
	}
	return s.EnqueueRunTxExec(ctx, tx, job, trigger, scheduleTime, time.Now(), 1, execID)
}

// EnqueueWithPolicyExec is EnqueueWithPolicy stamped with a workflow execution.
func (s *Store) EnqueueWithPolicyExec(ctx context.Context, job Job, trigger string, scheduleTime time.Time, execID uuid.UUID) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	id := execID
	r, err := s.enqueueWithPolicyTx(ctx, tx, job, trigger, scheduleTime, &id)
	if err != nil {
		return Run{}, err
	}
	return r, tx.Commit(ctx)
}

func (s *Store) recordSkippedTx(ctx context.Context, tx pgx.Tx, job Job) (Run, error) {
	return s.RecordSkippedReasonTx(ctx, tx, job, "")
}

// RecordSkippedReasonTx inserts a skipped schedule run with an optional error reason.
func (s *Store) RecordSkippedReasonTx(ctx context.Context, tx pgx.Tx, job Job, reason string) (Run, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO runs (job_id, attempt, trigger, status, target_type, target_id, schedule_time, not_before, finished_at, error)
		VALUES ($1, 1, $2, $3, $4, $5, now(), now(), now(), $6)
		RETURNING `+RunCols,
		job.ID, TriggerSchedule, RunSkipped, job.TargetType, s.resolveTargetID(job), reason,
	)
	return scanRun(row)
}

// RetryBackoff computes the delay before the next attempt (exponential).
func RetryBackoff(job Job, attempt int) time.Duration {
	base := time.Duration(job.RetryBaseDelayMs) * time.Millisecond
	if base <= 0 {
		base = time.Second
	}
	max := time.Duration(job.RetryMaxDelayMs) * time.Millisecond
	if max <= 0 {
		max = 30 * time.Second
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= max {
			return max
		}
	}
	return delay
}

// GetRun fetches one run.
func (s *Store) GetRun(ctx context.Context, id uuid.UUID) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `SELECT `+RunCols+` FROM runs WHERE id = $1`, id))
}

// RunFilter filters run listings.
type RunFilter struct {
	JobID  *uuid.UUID
	Status string
	// Since, when set, hides runs created earlier.
	Since  *time.Time
	Limit  int
	Offset int
}

// ListRuns returns runs newest first.
func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q := `SELECT ` + RunCols + ` FROM runs`
	var conds []string
	var args []any
	if f.JobID != nil {
		args = append(args, f.JobID.String())
		conds = append(conds, fmt.Sprintf("job_id = $%d::uuid", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		conds = append(conds, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if len(conds) > 0 {
		q += ` WHERE ` + joinStrings(conds, " AND ")
	}
	args = append(args, f.Limit)
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))
	args = append(args, f.Offset)
	q += fmt.Sprintf(` OFFSET $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// CountRuns returns the number of runs matching the filter (limit/offset ignored).
func (s *Store) CountRuns(ctx context.Context, f RunFilter) (int, error) {
	q := `SELECT count(*) FROM runs`
	var conds []string
	var args []any
	if f.JobID != nil {
		args = append(args, f.JobID.String())
		conds = append(conds, fmt.Sprintf("job_id = $%d::uuid", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		conds = append(conds, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if len(conds) > 0 {
		q += ` WHERE ` + joinStrings(conds, " AND ")
	}
	var n int
	err := s.pool.QueryRow(ctx, q, args...).Scan(&n)
	return n, err
}

// RunCounts returns queued/running counts and 24h rollup per status.
func (s *Store) RunCounts(ctx context.Context) (map[string]any, error) {
	var queued, running int
	var daySucceeded, dayFailed int
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM runs WHERE status IN ('queued','claimed','pending_approval')) AS queued,
			(SELECT COUNT(*) FROM runs WHERE status = 'running') AS running,
			(SELECT COUNT(*) FROM runs WHERE status = 'succeeded' AND created_at > now() - interval '24 hours') AS day_succeeded,
			(SELECT COUNT(*) FROM runs WHERE status = 'failed' AND created_at > now() - interval '24 hours') AS day_failed`,
	).Scan(&queued, &running, &daySucceeded, &dayFailed)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"queued":        queued,
		"running":       running,
		"day_succeeded": daySucceeded,
		"day_failed":    dayFailed,
	}, nil
}

// ---------- log chunks ----------

// LogChunk is one ordered piece of run output.
type LogChunk struct {
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Data      string    `json:"data"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	// MaxLogChunkBytes is the per-chunk ceiling written to Postgres.
	MaxLogChunkBytes = 64 * 1024
	// MaxLogRunBytes is the per-run ceiling; further chunks are dropped
	// after a single truncation marker.
	MaxLogRunBytes = 8 * 1024 * 1024
)

// AppendLog stores one log chunk, ignoring duplicates (crash-safe redelivery).
func (s *Store) AppendLog(ctx context.Context, runID uuid.UUID, seq int64, stream, data string) error {
	_, err := s.AppendLogs(ctx, runID, []LogChunk{{Seq: seq, Stream: stream, Data: data}})
	return err
}

// AppendLogs stores a batch of chunks in one statement, ignoring duplicates.
// Chunks are made safe for a text column first: invalid UTF-8 becomes
// U+FFFD, NUL bytes are dropped and oversized chunks are cut on a rune
// boundary, so a job that prints binary or splits a multibyte character
// across writes never poisons the batch. Once the run reaches MaxLogRunBytes
// a truncation marker is written, the rest is dropped and ErrLogCapped is
// returned; the accepted count is how many chunks were stored.
func (s *Store) AppendLogs(ctx context.Context, runID uuid.UUID, chunks []LogChunk) (accepted int, err error) {
	if len(chunks) == 0 {
		return 0, nil
	}
	var total int64
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(octet_length(data)), 0) FROM run_logs WHERE run_id = $1`, runID).Scan(&total); err != nil {
		return 0, err
	}
	if total >= MaxLogRunBytes {
		return 0, ErrLogCapped
	}
	capped := false
	rows := make([][]any, 0, len(chunks))
	for _, c := range chunks {
		data := SanitizeLogData(c.Data, MaxLogChunkBytes)
		if total+int64(len(data)) > MaxLogRunBytes {
			data = cutUTF8(data, int(MaxLogRunBytes-total))
			capped = true
		}
		if data != "" || !capped {
			rows = append(rows, []any{runID, c.Seq, c.Stream, data})
			accepted++
		}
		total += int64(len(data))
		if capped {
			rows = append(rows, []any{runID, c.Seq + 1, "system", "[kairos] log truncated: per-run cap reached"})
			break
		}
	}
	if len(rows) > 0 {
		b := &pgx.Batch{}
		for _, r := range rows {
			b.Queue(`
				INSERT INTO run_logs (run_id, seq, stream, data) VALUES ($1, $2, $3, $4)
				ON CONFLICT (run_id, seq) DO NOTHING`, r...)
		}
		br := s.pool.SendBatch(ctx, b)
		for range rows {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return 0, err
			}
		}
		if err := br.Close(); err != nil {
			return 0, err
		}
	}
	if capped {
		return accepted, ErrLogCapped
	}
	return accepted, nil
}

// SanitizeLogData makes job output storable in a text column: invalid UTF-8
// sequences become U+FFFD, NUL bytes are removed and the result is cut on a
// rune boundary at maxBytes.
func SanitizeLogData(data string, maxBytes int) string {
	if strings.IndexByte(data, 0) >= 0 {
		data = strings.ReplaceAll(data, "\x00", "")
	}
	if !utf8.ValidString(data) {
		data = strings.ToValidUTF8(data, "\uFFFD")
	}
	return cutUTF8(data, maxBytes)
}

// cutUTF8 truncates s to at most n bytes without splitting a rune.
func cutUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Logs returns chunks strictly after seq, ordered, capped.
func (s *Store) Logs(ctx context.Context, runID uuid.UUID, afterSeq int64, limit int) ([]LogChunk, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT seq, stream, data, created_at FROM run_logs
		WHERE run_id = $1 AND seq > $2
		ORDER BY seq ASC LIMIT $3`,
		runID, afterSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogChunk
	for rows.Next() {
		var c LogChunk
		if err := rows.Scan(&c.Seq, &c.Stream, &c.Data, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- retention ----------

// PruneRuns deletes finished runs beyond retentionRuns per job or older
// than retentionDays, in batches so the table is never locked for long.
// Runs still queued, running or waiting for approval are never pruned,
// however old: deleting them would drop work and orphan claims.
func (s *Store) PruneRuns(ctx context.Context, retentionRuns, retentionDays int) (int64, error) {
	const batch = 5000
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `
			DELETE FROM runs r USING (
				SELECT id FROM (
					SELECT id, status, row_number() OVER (PARTITION BY job_id ORDER BY created_at DESC) AS rn
					FROM runs
				) ranked
				WHERE rn > $1 AND status NOT IN ('queued','claimed','running','pending_approval')
				LIMIT $2
			) d WHERE r.id = d.id`,
			retentionRuns, batch,
		)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			break
		}
	}
	for {
		tag, err := s.pool.Exec(ctx, `
			DELETE FROM runs WHERE id IN (
				SELECT id FROM runs
				WHERE created_at < now() - ($1 * interval '1 day')
				  AND status NOT IN ('queued','claimed','running','pending_approval')
				LIMIT $2
			)`,
			retentionDays, batch,
		)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			break
		}
	}
	return total, nil
}

// PruneHousekeeping removes rows nothing references any more: expired
// sessions, cancel signals older than a day (their agents are gone), and
// workflow executions with no runs left. It returns the rows removed.
func (s *Store) PruneHousekeeping(ctx context.Context) (int64, error) {
	var total int64
	for _, q := range []string{
		`DELETE FROM sessions WHERE expires_at < now()`,
		`DELETE FROM run_cancel_signals WHERE created_at < now() - interval '1 day'`,
		`DELETE FROM workflow_executions we
		 WHERE we.created_at < now() - interval '1 day'
		   AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.workflow_execution_id = we.id)`,
	} {
		tag, err := s.pool.Exec(ctx, q)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// LogSearchHit is one matching log chunk.
type LogSearchHit struct {
	RunID     uuid.UUID `json:"run_id"`
	JobID     uuid.UUID `json:"job_id"`
	JobName   string    `json:"job_name"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Snippet   string    `json:"snippet"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchLogs finds chunks whose data contains q (case-insensitive).
// Optional jobID scopes to one job. q must already be trimmed.
func (s *Store) SearchLogs(ctx context.Context, q string, jobID *uuid.UUID, limit, offset int) ([]LogSearchHit, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	sql := `
		SELECT r.id, r.job_id, j.name, l.seq, l.stream, l.data, l.created_at
		FROM run_logs l
		JOIN runs r ON r.id = l.run_id
		JOIN jobs j ON j.id = r.job_id
		WHERE strpos(lower(l.data), lower($1)) > 0`
	args := []any{q}
	if jobID != nil {
		args = append(args, jobID.String())
		sql += fmt.Sprintf(` AND r.job_id = $%d::uuid`, len(args))
	}
	args = append(args, limit)
	sql += fmt.Sprintf(` ORDER BY l.created_at DESC LIMIT $%d`, len(args))
	args = append(args, offset)
	sql += fmt.Sprintf(` OFFSET $%d`, len(args))
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogSearchHit
	for rows.Next() {
		var h LogSearchHit
		var data string
		if err := rows.Scan(&h.RunID, &h.JobID, &h.JobName, &h.Seq, &h.Stream, &data, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Snippet = logSnippet(data, q)
		out = append(out, h)
	}
	return out, rows.Err()
}

func logSnippet(data, q string) string {
	const width = 160
	lower := strings.ToLower(data)
	needle := strings.ToLower(q)
	i := strings.Index(lower, needle)
	if i < 0 {
		if len(data) > width {
			return data[:width] + "…"
		}
		return data
	}
	start := i - 40
	if start < 0 {
		start = 0
	}
	end := i + len(q) + 80
	if end > len(data) {
		end = len(data)
	}
	s := data[start:end]
	if start > 0 {
		s = "…" + s
	}
	if end < len(data) {
		s += "…"
	}
	return s
}
