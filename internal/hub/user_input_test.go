package hub

import (
	"context"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestUserInputBridgeFrameRequiresRunOwnerAndRollout(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	agent := nativeUserInputTestAgent(agentID, 3)
	s.pool.RegisterAgent(agent)
	defer s.pool.UnregisterAgent(agentID, agent)
	browser := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 4), done: make(chan struct{})}}
	browser.nativeUserInputVersion.Store(3)
	s.pool.AddOrchestrationBrowser(run.ID, browser)
	defer s.pool.RemoveOrchestrationBrowser(run.ID, browser)
	request := protocol.UserInputRequestPayload{
		RequestID: "input_1", RunID: run.ID, ExecutionID: "execution_1", CLI: "codex", Blocking: true,
		Questions: []protocol.UserInputQuestion{{ID: "q1", Question: "Choose", SelectionMode: "single"}}, Revision: 1,
	}
	frame := protocol.MustEnvelope(protocol.TypeUserInputRequest, "", request)
	s.handleBridgeEnvelope(context.Background(), "foreign-agent", frame)
	select {
	case <-browser.send:
		t.Fatal("foreign Bridge delivered user input")
	default:
	}
	s.handleBridgeEnvelope(context.Background(), agentID, frame)
	select {
	case got := <-browser.send:
		if got.Type != protocol.TypeUserInputRequest {
			t.Fatalf("frame type = %q", got.Type)
		}
	default:
		t.Fatal("owner Bridge input was not routed")
	}
	s.cfg.Hub.FeatureRollouts[rollout.FeatureNativeUserInput] = "off"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	s.handleBridgeEnvelope(context.Background(), agentID, frame)
	select {
	case <-browser.send:
		t.Fatal("disabled rollout delivered user input")
	default:
	}
}

func TestUserInputBrowserResponseRoutesOnlyToOwnedActiveRun(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	agent := nativeUserInputTestAgent(agentID, 3)
	s.pool.RegisterAgent(agent)
	defer s.pool.UnregisterAgent(agentID, agent)
	browser := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 2), done: make(chan struct{})}}
	browser.nativeUserInputVersion.Store(3)
	answer := "custom"
	response := protocol.UserInputResponsePayload{RequestID: "input_1", RunID: run.ID, SubmissionID: "submit_1", Answers: map[string]protocol.UserInputAnswer{"q1": {CustomText: &answer}}}
	frame := protocol.MustEnvelope(protocol.TypeUserInputResponse, "", response)
	s.handleUserInputFromBrowser(context.Background(), "wrong-user", run.ID, browser, frame)
	select {
	case got := <-browser.send:
		status, _ := protocol.Decode[protocol.UserInputStatusPayload](got)
		if status.Status != "rejected" {
			t.Fatalf("foreign user status = %#v", status)
		}
	default:
		t.Fatal("foreign user did not receive rejection")
	}
	select {
	case <-agent.send:
		t.Fatal("foreign user reached Bridge")
	default:
	}
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, frame)
	select {
	case got := <-agent.send:
		if got.Type != protocol.TypeUserInputResponse {
			t.Fatalf("agent frame = %q", got.Type)
		}
	default:
		t.Fatal("owner answer did not reach Bridge")
	}
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, frame)
	select {
	case <-agent.send:
		t.Fatal("answer reached a completed run")
	default:
	}
}

func TestUserInputSyncAndSnapshotStayOnOwnedRun(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	agent := nativeUserInputTestAgent(agentID, 3)
	s.pool.RegisterAgent(agent)
	defer s.pool.UnregisterAgent(agentID, agent)
	browser := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 3), done: make(chan struct{})}}
	s.pool.AddOrchestrationBrowser(run.ID, browser)
	defer s.pool.RemoveOrchestrationBrowser(run.ID, browser)

	sync := protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "sync_1", ClientVersion: 3})
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, sync)
	select {
	case got := <-agent.send:
		if got.Type != protocol.TypeUserInputSync {
			t.Fatalf("agent frame type = %q", got.Type)
		}
	default:
		t.Fatal("owned run sync did not reach Bridge")
	}

	request := protocol.UserInputRequestPayload{
		RequestID: "input_1", RunID: run.ID, ExecutionID: "execution_1", CLI: "codex",
		Questions: []protocol.UserInputQuestion{{ID: "q1", Question: "Choose", SelectionMode: "single"}}, Revision: 1,
	}
	snapshot := protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", protocol.UserInputSnapshotPayload{
		RunID: run.ID, SyncID: "sync_1", Revision: 1, Requests: []protocol.UserInputRequestPayload{request},
		Statuses: []protocol.UserInputStatusPayload{{RunID: run.ID, RequestID: request.RequestID, ExecutionID: request.ExecutionID, Status: "queued", Lifecycle: "open", Revision: 1}},
	})
	s.handleBridgeEnvelope(context.Background(), "foreign-agent", snapshot)
	select {
	case <-browser.send:
		t.Fatal("foreign Bridge delivered snapshot")
	default:
	}
	s.handleBridgeEnvelope(context.Background(), agentID, snapshot)
	select {
	case got := <-browser.send:
		if got.Type != protocol.TypeUserInputSnapshot {
			t.Fatalf("browser frame type = %q", got.Type)
		}
	default:
		t.Fatal("owned Bridge snapshot was not routed")
	}
}

