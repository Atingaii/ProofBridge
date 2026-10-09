package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// An open pendingUserInput owns a native callback. A closed one keeps only
// bounded, non-sensitive routing and receipt metadata for reconnect recovery.
// No answer or native RPC id is stored in SQLite or orchestration events.
type pendingUserInput struct {
	request        protocol.UserInputRequestPayload
	ctx            context.Context
	deliver        func(context.Context, protocol.UserInputResponsePayload) error
	deliveryCancel context.CancelFunc
	writeStarted   bool
	state          string
	submissionID   string
	receipt        *protocol.UserInputReceiptPayload
	receipts       map[string]userInputReceiptRecord
	closedAt       time.Time
}

type userInputReceiptRecord struct {
	receipt protocol.UserInputReceiptPayload
	phase   string
}

type nativeUserInputWriteClaimKey struct{}

func claimNativeUserInputWrite(ctx context.Context) bool {
	claim, ok := ctx.Value(nativeUserInputWriteClaimKey{}).(func() bool)
	return !ok || claim()
}

const (
	userInputTerminalTTL       = 10 * time.Minute
	userInputTerminalMax       = 256
	userInputTerminalPerRunMax = 64
	userInputDispatchCapacity  = 128
	userInputDispatchWorkers   = 8
	userInputPerClientCapacity = 16
)

type queuedUserInputWork struct {
	key      string
	response protocol.UserInputResponsePayload
}

type orchestrationUserInputRequester struct {
	manager     *OrchestrationManager
	runID       string
	executionID string
	workerID    string
	turnID      string
	cli         string
}

func (r orchestrationUserInputRequester) RegisterUserInput(ctx context.Context, blocking bool, questions []protocol.UserInputQuestion, deliver func(context.Context, protocol.UserInputResponsePayload) error) (string, error) {
	if r.manager == nil || r.runID == "" || r.executionID == "" || len(questions) == 0 || len(questions) > 8 || deliver == nil || ctx.Err() != nil {
		return "", errors.New("native user input is not available")
	}
	seenQuestions := make(map[string]bool, len(questions))
	for _, question := range questions {
		if len(question.ID) == 0 || len(question.ID) > 128 || strings.TrimSpace(question.Question) == "" || len(question.Options) > 16 || seenQuestions[question.ID] {
			return "", errors.New("native question exceeds supported limits")
		}
		seenQuestions[question.ID] = true
		seenOptions := make(map[string]bool, len(question.Options))
		for _, option := range question.Options {
			if len(option.ID) == 0 || len(option.ID) > 128 || seenOptions[option.ID] {
				return "", errors.New("native option exceeds supported limits")
			}
			seenOptions[option.ID] = true
		}
	}
	request := protocol.UserInputRequestPayload{
		RequestID: store.NewID("uin"), RunID: r.runID, ExecutionID: r.executionID,
		WorkerID: r.workerID, TurnID: r.turnID, CLI: r.cli,
		Blocking: blocking, Questions: questions,
	}
	m := r.manager
	m.mu.Lock()
	request.InstanceID = m.userInputInstanceID
	if m.userInputs == nil {
		m.userInputs = make(map[string]*pendingUserInput)
	}
	if m.userInputRevision == nil {
		m.userInputRevision = make(map[string]uint64)
	}
	active, globalActive := 0, 0
	for _, item := range m.userInputs {
		if item.state != "closed" {
			globalActive++
			if item.request.RunID == r.runID {
				active++
			}
		}
	}
	if active >= userInputTerminalPerRunMax || globalActive >= userInputOutboxActiveMax || !m.userInputOutbox.reserve(request.RequestID, r.runID) {
		m.mu.Unlock()
		return "", errors.New("too many native questions are active")
	}
	m.userInputRevision[r.runID]++
	request.Revision = m.userInputRevision[r.runID]
	m.userInputs[request.RequestID] = &pendingUserInput{request: request, ctx: ctx, deliver: deliver, state: "pending"}
	m.userInputOrder = append(m.userInputOrder, request.RequestID)
	m.pruneUserInputsLocked()
	m.mu.Unlock()
	m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputRequest, "", request))
	context.AfterFunc(ctx, func() { m.CloseUserInput(request.RequestID, "native turn ended") })
	return request.RequestID, nil
}

