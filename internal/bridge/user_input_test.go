package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestCodexUserInputRoundTripAndSnapshot(t *testing.T) {
	params := map[string]any{
		"isBlocking": false,
		"questions": []any{map[string]any{
			"id": "choice", "header": "Choose", "question": "Alpha or Beta?", "isOther": true,
			"options": []any{map[string]any{"label": "Alpha", "description": "First"}, map[string]any{"label": "Beta", "description": "Second"}},
		}},
	}
	questions, blocking, err := decodeCodexUserInput(params)
	if err != nil || blocking || len(questions) != 1 || !questions[0].AllowCustom {
		t.Fatalf("Codex question decode: %#v, blocking=%v, err=%v", questions, blocking, err)
	}
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "run:attempt", workerID: "codex", turnID: "turn", cli: "codex"}
	var native any
	id, err := requester.RegisterUserInput(ctx, blocking, questions, func(_ context.Context, res protocol.UserInputResponsePayload) error {
		native = codexUserInputResult(questions, res.Answers)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	req := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	if req.RequestID != id || req.RunID != "run" || req.ExecutionID != "run:attempt" || req.Revision != 1 || req.Blocking {
		t.Fatalf("request = %#v", req)
	}
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "sync1"})
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if snapshot.Revision != 1 || len(snapshot.Requests) != 1 || len(snapshot.Statuses) != 0 {
		t.Fatalf("pending snapshot = %#v", snapshot)
	}
	answer := protocol.UserInputResponsePayload{
		RunID: "run", RequestID: id, SubmissionID: "submit1",
		Answers: map[string]protocol.UserInputAnswer{"choice": {OptionIDs: []string{"Alpha"}}},
	}
	if !manager.UserInputResponse(answer) {
		t.Fatal("valid native answer was not accepted")
	}
	status := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if status.Status != "delivered" || status.Revision != 2 || status.SubmissionID != "submit1" {
		t.Fatalf("status = %#v", status)
	}
	wantNative := map[string]any{"answers": map[string]any{"choice": map[string]any{"answers": []string{"Alpha"}}}}
	if !reflect.DeepEqual(native, wantNative) {
		t.Fatalf("native response = %#v", native)
	}
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "sync2"})
	snapshot = readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if snapshot.Revision != 2 || len(snapshot.Requests) != 1 || len(snapshot.Statuses) != 1 || snapshot.Statuses[0].Status != "delivered" {
		t.Fatalf("delivered snapshot = %#v", snapshot)
	}
	requester.CloseUserInput(id, "resolved")
	closed := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if closed.Status != "closed" || closed.Revision != 3 {
		t.Fatalf("closed status = %#v", closed)
	}
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "sync3"})
	snapshot = readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if snapshot.Revision != 3 || len(snapshot.Requests) != 0 || len(snapshot.Statuses) != 0 {
		t.Fatalf("closed snapshot = %#v", snapshot)
	}
	if len(snapshot.Closed) != 1 || snapshot.Closed[0].Status != "closed" || len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Outcome != "delivered" {
		t.Fatalf("closed snapshot lost transport receipt = %#v", snapshot)
	}
	manager.UserInputResponse(answer)
	retried := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if retried.Status != "closed" || retried.Receipt == nil || retried.Receipt.Outcome != "delivered" {
		t.Fatalf("duplicate after close = %#v", retried)
	}
	manager.mu.Lock()
	retained := manager.userInputs[id]
	referencesRetained := retained == nil || retained.ctx != nil || retained.deliver != nil || len(retained.request.Questions) != 0 || retained.request.WorkerID != ""
	manager.mu.Unlock()
	if referencesRetained {
		t.Fatalf("closed entry retained native references = %#v", retained)
	}
}

func TestUserInputEmptySnapshotEncodesArray(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 1)
	manager.AttachOut(out)
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "sync"})
	env := <-out
	if !bytes.Contains(env.Payload, []byte(`"requests":[]`)) {
		t.Fatalf("empty snapshot requests must be an array: %s", env.Payload)
	}
}

