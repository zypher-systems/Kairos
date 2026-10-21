# Kairos

Self-hosted job orchestrator. A Go server schedules work and runs it on Linux hosts via polling agents or SSH — shell scripts, Docker containers, and SFTP file transfers, triggered by schedules or file arrivals.

Version **1.18.0**. Kairos is open source. Every feature ships with the binary: scheduler, storage locations (mounts, SFTP, S3, agent directories) and transfers, workflows, notifications, metrics, API tokens, extra accounts, the audit log, YAML and git sync, OIDC SSO, retention, log search, SLA alerts, approval gates, Vault secret stores, skip-day calendars, and extra API replicas.

## Requirements

- Docker and Docker Compose
- A strong `KAIROS_SECRET` (used to encrypt job secrets and SSH credentials at rest)

## Quick start

```bash
cp .env.example .env
# set KAIROS_SECRET to a long random string, e.g.  openssl rand -hex 32
make up
```

Open http://localhost:8080. First visit creates the single admin account.

```bash
make down     # stop
make logs     # follow logs
make dev      # hot-reload server (Air) on the same port
```

Nothing compiles on the host. Tests, vet, and the web build run inside containers:

```bash
make test
make vet
make fmt-check
make web-build
```

## First job

1. Sign in through the setup wizard.
2. **Jobs → New job** — pick Script, Docker, or **Transfer**.
3. Target `local` to run on the server, or add an agent or SSH host under **Hosts & agents → Add a host**.
4. **Run now** or wait for the schedule. Logs stream live on the run page.

## What jobs can do

- **Script** — shell on the server, an agent, or over SSH.
- **Docker** — `docker run` on any of those.
- **Transfer** — move a file between the **Files** library and an SSH host, or host → host (streamed through the server, never staged on disk). Overwrite and move (delete-after-copy) options; writes land via a temp file + rename.

## Triggers

- **Schedule** — cron (with timezones), interval, or one-off. The **Schedule** page plots every job on one time axis.
- **File trigger** — a job also fires when a matching file appears: a glob on the server filesystem, or on an SSH host polled over SFTP. One-interval settle window so half-written files don't fire.

## Agent

Create the agent under **Hosts & agents → Add a host → Run one command on the machine**, or run:

```bash
docker run --rm \
  -e KAIROS_SERVER_URL=http://kairos:8080 \
  -e KAIROS_AGENT_TOKEN=<token> \
  -e KAIROS_AGENT_NAME=worker-1 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  kairos-agent:latest
```

Compose profile `dev` includes an `agent` service. For real hosts, use **push-install** from the host's edit dialog, or run the server-hosted installer: `curl -fsSL http://kairos:8080/api/v1/agent/install.sh | sh -s -- --server http://kairos:8080 --token <token>` — it writes a systemd unit (amd64/arm64).

## SSH keys

Hosts authenticate three ways: a pasted private key, a password, or a **managed keypair**. On **SSH Keys** you can generate an ed25519 pair on the server (private half encrypted at rest) or import an existing private key. The host's **edit** dialog carries a **provision key access** step: pick a key, enter the current password once, and Kairos installs the public half into `authorized_keys` and switches the host to key-only auth. Hosts are fully editable — secret fields left blank keep their stored values.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `KAIROS_HTTP_PORT` | `8080` | Host port for UI + API |
| `KAIROS_PG_PORT` | `5432` | Postgres, bound to localhost |
| `KAIROS_SECRET` | — | **Required.** Rejected if empty or `change-me-please` |
| `KAIROS_HTTPS` | `false` | Set `true` behind TLS so the session cookie is `Secure` |
| `KAIROS_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `KAIROS_DATA_DIR` | `/var/lib/kairos` | File-library blobs live under `files/` here |
| `KAIROS_PUBLIC_URL` | — | Public `https://…` DNS name behind a reverse proxy. CSRF origin check, notification links, agent install |
| `TZ` | `UTC` | Server default timezone |

See [`.env.example`](.env.example). Operator docs: [docs/user-guide.md](docs/user-guide.md) · [docs/runbook.md](docs/runbook.md).

## High availability

The default Compose file runs one `server`. Profile `ha` adds replicas in front of the same Postgres (same `KAIROS_SECRET`, shared data volume). Only the leader runs the scheduler, SLA scanner, pruner, file watcher, git sync, approval timeout, and agent watchdog. Followers stay up and serve HTTP, agents, and the UI.

```bash
docker compose --profile ha up -d
```

The second replica listens on `KAIROS_HA_HTTP_PORT` (default 8081) because host networking cannot share `:8080`. Put a reverse proxy in front of both ports for a single URL. Due jobs may pause up to the lease TTL (~15s) on failover.

## License

Kairos is released under the [MIT License](LICENSE).

## Auth model

Accounts with admin, operator, and viewer roles. Session cookie (`kairos_session`, HttpOnly, SameSite=Lax). Mutating requests with a foreign `Origin` host are rejected. Agents authenticate with a bearer token, not a user session.

## Repo

- Server: [`cmd/kairosd`](cmd/kairosd)
- Agent: [`cmd/kairos-agent`](cmd/kairos-agent)
- UI: [`web/`](web)
- GitHub: https://github.com/zypher-systems/kairos
