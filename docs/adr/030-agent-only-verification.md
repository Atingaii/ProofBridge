# ADR-030: Agent-only orchestration verification

## Status

Accepted, 2026-11-06, at the user's explicit request to remove the optional bundled verifier and its repository assets.

## Decision

New orchestration requests support only `verificationMode=agent`; an omitted mode selects that existing default. An explicit unsupported mode fails with `BAD_VERIFICATION_MODE` rather than silently selecting another authority. Bridge validates incoming work as well as Hub HTTP/MCP entry points.

The existing independent Agent Verifier policy, actual checker evidence, formal-proof workspace guidance, task graph, native sessions, MCP tools, and mail delivery remain unchanged. The optional runtime, embedded artifact, configuration, capability, UI selector and dedicated documents/tests are removed from the current tree. Git history and user runtime files are not rewritten or deleted.

## Historical compatibility

Keep the stored `verification_mode` text column and read historical values without rewriting them. Historical runs, events, verdicts and statistics remain readable. Continuing a historical run with an unsupported authority requires an explicit supported choice; it never changes authority implicitly. Older Bridge registration JSON may contain extra fields, which remain safely ignored.

## Validation

- Agent-mode creation, continuation, task graphs and verifier quorum retain regression coverage.
- New HTTP/MCP requests and incoming Bridge payloads reject unsupported modes.
- Stored historical values remain readable and user data is preserved.
- Frontend exposes the supported authority and retains historical event visibility.
- Go tests/build, frontend checks/build and document link checks pass after integration.

## Consequences

The binary no longer distributes or installs an additional verifier. Model-driven verification still reports independent verdicts separately from lifecycle completion; passing is not inferred merely from a completed run.