func TestUserInputSyncRequiresV3IdentityForReconciliation(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	agent := nativeUserInputTestAgent(agentID, 3)
	s.pool.RegisterAgent(agent)
	defer s.pool.UnregisterAgent(agentID, agent)
	browser := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 8), done: make(chan struct{})}}
	query := protocol.UserInputPendingSubmission{RequestID: "request", SubmissionID: "submission", ExecutionID: "attempt"}
	for _, tc := range []struct {
		name  string
		sync  protocol.UserInputSyncPayload
		valid bool
	}{
		{"missing instance", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "s1", ClientVersion: 3, PendingSubmissions: []protocol.UserInputPendingSubmission{query}}, false},
		{"old version", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "s2", InstanceID: "instance", ClientVersion: 2, PendingSubmissions: []protocol.UserInputPendingSubmission{query}}, false},
		{"duplicate", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "s3", InstanceID: "instance", ClientVersion: 3, PendingSubmissions: []protocol.UserInputPendingSubmission{query, query}}, false},
		{"valid", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "s4", InstanceID: "instance", ClientVersion: 3, PendingSubmissions: []protocol.UserInputPendingSubmission{query}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, protocol.MustEnvelope(protocol.TypeUserInputSync, "", tc.sync))
			if tc.valid {
				select {
				case frame := <-agent.send:
					if frame.Type != protocol.TypeUserInputSync {
						t.Fatalf("frame = %s", frame.Type)
					}
				default:
					t.Fatal("valid query did not route")
				}
			} else {
				select {
				case <-agent.send:
					t.Fatal("invalid query reached Bridge")
				default:
				}
				select {
				case <-browser.send:
				default:
					t.Fatal("invalid query lacked rejection")
				}
			}
		})
	}
}

func nativeUserInputTestAgent(agentID string, version int) *BridgeConn {
	return &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{NativeUserInput: map[string]protocol.NativeUserInputCapability{
			"codex": {Version: version, Available: true},
		}},
		wsSender: wsSender{send: make(chan protocol.Envelope, 8), done: make(chan struct{})},
	}
}

