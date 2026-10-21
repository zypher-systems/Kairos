# Kairos operator runbook

Day-two operations for a production Kairos install. Audience: whoever owns the box Kairos runs on.

## Service layout

| Piece | What it is | Where its state lives |
|---|---|---|
| `server` | Go binary: UI, API, scheduler, dispatcher, notifier | `KAIROS_DATA_DIR` (file library), process env |
| `postgres` | job/run/log/settings/agent/host/channel storage | `kairos-pg` volume |
| `agent` (optional) | remote worker on a target machine | nothing local — it polls the server |

Everything else (jobs, runs, files, keys, channels, workflows, admin) is in Postgres.

## Boot sequence

`kairosd` on start: loads config → connects Postgres → applies embedded migrations (goose) → takes or follows the scheduler lease → starts dispatcher and notifier (every replica) → starts scheduler/SLA/pruner/watcher/watchdog/git sync/approval timeout only on the leader → serves HTTP. A replica that is not leader stays up and serves HTTP.

Migrations are forward-only and idempotent; a fresh volume initializes fully on first boot.

## High availability

Default Compose is one `server`. Profile `ha` adds `server-2` on `KAIROS_HA_HTTP_PORT` (8081). Same database, same `KAIROS_SECRET`, shared `kairos-data` volume. Lease TTL is ~15s; the holder renews every ~5s and releases on shutdown. `/healthz` and `/api/v1/info` report `leader` and `instance_id`. `kairos_ha_leader` is on `/metrics`.

## Configuration reference

