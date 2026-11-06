package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type automationAuthTransport struct{ token string }

func (t automationAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}
func automationSDKClient(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "proofbridge-integration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: automationAuthTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func callAutomationSDK(t *testing.T, session *mcp.ClientSession, name string, args any, wantError bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "proofbridge_" + name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if got.IsError != wantError {
		t.Fatalf("%s isError=%v, wanted %v: %v", name, got.IsError, wantError, got.Content)
	}
	raw, err := json.Marshal(got.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestAutomationAccessMCPProtocolAndRevocation(t *testing.T) {
	s, st, uid, _ := newOrchestrationTestServer(t)
	s.cfg.Automation.PublicURL = "https://lab.example.test"
	access := authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	token := access["token"].(string)
	if !strings.HasPrefix(token, "pb_mcp_") || !strings.Contains(access["codexConfig"].(string), "https://lab.example.test/mcp") {
		t.Fatal("missing ready-to-copy configuration")
	}
	again := authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	if again["token"] != token {
		t.Fatal("token changed on read")
	}
	record, err := st.GetAutomationToken(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if record.Ciphertext == token || strings.Contains(record.Ciphertext, token) {
		t.Fatal("token stored without protection")
	}
	otherPurpose := *s.cfg
	otherPurpose.Auth.JWTSecret = "another-key"
	other := NewServer(&otherPurpose, st, BuildInfo{Version: "test"})
	if _, err = other.openAutomationToken(record.Ciphertext); err == nil {
		t.Fatal("wrong key decrypted PAT")
	}
	server := httptest.NewServer(s.httpSrv.Handler)
	defer server.Close()
	session := automationSDKClient(t, server.URL+"/mcp", token)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 20 {
		t.Fatalf("tool count %d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Name == "proofbridge_respond_run_interaction" && tool.Annotations.ReadOnlyHint {
			t.Fatal("interaction mutation marked read only")
		}
	}
	contextResult := callAutomationSDK(t, session, "get_context", map[string]any{}, false)
	raw, _ := json.Marshal(contextResult)
	if strings.Contains(string(raw), token) {
		t.Fatal("PAT leaked through tool result")
	}
	updated := callAutomationSDK(t, session, "update_preferences", map[string]any{"expectedVersion": 0, "data": map[string]any{"email": "me@example.test", "monitoringMode": "interactive"}}, false)
	if updated["version"] != float64(1) {
		t.Fatalf("version %v", updated)
	}
	conflict := callAutomationSDK(t, session, "update_preferences", map[string]any{"expectedVersion": 0, "data": map[string]any{"email": "other@example.test"}}, true)
	if conflict["error"].(map[string]any)["code"] != "REQUEST_CONFLICT" {
		t.Fatal("lost version conflict")
	}
	callAutomationSDK(t, session, "update_preferences", map[string]any{"expectedVersion": 1, "data": map[string]any{"admin": true}}, true)
	callAutomationSDK(t, session, "get_context", map[string]any{"token": "forbidden"}, true)
	callAutomationSDK(t, session, "get_run", map[string]any{"runId": nil}, true)
	// PAT is never a website-session or token-management credential.
	req, _ := http.NewRequest("POST", server.URL+"/api/automation/access/rotate", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("PAT authorized management: %d", resp.StatusCode)
	}
	revoked := authedJSON(t, s, uid, "POST", "/api/automation/access/revoke", map[string]any{}, 200)
	if revoked["token"] != nil || revoked["revoked"] != true || revoked["codexConfig"] != "" {
		t.Fatal("revocation response does not match full access shape")
	}
	if _, err = session.ListTools(context.Background(), nil); err == nil {
		t.Fatal("revoked PAT accepted existing client")
	}
	again = authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	if again["token"] != nil {
		t.Fatal("reading access reissued revoked PAT")
	}
	rotated := authedJSON(t, s, uid, "POST", "/api/automation/access/rotate", map[string]any{}, 200)
	if rotated["token"] == token || rotated["token"] == nil {
		t.Fatal("rotation did not replace PAT")
	}
	fresh := automationSDKClient(t, server.URL+"/mcp", rotated["token"].(string))
	callAutomationSDK(t, fresh, "get_context", map[string]any{}, false)
	// Cookie and ordinary access JWT are not accepted by /mcp.
	jwt, _, err := s.signer.Sign(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"", jwt} {
		req, _ = http.NewRequest("POST", server.URL+"/mcp", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+credential)
		req.AddCookie(&http.Cookie{Name: accessCookieName, Value: jwt})
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("wrong credential accepted: %d", resp.StatusCode)
		}
	}
}
func TestAutomationObservationOwnershipPagingAndCancellation(t *testing.T) {
	s, st, uid, aid := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, uid, aid)
	for i := 0; i < 3; i++ {
		content := "message"
		if i == 1 {
			content = strings.Repeat("中", 50000)
		}
		if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "turn.output", Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.automationRunEvents(ctx, uid, run.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	page := result.(automationEventPage)
	if len(page.Events) != 2 || page.NextSeq != 2 || !page.HasMore || len(page.TruncatedSeqs) != 1 {
		t.Fatalf("bad bounded page: %+v", page)
	}
	second, err := s.automationRunEvents(ctx, uid, run.ID, page.NextSeq, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.(automationEventPage).Events[0].Seq != 3 {
		t.Fatal("cursor skipped or repeated event")
	}
	if _, err = s.automationRunEvents(ctx, "foreign", run.ID, 0, 2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ownership error: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.waitAutomationRun(canceled, uid, automationArguments{RunID: run.ID, AfterSeq: 3}); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait canceled: %v", err)
	}
	current, err := st.OrchestrationRunByID(ctx, run.ID, uid)
	if err != nil || current.Status != run.Status {
		t.Fatal("observation cancellation changed run")
	}
	got, err := s.waitAutomationRun(ctx, uid, automationArguments{RunID: run.ID, AfterSeq: 3, TimeoutMS: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["timedOut"] != true {
		t.Fatal("wait timeout not reported")
	}
	if _, err = s.automationRunResult(ctx, "foreign", run.ID, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign result visible")
	}
}
func TestAutomationPublicURLAndPreferencesBoundaries(t *testing.T) {
	s, _, uid, aid := newOrchestrationTestServer(t)
	for _, raw := range []string{"http://lab.test", "https://user:pass@lab.test", "https://lab.test?token=x", "https://lab.test/#fragment"} {
		s.cfg.Automation.PublicURL = raw
		if _, err := s.automationPublicURL(); err == nil {
			t.Fatalf("invalid URL accepted: %s", raw)
		}
	}
	s.cfg.Automation.PublicURL = "https://lab.test"
	for _, raw := range []string{`{"email":"Name <me@example.test>"}`, `{"email":"a\\r\\nBcc:x@example.test"}`, `{"defaultEndpointId":"foreign"}`, `{"completionEnabled":null}`, `{"completionEnabled":"true"}`} {
		if _, err := s.putAutomationPreferences(context.Background(), uid, "", json.RawMessage(raw), 0); err == nil {
			t.Fatalf("invalid preferences accepted: %s", raw)
		}
	}
	if err := s.validateAutomationScope(context.Background(), uid, "workspace:"+aid+":/project"); err != nil {
		t.Fatal(err)
	}
	if err := s.validateAutomationScope(context.Background(), uid, "workspace:foreign:/project"); err == nil {
		t.Fatal("foreign workspace accepted")
	}
	// A proxy-controlled Host never appears in the generated configuration.
	req := httptest.NewRequest("GET", "https://attacker.test/api/automation/access", nil)
	response := httptest.NewRecorder()
	s.handleAutomationAccess(response, req, uid)
	data, _ := io.ReadAll(response.Result().Body)
	if !strings.Contains(string(data), "https://lab.test") || strings.Contains(string(data), "attacker.test") {
		t.Fatal("request host influenced copyable configuration")
	}
}

func TestAutomationRevokeBeforeFirstReadStaysRevoked(t *testing.T) {
	s, st, uid, _ := newOrchestrationTestServer(t)
	s.cfg.Automation.PublicURL = "https://lab.example.test"
	revoked := authedJSON(t, s, uid, "POST", "/api/automation/access/revoke", map[string]any{}, 200)
	if revoked["token"] != nil || revoked["revoked"] != true {
		t.Fatal("first revoke issued a token")
	}
	record, err := st.GetAutomationToken(context.Background(), uid)
	if err != nil || record.RevokedAt == 0 {
		t.Fatal("revocation did not persist")
	}
	read := authedJSON(t, s, uid, "GET", "/api/automation/access", nil, 200)
	if read["token"] != nil {
		t.Fatal("read reactivated revoked token")
	}
}
func TestAutomationSmallEventsNeverExposeNativeResume(t *testing.T) {
	s, st, uid, aid := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, aid)
	_, err := st.AddOrchestrationEvent(context.Background(), store.OrchestrationEvent{RunID: run.ID, Kind: "run.end", Status: store.OrchestrationCompleted,
		RunEndData: &protocol.RunEndData{CodexThreadID: "private-thread-id", ClaudeSessionID: "private-session-id", NativeResume: []protocol.NativeResumeInfo{{ID: "private-native", Command: "codex resume private-thread-id"}}, VerifierVerdict: &protocol.VerifierVerdict{Status: "continue"}},
		Data:       map[string]any{"sessionId": "private-session-id", "codexThreadId": "private-thread-id"}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.automationRunEvents(context.Background(), uid, run.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "nativeResume") {
		t.Fatalf("native handles escaped projection: %s", raw)
	}
	if !strings.Contains(string(raw), `"status":"continue"`) {
		t.Fatal("projection removed independent verification")
	}
}

func TestAutomationPreferenceDefaultsPreserveAccountMonitoring(t *testing.T) {
	s, _, uid, aid := newOrchestrationTestServer(t)
	s.cfg.Automation.PublicURL = "https://lab.example.test"
	s.cfg.Automation.SMTP.Host = "smtp.example.test"
	s.cfg.Automation.SMTP.From = "sender@example.test"
	saved, err := s.putAutomationPreferences(context.Background(), uid, "account", json.RawMessage(`{"email":"owner@example.test","monitoringMode":"background-mail"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	var account map[string]any
	_ = json.Unmarshal(saved.Data, &account)
	if account["completionEnabled"] != true || account["attentionEnabled"] != true {
		t.Fatal("response and sender defaults disagree")
	}
	workspace, err := s.automationPreferences(context.Background(), uid, "workspace:"+aid+":/project")
	if err != nil {
		t.Fatal(err)
	}
	if string(workspace.Data) != "{}" {
		t.Fatal("unset workspace silently overrides account defaults")
	}
	bridge := automationRunTestBridge(s, aid)
	defer s.pool.UnregisterAgent(aid, bridge)
	plan, err := s.resolveAutomationRun(context.Background(), uid, json.RawMessage(`{"endpointId":"`+aid+`","cwd":"/project","prompt":"verify"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MonitoringMode != "background-mail" {
		t.Fatal("account monitoring mode was overridden by missing workspace settings")
	}
}
