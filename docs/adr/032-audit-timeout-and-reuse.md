# ADR-032: Audit timeout isolation and independent final-audit reuse

Status: accepted, 2026-11-17.

## Decision

An audit timeout does not establish a source defect. Keep retries inside the
independent verifier invocation with a 24-hour deadline from the first attempt
and one audit-only retry at the same cap. Preserve retry usage and cancellation. Exhaustion emits a
terminal verification-timeout error instead of a successful task-node end, so Hub
cannot schedule another editing round from an incomplete audit.

The current independent reviewer may establish completion directly through a
native successful final-audit command evidence, without a fixed final handoff
format. A successful read of a fresh independent final-audit log also qualifies. A
fresh redirected log can supply that command's output. Require a complete bounded
source scan with no file modified after the audit/log timestamp. Subsequent
successful read-only commands preserve the result; writes and failed commands
invalidate it. Explicit remaining work, risks and blocked handoffs still block
direct closure. Ordinary worker prose, stale logs, failed commands and partial probes
do not establish this authority. Otherwise consult up to two Agent Verifiers, stopping immediately at the first
valid independent pass unless a concrete conflict has already been observed;
those agents inspect existing independent logs first and reuse validated results.

## Consequences

No schema, wire frame, permission or public configuration changes. This narrows
the unconditional second-audit policy in ADR-020 and ADR-030; the independent
reviewer remains the verification authority. Existing Hub task failure and
verified-early transactions provide termination and successor cancellation.
Bridge must be upgraded for the behavior to apply to its future executions.
See [design and exit gates](../features/audit-timeout-and-final-evidence.md).
