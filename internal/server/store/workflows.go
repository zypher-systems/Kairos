package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Workflow is a named DAG of jobs linked by conditional edges.
type Workflow struct {
	ID          uuid.UUID         `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Parameters  map[string]string `json:"parameters"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`

	// Schedule of the workflow itself. When it fires, one execution starts
	// every root job. "manual" means the workflow runs by hand, from its
	// file trigger, or not at all.
	ScheduleType    string     `json:"schedule_type"` // manual | cron | interval | oneoff
	CronExpr        string     `json:"cron_expr"`
	IntervalSeconds int        `json:"interval_seconds"`
	OneoffAt        *time.Time `json:"oneoff_at,omitempty"`
	Timezone        string     `json:"timezone"`
	CalendarID      *uuid.UUID `json:"calendar_id,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`

	// File trigger on a storage location.
	WatchEnabled     bool       `json:"watch_enabled"`
	WatchLocationID  *uuid.UUID `json:"watch_location_id,omitempty"`
	WatchPath        string     `json:"watch_path"`
	WatchIntervalSec int        `json:"watch_interval_seconds"`
	WatchLastSig     string     `json:"watch_last_sig,omitempty"`
}

// Validate checks the fields the store owns; the scheduler validates the
// schedule expression itself.
func (w *Workflow) Validate() error {
	if w.Name == "" {
		return errors.New("name is required")
	}
	if w.ScheduleType == "" {
		w.ScheduleType = "manual"
	}
	switch w.ScheduleType {
	case "manual", "cron", "interval", "oneoff":
	default:
		return errors.New("schedule_type must be manual, cron, interval, or oneoff")
	}
	if w.Timezone == "" {
		w.Timezone = "UTC"
	}
	if w.WatchIntervalSec == 0 {
		w.WatchIntervalSec = 60
	}
	if w.WatchEnabled {
		if w.WatchLocationID == nil {
			return errors.New("watch_location_id is required when the file trigger is on")
		}
		if w.WatchPath == "" {
			return errors.New("watch_path is required when the file trigger is on")
		}
		if w.WatchIntervalSec < 10 {
			return errors.New("watch_interval_seconds must be >= 10")
		}
	}
	if w.Parameters == nil {
		w.Parameters = map[string]string{}
	}
	return nil
}

