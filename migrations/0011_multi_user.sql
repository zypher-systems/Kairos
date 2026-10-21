-- +goose Up
-- +goose StatementBegin

ALTER TABLE admins
    ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'admin',
    ADD COLUMN IF NOT EXISTS team text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;

ALTER TABLE admins DROP CONSTRAINT IF EXISTS admins_role_check;
ALTER TABLE admins ADD CONSTRAINT admins_role_check
    CHECK (role IN ('admin', 'operator', 'viewer'));

ALTER TABLE api_tokens
    ADD COLUMN IF NOT EXISTS role_ceiling text;

ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_role_ceiling_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_role_ceiling_check
    CHECK (role_ceiling IS NULL OR role_ceiling IN ('admin', 'operator', 'viewer'));

CREATE TABLE IF NOT EXISTS audit_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id        uuid REFERENCES admins(id) ON DELETE SET NULL,
    actor_username  text NOT NULL,
    action          text NOT NULL,
    object_type     text NOT NULL DEFAULT '',
    object_id       text NOT NULL DEFAULT '',
    detail          text NOT NULL DEFAULT '',
    ip              text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_events_created_at_idx ON audit_events (created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_events;
ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_role_ceiling_check;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS role_ceiling;
ALTER TABLE admins DROP CONSTRAINT IF EXISTS admins_role_check;
ALTER TABLE admins DROP COLUMN IF EXISTS role;
ALTER TABLE admins DROP COLUMN IF EXISTS team;
ALTER TABLE admins DROP COLUMN IF EXISTS enabled;
-- +goose StatementEnd
