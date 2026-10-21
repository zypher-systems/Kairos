-- +goose Up
-- +goose StatementBegin

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS require_approval boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS approval_timeout_seconds integer NOT NULL DEFAULT 0;

ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS approved_by uuid REFERENCES admins(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS approved_at timestamptz,
    ADD COLUMN IF NOT EXISTS rejected_by uuid REFERENCES admins(id) ON DELETE SET NULL;

ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS notify_on_pending boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS notify_on_rejected boolean NOT NULL DEFAULT true;

DROP INDEX IF EXISTS idx_runs_status;
CREATE INDEX idx_runs_status ON runs (status) WHERE status IN ('queued','claimed','running','pending_approval');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_runs_status;
CREATE INDEX idx_runs_status ON runs (status) WHERE status IN ('queued','claimed','running');
ALTER TABLE notifications DROP COLUMN IF EXISTS notify_on_rejected;
ALTER TABLE notifications DROP COLUMN IF EXISTS notify_on_pending;
ALTER TABLE runs DROP COLUMN IF EXISTS rejected_by;
ALTER TABLE runs DROP COLUMN IF EXISTS approved_at;
ALTER TABLE runs DROP COLUMN IF EXISTS approved_by;
ALTER TABLE jobs DROP COLUMN IF EXISTS approval_timeout_seconds;
ALTER TABLE jobs DROP COLUMN IF EXISTS require_approval;
-- +goose StatementEnd
