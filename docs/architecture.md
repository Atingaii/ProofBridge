# Architecture

This is the single overview for runtime architecture. Detailed rationale lives
in ADRs; implementation paths live in [docs/code-map.md](code-map.md).

The public [orchestration design article](features/orchestration-design-article.md)
is a lazy-loaded static page at `/blog/orchestration-design`. Like the help page,
it skips account bootstrap; it does not query runs or other private API data.

User-facing production rollouts are evaluated by `internal/rollout` from
`hub.feature_rollouts`. The Hub enforces gates on protected operations and
returns enabled keys in `user.features`; browser code never determines rollout
membership itself. See [ADR-010](adr/010-user-feature-rollouts.md).

Administrator authorization, user metadata, and administrator rollout membership
share `internal/hub/server.go:isAdminUser`: the bootstrap administrator plus the
optional `auth.admin_usernames` allowlist. See
[ADR-018](adr/018-additional-administrator-usernames.md).

The Hub owns the reviewed, CLI-scoped model reasoning and Claude-context policy
in `internal/hub/model_catalog.go`; it resolves immutable saved-preset and
orchestration-binding snapshots before they reach a Bridge. The Bridge only
decrypts credentials and materializes native configuration. It does not infer
capabilities or create a Codex model catalog. Codex gets `config.toml` and
`auth.json`; Claude Code gets `settings.json`, including the selected
`effortLevel` and Hub-reviewed context values. See
[ADR-021](adr/021-refreshable-model-catalog.md).
The Hub refreshes public model metadata and reference prices every six hours,
loads an atomic last-good cache beside its database, and falls back to compiled
metadata offline. Request paths read only memory; vendor credentials are never
sent to the catalog.

```text
Browser UI (one selected run stream)
  | WSS /ws/chat?sid=<session>
Hub (Go + embedded UI + SQLite)
  | reverse WSS /api/agents/connect?token=<enroll>
Bridge (Go)
  | spawn per prompt
codex exec --json
```

All three WebSocket paths use standard ping/pong control frames in addition to
the existing JSON heartbeat envelopes. Hub serializes JSON and ping writes in
`internal/hub/pool.go:WriteLoop`; Bridge does the same in
`internal/bridge/client.go:connectOnce`. Read deadlines retain at least six
heartbeat windows (and at least 90 seconds). Chat reconnect in
`frontend/src/app/pages/Workspace.tsx:connectWS` keeps the same `sid`, reloads
persisted messages/runs, and receives any in-flight assistant buffer from
`internal/hub/ws_browser.go:handleBrowserWS`.

For orchestration, a transient reverse Bridge WebSocket loss is transport state,
not task cancellation. `internal/bridge/client.go:connectOnce` detaches the
dead output channel while `internal/bridge/orchestration_events.go:send` buffers
events until reconnect. `internal/hub/ws_bridge.go:bridgeReconnectGrace` holds
the Hub-side active run through the Bridge's maximum retry delay, jitter, and a
heartbeat window before declaring an offline machine failed. Process restart
and explicit cancellation remain terminal. After a registered transport drops,
`internal/bridge/client.go:bridgeReconnectDelay` permits one immediate retry;
failed connection attempts retain bounded exponential backoff with jitter. See
[orchestration transport recovery](features/orchestration-transport-recovery.md).
The event-history endpoint returns an authoritative earlier-page indicator, and
a terminal offline error records the bounded transport close reason. See
[orchestration history and terminal reasons](features/orchestration-history-and-terminal-reasons.md).

CLI endpoints created with the review-required profile use
`internal/bridge/appserver_runner.go` instead of `codex exec --json` for Codex
chat. That runner keeps a `codex app-server --listen stdio://` JSON-RPC session
open for the turn so Codex approval requests can be relayed to the browser.

Endpoints explicitly linked with the `strict-workspace` profile use
`internal/bridge/command_process.go:configureStrictWorkspaceCommand` for every
managed Codex, Claude Code, and ACP process. The binary re-execs itself through
`internal/bridge/strict_workspace_linux.go:RunStrictWorkspaceSandbox`, installs
a fail-closed Landlock ruleset, then execs the target. Only the canonical bound
workspace and per-workspace private runtime are writable. Non-home system and
mount trees, hidden home configuration, and executable components explicitly
exported through the endpoint's saved shell `PATH` are read-only; unrecognized
ordinary home directories remain inaccessible. The real `HOME` remains in the
child environment so login shells and tool managers keep their normal path
resolution, while Codex/Claude state and temporary files use private runtime
paths. Strict-mode Codex exec, resume, and app-server turns disable
Codex's nested Bubblewrap policy after entering Landlock. The inherited outer
ruleset still covers every command, while avoiding replacement `/proc` objects
that cannot be reopened by an already restricted process. Existing permission
profiles do not use this wrapper or argument override. See
[strict workspace auto execute](features/strict-workspace-auto-execute.md).

