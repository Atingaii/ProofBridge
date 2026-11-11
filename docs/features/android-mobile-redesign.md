# Android and mobile browser adaptation

Status: software published on 2026-10-08 at `https://codex.sparkon.cn`; signed Android 1.1.0 available. Actual-device installation and memory acceptance remain pending.

## Goals

Provide every existing website capability through the Android client and a usable
phone layout on codex.sparkon.cn. Improve navigation, reading, touch targets,
keyboard visibility, and the Android installation footprint.

The user explicitly reactivated Android maintenance and limited this work to
Android and phone presentation. Existing business behavior remains the contract.

## Explicit non-goals

No Hub/Bridge/store/protocol changes, database migrations, API or model-selection
changes, worker-role changes, pagination/retry/state-machine changes, or automatic
Bridge upgrades. No offline execution, new push service, other-platform client,
other-site release, or automatic removal of an existing Android installation.

## Presentation contract

- `frontend/src/app/pages/Workspace.tsx:Workspace`: preserve all callbacks and
  session identity while improving mobile navigation, reading, and input.
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`:
  expose history and configuration on phones; keep the transcript usable without
  permanently stacking the full configuration beneath a short reading area.
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:renderWorkerProfileSelectors`:
  retain official login and independent A/B model/effort setup at first dispatch.
- `frontend/src/app/components/Settings.tsx:SettingsModal`: preserve settings and
  endpoint/provider capabilities with keyboard-safe phone presentation.
- Existing task graphs, statistics, sharing, attachments, native questions,
  approvals, help, and authorized administrator pages remain reachable.
- On phones, existing run conversation and delivery history open in a full-height
  panel from the bottom navigation. Keep the same mounted input and handlers;
  short landscape screens must retain a usable main reading area.
- Panel visibility and viewport changes do not clear drafts, create contexts,
  submit answers, or change account/target state isolation.

## Data and protocol impact

None. Chat keeps the existing sid, orchestration keeps runID and the existing
prompts endpoint. See [continuity](orchestration-continuity.md). Android is a
same-origin presentation host, not a new credential or execution owner.

## Implementation steps

1. Capture and inspect current mobile screens, using existing design tokens.
2. Adapt phone presentation with original components and callbacks.
3. Implement the [single-WebView Android host](../adr/028-lightweight-android-webview.md).
4. Verify mobile feature parity, desktop regressions, and release APK boundaries.
5. Update only codex static assets; retain the previous root for rollback.

## Exit gates

- Complete the mobile capability matrix and mock API/WS interaction checks.
- Cover narrow/landscape phone layouts, larger text, keyboard, safe areas,
  history/configuration, structured input, and same-context continuation.
- With an active run, retain at least 120 CSS pixels of transcript height at
  844×390 and 390×400; opening the run-conversation panel must keep its draft
  and delivery records, with a reachable 48 CSS pixel send target.
- Preserve desktop behavior and existing business contracts.
- Build and verify a signed release APK, target URL, version, contents, and hash;
  use a 5 MiB engineering budget and report any measured exception.
- Record installation and host/renderer memory on an actual test device. Missing
  device results must remain explicitly unverified.
- Run focused frontend checks, regenerate embedded output, Go checks, Android
  checks, and document lint sequentially with bounded machine load.

## Reviewer Q&A

**Does this change orchestration or models?** No. Mobile controls invoke the
existing handlers and preserve initialization, roles, payloads, and continuation.

**Are phone features reduced?** No. Secondary surfaces may be opened on demand;
the acceptance matrix includes every existing capability and its authorization.

**Can the APK replace an old installation?** Only with compatible signing and
versioning. No old certificate has been verified; do not promise an in-place
upgrade or automatically uninstall user data.

**Is the memory target already proven?** No. System WebView memory includes the
renderer; APK size and Java heap alone are insufficient evidence.
