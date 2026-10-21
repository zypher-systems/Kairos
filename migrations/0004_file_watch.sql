-- +goose Up
-- +goose StatementBegin

-- File-watch triggers: a job can fire when a matching file appears on the
-- server filesystem (local) or on an SSH host (polled over SFTP).
-- watch_last_sig stores the last fired signature; the watcher holds the
-- settle-detection state in memory.
ALTER TABLE jobs ADD COLUMN watch_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE jobs ADD COLUMN watch_target text NOT NULL DEFAULT 'local'; -- local | ssh
ALTER TABLE jobs ADD COLUMN watch_ssh_host_id uuid REFERENCES ssh_hosts(id) ON DELETE SET NULL;
ALTER TABLE jobs ADD COLUMN watch_path text NOT NULL DEFAULT '';        -- glob, e.g. /data/incoming/*.csv
ALTER TABLE jobs ADD COLUMN watch_interval_seconds integer NOT NULL DEFAULT 60;
ALTER TABLE jobs ADD COLUMN watch_last_sig text NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_enabled;
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_target;
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_ssh_host_id;
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_path;
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_interval_seconds;
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_last_sig;
-- +goose StatementEnd