func TestUserInputSyncFencesUnreceivedSubmission(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", cli: "codex"}
	writes := 0
	id, err := requester.RegisterUserInput(context.Background(), true, testUserInputQuestion(), func(context.Context, protocol.UserInputResponsePayload) error {
		writes++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-out // original question
	manager.UserInputSync(protocol.UserInputSyncPayload{
		RunID: "run", SyncID: "fence", InstanceID: manager.userInputInstanceID, ClientVersion: 3,
		PendingSubmissions: []protocol.UserInputPendingSubmission{{RequestID: id, ExecutionID: "attempt", SubmissionID: "old"}},
	})
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if len(snapshot.Reconciliations) != 1 || snapshot.Reconciliations[0].Receipt == nil || snapshot.Reconciliations[0].Receipt.Outcome != "not_sent" {
		t.Fatalf("missing authoritative fence: %#v", snapshot)
	}
	manager.dispatchUserInput(testUserInputAnswer(id, "old"))
	stale := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if stale.Receipt == nil || stale.Receipt.Outcome != "not_sent" || writes != 0 {
		t.Fatalf("late old answer wrote to CLI: %#v, writes=%d", stale, writes)
	}
	manager.UserInputResponse(testUserInputAnswer(id, "new"))
	accepted := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if accepted.Status != "delivered" || writes != 1 {
		t.Fatalf("new submission = %#v, writes=%d", accepted, writes)
	}
}

func TestUserInputSyncRejectsWrongInstanceWithoutFencing(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 8)
	manager.AttachOut(out)
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", cli: "codex"}
	id, err := requester.RegisterUserInput(context.Background(), true, testUserInputQuestion(), func(context.Context, protocol.UserInputResponsePayload) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-out
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "wrong", InstanceID: "other", ClientVersion: 3,
		PendingSubmissions: []protocol.UserInputPendingSubmission{{RequestID: id, ExecutionID: "attempt", SubmissionID: "old"}}})
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if snapshot.ReconcileError == "" || len(snapshot.Reconciliations) != 0 {
		t.Fatalf("wrong instance was confirmed: %#v", snapshot)
	}
	manager.UserInputResponse(testUserInputAnswer(id, "old"))
	status := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if status.Status != "delivered" {
		t.Fatalf("wrong instance fenced valid answer: %#v", status)
	}
}

