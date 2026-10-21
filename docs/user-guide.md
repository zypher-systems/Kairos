# Kairos user guide

Version 1.14.0. For day-two operations (backup, upgrade, failure modes, config reference), see [runbook.md](runbook.md).

## What this is

Kairos is a self-hosted scheduler and mover of files. You define jobs in the UI. The server enqueues runs and executes them on the server itself, on a polling agent, or over SSH — and orchestrates SFTP file transfers between hosts.

## Install (Compose)

1. Copy `.env.example` to `.env`.
2. Set `KAIROS_SECRET` to a long random value (`openssl rand -hex 32`). The server will not start with an empty or example secret.
3. `make up` (or `docker compose up -d --build`).
4. Open the UI, create the admin account, sign in.

## Day-one operations

- **Today** — what needs attention, the next runs, the last 24 hours, and a setup checklist on new installs.
- **Schedule** — every enabled job on one time axis: past outcomes as dots, future firings as ticks, 24h or 7d window.
- **Jobs** — three kinds:
  - **Script** — shell on the server, an agent, or over SSH.
  - **Docker** — container runs on those targets.
  - **Transfer** — move a file between the library and an SSH host, or host → host.
- **Runs** — history, live logs, cancel, and log search.
- **Files** — the staging library. Upload payloads here to push to hosts; pull jobs land back here. The list is newest first; each row shows the full staged date and time so copies with the same name stay distinct. **View** opens text files in the page (2 MiB cap); download remains for everything else.
- **Where it runs** — the Kairos server, a named agent, an SSH host, or every agent matching labels (at least one label required).
- **SSH Keys** — managed keypairs for host auth.
- **Notifications** — webhook (HMAC), Discord, Slack, and email channels for run outcomes, including missed-run and late-run alerts.
- **Settings** — default timezone, retention, password change, users (admin), audit log, git sync, SSO, Vault secret stores, skip-day calendars, and high availability. Retention defaults to 100 runs per job for 30 days; both limits are editable.

## Storage locations

**Storage** lists the places files live. Transfers move files between them and file triggers watch them; the Kairos server is in the data path only when it has to be.

| Kind | What it is | Who moves the bytes |
|---|---|---|
| **NFS share** | An NFSv3 export on a NAS or file server, spoken to directly from Kairos: no mount, no root, nothing on the host. Give the server, the export path and the UID/GID the export should see | the server, or the agent on the other end |
| **SMB / Windows share** | A Windows, Samba or NAS share over SMB2/3 with a user name, password and optional domain, spoken to directly | the server, or the agent on the other end |
| **Mounted directory** | A directory already inside the Kairos server container: a share an operator mounted there, or its own disk | the server |
| **SFTP host** | A directory on an SSH host, using the host's stored credentials from Hosts & agents | the server, or the agent on the other end |
| **S3 bucket** | A prefix in an S3-compatible bucket (AWS, MinIO, Ceph, Wasabi…). Leave the keys empty to use the server's IAM role | the server, or the agent on the other end |
| **Agent directory** | A directory on an agent machine: its disk, or a share mounted there | that agent |

Paths in jobs are relative to a location's base path and cannot leave it. **Test** opens the location from the server and lists its base path, so a wrong bucket name or a share that is not mounted shows up before a job depends on it. Secret keys are sealed at rest and never shown again.

**NFS notes.** Kairos connects from a non-privileged port, so the export must allow that: `insecure` in `/etc/exports` on Linux, or "allow connections from non-privileged ports" on Synology, QNAP and TrueNAS. Files are read and written as the UID/GID on the location, or as the export maps them (`all_squash`, `anonuid`). NFSv4-only servers are not reachable this way; use a mounted directory for those. A server without a portmapper that serves MOUNT and NFS on one fixed port can be given that port as `host:port`.

A mounted directory is still available for shares an operator prefers to mount on the host: add a bind mount to the `server` service in Compose (for example `- /mnt/nfs/incoming:/mnt/incoming`) and give the location the container path.

## File transfers

A transfer job has a **route**: source → destination. Each end is a storage location, or the **library** (the Files page). Older jobs with an SSH host path or an agent path as an end keep working; new jobs use locations.

- **Source** is a file, or a glob such as `incoming/*.csv`, which copies every match. **Destination** is a file name, or a directory ending in `/` to keep the source names.
- **Where it runs** is derived from the ends: on the agent that owns an agent directory, otherwise on the server. A transfer between two different agents is not possible.
- **Overwrite** replaces an existing file; **Move** deletes each source only after its copy is committed; **Keep modification time** and **Verify checksum** (read the copy back and compare SHA-256) are per job.
- Every file lands atomically: a `*.kairos-part` temporary file renamed into place on directories and SFTP, a single object put on S3. A failed copy is a failed run and never deletes the source.
- **Use the files from the trigger** makes the source exactly the files the job's file trigger matched, read from the place it watches. That is how "when a file arrives, move it" is expressed: watch a location with a glob, and transfer from the trigger to wherever the files belong.

