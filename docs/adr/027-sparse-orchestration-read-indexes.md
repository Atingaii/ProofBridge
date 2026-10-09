# ADR 027: Sparse indexes for orchestration progress and administration

Status: Accepted

## Context

`internal/store/store.go:Open` keeps a single SQLite connection. Long runs can
contain hundreds of thousands of output events. Plan-marker text matching and
administrator legacy usage reads previously visited unrelated event bodies,
holding that connection while interactive requests and writes waited.

## Decision

Keep the existing connection model and append-only event schema. Add idempotent
partial indexes for the exact predicates consumed by progress and legacy usage
queries. Share predicate definitions between migration and queries where
appropriate; verify index selection with `EXPLAIN QUERY PLAN`.

`idx_orchestration_events_progress` covers plan markers plus `plan-review` task
metadata; marker-only scheduling queries include the shared predicate so SQLite
can select the same index. `idx_orchestration_events_legacy_usage` covers exact
legacy usage eligibility. Invalid historical JSON must remain safe to read.

Progress reads retain every plan marker and required review boundary while
omitting unrelated output. An old plan must survive arbitrarily many subsequent
events. Usage reads retain historical fallback and ledger-precedence semantics.
Do not truncate source histories to meet a latency target.

The browser prioritizes conversation details/history over progress, cancels
superseded reads, and prevents overlapping polling. Administrator reads use the
existing statistics deadline and fail explicitly on interrupted aggregation.

## Alternatives

- A larger connection pool would alter transaction and concurrency assumptions
  while leaving expensive scans intact; defer it until indexed reads are measured.
- A response cache would help repeated reads but leave first access expensive and
  require correct identity, pricing, range and mutation invalidation; not needed
  for this fix.
- A materialized projection adds backfill and write-path maintenance; partial
  indexes address the observed workload with less lifecycle complexity.

## Migration and rollback

`internal/store/store.go:Migrate` builds indexes once on an existing database.
This requires a scan during upgrade; measure it and retain an online backup.
Each index scans existing events independently. On a large database this can
take minutes before the Hub starts listening. Schedule the first upgrade in a
maintenance window after checking active runs; do not assume an ordinary short
restart. Measurements on a backup are estimates because host load and SQLite
implementations differ. Subsequent starts reuse the indexes.
Existing rows, protocol payloads and Bridge binaries are unchanged. An older Hub
can use the same database with these additional indexes left in place.

## Verification

Long-history fixtures compare old and new results and query plans. Tests cover
early plan retention, ledger precedence, ownership and cancellation. Browser
tests delay responses and switch runs/ranges to verify request ownership.
See [loading performance](../features/site-loading-performance.md).