func TestUserInputReaderReservesDuplicateBeforeQueueBusy(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", workerID: "codex", cli: "codex"}
	id, err := requester.RegisterUserInput(ctx, true, testUserInputQuestion(), func(ctx context.Context, _ protocol.UserInputResponsePayload) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-out
	answer := testUserInputAnswer(id, "same")
	client := &Client{orchestrations: manager}
	client.handleEnvelope(context.Background(), protocol.MustEnvelope(protocol.TypeUserInputResponse, "", answer), out)
	queued := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if queued.Status != "queued" {
		t.Fatalf("first admission = %#v", queued)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first native delivery did not start")
	}
	// Simulate saturation precisely at the duplicate's reader admission.
	// Its reserved lease must take precedence over the capacity branch.
	manager.userInputDispatchMu.Lock()
	manager.userInputDispatchActive = userInputDispatchCapacity
	manager.userInputDispatchMu.Unlock()
	client.handleEnvelope(context.Background(), protocol.MustEnvelope(protocol.TypeUserInputResponse, "", answer), out)
	duplicate := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if duplicate.Status != "queued" || duplicate.Receipt != nil {
		t.Fatalf("duplicate of admitted submission was falsely rejected: %#v", duplicate)
	}
	manager.userInputDispatchMu.Lock()
	manager.userInputDispatchActive = 1
	manager.userInputDispatchMu.Unlock()
	manager.CloseUserInput(id, "native ended")
}

func TestUserInputPerClientQueueDoesNotStarveOtherNativeClient(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	deliveredB := make(chan struct{})
	client := &Client{orchestrations: manager}
	requesterA := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", workerID: "codex-a", cli: "codex"}
	ids := make([]string, 8)
	for i := range ids {
		id, err := requesterA.RegisterUserInput(ctx, true, testUserInputQuestion(), func(ctx context.Context, _ protocol.UserInputResponsePayload) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		<-out
	}
	requesterB := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", workerID: "claude-b", cli: "claude"}
	idB, err := requesterB.RegisterUserInput(ctx, true, testUserInputQuestion(), func(context.Context, protocol.UserInputResponsePayload) error {
		close(deliveredB)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-out
	for i, id := range ids {
		client.handleEnvelope(context.Background(), protocol.MustEnvelope(protocol.TypeUserInputResponse, "", testUserInputAnswer(id, "a"+string(rune('0'+i)))), out)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native client A did not start")
	}
	client.handleEnvelope(context.Background(), protocol.MustEnvelope(protocol.TypeUserInputResponse, "", testUserInputAnswer(idB, "b")), out)
	select {
	case <-deliveredB:
	case <-time.After(time.Second):
		t.Fatal("A's queued submissions occupied every worker and starved B")
	}
	for _, id := range ids {
		manager.CloseUserInput(id, "native ended")
	}
}

func TestUserInputQueueBusyReceiptIsRecoverableAndNewSubmissionCanProceed(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "attempt", workerID: "codex", cli: "codex"}
	writes := 0
	entered := make(chan struct{})
	release := make(chan struct{})
	id, err := requester.RegisterUserInput(context.Background(), true, testUserInputQuestion(), func(context.Context, protocol.UserInputResponsePayload) error {
		writes++
		close(entered)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-out
	manager.userInputDispatchMu.Lock()
	manager.userInputDispatchActive = userInputDispatchCapacity
	manager.userInputDispatchMu.Unlock()
	manager.dispatchUserInput(testUserInputAnswer(id, "busy"))
	busy := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if busy.Lifecycle != "open" || busy.Phase != "queue" || busy.Receipt == nil || busy.Receipt.Outcome != "not_sent" {
		t.Fatalf("busy result did not prove no write: %#v", busy)
	}
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "recover"})
	snapshot := readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if len(snapshot.Receipts) != 1 || snapshot.Receipts[0].Outcome != "not_sent" || len(snapshot.Requests) != 1 {
		t.Fatalf("busy receipt missing on sync: %#v", snapshot)
	}
	manager.dispatchUserInput(testUserInputAnswer(id, "busy"))
	duplicate := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if duplicate.Receipt == nil || duplicate.Receipt.Outcome != "not_sent" || writes != 0 {
		t.Fatalf("busy submission was re-admitted: %#v, writes=%d", duplicate, writes)
	}
	manager.userInputDispatchMu.Lock()
	manager.userInputDispatchActive = 0
	manager.userInputDispatchMu.Unlock()
	manager.dispatchUserInput(testUserInputAnswer(id, "corrected"))
	queued := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if queued.Status != "queued" {
		t.Fatalf("new submission was not queued: %#v", queued)
	}
	<-entered
	manager.dispatchUserInput(testUserInputAnswer(id, "busy"))
	oldWhileQueued := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if oldWhileQueued.Status != "rejected" || oldWhileQueued.Phase != "queue" || oldWhileQueued.Receipt == nil || oldWhileQueued.Receipt.Outcome != "not_sent" {
		t.Fatalf("submission A lost its original result while B was in flight: %#v", oldWhileQueued)
	}
	close(release)
	delivered := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if delivered.Status != "delivered" || delivered.Receipt == nil || delivered.Receipt.Outcome != "delivered" || writes != 1 {
		t.Fatalf("new submission did not finish: %#v, writes=%d", delivered, writes)
	}
	manager.dispatchUserInput(testUserInputAnswer(id, "busy"))
	old := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if old.Receipt == nil || old.Receipt.Outcome != "not_sent" || writes != 1 {
		t.Fatalf("submission A lost its original result after B: %#v, writes=%d", old, writes)
	}
	manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "both"})
	snapshot = readUserInputFrame[protocol.UserInputSnapshotPayload](t, out, protocol.TypeUserInputSnapshot)
	if len(snapshot.Receipts) != 2 {
		t.Fatalf("snapshot did not retain both submission receipts: %#v", snapshot)
	}
}

func testUserInputQuestion() []protocol.UserInputQuestion {
	return []protocol.UserInputQuestion{{ID: "q", Question: "Choose", SelectionMode: "single", Options: []protocol.UserInputOption{{ID: "a", Label: "A"}}}}
}

func testUserInputAnswer(id, submissionID string) protocol.UserInputResponsePayload {
	return protocol.UserInputResponsePayload{RunID: "run", RequestID: id, SubmissionID: submissionID, Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a"}}}}
}

func TestUserInputRejectsInvalidAndStaleAnswers(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 8)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "run", cli: "codex"}
	id, err := requester.RegisterUserInput(ctx, false, []protocol.UserInputQuestion{{
		ID: "q", Question: "Choose", SelectionMode: "single", Options: []protocol.UserInputOption{{ID: "a", Label: "A"}},
	}}, func(context.Context, protocol.UserInputResponsePayload) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-out
	wrongRun := protocol.UserInputResponsePayload{RunID: "other", RequestID: id, SubmissionID: "s1", Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a"}}}}
	if manager.UserInputResponse(wrongRun) {
		t.Fatal("cross-run answer accepted")
	}
	invalid := protocol.UserInputResponsePayload{RunID: "run", RequestID: id, SubmissionID: "s2", Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a", "a"}}}}
	if !manager.UserInputResponse(invalid) {
		t.Fatal("invalid answer was not acknowledged")
	}
	status := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if status.Status != "rejected" {
		t.Fatalf("status = %#v", status)
	}
	cancel()
	closed := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if closed.Status != "closed" {
		t.Fatalf("closed = %#v", closed)
	}
	valid := protocol.UserInputResponsePayload{RunID: "run", RequestID: id, SubmissionID: "s3", Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a"}}}}
	manager.UserInputResponse(valid)
	status = readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if status.Status != "closed" {
		t.Fatalf("late answer = %#v", status)
	}
}

func TestUserInputSlowNativeWriteDoesNotBlockOtherRequests(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 12)
	manager.AttachOut(out)
	requester := orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "run", cli: "codex"}
	question := []protocol.UserInputQuestion{{ID: "q", Question: "Choose", SelectionMode: "single", Options: []protocol.UserInputOption{{ID: "a", Label: "A"}}}}
	started, release := make(chan struct{}), make(chan struct{})
	first, err := requester.RegisterUserInput(context.Background(), false, question, func(context.Context, protocol.UserInputResponsePayload) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := requester.RegisterUserInput(context.Background(), false, question, func(context.Context, protocol.UserInputResponsePayload) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-out
	<-out
	delivered := make(chan bool, 1)
	go func() {
		delivered <- manager.UserInputResponse(protocol.UserInputResponsePayload{
			RunID: "run", RequestID: first, SubmissionID: "first",
			Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a"}}},
		})
	}()
	<-started
	synced := make(chan struct{})
	go func() {
		manager.UserInputSync(protocol.UserInputSyncPayload{RunID: "run", SyncID: "sync"})
		manager.CloseUserInput(second, "resolved")
		close(synced)
	}()
	select {
	case <-synced:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("slow native write blocked unrelated sync and close")
	}
	close(release)
	if !<-delivered {
		t.Fatal("native response was not delivered")
	}
}

func TestBridgeRejectsUnknownUserInputWithVisibleStatus(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 2)
	manager.AttachOut(out)
	client := &Client{orchestrations: manager}
	client.handleEnvelope(context.Background(), protocol.MustEnvelope(protocol.TypeUserInputResponse, "", protocol.UserInputResponsePayload{
		RequestID: "gone", RunID: "run", SubmissionID: "retry",
		Answers: map[string]protocol.UserInputAnswer{"q": {OptionIDs: []string{"a"}}},
	}), out)
	status := readUserInputFrame[protocol.UserInputStatusPayload](t, out, protocol.TypeUserInputStatus)
	if status.Status != "closed" || status.Revision == 0 || status.RequestID != "gone" || status.SubmissionID != "retry" {
		t.Fatalf("unknown answer status = %#v", status)
	}
}

func TestClaudeUserInputPreservesQuestionsAndCustomAnswer(t *testing.T) {
	raw := json.RawMessage(`{"questions":[{"question":"Pick one","header":"Decision","options":[{"label":"Alpha","description":"First"}],"multiSelect":false},{"question":"Pick several","header":"Items","options":[{"label":"A","description":"A item"},{"label":"B","description":"B item"}],"multiSelect":true}]}`)
	questions, original, err := decodeClaudeUserInput(raw)
	if err != nil || len(questions) != 2 || questions[1].SelectionMode != "multiple" {
		t.Fatalf("Claude decode = %#v, %v", questions, err)
	}
	custom := "My own answer"
	additional := "Another item"
	if err := validateUserInputAnswers(questions, map[string]protocol.UserInputAnswer{
		"q0": {CustomText: &custom}, "q1": {OptionIDs: []string{"A"}, CustomText: &additional},
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := claudeUserInputResult(questions, original, map[string]protocol.UserInputAnswer{
		"q0": {CustomText: &custom}, "q1": {OptionIDs: []string{"A"}, CustomText: &additional},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Questions json.RawMessage `json:"questions"`
		Answers   map[string]any  `json:"answers"`
	}
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Questions, original) || decoded.Answers["Pick one"] != custom || !reflect.DeepEqual(decoded.Answers["Pick several"], []any{"A", additional}) {
		t.Fatalf("updatedInput = %s", updated)
	}
	duplicate := json.RawMessage(`{"questions":[{"question":"Same"},{"question":"Same"}]}`)
	if _, _, err := decodeClaudeUserInput(duplicate); err == nil {
		t.Fatal("duplicate Claude question text would overwrite native answer")
	}
}

func TestClaudeUserInputSocketWaitsForBrowser(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 8)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, serverConn := net.Pipe()
	defer client.Close()
	server := &claudeApprovalServer{userInput: orchestrationUserInputRequester{manager: manager, runID: "run", executionID: "run", cli: "claude"}}
	go handleClaudeApprovalSocketConn(ctx, serverConn, server)
	if err := json.NewEncoder(client).Encode(claudeApprovalSocketRequest{
		RequestID: "native", ToolName: "AskUserQuestion",
		Input: json.RawMessage(`{"questions":[{"question":"Choose","header":"Choice","options":[{"label":"Yes","description":"Proceed"}],"multiSelect":false}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	request := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	if !request.Blocking || request.CLI != "claude" {
		t.Fatalf("request = %#v", request)
	}
	done := make(chan bool, 1)
	go func() {
		done <- manager.UserInputResponse(protocol.UserInputResponsePayload{
			RunID: "run", RequestID: request.RequestID, SubmissionID: "submit",
			Answers: map[string]protocol.UserInputAnswer{"q0": {OptionIDs: []string{"Yes"}}},
		})
	}()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	var response claudeApprovalSocketResponse
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !<-done || response.Decision != "accept" || len(response.UpdatedInput) == 0 {
		t.Fatalf("Claude response = %#v", response)
	}
}

func readUserInputFrame[T any](t *testing.T, out <-chan protocol.Envelope, kind string) T {
	t.Helper()
	var zero T
	select {
	case env := <-out:
		if env.Type != kind {
			t.Fatalf("frame kind = %s, want %s", env.Type, kind)
		}
		value, err := protocol.Decode[T](env)
		if err != nil {
			t.Fatal(err)
		}
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", kind)
		return zero
	}
}