The **library** is still there for small payloads (scripts, configuration) pushed to hosts and for files pulled back for viewing on the Files page. Library files are stored **unencrypted** under `KAIROS_DATA_DIR/files`; set **auto-delete files older than N days** in Settings to bound them.

Transfers inherit everything jobs have: schedules, retries, timeouts, concurrency policy, live logs on the run page.

## File triggers

Any job can arm a **file trigger** in its Trigger section: a glob such as `incoming/*.csv` on a **storage location**, or, in the older forms, an absolute glob on the server filesystem (`local`), on an SSH host (polled over SFTP) or on an agent machine (`agent`, watched by the agent itself). When the matching set changes — a new file, a changed size or mtime — and stays stable for one poll interval, the job fires with trigger `file`. Minimum poll interval: 10 seconds.

The run records which files matched. A transfer job with **Use the files from the trigger** moves exactly those files from the watched place; a script sees them in `KAIROS_TRIGGER_FILES` (one path per line, relative to the location) and `KAIROS_TRIGGER_FILE` (the first).

Notes:

- The first poll only records a baseline: files that already exist when the trigger is armed do not fire.
- A file being slowly written won't fire until its size stops changing across one interval.
- Trigger and schedule coexist — a job can run hourly *and* when a file lands.
- Watches on agent directories are pushed to the agent on heartbeat; editing the watch takes effect on the next beat.

## SSH keys

The **SSH Keys** page manages keypairs used for host authentication. Three ways to authenticate a host:

- **Paste a private key** at host creation (as before) — stored encrypted.
- **Password** — stored encrypted; fine for a first connection.
- **Managed key** — reference a key pair from the **SSH keys** page.

The recommended flow for a new host:

1. Add the host with its **password** (bootstrap only).
2. **Generate a key pair** on the **SSH keys** page (ed25519, created on the server — the private half is encrypted and never leaves it).
3. Open the host's **edit** dialog → **provision key access**: pick the key, enter the current password once. Kairos installs the public key into the host's `authorized_keys` and switches the host to `managed_key` auth.
4. Optionally disable password auth on the host.

Hosts are fully editable — name, address, port, username, auth method — from **Edit** on **Hosts & agents → SSH hosts**. Secret fields left blank keep their stored values.

You can also **import** an existing private key for hosts that already trust its public half. Keys show a host-usage count; a key in use can't be deleted until its hosts are switched to other auth.

## Runs

A run is **queued** until a worker claims it, **running** while it executes, and then **succeeded**, **failed** or **canceled**. Every claim is a lease: the worker renews it every few seconds and kills its own process if it cannot renew before the lease lapses, because by then Kairos has handed the run to another worker. **Cancel** stops the process within a few seconds on the server, on SSH hosts and on agents; a run canceled before it started never starts.

**Retries** are queued the moment an attempt fails, with the configured backoff, in the same database transaction as the failure. Workflow fan-out and failure notifications wait for the final attempt, so a job with two retries notifies once and starts its downstream jobs once.

If a worker is stopped mid-run (server or agent shutdown, or a lost lease), the run goes back to the queue with the same attempt number and starts again on the next worker; its log says so. Runs are therefore at-least-once: a job that must not run twice should be idempotent.

## Workflows

**Workflows** chain jobs into a DAG. Each **edge** links two jobs with a condition — `success`, `failure`, or `always`. When a job finishes, its downstream edges whose condition matches fire the next jobs. The **edit** dialog is a lane-based canvas: roots on the left, downstream jobs to the right; add edges between any jobs, save the whole graph at once. Jobs referenced by edges are linked to the workflow automatically.

- **Schedule the workflow, not its first job.** A workflow has its own schedule (cron, interval, once, time zone, skip calendar) and its own file trigger on a storage location. When it fires, Kairos creates one execution and queues every root job under it, exactly as **Run** does by hand, so a fan-in with two roots gets both from the same execution. Jobs inside a workflow use the **Manual** schedule type: no schedule of their own, run only by the workflow, a file trigger or by hand.
- **run** fires the root jobs (those with no incoming edges) manually — the chain then propagates by outcome.
- A workflow file trigger records the matched files on the execution: root jobs get them in `KAIROS_TRIGGER_FILES`, and a transfer job in the workflow with **Use the files from the trigger** reads from the workflow's watched location.
- Fan-out (one job → many), success chains, and failure-recovery branches all compose.
- **Fan-in** (many → one) waits for every parent in the same execution and starts the child once, and only when each parent's outcome matches its own edge: with `A —success→ C` and `B —success→ C`, C does not run when A failed and B succeeded. A parent that is being retried has not finished yet.
- Workflows honor the jobs' own schedules and file triggers too; a scheduled job in a workflow still fires its downstream edges.
- Disabling a workflow stops its edges from firing; the jobs themselves still run on their own triggers.

