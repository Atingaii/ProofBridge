# ADR-016: Bridge-Managed TBCI Runtime

## Status

Accepted

## Context

ADR-015 made TBCI a selectable formal-proof verification authority, but its
first implementation required every Bridge operator to install an external
`tbci` executable and enable it in configuration. Browser users normally run
their own Bridge, so a TBCI selection could be persisted correctly and still
arrive at an endpoint that had no verifier. Discovering that only after a run
started wasted a reviewer round and left the result unresolved.

ProofBridge distributes the Bridge as one downloaded Go binary. Requiring a
second manual installation contradicts that delivery model and makes endpoint
capability impossible for the Hub to validate before dispatch.

## Decision

Supported Bridge builds carry a pinned, platform-specific TBCI executable as an
embedded artifact. At Bridge startup, before WebSocket registration, Bridge:

1. honors an explicit administrative disable;
2. uses an explicitly configured external executable when supplied;
3. otherwise materializes the embedded artifact below the user's private
   `~/.codex-bridge/tools/tbci/<sha256>/` directory with an atomic rename;
4. verifies the installed bytes against the build-time SHA-256 digest;
5. performs one bounded executable probe; and
6. reports a structured TBCI capability in `RegisterPayload.capabilities`.

The managed artifact is immutable by digest and is reused on later starts.
Preparation uses a process-wide lock, performs no network access, does not build
source, and does not download prover images or toolchains. The first supported
artifact is Linux/amd64. A build without an artifact reports `unsupported`
rather than pretending that TBCI can run.

The source revision, local compatibility patch, build command, artifact digest,
and three-adapter regression command are recorded in
`internal/bridge/tbci_assets/NOTICE.md`. The compatibility patch adds bounded
Rocq project discovery without changing or removing the existing Lean and
Isabelle adapters.

The Hub rejects creation or continuation in `tbci` mode unless the selected,
currently connected Agent advertises `available=true` and `state=ready`. The
browser disables the TBCI selector and displays the Bridge-provided reason when
that capability is not ready. Older Bridges therefore fail before run creation,
not inside a reviewer task.

`bridge.tbci_enabled` remains an emergency/administrative opt-out, but its
default becomes `true`. An empty `bridge.tbci_path` selects the managed artifact;
a non-empty value is an external override and is still probed and reported.

## Consequences

- Browser users do not separately install TBCI on a supported Bridge platform.
- Updating the ProofBridge binary updates the pinned managed artifact on the
  next Bridge restart without mutating an older digest directory.
- Bridge registration grows by one backward-compatible capability object.
- The main binary grows by the compressed/build representation of the TBCI
  artifact; startup writes at most one small static executable per digest.
- TBCI availability does not imply that every prover adapter is applicable.
  Contract-template and prover/toolchain checks remain task-time, fail-closed
  verification inputs.
- Unsupported operating systems or architectures remain able to use chat,
  orchestration, and Agent Verifiers.