func TestUserInputV3NegotiationRejectsOldBridgeAndOldBrowser(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	oldAgent := nativeUserInputTestAgent(agentID, 1)
	s.pool.RegisterAgent(oldAgent)
	legacy := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 8), done: make(chan struct{})}}
	modern := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 8), done: make(chan struct{})}}
	s.pool.AddOrchestrationBrowser(run.ID, legacy)
	s.pool.AddOrchestrationBrowser(run.ID, modern)
	defer s.pool.RemoveOrchestrationBrowser(run.ID, legacy)
	defer s.pool.RemoveOrchestrationBrowser(run.ID, modern)
	modernSync := protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "modern", ClientVersion: 3})
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, modern, modernSync)
	if modern.nativeUserInputVersion.Load() != 3 {
		t.Fatal("modern browser did not negotiate v3")
	}
	select {
	case <-oldAgent.send:
		t.Fatal("old Bridge received active v2 sync")
	default:
	}
	<-modern.send // v1 Bridge route rejection.
	request := protocol.UserInputRequestPayload{RunID: run.ID, RequestID: "input", ExecutionID: "execution", CLI: "codex", Questions: []protocol.UserInputQuestion{{ID: "q", Question: "Choose"}}}
	frame := protocol.MustEnvelope(protocol.TypeUserInputRequest, "", request)
	s.handleBridgeEnvelope(context.Background(), agentID, frame)
	select {
	case <-modern.send:
		t.Fatal("old Bridge question reached modern browser")
	default:
	}
	s.pool.UnregisterAgent(agentID, oldAgent)
	newAgent := nativeUserInputTestAgent(agentID, 3)
	s.pool.RegisterAgent(newAgent)
	defer s.pool.UnregisterAgent(agentID, newAgent)
	s.handleBridgeEnvelope(context.Background(), agentID, frame)
	select {
	case got := <-modern.send:
		if got.Type != protocol.TypeUserInputRequest {
			t.Fatalf("modern frame = %s", got.Type)
		}
	default:
		t.Fatal("modern browser missed v2 question")
	}
	select {
	case <-legacy.send:
		t.Fatal("legacy browser received v2 question")
	default:
	}
	status := protocol.MustEnvelope(protocol.TypeUserInputStatus, "", protocol.UserInputStatusPayload{RunID: run.ID, RequestID: "input", ExecutionID: "execution", Status: "queued", Source: "bridge"})
	snapshot := protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", protocol.UserInputSnapshotPayload{RunID: run.ID, SyncID: "modern", Requests: []protocol.UserInputRequestPayload{request}})
	for _, nativeFrame := range []protocol.Envelope{status, snapshot} {
		s.handleBridgeEnvelope(context.Background(), agentID, nativeFrame)
		select {
		case got := <-modern.send:
			if got.Type != nativeFrame.Type {
				t.Fatalf("modern frame = %s, want %s", got.Type, nativeFrame.Type)
			}
		default:
			t.Fatalf("modern browser missed %s", nativeFrame.Type)
		}
		select {
		case <-legacy.send:
			t.Fatalf("legacy browser received %s", nativeFrame.Type)
		default:
		}
	}
	answer := "answer"
	response := protocol.MustEnvelope(protocol.TypeUserInputResponse, "", protocol.UserInputResponsePayload{RunID: run.ID, RequestID: "input", SubmissionID: "submission", Answers: map[string]protocol.UserInputAnswer{"q": {CustomText: &answer}}})
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, legacy, response)
	select {
	case <-newAgent.send:
		t.Fatal("legacy browser answer reached Bridge")
	default:
	}
	<-legacy.send // Local reload-required rejection.
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, modern, response)
	select {
	case got := <-newAgent.send:
		if got.Type != protocol.TypeUserInputResponse {
			t.Fatalf("modern answer route = %s", got.Type)
		}
	default:
		t.Fatal("negotiated browser answer did not reach Bridge")
	}
}

func TestUserInputStartRequiresV3BridgeAndBrowserCapability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
	}{{"v1", 1}, {"v2", 2}, {"v3", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			version := tc.version
			s, st, uid, agentID := newOrchestrationTestServer(t)
			run := createOrchestrationRun(t, st, uid, agentID)
			agent := nativeUserInputTestAgent(agentID, version)
			s.pool.RegisterAgent(agent)
			defer s.pool.UnregisterAgent(agentID, agent)
			if err := s.startOrchestration(context.Background(), run, orchestrationStartRequest{Prompt: "task", Mode: "collaboration", NativeUserInputClientVersion: 3}, nil, false); err != nil {
				t.Fatal(err)
			}
			select {
			case frame := <-agent.send:
				payload, err := protocol.Decode[protocol.OrchestrationStartPayload](frame)
				if err != nil || payload.NativeUserInput != (version >= 3) || payload.NativeUserInputProtocolVersion != map[bool]int{true: 3, false: 0}[version >= 3] {
					t.Fatalf("v%d native start = %#v, err = %v", version, payload.NativeUserInput, err)
				}
			default:
				t.Fatal("start frame not sent")
			}
		})
	}
}

