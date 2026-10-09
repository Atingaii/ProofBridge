# Conversation and statistics loading performance

## Goals

- Open long conversations without progress reads monopolizing the Hub database.
- Read Token and administrator statistics without scanning unrelated output.
- Show immediate loading feedback, bounded failures and a retry action.
- Cancel superseded reads and keep slow polling from accumulating requests.

## Progressive account usage overview

`internal/hub/orchestration.go:handleUsageOverview` supports an opt-in bounded
read with `limit=1` and an optional opaque `cursor`. Positive limits are capped
at three runs. Requests without `limit` retain the complete historical response
for older clients; a cursor without a limit is rejected.

The existing `overview` value contains this page's complete per-run aggregates.
An additional `page` object contains `hasMore`, optional `nextCursor`, and
`scannedRuns` (source runs in this page). A run without billable usage still
advances the cursor. Only `hasMore=false` marks the end; a failed or canceled
page returns an error without a successful aggregate/page object.

`internal/hub/usage_overview_page.go:parseUsageOverviewPage` fixes the initial
time-range cutoff and validates the range on subsequent requests. The nested
Store history cursor binds ownership and uses the existing `(created_at,id)`
ordering with archived runs included. Run creation/update times do not filter
billable events. This is a stable traversal with a fixed cutoff, not a database
snapshot spanning all requests; active runs can continue recording usage.

The browser requests one run at a time, displays an explicit in-progress count,
and accumulates successful pages without treating partial values as final.
Machine counts are deduplicated from item machine IDs; daily buckets are summed,
and cost completeness is combined only across nonempty usage contributions.
Failed pages retry their cursor without double counting. Range changes cancel
the traversal. Each page retains the existing server/browser deadline rather
than extending the deadline for the whole account.

Exit gates include page-sum equivalence to the complete API on static fixtures,
same-timestamp runs, archived/foreign runs, empty source pages, event-time ranges,
fixed cutoffs, invalid cursors and failure/cancellation without partial success.
No schema, Bridge, pricing or ledger-priority changes are required. A single
large run can still take seconds; this removes the account-wide deadline trap,
not the cost of reading that run's complete accounting history.

## Non-goals

No mobile redesign, billing-rule changes, Bridge upgrade, additional database
service, connection-pool redesign, or loss of historical plans and usage.
Conversation history stays available through explicit pagination; 1000 is a
per-request ceiling, not a permanent history cutoff.

## Data and protocol impact

API shapes, authorization and conversation identifiers remain compatible.
Additional SQLite indexes are described in
[ADR 027](../adr/027-sparse-orchestration-read-indexes.md). Cancellation or a
deadline returns an error rather than a successful partial aggregate.

`internal/hub/task_graph.go:handleOrchestrationProgress` uses `internal/store/orchestration_progress.go:ListOrchestrationProgressEvents`.
It retains every plan marker and the review boundaries inside the same recent
event window used before this optimization; unrelated output is omitted. `internal/store/store.go:ListOrchestrationPlanEvents` also
serves next-round scheduling, preserving plans from the beginning of a run.
`internal/store/admin_analytics.go:AdminUsageSnapshot` keeps the administrator
projection content-free and the existing Token accounting rules unchanged.

## Implementation steps

1. Index exact plan and legacy-usage predicates and verify query plans.
2. Read only progress-relevant events while retaining complete plan history.
3. Bound administrator aggregation and propagate interrupted reads as failures.
4. Prioritize initial conversation loading, cancel old selections and prevent
   overlapping progress/poll requests in the orchestration workspace.
5. Reuse statistics request cancellation and deadlines in administrator pages.
6. Build standard and leading frontends independently and validate both.

## Exit gates

- Long-history equivalence and sparse-index plan tests pass.
- Permissions, full-history totals and timezone bucketing remain covered.
- Browser tests cover delayed responses, rapid switching and retry.
- Frontend tests/build, Go tests/build and document lint pass.
- Report measured query performance separately from deployment status.

## Reviewer Q&A

**Why not increase the SQLite pool?**

The observed bottleneck is reading unrelated large event bodies. Removing that
work preserves the existing concurrency contract and benefits both websites.

**Can progress or totals omit older history to load faster?**

No. The sparse projection preserves all relevant plan and usage events; output
volume must not determine whether an earlier plan or cost is visible.

**Does either website require a Bridge update?**

No. This change belongs to the Hub database/query and browser request paths.

## Initial history and manual pagination

The workspace fetches the latest 100 events concurrently with run details.
Scrolling to the top does not fetch older history; the explicit load-more
button reads 300 earlier events and preserves the scroll anchor. These defaults
keep the first read small; event counts are not counts of complete messages.
For a boundary turn with a complete final answer, rendering uses that answer
without duplicating streamed fragments. An unfinished boundary turn remains
a clearly identified recent-history fragment. Run selection
and request cancellation still govern response ownership. Live reconnection
catch-up uses sequential response cursors, independently of newer WebSocket
event sequence numbers, while plan and usage projections retain full history.

The events API defaults to 200 and caps each page at 1000 source events.
`hasMoreBefore` describes older history; `hasMoreAfter` describes further
incremental pages. The extra lookahead row is never returned as page content.

Administrator content previews also use bounded pages (default 200, maximum
1000). Orchestration pagination uses `beforeSeq` and returns `nextBeforeSeq`;
chat uses `beforeCreatedAt` plus `beforeId`, returning `nextBeforeCreatedAt`
and `nextBeforeId`. Both return `hasMoreBefore`. Cursors refer to source rows,
even when a page contains no displayable content. Safe orchestration content
is grouped within each source page; a turn spanning pages appears in parts.
The preview never aggregates the entire transcript before applying its limit.


## Authentication recovery after maintenance

Authentication reads share the Hub's single SQLite connection with statistics.
A temporary database error is not evidence that a valid session has expired.
`internal/hub/server.go:writeAuthUserReadError` maps a missing account to 401,
a context deadline to 504, and other account-read failures to 503; neither
`handleMe` nor `withAdmin` clears the session cookie for these failures.
`internal/hub/auth_availability_test.go:TestAuthUserReadFailuresDoNotInvalidateSession`
covers missing accounts, cancellation, deadlines, a closed database, and recovery
with the same account. The browser must preserve the original route and pending
work while retrying GET session verification; it must not replay submissions.
