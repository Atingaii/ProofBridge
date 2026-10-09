# Deployment Guide

ProofBridge is a single Go binary with two modes:

- **Hub** — the public server. Serves HTTPS/WSS (behind a reverse proxy),
  embeds the web UI, and stores history in SQLite.
- **Bridge** — runs on the machine that has Codex CLI / Claude Code installed.
  It reverse-connects to the Hub over WebSocket and runs the selected runner.

```text
Browser ──WSS──> Hub (public) <──reverse WS── Bridge (your workstation) ──> Codex / Claude
```

You always run **one Hub** and **one or more Bridges**. This guide covers the
common ways to deploy both.

## Default deployment scope

自 2026-09-29 起，按用户要求，日常“部署最新版”默认只更新
`https://proofbridge.sparkon.cn` 网站。构建最新前端和服务器本机架构的 Hub
二进制（页面嵌入 Go 程序，因此仍需该单平台构建），完成备份、Hub 更新、
公网健康检查与页面资源校验。

默认不执行 `make bridge-downloads`，不构建、发布或验收四个平台下载包；
已有跨平台下载文件保留，不能宣称它们随网站同步升级。原生架构下载路由直接
提供 Hub 二进制，会随 Hub 替换而更新。仅用户明确要求发布端点安装包时，
才按所需平台执行下方的端点发布流程。

网站部署不自动更新或重启 Bridge。若修复需要新版 Bridge，应报告该依赖及
实际生效范围，并在用户明确要求后单独更新；不能把端点升级或全平台发布
重新加入网站部署前置条件。保留原站点配置、数据库、身份及回滚备份。

## Prerequisites

| Component | Requirement |
| --- | --- |
| Build the binary | Go 1.25.7+ (see `go.mod:3`) |
| Build the web UI | Node.js 22+ (matches CI; only needed when building from source; releases ship the UI embedded) |
| Run a Bridge | Codex CLI and/or Claude Code installed and authenticated for the OS user |
| Production Hub | A reverse proxy that terminates TLS (Caddy config is provided) |

The web UI is compiled into the binary (`internal/web/static`). When you build
from source you must build the frontend first (`make frontend` or `make
build-all`); otherwise the embedded assets are stale.

## Option A — From source (development / single host)

```bash
git clone https://github.com/Atingaii/ProofBridge.git
cd ProofBridge

# 1. Config
cp configs/dev.yaml.example configs/dev.yaml
# Edit configs/dev.yaml: set auth.bootstrap_password and a strong auth.jwt_secret.

# 2. Create a login user and an enroll token
go run . user --username admin --password 'change-me'
go run . enroll
# Put the printed token into configs/dev.yaml under bridge.token

# 3. Run Hub and Bridge (two terminals)
make run-hub      # or: go run . hub
make run-bridge   # or: go run . bridge
```

Open <http://127.0.0.1:8088>.

## Option B — Build a binary with make

```bash
make build-all        # builds the frontend, then the Go binary -> bin/codex-bridge
./bin/codex-bridge hub
./bin/codex-bridge bridge
```

Useful targets (`make help` lists them all):

| Target | Purpose |
| --- | --- |
| `make frontend` | Build the web UI into `internal/web/static` |
| `make build` | Build the Go binary (assumes the UI is already built) |
| `make build-all` | `frontend` + `build` |
| `make portable-package` | Build a copy-to-server tarball under `dist/` |
| `make install` | Copy the binary to `$(PREFIX)/bin` (default `/usr/local/bin`) |
| `make test` | Run the Go test suite |
| `make doc-lint` | Validate docs |

## Option C — Portable package

Use this when a fresh Linux server should only need an archive upload, extract,
and one start command. The package includes the static binary, package-local
config, and scripts that initialize admin credentials, SQLite state, a same-host
Bridge token, logs, and pid files under the extracted directory.

Build the archive on the source machine:

```bash
make portable-package
# -> dist/codex-bridge-<version>-linux-amd64.tar.gz
```

Version tags publish the same archive, its checksum, and the standalone
`codex-bridge-linux-amd64` endpoint binary on GitHub Releases. The stable
standalone download URL is:

```text
https://github.com/Atingaii/ProofBridge/releases/latest/download/codex-bridge-linux-amd64
```

