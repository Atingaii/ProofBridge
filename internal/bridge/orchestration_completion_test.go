package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestCompletionPlateauAndStagnation(t *testing.T) {
	samples := make([]protocol.CompletionObservation, 6)
	for i, n := range []int64{20000, 30000, 22000, 1000, 1100, 1050} {
		samples[i] = protocol.CompletionObservation{Round: i + 1, UsageKey: "reviewer/b/codex/model", OutputTokens: n, SourceDigest: "unchanged", BlockerKey: "missing-proof"}
	}
	if !completionPlateau(samples[:5], samples[5]) {
		t.Fatal("missed plateau")
	}
	if !completionStalled(samples[:5], samples[5]) {
		t.Fatal("missed stable blocker")
	}
	for _, test := range []struct {
		name   string
		mutate func(*protocol.CompletionObservation)
	}{
		{"unknown", func(s *protocol.CompletionObservation) { s.UsageKey = "" }},
		{"missing", func(s *protocol.CompletionObservation) { s.OutputTokens = 0 }},
		{"different model", func(s *protocol.CompletionObservation) { s.UsageKey = "other" }},
		{"nonconsecutive", func(s *protocol.CompletionObservation) { s.Round = 9 }},
		{"high", func(s *protocol.CompletionObservation) { s.OutputTokens = 20000 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := samples[5]
			test.mutate(&sample)
			if completionPlateau(samples[:5], sample) {
				t.Fatal("false plateau")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*protocol.CompletionObservation)
	}{
		{"unknown source", func(s *protocol.CompletionObservation) { s.SourceDigest = "" }},
		{"progress", func(s *protocol.CompletionObservation) { s.SourceDigest = "new" }},
		{"changed blocker", func(s *protocol.CompletionObservation) { s.BlockerKey = "new obligation" }},
		{"changed task", func(s *protocol.CompletionObservation) { s.TaskDigest = "new task" }},
		{"pass", func(s *protocol.CompletionObservation) { s.BlockerKey = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := samples[5]
			test.mutate(&sample)
			if completionStalled(samples[:5], sample) {
				t.Fatal("false stagnation")
			}
		})
	}
}

func TestIndependentCompletionIgnoresPlanning(t *testing.T) {
	p := protocol.OrchestrationStartPayload{RunID: "run", TaskPrompt: "prove sort_correct", CWD: "/workspace", TaskGraph: &protocol.TaskGraphPayload{Round: 4, Tasks: []protocol.TaskPayload{{Role: store.TaskRoleWorker}}}}
	record := orchestrationTurn{Relay: orchestrationRelayPacket{Structured: true, Status: "resolved", Next: "none", Risks: "none"}}
	first := completionSample(p, record, "source")
	firstPrompt := composeAgentVerifierPrompt(p)
	for _, plan := range []string{"[PLAN_ITEM id=\"P1\" status=\"pending\"] plan-only-marker", "[PLAN_ITEM id=\"P1\" status=\"completed\"] plan-only-marker"} {
		p.PlanWorkspace = true
		p.Context = plan
		if got := completionSample(p, record, "source"); got != first {
			t.Fatalf("planning changed completion identity: %#v", got)
		}
		if composeAgentVerifierPrompt(p) != firstPrompt || !completionCandidate(p, record) {
			t.Fatal("planning affected independent verification")
		}
	}
	for _, role := range []string{store.TaskRolePlanner, store.TaskRolePlanReviewer, store.TaskRoleReviewer} {
		p.TaskGraph.Tasks[0].Role = role
		if completionCandidate(p, record) {
			t.Fatalf("%s became an execution candidate", role)
		}
	}
	p.TaskGraph.Tasks[0].Role = store.TaskRoleIntegrator
	if !completionCandidate(p, record) {
		t.Fatal("integrator candidate was ignored")
	}
	record.Relay.Risks = "sort_correct unproved"
	if completionCandidate(p, record) {
		t.Fatal("unresolved handoff became a candidate")
	}
}

func TestConcreteVerifierConflictAndStableIdentity(t *testing.T) {
	pass := agentVerifierResult{Agent: "a", Status: "pass", Checks: []protocol.VerifierCheck{{Name: "task", Status: "pass"}}}
	fail := agentVerifierResult{Agent: "b", Status: "continue", Checks: []protocol.VerifierCheck{{Name: "verification", Status: "continue", Code: "proof-incomplete", Target: "Sort.sort_correct", Evidence: "Print Assumptions Sort.sort_correct: sort_correct", Reason: "sort_correct unproved"}}}
	v := aggregateAgentVerifierQuorum([]agentVerifierResult{pass, fail})
	if v.Status != "continue" {
		t.Fatalf("concrete failed proof was overridden: %#v", v)
	}
	key := completionBlockerKey(v, []agentVerifierResult{pass, fail})
	fail.Checks[0].Reason = "The sort correctness obligation remains open"
	fail.Checks[0].Evidence = "Updated diagnostic at line 42: sort_correct"
	v = aggregateAgentVerifierQuorum([]agentVerifierResult{pass, fail})
	if key == "" || completionBlockerKey(v, []agentVerifierResult{pass, fail}) != key {
		t.Fatal("diagnostic wording broke stable blocker identity")
	}
	fail.Checks[0].Target = "Sort.sort_permutation"
	v = aggregateAgentVerifierQuorum([]agentVerifierResult{pass, fail})
	if completionBlockerKey(v, []agentVerifierResult{pass, fail}) == key {
		t.Fatal("different obligation reused stale blocker identity")
	}
}

func TestVerifierQueueHonorsCancellation(t *testing.T) {
	agentVerificationGate <- struct{}{}
	defer func() { <-agentVerificationGate }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.Default()
	m := NewOrchestrationManager(&cfg)
	defer m.CloseAll()
	r := m.runAgentVerifier(ctx, protocol.OrchestrationStartPayload{}, orchestrationTurnAssignment{CLI: "codex"}, 1, "unused")
	if r.Status == "pass" || r.Error == "" {
		t.Fatalf("canceled queue entry passed: %#v", r)
	}
}

func TestExecutionCandidateRequiresIndependentPass(t *testing.T) {
	for _, role := range []string{store.TaskRoleWorker, store.TaskRoleIntegrator, store.TaskRolePlanner, store.TaskRolePlanReviewer} {
		for _, status := range []string{"pass", "continue"} {
			t.Run(role+"/"+status, func(t *testing.T) {
				dir := t.TempDir()
				cli := filepath.Join(t.TempDir(), "codex")
				script := strings.ReplaceAll(fakeCodexExecScript("Msg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=coqc; next=none; risks=none"), `"status":"pass"`, `"status":"`+status+`"`)
				if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				cfg := config.Default()
				cfg.Bridge.CodexPath, cfg.Bridge.CWD = cli, dir
				m := NewOrchestrationManager(&cfg)
				defer m.CloseAll()
				out := make(chan protocol.Envelope, 256)
				m.AttachOut(out)
				m.run(context.Background(), protocol.OrchestrationStartPayload{RunID: "candidate", CWD: dir, Prompt: "prove", MaxTurns: 1, WorkerPair: protocol.WorkerPairCodexCodex, PlanWorkspace: true, TaskGraph: &protocol.TaskGraphPayload{ID: "graph", Round: 1, MaxRounds: 12, Tasks: []protocol.TaskPayload{{Role: role, WorkerSlot: "codex-a"}}}})
				var end protocol.OrchestrationEventPayload
				checks := 0
				for _, e := range drainOrchestrationEvents(t, out) {
					if e.Kind == "run.end" {
						end = e
					}
					if e.Kind == "verifier.verdict" {
						checks++
					}
				}
				execution := role == store.TaskRoleWorker || role == store.TaskRoleIntegrator
				if end.RunEndData == nil || (end.RunEndData.TerminalReason == "verified-early") != (execution && status == "pass") || end.RunEndData.CompletionObservation != nil {
					t.Fatalf("incorrect early decision or reviewer sample: %#v", end)
				}
				if (checks == 1) != execution {
					t.Fatalf("verifier calls=%d for %s", checks, role)
				}
			})
		}
	}
}

func TestCompletionFingerprintIgnoresBuildOutputsNotProofs(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Main.v", "Theorem sample : True. Proof. exact I. Qed.")
	first := completionSourceDigest(ctx, dir)
	if first == "" {
		t.Fatal("missing fingerprint")
	}
	write("Main.vo", "compiled")
	write("Main.glob", "glob")
	write(".Main.aux", "aux")
	if completionSourceDigest(ctx, dir) != first {
		t.Fatal("build output counted as progress")
	}
	write("Main.v", "Theorem sample : True. Admitted.")
	if completionSourceDigest(ctx, dir) == first {
		t.Fatal("proof edit missed")
	}
	if err := os.Symlink("Main.v", filepath.Join(dir, "alias.v")); err != nil {
		t.Fatal(err)
	}
	if completionSourceDigest(ctx, dir) != "" {
		t.Fatal("symlink cannot establish stagnation")
	}
}

func TestVerifierAuthorizedExecutionCompilesCoq(t *testing.T) {
	// No model call: the subprocess fixture performs a real proof build when
	// coqc is installed, and rejects the conflicting forced read-only policy.
	dir := t.TempDir()
	script := fakeCodexExecScript("worker")
	script = strings.Replace(script, "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:", `if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    import subprocess, shutil, pathlib
    assert "--dangerously-bypass-approvals-and-sandbox" in sys.argv
    assert "--sandbox" not in sys.argv
    assert "make -j1" in prompt
    if shutil.which("coqc"):
        subprocess.run(["coqc", "Main.v"], check=True, stdout=subprocess.DEVNULL)
    else:
        pathlib.Path("Main.vo").write_text("build fixture")`, 1)
	cli := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Main.v"), []byte("Theorem sample : True. Proof. exact I. Qed.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = cli
	cfg.Bridge.CWD = dir
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	m := NewOrchestrationManager(&cfg)
	defer m.CloseAll()
	p := protocol.OrchestrationStartPayload{RunID: "test", CWD: dir, Prompt: "prove sample"}
	before := completionSourceDigest(context.Background(), dir)
	result := m.runAgentVerifier(context.Background(), p, orchestrationTurnAssignment{CLI: "codex", WorkerSlot: "codex-a"}, 1, composeAgentVerifierPrompt(p))
	if result.Status != "pass" {
		t.Fatalf("verifier=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "Main.vo")); err != nil {
		t.Fatal(err)
	}
	if after := completionSourceDigest(context.Background(), dir); after != before {
		t.Fatal("compilation modified source fingerprint")
	}
}

func TestCompletionRunRejectsMutationAndStopsRepeatedBlocker(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		name := "stalled"
		if mutate {
			name = "mutation"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cli := filepath.Join(t.TempDir(), "codex")
			script := fakeCodexExecScript("最终结论：完成。\nHandoff: status=resolved; changed=none; verified=coqc; next=none; risks=none")
			if mutate {
				script = strings.Replace(script, "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:", "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:\n    open(\"Main.v\",\"a\").write(\"(* mutation *)\\n\")", 1)
			} else {
				script = strings.ReplaceAll(script, `"status":"pass"`, `"status":"continue"`)
			}
			if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "Main.v"), []byte("Theorem sample : True. Proof. exact I. Qed.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Bridge.CodexPath = cli
			cfg.Bridge.CWD = dir
			m := NewOrchestrationManager(&cfg)
			defer m.CloseAll()
			p := protocol.OrchestrationStartPayload{RunID: "completion", CWD: dir, Prompt: "prove", MaxTurns: 1, WorkerPair: protocol.WorkerPairCodexCodex, TaskGraph: &protocol.TaskGraphPayload{ID: "graph", Round: 1, MaxRounds: 12, Tasks: []protocol.TaskPayload{{Role: store.TaskRoleReviewer, WorkerSlot: "codex-b"}}}}
			for round := 1; round <= 3; round++ {
				p.TaskGraph.Round = round
				out := make(chan protocol.Envelope, 256)
				m.AttachOut(out)
				m.run(context.Background(), p)
				var terminal protocol.OrchestrationEventPayload
				for _, e := range drainOrchestrationEvents(t, out) {
					if e.Kind == "run.end" || e.Kind == "run.error" {
						terminal = e
					}
				}
				wantOutcome := "blocked"
				if round == 3 && !mutate {
					wantOutcome = "completed"
				}
				if terminal.RunEndData == nil || terminal.RunConclusion == nil || terminal.RunConclusion.Outcome != wantOutcome {
					t.Fatalf("false completion: %#v", terminal)
				}
				if mutate {
					if terminal.RunEndData.VerifierVerdict.Status != "continue" {
						t.Fatal("source mutation passed")
					}
					break
				}
				if round < 3 && terminal.Kind != "run.end" {
					t.Fatalf("stopped too early: %#v", terminal)
				}
				if round == 3 && (terminal.Kind != "run.end" || terminal.Status != store.OrchestrationCompleted || terminal.RunEndData.TerminalReason != "stalled" || terminal.RunConclusion.VerificationStatus != "continue" || len(terminal.RunConclusion.UnmetObligations) == 0) {
					t.Fatalf("did not stop stalled run: %#v", terminal)
				}
				if sample := terminal.RunEndData.CompletionObservation; sample != nil {
					p.CompletionHistory = append(p.CompletionHistory, *sample)
				}
			}
		})
	}
}

func TestVerifierKeepsConfiguredCodexPermissions(t *testing.T) {
	for _, sandbox := range []string{"read-only", "workspace-write"} {
		t.Run(sandbox, func(t *testing.T) {
			cli := filepath.Join(t.TempDir(), "codex")
			script := strings.Replace(fakeCodexExecScript("worker"), "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:", "if \"PROOFBRIDGE_AGENT_VERIFIER_V1\" in prompt:\n    assert \"--dangerously-bypass-approvals-and-sandbox\" not in sys.argv\n    assert sys.argv[sys.argv.index(\"--sandbox\")+1] == \""+sandbox+"\"", 1)
			if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Bridge.CodexPath, cfg.Bridge.CWD, cfg.Bridge.Sandbox = cli, t.TempDir(), sandbox
			m := NewOrchestrationManager(&cfg)
			defer m.CloseAll()
			p := protocol.OrchestrationStartPayload{RunID: "permissions", CWD: cfg.Bridge.CWD, Prompt: "verify"}
			r := m.runAgentVerifier(context.Background(), p, orchestrationTurnAssignment{CLI: "codex", WorkerSlot: "codex-a"}, 1, composeAgentVerifierPrompt(p))
			if r.Status != "pass" {
				t.Fatalf("configured permissions changed: %#v", r)
			}
		})
	}
}
