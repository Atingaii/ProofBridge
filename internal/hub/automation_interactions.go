package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type automationInteractionWaiter struct {
	agentID string
	runID   string
	result  chan protocol.Envelope
}
type automationInteractionBroker struct {
	mu      sync.Mutex
	waiters map[string]automationInteractionWaiter
}

func newAutomationInteractionBroker() *automationInteractionBroker {
	return &automationInteractionBroker{waiters: make(map[string]automationInteractionWaiter)}
}

func (b *automationInteractionBroker) register(key, agentID, runID string) (chan protocol.Envelope, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.waiters) >= 256 {
		return nil, nil, errors.New("INTERACTION_UNAVAILABLE: too many interaction queries are in flight")
	}
	if _, exists := b.waiters[key]; exists {
		return nil, nil, errors.New("REQUEST_CONFLICT: this interaction query is already in flight")
	}
	result := make(chan protocol.Envelope, 1)
	b.waiters[key] = automationInteractionWaiter{agentID: agentID, runID: runID, result: result}
	return result, func() { b.mu.Lock(); delete(b.waiters, key); b.mu.Unlock() }, nil
}
func (b *automationInteractionBroker) deliver(key, agentID, runID string, env protocol.Envelope) {
	if b == nil {
		return
	}
	b.mu.Lock()
	waiter, ok := b.waiters[key]
	b.mu.Unlock()
	if ok && waiter.agentID == agentID && waiter.runID == runID {
		select {
		case waiter.result <- env:
		default:
		}
	}
}

func (s *Server) automationInteractionExchange(ctx context.Context, agentID, runID, key string, env protocol.Envelope) (protocol.Envelope, error) {
	if s.automationInteractions == nil {
		return protocol.Envelope{}, errors.New("INTERACTION_UNAVAILABLE: interaction routing is not initialized")
	}
	reply, remove, err := s.automationInteractions.register(key, agentID, runID)
	if err != nil {
		return protocol.Envelope{}, err
	}
	defer remove()
	if err = s.pool.SendToAgent(agentID, env); err != nil {
		return protocol.Envelope{}, fmt.Errorf("INTERACTION_UNAVAILABLE: endpoint is offline")
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case value := <-reply:
		return value, nil
	case <-wait.Done():
		return protocol.Envelope{}, errors.New("DELIVERY_UNKNOWN: Bridge did not confirm the interaction; query its current snapshot before retrying")
	}
}

// Called only after the parent resource/Bridge identity was checked. It keeps
// bounded in-flight responses in memory; native question text never enters DB.
func (s *Server) captureAutomationInteraction(agentID string, env protocol.Envelope) {
	var runID, key string
	switch env.Type {
	case protocol.TypeApprovalSnapshot:
		p, err := protocol.Decode[protocol.ApprovalSnapshotPayload](env)
		if err != nil || p.SyncID == "" || len(p.Requests) > 64 || len(p.Receipts) > 256 {
			return
		}
		if p.InstanceID == "" {
			return
		}
		for _, request := range p.Requests {
			if request.RunID != p.RunID || request.InstanceID != p.InstanceID || request.RequestID == "" || request.ExecutionID == "" {
				return
			}
		}
		for _, receipt := range p.Receipts {
			if receipt.RunID != p.RunID || receipt.InstanceID != p.InstanceID || receipt.RequestID == "" || receipt.ExecutionID == "" {
				return
			}
		}
		runID, key = p.RunID, "approval_sync:"+p.SyncID
	case protocol.TypeApprovalStatus:
		p, err := protocol.Decode[protocol.ApprovalStatusPayload](env)
		if err != nil || p.SyncID == "" {
			return
		}
		runID, key = p.RunID, "approval_decision:"+p.SyncID
	case protocol.TypeUserInputSnapshot:
		p, err := protocol.Decode[protocol.UserInputSnapshotPayload](env)
		if err != nil || p.SyncID == "" {
			return
		}
		runID, key = p.RunID, "question_sync:"+p.SyncID
	case protocol.TypeUserInputStatus:
		p, err := protocol.Decode[protocol.UserInputStatusPayload](env)
		if err != nil || p.SubmissionID == "" {
			return
		}
		runID, key = p.RunID, "question_answer:"+p.RunID+":"+p.RequestID+":"+p.SubmissionID
	default:
		return
	}
	s.automationInteractions.deliver(key, agentID, runID, env)
}

func (s *Server) handleAutomationInteractionFrame(ctx context.Context, agentID string, env protocol.Envelope) bool {
	if env.Type != protocol.TypeApprovalSnapshot && env.Type != protocol.TypeApprovalStatus {
		return false
	}
	if env.Sid != "" {
		return true
	}
	var identity struct {
		RunID string `json:"runId"`
	}
	if json.Unmarshal(env.Payload, &identity) != nil || identity.RunID == "" {
		return true
	}
	run, err := s.store.OrchestrationRunByIDAnyUser(ctx, identity.RunID)
	if err != nil || run.AgentID != agentID {
		return true
	}
	s.captureAutomationInteraction(agentID, env)
	if env.Type == protocol.TypeApprovalStatus {
		status, err := protocol.Decode[protocol.ApprovalStatusPayload](env)
		if err == nil && status.Status == "closed" && status.InstanceID != "" && status.ExecutionID != "" && status.RequestID != "" {
			s.cancelAutomationAttention(ctx, run.ID, "approval:"+status.InstanceID+":"+status.ExecutionID+":"+status.RequestID)
		}
	}
	return true
}

