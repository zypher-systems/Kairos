-- +goose Up
-- +goose StatementBegin

-- A claimed outbox row stays claimed until its worker finishes or the lock
-- lapses, so two replicas never send the same notification. Rows that
-- exhausted their attempts are kept as dead letters instead of vanishing.
ALTER TABLE notify_outbox ADD COLUMN IF NOT EXISTS locked_until timestamptz NOT NULL DEFAULT to_timestamp(0);
ALTER TABLE notify_outbox ADD COLUMN IF NOT EXISTS dead_at timestamptz;
CREATE INDEX IF NOT EXISTS idx_notify_outbox_dead ON notify_outbox (dead_at) WHERE dead_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_runs_job_created ON runs (job_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_runs_job_created;
DROP INDEX IF EXISTS idx_sessions_expires;
DROP INDEX IF EXISTS idx_notify_outbox_dead;
ALTER TABLE notify_outbox DROP COLUMN IF EXISTS dead_at;
ALTER TABLE notify_outbox DROP COLUMN IF EXISTS locked_until;
-- +goose StatementEnd
