-- +goose Up
-- +goose StatementBegin

-- Notification channel event subscriptions. A channel fires on run
-- completion events it subscribes to; both default sensibly.
ALTER TABLE notifications ADD COLUMN notify_on_failure boolean NOT NULL DEFAULT true;
ALTER TABLE notifications ADD COLUMN notify_on_success boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notifications DROP COLUMN IF EXISTS notify_on_failure;
ALTER TABLE notifications DROP COLUMN IF EXISTS notify_on_success;
-- +goose StatementEnd
