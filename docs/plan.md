# ProofBridge Plan

Native Mac installation: platform downloads and cross-builds are implemented;
actual Mac install/link validation remains an operator exit gate. See
[macOS installation](features/macos-installation.md).

## Goal

Let a single user talk from any browser to Codex CLI running on a private machine.

## Resource Budget

- One public Hub process and one private Bridge process
- SQLite with a single open DB connection
- One reverse WebSocket between Bridge and Hub
- Native browser UI embedded with `go:embed`
- No React/Vite runtime, Redis, Postgres, queue, vector store, or file projection

## Phases

| Phase | Target | Status |
| --- | --- | --- |
| P0 | Echo runner over reverse WebSocket | implemented |
| P1 | `codex exec --json` runner | implemented |
| P2 | Multiple `sid` sessions over one Bridge connection | implemented |
| P3 | SQLite users, agents, sessions, messages | implemented |
| P3.1 | Agent-scoped chat session spaces | implemented |
| P4 | Cookie JWT login and Caddy/systemd deployment files | implemented |
| P5 | Heartbeat, reconnect, cancel, close-session cleanup | implemented |
| P5.1 | Machine-bound reconnect after enrollment expiry and online Bridge metadata | implemented |
| P6 | Orchestration create/continue event stream | implemented |
| P6.1 | Low-token orchestration handoff strategies | implemented |
| P6.2 | Deep collaboration routing and orchestration browser approval | implemented |
| P6.3 | Orchestration capability matrix and pass-through Claude/Codex relay | implemented |
| P6.4 | CLI endpoint repair commands | implemented |
| P6.5 | Formal-proof lightweight workspace bootstrap | implemented |
| P6.6 | Persistent per-context task queue | designed |
| P6.7 | Structured Agent dialogue relay and evidence-based convergence | implemented |
| P6.8 | Durable bounded task graph with shared-CWD serialized workers and reviewer barrier | implemented |
| P7 | Browser permission prompts over app-server | implemented for Codex chat and Codex orchestration |
| P8 | Empty-reply hardening and accessible session deletion | implemented |
| P8.1 | Same-thread chat recovery and bounded retry evidence | implemented |
| P9 | Turnstile-protected self-registration and per-user access | implemented |
| P10 | Public screenshot guide for chat, orchestration, and formal proof | implemented |
| P10.1 | Orchestration runtime, token usage, cost estimates, one-turn mode, and prominent final conclusion | implemented |
| P10.2 | Administrator activity and usage dashboard with user aggregates and read-only conversation detail | implemented |
| P10.3 | Isolated Codex/Claude worker profiles, dual-provider pairs, and deterministic quorum early-stop | implemented |
| P10.4 | [Intermediate completion and actual-model usage fixes](features/early-completion-model-accounting.md), including verifier usage and partial cost display | implemented; local verification |
| P10.4 | Selectable formal-proof verifier and Bridge-managed TBCI capability | implemented |
| P10.5 | [Independent early completion and completed lifecycle results](features/independent-early-run-completion.md) | Go/frontend checks passed; experimental v0.3.66 deployed |
| P10.6 | [Native structured orchestration questions](features/native-structured-user-input.md) | v3 remediation implemented with historical automated checks; [queued native interaction](features/native-structured-user-input-runtime-plan.md) approved and in implementation; real model/browser acceptance and target component versions remain unverified |
| P10.7 | [Official Codex A/B model and reasoning selections](features/official-codex-worker-models.md) with a shared-data enhanced leading website | website deployed 2026-10-08; runtime selections require ConfigSwitcher v3 |
| P10.8 | [Lightweight Android host and shared phone presentation](features/android-mobile-redesign.md) | software published 2026-10-08 on codex.sparkon.cn; signed Android 1.1.0 available; actual-device footprint/memory acceptance pending |

## Engineering Workflow

- Non-trivial changes need ADR or feature design before code.
- Use [docs/change-impact.md](change-impact.md) before editing and before
  submitting.
- Commit messages must include `Doc-Impact: ...`.
- `make doc-lint` checks the lightweight documentation contract.

## Follow-up

