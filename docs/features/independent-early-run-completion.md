# Independent early run completion

## Goals

- Show one completed result for every automatic early closure while retaining
  accurate independent check records; never label an unproved obligation proved.
- Remove plan output and progress from completion decisions entirely.
- Verify a resolved execution handoff immediately and stop undispatched work
  atomically if the independent verifier passes, in either orchestration mode.
- Improve stable blocker identity, explicit verification conflicts, cancellation,
  task continuity, and bounded resource use.

## Explicit non-goals

No planner-derived acceptance contract, extra corrective round on stagnation,
token-only success, new LSP service, framework migration, or new permissions.
The existing opt-in TBCI frozen-contract verifier remains a separate checking
implementation; its input must not be inferred from plan items. Compiler success
alone does not establish semantic task satisfaction.

## Data and protocol impact

Add optional checker `code`, `target`, and `evidence` for concrete failed
obligations and optional completion observation task identity. Keep legacy
checker JSON valid. Add factual verification status to final conclusions.
`stalled` becomes an ordinary `run.end` with `completed` outcome, while its
verifier verdict remains `continue`. Public UI labels automatic early endings
completed and renders check details below the result. No SQLite schema change.

## Implementation steps

1. Keep the original task as the independent verifier's only task input. Exclude
   plan nodes from completion sampling and from candidate verification triggers.
2. Permit resolved worker/integrator handoffs to trigger isolated verification;
   persist an independently verified early close atomically with its attempt,
   cancel only undispatched successor nodes, and prohibit stale resurrection.
3. Normalize concrete blocker identity by checker category and target rather
   than model prose where structured facts exist. Preserve legacy fallback;
   separate invocation errors from proof defects. Known verification conflicts
   must not be erased by another model's pass.
4. Close three-round stagnation immediately as execution completed. Retain the
   actual verdict and reasons in the final audit record. Missing usage or unknown
   snapshots cannot fabricate success or repeated-source evidence.
5. Serialize heavyweight independent checks, account for all model usage, and
   preserve cancellation, bounded history, and same-run follow-up resets.
6. Verify Bridge, Hub, persistence, and UI with deterministic fixtures and real
   small Coq compilation where available; build and deploy serially.

## Exit gates

Both orchestration modes: early verified handoff, unresolved candidate continues,
planner isolation, three-round automatic completed result with failed audit
retained, no next dispatch, replay/cancel/reconnect guards, original-task reset,
structured blocker stability, conflict handling, native usage preservation, and
frontend result labels. Run focused tests then Go suite with `GOMAXPROCS=2 -p 1
-parallel 1`, frontend checks/build, and doc lint. No paid model load test.

## Reviewer Q&A

**Does completed mean the theorem was proved?** It means this run ended. Only a
`pass` independent verdict establishes verified satisfaction; failed checks stay
visible as audit records even when automatic closure has a completed result.

**Can a plan mark a task done or prevent completion?** No. Plan output, progress,
and checkboxes do not enter the verifier input or the stop condition.

**Why no corrective round?** The user explicitly requested direct closure after
the repeated unchanged-blocker condition.

**Which open-source mechanisms are reused?** Typed/composable stop conditions
and explicit state transitions, as used by AutoGen and LangGraph, plus bounded
repeat detection as in OpenHands. No external framework is installed.

## Validation record

- `GOMAXPROCS=2 go test -p 1 -parallel 1 ./...`: passed, including both modes,
  stale/duplicate attempt guards, early execution verification, three-round
  closure with a retained failed verdict, and protocol compatibility.
- `npm test`: passed, including actual result-card rendering with one completed
  label and the original unproved-target check record.
- `npm run build`: passed; embedded UI regenerated.
- `TestVerifierAuthorizedExecutionCompilesCoq`: passed with local `coqc`.
- `make doc-lint`: passed with the existing `Server.Run` symbol warning.
- All model paths used deterministic CLI fixtures; no paid-model load test.

## Experimental rollout

Version `v0.3.66-exp-completion` updates the experimental Hub, local Bridge, and
Linux amd64/arm64 downloads. Public health and embedded-asset checks passed;
both public binary checksums match the builds. Remote endpoints must reinstall
and restart their Bridge to receive the new candidate and blocker logic.
Incoming old stagnation errors already receive the completed lifecycle policy
at the updated Hub. No historical run data is rewritten.

## Suggested commit message

```text
fix(bridge): decouple early completion from planning and unify run results

Change summary:
- internal/bridge/: verify execution candidates and close stable blockers independently of plans
- internal/store/task_graph.go: atomically close verified graphs and cancel undispatched successors
- internal/hub/: persist completed lifecycle results while preserving factual checks
- internal/protocol/: retain optional task identity, blocker evidence, and verification status
- frontend/: show a single completed result and retain check records

Exit gate:
- [x] Low-concurrency Go suite, frontend checks/build, real small Coq build, doc lint

Doc-Impact: docs/adr/020-independent-early-run-completion.md, docs/adr/019-completion-observation-continuity.md, docs/features/independent-early-run-completion.md, docs/features/completion-verification-and-stagnation.md, docs/features/agent-verifier-round-continuation.md, docs/features/configured-round-completion-and-usage.md, docs/features/orchestration-plan-progress-workspace.md, docs/features/claude-claude-and-quorum-verification.md, docs/features/durable-bounded-orchestration-task-graph.md, docs/architecture.md, docs/code-map.md, docs/dev-workflow.md, docs/change-impact.md, docs/plan.md
```
