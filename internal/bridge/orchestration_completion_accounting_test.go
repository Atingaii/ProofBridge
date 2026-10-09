package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestVerifierUsesWholeOriginalTaskWithoutNodeInstructions(t *testing.T) {
	task := strings.Repeat("requirement ", 1000) + "FINAL_REQUIREMENT"
	prompt := composeAgentVerifierPrompt(protocol.OrchestrationStartPayload{TaskPrompt: task, Prompt: task + "\nDurable task node:\nonly final configured round may finish", Context: "untrusted worker evidence"})
	if !strings.Contains(prompt, task) || strings.Contains(prompt, "Durable task node:") || strings.Contains(prompt, "untrusted worker evidence") {
		t.Fatal("verifier task input was truncated or polluted")
	}
	if !strings.Contains(composeAgentVerifierPrompt(protocol.OrchestrationStartPayload{Prompt: "legacy task"}), "legacy task") {
		t.Fatal("legacy task fallback lost")
	}
}

func TestIntermediateCompletionAndVerifierUsageBothModes(t *testing.T) {
	for _, mode := range []string{"collaboration", "debate"} {
		for _, verdict := range []string{"pass", "continue", "invalid"} {
			t.Run(mode+"/"+verdict, func(t *testing.T) {
				tmp := t.TempDir()
				script := fakeCodexExecScript("交接总结：等待验证。\nMsg: to=peer; intent=continue; need=review\nHandoff: status=needs_next; changed=none; verified=none; next=another review; risks=none")
				script = strings.Replace(script, "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:", `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    assert "Durable task node:" not in prompt
    assert "original completion target" in prompt
    print(json.dumps({"type":"turn.completed","usage":{"input_tokens":15,"output_tokens":2}}), flush=True)`, 1)
				if verdict == "continue" {
					script = strings.ReplaceAll(script, `"status":"pass"`, `"status":"continue"`)
				}
				if verdict == "invalid" {
					script = strings.Replace(script, `json.dumps(verdict, separators=(",", ":"))`, `"invalid verdict"`, 1)
				}
				cli := filepath.Join(tmp, "codex")
				if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				cfg := config.Default()
				cfg.Bridge.CodexPath = cli
				cfg.Bridge.CWD = tmp
				cfg.Bridge.Model = "gpt-5.6-sol"
				manager := NewOrchestrationManager(&cfg)
				defer manager.CloseAll()
				out := make(chan protocol.Envelope, 128)
				manager.AttachOut(out)
				manager.run(context.Background(), protocol.OrchestrationStartPayload{RunID: "early", Mode: mode, WorkerPair: protocol.WorkerPairCodexCodex, Prompt: "original completion target\n\nDurable task node:\nonly final configured round may finish", TaskPrompt: "original completion target", MaxTurns: 1, CWD: tmp, TaskGraph: &protocol.TaskGraphPayload{ID: "graph", Round: 1, MaxRounds: 4, Tasks: []protocol.TaskPayload{{Name: "review", Role: store.TaskRoleReviewer, WorkerSlot: orchestrationCodexSlotB}}}})
				events := drainOrchestrationEvents(t, out)
				var end protocol.OrchestrationEventPayload
				verifierTokens := int64(0)
				verifiers := 0
				verifierSlots := map[string]bool{}
				for _, event := range events {
					if event.Kind == "run.end" {
						end = event
					}
					if event.Kind == "run.error" {
						t.Fatalf("unexpected run error: %#v", event)
					}
					if event.Kind == "turn.usage" && event.Role == "verifier" {
						verifiers++
						verifierSlots[event.Data["workerSlot"].(string)] = true
						verifierTokens += int64(event.Data["inputTokens"].(float64))
					}
				}
				if end.RunEndData == nil || (end.RunEndData.TerminalReason == "verified-early") != (verdict == "pass") {
					t.Fatalf("terminal = %#v", end)
				}
				if verdict == "pass" && (end.RunConclusion == nil || end.RunConclusion.Outcome != "satisfied" || len(end.RunConclusion.UnmetObligations) > 0) {
					t.Fatalf("passed verdict displayed blocked: %#v", end)
				}
				if verifiers != 2 || verifierTokens != 30 || !verifierSlots["codex-a"] || !verifierSlots["codex-b"] {
					t.Fatalf("lost verifier usage: %d events %d tokens", verifiers, verifierTokens)
				}
			})
		}
	}
}

func TestCanceledVerifierCannotCompleteRun(t *testing.T) {
	tmp := t.TempDir()
	script := strings.Replace(fakeCodexExecScript("交接总结：等待审查。"), "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:", "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:\n    import time\n    time.sleep(10)", 1)
	cli := filepath.Join(tmp, "codex")
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = cli
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	manager.run(ctx, protocol.OrchestrationStartPayload{RunID: "cancel-verification", WorkerPair: protocol.WorkerPairCodexCodex, Prompt: "review", MaxTurns: 1, CWD: tmp, TaskGraph: &protocol.TaskGraphPayload{ID: "graph", Round: 1, MaxRounds: 4, Tasks: []protocol.TaskPayload{{Name: "review", Role: store.TaskRoleReviewer, WorkerSlot: orchestrationCodexSlotB}}}})
	canceled := false
	for _, event := range drainOrchestrationEvents(t, out) {
		if event.Kind == "run.end" {
			t.Fatal("canceled verifier completed run")
		}
		if event.Kind == "run.cancelled" {
			canceled = true
		}
	}
	if !canceled {
		t.Fatal("missing cancellation")
	}
}

func TestTurnUsageEmitsDisjointModelPartsThroughRetries(t *testing.T) {
	cfg := config.Default()
	manager := NewOrchestrationManager(&cfg)
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	first, _ := normalizeNativeUsage("claude", "default", json.RawMessage(`{"modelUsage":{"model-a":{"inputTokens":100,"costUSD":0.1},"model-b":{"inputTokens":200,"costUSD":0.2}}}`))
	second, _ := normalizeNativeUsage("claude", "model-a", json.RawMessage(`{"usage":{"input_tokens":50},"total_cost_usd":0.05}`))
	record := mergeOrchestrationTurnAttempts(orchestrationTurn{TurnID: "turn", CLI: "claude", Usage: first}, orchestrationTurn{TurnID: "turn", CLI: "claude", Usage: second})
	manager.emitTurnUsage("usage", record)
	total := 0.0
	byModel := map[string]float64{}
	for _, event := range drainOrchestrationEvents(t, out) {
		byModel[event.Data["model"].(string)] += event.Data["inputTokens"].(float64)
		total += event.Data["estimatedCostUsd"].(float64)
	}
	if len(byModel) != 2 || byModel["model-a"] != 150 || byModel["model-b"] != 200 || total < .34999 || total > .35001 {
		t.Fatalf("model totals=%#v cost=%f", byModel, total)
	}
}
