-- +goose Up
-- +goose StatementBegin

-- Managed file library: staged payloads for transfer jobs.
-- storage_path is relative to KAIROS_DATA_DIR/files.
CREATE TABLE files (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    size_bytes   bigint NOT NULL DEFAULT 0,
    sha256       text NOT NULL DEFAULT '',
    storage_path text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- File-transfer jobs: exec_mode = 'transfer', endpoints in transfer_config.
-- {
--   "source": {"kind": "library"|"ssh", "file_id": "...", "ssh_host_id": "...", "path": "..."},
--   "dest":   {"kind": "library"|"ssh", "ssh_host_id": "...", "path": "..."},
--   "overwrite": true,
--   "delete_source": false
-- }
ALTER TABLE jobs ADD COLUMN transfer_config jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE jobs DROP COLUMN IF EXISTS transfer_config;
DROP TABLE IF EXISTS files;
-- +goose StatementEnd