## Notifications

The **Notifications** page manages delivery channels for run outcomes. Four types:

- **Webhook** — POSTs the run event as JSON to your URL. Every delivery carries `X-Kairos-Delivery` (the same id on every retry of one event, for de-duplication) and `X-Kairos-Timestamp` (Unix seconds). With an HMAC secret set, two signatures are sent: `X-Kairos-Signature: sha256=<hex>` over the body, and `X-Kairos-Signature-V2: t=<timestamp>,v2=<hex>` over `"<timestamp>.<body>"`. Verify V2 and reject timestamps older than a few minutes; a captured request then cannot be replayed.
- **Discord** — rich embed via a Discord webhook URL.
- **Slack** — attachment via a Slack webhook URL.
- **Email (SMTP)** — plain-text email. `starttls` (the default) requires the relay to offer STARTTLS and fails otherwise; `ssl` connects over TLS; `none` sends in clear and is only for a relay on a trusted network.

Channels subscribe to **failure** (default) and/or **success** events; a canceled run reaches the failure subscribers as `run_canceled`. The **test** button sends a synthetic event immediately — use it before trusting a channel. Delivery is asynchronous with retries and runs per channel in parallel, so a slow endpoint holds up only its own queue. After eight failed attempts an event becomes **undelivered**: it appears at the bottom of the Alerts page with its last error until you retry or dismiss it, and `kairos_notify_dead_letters` counts it on `/metrics`. Webhook URLs, HMAC secrets, and SMTP passwords are encrypted at rest.

Job output is stored with the job's secret values replaced by `[redacted]` (values of six characters or more), on the server and on agents. A secret printed in pieces across separate writes is not caught.

Set `KAIROS_PUBLIC_URL` (e.g. `https://kairos.ops.example.com`) when Kairos is behind a reverse proxy — that host is allowed by the CSRF origin check, and it is used for clickable run links in notifications.

## Agent install

Two ways to put an agent on a host:

**From Kairos (install over SSH)** — **Hosts & agents → Add a host → Let Kairos install the agent over SSH**, then pick the SSH host. Kairos creates the identity, connects over the stored SSH credentials, downloads the binary from this server, writes a systemd unit, and starts it. The agent appears under **Hosts & agents** within seconds. Needs root or sudo on the host.

**Manually** — on any Linux host:

```bash
curl -fsSL http://kairos:8080/api/v1/agent/install.sh | sh -s -- \
  --server http://kairos:8080 --token <token> --name worker-1 --labels role=worker
```

Create the agent under **Hosts & agents → Add a host → Run one command on the machine**; it shows this command with the token filled in. The script detects the architecture (amd64/arm64), downloads from the server's `/api/v1/agent/download/...` endpoint, and installs a systemd unit. The same install script is served by the server itself — no external artifact hosting needed.

**Labels belong to Kairos, not the agent.** The labels an agent is created with (`--labels`, or the Add a host form) route work to it; edit them under **Hosts & agents**. An agent cannot change its own labels: a machine holding an agent token can only ever receive the work an administrator pointed at it. If an agent's `KAIROS_AGENT_LABELS` disagrees with the server, the server logs it and keeps its own.

**Stopping an agent.** SIGTERM stops claiming, waits `KAIROS_AGENT_DRAIN` (30s) for running jobs, then kills the rest and hands them back to the queue; another agent (or this one after restart) runs them again from the start. Job environments carry only the job's own variables plus a short allow-list (`PATH`, `HOME`, locale, proxy and Docker settings); the agent token is never visible to a job.

## Backup and restore

`scripts/backup.sh [dir]` writes a timestamped tarball containing a Postgres dump (custom format), the file library, and a manifest. `scripts/restore.sh <tarball>` drops and recreates the database and restores the library. Keep backups alongside a record of `KAIROS_SECRET` — job secrets, SSH credentials, and channel secrets are only decryptable with it.

## Accounts and roles

The first account created at setup is an **admin**. Admins can add more accounts.

Three roles:

- **admin** — settings, users, and everything operators can do
- **operator** — jobs, runs, files, targets, keys, notifications, workflows, and their own API tokens
- **viewer** — read-only, plus changing their own password

Teams are a label on the user, not a second tenancy. API tokens belong to the user who created them. A token can carry an optional **role ceiling** so automation is weaker than the owner (effective role is the weaker of owner and ceiling). Existing `kat_` tokens stay on the bootstrap admin.

Login, job changes, run cancel, settings, tokens, and user admin writes go into the **audit log** under **Settings → Audit**.

## SSO

OIDC sign-in. SAML is not included.

