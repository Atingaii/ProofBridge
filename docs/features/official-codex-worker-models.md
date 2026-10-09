# Official Codex Worker Models

> Status: implemented and website deployed 2026-10-08. Existing running Bridges were preserved; explicit selections require ConfigSwitcher v3. See [deployment](../deployment.md#official-codex-models-edition).

## Goal

Let Codex A and B reuse official CLI authentication while selecting independent models and model-supported reasoning efforts directly under their existing official-login choices. Keep the existing initialization, worker roles, confirmation, dispatch, and continuation workflow.

## Non-Goals

- No account-level official preset library, new OAuth flow, or separate A/B accounts.
- No changes to Claude selectors, role prompts, scheduling, verifier assignment, or live model switching.
- No new historical preset restoration screen or model-draft persistence contract.
- No second Hub, new database, Android work, or cross-platform package release.

## Original State

Before this change, `frontend/src/app/pages/OrchestrationWorkspace.tsx:renderWorkerProfileSelectors` had an official option without model selection; `internal/hub/orchestration.go:resolveWorkerProfiles` ignored official effort overrides, and `internal/bridge/orchestration_worker_profiles.go:workerRuntime` returned an empty official runtime.

The existing confirmation reset remains an explicit machine-wide operation. Per-worker parameter selection must not introduce additional writes to the shared CLI configuration.

## Design

### User flow

Add only model and model-supported effort controls to the official Codex settings (A/B, or the existing `codex` slot in a mixed pair). Read one native catalog for the selected endpoint and share the catalog data between A/B while retaining independent selected values. Switching a model clears an unsupported old effort; switching endpoint/pair follows existing selector-reset behavior.

Retain the legacy default choice when parameters are unspecified. An explicit effort requires an explicit matching model. An explicit model with omitted effort resolves to its native catalog default; an empty catalog default stays unspecified. Keep existing create/continue submission rules, including `workerProfilesTouched`; do not add a freeze rule to continuation.

### Data and protocol impact

- Add authenticated, owner-checked `GET /api/agents/{agentID}/cli-config/official-models?cli=codex` through `internal/hub/server.go:NewServer` and the existing CLI configuration request correlation/timeout mechanism.
- Add `cli_config_official_models` and a separate official model metadata list in `CLIConfigResult`: native model identity, display name, supported efforts, suggested default effort, default model marker. No credentials or private paths are returned.
- Bridge queries native app-server `model/list`, handles pagination, excludes hidden models, bounds responses, and closes its read-only discovery process. It does not call provider probes, reset/apply, or require a new API key.
- Add `workerProfileModels` to create/continue HTTP payloads; reuse `workerProfileEfforts`. Accept explicit models only for official Codex bindings in the existing slot mapping. Reject incorrect slots, duplicate normalized slots, third-party model overrides, unknown models, and unsupported efforts using the endpoint-derived catalog.
- Save model/effort/capability snapshots in existing `WorkerProfileBinding` JSON. `internal/store/store.go:SetOrchestrationWorkerProfiles` needs no schema migration.
- Raise `ConfigSwitcher.Version` to 3 to promise both official directory support and effective per-worker model/effort execution. Directory queries and explicit official overrides require this version; legacy parameter-free official requests remain compatible.

### Runtime

Official runtime carries selected model and effort without creating provider homes or copying credentials. Pass the selected model and command-local `model_reasoning_effort` to exec and exec resume; carry model/effort through app-server startup, thread start/resume, and each turn start, including additional turns caused by live input or native question replies. Preserve the original run and thread identities and existing configuration-change fingerprint handling. Official processes select `openai`/`chatgpt` with command-local overrides; official thread start/resume sends `modelProvider: openai`, including when model/effort are cleared. This suppresses persisted native model/effort restoration without writing shared configuration. Native behavior is grounded in [Codex thread resume source](https://github.com/openai/codex/blob/rust-v0.161.0/codex-rs/app-server/src/request_processors/thread_processor.rs), `merge_persisted_resume_metadata` and `has_model_resume_override`.

Verifier calls use their already assigned slot's parameters without changing verifier allocation or role prompts. Keep default/third-party/Claude execution unchanged when no official overrides are provided.

### Rollout and website

Register `official-codex-worker-models` in `internal/rollout/rollout.go:RegisteredFeatures`, configure its policy, enforce it for new mutation fields, and consume `user.features`. Build with `VITE_LEADING=true VITE_OFFICIAL_CODEX_MODELS=true npm run build` for the enhanced leading frontend so existing standard/leading deployed frontends retain their current UI. The new edition is based on the test frontend and routes API/WebSocket traffic to the existing Hub and SQLite database.

Follow [deployment](../deployment.md) for native Hub builds, online SQLite backups, preserved original static roots, cookie/origin validation, and rollback. Do not upgrade or restart existing Bridges as part of website deployment without endpoint authorization. Existing Bridges may use legacy defaults; explicit official parameters require a compatible Bridge.

## Implementation Steps

1. Native catalog RPC, HTTP adapter, bounded discovery lifecycle, and capability version.
2. Slot model mapping, server-side catalog validation, existing binding persistence/continuation.
3. Complete native per-worker model/effort forwarding and meaningful Bridge tests.
4. Two selector controls and localized states; independent A/B state and model-effort interaction tests.
5. Rollout/build configuration, deployment/docs sweep, new host integration, and shared-data verification.

## Exit Gates

- A/B can use different models or the same model with different efforts; actual native calls receive the matching values.
- Unsupported model/effort combinations fail clearly; changing A never changes B.
- First call, restore, later turns, and verifier calls preserve parameters and identity.
- No new writes to shared config/auth/home are caused by parameter selection or catalog reads.
- Old Bridges cannot silently ignore explicitly chosen parameters; third-party presets and parameter-free official requests remain compatible.
- The new host has the enhanced test-based UI and shared users/history; existing sites remain accessible with their existing frontends.
- Focused RPC/Hub/Bridge/browser tests, frontend checks/build, full Go tests/native build, and `make doc-lint` pass.

## Reviewer Q&A

**Why not reuse the public provider catalog?** Native official model/effort metadata is the correct source for CLI selectors. The public provider catalog does not describe the installed client's official picker behavior.

**Does model listing guarantee access?** No. Catalog information populates selectors; actual authentication and inference may still fail. Such errors must not silently substitute another model. See [OpenAI App Server](https://learn.chatgpt.com/docs/app-server).

**Why update Bridge as well as the website?** Bridge launches the native CLI, and current official execution drops runtime overrides. An older Bridge can advertise third-party profile support while ignoring official effort/model fields.

**Why no ADR or new schema?** This extends an existing feature using reversible request fields and the existing worker binding store; authentication, database authority, and runner boundaries remain intact.