// WorkflowExecution is one firing of a workflow DAG.
type WorkflowExecution struct {
	ID           uuid.UUID  `json:"id"`
	WorkflowID   uuid.UUID  `json:"workflow_id"`
	Trigger      string     `json:"trigger"`
	TriggerFiles []string   `json:"trigger_files,omitempty"`
	ScheduleTime *time.Time `json:"schedule_time,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// WorkflowEdge links two jobs: when FromJob finishes with the matching
// condition, ToJob is enqueued.
type WorkflowEdge struct {
	ID         uuid.UUID `json:"id"`
	WorkflowID uuid.UUID `json:"workflow_id"`
	FromJobID  uuid.UUID `json:"from_job_id"`
	ToJobID    uuid.UUID `json:"to_job_id"`
	Condition  string    `json:"condition"` // success | failure | always
}

// WorkflowDetail bundles a workflow with its edges and the job names for UI.
type WorkflowDetail struct {
	Workflow
	Edges []WorkflowEdge `json:"edges"`
	Jobs  []Job          `json:"jobs"` // jobs assigned to this workflow
}

// Validate checks edge invariants.
func (e *WorkflowEdge) Validate() error {
	switch e.Condition {
	case "success", "failure", "always":
	default:
		return errors.New("condition must be success, failure, or always")
	}
	if e.FromJobID == e.ToJobID {
		return errors.New("an edge cannot point at itself")
	}
	return nil
}

const workflowCols = `id, name, description, enabled, parameters, created_at, updated_at,
	schedule_type, cron_expr, interval_seconds, oneoff_at, timezone, calendar_id, next_run_at, last_run_at,
	watch_enabled, watch_location_id, watch_path, watch_interval_seconds, watch_last_sig`

func scanWorkflow(row pgx.Row) (Workflow, error) {
	var w Workflow
	var params []byte
	err := row.Scan(&w.ID, &w.Name, &w.Description, &w.Enabled, &params, &w.CreatedAt, &w.UpdatedAt,
		&w.ScheduleType, &w.CronExpr, &w.IntervalSeconds, &w.OneoffAt, &w.Timezone, &w.CalendarID, &w.NextRunAt, &w.LastRunAt,
		&w.WatchEnabled, &w.WatchLocationID, &w.WatchPath, &w.WatchIntervalSec, &w.WatchLastSig)
	if err != nil {
		return w, err
	}
	w.Parameters = unmarshalStringMap(params)
	return w, nil
}

// CreateWorkflow stores a workflow. nextRunAt is the first scheduled
// firing (nil for manual or disabled), computed by the caller.
func (s *Store) CreateWorkflow(ctx context.Context, wf *Workflow) error {
	if err := wf.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(wf.Parameters)
	if err != nil {
		return err
	}
	got, err := scanWorkflow(s.pool.QueryRow(ctx, `
		INSERT INTO workflows (name, description, enabled, parameters,
			schedule_type, cron_expr, interval_seconds, oneoff_at, timezone, calendar_id, next_run_at,
			watch_enabled, watch_location_id, watch_path, watch_interval_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING `+workflowCols,
		wf.Name, wf.Description, wf.Enabled, raw,
		wf.ScheduleType, wf.CronExpr, wf.IntervalSeconds, wf.OneoffAt, wf.Timezone, wf.CalendarID, wf.NextRunAt,
		wf.WatchEnabled, wf.WatchLocationID, wf.WatchPath, wf.WatchIntervalSec,
	))
	if err != nil {
		return err
	}
	*wf = got
	return nil
}

func (s *Store) ListWorkflows(ctx context.Context) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+workflowCols+` FROM workflows ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) GetWorkflow(ctx context.Context, id uuid.UUID) (Workflow, error) {
	return scanWorkflow(s.pool.QueryRow(ctx, `SELECT `+workflowCols+` FROM workflows WHERE id = $1`, id))
}

// GetWorkflowDetail loads a workflow with its edges and member jobs.
func (s *Store) GetWorkflowDetail(ctx context.Context, id uuid.UUID) (WorkflowDetail, error) {
	wf, err := s.GetWorkflow(ctx, id)
	if err != nil {
		return WorkflowDetail{}, err
	}
	edges, err := s.ListWorkflowEdges(ctx, id)
	if err != nil {
		return WorkflowDetail{}, err
	}
	jobs, err := s.JobsInWorkflow(ctx, id)
	if err != nil {
		return WorkflowDetail{}, err
	}
	return WorkflowDetail{Workflow: wf, Edges: edges, Jobs: jobs}, nil
}

// UpdateWorkflow replaces a workflow, including its schedule state (the
// caller recomputes next_run_at when the schedule changed).
func (s *Store) UpdateWorkflow(ctx context.Context, wf *Workflow) error {
	if err := wf.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(wf.Parameters)
	if err != nil {
		return err
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE workflows SET name=$1, description=$2, enabled=$3, parameters=$4, updated_at=now(),
			schedule_type=$6, cron_expr=$7, interval_seconds=$8, oneoff_at=$9, timezone=$10, calendar_id=$11, next_run_at=$12,
			watch_enabled=$13, watch_location_id=$14, watch_path=$15, watch_interval_seconds=$16
		WHERE id=$5`,
		wf.Name, wf.Description, wf.Enabled, raw, wf.ID,
		wf.ScheduleType, wf.CronExpr, wf.IntervalSeconds, wf.OneoffAt, wf.Timezone, wf.CalendarID, wf.NextRunAt,
		wf.WatchEnabled, wf.WatchLocationID, wf.WatchPath, wf.WatchIntervalSec,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DueWorkflowsTx locks and returns enabled workflows whose schedule is due.
func (s *Store) DueWorkflowsTx(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]Workflow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := tx.Query(ctx, `
		SELECT `+workflowCols+` FROM workflows
		WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1
		ORDER BY next_run_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SetWorkflowScheduleStateTx advances a workflow after its schedule fired.
func (s *Store) SetWorkflowScheduleStateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, nextRunAt *time.Time, lastRunAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE workflows SET next_run_at = $2, last_run_at = $3, updated_at = now() WHERE id = $1`,
		id, nextRunAt, lastRunAt)
	return err
}

// DisableWorkflowTx turns a workflow off (a schedule that cannot advance).
func (s *Store) DisableWorkflowTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE workflows SET enabled = false, next_run_at = NULL, updated_at = now() WHERE id = $1`, id)
	return err
}

// ListWatchWorkflows returns enabled workflows with the file trigger armed.
func (s *Store) ListWatchWorkflows(ctx context.Context) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+workflowCols+` FROM workflows WHERE enabled AND watch_enabled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SetWorkflowWatchSig persists the last-fired watch signature.
func (s *Store) SetWorkflowWatchSig(ctx context.Context, id uuid.UUID, sig string) error {
	_, err := s.pool.Exec(ctx, `UPDATE workflows SET watch_last_sig = $2 WHERE id = $1`, id, sig)
	return err
}

// StartWorkflow creates one execution and queues every root job under it.
// The manual Run button, the workflow's schedule and its file trigger all
// go through here, so they behave the same.
func (s *Store) StartWorkflow(ctx context.Context, wf Workflow, trigger string, scheduleTime time.Time, files []string) (WorkflowExecution, []Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowExecution{}, nil, err
	}
	defer tx.Rollback(ctx)
	exec, runs, err := s.StartWorkflowTx(ctx, tx, wf, trigger, scheduleTime, files)
	if err != nil {
		return WorkflowExecution{}, nil, err
	}
	return exec, runs, tx.Commit(ctx)
}

// StartWorkflowTx is StartWorkflow inside a transaction.
func (s *Store) StartWorkflowTx(ctx context.Context, tx pgx.Tx, wf Workflow, trigger string, scheduleTime time.Time, files []string) (WorkflowExecution, []Run, error) {
	var raw []byte
	if files != nil {
		raw, _ = json.Marshal(files)
	}
	exec := WorkflowExecution{WorkflowID: wf.ID, Trigger: trigger, TriggerFiles: files}
	if err := tx.QueryRow(ctx, `
		INSERT INTO workflow_executions (workflow_id, trigger, trigger_files, schedule_time)
		VALUES ($1, $2, $3, $4) RETURNING id, created_at, schedule_time`,
		wf.ID, trigger, raw, scheduleTime).Scan(&exec.ID, &exec.CreatedAt, &exec.ScheduleTime); err != nil {
		return exec, nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT `+jobCols+` FROM jobs
		WHERE workflow_id = $1 AND enabled
		  AND id NOT IN (SELECT to_job_id FROM workflow_edges WHERE workflow_id = $1)
		ORDER BY name`, wf.ID)
	if err != nil {
		return exec, nil, err
	}
	roots, err := scanJobs(rows)
	rows.Close()
	if err != nil {
		return exec, nil, err
	}
	var out []Run
	for _, j := range roots {
		run, err := s.enqueueWithPolicyTx(ctx, tx, j, trigger, scheduleTime, &exec.ID)
		if err != nil {
			return exec, nil, fmt.Errorf("queue %s: %w", j.Name, err)
		}
		if len(files) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE runs SET trigger_files = $2 WHERE id = $1`, run.ID, raw); err != nil {
				return exec, nil, err
			}
			run.TriggerFiles = files
		}
		out = append(out, run)
	}
	return exec, out, nil
}

// GetWorkflowExecution loads one execution.
func (s *Store) GetWorkflowExecution(ctx context.Context, id uuid.UUID) (WorkflowExecution, error) {
	var e WorkflowExecution
	err := s.pool.QueryRow(ctx, `
		SELECT id, workflow_id, trigger, trigger_files, schedule_time, created_at
		FROM workflow_executions WHERE id = $1`, id).Scan(&e.ID, &e.WorkflowID, &e.Trigger, &e.TriggerFiles, &e.ScheduleTime, &e.CreatedAt)
	return e, err
}

func (s *Store) DeleteWorkflow(ctx context.Context, id uuid.UUID) error {
	// jobs keep their workflow_id but the FK SET NULLs it
	_, err := s.pool.Exec(ctx, `DELETE FROM workflows WHERE id = $1`, id)
	return err
}

// ----- edges -----

const edgeCols = `id, workflow_id, from_job_id, to_job_id, condition`

func scanEdge(row pgx.Row) (WorkflowEdge, error) {
	var e WorkflowEdge
	err := row.Scan(&e.ID, &e.WorkflowID, &e.FromJobID, &e.ToJobID, &e.Condition)
	return e, err
}

func (s *Store) AddWorkflowEdge(ctx context.Context, e *WorkflowEdge) error {
	if err := e.Validate(); err != nil {
		return err
	}
	existing, err := s.ListWorkflowEdges(ctx, e.WorkflowID)
	if err != nil {
		return err
	}
	if err := EdgesAcyclic(append(existing, *e)); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO workflow_edges (workflow_id, from_job_id, to_job_id, condition)
		VALUES ($1, $2, $3, $4)
		RETURNING `+edgeCols,
		e.WorkflowID, e.FromJobID, e.ToJobID, e.Condition,
	).Scan(&e.ID, &e.WorkflowID, &e.FromJobID, &e.ToJobID, &e.Condition)
}

