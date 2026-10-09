package bridge

import (
	"encoding/json"
	"testing"
)

func TestResolvedNativeModelWithNoConfiguredOverride(t *testing.T) {
	model := appServerResolvedModel(json.RawMessage(`{"model":"gpt-future","thread":{"id":"native-thread"}}`))
	usage, ok := normalizeNativeUsage("codex", "", usageWithResolvedModel(json.RawMessage(`{"input_tokens":100,"output_tokens":10}`), model))
	if !ok || usage.Model != "gpt-future" || usage.InputTokens != 100 {
		t.Fatalf("lost resolved model: %+v", usage)
	}
	usage, ok = normalizeNativeUsage("codex", "", usageWithResolvedModel(json.RawMessage(`{"model":"routed-model","input_tokens":100}`), model))
	if !ok || usage.Model != "routed-model" {
		t.Fatal(usage)
	}
}
