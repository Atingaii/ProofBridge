# Desktop audit fixes

## Goals

- Preserve unsent orchestration text independently for each account and run,
  including refresh and switching while a send is pending.
- Make usage requests finish or show a recoverable error, and prevent stale
  responses from overwriting a newly selected time range or run.
- Distinguish an expired login from a temporarily unavailable authentication
  service, with an explicit retry path.
- Find older orchestration runs with server-side endpoint/status/search filters,
  pagination, rename, archive and restore.
- Bring help and release information into line with the current interface and
  make its language selector truthful.

## Non-goals

- Mobile navigation/layout changes, Android, notifications, budgets or editing
  accepted model input are outside this fix set.
- Do not change native CLI execution, pricing rules, run continuity or Bridge
  installation. No production model tasks or automatic deployment are required.
- Existing unrelated working-tree changes must be retained.

## Data and protocol impact

### History

`GET /api/orchestrations` keeps the existing `runs` array and adds an optional
`nextCursor`. Query parameters: `agentId`, `q` (title or initial prompt), `status`
(one existing status), `archived=false|true|all` (default false), `limit` (default
50, maximum 200), and an opaque `cursor`. Filter in the Store before limiting;
use descending `(created_at, id)` keyset pagination so activity and renaming do
not move items between pages. Invalid cursor/filter values return 400. Search
is parameterized and treats LIKE wildcard characters literally. All paths
enforce authenticated ownership, including cursor queries.

`PATCH /api/orchestrations/{runID}` accepts `{title?: string, archived?: boolean}`
and returns `{run: OrchestrationRun}`. Titles are trimmed, nonempty and at most
200 Unicode characters. Archiving is allowed only for terminal runs; an active
run returns 409. Restoring retains the same run ID and context. Concurrent
continuation/archive operations must not hide active work: resuming a run makes
it visible again. Missing/foreign runs use the existing not-found semantics.

`OrchestrationRun.archivedAt` is an optional Unix millisecond value. The additive
column, default and indexes are described in
[ADR 026](../adr/026-orchestration-history-metadata.md). Archived runs remain in
usage totals and are never deleted by the archive action. No WebSocket frame
or Bridge change is needed.

### Drafts and request recovery

Browser draft keys include user ID and run ID; new-run drafts use the selected
endpoint ID. Restore synchronously on selection and refresh. Successful sends
clear only the submitted text for the submitted target; text typed later and
another target's draft survive. Storage failure must not disable editing or
sending and should have an unobtrusive persistence warning. Store only composer
text, not attachment payloads, secrets or native question answers. Logout clears
only that account's draft namespace.

The browser API helper exposes the HTTP status while retaining Error behavior.
Only `/api/me` 401 enters the login flow. Transport/server failures and bounded
timeouts show a retry view and preserve the current route. Latest-request
identity and cancellation protect statistics rendering and unmounts; different
range/run data is not shown under the new selection. Shared UI wording remains
bilingual.

### Statistics and help

Investigate the actual usage read path in
`internal/hub/orchestration.go:handleUsageOverview` and
`internal/store/orchestration_usage_timeline.go:ListOrchestrationUsageTimeline`. Keep native ledger
precedence, legacy fallback, participant attribution, daily timestamp filtering
and the single SQLite connection invariant. Optimize measured query overhead
and bound endpoint work with request context; do not mask storage errors as
valid partial totals. Do not add a cache unless the measured query fix still
needs one, since stale financial/usage display adds an invalidation obligation.

Help describes dynamic execution, same-run follow-ups and native question
support with existing capability gates. Keep the historical release notes and
illustrations, while separating documented releases, frontend build information
and live Hub health version. Do not infer a Bridge version or invent a release.
EN must render meaningful English content, or clearly state that a particular
historical entry remains in its original language. Current-site links use the
current origin.

## Validation and error matrix

| Trigger | Required result |
| --- | --- |
| Bad history cursor, status, archive filter or limit | 400 `BAD_FILTER`; never a silently truncated or cross-account result |
| Empty/overlong title or malformed metadata body | 400 `BAD_REQUEST`; leave metadata unchanged |
| Metadata update for a missing or foreign run | 404 `NOT_FOUND` |
| Archive a queued, running or canceling run | 409 `RUN_ACTIVE` |
| Archive a terminal run, then continue it | Same ID/context, archived metadata cleared with the active status claim |
| Auth bootstrap returns 401 | Show the login flow |
| Auth bootstrap transport/5xx/timeout failure | Preserve the route and show retry |
| Statistics request times out or fails | Show recoverable error; never publish a partial total as success |
| Statistics response belongs to a replaced request | Ignore it, including its error/finally effects |
| Browser storage is blocked or full | Keep editing/sending in memory and show persistence warning |