func (r orchestrationUserInputRequester) CloseUserInput(requestID, reason string) {
	if r.manager != nil {
		r.manager.CloseUserInput(requestID, reason)
	}
}

// dispatchUserInput reserves a submission before the WebSocket reader moves on.
// This makes queue admission and duplicate detection one atomic decision: a
// second frame can never receive not_sent while its first copy might write.
func (m *OrchestrationManager) dispatchUserInput(res protocol.UserInputResponsePayload) {
	m.mu.Lock()
	p := m.userInputs[res.RequestID]
	if p == nil || p.request.RunID != res.RunID {
		m.mu.Unlock()
		m.UserInputUnavailable(res)
		return
	}
	if m.userInputReceiptLocked(p, res.SubmissionID) != nil {
		status := m.userInputStatusLocked(p, res.SubmissionID)
		m.userInputRevision[res.RunID]++
		status.Revision = m.userInputRevision[res.RunID]
		m.mu.Unlock()
		m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
		return
	}
	if p.state != "pending" {
		status := m.userInputStatusLocked(p, res.SubmissionID)
		if p.state == "queued" || p.state == "delivering" {
			if p.submissionID == res.SubmissionID {
				status.Status = "queued"
			} else {
				status.Status, status.Reason = "rejected", "native request is handling another submission"
			}
		} else if p.state == "closed" {
			status.Status, status.Reason = "closed", "native request has already ended"
		} else if p.state == "delivered" && p.submissionID != res.SubmissionID {
			status.Status, status.Reason = "rejected", "native request has already received an answer"
		}
		m.userInputRevision[res.RunID]++
		status.Revision = m.userInputRevision[res.RunID]
		m.mu.Unlock()
		m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
		return
	}
	if m.userInputReceiptSlotsLocked(res.RunID) >= userInputTerminalPerRunMax {
		m.closeUserInputLocked(p, "native answer history is full")
		status := m.userInputStatusLocked(p, res.SubmissionID)
		status.Reason = "native answer history is full"
		m.mu.Unlock()
		m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
		return
	}
	// Validation and an expired native context perform no I/O. They still go
	// through UserInputResponse to publish an authoritative not_sent or close.
	if p.ctx.Err() != nil || validateUserInputAnswers(p.request.Questions, res.Answers) != nil {
		m.mu.Unlock()
		m.UserInputResponse(res)
		return
	}
	m.userInputDispatchMu.Lock()
	// Reserve room for another native client even if one client accumulates
	// many answers while its writer is stalled.
	key := p.request.RunID + "\x00" + p.request.CLI + "\x00" + p.request.WorkerID
	clientQueued := 0
	for _, work := range m.userInputDispatchQueue {
		if work.key == key {
			clientQueued++
		}
	}
	if m.userInputDispatchActive+len(m.userInputDispatchQueue) >= userInputDispatchCapacity || clientQueued >= userInputPerClientCapacity {
		m.userInputDispatchMu.Unlock()
		p.submissionID = res.SubmissionID
		m.userInputRevision[res.RunID]++
		revision := m.userInputRevision[res.RunID]
		p.receipt = &protocol.UserInputReceiptPayload{
			RequestID: res.RequestID, RunID: res.RunID, ExecutionID: p.request.ExecutionID,
			InstanceID: m.userInputInstanceID, SubmissionID: res.SubmissionID,
			Outcome: "not_sent", Revision: revision,
		}
		m.recordUserInputReceiptLocked(p, *p.receipt, "queue")
		status := m.userInputStatusLocked(p, res.SubmissionID)
		status.Status, status.Phase, status.Reason = "rejected", "queue", "native user input queue is busy; use a new submission"
		m.mu.Unlock()
		m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
		return
	}
	// The key names one long-lived native client. Waiting items for that client
	// do not occupy workers; an unrelated client can still be dispatched.
	p.state, p.submissionID, p.receipt = "queued", res.SubmissionID, nil
	m.userInputRevision[res.RunID]++
	status := m.userInputStatusLocked(p, res.SubmissionID)
	m.userInputDispatchQueue = append(m.userInputDispatchQueue, queuedUserInputWork{key: key, response: res})
	m.scheduleUserInputLocked()
	m.userInputDispatchMu.Unlock()
	m.mu.Unlock()
	m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
}

