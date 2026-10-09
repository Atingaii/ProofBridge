package bridge

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	bridgeprofiles "github.com/tencent/codex-bridge/internal/bridge/profiles"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type OrchestrationManager struct {
	cfg                     *config.Config
	tbci                    *tbciRuntime
	cliConfig               *cliConfigManager
	lifetime                context.Context
	stop                    context.CancelFunc
	sendMu                  sync.Mutex
	progressMu              sync.Mutex
	mu                      sync.Mutex
	liveInputs              map[string]*liveInputMailbox
	runs                    map[string]*orchestrationRunHandle
	sessions                map[string]*orchestrationNativeSession
	output                  chan<- protocol.Envelope
	pending                 []protocol.Envelope
	userInputOutbox         *userInputOutbox
	approvals               map[string]orchestrationApproval
	userInputs              map[string]*pendingUserInput
	userInputRevision       map[string]uint64
	userInputOrder          []string
	userInputInstanceID     string
	userInputDispatchMu     sync.Mutex
	userInputDispatchQueue  []queuedUserInputWork
	userInputDispatchActive int
	userInputDispatchBusy   map[string]bool
	codexUserInputOnce      sync.Once
	codexUserInputAvailable bool
	conclusions             map[string]bool
	executions              map[string]orchestrationExecution
	progress                map[commandProgressKey]*pendingCommandProgress
	progressSeq             uint64
	// modelCapacityRetryWaits is intentionally kept on the manager rather than
	// as global mutable test state. Production uses the default schedule; focused
	// tests can shorten it without changing other concurrently running managers.
	modelCapacityRetryWaits []time.Duration
	// cliTransportRetryWaits is independent from missing-final continuations:
	// a stream failure while requesting a conclusion must not consume the
	// conclusion budget itself.
	cliTransportRetryWaits []time.Duration
	// codexThreadBusyRetryWaits handles a resumed native thread whose previous
	// turn still owns Codex's writer lease. The rejected prompt is unchanged.
	codexThreadBusyRetryWaits []time.Duration
	nativeUsage               map[string][]orchestrationUsage
}

type orchestrationExecution struct {
	runID string
	task  *protocol.TaskAttemptRef
}

type orchestrationApproval struct {
	runID string
	ch    chan protocol.ApprovalResponsePayload
}

type orchestrationRunHandle struct {
	cancel context.CancelFunc
	// done is closed when the run goroutine has fully exited. Start waits on it
	// before launching a replacement goroutine for the same run id so a
	// superseded run cannot interleave stale terminal events after the new
	// run's start.
	done chan struct{}
}

type orchestrationSessionState struct {
	WorkerPair           string
	CodexThreadID        string
	CodexThreadIDs       map[string]string
	ClaudeSessionID      string
	ClaudeSessionStarted bool
	ClaudeSessionIDs     map[string]string
	ClaudeStartedSlots   map[string]bool
	CodexResumeMode      string
	CodexResumeModes     map[string]string
	ClaudeResumeMode     string
	NativeSession        *orchestrationNativeSession
	CommandFingerprints  map[string]bridgeprofiles.CommandFingerprint
}

type orchestrationNativeSession struct {
	runID                   string
	cwd                     string
	nativeContextCompaction string
	mu                      sync.Mutex
	codex                   map[string]*orchestrationCodexSession
	claude                  map[string]*orchestrationClaudeSession
	profileRuntime          map[string]orchestrationWorkerRuntime
}

type workspaceSnapshot struct {
	Root      string
	Files     map[string]workspaceFileState
	Available bool
	Truncated bool
	Err       string
}

type workspaceFileState struct {
	Size    int64
	ModTime int64
}

type workspaceChangeReport struct {
	Root      string
	Changed   []string
	Available bool
	Truncated bool
	Err       string
}

var safeOrchestrationFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

const (
	orchestrationTurnContinuationMaxAttempts = 3
	orchestrationTurnContinuationIdleWait    = 200 * time.Millisecond
	orchestrationCodexDefaultSlot            = "codex"
	orchestrationCodexSlotA                  = "codex-a"
	orchestrationCodexSlotB                  = "codex-b"
	orchestrationClaudeDefaultSlot           = "claude"
	orchestrationClaudeSlotA                 = "claude-a"
	orchestrationClaudeSlotB                 = "claude-b"
)

var defaultModelCapacityRetryWaits = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute}
var defaultCLITransportRetryWaits = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
var defaultCodexThreadBusyRetryWaits = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}

func NewOrchestrationManager(cfg *config.Config) *OrchestrationManager {
	lifetime, stop := context.WithCancel(context.Background())
	return &OrchestrationManager{
		cfg:                       cfg,
		tbci:                      configuredTBCIRuntime(cfg),
		lifetime:                  lifetime,
		stop:                      stop,
		userInputOutbox:           newUserInputOutbox(lifetime),
		runs:                      make(map[string]*orchestrationRunHandle),
		sessions:                  make(map[string]*orchestrationNativeSession),
		approvals:                 make(map[string]orchestrationApproval),
		userInputs:                make(map[string]*pendingUserInput),
		userInputRevision:         make(map[string]uint64),
		userInputInstanceID:       store.NewID("uiinst"),
		conclusions:               make(map[string]bool),
		executions:                make(map[string]orchestrationExecution),
		progress:                  make(map[commandProgressKey]*pendingCommandProgress),
		modelCapacityRetryWaits:   append([]time.Duration(nil), defaultModelCapacityRetryWaits...),
		cliTransportRetryWaits:    append([]time.Duration(nil), defaultCLITransportRetryWaits...),
		codexThreadBusyRetryWaits: append([]time.Duration(nil), defaultCodexThreadBusyRetryWaits...),
		nativeUsage:               make(map[string][]orchestrationUsage),
	}
}

func configuredTBCIRuntime(cfg *config.Config) *tbciRuntime {
	if cfg == nil {
		return unavailableTBCIRuntime("error", "", "Bridge configuration is unavailable")
	}
	if !cfg.Bridge.TBCIEnabled {
		return unavailableTBCIRuntime("disabled", "", "TBCI was disabled by the Bridge administrator")
	}
	path := strings.TrimSpace(cfg.Bridge.TBCIPath)
	if path == "" || strings.EqualFold(path, "auto") {
		return unavailableTBCIRuntime("error", "", "managed TBCI was not prepared before orchestration")
	}
	return &tbciRuntime{path: path, capability: protocol.TBCICapability{Available: true, State: "ready"}}
}

func (m *OrchestrationManager) SetTBCIRuntime(runtime *tbciRuntime) {
	m.mu.Lock()
	m.tbci = runtime
	m.mu.Unlock()
}

func (m *OrchestrationManager) tbciRuntime() *tbciRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tbci
}

func (m *OrchestrationManager) SetCLIConfigManager(manager *cliConfigManager) {
	m.mu.Lock()
	m.cliConfig = manager
	m.mu.Unlock()
}

func (m *OrchestrationManager) AttachOut(out chan<- protocol.Envelope) {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	m.mu.Lock()
	pending := append([]protocol.Envelope(nil), m.pending...)
	m.pending = nil
	m.output = out
	m.userInputOutbox.attach(out)
	m.mu.Unlock()

	for index, env := range pending {
		timer := time.NewTimer(orchestrationSendTimeout)
		select {
		case out <- env:
			timer.Stop()
		case <-timer.C:
			m.mu.Lock()
			remaining := append([]protocol.Envelope(nil), pending[index:]...)
			m.pending = append(remaining, m.pending...)
			if len(m.pending) > 1000 {
				m.pending = m.pending[len(m.pending)-1000:]
			}
			if m.output == out {
				m.output = nil
			}
			m.mu.Unlock()
			slog.Warn("[bridge] orchestration reconnect flush paused: outbound queue saturated", "remaining", len(remaining))
			return
		}
	}
}

func (m *OrchestrationManager) DetachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userInputOutbox.detach(out)
	if m.output == out {
		m.output = nil
	}
}

func (m *OrchestrationManager) Start(payload protocol.OrchestrationStartPayload) {
	if payload.RunID == "" {
		m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
			Kind:  "run.error",
			Error: "orchestration run id is required",
		}))
		return
	}
	executionKey := orchestrationExecutionKey(payload)
	publicRunID := payload.RunID
	// A run belongs to the Bridge process, not to the reverse WebSocket that
	// delivered this frame. Transport loss only detaches output and must never
	// cancel the native CLI process.
	ctx, cancel := context.WithCancel(m.lifetime)
	// Cancel and join any goroutine still owning this run id before starting
	// the replacement: otherwise its stale terminal events interleave after
	// the new run.start and the hub records the wrong final status.
	for {
		m.mu.Lock()
		old := m.runs[executionKey]
		if old == nil {
			break
		}
		oldSession := m.sessions[executionKey]
		delete(m.sessions, executionKey)
		m.mu.Unlock()
		old.cancel()
		if oldSession != nil {
			oldSession.close()
		}
		if old.done != nil {
			<-old.done
		}
	}
	handle := &orchestrationRunHandle{cancel: cancel, done: make(chan struct{})}
	m.runs[executionKey] = handle
	m.executions[executionKey] = orchestrationExecution{runID: publicRunID, task: orchestrationTaskRef(payload)}
	delete(m.conclusions, executionKey)
	m.mu.Unlock()
	payload.RunID = executionKey

	go func() {
		defer close(handle.done)
		defer m.closeLiveInput(executionKey)
		defer func() {
			m.flushCommandProgressForRun(executionKey)
			cancel()
			m.mu.Lock()
			current := m.runs[executionKey]
			if m.runs[executionKey] == handle {
				delete(m.runs, executionKey)
			}
			delete(m.executions, executionKey)
			m.mu.Unlock()
			if current == handle {
				m.cancelApprovals(executionKey)
				m.closeRunUserInputs(executionKey, "run ended")
			}
		}()
		m.run(ctx, payload)
	}()
}

