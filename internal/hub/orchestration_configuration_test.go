package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestOrchestrationDetailProjectsAcceptedConfiguration(t *testing.T) {
	s, st, uid, aid := newOrchestrationTestServer(t)
	ctx := context.Background()
	configuration := json.RawMessage(`{"workerPair":"codex-codex","maxTurns":6,"profile":"formal-proof","secret":"must-not-return","workers":{"codex-a":{"cli":"codex","model":"selected-model","reasoningEffort":"high","nativeDefault":false,"credentialCiphertext":"must-not-return"},"codex-b":{"cli":"codex","nativeDefault":true}}}`)
	sub, _, err := st.ReserveAutomationSubmission(ctx, uid, "start_run", "snapshot-view", "digest", "interactive", configuration)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{UserID: uid, AgentID: aid, Mode: "collaboration", Prompt: "verify", AutomationSubmissionID: sub.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "verify"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/orchestrations/"+run.ID, nil)
	req.SetPathValue("runID", run.ID)
	response := httptest.NewRecorder()
	s.handleGetOrchestration(response, req, uid)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	var result struct {
		Configuration map[string]any `json:"executionConfiguration"`
		SubmissionID  string         `json:"submissionId"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SubmissionID != sub.ID || result.Configuration["workerPair"] != "codex-codex" || result.Configuration["maxTurns"] != float64(6) {
		t.Fatal("accepted snapshot was replaced by draft defaults")
	}
	if strings.Contains(response.Body.String(), "must-not-return") {
		t.Fatal("private configuration field leaked")
	}
	workers := result.Configuration["workers"].(map[string]any)
	if workers["codex-a"].(map[string]any)["model"] != "selected-model" || workers["codex-b"].(map[string]any)["nativeDefault"] != true {
		t.Fatal("worker choices lost")
	}
	denied := httptest.NewRecorder()
	s.handleGetOrchestration(denied, req, "other-user")
	if denied.Code != 404 {
		t.Fatal("foreign task detail visible")
	}
}
func TestOrchestrationDetailLegacyNativeConfiguration(t *testing.T) {
	s, st, uid, aid := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, aid)
	raw, id, err := s.orchestrationExecutionConfiguration(context.Background(), uid, run)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatal("invented submission identity")
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	if cfg["endpointId"] != aid || cfg["cwd"] != run.CWD {
		t.Fatal("legacy run choices lost")
	}
	workers := cfg["workers"].(map[string]any)
	if workers["codex"].(map[string]any)["nativeDefault"] != true {
		t.Fatal("legacy native model guessed")
	}
}
