package hub

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func verifierUsageFixture() ([]store.OrchestrationEvent, []store.OrchestrationTaskGraph) {
	payload, _ := json.Marshal(protocol.OrchestrationStartPayload{WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"codex-a": {CLI: "codex", OfficialLogin: true}, "codex-b": {CLI: "codex", OfficialLogin: true},
	}})
	events := []store.OrchestrationEvent{
		{Kind: "turn.usage", CLI: "codex", CreatedAt: 110, Data: map[string]any{"workerSlot": "codex-a", "model": "model-a", "inputTokens": 100, "native": true}},
		{Kind: "turn.usage", CLI: "codex", CreatedAt: 120, Data: map[string]any{"workerSlot": "codex-b", "model": "model-b", "inputTokens": 200, "native": true}},
		{Kind: "verifier.verdict", CLI: "codex", TurnID: "review", CreatedAt: 130, Data: map[string]any{"verifiers": []map[string]any{
			{"agent": "agent-1", "slot": "codex-b", "cli": "codex"}, // Reversed deliberately: no parity inference.
			{"agent": "agent-2", "slot": "codex-a", "cli": "codex"},
		}}},
		{Kind: "turn.usage", CLI: "codex", Role: "verifier", TurnID: "review-agent-1", CreatedAt: 131, Data: map[string]any{"inputTokens": 30, "native": true, "round": 2}},
		{Kind: "turn.usage", CLI: "codex", Role: "verifier", TurnID: "review-agent-2", CreatedAt: 132, Data: map[string]any{"inputTokens": 40, "native": true, "round": 2}},
	}
	return events, []store.OrchestrationTaskGraph{{CreatedAt: 100, PayloadJSON: string(payload)}}
}

func TestVerifierUsesGraphIdentityAcrossSameSecondBoundary(t *testing.T) {
	events, graphs := verifierUsageFixture()
	graphs[0].ID = "original"
	for i := range events {
		events[i].Task = &protocol.TaskAttemptRef{GraphID: "original", WorkerSlot: "codex-b", Round: 2}
	}
	// A continuation graph can be created in the same second as the old verdict.
	graphs = append(graphs, store.OrchestrationTaskGraph{ID: "next", CreatedAt: 130, PayloadJSON: `{"workerProfiles":{"codex-a":{"model":"next-model"},"codex-b":{"model":"next-model"}}}`})
	out := enrichVerifierUsage(enrichOrchestrationUsageTimeline(events, graphs), graphs)
	if out[4].Data["workerSlot"] != "verifier:codex-a" || out[4].Data["model"] != "model-a" || out[3].Data["model"] != "model-b" {
		t.Fatal(out)
	}
}

func TestAllWorkerPairsVerifierBillingReconciles(t *testing.T) {
	for _, pair := range []struct {
		name        string
		slots, clis [2]string
	}{
		{"codex-codex", [2]string{"codex-a", "codex-b"}, [2]string{"codex", "codex"}},
		{"claude-claude", [2]string{"claude-a", "claude-b"}, [2]string{"claude", "claude"}},
		{"claude-codex", [2]string{"claude", "codex"}, [2]string{"claude", "codex"}},
	} {
		t.Run(pair.name, func(t *testing.T) {
			profiles := map[string]protocol.WorkerProfileBinding{}
			events := []store.OrchestrationEvent{}
			agents := []map[string]string{}
			var expected int64
			for i, slot := range pair.slots {
				cli, model := pair.clis[i], "gpt-5.5"
				input, write := 120, 0 // Codex input includes cache hits.
				if cli == "claude" {
					model, input, write = "claude-opus-4-6", 100, 5
				}
				profiles[slot] = protocol.WorkerProfileBinding{CLI: cli, OfficialLogin: true}
				agent := []string{"agent-1", "agent-2"}[i]
				agents = append(agents, map[string]string{"agent": agent, "slot": slot, "cli": cli})
				for _, verifier := range []bool{false, true} {
					e := store.OrchestrationEvent{Kind: "turn.usage", CLI: cli, CreatedAt: 110, Data: map[string]any{"workerSlot": slot, "model": model, "round": 1, "native": true, "inputTokens": input, "cacheReadTokens": 20, "cacheWriteTokens": write, "outputTokens": 10, "costKnown": true, "costSource": "provider", "estimatedCostUsd": 0.25}}
					if verifier {
						e.Role, e.TurnID, e.CreatedAt = "verifier", "review-"+agent, 130
						delete(e.Data, "workerSlot")
						delete(e.Data, "model")
						e.Data["estimatedCostUsd"] = 0.05
					}
					events = append(events, e)
					expected += int64(130 + write)
				}
			}
			events = append(events, store.OrchestrationEvent{Kind: "verifier.verdict", TurnID: "review", CreatedAt: 129, Data: map[string]any{"verifiers": agents}})
			payload, _ := json.Marshal(protocol.OrchestrationStartPayload{WorkerProfiles: profiles})
			graphs := []store.OrchestrationTaskGraph{{CreatedAt: 100, PayloadJSON: string(payload)}}
			out := enrichVerifierUsage(enrichOrchestrationUsageTimeline(events, graphs), graphs)
			// A worker ledger must not erase the independent verifier/provider costs.
			stats := buildOrchestrationRunStatsWithLedger(store.OrchestrationRun{}, out, []protocol.OrchestrationUsageEvent{{CLI: pair.clis[0], InputTokens: 999999}}, []store.OrchestrationUsageSync{{Status: "complete"}})
			if stats.TotalTokens != expected || stats.CallCount != 4 || len(stats.ByWorker) != 4 || !stats.CostKnown || math.Abs(stats.EstimatedCostUSD-0.6) > 1e-9 {
				t.Fatal(stats)
			}
			var tokens int64
			var cost float64
			for _, round := range stats.Rounds {
				tokens += round.TotalTokens
				cost += round.EstimatedCostUSD
				if len(round.ByWorker) != 4 {
					t.Fatal(round)
				}
			}
			if tokens != expected || math.Abs(cost-stats.EstimatedCostUSD) > 1e-9 {
				t.Fatal("round billing mismatch")
			}
			for _, item := range stats.ByWorker {
				if item.Model == "" || item.CostSource != "provider" {
					t.Fatal(item)
				}
			}
		})
	}
}

