package bridge

import (
	"encoding/json"
	"testing"
)

func TestEstimateOrchestrationUsageMarksMissingNativeData(t *testing.T) {
	usage := estimateOrchestrationUsage("claude-sonnet-4", "12345678", "12345678")
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || !usage.Estimated || usage.CostKnown || usage.CostSource != "unavailable" {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestNormalizeNativeUsageCodex(t *testing.T) {
	usage, ok := normalizeNativeUsage("codex", "gpt-5.6-sol", json.RawMessage(`{"last":{"inputTokens":100,"cachedInputTokens":40,"cacheWriteInputTokens":2,"outputTokens":20}}`))
	if !ok || !usage.Native || usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.CacheReadTokens != 40 || usage.CacheWriteTokens != 2 || !usage.CostKnown || usage.PricingModel != "gpt-5.6-sol" || usage.CostSource != "official-catalog" {
		t.Fatalf("usage = %#v ok=%v", usage, ok)
	}
}

func TestNormalizeNativeUsageCodexDefaultKeepsTokensWithoutGuessingPrice(t *testing.T) {
	usage, ok := normalizeNativeUsage("codex", "default", json.RawMessage(`{"last":{"inputTokens":23858,"outputTokens":1605}}`))
	if !ok || usage.CostKnown || usage.PricingModel != "" || usage.InputTokens != 23858 || usage.OutputTokens != 1605 {
		t.Fatalf("usage = %#v ok=%v", usage, ok)
	}
}

func TestNormalizeNativeUsageClaudeProviderCost(t *testing.T) {
	usage, ok := normalizeNativeUsage("claude", "", json.RawMessage(`{"usage":{"input_tokens":100,"output_tokens":20},"modelUsage":{"claude-sonnet-4":{"costUSD":0.123}}}`))
	if !ok || usage.Model != "claude-sonnet-4" || usage.EstimatedCostUSD != 0.123 || usage.CostSource != "provider" {
		t.Fatalf("usage = %#v ok=%v", usage, ok)
	}
}

func TestMergeOrchestrationTurnAttemptsAggregatesUsage(t *testing.T) {
	first := orchestrationTurn{TurnID: "t", Usage: orchestrationUsage{Model: "m", InputTokens: 3, OutputTokens: 4, EstimatedCostUSD: .1, Estimated: true, CostKnown: true, CostSource: "provider"}}
	next := orchestrationTurn{TurnID: "t", Usage: orchestrationUsage{InputTokens: 5, OutputTokens: 6, CacheReadTokens: 2, EstimatedCostUSD: .2, CostKnown: false, CostSource: "unavailable"}}
	got := mergeOrchestrationTurnAttempts(first, next)
	if got.Usage.InputTokens != 8 || got.Usage.OutputTokens != 10 || got.Usage.CacheReadTokens != 2 || got.Usage.EstimatedCostUSD < .299 || got.Usage.EstimatedCostUSD > .301 || got.Usage.CostKnown || got.Usage.CostSource != "mixed" {
		t.Fatalf("usage = %#v", got.Usage)
	}
}

func TestMergeOrchestrationTurnAttemptsUsesLatestStructuredHandoff(t *testing.T) {
	first := orchestrationTurn{TurnID: "t", Err: "stream disconnected", Relay: orchestrationRelayPacket{}}
	next := newOrchestrationTurnRecord(
		"t", "reviewer", "codex",
		"最终结论：通过。\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Main.v; verified=coqc Main.v; next=none; risks=none",
		nil,
	)
	got := mergeOrchestrationTurnAttempts(first, next)
	if !got.Relay.Structured || got.Relay.Status != "resolved" || got.Relay.To != "user" || got.Relay.Intent != "final" {
		t.Fatalf("latest structured handoff was not retained: %#v", got.Relay)
	}
}

func TestNativeUsageModelBreakdownAndAliases(t *testing.T) {
	usage, ok := normalizeNativeUsage("claude", "default", json.RawMessage(`{"usage":{"input_tokens":9999},"total_cost_usd":0.3,"modelUsage":{"claude-opus-4-6":{"inputTokens":100,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":5,"costUSD":0.2},"claude-haiku-4-5":{"inputTokens":40,"outputTokens":10,"costUSD":0.1}}}`))
	if !ok || len(usage.ByModel) != 2 || usage.Model != "mixed" || usage.InputTokens != 140 || usage.OutputTokens != 30 || usage.CacheReadTokens != 30 || usage.CacheWriteTokens != 5 || !usage.CostKnown || usage.EstimatedCostUSD < .29999 || usage.EstimatedCostUSD > .30001 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.ByModel[0].Model != "claude-haiku-4-5" || usage.ByModel[1].Model != "claude-opus-4-6" {
		t.Fatalf("model identity lost: %#v", usage.ByModel)
	}
}

func TestNativeUsageCompatibilityAndUnknownModels(t *testing.T) {
	for _, raw := range []string{
		`{"model":"private-model","usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}},"total_cost_usd":0.4}`,
		`{"response":{"model":"private-model","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":30}},"costUSD":0.4}}`,
	} {
		usage, ok := normalizeNativeUsage("codex", "default", json.RawMessage(raw))
		if !ok || usage.Model != "private-model" || usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.CacheReadTokens != 30 || usage.EstimatedCostUSD != .4 || !usage.CostKnown {
			t.Fatalf("usage = %#v", usage)
		}
	}
	usage, ok := normalizeNativeUsage("claude", "unlisted", json.RawMessage(`{"usage":{"input_tokens":100,"output_tokens":20}}`))
	if !ok || !usage.Native || usage.InputTokens != 100 || usage.CostKnown {
		t.Fatalf("unknown model lost tokens or gained a price: %#v", usage)
	}
}

func TestClaudeUsageFallbackDeduplicatesAndResolvesActualModel(t *testing.T) {
	var observations claudeUsageObservations
	for _, tokens := range []float64{4, 8, 4} {
		observations.observe(map[string]any{"message": map[string]any{"id": "msg_1", "model": "private-model", "usage": map[string]any{"input_tokens": float64(20), "output_tokens": tokens}}})
	}
	usage, ok := normalizeNativeUsage("claude", "default", observations.payload(map[string]any{"total_cost_usd": .3}))
	if !ok || usage.Model != "private-model" || usage.InputTokens != 20 || usage.OutputTokens != 8 || usage.EstimatedCostUSD != .3 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestMultiModelTotalWithoutCostSplitIsNotGuessed(t *testing.T) {
	usage, ok := normalizeNativeUsage("claude", "default", json.RawMessage(`{"total_cost_usd":0.7,"modelUsage":{"private-a":{"inputTokens":100},"private-b":{"inputTokens":200}}}`))
	if !ok || usage.InputTokens != 300 || usage.EstimatedCostUSD != .7 || len(usage.ByModel) != 3 || usage.ByModel[2].Model != "unattributed" {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestNativeUsageKeepsAggregateTokensWithoutModelSplit(t *testing.T) {
	usage, ok := normalizeNativeUsage("claude", "default", json.RawMessage(`{"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50},"modelUsage":{"claude-sonnet-4.6":{"costUSD":0.1},"claude-opus-4.6":{"costUSD":0.2}}}`))
	if !ok || usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.CacheReadTokens != 50 || len(usage.ByModel) != 3 || usage.EstimatedCostUSD < .299 || usage.EstimatedCostUSD > .301 {
		t.Fatalf("aggregate usage lost or duplicated: %#v", usage)
	}
	if usage.ByModel[2].Model != "unattributed" {
		t.Fatalf("guessed model: %#v", usage.ByModel)
	}
}

func TestSilentClaudeErrorPreservesUsage(t *testing.T) {
	output, err := scanSilentClaudeVerifierOutput([]byte(`{"type":"assistant","message":{"id":"m1","model":"actual-model","usage":{"input_tokens":12,"output_tokens":4}}}
{"type":"error","message":"provider failed"}
`))
	usage, ok := normalizeNativeUsage("claude", "default", output.Usage)
	if err == nil || !ok || usage.InputTokens != 12 || usage.OutputTokens != 4 || usage.Model != "actual-model" {
		t.Fatalf("error usage lost: %#v %v", usage, err)
	}
}