// Caller holds userInputDispatchMu. At most one job per native client is
// active, and no goroutine is created for an item waiting in the queue.
func (m *OrchestrationManager) scheduleUserInputLocked() {
	if m.userInputDispatchBusy == nil {
		m.userInputDispatchBusy = make(map[string]bool)
	}
	for m.userInputDispatchActive < userInputDispatchWorkers {
		index := -1
		for i, work := range m.userInputDispatchQueue {
			if !m.userInputDispatchBusy[work.key] {
				index = i
				break
			}
		}
		if index < 0 {
			return
		}
		work := m.userInputDispatchQueue[index]
		copy(m.userInputDispatchQueue[index:], m.userInputDispatchQueue[index+1:])
		last := len(m.userInputDispatchQueue) - 1
		m.userInputDispatchQueue[last] = queuedUserInputWork{}
		m.userInputDispatchQueue = m.userInputDispatchQueue[:last]
		m.userInputDispatchActive++
		m.userInputDispatchBusy[work.key] = true
		go m.runQueuedUserInput(work)
	}
}

func (m *OrchestrationManager) runQueuedUserInput(work queuedUserInputWork) {
	if !m.UserInputResponse(work.response) {
		m.UserInputUnavailable(work.response)
	}
	m.userInputDispatchMu.Lock()
	m.userInputDispatchActive--
	delete(m.userInputDispatchBusy, work.key)
	m.scheduleUserInputLocked()
	m.userInputDispatchMu.Unlock()
}

func (m *OrchestrationManager) sendUserInputFrame(env protocol.Envelope) {
	m.userInputOutbox.enqueue(env)
}

