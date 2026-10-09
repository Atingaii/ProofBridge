# Development Workflow

## Requirements

- Go 1.25.7+ (see `go.mod:3`)
- Node.js 22+ only when rebuilding the frontend; this matches the CI toolchain
- Codex CLI on the private Bridge machine when `bridge.runner=codex`

Android maintenance was reactivated for the lightweight host and shared phone
layout. APK builds use a separate toolchain and remain outside routine website
release scope; see [Android release](#android-release).

## Trellis for Codex

Trellis is optional development tooling, initialized at version `0.6.17` for
Codex only. It does not change the Hub, Bridge, or deployment runtime.

```bash
# User-local global installation (ensure ~/.local/bin is on PATH)
npm install -g --prefix "$HOME/.local" @mindfoldhq/trellis@0.6.17
trellis --version
trellis platforms
```

The checked-in `.trellis/`, `.agents/skills/`, and `.codex/` files are the
project setup. On a new checkout, initialize only your local developer identity:

```bash
python3 .trellis/scripts/init_developer.py your-name
python3 .trellis/scripts/get_context.py
```

In Codex, use `$trellis-start` to load project context, `$trellis-continue` to
resume a task, and `$trellis-finish-work` to finish it. Repository rules in
`AGENTS.md` override generic generated workflow instructions. The backend and
frontend spec indexes link to the existing canonical docs. Journal/task
auto-commit is disabled in `.trellis/config.yaml`; commits still follow R6.

Codex hook injection requires the user-level `features.hooks = true` setting
and, on versions requiring hook review, approval of this repository's two
hooks through `/hooks`. Installed hooks are declared in `.codex/hooks.json`.
Until approved, use `$trellis-start` to load context explicitly. Do not copy
another repository's hook trust hashes.

The accompanying `grill-me` skill from
[mattpocock/skills](https://github.com/mattpocock/skills) is installed separately
in the user's Codex skills directory, together with its `grilling` dependency.
Use `$grill-me` for a requirements interview; these user-level skills are not
bundled in this repository. New Codex turns can discover the installed skills.

## Additional Administrators

The bootstrap username remains an administrator. To grant the same privileges
to additional existing accounts, set the YAML-only `auth.admin_usernames` list
in the Hub configuration, for example `admin_usernames: [operator]` under
`auth`, then restart the Hub. The default is `[]`. Entries are trimmed and
matched case-insensitively by exact username; empty entries are ignored.
Only list intended existing accounts: the name also authorizes a future account
created with that username. Remove an entry and restart to revoke its additional
grant. Existing sessions use the new policy immediately; refresh the browser to
reload administrator navigation. No password reset or Bridge upgrade is needed.

## Environment Variables

`/install.sh` detects Linux or macOS, amd64 or arm64. Build all four artifacts
sequentially with `GOMAXPROCS=2 make bridge-downloads`; place other-platform
binaries under `downloads/` beside the Hub executable. Native Mac uses the
existing nohup background launcher (no automatic restart after reboot), and
supports review-required or auto-execute; strict-workspace requires Linux. See
[macOS installation](features/macos-installation.md),
[Linux ARM64 installation](features/linux-arm64-installation.md) and
[deployment instructions](deployment.md#linux-arm64-endpoints).

This table is the single detailed source for environment variables. README files
should list names and point here for detail.

| Variable | Purpose | Default/source |
| --- | --- | --- |
| `APP_ENV` | Selects `configs/<env>.yaml` | `dev` |
| `CODEX_BRIDGE_CONFIG_DIR` | Alternate config directory | `configs` |
| `APP_HOST` | Hub listen host | config `gateway.host` |
| `APP_PORT` | Hub listen port | config `gateway.port` |
| `HUB_DB_PATH` | SQLite database path | config `hub.db_path` |
| `HUB_COOKIE_SECURE` | Force secure auth cookie | config `hub.cookie_secure` |
| `HUB_BRIDGE_DOWNLOAD_URL` | Optional external CLI binary URL; `{os}` + `{arch}` select linux/darwin and amd64/arm64; legacy `{arch}`-only URLs stay Linux-only and literals Linux amd64-only; empty uses Hub downloads | config `hub.bridge_download_url` |
| `HUB_BROWSER_LEASE_TTL` | How long Hub keeps a chat `sid` leased after the last browser WebSocket closes | config `hub.browser_lease_ttl` |
| `JWT_SECRET` | JWT signing secret, 32+ bytes | config `auth.jwt_secret` |
| `HUB_USERNAME` | Bootstrap/default username | config `auth.bootstrap_username` |
| `HUB_PASSWORD` | Bootstrap/default password | config `auth.bootstrap_password` |
| `REGISTRATION_ENABLED` | Enable self-service registration; Turnstile remains required by default | config `auth.registration.enabled` |
| `REGISTRATION_REQUIRE_TURNSTILE` | Require Turnstile for registration; default `true`, set `false` only for an isolated trusted trial | config `auth.registration.require_turnstile` |
| `TURNSTILE_SITE_KEY` | Public Cloudflare Turnstile Managed widget site key | config `auth.registration.turnstile_site_key` |
| `TURNSTILE_SECRET` | Server-only Cloudflare Turnstile Siteverify secret | config `auth.registration.turnstile_secret` |
| `TURNSTILE_HOSTNAME` | Expected production hostname returned by Siteverify | config `auth.registration.turnstile_hostname` |
| `BRIDGE_HUB_URL` | Hub URL used by Bridge | config `bridge.hub_url` |
| `BRIDGE_TOKEN` | Bridge enroll token | config `bridge.token` |
| `BRIDGE_TOKEN_FILE` | File containing Bridge token | config `bridge.token_file` |
| `BRIDGE_NAME` | Agent display name | config `bridge.name` |
| `BRIDGE_CWD` | Default workspace path for runner | config `bridge.cwd` |
| `BRIDGE_RUNNER` | `echo`, `codex`, `codex-app-server`, or `acp` | config `bridge.runner` |
| `BRIDGE_ACP_CLI` | ACP runner CLI selection: `claude` or `codex` | config `bridge.acp.cli` |
| `BRIDGE_ACP_CLAUDE_COMMAND` | Command that launches the Claude Code ACP adapter | config `bridge.acp.claude_command` |
| `BRIDGE_ACP_CODEX_COMMAND` | Command that launches the Codex ACP adapter | config `bridge.acp.codex_command` |
| `BRIDGE_ACP_PREFER_NATIVE_RESUME` | Offer a local native `resume` command for ACP sessions | config `bridge.acp.prefer_native_resume` |
| `BRIDGE_CODEX_PATH` | Codex CLI path used by Bridge | config `bridge.codex_path` |
| `BRIDGE_CLAUDE_PATH` | Claude Code CLI path used by orchestration | config `bridge.claude_path` |
| `BRIDGE_MODEL` | Model argument for Codex runner | config `bridge.model` |
| `BRIDGE_SANDBOX` | Codex sandbox policy | config `bridge.sandbox` |
| `BRIDGE_APPROVAL_POLICY` | Codex approval policy | config `bridge.approval_policy` |
| `BRIDGE_STRICT_WORKSPACE` | Enable fail-closed Linux Landlock isolation for managed CLI process trees | config `bridge.strict_workspace` |
| `BRIDGE_STRICT_WORKSPACE_READ_ONLY` | OS path-list of additional non-standard toolchain roots exposed read-only in strict mode | config `bridge.strict_workspace_read_only` |
| `CODEX_BRIDGE_RUNTIME_DIR` | Local base directory for strict-mode private homes and temporary files; defaults to `$XDG_RUNTIME_DIR/codex-bridge` or `~/.codex-bridge/runtime` | Bridge process environment |
| `BRIDGE_DURABLE_TASK_GRAPH` | Enable the bounded persistent orchestration task graph; set `false` only for legacy Bridge compatibility | config `bridge.durable_task_graph` |
| `BRIDGE_TBCI_ENABLED` | Allow the selectable TBCI mode; supported Bridge builds prepare their managed artifact by default (`true`) | config `bridge.tbci_enabled` |
| `BRIDGE_TBCI_PATH` | Optional administrator-supplied TBCI executable; empty uses the managed embedded artifact | config `bridge.tbci_path` |
| `BRIDGE_TBCI_TIMEOUT` | Maximum duration for one TBCI verification; default `2m` | config `bridge.tbci_timeout` |
| `BRIDGE_LONG_COMMAND_OBSERVER_ENABLED` | Enables Bridge long-command observer notes during orchestration | config `bridge.long_command_observer.enabled` |
| `BRIDGE_LONG_COMMAND_OBSERVER_AFTER` | Duration before a matching long command is observed, for example `2m` | config `bridge.long_command_observer.after` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | config `observability.log_level` |
| `LOG_FORMAT` | `console` or `json` | config `observability.log_format` |

## Local Smoke Flow

```bash
cp configs/dev.yaml.example configs/dev.yaml
# edit configs/dev.yaml: auth.jwt_secret must be 32+ bytes

/usr/local/go/bin/go run . user --username admin --password 'change-me'
/usr/local/go/bin/go run . enroll
# copy the printed token to bridge.token in configs/dev.yaml

/usr/local/go/bin/go run . hub
/usr/local/go/bin/go run . bridge
```

Open `http://127.0.0.1:8088`.

Browser self-registration is disabled by default. Use
`/usr/local/go/bin/go run . user --username <name> --password <password>` to
create or update local test accounts. To test normal registration, create a Cloudflare
Turnstile Managed widget, configure `enabled: true`, `require_turnstile: true`,
and the three Turnstile values, and use a
hostname allowed by that widget. The Hub validates each browser token with
Siteverify and rejects unavailable, expired, replayed, wrong-action, and
wrong-hostname results before creating a user. Never commit the secret.

## CLI Install Flow

`POST /api/bridge-tokens` returns `setupCommand` for the settings UI. It is a
single copyable shell line that runs `/install.sh` and then starts the Bridge;
`installCommand` and `connectCommand` are the same flow split into two commands
for manual fallback. Users must run the generated command from the target
workspace as the same OS user that runs Codex CLI and Claude Code. The connect
command prefers a restartable `systemd --user` service, falls back to `nohup`
when user systemd is not available, writes logs under
`~/.codex-bridge/logs/`, and keeps a per-working-directory machine id under
`~/.codex-bridge/machines/`. The setup command clears the per-directory log
before starting, stops any existing same-directory user service, refuses to
continue unless `codex` and `claude` are resolvable in the shell that runs the
command, and only prints `codex-bridge connected` after the Bridge logs
`[bridge] connected`; otherwise it prints recent log lines for diagnosis. It
downloads the Bridge binary into a temporary file using bounded, backed-off
resume attempts over HTTP/1.1, so transient TLS/edge interruptions do not
replace the existing executable or restart the transfer from zero. It
then exits; the following `link` starts or restarts only the endpoint for the
selected working directory. The installer itself never scans, stops, or
restarts other linked endpoints on the same machine. This keeps active runs in
other directories connected while a new endpoint is linked or a selected
endpoint is repaired. The normal workflow therefore remains the same two
install/connect commands. It
preserves `HOME`, `PATH`, `CODEX_HOME`, Claude config location variables,
resolved `BRIDGE_CODEX_PATH` / `BRIDGE_CLAUDE_PATH`, common model credential
variables such as `OPENAI_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, and
`ANTHROPIC_API_KEY`, and common proxy environment variables in
`~/.codex-bridge/services/<cwd-hash>.env` so background services keep the same
CLI, native history location, local model credentials, and Hub connectivity as
the shell that ran the setup command. Generated user services include
`OOMPolicy=continue`; if a heavy child process is killed by the kernel OOM
killer, systemd should keep the Bridge service up so Hub can surface the
command/run status instead of seeing a Bridge restart. The token response and
settings UI intentionally do not expose sudo/root setup commands because they
write native CLI state under a different user and break native resume
expectations.

The settings UI exposes three permission profiles:

User-facing gray rollouts are configured under `hub.feature_rollouts`. Each
entry maps a registered feature key to `off`, `admin`, `all`, `percent:N`, or
`users:name1,name2`. The default `strict-workspace: all` makes the
workspace-only mode available to every account as an explicit opt-in. Existing
endpoints retain their selected profile. Invalid or unknown policies fail
closed; changing a policy requires a Hub restart.
`orchestration-plan-workspace: all` similarly prepends the bounded planner and
independent plan-review nodes once per user task and exposes the progress workspace to every
account. Existing runs preserve the topology stored in their latest task graph,
even if rollout policy later changes.
`native-user-input: all` enables the experimental orchestration question path,
subject to the connected Bridge's per-CLI native capability. The Hub checks
this rollout when starting a task and at both directions of the WebSocket
exchange. Answers return to the live native CLI call in the same run; they are
not saved as orchestration events. Unsupported CLI versions or permission modes
are shown as unavailable in the orchestration workspace. See
[native structured user input](features/native-structured-user-input.md).

`official-codex-worker-models: all` permits native official Codex model catalog
queries and per-worker model/effort overrides. The enhanced leading frontend
also requires its build flag and a Bridge with `configSwitcher.version >= 3`.
Parameter-free official requests remain compatible with older endpoints. See
[official Codex worker models](features/official-codex-worker-models.md).

Completion verification follows the endpoint's existing execution profile;
auto-execute may create compiler outputs, while read-only stays read-only.
Strict mode retains its outer workspace boundary without a conflicting nested
Codex sandbox. Upgrading Hub alone cannot update verifier code on remote
Bridges: reinstall and restart/reconnect those clients after their active tasks
finish. The same applies to native model capture for Codex `exec --json`
verification. Historical verifier slot/model recovery runs on Hub statistics
reads and needs no remote restart or conversation replay.
Independent checks run serially and honor cancellation while queued.
Plans remain guidance only; automatic early endings show completed with separate
check records. The Hub normalizes new incoming legacy `stalled` errors to this
result policy; stored historical runs are not rewritten.
See [independent early completion](features/independent-early-run-completion.md).

- `review-required`: starts Bridge with `--runner codex-app-server --sandbox
  workspace-write --approval-policy untrusted`. Codex chat and Codex
  orchestration approval requests are shown in the browser and answered through
  run-scoped approval frames. Claude Code orchestration uses a temporary MCP
  permission tool so browser approval cards appear on the orchestration
  timeline. Single `cat`, `coqc`, and batch `coqtop` commands are automatically
  accepted only when their executable, options, and file paths pass the strict
  workspace allowlist; shell composition, redirection, expansion, and path
  escape still require browser review. Hub-managed orchestration requires the
  selected Bridge connection to expose both direct Claude Code and Codex CLI
  capabilities.
- `strict-workspace`: starts Bridge with `--runner codex --sandbox
  workspace-write --approval-policy never --strict-workspace`. Codex, Claude
  Code, ACP adapters, and every command they start run without browser approval
  inside a Linux Landlock boundary. The bound workspace and per-workspace CLI
  state are writable. Standard system/runtime trees, including `/proc` and
  `/sys`, non-home mount branches, hidden home entries, and executable
  components explicitly exported through the saved shell `PATH` are read-only.
  Other unrecognized ordinary home directories and shared `/tmp` are
  invisible. `link` merges the invoking shell and default login-shell PATH, so
  this rule works on a first-time machine without product-name or directory
  layout detection. Strict children preserve real `HOME` for shell/tool-manager
  initialization but use private Codex/Claude state, cache, and temporary paths.
  Once the Bridge applies Landlock, strict-mode Codex exec, resume, and
  app-server requests use Codex `dangerFullAccess` only to suppress its nested
  Bubblewrap layer. This cannot widen the inherited Landlock ruleset, and it
  avoids `/proc/sys/kernel/overflowuid` and nested-container initialization
  failures. Other permission profiles keep their existing Codex sandbox policy.
  A local environment inside another ordinary project is not borrowed unless
  its component `bin` was deliberately exported in the linked endpoint PATH.
  Set `BRIDGE_STRICT_WORKSPACE_READ_ONLY` to an OS path-list only when a custom
  shared toolchain lives elsewhere. Landlock ABI 3 or newer is required; startup fails
  closed on unsupported kernels and non-Linux hosts.
- `auto-execute`: starts Bridge with `--runner codex --sandbox
  danger-full-access --approval-policy never`, preserving the previous
  browser-first trusted-machine behavior. Claude Code orchestration maps this
  profile to Claude's bypass permission mode and, when Bridge itself runs as
  root, sets `IS_SANDBOX=1` only on the managed Claude child process so users do
  not need to edit global Claude settings. Bridge-launched child processes
  inherit the captured service environment before these runner-specific
  additions are applied.

Existing endpoints in the settings UI can be expanded to generate a repair
command. The repair command mints a fresh enroll token, installs the latest
Bridge binary, and reconnects with the endpoint's existing machine id, name, and
first known working directory so older endpoints do not accidentally register as
new agents. Because this is a directory-scoped `link`, it restarts only that
endpoint; do not run it on an endpoint that has an active task unless that task
is intentionally being interrupted.

An enroll token's expiry is the deadline for its first successful machine
binding. After binding, the same machine id may use that credential for normal
Bridge reconnects even after the deadline; another machine remains rejected,
and deleting the endpoint revokes the binding. The endpoint selector reports
the version and connection time of the active in-memory Bridge socket so an
online Hub is not mistaken for an online CLI endpoint.

Native structured orchestration questions use protocol v3. Deploy the Hub,
Bridge, and embedded UI from the same build, reconnect the Bridge after an
upgrade, and reload already open browser pages. The Hub starts new native
question runs only when the page passes `nativeUserInputClientVersion=3` and
the selected Bridge advertises v3. Each browser WebSocket must declare
`user_input_sync.clientVersion=3` before it can receive or answer active
questions. An older page can retain its ordinary timeline but cannot silently
participate in the new question flow. For an ended run, an authorized v3
browser may still sync bounded delivery receipts read-only while the same
Bridge instance retains them. Claude Code question support is separately
gated by its minimum CLI version and permission mode. Codex >= 0.147.0 and
Claude Code >= 2.0.71 are accepted, including later releases; this is a
compatibility policy, not a claim that each release has been tested. See
[native structured user input](features/native-structured-user-input.md).

The correlated query-error recovery fix is additive within v3: deploy its
matching Hub and UI together; an already v3-capable Bridge does not need an
upgrade for this fix. Reload the page after deployment. The UI can still time
out conservatively against an older Hub, but cannot display a correlated
rejection reason until the Hub includes `syncId`.

Codex 0.156.1 has two different native question paths: synchronous
`item/tool/requestUserInput` and asynchronous `agentMessage` with
`delivery=async`. The latter is exposed only to a root agent when that model's
catalog advertises the experimental async tool. A ready Bridge capability
does not prove the model invoked either tool; inspect the run's native request
stage before diagnosing a missing card. Async answers stay in the same Codex
thread even if its original turn has finished.

For deterministic tests use `bridge.runner=echo`. For real Codex:

```yaml
bridge:
  runner: codex
  cwd: /path/to/workspace
  sandbox: danger-full-access
  approval_policy: never
```

Orchestration long-command observation is opt-in. When enabled, commands whose
text matches `command_patterns` produce explicit Bridge-note rows after the
configured delay. Claude Code can receive a tagged stream-input note when the
stream-input side channel is active; Codex currently records the same note in
the browser timeline without injecting stdin.

Orchestration native context compaction is also opt-in from the browser
orchestration panel. The `after-turn` setting is persisted per run. Codex uses
the app-server `thread/compact/start` native RPC after successful business
turns; Claude Code stream-json is skipped with an info Bridge note until a
verified native control channel exists. Maintenance output is tagged as Bridge
notes and is not forwarded as handoff evidence.

```yaml
bridge:
  long_command_observer:
    enabled: true
    after: 2m
    command_patterns:
      - "python -m slow_build"
    applies_to:
      - claude
      - codex
```

## Frontend Build

The Go binary serves `internal/web/static/`. If you edit `frontend/`, rebuild
that output before final verification:

```bash
cd frontend
npm run build
cd ..
```

Then run Go verification:

```bash
/usr/local/go/bin/go test ./...
CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .
```

Desktop recovery/history checks use a local server serving the freshly built
assets. The browser scripts intercept API and WebSocket calls with fixtures;
they do not send model tasks. From `frontend/`:

```bash
npm test
npm run test:stats
DESKTOP_FIXES_TEST_URL=http://127.0.0.1:4173 npm run test:desktop-workspace-browser
DESKTOP_FIXES_TEST_URL=http://127.0.0.1:4173 npm run test:stats-browser
DOCUMENTATION_TEST_URL=http://127.0.0.1:4173 npm run test:documentation-browser
```

Use a leading build to exercise the running-task composer as well. Set optional
`DESKTOP_FIXES_SCREENSHOT_DIR` to retain desktop verification images. The help
check without `DOCUMENTATION_TEST_URL` runs only its source assertions.
Existing native-question and live-input browser suites remain separate gates;
see their `USER_INPUT_TEST_URL` and `LIVE_INPUT_TEST_URL` inputs in
`frontend/user-input-browser-check.mjs` and `frontend/live-input-browser-check.mjs`.

`frontend/vite.config.ts` stamps the frontend build time into the assets. The
help/update pages label that separately from the same-origin `/health` response
and historical release notes; a Hub version does not identify the frontend or
private Bridge build.

## Portable Package Build

For a copy-to-server bootstrap that does not require Go or Node on the target
machine, build the Linux amd64 portable archive:

```bash
make portable-package
```

This calls `scripts/build-portable-package.sh`, writes
`dist/codex-bridge-<version>-linux-amd64.tar.gz`, and includes
`deploy/portable/start.sh`, `stop.sh`, `status.sh`, package-local config, and
the static `codex-bridge` binary. On the target server:

```bash
tar -xzf codex-bridge-<version>-linux-amd64.tar.gz
cd codex-bridge-<version>-linux-amd64
ADMIN_USERNAME=admin ADMIN_PASSWORD='change-me' APP_HOST=0.0.0.0 APP_PORT=8088 ./start.sh
```

The script initializes `data/`, `state/`, `logs/`, and `run/` inside the
extracted directory. It defaults to `BRIDGE_RUNNER=echo`; set
`BRIDGE_RUNNER=codex` and `BRIDGE_CWD=/path/to/workspace` only after Codex CLI
is installed and authenticated for that OS user.

## Documentation Check

```bash
make doc-lint
```

The doc linter checks required docs, basic code anchors, and environment
variable references. It is intentionally lightweight and should not replace
review.

## Commit Messages

```text
<type>(<scope>): <short summary>

Change summary:
- <path>: <what changed>

Exit gate:
- [x] <test or manual verification>

Doc-Impact: none
```

Allowed `type`: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `chore`.

Use a path list instead of `none` when docs changed or should change:

```text
Doc-Impact: docs/dev-workflow.md, AGENTS.md
```

## Security Notes

- Do not commit `configs/*.yaml`; only `configs/*.yaml.example` belongs in git.
- Do not commit real enroll tokens, JWT secrets, OpenAI keys, or private host
  names.
- Put private ticket/security scan notes in `docs/private/`; that directory is
  ignored by git.
- Do not log passwords, API keys, enroll tokens, or browser cookies.

## Automatic model catalog

Hub uses `https://models.dev/api.json` as a public reference directory, refreshes
it at startup and every six hours, and caches the last valid response in
`model-catalog.json` beside `hub.db_path`. This file contains public metadata
only. No extra configuration is needed. Failed refreshes retain the last good
cache and compiled fallbacks. The stats page shows the catalog timestamp and
labels reference estimates; provider billing is retained. New model support
follows upstream catalog publication and still requires a successful provider
probe. Unknown prices/capabilities are not invented. See
[ADR-021](adr/021-refreshable-model-catalog.md).

Historical planning usage recovery requires the private Bridge build containing
`internal/bridge/usage_recovery.go:recoverPlanningUsageSessions`. Update the Bridge
when its running tasks finish. With capability `usageModelRecovery: 2`, reconnect
and completed-run stats reads automatically retry missing planning identities,
even if other sessions synced before. Failed native discovery retries after six
hours; the existing usage-sync action allows manual retry. Native logs must still exist. The Hub update alone
cannot reconstruct a model when the old invocation omitted its native session ID.

## Leading website build

`VITE_LEADING=true npm run build` in `frontend/` enables new dynamic runs and
the runtime instruction composer. The default build retains the bounded UI.
Both builds use relative API/WebSocket URLs, routed to the same experimental
Hub. Preserve the complete previous static output (including lazy chunks)
before replacing it. Deployment topology and data retention are described in
[deployment](deployment.md#leading-and-experimental-websites).

For the official Codex A/B model and model-supported reasoning selectors, use
`VITE_LEADING=true VITE_OFFICIAL_CODEX_MODELS=true npm run build`.
`VITE_OFFICIAL_CODEX_MODELS` is a frontend build-time flag, not a Hub or Bridge
runtime environment variable. The existing builds without this flag retain
their current selectors. The Hub rollout and compatible Bridge are also required.

## Android release

Android is a thin online host for `https://codex.sparkon.cn`; it shares the phone
website and packages neither website assets nor a browser engine. Design and
host boundaries: [ADR 028](adr/028-lightweight-android-webview.md),
[mobile feature](features/android-mobile-redesign.md).

Use JDK 21 and the minimal Android SDK (API 36, build-tools 35.0.0; platform-tools
only for device verification). `android/gradle/wrapper/gradle-wrapper.properties`
pins the Gradle distribution. No emulator is needed for building.

| Variable | Meaning |
| --- | --- |
| `JAVA_HOME` | JDK 21 directory for Android compilation |
| `ANDROID_HOME` | Installed minimal Android SDK directory |
| `ANDROID_SIGNING_PROPERTIES` | Private external Properties file with `storeFile`, `storePassword`, `keyAlias`, `keyPassword` |

Keep the signing file and keystore outside the repository with owner-only access
and persistent backups. Never print passwords, commit signing material or pass
passwords as command-line arguments. Reuse the same certificate for later
updates; an unavailable previous certificate prevents a promised in-place
upgrade. Do not uninstall an existing app automatically.

```bash
# Set the three variables above in your local environment first.
./scripts/build-android-release.sh
```

The script runs release unit tests, lint, shrinking and assembly with one Gradle
worker, then verifies the signed APK, its contents/5 MiB budget and SHA256.
Check available disk/memory/load first and run it separately from Vite, Go and
browser tests. Android builds no longer run Capacitor sync or rebuild the website.
CI uploads a signed build artifact on explicit dispatch; it does not publish a
GitHub Release or store listing. Configure the four repository secrets
`ANDROID_RELEASE_KEYSTORE_BASE64`, `ANDROID_RELEASE_STORE_PASSWORD`,
`ANDROID_RELEASE_KEY_ALIAS`, `ANDROID_RELEASE_KEY_PASSWORD` for
`.github/workflows/android-release.yml`. These are CI signing inputs, separate
from Hub/Bridge configuration.

Device acceptance must cover login, same-context continuation, file selection,
copying, external links, downloads, back/IME, offline retry and renderer recovery.
Record Android/system-WebView versions, installed/cache footprint, and host plus
renderer PSS/RSS using `adb shell dumpsys meminfo`. Test idle, long conversation,
graph/detail view and background return. Missing device measurements remain
unverified; APK bytes and Java heap do not prove runtime-memory compliance.
