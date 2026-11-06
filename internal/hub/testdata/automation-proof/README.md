# Automation proof transport fixture

The task is to read this README and verify `Goal.v` with `coqc Goal.v`.
The theorem uses no admitted proof or project axiom.

`codex_fixture.py` replaces the model CLI transport for deterministic integration
checks. Each worker turn and each independent verifier executes the installed
`coqc` process against this workspace. It derives command exit status and its
structured verifier result from that process. It never calls a model/provider.

This fixture verifies MCP, reverse WebSocket, Bridge orchestration, a real Coq
compiler, durable results and local SMTP together. It does not measure model
reasoning, natural-language routing, commercial-provider availability, real inbox
delivery or VPN connectivity.
