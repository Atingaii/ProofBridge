---
name: proofbridge
description: Use ProofBridge to run or inspect project verification on a connected machine, select saved models and execution presets, manage defaults, or handle a ProofBridge task's questions and results.
metadata:
  version: "1"
---

# ProofBridge

Use the configured `proofbridge` MCP server. The Hub manages the account,
configuration, task history and email. The user's selected Bridge owns the project
and runs Codex/Claude and the proof tools; the Hub's location is not the project's location.

If `PROOFBRIDGE_MANAGED_WORKER=1`, do not invoke this entrypoint, create a nested
ProofBridge run, or bypass the disabled MCP through shell/HTTP. Complete the assigned
local worker task instead. Do not change CLI permissions or disable other MCP servers.

## Select and submit

1. Call `proofbridge_get_context`. Resolve the user's endpoint, model options, execution
   preset and preferences with the discovery tools. Reuse an unambiguous saved default.
   Model presets, named execution presets and `profile=formal-proof` have different meanings.
2. Apply explicit choices over project defaults, then account defaults.
   If several endpoints/projects match without a selection, ask which one.
   For an unsupported model, effort or verifier, explain the available choices;
   do not silently replace it. MCP access does not provide model credentials or credits.
3. Ask the target Bridge to read the project's README as part of the task prompt.
   A local README can help interpret a same-machine request, but do not assume the CLI's
   current directory belongs to a different endpoint. The actual workspace is on the selected Bridge.
4. Use `proofbridge_preview_run` to resolve an unfamiliar configuration. Preview is not an
   extra user confirmation. Follow the server's actual schema; do not invent model names,
   reasoning levels, endpoint IDs or fields.
5. Call `proofbridge_start_run` with one stable `clientRequestId` for this action.
   On a lost reply, repeat the same arguments and key. Do not generate another key/run
   because a timeout or disconnect left acceptance uncertain.
6. Show the accepted `runId`, `submissionId`, actual endpoint/configuration and notification
   state. Accepted does not mean the proof passed.

## Observe or leave running

For “email me when done”, choose `background-mail` monitoring and check that completion
and attention notifications are ready. If the address or service setup is missing, explain
the personal-center action before submitting; do not promise an unavailable notification.
After acceptance, return control. The Hub sends email without keeping this CLI open.

For “wait here”, choose `interactive` and bounded `proofbridge_wait_run` calls.
Use returned cursors and limits for events. A wait timeout or canceled observation does not
cancel the task. Call `proofbridge_cancel_run` when the user asks to stop the work.

Closing the submitting CLI leaves accepted work running. The execution machine, its Bridge,
and its connection/VPN to the Hub must remain available. Attention email can link back to a
later approval or question; it does not automatically answer or grant permission.

## Continue and interact

- For a finished task, use `proofbridge_continue_run` with `operation=follow_up`,
  retaining the `runId`, endpoint and native context. A new top-level submission gets
  its own stable request key and `submissionId`.
- For a supported running-task instruction, use `operation=live_instruction` with the
  required message identity and explicit delivery mode. Preserve queued/delivered/unknown
  receipts. A rejection is not permission to create another run.
- Query `proofbridge_get_run_interactions` for an authoritative read-only snapshot.
  Do not reconstruct pending questions from old event text.
- Present native questions or command/file approvals to the user. Use
  `proofbridge_respond_run_interaction` to return their answer/decision to the same request.
  Do not convert answers into follow-up prompts, guess missing answers, or auto-approve.
- For uncertain delivery, call `proofbridge_respond_run_interaction` with `operation=reconcile`.
  Reconciliation can fence an old submission; it is not a read-only poll.
  Never replay unknown delivery as a new answer.
  Retry only after authoritative `not_sent` confirms the old ID is fenced and the request remains open.

## Preferences, results and reports

Save execution presets or persistent preferences when the user asks to remember or change
a default. A per-run override does not change global CLI configuration.
Read current model metadata rather than keeping a permanent model catalog in this skill.

Read `proofbridge_get_run` and the requested submission's evidence before reporting a result.
Keep execution status, verification authority/verdict, and mail status separate.
A completed run, exhausted budget, stalled work or compiler exit code alone does not
establish that the requested proof is satisfied. Preserve unmet obligations and checker evidence.
Report unavailable/truncated usage honestly; API-price estimates are not bills.

For history, statistics, notification troubleshooting and the full tool map, read
[the tool reference](references/tools.md). Missing MCP access should lead to the site's
personal-center setup/VPN instructions, not shell workarounds or credential requests.
