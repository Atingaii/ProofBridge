# ADR 033. Signed releases for managed Bridge endpoints

## Decision

A trusted installation can carry an Ed25519 release verification key and a numeric release sequence. Its connect process supervises a worker with the original connection arguments. It polls the same Hub origin for a signed release manifest, verifies the platform and executable digest, and stages the executable in a private per-endpoint directory. Each endpoint has an independent current release even when several link services use the same bootstrap executable. A durable sequence floor covers successful updates and failed trials.

For activation, the private Bridge requests an idle handoff over its existing registered WebSocket. The Hub serializes this decision with outbound work and checks persisted active chats and orchestration graphs. The Bridge also checks its local managers. Once both sides agree, the supervisor starts a trial worker with the same arguments. Only a successful Hub registration commits the current release. The previous release is available if the trial fails. A restart of the supervisor before confirmation keeps the last committed release.

## Consequences

The Hub distributes immutable release bytes and a manifest. A publisher holds the signing key independently. A Hub cannot direct endpoints to run a command, select an arbitrary URL or authorize an unsigned release. Initial trust is established when the bootstrap binary is installed through a trusted delivery path. Existing binaries without this mechanism require one installation of a trusted bootstrap. Future signed publications require no repeated installation.
