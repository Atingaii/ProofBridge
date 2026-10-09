package bridge

import (
	"context"
	"errors"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanceledCodexExecKeepsNativeModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeVerifierSession(t, home, "planning-session", "actual-planner-model")
	cli := filepath.Join(t.TempDir(), "codex")
	script := `#!/usr/bin/env python3
import json,sys,time
sys.stdin.read()
print(json.dumps({"type":"thread.started","thread_id":"planning-session"}),flush=True)
print(json.dumps({"type":"turn.completed","usage":{"input_tokens":198150,"cached_input_tokens":145920,"output_tokens":2709}}),flush=True)
print(json.dumps({"type":"item.completed","item":{"type":"agent_message","text":"cancel-ready"}}),flush=True)
time.sleep(30)
`
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = cli
	cfg.Bridge.CWD = t.TempDir()
	m := NewOrchestrationManager(&cfg)
	defer m.CloseAll()
	out := make(chan protocol.Envelope, 32)
	m.AttachOut(out)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan codexExecAttempt, 1)
	go func() {
		done <- m.runCodexExecAttempt(ctx, protocol.OrchestrationStartPayload{RunID: "plan", CWD: cfg.Bridge.CWD}, "plan-turn", "planner", "codex-a", "plan", "")
	}()
	early := false
	for {
		select {
		case env := <-out:
			e, _ := protocol.Decode[protocol.OrchestrationEventPayload](env)
			if e.Kind == "bridge.note" && e.Data["nativeSessionId"] == "planning-session" {
				early = true
			}
			if strings.Contains(e.Content, "cancel-ready") {
				cancel()
			}
		case result := <-done:
			if !early || !errors.Is(result.err, context.Canceled) || appServerResolvedModel(result.usage) != "actual-planner-model" || result.threadID != "planning-session" {
				t.Fatalf("early=%v result=%+v", early, result)
			}
			return
		case <-time.After(6 * time.Second):
			t.Fatal("canceled process did not stop")
		}
	}
}

func TestAppServerIdentityPrecedesCanceledTurn(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "codex")
	script := strings.ReplaceAll(fakeCodexAppServerScript(), `"thread": {"id": "thr_app"}`, `"model": "resolved-default", "thread": {"id": "thr_app"}`)
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = cli
	cfg.Bridge.CWD = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var id, model string
	result, err := NewCodexAppServerRunner(&cfg).Prompt(ctx, RunnerRequest{Content: "plan", OnNativeIdentity: func(threadID, resolved string) { id, model = threadID, resolved; cancel() }}, nil)
	if !errors.Is(err, context.Canceled) || id != "thr_app" || model != "resolved-default" || result.RemoteThreadID != id {
		t.Fatalf("identity=%s/%s result=%+v err=%v", id, model, result, err)
	}
}
