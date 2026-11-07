package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func cancellationAckFixture(t *testing.T) (*Server, *store.Store, string, string, store.OrchestrationRun, protocol.TaskAttemptRef) {
	t.Helper()
	s, st, uid, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	if _, err := st.PutAutomationSettings(ctx, uid, "account", json.RawMessage(`{"email":"owner@example.test"}`), 0); err != nil {
		t.Fatal(err)
	}
	run := createOrchestrationRun(t, st, uid, agentID)
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "isolated cancellation test"}); err != nil {
		t.Fatal(err)
	}
	graph, err := st.CreateOrchestrationTaskGraph(ctx, run.ID, `{}`, "cancel-graph", []store.CreateTaskSpec{{Name: "review", Role: store.TaskRoleReviewer, WorkerSlot: "codex", PayloadDigest: "cancel-review", PayloadJSON: `{}`}})
	if err != nil {
		t.Fatal(err)
	}
	task, attempt, claimed, err := st.ClaimReadyTask(ctx, graph.Tasks[0].ID, "")
	if err != nil || !claimed {
		t.Fatal("cannot claim isolated review attempt")
	}
	ref := protocol.TaskAttemptRef{GraphID: graph.ID, TaskID: task.ID, AttemptID: attempt.ID, Role: task.Role, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest}
	s.handleOrchestrationEventFromAgent(ctx, agentID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{RunID: run.ID, Kind: "run.start", Task: &ref}))
	if err = st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCanceling, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.persistAndDispatchOrchestrationCancel(ctx, run); err != nil {
		t.Fatal(err)
	}
	return s, st, uid, agentID, run, ref
}

func sendCancellationAck(s *Server, agentID, runID string, ref protocol.TaskAttemptRef) {
	s.handleOrchestrationEventFromAgent(context.Background(), agentID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{RunID: runID, Kind: "run.cancelled", Task: &ref, Content: "Bridge acknowledged user cancellation", RunConclusion: &protocol.RunConclusion{Outcome: "canceled", Summary: "User cancellation acknowledged"}}))
}

func assertOneCancellationReport(t *testing.T, st *store.Store, uid, runID string) store.AutomationSubmission {
	t.Helper()
	ctx := context.Background()
	events, err := st.ListOrchestrationEvents(ctx, runID, 100)
	if err != nil {
		t.Fatal(err)
	}
	public := 0
	for _, event := range events {
		if event.Task == nil && event.Kind == "run.cancelled" {
			public++
		}
	}
	if public != 1 {
		t.Fatalf("public cancellation count=%d, want 1", public)
	}
	sub, err := st.LatestAutomationSubmissionForRun(ctx, uid, runID)
	if err != nil || sub.State != "terminal" {
		t.Fatal("cancellation submission not terminal")
	}
	rows, err := st.ListAutomationNotifications(ctx, uid, runID, sub.ID, 100)
	if err != nil || len(rows) != 1 || rows[0].Kind != "completion" {
		t.Fatal("cancellation completion report was duplicated or absent")
	}
	return sub
}

func TestDurableCancellationAckPreventsLateTimeout(t *testing.T) {
	s, st, uid, agentID, run, ref := cancellationAckFixture(t)
	ctx := context.Background()
	sendCancellationAck(s, agentID, run.ID, ref)
	loaded, err := st.OrchestrationRunByID(ctx, run.ID, uid)
	if err != nil || loaded.Status != store.OrchestrationCanceled || loaded.Error != "" {
		t.Fatalf("valid Bridge cancellation did not settle run: status=%s error=%q err=%v", loaded.Status, loaded.Error, err)
	}
	graph, err := st.TaskGraphByRun(ctx, run.ID)
	if err != nil || graph.Status != store.TaskGraphCanceled {
		t.Fatal("real graph cancellation not preserved")
	}
	before := assertOneCancellationReport(t, st, uid, run.ID)
	if _, changed, err := st.CancelOrchestrationRunIfStillCanceling(ctx, run.ID, "selected CLI endpoint did not acknowledge cancellation before timeout"); err != nil || changed {
		t.Fatal("late timeout overwrote acknowledged cancellation")
	}
	sendCancellationAck(s, agentID, run.ID, ref)
	after := assertOneCancellationReport(t, st, uid, run.ID)
	if string(before.Result) != string(after.Result) || before.TerminalSeq != after.TerminalSeq {
		t.Fatal("duplicate ACK or timeout changed the frozen submission")
	}
	loaded, err = st.OrchestrationRunByID(ctx, run.ID, uid)
	if err != nil || loaded.Error != "" {
		t.Fatal("late timeout invented a cancellation failure")
	}
}

func TestDurableCancellationTimeoutRemainsWhenNoValidAck(t *testing.T) {
	for _, invalidAck := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-ack", true: "mismatched-ack"}[invalidAck], func(t *testing.T) {
			s, st, uid, agentID, run, ref := cancellationAckFixture(t)
			ctx := context.Background()
			if invalidAck {
				bad := ref
				bad.PayloadDigest = "stale-payload"
				sendCancellationAck(s, agentID, run.ID, bad)
			}
			reason := "selected CLI endpoint did not acknowledge cancellation before timeout"
			if _, changed, err := st.CancelOrchestrationRunIfStillCanceling(ctx, run.ID, reason); err != nil || !changed {
				t.Fatal("genuine missing ACK did not time out")
			}
			before := assertOneCancellationReport(t, st, uid, run.ID)
			sendCancellationAck(s, agentID, run.ID, ref)
			after := assertOneCancellationReport(t, st, uid, run.ID)
			loaded, err := st.OrchestrationRunByID(ctx, run.ID, uid)
			if err != nil || loaded.Status != store.OrchestrationCanceled || loaded.Error != reason {
				t.Fatal("late ACK erased the timeout that won the terminal CAS")
			}
			if string(before.Result) != string(after.Result) || !strings.Contains(string(after.Result), "cancellation before the timeout") {
				t.Fatal("late ACK replaced timeout result or notification snapshot")
			}
		})
	}
}
