package bridge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/gorilla/websocket"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestUserInputOutboxRecoversWithoutReconnect(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	t.Cleanup(manager.CloseAll)
	out := make(chan protocol.Envelope, 1)
	out <- protocol.MustEnvelope(protocol.TypeHeartbeat, "", nil)
	manager.AttachOut(out)
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "exec"}
	id, err := requester.RegisterUserInput(context.Background(), true, []protocol.UserInputQuestion{{ID: "q", Question: "Choose"}}, func(context.Context, protocol.UserInputResponsePayload) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	pending := len(manager.pending)
	manager.mu.Unlock()
	if pending != 0 {
		t.Fatal("native question entered the ordinary reconnect queue")
	}
	<-out // Congestion clears without detach/attach.
	req := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	if req.RequestID != id {
		t.Fatalf("request = %#v", req)
	}
}

func TestUserInputOutboxCloseSupersedesQuestionAndPreservesReceipt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	box := newUserInputOutbox(ctx)
	if !box.reserve("q", "run") {
		t.Fatal("reserve failed")
	}
	question := protocol.UserInputRequestPayload{RequestID: "q", RunID: "run", Revision: 1, Questions: []protocol.UserInputQuestion{{ID: "choice", Question: "secret question"}}}
	box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputRequest, "", question))
	box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", protocol.UserInputSnapshotPayload{RunID: "run", SyncID: "tab1", Requests: []protocol.UserInputRequestPayload{question}}))
	receipt := &protocol.UserInputReceiptPayload{RequestID: "q", RunID: "run", SubmissionID: "answer", Outcome: "delivered", Revision: 2}
	box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", protocol.UserInputStatusPayload{RequestID: "q", RunID: "run", SubmissionID: "answer", Receipt: receipt, Status: "delivered", Lifecycle: "open", Revision: 2}))
	box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", protocol.UserInputStatusPayload{RequestID: "q", RunID: "run", Status: "closed", Lifecycle: "closed", Revision: 3}))
	box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputRequest, "", question)) // stale publisher
	out := make(chan protocol.Envelope, 8)
	box.attach(out)
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if len(snapshot.Requests) != 0 || len(snapshot.Closed) != 1 {
		t.Fatalf("stale snapshot: %#v", snapshot)
	}
	status := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if status.Lifecycle != "closed" || status.Receipt == nil || status.Receipt.Outcome != "delivered" {
		t.Fatalf("receipt lost: %#v", status)
	}
	closed := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if closed.Status != "closed" {
		t.Fatalf("close = %#v", closed)
	}
	select {
	case env := <-out:
		t.Fatalf("unexpected duplicate: %#v", env)
	default:
	}
}

func TestUserInputOutboxConnectionReplacementAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	box := newUserInputOutbox(ctx)
	old := make(chan protocol.Envelope, 1)
	old <- protocol.MustEnvelope(protocol.TypeHeartbeat, "", nil)
	box.attach(old)
	box.reserve("q", "run")
	box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputRequest, "", protocol.UserInputRequestPayload{RequestID: "q", RunID: "run", Revision: 1}))
	box.detach(old)
	fresh := make(chan protocol.Envelope, 1)
	box.attach(fresh)
	box.detach(old) // A delayed old disconnect cannot detach the fresh channel.
	readUserInputFrame[protocol.UserInputRequestPayload](t, fresh, protocol.TypeUserInputRequest)
	<-old
	select {
	case env := <-old:
		t.Fatalf("sent to detached channel: %#v", env)
	default:
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		box.mu.Lock()
		closed := box.closed && !box.running && len(box.frames) == 0 && len(box.identities) == 0
		box.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-deadline:
			t.Fatal("outbox failed to release retained content")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestUserInputOutboxBoundedSyncsKeepTabIdentities(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	box := newUserInputOutbox(ctx)
	for i := 0; i < userInputOutboxSyncMax+10; i++ {
		box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", protocol.UserInputSnapshotPayload{RunID: "run", SyncID: fmt.Sprintf("sync-%d", i), Requests: []protocol.UserInputRequestPayload{}}))
	}
	box.mu.Lock()
	count := len(box.frames)
	box.mu.Unlock()
	if count != userInputOutboxSyncMax {
		t.Fatalf("sync slots = %d", count)
	}
	out := make(chan protocol.Envelope, userInputOutboxSyncMax)
	box.attach(out)
	for i := 0; i < userInputOutboxSyncMax; i++ {
		snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
		if snapshot.SyncID != fmt.Sprintf("sync-%d", i) {
			t.Fatalf("sync identity = %q", snapshot.SyncID)
		}
	}
}

func TestUserInputOutboxRegistrationRejectsGlobalCapacity(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	t.Cleanup(manager.CloseAll)
	for i := 0; i < userInputOutboxActiveMax+1; i++ {
		requester := orchestrationUserInputRequester{manager: manager, runID: fmt.Sprintf("run-%d", i/64), executionID: "exec"}
		_, err := requester.RegisterUserInput(context.Background(), true, []protocol.UserInputQuestion{{ID: "q", Question: "Choose"}}, func(context.Context, protocol.UserInputResponsePayload) error { return nil })
		if (err != nil) != (i == userInputOutboxActiveMax) {
			t.Fatalf("registration %d: %v", i, err)
		}
	}
	manager.mu.Lock()
	count := len(manager.userInputs)
	manager.mu.Unlock()
	if count != userInputOutboxActiveMax {
		t.Fatalf("retained requests = %d", count)
	}
}

// Exercise connectOnce's actual WebSocket reader. Only the question sender's
// application queue is controlled; incoming control frames use the real socket.
func TestUserInputOutboxCongestionKeepsClientReaderResponsive(t *testing.T) {
	connected := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var registration protocol.Envelope
		if ws.ReadJSON(&registration) != nil {
			return
		}
		if ws.WriteJSON(protocol.MustEnvelope(protocol.TypeRegistered, "", protocol.RegisteredPayload{AgentID: "test"})) != nil {
			return
		}
		connected <- ws
		<-release
	}))
	defer server.Close()
	defer close(release)
	cfg := config.Default()
	cfg.Bridge.HubURL = server.URL
	cfg.Bridge.CWD = t.TempDir()
	cfg.Bridge.HeartbeatInterval.Duration = time.Hour
	client := NewClient(&cfg, "test")
	client.sessions = NewSessionManager(&cfg)
	client.orchestrations = NewOrchestrationManager(&cfg)
	defer client.orchestrations.CloseAll()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.connectOnce(ctx, "test") }()
	var ws *websocket.Conn
	select {
	case ws = <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("client failed to connect")
	}
	deadline := time.After(5 * time.Second)
	for {
		client.orchestrations.mu.Lock()
		attached := client.orchestrations.output != nil
		client.orchestrations.mu.Unlock()
		if attached {
			break
		}
		select {
		case <-deadline:
			t.Fatal("client did not attach output")
		case <-time.After(time.Millisecond):
		}
	}
	full := make(chan protocol.Envelope, 1)
	full <- protocol.MustEnvelope(protocol.TypeHeartbeat, "", nil)
	client.orchestrations.AttachOut(full)
	requester := orchestrationUserInputRequester{manager: client.orchestrations, runID: "run", executionID: "exec"}
	if _, err := requester.RegisterUserInput(ctx, true, []protocol.UserInputQuestion{{ID: "q", Question: "Choose"}}, func(context.Context, protocol.UserInputResponsePayload) error { return nil }); err != nil {
		t.Fatal(err)
	}
	approval := make(chan protocol.ApprovalResponsePayload, 1)
	canceled := make(chan struct{})
	var once sync.Once
	client.orchestrations.mu.Lock()
	client.orchestrations.approvals["approval"] = orchestrationApproval{runID: "control", ch: approval}
	client.orchestrations.runs["control"] = &orchestrationRunHandle{cancel: func() { once.Do(func() { close(canceled) }) }}
	client.orchestrations.mu.Unlock()
	frames := []protocol.Envelope{
		protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: "run", SyncID: "reader-sync", ClientVersion: 3}),
		protocol.MustEnvelope(protocol.TypeHeartbeat, "", nil),
		protocol.MustEnvelope(protocol.TypeApprovalResponse, "", protocol.ApprovalResponsePayload{RequestID: "approval", Decision: "accept"}),
		protocol.MustEnvelope(protocol.TypeOrchestrationCancel, "", protocol.OrchestrationCancelPayload{RunID: "control"}),
	}
	for _, frame := range frames {
		if err := ws.WriteJSON(frame); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case result := <-approval:
		if result.Decision != "accept" {
			t.Fatalf("approval = %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("congestion blocked approval reader")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("congestion blocked cancellation reader")
	}
	<-full
	readUserInputFrame[protocol.UserInputRequestPayload](t, full, protocol.TypeUserInputRequest)
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, full, protocol.TypeUserInputSnapshot)
	if snapshot.SyncID != "reader-sync" || len(snapshot.Requests) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client failed to stop")
	}
}

