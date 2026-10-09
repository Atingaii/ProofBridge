# ADR 026: Reversible orchestration history metadata

## Status

Accepted for implementation, 2026-09-30.

## Context

The orchestration list handler previously returned only
a bounded recent list. Client-side endpoint filtering could hide older runs,
and history had no reversible organization controls. Deleting or recreating
runs would violate conversation continuity and lose accounting context.

## Decision

Add `orchestration_runs.archived_at INTEGER NOT NULL DEFAULT 0` through
`internal/store/store.go:Migrate`, expose optional `archivedAt`, and keep title
and archive mutation behind authenticated Store/Hub operations. Existing rows
remain visible. Archive only terminal runs; continuation restores visibility
atomically with the status update. Archive never deletes messages, native
identities, graphs or usage.

Filter authenticated history in SQL before a descending `(created_at, id)`
keyset limit. Add suitable user/archive/creation and endpoint indexes after
checking the existing indexes. Bind search values and validate cursor/filter
inputs. Keep the existing list envelope with an additive `nextCursor`.

The full API and UI contract is in
[desktop audit fixes](../features/desktop-audit-fixes.md).
The Store boundary is
`internal/store/orchestration_history.go:UpdateOrchestrationMetadata`; the list
handler is `internal/hub/orchestration_history.go:handleListOrchestrations`.

## Alternatives

- Keeping the latest 200 records and filtering in the browser cannot retrieve
  older records and incorrectly implies that a machine has no history.
- Offset paging can skip or repeat items when new runs are inserted.
- Hard deletion sacrifices continuity and accounting; a second archive table
  duplicates ownership/lifecycle logic unnecessarily.

## Consequences

The migration is additive and re-runnable; Store scanners/CRUD and tests must
include the new field. Archived records remain addressable by their owner and
participate in aggregate statistics. Older clients ignore the added JSON field.
This affects Hub storage and UI only, with no protocol or Bridge update.