// UserInputResponse claims one submission under m.mu. The native write runs
// without holding either the manager lock or the WebSocket reader. Closing a
// request is independent from the write result, so a late receipt cannot
// reopen it and a duplicate submission cannot write twice.
func (m *OrchestrationManager) UserInputResponse(res protocol.UserInputResponsePayload) bool {
	if res.RequestID == "" || res.RunID == "" || res.SubmissionID == "" {
		return false
	}
	m.mu.Lock()
	p := m.userInputs[res.RequestID]
	if p == nil || p.request.RunID != res.RunID {
		m.mu.Unlock()
		return false
	}
	if m.userInputReceiptLocked(p, res.SubmissionID) != nil {
		status := m.userInputStatusLocked(p, res.SubmissionID)
		m.userInputRevision[res.RunID]++
		status.Revision = m.userInputRevision[res.RunID]
		m.mu.Unlock()
		m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
		return true
	}
	status := m.userInputStatusLocked(p, res.SubmissionID)
	var deliver func(context.Context, protocol.UserInputResponsePayload) error
	var deliveryCtx context.Context
	switch {
	case p.submissionID == res.SubmissionID && p.state == "delivering":
		status.Status = "queued"
	case p.state == "closed":
		status.Status, status.Reason = "closed", "native request has already ended"
	case p.state != "pending" && (p.state != "queued" || p.submissionID != res.SubmissionID):
		status.Status, status.Reason = "rejected", "native request is no longer awaiting an answer"
	case p.state == "pending" && m.userInputReceiptSlotsLocked(res.RunID) >= userInputTerminalPerRunMax:
		m.closeUserInputLocked(p, "native answer history is full")
		status.Status, status.Reason = "closed", "native answer history is full"
	case p.ctx.Err() != nil:
		m.closeUserInputLocked(p, "native turn ended")
		status.Status, status.Reason = "closed", "native turn ended"
	case validateUserInputAnswers(p.request.Questions, res.Answers) != nil:
		status.Status, status.Reason = "rejected", "answer does not match the native questions"
		status.Phase = "validate"
		p.submissionID = res.SubmissionID
		p.receipt = &protocol.UserInputReceiptPayload{
			RequestID: res.RequestID, RunID: p.request.RunID,
			ExecutionID: p.request.ExecutionID, InstanceID: m.userInputInstanceID,
			SubmissionID: res.SubmissionID, Outcome: "not_sent",
		}
		status.Receipt = p.receipt
	default:
		p.state, p.submissionID, p.receipt = "delivering", res.SubmissionID, nil
		deliver = p.deliver
		deliveryCtx, p.deliveryCancel = context.WithCancel(p.ctx)
		deliveryCtx = context.WithValue(deliveryCtx, nativeUserInputWriteClaimKey{}, func() bool {
			m.mu.Lock()
			defer m.mu.Unlock()
			if p.state != "delivering" || p.ctx.Err() != nil || p.submissionID != res.SubmissionID {
				return false
			}
			p.writeStarted = true
			return true
		})
	}
	if deliver == nil {
		resultStatus, reason, phase, receipt := status.Status, status.Reason, status.Phase, status.Receipt
		status = m.userInputStatusLocked(p, res.SubmissionID)
		status.Status, status.Reason, status.Phase = resultStatus, reason, phase
		m.userInputRevision[p.request.RunID]++
		status.Revision = m.userInputRevision[p.request.RunID]
		if receipt != nil {
			receipt.Revision = status.Revision
			status.Receipt = receipt
			if receipt.Outcome == "not_sent" {
				m.recordUserInputReceiptLocked(p, *receipt, status.Phase)
			}
		}
		m.mu.Unlock()
		m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
		return true
	}
	m.mu.Unlock()

	// A close before the callback starts revokes its lease. Once it starts,
	// native transport cancellation must interrupt it; its result is then
	// recorded without changing an already-closed lifecycle.
	m.mu.Lock()
	canWrite := p.state == "delivering" && p.ctx.Err() == nil
	m.mu.Unlock()
	var err error
	if canWrite {
		err = deliver(deliveryCtx, res)
	} else {
		err = context.Canceled
	}
	m.mu.Lock()
	if p.deliveryCancel != nil {
		p.deliveryCancel()
		p.deliveryCancel = nil
	}
	if err == nil {
		m.userInputRevision[p.request.RunID]++
		p.receipt = &protocol.UserInputReceiptPayload{
			RequestID: res.RequestID, RunID: p.request.RunID,
			ExecutionID: p.request.ExecutionID, InstanceID: m.userInputInstanceID,
			SubmissionID: res.SubmissionID, Outcome: "delivered", Revision: m.userInputRevision[p.request.RunID],
		}
		m.recordUserInputReceiptLocked(p, *p.receipt, "")
		if p.state != "closed" {
			p.state = "delivered"
		}
	} else if canWrite {
		m.userInputRevision[p.request.RunID]++
		outcome := "unknown"
		phase := ""
		if !p.writeStarted {
			outcome, phase = "not_sent", "lease"
		}
		p.receipt = &protocol.UserInputReceiptPayload{
			RequestID: res.RequestID, RunID: p.request.RunID,
			ExecutionID: p.request.ExecutionID, InstanceID: m.userInputInstanceID,
			SubmissionID: res.SubmissionID, Outcome: outcome, Revision: m.userInputRevision[p.request.RunID],
		}
		m.recordUserInputReceiptLocked(p, *p.receipt, phase)
		if p.state != "closed" {
			m.closeUserInputLocked(p, "native answer delivery failed")
		}
	} else if p.state != "closed" {
		m.closeUserInputLocked(p, "native request has already ended")
	}
	if p.state == "closed" {
		m.releaseUserInputLocked(p)
	}
	status = m.userInputStatusLocked(p, res.SubmissionID)
	if err != nil && status.Reason == "" {
		status.Reason = "native answer delivery could not be confirmed"
	}
	status.Revision = m.userInputRevision[p.request.RunID]
	m.pruneUserInputsLocked()
	m.mu.Unlock()
	m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
	return true
}