func (m *OrchestrationManager) Cancel(runID string) {
	m.mu.Lock()
	var handles []*orchestrationRunHandle
	var keys []string
	for key, handle := range m.runs {
		execution := m.executions[key]
		if key == runID || execution.runID == runID {
			handles = append(handles, handle)
			keys = append(keys, key)
		}
	}
	m.mu.Unlock()
	for _, handle := range handles {
		if handle != nil {
			handle.cancel()
		}
	}
	for _, key := range keys {
		m.closeNativeSession(key)
		m.cancelApprovals(key)
		m.closeRunUserInputs(key, "run canceled")
	}
}

func orchestrationExecutionKey(payload protocol.OrchestrationStartPayload) string {
	if payload.TaskGraph != nil && len(payload.TaskGraph.Tasks) == 1 && payload.TaskGraph.Tasks[0].AttemptID != "" {
		return payload.RunID + ":" + payload.TaskGraph.Tasks[0].AttemptID
	}
	return payload.RunID
}

func orchestrationTaskRef(payload protocol.OrchestrationStartPayload) *protocol.TaskAttemptRef {
	if payload.TaskGraph == nil || len(payload.TaskGraph.Tasks) != 1 {
		return nil
	}
	task := payload.TaskGraph.Tasks[0]
	return &protocol.TaskAttemptRef{
		GraphID:       payload.TaskGraph.ID,
		TaskID:        task.ID,
		AttemptID:     task.AttemptID,
		Name:          task.Name,
		Role:          task.Role,
		WorkerSlot:    task.WorkerSlot,
		Round:         payload.TaskGraph.Round,
		MaxRounds:     payload.TaskGraph.MaxRounds,
		PayloadDigest: task.PayloadDigest,
	}
}

func (m *OrchestrationManager) executionFor(key string) orchestrationExecution {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.executions[key]
}

func (m *OrchestrationManager) CloseAll() {
	m.flushAllCommandProgress()
	if m.stop != nil {
		m.stop()
	}
	m.mu.Lock()
	var cancels []context.CancelFunc
	runIDs := make([]string, 0, len(m.runs))
	for runID, handle := range m.runs {
		if handle != nil {
			cancels = append(cancels, handle.cancel)
		}
		runIDs = append(runIDs, runID)
		delete(m.runs, runID)
	}
	var sessions []*orchestrationNativeSession
	for runID, session := range m.sessions {
		sessions = append(sessions, session)
		delete(m.sessions, runID)
	}
	for runID := range m.conclusions {
		delete(m.conclusions, runID)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, session := range sessions {
		session.close()
	}
	for _, runID := range runIDs {
		m.cancelApprovals(runID)
		m.closeRunUserInputs(runID, "bridge stopped")
	}
}

func (m *OrchestrationManager) nativeSession(runID, cwd string) *orchestrationNativeSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[string]*orchestrationNativeSession)
	}
	session := m.sessions[runID]
	if session == nil {
		session = &orchestrationNativeSession{runID: runID, cwd: cwd, codex: map[string]*orchestrationCodexSession{}, claude: map[string]*orchestrationClaudeSession{}, profileRuntime: map[string]orchestrationWorkerRuntime{}}
		m.sessions[runID] = session
	} else if cwd != "" {
		session.cwd = cwd
	}
	return session
}

func (m *OrchestrationManager) closeNativeSession(runID string) {
	m.mu.Lock()
	session := m.sessions[runID]
	delete(m.sessions, runID)
	m.mu.Unlock()
	if session != nil {
		session.close()
	}
}

func (s *orchestrationNativeSession) close() {
	s.mu.Lock()
	var codexSessions []*orchestrationCodexSession
	for _, codex := range s.codex {
		if codex != nil {
			codexSessions = append(codexSessions, codex)
		}
	}
	claudeSessions := make([]*orchestrationClaudeSession, 0, len(s.claude))
	for _, claude := range s.claude {
		if claude != nil {
			claudeSessions = append(claudeSessions, claude)
		}
	}
	runtimes := make([]orchestrationWorkerRuntime, 0, len(s.profileRuntime))
	for _, runtime := range s.profileRuntime {
		runtimes = append(runtimes, runtime)
	}
	s.codex = nil
	s.claude = nil
	s.profileRuntime = nil
	s.mu.Unlock()
	for _, codex := range codexSessions {
		if codex.client != nil {
			codex.client.unsubscribeThreadWithTimeout(codex.threadID)
			codex.client.close()
		}
	}
	for _, claude := range claudeSessions {
		_ = claude.stdin.Close()
		if claude.cmd != nil && claude.cmd.Process != nil {
			_ = terminateProcessGroup(claude.cmd.Process.Pid)
		}
		waitClaudeSessionExit(claude)
		if claude.release != nil {
			claude.release()
		}
	}
	for _, runtime := range runtimes {
		runtime.retainResumeMetadata()
	}
}

func normalizeClaudeWorkerSlot(slot string) string {
	switch strings.TrimSpace(slot) {
	case orchestrationClaudeSlotA:
		return orchestrationClaudeSlotA
	case orchestrationClaudeSlotB:
		return orchestrationClaudeSlotB
	default:
		return orchestrationClaudeDefaultSlot
	}
}

func (s *orchestrationNativeSession) claudeSessionLocked(workerSlot string) *orchestrationClaudeSession {
	if s == nil {
		return nil
	}
	return s.claude[normalizeClaudeWorkerSlot(workerSlot)]
}

func (s *orchestrationNativeSession) setClaudeSessionLocked(workerSlot string, claude *orchestrationClaudeSession) {
	if s == nil {
		return
	}
	if s.claude == nil {
		s.claude = map[string]*orchestrationClaudeSession{}
	}
	slot := normalizeClaudeWorkerSlot(workerSlot)
	if claude == nil {
		delete(s.claude, slot)
		return
	}
	s.claude[slot] = claude
}

func (s *orchestrationSessionState) claudeSessionID(workerSlot string) string {
	if s == nil {
		return ""
	}
	slot := normalizeClaudeWorkerSlot(workerSlot)
	if id := strings.TrimSpace(s.ClaudeSessionIDs[slot]); id != "" {
		return id
	}
	if slot == orchestrationClaudeDefaultSlot {
		return s.ClaudeSessionID
	}
	return ""
}

func (s *orchestrationSessionState) claudeStarted(workerSlot string) bool {
	if s == nil {
		return false
	}
	slot := normalizeClaudeWorkerSlot(workerSlot)
	if started, ok := s.ClaudeStartedSlots[slot]; ok {
		return started
	}
	return slot == orchestrationClaudeDefaultSlot && s.ClaudeSessionStarted
}

func (s *orchestrationSessionState) setClaudeStarted(workerSlot string, started bool) {
	if s == nil {
		return
	}
	slot := normalizeClaudeWorkerSlot(workerSlot)
	if s.ClaudeStartedSlots == nil {
		s.ClaudeStartedSlots = map[string]bool{}
	}
	s.ClaudeStartedSlots[slot] = started
	if slot == orchestrationClaudeDefaultSlot {
		s.ClaudeSessionStarted = started
	}
}

