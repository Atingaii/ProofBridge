package hub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/store"
)

func officialModelTestBridge(s *Server, agentID string, version int) *BridgeConn {
	// A missing fake RPC response must fail quickly instead of spending the
	// production 90-second probe budget on a fixture mistake.
	handler := s.httpSrv.Handler
	s.httpSrv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	conn := &BridgeConn{agentID: agentID, capabilities: &protocol.BridgeCapabilities{
		ConfigSwitcher:         &protocol.CLIConfigSwitcherCapability{Version: version, KeyID: "official-key", CLIs: []string{"codex", "claude"}},
		IsolatedWorkerProfiles: true,
		Orchestration:          map[string]protocol.BridgeCLICapability{"codex": {Available: true, BrowserApproval: true}, "claude": {Available: true, BrowserApproval: true}},
	}, wsSender: wsSender{send: make(chan protocol.Envelope, 4), done: make(chan struct{})}}
	s.pool.RegisterAgent(conn)
	return conn
}

func officialTestModels() []protocol.OfficialCLIModelMetadata {
	return []protocol.OfficialCLIModelMetadata{
		{Model: "native-a", DisplayName: "Native A", SupportedReasoningEfforts: []string{"medium", "high", "ultra"}, DefaultReasoningEffort: "medium", IsDefault: true},
		{Model: "native-b", DisplayName: "Native B", SupportedReasoningEfforts: []string{"low", "high"}, DefaultReasoningEffort: "low"},
	}
}

// Only the catalog RPC is answered; the subsequent start remains available for
// assertions. No native model or lifecycle refresh is started by these tests.
func answerOfficialCatalog(s *Server, conn *BridgeConn, models []protocol.OfficialCLIModelMetadata) <-chan error {
	done := make(chan error, 1)
	go func() {
		select {
		case env := <-conn.send:
			request, err := protocol.Decode[protocol.CLIConfigRequest](env)
			if err != nil || env.Type != protocol.TypeCLIConfigOfficialModels || request.CLI != "codex" || encryptedSecretPresent(request.Secret) {
				done <- errors.New("expected a credential-free official catalog RPC")
				if request.RequestID != "" {
					s.handleCLIConfigResult(conn.agentID, protocol.MustEnvelope(protocol.TypeCLIConfigResult, "", protocol.CLIConfigResult{RequestID: request.RequestID, OK: false}))
				}
				return
			}
			s.handleCLIConfigResult(conn.agentID, protocol.MustEnvelope(protocol.TypeCLIConfigResult, "", protocol.CLIConfigResult{RequestID: request.RequestID, CLI: "codex", OK: true, OfficialModels: models}))
			done <- nil
		case <-time.After(3 * time.Second):
			done <- errors.New("official directory request was not dispatched")
		}
	}()
	return done
}

func TestOfficialCLIModelsEndpointAuthorizationAndVersion(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	other, err := st.UpsertUser(context.Background(), "other-official", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agents/" + agentID + "/cli-config/official-models?cli=codex"
	getJSON(t, s, other.ID, path, http.StatusNotFound)
	getJSON(t, s, uid, strings.Replace(path, "cli=codex", "cli=claude", 1), http.StatusBadRequest)
	s.cfg.Hub.FeatureRollouts[rollout.FeatureOfficialCodexWorkerModels] = "off"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	getJSON(t, s, uid, path, http.StatusForbidden)
	s.cfg.Hub.FeatureRollouts[rollout.FeatureOfficialCodexWorkerModels] = "all"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	conn.capabilities.ConfigSwitcher.Version = 2
	getJSON(t, s, uid, path, http.StatusConflict)
	conn.capabilities.ConfigSwitcher.Version = 3
	done := answerOfficialCatalog(s, conn, officialTestModels())
	body := getJSON(t, s, uid, path, http.StatusOK)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	models := body["models"].([]any)
	if len(models) != 2 || models[0].(map[string]any)["model"] != "native-a" || strings.Contains(toJSON(body), "official-key") {
		t.Fatalf("official directory = %#v", body)
	}
	s.pool.UnregisterAgent(agentID, conn)
	getJSON(t, s, uid, path, http.StatusConflict)
}

func TestOfficialCLIModelsCanceledRequestAndAgentCorrelation(t *testing.T) {
	s, _, _, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.loadOfficialCLIModels(ctx, agentID); done <- err }()
	env := <-conn.send
	request, err := protocol.Decode[protocol.CLIConfigRequest](env)
	if err != nil {
		t.Fatal(err)
	}
	s.handleCLIConfigResult("foreign-agent", protocol.MustEnvelope(protocol.TypeCLIConfigResult, "", protocol.CLIConfigResult{RequestID: request.RequestID, OK: true, OfficialModels: officialTestModels()}))
	s.cliConfigMu.Lock()
	_, stillPending := s.cliConfigPending[request.RequestID]
	s.cliConfigMu.Unlock()
	if !stillPending {
		t.Fatal("foreign agent completed official directory request")
	}
	cancel()
	err = <-done
	var relay *workerProfileError
	if !errors.As(err, &relay) || relay.code != "BRIDGE_TIMEOUT" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	s.cliConfigMu.Lock()
	_, stillPending = s.cliConfigPending[request.RequestID]
	s.cliConfigMu.Unlock()
	if stillPending {
		t.Fatal("canceled official request remained pending")
	}
}

