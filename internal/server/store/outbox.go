package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NotifyOutboxRow is one durable notification delivery attempt.
type NotifyOutboxRow struct {
	ID            uuid.UUID       `json:"id"`
	ChannelID     uuid.UUID       `json:"channel_id"`
	Payload       json.RawMessage `json:"payload"`
	Attempt       int             `json:"attempt"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     string          `json:"last_error"`
	DeadAt        *time.Time      `json:"dead_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

const maxOutboxAttempts = 8

// ClaimOutboxDue takes due rows for a worker. The claim is a lock that
// outlives the statement: a row stays with its worker for lockFor (or until
// it completes or fails), so two replicas never deliver the same event.
func (s *Store) ClaimOutboxDue(ctx context.Context, limit int, lockFor time.Duration) ([]NotifyOutboxRow, error) {
	if limit <= 0 {
		limit = 16
	}
	if lockFor <= 0 {
		lockFor = time.Minute
	}
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM notify_outbox
			WHERE next_attempt_at <= now() AND locked_until < now() AND dead_at IS NULL
			ORDER BY next_attempt_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE notify_outbox o SET locked_until = now() + $2::interval
		FROM due WHERE o.id = due.id
		RETURNING o.id, o.channel_id, o.payload, o.attempt, o.next_attempt_at, o.last_error, o.dead_at, o.created_at`,
		limit, lockFor.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

func scanOutbox(rows pgx.Rows) ([]NotifyOutboxRow, error) {
	var out []NotifyOutboxRow
	for rows.Next() {
		var r NotifyOutboxRow
		if err := rows.Scan(&r.ID, &r.ChannelID, &r.Payload, &r.Attempt, &r.NextAttemptAt, &r.LastError, &r.DeadAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CompleteOutbox drops a successfully delivered row.
func (s *Store) CompleteOutbox(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notify_outbox WHERE id = $1`, id)
	return err
}

// FailOutbox records a delivery error and reschedules the row with backoff.
// After maxOutboxAttempts the row becomes a dead letter: it stays, shows in
// the UI and on /metrics, and an operator can retry or dismiss it.
func (s *Store) FailOutbox(ctx context.Context, id uuid.UUID, attempt int, errMsg string) error {
	if len(errMsg) > 1000 {
		errMsg = errMsg[:1000]
	}
	if attempt >= maxOutboxAttempts {
		_, err := s.pool.Exec(ctx, `
			UPDATE notify_outbox
			SET attempt = $2, last_error = $3, dead_at = now(), locked_until = to_timestamp(0)
			WHERE id = $1`, id, attempt, errMsg)
		return err
	}
	delay := time.Duration(1<<uint(attempt)) * time.Second
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE notify_outbox
		SET attempt = $2, last_error = $3, next_attempt_at = now() + $4::interval, locked_until = to_timestamp(0)
		WHERE id = $1`,
		id, attempt, errMsg, delay.String())
	return err
}

// ListDeadOutbox returns dead letters, newest first.
func (s *Store) ListDeadOutbox(ctx context.Context, limit int) ([]NotifyOutboxRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, channel_id, payload, attempt, next_attempt_at, last_error, dead_at, created_at
		FROM notify_outbox WHERE dead_at IS NOT NULL
		ORDER BY dead_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

// RetryDeadOutbox puts a dead letter back in the queue for delivery now.
func (s *Store) RetryDeadOutbox(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE notify_outbox
		SET dead_at = NULL, attempt = 0, next_attempt_at = now(), locked_until = to_timestamp(0)
		WHERE id = $1 AND dead_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteDeadOutbox dismisses a dead letter.
func (s *Store) DeleteDeadOutbox(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notify_outbox WHERE id = $1 AND dead_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// OutboxCounts reports pending and dead rows for /metrics and the UI.
func (s *Store) OutboxCounts(ctx context.Context) (pending, dead int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE dead_at IS NULL),
			COUNT(*) FILTER (WHERE dead_at IS NOT NULL)
		FROM notify_outbox`).Scan(&pending, &dead)
	return
}

func (s *Store) enqueueNotifyOutboxTx(ctx context.Context, tx pgx.Tx, run Run, job Job) error {
	kind := "run_failed"
	switch run.Status {
	case RunSucceeded:
		kind = "run_succeeded"
	case RunCanceled:
		// Delivered to the failure subscribers, but named for what it is so
		// a receiver can tell an operator's cancel from a crash.
		kind = "run_canceled"
	}
	return enqueueEventNotifyTx(ctx, tx, kind, job, &run)
}

func enqueueEventNotifyTx(ctx context.Context, tx pgx.Tx, kind string, job Job, run *Run) error {
	channels, err := channelsForEventTx(ctx, tx, kind)
	if err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}
	payload := map[string]any{
		"event":       kind,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"job_id":      job.ID.String(),
		"job_name":    job.Name,
		"status":      kind,
		"trigger":     TriggerSchedule,
		"target_type": job.TargetType,
		"attempt":     1,
	}
	if run != nil {
		payload["run_id"] = run.ID.String()
		payload["status"] = run.Status
		payload["trigger"] = run.Trigger
		payload["target_type"] = run.TargetType
		payload["attempt"] = run.Attempt
		if run.Error != "" {
			payload["error"] = run.Error
		}
		if run.ExitCode != nil {
			payload["exit_code"] = run.ExitCode
		}
		if run.StartedAt != nil {
			end := time.Now()
			if run.FinishedAt != nil {
				end = *run.FinishedAt
			}
			payload["duration_seconds"] = int64(end.Sub(*run.StartedAt).Seconds())
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if !jobMatchesChannel(job, ch.ID) {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO notify_outbox (channel_id, payload) VALUES ($1, $2)`,
			ch.ID, raw); err != nil {
			return err
		}
	}
	return nil
}

func jobMatchesChannel(job Job, channelID uuid.UUID) bool {
	if len(job.NotifyChannelIDs) == 0 {
		return true
	}
	for _, id := range job.NotifyChannelIDs {
		if id == channelID {
			return true
		}
	}
	return false
}

func channelsForEventTx(ctx context.Context, tx pgx.Tx, event string) ([]NotificationChannel, error) {
	col := "notify_on_failure"
	switch event {
	case "run_succeeded":
		col = "notify_on_success"
	case "run_missed":
		col = "notify_on_missed"
	case "run_late":
		col = "notify_on_late"
	case EventRunPending:
		col = "notify_on_pending"
	case EventRunRejected:
		col = "notify_on_rejected"
	}
	rows, err := tx.Query(ctx, `SELECT `+channelCols+` FROM notifications WHERE enabled AND `+col)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
