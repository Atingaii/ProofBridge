# Embedded TBCI artifact

`tbci-linux-amd64` is the Linux/amd64 static executable used by the isolated
ProofBridge TBCI experiment.

- module: `github.com/tbci-research/tbci`
- source revision: `39136b7f06e06f2172e45efea7cd6e62d758b7df`
- source patch: `patches/39136b7-proofbridge-rocq-v3.patch`
- adapter scope: additive Rocq project support; existing Lean and Isabelle
  adapters are retained unchanged
- Go toolchain: `go1.25.7`
- artifact SHA-256:
  `82f2fa855505a7cb3f5189f1e48db6a9b02433e5a623d3df3547af70c60f027d`
- target: `linux/amd64`

The digest is enforced again when the Bridge materializes the artifact.

## Reproduce

Starting from the source revision above, enter the `tbci/` Go module and apply
the module-relative patch there:

```bash
cd tbci
git apply /path/to/39136b7-proofbridge-rocq-v3.patch
CGO_ENABLED=0 GOMAXPROCS=2 /usr/local/go/bin/go build -p 1 -trimpath \
  -ldflags "-s -w" -o tbci-linux-amd64 .
sha256sum tbci-linux-amd64
```

The portable adapter regression gate is:

```bash
/usr/local/go/bin/go test -p 1 \
  ./internal/prover/rocq ./internal/prover/lean ./internal/prover/isabelle
```