func TestUserInputTerminalRunAllowsOwnerReceiptSyncOnly(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	agent := &BridgeConn{agentID: agentID, wsSender: wsSender{send: make(chan protocol.Envelope, 4), done: make(chan struct{})}}
	s.pool.RegisterAgent(agent)
	defer s.pool.UnregisterAgent(agentID, agent)
	browser := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 8), done: make(chan struct{})}}
	s.pool.AddOrchestrationBrowser(run.ID, browser)
	defer s.pool.RemoveOrchestrationBrowser(run.ID, browser)
	sync := protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "terminal", ClientVersion: 3})
	s.handleUserInputFromBrowser(context.Background(), "foreign-user", run.ID, browser, sync)
	select {
	case <-agent.send:
		t.Fatal("foreign user queried terminal run")
	default:
	}
	<-browser.send // Hub route rejection for foreign user.
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, sync)
	select {
	case got := <-agent.send:
		if got.Type != protocol.TypeUserInputSync {
			t.Fatalf("terminal owner route = %s", got.Type)
		}
	default:
		t.Fatal("terminal owner receipt sync was rejected")
	}
	answer := protocol.MustEnvelope(protocol.TypeUserInputResponse, "", protocol.UserInputResponsePayload{
		RunID: run.ID, RequestID: "input", SubmissionID: "submitted", Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a"}}},
	})
	s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, answer)
	select {
	case <-agent.send:
		t.Fatal("terminal run accepted an answer write")
	default:
	}
	<-browser.send // Route rejection is local to this submission.
	receipt := protocol.UserInputReceiptPayload{RequestID: "input", RunID: run.ID, ExecutionID: "attempt", InstanceID: "bridge-instance", SubmissionID: "submitted", Outcome: "delivered", Revision: 3}
	closed := protocol.UserInputStatusPayload{RequestID: "input", RunID: run.ID, ExecutionID: "attempt", InstanceID: receipt.InstanceID, SubmissionID: receipt.SubmissionID, Status: "closed", Lifecycle: "closed", Source: "bridge", Receipt: &receipt, Revision: 4}
	snapshot := protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", protocol.UserInputSnapshotPayload{
		RunID: run.ID, SyncID: "terminal", InstanceID: receipt.InstanceID, Revision: 4,
		Requests: []protocol.UserInputRequestPayload{}, Closed: []protocol.UserInputStatusPayload{closed}, Receipts: []protocol.UserInputReceiptPayload{receipt},
	})
	s.handleBridgeEnvelope(context.Background(), "foreign-agent", snapshot)
	select {
	case <-browser.send:
		t.Fatal("foreign Bridge exposed terminal receipt")
	default:
	}
	s.handleBridgeEnvelope(context.Background(), agentID, snapshot)
	select {
	case got := <-browser.send:
		if got.Type != protocol.TypeUserInputSnapshot {
			t.Fatalf("terminal snapshot route = %s", got.Type)
		}
	default:
		t.Fatal("terminal owner did not receive receipt snapshot")
	}
	active := protocol.MustEnvelope(protocol.TypeUserInputRequest, "", protocol.UserInputRequestPayload{
		RunID: run.ID, RequestID: "new", ExecutionID: "attempt", Questions: []protocol.UserInputQuestion{{ID: "q", Question: "Choose"}},
	})
	s.handleBridgeEnvelope(context.Background(), agentID, active)
	select {
	case <-browser.send:
		t.Fatal("terminal run received a new native question")
	default:
	}
}

func TestUserInputSyncRejectionIsCorrelatedWithoutDeliveryFacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		reason  string
	}{
		{"offline", 0, "CLI endpoint is offline"},
		{"old protocol", 2, "Bridge needs protocol v3 for structured input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, uid, agentID := newOrchestrationTestServer(t)
			run := createOrchestrationRun(t, st, uid, agentID)
			if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationRunning, ""); err != nil {
				t.Fatal(err)
			}
			if tc.version > 0 {
				agent := nativeUserInputTestAgent(agentID, tc.version)
				s.pool.RegisterAgent(agent)
				defer s.pool.UnregisterAgent(agentID, agent)
			}
			browser := &BrowserConn{wsSender: wsSender{send: make(chan protocol.Envelope, 2), done: make(chan struct{})}}
			frame := protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: "query-1", ClientVersion: 3})
			s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, frame)
			select {
			case env := <-browser.send:
				p, err := protocol.Decode[protocol.UserInputStatusPayload](env)
				if err != nil || env.Type != protocol.TypeUserInputStatus || p.RunID != run.ID || p.SyncID != "query-1" || p.RequestID != "" || p.Source != "hub" || p.Phase != "route" || p.Status != "rejected" || p.Reason != tc.reason || p.Revision != 0 || p.Receipt != nil || p.Lifecycle != "" || p.SubmissionID != "" || p.ExecutionID != "" || p.InstanceID != "" {
					t.Fatalf("query rejection = %#v (%v)", p, err)
				}
			default:
				t.Fatal("missing correlated query rejection")
			}
			browser.nativeUserInputVersion.Store(3)
			answer := "answer"
			frame = protocol.MustEnvelope(protocol.TypeUserInputResponse, "", protocol.UserInputResponsePayload{RunID: run.ID, RequestID: "request", SubmissionID: "submission", Answers: map[string]protocol.UserInputAnswer{"q": {CustomText: &answer}}})
			s.handleUserInputFromBrowser(context.Background(), uid, run.ID, browser, frame)
			p, err := protocol.Decode[protocol.UserInputStatusPayload](<-browser.send)
			if err != nil || p.RequestID != "request" || p.SubmissionID != "submission" || p.SyncID != "" || p.Receipt != nil {
				t.Fatalf("answer rejection = %#v (%v)", p, err)
			}
		})
	}
}
