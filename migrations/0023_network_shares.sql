-- +goose Up
-- +goose StatementBegin

-- NFS exports and SMB shares are storage locations Kairos speaks to
-- directly, without a mount on the host.
ALTER TABLE storage_locations DROP CONSTRAINT IF EXISTS storage_locations_kind_check;
ALTER TABLE storage_locations ADD CONSTRAINT storage_locations_kind_check
    CHECK (kind IN ('mount', 'nfs', 'smb', 'sftp', 's3', 'agent'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE storage_locations DROP CONSTRAINT IF EXISTS storage_locations_kind_check;
ALTER TABLE storage_locations ADD CONSTRAINT storage_locations_kind_check
    CHECK (kind IN ('mount', 'sftp', 's3', 'agent'));
-- +goose StatementEnd