func (s *orchestrationSessionState) claudeSessionIDsCopy() map[string]string {
	if s == nil {
		return nil
	}
	out := map[string]string{}
	for slot, id := range s.ClaudeSessionIDs {
		if id = strings.TrimSpace(id); id != "" {
			out[slot] = id
		}
	}
	if id := strings.TrimSpace(s.ClaudeSessionID); id != "" {
		out[orchestrationClaudeDefaultSlot] = id
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *orchestrationSessionState) claudeStartedSlotsCopy() map[string]bool {
	if s == nil {
		return nil
	}
	out := map[string]bool{}
	for slot, started := range s.ClaudeStartedSlots {
		if started {
			out[normalizeClaudeWorkerSlot(slot)] = true
		}
	}
	if s.ClaudeSessionStarted {
		out[orchestrationClaudeDefaultSlot] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *orchestrationNativeSession) codexSessionLocked(workerSlot string) *orchestrationCodexSession {
	if s == nil {
		return nil
	}
	slot := normalizeCodexWorkerSlot(workerSlot)
	return s.codex[slot]
}

func (s *orchestrationNativeSession) setCodexSessionLocked(workerSlot string, codex *orchestrationCodexSession) {
	if s == nil {
		return
	}
	if s.codex == nil {
		s.codex = map[string]*orchestrationCodexSession{}
	}
	slot := normalizeCodexWorkerSlot(workerSlot)
	if codex == nil {
		delete(s.codex, slot)
		return
	}
	s.codex[slot] = codex
}

func (s *orchestrationSessionState) setCodexThreadID(workerSlot, threadID string) {
	if s == nil {
		return
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	threadID = strings.TrimSpace(threadID)
	if s.CodexThreadIDs == nil {
		s.CodexThreadIDs = map[string]string{}
	}
	if threadID == "" {
		delete(s.CodexThreadIDs, workerSlot)
		s.refreshLegacyCodexThreadID()
		return
	}
	s.CodexThreadIDs[workerSlot] = threadID
	s.refreshLegacyCodexThreadID()
}

func (s *orchestrationSessionState) codexThreadID(workerSlot string) string {
	if s == nil {
		return ""
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	if threadID := strings.TrimSpace(s.CodexThreadIDs[workerSlot]); threadID != "" {
		return threadID
	}
	if workerSlot == orchestrationCodexDefaultSlot {
		return strings.TrimSpace(s.CodexThreadID)
	}
	return ""
}

func (s *orchestrationSessionState) codexThreadIDsCopy() map[string]string {
	if s == nil {
		return nil
	}
	out := make(map[string]string, len(s.CodexThreadIDs)+1)
	for key, value := range s.CodexThreadIDs {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 && strings.TrimSpace(s.CodexThreadID) != "" {
		out[orchestrationCodexDefaultSlot] = strings.TrimSpace(s.CodexThreadID)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *orchestrationSessionState) refreshLegacyCodexThreadID() {
	if s == nil {
		return
	}
	s.CodexThreadID = firstNonEmpty(
		s.CodexThreadIDs[orchestrationCodexDefaultSlot],
		s.CodexThreadIDs[orchestrationCodexSlotA],
		s.CodexThreadIDs[orchestrationCodexSlotB],
		s.CodexThreadID,
	)
}

func orchestrationDefaultCodexSlot(workerPair string) string {
	if protocol.NormalizeOrchestrationWorkerPair(workerPair) == protocol.WorkerPairCodexCodex {
		return orchestrationCodexSlotA
	}
	return orchestrationCodexDefaultSlot
}

func (s *orchestrationSessionState) setCodexResumeMode(workerSlot, mode string) {
	if s == nil {
		return
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	mode = strings.TrimSpace(mode)
	if s.CodexResumeModes == nil {
		s.CodexResumeModes = map[string]string{}
	}
	if mode == "" {
		delete(s.CodexResumeModes, workerSlot)
		if workerSlot == orchestrationCodexDefaultSlot {
			s.CodexResumeMode = ""
		}
		return
	}
	s.CodexResumeModes[workerSlot] = mode
	if workerSlot == orchestrationCodexDefaultSlot || s.CodexResumeMode == "" {
		s.CodexResumeMode = mode
	}
}

func (s orchestrationSessionState) codexResumeMode(workerSlot string) string {
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	if mode := strings.TrimSpace(s.CodexResumeModes[workerSlot]); mode != "" {
		return mode
	}
	if workerSlot == orchestrationCodexDefaultSlot {
		return strings.TrimSpace(s.CodexResumeMode)
	}
	return ""
}

func cleanCodexThreadIDs(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanClaudeStartedSlots(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for slot, started := range values {
		if started {
			out[normalizeClaudeWorkerSlot(slot)] = true
		}
	}
	return out
}

type orchestrationApprovalRequester struct {
	manager *OrchestrationManager
	runID   string
	turnID  string
	role    string
	cli     string
	cwd     string
}

func (r orchestrationApprovalRequester) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("apr_%d", time.Now().UnixNano())
	}
	if execution := r.manager.executionFor(r.runID); execution.runID != "" {
		req.RunID = execution.runID
	} else {
		req.RunID = r.runID
	}
	req.TurnID = r.turnID
	if req.CWD == "" {
		req.CWD = r.cwd
	}
	if req.Kind == "" {
		req.Kind = "orchestration.approval"
	}
	ch := make(chan protocol.ApprovalResponsePayload, 1)
	m := r.manager
	m.mu.Lock()
	if m.approvals == nil {
		m.approvals = make(map[string]orchestrationApproval)
	}
	m.approvals[req.RequestID] = orchestrationApproval{runID: r.runID, ch: ch}
	m.mu.Unlock()

	m.send(protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case res := <-ch:
			return res, nil
		case <-ticker.C:
			m.send(protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req))
		case <-ctx.Done():
			m.removeApproval(req.RequestID)
			return protocol.ApprovalResponsePayload{}, ctx.Err()
		}
	}
}

func (m *OrchestrationManager) ApprovalResponse(res protocol.ApprovalResponsePayload) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.approvals[res.RequestID]
	if pending.ch == nil {
		return false
	}
	delete(m.approvals, res.RequestID)
	pending.ch <- res
	return true
}

func (m *OrchestrationManager) removeApproval(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.approvals, requestID)
}

func (m *OrchestrationManager) cancelApprovals(runID string) {
	m.mu.Lock()
	var pending []orchestrationApproval
	for requestID, approval := range m.approvals {
		if approval.runID == runID {
			pending = append(pending, approval)
			delete(m.approvals, requestID)
		}
	}
	m.mu.Unlock()
	for _, approval := range pending {
		approval.ch <- protocol.ApprovalResponsePayload{Decision: "cancel"}
	}
}

func (m *OrchestrationManager) run(ctx context.Context, payload protocol.OrchestrationStartPayload) {
	runCWD := m.cwd(payload)
	profile := normalizeOrchestrationProfile(payload.Profile)
	verificationMode := protocol.NormalizeVerificationMode(payload.VerificationMode)
	if profile != bridgeprofiles.Formal() {
		verificationMode = protocol.VerificationModeAgent
	}
	originalPrompt := exactOrchestrationTaskPrompt(payload)
	var bootstrapNote string
	if profile == bridgeprofiles.Formal() {
		harness, err := prepareFormalProofHarness(m.cfg, payload, runCWD)
		if err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.error",
				Status: store.OrchestrationFailed,
				Error:  err.Error(),
			})
			return
		}
		payload.Prompt = harness.Prompt
		payload.Files = nil
		bootstrapNote = harness.BootstrapNote
	} else {
		preparedPrompt, _, err := PrepareOrchestrationPromptFiles(m.cfg, runCWD, payload.RunID, payload.Prompt, payload.Files)
		if err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.error",
				Status: store.OrchestrationFailed,
				Error:  err.Error(),
			})
			return
		}
		payload.Prompt = preparedPrompt
	}
	mode := payload.Mode
	if mode != "collaboration" && mode != "debate" {
		mode = "collaboration"
	}
	workerPair := protocol.NormalizeOrchestrationWorkerPair(payload.WorkerPair)
	firstCLI := normalizeRelayFirstCLI(payload.FirstCLI)
	if workerPair == protocol.WorkerPairCodexCodex {
		firstCLI = "codex"
	} else if workerPair == protocol.WorkerPairClaudeClaude {
		firstCLI = "claude"
	}
	maxTurns := payload.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 2
	}
	maxTurnsRequested := payload.MaxTurnsRequested
	if maxTurnsRequested <= 0 {
		maxTurnsRequested = maxTurns
	}
	if maxTurns > 12 {
		maxTurns = 12
	}
	round, maxRounds := 0, 0
	if payload.TaskGraph != nil {
		round = payload.TaskGraph.Round
		maxRounds = payload.TaskGraph.MaxRounds
	}
	nativeContextCompaction := protocol.NormalizeNativeContextCompaction(payload.NativeContextCompaction)
	nativeSession := m.nativeSession(payload.RunID, runCWD)
	nativeSession.mu.Lock()
	nativeSession.nativeContextCompaction = nativeContextCompaction
	nativeSession.mu.Unlock()
	sessionState := orchestrationSessionState{
		WorkerPair:          workerPair,
		CodexThreadIDs:      cleanCodexThreadIDs(payload.CodexThreadIDs),
		CodexResumeModes:    map[string]string{},
		ClaudeSessionID:     stableOrchestrationSessionID(payload.RunID, "claude"),
		ClaudeSessionIDs:    cleanCodexThreadIDs(payload.ClaudeSessionIDs),
		ClaudeStartedSlots:  cleanClaudeStartedSlots(payload.ClaudeStartedSlots),
		NativeSession:       nativeSession,
		CommandFingerprints: map[string]bridgeprofiles.CommandFingerprint{},
	}
	if payload.Resume {
		sessionState.CodexThreadID = payload.CodexThreadID
		if sessionState.CodexThreadID != "" && len(sessionState.CodexThreadIDs) == 0 {
			sessionState.setCodexThreadID(orchestrationDefaultCodexSlot(workerPair), sessionState.CodexThreadID)
		}
		sessionState.ClaudeSessionStarted = payload.ClaudeStarted
		if payload.ClaudeStarted {
			sessionState.setClaudeStarted(orchestrationClaudeDefaultSlot, true)
		}
	}
	if workerPair == protocol.WorkerPairClaudeClaude {
		// A fresh durable Claude+Claude task has no persisted session IDs yet.
		// cleanCodexThreadIDs intentionally returns nil for an empty payload, so
		// initialize the map before assigning the deterministic slot IDs below.
		if sessionState.ClaudeSessionIDs == nil {
			sessionState.ClaudeSessionIDs = map[string]string{}
		}
		if sessionState.ClaudeSessionIDs[orchestrationClaudeSlotA] == "" {
			sessionState.ClaudeSessionIDs[orchestrationClaudeSlotA] = stableOrchestrationSessionID(payload.RunID, orchestrationClaudeSlotA)
		}
		if sessionState.ClaudeSessionIDs[orchestrationClaudeSlotB] == "" {
			sessionState.ClaudeSessionIDs[orchestrationClaudeSlotB] = stableOrchestrationSessionID(payload.RunID, orchestrationClaudeSlotB)
		}
		if payload.Resume {
			// Legacy runs carry only one boolean. New runs retain each slot's
			// actual transcript state so a never-started peer cannot resume.
			if len(payload.ClaudeStartedSlots) == 0 && payload.ClaudeStarted {
				sessionState.setClaudeStarted(orchestrationClaudeSlotA, true)
				sessionState.setClaudeStarted(orchestrationClaudeSlotB, true)
			}
		}
	}
	if sessionState.CodexThreadID == "" {
		sessionState.CodexThreadID = firstNonEmpty(sessionState.CodexThreadIDs[orchestrationCodexDefaultSlot], sessionState.CodexThreadIDs[orchestrationCodexSlotA])
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.start",
		Status:  store.OrchestrationRunning,
		Content: fmt.Sprintf("Starting relay orchestration with %d CLI turns.", maxTurns),
		RunStartData: &protocol.RunStartData{
			CWD:                     runCWD,
			Mode:                    mode,
			WorkerPair:              workerPair,
			FirstCLI:                firstCLI,
			MaxTurnsRequested:       maxTurnsRequested,
			MaxTurnsApplied:         maxTurns,
			Round:                   round,
			MaxRounds:               maxRounds,
			PromptSeq:               payload.PromptSeq,
			Profile:                 profile,
			VerificationMode:        verificationMode,
			NativeContextCompaction: nativeContextCompaction,
		},
		Data: map[string]any{
			"cwd":                     runCWD,
			"mode":                    mode,
			"workerPair":              workerPair,
			"firstCli":                firstCLI,
			"maxTurns":                maxTurns,
			"maxTurnsRequested":       maxTurnsRequested,
			"maxTurnsApplied":         maxTurns,
			"round":                   round,
			"maxRounds":               maxRounds,
			"promptSeq":               payload.PromptSeq,
			"profile":                 profile,
			"verificationMode":        verificationMode,
			"nativeContextCompaction": nativeContextCompaction,
		},
	})
	if bootstrapNote != "" {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "info",
			Role:     "bootstrap",
			CLI:      "bridge",
			Content:  bootstrapNote,
			BridgeNoteData: &protocol.BridgeNoteData{
				// Keep the historical category stable for stored timelines and UI filters.
				Category: "formal-proof-harness-bootstrap",
			},
			Data: map[string]any{
				"category": "formal-proof-harness-bootstrap",
				"cwd":      runCWD,
			},
		})
	}
	var tbciSession *tbciVerificationSession
	if shouldPrepareTBCIVerifier(profile, verificationMode) {
		m.emitTBCINote(payload.RunID, "preparing", "Preparing independent TBCI verification.", "")
		var prepared *tbciVerificationSession
		var err error
		runtime := m.tbciRuntime()
		if runtime == nil || !runtime.capability.Available {
			reason := "TBCI is unavailable on this Bridge"
			if runtime != nil && strings.TrimSpace(runtime.capability.Reason) != "" {
				reason = runtime.capability.Reason
			}
			prepared = &tbciVerificationSession{unavailableWhy: reason}
		} else {
			prepared, err = m.prepareTBCIVerifier(ctx, payload, originalPrompt)
		}
		tbciSession = prepared
		if err != nil {
			reason := "TBCI preparation failed: " + boundedTBCIReason(err.Error())
			m.emitTBCINote(payload.RunID, "unavailable", reason, reason)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "run.error", Status: store.OrchestrationFailed, Error: reason})
			return
		} else if prepared == nil || !prepared.available {
			reason := "TBCI is unavailable; this verification remains unresolved."
			if prepared != nil && strings.TrimSpace(prepared.unavailableWhy) != "" {
				reason = prepared.unavailableWhy + "; this verification remains unresolved."
			}
			m.emitTBCINote(payload.RunID, "unavailable", reason, reason)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "run.error", Status: store.OrchestrationFailed, Error: reason})
			return
		} else {
			m.emitTBCINote(payload.RunID, "prepared", "TBCI contract and initial workspace snapshot are frozen for this user task.", "")
		}
	}

	var history []orchestrationTurn
	var terminalReason string
	var verifierVerdict *protocol.VerifierVerdict
	var completionObservation *protocol.CompletionObservation
	for turn := 1; turn <= maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		turnPlan := roleForTurnWithWorkerPair(mode, workerPair, firstCLI, turn)
		if payload.TaskGraph != nil && len(payload.TaskGraph.Tasks) == 1 {
			task := payload.TaskGraph.Tasks[0]
			turnPlan.Role = task.Role
			turnPlan.WorkerSlot = task.WorkerSlot
			switch task.WorkerSlot {
			case orchestrationCodexDefaultSlot, orchestrationCodexSlotA, orchestrationCodexSlotB:
				turnPlan.CLI = "codex"
			case orchestrationClaudeDefaultSlot, orchestrationClaudeSlotA, orchestrationClaudeSlotB:
				turnPlan.CLI = "claude"
			}
		}
		role, cli, workerSlot := turnPlan.Role, turnPlan.CLI, turnPlan.WorkerSlot
		binding, bound := workerProfileBinding(payload, workerSlot, cli)
		turnModel := m.workerModelForPayload(payload, workerSlot, cli)
		turnID := fmt.Sprintf("%s-%02d", payload.RunID, turn)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-%02d", payload.RunID, payload.PromptSeq, turn)
		}
		clearRelayResumeMode(cli, workerSlot, &sessionState)
		var taskScope *durableTaskPromptScope
		if payload.TaskGraph != nil && len(payload.TaskGraph.Tasks) == 1 {
			task := payload.TaskGraph.Tasks[0]
			taskScope = &durableTaskPromptScope{Name: task.Name, Role: task.Role, Round: payload.TaskGraph.Round, MaxRounds: payload.TaskGraph.MaxRounds}
		}
		prompt := composeRelayPromptWithTaskScope(mode, firstCLI, profile, payload.Prompt, payload.Context, payload.Resume, role, cli, workerSlot, turn, maxTurns, history, taskScope)
		resumeMode := plannedRelayResumeMode(cli, workerSlot, sessionState)
		turnStartedAt := time.Now()
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: orchestrationTurnStartContent(cli, workerSlot, &sessionState, turn, maxTurns, role),
			TurnStartData: &protocol.TurnStartData{
				StartedAt:  turnStartedAt.UnixMilli(),
				CLI:        cli,
				WorkerSlot: workerSlot,
				PresetName: func() string {
					if bound {
						return binding.Name
					}
					return ""
				}(),
				Model: turnModel,
				ReasoningEffort: func() string {
					if bound {
						return binding.ReasoningEffort
					}
					return ""
				}(),
				Turn:       turn,
				MaxTurns:   maxTurns,
				Round:      round,
				MaxRounds:  maxRounds,
				PromptText: prompt,
				Profile:    profile,
				ResumeMode: resumeMode,
			},
			Data: map[string]any{
				"cwd":        m.cwd(payload),
				"cli":        cli,
				"workerSlot": workerSlot,
				"model":      turnModel,
				"presetName": func() string {
					if bound {
						return binding.Name
					}
					return ""
				}(),
				"reasoningEffort": func() string {
					if bound {
						return binding.ReasoningEffort
					}
					return ""
				}(),
				"turn":       turn,
				"maxTurns":   maxTurns,
				"promptText": prompt,
				"profile":    profile,
				"relayOnly":  true,
				"resumeMode": resumeMode,
			},
		})
		record, turnStatus, err := m.runRelayTurnWithContinuations(ctx, payload, turnID, role, cli, workerSlot, prompt, &sessionState, runCWD)
		turnCompletedAt := time.Now()
		turnEndData := &protocol.TurnEndData{
			StartedAt:   turnStartedAt.UnixMilli(),
			CompletedAt: turnCompletedAt.UnixMilli(),
			DurationMs:  turnCompletedAt.Sub(turnStartedAt).Milliseconds(),
		}
		if err != nil {
			record.Err = visibleCLIError(err)
			history = append(history, record)
			m.emitTurnUsage(payload.RunID, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:        "turn.end",
				TurnID:      turnID,
				Role:        role,
				CLI:         cli,
				Content:     relayTerminalContent([]orchestrationTurn{record}),
				Status:      "error",
				Error:       record.Err,
				TurnEndData: turnEndData,
				RunEndData:  m.relayRunEndData(cli, workerSlot, workerPair, sessionState, runCWD),
				Data:        relayTurnEndData(cli, workerSlot, sessionState),
			})
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:          "run.cancelled",
					Status:        store.OrchestrationCanceled,
					Error:         "canceled",
					RunConclusion: runConclusionForStatus(store.OrchestrationCanceled, "canceled", history),
				})
				return
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:          "run.error",
				Status:        store.OrchestrationFailed,
				CLI:           cli,
				Error:         record.Err,
				Content:       relayTerminalContent(history),
				RunConclusion: runConclusionForStatus(store.OrchestrationFailed, record.Err, history),
				Data: map[string]any{
					"relayOnly": true,
					"error":     record.Err,
				},
			})
			return
		}
		history = append(history, record)
		m.emitTurnUsage(payload.RunID, record)
		content := record.Content
		if strings.TrimSpace(content) == "" {
			content = relayTerminalContent([]orchestrationTurn{record})
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:        "turn.end",
			TurnID:      turnID,
			Role:        role,
			CLI:         cli,
			Content:     content,
			Status:      turnStatus,
			TurnEndData: turnEndData,
			RunEndData:  m.relayRunEndData(cli, workerSlot, workerPair, sessionState, runCWD),
			Data:        relayTurnEndData(cli, workerSlot, sessionState),
		})
		reviewerBoundary := shouldRunAgentVerifier(payload)
		shouldRunVerifier := turnStatus == "success" && (reviewerBoundary || completionCandidate(payload, record))
		if shouldRunVerifier {
			sourceBefore := completionSourceDigest(ctx, runCWD)
			sample := completionSample(payload, record, sourceBefore)
			sample.Plateau = reviewerBoundary && completionPlateau(payload.CompletionHistory, sample)
			if sample.Plateau {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "bridge.note", Source: "bridge", Role: "verifier", Content: "评审输出 token 连续下降并趋稳：正在独立验收；低消耗本身不会判定完成。", BridgeNoteData: &protocol.BridgeNoteData{Category: "completion-diagnostic"}})
			}
			var verdict protocol.VerifierVerdict
			var verifierAgents []agentVerifierResult
			verdictRecord := record
			if verificationMode == protocol.VerificationModeTBCI {
				var tbci tbciVerificationResult
				if tbciSession != nil && tbciSession.available {
					m.emitTBCINote(payload.RunID, "running", "TBCI is attesting and verifying the completed workspace.", "")
					tbci = m.evaluateTBCIVerifier(ctx, payload, tbciSession)
				} else {
					tbci = tbciVerificationResult{Reason: "TBCI is unavailable"}
					if tbciSession != nil && strings.TrimSpace(tbciSession.unavailableWhy) != "" {
						tbci.Reason = tbciSession.unavailableWhy
					}
				}
				verdict = tbciVerifierVerdict(tbci)
				verdictRecord.CLI = "tbci"
				if tbci.Trusted {
					m.emitTBCINote(payload.RunID, "success", "TBCI independently verified the frozen contract and final workspace.", "")
				} else if tbciSession != nil && tbciSession.available {
					m.emitTBCINote(payload.RunID, "continue", verdict.Reason+"; another configured round is required.", verdict.Reason)
				}
			} else {
				verdict, verifierAgents = m.evaluateAgentVerifierQuorum(ctx, payload, mode, profile, workerPair, firstCLI, history)
			}
			sourceAfter := completionSourceDigest(ctx, runCWD)
			if sourceBefore != "" && sourceAfter != "" && sourceBefore != sourceAfter {
				verdict = protocol.VerifierVerdict{Status: verifierVerdictContinue, Reason: "source changed during independent verification; rerun verification on the final source", Checkers: []protocol.VerifierCheck{verifierCheckContinue("source-integrity", "source changed during verification")}}
			}
			sample.SourceDigest = sourceAfter
			sample.BlockerKey = completionBlockerKey(verdict, verifierAgents)
			if reviewerBoundary {
				completionObservation = &sample
			}
			verifierVerdict = &verdict
			m.emit(payload.RunID, verifierVerdictEvent(turnID, verdictRecord, verdict, verifierAgents))
			for _, agent := range verifierAgents {
				m.emitTurnUsage(payload.RunID, orchestrationTurn{
					TurnID:     turnID + "-" + agent.Agent,
					WorkerSlot: agent.Slot,
					Role:       "verifier",
					CLI:        agent.CLI,
					Usage:      agent.Usage,
				})
			}
			if ctx.Err() != nil {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "run.cancelled", Status: store.OrchestrationCanceled, Error: "canceled"})
				return
			}
			if reviewerBoundary && verdict.Status != verifierVerdictPass && completionStalled(payload.CompletionHistory, sample) {
				terminalReason = "stalled"
				break
			}
			if verdict.Status == verifierVerdictPass {
				terminalReason = "verified-early"
				break
			}
		} else if payload.TaskGraph == nil && turnStatus == "success" && relayCanConverge(mode, profile, history) {
			// Retain the legacy in-process relay's documented reviewer/critic
			// convergence behavior. Durable graphs use the Agent Verifier quorum.
			terminalReason = "verified-early"
			legacyVerdict := collectOrchestrationVerifierFacts(mode, profile, false, history)
			verifierVerdict = &legacyVerdict
			m.emit(payload.RunID, verifierVerdictEvent(turnID, record, legacyVerdict, nil))
			break
		}
		if turn < maxTurns && turnStatus == "success" {
			m.runPostTurnNativeMaintenance(ctx, payload.RunID, turnID, role, cli, workerSlot, &sessionState)
		}
	}
	finalContent := relayTerminalContent(history)
	if terminalReason == "stalled" {
		finalContent = completedExecutionConclusion(finalContent, history, verifierVerdict, terminalReason).Summary
	}
	if terminalReason == "" && shouldRunAgentVerifier(payload) {
		terminalReason = "max-turns"
	}
	finalRunEndData := m.runEndDataWithNativeResumeForSession(&protocol.RunEndData{
		WorkerPair:            workerPair,
		CodexThreadID:         sessionState.CodexThreadID,
		CodexThreadIDs:        cleanCodexThreadIDs(sessionState.CodexThreadIDs),
		ClaudeSessionID:       sessionState.ClaudeSessionID,
		ClaudeSessionIDs:      sessionState.claudeSessionIDsCopy(),
		CodexNativeResume:     codexNativeResumeInfoForSlot(orchestrationDefaultCodexSlot(workerPair), sessionState.CodexThreadID, runCWD, codexWorkerRuntime(sessionState.NativeSession, orchestrationDefaultCodexSlot(workerPair))),
		ClaudeNativeResume:    m.claudeNativeResumeInfoForSlotWithRuntime(orchestrationClaudeDefaultSlot, sessionState.ClaudeSessionID, runCWD, claudeWorkerRuntime(sessionState.NativeSession, orchestrationClaudeDefaultSlot)),
		TerminalReason:        terminalReason,
		VerifierVerdict:       verifierVerdict,
		CompletionObservation: completionObservation,
	}, runCWD, sessionState.NativeSession)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:          "run.end",
		Status:        store.OrchestrationCompleted,
		Content:       finalContent,
		RunEndData:    finalRunEndData,
		RunConclusion: completedExecutionConclusion(finalContent, history, verifierVerdict, terminalReason),
		Data: map[string]any{
			"relayOnly":          true,
			"workerPair":         workerPair,
			"codexThreadId":      sessionState.CodexThreadID,
			"codexThreadIds":     cleanCodexThreadIDs(sessionState.CodexThreadIDs),
			"claudeSessionId":    sessionState.ClaudeSessionID,
			"claudeSessionIds":   sessionState.claudeSessionIDsCopy(),
			"claudeStartedSlots": sessionState.claudeStartedSlotsCopy(),
			"codexNativeResume":  finalRunEndData.CodexNativeResume,
			"claudeNativeResume": finalRunEndData.ClaudeNativeResume,
			"nativeResume":       finalRunEndData.NativeResume,
			"terminalReason":     finalRunEndData.TerminalReason,
			"verifierVerdict":    finalRunEndData.VerifierVerdict,
		},
	})
	m.runFinalNativeMaintenance(ctx, workerPair, mode, firstCLI, len(history), &sessionState)
}

