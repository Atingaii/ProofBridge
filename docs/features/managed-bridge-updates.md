# Managed Bridge updates

## Goals and scope

Release publication lets linked and foreground `connect` endpoints on Linux and macOS adopt a new version when idle. Owners of independently packaged endpoints can continue using an unsigned build or disable the managed updater on connect. Existing model credentials, enrollment, workspace permissions and machine IDs stay under the endpoint owner's control.

## Protocol and storage

The public GET routes under `/downloads/releases/` expose one signed manifest per platform and immutable SHA-256 named executables. Ed25519 authenticates a release sequence, version, platform, size and digest. Endpoint state advances monotonically. The supervisor and each service's active executable are independently scoped so another workspace can keep running. The private signing key is kept outside the served directory.

A Bridge that has staged a signed release sends an idle request on its registered WebSocket. The Hub checks queued and running work and fences admission to the existing connection before granting. The Bridge checks its local sessions and runs before exiting for a handoff. The supervisor verifies the candidate's build identity and a real Hub registration before committing it. Startup failures return to the last working executable. The normal enrollment, authentication, model-selection and execution protocols are reused.

## Implementation and acceptance

Operators can build a pinned executable, sign its release and publish its
bootstrap download in a single step. Use the same private key for later
publications and increment the sequence. Each platform has its own channel.

```sh
make managed-bridge-release VERSION=1.4.2 RELEASE_SEQUENCE=42 \
  RELEASE_PLATFORM=linux-amd64 RELEASE_SIGNING_KEY=/srv/private/bridge-ed25519.pem \
  DOWNLOAD_DIR=/srv/proofbridge/bin/downloads
```

Repeat for `linux-arm64`, `darwin-amd64` and `darwin-arm64` when publishing
those platforms. The built-in Hub serves `downloads/releases/` next to its
executable. The publisher retains previous content-addressed binaries for
clients that already fetched a manifest. Install an initial trusted bootstrap
through the endpoint owner's existing deployment channel.

1. Build with the operator's trusted verification key and release sequence.
2. Publish an immutable executable, then atomically replace its platform manifest.
3. Stage and verify, acquire an idle handoff, run and confirm a trial.
4. Exercise a successful local handoff, active jobs, invalid signatures and digests, replay, failed trial, rollback and independent services sharing an installation.

## Reviewer questions

**How do older endpoints participate?** Install the trusted bootstrap once using the endpoint owner's existing deployment mechanism. They can then adopt future signed releases automatically.

**What happens on a long proof?** The already running worker keeps its connection and its executable while the replacement is staged. The Hub includes durable runs between graph attempts in its eligibility check.

## Publishing and bootstrapping

Generate the operator signing key once on the release machine. Keep the owner-only PEM outside the distribution directory and back it up separately. The command prints the public key pin used by the build.

```sh
umask 077
PB_SIGNING_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/proofbridge-release"
mkdir -p "$PB_SIGNING_DIR"
python3 scripts/publish-bridge-release.py keygen --key "$PB_SIGNING_DIR/ed25519.pem"
```

Use the generated key as `RELEASE_SIGNING_KEY` in the publication command above. With a reverse proxy, serve the published directory at the same `/downloads/releases/` path.

During the one-time migration of legacy services, deliver the trusted generated `codex-bridge-linux-amd64` executable (or its corresponding target) using the owner's existing secure installation channel. On HTTP-only LANs, compare the initial executable's digest with a value obtained over an authenticated administrative channel. The embedded Ed25519 pin then authenticates subsequent releases over the existing LAN URL. Check `codex-bridge release-info` for the expected operator pin and sequence. Switch each idle service to the trusted executable with its existing `connect` arguments, machine-ID file and permissions. Existing linked services run normally under their current supervisor or terminal.

A managed bootstrap polls immediately after registration and then every minute. It stores the active signed release, the previous confirmed release and the durable sequence floor beneath the endpoint user's `~/.codex-bridge/updates/`. A failed trial gets up to two minutes for startup and registration. A crash during the initial probation returns the endpoint to its previous confirmed build. A new publication with a higher sequence can replace a failed trial. Publish a reviewed earlier code revision under a new, higher sequence when reverting across a fleet. `connect --managed-updates=false` runs the installed executable directly for an explicitly pinned endpoint. Regular `bridge` daemons managed by an operating-system package retain that package's lifecycle.

## Validation

The standard Go tests exercise signature, digest, platform, failed startup and admission gates. This tagged test builds two actual releases, publishes through the operator script and verifies that the original bootstrap reconnects the same enrolled endpoint to the new version.

```sh
go test -tags release_smoke ./internal/hub -run '^TestSignedManagedReleaseAcrossExecutables$' -count=1 -v
```

The implementation entry points are `internal/bridgeupdate/supervise.go:Run`, `internal/bridge/client.go:connectOnce`, `internal/hub/managed_releases.go:handleReleaseHandoff` and `scripts/publish-bridge-release.py:publish`.
