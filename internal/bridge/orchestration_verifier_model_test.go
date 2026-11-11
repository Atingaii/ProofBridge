package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func writeVerifierSession(t *testing.T, home, id, model string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"session_meta","payload":{"id":"` + id + `"}}` + "\n" + `{"type":"turn_context","payload":{"model":"` + model + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "sessions", "rollout-"+id+".jsonl"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexVerifierNativeModelAndFailedUsage(t *testing.T) {
	for _, failed := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		writeVerifierSession(t, home, "verifier-session", "native-default")
		cli := filepath.Join(t.TempDir(), "codex")
		script := `#!/usr/bin/env python3
import json, sys
sys.stdin.read()
print(json.dumps({"type":"thread.started","thread_id":"verifier-session"}))
verdict={"status":"pass","reason":"verified","checks":[{"name":n,"status":"pass","reason":"checked"} for n in ["task","workspace","verification"]]}
print(json.dumps({"type":"item.completed","item":{"type":"agent_message","text":json.dumps(verdict)}}))
print(json.dumps({"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10}}))
`
		if failed {
			script += "sys.exit(1)\n"
		}
		if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Bridge.CodexPath, cfg.Bridge.CWD = cli, t.TempDir()
		m := NewOrchestrationManager(&cfg)
		result := m.runAgentVerifier(context.Background(), protocol.OrchestrationStartPayload{RunID: "model-test", CWD: cfg.Bridge.CWD}, orchestrationTurnAssignment{CLI: "codex", WorkerSlot: "codex-a"}, 1, "verify")
		m.CloseAll()
		if result.Model != "native-default" || result.Usage.Model != "native-default" || !result.Usage.Native || result.Usage.InputTokens != 100 || result.Usage.CacheReadTokens != 20 || result.Usage.OutputTokens != 10 {
			t.Fatalf("lost model/usage: %+v", result)
		}
		if failed != (result.Error != "") {
			t.Fatalf("changed execution result: %+v", result)
		}
	}
}

func TestCodexVerifierModelPriorityAndSessionIsolation(t *testing.T) {
	ambient, isolated := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", ambient)
	writeVerifierSession(t, ambient, "verifier-session", "ambient")
	writeVerifierSession(t, isolated, "verifier-session", "isolated")
	for _, test := range []struct{ home, id, raw, want string }{
		{isolated, "verifier-session", `{"input_tokens":10}`, "isolated"},
		{"", "verifier-session", `{"input_tokens":10}`, "ambient"},
		{isolated, "verifier-session", `{"input_tokens":10,"model":"reported"}`, "reported"},
		{isolated, "missing-session", `{"input_tokens":10}`, ""},
		{isolated, "../verifier-session", `{"input_tokens":10}`, ""},
	} {
		output := silentVerifierOutput{ThreadID: test.id, Usage: json.RawMessage(test.raw)}
		resolveCodexVerifierModel(&output, test.home)
		if got := appServerResolvedModel(output.Usage); got != test.want {
			t.Fatalf("got %q want %q", got, test.want)
		}
	}
	raw := []byte("{\"type\":\"thread.started\",\"thread_id\":\"verifier-session\",\"model\":\"stream-model\"}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":10}}\n{\"type\":\"error\",\"message\":\"failed\"}\n")
	output, err := scanSilentCodexVerifierOutput(raw)
	if err == nil || output.ThreadID != "verifier-session" || appServerResolvedModel(output.Usage) != "stream-model" {
		t.Fatal(output, err)
	}
}

func TestClaudeVerifierRetainsNativeModelsAndProviderCosts(t *testing.T) {
	for _, multi := range []bool{false, true} {
		cli := filepath.Join(t.TempDir(), "claude")
		script := `#!/usr/bin/env python3
import json
verdict={"status":"pass","reason":"verified","checks":[{"name":n,"status":"pass","reason":"checked"} for n in ["task","workspace","verification"]]}
models={"claude-opus-4-6":{"inputTokens":100,"outputTokens":10,"cacheReadInputTokens":20,"cacheCreationInputTokens":5,"costUSD":0.25}}
`
		if multi {
			script += "models['claude-sonnet-4-6']={'inputTokens':50,'outputTokens':5,'costUSD':0.05}\n"
		}
		script += "print(json.dumps({'type':'result','result':json.dumps(verdict),'modelUsage':models,'total_cost_usd':sum(m['costUSD'] for m in models.values())}))\n"
		if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Bridge.ClaudePath, cfg.Bridge.CWD = cli, t.TempDir()
		m := NewOrchestrationManager(&cfg)
		result := m.runAgentVerifier(context.Background(), protocol.OrchestrationStartPayload{RunID: "claude-model", CWD: cfg.Bridge.CWD}, orchestrationTurnAssignment{CLI: "claude", WorkerSlot: "claude-b"}, 2, "verify")
		m.CloseAll()
		parts := usageModelParts(result.Usage)
		want := 1
		if multi {
			want = 2
		}
		if result.Status != "pass" || len(parts) != want || !result.Usage.Native || !result.Usage.CostKnown {
			t.Fatal(result)
		}
		if !multi && (result.Model != "claude-opus-4-6" || result.Usage.InputTokens != 100 || result.Usage.CacheReadTokens != 20 || result.Usage.CacheWriteTokens != 5 || result.Usage.EstimatedCostUSD != 0.25) {
			t.Fatal(result)
		}
		for _, part := range parts {
			if part.Model == "" || part.CostSource != "provider" {
				t.Fatal(parts)
			}
		}
	}
}

func TestResumedCodexModelUsesLatestNativeContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeVerifierSession(t, home, "resumed-session", "first-model")
	path := filepath.Join(home, "sessions", "rollout-resumed-session.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("{\"type\":\"turn_context\",\"payload\":{\"model\":\"latest-model\"}}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	output := silentVerifierOutput{ThreadID: "resumed-session", Usage: json.RawMessage(`{"input_tokens":10}`)}
	resolveCodexVerifierModel(&output, home)
	if got := appServerResolvedModel(output.Usage); got != "latest-model" {
		t.Fatal(got)
	}
}

func TestCodexModelBeforeLargeCommandOutput(t *testing.T) {
	home := t.TempDir()
	writeVerifierSession(t, home, "large-session", "recorded-model")
	file, err := os.OpenFile(filepath.Join(home, "sessions", "rollout-large-session.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"output": string(make([]byte, 1<<20))}})
	if _, err = file.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	file.Close()
	output := silentVerifierOutput{ThreadID: "large-session", Usage: json.RawMessage(`{"input_tokens":10}`)}
	resolveCodexVerifierModel(&output, home)
	if got := appServerResolvedModel(output.Usage); got != "recorded-model" {
		t.Fatalf("head model lost behind >4 MiB output: %q", got)
	}
}
