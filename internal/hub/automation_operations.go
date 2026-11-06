package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

// operationValidation adapts existing field validators without routing an HTTP
// request or duplicating their authorization policy. Both transports call the
// same business operations below.
type operationValidation struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *operationValidation) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}
func (r *operationValidation) WriteHeader(status int)      { r.status = status }
func (r *operationValidation) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *operationValidation) failure() error {
	var body serverutil.ErrorBody
	_ = json.Unmarshal(r.body.Bytes(), &body)
	if body.Code == "" {
		body.Code = "OPERATION_FAILED"
		body.Message = "The operation could not be completed"
	}
	status := r.status
	if status == 0 {
		status = 500
	}
	return &operationError{status, body.Code, body.Message}
}

type operationError struct {
	Status  int
	Code    string
	Message string
}

func (e *operationError) Error() string { return e.Code + ": " + e.Message }
func writeOperationError(w http.ResponseWriter, err error) {
	var op *operationError
	if errors.As(err, &op) {
		serverutil.WriteError(w, op.Status, op.Code, op.Message)
		return
	}
	serverutil.WriteError(w, 500, "STORE_ERROR", "operation temporarily unavailable")
}

func (s *Server) createOrchestrationOperation(ctx context.Context, uid string, req orchestrationCreateRequest, submissionID string) (store.OrchestrationRun, error) {
	response := &operationValidation{}
	var w http.ResponseWriter = response
	startReq := orchestrationStartRequest{
		ExecutionMode:                req.ExecutionMode,
		NativeUserInputClientVersion: req.NativeUserInputClientVersion,
		AgentID:                      req.AgentID,
		Title:                        req.Title,
		Mode:                         req.Mode,
		WorkerPair:                   req.WorkerPair,
		FirstCLI:                     req.FirstCLI,
		Profile:                      req.Profile,
		VerificationMode:             req.VerificationMode,
		NativeContextCompaction:      req.NativeContextCompaction,
		Prompt:                       req.Prompt,
		CWD:                          req.CWD,
		MaxTurns:                     req.MaxTurns,
		Files:                        req.Files,
		WorkerProfiles:               nil,
	}
	if user, err := s.store.UserByID(ctx, uid); err == nil {
		startReq.PlanWorkspace = s.featureEnabled(rollout.FeatureOrchestrationPlanWorkspace, user)
	}
	normalized, ok := s.normalizeOrchestrationStart(w, startReq)
	if !ok {
		return store.OrchestrationRun{}, response.failure()
	}
	agentID, err := s.resolveAgentID(ctx, uid, normalized.AgentID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusBadRequest
		}
		serverutil.WriteError(w, status, "BAD_AGENT", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	if normalized.ExecutionMode == "dynamic" && !s.durableTaskGraphEnabled(agentID) {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", "dynamic orchestration requires a durable-task-graph Bridge")
		return store.OrchestrationRun{}, response.failure()
	}
	if err := s.validateOrchestrationCapabilities(agentID, normalized.WorkerPair); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	if err := s.validateVerificationCapability(agentID, normalized.Profile, normalized.VerificationMode); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "VERIFICATION_CAPABILITY_UNAVAILABLE", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	if len(req.WorkerProfilePresetIDs) > 0 {
		if err := s.validateIsolatedWorkerProfileCapability(agentID); err != nil {
			serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
			return store.OrchestrationRun{}, response.failure()
		}
	}

	if !s.authorizeOfficialWorkerRequestContext(w, ctx, uid, req) {
		return store.OrchestrationRun{}, response.failure()
	}
	profiles, err := s.resolveWorkerProfiles(ctx, uid, agentID, normalized.WorkerPair, req.WorkerProfilePresetIDs, req.WorkerProfileEfforts, req.WorkerProfileModels)
	if err != nil {
		s.writeWorkerProfileError(w, err)
		return store.OrchestrationRun{}, response.failure()
	}
	normalized.WorkerProfiles = profiles
	files := orchestrationFileMeta(normalized.Files)
	run, err := s.store.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		ExecutionMode: normalized.ExecutionMode, UserID: uid, AutomationSubmissionID: submissionID,
		AgentID:                 agentID,
		Title:                   normalized.Title,
		Mode:                    normalized.Mode,
		WorkerPair:              normalized.WorkerPair,
		FirstCLI:                normalized.FirstCLI,
		Profile:                 normalized.Profile,
		VerificationMode:        normalized.VerificationMode,
		NativeContextCompaction: normalized.NativeContextCompaction,
		Prompt:                  normalized.Prompt,
		CWD:                     normalized.CWD,
		MaxTurns:                normalized.MaxTurns,
		Files:                   files,
		WorkerProfiles:          normalized.WorkerProfiles,
	})
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create orchestration run")
		return store.OrchestrationRun{}, response.failure()
	}
	if err := s.startOrchestration(ctx, run, normalized, nil, false); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	run.Status = store.OrchestrationRunning
	return run, nil
}

