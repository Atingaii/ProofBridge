package hub

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http/httptest"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/bridge"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/store"
)

// Fixed CLI responses are a transport fixture. All proof subprocesses, Hub and
// Bridge execution, HTTP SDK, WebSocket persistence and SMTP exchanges are real.
func TestAutomationProofFlowRealCoqAndLocalSMTP(t *testing.T) {
	coqc, err := exec.LookPath("coqc")
	if err != nil {
		t.Skip("real coqc is required for the proof transport integration")
	}
	if _, err = exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for the deterministic CLI transport fixture")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	if err = os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Goal.v", "README.md"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "automation-proof", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(workspace, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "automation-proof", "codex_fixture.py"))
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "codex-fixture")
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cli, raw, 0700); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(root, "observer-closed")
	proofLog := filepath.Join(root, "coqc-runs.jsonl")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("PROOFBRIDGE_FIXTURE_COQC", coqc)
	t.Setenv("PROOFBRIDGE_FIXTURE_GATE", gate)
	t.Setenv("PROOFBRIDGE_FIXTURE_LOG", proofLog)
	s, st, uid, agentID := newOrchestrationTestServer(t)
	s.cfg.Automation.Enabled = true
	s.cfg.Automation.PublicURL = "http://localhost:8080"
	var receipts <-chan automationSMTPReceipt
	s.cfg.Automation.SMTP, receipts = automationSMTPSink(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	expires := time.Now().Add(time.Hour)
	enroll := store.NewToken("enr")
	if err = st.CreateEnrollTokenForUser(ctx, enroll, uid, "real-proof-flow", &expires); err != nil {
		t.Fatal(err)
	}
	if _, err = st.PutAutomationSettings(ctx, uid, "account", json.RawMessage(`{"email":"proof-owner@example.test","completionEnabled":true,"attentionEnabled":true}`), 0); err != nil {
		t.Fatal(err)
	}
	token, _, err := s.personalAutomationToken(ctx, uid, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	s.cfg.Automation.PublicURL = server.URL
	bridgeCfg := config.Default()
	bridgeCfg.Bridge.HubURL = server.URL
	bridgeCfg.Bridge.Token = enroll
	bridgeCfg.Bridge.Name = "real-coq-fixture"
	bridgeCfg.Bridge.CWD = workspace
	bridgeCfg.Bridge.MachineIDFile = filepath.Join(root, "machine-id")
	bridgeCfg.Bridge.Runner = "codex-app-server"
	bridgeCfg.Bridge.CodexPath = cli
	bridgeCfg.Bridge.ClaudePath = filepath.Join(root, "uninstalled-claude")
	bridgeCfg.Bridge.ApprovalPolicy = "never"
	bridgeCfg.Bridge.Sandbox = "danger-full-access"
	bridgeCfg.Bridge.DurableTaskGraph = true
	bridgeCfg.Bridge.HeartbeatInterval.Duration = 100 * time.Millisecond
	if err = os.WriteFile(bridgeCfg.Bridge.MachineIDFile, []byte("machine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bridgeDone := make(chan error, 1)
	mailDone := make(chan struct{})
	dispatchDone := make(chan struct{})
	go func() { bridgeDone <- bridge.NewClient(&bridgeCfg, "test").Run(ctx) }()
	go func() { defer close(mailDone); s.runAutomationNotifications(ctx) }()
	// httptest intentionally omits Server.Run and its external metadata refresh.
	// Exercise durable ready-task recovery using the production dispatcher.
	go func() {
		defer close(dispatchDone)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.dispatchReadyTaskGraphsForAgent(ctx, agentID)
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-bridgeDone:
		case <-time.After(5 * time.Second):
			t.Error("Bridge did not stop")
		}
		select {
		case <-mailDone:
		case <-time.After(3 * time.Second):
			t.Error("mail worker did not stop")
		}
		select {
		case <-dispatchDone:
		case <-time.After(3 * time.Second):
			t.Error("durable dispatcher did not stop")
		}
		server.Close()
	})
	deadline := time.Now().Add(10 * time.Second)
	for !s.pool.AgentOnline(agentID) {
		if time.Now().After(deadline) {
			t.Fatal("real reverse Bridge did not connect")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var manifest []map[string]any
	ids := []string{}
	submissions := []string{}
	for index := 0; index < 2; index++ {
		_ = os.Remove(gate)
		session := automationSDKClient(t, server.URL+"/mcp", token)
		args := map[string]any{"clientRequestId": "real-proof-" + string(rune('a'+index)), "endpointId": agentID, "cwd": workspace, "prompt": "Read README.md and verify Goal.v with coqc.", "workerPair": "codex-codex", "profile": "formal-proof", "verificationMode": "agent", "maxTurns": 2, "monitoringMode": "background-mail"}
		manifest = append(manifest, args)
		preview := callAutomationSDK(t, session, "preview_run", argsWithoutRequestKey(args), false)
		if preview["startsWork"] != false {
			t.Fatal("preview starts work")
		}
		first := callAutomationSDK(t, session, "start_run", args, false)
		runID, _ := first["runId"].(string)
		subID, _ := first["submissionId"].(string)
		if runID == "" || subID == "" || first["state"] != "accepted" {
			t.Fatalf("missing accepted durable identity: %#v", first)
		}
		retry := callAutomationSDK(t, session, "start_run", args, false)
		if retry["runId"] != runID || retry["submissionId"] != subID || retry["replayed"] != true {
			t.Fatal("same-key retry duplicated task")
		}
		if err = session.Close(); err != nil {
			t.Fatal(err)
		}
		before, err := st.OrchestrationRunByID(ctx, runID, uid)
		if err != nil || !orchestrationActiveStatus(before.Status) {
			t.Fatalf("task ended before client exit gate released: %s,%v", before.Status, err)
		}
		if err = os.WriteFile(gate, []byte("closed\n"), 0600); err != nil {
			t.Fatal(err)
		}
		var submission store.AutomationSubmission
		deadline = time.Now().Add(30 * time.Second)
		for {
			submission, err = st.AutomationSubmissionByID(ctx, uid, subID)
			if err != nil {
				t.Fatal(err)
			}
			if submission.State == "terminal" {
				break
			}
			if time.Now().After(deadline) {
				events, _ := st.ListOrchestrationEvents(ctx, runID, 100)
				t.Fatalf("proof task did not settle: state=%s events=%#v", submission.State, events)
			}
			time.Sleep(25 * time.Millisecond)
		}
		var result store.AutomationResult
		if err = json.Unmarshal(submission.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.Execution.Status != store.OrchestrationCompleted || result.Verification.Verdict == nil || result.Verification.Verdict.Status != "pass" {
			t.Fatalf("execution/verification outcome: %+v", result)
		}
		if _, err = os.Stat(filepath.Join(workspace, "Goal.vo")); err != nil {
			t.Fatalf("real coqc artifact absent: %v", err)
		}
		select {
		case receipt := <-receipts:
			if !receipt.accepted {
				t.Fatal("local SMTP rejected report")
			}
			message, err := mail.ReadMessage(strings.NewReader(receipt.raw))
			if err != nil {
				t.Fatal(err)
			}
			kind, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
			if err != nil || kind != "multipart/alternative" {
				t.Fatal("completion report is not multipart alternative")
			}
			plain, err := multipart.NewReader(message.Body, params["boundary"]).NextPart()
			if err != nil || !strings.HasPrefix(plain.Header.Get("Content-Type"), "text/plain") {
				t.Fatal("completion report lacks readable text fallback")
			}
			body, err := io.ReadAll(plain)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), runID) || !strings.Contains(string(body), subID) {
				t.Fatalf("SMTP report belongs to different submission: %s", body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("real local SMTP report did not arrive after client exit")
		}
		reconnect := automationSDKClient(t, server.URL+"/mcp", token)
		restored := callAutomationSDK(t, reconnect, "get_run", map[string]any{"runId": runID, "submissionId": subID}, false)
		if restored["result"].(map[string]any)["submissionId"] != subID {
			t.Fatal("reconnected MCP lost immutable result")
		}
		_ = reconnect.Close()
		ids = append(ids, runID)
		submissions = append(submissions, subID)
		t.Logf("task=%d run=%s submission=%s execution=%s verification=%s promptSeq=%d; original MCP observer closed before coqc, local SMTP accepted report", index+1, runID, subID, result.Execution.Status, result.Verification.Verdict.Status, submission.PromptSeq)
	}
	if ids[0] == ids[1] || submissions[0] == submissions[1] {
		t.Fatal("explicit new requests reused task identity")
	}
	history, err := st.ListOrchestrationHistory(ctx, uid, store.OrchestrationHistoryFilter{})
	if err != nil || len(history.Runs) != 2 {
		t.Fatalf("expected exactly two runs: %d,%v", len(history.Runs), err)
	}
	proofRuns, err := os.ReadFile(proofLog)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	execVerifiers := 0
	for _, line := range strings.Split(strings.TrimSpace(string(proofRuns)), "\n") {
		var record struct {
			ExitCode      int    `json:"exitCode"`
			ManagedWorker string `json:"managedWorker"`
			Transport     string `json:"transport"`
		}
		if json.Unmarshal([]byte(line), &record) != nil || record.ExitCode != 0 || record.ManagedWorker != "1" {
			t.Fatalf("proof check or worker isolation failed: %s", line)
		}
		if record.Transport == "exec" {
			execVerifiers++
		}
		count++
	}
	if count < 6 || execVerifiers == 0 {
		t.Fatalf("not all worker and independent verifier checks executed real coqc: %d", count)
	}
	// The real batch entrypoint retries these exact accepted requests. No new
	// task/model budget is consumed; its final report must contain both results.
	manifestFile := filepath.Join(root, "manifest.json")
	manifestRaw, _ := json.Marshal(manifest)
	if err = os.WriteFile(manifestFile, manifestRaw, 0600); err != nil {
		t.Fatal(err)
	}
	batchScript, err := filepath.Abs(filepath.Join("..", "..", "scripts", "mcp-batch.py"))
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		batchCtx, batchCancel := context.WithTimeout(ctx, 10*time.Second)
		command := exec.CommandContext(batchCtx, "python3", batchScript, "--url", server.URL+"/mcp", "--manifest", manifestFile, "--max-wait-seconds", "5")
		command.Env = append(os.Environ(), "PROOFBRIDGE_MCP_TOKEN="+token)
		output, runErr := command.CombinedOutput()
		batchCancel()
		if runErr != nil {
			t.Fatalf("real batch script failed: %v: %s", runErr, output)
		}
		accepted := 0
		reported := false
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			var entry map[string]json.RawMessage
			if json.Unmarshal([]byte(line), &entry) != nil {
				t.Fatalf("invalid batch JSON line: %s", line)
			}
			if receipt, exists := entry["accepted"]; exists {
				var identity struct {
					RunID        string `json:"runId"`
					SubmissionID string `json:"submissionId"`
					Replayed     bool   `json:"replayed"`
				}
				if json.Unmarshal(receipt, &identity) != nil || accepted >= len(ids) || identity.RunID != ids[accepted] || identity.SubmissionID != submissions[accepted] || !identity.Replayed {
					t.Fatalf("batch retry changed identity: %s", receipt)
				}
				accepted++
			}
			if report, exists := entry["report"]; exists {
				var value struct {
					Results    []json.RawMessage `json:"results"`
					Incomplete []json.RawMessage `json:"incomplete"`
				}
				if json.Unmarshal(report, &value) != nil || len(value.Results) != 2 || len(value.Incomplete) != 0 {
					t.Fatalf("batch report incomplete: %s", report)
				}
				reported = true
			}
		}
		if accepted != 2 || !reported {
			t.Fatalf("batch output did not retain accepted IDs and results: %s", output)
		}
	}
	afterBatch, err := os.ReadFile(proofLog)
	if err != nil || string(afterBatch) != string(proofRuns) {
		t.Fatal("batch retries re-executed proof work")
	}
	select {
	case duplicate := <-receipts:
		t.Fatalf("batch retry duplicated completion mail: %v", duplicate.accepted)
	default:
	}
	t.Log("scripts/mcp-batch.py replayed the same two-task manifest twice, retained both IDs and both final reports, and executed zero additional proof subprocesses")
	version, err := exec.Command(coqc, "--version").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s; actual coqc subprocesses=%d, independent exec verifier calls=%d; CLI transport deterministic, no model call", strings.TrimSpace(string(version)), count, execVerifiers)
}
func argsWithoutRequestKey(source map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range source {
		if key != "clientRequestId" {
			out[key] = value
		}
	}
	return out
}

// This negative probe creates no Hub task and calls no model. It proves that
// the deterministic verifier cannot report pass for a rejected Coq source.
func TestAutomationProofFixtureReturnsContinueOnRealCompilerFailure(t *testing.T) {
	coqc, err := exec.LookPath("coqc")
	if err != nil {
		t.Skip("real coqc is required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required")
	}
	root := t.TempDir()
	gate := filepath.Join(root, "gate")
	log := filepath.Join(root, "proof.jsonl")
	if err = os.WriteFile(gate, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "Goal.v"), []byte("Theorem impossible : False.\nProof. exact I. Qed.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "automation-proof", "codex_fixture.py"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, fixture, "exec", "--json", "-")
	command.Dir = root
	command.Stdin = strings.NewReader("verify the project")
	command.Env = append(os.Environ(), "PROOFBRIDGE_FIXTURE_COQC="+coqc, "PROOFBRIDGE_FIXTURE_GATE="+gate, "PROOFBRIDGE_FIXTURE_LOG="+log, "PROOFBRIDGE_MANAGED_WORKER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture failed: %v: %s", err, output)
	}
	verdictFound := false
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		var entry struct {
			Type string `json:"type"`
			Item struct {
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			t.Fatalf("bad fixture JSON: %s", line)
		}
		if entry.Type == "item.completed" {
			var verdict struct {
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(entry.Item.Text), &verdict) != nil || verdict.Status != "continue" {
				t.Fatalf("rejected proof yielded pass: %s", entry.Item.Text)
			}
			verdictFound = true
		}
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		ExitCode int `json:"exitCode"`
	}
	if json.Unmarshal(raw, &actual) != nil || actual.ExitCode == 0 || !verdictFound {
		t.Fatalf("negative compiler probe was not observed: %s", raw)
	}
	t.Logf("real coqc rejected False proof with exit=%d; verifier fixture returned continue; zero Hub tasks or model calls", actual.ExitCode)
}
