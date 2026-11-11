package bridge

import (
	"sort"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

type approvalReceiptRecord struct {
	status protocol.ApprovalStatusPayload
	at     time.Time
}

const approvalReceiptMax = 256

func approvalReceiptKey(requestID, decisionID string) string { return requestID + "\x00" + decisionID }

func (m *OrchestrationManager) pruneApprovalReceiptsLocked() {
	now := time.Now()
	for key, record := range m.approvalReceipts {
		// A reconciled decision is a fence until its original request closes.
		if _, open := m.approvals[record.status.RequestID]; !open && record.status.Status != "queued" && now.Sub(record.at) > userInputTerminalTTL {
			delete(m.approvalReceipts, key)
		}
	}

}

func (m *OrchestrationManager) rememberApprovalLocked(status protocol.ApprovalStatusPayload) bool {
	if m.approvalReceipts == nil {
		m.approvalReceipts = make(map[string]approvalReceiptRecord)
	}
	key := approvalReceiptKey(status.RequestID, status.DecisionID)
	m.pruneApprovalReceiptsLocked()
	if _, exists := m.approvalReceipts[key]; !exists && len(m.approvalReceipts) >= approvalReceiptMax {
		return false
	}
	status.SyncID = ""
	m.approvalReceipts[key] = approvalReceiptRecord{status: status, at: time.Now()}
	return true
}

func (m *OrchestrationManager) AutomationApprovalSync(query protocol.ApprovalSyncPayload) {
	if query.RunID == "" || query.SyncID == "" || len(query.SyncID) > 128 {
		return
	}
	m.mu.Lock()
	m.pruneApprovalReceiptsLocked()
	snapshot := protocol.ApprovalSnapshotPayload{RunID: query.RunID, SyncID: query.SyncID, InstanceID: m.userInputInstanceID, Requests: []protocol.ApprovalRequestPayload{}, Receipts: []protocol.ApprovalStatusPayload{}}
	if len(query.Pending) > 64 || (len(query.Pending) > 0 && query.InstanceID != m.userInputInstanceID) {
		snapshot.Error = "approval instance cannot be confirmed"
	} else {
		// Validate the complete batch before creating any fences.
		seen := map[string]bool{}
		for _, pending := range query.Pending {
			key := approvalReceiptKey(pending.RequestID, pending.DecisionID)
			if pending.RequestID == "" || pending.DecisionID == "" || pending.ExecutionID == "" ||
				len(pending.RequestID) > 128 || len(pending.DecisionID) > 128 || len(pending.ExecutionID) > 256 || seen[key] {
				snapshot.Error = "approval reconciliation is invalid"
				break
			}
			seen[key] = true
			if prior, ok := m.approvalReceipts[key]; ok && (prior.status.RunID != query.RunID || prior.status.ExecutionID != pending.ExecutionID) {
				snapshot.Error = "approval identity cannot be confirmed"
				break
			}
		}
		if snapshot.Error == "" {
			for _, pending := range query.Pending {
				key := approvalReceiptKey(pending.RequestID, pending.DecisionID)
				if prior, ok := m.approvalReceipts[key]; ok {
					if prior.status.RunID != query.RunID || prior.status.ExecutionID != pending.ExecutionID {
						snapshot.Error = "approval identity cannot be confirmed"
						break
					}
					continue
				}
				status := protocol.ApprovalStatusPayload{RunID: query.RunID, RequestID: pending.RequestID, ExecutionID: pending.ExecutionID, InstanceID: m.userInputInstanceID, DecisionID: pending.DecisionID, Status: "closed", Outcome: "unknown", Reason: "approval request is no longer available; prior delivery cannot be confirmed"}
				if req, ok := m.approvals[pending.RequestID]; ok && req.request.RunID == query.RunID && req.request.ExecutionID == pending.ExecutionID && req.ctx != nil && req.ctx.Err() == nil {
					status.Status = "pending"
					status.Outcome = "not_sent"
					status.Reason = "old decision fenced before delivery"
				}
				if !m.rememberApprovalLocked(status) {
					snapshot.Error = "approval recovery history is full"
					break
				}
			}
		}
	}
	for _, pending := range m.approvals {
		if pending.request.RunID == query.RunID && pending.ctx != nil && pending.ctx.Err() == nil {
			snapshot.Requests = append(snapshot.Requests, pending.request)
		}
	}
	for _, record := range m.approvalReceipts {
		if record.status.RunID == query.RunID {
			snapshot.Receipts = append(snapshot.Receipts, record.status)
		}
	}
	m.mu.Unlock()
	sort.Slice(snapshot.Requests, func(i, j int) bool { return snapshot.Requests[i].RequestID < snapshot.Requests[j].RequestID })
	sort.Slice(snapshot.Receipts, func(i, j int) bool {
		return snapshot.Receipts[i].RequestID+snapshot.Receipts[i].DecisionID < snapshot.Receipts[j].RequestID+snapshot.Receipts[j].DecisionID
	})
	m.send(protocol.MustEnvelope(protocol.TypeApprovalSnapshot, "", snapshot))
}

func (m *OrchestrationManager) AutomationApprovalDecision(decision protocol.ApprovalDecisionPayload) {
	status := protocol.ApprovalStatusPayload{RunID: decision.RunID, SyncID: decision.SyncID, RequestID: decision.RequestID, ExecutionID: decision.ExecutionID, InstanceID: m.userInputInstanceID, DecisionID: decision.DecisionID, Decision: decision.Decision, Status: "closed", Outcome: "not_sent"}
	m.mu.Lock()
	prior, exists := m.approvalReceipts[approvalReceiptKey(decision.RequestID, decision.DecisionID)]
	pending, open := m.approvals[decision.RequestID]
	switch {
	case decision.RunID == "" || decision.RequestID == "" || len(decision.RequestID) > 128 || decision.ExecutionID == "" || len(decision.ExecutionID) > 256 || decision.DecisionID == "" || len(decision.DecisionID) > 128 || decision.SyncID == "" || len(decision.SyncID) > 128:
		status.Reason = "invalid approval decision"
	case decision.InstanceID != m.userInputInstanceID:
		status.Outcome = "unknown"
		status.Reason = "Bridge instance changed; do not replay the decision"
	case exists:
		if prior.status.RunID != decision.RunID || prior.status.ExecutionID != decision.ExecutionID || (prior.status.Decision != "" && prior.status.Decision != decision.Decision) {
			status.Outcome = "unknown"
			status.Reason = "decision identity conflict"
		} else {
			status = prior.status
			status.SyncID = decision.SyncID
		}
	case !open || pending.request.RunID != decision.RunID || pending.request.ExecutionID != decision.ExecutionID || pending.ctx == nil || pending.ctx.Err() != nil:
		status.Outcome = "unknown"
		status.Reason = "approval request has ended or its execution changed; prior delivery cannot be confirmed"
	case !automationApprovalDecisionValid(decision.Decision):
		status.Status = "pending"
		status.Reason = "unsupported approval decision"
	default:
		status.Status = "queued"
		status.Outcome = "queued"
		status.Reason = "decision accepted by Bridge; native callback completion is not yet confirmed"
		if !m.rememberApprovalLocked(status) {
			status.Status = "pending"
			status.Outcome = "not_sent"
			status.Reason = "approval recovery history is full"
		} else {
			delete(m.approvals, decision.RequestID)
			pending.ch <- protocol.ApprovalResponsePayload{RequestID: decision.RequestID, Decision: decision.Decision, DecisionID: decision.DecisionID}
		}
	}
	m.mu.Unlock()
	m.send(protocol.MustEnvelope(protocol.TypeApprovalStatus, "", status))
}

func automationApprovalDecisionValid(decision string) bool {
	switch decision {
	case "accept", "decline", "cancel":
		return true
	}
	return false
}

func (m *OrchestrationManager) approvalCallbackConsumed(req protocol.ApprovalRequestPayload, res protocol.ApprovalResponsePayload) {
	status := protocol.ApprovalStatusPayload{RunID: req.RunID, RequestID: req.RequestID, ExecutionID: req.ExecutionID, InstanceID: m.userInputInstanceID, DecisionID: res.DecisionID, Decision: res.Decision, Status: "closed", Outcome: "delivered", Reason: "decision reached the approval callback; this is not a command execution acknowledgement"}
	m.mu.Lock()
	m.rememberApprovalLocked(status)
	m.mu.Unlock()
	m.send(protocol.MustEnvelope(protocol.TypeApprovalStatus, "", status))
}
