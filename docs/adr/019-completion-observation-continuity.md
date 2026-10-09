> **DEPRECATED - Completion policy and task-bound observations are superseded.**
>
> Current design: [ADR-020](020-independent-early-run-completion.md). Historical only; do not implement from this doc.

# ADR-019: Durable completion diagnostics

Status: Accepted

## Context

Independent verification failures can recur across durable graph generations.
Per-node in-memory history cannot detect that recurrence, and aggregate token
charts mix planning, worker and verifier costs. A low chart value is not proof.

## Decision

Carry at most eight `protocol.CompletionObservation` values in the existing
`OrchestrationStartPayload` graph JSON, populated from reviewer `RunEndData`.
No table migration is needed. Samples contain source-content digests, comparable
native reviewer-output usage, and normalized blocker identities. They are private
diagnostics, stripped from anonymous shares, reset for a new user prompt, and
never accepted as a successful verdict. Use the existing blocked conclusion and
failed execution status for a repeated stalled attempt, with terminal reason
`stalled`. A normal round-budget end remains an execution completion but cannot
claim satisfied if independent verification continued.

The Bridge verifier honors existing endpoint execution permissions; no permission
fallback is introduced. The configured outer strict-workspace boundary remains
active. Planning runs only on the first execution graph of each user task.

## Consequences

Older Bridges omit diagnostics and retain existing behavior; updating only the
Hub does not fix their verifier invocation. Updated payloads remain optional and
compatible. Bounded scans can disable progress detection for large/unreadable
workspaces without disabling independently evidenced completion.

Implementation, gates and reviewer Q&A:
[completion-verification-and-stagnation.md](../features/completion-verification-and-stagnation.md).