func TestHistoricalVerifierAssignmentAndModels(t *testing.T) {
	for _, same := range []bool{false, true} {
		events, graphs := verifierUsageFixture()
		if same {
			events[1].Data["model"] = "model-a"
		}
		out := enrichVerifierUsage(enrichOrchestrationUsageTimeline(events, graphs), graphs)
		want := events[1].Data["model"]
		if out[3].Data["workerSlot"] != "verifier:codex-b" || out[3].Data["model"] != want || out[4].Data["model"] != "model-a" || out[3].Data["modelSource"] != "worker-config" {
			t.Fatal(out)
		}
		if !reflect.DeepEqual(out, enrichVerifierUsage(enrichOrchestrationUsageTimeline(out, nil), nil)) {
			t.Fatal("projection is not idempotent")
		}
		if _, exists := events[3].Data["model"]; exists {
			t.Fatal("mutated historical evidence")
		}
		stats := buildOrchestrationRunStatsWithLedger(store.OrchestrationRun{}, out, nil, nil)
		if stats.TotalTokens != 370 || len(stats.ByWorker) != 4 {
			t.Fatal(stats)
		}
		var sum int64
		for _, worker := range stats.ByWorker {
			sum += worker.InputTokens
		}
		if sum != stats.TotalTokens {
			t.Fatal("worker totals changed")
		}
	}
}

func TestVerifierModelEvidenceBoundaries(t *testing.T) {
	for _, test := range []string{"native", "verdict", "binding", "changed-graph", "ambiguous", "missing-assignment", "future-model"} {
		t.Run(test, func(t *testing.T) {
			events, graphs := verifierUsageFixture()
			want := ""
			switch test {
			case "native":
				events[3].Data["model"] = "native-override"
				want = "native-override"
			case "verdict":
				events[2].Data["verifiers"].([]map[string]any)[0]["model"] = "verdict-model"
				want = "verdict-model"
			case "binding":
				graphs[0].PayloadJSON = `{"workerProfiles":{"codex-b":{"cli":"codex","model":"configured"}}}`
				want = "configured"
			case "changed-graph":
				graphs = append(graphs, store.OrchestrationTaskGraph{CreatedAt: 125, PayloadJSON: graphs[0].PayloadJSON})
			case "ambiguous":
				events = append(events, store.OrchestrationEvent{Kind: "turn.usage", CLI: "codex", CreatedAt: 125, Data: map[string]any{"workerSlot": "codex-b", "model": "another-model"}})
			case "missing-assignment":
				events[2].Data = nil
			case "future-model":
				events[1].CreatedAt = 200
			}
			out := enrichVerifierUsage(enrichOrchestrationUsageTimeline(events, graphs), graphs)
			if usageText(out[3].Data, "model") != want {
				t.Fatalf("got %v want %s", out[3].Data, want)
			}
		})
	}
}
