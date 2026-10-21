-- +goose Up
-- +goose StatementBegin

ALTER TABLE admins
    ADD COLUMN IF NOT EXISTS oidc_subject text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS admins_oidc_subject_uidx
    ON admins (oidc_subject) WHERE oidc_subject <> '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS admins_oidc_subject_uidx;
ALTER TABLE admins DROP COLUMN IF EXISTS oidc_subject;
-- +goose StatementEnd