func validateUserInputAnswers(questions []protocol.UserInputQuestion, answers map[string]protocol.UserInputAnswer) error {
	if len(questions) == 0 || len(answers) != len(questions) {
		return errors.New("answer count mismatch")
	}
	for _, question := range questions {
		answer, ok := answers[question.ID]
		if !ok {
			return fmt.Errorf("missing answer for %s", question.ID)
		}
		if answer.CustomText != nil {
			if !question.AllowCustom || (question.SelectionMode != "multiple" && len(answer.OptionIDs) != 0) || strings.TrimSpace(*answer.CustomText) == "" || len(*answer.CustomText) > 8192 {
				return fmt.Errorf("invalid custom answer for %s", question.ID)
			}
		}
		if len(answer.OptionIDs) == 0 && answer.CustomText == nil || len(answer.OptionIDs) > 16 || (question.SelectionMode != "multiple" && answer.CustomText == nil && len(answer.OptionIDs) != 1) {
			return fmt.Errorf("invalid selection count for %s", question.ID)
		}
		valid := make(map[string]bool, len(question.Options))
		for _, option := range question.Options {
			valid[option.ID] = true
		}
		seen := make(map[string]bool, len(answer.OptionIDs))
		for _, id := range answer.OptionIDs {
			if !valid[id] || seen[id] {
				return fmt.Errorf("unknown or duplicate option for %s", question.ID)
			}
			seen[id] = true
		}
	}
	return nil
}

func (m *OrchestrationManager) CloseUserInput(requestID, reason string) {
	m.mu.Lock()
	p := m.userInputs[requestID]
	if p == nil || p.state == "closed" {
		m.mu.Unlock()
		return
	}
	m.closeUserInputLocked(p, reason)
	status := m.userInputStatusLocked(p, p.submissionID)
	status.Reason = reason
	m.pruneUserInputsLocked()
	m.mu.Unlock()
	// Discard queued answers promptly after close, including their custom text.
	// A job already running observes the closed state or its canceled context.
	m.userInputDispatchMu.Lock()
	for i := 0; i < len(m.userInputDispatchQueue); {
		if m.userInputDispatchQueue[i].response.RequestID != requestID {
			i++
			continue
		}
		copy(m.userInputDispatchQueue[i:], m.userInputDispatchQueue[i+1:])
		last := len(m.userInputDispatchQueue) - 1
		m.userInputDispatchQueue[last] = queuedUserInputWork{}
		m.userInputDispatchQueue = m.userInputDispatchQueue[:last]
	}
	m.userInputDispatchMu.Unlock()
	m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status))
}

func (m *OrchestrationManager) closeUserInputLocked(p *pendingUserInput, reason string) {
	if p.state == "closed" {
		return
	}
	wasDelivering := p.state == "delivering"
	p.state = "closed"
	p.closedAt = time.Now()
	// Pending reconnect frames may still hold question text after the native
	// request ends. The terminal snapshot carries only IDs and receipts.
	kept := m.pending[:0]
	for _, env := range m.pending {
		if env.Type == protocol.TypeUserInputRequest {
			request, err := protocol.Decode[protocol.UserInputRequestPayload](env)
			if err == nil && request.RequestID == p.request.RequestID {
				continue
			}
		}
		kept = append(kept, env)
	}
	m.pending = kept
	m.userInputRevision[p.request.RunID]++
	if p.deliveryCancel != nil && !p.writeStarted {
		p.deliveryCancel()
	}
	if !wasDelivering {
		m.releaseUserInputLocked(p)
	}
	_ = reason // Only the emitted status carries the human-readable reason.
}

func (m *OrchestrationManager) releaseUserInputLocked(p *pendingUserInput) {
	// Keep only the routing identity and a non-sensitive receipt. The closure
	// may capture original questions and the native socket or process.
	p.request.Questions = nil
	p.request.WorkerID = ""
	p.request.TurnID = ""
	p.request.CLI = ""
	p.ctx = nil
	p.deliver = nil
	p.deliveryCancel = nil
	p.writeStarted = false
}

func (m *OrchestrationManager) userInputReceiptLocked(p *pendingUserInput, submissionID string) *userInputReceiptRecord {
	if record, ok := p.receipts[submissionID]; ok {
		return &record
	}
	return nil
}

func (m *OrchestrationManager) recordUserInputReceiptLocked(p *pendingUserInput, receipt protocol.UserInputReceiptPayload, phase string) {
	if p.receipts == nil {
		p.receipts = make(map[string]userInputReceiptRecord)
	}
	p.receipts[receipt.SubmissionID] = userInputReceiptRecord{receipt: receipt, phase: phase}
}

