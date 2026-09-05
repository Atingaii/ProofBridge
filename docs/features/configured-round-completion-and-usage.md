# Configured-round completion and native usage

## Goals

- On the experimental site, complete a normally executed final configured
  reviewer round successfully without consulting the initial plan or requiring
  another verifier verdict. Preserve actual worker errors and cancellation.
- Count every observed Codex response in a turn, without counting repeated
  cumulative notifications or previous turns again.
- Present disjoint input/cache/output totals consistently for both historical
  snapshots and the native ledger.

## Non-goals

- Do not claim a round-limit completion is an independently verified proof.
- Do not rewrite historical run statuses, alter the plan checklist, create a
  new run on follow-up, or deploy to the main site.
- Do not estimate missing usage or change model prices.

## Data and protocol impact

No schema or protocol fields change. Existing `max-turns` terminal metadata
denotes budget completion, while `verified-early` still requires verification.
Native snapshots continue to store provider input counts (including Codex
cache reads); the Hub converts them to disjoint display categories. Existing
historical snapshots receive the same correction when read.

## Implementation steps

1. In `internal/bridge/orchestration.go:run`, short-circuit final reviewer
   verification after a successful worker turn and remove the exhausted-round
   error. Keep early verification and normal Hub round advancement.
2. In `internal/bridge/appserver_runner.go:readEvents`, accumulate scoped token
   notifications across a turn and deduplicate cumulative updates. Keep raw
   response usage only as a fallback to the authoritative token stream.
3. In `internal/hub/orchestration.go:buildOrchestrationRunStatsWithLedger` and
   `internal/hub/orchestration.go:appendOverviewTrend`, normalize snapshots for
   display and count the observed snapshot records consistently.
4. Cover native usage aliases and duplicate Claude assistant ledger entries.
   Keep isolated-runtime snapshot fallback and existing pricing semantics.

## Exit gates

- Final reviewer completion succeeds with an unfinished initial plan and no
  verifier invocation; worker failure and cancellation still terminate correctly.
- Intermediate review retains early verification and same-run round progression.
- Multiple Codex responses, repeated notifications, resumed-thread totals,
  native cached-token aliases, and Claude message duplicates have regression tests.
- Run/task/overview totals agree; input + cache + output equals total.
- Run focused tests and the Go suite with `GOMAXPROCS=2`, `-p 1`, and
  `-parallel 1`; build serially and check machine load before deployment.
- Doc lint, experimental Hub health, and Bridge reconnection pass; push the
  reviewed commits to the experimental branch.

## Reviewer Q&A

**Does reaching the round limit prove the theorem?**

No. It marks the requested execution budget complete. Independent verifier
evidence, if obtained earlier, remains distinct and visible.

**What if a CLI fails during the last round?**

The existing error/cancellation paths return before the completion shortcut.

**Can historical missing tokens be recovered?**

Only where original native logs remain available. Historical cache overlap can
be corrected on read; deleted native logs cannot be reconstructed from text.

**Does follow-up preserve context?**

Yes. The existing run ID, prompt endpoint, and native session IDs are reused.