// The caller holds orchestrationInputMu, just like the existing browser path.
func (s *Server) continueOrchestrationOperation(ctx context.Context, uid string, run store.OrchestrationRun, req orchestrationCreateRequest, submissionID string) (store.OrchestrationRun, error) {
	response := &operationValidation{}
	var w http.ResponseWriter = response
	startReq := orchestrationStartRequest{
		ExecutionMode:                req.ExecutionMode,
		NativeUserInputClientVersion: req.NativeUserInputClientVersion,
		AgentID:                      req.AgentID,
		Title:                        req.Title,
		Mode:                         req.Mode,
		WorkerPair:                   req.WorkerPair,
		FirstCLI:                     req.FirstCLI,
		Profile:                      req.Profile,
		VerificationMode:             req.VerificationMode,
		NativeContextCompaction:      req.NativeContextCompaction,
		Prompt:                       req.Prompt,
		CWD:                          req.CWD,
		MaxTurns:                     req.MaxTurns,
		Files:                        req.Files,
		WorkerProfiles:               nil,
	}
	// Execution policy belongs to the original run and cannot change on follow-up.
	if req.ExecutionMode != "" && req.ExecutionMode != run.ExecutionMode {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_EXECUTION_MODE", "follow-up must retain the original execution mode")
		return store.OrchestrationRun{}, response.failure()
	}
	startReq.ExecutionMode = run.ExecutionMode
	previousProfiles, profilesErr := s.store.OrchestrationWorkerProfiles(ctx, run.ID)
	if profilesErr != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration worker profiles")
		return store.OrchestrationRun{}, response.failure()
	}
	if graph, graphErr := s.store.TaskGraphByRun(ctx, run.ID); graphErr == nil {
		var previous protocol.OrchestrationStartPayload
		if json.Unmarshal([]byte(graph.PayloadJSON), &previous) == nil {
			startReq.PlanWorkspace = previous.PlanWorkspace
			if previousProfiles == nil {
				// Compatibility with runs created before bindings had their own
				// private persistence table.
				previousProfiles = previous.WorkerProfiles
			}
		}
	}
	if startReq.AgentID == "" {
		startReq.AgentID = run.AgentID
	}
	if startReq.AgentID != run.AgentID {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_AGENT", "orchestration follow-up must use the same CLI endpoint as the original run")
		return store.OrchestrationRun{}, response.failure()
	}
	if startReq.Mode == "" {
		startReq.Mode = run.Mode
	}
	if startReq.WorkerPair == "" {
		startReq.WorkerPair = run.WorkerPair
	}
	if startReq.FirstCLI == "" {
		startReq.FirstCLI = run.FirstCLI
	}
	if startReq.Profile == "" {
		startReq.Profile = run.Profile
	}
	if startReq.VerificationMode == "" {
		startReq.VerificationMode = run.VerificationMode
	}
	if startReq.NativeContextCompaction == "" {
		startReq.NativeContextCompaction = run.NativeContextCompaction
	}
	if startReq.CWD == "" {
		startReq.CWD = run.CWD
	}
	if startReq.MaxTurns <= 0 {
		startReq.MaxTurns = run.MaxTurns
	}
	normalized, ok := s.normalizeOrchestrationStart(w, startReq)
	if !ok {
		return store.OrchestrationRun{}, response.failure()
	}
	agentID, err := s.resolveAgentID(ctx, uid, normalized.AgentID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusBadRequest
		}
		serverutil.WriteError(w, status, "BAD_AGENT", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	if normalized.ExecutionMode == "dynamic" && !s.durableTaskGraphEnabled(agentID) {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", "dynamic orchestration requires a durable-task-graph Bridge")
		return store.OrchestrationRun{}, response.failure()
	}
	if err := s.validateOrchestrationCapabilities(agentID, normalized.WorkerPair); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	if err := s.validateVerificationCapability(agentID, normalized.Profile, normalized.VerificationMode); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "VERIFICATION_CAPABILITY_UNAVAILABLE", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	if !s.authorizeOfficialWorkerRequestContext(w, ctx, uid, req) {
		return store.OrchestrationRun{}, response.failure()
	}
	if req.WorkerProfilePresetIDs == nil {
		normalized.WorkerProfiles = previousProfiles
	} else {
		profiles, err := s.resolveWorkerProfiles(ctx, uid, agentID, normalized.WorkerPair, req.WorkerProfilePresetIDs, req.WorkerProfileEfforts, req.WorkerProfileModels)
		if err != nil {
			s.writeWorkerProfileError(w, err)
			return store.OrchestrationRun{}, response.failure()
		}
		normalized.WorkerProfiles = profiles
	}
	if len(normalized.WorkerProfiles) > 0 {
		if err := s.validateIsolatedWorkerProfileCapability(agentID); err != nil {
			serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
			return store.OrchestrationRun{}, response.failure()
		}
	}
	if hasExplicitOfficialWorkerParameters(normalized.WorkerProfiles) {
		if err := s.validateOfficialWorkerCapability(agentID); err != nil {
			s.writeWorkerProfileError(w, err)
			return store.OrchestrationRun{}, response.failure()
		}
	}
	normalized.AgentID = agentID
	files := mergeOrchestrationFiles(run.Files, orchestrationFileMeta(normalized.Files))

	events, err := s.store.ListOrchestrationEvents(ctx, run.ID, 10000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration context")
		return store.OrchestrationRun{}, response.failure()
	}
	contextSummary := compactOrchestrationContext(run, events)
	var claimed bool
	if submissionID != "" {
		claimed, err = s.store.ClaimAutomationRunForContinue(ctx, run.ID, uid, submissionID)
	} else {
		claimed, err = s.store.ClaimOrchestrationRunForContinue(ctx, run.ID)
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to claim orchestration run")
		return store.OrchestrationRun{}, response.failure()
	}
	if !claimed {
		serverutil.WriteError(w, http.StatusConflict, "RUN_ACTIVE", "orchestration run is still active")
		return store.OrchestrationRun{}, response.failure()
	}
	if err := s.store.UpdateOrchestrationRunSettings(ctx, run.ID, agentID, normalized.Mode, normalized.WorkerPair, normalized.FirstCLI, normalized.Profile, normalized.VerificationMode, normalized.CWD, normalized.NativeContextCompaction, normalized.MaxTurns, files); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration run")
		return store.OrchestrationRun{}, response.failure()
	}
	if req.WorkerProfilePresetIDs != nil {
		if err := s.store.SetOrchestrationWorkerProfiles(ctx, run.ID, normalized.WorkerProfiles); err != nil {
			serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration worker profiles")
			return store.OrchestrationRun{}, response.failure()
		}
	}
	run.AgentID = agentID
	run.Mode = normalized.Mode
	run.WorkerPair = normalized.WorkerPair
	run.FirstCLI = normalized.FirstCLI
	run.Profile = normalized.Profile
	run.VerificationMode = normalized.VerificationMode
	run.NativeContextCompaction = normalized.NativeContextCompaction
	run.CWD = normalized.CWD
	run.MaxTurns = normalized.MaxTurns
	run.Files = files
	if err := s.startOrchestration(ctx, run, normalized, []string{contextSummary}, true); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return store.OrchestrationRun{}, response.failure()
	}
	run.Status = store.OrchestrationRunning
	run.ArchivedAt = 0
	run.Error = ""
	run.FinishedAt = 0
	return run, nil
}
