# Orchestration usage overview

Participant charts, historical identity recovery and automatic catalog updates:
[orchestration-worker-usage.md](orchestration-worker-usage.md).

Follow-up fixes to intermediate completion, model attribution, pricing, and
ledger precedence: [early-completion-model-accounting.md](early-completion-model-accounting.md).

## Goals

- Present usage with four explicit measures: input, output, cache, and total tokens.
- Show a compact per-round trend for every orchestration run.
- Switch run usage between the whole conversation and each user task while
  keeping configured internal rounds inside their parent task.
- Provide a user-scoped overview grouped by machine, orchestration task, and CLI/model.
- Keep the all-conversation overview separate from a single run, with selectable
  time windows and token/cost trend dimensions.
- Reuse the existing Hub SQLite ledger and Bridge usage sync; add no service, port, or user command.

## Non-goals

- This is not an invoice or provider billing replacement.
- Do not change native CLI scanning, pricing catalog rules, orchestration continuity, or run lifecycle behavior.
- Do not expose internal proof notes, relay prompts, or transport details in the product view.

## Data and API impact

`GET /api/orchestrations/{runID}/stats` keeps all existing fields and adds
`cacheTokens`, `totalTokens`, `rounds`, and `tasks`. Each round contains input,
output, cache, total tokens, calls, and cost. Each task repeats the same stats
shape plus `taskNumber`, `promptSeq`, and `prompt`. Task boundaries come from
durable graph payloads: graph generations with the same `promptSeq` are internal
rounds, while a follow-up prompt starts the next task. The top-level fields
remain the exact all-run aggregate for compatibility. Participant series use
CLI + worker slot + model; independent checks use `verifier:<slot>` and
`modelSource` marks historical configuration attribution. A round can include
both participants and their independent checks. The same projection and cost
normalization apply to Codex/Codex, Claude/Claude and mixed pairs.

`GET /api/usage/overview` returns only the authenticated user's runs, grouped by
machine and task, with totals, CLI/model breakdowns, and a daily trend. The
optional `days` query accepts `7`, `30`, `90`, or `0` (all history). The optional
`timezoneOffset` query uses the browser's `Date.getTimezoneOffset()` convention
so day boundaries match the user's local calendar. Range filtering is applied
to persisted usage-event timestamps, not just run creation timestamps.

Both statistics endpoints serialize absent collection fields as empty arrays.
The desktop request recovery and query-performance contract is described in
[desktop audit fixes](desktop-audit-fixes.md). The browser keeps each response
bound to its selected run/range and offers timeout/error retries; archived runs
remain included in the user's aggregate totals.
The native local ledger is preferred only when its sync is complete and has
events. If an isolated worker has already cleaned up its private runtime, a
subsequent ledger scan may be unavailable; the Hub then retains the native
per-turn usage snapshots recorded during execution. Snapshot statistics use
non-cached Codex input, group by CLI and model, and count available snapshots;
see [configured-round-completion-and-usage.md](configured-round-completion-and-usage.md).
The browser additionally
treats malformed, missing, or non-finite historic usage values as zero/empty
display values so a partial ledger cannot prevent the usage page from
rendering.

## Implementation

1. Aggregate native ledger events in Hub, deriving round boundaries from persisted orchestration round-advance events.
2. Add a user-scoped overview query path that reuses the run stats builder and joins each run to its Agent metadata.
3. Add a responsive run chart and an independent overview page using the
   existing React, Tailwind, lucide, and Recharts dependencies. Compact chart
   axes use `K`, `M`, `B`, and `T`; tooltips retain exact values.
4. Let the overview switch between total, input, output, cache, and known-cost
   daily trends, and group its task list into explicit machine sections.
5. Keep legacy turn-snapshot fallback behavior and old API fields intact.
6. Return all task projections in the initial stats response so task switching
   is browser-local and adds no polling, worker, or Redis load.
7. Keep empty and incomplete historical records displayable; they may show zero
   usage but must not trip the application error boundary.
8. Keep `internal/store/orchestration_usage_timeline.go:ListOrchestrationUsageTimeline` on the sparse
   `idx_orchestration_events_usage` index so output/context events do not dominate
   usage reads. Its partial-index predicate must match the timeline query.
   Statistics and usage recovery read graph identity, timestamps and payloads via
   `internal/store/task_graph.go:ListTaskGraphMetadataByRun`; execution paths that need
   tasks and dependencies retain the full graph loader.
