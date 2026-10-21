-- +goose Up
-- +goose StatementBegin

-- Managed SSH keypairs. Private halves are encrypted at rest like host
-- credentials. Hosts can reference a managed key (auth_type = 'managed_key')
-- or keep using an inline pasted key (auth_type = 'key').
CREATE TABLE ssh_keys (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name             text NOT NULL,
    public_key       text NOT NULL,              -- OpenSSH authorized_keys line
    private_key_enc  bytea NOT NULL,             -- PEM, encrypted with KAIROS_SECRET
    fingerprint      text NOT NULL DEFAULT '',   -- SHA256:... of the public key
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- Which key a host uses (nullable; only set for managed_key auth).
ALTER TABLE ssh_hosts ADD COLUMN ssh_key_id uuid REFERENCES ssh_keys(id) ON DELETE SET NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ssh_hosts DROP COLUMN IF EXISTS ssh_key_id;
DROP TABLE IF EXISTS ssh_keys;
-- +goose StatementEnd
