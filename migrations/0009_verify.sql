-- +goose Up
-- +goose StatementBegin

CREATE TABLE notify_outbox (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    payload         jsonb NOT NULL,
    attempt         integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_notify_outbox_due ON notify_outbox (next_attempt_at);

CREATE INDEX IF NOT EXISTS idx_runs_queued_dispatcher
    ON runs (queued_at)
    WHERE status = 'queued' AND target_type IN ('local', 'ssh');

CREATE INDEX IF NOT EXISTS idx_queue_claims_lease
    ON queue_claims (lease_expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_queue_claims_lease;
DROP INDEX IF EXISTS idx_runs_queued_dispatcher;
DROP TABLE IF EXISTS notify_outbox;
-- +goose StatementEnd
