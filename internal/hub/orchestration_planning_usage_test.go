package hub

import (
	"encoding/json"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"testing"
)

func TestCanceledPlanningIdentityProjection(t *testing.T) {
	events := []store.OrchestrationEvent{
		{Kind: "turn.start", CLI: "codex", TurnID: "plan", CreatedAt: 100, TurnStartData: &protocol.TurnStartData{WorkerSlot: "codex-a", Round: 1}},
		{Kind: "bridge.note", CLI: "codex", TurnID: "plan", CreatedAt: 101, Data: map[string]any{"category": "native-identity", "model": "actual", "nativeSessionId": "plan-session"}},
		{Kind: "turn.usage", CLI: "codex", TurnID: "plan", CreatedAt: 110, Data: map[string]any{"inputTokens": 198150, "cacheReadTokens": 145920, "outputTokens": 2709, "native": true}},
		{Kind: "turn.usage", CLI: "codex", TurnID: "plan", CreatedAt: 110, Data: map[string]any{"estimated": true}},
	}
	run := store.OrchestrationRun{WorkerPair: "codex-codex"}
	sessions := orchestrationUsageSessions(run, events, nil)
	if len(sessions) != 1 || sessions[0].WorkerSlot != "codex-a" || sessions[0].SessionID != "plan-session" {
		t.Fatal(sessions)
	}
	stats := buildOrchestrationRunStatsWithLedger(run, events, nil, nil)
	if len(stats.ByWorker) != 1 || stats.ByWorker[0].Model != "actual" || stats.TotalTokens != 200859 || stats.CallCount != 1 {
		t.Fatalf("%+v", stats)
	}
	if events[2].Data["model"] != nil {
		t.Fatal("modified historical evidence")
	}
	events = append(events, store.OrchestrationEvent{Kind: "bridge.note", CLI: "codex", TurnID: "plan", Data: map[string]any{"category": "native-identity", "model": "conflicting"}})
	enriched := enrichOrchestrationUsageTimeline(events, nil)
	if !missingUsageModel(usageText(enriched[2].Data, "model")) {
		t.Fatal("guessed conflicting model")
	}
	if snapshotUsageCallCount(orchestrationUsageStats{Native: true}) != 1 {
		t.Fatal("dropped real zero-token report")
	}
	if !publicOrchestrationEventHidden(store.OrchestrationEvent{Kind: "bridge.note", Source: "bridge", Data: events[1].Data}) {
		t.Fatal("exposed native identity")
	}
}

func TestHistoricalPlanningRecoveryHints(t *testing.T) {
	for _, official := range []bool{true, false} {
		payload, _ := json.Marshal(protocol.OrchestrationStartPayload{CWD: "/workspace", WorkerProfiles: map[string]protocol.WorkerProfileBinding{"codex-a": {CLI: "codex", OfficialLogin: official}}})
		graphs := []store.OrchestrationTaskGraph{{ID: "old", PayloadJSON: string(payload)}}
		events := []store.OrchestrationEvent{
			{Kind: "turn.start", CLI: "codex", TurnID: "plan", CreatedAt: 100, Task: &protocol.TaskAttemptRef{GraphID: "old", Role: "planner", WorkerSlot: "codex-a"}},
			{Kind: "turn.usage", CLI: "codex", TurnID: "plan", CreatedAt: 200, Data: map[string]any{"native": true, "inputTokens": 198150, "cacheReadTokens": 145920, "outputTokens": 2709}},
		}
		hints := planningUsageRecoveryHints(events, graphs)
		if official && (len(hints) != 1 || hints[0].InputTokens != 198150 || hints[0].CWD != "/workspace") {
			t.Fatal(hints)
		}
		if !official && len(hints) != 0 {
			t.Fatal("isolated runtime fell back to global home")
		}
		events[0].Task.GraphID = "missing"
		if len(planningUsageRecoveryHints(events, graphs)) != 0 {
			t.Fatal("used unrelated graph")
		}
	}
}
