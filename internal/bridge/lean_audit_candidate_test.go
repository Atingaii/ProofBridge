package bridge

import (
	"context"
	"encoding/json"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Native shell command, including argv quoting and localized exit capture, from
// the first live MCP regression experiment.
const leanCandidateAudit = "/bin/bash -lc 'set -o pipefail\ntimeout --kill-after=10s 24h make -j1 final-audit 2>&1 | tee .lake/final-audit-submission.log\nproof_audit_status=$?\nprintf '\"'审计命令退出码 %s\\\\n' \\\"\"'$proof_audit_status\"\nexit \"$proof_audit_status\"'"
const leanCandidateOutput = "FROZEN SPEC CHECK PASSED.\nFINAL AUDIT PASSED (submission).\n审计命令退出码 0\n"

func TestLeanNativeLocalCWD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "数学 proof")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	for _, v := range []string{path, uri, strings.Replace(uri, "file:///", "file://localhost/", 1)} {
		got, err := leanNativeWorkspace(v)
		if err != nil || got != path {
			t.Errorf("cwd %q: %q, %v", v, got, err)
		}
	}
	for _, v := range []string{"", "not/absolute", "http://example.test/p", "file:relative", "file:/missing-authority", "file://example.test" + path, uri + "?", uri + "#", uri + "#frag", uri + "?q=1", "file://user@localhost" + path, "file://localhost:9" + path, strings.Replace(uri, "/tmp/", "/tmp%2F", 1), uri + "%5c", uri + "%00", "file:////host/path"} {
		if _, err := leanNativeWorkspace(v); err == nil {
			t.Errorf("accepted non-local/ambiguous URI %q", v)
		}
	}
}

func TestLeanCandidateRequiresActualPropagatedPipelineExit(t *testing.T) {
	zero, nonzero := 0, 2
	for _, tc := range []struct {
		name, command, output string
		exit                  *int
		want                  bool
	}{
		{"actual native record", leanCandidateAudit, leanCandidateOutput, &zero, true},
		{"failed real exit", leanCandidateAudit, leanCandidateOutput, &nonzero, false},
		{"masked status", strings.Replace(leanCandidateAudit, "set -o pipefail", "true", 1), leanCandidateOutput, &zero, false},
		{"different variable on exit", strings.Replace(leanCandidateAudit, `exit "$proof_audit_status"`, `exit "$other"`, 1), leanCandidateOutput, &zero, false},
		{"fake result", leanCandidateAudit, strings.Replace(leanCandidateOutput, "退出码 0", "退出码 127", 1), &zero, false},
		{"copied log", `cat .lake/final-audit-submission.log`, leanCandidateOutput, &zero, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok := parseLeanAuditCommand(tc.command)
			pass := ok && cmd.passed(RunnerToolEvent{Status: "completed", ExitCode: tc.exit, Output: tc.output})
			if pass != tc.want {
				t.Fatalf("pass=%t parse=%t %q", pass, ok, tc.command)
			}
		})
	}
}

func TestLeanCandidateIndependentAdoptionAndVeto(t *testing.T) {
	m, p, s := leanAuditTestScope(t)
	zero, nonzero := 0, 2
	start := func(work bool, slot string) *leanReviewTurn {
		r := newLeanReviewForTest(t.Context(), s)
		r.workNode = work
		r.slot = slot
		return r
	}
	cwd := (&url.URL{Scheme: "file", Path: s.root}).String()
	passed := RunnerToolEvent{ID: "exec", CWD: cwd, Command: leanCandidateAudit, Status: "completed", ExitCode: &zero, Output: leanCandidateOutput}
	adopt := RunnerToolEvent{ID: "adopt", CWD: cwd, Command: leanAdoptDeclaration, Status: "completed", ExitCode: &zero}
	if hint := s.turnGuidance(t.Context(), store.TaskRoleReviewer, orchestrationCodexSlotB); strings.Contains(hint, leanAdoptDeclaration) || !strings.Contains(hint, leanFinalDeclaration) {
		t.Fatal("reviewer offered nonexistent evidence")
	}
	author := start(true, orchestrationCodexSlotA)
	author.observe(passed, "worker-A")
	author.observe(adopt, "worker-A")
	if author.accepted || author.receipt.Reviewer != "" {
		t.Fatal("author granted an independent decision")
	}
	if _, ok := s.verdict(t.Context(), author.receipt); ok {
		t.Fatal("candidate promoted directly at verification boundary")
	}
	self := start(false, orchestrationCodexSlotA)
	self.observe(adopt, "review-A")
	if self.accepted || !self.adoptionUnavailable {
		t.Fatal("reviewer accepted its own candidate")
	}
	if hint := s.turnGuidance(t.Context(), store.TaskRoleIntegrator, orchestrationCodexSlotA); strings.Contains(hint, "timeout --kill-after") || !strings.Contains(hint, "already observed") {
		t.Fatal("integrator asked to repeat its own current evaluation")
	}
	if hint := s.turnGuidance(t.Context(), store.TaskRoleReviewer, orchestrationCodexSlotB); !strings.Contains(hint, leanAdoptDeclaration) {
		t.Fatal("current other-slot observation missed")
	}
	oldNative := start(true, orchestrationCodexSlotB)
	oldNative.observe(passed, "work-B")
	s = m.leanSubmissionScope(t.Context(), p, s.root)
	b := start(false, orchestrationCodexSlotB)
	b.observe(adopt, "review-B")
	if !b.accepted || b.receipt.Reviewer != "review-B" || b.receipt.EvaluatorSlot != orchestrationCodexSlotA {
		t.Fatalf("wrong native provenance %#v", b.receipt)
	}
	bad := RunnerToolEvent{ID: "bad", CWD: cwd, Command: "make final-audit", Status: "failed", ExitCode: &nonzero, Output: "ERROR: Submission.lean:1: proof hole is not allowed here: sorry"}
	author = start(true, orchestrationCodexSlotA)
	author.observe(bad, "later-work")
	if ev, ok := s.availableReceipt(b.receipt.Digest, orchestrationCodexSlotB, false); !ok || ev.Reviewer != "review-B" {
		t.Fatal("a failed candidate erased an independent pass")
	}
	b = start(false, orchestrationCodexSlotB)
	b.observe(bad, "new-independent-check")
	author = start(true, orchestrationCodexSlotA)
	author.observe(passed, "new-worker-pass")
	b = start(false, orchestrationCodexSlotB)
	b.observe(adopt, "new-independent-adopt")
	if b.accepted {
		t.Fatal("worker observation overrode an independent failure")
	}
	b.observe(RunnerToolEvent{ID: "direct", CWD: cwd, Command: leanFinalDeclaration + "make final-audit", Status: "completed", ExitCode: &zero, Output: leanSubmissionMarker}, "new-independent-direct")
	if !b.accepted {
		t.Fatal("independent authoritative reevaluation did not supersede its old failure")
	}
	if err := os.WriteFile(filepath.Join(s.root, "Submission.lean"), []byte("theorem newProof : True := True.intro"), 0600); err != nil {
		t.Fatal(err)
	}
	b = start(false, orchestrationCodexSlotB)
	b.observe(adopt, "stale")
	if b.accepted || strings.Contains(s.turnGuidance(t.Context(), store.TaskRoleReviewer, b.slot), leanAdoptDeclaration) {
		t.Fatal("stale candidate still offered for adoption")
	}
}