Copy the archive to the target server and run:

```bash
tar -xzf codex-bridge-<version>-linux-amd64.tar.gz
cd codex-bridge-<version>-linux-amd64
ADMIN_USERNAME=admin ADMIN_PASSWORD='change-me' APP_HOST=0.0.0.0 APP_PORT=8088 ./start.sh
```

Open `http://<server-ip>:8088` and log in with the admin credentials. If
`ADMIN_PASSWORD` is omitted on first start, `start.sh` generates one and writes
it to `state/admin_password`.

The portable package defaults the same-host Bridge to `BRIDGE_RUNNER=echo` so a
new server can be verified without Codex or Claude installed. For real CLI use,
install and authenticate the target CLI as the same OS user, then start with
overrides such as:

```bash
PUBLIC_URL='https://bridge.example.com' \
BRIDGE_RUNNER=codex \
BRIDGE_CWD=/srv/workspace \
./start.sh
```

Operational commands:

```bash
./status.sh
./stop.sh
```

Runtime files remain inside the extracted directory: `data/`, `state/`,
`logs/`, `run/`, and `work/`. Use the systemd + Caddy path below when you need
host-level service management and TLS automation.

## Option D — Docker

A multi-stage `Dockerfile` builds the UI, compiles the binary, and ships a
minimal distroless image.

```bash
# Build (also available as: make docker)
docker build -t codex-bridge:local .

# Run the Hub (mount your config and a data volume)
docker run --rm -p 8088:8088 \
  -e APP_ENV=prod \
  -v "$PWD/configs:/opt/codex-bridge/configs:ro" \
  -v codex-bridge-data:/opt/codex-bridge/data \
  codex-bridge:local hub
```

The image `ENTRYPOINT` is the binary and the default `CMD` is `hub`; pass
`bridge` (or any subcommand) to change the mode. Provide config via a mounted
`configs/prod.yaml` or via the environment variables listed below.

Notes:

- A Bridge usually runs on your own workstation next to Codex/Claude, so the
  container path mainly suits the Hub. To run a Bridge in a container you must
  also make the Codex/Claude CLIs and their credentials available inside it.
- The runtime image is non-root (`distroless ... :nonroot`); ensure mounted
  volumes are writable by that user.

## Option E — Production (systemd + Caddy)

This mirrors the `sparkapi.tech` setup using the assets in `deploy/`.

```bash
# 1. Install the binary
make build-all
sudo make install                       # -> /usr/local/bin/codex-bridge

# 2. Lay out config + data
sudo mkdir -p /opt/codex-bridge/configs /opt/codex-bridge/data
sudo cp configs/dev.yaml.example /opt/codex-bridge/configs/prod.yaml

# 3. Reverse proxy (edit the domain in deploy/Caddyfile first)
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy

# 4. systemd units (edit BRIDGE_HUB_URL / domain as needed)
sudo cp deploy/systemd-hub.service /etc/systemd/system/codex-bridge-hub.service
sudo cp deploy/systemd-bridge.service /etc/systemd/system/codex-bridge-bridge.service
sudo systemctl daemon-reload
sudo systemctl enable --now codex-bridge-hub
sudo systemctl enable --now codex-bridge-bridge
```

Set these in `/opt/codex-bridge/configs/prod.yaml` for production:

- `gateway.host: 127.0.0.1`
- `gateway.port: 8088`
- `hub.cookie_secure: true`
- `auth.jwt_secret`: a fresh 32+ byte secret
- `auth.registration.enabled: false` unless a production Turnstile Managed
  widget has been created and all registration fields are configured
- `bridge.hub_url: https://<your-domain>`
- `bridge.token` or `bridge.token_file`: the enroll token

Caddy terminates HTTPS and proxies the Hub on `127.0.0.1:8088`. If a CDN
(e.g. Cloudflare) already points the domain at the host, Caddy still issues and
serves the certificate to the origin.

### Recommended endpoint flow (Settings page)

For a real endpoint you usually do **not** hand-edit the Bridge config. After
logging into the Hub, open **Settings**, create a CLI token, and copy the single
install-and-connect command. Run it from the workspace as the same OS user that
runs Codex/Claude. It installs and starts a `systemd --user` service, preserves
`HOME`, `PATH`, `CODEX_HOME`, Claude config vars, resolved CLI paths, model
credentials, and proxy variables, and only reports success after the Bridge logs
`[bridge] connected`. See the README "Commands" section for `link` / `connect` /
`bridge` details.

