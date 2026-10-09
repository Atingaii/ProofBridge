# Participant usage and historical model attribution

> The current presentation is specified in “Round/model summary” below. Earlier
> participant chart requirements are historical; precise participant accounting
> remains available in the API.

## Round/model summary (current)

Goals: show a stacked bar chart, a model trend line chart and an exact table (round, actual model, input, output,
cache, total tokens and cost). Show input/output/cache in the run summary too,
alongside full runtime, round count, tokens and cost. Merge A, B and verifier entries using the same model within each
round, for existing completed runs as well as new runs. Different models remain
separate stacked chart segments and table rows. Keep task scope and usage sync.

Non-goals: no runner, pricing, API, persistence or round-number changes. Do not
infer an unknown model from other participants or later rounds. Keep the
underlying participant ledger and total billing unchanged.

Implementation: aggregate normalized `round.byWorker` in the browser by model;
sum input, output, cache read and cache write once (reasoning is already included
in output). Preserve unresolved usage as its own model group. Legacy rounds
without participant detail show their recorded total as unresolved; any positive
unattributed remainder is preserved. Replace participant charts/cards with the
summary, without changing the all-runs overview. The two charts share the same
round/model totals, colors and exact-value tooltips. Use prominent icon-and-text buttons to switch between bar and line views,
with a filled active state and keyboard focus ring. Default to bars and show
only the selected chart at full width on every screen. Retain horizontal
scrolling with at least 48 px per round for long runs. Lines connect recorded round values
with straight segments; missing model/round entries are gaps, and single-round
models remain visible as dots. Keep the seven-column table unchanged.

Add a model-total summary below the round detail for the selected run/task scope.
Fold the exact same round/model rows across rounds, keeping unresolved models
separate and summing original unrounded quotes. Show each model's total tokens
and USD cost prominently, with input/output/cache breakdowns and horizontal
comparison bars (tokens and cost use separate scales). Partial costs retain `*`;
fully unknown costs show a dash, never a free-usage bar. No API or storage changes.
Explain beside the round chart that a round covers a collaboration cycle and
may contain different actual models; CLI participant names do not fix a model.

Exit gates: equal-model merging across all three CLI combinations, multiple
models, missing identity, legacy totals, task filtering, unchanged historical
totals, desktop/mobile chart switching and long-run scrolling, exact table checks,
frontend build, serial Go checks,
embedded delivery and doc lint.
Model totals must reconcile with round details for mixed models, identical
models across workers, missing prices, completed runs and task selection.

Reviewer Q&A: old runs benefit on read, with no replay or database migration.
Grouping changes only presentation, not billing or model provenance. Unknown
historical models still require native evidence to recover their identity.

Recovery lifecycle: [ADR 023](../adr/023-retryable-native-model-recovery.md).
Prior session syncs do not mean every planning call has an identified model.
Use explicit Bridge recovery capability and persistent retry markers to resume
historical discovery automatically. Show upgrade/offline/searching/unresolved
states accurately. Cache is read plus write; per-model costs sum original
quotes, with a partial marker if any contributing cost is unknown. Keep the
round/model chart and horizontal table scrolling on narrow screens.

## Goals

Show each participant and actual model separately in per-round and cumulative
charts, including completed conversations. Preserve existing CLI totals, task
boundaries, native token normalization and provider-reported billing.

## Non-goals

Do not replay conversations, estimate missing tokens, infer a worker from turn
parity, or replace immutable historical bindings with current preset settings.

## Data / protocol impact

Add `byWorker` to run/task/round statistics. Keys include CLI, worker slot and
model. Bridge snapshots include `workerSlot`; historical reads join matching
turn IDs to `turn.start` metadata. Model identity comes from native usage first,
then that turn's recorded model or immutable graph binding. Ambiguous records
remain explicitly unattributed. No SQLite migration or event rewriting.

Historical native-log sync normalizes legacy CLI aliases to CLI plus worker
slot, scans the native home for official-login bindings, and excludes copied
session IDs with conflicting owners. Structured slot maps from historical task
ends take precedence over copied legacy resume labels. Old identity-related scan failures are
retried at most every six hours on statistics reads; explicit sync remains
available. A unique native model inside the matching worker/turn interval can
restore missing snapshot model identity without changing its token counts.

## Implementation

### Independent verifier identity repair

Verifiers execute the two recorded worker assignments using each slot's runtime
configuration. Keep those assignments in usage events and read historical
`verifier.verdict` records to recover the exact agent-to-slot mapping. Display
`verifier:<slot>` as a separate chart series so verification costs never merge
with worker turns or the other verifier. Token counters and verdict decisions
are unchanged; there is no database migration or conversation replay.

