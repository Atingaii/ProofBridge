package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	TypeRegister                      = "register"
	TypeRegistered                    = "registered"
	TypeHeartbeat                     = "heartbeat"
	TypeOpenSession                   = "open_session"
	TypeSessionOpened                 = "session_opened"
	TypePrompt                        = "prompt"
	TypeSessionUpdate                 = "session_update"
	TypePromptComplete                = "prompt_complete"
	TypeApprovalRequest               = "approval_request"
	TypeApprovalResponse              = "approval_response"
	TypeUserInputRequest              = "user_input_request"
	TypeUserInputResponse             = "user_input_response"
	TypeUserInputStatus               = "user_input_status"
	TypeUserInputSync                 = "user_input_sync"
	TypeUserInputSnapshot             = "user_input_snapshot"
	TypeCancel                        = "cancel"
	TypeCloseSession                  = "close_session"
	TypeOrchestrationStart            = "orchestration_start"
	TypeOrchestrationEvent            = "orchestration_event"
	TypeOrchestrationInput            = "orchestration_input"
	TypeOrchestrationInputStatus      = "orchestration_input_status"
	TypeOrchestrationCancel           = "orchestration_cancel"
	TypeOrchestrationUsageSyncRequest = "orchestration_usage_sync_request"
	TypeOrchestrationUsageSyncResult  = "orchestration_usage_sync_result"
	TypeCLIConfigTest                 = "cli_config_test"
	TypeCLIConfigApply                = "cli_config_apply"
	TypeCLIConfigReset                = "cli_config_reset"
	TypeCLIConfigExport               = "cli_config_export"
	TypeCLIConfigOfficialModels       = "cli_config_official_models"
	TypeCLIConfigResult               = "cli_config_result"
	TypeAgentShutdown                 = "agent_shutdown"
	TypeError                         = "error"
	TypeStatus                        = "status"
)

