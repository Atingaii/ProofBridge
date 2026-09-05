package bridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestAppServerTurnUsageCountsResponsesWithoutPriorTurns(t *testing.T) {
	client := &appServerClient{events: make(chan appServerMessage, 12)}
	push := func(method, turn string, fields map[string]any) {
		fields["threadId"], fields["turnId"] = "thread-1", turn
		client.events <- appServerMessage{Method: method, Params: fields}
	}
	usage := func(input, cache, output int64) map[string]any {
		return map[string]any{"inputTokens": input, "cachedInputTokens": cache, "outputTokens": output}
	}
	update := map[string]any{"tokenUsage": map[string]any{"total": usage(1100, 880, 110), "last": usage(100, 80, 10)}}
	push("thread/tokenUsage/updated", "old-turn", update)
	push("thread/tokenUsage/updated", "turn-1", update)
	push("thread/tokenUsage/updated", "turn-1", update)
	push("thread/tokenUsage/updated", "turn-1", map[string]any{"tokenUsage": map[string]any{"total": usage(1300, 1030, 130), "last": usage(200, 150, 20)}})
	// A raw last-response notification must not overwrite accumulated usage.
	push("raw/responseCompleted", "turn-1", map[string]any{"usage": usage(200, 150, 20)})
	push("item/agentMessage/delta", "turn-1", map[string]any{"delta": "done"})
	push("turn/completed", "turn-1", map[string]any{"turn": map[string]any{"id": "turn-1", "status": "completed"}})
	scope := newAppServerTurnScope("thread-1")
	scope.setTurnID("turn-1")
	done := make(chan appServerTurnResult, 1)
	cfg := config.Default()
	NewCodexAppServerRunner(&cfg).readEvents(context.Background(), client, RunnerRequest{}, scope, func(RunnerUpdate) {}, done)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	got, ok := normalizeNativeUsage("codex", "gpt-5.6-sol", result.result.Usage)
	if !ok || got.InputTokens != 300 || got.CacheReadTokens != 230 || got.OutputTokens != 30 {
		t.Fatalf("usage = %#v, raw=%s", got, result.result.Usage)
	}
}

func TestAppServerUsageResetAndMissingTotals(t *testing.T) {
	var accumulator appServerUsageAccumulator
	var last json.RawMessage
	for _, raw := range []string{
		`{"total":{"inputTokens":1000},"last":{"inputTokens":100}}`,
		`{"total":{"inputTokens":20},"last":{"inputTokens":20}}`,
		`{"last":{"inputTokens":30}}`,
	} {
		var payload any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatal(err)
		}
		last = accumulator.observe(payload)
	}
	got, ok := normalizeNativeUsage("codex", "unknown", last)
	if !ok || got.InputTokens != 150 {
		t.Fatalf("usage = %#v", got)
	}
}

func TestNormalizeNativeUsageSnakeCaseCodexCache(t *testing.T) {
	got, ok := normalizeNativeUsage("codex", "unknown", json.RawMessage(`{"input_tokens":100,"cached_input_tokens":80,"cache_write_input_tokens":5,"output_tokens":10}`))
	if !ok || got.InputTokens != 100 || got.CacheReadTokens != 80 || got.CacheWriteTokens != 5 || got.OutputTokens != 10 {
		t.Fatalf("usage = %#v", got)
	}
}
