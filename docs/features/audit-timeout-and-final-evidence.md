# Audit timeout and final evidence convergence

## Goals

A timed-out audit is incomplete infrastructure work, not a proof defect. Retry
only the independent audit with a doubled time allowance, from 10 minutes up to
24 hours. An exhausted audit stops with an explicit error; it must never dispatch
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
is local to the current successful reviewer turn: a resolved final handoff with
no outstanding work, plus native successful final-audit command output. A later
tool execution invalidates immediate reuse. Existing logs may be inspected by
independent verifiers; their provenance, current sources, full task scope and
successful exit must be confirmed before accepting them without a rerun.

## Implementation

1. Separate deadline failures and model-reported audit timeouts from concrete
   verification failures; retry the same read-only audit, preserving usage.
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
extra verifier invocations, and rejected prose/echo/failed/later-command evidence.
Run Go suite, documentation lint, and build. Deploy the 8088 shared Hub and affected
local Bridge only after checking active work; verify service health and versions.

## Reviewer Q&A

**Why not report continue on timeout?** It asks the scheduler to modify code even
though the audit has produced no evidence against it.

**Why not accept any log containing the marker?** A log must belong to the current
source and original task. Live independent reviewer command evidence provides a
bounded direct path; historical logs need independent provenance inspection.

**Why is a final handoff also required?** The marker certifies an audit command,
while the independent reviewer confirms coverage of the original task and reports
remaining obligations. A partial successful probe cannot close the whole task.