- Chat resumes native Codex history: short-lived `codex exec` paths resume with the
  returned thread id, and review-required chat uses the `codex app-server` runner
  (`internal/bridge/appserver_runner.go`) with `initialize` / `thread/start` /
  `turn/start` / streamed deltas / `turn/interrupt`.
- Orchestration is a native-session relay: Claude + Codex runs keep one
  long-lived Codex app-server thread and one long-lived Claude Code stream-json
  session per run, Codex + Codex runs keep independent `codex-a` and `codex-b`
  app-server threads, and Claude + Claude runs keep independent `claude-a` and
  `claude-b` sessions. Native sessions are reused across turns so the
  user can `resume` them from the workspace. The Bridge only relays worker
  output and turn context; it does not inject remediation into worker sessions.
  After every round's durable reviewer, two fresh Agent Verifiers
  independently inspect the original task and real workspace using the role
  presets. Either passing verdict ends unused rounds early. Final rounds also
  verify; repeated unchanged blockers can stop as incomplete, and the initial
  plan remains advisory. See
  [independent-early-run-completion.md](features/independent-early-run-completion.md).
  Saved CLI presets can be pinned independently to `claude`, `codex-a`,
  `codex-b`, `claude-a`, and `claude-b` worker slots without changing
  machine-wide configuration.
  Formal-proof is opt-in *prompt guidance* via
  `internal/bridge/profiles/registry` + `internal/bridge/profiles/formalproof`;
  new formal-proof runs also get a persistent lightweight proof ledger under the
  run cwd before scheduled CLI turns begin.
- The earlier serial persistent-queue proposal is deprecated. New graph-capable
  orchestration runs use the Hub SQLite durable bounded task graph described in
  [docs/features/durable-bounded-orchestration-task-graph.md](features/durable-bounded-orchestration-task-graph.md).
- Administrators can gray-test the optional planning progress workspace: two
  planning/review nodes precede the existing graph, while a default-closed
  overlay presents prompt history, a React Flow node map, and checklist state
  without resizing the live transcript. See
  [docs/features/orchestration-plan-progress-workspace.md](features/orchestration-plan-progress-workspace.md).

## Maintenance Log