- Configure issuer, client id, client secret, and redirect URL under **Settings → Integrations → Single sign-on**. Redirect is typically `https://<host>/api/v1/auth/sso/callback`.
- Paste the IdP `sub` onto an existing user. Kairos **does not** create accounts from the IdP and **does not** map groups to roles — admin / operator / viewer stay in Kairos.
- Password login stays as fallback.

## History

Retention defaults to **100 runs per job** for **30 days**. Both limits are editable under **Settings**. The pruner deletes the oldest runs past those limits.

On **Runs**, search log text across all jobs or one job. Hits link to the run.

## SLA alerts

Success and failure notifications are always on. SLA is per-job.

- **Missed-run**: a scheduled fire did not start within the job’s grace window.
- **Late-run**: a run’s duration exceeded the job’s SLA. This **notifies only** — `timeout_seconds` is still the kill switch.

Empty / zero SLA fields mean off.

## Approval gates

Approval is per-job. If **Require approval** is on, every trigger waits in `pending_approval` until an admin or operator approves (then the run is queued) or rejects (skipped). A per-job timeout skips with `approval_timeout`; `0` waits forever. Cancel works on pending runs too.

Channels can notify on pending hold (`run_pending`) and on reject or timeout (`run_rejected`). Approve is silent aside from the live activity event.

YAML: optional `require_approval` and `approval_timeout_seconds`.

## Secret stores

A store is HashiCorp Vault KV v2: name, address, KV mount, optional namespace, and a token sealed with `KAIROS_SECRET`. Inline job env secrets stay available too. Operators manage stores under **Settings**. On a job, an env row can be plain, an inline secret, or **from store** (store + path + key → env name).

The server fetches at claim — the same choke point as inline `RevealJobSecrets` — and merges into the process env for local, agent, and SSH. Resolved values are never written to `runs` or logs. A missing or denied key fails the run.

YAML: `kind: SecretStore` (token apply-only, never exported) and job `secret_refs` by store name.

## Calendars

A calendar is a named list of skip days (`YYYY-MM-DD`). Operators manage them under **Settings**. A job can attach one calendar; empty means run as today.

Cron and interval fires whose date (in the **job timezone**) is on the calendar are skipped: a `skipped` run with error `calendar` is recorded and `next_run_at` still advances. One-off and manual run-now ignore the calendar.

YAML: `kind: Calendar` with `spec.dates`, and job `calendar:` by name.

## Jobs as code

YAML export, apply, and git sync. There is no in-tree CLI — Compose is the unit of deploy.

- **Jobs → Export YAML** downloads every calendar, secret store, job, and workflow as `kairos/v1` documents. Hosts, agents, files, notify channels, secret stores, and calendars are referenced by **name**, not UUID. Vault tokens and secret values are never exported.
- **Jobs → Apply YAML** upserts by `metadata.name`. Dry-run shows create/update/unchanged without writing.
- **Settings → Integrations → Git sync** clones an HTTPS repo (optional PAT) on an interval or via **Sync now**, then applies `.yaml`/`.yml` under the configured path.
- Secret **values** are not exported — only secret names. Apply keeps existing values unless the YAML map includes a new one.
- OpenAPI 3 lives at `/api/v1/openapi.json` (unauthenticated).

## Workflow parameters

A workflow can carry named strings (**parameters**). When a run is part of a workflow execution, those values are copied into the job's environment (parameters win on conflict).

## Security notes

- Put TLS in front of the server and set `KAIROS_HTTPS=true` so the session cookie is marked Secure.
- Mutating API calls with a foreign `Origin` host are rejected. Behind a reverse proxy set `KAIROS_PUBLIC_URL` to the public `https://…` name — that host is allowed even when the proxy rewrites `Host` to the backend.
- Login is rate-limited per client address.
- Job secrets and SSH keys are encrypted with `KAIROS_SECRET`. Losing that value means those secrets cannot be decrypted.
- Library blobs live under `files/` inside `KAIROS_DATA_DIR` (a Compose volume); the database holds names, sizes, and SHA-256 hashes.

## High availability

The default Compose file runs one server. Profile `ha` adds API replicas against the same Postgres (same `KAIROS_SECRET`). Only one replica is the scheduler leader. Followers stay up and serve the UI, API, and agents.

`docker compose --profile ha up -d` starts a second replica on port 8081 (host networking cannot share 8080). Put TLS/a reverse proxy in front of both ports if you want a single URL. `/api/v1/info` and Settings show whether this replica holds the lease. Failover can pause due jobs for about 15 seconds.

## Backup

`scripts/backup.sh [dir]` produces a timestamped tarball (Postgres custom-format dump + file library + manifest); `scripts/restore.sh <tarball>` drops and recreates the database and restores the library. Keep the tarball and `KAIROS_SECRET` together — job secrets, SSH credentials, and channel secrets decrypt only with it.
