# ADR-017: TBCI Task Input and Workspace Baseline

## Status

Accepted

## Context

ADR-015 makes TBCI the sole completion authority when the user selects the
`tbci` verification mode. The first implementation nevertheless prepared TBCI
only inside the durable reviewer node and required exactly one user-authored
contract template. That has two incorrect consequences:

1. an ordinary browser prompt cannot use the Bridge-managed verifier without
   first modifying the repository; and
2. the so-called initial snapshot may already contain changes made by planner,
   worker, and integrator nodes.

Durable orchestration also rewrites `Prompt` with a node instruction and clears
`Resume` for each isolated node. Consequently, `Resume` cannot identify a user
task, and it cannot decide whether follow-up uploads should be materialized.
The verification boundary needs an identity independent of native CLI sessions
and graph attempts.

## Decision

### User-task identity

One TBCI decision is keyed by `(runID, promptSeq)`. `runID` preserves browser
conversation continuity; the Hub-assigned `promptSeq` identifies one exact user
submission within that run. Durable graph nodes, retries, and additional graph
rounds for the same submission reuse the same key.

`internal/protocol.OrchestrationStartPayload` carries a new optional
`taskPrompt` field containing the exact user submission. The Hub sets it before
adding durable node instructions, and every graph dispatch preserves it. A
legacy sender without `taskPrompt` falls back to `Prompt` only when the payload
does not contain a durable task graph.

### Repository boundary

The selected canonical project directory is the containment boundary. For an
automatically generated contract, Bridge resolves one proof-project root at or
below it: the nearest root marker for the uniquely selected target
(`_CoqProject`/`dune-project`, a Lake/Lean marker, or Isabelle `ROOT`), falling
back to the target source directory. That resolved root is both the initial and
final TBCI repository root. An explicit template continues to use the selected
project directory because its authorized paths are issuer-controlled. Bridge
records the canonical proof root with the task session and rejects a later node
whose selected directory no longer contains it. TBCI never compares two
different checkouts merely because a durable node cleared `RunCWD`.

The states are:

| Situation | Initial repository | Final repository |
| --- | --- | --- |
| New run, no uploads | Resolved proof root after Bridge bootstrap metadata is prepared | Same proof root when its reviewer finishes |
| New run with uploads | Resolve after this submission's uploads are safely materialized, then freeze that proof root | Same proof root when its reviewer finishes |
| Retry or later graph round for one submission | Reuse the already frozen `(runID, promptSeq)` snapshot | Current state of the same project |
| Follow-up in the same run | Previous submission's current final project, then apply the new submission's uploads | Same project after the follow-up reviewer |

Uploads are materialized before target selection. Files inside the resolved
proof root are bound into the initial snapshot; ordinary reference documents
outside that root remain Agent task inputs but are not TBCI proof evidence or
automatically authorized output paths. Safely extracted proof-source archives
may provide both the project marker and target source. Bridge-owned
`.codex-bridge` notes and prompt markers are excluded from TBCI snapshots and
may not become proof evidence.

Upload and follow-up preparation is idempotent by `(runID, promptSeq)`, not by
`Resume`. Bridge records an atomic private marker only after all attachments and
the follow-up ledger update succeed. Replayed graph nodes therefore neither
duplicate a regular upload under a new filename nor append the same request
again.

### Contract selection

An explicit valid template remains authoritative. Zero templates no longer
means not applicable: Bridge deterministically derives a conservative template
from `taskPrompt` and the prepared initial repository. Derivation must identify
one supported proof assistant (Rocq/Coq, Lean 4, or Isabelle), one uniquely located theorem/lemma target, its
source module, and authorized source path. Ambiguous assistants, targets,
modules, or multiple explicit templates fail closed with a visible TBCI reason.
The Bridge never asks an Agent Verifier to resolve ambiguity.

The generated contract and target-discovery record are written only to the
private TBCI runtime. They do not modify the user's checkout. An explicit
template and an automatically generated template receive the same TBCI freeze,
attest, and verify treatment.

### Timing and persistence

For TBCI mode, the first durable node prepares the formal-proof inputs and then
freezes the contract and initial snapshot before that node's CLI starts. The
private session is stored below the Bridge runtime directory, keyed by
`(runID, promptSeq)`, so later nodes and a Bridge reconnect can reload it.
Reviewer nodes only create the final snapshot and evaluate the already frozen
session.

Preparation failure is a task failure before implementation begins. Evaluation
that is not `Trusted` advances a remaining configured graph round or ends
unresolved on the last round. Neither path invokes Agent Verifiers.

## Consequences

- Browser users can select TBCI without installing TBCI or authoring a template.
- The exact user input, including follow-up identity and uploads, is separated
  from Hub-generated node instructions.
- A graph round cannot redefine the baseline after workers have changed files.
- A follow-up keeps the same orchestration run but intentionally receives a new
  TBCI contract/baseline derived from the prior final repository.
- Conservative automatic discovery may reject an ambiguous task rather than
  guessing the wrong theorem. The error must name what is ambiguous and how an
  explicit template can disambiguate it.
- Protocol, Hub dispatch, formal-proof bootstrap, TBCI session lifecycle,
  architecture, code map, feature documentation, and integration tests must
  change together.
