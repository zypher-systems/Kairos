-- +goose Up
-- +goose StatementBegin

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS sla_missed_grace_seconds integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sla_late_seconds integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sla_missed_alerted_for timestamptz;

ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS sla_missed_alerted_at timestamptz,
    ADD COLUMN IF NOT EXISTS sla_late_alerted_at timestamptz;

ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS notify_on_missed boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS notify_on_late boolean NOT NULL DEFAULT true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notifications DROP COLUMN IF EXISTS notify_on_missed;
ALTER TABLE notifications DROP COLUMN IF EXISTS notify_on_late;
ALTER TABLE runs DROP COLUMN IF EXISTS sla_missed_alerted_at;
ALTER TABLE runs DROP COLUMN IF EXISTS sla_late_alerted_at;
ALTER TABLE jobs DROP COLUMN IF EXISTS sla_missed_grace_seconds;
ALTER TABLE jobs DROP COLUMN IF EXISTS sla_late_seconds;
ALTER TABLE jobs DROP COLUMN IF EXISTS sla_missed_alerted_for;
-- +goose StatementEnd