func TestLeanMCPWorkerThenIndependentReviewerNoExtraEvaluation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	handoff := "Independent checks complete.\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=submission; next=none; risks=none"
	script := fakeCodexExecScriptWithSuccessfulCommand(handoff)
	command, _ := json.Marshal(leanCandidateAudit)
	output, _ := json.Marshal(leanCandidateOutput)
	needle := `            turn_id = "turn_verified"`
	script = strings.Replace(script, needle, needle+`
            from pathlib import Path
            task_text = json.dumps(params.get("input"))
            is_reviewer = "Independent Lean reviewer: first check" in task_text
            if is_reviewer and "Bridge already holds a current native submission PASS" not in task_text:
                raise RuntimeError("reviewer dispatched without the eligible current candidate")
            chosen = ": PROOFBRIDGE_FINAL_REVIEW" if is_reviewer else `+string(command)+`
            observed = "" if is_reviewer else `+string(output), 1)
	script = strings.ReplaceAll(script, `"command":"go test ./..."`, `"command":chosen,"cwd":Path.cwd().as_uri()`)
	script = strings.ReplaceAll(script, `"exitCode":0,"status":"completed"`, `"exitCode":0,"status":"completed","aggregatedOutput":observed`)
	script = strings.Replace(script, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:`, `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    raise RuntimeError("no silent verifier may run after independent native adoption")`, 1)
	m, cwd, out := auditTestManager(t, script)
	for n, v := range map[string]string{"lakefile.toml": "name = \"test\"", "Submission.lean": "theorem main : True := True.intro", "Makefile": "final-audit:\n\ttrue\n"} {
		if err := os.WriteFile(filepath.Join(cwd, n), []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := auditTestPayload(cwd)
	p.Profile = "formal-proof"
	p.TaskPrompt = "Complete the original Lean proof and run make final-audit."
	p.RunID = "proof:attemptA"
	p.TaskGraph.Tasks[0].Role, p.TaskGraph.Tasks[0].WorkerSlot = store.TaskRoleWorker, orchestrationCodexSlotA
	m.executions[p.RunID] = orchestrationExecution{runID: "proof"}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	m.run(ctx, p)
	auditCount := 0
	for _, e := range drainOrchestrationEvents(t, out) {
		if e.Kind == "command.end" && e.CommandData != nil && e.CommandData.Command == leanCandidateAudit {
			auditCount++
		}
		if e.Kind == "verifier.verdict" || (e.Kind == "run.end" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verified-early") {
			t.Fatal("worker prematurely ended verification")
		}
	}
	// Restart the Bridge and load the candidate by public task identity.
	m.CloseAll()
	reviewer := NewOrchestrationManager(m.cfg)
	t.Cleanup(reviewer.CloseAll)
	events := make(chan protocol.Envelope, 256)
	reviewer.AttachOut(events)
	p.RunID = "proof:attemptB"
	p.TaskGraph.Tasks[0].Role, p.TaskGraph.Tasks[0].WorkerSlot = store.TaskRoleReviewer, orchestrationCodexSlotB
	reviewer.executions[p.RunID] = orchestrationExecution{runID: "proof"}
	reviewer.run(ctx, p)
	passed, adopted := false, false
	for _, e := range drainOrchestrationEvents(t, events) {
		if e.Kind == "command.end" && e.CommandData != nil {
			if e.CommandData.Command == leanCandidateAudit {
				auditCount++
			}
			if e.CommandData.Command == leanAdoptDeclaration {
				adopted = true
			}
		}
		if e.Kind == "turn.usage" && e.Role == "verifier" {
			t.Fatal("redundant verifier invoked")
		}
		if e.Kind == "run.end" && e.RunEndData != nil && e.RunEndData.TerminalReason == "verified-early" && e.RunConclusion.Outcome == "satisfied" {
			passed = true
		}
		if e.Kind == "run.error" {
			t.Fatalf("Bridge failed: %s", e.Error)
		}
	}
	if auditCount != 1 || !passed || !adopted {
		t.Fatalf("evaluations=%d independently adopted=%t verified=%t", auditCount, adopted, passed)
	}
}
