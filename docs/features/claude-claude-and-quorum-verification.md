> Completion-policy update: [independent early run completion](independent-early-run-completion.md) supersedes the reviewer-only barrier and incomplete stagnation result described below. Completion decisions ignore planning; all automatic early closures show completed while retaining factual checks. Other feature behavior remains applicable.

# Claude + Claude And Quorum Verification

## Goals

- Add `claude-claude`, with independently selected `claude-a` and `claude-b`
  presets and native Claude sessions.
- Replace the sole early-stop verifier decision with two independent Agent
  Verifiers. Each verifier inspects the real project workspace using only the
  original task prompt; Worker history and local command facts are excluded.
- Deploy the new behavior at `https://proofbridge.sparkon.cn` using an
  independent Cloudflare-routed Hub and SQLite database.
- Preserve the existing browser workflow, task graph, plan, branch map, and
  default Claude + Codex behavior.

## Non-Goals

- Do not run an unbounded judge pool; use exactly the two configured role
  presets after the durable final reviewer.
- Do not migrate or write to the live `sparkon.cn` database.
- Do not alter global Codex or Claude configuration.

## Open-Source Inputs

`ccswitch` informs named provider profiles and isolated configuration
materialization. DeepSeek Harness AgentTeams informs explicit participant
ownership, durable work state, dependency-gated task progression, direct
handoff records, and a live activity model. This implementation takes those
constraints rather than adding its daemon, filesystem mailbox, or plugin
runtime.

## Data And Protocol Impact

- `workerPair` accepts `claude-claude`; valid slots are `claude-a` and
  `claude-b`.
- Native resume metadata carries a worker slot so both Claude sessions are
  visible without one overwriting the other.
- A verifier verdict includes named checker results. Existing event,
  timeline, plan, and branch-map consumers continue to use the same event.

## Implementation Steps

1. Extend protocol, Hub validation, capability checks, task-graph slot
   scheduling, and profile authorization for `claude-claude`.
2. Change Bridge native Claude state to be keyed by worker slot, including
   private runtime, interactive process, resume ID, transcript lookup, and
   cleanup.
3. Run role 1 and role 2's presets after the durable final reviewer in fresh,
   serial verifier sessions pointed at the same real project workspace.
   Wait for both structured results; one passing model is sufficient for early
   termination. A verdict where both models continue advances the next
   configured round.
4. Add compact UI selection and safe rendering for the new pair and checker
   details; rebuild embedded UI.
5. Add fake-CLI tests for separate Claude runtime/session state, Hub payload
   validation, pair scheduling, checker quorum behavior, and frontend build.
6. Provision the separate Hub/Bridge service, Cloudflare DNS record, and
   independent SQLite state; verify health, login, Bridge registration, and
   both the existing and isolated public endpoints.

## Exit Gates

- `claude-a` and `claude-b` use different saved presets and their child
  processes receive different private configuration directories.
- No Claude-A transcript/process/runtime is reused by Claude-B.
- Early termination requires at least one passing Agent Verifier; only two
  continuing or invalid verifier results prevent it. Each verifier determines
  evidence by inspecting the current workspace itself.
- The existing `claude-codex` and `codex-codex` paths retain their selectors,
  graph rendering, and follow-up behavior.
- The new hostname serves a fresh database and does not change the live Hub.

## Reviewer Q&A

**Are the Verifiers independent agents?**

Yes. Bridge starts two fresh native CLI calls using the two role-bound presets;
neither receives the other's result or reuses a worker conversation. Both run
against the actual workspace and must independently inspect files and perform
checks before returning validated JSON.
