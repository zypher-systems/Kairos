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

// Job is a scheduled unit of work.
type Job struct {
	ID                     uuid.UUID         `json:"id"`
	Name                   string            `json:"name"`
	Description            string            `json:"description"`
	ScheduleType           string            `json:"schedule_type"` // cron | interval | oneoff
	CronExpr               string            `json:"cron_expr"`
	IntervalSeconds        int               `json:"interval_seconds"`
	OneoffAt               *time.Time        `json:"oneoff_at,omitempty"`
	Timezone               string            `json:"timezone"`
	TargetType             string            `json:"target_type"` // local | agent | ssh | selector
	TargetSelector         json.RawMessage   `json:"target_selector"`
	SSHHostID              *uuid.UUID        `json:"ssh_host_id,omitempty"`
	ExecMode               string            `json:"exec_mode"` // shell | docker | transfer
	DockerImage            string            `json:"docker_image"`
	DockerCommand          []string          `json:"docker_command"`
	ShellScript            string            `json:"shell_script"`
	Workdir                string            `json:"workdir"`
	TransferConfig         TransferConfig    `json:"transfer_config"`
	EnvPlain               map[string]string `json:"env_plain"`
	EnvSecrets             map[string]string `json:"env_secrets"`
	EnvSecretRefs          []EnvSecretRef    `json:"env_secret_refs"`
	RetryCount             int               `json:"retry_count"`
	RetryBaseDelayMs       int               `json:"retry_base_delay_ms"`
	RetryMaxDelayMs        int               `json:"retry_max_delay_ms"`
	TimeoutSeconds         int               `json:"timeout_seconds"`
	SLAMissedGraceSeconds  int               `json:"sla_missed_grace_seconds"`
	SLALateSeconds         int               `json:"sla_late_seconds"`
	RequireApproval        bool              `json:"require_approval"`
	ApprovalTimeoutSeconds int               `json:"approval_timeout_seconds"`
	ConcurrencyPolicy      string            `json:"concurrency_policy"` // allow | forbid | replace
	Enabled                bool              `json:"enabled"`
	Tags                   []string          `json:"tags"`
	WorkflowID             *uuid.UUID        `json:"workflow_id,omitempty"`
	WatchEnabled           bool              `json:"watch_enabled"`
	WatchTarget            string            `json:"watch_target"` // local | ssh | agent
	WatchSSHHostID         *uuid.UUID        `json:"watch_ssh_host_id,omitempty"`
	WatchAgentID           *uuid.UUID        `json:"watch_agent_id,omitempty"`
	WatchLocationID        *uuid.UUID        `json:"watch_location_id,omitempty"`
	WatchPath              string            `json:"watch_path"`
	WatchIntervalSec       int               `json:"watch_interval_seconds"`
	WatchLastSig           string            `json:"watch_last_sig,omitempty"`
	NotifyChannelIDs       []uuid.UUID       `json:"notify_channel_ids,omitempty"`
	CalendarID             *uuid.UUID        `json:"calendar_id,omitempty"`
	NextRunAt              *time.Time        `json:"next_run_at,omitempty"`
	LastRunAt              *time.Time        `json:"last_run_at,omitempty"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

// Validate checks job invariants, applying defaults first.
func (j *Job) Validate() error {
	// A transfer's target is derived from its endpoints by the store, so
	// whatever a client sent for it is replaced by the placeholder here.
	if j.ExecMode == "transfer" {
		j.TargetType = "local"
		j.TargetSelector = json.RawMessage(`{}`)
		j.SSHHostID = nil
	}
	j.normalize()
	if j.Name == "" {
		return errors.New("name is required")
	}
	switch j.ScheduleType {
	case "cron", "interval", "oneoff", "manual":
	default:
		return errors.New("schedule_type must be cron, interval, oneoff, or manual")
	}
	switch j.ExecMode {
	case "shell", "docker":
	case "transfer":
		if err := j.TransferConfig.Validate(); err != nil {
			return err
		}
		// The target of a transfer is derived from its endpoints by
		// applyTransferTarget before validation: the agent that owns an
		// endpoint, or the server. Only the derived shapes are valid here.
		if j.TargetType != "local" && j.TargetType != "agent" {
			return errors.New("transfer jobs run on the server or on the agent that owns an endpoint")
		}
	default:
		return errors.New("exec_mode must be shell, docker, or transfer")
	}
	if j.TargetType == "ssh" && j.SSHHostID == nil {
		return errors.New("ssh_host_id required for ssh targets")
	}
	if j.TargetType == "agent" {
		var sel struct {
			AgentID string `json:"agent_id"`
		}
		_ = json.Unmarshal(j.TargetSelector, &sel)
		if sel.AgentID == "" {
			return errors.New("target_selector.agent_id required for agent targets")
		}
	}
	if j.TargetType == "selector" {
		var sel map[string]string
		if err := json.Unmarshal(j.TargetSelector, &sel); err != nil {
			return errors.New("target_selector must be a label object")
		}
		if len(sel) == 0 {
			return errors.New("target_selector must include at least one label")
		}
	}
	if j.WatchEnabled {
		if j.WatchPath == "" {
			return errors.New("watch_path is required when the file trigger is on")
		}
		if j.WatchIntervalSec < 10 {
			return errors.New("watch_interval_seconds must be >= 10")
		}
		switch j.WatchTarget {
		case "local":
		case "ssh":
			if j.WatchSSHHostID == nil {
				return errors.New("watch_ssh_host_id required for ssh file triggers")
			}
		case "agent":
			if j.WatchAgentID == nil {
				return errors.New("watch_agent_id required for agent file triggers")
			}
		case "location":
			if j.WatchLocationID == nil {
				return errors.New("watch_location_id required for location file triggers")
			}
		default:
			return errors.New("watch_target must be local, ssh, agent, or location")
		}
	}
	if j.SLAMissedGraceSeconds < 0 {
		return errors.New("sla_missed_grace_seconds must be >= 0")
	}
	if j.SLALateSeconds < 0 {
		return errors.New("sla_late_seconds must be >= 0")
	}
	if j.ApprovalTimeoutSeconds < 0 {
		return errors.New("approval_timeout_seconds must be >= 0")
	}
	for i, r := range j.EnvSecretRefs {
		if r.Name == "" || r.StoreID == uuid.Nil || r.Path == "" || r.Key == "" {
			return fmt.Errorf("env_secret_refs[%d]: name, store_id, path, and key are required", i)
		}
	}
	return nil
}

// EnvSecretRef pulls one Vault KV key into a job env name at claim.
type EnvSecretRef struct {
	Name    string    `json:"name"`
	StoreID uuid.UUID `json:"store_id"`
	Path    string    `json:"path"`
	Key     string    `json:"key"`
}

// MergedEnv is plain env overlaid with revealed secrets (inline and store).
func (j Job) MergedEnv() map[string]string {
	out := map[string]string{}
	for k, v := range j.EnvPlain {
		out[k] = v
	}
	for k, v := range j.EnvSecrets {
		out[k] = v
	}
	return out
}

// SecretMask is what API responses show for stored secret values.
const SecretMask = "••••••"

// SealJobSecrets encrypts EnvSecrets values in place (write path).
func (s *Store) SealJobSecrets(j *Job) error {
	if s.box == nil {
		return nil // dev mode without master secret
	}
	for k, v := range j.EnvSecrets {
		if v == "" || v == SecretMask {
			continue
		}
		sealed, err := s.box.Seal([]byte(v))
		if err != nil {
			return err
		}
		j.EnvSecrets[k] = sealed
	}
	return nil
}

// ErrSecretUnreadable is returned when a stored secret cannot be decrypted,
// usually because KAIROS_SECRET changed. The run must fail rather than
// start with the variable empty.
var ErrSecretUnreadable = errors.New("a stored secret cannot be decrypted with this KAIROS_SECRET")

// RevealJobSecrets decrypts EnvSecrets values in place (execution path).
func (s *Store) RevealJobSecrets(j *Job) error {
	if s.box == nil {
		return nil
	}
	for k, v := range j.EnvSecrets {
		if v == "" || v == SecretMask {
			continue
		}
		raw, err := s.box.Open(v)
		if err != nil {
			return fmt.Errorf("%w: variable %s", ErrSecretUnreadable, k)
		}
		j.EnvSecrets[k] = string(raw)
	}
	return nil
}

// MaskJobSecrets redacts values for API responses.
func (s *Store) MaskJobSecrets(j *Job) {
	for k, v := range j.EnvSecrets {
		if v != "" && v != SecretMask {
			j.EnvSecrets[k] = SecretMask
		}
	}
}

func (j *Job) rawJSON() (tc, dc, ep, es, er, xc []byte, err error) {
	dc, err = json.Marshal(j.DockerCommand)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	ep, err = json.Marshal(j.EnvPlain)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	es, err = json.Marshal(j.EnvSecrets)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if j.EnvSecretRefs == nil {
		j.EnvSecretRefs = []EnvSecretRef{}
	}
	er, err = json.Marshal(j.EnvSecretRefs)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	xc, err = json.Marshal(j.TransferConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if len(j.TargetSelector) > 0 {
		tc = j.TargetSelector
	} else {
		tc = []byte(`{}`)
	}
	return tc, dc, ep, es, er, xc, nil
}

const jobCols = `
	id, name, description, schedule_type, cron_expr, interval_seconds, oneoff_at,
	timezone, target_type, target_selector, ssh_host_id, exec_mode, docker_image,
	docker_command, shell_script, workdir, env_plain, env_secrets, env_secret_refs, transfer_config,
	retry_count, retry_base_delay_ms, retry_max_delay_ms, timeout_seconds,
	sla_missed_grace_seconds, sla_late_seconds, require_approval, approval_timeout_seconds,
	concurrency_policy, enabled, tags, workflow_id,
	watch_enabled, watch_target, watch_ssh_host_id, watch_agent_id, watch_path, watch_interval_seconds, watch_last_sig,
	notify_channel_ids, calendar_id,
	next_run_at, last_run_at,
	created_at, updated_at, watch_location_id`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	var tc, dc, ep, es, er, xc []byte
	var tags []string
	var notifyIDs []uuid.UUID
	err := row.Scan(
		&j.ID, &j.Name, &j.Description, &j.ScheduleType, &j.CronExpr, &j.IntervalSeconds, &j.OneoffAt,
		&j.Timezone, &j.TargetType, &tc, &j.SSHHostID, &j.ExecMode, &j.DockerImage,
		&dc, &j.ShellScript, &j.Workdir, &ep, &es, &er, &xc,
		&j.RetryCount, &j.RetryBaseDelayMs, &j.RetryMaxDelayMs, &j.TimeoutSeconds,
		&j.SLAMissedGraceSeconds, &j.SLALateSeconds, &j.RequireApproval, &j.ApprovalTimeoutSeconds,
		&j.ConcurrencyPolicy, &j.Enabled, &tags, &j.WorkflowID,
		&j.WatchEnabled, &j.WatchTarget, &j.WatchSSHHostID, &j.WatchAgentID, &j.WatchPath, &j.WatchIntervalSec, &j.WatchLastSig,
		&notifyIDs, &j.CalendarID,
		&j.NextRunAt, &j.LastRunAt,
		&j.CreatedAt, &j.UpdatedAt, &j.WatchLocationID,
	)
	if err != nil {
		return j, err
	}
	j.TargetSelector = json.RawMessage(tc)
	j.DockerCommand = unmarshalStringSlice(dc)
	j.EnvPlain = unmarshalStringMap(ep)
	j.EnvSecrets = unmarshalStringMap(es)
	j.EnvSecretRefs = unmarshalSecretRefs(er)
	if len(xc) > 0 {
		_ = json.Unmarshal(xc, &j.TransferConfig)
	}
	j.Tags = tags
	if notifyIDs == nil {
		notifyIDs = []uuid.UUID{}
	}
	j.NotifyChannelIDs = notifyIDs
	return j, nil
}

func unmarshalStringSlice(raw []byte) []string {
	var out []string
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func unmarshalStringMap(raw []byte) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func unmarshalSecretRefs(raw []byte) []EnvSecretRef {
	var out []EnvSecretRef
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = []EnvSecretRef{}
	}
	return out
}

// normalize fills non-nilable fields decoded from JSON so NULL never hits
// NOT NULL columns.
func (j *Job) normalize() {
	if j.Tags == nil {
		j.Tags = []string{}
	}
	if j.DockerCommand == nil {
		j.DockerCommand = []string{}
	}
	if j.EnvPlain == nil {
		j.EnvPlain = map[string]string{}
	}
	if j.EnvSecrets == nil {
		j.EnvSecrets = map[string]string{}
	}
	if j.EnvSecretRefs == nil {
		j.EnvSecretRefs = []EnvSecretRef{}
	}
	if len(j.TargetSelector) == 0 {
		j.TargetSelector = json.RawMessage(`{}`)
	}
	if j.TargetType == "" {
		j.TargetType = "local"
	}
	if j.ExecMode == "" {
		j.ExecMode = "shell"
	}
	if j.ScheduleType == "" {
		j.ScheduleType = "cron"
	}
	if j.Timezone == "" {
		j.Timezone = "UTC"
	}
	if j.ConcurrencyPolicy == "" {
		j.ConcurrencyPolicy = "allow"
	}
	if j.TimeoutSeconds == 0 {
		j.TimeoutSeconds = 3600
	}
	if j.RetryBaseDelayMs == 0 {
		j.RetryBaseDelayMs = 1000
	}
	if j.RetryMaxDelayMs == 0 {
		j.RetryMaxDelayMs = 30000
	}
	if j.WatchTarget == "" {
		j.WatchTarget = "local"
	}
	if j.WatchIntervalSec == 0 {
		j.WatchIntervalSec = 60
	}
	if j.NotifyChannelIDs == nil {
		j.NotifyChannelIDs = []uuid.UUID{}
	}
	if j.CalendarID != nil && *j.CalendarID == uuid.Nil {
		j.CalendarID = nil
	}
}

// CreateJob inserts a job. nextRunAt is computed by the scheduler and must
// be supplied by the caller. Returns the persisted row.
func (s *Store) CreateJob(ctx context.Context, j *Job, nextRunAt *time.Time) (Job, error) {
	if err := s.applyTransferTarget(ctx, j); err != nil {
		return Job{}, err
	}
	j.normalize()
	if err := s.SealJobSecrets(j); err != nil {
		return Job{}, err
	}
	tc, dc, ep, es, er, xc, err := j.rawJSON()
	if err != nil {
		return Job{}, err
	}
	return scanJob(s.pool.QueryRow(ctx, `
		INSERT INTO jobs (
			name, description, schedule_type, cron_expr, interval_seconds, oneoff_at,
			timezone, target_type, target_selector, ssh_host_id, exec_mode, docker_image,
			docker_command, shell_script, workdir, env_plain, env_secrets, env_secret_refs, transfer_config,
			retry_count, retry_base_delay_ms, retry_max_delay_ms, timeout_seconds,
			sla_missed_grace_seconds, sla_late_seconds, require_approval, approval_timeout_seconds,
			concurrency_policy, enabled, tags, workflow_id,
			watch_enabled, watch_target, watch_ssh_host_id, watch_agent_id, watch_path, watch_interval_seconds, watch_last_sig,
			notify_channel_ids, calendar_id, next_run_at, watch_location_id
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42
		) RETURNING `+jobCols,
		j.Name, j.Description, j.ScheduleType, j.CronExpr, j.IntervalSeconds, j.OneoffAt,
		j.Timezone, j.TargetType, tc, j.SSHHostID, j.ExecMode, j.DockerImage,
		dc, j.ShellScript, j.Workdir, ep, es, er, xc,
		j.RetryCount, j.RetryBaseDelayMs, j.RetryMaxDelayMs, j.TimeoutSeconds,
		j.SLAMissedGraceSeconds, j.SLALateSeconds, j.RequireApproval, j.ApprovalTimeoutSeconds,
		j.ConcurrencyPolicy, j.Enabled, j.Tags, j.WorkflowID,
		j.WatchEnabled, j.WatchTarget, j.WatchSSHHostID, j.WatchAgentID, j.WatchPath, j.WatchIntervalSec, j.WatchLastSig,
		j.NotifyChannelIDs, j.CalendarID, nextRunAt, j.WatchLocationID,
	))
}

// GetJob fetches one job.
func (s *Store) GetJob(ctx context.Context, id uuid.UUID) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = $1`, id))
}

