package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestAutomationInteractionBrokerIsolatesIdentityAndDuplicates(t *testing.T) {
	broker := newAutomationInteractionBroker()
	result, remove, err := broker.register("key", "agent", "run")
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	if _, _, err := broker.register("key", "agent", "run"); err == nil {
		t.Fatal("duplicate in-flight identity accepted")
	}
	env := protocol.MustEnvelope("response", "", map[string]string{"ok": "yes"})
	broker.deliver("key", "other", "run", env)
	broker.deliver("key", "agent", "other", env)
	select {
	case <-result:
		t.Fatal("foreign response crossed identities")
	default:
	}
	broker.deliver("key", "agent", "run", env)
	broker.deliver("key", "agent", "run", env)
	if len(result) != 1 {
		t.Fatalf("duplicate response count=%d", len(result))
	}
	<-result
	remove()
	broker.deliver("key", "agent", "run", env)
	select {
	case <-result:
		t.Fatal("late response reached retired waiter")
	default:
	}
}
func TestAutomationGetInteractionsUsesReadOnlySnapshots(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	bridge := nativeUserInputTestAgent(agentID, 3)
	bridge.capabilities.AutomationInteractions = 1
	s.pool.RegisterAgent(bridge)
	defer s.pool.UnregisterAgent(agentID, bridge)
	s.automationInteractions = newAutomationInteractionBroker()
	type reply struct {
		value any
		err   error
	}
	done := make(chan reply, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { value, err := s.automationGetInteractions(ctx, uid, run.ID); done <- reply{value, err} }()
	select {
	case frame := <-bridge.send:
		query, err := protocol.Decode[protocol.ApprovalSyncPayload](frame)
		if err != nil || frame.Type != protocol.TypeApprovalSync || len(query.Pending) != 0 || query.InstanceID != "" {
			t.Fatalf("R query carries write state: %s", frame.Payload)
		}
		s.handleBridgeEnvelope(ctx, agentID, protocol.MustEnvelope(protocol.TypeApprovalSnapshot, "", protocol.ApprovalSnapshotPayload{RunID: run.ID, SyncID: query.SyncID, InstanceID: "instance", Requests: []protocol.ApprovalRequestPayload{}}))
	case <-ctx.Done():
		t.Fatal("approval query not dispatched")
	}
	select {
	case frame := <-bridge.send:
		query, err := protocol.Decode[protocol.UserInputSyncPayload](frame)
		if err != nil || frame.Type != protocol.TypeUserInputSync || query.ClientVersion != 3 || len(query.PendingSubmissions) != 0 || query.InstanceID != "" {
			t.Fatalf("native R query carries write state: %s", frame.Payload)
		}
		s.handleBridgeEnvelope(ctx, agentID, protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", protocol.UserInputSnapshotPayload{RunID: run.ID, SyncID: query.SyncID, InstanceID: "instance", Requests: []protocol.UserInputRequestPayload{}}))
	case <-ctx.Done():
		t.Fatal("question query not dispatched")
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		value := got.value.(automationInteractionResult)
		if !value.ApprovalsAvailable || !value.QuestionsAvailable || value.NeedsAttention {
			t.Fatalf("snapshot result = %#v", value)
		}
	case <-ctx.Done():
		t.Fatal("snapshot query did not finish")
	}
}
func TestAutomationInteractionsOwnershipCapabilitiesAndSharedRollout(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	if _, err := s.automationGetInteractions(context.Background(), "other", run.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign read = %v", err)
	}
	bridge := nativeUserInputTestAgent(agentID, 2)
	s.pool.RegisterAgent(bridge)
	defer s.pool.UnregisterAgent(agentID, bridge)
	if _, err := s.automationGetInteractions(context.Background(), uid, run.ID); err == nil || !strings.Contains(err.Error(), "INTERACTION_UNAVAILABLE") {
		t.Fatalf("old Bridge = %v", err)
	}
	bridge.capabilities.NativeUserInput["codex"] = protocol.NativeUserInputCapability{Version: 3, Available: true}
	s.automationInteractions = newAutomationInteractionBroker()
	s.cfg.Hub.FeatureRollouts[rollout.FeatureNativeUserInput] = "off"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	text := "answer"
	raw, _ := json.Marshal(automationInteractionArguments{Operation: "answer", Kind: "question", RunID: run.ID, InstanceID: "instance", RequestID: "request", ExecutionID: "execution", SubmissionID: "submission", Answers: map[string]protocol.UserInputAnswer{"q": {CustomText: &text}}})
	if _, err := s.automationRespondInteraction(context.Background(), uid, raw); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("disabled rollout = %v", err)
	}
	select {
	case frame := <-bridge.send:
		t.Fatalf("disabled rollout dispatched %s", frame.Type)
	default:
	}
}
func TestAutomationApprovalDecisionUsesExistingRunAndCorrelatedReceipt(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	bridge := testBridgeConn(agentID, 8)
	bridge.capabilities.AutomationInteractions = 1
	s.pool.RegisterAgent(bridge)
	defer s.pool.UnregisterAgent(agentID, bridge)
	s.automationInteractions = newAutomationInteractionBroker()
	raw, _ := json.Marshal(automationInteractionArguments{Operation: "decide", Kind: "approval", RunID: run.ID, InstanceID: "instance", ExecutionID: "execution", RequestID: "request", DecisionID: "decision", Decision: "decline"})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		result, err := s.automationRespondInteraction(ctx, uid, raw)
		if err == nil && result.(protocol.ApprovalStatusPayload).Outcome != "queued" {
			err = errors.New("wrong receipt")
		}
		done <- err
	}()
	select {
	case frame := <-bridge.send:
		decision, err := protocol.Decode[protocol.ApprovalDecisionPayload](frame)
		if err != nil || frame.Type != protocol.TypeApprovalDecision || decision.RunID != run.ID || decision.Decision != "decline" || decision.DecisionID != "decision" {
			t.Fatalf("decision route = %s", frame.Payload)
		}
		receipt := protocol.MustEnvelope(protocol.TypeApprovalStatus, "", protocol.ApprovalStatusPayload{RunID: run.ID, SyncID: decision.SyncID, RequestID: decision.RequestID, InstanceID: decision.InstanceID, ExecutionID: decision.ExecutionID, DecisionID: decision.DecisionID, Status: "queued", Outcome: "queued"})
		s.handleBridgeEnvelope(ctx, "foreign-agent", receipt)
		select {
		case err := <-done:
			t.Fatalf("foreign agent settled decision: %v", err)
		default:
		}
		s.handleBridgeEnvelope(ctx, agentID, receipt)
	case <-ctx.Done():
		t.Fatal("decision did not route")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("decision receipt not correlated")
	}
}
