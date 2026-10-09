# Linux ARM64 Bridge Installation

> Native macOS support extends this design; see [macOS installation](macos-installation.md).

## Goals

An ARM64 Ubuntu endpoint must receive a Linux ARM64 Bridge instead of the Hub's
x86-64 executable. Preserve existing Linux amd64 installations and endpoint
identity when rerunning the installer.

## Non-goals

Native macOS/Windows packaging, CPU emulation, permission-profile changes,
automatic restarts of existing endpoints, and porting the embedded TBCI tool.
ARM64 Bridges report TBCI unavailable unless a working native override exists;
the existing agent verification mode remains usable.

## Data and protocol impact

Keep `/install.sh` and `/downloads/codex-bridge-linux-amd64`; add the fixed
`/downloads/codex-bridge-linux-arm64` route. The installer selects its URL from
`uname -s` and `uname -m`, supporting Linux x86_64/amd64 and aarch64/arm64.
Unsupported platforms stop before downloading or replacing any executable.

`internal/hub/server.go:bridgeDownloadURLForArch` supports `{arch}` in the
existing external download URL. A legacy literal URL remains amd64-only;
ARM64 uses its Hub route. The API's existing download metadata remains
compatible; generated setup commands still invoke `/install.sh`.

`internal/hub/bridge_download.go:serveBridgeBinary` serves the running Hub
binary for its own Linux architecture, or the exact sibling file
`downloads/codex-bridge-linux-<arch>` for the other architecture. Validate the
ELF machine and executable type before serving; missing or wrong artifacts
return explicit errors, never a binary for the other architecture. No database
or WebSocket change is needed.

## Implementation steps

1. Detect the client platform in the installer; run the downloaded executable's
   `help` command before atomically replacing an existing installation.
2. Add the bounded download handler and architecture-specific URL selection.
3. Add a sequential dual-architecture build target and ARM64 release artifact.
4. Build and stage both artifacts on the isolated Hub, restart only the Hub,
   and verify public downloads and remote Bridge reconnects.

## Exit gates

- Executed shell tests select the correct URLs for both supported architectures.
- Unsupported platforms and invalid downloads preserve an existing binary.
- Download handler tests reject missing/mislabeled artifacts and support ranges.
- Go tests and both Linux builds succeed with bounded concurrency.
- Public ARM64 download has the expected ELF machine and matches the build hash.
- Report the distinction between cross-compilation/emulated checks and actual
  native execution; do not claim testing on an ARM64 machine unless performed.

## Reviewer Q&A

**Why not serve the Hub executable for every machine?**

An x86-64 executable cannot execute natively on ARM64. The remote endpoint's
architecture determines the download, independently of the Hub host.

**Why check the executable before replacing the old file?**

A failed download, incorrect external artifact, or unsupported format should
leave the existing installation intact and produce an actionable error.

**Does installing update or stop existing sessions?**

No. Installation only replaces the file. The existing `link` command handles
the selected workspace's endpoint, preserving the established identity flow.

## Verification record

On 2026-09-08, executed installer simulations, download-handler tests, the full
Go test suite, and sequential Linux amd64/arm64 builds passed. The isolated
Hub deployed version `v0.3.64-exp-linux-arm64`; both public downloads matched
their build SHA-256 values and ELF architectures, and the ARM64 route served
HTTP byte ranges successfully. Native ARM64 execution was not tested on this
amd64 host. The installer checks execution on the endpoint before replacement.
