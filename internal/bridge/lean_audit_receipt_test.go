package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestLeanAuditNativeShellEvidence(t *testing.T) {
	// These command strings are native Codex renderings from the failing
	// benchmark. They include the exact shell quoting, status and log dataflow.
	fixtures := []struct {
		name, command, output string
		ok                    bool
	}{
		{"round 1 masked failure", "/bin/bash -c \"timeout --kill-after=10s 180s lake build; printf 'build_exit=%s\\\\n' \\\"\"'$?\"; timeout --kill-after=10s 240s make final-audit; printf '\"'final_audit_exit=%s\\\\n' \\\"\"'$?\"'", "ERROR: Submission.lean:11: proof hole is not allowed here: sorry\nfinal_audit_exit=2", false},
		{"round 2 tee alone", "/bin/bash -lc 'timeout --kill-after=10s 3600s make final-audit 2>&1 | tee /tmp/final-audit-review.log'", "FINAL AUDIT PASSED (submission).", false},
		{"round 3 saved exit", "/bin/bash -c 'timeout --kill-after=20s 1800s make final-audit > /tmp/review-final-audit.log 2>&1; ec=$?; echo \"exit=$ec\"; tail -80 /tmp/review-final-audit.log'", "exit=0\nFINAL AUDIT PASSED (submission).", true},
		{"round 10 propagated child exit", "/bin/bash -c 'set -o pipefail\nlog=\"validation/reviewer-independent-final-audit-$(date -u +%Y%m%dT%H%M%SZ).log\"\nprintf '\"'LOG=%s\\\\nSTART=%s\\\\n' \\\"\"'$log\" \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\" | tee \"$log\"\ntimeout --kill-after=30s 10800s make final-audit 2>&1 | tee -a \"$log\"\nstatus=${PIPESTATUS[0]}\nprintf '\"'END=%s\\\\nEXIT=%s\\\\n' \\\"\"'$(date -u +%Y-%m-%dT%H:%M:%SZ)\" \"$status\" | tee -a \"$log\"\nexit \"$status\"'", "FINAL AUDIT PASSED (submission).\nEND=2026-11-17T16:54:27Z\nEXIT=0", true},
		{"round 13 independently recorded exit", "/bin/bash -c 'TS=$(date -u +%Y%m%dT%H%M%SZ); LOG=\"validation/reviewer-current-round-final-audit-${TS}.log\"; echo \"LOG=$LOG\"; bash -o pipefail -c '\"'timeout --kill-after=30s 14400s make final-audit' 2>&1 | tee \\\"\"'$LOG\"; echo \"AUDIT_EXIT=${PIPESTATUS[0]}\" | tee -a \"$LOG\"'", "FINAL AUDIT PASSED (submission).\nAUDIT_EXIT=0", true},
		{"direct", "make final-audit", leanSubmissionMarker, true},
		{"pipefail", "set -o pipefail; make final-audit 2>&1 | tee validation/review.log", leanSubmissionMarker, true},
		{"shell pipefail", "/bin/bash -o pipefail -c 'make final-audit 2>&1 | tee validation/review.log'", leanSubmissionMarker, true},
		{"baseline", "make final-audit", "FINAL AUDIT PASSED (baseline).", false},
		{"bare", "make final-audit", "FINAL AUDIT PASSED", false},
		{"partial", "make final-audit", leanSubmissionMarker + "\nERROR: kernel failed", false},
		{"echo", "echo 'FINAL AUDIT PASSED (submission).'", leanSubmissionMarker, false},
		{"cat copied result", "cat validation/reviewer-final-audit.log", leanSubmissionMarker, false},
		{"forged exit", "make final-audit 2>&1 | tee validation/r.log; echo AUDIT_EXIT=0", leanSubmissionMarker + "\nAUDIT_EXIT=0", false},
		{"failed child", `make final-audit 2>&1 | tee validation/r.log; echo "AUDIT_EXIT=${PIPESTATUS[0]}"`, leanSubmissionMarker + "\nAUDIT_EXIT=2", false},
		{"other log", `make final-audit > /tmp/a.log 2>&1; rc=$?; echo "exit=$rc"; tail -80 /tmp/b.log`, "exit=0\n" + leanSubmissionMarker, false},
		{"other variable", `make final-audit > /tmp/a.log 2>&1; rc=$?; echo "exit=$wrong"; tail -80 /tmp/a.log`, "exit=0\n" + leanSubmissionMarker, false},
		{"injected command", `TS=$(date -u +%Y%m%dT%H%M%SZ); echo 'FINAL AUDIT PASSED (submission).'; make final-audit`, leanSubmissionMarker, false},
		{"outer expansion", `bash -c "make final-audit $(echo injected)"`, leanSubmissionMarker, false},
		{"swallowed code", `make final-audit || true`, leanSubmissionMarker, false},
		{"nonterminal designation", leanFinalDeclaration + `make final-audit; true`, leanSubmissionMarker, false},
	}
	zero := 0
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			c, known := parseLeanAuditCommand(f.command)
			success := known && c.passed(RunnerToolEvent{Status: "completed", ExitCode: &zero, Output: f.output})
			if success != f.ok {
				t.Fatalf("got pass=%t known=%t descriptor=%#v command=%q", success, known, c, f.command)
			}
		})
	}
}