func TestOfficialCLIModelsDirectoryRejectsEmptyAmbiguousMetadata(t *testing.T) {
	for _, models := range [][]protocol.OfficialCLIModelMetadata{
		nil,
		{{Model: "duplicate"}, {Model: " duplicate "}},
		{{Model: "valid", SupportedReasoningEfforts: []string{"high"}, DefaultReasoningEffort: "low"}},
		{{Model: "valid", SupportedReasoningEfforts: []string{"high", " high "}}},
	} {
		if _, err := validateOfficialModelDirectory(models); err == nil {
			t.Fatalf("accepted invalid metadata %#v", models)
		}
	}
	models, err := validateOfficialModelDirectory([]protocol.OfficialCLIModelMetadata{{Model: "non-reasoning"}})
	if err != nil || len(models[0].SupportedReasoningEfforts) != 0 {
		t.Fatalf("non-reasoning model = %#v, %v", models, err)
	}
}

func TestOfficialWorkerModelsPersistAcrossFollowUpAndCanClear(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	done := answerOfficialCatalog(s, conn, officialTestModels())
	body := createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "independent official models", "workerPair": "codex-codex", "maxTurns": 2,
		"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
		"workerProfileModels":    map[string]string{"codex-a": "native-a", "codex-b": "native-b"},
		"workerProfileEfforts":   map[string]string{"codex-a": "ultra", "codex-b": "low"},
	}, http.StatusCreated)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runID := body["run"].(map[string]any)["id"].(string)
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](<-conn.send)
	if err != nil {
		t.Fatal(err)
	}
	a, b := payload.WorkerProfiles["codex-a"], payload.WorkerProfiles["codex-b"]
	if a.Model != "native-a" || a.ReasoningEffort != "ultra" || b.Model != "native-b" || b.ReasoningEffort != "low" || !a.OfficialLogin || a.ReasoningDefault != "medium" {
		t.Fatalf("official bindings = %#v", payload.WorkerProfiles)
	}
	stored, err := st.OrchestrationWorkerProfiles(context.Background(), runID)
	if err != nil || stored["codex-a"].Model != "native-a" {
		t.Fatalf("stored official bindings = %#v, %v", stored, err)
	}
	if err := st.UpdateOrchestrationRunStatus(context.Background(), runID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	continueOrchestration(t, s, uid, runID, map[string]any{"prompt": "reuse bindings"}, http.StatusOK)
	payload, err = protocol.Decode[protocol.OrchestrationStartPayload](<-conn.send)
	if err != nil || payload.RunID != runID || payload.WorkerProfiles["codex-a"].ReasoningEffort != "ultra" || payload.WorkerProfiles["codex-b"].Model != "native-b" {
		t.Fatalf("continued official bindings = %#v, %v", payload, err)
	}
	if err := st.UpdateOrchestrationRunStatus(context.Background(), runID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	conn.capabilities.ConfigSwitcher.Version = 2
	continueOrchestration(t, s, uid, runID, map[string]any{"prompt": "cannot silently ignore"}, http.StatusConflict)
	continueOrchestration(t, s, uid, runID, map[string]any{"prompt": "clear bindings", "workerProfilePresetIds": map[string]string{}}, http.StatusOK)
	payload, err = protocol.Decode[protocol.OrchestrationStartPayload](<-conn.send)
	if err != nil || len(payload.WorkerProfiles) != 0 {
		t.Fatalf("cleared official bindings = %#v, %v", payload, err)
	}
}

func TestOfficialWorkerModelOverridesRejectInvalidSelections(t *testing.T) {
	s, _, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	for _, selection := range []struct{ model, effort string }{
		{"unknown", ""}, {"native-b", "ultra"}, {"native-a", "browser-invented"},
	} {
		done := answerOfficialCatalog(s, conn, officialTestModels())
		createOrchestrationHTTP(t, s, uid, map[string]any{
			"agentId": agentID, "prompt": "reject invalid selection", "workerPair": "codex-codex",
			"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
			"workerProfileModels":    map[string]string{"codex-a": selection.model},
			"workerProfileEfforts":   map[string]string{"codex-a": selection.effort},
		}, http.StatusBadRequest)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for _, models := range []map[string]string{
		{"claude-a": "native-a"}, {"codex-a": "native-a", " codex-a ": "native-b"}, {"codex-a": ""},
	} {
		createOrchestrationHTTP(t, s, uid, map[string]any{
			"agentId": agentID, "prompt": "reject invalid override map", "workerPair": "codex-codex",
			"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
			"workerProfileModels":    models,
		}, http.StatusBadRequest)
	}
	if _, err := normalizeOfficialWorkerModels(map[string]string{"codex-a": "native-a"}, map[string]string{"codex-a": "saved-third-party"}); err == nil {
		t.Fatal("model override changed third-party saved model")
	}
	createOrchestrationHTTP(t, s, uid, map[string]any{"agentId": agentID, "prompt": "reject isolated override", "workerProfileModels": map[string]string{"codex-a": "native-a"}}, http.StatusBadRequest)
	s.cfg.Hub.FeatureRollouts[rollout.FeatureOfficialCodexWorkerModels] = "off"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "blocked by feature policy", "workerPair": "codex-codex",
		"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
		"workerProfileModels":    map[string]string{"codex-a": "native-a"},
	}, http.StatusForbidden)
	s.cfg.Hub.FeatureRollouts[rollout.FeatureOfficialCodexWorkerModels] = "all"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	conn.capabilities.ConfigSwitcher.Version = 2
	createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "old Bridge rejects explicit parameters", "workerPair": "codex-codex",
		"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
		"workerProfileModels":    map[string]string{"codex-a": "native-a"},
	}, http.StatusConflict)
	createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "legacy official defaults compatible", "workerPair": "codex-codex",
		"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
	}, http.StatusCreated)
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](<-conn.send)
	if err != nil || payload.WorkerProfiles["codex-a"].Model != "" || payload.WorkerProfiles["codex-a"].ReasoningEffort != "" {
		t.Fatalf("legacy official bindings = %#v, %v", payload, err)
	}
}