// A queued or currently writing answer reserves its future receipt slot. Once
// a run reaches the 64-submission recovery bound, further unanswered requests
// close rather than forgetting an older idempotency result and risking replay.
func (m *OrchestrationManager) userInputReceiptSlotsLocked(runID string) int {
	slots := 0
	for _, p := range m.userInputs {
		if p.request.RunID != runID {
			continue
		}
		slots += len(p.receipts)
		if p.receipt == nil && (p.state == "queued" || p.state == "delivering" || p.deliveryCancel != nil) {
			slots++
		}
	}
	return slots
}

func (m *OrchestrationManager) userInputStatusLocked(p *pendingUserInput, submissionID string) protocol.UserInputStatusPayload {
	status := protocol.UserInputStatusPayload{
		RequestID: p.request.RequestID, RunID: p.request.RunID,
		ExecutionID: p.request.ExecutionID, InstanceID: m.userInputInstanceID,
		SubmissionID: submissionID, Revision: m.userInputRevision[p.request.RunID], Source: "bridge",
		Lifecycle: "open", Status: "pending",
	}
	if p.state == "closed" {
		status.Lifecycle, status.Status = "closed", "closed"
	} else if p.state == "delivered" {
		status.Status = "delivered"
	} else if p.state == "queued" || p.state == "delivering" {
		status.Status = "queued"
	}
	if record := m.userInputReceiptLocked(p, submissionID); record != nil {
		receipt := record.receipt
		status.Receipt = &receipt
		if receipt.Outcome == "not_sent" && p.state != "closed" {
			status.Status, status.Phase = "rejected", record.phase
		}
	}
	return status
}