### Isolated Cloudflare Variant

Use this layout when trialing a major orchestration change without sharing the
current Hub service or SQLite database. The isolated ProofBridge deployment is
served at `https://proofbridge.sparkon.cn`; it must not reuse the live
`sparkon.cn` binary, configuration directory, database, JWT secret, systemd
units, or Bridge enrollment token.

1. Build the embedded frontend and binary with the low-resource settings used
   by this host: `GOMAXPROCS=2 make frontend`, then
   `GOMAXPROCS=2 CGO_ENABLED=0 go build -p 1 -ldflags "-s -w" -o bin/codex-bridge .`.
2. Install the binary at `/opt/proofbridge-isolated/codex-bridge`, create
   `/etc/proofbridge-isolated/prod.yaml`, and set `gateway.host: 127.0.0.1`,
   `gateway.port: 8090`, `hub.db_path: /var/lib/proofbridge-isolated/proofbridge.db`,
   `hub.cookie_secure: true`, and a fresh `auth.jwt_secret`.
   To allow accounts on this isolated trial while Cloudflare Turnstile is not
   provisioned, set `auth.registration.enabled: true` and
   `auth.registration.require_turnstile: false`. Leave all Turnstile fields
   empty. This exception is limited to the isolated hostname; the primary
   deployment retains the default `require_turnstile: true`.
3. Create a new administrator and enrollment token using only
   `CODEX_BRIDGE_CONFIG_DIR=/etc/proofbridge-isolated APP_ENV=prod`. Do not
   copy credentials from the existing deployment. Store the token in the
   isolated Bridge unit or its token file.
4. Create separate `proofbridge-isolated-hub.service` and
   `proofbridge-isolated-bridge.service` units. The Bridge points to
   `https://proofbridge.sparkon.cn`, runs as the intended CLI user, and keeps
   its existing workspace scope.
5. Add a separate Caddy site block that reverse-proxies only
   `proofbridge.sparkon.cn` to `127.0.0.1:8090`. In Cloudflare, add a proxied
   A/AAAA record for that exact hostname in the `sparkon.cn` zone. Do not edit
   the existing `sparkon.cn` record or its Caddy upstream.
6. Verify both endpoints independently: `curl -fsS
   https://proofbridge.sparkon.cn/health` and `curl -fsS https://sparkon.cn/health`.
   Confirm the isolated Settings page shows only the new Bridge, then exercise
   Codex + Codex, Claude + Codex, and Claude + Claude with fake or low-cost
   presets before normal use.

The isolated Hub owns its own database. CLI worker profiles are additionally
materialized per run and slot, so `codex-a` / `codex-b` and `claude-a` /
`claude-b` can use different saved presets without changing the Bridge user's
global Codex or Claude configuration.

## Configuration reference

### Linux ARM64 endpoints

The same `/install.sh` command detects Linux and native macOS amd64/arm64 from
`uname`. It checks the downloaded program can execute `help` before replacing
an existing binary. Apple Silicon Macs use Darwin arm64; their Ubuntu guests
use Linux arm64. Mac uses the existing nohup launcher and requires restarting
the endpoint after reboot. Strict workspace isolation remains Linux-only.

Only for an explicitly requested four-platform endpoint release, build all four
artifacts sequentially from the same source and embedded UI. This is not a
prerequisite for the default website deployment:

```bash
GOMAXPROCS=2 make bridge-downloads VERSION=<release-version>
```

Install the matching Hub-host artifact as the Hub executable. Place all other
artifacts in `downloads/` beside it using `codex-bridge-<os>-<arch>` names.
For the isolated x86-64 Hub, these are Linux arm64, Darwin amd64, and Darwin
arm64 under `/opt/proofbridge-isolated/downloads/`. Stage artifacts before
restarting the Hub. Existing configurations and endpoint identities are retained.
Version tags publish all four binaries; the portable Hub archive stays Linux
amd64. See [macOS installation](features/macos-installation.md) for validation
scope and platform limits.