func (s *Store) ListWorkflowEdges(ctx context.Context, workflowID uuid.UUID) ([]WorkflowEdge, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+edgeCols+` FROM workflow_edges WHERE workflow_id = $1`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowEdge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWorkflowEdge(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM workflow_edges WHERE id = $1`, id)
	return err
}

// ReplaceWorkflowEdges swaps the full edge set atomically (editor save).
func (s *Store) ReplaceWorkflowEdges(ctx context.Context, workflowID uuid.UUID, edges []WorkflowEdge) error {
	for i := range edges {
		edges[i].WorkflowID = workflowID
		if err := edges[i].Validate(); err != nil {
			return err
		}
	}
	if err := EdgesAcyclic(edges); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM workflow_edges WHERE workflow_id = $1`, workflowID); err != nil {
		return err
	}
	for _, e := range edges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO workflow_edges (workflow_id, from_job_id, to_job_id, condition)
			VALUES ($1, $2, $3, $4)`,
			e.WorkflowID, e.FromJobID, e.ToJobID, e.Condition,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// JobsInWorkflow lists jobs assigned to a workflow.
func (s *Store) JobsInWorkflow(ctx context.Context, workflowID uuid.UUID) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs WHERE workflow_id = $1 ORDER BY name`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// EdgesFromJob returns the out-edges for a job within any workflow — the
// downstream jobs to fire when it completes.
func (s *Store) EdgesFromJob(ctx context.Context, fromJobID uuid.UUID) ([]WorkflowEdge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT we.id, we.workflow_id, we.from_job_id, we.to_job_id, we.condition
		FROM workflow_edges we
		JOIN workflows w ON w.id = we.workflow_id
		WHERE we.from_job_id = $1 AND w.enabled`, fromJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowEdge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetJobsWorkflowFromEdges links every job referenced by the edge set to
// the workflow. Jobs previously in the workflow but no longer referenced
// are unlinked.
func (s *Store) SetJobsWorkflowFromEdges(ctx context.Context, workflowID uuid.UUID, edges []WorkflowEdge) error {
	ids := map[uuid.UUID]bool{}
	for _, e := range edges {
		ids[e.FromJobID] = true
		ids[e.ToJobID] = true
	}
	// unlink jobs no longer referenced
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET workflow_id = NULL WHERE workflow_id = $1`, workflowID); err != nil {
		return err
	}
	for id := range ids {
		if _, err := s.pool.Exec(ctx, `UPDATE jobs SET workflow_id = $1 WHERE id = $2`, workflowID, id); err != nil {
			return err
		}
	}
	return nil
}

