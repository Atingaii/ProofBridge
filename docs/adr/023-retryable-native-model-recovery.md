# ADR 023: Retryable historical model recovery

Status: Accepted

## Context

ADR 022 introduced evidence-based recovery but did not complete its lifecycle.
Reconnect backfill skipped every run with any prior sync, read only a bounded
recent event window, and could send recovery hints to Bridges that ignored them.
Discovery failures left no durable retry state. Exec model lookup read only the
last 4 MiB, which can omit the model context before a long command output.

## Decision

Keep exact native evidence as the authority. Add `usageModelRecovery: 2` to
Bridge capabilities and deterministic versioned recovery markers to the existing
usage-sync session results. Markers carry only completion/failure and attempt
time, never tokens; no SQLite schema migration. An updated Bridge serializes
usage scans and records failed/ambiguous/budget-limited recovery attempts.

Reconnect backfill and terminal stats reads use the compact complete usage
timeline, retry missing planning identities even with prior successful unrelated
syncs, and respect a six-hour persistent retry interval. A short in-memory
request cooldown prevents simultaneous page reads from duplicating scans.
Old Bridges retain ordinary session sync; automatic recovery requires the new
capability. Stats expose a small model-recovery status so a required endpoint
upgrade is distinguishable from a completed unsuccessful native-log search.

Search the most recent bounded 64 MiB of model metadata, validating the
exact native session ID. Native-log discovery checks the UTC date directory and its
two neighboring dates, covering local-date layouts, with the same
candidate/total-byte budgets and exact cwd/time/token fingerprint requirements.
A candidate log may consume up to the remaining 64 MiB discovery budget.
Do not substitute the current preset or a later model. Preserve immutable graph
IDs when attributing configured worker models, rather than second-resolution
timestamps when an exact graph ID exists.

## Consequences and alternatives

This is automatic repair of retained private evidence, not reconstruction from
token counts alone. An old private Bridge must be upgraded before new discovery
code can run there. Expose that state accurately; never claim a guessed model as
observed. No conversation replay, new task, context change or native log upload.

## Exit gates

Test prior complete sync plus missing planning model, cooldown/retry markers,
old capability, compact historical reads, exact graph identity, long native
logs, date layouts, unique/ambiguous matches, no token duplication, serialized
scan behavior and all existing CLI-pair accounting. Build embedded UI and all
four delivery binaries, run Go checks serially, verify deployed stats/health.

References: `internal/hub/ws_bridge.go:scheduleHistoricalUsageBackfill`,
`internal/hub/orchestration.go:handleOrchestrationStats`,
`internal/bridge/usage_recovery.go:recoverPlanningUsageSessions`,
`internal/bridge/orchestration_verifier_model.go:resolveCodexVerifierModel`.
