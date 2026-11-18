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
For projects without the Lean submission contract, standalone log reuse recognizes a successful `cat` or native `Read` of a regular
workspace `.log` whose basename contains `final`, `audit`, and one of `review`,
`independent`, or `verifier`, such as `ReviewerIndependentFinalAudit.log`.
Conventional native `bash -lc` / `sh -c` wrappers are normalized before these
checks. The file must contain the pass marker, predate the read completion, and be at
least as new as every source file in the existing bounded source scan.

## Lean submission evidence

For a Lake project whose task calls for `make final-audit`, the designated
independent reviewer owns the final decision. A worker's candidate still goes
to that reviewer. Bridge registers the task revision, workspace and frozen
evaluator inputs before editing begins under the service user’s local state directory. These records survive reconnection and a Bridge restart. It observes the reviewer's native tool
events and binds a completed submission audit to the digest of the source files,
build configuration, evaluator and frozen specification. Report and build output
can change without changing that identity.

After checking requirements outside the evaluator, the reviewer can use the
ordinary native shell invocation `cd <project> && env PROOFBRIDGE_FINAL_REVIEW=1 timeout
--kill-after=10s 24h make -j1 final-audit`. This declaration designates the
command as its final decision. A native zero exit and the submission pass line
immediately settle the reviewer turn. Bridge closes the native turn and
publishes the normal verified run outcome. User cancellation and newer inputs
retain their normal precedence. An ordinary independent final audit can also
supply evidence for the reviewer's final handoff when the native event identifies this workspace. The fixed directory prefix on the designated command also supports CLIs that omit the native working directory. A reviewer with an already
observed successful audit for the same task and source can use
`: PROOFBRIDGE_FINAL_REVIEW` to adopt it without executing the evaluator again.

The command observer accepts an audit through a terminal `pipefail` pipeline
or a checked capture of the evaluator's actual status. A plain `tee` exit, a
baseline audit, copied file, fabricated pass line or subsequent shell command
cannot provide the required exit status. Lean log reuse requires Bridge's record
of the independent execution, bound to the same scope and proof inputs.
Source changes, changes to the evaluator, or a conflicting reviewer handoff
invalidate the previous decision.

### An implementer's submission audit

A work-node `make final-audit` can supply the evaluator result later inspected by
the independent reviewer. Bridge stores this native observation separately per
worker slot, with the command, source digest and original evaluator identity.
Worker and integrator actions never grant an independent decision. Only the
assigned reviewer in a different slot may explicitly adopt such an observation
after checking the other original requirements. A resolved handoff alone may
reuse a previous independent decision, but cannot promote a candidate.
An independent failed evaluation takes precedence until an independent
evaluation passes on these inputs.

The worker receives the absolute project-bound evaluator command. On reviewer
dispatch, Bridge offers adoption only if it can resolve matching native evidence
right now. Otherwise the prompt specifies the direct designated audit. A stale
or missing adoption emits a Bridge note and has no verification effect. Native
Codex absolute paths and local file URIs bind the same workspace. Foreign file
authorities and ambiguous encodings are ineligible. A shell that enables
`pipefail`, saves the pipeline status, prints a localized status and exits with
that saved status carries the evaluator's exit reliably.

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
rejected prose/echo/failed/source-write/stale-log evidence. Replay the real masked pipeline, captured child status and logged `PIPESTATUS` forms. Exercise an unresponsive native reviewer after a successful submission, cancellation, backdated writes, source changes, frozen evaluator changes and restart reuse.
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