| Variable | Default | Notes |
|---|---|---|
| `KAIROS_HTTP_PORT` | `8080` | UI + API port |
| `KAIROS_HTTP_ADDR` | `:8080` | listen address (host-networking) |
| `KAIROS_DB_URL` | compose default | Postgres DSN |
| `KAIROS_DATA_DIR` | `/var/lib/kairos` | file-library blobs under `files/`. Shares used as mount locations are separate bind mounts on the `server` service |
| `KAIROS_SECRET` | — | **required**; encrypts job secrets, SSH creds, channel secrets |
| `KAIROS_HTTPS` | `false` | set `true` behind TLS: the session cookie becomes `Secure` and HSTS is sent. Compose now passes it through (it was ignored before 1.13) |
| `KAIROS_TRUSTED_PROXIES` | — | comma-separated IPs/CIDRs of your reverse proxies. Only these may set `X-Forwarded-For` / `X-Real-IP`; otherwise the connection's own address is used for the login rate limit and audit log |
| `KAIROS_PUBLIC_URL` | — | public `https://…` name behind a reverse proxy. CSRF origin check + notification/agent URLs |
| `KAIROS_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `KAIROS_LOG_FORMAT` | `text` | `text` \| `json` (one JSON object per line for log shippers) |
| `KAIROS_METRICS_TOKEN` | — | when set, `/metrics` requires `Authorization: Bearer <token>` |
| `KAIROS_DB_MAX_CONNS` | `16` | connection pool size. Raise it with many agents or a high dispatcher concurrency |
| `KAIROS_QUEUE_LEASE` | `30s` | how long a worker may go without renewing a run's claim before the run is handed to another worker. Minimum `5s`. Workers renew at a third of it and kill their own run if they cannot renew within it |
| `KAIROS_HEARTBEAT_TIMEOUT` | `90s` | an agent that has not heartbeated for this long shows offline |
| `KAIROS_DISPATCHER_CONCURRENCY` | `8` | local and SSH runs executing at once on this replica (1–256) |
| `KAIROS_SHUTDOWN_GRACE` | `30s` | on SIGTERM, how long in-flight local and SSH runs may finish before they are killed and returned to the queue |
| `KAIROS_DEVELOPMENT` | `false` | debug logging and dev-only behaviour; never set in production |
| `TZ` | `UTC` | server default timezone |

A setting that does not parse (a duration without a unit, a non-integer) stops the server at boot with the setting named, instead of running on the default.

Agent variables: `KAIROS_SERVER_URL`, `KAIROS_AGENT_TOKEN` (required), `KAIROS_AGENT_NAME`, `KAIROS_AGENT_HEARTBEAT` (`15s`), `KAIROS_AGENT_MAX_CONCURRENT` (`2`), `KAIROS_AGENT_DRAIN` (`30s`: how long SIGTERM waits for running jobs before they are killed and released back to the queue). `KAIROS_AGENT_LABELS` is only reported: labels are set in Kairos when the agent is created and edited under Hosts & agents.

## Observability

**Access log.** Every request is logged at `info` with method, path, status, bytes, duration, client IP and request id; probes (`/healthz`, `/metrics`) and the agents' claim/heartbeat polls log at `debug`. Set `KAIROS_LOG_FORMAT=json` for a log shipper.

**`/healthz`** answers `healthy`, or 503 with `unhealthy` (database unreachable) or `degraded` (this replica is leader and the scheduler has not completed a tick in 30s). Followers answer `healthy`. The body carries `leader`, `instance_id` and, on the leader, `scheduler_tick_age_seconds`.

**`/metrics`** (Prometheus text format; protect it with `KAIROS_METRICS_TOKEN` on a public listener):

| Metric | Meaning |
|---|---|
| `kairos_runs_started_total{target}` / `kairos_runs_finished_total{status}` | run throughput on this replica |
| `kairos_runs_requeued_total{reason}` / `kairos_runs_fenced_total` | runs returned to the queue on shutdown, and runs killed because their claim was lost. Non-zero fenced means workers lost their database or lease |
| `kairos_scheduler_ticks_total{outcome}` / `kairos_scheduler_last_tick_timestamp_seconds` / `kairos_scheduler_enqueued_total` | scheduler health on the leader; alert when `time() - last_tick > 60` on the leader |
| `kairos_runs_queued`, `kairos_runs_running`, `kairos_runs_day_succeeded`, `kairos_runs_day_failed` | queue depth and 24h outcomes (database counts) |
| `kairos_notify_outbox_pending` / `kairos_notify_dead_letters` / `kairos_notify_delivered_total{type}` / `kairos_notify_failed_total{type}` | notification pipeline; alert on dead letters > 0 |
| `kairos_agents_online` | agents that heartbeated within `KAIROS_HEARTBEAT_TIMEOUT` |
| `kairos_http_requests_total{method,class}` / `kairos_http_request_duration_seconds_sum|count{class}` | API traffic and latency |
| `kairos_db_pool_connections{state}` | pool `total`, `idle`, `acquired`, `max`; acquired at max means `KAIROS_DB_MAX_CONNS` is too small |
| `kairos_ha_leader`, `kairos_info{version}` | leadership and build |

## Routine operations

**Backup.** `scripts/backup.sh [dir]` → timestamped tarball (Postgres dump + file library + manifest). Run it on a cron *outside* Kairos (or as a Kairos job). Keep the tarball **and** `KAIROS_SECRET` together — without the secret, job/SSH/channel credentials in the dump can't be decrypted.

**Restore.** `scripts/restore.sh <tarball>` — drops and recreates the database, restores the library. Then `docker compose restart server`.

**Upgrade.** `docker compose pull && docker compose up -d` (or rebuild). Migrations run automatically on boot; check logs for `goose:` lines. Backup first.

**Stopping the server.** SIGTERM stops claiming new local and SSH runs, gives the in-flight ones `KAIROS_SHUTDOWN_GRACE` to finish while the API stays up, then kills the rest and puts them back in the queue with the same attempt number; a restart (or another replica) runs them again from the start. A job that must not run twice needs to be idempotent, or long enough `KAIROS_SHUTDOWN_GRACE` to finish. Agents behave the same with `KAIROS_AGENT_DRAIN`. The run log records when this happens.

**What a lost lease means.** A worker that cannot renew a run's claim within `KAIROS_QUEUE_LEASE` (the database was unreachable, the process stalled) kills the run itself; the server has already requeued it for another worker. Cancelling a run kills the process within a few seconds on every target type.

**Rotate the admin password.** Settings → password. Sessions are server-side and revocable; changing the password does not log out existing sessions — have the operator sign back in.

## Failure modes & what to check

| Symptom | First checks |
|---|---|
| Jobs not firing | `docker compose logs server` — scheduler lines; job `enabled`; `next_run_at` in the job row; this replica `leader` on `/api/v1/info` |
| Runs stuck `queued` | dispatcher logs; agent online?; target selector matches a live agent? |
| Agent offline | agent machine can reach `KAIROS_SERVER_URL`?; token still valid (re-issue if the agent was deleted) |
| NFS location fails to test | the export refuses non-privileged source ports: add `insecure` to the export (Linux) or enable non-privileged ports on the NAS; `MNT3ERR_ACCES` means the Kairos server's IP is not in the export's allowed clients; permission errors on files mean the location's UID/GID (or the export's squash mapping) cannot write there |
| SMB location fails to test | NTLMv2 only (no Kerberos, no guest); check domain\user; SMB1-only servers are not supported |
| Transfers failing | Storage → **Test** on each location; the run log names the file and the failing step; destination directory perms; overwrite flag. A mount location needs the share bind-mounted into the `server` container at the path given |
| Notifications silent | channel **test** button; enabled + subscribed event; the **Undelivered** list at the bottom of Alerts (retry or dismiss); server logs `notify:`; `kairos_notify_dead_letters` |
| `/healthz` says `degraded` | leader's scheduler stalled: logs `scheduler tick failed` / `scheduling job failed`; a job that cannot compute its next run is disabled with a log line (`cannot compute next run; disabling job`) — fix its timezone or expression and re-enable it |
| A run failed with `a stored secret cannot be decrypted` | `KAIROS_SECRET` changed since the secret was saved. Restore the old value, or re-enter the job's secrets |
| Push-install fails at download | `KAIROS_PUBLIC_URL` set / reachable from the target? |
| WebSocket reconnecting | reverse proxy buffering; `/api/v1/ws` needs upgrade headers passed through |
| `cross-origin request rejected` | set `KAIROS_PUBLIC_URL=https://your.dns.name` (and `KAIROS_HTTPS=true` behind TLS); recreate the server. Proxies often rewrite `Host` to localhost |

## Security posture

- Accounts with roles (viewer, operator, admin), argon2id, server-side sessions (7-day, sliding), login rate-limited per IP.
- CSRF: mutating requests with a foreign `Origin` host are rejected. `KAIROS_PUBLIC_URL` is the allowed public host when a reverse proxy rewrites `Host`.
- `KAIROS_SECRET` encrypts at rest: job env secrets, SSH private keys/passwords, notification channel secrets (AES-GCM).
- Host keys: TOFU fingerprint on first contact, verified on subsequent connections.
- Library files are **unencrypted** — bound exposure with Settings → file-library retention.
- Agent tokens are bearer tokens, shown once; re-issue on the Targets page.