// RootJobs returns jobs in a workflow with no incoming edges (entry points).
func (s *Store) RootJobs(ctx context.Context, workflowID uuid.UUID) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobCols+` FROM jobs
		WHERE workflow_id = $1
		  AND id NOT IN (SELECT to_job_id FROM workflow_edges WHERE workflow_id = $1)
		ORDER BY name`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// CreateWorkflowExecution records one firing of a workflow DAG.
func (s *Store) CreateWorkflowExecution(ctx context.Context, workflowID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workflow_executions (workflow_id) VALUES ($1) RETURNING id`, workflowID).Scan(&id)
	return id, err
}

// OverlayWorkflowParams copies workflow parameters onto job.EnvPlain (params win).
func (s *Store) OverlayWorkflowParams(ctx context.Context, job *Job, execID *uuid.UUID) {
	if job == nil || execID == nil || *execID == uuid.Nil {
		return
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT w.parameters FROM workflow_executions e
		JOIN workflows w ON w.id = e.workflow_id
		WHERE e.id = $1`, *execID).Scan(&raw)
	if err != nil {
		return
	}
	params := unmarshalStringMap(raw)
	if len(params) == 0 {
		return
	}
	if job.EnvPlain == nil {
		job.EnvPlain = map[string]string{}
	}
	for k, v := range params {
		job.EnvPlain[k] = v
	}
}