func (m *OrchestrationManager) emitTBCINote(runID, status, content, injectedText string) {
	severity := "info"
	if status == "fallback" || status == "continue" || status == "unavailable" {
		severity = "warning"
	}
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:     "bridge.note",
		Source:   "bridge",
		Severity: severity,
		Role:     "verifier",
		CLI:      "bridge",
		Status:   status,
		Content:  content,
		BridgeNoteData: &protocol.BridgeNoteData{
			Category:     "tbci-verification",
			InjectedText: injectedText,
		},
		Data: map[string]any{"category": "tbci-verification"},
	})
}

func shouldRunAgentVerifier(payload protocol.OrchestrationStartPayload) bool {
	return payload.TaskGraph != nil && len(payload.TaskGraph.Tasks) == 1 && payload.TaskGraph.Tasks[0].Role == store.TaskRoleReviewer
}

func durableTaskHasFormalCheck(history []orchestrationTurn) bool {
	for _, turn := range history {
		if relayHasSuccessfulFormalCheck(turn.Tools) {
			return true
		}
	}
	return false
}

func durableReviewerCanComplete(profile string, history []orchestrationTurn) bool {
	if !durableReviewerCanAdvance(profile, history) {
		return false
	}
	record := history[len(history)-1]
	packet := record.Relay
	return packet.Status == "resolved" && packet.To == "user" && packet.Intent == "final" && machineExplicitNone(packet.Next) && machineExplicitNone(packet.Risks)
}

