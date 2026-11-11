> **DEPRECATED - Stagnation now closes the run as completed; verification is independent of planning.**
>
> Current design: [independent early run completion](independent-early-run-completion.md). Historical only; do not implement from this doc.

# Completion verification and stagnation

## Goals

Fix independently observed verifier sandbox failures; end a verified task before
its round budget, and stop repeated unproductive verification with an explicit
blocked result rather than declaring success. Run planning only once per user
task. Preserve original-task identity, follow-up continuity, and bounded load.

## Non-goals

No completion from low token counts alone, no permission escalation beyond the
endpoint's configured execution profile, no automatic permission fallback, no
model changes, and no schema migration. Do not silently upgrade remote Bridges.

## Data and protocol impact

Add optional bounded completion observations to the existing start/end payloads.
Hub persists them in graph payload JSON across internal rounds; a new user task
starts fresh. Each reviewer observation records a bounded source fingerprint,
its native output-token counts and actual model, and a verification blocker key.
Unknown/truncated fingerprints or usage cannot establish convergence. Public
shares omit these internal observations. Existing clients ignore optional fields.

## Implementation steps

1. Verifier CLI commands honor the existing Bridge permission profile. Strict
   workspace keeps the existing outer Landlock boundary; do not nest a conflicting
   Codex sandbox inside it. Read-only profiles stay read-only; auto-execute allows
   proof compiler outputs. Verifiers may build but must not edit source or expand
   permissions. Compare bounded source fingerprints around verification.
2. Return verification failure details to workers through the next round's
   context; show concrete reasons in the timeline. Reject a malformed, canceled,
   or source-mutating pass. Distinguish budget exhaustion from verified completion.
3. Persist up to eight reviewer observations. A native-output plateau compares
   the same role/slot/model, excludes planning/cache/verification tokens, and
   requires three low stable samples versus three preceding samples. It requests
   a convergence diagnosis, never grants a pass. Three unchanged source states
   with the same independently reported blocker stop as blocked; persistent
   verification infrastructure failures are classified separately.
4. Keep planning and plan review on round one only. Preserve reviewed plan and
   updates in later workers' context; planning consumes tokens/time but no extra
   execution round. New follow-up tasks receive their own planning phase.
5. Harden Hub acceptance of verified terminal reasons, continuation, and recovery;
   validate both collaboration and debate modes with deterministic CLI fixtures.

## Exit gates

Permission argument coverage, real subprocess fixture compilation and verdict
parsing, source-mutation rejection, low-token-but-incomplete and missing-usage
regressions, blocker persistence, planning-once and follow-up reset, Hub stop and
stale-event handling tests. Run focused and full Go tests serially, frontend
reducer checks/build if touched, and doc lint. Build both supported architectures
with bounded concurrency; report deployment and remote-client update limits.

## Reviewer Q&A

**Can low consumption prove a Coq theorem?** No. Only the existing independent
verification path can declare completion; a plateau is diagnostic context.

**Why did existing runs use every round?** Inspected historical records include
repeated Bubblewrap namespace failures inside both independent verifiers, despite
worker execution succeeding. Another observed run correctly ended at round 2/12.

**Does the plan impose extra requirements?** No. The original user task is the
acceptance contract. Plans are advisory progress tracking, may be corrected, and
must not postpone an independently verified completion.

**Why keep a blocked outcome?** Compilation failure, missing requirements, and
inaccessible tools are not successful verification. Repeating the same verified
blocker without source changes should not consume the entire budget indefinitely.

## Verification record

2026-09-09: full Go suite passed with `GOMAXPROCS=2`, `-p 1`, `-parallel 1`.
Regressions cover both orchestration modes, last-round validation, malformed and
ambiguous verdicts, canceled work, permission profiles, source mutation, repeated
blockers, model/sample comparability, planning once, failed-check prioritization,
follow-up reset, stale terminals, and public-share sanitization. A deterministic
CLI fixture ran real Coq 8.15 compilation; another exercised the actual
Bridge/WebSocket/Hub persistence path. No paid model invocation was used.

Frontend checks and embedded static build passed. Doc lint reported zero errors
and the pre-existing method-anchor scanner warning for
`internal/hub/server.go:Server.Run`. Integration fixtures now keep Hub databases
outside proof workspaces and wait for browser subscription registration before
injecting an approval event.

The isolated experimental site was deployed as `v0.3.65-exp-completion` on
2026-09-09. Hub and its local Bridge were restarted on the new binary; health,
static assets, and Bridge reconnection checks passed. Linux amd64 and arm64
artifacts were built serially, and public downloads matched the built checksums.
The previous binaries remain available for rollback. Remote private Bridges
must be updated and restarted separately to use the new verifier behavior.

## Suggested commit message

```text
fix(bridge): verify completion consistently and stop repeated blockers

Change summary:
- internal/bridge/orchestration_verifier.go: honor configured permissions and validate every reviewer round
- internal/bridge/orchestration_completion.go: bound native usage and workspace stagnation diagnostics
- internal/hub/task_graph.go: persist failure feedback, plan once, and require verified satisfaction
- internal/protocol/envelope.go: carry private optional completion observations
- internal/integration/bridge_flow_test.go: verify transport/persistence and synchronize test subscriptions

Exit gate:
- [x] Serial Go suite, real Coq subprocess fixture, and both orchestration modes
- [x] Frontend checks, embedded build, and doc lint

Doc-Impact: docs/adr/019-completion-observation-continuity.md, docs/features/completion-verification-and-stagnation.md, docs/features/agent-verifier-round-continuation.md, docs/features/configured-round-completion-and-usage.md, docs/features/orchestration-plan-progress-workspace.md, docs/features/claude-claude-and-quorum-verification.md, docs/architecture.md, docs/code-map.md, docs/dev-workflow.md, docs/change-impact.md, docs/deployment.md, docs/plan.md
```
