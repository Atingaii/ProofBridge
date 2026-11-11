package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/hub"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// One request crosses the real Hub WebSockets, the Bridge manager, the Claude
// permission socket, and the project MCP helper. No external model is called.
func TestClaudeAskUserQuestionMCPThroughHubAndBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Default()
	cfg.Hub.DBPath = t.TempDir() + "/hub.db"
	cfg.Auth.JWTSecret = "native-user-input-integration-secret-32-bytes"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := st.UpsertUser(ctx, "native-test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "test-bridge", "native-machine", "localhost", "instance", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{UserID: user.ID, AgentID: agent.ID, Title: "native test", Mode: "collaboration", Prompt: "question", MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	enroll := store.NewToken("enr")
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollTokenForUser(ctx, enroll, user.ID, "test", &expires); err != nil {
		t.Fatal(err)
	}
	server := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test"})
	// Keep the real route/middleware stack without Hub.Run's background
	// modelcatalog.Default refresh, which would make unrelated pricing tests
	// depend on network availability and test order.
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	base := httpServer.URL
	wsBase := "ws" + strings.TrimPrefix(base, "http")

	bridgeWS, _, err := websocket.DefaultDialer.Dial(wsBase+"/api/agents/connect?token="+url.QueryEscape(enroll), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bridgeWS.Close()
	register := protocol.RegisterPayload{MachineID: "native-machine", Name: "test-bridge", Instance: "instance", Capabilities: &protocol.BridgeCapabilities{NativeUserInput: map[string]protocol.NativeUserInputCapability{"claude": {Version: 3, Available: true, Blocking: true}}}}
	if err := bridgeWS.WriteJSON(protocol.MustEnvelope(protocol.TypeRegister, "", register)); err != nil {
		t.Fatal(err)
	}
	var registered protocol.Envelope
	_ = bridgeWS.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err := bridgeWS.ReadJSON(&registered); err != nil || registered.Type != protocol.TypeRegistered {
		t.Fatalf("Bridge registration = %#v, err=%v", registered, err)
	}
	_ = bridgeWS.SetReadDeadline(time.Time{})

	manager := NewOrchestrationManager(&cfg)
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	bridgeClient := &Client{orchestrations: manager}
	transportDone := make(chan error, 2)
	go func() {
		for {
			select {
			case frame := <-out:
				if err := bridgeWS.WriteJSON(frame); err != nil {
					transportDone <- err
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for {
			var frame protocol.Envelope
			if err := bridgeWS.ReadJSON(&frame); err != nil {
				transportDone <- err
				return
			}
			bridgeClient.handleEnvelope(ctx, frame, out)
		}
	}()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	login, err := httpClient.Post(base+"/api/login", "application/json", strings.NewReader(`{"username":"native-test","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", login.StatusCode)
	}
	parsedBase, _ := url.Parse(base)
	wsHeader := http.Header{}
	for _, cookie := range jar.Cookies(parsedBase) {
		wsHeader.Add("Cookie", cookie.String())
	}
	browserWS, _, err := websocket.DefaultDialer.Dial(wsBase+"/ws/orchestrations?runId="+url.QueryEscape(run.ID), wsHeader)
	if err != nil {
		t.Fatal(err)
	}
	defer browserWS.Close()
	readBrowser := func(want string) protocol.Envelope {
		t.Helper()
		for {
			_ = browserWS.SetReadDeadline(time.Now().Add(15 * time.Second))
			var frame protocol.Envelope
			if err := browserWS.ReadJSON(&frame); err != nil {
				t.Fatalf("read browser %s: %v", want, err)
			}
			if frame.Type == want {
				return frame
			}
		}
	}
	if err := browserWS.WriteJSON(protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "initial", ClientVersion: 3})); err != nil {
		t.Fatal(err)
	}
	initial := readBrowser(protocol.TypeUserInputSnapshot)
	initialPayload, err := protocol.Decode[protocol.UserInputSnapshotPayload](initial)
	if err != nil || initialPayload.SyncID != "initial" || len(initialPayload.Requests) != 0 {
		t.Fatalf("initial snapshot = %#v, err=%v", initialPayload, err)
	}

	socketPath := t.TempDir() + "/approval.sock"
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	approval := &claudeApprovalServer{userInput: orchestrationUserInputRequester{manager: manager, runID: run.ID, executionID: "attempt", workerID: "claude", cli: "claude"}}
	go serveClaudeApprovalSocket(ctx, listener, approval)
	params := map[string]any{"name": "browser_approval", "arguments": map[string]any{"tool_name": "AskUserQuestion", "tool_input": map[string]any{"questions": []any{map[string]any{"question": "Pick", "header": "Choice", "options": []any{map[string]any{"label": "Yes", "description": "Continue"}}, "multiSelect": false}}}}}
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	mcpDone := make(chan []byte, 1)
	go func() {
		var result bytes.Buffer
		_ = RunClaudeApprovalMCP(socketPath, bytes.NewReader(append(line, '\n')), &result)
		mcpDone <- result.Bytes()
	}()
	requestFrame := readBrowser(protocol.TypeUserInputRequest)
	request, err := protocol.Decode[protocol.UserInputRequestPayload](requestFrame)
	if err != nil || request.CLI != "claude" || request.RunID != run.ID || len(request.Questions) != 1 {
		t.Fatalf("browser question = %#v, err=%v", request, err)
	}
	response := protocol.UserInputResponsePayload{RunID: run.ID, RequestID: request.RequestID, SubmissionID: "browser-answer", Answers: map[string]protocol.UserInputAnswer{"q0": {OptionIDs: []string{"Yes"}}}}
	if err := browserWS.WriteJSON(protocol.MustEnvelope(protocol.TypeUserInputResponse, "", response)); err != nil {
		t.Fatal(err)
	}
	seenReceipt, seenClosed := false, false
	for i := 0; i < 4 && (!seenReceipt || !seenClosed); i++ {
		frame := readBrowser(protocol.TypeUserInputStatus)
		status, err := protocol.Decode[protocol.UserInputStatusPayload](frame)
		if err != nil {
			t.Fatal(err)
		}
		seenReceipt = seenReceipt || status.Receipt != nil && status.Receipt.Outcome == "delivered"
		seenClosed = seenClosed || status.Lifecycle == "closed"
	}
	if !seenReceipt || !seenClosed {
		t.Fatalf("answer result: receipt=%v closed=%v", seenReceipt, seenClosed)
	}
	select {
	case result := <-mcpDone:
		var rpc struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(result, &rpc); err != nil || len(rpc.Result.Content) != 1 || !strings.Contains(rpc.Result.Content[0].Text, `"Pick":"Yes"`) {
			t.Fatalf("MCP answer = %s, err=%v", result, err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Claude MCP helper did not resume")
	}
	if err := browserWS.WriteJSON(protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "recover", ClientVersion: 3})); err != nil {
		t.Fatal(err)
	}
	recovered := readBrowser(protocol.TypeUserInputSnapshot)
	snapshot, err := protocol.Decode[protocol.UserInputSnapshotPayload](recovered)
	if err != nil || snapshot.SyncID != "recover" || len(snapshot.Requests) != 0 || len(snapshot.Closed) != 1 || len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Outcome != "delivered" {
		t.Fatalf("recovered receipt = %#v, err=%v", snapshot, err)
	}
	select {
	case err := <-transportDone:
		t.Fatalf("Bridge transport stopped early: %v", err)
	default:
	}
}
