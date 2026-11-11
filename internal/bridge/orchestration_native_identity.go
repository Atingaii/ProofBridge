package bridge

import "github.com/tencent/codex-bridge/internal/protocol"

func nativeIdentityEvent(turnID, role, slot, sessionID, model string) protocol.OrchestrationEventPayload {
	return protocol.OrchestrationEventPayload{Kind: "bridge.note", Source: "bridge", CLI: "codex", TurnID: turnID, Role: role, Data: map[string]any{
		"category": "native-identity", "nativeSessionId": sessionID, "workerSlot": slot, "model": model,
	}}
}
