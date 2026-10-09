# Change Impact

This is the detailed "if X changes, Y must also change" table. `AGENTS.md`
keeps only the top frequent subset.

## General

| Change | Must update |
| --- | --- |
| Add or change config field | `internal/config/` structs/loaders, `configs/*.yaml.example`, `docs/dev-workflow.md`, README if user-facing |
| Add or change env var | `internal/config/load.go`, `configs/dev.yaml.example`, `docs/dev-workflow.md` env table, deploy docs |
| Add or change HTTP endpoint | `internal/hub/server.go` route, handler tests, frontend API caller, feature/API docs |
| Add or change WebSocket frame | `internal/protocol/envelope.go`, Hub handler, Bridge handler, frontend parser, integration tests, `docs/architecture.md` |
| Add or change SQLite table/column | `internal/store.Store.Migrate`, store structs/scanners/CRUD, store tests, `docs/code-map.md`, architecture storage table |
| Event read predicates or partial indexes | Store migration and exact query predicates, query-plan/equivalence tests, full-history progress/usage semantics, [ADR 027](adr/027-sparse-orchestration-read-indexes.md) |
| Add or change auth/cookie behavior | `internal/auth/`, `internal/hub/server.go`, frontend login/logout handling, security docs |
| Add a user-visible feature rollout | Register the key in `internal/rollout/rollout.go:RegisteredFeatures`, add the default `hub.feature_rollouts` policy, enforce it at backend mutation boundaries, consume `user.features` in the frontend, and add allowed/denied tests |
| Add or change runner behavior | `internal/bridge/runner.go` or `internal/bridge/orchestration.go`, protocol payload if needed, tests, runner notes in docs |
| Add or change ACP runner behavior | `internal/bridge/acp_runner.go`, `internal/bridge/acp_client.go`, `internal/bridge/runner.go:SessionRunner`, `internal/bridge/session.go` dispatch, `internal/protocol.SessionOpenedPayload`/`PromptCompletePayload` native-resume fields, `docs/features/acp-runner.md`, tests |
| Add or change ACP takeover UI | `frontend/src/app/components/chat/TakeoverHint.tsx`, `frontend/src/app/pages/Workspace.tsx`, `frontend/src/app/lib/types.ts`, `frontend/src/app/lib/i18n.ts`, rebuild `internal/web/static` via `npm run build`, `docs/features/acp-runner-pr2.md` |
| Add or change deployment unit | `deploy/`, `Dockerfile`, `Makefile`, `docs/deployment.md`, README/README.zh-CN deployment sections |
| Routine website deployment | Follow `AGENTS.md` R9 and [default deployment scope](deployment.md#default-deployment-scope): build frontend and host-architecture Hub only; cross-platform endpoint packages and Bridge upgrades require an explicit request |
| Add or change CI / build pipeline | `.github/workflows/ci.yml`, `Makefile` targets it calls, README CI badge |
| Add or change install/download flow | `internal/hub/server.go`, generated-installer tests, `configs/*.yaml.example`, README/README.zh-CN, `docs/dev-workflow.md`, relevant feature docs |
| Android host or shared phone presentation | Preserve existing business callbacks/state and API payloads; validate mobile/desktop parity, Android host facilities and signed release; update [mobile contract](features/android-mobile-redesign.md), [ADR 028](adr/028-lightweight-android-webview.md), code map and Android build instructions. Routine website releases do not automatically publish APKs |
| Add or change embedded static assets | `frontend/` source, run `npm run build`, verify generated `internal/web/static/` |

## Conversation And Orchestration

| Change | Must update |
| --- | --- |
| Chat session creation or selection | `internal/hub/server.go`, `internal/store/` session methods, `frontend/src/app/pages/Workspace.tsx`, continuity feature doc |
| Chat prompt flow | `internal/hub/ws_browser.go`, `internal/bridge/session.go`, `internal/bridge/runner.go`, run/message store methods, integration tests |
| Persistent task queue | SQLite schema/store queue CRUD, `internal/hub/ws_browser.go`, `internal/hub/ws_bridge.go`, `internal/hub/orchestration.go`, `internal/hub/server.go`, queue browser updates in frontend chat/orchestration pages, public-share sanitization review, `docs/features/persistent-task-queue.md`, tests |
| Durable orchestration task graph | SQLite graph/task/attempt schema and transitions, `internal/protocol/envelope.go`, `internal/hub/task_graph.go`, Hub event/status handling and recovery, Bridge capability/workspace/role handling, public-share sanitization review, `docs/features/durable-bounded-orchestration-task-graph.md`, tests |
| Durable task-graph compatibility switch | `internal/config/`, `configs/*.yaml.example`, `internal/bridge/client.go`, integration coverage, `docs/dev-workflow.md`, durable task-graph feature doc |
| Browser chat reconnect/close behavior | `internal/hub/ws_browser.go`, `internal/hub/browser_lease.go`, `internal/config/`, `configs/*.yaml.example`, integration tests, `docs/features/browser-session-lease.md` |
| Codex thread resume behavior | `internal/bridge/runner.go:args`, `internal/hub/ws_bridge.go:handlePromptComplete`, `internal/store/store.go:UpdateSessionRemoteThreadByID`, tests |
| Orchestration create/continue flow | `internal/hub/orchestration.go`, `internal/bridge/orchestration*.go`, `frontend/src/app/pages/OrchestrationWorkspace.tsx`, `docs/features/orchestration-continuity.md` |
| Structured input query errors and recovery notices | Hub syncId-correlated route rejection, strict browser sync-error decoder, request-status isolation, pending/receipt identity and storage errors, timeout/late-success/no-replay browser regressions, ADR-024 and native structured input feature; no Bridge change for Hub-only query rejection |
| Native structured orchestration questions | `internal/protocol/envelope.go`, Bridge native adapters, bounded dispatch/outbox and lifecycle/receipt cache, `internal/hub/user_input.go`, `internal/hub/pool.go` browser v3 negotiation/filter, create/continue client version and start-payload gate, terminal-run read-only WebSocket guard, `frontend/src/app/lib/userInput.ts` pending-submission storage/reconciliation, `frontend/src/app/pages/OrchestrationWorkspace.tsx`, `frontend/src/app/components/UserInputCard.tsx`, capability/rollout gates, reconnect/ownership/race tests, `docs/features/native-structured-user-input.md` |
| Worker-bound orchestration CLI profile | create/continue validation, `protocol.OrchestrationStartPayload`, Bridge runtime-home lifecycle, native-resume fingerprinting, frontend slot selector, graph/progress/public-share sanitizers, profile and continuity docs, Hub/Bridge tests |
| Official Codex per-worker model/effort | Native `model/list` RPC and shared protocol metadata limits, ConfigSwitcher v3, Hub catalog authorization/validation and rollout, existing slot bindings/continuation, Bridge exec/resume/app-server turns and verifier parameters, native fingerprint/provider override when clearing defaults, frontend edition flag/selectors, `docs/features/official-codex-worker-models.md`, Hub/Bridge/browser tests |
| User-scoped CLI provider preset | preset metadata/credential/activation store migration, Bridge key-wrapping protocol, settings and orchestration callers, storage/Hub/Bridge tests, provider-preset feature docs |
| Dynamic execution / runtime input | Store execution_mode migration and messages table, Hub acceptance/completion locking and graph input version, Bridge native queues/receipts, same-run prompts GET/POST, leading/default frontend builds, late-input/idempotency/CLI tests, ADR-025 and dynamic input feature |
| Orchestration endpoint continuity | `internal/hub/orchestration.go:handleContinueOrchestration`, `frontend/src/app/pages/OrchestrationWorkspace.tsx`, `docs/features/orchestration-continuity.md`, `docs/features/manual-orchestration-rounds.md` |
| History page size, cursors or boundary rendering | events/admin Store queries, Hub bounded API and lookahead semantics, frontend initial/manual/catch-up callers, final-answer and realtime-race regressions, [loading performance](features/site-loading-performance.md) |
| Orchestration history filtering, title or archive metadata | Store migration/scanners/query and status transitions, Hub list/PATCH ownership validation, frontend run types and paginated history caller, continuation/unarchive and usage inclusion tests, [desktop audit fixes](features/desktop-audit-fixes.md), [ADR 026](adr/026-orchestration-history-metadata.md) |
| Browser orchestration drafts or auth bootstrap recovery | Per-account/target draft namespace, async send completion, logout cleanup, HTTP error status handling, retry/timeout UI and browser failure regressions, [desktop audit fixes](features/desktop-audit-fixes.md) |
| Native question minimum CLI version | `internal/bridge/user_input_version.go`, capability and runtime gates in Bridge, version-boundary tests, upstream evidence in `docs/features/native-structured-user-input.md`, README and workflow compatibility notes |
| Bridge capability reporting | `internal/protocol.RegisterPayload`, `internal/bridge/client.go`, `internal/hub/pool.go`, `internal/hub/server.go:handleAgents`, frontend agent types/UI, policy tests |
| Managed TBCI runtime or artifact | `internal/bridge/tbci_runtime*.go`, `internal/protocol.BridgeCapabilities`, Hub orchestration capability validation, frontend Agent/selector UI, portable/download packaging assumptions, TBCI feature doc and ADR |
| TBCI task identity, target discovery, or proof-root baseline | `internal/protocol.OrchestrationStartPayload`, Hub durable dispatch, `internal/bridge/orchestration_{harness,tbci}.go`, Rocq/Lean/Isabelle discovery regressions, ADR-017, architecture/code map, and TBCI feature docs |
| Bridge enrollment or reconnect semantics | `internal/store/store.go:ConsumeEnrollTokenInfo`, `internal/hub/ws_bridge.go:handleBridgeWS`, endpoint deletion/revocation flow, store and integration tests, `docs/dev-workflow.md` |
| Orchestration event shape | `internal/protocol.OrchestrationEventPayload`, Bridge emitters in `internal/bridge/orchestration_events.go`, `internal/store.OrchestrationEvent`, Hub persistence/share sanitizers, visible event reducer in `frontend/src/app/lib/utils.ts`, integration tests, `docs/architecture.md`, `docs/code-map.md` |
| Orchestration verifier or terminal reason | verifier quorum tests, Bridge relay-stop behavior, durable task-graph completion/recovery, `protocol.RunEndData`, Hub progress/public-share sanitizers, frontend timeline/plan/branch-map reducers, continuity and feature docs |
| Public model catalog refresh / capability or price resolution | `internal/modelcatalog/`, `internal/usagepricing/`, `internal/hub/model_catalog.go`, Hub lifecycle/cache, preset normalization, usage source labels, catalog failure/new-model tests, `docs/adr/021-refreshable-model-catalog.md` |
| Participant identity in usage | Bridge snapshots and native model capture, Hub historical turn/graph projection and official-login sync sessions, ledger/snapshot/task reconciliation tests, frontend worker charts/types, `docs/features/orchestration-worker-usage.md` |
| Orchestration usage statistics | `internal/bridge/orchestration_usage.go`, `internal/usagepricing/catalog.go`, `internal/bridge/usage_ledger.go`, `internal/store/orchestration_usage.go`, `internal/hub/orchestration.go:handleOrchestrationStats`, `internal/hub/orchestration.go:handleUsageOverview`, `frontend/src/app/pages/OrchestrationStatsPage.tsx`, `frontend/src/app/lib/types.ts`, `frontend/src/app/lib/i18n.ts`, embedded static UI, usage tests, `docs/features/orchestration-usage-overview.md` |
| Administrator usage analytics or content access | `internal/store/admin_analytics.go`, `internal/store/admin_content.go:AdminConversationContent`, `internal/hub/admin.go:handleAdminUsage`, `internal/hub/admin.go:handleAdminUserUsage`, `internal/hub/admin.go:handleAdminConversationContent`, admin authorization/ownership/privacy tests, `frontend/src/app/pages/AdminUsagePage.tsx`, `frontend/src/app/pages/AdminUserUsagePage.tsx`, administrator-only navigation, embedded static UI, `docs/features/admin-usage-dashboard.md` |
| Orchestration profile behavior | Hub create/continue payloads, `internal/store.OrchestrationRun.profile`, Bridge prompt composition, `internal/bridge/profiles/`, frontend profile selector, continuity docs |
| Formal-proof workspace bootstrap | `internal/bridge/orchestration_harness.go`, `internal/bridge/orchestration.go:run`, formal-proof prompt composition, bridge tests, `docs/features/formal-proof-lightweight-workspace.md`, `docs/architecture.md`, `docs/code-map.md` |
| Formal-proof offline benchmark | `benchmarks/formal-proof/` fixtures/source metadata/results, `scripts/run-formal-proof-benchmarks.sh`, `Makefile:benchmark-formal-proof`, `docs/features/formal-proof-offline-benchmarks.md` |
| Orchestration native context compaction | `internal/protocol.OrchestrationStartPayload`/`RunStartData`, `internal/store.OrchestrationRun.native_context_compaction`, Hub create/continue/share payloads, `internal/bridge/orchestration_native.go`, Codex/Claude interactive turn helpers, frontend orchestration settings, `docs/features/native-context-compaction.md`, tests |
| Orchestration native resume metadata | `internal/protocol.RunEndData`, `internal/bridge/orchestration_native.go`, `internal/bridge/orchestration_relay.go`, frontend run-end types/reducers, share sanitizer, `docs/features/claude-native-resume-visibility.md`, tests |
| Orchestration long-command observer | `internal/config/`, `configs/*.yaml.example`, Bridge scanner/observer tests for Claude and Codex, frontend Bridge-note rendering, `docs/dev-workflow.md`, README if user-facing |
| Orchestration explicit command timeout convergence | `internal/bridge/orchestration_timeout.go`, native Codex/Claude turn paths, command event lifecycle tests, `docs/features/orchestration-explicit-timeout-convergence.md`, `docs/architecture.md`, `docs/code-map.md` |
| Cancellation semantics | Hub cancel handler, Bridge cancel manager, status constants, frontend stop button, tests |
| Attachment handling | Hub size/type validation, Bridge file materialization, frontend upload limits, README if limits are user-facing |

## Security

Completion observation changes couple Bridge source/usage sampling and verdict
handling to Hub graph payload persistence/reset, public-share sanitization,
frontend run-end types, protocol round-trip tests, and
[ADR-020](adr/020-independent-early-run-completion.md).
Early execution completion also couples attempt transactions to successor
cancellation and Hub conclusion publishing; cover stale attempts, active sibling
nodes, plan-node exclusion, and factual verifier status separately from the
completed lifecycle label.

| Change | Must update |
| --- | --- |
| Secret field or token source | Example configs with empty placeholders, env var docs, logging redaction review |
| Login/register validation | Auth tests, frontend validation text, rate limit notes |
| CORS/origin/cookie policy | `internal/hub/server.go`, deploy/Caddy docs, README deployment guidance |
| Logging fields | Check no password/token/API key appears in logs, update dev workflow logging notes |
| Private operational notes | Put in `docs/private/`; do not commit tickets, scan reports, real tokens, or private hostnames |

## Documentation

| Change | Must update |
| --- | --- |
| New ADR | Add `docs/adr/NNN-<slug>.md`, link from architecture/AGENTS if top-level |
| New feature design | Add `docs/features/<slug>.md`, update `docs/plan.md` if roadmap/status changes |
| New package or major file | `docs/code-map.md` and AGENTS brief table if top-level |
| Changed setup command | README, README.zh-CN if Chinese quick start changes, `docs/dev-workflow.md` |
| Deprecated design | Mark old doc as deprecated and link the replacement |

Last updated: 2026-10-08