func TestOfficialCLIModelsRejectsBridgeFailure(t *testing.T) {
	s, _, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	done := answerOfficialCatalog(s, conn, nil)
	getJSON(t, s, uid, "/api/agents/"+agentID+"/cli-config/official-models?cli=codex", http.StatusBadGateway)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOfficialWorkerOverridesRequirePresetSelectionOnContinue(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	continueOrchestration(t, s, uid, run.ID, map[string]any{"prompt": "no silent override", "workerProfileModels": map[string]string{"codex-a": "native-a"}}, http.StatusBadRequest)
}

func TestOfficialWorkerModelsMixedPairAndModelDefault(t *testing.T) {
	s, _, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	done := answerOfficialCatalog(s, conn, officialTestModels())
	createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "mixed pair uses native model default", "workerPair": "claude-codex",
		"workerProfilePresetIds": map[string]string{"codex": officialLoginPresetID, "claude": officialLoginPresetID},
		"workerProfileModels":    map[string]string{"codex": "native-a"},
	}, http.StatusCreated)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](<-conn.send)
	if err != nil || payload.WorkerProfiles["codex"].Model != "native-a" || payload.WorkerProfiles["codex"].ReasoningEffort != "medium" || payload.WorkerProfiles["claude"].Model != "" {
		t.Fatalf("mixed pair default parameters = %#v, %v", payload, err)
	}
	createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "Claude remains unchanged", "workerPair": "claude-codex",
		"workerProfilePresetIds": map[string]string{"codex": officialLoginPresetID, "claude": officialLoginPresetID},
		"workerProfileModels":    map[string]string{"claude": "native-a"},
	}, http.StatusBadRequest)
}

