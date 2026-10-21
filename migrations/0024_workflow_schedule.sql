-- +goose Up
-- +goose StatementBegin

-- A workflow can carry its own schedule and file trigger. When it fires,
-- one execution is created and every root job is queued under it, exactly
-- as the manual Run does. Jobs inside a workflow can then use the
-- "manual" schedule type (no schedule of their own).
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS schedule_type          text NOT NULL DEFAULT 'manual';
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS cron_expr              text NOT NULL DEFAULT '';
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS interval_seconds       integer NOT NULL DEFAULT 0;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS oneoff_at              timestamptz;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS timezone               text NOT NULL DEFAULT 'UTC';
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS calendar_id            uuid REFERENCES calendars(id) ON DELETE SET NULL;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS next_run_at            timestamptz;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS last_run_at            timestamptz;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS watch_enabled          boolean NOT NULL DEFAULT false;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS watch_location_id      uuid REFERENCES storage_locations(id) ON DELETE SET NULL;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS watch_path             text NOT NULL DEFAULT '';
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS watch_interval_seconds integer NOT NULL DEFAULT 60;
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS watch_last_sig         text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_workflows_due ON workflows (next_run_at)
    WHERE enabled AND next_run_at IS NOT NULL;

-- What started an execution, and the files a trigger matched.
ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS trigger       text NOT NULL DEFAULT 'workflow';
ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS trigger_files jsonb;
ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS schedule_time timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workflow_executions DROP COLUMN IF EXISTS schedule_time;
ALTER TABLE workflow_executions DROP COLUMN IF EXISTS trigger_files;
ALTER TABLE workflow_executions DROP COLUMN IF EXISTS trigger;
DROP INDEX IF EXISTS idx_workflows_due;
ALTER TABLE workflows DROP COLUMN IF EXISTS watch_last_sig;
ALTER TABLE workflows DROP COLUMN IF EXISTS watch_interval_seconds;
ALTER TABLE workflows DROP COLUMN IF EXISTS watch_path;
ALTER TABLE workflows DROP COLUMN IF EXISTS watch_location_id;
ALTER TABLE workflows DROP COLUMN IF EXISTS watch_enabled;
ALTER TABLE workflows DROP COLUMN IF EXISTS last_run_at;
ALTER TABLE workflows DROP COLUMN IF EXISTS next_run_at;
ALTER TABLE workflows DROP COLUMN IF EXISTS calendar_id;
ALTER TABLE workflows DROP COLUMN IF EXISTS timezone;
ALTER TABLE workflows DROP COLUMN IF EXISTS oneoff_at;
ALTER TABLE workflows DROP COLUMN IF EXISTS interval_seconds;
ALTER TABLE workflows DROP COLUMN IF EXISTS cron_expr;
ALTER TABLE workflows DROP COLUMN IF EXISTS schedule_type;
-- +goose StatementEnd
