package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

type asyncFrameWriter struct{ frames chan []byte }

func (w *asyncFrameWriter) Write(frame []byte) (int, error) {
	w.frames <- append([]byte(nil), frame...)
	return len(frame), nil
}

func (*asyncFrameWriter) Close() error { return nil }

func readAsyncNativeRPC(t *testing.T, frames <-chan []byte, method string) map[string]any {
	t.Helper()
	select {
	case frame := <-frames:
		var rpc map[string]any
		if err := json.Unmarshal(frame, &rpc); err != nil {
			t.Fatal(err)
		}
		if rpc["method"] != method {
			t.Fatalf("native RPC = %#v, want %s", rpc, method)
		}
		return rpc
	case <-time.After(3 * time.Second):
		t.Fatalf("missing %s native RPC", method)
		return nil
	}
}

func replyAsyncNativeRPC(t *testing.T, client *appServerClient, rpc map[string]any, result string, rpcErr error) {
	t.Helper()
	id := int64(rpc["id"].(float64))
	client.mu.Lock()
	response := client.pending[id]
	client.mu.Unlock()
	if response == nil {
		t.Fatalf("native RPC %d has no pending response", id)
	}
	response <- appServerResponse{result: json.RawMessage(result), err: rpcErr}
}

func TestCodexAsyncQuestionsKeepRunAndNativeThreadUntilAllAnswered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewOrchestrationManager(&config.Config{})
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	writer := &asyncFrameWriter{frames: make(chan []byte, 4)}
	client := &appServerClient{stdin: writer, pending: make(map[int64]chan appServerResponse), events: make(chan appServerMessage, 8), stop: make(chan struct{})}
	scope := newAppServerTurnScope("native-thread")
	scope.setTurnID("turn-0")
	requester := orchestrationUserInputRequester{manager: manager, runID: "run-1", executionID: "attempt-1", workerID: "codex-a", cli: "codex"}
	done := make(chan appServerTurnResult, 1)
	runner := NewCodexAppServerRunner(&config.Config{})
	go runner.readEvents(ctx, client, RunnerRequest{NativeUserInput: true, UserInput: requester, Model: "official-a", ReasoningEffort: "high"}, scope, func(RunnerUpdate) {}, done)
	client.events <- appServerMessage{Method: "item/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-0", "item": map[string]any{"type": "agentMessage", "text": "Before the answer"}}}
	asyncItem := map[string]any{"type": "agentMessage", "delivery": "async", "id": "call-1", "text": "Queued follow-up inputs", "questions": []any{
		map[string]any{"title": "First?", "options": []any{"A", "B"}},
		map[string]any{"title": "Second?", "options": []any{"C", "D"}},
	}}
	client.events <- appServerMessage{Method: "item/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-0", "item": asyncItem}}
	client.events <- appServerMessage{Method: "turn/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-0", "turn": map[string]any{"id": "turn-0", "status": "completed", "items": []any{asyncItem}}}}
	first := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	second := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	for _, request := range []protocol.UserInputRequestPayload{first, second} {
		if request.RunID != "run-1" || request.ExecutionID != "attempt-1" || request.WorkerID != "codex-a" || request.Blocking || len(request.Questions) != 1 {
			t.Fatalf("async question identity = %#v", request)
		}
	}
	if first.Questions[0].ID != `["request_user_input_async","call-1",0]` || second.Questions[0].ID != `["request_user_input_async","call-1",1]` {
		t.Fatalf("native question ids = %q, %q", first.Questions[0].ID, second.Questions[0].ID)
	}
	select {
	case result := <-done:
		t.Fatalf("run ended before answers: %#v", result)
	default:
	}
	firstAccepted := make(chan bool, 1)
	go func() {
		firstAccepted <- manager.UserInputResponse(protocol.UserInputResponsePayload{RunID: "run-1", RequestID: first.RequestID, SubmissionID: "answer-1", Answers: map[string]protocol.UserInputAnswer{first.Questions[0].ID: {OptionIDs: []string{"A"}}}})
	}()
	start := readAsyncNativeRPC(t, writer.frames, "turn/start")
	params := start["params"].(map[string]any)
	if params["model"] != "official-a" || params["effort"] != "high" {
		t.Fatalf("async answer lost official parameters: %#v", params)
	}
	if params["threadId"] != "native-thread" || !strings.Contains(params["input"].([]any)[0].(map[string]any)["text"].(string), `"answer":"A"`) {
		t.Fatalf("first native answer = %#v", params)
	}
	replyAsyncNativeRPC(t, client, start, `{"turn":{"id":"turn-1"}}`, nil)
	if !<-firstAccepted {
		t.Fatal("first answer rejected")
	}
	select {
	case result := <-done:
		t.Fatalf("run ended with second question pending: %#v", result)
	default:
	}
	secondAccepted := make(chan bool, 1)
	go func() {
		secondAccepted <- manager.UserInputResponse(protocol.UserInputResponsePayload{RunID: "run-1", RequestID: second.RequestID, SubmissionID: "answer-2", Answers: map[string]protocol.UserInputAnswer{second.Questions[0].ID: {OptionIDs: []string{"D"}}}})
	}()
	steer := readAsyncNativeRPC(t, writer.frames, "turn/steer")
	params = steer["params"].(map[string]any)
	if params["threadId"] != "native-thread" || params["expectedTurnId"] != "turn-1" {
		t.Fatalf("second answer changed native context: %#v", params)
	}
	replyAsyncNativeRPC(t, client, steer, `{}`, nil)
	if !<-secondAccepted {
		t.Fatal("second answer rejected")
	}
	client.events <- appServerMessage{Method: "item/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-1", "item": map[string]any{"type": "agentMessage", "text": "Finished"}}}
	client.events <- appServerMessage{Method: "turn/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}}
	select {
	case result := <-done:
		if result.err != nil || result.result.Content != "Finished" {
			t.Fatalf("final result = %#v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native answer turn did not finish")
	}
}

func TestCodexAsyncSteerRaceFallsBackOnlyForKnownIdleError(t *testing.T) {
	if got := codexSteerActualTurnID(errors.New("expected active turn id `old` but found `new`"), "old"); got != "new" {
		t.Fatalf("active turn mismatch = %q", got)
	}
	if got := codexSteerActualTurnID(errors.New("expected active turn id `other` but found `new`"), "old"); got != "" {
		t.Fatalf("unrelated active turn mismatch was accepted: %q", got)
	}
	if got := codexSteerActualTurnID(errors.New("no active turn to steer"), "old"); got != "" {
		t.Fatalf("idle turn was treated as active: %q", got)
	}
}

func TestCodexAsyncAnswerFallsBackToSameThreadAfterSteerRace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewOrchestrationManager(&config.Config{})
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 8)
	manager.AttachOut(out)
	writer := &asyncFrameWriter{frames: make(chan []byte, 4)}
	client := &appServerClient{stdin: writer, pending: make(map[int64]chan appServerResponse), events: make(chan appServerMessage, 4), stop: make(chan struct{})}
	scope := newAppServerTurnScope("native-thread")
	scope.setTurnID("turn-0")
	requester := orchestrationUserInputRequester{manager: manager, runID: "run-1", executionID: "attempt-1", workerID: "codex-a", cli: "codex"}
	done := make(chan appServerTurnResult, 1)
	go NewCodexAppServerRunner(&config.Config{}).readEvents(ctx, client, RunnerRequest{NativeUserInput: true, UserInput: requester}, scope, func(RunnerUpdate) {}, done)
	client.events <- appServerMessage{Method: "item/completed", Params: map[string]any{
		"threadId": "native-thread", "turnId": "turn-0", "item": map[string]any{
			"type": "agentMessage", "delivery": "async", "id": "call-race", "questions": []any{map[string]any{"title": "Continue?", "options": []any{"Yes", "No"}}},
		},
	}}
	request := readUserInputFrame[protocol.UserInputRequestPayload](t, out, protocol.TypeUserInputRequest)
	accepted := make(chan bool, 1)
	go func() {
		accepted <- manager.UserInputResponse(protocol.UserInputResponsePayload{RunID: "run-1", RequestID: request.RequestID, SubmissionID: "answer", Answers: map[string]protocol.UserInputAnswer{request.Questions[0].ID: {OptionIDs: []string{"Yes"}}}})
	}()
	steer := readAsyncNativeRPC(t, writer.frames, "turn/steer")
	if steer["params"].(map[string]any)["expectedTurnId"] != "turn-0" {
		t.Fatalf("unexpected steer = %#v", steer)
	}
	replyAsyncNativeRPC(t, client, steer, "", errors.New("no active turn to steer"))
	start := readAsyncNativeRPC(t, writer.frames, "turn/start")
	if start["params"].(map[string]any)["threadId"] != "native-thread" {
		t.Fatalf("fallback left native thread: %#v", start)
	}
	replyAsyncNativeRPC(t, client, start, `{"turn":{"id":"turn-1"}}`, nil)
	if !<-accepted {
		t.Fatal("raced answer rejected")
	}
	// The original completion can arrive after the new turn has started.
	client.events <- appServerMessage{Method: "turn/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-0", "turn": map[string]any{"id": "turn-0", "status": "completed"}}}
	client.events <- appServerMessage{Method: "item/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-1", "item": map[string]any{"type": "agentMessage", "text": "Answered"}}}
	client.events <- appServerMessage{Method: "turn/completed", Params: map[string]any{"threadId": "native-thread", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}}
	select {
	case result := <-done:
		if result.err != nil || result.result.Content != "Answered" {
			t.Fatalf("raced answer result = %#v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raced answer turn did not finish")
	}
}

func TestCodexAsyncAnswerEnvelopeEscapesCustomText(t *testing.T) {
	answer := "  Other: \"quoted\"\nline  "
	question := protocol.UserInputQuestion{ID: `["request_user_input_async","call",0]`, Question: "Choose\nnow"}
	message, err := codexAsyncUserInputText(question, protocol.UserInputAnswer{CustomText: &answer})
	if err != nil {
		t.Fatal(err)
	}
	const open = "<send_user_message_question_reply>\n"
	const close = "\n</send_user_message_question_reply>"
	if !strings.HasPrefix(message, open) || !strings.HasSuffix(message, close) {
		t.Fatalf("invalid native answer framing: %q", message)
	}
	var entries []map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(message, open), close)), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["answer"] != answer || entries[0]["question"] != "Choose now" || entries[0]["questionItemId"] != question.ID {
		t.Fatalf("native answer payload = %#v", entries)
	}
}
