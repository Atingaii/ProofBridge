# ADR-020: Independent early run completion

Status: accepted, implemented and validated.

## Decision

Completion is independent of planner output, plan progress, and checklist state.
Only the original user task, the actual workspace, and independent verifier
observations enter adjudication. A resolved worker/integrator handoff may request
verification after its process finishes; it is a trigger, never evidence of success.
The same verifier path serves collaboration and debate.

A verified pass can terminate the durable graph from an execution node. Persist
that decision with the current attempt and cancel undispatched successors in the
same transaction. Planner nodes cannot terminate a graph this way. Retain attempt,
generation, cancellation, and task identity guards during replay and reconnect.

Following the user's explicit policy, three unchanged consecutive reviewer
snapshots with the same blocker terminate immediately without a corrective round.
Both this automatic closure and verified completion publish `run.end` with run
status `completed`, and the UI presents one completion result. A separate factual
verification status and retained check records distinguish execution completion
from proved task satisfaction. A failed check must never be converted to `pass`.

Optional structured checker codes/targets stabilize blocker identity. Bind new
observations to the original task and workspace, not the plan. Unknown observations
do not prove stagnation. Native output-token plateaus remain diagnostic only.

## Consequences

No schema migration, new agent framework, changed permission policy, or extra
planning output. Preserve the existing single Go binary and native sessions.
New protocol fields are optional; incoming legacy `stalled` errors are normalized
to completed before persistence, without rewriting historical events.
Heavy verifier invocations are serialized with cancellation while waiting.
See [feature design](../features/independent-early-run-completion.md).
