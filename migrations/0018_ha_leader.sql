-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS ha_leader (
    name text PRIMARY KEY,
    holder_id text NOT NULL DEFAULT '',
    lease_expires_at timestamptz NOT NULL DEFAULT to_timestamp(0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO ha_leader (name, holder_id, lease_expires_at)
VALUES ('scheduler', '', to_timestamp(0))
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ha_leader;
-- +goose StatementEnd