## Good, base and bad cases

- Good: load a second history page, select an old task, rename it and archive it;
  restore it from archived history with the same ID and unchanged usage.
- Base: a single task's unsent draft survives a page refresh, and normal requests
  render the same results as before.
- Bad: send A, switch to B and type, then receive A's response. B must retain its
  text. A's later edit must also survive the earlier success. The browser tests
  must control response timing to exercise this case.

Wrong: fetch the latest 200 account records and filter by endpoint in React.
Correct: bind authenticated ownership and endpoint filters in SQL before
applying the stable cursor and limit.

Wrong: set `loading=false` in every request's unconditional `finally` block.
Correct: only the currently active request may change data, loading or error.

## Implementation steps

1. Capture the pre-existing worktree and use disjoint ownership for data,
   workspace and help changes.
2. Implement Store migration/history queries and API ownership/validation tests;
   diagnose and fix usage query costs with regression coverage.
3. Add history controls and independent persisted drafts; add recoverable auth
   bootstrap and stale-response protection for statistics.
4. Update help, release metadata and language behavior without mobile redesign.
5. Rebuild embedded assets, run Go/frontend checks and browser fixture regressions,
   then perform the document sweep and an independent review.

## Exit gates

- A/B/new-run drafts survive switches and refresh; delayed A success cannot erase
  B or newly typed A text. Failed sends retain drafts; storage failure is safe.
- A slow 30-day response cannot replace a completed 7-day response. Timeout,
  failure, retry and navigation away all terminate the loading state correctly.
- `/api/me` 503/network failures show retry, recovery loads the requested route,
  and 401 still shows login.
- With browser storage blocked, usage and statistics routes still render.
  Missing optional run memory falls back to the overview; an explicit `?run=`
  remains usable.
- History tests exceed 200 runs and cover ownership, filters, cursor validation,
  repeated pages, rename, archive/restore and active-run conflicts.
- Usage tests retain ledger/fallback arithmetic and account/time-range isolation;
  query diagnostics or representative measurements demonstrate the optimization.
- Desktop browser checks validate draft/history/auth/statistics flows, and the
  existing native-question/live-input fixture suites still pass.
- `npm test`, `npm run build`, `go test ./...`, a host Go build,
  `make doc-lint` and a changed-file whitespace check pass.

## Reviewer Q&A

### Why store drafts in the browser?

These are unsent local edits. Per-account/target storage fixes refresh loss and
cross-run contamination without adding Hub writes or changing accepted input.

### Why creation-time pagination?

Creation time and ID are stable. Updated-time ordering can skip or duplicate a
run when it emits events between page requests. Explicit refresh includes newly
created runs; ordinary activity does not reorder already loaded history.

### Does archiving affect context or accounting?

No. It is reversible list metadata. Prompts still target the same run, and usage
aggregation includes archived records. A continued run becomes visible again.

### Why not just add a loading timeout?

A timeout provides recovery but cannot make the statistics useful. The Store
query must also be investigated and optimized, while preserving ledger semantics.

### What does the version display prove?

Only its labeled source: the frontend build, the Hub health response, or the
documented release entry. A static page cannot assert the private Bridge version.

### Authentication recovery after Hub maintenance

`frontend/src/app/App.tsx:App` retains the authentication error card while a
transient `/api/me` failure is retried. Each request has a 12-second deadline;
only one request is active. Retry delays are 2, 5, 10, then at most 30 seconds,
measured after the previous failure. Automatic attempts require a visible,
on-offline page. Online/visibility recovery can bring the next attempt forward,
with at least two seconds between automatic starts; hidden/offline tabs do not
start new retries. The manual retry remains available. A successful response
restores the original URL; 401 stops retries and displays login. This flow does
not change cookies, submit model tasks, or navigate to a fresh session.

Exit gate: the local authentication recovery browser fixture checks transient
503 and timeout recovery, stable error feedback during retries, no overlapping
requests, hidden/offline suspension, manual retry, and no retries after 401.
