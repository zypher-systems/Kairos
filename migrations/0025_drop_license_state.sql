-- +goose Up
-- +goose StatementBegin

-- Licensing was removed. Drop the stored key, lease, offline file and the
-- legacy licenses table from the first migration.
DROP TABLE IF EXISTS license_state;
DROP TABLE IF EXISTS licenses;
DELETE FROM settings WHERE key = 'license_instance_id';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS license_state (
    id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    mode text NOT NULL DEFAULT 'none',
    key_enc text NOT NULL DEFAULT '',
    lease text NOT NULL DEFAULT '',
    file_token text NOT NULL DEFAULT '',
    heartbeat_seconds integer NOT NULL DEFAULT 86400,
    last_code text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO license_state (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd
