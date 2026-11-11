# ADR 022: Native identity for canceled planning calls

Status: Accepted

## Context

An old canceled official-login planning attempt can retain native token totals
but lose both the CLI session ID and default model. The Hub cannot infer that
model from a later graph or a mutable current preset. Missing IDs also prevent
existing explicit-session usage sync from finding the original native log.

## Decision

Persist native identity through the existing private `bridge.note` event stream
before turn completion. No database schema, event envelope or browser command
changes. The optional local `RunnerRequest.OnNativeIdentity` callback lets the
orchestration caller persist app-server thread preparation without changing
ordinary chat notices or the Runner interface.

Extend `protocol.OrchestrationUsageSyncRequest` with optional `recovery` hints
constructed from immutable official-login planning evidence. Each hint carries
recorded cwd, worker slot, start/end and native token totals. Only the Bridge
examines local logs; it returns ordinary usage events and session scan results.
No prompt/log content or credentials cross this boundary. The browser continues
to use the existing usage-sync endpoint. Old Bridges ignore the optional field.

Use exact metadata and token fingerprints, accept only a unique candidate, and
retain unknown on ambiguity or incomplete evidence. Discovery checks at most
8 hints, 256 candidate files per hint, 16 MiB per matching log and a shared
64 MiB discovery budget. Search only native date directories around the start.
Existing explicit-session sync behavior is preserved. Full feature scope,
rollout requirements and exit gates are in
[participant usage](../features/orchestration-worker-usage.md).

## Alternatives and consequences

Guessing the first model from a later worker produces unsupported billing.
Replaying work would charge again and would not establish historical identity.
Scanning arbitrary workspaces is unnecessary. Bounded private-log discovery can
repair completed runs without replay but requires an updated private Bridge,
retained native logs and unique evidence. Oversized or conflicting records may
remain unresolved. The Hub deployment alone cannot repair every missing model.

## Validation

Cover canceled exec calls, app-server identity before cancellation, latest model
on resumed sessions, both persisted event JSON formats, unique and ambiguous
historical fingerprints, wrong cwd/time/tokens, isolated bindings and file size
limits. Preserve native totals, all worker-pair billing and public-share privacy.

References: `internal/protocol/envelope.go:OrchestrationUsageRecovery`,
`internal/hub/orchestration_planning_usage.go:planningUsageRecoveryHints`,
`internal/bridge/usage_recovery.go:recoverPlanningUsageSessions`.
