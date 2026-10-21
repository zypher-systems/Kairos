# Changelog

## Unreleased

Kairos is open source under the MIT License. Container images publish from GitHub Actions to `ghcr.io/zypher-systems/kairos` and `ghcr.io/zypher-systems/kairos-agent` for linux/amd64 and linux/arm64. The licensing server, license keys, trials, offline license files, and every feature gate are gone. Accounts, the audit log, YAML and git sync, SSO, retention, log search, SLA alerts, approval gates, Vault secret stores, skip calendars, and extra API replicas are included. A follower stays up and serves HTTP. `KAIROS_LICENSE_KEY`, `KAIROS_LICENSE_FILE`, and `KAIROS_LICENSE_SERVER` are ignored. Existing installs drop the `license_state` and `licenses` tables on the next migration.

## 1.18.0

A new look: Dial.

- The interface is redrawn around a 24-hour clock face. Today opens on the dial: what ran, what failed, what is still to come, and a hand on now, with the next run named in the centre. Each job row carries a small dial showing when in the day the job runs.
- Colours are the Graphite palette shared with the other Zypher Systems apps: neutral greys, one blue for actions and for anything running, and green, amber and red only on status marks. Buttons are pills. Headings and numerals are set in Unbounded, text in Onest.
- Dark is the default theme. **Light** and **Match system** are in the user menu, and the button in the top bar switches between dark and light. A theme chosen before the upgrade is kept.
- One standard on every page. Page actions line up with the title. A run's status looks the same wherever it appears. Every list keeps its frequent actions as buttons and puts **Edit** and **Delete** or **Remove** in a "⋯" menu. Times use the 24-hour clock everywhere.
- Every time printed for a job follows one clock, and says which: its schedule sentence, its next runs, the Next column on Jobs and the dot on its dial all use the job's time zone. A one-off, which you pick in your browser's time zone, uses that zone throughout. Next runs were shown in the browser's time zone before, without saying so.
- The Today dial follows the wall clock, so it stays right on the days the clocks change.
- Fixed: cron, path and key fields use the monospace face as intended.

## 1.17.0

Workflows have their own schedule.

- A workflow can be scheduled (cron, interval, once, time zone, skip calendar) and can have a file trigger on a storage location. When it fires, Kairos creates one execution and queues every root job under it, the same path the **Run** button takes. Fan-in with several roots therefore shares one execution from the start.
- New job schedule type **Manual**: no schedule of its own. Jobs inside a workflow should use it so the cadence lives on the workflow; existing jobs keep their schedules until you move them.
- Workflow file triggers record the matched files on the execution: root jobs see `KAIROS_TRIGGER_FILES` / `KAIROS_TRIGGER_FILE`, and a transfer inside the workflow with "use the files from the trigger" reads from the workflow's watched location.
- The Schedule page and the Workflows list show workflow firings; jobs-as-code Workflow documents accept `schedule:` and `watch:` like jobs.
- Migration 0024 adds the schedule and trigger columns to workflows and records the trigger on executions. Nothing changes for existing workflows until a schedule is set on them.

## 1.16.1

Network shares set up from the app.

- **NFS share** and **SMB / Windows share** are storage location kinds. Kairos speaks NFSv3 and SMB2/3 directly from the server (and from agents), so a share is added on the Storage page with the server, export or share name and credentials — nothing to mount on the host, no privileged container. **Test** connects and lists the share.
- NFS: the export must accept non-privileged source ports (`insecure` on Linux, the NAS checkbox for non-privileged ports); the location's UID/GID is the identity the export sees. NFSv4-only servers still need a mounted directory.
- SMB: NTLMv2 with an optional domain; the password is sealed at rest.
- Migration 0023 widens the location kind constraint. Mounted directories, SFTP, S3 and agent directories are unchanged.

## 1.16.0

Storage locations: Kairos moves files between the places they already live instead of staging them on its own disk.

### Upgrade notes
- Existing transfer jobs keep working unchanged. Their SSH-host and agent-path ends are shown as "older style" in the editor; new jobs use storage locations.
- Existing file triggers keep working. New triggers can watch a storage location, and the run they queue records which files matched.
- Where a transfer runs is now derived from its ends (an agent that owns one, else the server). A client that sends `target_type` for a transfer job is ignored on that field.
- Agents older than 1.16 still handle library ↔ agent-path transfers; upgrade them for everything else.
- Migration 0022 adds `storage_locations`, `jobs.watch_location_id` and `runs.trigger_files`.