type automationInteractionResult struct {
	RunID              string                             `json:"runId"`
	ApprovalsAvailable bool                               `json:"approvalsAvailable"`
	QuestionsAvailable bool                               `json:"questionsAvailable"`
	Approvals          *protocol.ApprovalSnapshotPayload  `json:"approvals,omitempty"`
	Questions          *protocol.UserInputSnapshotPayload `json:"questions,omitempty"`
	NeedsAttention     bool                               `json:"needsAttention"`
}

func (s *Server) automationGetInteractions(ctx context.Context, uid, runID string) (any, error) {
	run, err := s.store.OrchestrationRunByID(ctx, runID, uid)
	if err != nil {
		return nil, err
	}
	caps, online := s.pool.AgentCapabilities(run.AgentID)
	if !online || caps == nil {
		return nil, errors.New("INTERACTION_UNAVAILABLE: endpoint is offline; outstanding requests cannot be reconstructed from history")
	}
	result := automationInteractionResult{RunID: runID}
	if caps.AutomationInteractions >= 1 {
		result.ApprovalsAvailable = true
		id := store.NewID("aps")
		env, err := s.automationInteractionExchange(ctx, run.AgentID, runID, "approval_sync:"+id, protocol.MustEnvelope(protocol.TypeApprovalSync, "", protocol.ApprovalSyncPayload{RunID: runID, SyncID: id}))
		if err != nil {
			return nil, err
		}
		snapshot, err := protocol.Decode[protocol.ApprovalSnapshotPayload](env)
		if err != nil {
			return nil, err
		}
		if snapshot.Error != "" {
			return nil, fmt.Errorf("DELIVERY_UNKNOWN: %s", snapshot.Error)
		}
		result.Approvals = &snapshot
		result.NeedsAttention = len(snapshot.Requests) > 0
	}
	if s.nativeUserInputV3(run.AgentID, "") {
		id := store.NewID("uis")
		query := protocol.UserInputSyncPayload{RunID: runID, SyncID: id, ClientVersion: 3}
		resultEnvelope, err := s.automationNativeExchange(ctx, uid, run, "question_sync:"+id, protocol.MustEnvelope(protocol.TypeUserInputSync, "", query))
		if err != nil {
			if !result.ApprovalsAvailable {
				return nil, err
			}
		} else {
			snapshot, err := protocol.Decode[protocol.UserInputSnapshotPayload](resultEnvelope)
			if err != nil {
				return nil, err
			}
			result.QuestionsAvailable = true
			result.Questions = &snapshot
			result.NeedsAttention = result.NeedsAttention || len(snapshot.Requests) > 0
		}
	}
	if !result.ApprovalsAvailable && !result.QuestionsAvailable {
		return nil, errors.New("INTERACTION_UNAVAILABLE: endpoint needs the automation approval or native input v3 capability")
	}
	return result, nil
}

type automationInteractionArguments struct {
	Operation          string                                `json:"operation"`
	Kind               string                                `json:"kind"`
	RunID              string                                `json:"runId"`
	RequestID          string                                `json:"requestId"`
	InstanceID         string                                `json:"instanceId"`
	ExecutionID        string                                `json:"executionId"`
	DecisionID         string                                `json:"decisionId"`
	Decision           string                                `json:"decision"`
	SubmissionID       string                                `json:"submissionId"`
	Answers            map[string]protocol.UserInputAnswer   `json:"answers"`
	PendingSubmissions []protocol.UserInputPendingSubmission `json:"pendingSubmissions,omitempty"`
	PendingDecisions   []protocol.ApprovalPendingDecision    `json:"pendingDecisions,omitempty"`
}

// Using the same shared native route preserves account rollout, v3 negotiation,
// terminal guards and bounds from the website. The client is request-scoped.
func (s *Server) automationNativeExchange(ctx context.Context, uid string, run store.OrchestrationRun, key string, env protocol.Envelope) (protocol.Envelope, error) {
	if s.automationInteractions == nil {
		return protocol.Envelope{}, errors.New("INTERACTION_UNAVAILABLE: interaction routing is unavailable")
	}
	result, remove, err := s.automationInteractions.register(key, run.AgentID, run.ID)
	if err != nil {
		return protocol.Envelope{}, err
	}
	defer remove()
	client := &automationNativeClient{version: 3}
	s.handleUserInputFromClient(ctx, uid, run.ID, client, env)
	if client.rejection != nil {
		return protocol.Envelope{}, fmt.Errorf("INTERACTION_UNAVAILABLE: %s", client.rejection.Reason)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case response := <-result:
		return response, nil
	case <-wait.Done():
		return protocol.Envelope{}, errors.New("DELIVERY_UNKNOWN: native answer/query is unconfirmed; reconcile its original submission instead of replaying")
	}
}

