package hub

import (
	"context"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestActiveOrchestrationMessagesOwnershipIdempotencyAndLateAck(t *testing.T) {
	s, st, uid, agent := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, uid, agent)
	conn := &BridgeConn{agentID: agent, capabilities: &protocol.BridgeCapabilities{OrchestrationLiveInput: 1}, wsSender: newWSSender(nil, 8)}
	s.pool.RegisterAgent(conn)
	url := "/api/orchestrations/" + run.ID + "/prompts"
	p := map[string]any{"messageId": "m1", "prompt": "add tests", "deliveryMode": "queue"}
	authedJSON(t, s, uid, http.MethodPost, url, p, http.StatusAccepted)
	frame := <-conn.send
	input, err := protocol.Decode[protocol.OrchestrationInputPayload](frame)
	if err != nil || input.Prompt != "add tests" {
		t.Fatal(input, err)
	}
	authedJSON(t, s, uid, http.MethodPost, url, p, http.StatusOK)
	select {
	case <-conn.send:
		t.Fatal("duplicate native dispatch")
	default:
	}
	status := protocol.OrchestrationInputStatusPayload{RunID: run.ID, MessageID: "m1", ExecutionID: run.ID, Status: "delivered"}
	s.handleOrchestrationInputStatus(ctx, "foreign", protocol.MustEnvelope(protocol.TypeOrchestrationInputStatus, "", status))
	m, _ := st.OrchestrationMessageByID(ctx, run.ID, "m1")
	if m.Status != "sending" {
		t.Fatal("foreign ack accepted", m)
	}
	status.ExecutionID = "stale"
	s.handleOrchestrationInputStatus(ctx, agent, protocol.MustEnvelope(protocol.TypeOrchestrationInputStatus, "", status))
	m, _ = st.OrchestrationMessageByID(ctx, run.ID, "m1")
	if m.Status != "sending" {
		t.Fatal("stale ack accepted", m)
	}
	status.ExecutionID = run.ID
	s.handleOrchestrationInputStatus(ctx, agent, protocol.MustEnvelope(protocol.TypeOrchestrationInputStatus, "", status))
	payload := protocol.OrchestrationStartPayload{RunID: run.ID, TaskPrompt: "original"}
	if err := s.appendOrchestrationInputContext(ctx, &payload); err != nil || !strings.Contains(payload.Context, "add tests") || !strings.Contains(payload.TaskPrompt, "add tests") {
		t.Fatal(payload, err)
	}
	getJSON(t, s, uid, url, http.StatusOK)
	_ = st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, "")
	authedJSON(t, s, uid, http.MethodPost, url, p, http.StatusOK)
	p["messageId"] = "new"
	authedJSON(t, s, uid, http.MethodPost, url, p, http.StatusConflict)
	other, err := st.UpsertUser(ctx, "other-input-owner", "secret")
	if err != nil {
		t.Fatal(err)
	}
	getJSON(t, s, other.ID, url, http.StatusNotFound)
}
