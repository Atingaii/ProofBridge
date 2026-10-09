# Official Login Worker Presets

## Goals

- Show an official-login choice in each orchestration worker selector, selected by default.
- Require an explicit confirmation before applying the official-login mode.
- Reuse the existing CLI configuration reset operation, then let the operator complete `codex login` or Claude Code authorization on the private machine.
- Preserve saved provider presets, reasoning controls, continuation, and all existing chat/configuration flows.

## Non-goals

- This does not perform OAuth or CLI authorization from the browser.
- This does not change saved provider preset storage or credential encryption.
- This does not alter the default CLI configuration outside an explicit confirmation.

## Design

The browser uses a synthetic `__official__` selector value. Confirmation calls the existing per-agent official reset endpoint for the selected CLI and records local confirmation state. New orchestration requests include an explicit official-login map; the Hub resolves these entries into `WorkerProfileBinding{OfficialLogin:true}` without requiring an encrypted API key. The Bridge recognizes this binding as a request to use the machine's normal CLI home and does not create an isolated provider runtime. Saved presets continue to use the existing encrypted, isolated runtime path.

## Implementation steps

1. Add the protocol binding flag and Hub request validation/resolution.
2. Add Bridge handling that preserves the global official CLI authentication path.
3. Add default browser choices, confirmation controls, request fields, and localized copy.
4. Rebuild embedded static assets and add focused tests.

## Exit gates

- Official choices are selected by default and cannot start before confirmation.
- Confirmation failure prevents starting.
- Both Codex and Claude official bindings use local official authentication.
- Existing saved presets remain unchanged.
- Frontend build and focused Go tests pass.

## Reviewer Q&A

**Why is confirmation separate from selection?** Selecting a mode is reversible UI state; confirmation is the point at which custom provider overrides are removed.

**Why does the Hub not require a credential?** Official login credentials already belong to the private machine and must never be synthesized or sent through the Hub.
