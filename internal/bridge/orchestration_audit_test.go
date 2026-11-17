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

func auditTestPayload(cwd string) protocol.OrchestrationStartPayload {
	return protocol.OrchestrationStartPayload{RunID: "audit-test", Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex, TaskPrompt: "verify every original target", Prompt: "verify every original target", CWD: cwd, MaxTurns: 1, TaskGraph: &protocol.TaskGraphPayload{ID: "graph", Round: 1, MaxRounds: 4, Tasks: []protocol.TaskPayload{{Name: "review", Role: store.TaskRoleReviewer, WorkerSlot: orchestrationCodexSlotB}}}}
}

func auditTestManager(t *testing.T, script string) (*OrchestrationManager, string, chan protocol.Envelope) {
	t.Helper()
	cwd := t.TempDir()
	cli := filepath.Join(cwd, "codex")
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = cli
	cfg.Bridge.CWD = cwd
	m := NewOrchestrationManager(&cfg)
	t.Cleanup(m.CloseAll)
	out := make(chan protocol.Envelope, 256)
	m.AttachOut(out)
	return m, cwd, out
}

func TestAuditDeadlineRetriesSameVerifierAndPreservesUsage(t *testing.T) {
	script := strings.Replace(fakeCodexExecScript("review"), `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:`, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    import pathlib,time
    counter=pathlib.Path("attempt-count")
    n=int(counter.read_text())+1 if counter.exists() else 1
    counter.write_text(str(n))
    print(json.dumps({"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}),flush=True)
    if n==1:
        time.sleep(5)`, 1)
	m, cwd, _ := auditTestManager(t, script)
	m.verifierTimeouts = []time.Duration{250 * time.Millisecond, 3 * time.Second}
	assignment := orchestrationTurnAssignment{CLI: "codex", WorkerSlot: orchestrationCodexSlotA}
	r := m.runAgentVerifier(context.Background(), auditTestPayload(cwd), assignment, 1, "PROOFBRIDGE_AGENT_VERIFIER_V1")
	if r.Status != verifierVerdictPass || r.Attempts != 2 || r.TimedOut || r.Usage.InputTokens != 20 {
		t.Fatalf("retry result: %#v", r)
	}
}

func TestReportedAuditTimeoutNeverStartsNextWorkerRound(t *testing.T) {
	script := strings.Replace(fakeCodexExecScript("交接总结：等待审核。"), `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:`, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    print(json.dumps({"type":"item.agent_message.delta","delta":json.dumps({"status":"continue","reason":"audit timeout after 120s","checks":[{"name":n,"status":"continue","reason":"audit timeout after 120s","code":"environment-unavailable"} for n in ["task","workspace","verification"]]})}),flush=True)
    raise SystemExit(0)`, 1)
	m, cwd, out := auditTestManager(t, script)
	m.verifierTimeouts = []time.Duration{time.Second, 2 * time.Second}
	m.run(context.Background(), auditTestPayload(cwd))
	starts, retries, failures := 0, 0, 0
	for _, e := range drainOrchestrationEvents(t, out) {
		if e.Kind == "turn.start" {
			starts++
		}
		if e.BridgeNoteData != nil && e.BridgeNoteData.Category == "verification-timeout-retry" {
			retries++
		}
		if e.Kind == "run.error" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verification-timeout" {
			failures++
		}
		if e.Kind == "run.end" {
			t.Fatalf("timeout became a round completion: %#v", e)
		}
	}
	if starts != 1 || retries != 2 || failures != 1 {
		t.Fatalf("starts=%d retries=%d failures=%d", starts, retries, failures)
	}
}

func TestIndependentReviewerFinalAuditSkipsExtraModelCalls(t *testing.T) {
	text := "交接总结：原始任务全部满足。\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=make final-audit; next=none; risks=none"
	script := fakeCodexExecScriptWithSuccessfulCommand(text)
	script = strings.ReplaceAll(script, "go test ./...", "make -j1 final-audit")
	script = strings.ReplaceAll(script, `"status":"completed"}}}`, `"status":"completed","aggregatedOutput":"FINAL AUDIT PASSED (--submission).\n"}}}`)
	script = strings.Replace(script, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:`, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    raise RuntimeError("duplicate verifier must never start")`, 1)
	m, cwd, out := auditTestManager(t, script)
	m.run(context.Background(), auditTestPayload(cwd))
	passed := false
	for _, e := range drainOrchestrationEvents(t, out) {
		if e.Kind == "turn.usage" && e.Role == "verifier" {
			t.Fatal("duplicate model audit ran")
		}
		if e.Kind == "run.end" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verified-early" {
			passed = true
		}
	}
	if !passed {
		t.Fatal("independent final audit failed to close the task")
	}
}

func TestFinalAuditRejectsUnboundOrSupersededEvidence(t *testing.T) {
	zero, one := 0, 1
	base := orchestrationTurn{Role: store.TaskRoleReviewer, Relay: orchestrationRelayPacket{Structured: true, Status: "resolved", To: "user", Intent: "final", Next: "none", Risks: "none"}, Tools: []RunnerToolEvent{{ID: "audit", Status: "completed", Command: "timeout --kill-after=10s 24h make -j1 final-audit", ExitCode: &zero, Output: "FINAL AUDIT PASSED (--submission).\n"}}}
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "Proof.v"), []byte("proof"), 0600); err != nil {
		t.Fatal(err)
	}
	base.Tools[0].CompletedAt = time.Now().Add(time.Second)
	p := auditTestPayload(cwd)
	if _, ok := reviewerFinalAuditVerdict(context.Background(), p, base); !ok {
		t.Fatal("valid audit was rejected")
	}
	for _, name := range []string{"echo", "old-log", "failed", "later-edit", "running", "partial", "worker", "unresolved", "source-changed"} {
		t.Run(name, func(t *testing.T) {
			r := base
			r.Tools = append([]RunnerToolEvent(nil), base.Tools...)
			switch name {
			case "echo":
				r.Tools[0].Command = `echo 'FINAL AUDIT PASSED'`
			case "old-log":
				r.Tools[0].Command = "cat ReviewerFinalAudit.log"
			case "failed":
				r.Tools[0].ExitCode = &one
			case "later-edit":
				r.Tools = append(r.Tools, RunnerToolEvent{ID: "edit", Command: "write Proof.v", Status: "completed", ExitCode: &zero})
			case "running":
				r.Tools[0].Status = "running"
			case "partial":
				r.Tools[0].Output = "DRAFT AUDIT PASSED\n"
			case "worker":
				r.Role = store.TaskRoleWorker
			case "source-changed":
				r.Tools[0].CompletedAt = time.Now().Add(-time.Hour)
			case "unresolved":
				r.Relay.Next = "verify another target"
			}
			if _, ok := reviewerFinalAuditVerdict(context.Background(), p, r); ok {
				t.Fatal("invalid final evidence accepted")
			}
		})
	}
}

