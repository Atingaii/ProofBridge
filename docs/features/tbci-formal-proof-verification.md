# Selectable Formal-Proof Verification Mode

## Goals

- Keep the existing two-Agent Verifier completion policy as the default for
  durable `formal-proof` reviewer tasks.
- Add a browser control beside the formal-proof profile so the user can choose
  either `agent` or `tbci` verification before starting a run.
- Persist the selected mode on the run and preserve it for follow-up prompts,
  durable graph rounds, reloads, and public run metadata.
- In `tbci` mode, freeze the contract and initial workspace before the Worker
  starts, then attest and verify the final workspace after the Worker succeeds.
- Generate a conservative contract from the exact user task and prepared
  repository when no explicit template exists.
- Bind uploads, durable graph rounds, retries, and same-run follow-ups to the
  correct per-submission initial and final repository states.
- Finish early only when the selected verifier mode passes. Never silently
  switch verifier modes after a failure or unavailable result.
- Bound and serialize TBCI phases so the isolated experiment machine remains
  responsive.
- Make TBCI self-contained on supported Bridge builds and reject TBCI runs
  before dispatch when the selected Agent cannot provide it.

## Non-goals

- Enabling TBCI for the default, non-formal-proof profile.
- Guessing among multiple proof assistants, target declarations, modules, or
  conflicting explicit templates. Ambiguity is a visible fail-closed result.
- Running TBCI and both Agent Verifiers for the same reviewer decision.
- Changing Worker prompts, worker pairs, native session continuity, or the
  configured round topology.
- Modifying the user workspace or retaining private verification snapshots.

## Data and protocol impact

- Add `verification_mode` to `orchestration_runs`, defaulting to `agent` for
  existing and newly omitted values. See
  [ADR-015](../adr/015-selectable-orchestration-verification-mode.md).
- Add optional `verificationMode` to orchestration create/continue requests,
  `internal/protocol.OrchestrationStartPayload`, and
  `internal/protocol.RunStartData`. The only normalized values are `agent` and
  `tbci`; omitted values normalize to `agent`.
- The Hub rejects `tbci` unless `profile=formal-proof`. A follow-up request that
  omits the field reuses the run's persisted value.
- Bridge continues to emit the existing `verifier.verdict` event shape. Agent
  mode retains the two Agent Verifier records; TBCI mode emits one `tbci`
  verifier record. TBCI lifecycle states use the existing `bridge.note` event
  with category `tbci-verification`.
- Public shares may expose the non-secret mode name but never TBCI runtime paths,
  templates, attestations, snapshots, or worker credentials.
- `internal/protocol.BridgeCapabilities` exposes a non-secret `tbci` object with
  availability, managed/external source, state, platform, artifact digest, and
  a bounded failure reason. It never exposes the installed filesystem path.

For an applicable TBCI task, Bridge creates a private runtime below its runtime
directory, keyed by `(runID, promptSeq)`. It contains the exact user task,
target-discovery record, initial and final snapshots, generated or explicit
template, frozen contract, attestation, and verifier-private handoff data. It
survives individual durable graph nodes and is not stored on the Hub.

`taskPrompt` is the immutable user submission. `Prompt` may additionally carry
a Hub-generated durable-node instruction. Bridge uses only `taskPrompt` for
contract discovery and uses `(runID, promptSeq)` rather than `Resume` to apply
uploads and append the proof ledger exactly once.

The repository boundary follows
[ADR-017](../adr/017-tbci-task-input-and-workspace-baseline.md): new submissions
materialize uploads under the selected canonical project, resolve one contained
proof-project root for an automatically generated contract, and freeze it. All
graph rounds compare against that same baseline; a same-run follow-up freezes a
new baseline from the previous final project plus its new uploads. Initial and
final roots must be the same canonical proof directory. Uploaded reference
documents outside that proof root remain task inputs but cannot become proof
evidence.

## Implementation steps

1. Render an `Agent Verifier` / `TBCI` segmented control below the
   `default` / `formal-proof` profile control. Show it only for formal-proof;
   switching to the default profile resets the draft selection to `agent`.
2. Persist and return `verificationMode` with each orchestration run. Preserve
   it through `POST /api/orchestrations/{runID}/prompts`, durable graph
   dispatch, and run-start timeline data.
3. In `agent` mode, run the current two independent Agent Verifiers after the
   intermediate-round durable reviewer. Wait for both and pass when at least one passes.
4. In `tbci` mode, preserve `taskPrompt`; apply this submission's uploads and
   ledger entry exactly once; select one explicit contract at
   `.tbci/task-template.json`, `tbci-task-template.json`, or
   `task-template.json`, or generate one from a uniquely identified target;
   snapshot the initial workspace and run `tbci contract freeze` before the
   first durable node CLI.
5. After a successful Worker, snapshot the final workspace and run
   `tbci attest`, then `tbci verify`. `Trusted` passes and ends with
   `verified-early`; `Untrusted`, `Inconclusive`, timeout, invalid contract,
   missing executable, or command failure produces Continue. Intermediate
   rounds advance normally. A normally completed last configured reviewer
   skips this extra attestation and ends successfully with `max-turns` (budget
   completion, not a `Trusted` verdict); see
   [configured-round-completion-and-usage.md](configured-round-completion-and-usage.md).
