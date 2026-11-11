package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestConfiguredReviewerRoundCompletion(t *testing.T) {
	for _, test := range []struct {
		name                 string
		round, limit         int
		fail, cancel         bool
		wantKind, wantReason string
	}{
		{"final", 12, 12, false, false, "run.end", "verified-early"},
		{"single", 1, 1, false, false, "run.end", "verified-early"},
		{"intermediate", 1, 2, false, false, "run.end", "verified-early"},
		{"final incomplete", 12, 12, false, false, "run.end", "max-turns"},
		{"worker error", 1, 1, true, false, "run.error", ""},
		{"canceled", 1, 1, false, true, "run.cancelled", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "codex")
			content := "最终结论：证明已完成。\n[PLAN_ITEM id=\"P1\" status=\"pending\"] old plan\nHandoff: status=resolved; changed=Main.v; verified=coqc Main.v; next=none; risks=none"
			script := fakeCodexExecScript(content)
			if test.wantReason == "max-turns" {
				script = strings.ReplaceAll(script, `"status":"pass"`, `"status":"continue"`)
			}
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Bridge.CodexPath, cfg.Bridge.CWD = path, dir
			if test.fail {
				cfg.Bridge.CodexPath = path + ".missing"
			}
			manager := NewOrchestrationManager(&cfg)
			defer manager.CloseAll()
			out := make(chan protocol.Envelope, 128)
			manager.AttachOut(out)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if test.cancel {
				cancel()
			}
			manager.run(ctx, protocol.OrchestrationStartPayload{
				RunID: "round-test", Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex,
				Prompt: "Finish proof", CWD: dir, MaxTurns: 1,
				TaskGraph: &protocol.TaskGraphPayload{Round: test.round, MaxRounds: test.limit, Tasks: []protocol.TaskPayload{{Role: store.TaskRoleReviewer, WorkerSlot: orchestrationCodexSlotA}}},
			})
			var terminal protocol.OrchestrationEventPayload
			verdicts := 0
			for len(out) > 0 {
				env := <-out
				if env.Type != protocol.TypeOrchestrationEvent {
					continue
				}
				event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind == "verifier.verdict" {
					verdicts++
				}
				if event.Kind == "run.end" || event.Kind == "run.error" || event.Kind == "run.cancelled" {
					terminal = event
				}
			}
			if terminal.Kind != test.wantKind {
				t.Fatalf("terminal = %#v", terminal)
			}
			if test.wantReason != "" && (terminal.RunEndData == nil || terminal.RunEndData.TerminalReason != test.wantReason) {
				t.Fatalf("reason = %#v", terminal.RunEndData)
			}
			if test.wantReason == "max-turns" && (verdicts == 0 || terminal.RunConclusion == nil || terminal.RunConclusion.Outcome != "blocked") {
				t.Fatalf("unverified final round claimed completion: %#v", terminal)
			}
			if test.wantReason == "verified-early" && verdicts == 0 {
				t.Fatal("completion verifier was skipped")
			}
		})
	}
}