// StampRunExecution sets workflow_execution_id on a run that was not started
// as part of a workflow (a scheduled job that is also a DAG node).
func (s *Store) StampRunExecution(ctx context.Context, runID, execID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET workflow_execution_id = $2 WHERE id = $1`, runID, execID)
	return err
}

// rowQuerier is the slice of pgx.Tx and *pgxpool.Pool the join check needs.
type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WorkflowJoinReady reports whether child may start in this execution: it
// has not been enqueued yet, every parent edge's job has settled, and each
// parent's final status satisfies that edge's condition. A parent whose
// outcome does not match its edge keeps the child from ever running in
// this execution, so A-success->C with B-success->C does not run C when A
// failed and B succeeded.
func (s *Store) WorkflowJoinReady(ctx context.Context, execID, workflowID, child uuid.UUID) (bool, error) {
	return joinReady(ctx, s.pool, execID, workflowID, child)
}

func workflowJoinReadyTx(ctx context.Context, tx pgx.Tx, execID, workflowID, child uuid.UUID) (bool, error) {
	return joinReady(ctx, tx, execID, workflowID, child)
}

func joinReady(ctx context.Context, q rowQuerier, execID, workflowID, child uuid.UUID) (bool, error) {
	rows, err := q.Query(ctx, `
		SELECT from_job_id, condition FROM workflow_edges
		WHERE workflow_id = $1 AND to_job_id = $2`, workflowID, child)
	if err != nil {
		return false, err
	}
	type edge struct {
		from uuid.UUID
		cond string
	}
	var in []edge
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.from, &e.cond); err != nil {
			rows.Close()
			return false, err
		}
		in = append(in, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(in) == 0 {
		return true, nil
	}
	var existing int
	if err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM runs
		WHERE workflow_execution_id = $1 AND job_id = $2`,
		execID, child).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}
	for _, e := range in {
		// The parent's latest run in this execution: a queued retry means
		// the parent has not settled, whatever the earlier attempt did.
		var status string
		err := q.QueryRow(ctx, `
			SELECT status FROM runs
			WHERE workflow_execution_id = $1 AND job_id = $2
			ORDER BY created_at DESC, attempt DESC LIMIT 1`,
			execID, e.from).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !isTerminalStatus(status) || !edgeConditionMatches(e.cond, status) {
			return false, nil
		}
	}
	return true, nil
}

func isTerminalStatus(status string) bool {
	switch status {
	case RunSucceeded, RunFailed, RunCanceled, RunSkipped:
		return true
	}
	return false
}

func (s *Store) fanoutWorkflowTx(ctx context.Context, tx pgx.Tx, run Run, job Job) ([]Run, error) {
	rows, err := tx.Query(ctx, `
		SELECT we.id, we.workflow_id, we.from_job_id, we.to_job_id, we.condition
		FROM workflow_edges we
		JOIN workflows w ON w.id = we.workflow_id
		WHERE we.from_job_id = $1 AND w.enabled`, job.ID)
	if err != nil {
		return nil, err
	}
	var edges []WorkflowEdge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}

	execID, err := ensureExecutionTx(ctx, tx, run, edges[0].WorkflowID)
	if err != nil {
		return nil, err
	}
	run.WorkflowExecutionID = &execID
	// Serialize fan-out per execution. Two parents finishing together would
	// otherwise each count only itself at the join and the child would never
	// start, or both would pass the "not yet enqueued" check and start it
	// twice.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM workflow_executions WHERE id = $1 FOR UPDATE`, execID); err != nil {
		return nil, err
	}

	var out []Run
	for _, e := range edges {
		if !edgeConditionMatches(e.Condition, run.Status) {
			continue
		}
		ready, err := workflowJoinReadyTx(ctx, tx, execID, e.WorkflowID, e.ToJobID)
		if err != nil {
			return nil, err
		}
		if !ready {
			continue
		}
		next, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = $1`, e.ToJobID))
		if err != nil {
			return nil, err
		}
		child, err := s.enqueueWithPolicyTx(ctx, tx, next, TriggerWorkflow, time.Now(), &execID)
		if err != nil {
			return nil, err
		}
		out = append(out, child)
	}
	return out, nil
}

func ensureExecutionTx(ctx context.Context, tx pgx.Tx, run Run, workflowID uuid.UUID) (uuid.UUID, error) {
	if run.WorkflowExecutionID != nil {
		return *run.WorkflowExecutionID, nil
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO workflow_executions (workflow_id) VALUES ($1) RETURNING id`, workflowID).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET workflow_execution_id = $2 WHERE id = $1`, run.ID, id); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func edgeConditionMatches(cond, status string) bool {
	succeeded := status == RunSucceeded
	switch cond {
	case "always":
		return true
	case "success":
		return succeeded
	case "failure":
		return !succeeded
	}
	return false
}
