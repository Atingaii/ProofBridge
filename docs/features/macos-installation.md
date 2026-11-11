# Native macOS Bridge installation

## Goals

Allow the existing `/install.sh` and `codex-bridge link` flow to install native
Apple Silicon and Intel Mac endpoints, preserving Linux downloads and all
existing credentials, presets, and endpoint configuration.

## Non-goals

No protocol, database, frontend, or enrollment changes. No launchd service,
macOS notarization, bundled proof tools, or Linux Landlock support on macOS.
The existing nohup background path is used on Mac; it does not restart after
reboot. Native macOS runtime validation must be reported separately from Linux
tests and cross-compilation.

## Data and API impact

Add `/downloads/codex-bridge-darwin-amd64` and
`/downloads/codex-bridge-darwin-arm64`. Validate Mach-O CPU/type before serving.
Preserve existing Linux endpoints and the legacy literal / `{arch}` external
download configuration. `{os}` is opt-in for cross-platform external hosting;
without it, legacy external URLs remain Linux-only and Mac uses Hub artifacts.

## Implementation steps

1. Extend `internal/hub/server.go:handleInstallScript` platform selection and
   `internal/hub/bridge_download.go:serveBridgePlatformBinary` validation.
2. Build all four artifacts sequentially via `Makefile:bridge-downloads` and
   publish them in the release workflow.
3. Preserve the actual child PID before releasing the nohup process in
   `link.go:startNohupBridge`.
4. Update installation docs; deploy matching artifacts before the new Hub.

## Exit gates

- Installer platform tests cover Darwin arm64/amd64, unsupported platforms,
  invalid binaries, and preserving an existing installation on failure.
- Download tests cover Mach-O CPU validation, missing artifacts, ranges, and
  legacy external URL compatibility.
- Low-concurrency Go tests, Linux/Darwin cross-builds, and doc lint pass.
- Public installer selects native Mac URLs and those downloads serve Mach-O.
- Mac operator runs install/link to verify actual connection on native macOS.

## Reviewer Q&A

**Why does an ARM64 Linux binary not work on Apple Silicon?**
CPU architecture alone is insufficient: Linux uses ELF; macOS uses Mach-O and
different operating-system interfaces.

**Are existing users required to configure providers again?**
No. Installation replaces only the executable after its `help` command passes.
No preset migration or credential rewrite is introduced.

**Does installing restart other workspaces?**
No. The installer keeps the existing endpoint-neutral behavior. Mac currently
uses the existing nohup fallback; launchd lifecycle management is future work.

## Validation record

2026-09-10: low-concurrency full Go tests, platform installer/download tests,
and all four cross-builds passed. The isolated Hub now serves
`v0.3.68-exp-macos`. Public Darwin downloads matched local SHA-256 hashes;
all four download routes returned valid 206 ranges with matching executable
headers. Doc lint reported zero errors and the existing `Server.Run` warning.
Native Mac installation and Hub connection remain to be confirmed on the
operator's Mac. No live preset, credential, or endpoint configuration changed.
