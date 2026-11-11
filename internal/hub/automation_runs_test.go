package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func automationRunTestBridge(s *Server, agentID string) *BridgeConn {
	bridge := testBridgeConn(agentID, 32)
	bridge.capabilities = &protocol.BridgeCapabilities{AutomationInteractions: 1, ApprovalPolicy: "never", Sandbox: "danger-full-access", OrchestrationLiveInput: 1, Orchestration: map[string]protocol.BridgeCLICapability{"codex": {Available: true}, "claude": {Available: true}}}
	s.pool.RegisterAgent(bridge)
	return bridge
}
func automationRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestAutomationRunPreviewAndRetryIgnoreChangedDefaults(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	bridge := automationRunTestBridge(s, agentID)
	defer s.pool.UnregisterAgent(agentID, bridge)
	ctx := context.Background()
	request := automationRaw(t, map[string]any{"clientRequestId": "once", "prompt": "prove the requested lemma", "endpointId": agentID, "workerPair": "codex-codex"})
	preview, err := s.previewAutomationRun(ctx, uid, request)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(preview)
	if !strings.Contains(string(serialized), `"nativeDefault":true`) || strings.Contains(string(serialized), "prove the requested lemma") {
		t.Fatalf("preview missing native defaults or retained prompt: %s", serialized)
	}
	select {
	case frame := <-bridge.send:
		t.Fatalf("preview dispatched %s", frame.Type)
	default:
	}
	page, err := st.ListOrchestrationHistory(ctx, uid, store.OrchestrationHistoryFilter{})
	if err != nil || len(page.Runs) != 0 {
		t.Fatalf("preview created run: %#v, %v", page, err)
	}
	first, err := s.startAutomationRun(ctx, uid, request, "")
	if err != nil {
		t.Fatal(err)
	}
	response := first.(map[string]any)
	if response["state"] != "accepted" || response["promptSeq"].(int64) <= 0 {
		t.Fatalf("not durably accepted: %#v", response)
	}
	frame := <-bridge.send
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](frame)
	if err != nil || frame.Type != protocol.TypeOrchestrationStart || payload.RunID != response["runId"] {
		t.Fatalf("dispatch = %#v, %v", payload, err)
	}
	_, err = st.PutAutomationSettings(ctx, uid, "account", json.RawMessage(`{"defaultEndpointId":"removed","defaultPresetId":"removed"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	s.pool.UnregisterAgent(agentID, bridge)
	retry, err := s.startAutomationRun(ctx, uid, request, "")
	if err != nil {
		t.Fatalf("retry re-resolved changed defaults or offline endpoint: %v", err)
	}
	replay := retry.(map[string]any)
	if replay["runId"] != response["runId"] || replay["submissionId"] != response["submissionId"] || replay["replayed"] != true {
		t.Fatalf("retry created identity: %#v", replay)
	}
	changed := automationRaw(t, map[string]any{"clientRequestId": "once", "prompt": "different", "endpointId": agentID, "workerPair": "codex-codex"})
	if _, err = s.startAutomationRun(ctx, uid, changed, ""); err == nil || !strings.Contains(err.Error(), "REQUEST_CONFLICT") {
		t.Fatalf("key reuse = %v", err)
	}
	select {
	case frame := <-bridge.send:
		t.Fatalf("retry dispatched %s", frame.Type)
	default:
	}
}
func TestAutomationRunConcurrentStartDispatchesOnce(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	bridge := automationRunTestBridge(s, agentID)
	defer s.pool.UnregisterAgent(agentID, bridge)
	raw := automationRaw(t, map[string]any{"clientRequestId": "concurrent", "prompt": "verify", "endpointId": agentID})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.startAutomationRun(context.Background(), uid, raw, "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.ListOrchestrationHistory(context.Background(), uid, store.OrchestrationHistoryFilter{})
	if err != nil || len(page.Runs) != 1 || len(bridge.send) != 1 {
		t.Fatalf("concurrent submission duplicated run or dispatch: runs=%d frames=%d err=%v", len(page.Runs), len(bridge.send), err)
	}
}
func TestAutomationRunFollowupAndLiveInstructionRetainContext(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	bridge := automationRunTestBridge(s, agentID)
	defer s.pool.UnregisterAgent(agentID, bridge)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	live := automationRaw(t, map[string]any{"operation": "live_instruction", "runId": run.ID, "messageId": "native-input", "prompt": "also check the invariant"})
	if _, err := s.continueAutomationRun(ctx, uid, live); err != nil {
		t.Fatal(err)
	}
	frame := <-bridge.send
	input, err := protocol.Decode[protocol.OrchestrationInputPayload](frame)
	if err != nil || frame.Type != protocol.TypeOrchestrationInput || input.RunID != run.ID {
		t.Fatalf("live input made new context: %#v,%v", input, err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	// Original runtime instruction receipts remain addressable after run end.
	if _, err := s.continueAutomationRun(ctx, uid, live); err != nil {
		t.Fatal(err)
	}
	freshLive := automationRaw(t, map[string]any{"operation": "live_instruction", "runId": run.ID, "messageId": "later", "prompt": "new runtime input"})
	if _, err := s.continueAutomationRun(ctx, uid, freshLive); err == nil || !strings.Contains(err.Error(), "RUN_ENDED") {
		t.Fatalf("ended live instruction became follow-up: %v", err)
	}
	follow := automationRaw(t, map[string]any{"operation": "follow_up", "runId": run.ID, "clientRequestId": "follow", "prompt": "now prove the second lemma"})
	if _, err := s.continueAutomationRun(ctx, uid, follow); err != nil {
		t.Fatal(err)
	}
	frame = <-bridge.send
	next, err := protocol.Decode[protocol.OrchestrationStartPayload](frame)
	if err != nil || next.RunID != run.ID || !next.Resume {
		t.Fatalf("followup lost context: %#v,%v", next, err)
	}
	page, err := st.ListOrchestrationHistory(ctx, uid, store.OrchestrationHistoryFilter{})
	if err != nil || len(page.Runs) != 1 {
		t.Fatalf("followup created run: %d,%v", len(page.Runs), err)
	}
}
func TestAutomationRunTargetsMailAndBridgeGates(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	bridge := automationRunTestBridge(s, agentID)
	defer s.pool.UnregisterAgent(agentID, bridge)
	ctx := context.Background()
	raw := automationRaw(t, map[string]any{"prompt": "verify", "endpointId": agentID})
	bridge.capabilities.AutomationInteractions = 0
	if _, err := s.previewAutomationRun(ctx, uid, raw); err == nil || !strings.Contains(err.Error(), "UPGRADE_BRIDGE_REQUIRED") {
		t.Fatalf("old autoexecute bridge accepted: %v", err)
	}
	bridge.capabilities.AutomationInteractions = 1
	other, err := st.UpsertAgentForUser(ctx, uid, "other", "other-machine", "host", "instance", []string{"/other"})
	if err != nil {
		t.Fatal(err)
	}
	otherBridge := automationRunTestBridge(s, other.ID)
	defer s.pool.UnregisterAgent(other.ID, otherBridge)
	if _, err := s.previewAutomationRun(ctx, uid, json.RawMessage(`{"prompt":"verify"}`)); err == nil || !strings.Contains(err.Error(), "TARGET_AMBIGUOUS") {
		t.Fatalf("ambiguous endpoint selected: %v", err)
	}
	mailRaw := automationRaw(t, map[string]any{"prompt": "verify", "endpointId": agentID, "monitoringMode": "background-mail"})
	if _, err := s.previewAutomationRun(ctx, uid, mailRaw); err == nil || !strings.Contains(err.Error(), "NOTIFICATION_NOT_READY") {
		t.Fatalf("unconfigured background mail accepted: %v", err)
	}
	s.cfg.Automation.Enabled = true
	s.cfg.Automation.PublicURL = "http://localhost:8080"
	s.cfg.Automation.SMTP.Host = "localhost"
	s.cfg.Automation.SMTP.Port = 2525
	s.cfg.Automation.SMTP.From = "test@example.com"
	s.cfg.Automation.SMTP.AllowInsecure = true
	if _, err := st.PutAutomationSettings(ctx, uid, "account", json.RawMessage(`{"email":"recipient@example.com"}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.previewAutomationRun(ctx, uid, mailRaw); err != nil {
		t.Fatalf("absent notification flags did not use Store true defaults: %v", err)
	}
}
func TestAutomationPresetRejectsTaskSecretsForeignAndUnsupportedEffort(t *testing.T) {
	s, st, uid, _ := newOrchestrationTestServer(t)
	ctx := context.Background()
	for _, raw := range []string{`{"prompt":"do not retain"}`, `{"apiKey":"secret"}`, `{"workerProfilePresetIds":{"codex":{}}}`} {
		if _, err := s.saveAutomationPreset(ctx, uid, automationArguments{Name: "unsafe", Config: json.RawMessage(raw)}); err == nil {
			t.Fatalf("unsafe preset saved: %s", raw)
		}
	}
	other, err := st.UpsertUser(ctx, "other-preset-owner", "secret")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := st.CreateCLIConfigPreset(ctx, store.CLIConfigPreset{UserID: other.ID, CLI: "codex", Name: "foreign", Model: "gpt-5", BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	raw := automationRaw(t, map[string]any{"workerPair": "codex-codex", "workerProfilePresetIds": map[string]string{"codex-a": foreign.ID, "codex-b": foreign.ID}})
	if _, err = s.saveAutomationPreset(ctx, uid, automationArguments{Name: "foreign", Config: raw}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign model ref saved: %v", err)
	}
	own, err := st.CreateCLIConfigPreset(ctx, store.CLIConfigPreset{UserID: uid, CLI: "codex", Name: "own", Model: "gpt-5", BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	raw = automationRaw(t, map[string]any{"workerPair": "codex-codex", "workerProfilePresetIds": map[string]string{"codex-a": own.ID, "codex-b": own.ID}, "workerProfileEfforts": map[string]string{"codex-a": "invented"}})
	if _, err = s.saveAutomationPreset(ctx, uid, automationArguments{Name: "bad effort", Config: raw}); err == nil {
		t.Fatal("unsupported effort saved")
	}
}
