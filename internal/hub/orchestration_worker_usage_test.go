package hub

import (
	"encoding/json"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestHistoricalWorkersKeepSameModelSeparate(t *testing.T) {
	run := store.OrchestrationRun{ID: "old", CreatedAt: 100, FinishedAt: 200, Status: store.OrchestrationCompleted}
	events := []store.OrchestrationEvent{}
	for i, slot := range []string{"codex-a", "codex-b"} {
		turn := slot + "-turn"
		events = append(events, store.OrchestrationEvent{Kind: "turn.start", CLI: "codex", TurnID: turn, CreatedAt: int64(110 + i), TurnStartData: &protocol.TurnStartData{WorkerSlot: slot, Model: "gpt-5.6-sol", Round: 2}}, store.OrchestrationEvent{Kind: "turn.usage", CLI: "codex", TurnID: turn, CreatedAt: int64(120 + i), Data: map[string]any{"inputTokens": 100 * (i + 1), "cacheReadTokens": 20, "outputTokens": 10, "native": true}})
	}
	stats := buildOrchestrationRunStatsWithLedger(run, events, nil, nil)
	if len(stats.ByCLI) != 1 || len(stats.ByWorker) != 2 || stats.InputTokens != 260 || stats.TotalTokens != 320 {
		t.Fatalf("bad aggregation: %+v", stats)
	}
	if stats.ByWorker[0].WorkerSlot != "codex-a" || stats.ByWorker[1].InputTokens != 180 || stats.ByWorker[1].Model != "gpt-5.6-sol" {
		t.Fatal(stats.ByWorker)
	}
	if stats.Rounds[len(stats.Rounds)-1].Round != 2 || len(stats.Rounds[len(stats.Rounds)-1].ByWorker) != 2 {
		t.Fatal(stats.Rounds)
	}
	if _, mutated := events[1].Data["model"]; mutated {
		t.Fatal("mutated stored evidence")
	}
}

func TestImmutableGraphModelAndNativePrecedence(t *testing.T) {
	payload, _ := json.Marshal(protocol.OrchestrationStartPayload{WorkerProfiles: map[string]protocol.WorkerProfileBinding{"codex-a": {Model: "old-model"}}})
	events := []store.OrchestrationEvent{
		{Kind: "turn.start", CLI: "codex", TurnID: "a", CreatedAt: 110, TurnStartData: &protocol.TurnStartData{WorkerSlot: "codex-a", Round: 1}},
		{Kind: "turn.usage", CLI: "codex", TurnID: "a", CreatedAt: 120, Data: map[string]any{"model": "native-model"}},
		{Kind: "turn.usage", CLI: "codex", TurnID: "a", CreatedAt: 121, Data: map[string]any{}},
		{Kind: "turn.usage", CLI: "codex", TurnID: "missing", CreatedAt: 122, Data: map[string]any{}},
	}
	enriched := enrichOrchestrationUsageTimeline(events, []store.OrchestrationTaskGraph{{CreatedAt: 100, PayloadJSON: string(payload)}})
	if enriched[1].Data["model"] != "native-model" || enriched[2].Data["model"] != "old-model" || usageText(enriched[3].Data, "model") != "" {
		t.Fatal(enriched)
	}
}

func TestLedgerUsesWorkerRoundAndPreservesTotals(t *testing.T) {
	events := []store.OrchestrationEvent{
		{Kind: "turn.start", CLI: "codex", CreatedAt: 110, TurnStartData: &protocol.TurnStartData{WorkerSlot: "codex-b", Round: 1}},
		{Kind: "turn.start", CLI: "codex", CreatedAt: 120, TurnStartData: &protocol.TurnStartData{WorkerSlot: "codex-a", Round: 2}},
	}
	ledger := []protocol.OrchestrationUsageEvent{{CLI: "codex", WorkerSlot: "codex-a", Model: "gpt-5.5", OccurredAt: 125, InputTokens: 100}, {CLI: "codex", WorkerSlot: "codex-b", Model: "gpt-5.5", OccurredAt: 126, InputTokens: 200}}
	stats := buildOrchestrationRunStatsWithLedger(store.OrchestrationRun{}, events, ledger, []store.OrchestrationUsageSync{{Status: "complete"}})
	if len(stats.ByWorker) != 2 || stats.Rounds[0].InputTokens != 200 || stats.Rounds[1].InputTokens != 100 || stats.TotalTokens != 300 {
		t.Fatal(stats)
	}
}

func TestOfficialLoginUsageSyncUsesNativeHomeAndNormalizesLegacySlots(t *testing.T) {
	run := store.OrchestrationRun{WorkerPair: "codex-codex", CodexThreadIDs: map[string]string{"codex-a": "session-a", "codex-b": "session-b"}}
	events := []store.OrchestrationEvent{{RunEndData: &protocol.RunEndData{NativeResume: []protocol.NativeResumeInfo{{CLI: "codex-a", ID: "session-b"}, {CLI: "codex-b", ID: "session-b"}, {CLI: "claude", ID: "orphan"}}}}}
	sessions := orchestrationUsageSessions(run, events, map[string]protocol.WorkerProfileBinding{"codex-a": {OfficialLogin: true}, "codex-b": {OfficialLogin: true}})
	if len(sessions) != 2 {
		t.Fatal(sessions)
	}
	for _, s := range sessions {
		if s.Isolated || s.CLI != "codex" || s.WorkerSlot == "" {
			t.Fatal(s)
		}
	}
}

func TestHistoricalIdentitySyncRetryIsBounded(t *testing.T) {
	syncs := []store.OrchestrationUsageSync{{Status: "unavailable", Error: "isolated worker runtime is no longer available", ScannedAt: 100}}
	if historicalUsageSyncDue(syncs, 200) {
		t.Fatal("repeated recent scan")
	}
	if !historicalUsageSyncDue(syncs, 22000) {
		t.Fatal("legacy repair not retried")
	}
	syncs = append(syncs, store.OrchestrationUsageSync{Status: "complete", ScannedAt: 21999})
	if historicalUsageSyncDue(syncs, 22000) {
		t.Fatal("stale alias triggered another scan")
	}
}

func TestLegacyDuplicatedSessionIsNotChargedToBothWorkers(t *testing.T) {
	run := store.OrchestrationRun{WorkerPair: "codex-codex", CodexThreadIDs: map[string]string{"codex-a": "current-a", "codex-b": "current-b"}}
	events := []store.OrchestrationEvent{{RunEndData: &protocol.RunEndData{NativeResume: []protocol.NativeResumeInfo{{CLI: "codex-a", ID: "copied-old"}, {CLI: "codex-b", ID: "copied-old"}}}}}
	sessions := orchestrationUsageSessions(run, events, nil)
	if len(sessions) != 2 {
		t.Fatal(sessions)
	}
	for _, session := range sessions {
		if session.SessionID == "copied-old" {
			t.Fatal("ambiguous historical session attributed to a worker", session)
		}
	}
}

func TestHistoricalSlotMapDisambiguatesCopiedResumeLabels(t *testing.T) {
	run := store.OrchestrationRun{WorkerPair: "codex-codex", CodexThreadIDs: map[string]string{"codex-a": "current-a", "codex-b": "current-b"}}
	events := []store.OrchestrationEvent{{RunEndData: &protocol.RunEndData{
		CodexThreadIDs: map[string]string{"codex-b": "old-b"},
		NativeResume:   []protocol.NativeResumeInfo{{CLI: "codex-a", ID: "old-b"}, {CLI: "codex-b", ID: "old-b"}},
	}}}
	sessions := orchestrationUsageSessions(run, events, nil)
	if len(sessions) != 3 {
		t.Fatal(sessions)
	}
	for _, session := range sessions {
		if session.SessionID == "old-b" && session.WorkerSlot != "codex-b" {
			t.Fatal("historical slot map lost precedence", session)
		}
	}
}

func TestHistoricalSnapshotModelRecoveredOnlyFromMatchingWorkerInterval(t *testing.T) {
	events := []store.OrchestrationEvent{
		{Kind: "turn.start", CLI: "codex", TurnID: "a", CreatedAt: 100, TurnStartData: &protocol.TurnStartData{WorkerSlot: "codex-a"}},
		{Kind: "turn.usage", CLI: "codex", TurnID: "a", CreatedAt: 110, Data: map[string]any{"inputTokens": 100}},
	}
	ledger := []protocol.OrchestrationUsageEvent{{CLI: "codex", WorkerSlot: "codex-a", OccurredAt: 105, Model: "actual"}, {CLI: "codex", WorkerSlot: "codex-b", OccurredAt: 105, Model: "other"}, {CLI: "codex", WorkerSlot: "codex-a", OccurredAt: 99, Model: "old"}}
	out := enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(events, nil), ledger)
	if out[1].Data["model"] != "actual" {
		t.Fatal(out[1].Data)
	}
	ledger = append(ledger, protocol.OrchestrationUsageEvent{CLI: "codex", WorkerSlot: "codex-a", OccurredAt: 106, Model: "switched"})
	out = enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(events, nil), ledger)
	if usageText(out[1].Data, "model") != "" {
		t.Fatal("ambiguous model guessed")
	}
}