func (m *OrchestrationManager) closeRunUserInputs(runID, reason string) {
	m.mu.Lock()
	ids := make([]string, 0)
	for id, p := range m.userInputs {
		if (p.request.RunID == runID || p.request.ExecutionID == runID) && p.state != "closed" {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.CloseUserInput(id, reason)
	}
}

func (m *OrchestrationManager) UserInputSync(sync protocol.UserInputSyncPayload) {
	m.mu.Lock()
	m.pruneUserInputsLocked()
	snapshot := protocol.UserInputSnapshotPayload{
		RunID: sync.RunID, SyncID: sync.SyncID, InstanceID: m.userInputInstanceID,
		Revision: m.userInputRevision[sync.RunID], Requests: []protocol.UserInputRequestPayload{},
	}
	if len(sync.PendingSubmissions) > 64 || (len(sync.PendingSubmissions) > 0 && sync.InstanceID != m.userInputInstanceID) {
		snapshot.ReconcileError = "native input instance cannot be confirmed"
	} else {
		seen := make(map[string]bool, len(sync.PendingSubmissions))
		for _, pending := range sync.PendingSubmissions {
			key := pending.RequestID + "\x00" + pending.SubmissionID
			if pending.RequestID == "" || pending.SubmissionID == "" || pending.ExecutionID == "" || len(pending.RequestID) > 128 || len(pending.SubmissionID) > 128 || len(pending.ExecutionID) > 128 || seen[key] {
				snapshot.ReconcileError = "native input submission query is invalid"
				break
			}
			seen[key] = true
		}
	}
	if snapshot.ReconcileError == "" {
		for _, pending := range sync.PendingSubmissions {
			p := m.userInputs[pending.RequestID]
			if p == nil || p.request.RunID != sync.RunID || p.request.ExecutionID != pending.ExecutionID {
				snapshot.Reconciliations = append(snapshot.Reconciliations, protocol.UserInputStatusPayload{
					RequestID: pending.RequestID, RunID: sync.RunID, ExecutionID: pending.ExecutionID,
					InstanceID: m.userInputInstanceID, SubmissionID: pending.SubmissionID,
					Revision: m.userInputRevision[sync.RunID], Source: "bridge", Status: "closed", Lifecycle: "closed",
					Reason: "native input identity is no longer available",
				})
				continue
			}
			if m.userInputReceiptLocked(p, pending.SubmissionID) != nil || p.state == "closed" {
				snapshot.Reconciliations = append(snapshot.Reconciliations, m.userInputStatusLocked(p, pending.SubmissionID))
				continue
			}
			if p.state == "queued" || p.state == "delivering" || p.state == "delivered" {
				snapshot.Reconciliations = append(snapshot.Reconciliations, m.userInputStatusLocked(p, p.submissionID))
				continue
			}
			if m.userInputReceiptSlotsLocked(sync.RunID) >= userInputTerminalPerRunMax {
				snapshot.ReconcileError = "native input receipt capacity is full"
				break
			}
			m.userInputRevision[sync.RunID]++
			receipt := protocol.UserInputReceiptPayload{
				RequestID: pending.RequestID, RunID: sync.RunID, ExecutionID: pending.ExecutionID,
				InstanceID: m.userInputInstanceID, SubmissionID: pending.SubmissionID,
				Outcome: "not_sent", Revision: m.userInputRevision[sync.RunID],
			}
			m.recordUserInputReceiptLocked(p, receipt, "reconcile")
			snapshot.Reconciliations = append(snapshot.Reconciliations, m.userInputStatusLocked(p, pending.SubmissionID))
		}
	}
	if snapshot.ReconcileError != "" {
		snapshot.Reconciliations = nil
	}
	snapshot.Revision = m.userInputRevision[sync.RunID]
	for _, p := range m.userInputs {
		if p.request.RunID != sync.RunID {
			continue
		}
		for _, record := range p.receipts {
			snapshot.Receipts = append(snapshot.Receipts, record.receipt)
		}
		if p.state == "closed" {
			snapshot.Closed = append(snapshot.Closed, m.userInputStatusLocked(p, p.submissionID))
			continue
		}
		if p.ctx != nil && p.ctx.Err() == nil {
			snapshot.Requests = append(snapshot.Requests, p.request)
			if p.state == "delivered" || p.state == "queued" || p.state == "delivering" {
				snapshot.Statuses = append(snapshot.Statuses, m.userInputStatusLocked(p, p.submissionID))
			}
		}
	}
	m.mu.Unlock()
	m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", snapshot))
}

// UserInputUnavailable closes a stale browser card even if its terminal entry
// has already been pruned from Bridge's bounded cache.
func (m *OrchestrationManager) UserInputUnavailable(res protocol.UserInputResponsePayload) {
	m.mu.Lock()
	if m.userInputRevision == nil {
		m.userInputRevision = make(map[string]uint64)
	}
	m.userInputRevision[res.RunID]++
	revision := m.userInputRevision[res.RunID]
	m.mu.Unlock()
	m.sendUserInputFrame(protocol.MustEnvelope(protocol.TypeUserInputStatus, "", protocol.UserInputStatusPayload{
		RequestID: res.RequestID, RunID: res.RunID, SubmissionID: res.SubmissionID,
		Status: "closed", Lifecycle: "closed", Source: "bridge", InstanceID: m.userInputInstanceID,
		Reason: "native user input request is no longer available", Revision: revision,
	}))
}

func (m *OrchestrationManager) pruneUserInputsLocked() {
	// Terminal metadata has both an age bound and count bounds. Active native
	// callbacks are never removed by an unrelated completed run.
	now := time.Now()
	kept := m.userInputOrder[:0]
	perRun := map[string]int{}
	receiptsPerRun := map[string]int{}
	terminalCount := 0
	for _, id := range m.userInputOrder {
		p := m.userInputs[id]
		if p == nil {
			continue
		}
		if p.state == "closed" && p.deliveryCancel == nil && now.Sub(p.closedAt) >= userInputTerminalTTL {
			delete(m.userInputs, id)
			continue
		}
		kept = append(kept, id)
		receiptsPerRun[p.request.RunID] += len(p.receipts)
		if p.state == "closed" && p.deliveryCancel == nil {
			perRun[p.request.RunID]++
			terminalCount++
		}
	}
	remaining := kept[:0]
	for _, id := range kept {
		p := m.userInputs[id]
		if p.state == "closed" && p.deliveryCancel == nil && (terminalCount > userInputTerminalMax || perRun[p.request.RunID] > userInputTerminalPerRunMax || receiptsPerRun[p.request.RunID] > userInputTerminalPerRunMax && len(p.receipts) > 0) {
			terminalCount--
			perRun[p.request.RunID]--
			receiptsPerRun[p.request.RunID] -= len(p.receipts)
			delete(m.userInputs, id)
			continue
		}
		remaining = append(remaining, id)
	}
	m.userInputOrder = remaining
}