Resolve missing verifier models from the recorded verdict, then the immutable
graph binding (task graph ID takes precedence over second-resolution timestamps).
For default-model historical calls, use the unique model observed
for the assigned worker in the same graph before verification, explicitly marked
as inherited worker configuration. Conflicting models or missing assignments
must not be guessed. Enrich before task filtering and preserve the projection
when aggregating it again.

For new Codex `exec --json` verifier calls, capture the native thread/model
metadata and, when needed, read bounded metadata from that exact session log in
the command's effective CODEX_HOME. Retain native usage on failed calls and put
the resolved model into both usage and verifier verdict records. Claude native
usage models also populate verdict identity.

A chart round represents the full collaboration cycle (worker calls, integration,
review and optional independent checks), not a single A/B message. Show this
explanation next to the charts. Test Codex/Codex, Claude/Claude and mixed
Claude/Codex combinations, preserving Codex inclusive-cache normalization,
Claude cache writes, per-model parts and provider-reported costs.

Exit gates: historical A/B verifier separation, explicit/native model priority,
graph boundaries and ambiguous defaults, unchanged token totals, idempotent
aggregation, Codex default-model and failed-call capture, compact store reads,
desktop/mobile charts, serial regression tests and embedded UI build.

Reviewer Q&A: inherited defaults are labelled as configuration attribution,
not claimed as model metadata reported by the old verifier invocation. New
native capture requires the updated Bridge; Hub read-time recovery also works
with existing Bridge versions and completed runs.

1. Enrich compact usage timelines before task/range filtering.
2. Aggregate additive worker series alongside unchanged `byCli` totals.
3. Render grouped per-round bars, cumulative lines and exact detail values.
4. Integrate the automatically refreshed catalog from ADR 021 for prices and
   model capabilities; provider billing remains authoritative.

## Exit gates

Historical snapshots and native ledgers distinguish equal-model A/B; model
changes retain separate series; worker totals reconcile to run totals; task
filtering and unknown identities are tested. Verify desktop/mobile charts,
frontend build, serial Go regression tests and doc lint at low concurrency.

## Reviewer Q&A

- Why additive fields? Existing API consumers retain CLI/model aggregation.
- Why read-time recovery? Completed runs benefit immediately without replay.
- Missing historical evidence? Show unknown identity/model, never guess.
- Current versus historical prices? Catalog values are current reference
  estimates; recorded provider costs are retained, not repriced.

References: `internal/hub/orchestration.go:handleOrchestrationStats`,
`internal/hub/orchestration_worker_usage.go:enrichOrchestrationUsageTimeline`,
`frontend/src/app/pages/OrchestrationStatsPage.tsx:RunStatsPage`.

The model-library editor retains a successful probe when the already selected
model is clicked again, and explains the required preset name. Missing effort
options do not prevent saving a tested preset.

## Canceled planning calls and round comparison

Protocol and runner decision: [ADR 022](../adr/022-canceled-planning-native-identity.md).

Persist Codex native identity as a `bridge.note` with category `native-identity`
as soon as a thread is prepared/started, before task completion. Compact usage
reads retain these notes; exact-turn joins recover model/session identity even
when cancellation omits `run.end`. Normal exec calls also resolve missing models
from the exact native log, including failed/canceled calls. No synthetic tokens.

Add optional `recovery` hints to the existing usage-sync request, generated only
for missing-model, native Codex planning turns with immutable official-login
bindings. A hint contains recorded cwd, start/end, worker slot and native token
fingerprint. The private Bridge examines bounded date directories and file sizes,
requires matching session metadata and exact token totals, and accepts exactly
one candidate. Return only normal usage rows, never log contents. Ambiguity,
missing files or scan limits leave the original accounting unchanged. Old
Bridges ignore the optional hints; recovery requires updating the private Bridge
and syncing usage, including for completed historical runs. No replay is needed.

Exclude empty estimated placeholders from call counts; native zero-token reports
remain intact. Explain next to charts that a collaboration round groups work,
feedback and checks for the same objective. Grouping aids contribution/cost
comparison and complete-cycle round budgets; numbering alone neither saves
tokens nor improves reasoning or implies concurrent execution. A-then-B numbered
as two turns is a useful chronological view of the same possible workflow.

Exit gates: canceled exec capture, early identity persistence, historical exact
turn joins, recovery fingerprint/cwd/time/ambiguity/isolation/scan-limit cases,
unchanged three-pair totals, desktop/mobile chart explanation, serial Go tests,
frontend build and doc lint. Reviewer Q&A: a missing historical model cannot be
reconstructed on the Hub when both native identity and explicit model are absent;
the new recovery path consults evidence on the private machine, never a later
worker's model or today's configuration.
