package hub

import (
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestSnapshotUsageMatchesLedgerAndOverview(t *testing.T) {
	run := store.OrchestrationRun{ID: "usage", CreatedAt: 100, FinishedAt: 110, Status: store.OrchestrationCompleted}
	timeline := []store.OrchestrationEvent{
		{Kind: "turn.usage", CLI: "codex", CreatedAt: 101, Data: map[string]any{"model": "gpt-5.6-sol", "inputTokens": 100, "cacheReadTokens": 80, "outputTokens": 5, "native": true}},
		{Kind: "turn.usage", CLI: "claude", CreatedAt: 102, Data: map[string]any{"model": "claude-sonnet-4", "inputTokens": 20, "cacheReadTokens": 80, "outputTokens": 5, "native": true}},
	}
	snapshot := buildOrchestrationRunStatsWithLedger(run, timeline, nil, []store.OrchestrationUsageSync{{Status: "unavailable"}})
	ledger := buildOrchestrationRunStatsWithLedger(run, nil, []protocol.OrchestrationUsageEvent{
		{CLI: "codex", Model: "gpt-5.6-sol", InputTokens: 20, CacheReadTokens: 80, OutputTokens: 5},
		{CLI: "claude", Model: "claude-sonnet-4", InputTokens: 20, CacheReadTokens: 80, OutputTokens: 5},
	}, []store.OrchestrationUsageSync{{Status: "complete"}})
	if snapshot.InputTokens != 40 || snapshot.TotalTokens != 210 || snapshot.CallCount != 2 || snapshot.TotalTokens != ledger.TotalTokens || snapshot.EstimatedCostUSD != ledger.EstimatedCostUSD {
		t.Fatalf("snapshot = %#v, ledger = %#v", snapshot, ledger)
	}
	trend := map[string]*orchestrationUsageTrendPoint{}
	appendOverviewTrend(trend, run, timeline, nil, nil, 0)
	for _, point := range trend {
		if point.TotalTokens != snapshot.TotalTokens || point.InputTokens != snapshot.InputTokens || point.CallCount != snapshot.CallCount {
			t.Fatalf("trend = %#v, snapshot = %#v", point, snapshot)
		}
	}
	for _, cli := range snapshot.ByCLI {
		if cli.CallCount != 1 {
			t.Fatalf("CLI calls = %#v", cli)
		}
	}
}

func TestSnapshotUsageSeparatesModelsAndClampsCacheOverlap(t *testing.T) {
	events := []store.OrchestrationEvent{
		{Kind: "turn.usage", CLI: "codex", Data: map[string]any{"model": "a", "inputTokens": 10, "cacheReadTokens": 20}},
		{Kind: "turn.usage", CLI: "codex", Data: map[string]any{"model": "b", "inputTokens": 30}},
	}
	stats := buildOrchestrationRunStats(store.OrchestrationRun{}, events)
	if stats.InputTokens != 30 || stats.TotalTokens != 50 || len(stats.ByCLI) != 2 {
		t.Fatalf("stats = %#v", stats)
	}
}