9. Bound Hub statistics work to 20 seconds and browser requests to 25 seconds.
   Return an error for interrupted aggregation instead of partial totals. Only
   the latest browser request may update data, errors or loading state.

## Exit gates

- Go tests cover total-token arithmetic, round aggregation, time-range filtering,
  task boundary aggregation, daily trends, and user-scoped overview behavior.
- `npm run build` refreshes `internal/web/static/`.
- `go test ./...`, `git diff --check`, and a Hub health check pass.

## Reviewer Q&A

### Why combine cache read and cache write?

The requested product metric is total cache consumption. The existing read/write fields remain available for detailed accounting, while `cacheTokens` is their sum.

### Are historical conversations included?

Yes. The overview scans the existing persisted runs and uses the native ledger when available, falling back to legacy turn snapshots for older runs.

### Does this require another Bridge update?

No for the overview and chart changes themselves. They only change the Hub API
and embedded UI and reuse the already-shipped local usage ledger. A Bridge only
needs updating if it predates local usage-ledger support.

### Why not query each task separately?

The Hub already loads the run ledger once. Projecting its persisted events into
task time windows during that request avoids extra browser round trips and keeps
task switching instant without increasing steady-state server load.

Legacy usage records without a parseable timestamp are assigned to task 1 so
the sum of task totals remains equal to the compatible all-run total.

For missing models in canceled planning calls, see the early native identity and
bounded historical-log recovery design in [participant usage](orchestration-worker-usage.md).
Usage sync supports already completed runs, but native discovery requires an
updated private Bridge. Empty estimated placeholders are not counted as calls.

Run statistics now show a full-width chart with prominent bar/line switch buttons and exact table with round, model, input, output, cache, total tokens and cost,
combining equal-model participants and verification within each round. Historical
runs use the same read-time presentation; detailed accounting and pricing remain
unchanged. The runtime/rounds/total/cost cards also include input, output and cache
totals. Model recovery state distinguishes an endpoint upgrade from pending or
unsuccessful discovery; see [ADR 023](../adr/023-retryable-native-model-recovery.md).
See [current display design](orchestration-worker-usage.md#roundmodel-summary-current)
and `frontend/src/app/components/RoundModelUsageCharts.tsx:RoundModelUsageCharts`.

The run/task statistics also show model totals across the selected rounds, with
Token/cost comparison bars and exact input/output/cache breakdowns. They fold
`frontend/src/app/lib/roundModelUsage.ts:modelUsageTotals` over the same displayed
round rows, retaining original call quotes and incomplete-cost markers. This
read-time presentation applies to completed runs without changing their ledger.

Long-history read performance and cancellable loading are specified in
[site loading performance](site-loading-performance.md), with sparse-index
migration and rollback in [ADR 027](../adr/027-sparse-orchestration-read-indexes.md).

## Progressive overview loading

The browser requests `GET /api/usage/overview?days=...&timezoneOffset=...&limit=1`
and follows the opaque `cursor` serially. Each successful response contains
one run's overview increment and `page: { hasMore, nextCursor?, scannedRuns }`;
`scannedRuns` counts source runs on that page, including runs whose displayed
items are empty. The cursor binds the account, range, timezone, and initial
cutoff. Requests without `limit` keep the legacy full-response contract.

`frontend/src/app/lib/usageOverviewPages.ts:mergeUsageOverviewPage` merges each
page once by request cursor and each nonempty contribution once by run ID.
It adds the existing accounting values, combines trend points by date with
cost-completeness AND, derives unique machines from accumulated items, and
ignores empty pages for cost completeness. It does not change billing rules.
The UI uses one-run pages so a repeated run cannot partially overlap a page's
aggregate trend. Unexpected multi-run pages fail visibly instead of guessing.

`frontend/src/app/lib/useUsageOverview.ts:useUsageOverview` gives every page its
own 25-second deadline, rather than limiting the whole chain. A successful page
is immediately visible with an explicit incomplete-statistics notice and
source-run progress. Only `hasMore=false` marks the totals complete. Empty
pages advance by source cursor. Failures keep accumulated data and retry the
same cursor without counting it twice. A range change or unmount cancels the
current request and suppresses late responses; refresh starts a new chain.

Exit gates: pure merge tests cover duplicate cursors/runs, shared machines,
empty pages, unknown costs and overlapping dates. Local browser fixtures cover
partial display before the next response, chains longer than 25 seconds,
empty-page continuation, same-cursor retry, cancellation on range/route changes,
and explicit final completion. No automatic usage sync or model task is sent.
