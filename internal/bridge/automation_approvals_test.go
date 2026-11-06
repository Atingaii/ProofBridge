package bridge

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func automationApprovalFixture(t *testing.T) (*OrchestrationManager, chan protocol.Envelope, protocol.ApprovalRequestPayload, chan protocol.ApprovalResponsePayload) {
	t.Helper()
	m := NewOrchestrationManager(&config.Config{})
	t.Cleanup(m.CloseAll)
	out := make(chan protocol.Envelope, 64)
	m.AttachOut(out)
	req := protocol.ApprovalRequestPayload{RunID: "run", RequestID: "request", ExecutionID: "execution", InstanceID: m.userInputInstanceID}
	ch := make(chan protocol.ApprovalResponsePayload, 1)
	m.approvals[req.RequestID] = orchestrationApproval{runID: req.RunID, request: req, ctx: context.Background(), ch: ch}
	return m, out, req, ch
}
func automationDecision(req protocol.ApprovalRequestPayload, id string) protocol.ApprovalDecisionPayload {
	return protocol.ApprovalDecisionPayload{RunID: req.RunID, RequestID: req.RequestID, ExecutionID: req.ExecutionID, InstanceID: req.InstanceID, DecisionID: id, Decision: "accept", SyncID: "sync-" + id}
}
func TestAutomationApprovalReadIsPureAndReconcileFences(t *testing.T) {
	m, out, req, ch := automationApprovalFixture(t)
	m.AutomationApprovalSync(protocol.ApprovalSyncPayload{RunID: req.RunID, SyncID: "read"})
	snapshot := readUserInputFrame[protocol.ApprovalSnapshotPayload](t, out, protocol.TypeApprovalSnapshot)
	if len(snapshot.Requests) != 1 || len(snapshot.Receipts) != 0 || len(m.approvalReceipts) != 0 {
		t.Fatalf("read altered recovery state: %#v", snapshot)
	}
	select {
	case <-ch:
		t.Fatal("read approved native callback")
	default:
	}
	m.AutomationApprovalSync(protocol.ApprovalSyncPayload{RunID: req.RunID, SyncID: "recover", InstanceID: req.InstanceID, Pending: []protocol.ApprovalPendingDecision{{RequestID: req.RequestID, ExecutionID: req.ExecutionID, DecisionID: "old"}}})
	snapshot = readUserInputFrame[protocol.ApprovalSnapshotPayload](t, out, protocol.TypeApprovalSnapshot)
	if len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Outcome != "not_sent" || len(snapshot.Requests) != 1 {
		t.Fatalf("fence = %#v", snapshot)
	}
	m.AutomationApprovalDecision(automationDecision(req, "old"))
	status := readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "not_sent" {
		t.Fatalf("old answer replay = %#v", status)
	}
	select {
	case <-ch:
		t.Fatal("fenced decision reached callback")
	default:
	}
	m.AutomationApprovalDecision(automationDecision(req, "new"))
	status = readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "queued" {
		t.Fatalf("new answer = %#v", status)
	}
	res := <-ch
	m.approvalCallbackConsumed(req, res)
	status = readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "delivered" || status.Status != "closed" {
		t.Fatalf("callback receipt = %#v", status)
	}
	m.AutomationApprovalDecision(automationDecision(req, "new"))
	status = readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "delivered" {
		t.Fatalf("duplicate receipt = %#v", status)
	}
	changed := automationDecision(req, "new")
	changed.Decision = "decline"
	m.AutomationApprovalDecision(changed)
	status = readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "unknown" {
		t.Fatalf("identity reused for different decision = %#v", status)
	}
	select {
	case <-ch:
		t.Fatal("duplicate executed twice")
	default:
	}
}
func TestAutomationApprovalConcurrentDecisionAndUnknown(t *testing.T) {
	m, out, req, ch := automationApprovalFixture(t)
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); m.AutomationApprovalDecision(automationDecision(req, id)) }(id)
	}
	wg.Wait()
	queued := 0
	for i := 0; i < 2; i++ {
		s := readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
		if s.Outcome == "queued" {
			queued++
		}
	}
	if queued != 1 || len(ch) != 1 {
		t.Fatalf("concurrent decisions queued %d callbacks=%d", queued, len(ch))
	}
	m2 := NewOrchestrationManager(&config.Config{})
	defer m2.CloseAll()
	out2 := make(chan protocol.Envelope, 4)
	m2.AttachOut(out2)
	m2.AutomationApprovalDecision(automationDecision(req, "a"))
	status := readUserInputFrame[protocol.ApprovalStatusPayload](t, out2, protocol.TypeApprovalStatus)
	if status.Outcome != "unknown" {
		t.Fatalf("restarted Bridge claimed known delivery: %#v", status)
	}
}
func TestAutomationApprovalCancelBeforeConsumeIsUnknown(t *testing.T) {
	m, out, req, ch := automationApprovalFixture(t)
	m.AutomationApprovalDecision(automationDecision(req, "a"))
	first := readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if first.Outcome != "queued" {
		t.Fatalf("premature acknowledgement = %#v", first)
	}
	m.removeApproval(req.RequestID)
	status := readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "unknown" || status.Status != "closed" {
		t.Fatalf("abandoned callback = %#v", status)
	}
	if len(ch) != 1 {
		t.Fatal("fixture callback queue unexpectedly consumed")
	}
	m.AutomationApprovalDecision(automationDecision(req, "a"))
	status = readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "unknown" {
		t.Fatalf("abandoned answer replay = %#v", status)
	}
}
func TestAutomationApprovalNativeLifecycle(t *testing.T) {
	m := NewOrchestrationManager(&config.Config{})
	defer m.CloseAll()
	out := make(chan protocol.Envelope, 16)
	m.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (orchestrationApprovalRequester{manager: m, runID: "run"}).RequestApproval(ctx, protocol.ApprovalRequestPayload{RequestID: "native"})
		done <- err
	}()
	req := readUserInputFrame[protocol.ApprovalRequestPayload](t, out, protocol.TypeApprovalRequest)
	if req.InstanceID == "" || req.ExecutionID != "run" {
		t.Fatalf("missing native identity: %#v", req)
	}
	m.AutomationApprovalDecision(automationDecision(req, "answer"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native approval callback did not return")
	}
	outcomes := map[string]bool{}
	for i := 0; i < 2; i++ {
		status := readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
		outcomes[status.Outcome] = true
	}
	if !outcomes["queued"] || !outcomes["delivered"] {
		t.Fatalf("lifecycle receipts = %#v", outcomes)
	}
}
func TestAutomationNativeAnswerRejectsStaleIdentity(t *testing.T) {
	m := NewOrchestrationManager(&config.Config{})
	defer m.CloseAll()
	out := make(chan protocol.Envelope, 8)
	m.AttachOut(out)
	writes := 0
	id, err := (orchestrationUserInputRequester{manager: m, runID: "run", executionID: "execution", cli: "codex"}).RegisterUserInput(context.Background(), true, testUserInputQuestion(), func(context.Context, protocol.UserInputResponsePayload) error { writes++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	for _, identity := range []struct{ instance, execution string }{{"old", "execution"}, {m.userInputInstanceID, "old"}} {
		if m.UserInputResponse(protocol.UserInputResponsePayload{RunID: "run", RequestID: id, InstanceID: identity.instance, ExecutionID: identity.execution, SubmissionID: "stale", Answers: map[string]protocol.UserInputAnswer{"choice": {OptionIDs: []string{"a"}}}}) {
			t.Fatal("stale native answer accepted")
		}
	}
	if writes != 0 {
		t.Fatalf("stale identities wrote %d answers", writes)
	}
}

func TestAutomationApprovalRecoveryBoundRetainsUnexpiredReceipts(t *testing.T) {
	m, out, req, ch := automationApprovalFixture(t)
	m.approvalReceipts = make(map[string]approvalReceiptRecord)
	for i := 0; i < approvalReceiptMax; i++ {
		id := fmt.Sprintf("prior-%d", i)
		m.approvalReceipts[approvalReceiptKey(id, id)] = approvalReceiptRecord{status: protocol.ApprovalStatusPayload{RunID: req.RunID, RequestID: id, ExecutionID: "old-execution", InstanceID: req.InstanceID, DecisionID: id, Status: "closed", Outcome: "delivered"}, at: time.Now()}
	}
	m.AutomationApprovalDecision(automationDecision(req, "new"))
	status := readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "not_sent" || len(ch) != 0 || len(m.approvalReceipts) != approvalReceiptMax {
		t.Fatalf("full history evicted evidence or delivered: %#v count=%d", status, len(m.approvalReceipts))
	}
	key := approvalReceiptKey("prior-0", "prior-0")
	expired := m.approvalReceipts[key]
	expired.at = time.Now().Add(-2 * userInputTerminalTTL)
	m.approvalReceipts[key] = expired
	m.AutomationApprovalDecision(automationDecision(req, "new"))
	status = readUserInputFrame[protocol.ApprovalStatusPayload](t, out, protocol.TypeApprovalStatus)
	if status.Outcome != "queued" || len(ch) != 1 {
		t.Fatalf("expired terminal evidence did not release capacity: %#v", status)
	}
}
func TestAutomationApprovalReconcileValidatesWholeIdentityBatch(t *testing.T) {
	m, out, req, ch := automationApprovalFixture(t)
	prior := protocol.ApprovalStatusPayload{RunID: "other", RequestID: "foreign", DecisionID: "foreign", ExecutionID: "other-execution", Status: "closed", Outcome: "delivered"}
	m.rememberApprovalLocked(prior)
	m.AutomationApprovalSync(protocol.ApprovalSyncPayload{RunID: req.RunID, SyncID: "batch", InstanceID: req.InstanceID, Pending: []protocol.ApprovalPendingDecision{
		{RequestID: req.RequestID, ExecutionID: req.ExecutionID, DecisionID: "safe"},
		{RequestID: prior.RequestID, ExecutionID: prior.ExecutionID, DecisionID: prior.DecisionID},
	}})
	snapshot := readUserInputFrame[protocol.ApprovalSnapshotPayload](t, out, protocol.TypeApprovalSnapshot)
	if snapshot.Error == "" || len(m.approvalReceipts) != 1 || len(ch) != 0 {
		t.Fatalf("invalid later identity partially fenced earlier answer: %#v", snapshot)
	}
}
