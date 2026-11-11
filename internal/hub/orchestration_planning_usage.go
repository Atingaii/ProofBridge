package hub

import (
	"encoding/json"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// Only the immutable binding authorizes looking in the official native home.
// Never search a current preset's home for an old isolated or ambiguous call.
func planningUsageRecoveryHints(events []store.OrchestrationEvent, graphs []store.OrchestrationTaskGraph) []protocol.OrchestrationUsageRecovery {
	payloads := map[string]protocol.OrchestrationStartPayload{}
	for _, graph := range graphs {
		var p protocol.OrchestrationStartPayload
		if json.Unmarshal([]byte(graph.PayloadJSON), &p) == nil {
			payloads[graph.ID] = p
		}
	}
	starts := map[string]store.OrchestrationEvent{}
	nativeIDs := map[string]bool{}
	for _, e := range events {
		if e.Kind == "turn.start" {
			starts[e.TurnID] = e
		}
		if e.Kind == "bridge.note" && usageText(e.Data, "category") == "native-identity" && usageText(e.Data, "nativeSessionId") != "" {
			nativeIDs[e.TurnID] = true
		}
	}
	var hints []protocol.OrchestrationUsageRecovery
	seen := map[string]bool{}
	for _, e := range events {
		if e.Kind != "turn.usage" || e.CLI != "codex" || !missingUsageModel(usageText(e.Data, "model")) || nativeIDs[e.TurnID] || seen[e.TurnID] {
			continue
		}
		start := starts[e.TurnID]
		if start.Task == nil || start.Task.Role != "planner" {
			continue
		}
		p, ok := payloads[start.Task.GraphID]
		binding, bound := p.WorkerProfiles[start.Task.WorkerSlot]
		if !ok || !bound || !binding.OfficialLogin || binding.CLI != "codex" {
			continue
		}
		usage := orchestrationUsageFromData(e.Data)
		if !usage.Native || usage.InputTokens <= 0 || usage.OutputTokens <= 0 || usage.CacheWriteTokens != 0 {
			continue
		}
		cwd := p.RunCWD
		if cwd == "" {
			cwd = p.CWD
		}
		if cwd == "" || start.CreatedAt <= 0 || e.CreatedAt < start.CreatedAt || e.CreatedAt-start.CreatedAt > 86400 {
			continue
		}
		hints = append(hints, protocol.OrchestrationUsageRecovery{WorkerSlot: start.Task.WorkerSlot, CWD: cwd, StartedAt: start.CreatedAt, EndedAt: e.CreatedAt, InputTokens: usage.InputTokens, CacheReadTokens: usage.CacheReadTokens, OutputTokens: usage.OutputTokens})
		seen[e.TurnID] = true
		if len(hints) == 8 {
			break
		}
	}
	return hints
}