func TestUserInputOutboxClosedSnapshotStaysBoundedAndConsistent(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-closure-%t", duplicate), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			box := newUserInputOutbox(ctx)
			box.reserve("q", "run")
			receipt := protocol.UserInputReceiptPayload{RequestID: "old-0", RunID: "run", SubmissionID: "old-answer", Outcome: "delivered", Revision: 1}
			snapshot := protocol.UserInputSnapshotPayload{
				RunID: "run", SyncID: "tab", Revision: 65,
				Requests: []protocol.UserInputRequestPayload{{RequestID: "q", RunID: "run", Revision: 65, Questions: []protocol.UserInputQuestion{{ID: "choice", Question: "secret question"}}}},
				Statuses: []protocol.UserInputStatusPayload{{RequestID: "q", RunID: "run", Status: "queued", Lifecycle: "open", Revision: 65}},
				Receipts: []protocol.UserInputReceiptPayload{receipt},
			}
			for i := 0; i < userInputTerminalPerRunMax; i++ {
				id := fmt.Sprintf("old-%d", i)
				if duplicate && i == userInputTerminalPerRunMax-1 {
					id = "q"
				}
				snapshot.Closed = append(snapshot.Closed, protocol.UserInputStatusPayload{RequestID: id, RunID: "run", Status: "closed", Lifecycle: "closed", Revision: uint64(i + 1)})
			}
			box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", snapshot))
			closed := protocol.UserInputStatusPayload{RequestID: "q", RunID: "run", Status: "closed", Lifecycle: "closed", Revision: 66}
			box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", closed))
			// Repeated scrubbing and a late old snapshot must stay idempotent.
			box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", snapshot))
			box.enqueue(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", closed))
			out := make(chan protocol.Envelope, 8)
			box.attach(out)
			got := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
			if len(got.Requests) != 0 || len(got.Statuses) != 0 || len(got.Closed) != userInputTerminalPerRunMax {
				t.Fatalf("inconsistent recovery snapshot: requests=%d statuses=%d closed=%d", len(got.Requests), len(got.Statuses), len(got.Closed))
			}
			seen := make(map[string]bool)
			for _, item := range got.Closed {
				if seen[item.RequestID] {
					t.Fatalf("duplicate closure: %q", item.RequestID)
				}
				seen[item.RequestID] = true
				if item.RequestID == "q" && item.Revision != 66 {
					t.Fatalf("stale closure: %#v", item)
				}
			}
			if !seen["q"] || len(got.Receipts) != 1 || got.Receipts[0] != receipt {
				t.Fatalf("new closure or independent receipt lost: %#v", got)
			}
		})
	}
}