func durableReviewerCanAdvance(profile string, history []orchestrationTurn) bool {
	if len(history) == 0 {
		return false
	}
	record := history[len(history)-1]
	packet := record.Relay
	if record.Role != store.TaskRoleReviewer || !packet.Structured || (packet.Status != "resolved" && packet.Status != "needs_next" && packet.Status != "blocked") {
		return false
	}
	if !relayHasSuccessfulCommand(record.Tools) {
		return false
	}
	return normalizeOrchestrationProfile(profile) != bridgeprofiles.Formal() || durableTaskHasFormalCheck(history)
}

func (m *OrchestrationManager) runRelayTurnWithContinuations(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, workerSlot, prompt string, state *orchestrationSessionState, runCWD string) (orchestrationTurn, string, error) {
	var combined orchestrationTurn
	status := "success"
	nextPrompt := prompt
	var lastErr error
	for attempt := 0; attempt <= orchestrationTurnContinuationMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := waitOrchestrationTurnContinuationIdle(ctx); err != nil {
				return combined, status, err
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "info",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("CLI did not return a final conclusion or handoff summary; Bridge is continuing this same turn (%d/%d).", attempt, orchestrationTurnContinuationMaxAttempts),
				Data: map[string]any{
					"relayOnly": true,
					"category":  "turn-continuation-retry",
					"attempt":   attempt,
					"max":       orchestrationTurnContinuationMaxAttempts,
				},
			})
		}
		callPrompt := nextPrompt
		var err error
		for transportRetry := 0; ; transportRetry++ {
			content, tools, callErr := m.runRelayCLIWithSubmissionRetries(ctx, payload, turnID, role, cli, workerSlot, callPrompt, state)
			recordCommandFingerprints(state, runCWD, tools)
			record := newOrchestrationTurnRecordWithSlot(turnID, role, cli, workerSlot, content, tools)
			record.Usage = m.orchestrationUsageForTurn(turnID, m.workerModelForPayload(payload, workerSlot, cli), callPrompt, content)
			if callErr != nil {
				record.Err = visibleCLIError(callErr)
			}
			combined = mergeOrchestrationTurnAttempts(combined, record)
			err = callErr
			if callErr == nil {
				combined.Err = ""
			}
			if !isRecoverableCLITransportError(cli, err) {
				break
			}
			waits := m.cliTransportRetryWaits
			if len(waits) == 0 {
				waits = defaultCLITransportRetryWaits
			}
			if ctx.Err() != nil {
				return combined, status, ctx.Err()
			}
			if transportRetry >= len(waits) {
				message := visibleCLIError(err)
				combined.Err = message
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:     "turn.delta",
					Source:   "bridge",
					Severity: "error",
					TurnID:   turnID,
					Role:     role,
					CLI:      cli,
					Content:  fmt.Sprintf("%s 连接恢复失败，已完成 %d 次退避重试，无法继续此回合。", cliDisplay(cli), len(waits)),
					Error:    message,
					Data: map[string]any{
						"relayOnly": true,
						"category":  "cli-transport-retry-exhausted",
						"attempts":  len(waits),
						"error":     message,
					},
				})
				return combined, status, err
			}
			wait := waits[transportRetry]
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "warning",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("%s 流连接中断，将在 %s后从同一原生会话恢复（第 %d/%d 次重试）。", cliDisplay(cli), humanRetryWait(wait), transportRetry+1, len(waits)),
				Error:    visibleCLIError(err),
				Data: map[string]any{
					"relayOnly":         true,
					"category":          "cli-transport-retry-wait",
					"retry":             transportRetry + 1,
					"maxRetries":        len(waits),
					"retryAfterSeconds": int(wait.Seconds()),
				},
			})
			m.resetNativeInteractiveSessionForContinuation(cli, workerSlot, state)
			if waitErr := waitModelCapacityRetry(ctx, wait); waitErr != nil {
				return combined, status, waitErr
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "info",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("正在从同一 %s 会话恢复当前回合（第 %d/%d 次重试）。", cliDisplay(cli), transportRetry+1, len(waits)),
				Data: map[string]any{
					"relayOnly":  true,
					"category":   "cli-transport-retry-start",
					"retry":      transportRetry + 1,
					"maxRetries": len(waits),
				},
			})
			callPrompt = composeCLITransportRecoveryPrompt(combined, transportRetry+1, len(waits))
		}
		record := combined
		if err == nil && !orchestrationTurnNeedsContinuation(record, err) {
			return combined, status, nil
		}
		if recoverableRelayCLIError(cli, record.Content, err) && orchestrationTurnHasFinalConclusion(record) {
			warning := visibleCLIError(err)
			m.resetCodexInteractiveSessionAfterRecoverableError(workerSlot, state)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "warning",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  "Codex app-server reported an empty tail error after visible output; Bridge kept the visible reply and continued the orchestration.",
				Error:    warning,
				Data: map[string]any{
					"relayOnly":   true,
					"recoverable": true,
					"error":       warning,
					"category":    "codex-empty-tail-error-after-visible-output",
				},
			})
			return combined, status, nil
		}
		if !shouldContinueInterruptedRelayTurn(record, err) {
			return combined, status, err
		}
		lastErr = err
		if attempt >= orchestrationTurnContinuationMaxAttempts {
			if combined.Err == "" {
				combined.Err = visibleCLIError(err)
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "warning",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("CLI still did not return a final conclusion or handoff summary after %d continuation attempts; Bridge is preserving this turn's command events and moving to the next turn.", orchestrationTurnContinuationMaxAttempts),
				Error:    combined.Err,
				Data: map[string]any{
					"relayOnly": true,
					"category":  "turn-continuation-exhausted",
					"attempts":  orchestrationTurnContinuationMaxAttempts,
				},
			})
			return combined, "warning", nil
		}
		m.resetNativeInteractiveSessionForContinuation(cli, workerSlot, state)
		nextPrompt = composeInterruptedTurnContinuationPrompt(prompt, combined, attempt+1, orchestrationTurnContinuationMaxAttempts)
	}
	return combined, status, lastErr
}