func TestOfficialWorkerModelsSameModelIndependentEfforts(t *testing.T) {
	s, _, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	done := answerOfficialCatalog(s, conn, officialTestModels())
	profiles, err := s.resolveWorkerProfiles(context.Background(), uid, agentID, "codex-codex",
		map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
		map[string]string{"codex-a": "high", "codex-b": "ultra"},
		map[string]string{"codex-a": "native-a", "codex-b": "native-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if profiles["codex-a"].ReasoningEffort != "high" || profiles["codex-b"].ReasoningEffort != "ultra" {
		t.Fatalf("same-model independent efforts = %#v", profiles)
	}
}

func TestOfficialWorkerEffortFeatureGateNormalizesSlots(t *testing.T) {
	s, _, uid, agentID := newOrchestrationTestServer(t)
	conn := officialModelTestBridge(s, agentID, 3)
	defer s.pool.UnregisterAgent(agentID, conn)
	s.cfg.Hub.FeatureRollouts[rollout.FeatureOfficialCodexWorkerModels] = "off"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	createOrchestrationHTTP(t, s, uid, map[string]any{
		"agentId": agentID, "prompt": "cannot bypass policy through padded slot", "workerPair": "codex-codex",
		"workerProfilePresetIds": map[string]string{"codex-a": officialLoginPresetID, "codex-b": officialLoginPresetID},
		"workerProfileEfforts":   map[string]string{" codex-a ": "high"},
	}, http.StatusForbidden)
}

func TestOfficialCLIModelsSharedSizeBoundaries(t *testing.T) {
	model := strings.Repeat("m", protocol.MaxOfficialModelIDBytes)
	display := strings.Repeat("n", protocol.MaxOfficialModelDisplayNameBytes)
	effort := strings.Repeat("e", protocol.MaxOfficialReasoningEffortBytes)
	metadata := protocol.OfficialCLIModelMetadata{Model: model, DisplayName: display, SupportedReasoningEfforts: []string{effort}, DefaultReasoningEffort: effort}
	models, err := validateOfficialModelDirectory([]protocol.OfficialCLIModelMetadata{metadata})
	if err != nil || len(models) != 1 {
		t.Fatalf("native boundary metadata rejected: %#v, %v", models, err)
	}
	selected, err := normalizeOfficialWorkerModels(map[string]string{"codex-a": model}, map[string]string{"codex-a": officialLoginPresetID})
	if err != nil || selected["codex-a"] != model {
		t.Fatalf("native boundary model selection rejected: %#v, %v", selected, err)
	}
	if _, err := normalizeOfficialWorkerModels(map[string]string{"codex-a": model + "x"}, map[string]string{"codex-a": officialLoginPresetID}); err == nil {
		t.Fatal("oversized official model selection accepted")
	}
	for _, tooLarge := range []protocol.OfficialCLIModelMetadata{
		{Model: model + "x"},
		{Model: "valid", DisplayName: display + "x"},
		{Model: "valid", SupportedReasoningEfforts: []string{effort + "x"}},
	} {
		if _, err := validateOfficialModelDirectory([]protocol.OfficialCLIModelMetadata{tooLarge}); err == nil {
			t.Fatalf("oversized native metadata accepted: %#v", tooLarge)
		}
	}
}