6. Never call Agent Verifiers from TBCI mode and never call TBCI from Agent
   mode. Timeline labels must make the selected authority explicit.
7. Serialize each bounded TBCI command phase, cap snapshots at 512 MiB and
   100,000 files, and retain the configured timeout.
8. Before Bridge registration, reuse or atomically materialize the pinned
   embedded TBCI artifact, verify its SHA-256 digest, and run one bounded probe.
   Do not download, compile, or prepare prover images during Bridge startup.
9. Reject create/continue requests at the Hub when `verificationMode=tbci` and
   the selected Agent does not advertise an available TBCI capability. Disable
   the browser selector and show the reported reason in that state.
10. Persist the private TBCI session across node attempts and graph rounds for
    one `(runID, promptSeq)`. Reject repository-root or task-prompt drift; a new
    `promptSeq` in the same run creates a new baseline after applying only that
    submission's uploads.

## Exit gates

- Store migration and CRUD tests prove old rows default to `agent` and new
  `tbci` selections round-trip.
- Hub tests cover normalization, formal-proof validation, create/continue
  persistence, run-start propagation, and durable graph round propagation.
- Bridge tests prove Agent mode retains two verifier calls, TBCI mode invokes no
  Agent Verifier, and Trusted/non-Trusted/unavailable TBCI results have the
  expected early/continue behavior.
- Bridge and Hub tests prove the initial snapshot precedes the first graph CLI,
  later nodes/rounds reuse it, follow-up uploads are applied once despite node
  `Resume=false`, and initial/final repository-root drift is rejected.
- Contract-discovery tests cover explicit precedence, unique Rocq/Lean/Isabelle
  targets, uploaded proof sources, missing targets, and ambiguity.
- Managed-artifact regressions cover a namespaced `_CoqProject` with a local
  dependency, while the Lean and Isabelle adapter suites and Bridge generation
  paths remain enabled; the Rocq extension may not narrow either backend.
- Frontend checks cover the conditional selector, default selection, request
  field, restored run selection, and visible TBCI lifecycle/verdict labels.
- Managed-runtime tests cover atomic installation, digest repair/reuse,
  administrative disable, unsupported platforms, capability reporting, and Hub
  rejection before run creation.
- Low-parallelism frontend build, focused Go tests, full Go tests, static Go
  build, and documentation lint pass.
- The isolated experiment station serves the rebuilt frontend; its Hub and
  Bridge remain healthy without touching the main site or Android archive.

## Reviewer Q&A

**What happens to existing runs and ordinary users?**

They normalize to `agent`, so the current two-Agent Verifier behavior is
unchanged. TBCI is opt-in and visible only under formal-proof.

**Can TBCI silently fall back when it is missing or inconclusive?**

No. The selected mode is the decision authority. TBCI failure advances a
remaining round or ends unresolved on the last round, with a visible reason.

**Can a follow-up switch modes?**

The UI restores the persisted run mode. An explicit follow-up value may update
the setting for that follow-up and subsequent prompts; omission preserves it.
The run id and native continuity remain unchanged.

**Does TBCI judge recent Worker replies?**

No. It uses the frozen contract and private before/after workspace snapshots.
Worker prose and recent command output are not verdict inputs.

**Which repositories does TBCI compare?**

The selected canonical project is the containment boundary. For an automatic
contract, Bridge chooses the unique target and resolves its nearest contained
Rocq, Lean, or Isabelle project root; both snapshots use that exact root. The
initial state is taken after the current user submission's uploads are
materialized and before its first graph CLI. The final state is that same root
at reviewer time. Graph retries and rounds reuse the baseline; a follow-up in
the same run starts a new baseline from the previous final state plus new
uploads.

**What if the user did not upload a contract template?**

Bridge generates one when the exact user task and prepared repository identify
one supported prover and one target declaration. If that selection is
ambiguous, TBCI fails closed and explains the ambiguity; it does not call an
Agent Verifier.

**Can concurrent TBCI runs overload the machine?**

TBCI phases are one-at-a-time, bounded by timeout and snapshot limits. Workers
do not hold the TBCI semaphore while they run.

**Does a browser user install TBCI separately?**

No on a supported Bridge build. The downloaded `codex-bridge` binary carries a
pinned artifact and installs it into private Bridge state on first start. An
administrator may still disable it or override it with an external executable.

**Does managed TBCI also install Rocq, Lean, or Isabelle?**

No. It makes the TBCI verifier executable available. A selected contract still
has to be applicable to the workspace and its pinned prover/toolchain; missing
task-specific evidence or tools remains an explicit non-Trusted result.

**Did automatic contract generation reduce TBCI to one prover?**

No. Discovery and generated contracts remain available for Rocq/Coq, Lean 4,
and Isabelle. The managed artifact adds bounded `_CoqProject` support to Rocq;
it does not remove or weaken the existing Lean and Isabelle adapters.