When the endpoint runs with `bridge.runner: acp`, chat uses
`internal/bridge/acp_runner.go:ACPRunner` instead of a per-turn process. It keeps
one resident Agent Client Protocol adapter per chat session
(`internal/bridge/acp_client.go:acpClient` over stdio JSON-RPC) so prompts stream
into a live conversation without restarting the process (target A). It also
resolves the underlying CLI's own session id so the same conversation can be
continued from the workspace with `claude --resume <id>` / `codex resume <id>`
(target B). The dual ids round-trip to the browser through the optional
`SessionOpenedPayload`/`PromptCompletePayload` `nativeResumeId` and
`nativeResumeCommand` fields; the ACP session id is stored as the existing
`remote_thread_id` so continuity plumbing is unchanged. The full design is in
[docs/features/acp-runner.md](features/acp-runner.md). The `echo`, `codex-exec`,
and `codex-app-server` runners are unchanged; only `ACPRunner` implements the
`internal/bridge/runner.go:SessionRunner` interface and the session layer falls
back to one-shot `Runner.Prompt` for every other runner. The browser surfaces
the takeover command via `frontend/src/app/components/chat/TakeoverHint.tsx` and
an ACP badge in `frontend/src/app/pages/Workspace.tsx`, shown only when the
Bridge actually resolved a command (otherwise a neutral "unavailable" note);
see [docs/features/acp-runner-pr2.md](features/acp-runner-pr2.md). Orchestration
resident-session reuse is intentionally deferred (it would touch the
review-required approval pipeline).

The orchestration UI uses HTTP for create/continue/cancel plus a run-scoped
WebSocket for event streaming:

Leading builds (`VITE_LEADING=true`) create `executionMode=dynamic` runs. The
Hub persists `orchestration_runs.execution_mode`; historical rows default to
`bounded`. Dynamic graphs retain completion/stagnation checks but have no total
round limit. Node-local `MaxTurns=1` is independent of that policy.
`orchestration_messages` persists idempotent live instructions on the same run;
`orchestration_input` and `orchestration_input_status` connect Hub and Bridge.
Queued/sending receipts remain distinct from native delivery and execution.
The graph's input version invalidates verdicts for superseded instructions,
and later workers receive delivered instructions in the acceptance contract.
Instruction acceptance and graph completion share a Hub critical section.
Two frontend hosts share one Hub/SQLite/Bridge pool; never run competing
schedulers against the same database. See
[dynamic orchestration and input](features/dynamic-orchestration-input.md).

Native structured questions use the same run-scoped WebSocket without creating
a new orchestration prompt or run. Codex synchronous requests answer their
original JSON-RPC call; Codex async `agentMessage` questions carry a native
`questionItemId` and are answered within the same CLI thread via `turn/steer`
or, once its native turn has ended, `turn/start`. The Bridge keeps the pending
question and sole event reader alive until delivery or cancellation.
`internal/protocol/envelope.go:UserInputRequestPayload`
defines the Bridge-owned question, answer, status, and snapshot frames.
`internal/hub/user_input.go:handleUserInputFromBridge` verifies the owning
Bridge and the `native-user-input` rollout; the browser response is checked
against the authenticated run before it is sent to that Bridge. The Bridge
holds the live native callback, so questions and answers are not written into
Hub SQLite events or public shares. A refreshed browser requests a snapshot;
only an active native process can confirm an outstanding question. Approval
decisions remain on their separate permission path. See
[native structured user input](features/native-structured-user-input.md).