func leanAuditTestScope(t *testing.T) (*OrchestrationManager, protocol.OrchestrationStartPayload, *leanAuditScope) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	for name, body := range map[string]string{"lakefile.toml": "name = \"proof\"", "Submission.lean": "theorem main : True := True.intro", "Makefile": "final-audit:\n\tpython3 tools/audit.py submission\n", "README.md": "make final-audit"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	m := NewOrchestrationManager(&cfg)
	t.Cleanup(m.CloseAll)
	p := auditTestPayload(cwd)
	p.Profile = "formal-proof"
	p.TaskPrompt = "Run make final-audit for all required Lean theorems."
	s := m.leanSubmissionScope(t.Context(), p, cwd)
	if s == nil || s.original.Frozen == "" {
		t.Fatal("missing initial submission identity")
	}
	return m, p, s
}

func newLeanReviewForTest(ctx context.Context, s *leanAuditScope) *leanReviewTurn {
	live, cancel := context.WithCancelCause(ctx)
	in, _ := s.current(live, time.Time{})
	return &leanReviewTurn{scope: s, ctx: live, cancel: cancel, turnStart: time.Now(), initial: in.Digest, started: map[string]leanAuditStart{}, active: map[string]bool{}}
}

func TestLeanReceiptBindsReviewerInputAndSurvivesRestart(t *testing.T) {
	m, p, s := leanAuditTestScope(t)
	r := newLeanReviewForTest(t.Context(), s)
	zero := 0
	event := RunnerToolEvent{ID: "audit", CWD: s.root, Command: "make -j1 final-audit", Status: "in_progress"}
	r.observe(event, "review-1")
	if err := os.MkdirAll(filepath.Join(s.root, "validation"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "validation", "another-log.log"), []byte("probe"), 0600); err != nil {
		t.Fatal(err)
	}
	event.Status, event.ExitCode, event.Output = "completed", &zero, leanSubmissionMarker
	r.observe(event, "review-1")
	if r.accepted || r.receipt.Digest == "" {
		t.Fatal("ordinary independent receipt did not wait for remaining reviewer checks")
	}
	after := m.leanSubmissionScope(t.Context(), p, s.root)
	r = newLeanReviewForTest(t.Context(), after)
	adopt := RunnerToolEvent{ID: "adopt", CWD: s.root, Command: leanAdoptDeclaration, Status: "completed", ExitCode: &zero}
	r.observe(adopt, "review-2")
	if !r.accepted || r.receipt.Reviewer != "review-1" || context.Cause(r.ctx) != errLeanAuditAccepted {
		t.Fatal("registered audit was not reusable")
	}
	p.InputVersion++
	other := newLeanReviewForTest(t.Context(), m.leanSubmissionScope(t.Context(), p, s.root))
	other.observe(adopt, "new-scope")
	if other.accepted {
		t.Fatal("new user instruction reused prior scope")
	}
	if err := os.WriteFile(filepath.Join(s.root, "validation", "NewHelper.lean"), []byte("axiom bad : False"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := newLeanReviewForTest(t.Context(), after)
	changed.observe(adopt, "later")
	if changed.accepted {
		t.Fatal("changed proof inputs reused receipt")
	}
}

func TestLeanRootAuditOutputAndFrozenLogAreDistinct(t *testing.T) {
	m, p, s := leanAuditTestScope(t)
	log := filepath.Join(s.root, "FrozenFinalAudit.log")
	if err := os.WriteFile(log, []byte("frozen reference output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "spec.sha256"), []byte(strings.Repeat("a", 64)+"  FrozenFinalAudit.log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p.RunID = "separate-task-with-frozen-manifest"
	s = m.leanSubmissionScope(t.Context(), p, s.root)
	before, ok := s.current(t.Context(), time.Time{})
	if !ok {
		t.Fatal("input missing")
	}
	if err := os.WriteFile(filepath.Join(s.root, "ReviewerFinalAudit.log"), []byte(leanSubmissionMarker), 0600); err != nil {
		t.Fatal(err)
	}
	after, ok := s.current(t.Context(), time.Time{})
	if !ok || before.Digest != after.Digest {
		t.Fatal("root reviewer output counted as a proof edit")
	}
	if err := os.WriteFile(log, []byte("changed frozen reference"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.current(t.Context(), time.Time{}); ok {
		t.Fatal("frozen evaluator input with a .log name was ignored")
	}
}

func TestLeanNativeFinalRejectsForeignInputsAndConcurrentWrites(t *testing.T) {
	_, _, s := leanAuditTestScope(t)
	zero := 0
	e := RunnerToolEvent{ID: "cmd", Status: "completed", Command: leanFinalDeclaration + "make final-audit", ExitCode: &zero, Output: leanSubmissionMarker}
	for _, cwd := range []string{"", t.TempDir()} {
		r := newLeanReviewForTest(t.Context(), s)
		e.CWD = cwd
		r.observe(e, "B")
		if r.accepted {
			t.Fatal("unbound or foreign working directory passed")
		}
	}
	r := newLeanReviewForTest(t.Context(), s)
	e.CWD = s.root
	e.Status = "running"
	r.observe(e, "B")
	source := filepath.Join(s.root, "Submission.lean")
	if err := os.WriteFile(source, []byte("theorem modified : True := True.intro"), 0600); err != nil {
		t.Fatal(err)
	}
	old := r.turnStart.Add(-time.Hour)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	e.Status = "completed"
	r.observe(e, "B")
	if r.accepted {
		t.Fatal("backdated change during audit passed")
	}
	r = newLeanReviewForTest(t.Context(), s)
	e.CWD = ""
	e.Command = "cd '" + s.root + "' && " + leanFinalDeclaration + "make final-audit"
	r.observe(e, "B")
	if !r.accepted {
		t.Fatal("bound terminal audit was not accepted")
	}
	if err := os.WriteFile(filepath.Join(s.root, "Makefile"), []byte("final-audit:\n\t@echo bogus"), 0600); err != nil {
		t.Fatal(err)
	}
	r = newLeanReviewForTest(t.Context(), s)
	r.observe(e, "B")
	if r.accepted {
		t.Fatal("frozen evaluator replaced before reviewer was accepted")
	}
}

func TestLeanReviewerEndsNativeTurnAtAuthoritativeCommand(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	script := fakeCodexExecScriptWithSuccessfulCommand("this turn must have already ended")
	script = strings.ReplaceAll(script, "go test ./...", leanFinalDeclaration+"timeout --kill-after=10s 24h make -j1 final-audit")
	script = strings.ReplaceAll(script, `"status":"completed"}}}`, `"status":"completed","cwd":__import__("os").getcwd(),"aggregatedOutput":"FINAL AUDIT PASSED (submission).\n"}}}`)
	needle := `            print(json.dumps({"method":"item/agentMessage/delta"`
	script = strings.Replace(script, needle, `            __import__("time").sleep(45)
`+needle, 1)
	script = strings.Replace(script, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:`, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    raise RuntimeError("unexpected duplicate verifier")`, 1)
	m, cwd, out := auditTestManager(t, script)
	for name, body := range map[string]string{"lakefile.toml": "name = \"test\"", "Submission.lean": "theorem main : True := True.intro", "Makefile": "final-audit:\n\ttrue\n"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := auditTestPayload(cwd)
	p.Profile = "formal-proof"
	p.TaskPrompt = "Check the Lean submission using make final-audit."
	start := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	m.run(ctx, p)
	if elapsed := time.Since(start); elapsed >= 9*time.Second {
		t.Fatalf("native reviewer was not stopped: %v", elapsed)
	}
	found := false
	var timeline []string
	for _, e := range drainOrchestrationEvents(t, out) {
		timeline = append(timeline, fmt.Sprintf("%s %s %s", e.Kind, e.Error, e.Content))
		if e.Kind == "turn.usage" && e.Role == "verifier" {
			t.Fatal("duplicate verifier was started")
		}
		if e.Kind == "run.cancelled" || e.Kind == "run.error" {
			t.Fatalf("unexpected terminal event %s %s", e.Kind, e.Error)
		}
		if e.Kind == "run.end" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verified-early" && e.RunConclusion != nil && e.RunConclusion.Outcome == "satisfied" {
			found = true
		}
	}
	if !found {
		t.Fatalf("native pass missing: %v", timeline)
	}
}

func TestLeanEvaluatorMakefilePrecedenceIsFrozen(t *testing.T) {
	for _, name := range []string{"GNUmakefile", "makefile", "extra.mk"} {
		t.Run(name, func(t *testing.T) {
			_, _, s := leanAuditTestScope(t)
			if err := os.WriteFile(filepath.Join(s.root, name), []byte("final-audit:\n\t@true\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, ok := s.current(t.Context(), time.Time{}); ok {
				t.Fatal("a newly overriding evaluator file preserved the original evaluator identity")
			}
		})
	}
}

func TestLeanAuditUserCancellationWins(t *testing.T) {
	_, _, s := leanAuditTestScope(t)
	ctx, cancel := context.WithCancel(t.Context())
	r := newLeanReviewForTest(ctx, s)
	cancel()
	zero := 0
	r.observe(RunnerToolEvent{ID: "end", CWD: s.root, Command: leanFinalDeclaration + "make final-audit", ExitCode: &zero, Status: "completed", Output: leanSubmissionMarker}, "B")
	if r.accepted || context.Cause(r.ctx) != context.Canceled {
		t.Fatal("user cancellation converted into a proof pass")
	}
}

func TestLeanProofHoleReturnsToImplementer(t *testing.T) {
	_, p, s := leanAuditTestScope(t)
	r := newLeanReviewForTest(t.Context(), s)
	failed := 2
	r.observe(RunnerToolEvent{ID: "a", CWD: s.root, Command: "make final-audit", ExitCode: &failed, Status: "failed", Output: "ERROR: Submission.lean:11: proof hole is not allowed here: sorry"}, "B")
	if r.failure == nil || r.accepted {
		t.Fatal("real proof hole must continue")
	}
	v, ok := reviewerCompletionAudit(t.Context(), p, orchestrationTurn{Role: store.TaskRoleReviewer, LeanFailure: r.failure}, s)
	if !ok || v.Status != verifierVerdictContinue || len(v.Checkers) != 1 || v.Checkers[0].Target != "Submission.lean:11" {
		t.Fatalf("bad proof failure %#v", v)
	}
}

func TestLeanFinalWaitsForConcurrentToolOnSameInputs(t *testing.T) {
	for _, kind := range []string{"read", "change", "fail"} {
		t.Run(kind, func(t *testing.T) {
			_, _, s := leanAuditTestScope(t)
			r := newLeanReviewForTest(t.Context(), s)
			zero, one := 0, 1
			probe := RunnerToolEvent{ID: "probe", Command: "read helper", CWD: s.root, Status: "running"}
			r.observe(probe, "B")
			final := RunnerToolEvent{ID: "final", Command: leanFinalDeclaration + "make final-audit", CWD: s.root, Status: "completed", ExitCode: &zero, Output: leanSubmissionMarker}
			r.observe(final, "B")
			if r.accepted || !r.pending {
				t.Fatal("did not wait for concurrent native probe")
			}
			probe.Status, probe.ExitCode = "completed", &zero
			if kind == "change" {
				if err := os.WriteFile(filepath.Join(s.root, "Submission.lean"), []byte("theorem revised : True := True.intro"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "fail" {
				probe.ExitCode = &one
				probe.Status = "failed"
			}
			r.observe(probe, "B")
			if r.accepted != (kind == "read") {
				t.Fatalf("accepted=%t after %s", r.accepted, kind)
			}
		})
	}
}

func TestLeanIndependentHandoffAcceptsExistingCapturedExit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	handoff := "Independent submission passed.\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=full audit; next=none; risks=none"
	script := fakeCodexExecScriptWithSuccessfulCommand(handoff)
	cmd := `timeout --kill-after=20s 1800s make final-audit > /tmp/review-final-audit.log 2>&1; rc=$?; echo "exit=$rc"; tail -80 /tmp/review-final-audit.log`
	quoted, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	script = strings.ReplaceAll(script, `"go test ./..."`, string(quoted))
	script = strings.ReplaceAll(script, `"status":"completed"}}}`, `"status":"completed","cwd":__import__("os").getcwd(),"aggregatedOutput":"exit=0\nFINAL AUDIT PASSED (submission).\n"}}}`)
	m, cwd, out := auditTestManager(t, script)
	for name, body := range map[string]string{"lakefile.toml": "name = \"test\"", "Submission.lean": "theorem main : True := True.intro", "Makefile": "final-audit:\n\ttrue\n"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := auditTestPayload(cwd)
	p.Profile = "formal-proof"
	p.TaskPrompt = "Run make final-audit after checking the required Lean obligations."
	m.run(t.Context(), p)
	passed := false
	for _, e := range drainOrchestrationEvents(t, out) {
		if e.Kind == "turn.usage" && e.Role == "verifier" {
			t.Fatal("a proven independent reviewer handoff invoked the model verifiers")
		}
		if e.Kind == "run.end" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verified-early" {
			passed = true
		}
	}
	if !passed {
		t.Fatal("captured native exit and resolved original reviewer handoff did not pass")
	}
	p.TaskGraph.Tasks[0].Role = store.TaskRoleWorker
	m.run(t.Context(), p)
	for _, e := range drainOrchestrationEvents(t, out) {
		if e.Kind == "verifier.verdict" || (e.Kind == "run.end" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verified-early") {
			t.Fatal("implementer bypassed the designated reviewer")
		}
	}
}
