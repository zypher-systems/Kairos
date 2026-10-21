-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS secret_stores (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    type text NOT NULL DEFAULT 'vault',
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    token_cipher text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS env_secret_refs jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE jobs DROP COLUMN IF EXISTS env_secret_refs;
DROP TABLE IF EXISTS secret_stores;
-- +goose StatementEnd