Hub query rejections carry the original `syncId` in
`internal/protocol/envelope.go:UserInputStatusPayload` and are decoded separately
from request statuses. They report route failure, never delivery or closure;
only a matching current run/query may update the query notice. This additive
repair needs a matching Hub/UI deployment, not a Bridge upgrade. See
[ADR 024](adr/024-native-structured-user-input.md#addendum-correlated-sync-rejection-2026-10-03).

Participant labels use the native worker slot independently of task role and
model name. Claude's question helper binds that slot at session creation and
updates its turn/role before a new prompt is written. The browser keeps
multi-question drafts in memory while navigating and submits one answer map.
See [participant identity and question flow](features/participant-identity-and-question-flow.md).

The Bridge retains a bounded, content-free terminal receipt window after a
native request closes. An owning browser may make a read-only sync for a
finished run to recover a lost delivery acknowledgement; Hub still rejects
answers once the run is no longer active. Bridge instance identity separates
revision domains across restarts, and native writes run outside the reverse
WebSocket reader through a bounded, per-client-fair scheduler. The browser
declares `clientVersion=3` on `user_input_sync`, and create/continue requests
carry `nativeUserInputClientVersion=3`. Hub advertises support through `/api/me`,
restricts active question flow to negotiated v3 browser connections, and
requires a v3 Bridge capability. The start payload repeats the negotiated
version so a new Bridge attached to an old Hub cannot enable questions from an
old true-only payload. The browser stores only pending submission identities in
session storage and asks Bridge to reconcile them after refresh; Bridge
atomically fences an unseen submission before returning `not_sent`. An old
page may receive a terminal run's read-only snapshot, but cannot submit an
answer. The UI opens the question panel on a new request, keeps a single
editable card per selected request and a visible entry outside the timeline
scroll container. See
`internal/bridge/user_input.go:UserInputSync`,
`internal/hub/user_input.go:handleUserInputFromBrowser`, and
`frontend/src/app/pages/OrchestrationWorkspace.tsx:connectRun`.

```text
Browser UI
  | POST /api/orchestrations
  | POST /api/orchestrations/<run>/prompts
  | WSS /ws/orchestrations?runId=<run>
Hub (SQLite runs/events + durable task graph/attempts)
  | reverse WSS orchestration_start / orchestration_event
Bridge (independent execution handles per run)
  | run-scoped native CLI sessions
Codex CLI / Claude CLI
```

For users in the `orchestration-plan-workspace` rollout, new durable runs
prepend planner and independent plan-review tasks once per user task. Later
internal rounds carry the reviewed plan and validated updates without repeating
planning. Planning uses tokens/time but no additional execution round. Their structured event
markers are projected by `internal/hub/task_graph.go:reduceOrchestrationPlan`
into one proof-domain plan containing the overall goal, branches,
dependencies, difficulty, priority, readiness, local progress, and evidence.
The browser renders this proof map and checklist from the same projection while
keeping the durable Agent task graph as a separate, secondary runtime view.
The workspace is a default-closed inline section immediately below the runtime
toolbar. Its bounded expanded state leaves the transcript in normal document
flow, and its checklist never falls back to internal Agent errors.

Active-run deletion is a durable state transition rather than a browser-only
removal. `internal/store/store.go:RequestDeleteOrchestrationRun` records the
delete intent and atomically cancels unfinished attempts, tasks, and the active
graph before Hub dispatches the Bridge cancellation. Late terminal events
cannot revive the run or unlock successor tasks, and terminal acknowledgment
or the existing cancellation timeout deletes the requested row by cascade.

The Bridge keeps orchestration deterministic while preserving native CLI
continuity. Each run persists a narrow `worker_pair`: `claude-codex` keeps the
default Claude Code + Codex relay, `codex-codex` runs `codex-a` and `codex-b`,
and `claude-claude` runs `claude-a` and `claude-b` in separate worker slots. The
new-run browser workflow explicitly snapshots an encrypted saved preset
onto each active slot; Hub authorizes and persists ciphertext only, while
Bridge decrypts it only to materialize a mode-0700 per-run/slot runtime home.
Two Codex or Claude slots can therefore use distinct models without mutating
global CLI state. For Claude + Codex runs, Bridge keeps
one long-lived Codex app-server thread and one long-lived Claude Code
stream-json session. For Codex + Codex runs, Bridge keeps independent
`codex-a` and `codex-b` app-server threads so each participant has distinct
native context. Direct orchestration is a pass-through relay: the run's
persisted `worker_pair` and `first_cli` settings decide which worker receives
the browser task first, Bridge streams CLI deltas, typed command lifecycle and
progress events, coalescing only high-frequency command output updates before
transport while preserving their text and terminal ordering, and
terminal status to the browser, and the next worker receives a parsed
Agent-to-Agent packet containing the newest request, changed files,
verification, next action, risks, and useful command context. Full prose is a
bounded fallback only when the structured packet is absent or malformed.
Direct serial relay reviewer/critic turns may end below their configured ceiling
only after an explicit resolved handoff with independent evidence. For
graph-capable Bridges, `max_turns` instead budgets whole collaboration rounds:
each round runs two bounded candidates, serial integration, and an independent
reviewer, and Hub creates subsequent generations under the same run id when
independent verification does not confirm completion. Intermediate reviewer
handoffs may report task completion; the separate verifier decides whether
unused rounds can end. Formal-proof review additionally requires a successful
reviewing-turn checker/audit command. Hub SQLite is the task authority; node
attempts carry stable identities and payload digests, ambiguous restart state
becomes `unknown`, and Bridge executes each node serially in the selected
project workspace. Native Codex and Claude sessions are owned by an individual
durable task attempt; compact evidence, rather than a prior attempt's native
id, carries context into the next node. Older Bridges retain the serial relay behavior. See
[ADR-008](adr/008-durable-orchestration-task-graph.md).
Bridge persists the legacy
Codex thread id, the `codex_thread_ids_json` slot map, the stable Claude
session id, and the `claude_session_ids_json` / `claude_started_slots_json`
slot maps so follow-up prompts can resume native history after a Bridge
restart where the CLI supports it. Run-end data also carries direct native
resume metadata for the participating CLIs: Codex exposes
`codex resume <thread-id>` for each persisted Codex slot, and Claude exposes
`claude --resume <session-id>` plus the project transcript path under
`~/.claude/projects/<encoded-cwd>/`. For isolated slots, the direct command
retains the matching private `CODEX_HOME` or `CLAUDE_CONFIG_DIR`. Bridge copies
only the Codex session JSONL or normalized Claude transcript into the ordinary
picker location; it never copies a slot's configuration or credential files.
Thus `/resume` can discover the conversation while direct resume still uses
the model preset that created it.

Official Codex bindings can reuse native authentication while carrying independent
per-slot model and reasoning effort in the existing worker-profile JSON. The
`cli_config_official_models` request returns a separate endpoint-native catalog;
ConfigSwitcher v3 promises both discovery and effective runtime overrides.
Hub validates choices against that catalog and forwards them through the native
exec/resume and app-server turn paths without per-selection shared-config writes.
The enhanced frontend adds only the two selectors and preserves run/thread
continuity. See [official Codex worker models](features/official-codex-worker-models.md).

After successful Claude turns, Bridge updates only the current cwd entry in
`~/.claude.json` so native Claude
project metadata points at the Bridge session without touching unrelated
projects, and materializes the same Claude-written transcript so Claude Code's
interactive `/resume` picker can show it from the run cwd. It does not add
model-driven proof strategy or remediation turns. After every round's
durable reviewer, or a resolved worker/integrator completion candidate, Bridge
starts two fresh Agent Verifier calls using role 1 and
role 2's bound presets, executed serially to bound machine load. Each receives
the full original `TaskPrompt`, excluding durable-node instructions, and runs in
the real project workspace under the endpoint's configured execution permissions,
including build outputs when authorized, without changing source. Bridge
waits for both results before emitting a visible `verifier.verdict`; any
passing Agent Verifier without a concrete evidence conflict ends unused rounds
with `verified-early`, while the Hub
dispatches the next configured round when both verifiers continue and budget
remains. Final rounds still verify; budget exhaustion is `max-turns` with a
blocked conclusion unless independently verified. Bounded source/usage/blocker
observations persist in graph JSON across internal rounds, reset on a new user
prompt, and are stripped from public shares. Low native reviewer output is
diagnostic only; three unchanged source states with the same verified blocker
end as `stalled` with a completed run result, retaining the actual failed verdict.
Plan output, progress and checkboxes never enter completion adjudication.
Verified execution candidates atomically cancel undispatched graph successors;
only reviewer samples count toward stagnation. Task identity prevents reuse across
follow-up tasks. Independent agent checks are serialized across runs.
See [ADR-020](adr/020-independent-early-run-completion.md) and
[independent early completion](features/independent-early-run-completion.md).
Formal-proof guidance is opt-in
through the persisted `profile=formal-proof` run setting selected in the
orchestration UI; the default profile does not activate proof guidance based on
prompt keywords. Under that profile, collaboration alternates a proof author
and independent proof auditor, while debate alternates a falsifiable proof
claim and adversarial critic. Both contracts freeze the named statement, carry
proof-state/checker evidence forward, audit trust dependencies, and require a
final adjudication that cannot call placeholder, weakened, axiom-tainted, or
unfinished output a completed proof. Existing UI modes, role identifiers,
turn counts, and protocol payloads are unchanged. See
[docs/features/formal-proof-orchestration-contracts.md](features/formal-proof-orchestration-contracts.md).
Each orchestration run persists `verification_mode=agent|tbci`; omitted and
legacy values normalize to `agent`. The browser exposes this selector only for
the persisted `formal-proof` profile. Agent mode retains the two independent
Agent Verifiers. When `tbci` is explicitly selected, the Hub first requires the
connected Agent to advertise a ready TBCI capability; the durable reviewer then
runs TBCI alone against private before/verification-time workspace snapshots.
Bridge preserves the exact user submission separately from durable node
instructions, applies uploads once per `(runID,promptSeq)`, and freezes before
the first CLI node. Automatic contract discovery supports Rocq/Coq, Lean 4,
and Isabelle and resolves one contained proof-project root; every round reuses
that initial root, while a same-run follow-up gets a new baseline from the
previous final state plus its new uploads.
Supported Bridge builds carry a pinned TBCI executable, verify and atomically
materialize it under private Bridge state on startup, and report its non-secret
readiness metadata during registration. An administrator can disable this
managed runtime or override it with an external executable. Only `Trusted` can
finish early; all other results remain visibly unresolved without an Agent
fallback. TBCI phases are serialized, bounded by timeout and snapshot limits,
and do not hold their semaphore while Workers run. See
[ADR-015](adr/015-selectable-orchestration-verification-mode.md),
[ADR-016](adr/016-managed-tbci-runtime.md), and
[selectable formal-proof verification](features/tbci-formal-proof-verification.md).
Relay prompts reuse each worker's native session as the primary memory: a
returning worker receives only the newest cross-worker handoff plus bounded
command evidence instead of copies of its own earlier output. A worker entering
the run for the first time still receives the bounded prior history it needs.
For new formal-proof runs, Bridge keeps the selected project directory as the
CLI cwd and writes one `proof-notes/<run-id>.md` evidence ledger beneath its
`.codex-bridge` metadata directory before scheduled CLI turns begin. Uploaded
projects are materialized directly into that selected project directory. This
bootstrap is not a hidden verifier turn and does not consume the user's turn
budget. Workers run project-appropriate proof assistant commands directly and
update the ledger only with material targets,
obligations, command evidence, blockers, or decisions; Bridge does not generate
a checker or run per-turn metadata synchronization. Bridge maintains follow-up
requests inside one delimited block, retaining the newest eight bounded requests
and a count of compacted predecessors without rewriting worker-owned evidence.
See
[docs/features/formal-proof-lightweight-workspace.md](features/formal-proof-lightweight-workspace.md).
Follow-up prompts reuse the locked selected project cwd through the existing `RunCWD`
continuity path. The native-session design is documented in
[docs/features/native-interactive-orchestration.md](features/native-interactive-orchestration.md),
and the relay contract is documented in
[docs/features/orchestration-pass-through-cli.md](features/orchestration-pass-through-cli.md).
The structured handoff and convergence rules are documented in
[docs/features/structured-agent-dialogue-relay.md](features/structured-agent-dialogue-relay.md).
Runs may opt in to `native_context_compaction=after-turn`; Bridge then sends
native compaction maintenance after each successful business turn where the CLI
surface exposes a verified control channel. Codex uses app-server
`thread/compact/start`; Claude Code stream-json is skipped with an info Bridge
note until it exposes an equivalent control channel. Bridge keeps maintenance
output out of handoffs and treats compaction failures as warnings.
Profile-specific prompt fragments, assessments, manual-build carry-over, and
command fingerprint policy live behind `internal/bridge/profiles/registry` and
`internal/bridge/profiles/formalproof/`; `internal/bridge/orchestration.go`
only calls the neutral registry boundary.

Orchestration events use a typed contract in
`internal/protocol/envelope.go:OrchestrationEventPayload`. `source`
distinguishes `cli`, `bridge`, `user`, and deterministic `verifier` events; `severity` carries
Bridge-internal log levels without overloading lifecycle `status`; command
start/update/end,
run-start, turn-start, run-end, Bridge-note, and final-conclusion details live
in typed sub-payloads. `turn.start.content` is a one-line status; the full
local prompt is kept in `TurnStartData.PromptText` for authenticated local
diagnostics and is stripped from public shares. Every terminal run emits one
structured `run.conclusion` event before `run.end`, `run.error`, or
`run.cancelled`. Each `turn.end` also carries Bridge-captured start, completion,
and elapsed wall-clock time, so follow-up turns append independent timing to the
same run.

Bridge emits persisted `turn.usage` snapshots for each actual model used in a
turn, including verifier calls and observed usage on verifier failure. Model
parts are emitted without a duplicate aggregate. Native provider cost takes
precedence; catalog fallback requires an explicit known model. Unallocated
provider totals are retained separately without inventing model assignments.
Snapshots now carry worker slots. Historical statistics recover slots/models
from matching turn metadata and immutable task bindings, adding `byWorker`
series to run/task/round responses while preserving `byCli`. Official-login
bindings use native home logs during synchronization; old CLI/slot aliases are
normalized. Independent verifier series retain their assigned slot separately
from worker calls; historical verdict assignments and immutable bindings restore
identity at read time, with inherited default-model attribution labelled.
See [participant usage](features/orchestration-worker-usage.md).
Current Bridges additionally handle `orchestration_usage_sync_request` by
scanning only the named native CLI session logs read-only and returning
normalized, content-free usage events. The Hub validates run ownership and
stores those events idempotently in a separate ledger. Terminal runs trigger a
sync automatically, while the owner-scoped statistics endpoint and retry route
support historical backfill. Ledger statistics distinguish non-cached input,
cache read/write, output, and reasoning; legacy snapshots remain visible but
are explicitly incomplete. Codex app-server snapshots accumulate response
deltas within the current turn, excluding resumed history and duplicate
notifications. Snapshot statistics subtract cached Codex input before adding
cache separately; Claude native records deduplicate by assistant message ID.
Snapshots containing verifier usage or provider costs remain authoritative over
a worker-only ledger refresh. Unknown rates retain tokens and the UI displays
known cost subtotals; see
[early-completion-model-accounting.md](features/early-completion-model-accounting.md).
See
[docs/features/embedded-cli-usage-ledger.md](features/embedded-cli-usage-ledger.md).

Bridge long-command observation is controlled by
`bridge.long_command_observer`. Matching Claude commands can receive a tagged
stream-input note, and matching Codex commands emit a visible Bridge-note row
when no stdin side-channel exists. Both paths use
`BridgeNoteData.InjectedText` so the browser timeline records exactly what
Bridge said.

Transient orchestration failures use separate bounded recovery classes. An
explicit model-capacity rejection keeps the same relay turn and replays the
same prompt after 10/30/60-second backoffs. Codex app-server
`Reconnecting N/M` progress first keeps the current native turn alive for a
bounded grace period; if that native reconnect fails, Bridge recreates only
the damaged app-server process and resumes the same Codex thread after
5/15/30-second backoffs with accumulated output and command evidence instead
of replaying the original task. When a restarted Bridge resumes a Codex thread
whose prior native turn still owns the writer lease, it keeps that thread and
retries the still-unaccepted original prompt after 5/15/30/60-second backoffs.
Durable task attempts close their short-lived app-server process and unsubscribe
the persisted thread before emitting any terminal event, including failures, so
the next graph node or an external `codex resume` cannot inherit a stale writer.
Waiting, retry start, native progress, and retry exhaustion are persisted as
Bridge-originated `turn.delta` notices, and cancellation interrupts every
wait. Authentication, validation, command, and proof failures remain terminal. See
[docs/features/orchestration-transient-cli-recovery.md](features/orchestration-transient-cli-recovery.md).

Review-required Claude orchestration uses Claude Code's
`--permission-prompt-tool` support.
`internal/bridge/orchestration_claude.go:runClaude` and
`internal/bridge/orchestration_claude.go:runClaudeInteractive` write a
temporary MCP config, run `codex-bridge claude-approval-mcp` as a stdio MCP
server, and forward MCP permission prompts back to the parent Bridge over a
Unix socket. Hub then reuses existing `approval_request` and
`approval_response` frames with `payload.runId` for browser approval on the
orchestration timeline.

Codex orchestration uses `codex app-server --listen stdio://` through
`internal/bridge/orchestration_codex.go:runCodexInteractive`. App-server approval
callbacks are mapped to run-scoped `approval_request` frames with
`payload.runId`, and browser decisions return as `approval_response` frames to
the owning Bridge. The standalone `internal/bridge/appserver_runner.go:Prompt`
path remains the Codex app-server runner for chat and non-orchestration runner
uses.

In review-required mode, command execution callbacks pass through
`internal/bridge/approval_allowlist.go:isProofCommandAutoApprovable` before
browser relay. A single workspace-contained `cat`, `coqc`, or batch `coqtop`
request can receive a request-scoped approval; file changes, permission grants,
compound shell syntax, unknown options, and paths escaping the workspace keep
the normal browser approval path. The same validator is applied at the Claude
permission socket boundary.

The standalone app-server path retries process initialization and thread
preparation once, before `turn/start`; it never replays a submitted turn because
tool and file side effects are not idempotent. It recovers terminal-only agent
text, ignores empty unscoped protocol noise until an attributable terminal
event arrives, and preserves a bounded diagnostic tail when the child process
exits. `internal/bridge/session_recovery.go:runChatPromptAttempt` also retains a
bounded assistant/tool summary for ordinary chat: streamed text can complete a
missing terminal payload, while an empty or interrupted turn can issue one
short continuation in the same native thread without replaying the original
prompt. `internal/hub/ws_bridge.go:handlePromptComplete` remains the final empty
completion guard. See
[docs/features/cli-thread-recovery-and-empty-response-retry.md](features/cli-thread-recovery-and-empty-response-retry.md).

Orchestration is a native-session relay only. Each run drives one long-lived
Codex app-server thread and one long-lived Claude Code stream-json session on the
selected Bridge, and follow-up turns reuse those native conversations so the user
can `resume` them from the workspace. The Bridge streams CLI output and typed
command events to the browser and carries the previous turn's visible result
forward; it does not inject verifier, remediation, or assessment turns. The
earlier per-turn design (and the external CCB backend) was removed on 2026-05-30.

Bridge registration includes `protocol.RegisterPayload.Capabilities`. Hub keeps
the latest online capabilities in `internal/hub/pool.go` and returns them from
`GET /api/agents`, allowing the frontend to show whether Codex and Claude
orchestration execution and browser approval are available. Hub blocks
orchestration when the selected endpoint cannot execute both CLIs, and blocks
review-required orchestration when the endpoint cannot provide the required
approvals instead of falling back to `codex exec --json`.

Conversation share links are Hub-only public reads. Authenticated users create
share records for chat sessions or orchestration runs; anonymous viewers fetch
sanitized persisted transcripts through `GET /api/public/shares/<share>`. The
Bridge is not contacted for public reads, and the frontend `/share/<share>`
route renders before login bootstrap. Orchestration share sanitization in
`internal/hub/share.go:publicOrchestrationEvents` drops severity events (except
`turn.end`, whose lifecycle status stays public so failed turns close visibly),
internal Bridge notes, `TurnStartData.PromptText`, `RunStartData.CWD`, and all
native-resume fields from `RunEndData`. The worker pair, safe terminal reason,
and verifier verdict remain visible; native resume commands, thread and session
ids, transcript paths, and the run cwd never reach anonymous viewers.

## Decisions

| ID | Decision | Reason |
| --- | --- | --- |
| ADR-001 | Bridge reverse-connects to Hub | Works behind NAT without opening inbound ports |
| ADR-002 | Single Bridge WebSocket with `sid` envelopes | Multiple browser tabs share one long connection |
| ADR-003 | Short-lived Codex runner for v1 | Lower resident memory and simpler crash cleanup |
| ADR-004 | SQLite only on Hub | Single-user persistence without extra services |
| ADR-005 | Embedded native frontend | No Node build, smaller deployment surface |
| ADR-006 | Orchestration continue reuses `runID` | Follow-up tasks keep context through event compaction |
| ADR-007 | Public conversation share links | Anonymous readers can view sanitized transcripts without workspace access |
| ADR-009 | Remote CLI configuration switcher | Hub stores encrypted presets; Bridge probes providers and updates native CLI configuration |
| ADR-010 | User feature rollouts | Hub centrally evaluates account-level gray rollout policies and exposes enabled feature keys |
| ADR-024 | Native structured user input | Live native questions return browser answers to the same orchestration call |
| ADR-011 | Orchestration worker profiles and verdicts | Per-slot isolated CLI profiles and deterministic early-stop verification |
| ADR-012 | Dual Claude workers and quorum verification | Independent Claude slots and a conservative multi-checker completion barrier |

## Android presentation host

Android uses one system WebView to load the shared mobile website. The native
host owns navigation, file selection and renderer lifecycle; business state,
authentication and the reverse Bridge remain unchanged. See
[ADR 028](adr/028-lightweight-android-webview.md) and the
[mobile contract](features/android-mobile-redesign.md).

## Protocol

Every Hub-Bridge and browser-Hub frame uses:

```go
type Envelope struct {
    Type    string          `json:"type"`
    Sid     string          `json:"sid,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

Implemented frame types:

- `register`, `registered`, `heartbeat`
- `open_session`, `session_opened`, `close_session`
- `prompt`, `session_update`, `prompt_complete`
- `approval_request`, `approval_response`
- `user_input_request`, `user_input_response`, `user_input_status`, `user_input_sync`, `user_input_snapshot`
- `cancel`, `error`, `status`
- `orchestration_start`, `orchestration_event`, `orchestration_cancel`
- `agent_shutdown`

Bridge-originated `heartbeat` payloads may include `workingDirs`. Hub treats
that as live endpoint metadata, updates `agents.working_dirs_json`, and still
accepts older heartbeat payloads that only carry a timestamp.

Enrollment-token expiry limits first use. After a token is bound through
`internal/store/store.go:ConsumeEnrollTokenInfo`, only that machine id may reuse
it for reconnects, including after the bootstrap expiry; endpoint deletion
revokes the binding. Hub retains `RegisterPayload.Version` and connection time
only in `internal/hub/pool.go:BridgeConn`, and exposes them for online endpoints
through the authenticated, ownership-filtered agent list.

## Continuity

Chat continuity:

1. Hub loads `sessions.remote_thread_id`.
2. Hub sends it in `open_session`.
3. Bridge stores it in the live session.
4. The saved `remote_thread_id` is passed to the configured runner for
   follow-up prompts. Codex app-server runner paths use `thread/resume`; Codex
   exec runner paths use `codex exec resume <thread-id> -`.
5. Hub persists the latest returned thread id on `prompt_complete`.
6. When the last browser WebSocket for a chat `sid` disconnects, Hub starts an
   in-memory `leaseIdleLeased` timer in
   `internal/hub/browser_lease.go:startBrowserLease`. Reopening the same `sid`
   before `hub.browser_lease_ttl` expires calls
   `internal/hub/browser_lease.go:tryReattach`, sends the existing
   `open_session` frame again, and lets
   `internal/bridge/session.go:Open` rebind the browser output
   channel to the existing Bridge-side session. TTL expiry sends
   `close_session`; explicit session deletion still closes immediately.
   Setting `hub.browser_close_session: true` opts back into the legacy
   close-after-`browser_close_grace` behavior.

Orchestration continuity:

1. New tasks create an `orchestration_runs` row.
2. Follow-up tasks call `/api/orchestrations/{runID}/prompts` and stay on the
   run's original `agentId`; switching CLI endpoint requires an explicit new
   run. The same persisted `worker_pair` and `first_cli` values are reused
   unless the request explicitly changes them.
3. Hub compacts prior `orchestration_events` into context.
4. Hub also restores native CLI state from `orchestration_runs`: the latest
   legacy Codex thread id, Codex thread ids by worker slot, Claude session ids
   and successful-turn markers by worker slot, and the locked absolute run cwd reported by
   Bridge. The persisted
   `native_context_compaction` setting is restored with the same run.
5. Bridge receives the same `runID` with `Resume=true`, reuses any live
   run-scoped native sessions, can resume Codex and Claude by persisted native
   ids after restart where supported, and materializes new uploads under the
   locked run cwd.
6. The frontend stores the last selected run id locally and restores it on
   `/orchestrate`.

Interrupted-run recovery: a Hub restart loses every in-memory bridge
connection, so `internal/hub/server.go:recoverInterruptedRuns` runs once at
startup and settles rows the previous process left behind — non-terminal chat
`runs` and queued/running `orchestration_runs` become `failed` with a
restart reason and a `run.error` conclusion event, and `canceling` runs settle
as `canceled`. Follow-up prompts claim a terminal run back to `running`
atomically (`store.ClaimOrchestrationRunForContinue`), so two concurrent
continues cannot double-start the same run on the Bridge.

Chat session isolation:

1. Each `sessions` row stores its owning CLI endpoint in `sessions.agent_id`.
2. The frontend filters the chat sidebar by the selected agent.
3. Switching agents closes the active chat WebSocket and restores that agent's
   remembered session from `codexBridge.activeSessionByAgent`.
4. Sending from an empty agent space creates a new session for that agent.

## Authentication And User Isolation

Bridge installation detects Linux/macOS and CPU architecture in `/install.sh`.
Platform-specific HTTP downloads serve the native Hub executable or a validated
ELF/Mach-O sibling artifact. See
[Linux ARM64 installation](features/linux-arm64-installation.md) and
[macOS installation](features/macos-installation.md).

The bootstrap administrator is created by CLI/config. Operators may explicitly
enable self-service registration with a Cloudflare Turnstile Managed widget.
The browser receives only the public site key from `GET /api/auth/config`; the
Hub validates the single-use token, `action=register`, optional production
hostname, password policy, duplicate username, and a separate registration rate
limit before creating a user and issuing the existing HttpOnly JWT cookie.
Registration is fail-closed and remains disabled unless enabled with both keys.

Agents, sessions, orchestration runs, and conversation shares carry a user id.
Every authenticated HTTP and WebSocket entry loads its parent record using the
JWT subject before child messages, chat runs, or orchestration events can be
read or changed. Every user, including the bootstrap administrator, can use
both chat and orchestration but sees and controls only explicitly owned
endpoints and records. Legacy unowned agents require an explicit ownership
migration before use. Public shares remain an explicit, revocable exception
containing sanitized read-only data.

The bootstrap and additional configured administrators have one explicit, read-only
cross-user analytics boundary: `GET /api/admin/usage` and
`GET /api/admin/users/{userID}/usage`, plus the on-demand read endpoint
`GET /api/admin/users/{userID}/conversations/{kind}/{conversationID}`. The
overview exposes aggregate activity, endpoint, workload, Token, and
price-estimate fields. User detail adds conversation metadata and usage without
content. The final endpoint checks ownership and returns only message bodies or
the orchestration prompt and visible text events. Workspace paths and native
session identifiers are excluded, and no administrator endpoint grants access
to user-scoped mutation APIs. See
[docs/features/admin-usage-dashboard.md](features/admin-usage-dashboard.md).

## Storage

Long-history interactive reads keep the existing single-connection model and use
sparse event indexes rather than reading unrelated output bodies. Migration,
rollback and query-plan requirements are recorded in
[ADR 027](adr/027-sparse-orchestration-read-indexes.md).

SQLite tables:

- `users`
- `agents` (`deleted_at` soft-deletes CLI endpoints while preserving history)
- `sessions`
- `messages`
- `runs`
- `enroll_tokens`
- `orchestration_runs` (including persisted mode, `worker_pair`, `first_cli`,
  `profile`, cwd, max turns, status, native CLI continuity state,
  `codex_thread_ids_json`, `claude_session_ids_json`,
  `claude_started_slots_json`, native context compaction preference, locked
  runtime cwd, uploaded file metadata, reversible `archived_at` history metadata,
  and a durable cancel-before-delete intent)
- `orchestration_worker_profiles` (private per-run Bridge-targeted preset
  ciphertext, excluded from run JSON, progress responses, and public shares)
- `orchestration_events` (including `source`, `severity`, lifecycle status,
  and typed event payload JSON)
- `orchestration_usage_syncs` and `orchestration_usage_events` (private native
  session scan state and normalized per-call counters; no transcript content)
- `orchestration_task_graphs`, `orchestration_tasks`,
  `orchestration_task_dependencies`, and `orchestration_task_attempts`
  (bounded scheduling, dependency state, planner/plan-reviewer/worker roles,
  identity, retry lineage, and evidence)
- `conversation_shares`
- `cli_config_presets` (user-scoped provider metadata plus an encrypted-at-rest
  credential vault value)
- `cli_config_preset_credentials` (per-Bridge API-key envelopes materialized
  from the vault or retained from legacy machine-scoped presets)
- `cli_config_active_presets` (machine-scoped native CLI activation pointers)

History archive is separate from deletion and execution state. Owned terminal
runs may be archived/restored; continuing a run restores visibility without
changing its ID or native context. Usage includes archived history. The history
API filters ownership, endpoint, status and search before stable creation-time
keyset pagination. See [ADR 026](adr/026-orchestration-history-metadata.md) and
[desktop audit fixes](features/desktop-audit-fixes.md).

Hub stores browser auth, chat history, and encrypted-at-rest user model
credentials. Bridge stores its generated `machine_id`, persistent configuration
switcher decryption key/state, and writes native Codex/Claude credentials on the
private machine. Configuration test/apply/reset/export frames are
capability-gated and request-correlated; export wraps a key to Hub's vault
receiver and requires Bridge capability version 2. Older Bridges retain their
existing machine credential behavior until upgraded.

Canceled planning identity is persisted before completion through existing
`bridge.note` events (`internal/bridge/orchestration_native_identity.go:nativeIdentityEvent`).
The existing usage-sync request optionally carries bounded historical recovery
hints; only the private Bridge reads matching native logs, returning usage rows
without log text. See [participant usage](features/orchestration-worker-usage.md).
Decision details: [ADR 022](adr/022-canceled-planning-native-identity.md).

Historical recovery scheduling uses `usageModelRecovery: 2` capabilities, persistent
zero-usage retry markers and serialized private scans. Completed runs with earlier
syncs remain eligible when planning identity is missing. See [ADR 023](adr/023-retryable-native-model-recovery.md).
