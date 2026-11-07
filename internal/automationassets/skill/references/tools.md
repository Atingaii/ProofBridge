# ProofBridge tool map

The connected server's schemas are authoritative. This map describes intent,
not substitute argument schemas. Resources must belong to the authenticated user.
ProofBridge, Codex Bridge and CodexBridge all use the same `proofbridge_` tools.

| Tool | Purpose |
| --- | --- |
| `proofbridge_get_context` | Inspect account/deployment capabilities, defaults and notification readiness. |
| `proofbridge_list_endpoints` | Find owned machines, workspaces, online state and execution/interaction capabilities. |
| `proofbridge_list_model_options` | Find saved provider presets and supported official-login model/effort options. |
| `proofbridge_list_execution_presets` | List named combinations, defaults and unavailable references. |
| `proofbridge_save_execution_preset` | Create/update a named combination with the expected version; do not start work. |
| `proofbridge_delete_execution_preset` | Delete an owned combination without changing accepted-task snapshots. |
| `proofbridge_get_preferences` | Read account/workspace defaults and notification choices. |
| `proofbridge_update_preferences` | Save explicitly requested defaults with version conflict checks. |
| `proofbridge_preview_run` | Resolve configuration/capabilities without running the proof. |
| `proofbridge_start_run` | Accept a task with a stable client request ID; return run/submission identity. |
| `proofbridge_continue_run` | Choose terminal follow-up or supported live instruction; preserve the original run. |
| `proofbridge_list_runs` | Query owned history with server filtering and bounded pagination. |
| `proofbridge_get_run` | Read task or frozen submission result and evidence; query notifications separately. |
| `proofbridge_get_run_events` | Read bounded pages using source cursors and truncation indicators. |
| `proofbridge_wait_run` | Observe changes for a bounded interval; do not own the execution lifetime. |
| `proofbridge_cancel_run` | Request cancellation and observe canceling/canceled state. |
| `proofbridge_get_run_interactions` | Read live snapshots/existing receipts; no pending-submission reconciliation. |
| `proofbridge_respond_run_interaction` | Answer/decide or explicitly reconcile delivery; it may change execution state. |
| `proofbridge_get_run_statistics` | Read usage provenance, wall-clock duration and labeled price estimates. |
| `proofbridge_get_notifications` | Inspect queued/sending/sent/retry/failed delivery and actionable causes. |

## Identities and failure recovery

- `runId` preserves task context. A top-level `submissionId` identifies one new/follow-up
  requirement and its frozen configuration/result/email.
- A live-instruction `messageId` has its own receipts. It is not a top-level submission.
- A native answer/decision identity binds a run, request, execution and Bridge instance.
  It is not the top-level task submission ID.
- After uncertain acceptance, reuse the original key and content.
  Do not switch from live instruction to follow-up or from follow-up to new run automatically.
- For uncertain answer delivery, query/reconcile. `unknown` does not mean nothing was delivered;
  only authoritative, fenced `not_sent` permits a new answer attempt.
- An unavailable Bridge cannot prove there are no pending questions. Retain the
  pending/unknown state rather than guessing from history.
- On a preference/preset version conflict, reload the current value and preserve user intent.
  Do not blindly overwrite another client's changes.

## Reporting

Distinguish: execution ended, an independent verifier accepted the request, and the mail
service accepted the report. Show actual authority, checks, unmet obligations and failures.
Mail failure is not proof failure. Attention email is not a completion report.

`background-mail` requires an account address and ready mail service. Both completion
and attention notifications are required so review-required work cannot silently wait forever.
Check `mailDeliveryMode`: `preview` is local capture only; `smtp` identifies the configured
outgoing transport, not a delivery guarantee. Notification `deliveryModeScope=current-transport`
describes the current configuration, not each historical message. `sentStatusMeaning=smtp-accepted`
means the SMTP server accepted the message; it does not establish external inbox receipt.
Pending requests retain their original native callback. Result links remain private and
may require VPN and sign-in.

Use MCP tools. Do not ask users to paste model API keys or the MCP token into chat;
client configuration and existing account/endpoint authentication own those secrets.
