package hub

import (
	"context"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/store"
)

// Writes require an active, enabled run. A terminal run (or a rollout turned
// off while an answer was in flight) may still route read-only receipts from
// its original Bridge. The Hub never stores native questions or answers.
func (s *Server) userInputRun(ctx context.Context, runID, agentID string, readOnly bool) (store.OrchestrationRun, bool) {
	if runID == "" {
		return store.OrchestrationRun{}, false
	}
	run, err := s.store.OrchestrationRunByIDAnyUser(ctx, runID)
	if err != nil || run.AgentID != agentID || (!orchestrationActiveStatus(run.Status) && (!readOnly || !orchestrationTerminalStatus(run.Status))) {
		return store.OrchestrationRun{}, false
	}
	user, err := s.store.UserByID(ctx, run.UserID)
	if err != nil || (!readOnly && !s.featureEnabled(rollout.FeatureNativeUserInput, user)) {
		return store.OrchestrationRun{}, false
	}
	return run, true
}

func (s *Server) handleUserInputFromBridge(ctx context.Context, agentID string, env protocol.Envelope) {
	if env.Sid != "" {
		return
	}
	var runID string
	readOnly := false
	cli := ""
	switch env.Type {
	case protocol.TypeUserInputRequest:
		p, err := protocol.Decode[protocol.UserInputRequestPayload](env)
		if err != nil || p.RequestID == "" || p.ExecutionID == "" || p.CLI == "" || len(p.Questions) == 0 || len(p.Questions) > 8 {
			return
		}
		runID = p.RunID
		cli = p.CLI
	case protocol.TypeUserInputStatus:
		p, err := protocol.Decode[protocol.UserInputStatusPayload](env)
		if err != nil || p.RequestID == "" || (p.Source != "" && p.Source != "bridge") || (p.Status != "delivered" && p.Status != "closed" && p.Status != "rejected" && p.Status != "queued") {
			return
		}
		if p.Receipt != nil && !validUserInputReceipt(*p.Receipt, p.RunID, p.RequestID, p.InstanceID, p.ExecutionID) {
			return
		}
		runID = p.RunID
		readOnly = p.Status == "closed" || p.Receipt != nil
	case protocol.TypeUserInputSnapshot:
		p, err := protocol.Decode[protocol.UserInputSnapshotPayload](env)
		if err != nil || p.SyncID == "" || len(p.Requests) > 64 || len(p.Statuses) > 64 || len(p.Closed) > 64 || len(p.Receipts) > 64 || len(p.Reconciliations) > 64 || len(p.ReconcileError) > 256 {
			return
		}
		runID = p.RunID
		activeRequests := make(map[string]string, len(p.Requests))
		for _, request := range p.Requests {
			if request.RunID != runID || request.RequestID == "" || request.ExecutionID == "" || request.CLI == "" || len(request.Questions) == 0 || (p.InstanceID != "" && request.InstanceID != p.InstanceID) || !s.nativeUserInputV3(agentID, request.CLI) {
				return
			}
			activeRequests[request.RequestID] = request.ExecutionID
		}
		for _, status := range p.Statuses {
			executionID, found := activeRequests[status.RequestID]
			if status.RunID != runID || status.RequestID == "" || !found || (status.ExecutionID != "" && status.ExecutionID != executionID) || (status.Status != "delivered" && status.Status != "queued") || status.Lifecycle == "closed" || (status.Source != "" && status.Source != "bridge") || (p.InstanceID != "" && status.InstanceID != p.InstanceID) {
				return
			}
			if status.Receipt != nil && !validUserInputReceipt(*status.Receipt, runID, status.RequestID, status.InstanceID, status.ExecutionID) {
				return
			}
		}
		for _, status := range p.Closed {
			if status.RunID != runID || status.RequestID == "" || status.Status != "closed" || status.Lifecycle != "closed" || (status.Source != "" && status.Source != "bridge") || (p.InstanceID != "" && status.InstanceID != p.InstanceID) {
				return
			}
			if status.Receipt != nil && !validUserInputReceipt(*status.Receipt, runID, status.RequestID, status.InstanceID, status.ExecutionID) {
				return
			}
		}
		for _, receipt := range p.Receipts {
			if !validUserInputReceipt(receipt, runID, receipt.RequestID, p.InstanceID, receipt.ExecutionID) {
				return
			}
		}
		for _, status := range p.Reconciliations {
			if status.RunID != runID || status.RequestID == "" || status.Source != "bridge" || (p.InstanceID != "" && status.InstanceID != p.InstanceID) || (status.Status != "queued" && status.Status != "delivered" && status.Status != "rejected" && status.Status != "closed") {
				return
			}
			if status.Receipt != nil && !validUserInputReceipt(*status.Receipt, runID, status.RequestID, status.InstanceID, status.ExecutionID) {
				return
			}
		}
		readOnly = len(p.Requests) == 0
	default:
		return
	}
	run, ok := s.userInputRun(ctx, runID, agentID, readOnly)
	if !ok || (!orchestrationActiveStatus(run.Status) && env.Type == protocol.TypeUserInputSnapshot && !readOnly) {
		return
	}
	terminalReadOnly := orchestrationTerminalStatus(run.Status) && readOnly && env.Type == protocol.TypeUserInputSnapshot
	if !terminalReadOnly && !s.nativeUserInputV3(agentID, cli) {
		return
	}
	if user, err := s.store.UserByID(ctx, run.UserID); err != nil || !s.featureEnabled(rollout.FeatureNativeUserInput, user) {
		if env.Type == protocol.TypeUserInputRequest || (env.Type == protocol.TypeUserInputSnapshot && lenSnapshotRequests(env) > 0) {
			return
		}
	}
	s.pool.BroadcastNativeUserInput(runID, env, terminalReadOnly)
}

