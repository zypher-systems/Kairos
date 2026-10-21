-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS calendars (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    dates jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS calendar_id uuid REFERENCES calendars(id) ON DELETE SET NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE jobs DROP COLUMN IF EXISTS calendar_id;
DROP TABLE IF EXISTS calendars;
-- +goose StatementEnd