type automationNativeClient struct {
	version   int32
	rejection *protocol.UserInputStatusPayload
}

func (c *automationNativeClient) Send(env protocol.Envelope) error {
	p, err := protocol.Decode[protocol.UserInputStatusPayload](env)
	if err == nil {
		c.rejection = &p
	}
	return err
}
func (c *automationNativeClient) userInputVersion() int32     { return c.version }
func (c *automationNativeClient) setUserInputVersion(v int32) { c.version = v }

func (s *Server) automationRespondInteraction(ctx context.Context, uid string, raw json.RawMessage) (any, error) {
	var args automationInteractionArguments
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errors.New("BAD_REQUEST: invalid interaction arguments")
	}
	run, err := s.store.OrchestrationRunByID(ctx, args.RunID, uid)
	if err != nil {
		return nil, err
	}
	if args.Operation != "reconcile" && !orchestrationActiveStatus(run.Status) {
		return nil, errors.New("INTERACTION_CLOSED: this run is no longer active")
	}
	if args.InstanceID == "" {
		return nil, errors.New("BAD_REQUEST: use the Bridge instance identity from a current interaction snapshot")
	}
	switch args.Kind {
	case "approval":
		caps, online := s.pool.AgentCapabilities(run.AgentID)
		if !online || caps == nil || caps.AutomationInteractions < 1 {
			return nil, errors.New("INTERACTION_UNAVAILABLE: endpoint needs automation approval protocol v1")
		}
		id := store.NewID("apx")
		if args.Operation == "reconcile" {
			if len(args.PendingDecisions) == 0 || len(args.PendingDecisions) > 64 {
				return nil, errors.New("BAD_REQUEST: supply 1–64 pending decisions")
			}
			env, err := s.automationInteractionExchange(ctx, run.AgentID, run.ID, "approval_sync:"+id, protocol.MustEnvelope(protocol.TypeApprovalSync, "", protocol.ApprovalSyncPayload{RunID: run.ID, SyncID: id, InstanceID: args.InstanceID, Pending: args.PendingDecisions}))
			if err != nil {
				return nil, err
			}
			return protocol.Decode[protocol.ApprovalSnapshotPayload](env)
		}
		if args.Operation != "decide" || args.RequestID == "" || args.ExecutionID == "" || args.DecisionID == "" || !validApprovalDecision(args.Decision) {
			return nil, errors.New("BAD_REQUEST: supply a valid approval decision and its complete request identity")
		}
		env, err := s.automationInteractionExchange(ctx, run.AgentID, run.ID, "approval_decision:"+id, protocol.MustEnvelope(protocol.TypeApprovalDecision, "", protocol.ApprovalDecisionPayload{RunID: run.ID, SyncID: id, InstanceID: args.InstanceID, ExecutionID: args.ExecutionID, RequestID: args.RequestID, DecisionID: args.DecisionID, Decision: args.Decision}))
		if err != nil {
			return nil, err
		}
		return protocol.Decode[protocol.ApprovalStatusPayload](env)
	case "question":
		if !s.nativeUserInputV3(run.AgentID, "") {
			return nil, errors.New("INTERACTION_UNAVAILABLE: endpoint needs native input protocol v3")
		}
		if args.Operation == "reconcile" {
			if len(args.PendingSubmissions) == 0 || len(args.PendingSubmissions) > 64 {
				return nil, errors.New("BAD_REQUEST: supply 1–64 pending answer submissions")
			}
			id := store.NewID("uis")
			env, err := s.automationNativeExchange(ctx, uid, run, "question_sync:"+id, protocol.MustEnvelope(protocol.TypeUserInputSync, "", protocol.UserInputSyncPayload{RunID: run.ID, SyncID: id, ClientVersion: 3, InstanceID: args.InstanceID, PendingSubmissions: args.PendingSubmissions}))
			if err != nil {
				return nil, err
			}
			return protocol.Decode[protocol.UserInputSnapshotPayload](env)
		}
		if args.Operation != "answer" || args.RequestID == "" || args.ExecutionID == "" || args.SubmissionID == "" || len(args.Answers) == 0 {
			return nil, errors.New("BAD_REQUEST: supply a structured answer and its complete request identity")
		}
		reply := protocol.UserInputResponsePayload{RunID: run.ID, RequestID: args.RequestID, InstanceID: args.InstanceID, ExecutionID: args.ExecutionID, SubmissionID: args.SubmissionID, Answers: args.Answers}
		env, err := s.automationNativeExchange(ctx, uid, run, "question_answer:"+run.ID+":"+args.RequestID+":"+args.SubmissionID, protocol.MustEnvelope(protocol.TypeUserInputResponse, "", reply))
		if err != nil {
			return nil, err
		}
		return protocol.Decode[protocol.UserInputStatusPayload](env)
	default:
		return nil, errors.New("BAD_REQUEST: interaction kind must be approval or question")
	}
}