func (s *Server) nativeUserInputV3(agentID, cli string) bool {
	caps, ok := s.pool.AgentCapabilities(agentID)
	if !ok {
		return false
	}
	if cli != "" {
		capability, found := caps.NativeUserInput[cli]
		return found && capability.Available && capability.Version >= 3
	}
	for _, capability := range caps.NativeUserInput {
		if capability.Available && capability.Version >= 3 {
			return true
		}
	}
	return false
}

func validUserInputReceipt(receipt protocol.UserInputReceiptPayload, runID, requestID, instanceID, executionID string) bool {
	return receipt.RunID == runID && receipt.RequestID == requestID && receipt.RequestID != "" &&
		receipt.ExecutionID != "" && receipt.SubmissionID != "" &&
		(receipt.Outcome == "delivered" || receipt.Outcome == "unknown" || receipt.Outcome == "not_sent") &&
		(instanceID == "" || receipt.InstanceID == instanceID) &&
		(executionID == "" || receipt.ExecutionID == executionID)
}

func lenSnapshotRequests(env protocol.Envelope) int {
	p, err := protocol.Decode[protocol.UserInputSnapshotPayload](env)
	if err != nil {
		return 0
	}
	return len(p.Requests)
}

func (s *Server) handleUserInputFromBrowser(ctx context.Context, uid, runID string, conn *BrowserConn, env protocol.Envelope) {
	var requestID, submissionID, syncID string
	reject := func(reason string) {
		if env.Type == protocol.TypeUserInputSync {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", protocol.UserInputStatusPayload{
				RunID: runID, SyncID: syncID, Status: "rejected", Source: "hub", Phase: "route", Reason: reason,
			}))
			return
		}
		s.sendUserInputRejected(conn, runID, requestID, submissionID, reason)
	}
	var valid bool
	switch env.Type {
	case protocol.TypeUserInputResponse:
		p, err := protocol.Decode[protocol.UserInputResponsePayload](env)
		requestID, submissionID = p.RequestID, p.SubmissionID
		valid = err == nil && p.RunID == runID && p.RequestID != "" && p.SubmissionID != "" && len(p.Answers) > 0 && len(p.Answers) <= 8
		for id, answer := range p.Answers {
			if strings.TrimSpace(id) == "" || len(id) > 128 || len(answer.OptionIDs) > 16 || (len(answer.OptionIDs) == 0 && answer.CustomText == nil) {
				valid = false
				break
			}
			for _, optionID := range answer.OptionIDs {
				if len(optionID) == 0 || len(optionID) > 128 {
					valid = false
				}
			}
			if answer.CustomText != nil && (len(strings.TrimSpace(*answer.CustomText)) == 0 || len(*answer.CustomText) > 8192) {
				valid = false
			}
		}
	case protocol.TypeUserInputSync:
		p, err := protocol.Decode[protocol.UserInputSyncPayload](env)
		if len(p.SyncID) <= 128 {
			syncID = p.SyncID
		}
		valid = err == nil && p.RunID == runID && p.SyncID != "" && len(p.SyncID) <= 128 && p.ClientVersion == 3 && len(p.PendingSubmissions) <= 64 && len(p.InstanceID) <= 128
		if len(p.PendingSubmissions) > 0 && p.InstanceID == "" {
			valid = false
		}
		seen := make(map[string]bool, len(p.PendingSubmissions))
		for _, pending := range p.PendingSubmissions {
			key := pending.RequestID + "\x00" + pending.SubmissionID
			if pending.RequestID == "" || len(pending.RequestID) > 128 || pending.SubmissionID == "" || len(pending.SubmissionID) > 128 || pending.ExecutionID == "" || len(pending.ExecutionID) > 128 || seen[key] {
				valid = false
			}
			seen[key] = true
		}
	default:
		return
	}
	if !valid {
		if env.Type == protocol.TypeUserInputSync {
			reject("Invalid structured input query")
		} else {
			reject("Invalid structured answer")
		}
		return
	}
	run, err := s.store.OrchestrationRunByID(ctx, runID, uid)
	if err != nil || (!orchestrationActiveStatus(run.Status) && (env.Type != protocol.TypeUserInputSync || !orchestrationTerminalStatus(run.Status))) {
		reject("Run is no longer active")
		return
	}
	if _, ok := s.userInputRun(ctx, runID, run.AgentID, env.Type == protocol.TypeUserInputSync); !ok {
		reject("Structured input is unavailable")
		return
	}
	if env.Type == protocol.TypeUserInputSync {
		p, _ := protocol.Decode[protocol.UserInputSyncPayload](env)
		if p.ClientVersion == 3 {
			conn.nativeUserInputVersion.Store(int32(p.ClientVersion))
		} else if orchestrationActiveStatus(run.Status) {
			reject("Browser needs to reload for structured input")
			return
		}
	} else if conn.nativeUserInputVersion.Load() < 3 {
		reject("Browser needs to reload for structured input")
		return
	}
	if !s.pool.AgentOnline(run.AgentID) {
		reject("CLI endpoint is offline")
		return
	}
	if orchestrationActiveStatus(run.Status) && !s.nativeUserInputV3(run.AgentID, "") {
		reject("Bridge needs protocol v3 for structured input")
		return
	}
	if err := s.pool.SendToAgent(run.AgentID, env); err != nil {
		reject("CLI endpoint is offline")
	}
}

func (s *Server) sendUserInputRejected(conn *BrowserConn, runID, requestID, submissionID, reason string) {
	_ = conn.Send(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", protocol.UserInputStatusPayload{
		RunID: runID, RequestID: requestID, SubmissionID: submissionID,
		Status: "rejected", Source: "hub", Phase: "route", Reason: reason,
	}))
}