### Storage locations (Storage page, `/api/v1/storage-locations`)
- Four kinds: a mounted directory on the server (NFS/SMB share or disk), an SFTP host directory, an S3-compatible bucket prefix (AWS, MinIO, Ceph, Wasabi; static keys or the server's IAM role), and a directory on an agent machine.
- Paths in jobs are relative to the location's base path and cannot escape it. **Test** opens the location and lists it. Secret keys are sealed at rest; jobs-as-code refers to locations by name.

### Transfers
- Any location to any location, plus the library on either side. Globs copy every match; a destination ending in `/` keeps source names.
- Transfers touching an agent directory run on that agent, which opens SFTP and S3 itself: bytes no longer pass through the Kairos server unless the library is one end.
- Per-job options: keep modification time, verify checksum (SHA-256 read-back).
- Every file is committed atomically and the source is removed only after its copy is in place. A run log names every file with its size and checksum.

### File triggers
- A trigger can watch a storage location. When it fires, the run records the matched files: a transfer with **Use the files from the trigger** copies exactly those, and a script gets them in `KAIROS_TRIGGER_FILES` (one per line) and `KAIROS_TRIGGER_FILE` (the first).
- Agent-side watches report the matched files too.

### Internals
- New `internal/storage` package (local, SFTP, S3 backends behind one interface; streaming copies with checksums) shared by the server and the agent. The S3 client is minio-go.

## 1.15.1

Operations: what the server tells you, and what it does on its own while you are not looking.

### Upgrade notes
- SMTP channels with `tls: starttls` (the default) now fail when the relay does not offer STARTTLS, instead of silently sending in clear. A relay on a trusted network without STARTTLS needs `tls: none` on the channel.
- A setting that does not parse (`KAIROS_QUEUE_LEASE=30`, `KAIROS_DB_MAX_CONNS=lots`) stops the server at boot. Before, it was ignored and the default used.
- `KAIROS_HEARTBEAT_TIMEOUT` must be at least twice `KAIROS_QUEUE_LEASE`.
- Secrets that cannot be decrypted (a changed `KAIROS_SECRET`) fail the run with that reason. Before, the run started with the variable empty.
- New: `KAIROS_LOG_FORMAT`, `KAIROS_METRICS_TOKEN`, `KAIROS_DB_MAX_CONNS`. `KAIROS_RETENTION_RUNS` (never read) is gone.

### Notifications
- Claimed outbox rows are locked in the database, so several replicas can run the notifier without duplicate sends.
- Channels deliver in parallel (up to eight at once); a dead endpoint delays only its own channel.
- After eight failed attempts an event is kept as an undelivered dead letter, listed on the Alerts page with its last error, retryable or dismissable, and counted on `/metrics`. Before, it was deleted.
- Webhooks send `X-Kairos-Delivery` (stable per event across retries) and `X-Kairos-Timestamp`, plus `X-Kairos-Signature-V2` over `timestamp.body` for replay protection. `X-Kairos-Signature` is unchanged.
- Canceled runs are reported as `run_canceled`, not `run_failed`.

### Observability
- `KAIROS_LOG_LEVEL` is honoured (it was ignored) and `KAIROS_LOG_FORMAT=json` is available. The access log is at `info`, with probes and agent polls at `debug`.
- `/metrics` grew from four database counts to run, scheduler, notifier, agent, HTTP and connection-pool metrics, maintained in-process; optional bearer-token auth.
- `/healthz` reports `degraded` when the leader's scheduler has not ticked in 30 seconds.

### Scheduler and retention
- One job whose enqueue fails no longer blocks the whole batch: each job is a savepoint. A job whose next run cannot be computed is disabled with a log line instead of silently never firing again.
- The pruner never deletes queued, running or pending-approval runs, deletes in batches of 5000, and also clears expired sessions, stale cancel signals and empty workflow executions.

### Secrets
- Job output is redacted: the job's secret values (6+ characters) become `[redacted]` in stored and streamed logs, on the server and on agents.
- The git-sync token travels in git's environment as an HTTP header, not in the clone URL on the command line.

### Config and pool
- Connection pool defaults to 16 connections with lifetime and idle limits; HTTP keep-alives idle out after two minutes; request headers are capped at 64 KiB.

## 1.15.0

Runs are correct under failure. This release changes nothing about what jobs can do; it changes what happens when a worker, the server or the network fails while they run.

### Upgrade notes
- Upgrade the server before the agents. Old agents keep working: they renew leases without an epoch and never get a lease from the server, so they fall back to their own `KAIROS_AGENT_LEASE_RENEW`. Upgrade them to get self-fencing, result retries and drain.
- Agent labels are now set only in Kairos (Hosts & agents, or when the agent is created). `KAIROS_AGENT_LABELS` on the agent is reported and logged when it differs, never applied. Check that every agent's labels in Kairos are the ones you want before upgrading: an agent whose labels lived only in its environment keeps the labels Kairos already shows.
- API tokens can no longer be created with an API token, and a token's role ceiling is capped at the role of whoever creates it. Existing tokens are unchanged. New tokens default to a 90-day expiry in the UI; the API keeps "never" unless `expires_in_days` is sent.
- New settings: `KAIROS_SHUTDOWN_GRACE` (30s), `KAIROS_DISPATCHER_CONCURRENCY` (8), `KAIROS_AGENT_DRAIN` (30s). `KAIROS_QUEUE_LEASE` now has a 5s minimum.

### Execution
- **Cancel stops the process.** Cancelling a local or SSH run kills it within a few seconds; before, the UI said canceled while the job ran to the end. A run canceled between claim and start never starts.
- **Lost leases are fenced.** A worker that cannot renew a run's claim within the lease kills the run itself; the run is already on its way to another worker. A superseded worker's late result is rejected. This also fences two HA replicas that both run `local` jobs.
- **Shutdown drains.** SIGTERM stops claiming, waits `KAIROS_SHUTDOWN_GRACE` for in-flight local and SSH runs while the API stays up, then kills the rest and returns them to the queue with the same attempt number. The leader lease is released only after that. Before, every run was killed at once and re-ran after restart with no record of why.
- **Agent results always land.** The agent reports a result until the server accepts it or says the claim is stale, with backoff, on its own context, so a blip or a server restart no longer loses a finished run and re-executes it. The agent drains on SIGTERM like the server (`KAIROS_AGENT_DRAIN`) and releases what it cannot finish.
- **Agent runs are `running`.** An agent claim now marks the run running with `started_at`, so late-run SLAs, durations and the running gauge include agent runs and missed-run alerts stop firing for runs that are executing.
- **Retries are transactional.** The next attempt is queued in the same transaction as the failure, with its backoff already set, so a crash between the two cannot lose it and no worker can claim it early. Workflow fan-out and failure notifications wait for the final attempt.
- The "replace" concurrency policy tells the agent to kill the replaced run instead of orphaning it.

### Workflows
- Fan-in is serialized per execution: two parents finishing together no longer skip the child or start it twice.
- A join requires every incoming edge's condition to match its parent's outcome; retried parents count as unfinished. Retries keep the workflow execution id.

### Logs
- Output is stored in batches (one insert per ~250ms per run instead of one per write), never splits a multibyte character across chunks, and survives binary output: invalid UTF-8 becomes U+FFFD and NUL bytes are dropped. Before, such output made Postgres reject the chunk, and an agent would resend the rejected batch forever and lose every later line of that run.
- An agent drops a batch the server rejects for good and retries transient failures three times.

### Security
- Agents cannot set their own labels, so an agent token cannot route other agents' work (and its secrets) to itself.
- Job processes on agents get the same environment allow-list as on the server: `KAIROS_AGENT_TOKEN` is no longer inherited by jobs.
- Docker jobs receive variables by name (`-e NAME`) on the server, on agents and over SSH; secret values no longer appear in `ps` or `docker inspect`.
- API tokens: no minting via token, ceiling capped at the creator's role, optional expiry.
- Agent uploads into the file library require a claimed transfer run for that agent and are capped at 2 GiB.

### File transfers
- A transfer whose final rename into place fails is now a failed run and never deletes the source. Before, it logged a warning, reported success and could delete the source with the destination still at `*.kairos-part`. With overwrite on, the rename replaces an existing file atomically where the SFTP server supports posix-rename.

## 1.14.0

### Upgrade notes — read before deploying
- **Licenses from before 1.14 (`kairos_license_v1_…`) stop working.** Before upgrading a licensed install, get its new license: a license key (`zsl1.kairos.…`) for installs that can reach licensing.zyphersystems.com, or an offline license file for its Install ID (shown under Settings → License) for air-gapped installs. After the upgrade, add it under **Settings → License**, or set `KAIROS_LICENSE_KEY` / `KAIROS_LICENSE_FILE`.
- **Removed settings:** `KAIROS_LICENSE_PUBKEY`, `KAIROS_LICENSE_CHECKIN`, `KAIROS_LICENSE_CHECK_EVERY`, `KAIROS_LICENSE_EMAIL`. Leftover values are ignored. `make self-license` and `tools/licensegen` are gone.
- An install with a license key makes an outbound HTTPS call to licensing.zyphersystems.com when activated and about once a day after. A Community install or one with an offline file makes none.

### Licensing
- zsl2 licensing: license key with daily lease renewal, free 14-day trial by email, offline license files for air-gapped installs, and removal releases the install's seat.
- Paid features come only from tokens signed by the production Kairos key (kid `5f20f3a135d06fcf`), compiled in. The runtime key override and the unsigned check-in path are gone.
- The licensing server unreachable: Kairos runs on its saved lease, then a grace period, then Community. A rejected, revoked or expired license drops to Community at once.
- Licenses are re-checked every minute on every replica, so a lapse or renewal takes effect without a restart.
- Settings → License: key, trial and offline-file flows, grace and lapsed states, the last answer from the licensing server, and exactly what is sent.

### When a license lapses
Nothing is deleted and renewing restores everything.
- Only the first administrator can sign in; other accounts return with the license.
- Runs older than 30 days are hidden, not deleted (the pruner follows the saved settings, never the license).
- Jobs that require approval keep waiting for approval; they never run unreviewed.
- Jobs that read from a secret store fail with a "needs a license" error instead of running without those variables.
- Skip calendars pause (the Jobs page says so); git sync pauses and says so; an extra HA replica reports unhealthy on `/healthz`.


## 1.13.0

### Upgrade notes — read before deploying
- **Behind a reverse proxy, set `KAIROS_TRUSTED_PROXIES`** to the proxy's IP or CIDR. Kairos no longer believes `X-Forwarded-For` / `X-Real-IP` from anyone else. Without it, every user appears to come from the proxy's address, so the login rate limit (10 attempts a minute per address) is shared by everyone.
- **`KAIROS_HTTPS` now reaches the server** (Compose passed it through before, but only in `.env`, where it was ignored). Set it to `true` only when users open Kairos over HTTPS: session cookies become `Secure` and HSTS is sent. Leave it `false` for plain-HTTP installs or browsers will drop the session cookie.
- **License check-in is off by default** (since 1.12.1). A license pasted in Settings or set with `KAIROS_LICENSE_KEY` keeps working with no server contact.
- **SSO sign-ins in progress** during the upgrade must be restarted; the pending sign-in now lives in a cookie instead of server memory.

### Interface
- New Tempo design in light and dark (System / Light / Dark in the user menu): labelled sidebar, ⌘K / Ctrl+K search, keyboard shortcuts, license banner, mobile navigation.
- Today page with a setup checklist, the next runs, and what needs attention.
- Jobs list led by the schedule in plain English; the job editor is a full page with inline validation and an unsaved-changes guard.
- Runs table and a run page that loads the whole log (it used to stop at 200 chunks), with Run again, a line filter and download.
- Hosts & agents with an "Add a host" guide (one-command install, install over SSH, SSH only).
- Settings split into pages, and a License page that states what is sent to the licensing server.
- Every delete, revoke, cancel and reject asks first. Pro settings are shown disabled with a Pro badge instead of hidden, and are never wiped on save.

### Security
- Webhook, Slack, Discord and SMTP destinations are checked at connect time (no DNS-rebinding bypass), redirects are not followed, and CGNAT, NAT64, `0.0.0.0/8` and other special ranges are blocked. Email headers can no longer be injected through job names or addresses.
- Forwarding headers are trusted only from `KAIROS_TRUSTED_PROXIES`. The login limiter's memory is bounded.
- HSTS when `KAIROS_HTTPS=true`.
- Pushing an SSH key to a host verifies the host's pinned key before sending the password. First-use pinning cannot be overwritten by a racing connection.
- High availability: a leader whose lease renewals stall stops acting as leader before another replica can take over; replicas apply migrations one at a time.
- SSO works across replicas and only completes in the browser that started it (login CSRF).

### Fixes
- A lapsed license no longer blocks editing jobs that use paid settings; the stored values are kept.
- Fonts are no longer inlined as `data:` URLs, which the Content-Security-Policy blocked.
- The e2e suite runs again (Origin header, one shared session, stable selectors).

## 1.12.1
- License check-in defaults off, and a check-in answer never deletes a stored license. Earlier releases deleted a pasted license on every check-in because the licensing server answers keyless check-ins with `community`; the pruner then cut run history to Community limits.
- Editing an SSH host keeps its pinned host key.
- Local jobs get an allowlisted environment instead of the server's (which included `KAIROS_SECRET` and the database URL).

## 1.12.0
- File library lists files newest first with the staged date and time, and opens text files in a viewer (up to 2 MiB).
