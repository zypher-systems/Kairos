-- +goose Up
-- +goose StatementBegin

ALTER TABLE workflows
    ADD COLUMN IF NOT EXISTS parameters jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workflows DROP COLUMN IF EXISTS parameters;
-- +goose StatementEnd