- 2026-10-03: Deployed the standard frontend and shared Hub as
  `v0.3.82-input-recovery-20261003`. Structured-input query errors now correlate
  by syncId, distinguish discovery failure from unknown answer delivery, and
  recover without stale notices or answer replay. The standard site also picks
  up the desktop repairs below; test retains its leading frontend. No Bridge
  was upgraded or restarted. See [native structured input](features/native-structured-user-input.md)
  and [deployment variants](deployment.md#leading-and-experimental-websites).
  Eleven desktop recovery scenarios, targeted checks, frontend tests and builds
  pass. Full Go testing reported two timing-sensitive connection/lease failures;
  both passed individual rechecks, without making the full run all-green.
- 2026-09-30: Desktop audit repairs are implemented and deployed to the leading
  website and shared Hub as `v0.3.82-desktop-fixes-20260930`: isolated drafts,
  recoverable authentication/statistics requests, history search and reversible
  archive, plus accurate bilingual help/version information. See
  [desktop audit fixes](features/desktop-audit-fixes.md). The separately authorized
  website release retains the previous site's frontend and running Bridges;
  public health, resources, desktop pages, history and statistics reads were
  verified. The first post-release usage overview timed out; a bounded retry
  returned successfully in about three seconds, without establishing the cause.
  Mobile changes and notification/budget suggestions remain outside this work item.
  Desktop browser, Hub/Store and integration checks pass; the full Go gate still
  exposes an existing intermittent Bridge CLI stderr collection failure.
- 2026-09-11: Added participant/model charts with historical slot and native-log
  recovery, and a cached automatic model capability/reference-price catalog.
  Preset probes survive reselecting the same model. See
  [participant usage](features/orchestration-worker-usage.md) and
  [catalog ADR](adr/021-refreshable-model-catalog.md).
- 2026-09-08: Added Linux ARM64 endpoint downloads, platform detection and
  pre-replacement execution checks in the installer, with sequential dual-arch
  builds. See [Linux ARM64 installation](features/linux-arm64-installation.md).

- 2026-09-01: Made TBCI baselines submission-scoped and persistent across
  durable graph nodes, generated contracts for unique Rocq/Lean/Isabelle
  targets after upload preparation, and added bounded namespaced Rocq project
  support without narrowing the Lean or Isabelle paths.
- 2026-09-01: Embedded the pinned Linux/amd64 TBCI runtime in supported Bridge
  builds, added private atomic installation and digest verification at startup,
  reported readiness to the Hub, and rejected unavailable TBCI selections
  before run creation while preserving Agent Verifier as the default.
- 2026-08-15: Delayed durable task recovery until the existing bounded Bridge
  reconnect window after Hub startup, so a brief Hub-only reload preserves
  active attempt identity and buffered terminal evidence; only endpoints still
  offline after that window are conservatively recovered.
- 2026-08-14: Made Bridge installation endpoint-neutral. `/install.sh` now
  atomically replaces only the local binary; the following directory-scoped
  `link` starts or restarts only the selected endpoint, so linking another
  workspace cannot interrupt active same-machine orchestration runs.
- 2026-08-11: Archived the Android wrapper and removed APK builds from the
  automatic release path. Android is no longer maintained or considered a
  release exit gate; existing wrapper sources remain for historical reference.
- 2026-08-09: Coalesced high-frequency native command progress at the Bridge
  before transport and SQLite persistence, preserving full output and terminal
  ordering without adding a database service or user configuration. Durable
  orchestration prompts now use workspace-neutral wording and do not prescribe
  a Git workflow.
- 2026-08-08: Added a Hub-persisted task graph with stable attempt identity,
  ambiguity-safe restart recovery, user-selected shared CWD execution,
  serialized candidate writes, and a final reviewer/formal-checker barrier.
- 2026-08-08: Removed bootstrap-admin cross-user and legacy-unowned CLI
  endpoint visibility, and protected existing machine ownership from
  reassignment during reconnect.
- 2026-08-08: Added structured Agent-to-Agent relay packets, compact incremental
  cross-worker context, and conservative reviewer/critic-confirmed early
  convergence without changing orchestration APIs, UI, or native sessions.
- 2026-08-07: Added WebSocket ping/pong resilience, bounded same-session chat
  reconnect with state/output recovery, wider Bridge liveness tolerance, and
  request-scoped strict approval for workspace `cat`, `coqc`, and batch
  `coqtop` commands.
- 2026-08-07: Prevented transient Bridge outbound-queue saturation from
  forcing a reconnect, added heartbeat interval defaults, and reset backoff
  after stable connections.
- 2026-08-07: Added bounded ordinary-chat response evidence, direct recovery
  from streamed text, and one same-native-thread continuation for empty or
  interrupted CLI terminal responses without replaying the original prompt.
- 2026-08-06: Fixed expired machine-bound Bridge reconnects, exposed active
  Bridge version/connection time, reduced returning-worker relay history, and
  bounded the Bridge-owned formal-proof follow-up ledger.
- 2026-08-05: Added the public `/help` guide and `/hlep` alias with 17
  deterministic application screenshots, detailed collaboration/debate and
  formal-proof workflows, copyable demos, and low-context orchestration advice.
- 2026-05-30: Removed the abandoned external CCB orchestration backend and the
  superseded per-turn orchestration design (verifier / remediation / acceptance
  assessment), the deprecated `orchestration_runner` config, and dead CCB install
  code in `link.go`. `internal/bridge/orchestration.go` 7358 -> ~3200 lines;
  `profiles/formalproof` 1579 -> ~335; `profiles/registry` 109 -> ~39. Full
  `go test ./...` green. Verified unreachable code with
  `go run golang.org/x/tools/cmd/deadcode@latest ./...`.
- 2026-05-30: Completed the Go Part A monolith split from
  `docs/features/monolith-file-split.md`, moving orchestration relay, Codex,
  Claude, events, redaction, and profile helpers into same-package
  `internal/bridge/orchestration*.go` files.
- 2026-05-30: Completed the frontend Part B monolith split from
  `docs/features/monolith-file-split.md`, reducing `frontend/src/app/App.tsx`
  to root routing/bootstrap and moving pages, shared helpers, chat components,
  settings, orchestration renderers, and UI primitives under
  `frontend/src/app/{pages,components,lib}`.