func waitOrchestrationTurnContinuationIdle(ctx context.Context) error {
	timer := time.NewTimer(orchestrationTurnContinuationIdleWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mergeOrchestrationTurnAttempts(current, next orchestrationTurn) orchestrationTurn {
	if current.TurnID == "" {
		return next
	}
	currentHasUsage := orchestrationUsageHasCounts(current.Usage)
	nextHasUsage := orchestrationUsageHasCounts(next.Usage)
	if strings.TrimSpace(next.Content) != "" {
		current.Content = mergeOrchestrationTurnContent(current.Content, next.Content)
	}
	if strings.TrimSpace(next.Handoff) != "" {
		current.Handoff = next.Handoff
	}
	if next.Relay.Structured {
		current.Relay = next.Relay
	}
	if strings.TrimSpace(next.Err) != "" {
		current.Err = next.Err
	}
	current.Tools = append(current.Tools, next.Tools...)
	current.Usage.ByModel = append(usageModelParts(current.Usage), usageModelParts(next.Usage)...)
	current.Usage.InputTokens += next.Usage.InputTokens
	current.Usage.OutputTokens += next.Usage.OutputTokens
	current.Usage.CacheReadTokens += next.Usage.CacheReadTokens
	current.Usage.CacheWriteTokens += next.Usage.CacheWriteTokens
	current.Usage.EstimatedCostUSD += next.Usage.EstimatedCostUSD
	current.Usage.Estimated = current.Usage.Estimated || next.Usage.Estimated
	current.Usage.Native = current.Usage.Native || next.Usage.Native
	if currentHasUsage && nextHasUsage {
		current.Usage.CostKnown = current.Usage.CostKnown && next.Usage.CostKnown
	} else if nextHasUsage {
		current.Usage.CostKnown = next.Usage.CostKnown
	}
	if current.Usage.CostSource == "" {
		current.Usage.CostSource = next.Usage.CostSource
	} else if next.Usage.CostSource != "" && current.Usage.CostSource != next.Usage.CostSource {
		current.Usage.CostSource = "mixed"
	}
	if current.Usage.PricingModel == "" {
		current.Usage.PricingModel = next.Usage.PricingModel
	} else if next.Usage.PricingModel != "" && current.Usage.PricingModel != next.Usage.PricingModel {
		current.Usage.PricingModel = "mixed"
	}
	if current.Usage.Model == "" {
		current.Usage.Model = next.Usage.Model
	}
	return current
}

func orchestrationUsageHasCounts(usage orchestrationUsage) bool {
	return usage.InputTokens+usage.OutputTokens+usage.CacheReadTokens+usage.CacheWriteTokens > 0
}

func mergeOrchestrationTurnContent(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	if strings.HasPrefix(next, current) {
		return next
	}
	if strings.HasSuffix(current, next) {
		return current
	}
	return current + "\n\n" + next
}

func orchestrationTurnNeedsContinuation(record orchestrationTurn, err error) bool {
	if err != nil {
		return true
	}
	return !orchestrationTurnHasFinalConclusion(record)
}

func shouldContinueInterruptedRelayTurn(record orchestrationTurn, err error) bool {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return false
	}
	// Capacity retries have their own bounded policy. Once that policy returns
	// an error, do not reinterpret partial output as an unrelated missing-final
	// continuation and issue another provider request.
	if isModelCapacityError(err) {
		return false
	}
	if err == nil {
		return true
	}
	return strings.TrimSpace(record.Content) != "" || len(record.Tools) > 0
}

func (m *OrchestrationManager) resetNativeInteractiveSessionForContinuation(cli, workerSlot string, state *orchestrationSessionState) {
	if state == nil || state.NativeSession == nil {
		return
	}
	session := state.NativeSession
	session.mu.Lock()
	defer session.mu.Unlock()
	switch cli {
	case "codex":
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
		codex := session.codexSessionLocked(workerSlot)
		if codex == nil {
			return
		}
		if codex.threadID != "" {
			state.setCodexThreadID(workerSlot, codex.threadID)
		}
		if codex.client != nil {
			codex.client.close()
		}
		session.setCodexSessionLocked(workerSlot, nil)
	case "claude":
		claude := session.claudeSessionLocked(workerSlot)
		if claude == nil {
			return
		}
		_ = claude.stdin.Close()
		if claude.cmd != nil && claude.cmd.Process != nil {
			_ = terminateProcessGroup(claude.cmd.Process.Pid)
		}
		waitClaudeSessionExit(claude)
		if claude.release != nil {
			claude.release()
		}
		session.setClaudeSessionLocked(workerSlot, nil)
	}
}

func composeInterruptedTurnContinuationPrompt(original string, record orchestrationTurn, attempt, max int) string {
	var b strings.Builder
	b.WriteString("ProofBridge is continuing the same orchestration turn because the previous CLI invocation returned command events or partial visible output but no final conclusion or handoff summary. Do not treat this as a new user request, and do not discard completed work.\n\n")
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Continuation attempt: %d of %d.\n\n", attempt, max))
	if strings.TrimSpace(record.Content) != "" {
		b.WriteString("Visible output already produced in this turn:\n")
		b.WriteString(trimForPrompt(record.Content, 3000))
		b.WriteString("\n\n")
	}
	if len(record.Tools) > 0 {
		b.WriteString("Command events already observed in this turn:\n")
		for _, line := range relayCommandSummaries(record.Tools, 8) {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if strings.TrimSpace(record.Err) != "" {
		b.WriteString("Last interruption detail:\n")
		b.WriteString(trimForPrompt(record.Err, 1200))
		b.WriteString("\n\n")
	}
	b.WriteString("Continue from the current state and finish this same turn with a concise final conclusion and handoff summary. If a command failed, explain how you handled it or what remains blocked instead of ending on the raw command event.\n\n")
	b.WriteString("Original turn prompt:\n")
	b.WriteString(trimForPrompt(original, 12000))
	return b.String()
}

func composeCLITransportRecoveryPrompt(record orchestrationTurn, attempt, max int) string {
	var b strings.Builder
	b.WriteString("ProofBridge is recovering this same orchestration turn after the provider stream disconnected. This is not a new user request. Resume from the current native conversation and workspace state; do not replay the original request or repeat completed commands.\n\n")
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Transport recovery attempt: %d of %d.\n\n", attempt, max))
	if strings.TrimSpace(record.Content) != "" {
		b.WriteString("Visible assistant output already delivered before the disconnect:\n")
		b.WriteString(trimForPrompt(record.Content, 3000))
		b.WriteString("\n\n")
	}
	if len(record.Tools) > 0 {
		b.WriteString("Command events already observed (inspect their effects; do not blindly repeat them):\n")
		for _, line := range relayCommandSummaries(record.Tools, 8) {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if strings.TrimSpace(record.Err) != "" {
		b.WriteString("Last transport error:\n")
		b.WriteString(trimForPrompt(record.Err, 1200))
		b.WriteString("\n\n")
	}
	b.WriteString("Inspect the current project state, continue only unfinished work, and finish this same turn with the required concise conclusion and handoff summary. If recovery is impossible, report the concrete blocker.")
	return b.String()
}

func stableOrchestrationSessionID(runID, cli string) string {
	sum := sha1.Sum([]byte("codex-bridge/orchestration/" + runID + "/" + cli))
	raw := append([]byte(nil), sum[:16]...)
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func nativeSessionDisplayName(runID, cli string) string {
	cli = strings.TrimSpace(cli)
	if cli == "" {
		cli = "cli"
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "ProofBridge " + cli
	}
	return "ProofBridge " + cli + " " + runID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (m *OrchestrationManager) cwd(payload protocol.OrchestrationStartPayload) string {
	if strings.TrimSpace(payload.RunCWD) != "" {
		path := expandHome(payload.RunCWD)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return path
	}
	raw := payload.CWD
	if strings.TrimSpace(raw) == "" {
		raw = m.cfg.Bridge.CWD
	}
	if strings.TrimSpace(raw) == "" {
		raw = "."
	}
	path := expandHome(raw)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
}

func (m *OrchestrationManager) codexPath() string {
	if m.cfg.Bridge.CodexPath == "" {
		return "codex"
	}
	return m.cfg.Bridge.CodexPath
}

func (m *OrchestrationManager) claudePath() string {
	if m.cfg.Bridge.ClaudePath == "" {
		return "claude"
	}
	return m.cfg.Bridge.ClaudePath
}

func PrepareOrchestrationPromptFiles(cfg *config.Config, runCWD, runID, prompt string, files []protocol.AttachmentPayload) (string, []store.OrchestrationFile, error) {
	if len(files) == 0 {
		return strings.TrimSpace(prompt), nil, nil
	}
	if len(files) > 12 {
		return "", nil, errors.New("at most 12 files can be uploaded")
	}
	baseDir := runCWD
	if baseDir == "" {
		baseDir = cfg.Bridge.CWD
	}
	if baseDir == "" {
		baseDir = "."
	}
	baseDir = expandHome(baseDir)
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	uploadDir := filepath.Join(baseDir, ".codex-bridge", "orchestrations", safeFileName(runID))
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create orchestration upload directory: %w", err)
	}
	maxBytes := cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}

	var metas []store.OrchestrationFile
	var paths []string
	for i, file := range files {
		if file.Size <= 0 || file.Size > maxBytes {
			return "", nil, fmt.Errorf("file %q is too large", file.Name)
		}
		raw, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			return "", nil, fmt.Errorf("decode file %q: %w", file.Name, err)
		}
		if int64(len(raw)) > maxBytes {
			return "", nil, fmt.Errorf("file %q is too large", file.Name)
		}
		name := safeOrchestrationUploadName(file.Name)
		if name == "" {
			name = fmt.Sprintf("upload-%02d.bin", i+1)
		}
		path := filepath.Join(uploadDir, fmt.Sprintf("%s-%s", attachmentID(i), name))
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return "", nil, fmt.Errorf("write file %q: %w", file.Name, err)
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		paths = append(paths, path)
		metas = append(metas, store.OrchestrationFile{Name: file.Name, MimeType: file.MimeType, Size: int64(len(raw))})
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\nUploaded files for this orchestration run:\n")
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("\nUse these local file paths directly when the task refers to uploaded files.")
	return b.String(), metas, nil
}

func safeOrchestrationUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = safeOrchestrationFileName.ReplaceAllString(name, "-")
	return strings.Trim(name, ".-")
}

const (
	userInputOutboxActiveMax = 256
	userInputOutboxSyncMax   = 64
	userInputOutboxBatch     = 16
)

type userInputOutboxIdentity struct {
	runID        string
	closedAt     time.Time
	revision     uint64
	closedStatus protocol.UserInputStatusPayload
}

// The outbox owns only native-question frames. Ordinary events retain their
// existing pending queue. Its mutex is never acquired before manager.mu, and
// delivery never holds manager.mu or sendMu. A nonblocking channel send under
// this mutex makes detach atomic with respect to choosing the old connection.
type userInputOutbox struct {
	mu         sync.Mutex
	ctx        context.Context
	output     chan<- protocol.Envelope
	identities map[string]userInputOutboxIdentity
	frames     map[string]protocol.Envelope
	order      []string
	syncs      int
	running    bool
	closed     bool
}

func newUserInputOutbox(ctx context.Context) *userInputOutbox {
	b := &userInputOutbox{ctx: ctx, identities: make(map[string]userInputOutboxIdentity), frames: make(map[string]protocol.Envelope)}
	context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.closed = true
		b.output, b.frames, b.order, b.identities = nil, nil, nil, nil
		b.mu.Unlock()
	})
	return b
}

func (b *userInputOutbox) reserve(id, runID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	if b.closed || b.ctx.Err() != nil || len(b.identities) >= userInputOutboxActiveMax+userInputTerminalMax {
		return false
	}
	b.identities[id] = userInputOutboxIdentity{runID: runID}
	return true
}

func (b *userInputOutbox) attach(out chan<- protocol.Envelope) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.output = out
	b.startLocked()
}

func (b *userInputOutbox) detach(out chan<- protocol.Envelope) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.output == out {
		b.output = nil
	}
}

