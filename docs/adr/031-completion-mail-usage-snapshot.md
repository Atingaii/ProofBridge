# ADR-031: Freeze Completion-Mail Usage In The Outbox

## Status

Accepted, 2026-11-07. Extends [ADR-029](029-hub-mcp-and-submission-notifications.md).

## Context

Completion email needs the same Token and estimated-cost values as the website.
One run can contain later submissions, while an email can be retried after its
original submission finishes. Reading current totals on every send would mix
submission boundaries and change a report during retries.

The existing statistics aggregation lives in Hub. Submission settlement runs
inside a Store transaction with one SQLite connection. Calling normal Store
reads from that transaction would wait for its own connection; moving the whole
aggregation or registering a Store-to-Hub callback would expand the dependency
boundary for a presentation feature.

## Decision

1. Freeze the actual terminal event sequence as the submission result's optional
   `usageCutoffSeq`. A node completion or an unconfirmed recovery is not a valid
   cutoff.
2. After claiming an outbox row, collect its run, bounded timeline, observed
   identities, ledger, syncs and graph metadata in one read-only transaction.
   Close that transaction before reusing the website's existing aggregation.
3. Validate attribution before aggregation. Do not use the run's current native
   identities. If a later prompt or insufficient ledger identity makes the
   boundary ambiguous, report usage as unrecorded.
4. Compare-and-swap a small `usageSummary` into only the claimed outbox snapshot.
   The row ID, claim token, unexpired lease and original snapshot must still
   match. Do not modify the accepted submission result. Subsequent retries and
   late conclusions retain this first projection.
5. An accounting failure must not change the proof outcome or indefinitely delay
   its status notice. Freeze an unavailable metric result instead. Unknown costs
   are not zero; available costs remain estimates, not invoices.

No new table, column, wire frame, provider request, price refresh or Store-to-Hub
callback is required. Existing snapshots without a trustworthy cutoff remain
readable and show unrecorded metrics.

## Consequences

The email describes run-cumulative usage through that submission's completion,
not a future total or a new per-message billing system. Conservative attribution
can omit otherwise visible current totals. This preserves a truthful boundary.

The outbox has a derived, immutable presentation projection; settlement and
delivery receipts retain their existing authority and retry behavior. Expired
claims cannot prepare or send a new report.

## Implementation And Validation

- `internal/hub/automation_mail_summary.go:prepareAutomationNotification`
- `internal/hub/automation_mail_summary.go:automationCompletionUsage`
- `internal/store/automation_usage_snapshot.go:ReadAutomationNotificationUsage`
- `internal/store/automation_usage_snapshot.go:FreezeAutomationNotificationUsage`
- `internal/hub/automation_mail_summary_test.go:TestAutomationCompletionSummaryMatchesDetailsAndFreezesAcrossContinuation`
- `internal/store/automation_usage_snapshot_test.go:TestAutomationUsageCutoffReadAndFreezeCAS`

The user-facing content, MIME fallback, visual references and exit gates are in
the [completion email contract](../features/completion-email-summary.md).