Verifier fixes execute on the Bridge host. When an endpoint upgrade is explicitly
requested, publish the requested platform artifacts and reinstall and
restart/reconnect the selected endpoints once their active work is finished.
Replacing a file does not update an already running
Bridge process. Preserve its existing configuration, machine identity and
enrollment; no fresh run or fresh account is required. Verify the reported
endpoint version after reconnecting. Updating Hub alone does not fix verifier
sandbox behavior in an older remote binary.

The public routes use `/downloads/codex-bridge-<os>-<arch>`, where OS is linux
or darwin and architecture is amd64 or arm64. The native route serves the Hub
executable; other routes validate ELF or Mach-O architecture before serving
the matching sibling artifact.
Missing artifacts return 404 and mismatched artifacts return 500.
An external `hub.bridge_download_url` may contain both `{os}` and `{arch}`.
Legacy `{arch}`-only URLs remain Linux-only and literal URLs Linux amd64-only;
other platforms fall back to Hub routes. See the
[configuration reference](dev-workflow.md#environment-variables).

ARM64 builds currently do not embed TBCI. Use agent verification for formal
proof runs, or explicitly configure a working native `bridge.tbci_path`.

### Common configuration

Config loads from `configs/${APP_ENV:-dev}.yaml`, then selected environment
variables override it. Set `CODEX_BRIDGE_CONFIG_DIR` to read config files from
another directory. Common overrides:

- `APP_HOST`, `APP_PORT`, `APP_ENV`
- `HUB_DB_PATH`, `HUB_COOKIE_SECURE`, `HUB_BROWSER_LEASE_TTL`
- `JWT_SECRET`, `HUB_USERNAME`, `HUB_PASSWORD`
- `REGISTRATION_ENABLED`, `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET`,
  `TURNSTILE_HOSTNAME`, `REGISTRATION_REQUIRE_TURNSTILE`
- `BRIDGE_HUB_URL`, `BRIDGE_TOKEN`, `BRIDGE_TOKEN_FILE`
- `BRIDGE_NAME`, `BRIDGE_CWD`, `BRIDGE_RUNNER`, `BRIDGE_MODEL`
- `BRIDGE_SANDBOX`, `BRIDGE_APPROVAL_POLICY`
- `LOG_LEVEL`, `LOG_FORMAT`

The full env and YAML reference (including browser session lease TTL, the ACP
runner, and the long-command observer) lives in
[dev-workflow.md](dev-workflow.md).

## Verifying a deployment

1. Hub health: `curl -fsS http://127.0.0.1:8088/health` returns `200`.
2. Bridge connection: the Bridge logs `[bridge] connected`; the Hub logs
   `bridge connected` and the endpoint appears online under Settings.
3. Open a chat session and send a prompt; confirm streamed output.
4. When registration is enabled, open the register tab and complete Turnstile
   when it is required. Confirm the new account sees no other user's endpoints,
   sessions, runs, or shares.

## Troubleshooting

- **Endpoint never shows online** — the CLI is missing from the Bridge shell, or
  the enroll token is wrong/expired. Check `~/.codex-bridge/logs/`.
- **Stale UI after a code change** — rebuild the frontend (`make frontend`) so
  `internal/web/static` is regenerated, then rebuild the binary.
- **Cookies rejected over HTTPS** — set `hub.cookie_secure: true` only when
  serving over TLS; keep it `false` for plain-HTTP local dev.
- **Registration tab is hidden** — registration fails closed unless enabled;
  when `require_turnstile: true`, both Turnstile keys must also be non-empty.
  Check the widget hostname and set `TURNSTILE_HOSTNAME` to the hostname
  Siteverify returns in production.

## Leading and experimental websites

The user-authorized leading website is `https://test.sparkon.cn`;
`https://proofbridge.sparkon.cn` uses the standard frontend. On 2026-10-03 at
06:56 UTC both frontends and their shared Hub were updated to
`v0.3.82-loading-overview-20261003`. This includes desktop fixes,
structured-input recovery, bounded on-demand history pagination,
[loading performance](features/site-loading-performance.md) query indexes,
fair batched usage reads, progressive usage overview pages, and automatic
recovery from temporary authentication outages. Standard and leading remain separate builds.

At 09:41 UTC the standard frontend was updated again to default a fresh
workspace to 6 rounds; the shared Hub then reported
`v0.3.82-default-rounds6-20261003`. The leading static frontend was retained.
This frontend-only behavior change added no database migration or Bridge
requirement; the embedded-UI Hub restart recovered in about 1 second.
Existing run budgets and user selections remain unchanged.

At 10:22 UTC a follow-up corrected New Run inheriting the last viewed run's
budget: every explicit New Run on the standard frontend now resets to 6.
Existing runs still restore their saved budgets, and manual choices remain
effective within the current draft. The shared Hub now reports
`v0.3.82-new-run-rounds6-20261003`; restart-to-health took about 2.4 seconds.
The leading static frontend and all Bridges were retained. No migration was
added.

The preceding 06:13 UTC release first built two query indexes; this took about
227 seconds before the Hub became healthy, temporarily interrupting both sites'
authentication/API access. The 06:30 follow-up added no migration and recovered
in about 2.3 seconds. The 06:56 progressive overview update also added no
migration and recovered in about 3.6 seconds. Bridges were neither upgraded
nor restarted.
Both hosts proxy `/api/*`, `/ws/*`, `/health`, `/install.sh`, and download routes
to the same experimental Hub on `127.0.0.1:8090`. SPA resources are selected by
host; do not start a second Hub on the shared SQLite database. Keep the original
config directory, JWT secret, vault material, database and Bridge identities.
Users may need to sign in on the new host because browser cookies are scoped
to a hostname; server-side users, machines, presets and histories are shared.

Build the leading frontend from `frontend/` with `VITE_LEADING=true npm run build`
before building the server's native binary; a plain build selects the standard
interface. Verify the deployed edition and frontend build time on `/help` or
`/updates`, separately from the live Hub version reported by `/health`. Retain the
previous binary, Caddy config, full static snapshot, and a SQLite online backup
before upgrade. Do not copy a live bare SQLite file or replace it with a newly
initialized database. No cross-platform download build is required.

Runtime instructions additionally require a Bridge advertising
`orchestrationLiveInput >= 1`. A website update alone cannot enable this on an
old running Bridge; obtain the user's endpoint-update authorization and handle
active runs before restarting it. Keep all Bridge configs, enroll tokens and
machine ids. Current implementation details and verification are tracked in
[dynamic input](features/dynamic-orchestration-input.md).

### Official Codex models edition

Published on 2026-10-08 with Hub version
`v0.3.83-official-codex-models-20261008`. The static root is
`/opt/proofbridge-isolated/web/leading-20261008-official-codex-models`.
The Cloudflare proxied CNAME targets `proofbridge.sparkon.cn`.
Public TLS, edition/resource hashes and cache headers, authenticated shared
users/agents/history, and same-origin WebSockets passed for all three hosts;
the retained test and standard HTML hashes were unchanged. Hub restart to
healthy took about 3 seconds with no active chats or orchestrations. The
running Bridge process was preserved; the public UI verified its old-version
upgrade gate, so this endpoint needs a separately authorized Bridge update
before explicit selections can execute.

Rollback materials, including the validated compressed SQL export, are at
`/opt/proofbridge-isolated/backups/20261008-official-codex-032533`.

The enhanced leading frontend for `https://codex.sparkon.cn` is built with
`VITE_LEADING=true VITE_OFFICIAL_CODEX_MODELS=true npm run build`. Its static
root is separate from the retained standard and test roots; API, WebSocket,
health and installer/download routes use the same Hub on `127.0.0.1:8090`.
It shares users, machines, presets and history through the existing SQLite
file and retains the existing JWT/vault configuration. Host-scoped cookies
may require signing in again on the new hostname.

Explicit official model/effort selection requires the registered
`official-codex-worker-models` rollout and a connected Bridge advertising
ConfigSwitcher version 3. Old endpoints keep the parameter-free workflow
and show an upgrade message; website deployment does not update them.
No schema migration or multi-platform endpoint release is needed. See
[official Codex worker models](features/official-codex-worker-models.md).

When disk space cannot accommodate another full database file, use a
consistent read-only SQLite transaction and gzip-compressed SQL export,
with an explicit free-space reserve and low I/O priority. Preserve the
binary, configuration, Caddy file and original static roots separately.
Validate the gzip stream before publishing. A binary/config rollback keeps
the current live database and never restores the older snapshot over it.
