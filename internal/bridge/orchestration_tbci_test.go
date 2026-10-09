package bridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestParseTBCIVerdict(t *testing.T) {
	verdict, reason := parseTBCIVerdict([]byte(`{"verdict":"Trusted","reason":"checks passed"}`))
	if verdict != "Trusted" || reason != "checks passed" {
		t.Fatalf("got %q/%q", verdict, reason)
	}
	verdict, _ = parseTBCIVerdict([]byte(`{"result":{"verdict":"Untrusted"}}`))
	if verdict != "Untrusted" {
		t.Fatalf("nested verdict = %q", verdict)
	}
}

func TestCopyWorkspaceSnapshotIsPrivateAndSkipsMetadata(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot")
	if err := copyWorkspaceSnapshot(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git copied: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, ".codex-bridge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".codex-bridge", "prompt.json"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "snapshot")
	if err := copyWorkspaceSnapshot(source, second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(second, ".codex-bridge")); !os.IsNotExist(err) {
		t.Fatalf(".codex-bridge copied: %v", err)
	}
}

func TestGenerateTBCITemplateSelectsNamedRocqTarget(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "Seqlock-Coq"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Seqlock-Coq", "_CoqProject"), []byte("-R . Top\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "Lemma helper : True.\nProof. exact I. Qed.\n\nTheorem seqlock_linearizable : True.\nProof. exact I. Qed.\n"
	if err := os.WriteFile(filepath.Join(workspace, "Seqlock-Coq", "SeqlockLinearizability.v"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "template.json")
	template, err := generateTBCITemplate(workspace, "请完成 Coq 的 seqlock_linearizable，并检查 SeqlockLinearizability.v", path)
	if err != nil {
		t.Fatal(err)
	}
	if template.Goal.Prover != "rocq" || template.Goal.Module != "SeqlockLinearizability" || template.Goal.Name != "seqlock_linearizable" {
		t.Fatalf("generated target = %#v", template.Goal)
	}
	if len(template.AuthorizedPaths) != 1 || template.AuthorizedPaths[0] != "SeqlockLinearizability.v" {
		t.Fatalf("authorized paths = %#v", template.AuthorizedPaths)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded tbciTemplate
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Goal.Name != template.Goal.Name {
		t.Fatalf("persisted template = %#v, err=%v", decoded, err)
	}
}

func TestGenerateTBCITemplateRejectsAmbiguousTarget(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Main.v"), []byte("Lemma one : True.\nProof. exact I. Qed.\nLemma two : True.\nProof. exact I. Qed.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := generateTBCITemplate(workspace, "请完成这个 Coq 证明", filepath.Join(t.TempDir(), "template.json"))
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
}

func TestGenerateTBCITemplatePreservesLeanAndIsabelleCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		marker     string
		markerBody string
		source     string
		sourceBody string
		prompt     string
		prover     string
		module     string
		target     string
	}{
		{
			name:       "lean",
			marker:     "lean-toolchain",
			markerBody: "leanprover/lean4:stable\n",
			source:     "Main.lean",
			sourceBody: "theorem lean_target : True := by trivial\n",
			prompt:     "请用 Lean 完成 lean_target",
			prover:     "lean4",
			module:     "Main",
			target:     "lean_target",
		},
		{
			name:       "isabelle",
			marker:     "ROOT",
			markerBody: "session Demo = HOL + theories Demo\n",
			source:     "Demo.thy",
			sourceBody: "theory Demo imports Main begin\ntheorem isabelle_target: True by simp\nend\n",
			prompt:     "请用 Isabelle 完成 isabelle_target",
			prover:     "isabelle",
			module:     "Demo",
			target:     "isabelle_target",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, test.marker), []byte(test.markerBody), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, test.source), []byte(test.sourceBody), 0o600); err != nil {
				t.Fatal(err)
			}
			template, err := generateTBCITemplate(workspace, test.prompt, filepath.Join(t.TempDir(), "template.json"))
			if err != nil {
				t.Fatal(err)
			}
			if template.Goal.Prover != test.prover || template.Goal.Module != test.module || template.Goal.Name != test.target {
				t.Fatalf("generated target = %#v", template.Goal)
			}
		})
	}
}

