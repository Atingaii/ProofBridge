> Completion-policy update: [independent early run completion](independent-early-run-completion.md) supersedes the reviewer-only barrier and incomplete stagnation result described below. Completion decisions ignore planning; all automatic early closures show completed while retaining factual checks. Other feature behavior remains applicable.

# Agent-Verifier Round Continuation

## Goals

- Make early orchestration completion depend on at least one passing verdict
  from two independent Agent Verifiers after every round's durable
  reviewer, rather than a Bridge-local semantic gate.
- Require each verifier to inspect the real project workspace independently
  using only the original task prompt as its task input.
- Continue through intermediate collaboration rounds when neither agent
  confirms completion, unless stable repeated blockers stop it as incomplete.
  Final rounds also verify; see
  [completion-verification-and-stagnation.md](completion-verification-and-stagnation.md).
- Present planning and plan review as pre-collaboration stages, not as
  collaboration round numbers.
- Remove the `strict-workspace` (automatic execution limited to the workspace)
  option from the browser's new-endpoint selector without changing server
  support or existing endpoint behavior.

## Non-Goals

- Do not add a third worker, change the `candidate-a -> candidate-b ->
  integrate -> review` topology, or alter worker-slot ownership.
- Do not change protocol frames, persisted schema, main-site deployment, or
  existing endpoint compatibility.
- Do not let Worker messages, handoffs, plans, command summaries, or recorded
  local facts influence an Agent Verifier's decision.
- TBCI is a separately selected `formal-proof` verification mode. When TBCI is
  selected, this Agent Verifier barrier is not invoked.

## Data And Protocol Impact

- Existing verifier events retain their shape. Checker entries describe each
  verifier's own task, workspace, and verification checks; no Worker-derived
  `recorded/` checks are included.
- The Hub still advances a completed durable reviewer node by its existing
  task-graph transition. `verified-early` remains the existing terminal reason.
- No database migration or wire-field change is required.

## Implementation Steps

1. Run two role-bound verifier agents serially after every durable `review` node.
   Each receives the full original `TaskPrompt` (legacy fallback: `Prompt`),
   excluding internal durable-node instructions, and runs in the same real project
   workspace as the Worker, under its configured execution profile. Build
   outputs are permitted when authorized; editing source is prohibited.
2. Wait for both verifier calls to return. At least one passing verdict
   produces `verified-early`; both results remain visible for audit even when
   they disagree.
3. On a non-final round, emit ordinary task completion after a continuing
   verifier verdict so the Hub dispatches the next graph. On the final round,
   still verify and emit `max-turns` with a blocked conclusion if incomplete.
4. Hide collaboration round metadata on `planner` and `plan-reviewer` timeline
   events and label graph navigation from actual round-bearing work graphs.
5. Hide `strict-workspace` in the frontend selection list while preserving
   repair commands and status visibility for endpoints that already use it.
6. For a completed formal-proof audit, distinguish an actual open proof,
   checker, statement, shortcut, or trust issue from a reviewer merely noting
   the bounded scope of completed source inspection or suggesting an optional
   repeat of an already-passing command. Final reviewer guidance requires
   `next=none; risks=none` for the latter case.

## Exit Gates

- Two Agent Verifiers run after each durable reviewer task, including the last.
- A verifier that cannot inspect the workspace, cannot validate the task, or
  finds a missing formal checker advances an intermediate round instead of
  ending the run immediately.
- An early successful end requires at least one of the two Agent Verifiers to
  pass. The run continues only when both return `continue` or fail validation.
- A completed formal-proof audit is not kept alive solely by an optional
  re-audit or a statement that every tactic body was not read verbatim.
- A final reviewer cannot claim satisfaction without independent verification;
  genuine execution failures and cancellation retain their terminal states.
- Planning and plan review never display as `Round 1/N`.
- Frontend build, focused Bridge tests, task-graph tests, full Go tests, doc
  lint, and experimental deployment health checks pass.

## Reviewer Q&A

**Can a verifier rely on the Worker saying that a proof checker passed?**

No. The verifier must inspect the current workspace and run or inspect the
required checker itself. Worker handoffs and command output are intentionally
absent from its prompt.

**What reaches the next round?**

The Hub prioritizes the latest reviewer's handoff, conclusion and independent
failed checks in the next round's context, alongside the current reviewed plan.
Planning itself runs only once per new user task.

**Why does one passing verifier now end the run?**

The two verifiers remain independent and both verdicts remain visible, but the
experimental policy favors stopping once either verifier independently confirms
the original task in the current workspace. A disagreement is therefore
recorded without consuming another collaboration round.

Follow-up task-input, cancellation, and model-accounting corrections are defined
in [early-completion-model-accounting.md](early-completion-model-accounting.md).