// ListJobs fetches jobs with optional filters, newest first.
func (s *Store) ListJobs(ctx context.Context, enabled *bool, tag string, limit, offset int) ([]Job, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + jobCols + ` FROM jobs`
	countQ := `SELECT count(*) FROM jobs`
	var conds []string
	var args []any
	if enabled != nil {
		args = append(args, *enabled)
		conds = append(conds, fmt.Sprintf("enabled = $%d", len(args)))
	}
	if tag != "" {
		args = append(args, tag)
		conds = append(conds, fmt.Sprintf("$%d = ANY(tags)", len(args)))
	}
	if len(conds) > 0 {
		where := ` WHERE ` + joinStrings(conds, " AND ")
		q += where
		countQ += where
	}
	var total int
	if err := s.pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	return jobs, total, err
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func scanJobs(rows pgx.Rows) ([]Job, error) {
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateJob applies a full job update and returns the new row.
func (s *Store) UpdateJob(ctx context.Context, j *Job) (Job, error) {
	if err := s.applyTransferTarget(ctx, j); err != nil {
		return Job{}, err
	}
	j.normalize()
	// Preserve existing secrets when the client sent masked/empty values.
	if len(j.EnvSecrets) > 0 {
		existing, err := s.GetJob(ctx, j.ID)
		if err == nil {
			if err := s.RevealJobSecrets(&existing); err != nil {
				return Job{}, err
			}
			for k, v := range j.EnvSecrets {
				if v == "" || v == SecretMask {
					if ev, ok := existing.EnvSecrets[k]; ok {
						j.EnvSecrets[k] = ev
					} else {
						delete(j.EnvSecrets, k)
					}
				}
			}
		}
	}
	if err := s.SealJobSecrets(j); err != nil {
		return Job{}, err
	}
	tc, dc, ep, es, er, xc, err := j.rawJSON()
	if err != nil {
		return Job{}, err
	}
	return scanJob(s.pool.QueryRow(ctx, `
		UPDATE jobs SET
			name=$1, description=$2, schedule_type=$3, cron_expr=$4, interval_seconds=$5, oneoff_at=$6,
			timezone=$7, target_type=$8, target_selector=$9, ssh_host_id=$10, exec_mode=$11, docker_image=$12,
			docker_command=$13, shell_script=$14, workdir=$15, env_plain=$16, env_secrets=$17, env_secret_refs=$18, transfer_config=$19,
			retry_count=$20, retry_base_delay_ms=$21, retry_max_delay_ms=$22, timeout_seconds=$23,
			sla_missed_grace_seconds=$24, sla_late_seconds=$25, require_approval=$26, approval_timeout_seconds=$27,
			concurrency_policy=$28, enabled=$29, tags=$30, workflow_id=$31,
			watch_enabled=$32, watch_target=$33, watch_ssh_host_id=$34, watch_agent_id=$35, watch_path=$36,
			watch_interval_seconds=$37, watch_last_sig=$38, notify_channel_ids=$39, calendar_id=$40, updated_at=now(),
			watch_location_id=$42
		WHERE id=$41
		RETURNING `+jobCols,
		j.Name, j.Description, j.ScheduleType, j.CronExpr, j.IntervalSeconds, j.OneoffAt,
		j.Timezone, j.TargetType, tc, j.SSHHostID, j.ExecMode, j.DockerImage,
		dc, j.ShellScript, j.Workdir, ep, es, er, xc,
		j.RetryCount, j.RetryBaseDelayMs, j.RetryMaxDelayMs, j.TimeoutSeconds,
		j.SLAMissedGraceSeconds, j.SLALateSeconds, j.RequireApproval, j.ApprovalTimeoutSeconds,
		j.ConcurrencyPolicy, j.Enabled, j.Tags, j.WorkflowID,
		j.WatchEnabled, j.WatchTarget, j.WatchSSHHostID, j.WatchAgentID, j.WatchPath, j.WatchIntervalSec, j.WatchLastSig,
		j.NotifyChannelIDs, j.CalendarID, j.ID, j.WatchLocationID,
	))
}

// DeleteJob removes a job and (via FK cascade) its runs and logs.
func (s *Store) DeleteJob(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, id)
	return err
}

// SetJobScheduleState advances scheduler state after a due run is queued.
func (s *Store) SetJobScheduleState(ctx context.Context, id uuid.UUID, nextRunAt *time.Time, lastRunAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET next_run_at = $2, last_run_at = $3, updated_at = now() WHERE id = $1`,
		id, nextRunAt, lastRunAt,
	)
	return err
}

// SetJobNextRun recomputes the stored next run time (job create/update path).
func (s *Store) SetJobNextRun(ctx context.Context, id uuid.UUID, nextRunAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET next_run_at = $2, updated_at = now() WHERE id = $1`,
		id, nextRunAt,
	)
	return err
}

// DueJobs locks and returns enabled jobs whose next_run_at is due.
func (s *Store) DueJobs(ctx context.Context, now time.Time, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 250
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobCols+` FROM jobs
		WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1
		ORDER BY next_run_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// DueJobsTx is the transactional variant used by the scheduler engine so
// enqueued runs and advanced next_run_at commit atomically.
func (s *Store) DueJobsTx(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 250
	}
	rows, err := tx.Query(ctx, `
		SELECT `+jobCols+` FROM jobs
		WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1
		ORDER BY next_run_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// SetJobScheduleStateTx updates schedule state within an engine transaction.
func (s *Store) SetJobScheduleStateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, nextRunAt *time.Time, lastRunAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE jobs SET next_run_at = $2, last_run_at = $3, updated_at = now() WHERE id = $1`,
		id, nextRunAt, lastRunAt,
	)
	return err
}
