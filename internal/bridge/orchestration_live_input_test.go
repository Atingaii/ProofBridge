package bridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func liveInputTestManager(t *testing.T) (*OrchestrationManager, chan protocol.Envelope) {
	t.Helper()
	m := NewOrchestrationManager(&config.Config{})
	t.Cleanup(m.CloseAll)
	out := make(chan protocol.Envelope, 32)
	m.AttachOut(out)
	m.executions["run:attempt"] = orchestrationExecution{runID: "run"}
	return m, out
}
func TestCodexLiveInputQueueAndImmediateKeepNativeThread(t *testing.T) {
	for _, mode := range []string{"queue", "immediate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m, out := liveInputTestManager(t)
			box := m.liveInputFor("run:attempt")
			writer := &asyncFrameWriter{frames: make(chan []byte, 8)}
			client := &appServerClient{stdin: writer, pending: make(map[int64]chan appServerResponse), events: make(chan appServerMessage, 8), stop: make(chan struct{})}
			scope := newAppServerTurnScope("thread")
			scope.setTurnID("original")
			done := make(chan appServerTurnResult, 1)
			go NewCodexAppServerRunner(&config.Config{}).readEvents(ctx, client, RunnerRequest{LiveInput: box, Model: "official-b", ReasoningEffort: "low"}, scope, func(RunnerUpdate) {}, done)
			p := protocol.OrchestrationInputPayload{RunID: "run", ExecutionID: "run:attempt", MessageID: "one", Prompt: "追加检查", DeliveryMode: mode}
			m.dispatchLiveInput(p)
			method := "turn/steer"
			nativeTurn := "original"
			if mode == "queue" {
				select {
				case f := <-writer.frames:
					t.Fatalf("queue interrupted native turn: %s", f)
				case <-time.After(30 * time.Millisecond):
				}
				client.events <- appServerMessage{Method: "turn/completed", Params: map[string]any{"threadId": "thread", "turnId": "original", "turn": map[string]any{"id": "original", "status": "completed"}}}
				method = "turn/start"
				nativeTurn = "followup"
			}
			rpc := readAsyncNativeRPC(t, writer.frames, method)
			params := rpc["params"].(map[string]any)
			if method == "turn/start" && (params["model"] != "official-b" || params["effort"] != "low") {
				t.Fatalf("queued turn lost official parameters: %#v", params)
			}
			if params["threadId"] != "thread" {
				t.Fatal(params)
			}
			replyAsyncNativeRPC(t, client, rpc, `{"turn":{"id":"followup"}}`, nil)
			receipt := readUserInputFrame[protocol.OrchestrationInputStatusPayload](t, out, protocol.TypeOrchestrationInputStatus)
			if receipt.Status != "delivered" {
				t.Fatal(receipt)
			}
			m.dispatchLiveInput(p)
			select {
			case f := <-writer.frames:
				t.Fatalf("duplicate native write: %s", f)
			case <-time.After(20 * time.Millisecond):
			}
			client.events <- appServerMessage{Method: "item/completed", Params: map[string]any{"threadId": "thread", "turnId": nativeTurn, "item": map[string]any{"type": "agentMessage", "text": "Finished additional requirement"}}}
			client.events <- appServerMessage{Method: "turn/completed", Params: map[string]any{"threadId": "thread", "turnId": nativeTurn, "turn": map[string]any{"id": nativeTurn, "status": "completed"}}}
			select {
			case result := <-done:
				if result.err != nil {
					t.Fatal(result.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("native input did not complete")
			}
		})
	}
}
func readClaudeLiveFrame(t *testing.T, frames <-chan []byte) map[string]any {
	t.Helper()
	select {
	case raw := <-frames:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("missing Claude frame")
		return nil
	}
}
func TestClaudeLiveInputQueueLifecycleProtectsOriginalResult(t *testing.T) {
	m, out := liveInputTestManager(t)
	box := m.liveInputFor("run:attempt")
	writer := &asyncFrameWriter{frames: make(chan []byte, 8)}
	live := newClaudeLiveInput(box, writer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go live.run(ctx)
	p := protocol.OrchestrationInputPayload{RunID: "run", ExecutionID: "run:attempt", MessageID: "arbitrary-message-id", Prompt: "new goal", DeliveryMode: "queue"}
	m.dispatchLiveInput(p)
	frame := readClaudeLiveFrame(t, writer.frames)
	id := frame["uuid"].(string)
	if frame["type"] != "user" || frame["priority"] != "next" || id == p.MessageID {
		t.Fatal(frame)
	}
	receipt := readUserInputFrame[protocol.OrchestrationInputStatusPayload](t, out, protocol.TypeOrchestrationInputStatus)
	if receipt.Status != "delivered" {
		t.Fatal(receipt)
	}
	if done, err := live.observe(map[string]any{"type": "result"}); done || err != nil {
		t.Fatal("original result ended queued instruction", done, err)
	}
	if done, err := live.observe(map[string]any{"type": "command_lifecycle", "command_uuid": id, "state": "started"}); done || err != nil {
		t.Fatal(done, err)
	}
	if done, err := live.observe(map[string]any{"type": "result"}); done || err != nil {
		t.Fatal("result ahead of lifecycle completed", done, err)
	}
	if done, err := live.observe(map[string]any{"type": "command_lifecycle", "command_uuid": id, "state": "completed"}); !done || err != nil {
		t.Fatal("followup did not complete", done, err)
	}
}
func TestClaudeLiveInputImmediateWaitsForInterruptAck(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "accepted", false: "rejected"}[success], func(t *testing.T) {
			m, out := liveInputTestManager(t)
			box := m.liveInputFor("run:attempt")
			writer := &asyncFrameWriter{frames: make(chan []byte, 8)}
			live := newClaudeLiveInput(box, writer)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go live.run(ctx)
			m.dispatchLiveInput(protocol.OrchestrationInputPayload{RunID: "run", ExecutionID: "run:attempt", MessageID: "now", Prompt: "change now", DeliveryMode: "immediate"})
			control := readClaudeLiveFrame(t, writer.frames)
			if control["type"] != "control_request" || control["request"].(map[string]any)["subtype"] != "interrupt" {
				t.Fatal(control)
			}
			select {
			case f := <-writer.frames:
				t.Fatalf("input sent before interrupt acknowledgment: %s", f)
			case <-time.After(20 * time.Millisecond):
			}
			if done, _ := live.observe(map[string]any{"type": "result"}); done {
				t.Fatal("old turn result completed the execution")
			}
			subtype := "success"
			if !success {
				subtype = "error"
			}
			_, _ = live.observe(map[string]any{"type": "control_response", "response": map[string]any{"request_id": control["request_id"], "subtype": subtype, "error": "unsupported"}})
			if success {
				frame := readClaudeLiveFrame(t, writer.frames)
				if frame["type"] != "user" {
					t.Fatal(frame)
				}
			}
			receipt := readUserInputFrame[protocol.OrchestrationInputStatusPayload](t, out, protocol.TypeOrchestrationInputStatus)
			if success && receipt.Status != "delivered" || !success && receipt.Status != "failed" {
				t.Fatal(receipt)
			}
			if !success {
				select {
				case f := <-writer.frames:
					t.Fatalf("instruction written after rejected interrupt: %s", f)
				default:
				}
			}
		})
	}
}

