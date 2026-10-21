-- +goose Up
-- +goose StatementBegin

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS notify_channel_ids uuid[] NOT NULL DEFAULT '{}';

CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id     uuid NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    prefix       text NOT NULL,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_api_tokens_hash ON api_tokens (token_hash);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_tokens;
ALTER TABLE jobs DROP COLUMN IF EXISTS notify_channel_ids;
-- +goose StatementEnd
