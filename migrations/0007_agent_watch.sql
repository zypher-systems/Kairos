-- +goose Up
-- +goose StatementBegin

-- Agents can watch their own filesystems: a job's file trigger can point at
-- an agent (watch_target='agent') with watch_agent_id identifying it.
ALTER TABLE jobs ADD COLUMN watch_agent_id uuid REFERENCES agents(id) ON DELETE SET NULL;

-- Library retention: auto-prune blobs older than the configured age
-- (0 = keep forever). Stored in settings; no schema change needed.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE jobs DROP COLUMN IF EXISTS watch_agent_id;
-- +goose StatementEnd