func (b *userInputOutbox) enqueue(env protocol.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.ctx.Err() != nil {
		return
	}
	b.pruneLocked()
	key := ""
	switch env.Type {
	case protocol.TypeUserInputRequest:
		req, err := protocol.Decode[protocol.UserInputRequestPayload](env)
		identity, exists := b.identities[req.RequestID]
		if err != nil || !exists || !identity.closedAt.IsZero() || req.Revision < identity.revision {
			return
		}
		identity.revision = req.Revision
		b.identities[req.RequestID] = identity
		key = "request:" + req.RequestID
	case protocol.TypeUserInputStatus:
		status, err := protocol.Decode[protocol.UserInputStatusPayload](env)
		if err != nil {
			return
		}
		identity, exists := b.identities[status.RequestID]
		if !exists {
			// Unavailable/expired requests have no reserved active slot. Bound
			// these terminal diagnostics independently of native registration.
			if len(b.identities) >= userInputOutboxActiveMax+userInputTerminalMax {
				return
			}
			identity = userInputOutboxIdentity{runID: status.RunID}
		}
		closing := status.Lifecycle == "closed" || status.Status == "closed"
		if closing && identity.closedAt.IsZero() {
			identity.closedAt = time.Now()
			b.removeLocked("request:" + status.RequestID)
			b.scrubSnapshotsLocked(status)
			// Earlier queued statuses must not briefly reopen a closed card;
			// transport receipts remain independently recoverable.
			for candidate, pending := range b.frames {
				if candidate == "status:"+status.RequestID || strings.HasPrefix(candidate, "status:"+status.RequestID+":") {
					prior, _ := protocol.Decode[protocol.UserInputStatusPayload](pending)
					prior.Lifecycle, prior.Status = "closed", "closed"
					b.frames[candidate] = protocol.MustEnvelope(protocol.TypeUserInputStatus, "", prior)
				}
			}
		}
		if !identity.closedAt.IsZero() {
			status.Lifecycle, status.Status = "closed", "closed"
		}
		if status.Revision > identity.revision {
			identity.revision = status.Revision
		}
		if closing {
			identity.closedStatus = status
		}
		b.identities[status.RequestID] = identity
		key = "status:" + status.RequestID
		if status.Receipt != nil {
			key += ":" + status.Receipt.SubmissionID
			// Manager receipt admission is bounded to 64/run. Also cap this
			// boundary so malformed callers cannot create an unbounded map.
			count := 0
			for candidate := range b.frames {
				if strings.HasPrefix(candidate, "status:"+status.RequestID+":") {
					count++
				}
			}
			if _, known := b.frames[key]; !known && count >= userInputTerminalPerRunMax {
				return
			}
		}
		if previous, ok := b.frames[key]; ok {
			prior, _ := protocol.Decode[protocol.UserInputStatusPayload](previous)
			if prior.Revision > status.Revision && !closing {
				return
			}
		}
		env = protocol.MustEnvelope(protocol.TypeUserInputStatus, "", status)
	case protocol.TypeUserInputSnapshot:
		snapshot, err := protocol.Decode[protocol.UserInputSnapshotPayload](env)
		if err != nil {
			return
		}
		key = "sync:" + snapshot.RunID + ":" + snapshot.SyncID
		if _, exists := b.frames[key]; !exists {
			if b.syncs >= userInputOutboxSyncMax {
				// Do not acknowledge a sync we cannot retain. Its client timeout
				// performs bounded reconciliation using a fresh sync identity.
				return
			}
			b.syncs++
		}
	default:
		return
	}
	if _, exists := b.frames[key]; !exists {
		b.order = append(b.order, key)
	}
	b.frames[key] = env
	if env.Type == protocol.TypeUserInputSnapshot {
		for _, identity := range b.identities {
			if !identity.closedAt.IsZero() {
				b.scrubSnapshotsLocked(identity.closedStatus)
			}
		}
	}
	b.pruneLocked()
	// Preserve immediate delivery when idle without waiting on the channel.
	b.drainLocked(1)
	b.startLocked()
}