func TestAuditLimitsAndRealBlockers(t *testing.T) {
	limits := defaultVerifierTimeouts()
	if limits[0] != 10*time.Minute || limits[len(limits)-1] != 24*time.Hour {
		t.Fatal(limits)
	}
	for i := 1; i < len(limits); i++ {
		if limits[i] > 24*time.Hour || limits[i] > limits[i-1]*2 {
			t.Fatal(limits)
		}
	}
	r := agentVerifierResult{Status: verifierVerdictContinue, Reason: "audit timed out", Checks: []protocol.VerifierCheck{{Status: verifierVerdictContinue, Code: "missing-target", Target: "Proof.v", Evidence: "file missing"}}}
	if reportedAuditTimeout(r) {
		t.Fatal("timeout hid a concrete missing target")
	}
}

func TestReviewerReusesFreshRedirectedIndependentLog(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "ReviewerFinalAudit.log")
	start := time.Now().Add(-time.Second)
	if err := os.WriteFile(path, []byte("FINAL AUDIT PASSED (--submission).\n"), 0600); err != nil {
		t.Fatal(err)
	}
	zero := 0
	r := orchestrationTurn{Role: store.TaskRoleReviewer, Relay: orchestrationRelayPacket{Structured: true, Status: "resolved", To: "user", Intent: "final", Next: "none", Risks: "none"}, Tools: []RunnerToolEvent{{ID: "audit", Status: "completed", Command: "make -j1 final-audit > ReviewerFinalAudit.log 2>&1", ExitCode: &zero, StartedAt: start, CompletedAt: time.Now().Add(time.Second)}}}
	if _, ok := reviewerFinalAuditVerdict(context.Background(), auditTestPayload(cwd), r); !ok {
		t.Fatal("fresh redirected audit log was not reused")
	}
	r.Tools[0].StartedAt = time.Now().Add(time.Minute)
	if _, ok := reviewerFinalAuditVerdict(context.Background(), auditTestPayload(cwd), r); ok {
		t.Fatal("old redirected log was accepted")
	}
}
