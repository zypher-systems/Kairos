-- +goose Up
-- +goose StatementBegin

-- Runs not claimable before this instant (retry backoff, future scheduling).
ALTER TABLE runs ADD COLUMN not_before timestamptz NOT NULL DEFAULT now();
CREATE INDEX idx_runs_not_before ON runs (not_before) WHERE status = 'queued';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_runs_not_before;
ALTER TABLE runs DROP COLUMN IF EXISTS not_before;
-- +goose StatementEnd