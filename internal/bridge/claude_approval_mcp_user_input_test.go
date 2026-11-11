package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

// Exercise the serialized MCP response, rather than only the internal
// decision map: Claude reads one JSON-RPC result with one text content block.
func TestClaudeApprovalMCPJSONRPCSingleTextContent(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		decision string
		updated  json.RawMessage
		want     string
	}{
		{"ordinary accept", "Bash", "accept", nil, "allow"},
		{"ordinary deny", "Bash", "decline", nil, "deny"},
		{"question single", "AskUserQuestion", "accept", json.RawMessage(`{"questions":[],"answers":{"Pick":"Yes"}}`), "allow"},
		{"question multiple", "AskUserQuestion", "accept", json.RawMessage(`{"questions":[],"answers":{"Pick":["A","B"]}}`), "allow"},
		{"question custom", "AskUserQuestion", "accept", json.RawMessage(`{"questions":[],"answers":{"Pick":"Something else"}}`), "allow"},
		{"question cancel", "AskUserQuestion", "cancel", nil, "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socketPath := t.TempDir() + "/approval.sock"
			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				var req claudeApprovalSocketRequest
				if json.NewDecoder(conn).Decode(&req) == nil {
					_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: req.RequestID, Decision: tc.decision, UpdatedInput: tc.updated})
				}
			}()
			arguments := map[string]any{"tool_name": tc.toolName, "tool_input": map[string]any{"questions": []any{}, "command": "pwd"}}
			line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "browser_approval", "arguments": arguments}})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := RunClaudeApprovalMCP(socketPath, bytes.NewReader(append(line, '\n')), &output); err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Result map[string]json.RawMessage `json:"result"`
				Error  json.RawMessage            `json:"error"`
			}
			if err := json.Unmarshal(output.Bytes(), &wire); err != nil || len(wire.Error) != 0 {
				t.Fatalf("invalid MCP JSON-RPC response: %s, err=%v", output.Bytes(), err)
			}
			if _, hasStructured := wire.Result["structuredContent"]; hasStructured {
				t.Fatalf("Claude-incompatible structuredContent returned: %s", output.Bytes())
			}
			var content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(wire.Result["content"], &content); err != nil || len(content) != 1 || content[0].Type != "text" {
				t.Fatalf("MCP content must be one text block: %s, err=%v", output.Bytes(), err)
			}
			var decision map[string]json.RawMessage
			if err := json.Unmarshal([]byte(content[0].Text), &decision); err != nil {
				t.Fatal(err)
			}
			var behavior string
			if err := json.Unmarshal(decision["behavior"], &behavior); err != nil || behavior != tc.want {
				t.Fatalf("permission decision = %s, err=%v", content[0].Text, err)
			}
			if tc.toolName == "AskUserQuestion" && tc.decision == "accept" && !bytes.Equal(decision["updatedInput"], tc.updated) {
				t.Fatalf("native question answer changed: %s", content[0].Text)
			}
		})
	}
}

// This uses the actual project MCP helper and Claude permission socket, with
// the browser answer entering through Client's WebSocket-frame dispatcher.
// It deliberately does not call an external Claude model.
func TestClaudeAskUserQuestionMCPManagerRoundTrip(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socketPath := t.TempDir() + "/approval.sock"
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &claudeApprovalServer{userInput: orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", workerID: "claude-b", cli: "claude"}}
	server.updateTurn("candidate-turn", "worker")
	server.updateTurn("review-turn", "reviewer")
	go serveClaudeApprovalSocket(ctx, listener, server)
	params := map[string]any{"name": "browser_approval", "arguments": map[string]any{
		"tool_name": "AskUserQuestion", "tool_input": map[string]any{"questions": []any{map[string]any{
			"question": "Pick", "header": "Choice", "options": []any{map[string]any{"label": "Yes", "description": "Continue"}}, "multiSelect": false,
		}}},
	}}
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	type mcpResult struct {
		data []byte
		err  error
	}
	done := make(chan mcpResult, 1)
	go func() {
		var output bytes.Buffer
		err := RunClaudeApprovalMCP(socketPath, bytes.NewReader(append(line, '\n')), &output)
		done <- mcpResult{data: output.Bytes(), err: err}
	}()
	request := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	if request.WorkerID != "claude-b" || request.TurnID != "review-turn" || request.CLI != "claude" || !request.Blocking || request.ExecutionID != "attempt" || request.InstanceID == "" {
		t.Fatalf("native request = %#v", request)
	}
	client := &Client{orchestrations: manager}
	client.handleEnvelope(context.Background(), protocol.MustEnvelope(protocol.TypeUserInputResponse, "", protocol.UserInputResponsePayload{
		RunID: "run", RequestID: request.RequestID, SubmissionID: "answer", Answers: map[string]protocol.UserInputAnswer{"q0": {OptionIDs: []string{"Yes"}}},
	}), out)
	seenClosed, seenReceipt := false, false
	var statuses []protocol.UserInputStatusPayload
	deadline := time.After(5 * time.Second)
	for !seenClosed || !seenReceipt {
		select {
		case frame := <-out:
			if frame.Type != protocol.TypeUserInputStatus {
				t.Fatalf("unexpected frame while waiting for Claude receipt: %s", frame.Type)
			}
			status, err := protocol.Decode[protocol.UserInputStatusPayload](frame)
			if err != nil {
				t.Fatal(err)
			}
			statuses = append(statuses, status)
			seenClosed = seenClosed || status.Lifecycle == "closed"
			seenReceipt = seenReceipt || status.Receipt != nil && status.Receipt.Outcome == "delivered"
		case <-deadline:
			t.Fatalf("Claude answer did not settle: statuses=%#v", statuses)
		}
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		var wire struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(result.data, &wire); err != nil || len(wire.Result.Content) != 1 {
			t.Fatalf("MCP output = %s, err=%v", result.data, err)
		}
		var decision struct {
			Behavior     string `json:"behavior"`
			UpdatedInput struct {
				Answers map[string]string `json:"answers"`
			} `json:"updatedInput"`
		}
		if err := json.Unmarshal([]byte(wire.Result.Content[0].Text), &decision); err != nil || decision.Behavior != "allow" || decision.UpdatedInput.Answers["Pick"] != "Yes" {
			t.Fatalf("MCP decision = %s, err=%v", wire.Result.Content[0].Text, err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP helper did not receive the native answer")
	}
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "recover"})
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if len(snapshot.Requests) != 0 || len(snapshot.Closed) != 1 || len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Outcome != "delivered" {
		t.Fatalf("Claude terminal recovery = %#v", snapshot)
	}
}
