# Audit timeout and final evidence convergence

## Goals

A timed-out audit is incomplete infrastructure work, not a proof defect. Retry
only the independent audit with a 24-hour allowance from its first attempt,
with one automatic audit-only retry at the same 24-hour cap. An exhausted audit stops with an explicit error; it must never dispatch
another source-editing round. Cancellation always wins.

A successful final audit performed by the current independent reviewer can finish
verification immediately. Do not repeat that audit in the workspace or launch
extra model verifiers after the authoritative result is available.

## Non-goals

No permission changes, historical result rewriting, paid model load tests, database
migration, or new HTTP/configuration fields. A marker in worker prose, an echo,
a failed command, a partial probe, or an old unbound log cannot prove completion.

## Data and protocol impact

Use existing verdict, bridge note, usage, run.error and run.end events. Keep retry
usage, and expose timeout attempts in verifier result metadata. An audit receipt
belongs to the current independent reviewer: successful final-audit output, or
a successful read of an independent final-audit log whose timestamp covers the
current sources. A fixed final handoff format is not required. Explicit remaining
work, risks and blocked handoffs still prevent direct closure. Later successful
read-only commands preserve the result; writes and failed commands invalidate it. Existing logs may be inspected by
independent verifiers; their provenance, current sources, full task scope and
successful exit must be confirmed before accepting them without a rerun.
Standalone log reuse recognizes a successful `cat` or native `Read` of a regular
workspace `.log` whose basename contains `final`, `audit`, and one of `review`,
`independent`, or `verifier`, such as `ReviewerIndependentFinalAudit.log`.
Conventional native `bash -lc` / `sh -c` wrappers are normalized before these
checks. The file must contain the pass marker, predate the read completion, and be at
least as new as every source file in the existing bounded source scan.

## Implementation

1. Separate deadline failures and model-reported audit timeouts from concrete
   verification failures; retry the same read-only audit, preserving usage. Start
   with 24 hours instead of repeatedly interrupting long audits at shorter limits.
2. Keep the host serialization gate while retrying; release it on cancellation.
3. Stop orchestration with run.error on timeout exhaustion, before round dispatch
   or stagnation accounting.
4. Recognize a successful final audit at the independent reviewer boundary and
   publish a pass immediately; keep ordinary agent verification for other cases, stopping at the first
   independent pass instead of launching a redundant second verifier.
5. Replace short proof-audit timeout examples with the 24-hour final-audit policy
   and tell verifiers to reuse validated independent logs.

## Exit gates

Deterministic CLI tests: timeout then success, capped repeated timeout with no new
worker round, cancellation, retry usage, successful reviewer final audit with zero
extra verifier invocations, relaxed handoff formats, safe read follow-ups, fresh independent log reads, and
rejected prose/echo/failed/source-write/stale-log evidence.
Run Go suite, documentation lint, and build. Deploy the 8088 shared Hub and affected
local Bridge only after checking active work; verify service health and versions.

## Reviewer Q&A

**Why not report continue on timeout?** It asks the scheduler to modify code even
though the audit has produced no evidence against it.

**Why not accept any log containing the marker?** A log must belong to the current
source and original task. Live independent reviewer command evidence provides a
bounded direct path; historical logs need independent provenance inspection.

**Does handoff formatting block a successful audit?** No. Native independent
final-audit evidence is sufficient when source freshness is established and no
explicit remaining work or risks contradict it. A partial probe cannot close
the whole task. Existing independent logs must be read successfully from the
workspace, contain the final pass marker, and be at least as new as the sources.
