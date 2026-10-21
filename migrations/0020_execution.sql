-- +goose Up
-- +goose StatementBegin

-- API tokens can expire. NULL keeps the token until it is deleted.
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS expires_at timestamptz;

-- The pruner and the shutdown path look runs up by state and age.
CREATE INDEX IF NOT EXISTS idx_runs_created_at ON runs (created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_runs_created_at;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS expires_at;
-- +goose StatementEnd