func (b *userInputOutbox) scrubSnapshotsLocked(status protocol.UserInputStatusPayload) {
	for key, env := range b.frames {
		if env.Type != protocol.TypeUserInputSnapshot {
			continue
		}
		snapshot, err := protocol.Decode[protocol.UserInputSnapshotPayload](env)
		if err != nil || snapshot.RunID != status.RunID {
			continue
		}
		requests := make([]protocol.UserInputRequestPayload, 0, len(snapshot.Requests))
		removed := false
		for _, request := range snapshot.Requests {
			if request.RequestID == status.RequestID {
				removed = true
				continue
			}
			requests = append(requests, request)
		}
		statuses := make([]protocol.UserInputStatusPayload, 0, len(snapshot.Statuses))
		for _, pending := range snapshot.Statuses {
			if pending.RequestID == status.RequestID {
				removed = true
				continue
			}
			statuses = append(statuses, pending)
		}
		// A queued snapshot is a bounded recovery view, not an event log.
		// Keep one closure per identity and the newest 64 closures. Receipts
		// have their own snapshot collection and are not removed here.
		closed := make([]protocol.UserInputStatusPayload, 0, len(snapshot.Closed)+1)
		indices := make(map[string]int, len(snapshot.Closed))
		for _, prior := range snapshot.Closed {
			if index, exists := indices[prior.RequestID]; exists {
				if prior.Revision > closed[index].Revision {
					closed[index] = prior
				}
				continue
			}
			indices[prior.RequestID] = len(closed)
			closed = append(closed, prior)
		}
		if index, exists := indices[status.RequestID]; exists {
			if status.Revision >= closed[index].Revision {
				latest := status
				if latest.Receipt == nil {
					latest.Receipt = closed[index].Receipt
				}
				closed[index] = latest
			}
		} else if removed {
			closed = append(closed, status)
		}
		if !removed && len(closed) == len(snapshot.Closed) {
			if _, exists := indices[status.RequestID]; !exists {
				continue
			}
		}
		for len(closed) > userInputTerminalPerRunMax {
			oldest := 0
			for i := 1; i < len(closed); i++ {
				if closed[i].Revision < closed[oldest].Revision {
					oldest = i
				}
			}
			closed = append(closed[:oldest], closed[oldest+1:]...)
		}
		snapshot.Requests, snapshot.Statuses, snapshot.Closed = requests, statuses, closed
		if status.Revision > snapshot.Revision {
			snapshot.Revision = status.Revision
		}
		b.frames[key] = protocol.MustEnvelope(protocol.TypeUserInputSnapshot, "", snapshot)
	}
}

func (b *userInputOutbox) removeLocked(key string) {
	if _, exists := b.frames[key]; !exists {
		return
	}
	delete(b.frames, key)
	if strings.HasPrefix(key, "sync:") {
		b.syncs--
	}
	for i, candidate := range b.order {
		if candidate == key {
			copy(b.order[i:], b.order[i+1:])
			b.order[len(b.order)-1] = ""
			b.order = b.order[:len(b.order)-1]
			break
		}
	}
}

func (b *userInputOutbox) pruneLocked() {
	perRun := make(map[string]int)
	terminal := 0
	for _, identity := range b.identities {
		if !identity.closedAt.IsZero() {
			terminal++
			perRun[identity.runID]++
		}
	}
	for {
		oldestID := ""
		var oldest time.Time
		for id, identity := range b.identities {
			if identity.closedAt.IsZero() || (time.Since(identity.closedAt) < userInputTerminalTTL && terminal <= userInputTerminalMax && perRun[identity.runID] <= userInputTerminalPerRunMax) {
				continue
			}
			if oldestID == "" || identity.closedAt.Before(oldest) {
				oldestID, oldest = id, identity.closedAt
			}
		}
		if oldestID == "" {
			return
		}
		identity := b.identities[oldestID]
		delete(b.identities, oldestID)
		terminal--
		perRun[identity.runID]--
		for key := range b.frames {
			if key == "request:"+oldestID || key == "status:"+oldestID || strings.HasPrefix(key, "status:"+oldestID+":") {
				b.removeLocked(key)
			}
		}
	}
}

func (b *userInputOutbox) drainLocked(limit int) {
	for i := 0; i < limit && b.output != nil && len(b.order) > 0; i++ {
		key := b.order[0]
		select {
		case b.output <- b.frames[key]:
			b.removeLocked(key)
		default:
			return
		}
	}
}

func (b *userInputOutbox) startLocked() {
	if b.running || b.closed || b.output == nil || len(b.order) == 0 {
		return
	}
	b.running = true
	go func() {
		// A short pause bounds shared-channel occupancy to one batch per tick.
		// No timer or worker exists for a manager without pending questions.
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-b.ctx.Done():
				b.mu.Lock()
				b.running = false
				b.mu.Unlock()
				return
			case <-ticker.C:
				b.mu.Lock()
				b.pruneLocked()
				b.drainLocked(userInputOutboxBatch)
				if b.closed || b.output == nil || len(b.order) == 0 {
					b.running = false
					b.mu.Unlock()
					return
				}
				b.mu.Unlock()
			}
		}
	}()
}
