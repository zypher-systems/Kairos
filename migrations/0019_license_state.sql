-- +goose Up
-- +goose StatementBegin

-- zsl2 licensing (1.14): one row per install. The legacy `licenses` table
-- (kairos_license_v1_ blobs) is no longer read.
CREATE TABLE IF NOT EXISTS license_state (
    id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    mode text NOT NULL DEFAULT 'none',            -- none | online | file
    key_enc text NOT NULL DEFAULT '',             -- zsl1 license key, sealed with KAIROS_SECRET
    lease text NOT NULL DEFAULT '',               -- newest signed zsl2 lease (online mode)
    file_token text NOT NULL DEFAULT '',          -- signed zsl2 offline license file
    heartbeat_seconds integer NOT NULL DEFAULT 86400,
    last_code text NOT NULL DEFAULT '',           -- last licensing-server answer code ('' = ok)
    last_error text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO license_state (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS license_state;
-- +goose StatementEnd
