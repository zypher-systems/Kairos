-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Admin accounts (single-admin install, table allows future expansion)
CREATE TABLE admins (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Sessions (server-side, revocable)
CREATE TABLE sessions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id   uuid NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    user_agent text,
    ip         text,
    last_seen  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Key/value settings (global defaults: TZ, retention, etc.)
CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- License (ed25519 signed)
CREATE TABLE licenses (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    license_key  text NOT NULL UNIQUE,
    payload      jsonb NOT NULL,             -- licensee, issued_at, expires_at, features
    status       text NOT NULL DEFAULT 'active', -- active | expired | invalid | trial
    activated_at timestamptz NOT NULL DEFAULT now()
);

-- Scheduled jobs
CREATE TABLE jobs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    schedule_type     text NOT NULL DEFAULT 'cron',      -- cron | interval | oneoff
    cron_expr         text NOT NULL DEFAULT '* * * * *',
    interval_seconds  integer NOT NULL DEFAULT 0,
    oneoff_at         timestamptz,
    timezone          text NOT NULL DEFAULT 'UTC',
    -- target
    target_type       text NOT NULL DEFAULT 'local',     -- local | agent | ssh | selector
    target_selector   jsonb NOT NULL DEFAULT '{}'::jsonb, -- labels for selector type
    ssh_host_id       uuid,                               -- FK added after ssh_hosts
    -- execution
    exec_mode         text NOT NULL DEFAULT 'shell',     -- shell | docker
    docker_image      text NOT NULL DEFAULT '',
    docker_command    jsonb NOT NULL DEFAULT '[]'::jsonb,
    shell_script      text NOT NULL DEFAULT '',
    workdir           text NOT NULL DEFAULT '',
    env_plain         jsonb NOT NULL DEFAULT '{}'::jsonb,
    env_secrets       jsonb NOT NULL DEFAULT '{}'::jsonb, -- encrypted values
    -- policy
    retry_count       integer NOT NULL DEFAULT 0,
    retry_base_delay_ms integer NOT NULL DEFAULT 1000,
    retry_max_delay_ms  integer NOT NULL DEFAULT 30000,
    timeout_seconds   integer NOT NULL DEFAULT 3600,
    concurrency_policy text NOT NULL DEFAULT 'allow',    -- allow | forbid | replace
    enabled           boolean NOT NULL DEFAULT true,
    tags              text[] NOT NULL DEFAULT '{}',
    workflow_id       uuid,                               -- FK added after workflows
    -- schedule engine state
    next_run_at       timestamptz,
    last_run_at       timestamptz,
    -- timestamps
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_jobs_next_run ON jobs (next_run_at) WHERE enabled AND next_run_at IS NOT NULL;
CREATE INDEX idx_jobs_workflow ON jobs (workflow_id);

-- Execution runs
CREATE TABLE runs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id        uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    attempt       integer NOT NULL DEFAULT 1,
    trigger       text NOT NULL DEFAULT 'schedule',     -- schedule | manual | retry | workflow
    status        text NOT NULL DEFAULT 'queued',       -- queued | claimed | running | succeeded | failed | canceled | skipped
    target_type   text NOT NULL DEFAULT '',
    target_id     text NOT NULL DEFAULT '',
    schedule_time timestamptz NOT NULL DEFAULT now(),
    queued_at     timestamptz NOT NULL DEFAULT now(),
    claimed_at    timestamptz,
    started_at    timestamptz,
    finished_at   timestamptz,
    exit_code     integer,
    error         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_runs_job_created ON runs (job_id, created_at DESC);
CREATE INDEX idx_runs_status ON runs (status) WHERE status IN ('queued','claimed','running');

-- Run log chunks
CREATE TABLE run_logs (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    seq        bigint NOT NULL,
    stream     text NOT NULL DEFAULT 'stdout',           -- stdout | stderr | system
    data       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, seq)
);

-- Work-queue claims (DB-backed; lease-based for crash safety)
CREATE TABLE queue_claims (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id           uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    target_type      text NOT NULL,
    target_id        text NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    claim_version    integer NOT NULL DEFAULT 0,
    claimed_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id)
);
CREATE INDEX idx_queue_claims_open ON queue_claims (target_type, target_id, lease_expires_at);

-- Agents (polling workers on remote hosts)
CREATE TABLE agents (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL,
    token_hash        text NOT NULL UNIQUE,
    hostname          text NOT NULL DEFAULT '',
    labels            jsonb NOT NULL DEFAULT '{}'::jsonb,
    capabilities      jsonb NOT NULL DEFAULT '{}'::jsonb, -- docker: bool, os, arch, mem...
    version           text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'online',     -- online | offline
    last_heartbeat_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- SSH hosts (server pushes via SSH)
CREATE TABLE ssh_hosts (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    text NOT NULL,
    host                    text NOT NULL,
    port                    integer NOT NULL DEFAULT 22,
    username                text NOT NULL DEFAULT 'root',
    auth_type               text NOT NULL DEFAULT 'key',  -- key | password
    private_key_encrypted   bytea,
    password_encrypted      bytea,
    host_key_fingerprint    text NOT NULL DEFAULT '',
    verify_host_key         boolean NOT NULL DEFAULT true,
    last_seen_at            timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE jobs ADD CONSTRAINT fk_jobs_ssh_host
    FOREIGN KEY (ssh_host_id) REFERENCES ssh_hosts(id) ON DELETE SET NULL;

-- Workflows (DAG) + edges
CREATE TABLE workflows (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE jobs ADD CONSTRAINT fk_jobs_workflow
    FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE SET NULL;

CREATE TABLE workflow_edges (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    from_job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    to_job_id   uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    condition   text NOT NULL DEFAULT 'success',          -- success | failure | always
    UNIQUE (workflow_id, from_job_id, to_job_id, condition)
);

-- Notification channels
CREATE TABLE notifications (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    type       text NOT NULL,                             -- webhook | smtp | discord | slack
    config     jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notifications CASCADE;
DROP TABLE IF EXISTS workflow_edges CASCADE;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS fk_jobs_workflow;
DROP TABLE IF EXISTS workflows CASCADE;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS fk_jobs_ssh_host;
DROP TABLE IF EXISTS ssh_hosts CASCADE;
DROP TABLE IF EXISTS agents CASCADE;
DROP TABLE IF EXISTS queue_claims CASCADE;
DROP TABLE IF EXISTS run_logs CASCADE;
DROP TABLE IF EXISTS runs CASCADE;
DROP TABLE IF EXISTS jobs CASCADE;
DROP TABLE IF EXISTS licenses CASCADE;
DROP TABLE IF EXISTS settings CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS admins CASCADE;
DROP EXTENSION IF EXISTS pgcrypto;
-- +goose StatementEnd