type Envelope struct {
	Type    string          `json:"type"`
	Sid     string          `json:"sid,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(typ, sid string, payload any) (Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = b
	}
	return Envelope{Type: typ, Sid: sid, Payload: raw}, nil
}

func MustEnvelope(typ, sid string, payload any) Envelope {
	env, err := NewEnvelope(typ, sid, payload)
	if err != nil {
		panic(err)
	}
	return env
}

func Decode[T any](env Envelope) (T, error) {
	var out T
	if len(env.Payload) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		return out, fmt.Errorf("decode %s payload: %w", env.Type, err)
	}
	return out, nil
}

type RegisterPayload struct {
	Name         string              `json:"name"`
	MachineID    string              `json:"machineId"`
	Hostname     string              `json:"hostname"`
	Version      string              `json:"version"`
	Instance     string              `json:"instance,omitempty"`
	WorkingDirs  []string            `json:"workingDirs,omitempty"`
	Capabilities *BridgeCapabilities `json:"capabilities,omitempty"`
}

type RegisteredPayload struct {
	AgentID string `json:"agentId"`
}

type HeartbeatPayload struct {
	TS          int64    `json:"ts,omitempty"`
	WorkingDirs []string `json:"workingDirs,omitempty"`
}

type AgentShutdownPayload struct {
	Reason string `json:"reason,omitempty"`
}

type BridgeCapabilities struct {
	OrchestrationLiveInputCLIs map[string]bool                      `json:"orchestrationLiveInputCLIs,omitempty"`
	OrchestrationLiveInput     int                                  `json:"orchestrationLiveInput,omitempty"`
	UsageModelRecovery         int                                  `json:"usageModelRecovery,omitempty"`
	Runner                     string                               `json:"runner,omitempty"`
	Sandbox                    string                               `json:"sandbox,omitempty"`
	ApprovalPolicy             string                               `json:"approvalPolicy,omitempty"`
	Chat                       map[string]BridgeCLICapability       `json:"chat,omitempty"`
	Orchestration              map[string]BridgeCLICapability       `json:"orchestration,omitempty"`
	NativeUserInput            map[string]NativeUserInputCapability `json:"nativeUserInput,omitempty"`
	Metadata                   map[string]string                    `json:"metadata,omitempty"`
	ACP                        *ACPCapability                       `json:"acp,omitempty"`
	DurableTaskGraph           bool                                 `json:"durableTaskGraph,omitempty"`
	UsageLedger                bool                                 `json:"usageLedger,omitempty"`
	ConfigSwitcher             *CLIConfigSwitcherCapability         `json:"configSwitcher,omitempty"`
	TBCI                       *TBCICapability                      `json:"tbci,omitempty"`
	// IsolatedWorkerProfiles confirms that this Bridge materializes orchestration
	// slot presets into private native CLI homes. Older Bridges accepted the
	// payload but used their legacy global configuration path instead.
	IsolatedWorkerProfiles bool `json:"isolatedWorkerProfiles,omitempty"`
}

// NativeUserInputCapability reports a tested orchestration adapter, not merely
// the presence of a CLI binary or a wire protocol frame.
type NativeUserInputCapability struct {
	Version         int      `json:"version"`
	Available       bool     `json:"available"`
	Blocking        bool     `json:"blocking,omitempty"`
	NonBlocking     bool     `json:"nonBlocking,omitempty"`
	PermissionModes []string `json:"permissionModes,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

// TBCICapability reports whether the connected Bridge can execute the
// selectable formal-proof TBCI verifier. Paths stay private to the Bridge.
type TBCICapability struct {
	Available      bool   `json:"available"`
	Managed        bool   `json:"managed,omitempty"`
	State          string `json:"state,omitempty"`
	Platform       string `json:"platform,omitempty"`
	Version        string `json:"version,omitempty"`
	ArtifactSHA256 string `json:"artifactSha256,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type CLIConfigSwitcherCapability struct {
	Version   int      `json:"version"`
	PublicKey string   `json:"publicKey"`
	KeyID     string   `json:"keyId"`
	CLIs      []string `json:"clis"`
}

type EncryptedSecret struct {
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Salt               string `json:"salt"`
	IV                 string `json:"iv"`
	Ciphertext         string `json:"ciphertext"`
}

type CLIConfigRequest struct {
	RequestID        string   `json:"requestId"`
	CLI              string   `json:"cli"`
	Name             string   `json:"name,omitempty"`
	BaseURL          string   `json:"baseUrl,omitempty"`
	Model            string   `json:"model,omitempty"`
	ReasoningEffort  string   `json:"reasoningEffort,omitempty"`
	ReasoningLevels  []string `json:"reasoningLevels,omitempty"`
	ReasoningDefault string   `json:"reasoningDefault,omitempty"`
	// ClaudeContextWindow and ClaudeDisableUnknownModelWindowEnforcement are
	// reviewed, Hub-authorized settings. Bridge only materializes them.
	ClaudeContextWindow                        int             `json:"claudeContextWindow,omitempty"`
	ClaudeDisableUnknownModelWindowEnforcement bool            `json:"claudeDisableUnknownModelWindowEnforcement,omitempty"`
	Secret                                     EncryptedSecret `json:"secret,omitempty"`
	RecipientPublicKey                         string          `json:"recipientPublicKey,omitempty"`
}

type CLIConfigResult struct {
	OfficialModels []OfficialCLIModelMetadata `json:"officialModels,omitempty"`
	RequestID      string                     `json:"requestId"`
	OK             bool                       `json:"ok"`
	CLI            string                     `json:"cli,omitempty"`
	BaseURL        string                     `json:"baseUrl,omitempty"`
	Protocol       string                     `json:"protocol,omitempty"`
	Models         []string                   `json:"models,omitempty"`
	ModelsListed   bool                       `json:"modelsListed,omitempty"`
	ModelMetadata  *CLIModelMetadata          `json:"modelMetadata,omitempty"`
	AppliedModel   string                     `json:"appliedModel,omitempty"`
	Message        string                     `json:"message,omitempty"`
	Error          string                     `json:"error,omitempty"`
	ConfigChanged  bool                       `json:"configChanged,omitempty"`
	Secret         EncryptedSecret            `json:"secret,omitempty"`
}

// OfficialCLIModelMetadata comes from the endpoint's native Codex model/list,
// separately from the Hub-reviewed third-party provider catalog.
// Official model metadata limits apply at both ends of the Bridge protocol.
const (
	MaxOfficialModels                = 500
	MaxOfficialModelIDBytes          = 256
	MaxOfficialModelDisplayNameBytes = 512
	MaxOfficialReasoningEfforts      = 16
	MaxOfficialReasoningEffortBytes  = 32
)

type OfficialCLIModelMetadata struct {
	Model                     string   `json:"model"`
	DisplayName               string   `json:"displayName"`
	SupportedReasoningEfforts []string `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort    string   `json:"defaultReasoningEffort,omitempty"`
	IsDefault                 bool     `json:"isDefault,omitempty"`
}

// CLIModelMetadata is sourced only from the Hub's reviewed model catalog.
// An unknown provider alias intentionally has no reasoning levels.
type CLIModelMetadata struct {
	Model                    string   `json:"model"`
	Reviewed                 bool     `json:"reviewed"`
	SupportedReasoningLevels []string `json:"supportedReasoningLevels,omitempty"`
	DefaultReasoningLevel    string   `json:"defaultReasoningLevel,omitempty"`
}

// ACPCapability advertises whether the endpoint can run an Agent Client
// Protocol adapter for interactive long sessions and whether those sessions can
// be resumed from the native CLI in the workspace (target B). It is nil when the
// endpoint does not use the ACP runner so existing endpoints stay unaffected.
type ACPCapability struct {
	Available bool `json:"available"`
	// LoadSession reports whether the adapter advertised session/load support so
	// the Bridge can resume an ACP session it previously opened.
	LoadSession bool `json:"loadSession"`
	// NativeResume reports whether a local `resume` command can be offered for
	// sessions opened through this endpoint (target B).
	NativeResume bool `json:"nativeResume"`
}

type BridgeCLICapability struct {
	Available       bool   `json:"available"`
	Execution       string `json:"execution,omitempty"`
	BrowserApproval bool   `json:"browserApproval"`
	ApprovalMode    string `json:"approvalMode,omitempty"`
}

type OpenSessionPayload struct {
	Sid            string `json:"sid"`
	CWD            string `json:"cwd,omitempty"`
	RemoteThreadID string `json:"remoteThreadId,omitempty"`
}

type SessionOpenedPayload struct {
	RemoteThreadID string `json:"remoteThreadId,omitempty"`
	Runner         string `json:"runner,omitempty"`
	// NativeResumeID is the underlying CLI's own session id used for local
	// takeover (target B). It is the same value as RemoteThreadID for Claude and
	// a separately resolved id for Codex. Empty when no native resume is
	// available; never fabricated.
	NativeResumeID string `json:"nativeResumeId,omitempty"`
	// NativeResumeCommand is a ready-to-copy command that continues this same
	// conversation in the native CLI from the workspace, e.g.
	// `claude --resume <id>`. Empty when unavailable.
	NativeResumeCommand string `json:"nativeResumeCommand,omitempty"`
}

type PromptPayload struct {
	Content     string              `json:"content"`
	PromptID    string              `json:"promptId,omitempty"`
	RunID       string              `json:"runId,omitempty"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
}

type AttachmentPayload struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Data     string `json:"data"`
}

type SessionUpdatePayload struct {
	Delta    string     `json:"delta,omitempty"`
	Content  string     `json:"content,omitempty"`
	RunID    string     `json:"runId,omitempty"`
	PromptID string     `json:"promptId,omitempty"`
	Event    string     `json:"event,omitempty"`
	Tool     *ToolEvent `json:"tool,omitempty"`
}

type PromptCompletePayload struct {
	Content        string          `json:"content,omitempty"`
	Usage          json.RawMessage `json:"usage,omitempty"`
	RemoteThreadID string          `json:"remoteThreadId,omitempty"`
	RunID          string          `json:"runId,omitempty"`
	PromptID       string          `json:"promptId,omitempty"`
	// NativeResumeID and NativeResumeCommand mirror SessionOpenedPayload so the
	// browser can refresh the local-takeover command after a turn (the native id
	// can become resolvable only once the CLI has written its rollout). Both are
	// optional and never fabricated.
	NativeResumeID      string `json:"nativeResumeId,omitempty"`
	NativeResumeCommand string `json:"nativeResumeCommand,omitempty"`
}

type ErrorPayload struct {
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
	RunID    string `json:"runId,omitempty"`
	PromptID string `json:"promptId,omitempty"`
}

type ApprovalRequestPayload struct {
	RequestID string          `json:"requestId"`
	Kind      string          `json:"kind"`
	Command   string          `json:"command,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ThreadID  string          `json:"threadId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	RunID     string          `json:"runId,omitempty"`
	PromptID  string          `json:"promptId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type ApprovalResponsePayload struct {
	RequestID string `json:"requestId"`
	Decision  string `json:"decision"`
}

// User input is distinct from command or file-change approval. The Bridge
// holds the native request and only uses these payloads to collect an answer.
type UserInputOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type UserInputQuestion struct {
	ID            string            `json:"id"`
	Header        string            `json:"header,omitempty"`
	Question      string            `json:"question"`
	Options       []UserInputOption `json:"options,omitempty"`
	SelectionMode string            `json:"selectionMode"` // single or multiple
	AllowCustom   bool              `json:"allowCustom,omitempty"`
	Secret        bool              `json:"secret,omitempty"`
}

type UserInputRequestPayload struct {
	RequestID   string              `json:"requestId"`
	RunID       string              `json:"runId"`
	ExecutionID string              `json:"executionId"`
	InstanceID  string              `json:"instanceId,omitempty"`
	WorkerID    string              `json:"workerId,omitempty"`
	TurnID      string              `json:"turnId,omitempty"`
	CLI         string              `json:"cli"`
	Blocking    bool                `json:"blocking"`
	Questions   []UserInputQuestion `json:"questions"`
	Revision    uint64              `json:"revision"`
}

type UserInputResponsePayload struct {
	RequestID    string                     `json:"requestId"`
	RunID        string                     `json:"runId"`
	SubmissionID string                     `json:"submissionId"`
	Answers      map[string]UserInputAnswer `json:"answers"`
}

type UserInputAnswer struct {
	OptionIDs  []string `json:"optionIds,omitempty"`
	CustomText *string  `json:"customText,omitempty"`
}

// Status is authoritative only for the Bridge's knowledge of the native
// request. "delivered" means its response was written, not model-consumed.
type UserInputStatusPayload struct {
	SyncID       string                   `json:"syncId,omitempty"` // Hub query rejection only; never a delivery receipt.
	RequestID    string                   `json:"requestId"`
	RunID        string                   `json:"runId"`
	ExecutionID  string                   `json:"executionId,omitempty"`
	InstanceID   string                   `json:"instanceId,omitempty"`
	SubmissionID string                   `json:"submissionId,omitempty"`
	Status       string                   `json:"status"`              // queued, delivered, closed, or rejected
	Lifecycle    string                   `json:"lifecycle,omitempty"` // open or closed; independent of delivery
	Receipt      *UserInputReceiptPayload `json:"receipt,omitempty"`
	Source       string                   `json:"source,omitempty"` // bridge or hub
	Phase        string                   `json:"phase,omitempty"`  // route, validate, or queue
	Reason       string                   `json:"reason,omitempty"`
	Revision     uint64                   `json:"revision"`
}

// A receipt records only a submission's transport result. It never contains
// questions or answers and may outlive the native request for a bounded time.
type UserInputReceiptPayload struct {
	RequestID    string `json:"requestId"`
	RunID        string `json:"runId"`
	ExecutionID  string `json:"executionId"`
	InstanceID   string `json:"instanceId"`
	SubmissionID string `json:"submissionId"`
	Outcome      string `json:"outcome"` // delivered, not_sent, or unknown
	Revision     uint64 `json:"revision"`
}

type UserInputSyncPayload struct {
	RunID      string `json:"runId"`
	SyncID     string `json:"syncId"`
	InstanceID string `json:"instanceId,omitempty"`
	// ClientVersion 3 supports atomic submission reconciliation. Older pages
	// cannot safely unlock a submission after an ambiguous route failure.
	ClientVersion      int                          `json:"clientVersion,omitempty"`
	PendingSubmissions []UserInputPendingSubmission `json:"pendingSubmissions,omitempty"`
}

type UserInputPendingSubmission struct {
	RequestID    string `json:"requestId"`
	SubmissionID string `json:"submissionId"`
	ExecutionID  string `json:"executionId"`
}

type UserInputSnapshotPayload struct {
	RunID           string                    `json:"runId"`
	SyncID          string                    `json:"syncId"`
	InstanceID      string                    `json:"instanceId,omitempty"`
	Revision        uint64                    `json:"revision"`
	Requests        []UserInputRequestPayload `json:"requests"`
	Statuses        []UserInputStatusPayload  `json:"statuses,omitempty"`
	Closed          []UserInputStatusPayload  `json:"closed,omitempty"`
	Receipts        []UserInputReceiptPayload `json:"receipts,omitempty"`
	Reconciliations []UserInputStatusPayload  `json:"reconciliations,omitempty"`
	ReconcileError  string                    `json:"reconcileError,omitempty"`
}

type OrchestrationStartPayload struct {
	InputVersion                   int                             `json:"inputVersion,omitempty"`
	ExecutionMode                  string                          `json:"executionMode,omitempty"`
	RunID                          string                          `json:"runId"`
	Mode                           string                          `json:"mode"`
	WorkerPair                     string                          `json:"workerPair,omitempty"`
	FirstCLI                       string                          `json:"firstCli,omitempty"`
	Prompt                         string                          `json:"prompt"`
	TaskPrompt                     string                          `json:"taskPrompt,omitempty"`
	Context                        string                          `json:"context,omitempty"`
	Resume                         bool                            `json:"resume,omitempty"`
	PromptSeq                      int64                           `json:"promptSeq,omitempty"`
	MaxTurns                       int                             `json:"maxTurns,omitempty"`
	MaxTurnsRequested              int                             `json:"maxTurnsRequested,omitempty"`
	Round                          int                             `json:"round,omitempty"`
	MaxRounds                      int                             `json:"maxRounds,omitempty"`
	CWD                            string                          `json:"cwd,omitempty"`
	Files                          []AttachmentPayload             `json:"files,omitempty"`
	CodexThreadID                  string                          `json:"codexThreadId,omitempty"`
	CodexThreadIDs                 map[string]string               `json:"codexThreadIds,omitempty"`
	ClaudeStarted                  bool                            `json:"claudeStarted,omitempty"`
	ClaudeSessionIDs               map[string]string               `json:"claudeSessionIds,omitempty"`
	ClaudeStartedSlots             map[string]bool                 `json:"claudeStartedSlots,omitempty"`
	RunCWD                         string                          `json:"runCwd,omitempty"`
	Profile                        string                          `json:"profile,omitempty"`
	VerificationMode               string                          `json:"verificationMode,omitempty"`
	NativeContextCompaction        string                          `json:"nativeContextCompaction,omitempty"`
	PlanWorkspace                  bool                            `json:"planWorkspace,omitempty"`
	NativeUserInput                bool                            `json:"nativeUserInput,omitempty"`
	NativeUserInputProtocolVersion int                             `json:"nativeUserInputProtocolVersion,omitempty"`
	CompletionHistory              []CompletionObservation         `json:"completionHistory,omitempty"`
	TaskGraph                      *TaskGraphPayload               `json:"taskGraph,omitempty"`
	WorkerProfiles                 map[string]WorkerProfileBinding `json:"workerProfiles,omitempty"`
}

// WorkerProfileBinding is an immutable Hub-authorized worker snapshot. The
// credential remains encrypted end-to-end for the selected Bridge.
type WorkerProfileBinding struct {
	OfficialLogin                              bool            `json:"officialLogin,omitempty"`
	PresetID                                   string          `json:"presetId"`
	CLI                                        string          `json:"cli"`
	Name                                       string          `json:"name"`
	BaseURL                                    string          `json:"baseUrl"`
	Model                                      string          `json:"model"`
	ReasoningEffort                            string          `json:"reasoningEffort,omitempty"`
	ReasoningLevels                            []string        `json:"reasoningLevels,omitempty"`
	ReasoningDefault                           string          `json:"reasoningDefault,omitempty"`
	ClaudeContextWindow                        int             `json:"claudeContextWindow,omitempty"`
	ClaudeDisableUnknownModelWindowEnforcement bool            `json:"claudeDisableUnknownModelWindowEnforcement,omitempty"`
	Secret                                     EncryptedSecret `json:"secret"`
}

type TaskGraphPayload struct {
	ID            string        `json:"id"`
	Generation    int           `json:"generation"`
	Round         int           `json:"round"`
	MaxRounds     int           `json:"maxRounds"`
	ParallelLimit int           `json:"parallelLimit"`
	Tasks         []TaskPayload `json:"tasks"`
}

type TaskPayload struct {
	ID            string   `json:"id"`
	AttemptID     string   `json:"attemptId"`
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	WorkerSlot    string   `json:"workerSlot,omitempty"`
	PayloadDigest string   `json:"payloadDigest"`
	Dependencies  []string `json:"dependencies,omitempty"`
}

type TaskAttemptRef struct {
	GraphID       string `json:"graphId"`
	TaskID        string `json:"taskId"`
	AttemptID     string `json:"attemptId"`
	Name          string `json:"name,omitempty"`
	Role          string `json:"role,omitempty"`
	WorkerSlot    string `json:"workerSlot,omitempty"`
	Round         int    `json:"round,omitempty"`
	MaxRounds     int    `json:"maxRounds,omitempty"`
	PayloadDigest string `json:"payloadDigest"`
}

type OrchestrationCancelPayload struct {
	RunID string `json:"runId"`
}

type OrchestrationUsageSession struct {
	CLI        string `json:"cli"`
	WorkerSlot string `json:"workerSlot,omitempty"`
	SessionID  string `json:"sessionId"`
	// Isolated prevents a post-run ledger scan from falling back to the
	// operator's global CLI home for a profile-bound worker.
	Isolated bool `json:"isolated,omitempty"`
}

// OrchestrationUsageRecovery identifies a lost first planning session without
// exposing native log contents. InputTokens uses the Codex inclusive-cache convention.
type OrchestrationUsageRecovery struct {
	WorkerSlot      string `json:"workerSlot"`
	CWD             string `json:"cwd"`
	StartedAt       int64  `json:"startedAt"`
	EndedAt         int64  `json:"endedAt"`
	InputTokens     int64  `json:"inputTokens"`
	CacheReadTokens int64  `json:"cacheReadTokens"`
	OutputTokens    int64  `json:"outputTokens"`
}

type OrchestrationUsageSyncRequest struct {
	RunID    string                       `json:"runId"`
	Sessions []OrchestrationUsageSession  `json:"sessions"`
	Recovery []OrchestrationUsageRecovery `json:"recovery,omitempty"`
}

type OrchestrationUsageEvent struct {
	EventID          string `json:"eventId"`
	CLI              string `json:"cli"`
	WorkerSlot       string `json:"workerSlot,omitempty"`
	SessionID        string `json:"sessionId"`
	OccurredAt       int64  `json:"occurredAt,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	InputTokens      int64  `json:"inputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens,omitempty"`
}

type OrchestrationUsageSessionResult struct {
	CLI        string `json:"cli"`
	WorkerSlot string `json:"workerSlot,omitempty"`
	SessionID  string `json:"sessionId"`
	Status     string `json:"status"`
	EventCount int    `json:"eventCount"`
	Error      string `json:"error,omitempty"`
}

type OrchestrationUsageSyncResult struct {
	RunID     string                            `json:"runId"`
	Status    string                            `json:"status"`
	ScannedAt int64                             `json:"scannedAt"`
	Sessions  []OrchestrationUsageSessionResult `json:"sessions"`
	Events    []OrchestrationUsageEvent         `json:"events"`
}

type OrchestrationEventPayload struct {
	ID             string          `json:"id,omitempty"`
	RunID          string          `json:"runId"`
	Seq            int64           `json:"seq,omitempty"`
	TurnID         string          `json:"turnId,omitempty"`
	Kind           string          `json:"kind"`
	Source         string          `json:"source,omitempty"`
	Severity       string          `json:"severity,omitempty"`
	Role           string          `json:"role,omitempty"`
	CLI            string          `json:"cli,omitempty"`
	Content        string          `json:"content,omitempty"`
	Status         string          `json:"status,omitempty"`
	Error          string          `json:"error,omitempty"`
	CommandData    *CommandData    `json:"commandData,omitempty"`
	RunStartData   *RunStartData   `json:"runStartData,omitempty"`
	TurnStartData  *TurnStartData  `json:"turnStartData,omitempty"`
	TurnEndData    *TurnEndData    `json:"turnEndData,omitempty"`
	RunEndData     *RunEndData     `json:"runEndData,omitempty"`
	BridgeNoteData *BridgeNoteData `json:"bridgeNoteData,omitempty"`
	RunConclusion  *RunConclusion  `json:"runConclusion,omitempty"`
	Data           map[string]any  `json:"data,omitempty"`
	CreatedAt      int64           `json:"createdAt,omitempty"`
	Task           *TaskAttemptRef `json:"task,omitempty"`
}

type ToolEvent struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Command  string `json:"command,omitempty"`
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

type CommandData struct {
	ID                    string `json:"id,omitempty"`
	Command               string `json:"command,omitempty"`
	Input                 string `json:"input,omitempty"`
	Output                string `json:"output,omitempty"`
	Name                  string `json:"name,omitempty"`
	Status                string `json:"status,omitempty"`
	ExitCode              *int   `json:"exitCode,omitempty"`
	StartedAt             int64  `json:"startedAt,omitempty"`
	CompletedAt           int64  `json:"completedAt,omitempty"`
	DurationMs            int64  `json:"durationMs,omitempty"`
	PID                   int    `json:"pid,omitempty"`
	PGID                  int    `json:"pgid,omitempty"`
	WillSuppressOnFailure bool   `json:"willSuppressOnFailure,omitempty"`
}

type RunStartData struct {
	CWD                     string `json:"cwd,omitempty"`
	Mode                    string `json:"mode,omitempty"`
	WorkerPair              string `json:"workerPair,omitempty"`
	FirstCLI                string `json:"firstCli,omitempty"`
	MaxTurnsRequested       int    `json:"maxTurnsRequested,omitempty"`
	MaxTurnsApplied         int    `json:"maxTurnsApplied,omitempty"`
	Round                   int    `json:"round,omitempty"`
	MaxRounds               int    `json:"maxRounds,omitempty"`
	PromptSeq               int64  `json:"promptSeq,omitempty"`
	Profile                 string `json:"profile,omitempty"`
	VerificationMode        string `json:"verificationMode,omitempty"`
	NativeContextCompaction string `json:"nativeContextCompaction,omitempty"`
}

type TurnStartData struct {
	StartedAt       int64  `json:"startedAt,omitempty"`
	CLI             string `json:"cli,omitempty"`
	WorkerSlot      string `json:"workerSlot,omitempty"`
	PresetName      string `json:"presetName,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	Turn            int    `json:"turn,omitempty"`
	MaxTurns        int    `json:"maxTurns,omitempty"`
	Round           int    `json:"round,omitempty"`
	MaxRounds       int    `json:"maxRounds,omitempty"`
	PromptText      string `json:"promptText,omitempty"`
	Profile         string `json:"profile,omitempty"`
	ResumeMode      string `json:"resumeMode,omitempty"`
}

// TurnEndData records elapsed wall-clock time for one CLI turn. It is emitted
// by Bridge so follow-up prompts append independently timed turns to the same
// orchestration run.
type TurnEndData struct {
	StartedAt   int64 `json:"startedAt,omitempty"`
	CompletedAt int64 `json:"completedAt,omitempty"`
	DurationMs  int64 `json:"durationMs,omitempty"`
}

type RunEndData struct {
	CodexThreadID         string                 `json:"codexThreadId,omitempty"`
	CodexThreadIDs        map[string]string      `json:"codexThreadIds,omitempty"`
	ClaudeSessionID       string                 `json:"claudeSessionId,omitempty"`
	ClaudeSessionIDs      map[string]string      `json:"claudeSessionIds,omitempty"`
	WorkerPair            string                 `json:"workerPair,omitempty"`
	NativeResume          []NativeResumeInfo     `json:"nativeResume,omitempty"`
	CodexNativeResume     *NativeResumeInfo      `json:"codexNativeResume,omitempty"`
	ClaudeNativeResume    *NativeResumeInfo      `json:"claudeNativeResume,omitempty"`
	TerminalReason        string                 `json:"terminalReason,omitempty"`
	VerifierVerdict       *VerifierVerdict       `json:"verifierVerdict,omitempty"`
	CompletionObservation *CompletionObservation `json:"completionObservation,omitempty"`
}

// CompletionObservation is bounded private diagnostic state, never proof of
// completion. Hub carries reviewer samples across rounds of the same user task.
type CompletionObservation struct {
	Round        int    `json:"round"`
	TaskDigest   string `json:"taskDigest,omitempty"`
	SourceDigest string `json:"sourceDigest,omitempty"`
	UsageKey     string `json:"usageKey,omitempty"`
	OutputTokens int64  `json:"outputTokens,omitempty"`
	BlockerKey   string `json:"blockerKey,omitempty"`
	Plateau      bool   `json:"plateau,omitempty"`
}

type VerifierVerdict struct {
	Status   string          `json:"status"`
	Reason   string          `json:"reason"`
	Evidence []string        `json:"evidence,omitempty"`
	Checkers []VerifierCheck `json:"checkers,omitempty"`
}

// VerifierCheck is one bounded model or local adjudication result. A passed
// verdict requires both invoked Agent Verifiers to return, with at least one
// Agent Verifier with no concrete evidence conflict, and applicable hard gates passing.
type VerifierCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Code     string `json:"code,omitempty"`
	Target   string `json:"target,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type BridgeNoteData struct {
	Category     string `json:"category,omitempty"`
	Command      string `json:"command,omitempty"`
	AfterSeconds int    `json:"afterSeconds,omitempty"`
	InjectedText string `json:"injectedText,omitempty"`
}

type NativeResumeInfo struct {
	CLI              string `json:"cli,omitempty"`
	WorkerSlot       string `json:"workerSlot,omitempty"`
	ID               string `json:"id,omitempty"`
	Command          string `json:"command,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	TranscriptPath   string `json:"transcriptPath,omitempty"`
	Visible          bool   `json:"visible"`
	VisibilityReason string `json:"visibilityReason,omitempty"`
}

const (
	NativeContextCompactionOff       = "off"
	NativeContextCompactionAfterTurn = "after-turn"
	WorkerPairClaudeCodex            = "claude-codex"
	WorkerPairCodexCodex             = "codex-codex"
	WorkerPairClaudeClaude           = "claude-claude"
	VerificationModeAgent            = "agent"
	VerificationModeTBCI             = "tbci"
)

func NormalizeNativeContextCompaction(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case NativeContextCompactionAfterTurn:
		return NativeContextCompactionAfterTurn
	default:
		return NativeContextCompactionOff
	}
}

func NormalizeOrchestrationWorkerPair(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WorkerPairCodexCodex:
		return WorkerPairCodexCodex
	case WorkerPairClaudeClaude:
		return WorkerPairClaudeClaude
	default:
		return WorkerPairClaudeCodex
	}
}

func NormalizeVerificationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case VerificationModeTBCI:
		return VerificationModeTBCI
	default:
		return VerificationModeAgent
	}
}

type RunConclusion struct {
	Outcome              string   `json:"outcome"`
	VerificationStatus   string   `json:"verificationStatus,omitempty"`
	Summary              string   `json:"summary"`
	BuildOrAuditCommands []string `json:"buildOrAuditCommands,omitempty"`
	UnmetObligations     []string `json:"unmetObligations,omitempty"`
	EvidenceRefs         []string `json:"evidenceRefs,omitempty"`
}

// OrchestrationInputPayload targets one active attempt; never silently retarget it.
type OrchestrationInputPayload struct {
	RunID        string `json:"runId"`
	MessageID    string `json:"messageId"`
	ExecutionID  string `json:"executionId"`
	Prompt       string `json:"prompt"`
	DeliveryMode string `json:"deliveryMode"`
}
type OrchestrationInputStatusPayload struct {
	RunID       string `json:"runId"`
	MessageID   string `json:"messageId"`
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}