func TestClaudeLiveInputMissingInterruptReceiptWakesFinishedScanner(t *testing.T) {
	m, out := liveInputTestManager(t)
	box := m.liveInputFor("run:attempt")
	writer := &asyncFrameWriter{frames: make(chan []byte, 8)}
	live := newClaudeLiveInput(box, writer)
	woke := make(chan struct{}, 1)
	live.wakeAfterFailedInput = func() { woke <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go live.run(ctx)
	m.dispatchLiveInput(protocol.OrchestrationInputPayload{RunID: "run", ExecutionID: "run:attempt", MessageID: "missing-ack", Prompt: "change", DeliveryMode: "immediate"})
	_ = readClaudeLiveFrame(t, writer.frames)
	if done, _ := live.observe(map[string]any{"type": "result"}); done {
		t.Fatal("pending interrupt completed early")
	}
	// Cancellation and timeout share the same interrupt error/settlement path.
	cancel()
	receipt := readUserInputFrame[protocol.OrchestrationInputStatusPayload](t, out, protocol.TypeOrchestrationInputStatus)
	if receipt.Status != "failed" {
		t.Fatal(receipt)
	}
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("finished stdout scanner was left blocked")
	}
	select {
	case f := <-writer.frames:
		t.Fatalf("sent user input without acknowledgment: %s", f)
	default:
	}
}
