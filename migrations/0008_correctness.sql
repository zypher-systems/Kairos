-- +goose Up
-- +goose StatementBegin

ALTER TABLE runs ADD COLUMN IF NOT EXISTS claim_epoch integer NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS workflow_execution_id uuid;

CREATE TABLE IF NOT EXISTS workflow_executions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_runs_workflow_exec ON runs (workflow_execution_id)
    WHERE workflow_execution_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS run_cancel_signals (
    run_id     uuid NOT NULL,
    target_id  text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, target_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS run_cancel_signals;
DROP INDEX IF EXISTS idx_runs_workflow_exec;
DROP TABLE IF EXISTS workflow_executions;
ALTER TABLE runs DROP COLUMN IF EXISTS workflow_execution_id;
ALTER TABLE runs DROP COLUMN IF EXISTS claim_epoch;

-- +goose StatementEnd
