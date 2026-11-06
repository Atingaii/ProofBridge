package protocol

// Automation approval queries are independent of browser connection lifetime.
// The Bridge is authoritative for request existence and decision delivery.
const (
	TypeApprovalSync     = "approval_sync"
	TypeApprovalSnapshot = "approval_snapshot"
	TypeApprovalDecision = "approval_decision"
	TypeApprovalStatus   = "approval_status"
)

type ApprovalSyncPayload struct {
	RunID      string                    `json:"runId"`
	SyncID     string                    `json:"syncId"`
	InstanceID string                    `json:"instanceId,omitempty"`
	Pending    []ApprovalPendingDecision `json:"pending,omitempty"`
}
type ApprovalPendingDecision struct {
	RequestID   string `json:"requestId"`
	ExecutionID string `json:"executionId"`
	DecisionID  string `json:"decisionId"`
}
type ApprovalSnapshotPayload struct {
	RunID      string                   `json:"runId"`
	SyncID     string                   `json:"syncId"`
	InstanceID string                   `json:"instanceId"`
	Requests   []ApprovalRequestPayload `json:"requests"`
	Receipts   []ApprovalStatusPayload  `json:"receipts"`
	Error      string                   `json:"error,omitempty"`
}
type ApprovalDecisionPayload struct {
	RunID       string `json:"runId"`
	SyncID      string `json:"syncId"`
	RequestID   string `json:"requestId"`
	ExecutionID string `json:"executionId"`
	InstanceID  string `json:"instanceId"`
	DecisionID  string `json:"decisionId"`
	Decision    string `json:"decision"`
}
type ApprovalStatusPayload struct {
	RunID       string `json:"runId"`
	SyncID      string `json:"syncId,omitempty"`
	RequestID   string `json:"requestId"`
	ExecutionID string `json:"executionId"`
	InstanceID  string `json:"instanceId"`
	DecisionID  string `json:"decisionId,omitempty"`
	Decision    string `json:"decision,omitempty"`
	Status      string `json:"status"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason,omitempty"`
}
