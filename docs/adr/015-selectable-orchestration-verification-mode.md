# ADR-015: Selectable Orchestration Verification Mode

## Status

Accepted

## Context

Durable formal-proof orchestration currently uses two independent Agent
Verifiers after its reviewer task. TBCI adds a machine-checkable alternative,
but making it an implicit pre-check or automatic fallback obscures which system
actually authorized completion. A browser-only toggle would also be lost after
a reload or follow-up prompt.

## Decision

An orchestration run persists one `verification_mode` value. `agent` is the
default and preserves the existing two-Agent policy. `tbci` is accepted only
with the `formal-proof` profile and makes TBCI the sole completion verifier for
that decision. The Bridge never invokes both modes for one reviewer result and
never silently falls back from one to the other.

The mode is carried as optional `verificationMode` in HTTP and Bridge payloads.
Omitted and legacy values normalize to `agent`. Follow-up prompts reuse the
persisted mode when the field is omitted, and an explicitly supplied valid mode
updates the same run before dispatch. Durable graph payloads copy the field
unchanged across rounds.

SQLite stores the value directly on `orchestration_runs` because it is public,
non-secret run configuration required by run lists, reloads, continuation, and
public-share rendering. It does not belong in the private encrypted worker
profile table or only inside task-graph JSON.

## Consequences

- Existing rows and clients retain Agent Verifier behavior.
- The browser can accurately restore and display the selected authority.
- TBCI unavailability is visible and conservative; it cannot trigger model
  verification that the user did not select.
- Changing the mode during a follow-up keeps the same run and native sessions,
  but the new explicit value becomes the run's persisted setting.
- Schema, CRUD, HTTP, protocol, Bridge, frontend, share, documentation, and
  continuity tests must move together.