func TestManagedTBCIAutomaticContractFreezesBeforeFinalChange(t *testing.T) {
	if _, err := exec.LookPath("coqc"); err != nil {
		t.Skip("coqc is unavailable")
	}
	artifact, err := filepath.Abs(filepath.Join("tbci_assets", "tbci-linux-amd64"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Skipf("managed TBCI artifact unavailable: %v", err)
	}
	tbciPath := filepath.Join(t.TempDir(), "tbci")
	if err := os.WriteFile(tbciPath, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "_CoqProject"), []byte("-R . Top\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Dep.v"), []byte("Lemma helper : True.\nProof. exact I. Qed.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(workspace, "Main.v")
	if err := os.WriteFile(sourcePath, []byte("From Top Require Import Dep.\nTheorem generated_target : True.\nProof. Admitted.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BRIDGE_RUNTIME_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Bridge.CWD = workspace
	cfg.Bridge.TBCIPath = tbciPath
	manager := &OrchestrationManager{cfg: &cfg}
	payload := protocol.OrchestrationStartPayload{RunID: "orc_auto_contract", PromptSeq: 7, Prompt: "请用 Coq 完成 generated_target", TaskPrompt: "请用 Coq 完成 generated_target", CWD: workspace}
	session, err := manager.prepareTBCIVerifier(context.Background(), payload, payload.TaskPrompt)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := os.ReadFile(filepath.Join(session.initial, "Main.v"))
	if err != nil || !strings.Contains(string(initial), "Admitted") {
		t.Fatalf("initial snapshot was not frozen before the change: %q, err=%v", initial, err)
	}
	if err := os.WriteFile(sourcePath, []byte("From Top Require Import Dep.\nTheorem generated_target : True.\nProof. exact helper. Qed.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := manager.evaluateTBCIVerifier(context.Background(), payload, session)
	if !result.Trusted {
		t.Fatalf("automatic TBCI result = %#v", result)
	}
	reused, err := manager.prepareTBCIVerifier(context.Background(), payload, payload.TaskPrompt)
	if err != nil || reused.runtime != session.runtime {
		t.Fatalf("same user task did not reuse baseline: runtime=%q want=%q err=%v", reused.runtime, session.runtime, err)
	}
	reusedInitial, err := os.ReadFile(filepath.Join(reused.initial, "Main.v"))
	if err != nil || !strings.Contains(string(reusedInitial), "Admitted") {
		t.Fatalf("reused baseline changed after worker edit: %q, err=%v", reusedInitial, err)
	}
}

func TestShouldRunTBCIVerifierOptInFormalProof(t *testing.T) {
	payload := protocol.OrchestrationStartPayload{TaskGraph: &protocol.TaskGraphPayload{Tasks: []protocol.TaskPayload{{Role: "reviewer"}}}, Prompt: "task"}
	if shouldRunTBCIVerifier("formal-proof", "agent", payload) {
		t.Fatal("Agent Verifier mode must not run TBCI")
	}
	if !shouldRunTBCIVerifier("formal-proof", "tbci", payload) {
		t.Fatal("selected formal-proof TBCI should run")
	}
	if shouldRunTBCIVerifier("default", "tbci", payload) {
		t.Fatal("non formal-proof TBCI should not run")
	}
	if shouldRunTBCIVerifier("formal-proof", "tbci", protocol.OrchestrationStartPayload{}) {
		t.Fatal("without task graph TBCI should not run")
	}
	payload.TaskGraph.Tasks[0].Role = "worker"
	if shouldRunTBCIVerifier("formal-proof", "tbci", payload) {
		t.Fatal("non-reviewer task TBCI should not run")
	}
}

func TestTBCIVerifierVerdictIsIndependent(t *testing.T) {
	pass := tbciVerifierVerdict(tbciVerificationResult{Trusted: true, Reason: "Trusted"})
	if pass.Status != verifierVerdictPass || len(pass.Checkers) != 1 || pass.Checkers[0].Name != "tbci" {
		t.Fatalf("trusted verdict = %#v", pass)
	}
	continued := tbciVerifierVerdict(tbciVerificationResult{Reason: "Inconclusive"})
	if continued.Status != verifierVerdictContinue || len(continued.Checkers) != 1 || continued.Checkers[0].Name != "tbci" {
		t.Fatalf("non-Trusted verdict = %#v", continued)
	}
}
