-- +goose Up
-- +goose StatementBegin

-- Storage locations: the places files live, as first-class objects a
-- transfer or a file trigger refers to by id. config holds the non-secret
-- settings for the kind; secret_cipher holds the sealed credentials.
CREATE TABLE IF NOT EXISTS storage_locations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL UNIQUE,
    kind          text NOT NULL CHECK (kind IN ('mount', 'sftp', 's3', 'agent')),
    config        jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_cipher text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- A file trigger can watch a location.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS watch_location_id uuid REFERENCES storage_locations(id) ON DELETE SET NULL;

-- The files a file trigger matched when it fired, so the run (a transfer
-- with "use the files from the trigger", or a script through
-- KAIROS_TRIGGER_FILES) knows what arrived.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS trigger_files jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE runs DROP COLUMN IF EXISTS trigger_files;
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_location_id;
DROP TABLE IF EXISTS storage_locations;
-- +goose StatementEnd
