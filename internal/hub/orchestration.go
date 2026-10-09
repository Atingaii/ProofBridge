package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/modelcatalog"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
	"github.com/tencent/codex-bridge/internal/usagepricing"
)

type orchestrationCreateRequest struct {
	ExecutionMode                string                       `json:"executionMode"`
	NativeUserInputClientVersion int                          `json:"nativeUserInputClientVersion"`
	AgentID                      string                       `json:"agentId"`
	Title                        string                       `json:"title"`
	Mode                         string                       `json:"mode"`
	WorkerPair                   string                       `json:"workerPair"`
	FirstCLI                     string                       `json:"firstCli"`
	Profile                      string                       `json:"profile"`
	VerificationMode             string                       `json:"verificationMode"`
	NativeContextCompaction      string                       `json:"nativeContextCompaction"`
	Prompt                       string                       `json:"prompt"`
	CWD                          string                       `json:"cwd"`
	MaxTurns                     int                          `json:"maxTurns"`
	Files                        []protocol.AttachmentPayload `json:"files"`
	WorkerProfilePresetIDs       map[string]string            `json:"workerProfilePresetIds"`
	WorkerProfileEfforts         map[string]string            `json:"workerProfileEfforts"`
	WorkerProfileModels          map[string]string            `json:"workerProfileModels"`
}

const officialLoginPresetID = "__official__"

type orchestrationStartRequest struct {
	ExecutionMode                string
	NativeUserInputClientVersion int
	AgentID                      string
	Title                        string
	Mode                         string
	WorkerPair                   string
	FirstCLI                     string
	Profile                      string
	VerificationMode             string
	NativeContextCompaction      string
	Prompt                       string
	CWD                          string
	MaxTurns                     int
	MaxTurnsRequested            int
	Files                        []protocol.AttachmentPayload
	PlanWorkspace                bool
	WorkerProfiles               map[string]protocol.WorkerProfileBinding
	WorkerProfileEfforts         map[string]string
}

const orchestrationCancelAckTimeout = 5 * time.Second

func (s *Server) handleCreateOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	var req orchestrationCreateRequest
	maxBytes := s.cfg.Hub.MaxPromptBytes + s.cfg.Hub.MaxAttachmentBytes*16 + 128*1024
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orchestration payload")
		return
	}
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
	if user, err := s.store.UserByID(r.Context(), uid); err == nil {
		startReq.PlanWorkspace = s.featureEnabled(rollout.FeatureOrchestrationPlanWorkspace, user)
	}
	normalized, ok := s.normalizeOrchestrationStart(w, startReq)
	if !ok {
		return
	}
	agentID, err := s.resolveAgentID(r.Context(), uid, normalized.AgentID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusBadRequest
		}
		serverutil.WriteError(w, status, "BAD_AGENT", err.Error())
		return
	}
	if normalized.ExecutionMode == "dynamic" && !s.durableTaskGraphEnabled(agentID) {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", "dynamic orchestration requires a durable-task-graph Bridge")
		return
	}
	if err := s.validateOrchestrationCapabilities(agentID, normalized.WorkerPair); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
		return
	}
	if err := s.validateVerificationCapability(agentID, normalized.Profile, normalized.VerificationMode); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "VERIFICATION_CAPABILITY_UNAVAILABLE", err.Error())
		return
	}
	if len(req.WorkerProfilePresetIDs) > 0 {
		if err := s.validateIsolatedWorkerProfileCapability(agentID); err != nil {
			serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
			return
		}
	}

	if !s.authorizeOfficialWorkerRequest(w, r, uid, req) {
		return
	}
	profiles, err := s.resolveWorkerProfiles(r.Context(), uid, agentID, normalized.WorkerPair, req.WorkerProfilePresetIDs, req.WorkerProfileEfforts, req.WorkerProfileModels)
	if err != nil {
		s.writeWorkerProfileError(w, err)
		return
	}
	normalized.WorkerProfiles = profiles
	files := orchestrationFileMeta(normalized.Files)
	run, err := s.store.CreateOrchestrationRun(r.Context(), store.CreateOrchestrationRunParams{
		ExecutionMode: normalized.ExecutionMode, UserID: uid,
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
		return
	}
	if err := s.startOrchestration(r.Context(), run, normalized, nil, false); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return
	}
	run.Status = store.OrchestrationRunning
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{"run": run})
}

func (s *Server) handleContinueOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	s.orchestrationInputMu.Lock()
	defer s.orchestrationInputMu.Unlock()
	runID := r.PathValue("runID")
	run, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	if orchestrationTerminalStatus(run.Status) && s.handleTerminalOrchestrationMessage(w, r, run) {
		return
	}
	if !orchestrationTerminalStatus(run.Status) {
		s.handleActiveOrchestrationMessage(w, r, run)
		return
	}
	var req orchestrationCreateRequest
	maxBytes := s.cfg.Hub.MaxPromptBytes + s.cfg.Hub.MaxAttachmentBytes*16 + 128*1024
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orchestration payload")
		return
	}
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
		return
	}
	startReq.ExecutionMode = run.ExecutionMode
	previousProfiles, profilesErr := s.store.OrchestrationWorkerProfiles(r.Context(), run.ID)
	if profilesErr != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration worker profiles")
		return
	}
	if graph, graphErr := s.store.TaskGraphByRun(r.Context(), run.ID); graphErr == nil {
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
		return
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
		return
	}
	agentID, err := s.resolveAgentID(r.Context(), uid, normalized.AgentID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusBadRequest
		}
		serverutil.WriteError(w, status, "BAD_AGENT", err.Error())
		return
	}
	if normalized.ExecutionMode == "dynamic" && !s.durableTaskGraphEnabled(agentID) {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", "dynamic orchestration requires a durable-task-graph Bridge")
		return
	}
	if err := s.validateOrchestrationCapabilities(agentID, normalized.WorkerPair); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
		return
	}
	if err := s.validateVerificationCapability(agentID, normalized.Profile, normalized.VerificationMode); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "VERIFICATION_CAPABILITY_UNAVAILABLE", err.Error())
		return
	}
	if !s.authorizeOfficialWorkerRequest(w, r, uid, req) {
		return
	}
	if req.WorkerProfilePresetIDs == nil {
		normalized.WorkerProfiles = previousProfiles
	} else {
		profiles, err := s.resolveWorkerProfiles(r.Context(), uid, agentID, normalized.WorkerPair, req.WorkerProfilePresetIDs, req.WorkerProfileEfforts, req.WorkerProfileModels)
		if err != nil {
			s.writeWorkerProfileError(w, err)
			return
		}
		normalized.WorkerProfiles = profiles
	}
	if len(normalized.WorkerProfiles) > 0 {
		if err := s.validateIsolatedWorkerProfileCapability(agentID); err != nil {
			serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
			return
		}
	}
	if hasExplicitOfficialWorkerParameters(normalized.WorkerProfiles) {
		if err := s.validateOfficialWorkerCapability(agentID); err != nil {
			s.writeWorkerProfileError(w, err)
			return
		}
	}
	normalized.AgentID = agentID
	files := mergeOrchestrationFiles(run.Files, orchestrationFileMeta(normalized.Files))

	events, err := s.store.ListOrchestrationEvents(r.Context(), run.ID, 10000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration context")
		return
	}
	contextSummary := compactOrchestrationContext(run, events)
	claimed, err := s.store.ClaimOrchestrationRunForContinue(r.Context(), run.ID)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to claim orchestration run")
		return
	}
	if !claimed {
		serverutil.WriteError(w, http.StatusConflict, "RUN_ACTIVE", "orchestration run is still active")
		return
	}
	if err := s.store.UpdateOrchestrationRunSettings(r.Context(), run.ID, agentID, normalized.Mode, normalized.WorkerPair, normalized.FirstCLI, normalized.Profile, normalized.VerificationMode, normalized.CWD, normalized.NativeContextCompaction, normalized.MaxTurns, files); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration run")
		return
	}
	if req.WorkerProfilePresetIDs != nil {
		if err := s.store.SetOrchestrationWorkerProfiles(r.Context(), run.ID, normalized.WorkerProfiles); err != nil {
			serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration worker profiles")
			return
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
	if err := s.startOrchestration(r.Context(), run, normalized, []string{contextSummary}, true); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return
	}
	run.Status = store.OrchestrationRunning
	run.ArchivedAt = 0
	run.Error = ""
	run.FinishedAt = 0
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}

// resolveWorkerProfiles authorizes the encrypted preset snapshot for only the
// worker slots used by this run. Hub deliberately does not decrypt it.
func (s *Server) resolveWorkerProfiles(ctx context.Context, userID, agentID, workerPair string, requested map[string]string, requestedEfforts map[string]string, requestedModels map[string]string) (map[string]protocol.WorkerProfileBinding, error) {
	if len(requested) == 0 {
		if len(requestedModels) > 0 {
			return nil, errors.New("official model overrides require selected worker profiles")
		}
		return nil, nil
	}
	expected := map[string]string{"claude": "claude", "codex": "codex"}
	switch protocol.NormalizeOrchestrationWorkerPair(workerPair) {
	case protocol.WorkerPairCodexCodex:
		expected = map[string]string{"codex-a": "codex", "codex-b": "codex"}
	case protocol.WorkerPairClaudeClaude:
		expected = map[string]string{"claude-a": "claude", "claude-b": "claude"}
	}
	selectedPresets := make(map[string]string, len(requested))
	for rawSlot, rawPresetID := range requested {
		slot := strings.TrimSpace(rawSlot)
		presetID := strings.TrimSpace(rawPresetID)
		if slot == "" || presetID == "" {
			return nil, errors.New("worker profile slot and preset id are required")
		}
		if _, ok := expected[slot]; !ok {
			return nil, fmt.Errorf("worker slot %q is not available for %s", slot, protocol.NormalizeOrchestrationWorkerPair(workerPair))
		}
		if _, exists := selectedPresets[slot]; exists {
			return nil, fmt.Errorf("worker slot %q has more than one saved preset", slot)
		}
		selectedPresets[slot] = presetID
	}
	for slot := range expected {
		if selectedPresets[slot] == "" {
			return nil, fmt.Errorf("worker slot %q requires a saved preset", slot)
		}
	}
	selectedEfforts := make(map[string]string, len(requestedEfforts))
	for rawSlot, rawEffort := range requestedEfforts {
		slot := strings.TrimSpace(rawSlot)
		effort := strings.TrimSpace(rawEffort)
		if effort == "" {
			continue
		}
		if _, ok := expected[slot]; !ok {
			return nil, fmt.Errorf("reasoning effort slot %q is not available for %s", slot, protocol.NormalizeOrchestrationWorkerPair(workerPair))
		}
		if _, exists := selectedEfforts[slot]; exists {
			return nil, fmt.Errorf("worker slot %q has more than one reasoning effort", slot)
		}
		selectedEfforts[slot] = effort
	}
	selectedModels, err := normalizeOfficialWorkerModels(requestedModels, selectedPresets)
	if err != nil {
		return nil, err
	}
	needsOfficialCatalog := len(selectedModels) > 0
	for slot, effort := range selectedEfforts {
		if selectedPresets[slot] != officialLoginPresetID {
			continue
		}
		if !officialCodexWorkerSlot(slot) || selectedModels[slot] == "" {
			return nil, fmt.Errorf("worker slot %q requires an explicit official Codex model before selecting reasoning effort %q", slot, effort)
		}
		needsOfficialCatalog = true
	}
	var officialModels map[string]protocol.OfficialCLIModelMetadata
	if needsOfficialCatalog {
		models, err := s.loadOfficialCLIModels(ctx, agentID)
		if err != nil {
			return nil, err
		}
		officialModels = make(map[string]protocol.OfficialCLIModelMetadata, len(models))
		for _, model := range models {
			officialModels[model.Model] = model
		}
	}
	connection, online := s.pool.AgentConnectionInfo(agentID)
	if !online || connection.Capabilities == nil || connection.Capabilities.ConfigSwitcher == nil {
		return nil, errors.New("selected CLI endpoint does not support encrypted model profiles")
	}
	keyID := connection.Capabilities.ConfigSwitcher.KeyID
	profiles := make(map[string]protocol.WorkerProfileBinding, len(selectedPresets))
	for slot, presetID := range selectedPresets {
		wantCLI := expected[slot]
		if presetID == officialLoginPresetID {
			binding := protocol.WorkerProfileBinding{PresetID: officialLoginPresetID, CLI: wantCLI, Name: "Official login", OfficialLogin: true}
			if modelID := selectedModels[slot]; modelID != "" {
				metadata, ok := officialModels[modelID]
				if !ok {
					return nil, fmt.Errorf("worker slot %q selects unknown official Codex model %q", slot, modelID)
				}
				effort := selectedEfforts[slot]
				if effort == "" {
					effort = metadata.DefaultReasoningEffort
				}
				if effort != "" && !containsString(metadata.SupportedReasoningEfforts, effort) {
					return nil, fmt.Errorf("worker slot %q selects unsupported reasoning effort %q for official model %q", slot, effort, modelID)
				}
				binding.Model = modelID
				binding.ReasoningEffort = effort
				binding.ReasoningLevels = append([]string(nil), metadata.SupportedReasoningEfforts...)
				binding.ReasoningDefault = metadata.DefaultReasoningEffort
			}
			profiles[slot] = binding
			continue
		}
		preset, err := s.store.CLIConfigPresetByID(ctx, presetID, userID, agentID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("model preset %q was not found for the selected endpoint", presetID)
		}
		if err != nil {
			return nil, fmt.Errorf("load model preset: %w", err)
		}
		if preset.CLI != wantCLI {
			return nil, fmt.Errorf("worker slot %q requires a %s preset", slot, wantCLI)
		}
		if err := s.materializeCLIConfigPresetCredential(ctx, userID, agentID, &preset); err != nil {
			return nil, fmt.Errorf("prepare model preset %q for the selected endpoint: %w", preset.Name, err)
		}
		normalizeReviewedPreset(&preset)
		if preset.KeyHint == "" || preset.KeyHint != keyID {
			return nil, fmt.Errorf("model preset %q was encrypted for an earlier Bridge key; update its API key first", preset.Name)
		}
		effort := preset.ReasoningEffort
		if selectedEfforts != nil {
			if selected := selectedEfforts[slot]; selected != "" {
				if len(preset.ReasoningLevels) == 0 || !containsString(preset.ReasoningLevels, selected) {
					return nil, fmt.Errorf("worker slot %q selects unsupported reasoning effort %q for preset %q", slot, selected, preset.Name)
				}
				effort = selected
			}
		}
		binding := protocol.WorkerProfileBinding{
			PresetID: preset.ID, CLI: preset.CLI, Name: preset.Name, BaseURL: preset.BaseURL, Model: preset.Model, ReasoningEffort: effort, ReasoningLevels: append([]string(nil), preset.ReasoningLevels...), ReasoningDefault: preset.ReasoningDefault, Secret: preset.Secret,
		}
		if preset.CLI == "claude" {
			if contextProfile, ok := reviewedClaudeContextForModel(preset.Model); ok {
				binding.ClaudeContextWindow = contextProfile.Window
				binding.ClaudeDisableUnknownModelWindowEnforcement = contextProfile.DisableUnknownEnforcement
			}
		}
		profiles[slot] = binding
	}
	return profiles, nil
}

func (s *Server) normalizeOrchestrationStart(w http.ResponseWriter, req orchestrationStartRequest) (orchestrationStartRequest, bool) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "prompt is required")
		return req, false
	}
	if int64(len(req.Prompt)) > s.cfg.Hub.MaxPromptBytes {
		serverutil.WriteError(w, http.StatusBadRequest, "PROMPT_TOO_LARGE", "prompt is too large")
		return req, false
	}
	if req.Mode == "" {
		req.Mode = "collaboration"
	}
	if req.Mode != "collaboration" && req.Mode != "debate" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_MODE", "mode must be collaboration or debate")
		return req, false
	}
	req.WorkerPair = protocol.NormalizeOrchestrationWorkerPair(req.WorkerPair)
	req.FirstCLI = normalizeOrchestrationFirstCLI(req.FirstCLI)
	if req.WorkerPair == protocol.WorkerPairCodexCodex {
		req.FirstCLI = "codex"
	} else if req.WorkerPair == protocol.WorkerPairClaudeClaude {
		req.FirstCLI = "claude"
	}
	req.Profile = normalizeOrchestrationProfile(req.Profile)
	req.VerificationMode = protocol.NormalizeVerificationMode(req.VerificationMode)
	if req.VerificationMode == protocol.VerificationModeTBCI && req.Profile != "formal-proof" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_VERIFICATION_MODE", "TBCI verification requires the formal-proof profile")
		return req, false
	}
	req.ExecutionMode = strings.TrimSpace(req.ExecutionMode)
	if req.ExecutionMode == "" {
		req.ExecutionMode = "bounded"
	}
	if req.ExecutionMode != "bounded" && req.ExecutionMode != "dynamic" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_EXECUTION_MODE", "executionMode must be bounded or dynamic")
		return req, false
	}
	req.NativeContextCompaction = protocol.NormalizeNativeContextCompaction(req.NativeContextCompaction)
	if req.MaxTurns <= 0 {
		req.MaxTurns = 2
	}
	req.MaxTurnsRequested = req.MaxTurns
	if req.MaxTurns > 12 {
		req.MaxTurns = 12
	}
	if req.ExecutionMode == "dynamic" {
		req.MaxTurns = 0
		req.MaxTurnsRequested = 0
	}
	if err := s.validateOrchestrationFiles(req.Files); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_FILE", err.Error())
		return req, false
	}
	return req, true
}

type orchestrationUsageStats struct {
	WorkerSlot       string  `json:"workerSlot,omitempty"`
	CLI              string  `json:"cli"`
	Model            string  `json:"model"`
	ModelSource      string  `json:"modelSource,omitempty"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
	Estimated        bool    `json:"estimated"`
	Native           bool    `json:"native"`
	CostKnown        bool    `json:"costKnown"`
	CostSource       string  `json:"costSource"`
	PricingModel     string  `json:"pricingModel,omitempty"`
	ReasoningTokens  int64   `json:"reasoningTokens,omitempty"`
	CallCount        int     `json:"callCount,omitempty"`
}

type orchestrationRoundStats struct {
	ByWorker         []orchestrationUsageStats `json:"byWorker"`
	Round            int                       `json:"round"`
	InputTokens      int64                     `json:"inputTokens"`
	OutputTokens     int64                     `json:"outputTokens"`
	CacheTokens      int64                     `json:"cacheTokens"`
	TotalTokens      int64                     `json:"totalTokens"`
	EstimatedCostUSD float64                   `json:"estimatedCostUsd"`
	CallCount        int                       `json:"callCount"`
}

type orchestrationRunStats struct {
	ModelRecoveryStatus string                    `json:"modelRecoveryStatus,omitempty"`
	CatalogUpdatedAt    int64                     `json:"catalogUpdatedAt,omitempty"`
	ByWorker            []orchestrationUsageStats `json:"byWorker"`
	RunID               string                    `json:"runId"`
	StartedAt           int64                     `json:"startedAt,omitempty"`
	FinishedAt          int64                     `json:"finishedAt,omitempty"`
	RuntimeSeconds      int64                     `json:"runtimeSeconds"`
	InputTokens         int64                     `json:"inputTokens"`
	OutputTokens        int64                     `json:"outputTokens"`
	CacheReadTokens     int64                     `json:"cacheReadTokens"`
	CacheWriteTokens    int64                     `json:"cacheWriteTokens"`
	CacheTokens         int64                     `json:"cacheTokens"`
	TotalTokens         int64                     `json:"totalTokens"`
	EstimatedCostUSD    float64                   `json:"estimatedCostUsd"`
	Estimated           bool                      `json:"estimated"`
	Native              bool                      `json:"native"`
	CostKnown           bool                      `json:"costKnown"`
	CostSource          string                    `json:"costSource"`
	PricingModels       []string                  `json:"pricingModels,omitempty"`
	ByCLI               []orchestrationUsageStats `json:"byCli"`
	ReasoningTokens     int64                     `json:"reasoningTokens,omitempty"`
	CallCount           int                       `json:"callCount,omitempty"`
	AccountingStatus    string                    `json:"accountingStatus"`
	AccountingSource    string                    `json:"accountingSource"`
	ScannedAt           int64                     `json:"scannedAt,omitempty"`
	Rounds              []orchestrationRoundStats `json:"rounds,omitempty"`
	TaskNumber          int                       `json:"taskNumber,omitempty"`
	PromptSeq           int64                     `json:"promptSeq,omitempty"`
	Prompt              string                    `json:"prompt,omitempty"`
	Tasks               []orchestrationRunStats   `json:"tasks,omitempty"`
}

type orchestrationUsageOverviewItem struct {
	RunID            string                    `json:"runId"`
	Title            string                    `json:"title"`
	MachineID        string                    `json:"machineId"`
	MachineName      string                    `json:"machineName"`
	Hostname         string                    `json:"hostname"`
	CreatedAt        int64                     `json:"createdAt"`
	Status           string                    `json:"status"`
	InputTokens      int64                     `json:"inputTokens"`
	OutputTokens     int64                     `json:"outputTokens"`
	CacheTokens      int64                     `json:"cacheTokens"`
	TotalTokens      int64                     `json:"totalTokens"`
	EstimatedCostUSD float64                   `json:"estimatedCostUsd"`
	CallCount        int                       `json:"callCount"`
	CostKnown        bool                      `json:"costKnown"`
	ByCLI            []orchestrationUsageStats `json:"byCli"`
}

type orchestrationUsageTrendPoint struct {
	Date             string  `json:"date"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheTokens      int64   `json:"cacheTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
	CallCount        int     `json:"callCount"`
	CostKnown        bool    `json:"costKnown"`
}

type orchestrationUsageOverview struct {
	InputTokens      int64                            `json:"inputTokens"`
	OutputTokens     int64                            `json:"outputTokens"`
	CacheTokens      int64                            `json:"cacheTokens"`
	TotalTokens      int64                            `json:"totalTokens"`
	EstimatedCostUSD float64                          `json:"estimatedCostUsd"`
	CallCount        int                              `json:"callCount"`
	Runs             int                              `json:"runs"`
	Machines         int                              `json:"machines"`
	Days             int                              `json:"days"`
	TimezoneOffset   int                              `json:"timezoneOffset"`
	CostKnown        bool                             `json:"costKnown"`
	Trend            []orchestrationUsageTrendPoint   `json:"trend"`
	Items            []orchestrationUsageOverviewItem `json:"items"`
}

func addDerivedTokenTotals(stats *orchestrationRunStats) {
	stats.CacheTokens = stats.CacheReadTokens + stats.CacheWriteTokens
	stats.TotalTokens = stats.InputTokens + stats.OutputTokens + stats.CacheTokens
}

func usageRoundNumber(data map[string]any) int {
	if data == nil {
		return 0
	}
	for _, key := range []string{"round", "ordinal"} {
		switch value := data[key].(type) {
		case float64:
			if value > 0 {
				return int(value)
			}
		case int:
			if value > 0 {
				return value
			}
		case int64:
			if value > 0 {
				return int(value)
			}
		}
	}
	return 0
}

type orchestrationRoundBoundary struct {
	round int
	at    int64
}

func orchestrationRoundBoundaries(events []store.OrchestrationEvent) []orchestrationRoundBoundary {
	boundaries := map[int]int64{1: 0}
	for _, event := range events {
		round := usageRoundNumber(event.Data)
		if event.Task != nil && event.Task.Round > 0 {
			round = event.Task.Round
		}
		if event.TurnStartData != nil && event.TurnStartData.Round > 0 {
			round = event.TurnStartData.Round
		}
		if event.RunStartData != nil && event.RunStartData.Round > 0 {
			round = event.RunStartData.Round
		}
		if round <= 0 || event.CreatedAt <= 0 {
			continue
		}
		if previous, ok := boundaries[round]; !ok || event.CreatedAt < previous {
			boundaries[round] = event.CreatedAt
		}
	}
	out := make([]orchestrationRoundBoundary, 0, len(boundaries))
	for round, at := range boundaries {
		out = append(out, orchestrationRoundBoundary{round: round, at: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].round < out[j].round })
	return out
}

func orchestrationRoundForTime(boundaries []orchestrationRoundBoundary, at int64) int {
	round := 1
	for _, boundary := range boundaries {
		if boundary.at <= at {
			round = boundary.round
		}
	}
	return round
}

func appendRoundUsage(rounds map[int]*orchestrationRoundStats, round int, input, output, cacheRead, cacheWrite int64, calls int) {
	if round <= 0 {
		round = 1
	}
	item := rounds[round]
	if item == nil {
		item = &orchestrationRoundStats{Round: round, ByWorker: []orchestrationUsageStats{}}
		rounds[round] = item
	}
	item.InputTokens += input
	item.OutputTokens += output
	item.CacheTokens += cacheRead + cacheWrite
	item.TotalTokens += input + output + cacheRead + cacheWrite
	item.CallCount += calls
}

func initializeOrchestrationRounds(rounds map[int]*orchestrationRoundStats, boundaries []orchestrationRoundBoundary) {
	for _, boundary := range boundaries {
		if boundary.round > 0 && rounds[boundary.round] == nil {
			rounds[boundary.round] = &orchestrationRoundStats{Round: boundary.round, ByWorker: []orchestrationUsageStats{}}
		}
	}
}

func finalizeOrchestrationRounds(stats *orchestrationRunStats, rounds map[int]*orchestrationRoundStats) {
	if len(rounds) == 0 {
		return
	}
	keys := make([]int, 0, len(rounds))
	for round := range rounds {
		keys = append(keys, round)
	}
	sort.Ints(keys)
	for _, round := range keys {
		stats.Rounds = append(stats.Rounds, *rounds[round])
	}
}

func (s *Server) handleOrchestrationStats(w http.ResponseWriter, r *http.Request, uid string) {
	ctx, cancel := context.WithTimeout(r.Context(), orchestrationStatsTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		writeUsageReadError(w, r, "failed to load orchestration run")
		return
	}
	events, err := s.store.ListOrchestrationUsageTimeline(r.Context(), run.ID)
	if err != nil {
		writeUsageReadError(w, r, "failed to load orchestration statistics")
		return
	}
	ledger, err := s.store.ListOrchestrationUsageEvents(r.Context(), run.ID)
	if err != nil {
		writeUsageReadError(w, r, "failed to load usage ledger")
		return
	}
	syncs, err := s.store.ListOrchestrationUsageSyncs(r.Context(), run.ID)
	if err != nil {
		writeUsageReadError(w, r, "failed to load usage sync state")
		return
	}

	graphs, graphErr := s.store.ListTaskGraphMetadataByRun(r.Context(), run.ID)
	if graphErr != nil {
		writeUsageReadError(w, r, "failed to load orchestration task boundaries")
		return
	}
	events = enrichVerifierUsage(enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(events, graphs), ledger), graphs)
	hints := planningUsageRecoveryHints(events, graphs)
	now := time.Now().Unix()
	if orchestrationTerminalStatus(run.Status) && (historicalUsageSyncDue(syncs, now) || (s.supportsModelRecovery(run.AgentID) && modelRecoveryDue(hints, syncs, now))) {
		_ = s.requestOrchestrationUsageSync(r.Context(), run, events)
	}
	stats := buildOrchestrationRunStatsWithLedger(run, events, ledger, syncs)
	stats.Tasks = buildOrchestrationTaskStats(run, events, ledger, syncs, graphs)
	stats.ModelRecoveryStatus = s.modelRecoveryStatus(run.AgentID, hints, syncs, now)
	stats.CatalogUpdatedAt = modelcatalog.Default.UpdatedAt()
	if r.Context().Err() != nil {
		writeUsageReadError(w, r, "statistics request interrupted")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"stats": stats})
}

func (s *Server) handleUsageOverview(w http.ResponseWriter, r *http.Request, uid string) {
	ctx, cancel := context.WithTimeout(r.Context(), orchestrationStatsTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	days, timezoneOffset, cutoff, err := usageOverviewRange(r)
	if err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_RANGE", err.Error())
		return
	}
	pageRequest, err := parseUsageOverviewPage(r, days, timezoneOffset, cutoff)
	if err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_QUERY", err.Error())
		return
	}
	var runs []store.OrchestrationRun
	var page *usageOverviewPage
	if pageRequest == nil {
		runs, err = s.store.ListAllOrchestrationRuns(r.Context(), uid)
	} else {
		var history store.OrchestrationHistoryPage
		history, err = s.store.ListOrchestrationHistory(r.Context(), uid, store.OrchestrationHistoryFilter{
			Archived: "all", Limit: pageRequest.Limit, Cursor: pageRequest.History,
		})
		runs = history.Runs
		page = pageRequest.response(history.NextCursor, len(runs))
		cutoff = pageRequest.Cutoff
	}
	if err != nil {
		if errors.Is(err, store.ErrInvalidOrchestrationHistory) {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_QUERY", "invalid usage overview cursor")
			return
		}
		writeUsageReadError(w, r, "failed to load usage overview")
		return
	}
	overview := orchestrationUsageOverview{Days: days, TimezoneOffset: timezoneOffset, CostKnown: true, Trend: []orchestrationUsageTrendPoint{}, Items: []orchestrationUsageOverviewItem{}}
	machineSet := map[string]bool{}
	trend := map[string]*orchestrationUsageTrendPoint{}
	// Runs often share an endpoint. Keep this cache request-local so ownership,
	// deletion and endpoint metadata are refreshed on every overview request.
	agents := map[string]*store.Agent{}
	for _, run := range runs {
		events, eventErr := s.store.ListOrchestrationUsageTimeline(r.Context(), run.ID)
		if eventErr != nil {
			writeUsageReadError(w, r, "failed to load usage timeline")
			return
		}
		ledger, ledgerErr := s.store.ListOrchestrationUsageEvents(r.Context(), run.ID)
		if ledgerErr != nil {
			writeUsageReadError(w, r, "failed to load usage ledger")
			return
		}
		syncs, syncErr := s.store.ListOrchestrationUsageSyncs(r.Context(), run.ID)
		if syncErr != nil {
			writeUsageReadError(w, r, "failed to load usage sync state")
			return
		}
		graphs, graphErr := s.store.ListTaskGraphMetadataByRun(r.Context(), run.ID)
		if graphErr != nil {
			writeUsageReadError(w, r, "failed to load orchestration task boundaries")
			return
		}
		events = enrichVerifierUsage(enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(events, graphs), ledger), graphs)
		filteredEvents := filterOverviewTimeline(events, cutoff)
		filteredLedger := filterOverviewLedger(run, ledger, cutoff)
		stats := buildOrchestrationRunStatsWithLedger(run, filteredEvents, filteredLedger, syncs)
		if stats.TotalTokens == 0 && stats.CallCount == 0 {
			continue
		}
		appendOverviewTrend(trend, run, filteredEvents, filteredLedger, syncs, timezoneOffset)
		agent, loaded := agents[run.AgentID]
		if !loaded {
			endpoint, agentErr := s.store.AgentByIDForUser(r.Context(), run.AgentID, uid, false)
			if agentErr != nil && !errors.Is(agentErr, store.ErrNotFound) {
				writeUsageReadError(w, r, "failed to load usage endpoint")
				return
			}
			if agentErr == nil {
				agent = &endpoint
			}
			agents[run.AgentID] = agent
		}
		machineID, machineName, hostname := run.AgentID, run.AgentID, ""
		if agent != nil {
			machineID, machineName, hostname = agent.MachineID, agent.Name, agent.Hostname
		}
		machineSet[machineID] = true
		overview.Runs++
		overview.InputTokens += stats.InputTokens
		overview.OutputTokens += stats.OutputTokens
		overview.CacheTokens += stats.CacheTokens
		overview.TotalTokens += stats.TotalTokens
		overview.EstimatedCostUSD += stats.EstimatedCostUSD
		overview.CallCount += stats.CallCount
		overview.CostKnown = overview.CostKnown && stats.CostKnown
		overview.Items = append(overview.Items, orchestrationUsageOverviewItem{
			RunID: run.ID, Title: run.Title, MachineID: machineID, MachineName: machineName, Hostname: hostname,
			CreatedAt: run.CreatedAt, Status: run.Status, InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens,
			CacheTokens: stats.CacheTokens, TotalTokens: stats.TotalTokens, EstimatedCostUSD: stats.EstimatedCostUSD,
			CallCount: stats.CallCount, CostKnown: stats.CostKnown, ByCLI: stats.ByCLI,
		})
	}
	overview.Machines = len(machineSet)
	for _, point := range trend {
		overview.Trend = append(overview.Trend, *point)
	}
	sort.Slice(overview.Trend, func(i, j int) bool { return overview.Trend[i].Date < overview.Trend[j].Date })
	if overview.Runs == 0 {
		overview.CostKnown = false
	}
	if r.Context().Err() != nil {
		writeUsageReadError(w, r, "statistics request interrupted")
		return
	}
	response := map[string]any{"overview": overview}
	if page != nil {
		response["page"] = page
	}
	serverutil.WriteJSON(w, http.StatusOK, response)
}

func usageOverviewRange(r *http.Request) (days, timezoneOffset int, cutoff int64, err error) {
	days = 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || (parsed != 0 && parsed != 7 && parsed != 30 && parsed != 90) {
			return 0, 0, 0, errors.New("days must be one of 0, 7, 30, or 90")
		}
		days = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("timezoneOffset")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < -840 || parsed > 840 {
			return 0, 0, 0, errors.New("timezoneOffset must be between -840 and 840")
		}
		timezoneOffset = parsed
	}
	if days == 0 {
		return days, timezoneOffset, 0, nil
	}
	now := time.Now().Add(-time.Duration(timezoneOffset) * time.Minute)
	localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	cutoff = localMidnight.Add(-time.Duration(days-1)*24*time.Hour + time.Duration(timezoneOffset)*time.Minute).Unix()
	return days, timezoneOffset, cutoff, nil
}

func filterOverviewTimeline(events []store.OrchestrationEvent, cutoff int64) []store.OrchestrationEvent {
	if cutoff == 0 {
		return events
	}
	filtered := make([]store.OrchestrationEvent, 0, len(events))
	for _, event := range events {
		if event.Kind != "turn.usage" || event.CreatedAt >= cutoff {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func filterOverviewLedger(run store.OrchestrationRun, events []protocol.OrchestrationUsageEvent, cutoff int64) []protocol.OrchestrationUsageEvent {
	if cutoff == 0 {
		return events
	}
	filtered := make([]protocol.OrchestrationUsageEvent, 0, len(events))
	for _, event := range events {
		at := event.OccurredAt
		if at == 0 {
			at = run.CreatedAt
		}
		if at >= cutoff {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func overviewDate(at int64, timezoneOffset int) string {
	return time.Unix(at, 0).Add(-time.Duration(timezoneOffset) * time.Minute).UTC().Format("2006-01-02")
}

func overviewTrendPoint(trend map[string]*orchestrationUsageTrendPoint, date string) *orchestrationUsageTrendPoint {
	point := trend[date]
	if point == nil {
		point = &orchestrationUsageTrendPoint{Date: date, CostKnown: true}
		trend[date] = point
	}
	return point
}

func appendOverviewTrend(trend map[string]*orchestrationUsageTrendPoint, run store.OrchestrationRun, timeline []store.OrchestrationEvent, ledger []protocol.OrchestrationUsageEvent, syncs []store.OrchestrationUsageSync, timezoneOffset int) {
	if shouldUseOrchestrationUsageLedger(timeline, ledger, syncs) {
		for _, event := range ledger {
			at := event.OccurredAt
			if at == 0 {
				at = run.CreatedAt
			}
			point := overviewTrendPoint(trend, overviewDate(at, timezoneOffset))
			point.InputTokens += event.InputTokens
			point.OutputTokens += event.OutputTokens
			point.CacheTokens += event.CacheReadTokens + event.CacheWriteTokens
			point.TotalTokens += event.InputTokens + event.OutputTokens + event.CacheReadTokens + event.CacheWriteTokens
			point.CallCount++
			if quote, ok := usagepricing.EstimateNormalized(event.CLI, event.Model, event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.CacheWriteTokens); ok {
				point.EstimatedCostUSD += quote.CostUSD
			} else {
				point.CostKnown = false
			}
		}
		return
	}
	for _, event := range timeline {
		if event.Kind != "turn.usage" || event.Data == nil {
			continue
		}
		usage := orchestrationSnapshotUsage(event)
		point := overviewTrendPoint(trend, overviewDate(event.CreatedAt, timezoneOffset))
		point.InputTokens += usage.InputTokens
		point.OutputTokens += usage.OutputTokens
		point.CacheTokens += usage.CacheReadTokens + usage.CacheWriteTokens
		point.TotalTokens += usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
		point.EstimatedCostUSD += usage.EstimatedCostUSD
		point.CallCount += snapshotUsageCallCount(usage)
		if !usage.CostKnown {
			point.CostKnown = false
		}
	}
}

func buildOrchestrationRunStats(run store.OrchestrationRun, events []store.OrchestrationEvent) orchestrationRunStats {
	return buildOrchestrationRunStatsWithLedger(run, events, nil, nil)
}

type orchestrationTaskBoundary struct {
	TaskNumber int
	PromptSeq  int64
	Prompt     string
	StartedAt  int64
	FinishedAt int64
}

func orchestrationTaskBoundaries(run store.OrchestrationRun, graphs []store.OrchestrationTaskGraph) []orchestrationTaskBoundary {
	boundaries := make([]orchestrationTaskBoundary, 0)
	var previousPayload protocol.OrchestrationStartPayload
	previousPayloadValid := false
	for _, graph := range graphs {
		var payload protocol.OrchestrationStartPayload
		payloadValid := json.Unmarshal([]byte(graph.PayloadJSON), &payload) == nil
		payload.Prompt = strings.TrimSpace(payload.Prompt)
		continuesPrevious := len(boundaries) > 0 && sameOrchestrationUserTask(previousPayload, previousPayloadValid, payload, payloadValid)
		if !continuesPrevious {
			boundaries = append(boundaries, orchestrationTaskBoundary{TaskNumber: len(boundaries) + 1, PromptSeq: payload.PromptSeq, Prompt: payload.Prompt, StartedAt: graph.CreatedAt})
		}
		boundaries[len(boundaries)-1].FinishedAt = graph.FinishedAt
		previousPayload = payload
		previousPayloadValid = payloadValid
	}
	if len(boundaries) == 0 {
		return []orchestrationTaskBoundary{{TaskNumber: 1, Prompt: run.Prompt, StartedAt: run.CreatedAt, FinishedAt: run.FinishedAt}}
	}
	for index := range boundaries {
		if index+1 < len(boundaries) {
			boundaries[index].FinishedAt = boundaries[index+1].StartedAt
		} else if orchestrationTerminalStatus(run.Status) && run.FinishedAt > 0 {
			boundaries[index].FinishedAt = run.FinishedAt
		} else {
			boundaries[index].FinishedAt = 0
		}
	}
	return boundaries
}

func buildOrchestrationTaskStats(run store.OrchestrationRun, timeline []store.OrchestrationEvent, ledger []protocol.OrchestrationUsageEvent, syncs []store.OrchestrationUsageSync, graphs []store.OrchestrationTaskGraph) []orchestrationRunStats {
	boundaries := orchestrationTaskBoundaries(run, graphs)
	tasks := make([]orchestrationRunStats, 0, len(boundaries))
	for index, boundary := range boundaries {
		end := boundary.FinishedAt
		contains := func(timestamp int64) bool {
			if timestamp < boundary.StartedAt {
				return false
			}
			if index+1 < len(boundaries) {
				return timestamp < boundaries[index+1].StartedAt
			}
			return end == 0 || timestamp <= end
		}
		filteredTimeline := make([]store.OrchestrationEvent, 0)
		for _, event := range timeline {
			if contains(event.CreatedAt) {
				filteredTimeline = append(filteredTimeline, event)
			}
		}
		filteredLedger := make([]protocol.OrchestrationUsageEvent, 0)
		for _, event := range ledger {
			if (event.OccurredAt <= 0 && index == 0) || (event.OccurredAt > 0 && contains(event.OccurredAt)) {
				filteredLedger = append(filteredLedger, event)
			}
		}
		taskRun := run
		taskRun.CreatedAt = boundary.StartedAt
		taskRun.FinishedAt = end
		if index+1 < len(boundaries) || end > 0 {
			taskRun.Status = store.OrchestrationCompleted
		}
		stats := buildOrchestrationRunStatsWithLedger(taskRun, filteredTimeline, filteredLedger, syncs)
		stats.TaskNumber = boundary.TaskNumber
		stats.PromptSeq = boundary.PromptSeq
		stats.Prompt = boundary.Prompt
		tasks = append(tasks, stats)
	}
	return tasks
}

func orchestrationStatsStartTime(run store.OrchestrationRun, events []store.OrchestrationEvent) int64 {
	// created_at is the stable beginning of the whole orchestration. Event
	// windows are capped, so relying only on the first visible run.start would
	// incorrectly measure just the last continuation round.
	startedAt := run.CreatedAt
	for _, event := range events {
		if event.Kind == "run.start" && (startedAt == 0 || event.CreatedAt < startedAt) {
			startedAt = event.CreatedAt
		}
	}
	return startedAt
}

func buildOrchestrationRunStatsWithLedger(run store.OrchestrationRun, events []store.OrchestrationEvent, ledger []protocol.OrchestrationUsageEvent, syncs []store.OrchestrationUsageSync) orchestrationRunStats {
	events = enrichOrchestrationUsageTimeline(events, nil)
	events = enrichVerifierUsage(enrichSnapshotModelsFromLedger(events, ledger), nil)
	// These collections are part of the browser contract. Keep them as [] rather
	// than null when a run has not produced usage yet.
	stats := orchestrationRunStats{RunID: run.ID, FinishedAt: run.FinishedAt, StartedAt: orchestrationStatsStartTime(run, events), ByCLI: []orchestrationUsageStats{}, ByWorker: []orchestrationUsageStats{}}
	if shouldUseOrchestrationUsageLedger(events, ledger, syncs) {
		return buildLedgerOrchestrationRunStats(run, events, ledger, syncs)
	}
	stats.AccountingStatus = "legacy-incomplete"
	stats.AccountingSource = "turn-snapshots"
	byCLI := map[string]*orchestrationUsageStats{}
	rounds := map[int]*orchestrationRoundStats{}
	boundaries := orchestrationRoundBoundaries(events)
	initializeOrchestrationRounds(rounds, boundaries)
	usageCount := 0
	for _, event := range events {
		if event.Kind != "turn.usage" || event.Data == nil {
			continue
		}
		usage := orchestrationSnapshotUsage(event)
		if usage.CLI == "" {
			usage.CLI = "unknown"
		}
		usageCount++
		calls := snapshotUsageCallCount(usage)
		stats.CallCount += calls
		stats.InputTokens += usage.InputTokens
		stats.OutputTokens += usage.OutputTokens
		stats.CacheReadTokens += usage.CacheReadTokens
		stats.CacheWriteTokens += usage.CacheWriteTokens
		round := usageRoundNumber(event.Data)
		if round == 0 {
			round = orchestrationRoundForTime(boundaries, event.CreatedAt)
		}
		appendRoundUsage(rounds, round, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, calls)
		usage.CallCount = calls
		appendParticipantRound(&stats, rounds, round, usage)
		stats.EstimatedCostUSD += usage.EstimatedCostUSD
		stats.Estimated = stats.Estimated || usage.Estimated
		stats.Native = stats.Native || usage.Native
		if usageCount == 1 {
			stats.CostKnown = usage.CostKnown
		} else {
			stats.CostKnown = stats.CostKnown && usage.CostKnown
		}
		if usage.CostSource != "" {
			if stats.CostSource == "" {
				stats.CostSource = usage.CostSource
			} else if stats.CostSource != usage.CostSource {
				stats.CostSource = "mixed"
			}
		}
		stats.PricingModels = appendUnique(stats.PricingModels, usage.PricingModel)
		key := usage.CLI + "\x00" + usage.Model
		item := byCLI[key]
		newItem := item == nil
		if item == nil {
			item = &orchestrationUsageStats{CLI: usage.CLI, Model: usage.Model, Estimated: usage.Estimated, CostKnown: usage.CostKnown}
			byCLI[key] = item
		}
		item.CallCount += calls
		item.InputTokens += usage.InputTokens
		item.OutputTokens += usage.OutputTokens
		item.CacheReadTokens += usage.CacheReadTokens
		item.CacheWriteTokens += usage.CacheWriteTokens
		item.EstimatedCostUSD += usage.EstimatedCostUSD
		item.Estimated = item.Estimated || usage.Estimated
		item.Native = item.Native || usage.Native
		if !newItem {
			item.CostKnown = item.CostKnown && usage.CostKnown
		}
		if item.PricingModel == "" {
			item.PricingModel = usage.PricingModel
		} else if usage.PricingModel != "" && item.PricingModel != usage.PricingModel {
			item.PricingModel = "mixed"
		}
		if usage.CostSource != "" {
			if item.CostSource == "" {
				item.CostSource = usage.CostSource
			} else if item.CostSource != usage.CostSource {
				item.CostSource = "mixed"
			}
		}
	}
	if stats.StartedAt == 0 {
		stats.StartedAt = run.CreatedAt
	}
	end := run.FinishedAt
	if end == 0 && run.Status == store.OrchestrationRunning {
		end = time.Now().Unix()
	}
	if end >= stats.StartedAt {
		stats.RuntimeSeconds = end - stats.StartedAt
	}
	for _, usage := range byCLI {
		stats.ByCLI = append(stats.ByCLI, *usage)
	}
	addDerivedTokenTotals(&stats)
	finalizeOrchestrationRounds(&stats, rounds)
	return stats
}

// shouldUseOrchestrationUsageLedger only prefers a local ledger when it is a
// complete replacement for the per-turn snapshots. Isolated worker homes are
// intentionally removed after a run; a later ledger scan then reports an
// unavailable, empty result even though the bridge already persisted native
// turn usage while the CLI was running.
func shouldUseOrchestrationUsageLedger(timeline []store.OrchestrationEvent, ledger []protocol.OrchestrationUsageEvent, syncs []store.OrchestrationUsageSync) bool {
	syncs = accountingUsageSyncs(syncs)
	if len(syncs) == 0 {
		return false
	}
	complete := true
	for _, sync := range syncs {
		if sync.Status != "complete" {
			complete = false
			break
		}
	}
	// Worker-session logs do not cover independent verifier invocations or
	// provider billing amounts. Replacing these snapshots would lose usage/cost.
	for _, event := range timeline {
		if event.Kind == "turn.usage" && (event.Role == "verifier" || (event.Data != nil && event.Data["costSource"] == "provider")) {
			return false
		}
	}
	if complete && len(ledger) > 0 {
		return true
	}
	for _, event := range timeline {
		if event.Kind == "turn.usage" && event.Data != nil {
			return false
		}
	}
	// No snapshots are available, so retain a partial ledger rather than
	// discarding the only observed accounting data. An empty ledger is not a
	// data source even when its sync records exist.
	return len(ledger) > 0
}

func buildLedgerOrchestrationRunStats(run store.OrchestrationRun, timeline []store.OrchestrationEvent, events []protocol.OrchestrationUsageEvent, syncs []store.OrchestrationUsageSync) orchestrationRunStats {
	syncs = accountingUsageSyncs(syncs)
	stats := orchestrationRunStats{RunID: run.ID, FinishedAt: run.FinishedAt, StartedAt: orchestrationStatsStartTime(run, timeline), Native: true, AccountingStatus: "complete", AccountingSource: "local-cli-ledger", CostKnown: len(events) > 0, ByCLI: []orchestrationUsageStats{}, ByWorker: []orchestrationUsageStats{}}
	if stats.StartedAt == 0 {
		stats.StartedAt = run.CreatedAt
	}
	end := run.FinishedAt
	if end == 0 && run.Status == store.OrchestrationRunning {
		end = time.Now().Unix()
	}
	if end >= stats.StartedAt {
		stats.RuntimeSeconds = end - stats.StartedAt
	}
	byCLI := map[string]*orchestrationUsageStats{}
	rounds := map[int]*orchestrationRoundStats{}
	boundaries := orchestrationRoundBoundaries(timeline)
	initializeOrchestrationRounds(rounds, boundaries)
	for _, sync := range syncs {
		if sync.ScannedAt > stats.ScannedAt {
			stats.ScannedAt = sync.ScannedAt
		}
		if sync.Status != "complete" {
			stats.AccountingStatus = "partial"
		}
	}
	for _, event := range events {
		stats.InputTokens += event.InputTokens
		stats.CacheReadTokens += event.CacheReadTokens
		stats.CacheWriteTokens += event.CacheWriteTokens
		stats.OutputTokens += event.OutputTokens
		stats.ReasoningTokens += event.ReasoningTokens
		stats.CallCount++
		round := workerLedgerRound(timeline, boundaries, event)
		appendRoundUsage(rounds, round, event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.CacheWriteTokens, 1)
		key := event.CLI + "\x00" + event.Model
		item := byCLI[key]
		if item == nil {
			item = &orchestrationUsageStats{CLI: event.CLI, Model: event.Model, Native: true, CostKnown: true}
			byCLI[key] = item
		}
		item.InputTokens += event.InputTokens
		item.CacheReadTokens += event.CacheReadTokens
		item.CacheWriteTokens += event.CacheWriteTokens
		item.OutputTokens += event.OutputTokens
		item.ReasoningTokens += event.ReasoningTokens
		item.CallCount++
		worker := orchestrationUsageStats{CLI: event.CLI, WorkerSlot: event.WorkerSlot, Model: event.Model, Native: true, InputTokens: event.InputTokens, OutputTokens: event.OutputTokens, CacheReadTokens: event.CacheReadTokens, CacheWriteTokens: event.CacheWriteTokens, ReasoningTokens: event.ReasoningTokens, CallCount: 1}
		// Pricing is resolved from the concrete model, not the transport provider.
		// Local relays commonly report provider=custom while still using a model
		// with a known public catalog price. Unknown models remain unpriced.
		quote, ok := usagepricing.EstimateNormalized(event.CLI, event.Model, event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.CacheWriteTokens)
		if ok {
			worker.CostKnown = true
			worker.EstimatedCostUSD = quote.CostUSD
			worker.CostSource = quote.Source
			worker.PricingModel = quote.PricingModel
			stats.EstimatedCostUSD += quote.CostUSD
			item.EstimatedCostUSD += quote.CostUSD
			stats.PricingModels = appendUnique(stats.PricingModels, quote.PricingModel)
			item.PricingModel = quote.PricingModel
			item.CostSource = quote.Source
			if stats.CostSource == "" {
				stats.CostSource = quote.Source
			} else if stats.CostSource != quote.Source {
				stats.CostSource = "mixed"
			}
		} else {
			stats.CostKnown = false
			item.CostKnown = false
		}
		appendParticipantRound(&stats, rounds, round, worker)
	}
	if len(events) == 0 {
		stats.CostKnown = false
	}
	for _, item := range byCLI {
		stats.ByCLI = append(stats.ByCLI, *item)
	}
	addDerivedTokenTotals(&stats)
	finalizeOrchestrationRounds(&stats, rounds)
	return stats
}

func (s *Server) handleOrchestrationUsageSync(w http.ResponseWriter, r *http.Request, uid string) {
	ctx, cancel := context.WithTimeout(r.Context(), orchestrationStatsTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		writeUsageReadError(w, r, "failed to load orchestration run")
		return
	}
	events, err := s.store.ListOrchestrationUsageTimeline(r.Context(), run.ID)
	if err != nil {
		writeUsageReadError(w, r, "failed to load orchestration run")
		return
	}
	if err := s.requestOrchestrationUsageSync(r.Context(), run, events); err != nil {
		if r.Context().Err() != nil {
			writeUsageReadError(w, r, "statistics request interrupted")
			return
		}
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return
	}
	serverutil.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "syncing"})
}

func (s *Server) requestOrchestrationUsageSync(ctx context.Context, run store.OrchestrationRun, events []store.OrchestrationEvent) error {
	if !s.pool.AgentSupportsUsageLedger(run.AgentID) {
		return errors.New("connected Bridge does not support the local usage ledger")
	}
	profiles, err := s.store.OrchestrationWorkerProfiles(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("load worker profile bindings: %w", err)
	}
	sessions := orchestrationUsageSessions(run, events, profiles)
	graphs, err := s.store.ListTaskGraphMetadataByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	recovery := planningUsageRecoveryHints(events, graphs)
	if !s.supportsModelRecovery(run.AgentID) {
		recovery = nil
	}
	if len(sessions) == 0 && len(recovery) == 0 {
		return errors.New("native session metadata unavailable; update the Bridge to enable historical model recovery")
	}
	if !s.reserveUsageSync(run.ID, time.Now()) {
		return nil
	}
	err = s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationUsageSyncRequest, "", protocol.OrchestrationUsageSyncRequest{RunID: run.ID, Sessions: sessions, Recovery: recovery}))
	if err != nil {
		s.releaseUsageSync(run.ID)
	}
	return err
}

func orchestrationUsageSessions(run store.OrchestrationRun, events []store.OrchestrationEvent, profiles map[string]protocol.WorkerProfileBinding) []protocol.OrchestrationUsageSession {
	seen := map[string]bool{}
	var sessions []protocol.OrchestrationUsageSession
	// Structured per-slot maps are stronger evidence than legacy resume labels,
	// which copied the last worker ID into both A and B. Include old task ends:
	// the run's current maps only retain the latest native sessions.
	knownOwners := map[string]string{}
	conflictingOwners := map[string]bool{}
	remember := func(cli string, ids map[string]string) {
		for slot, id := range ids {
			key := cli + "\x00" + strings.TrimSpace(id)
			if owner, ok := knownOwners[key]; ok && owner != slot {
				conflictingOwners[key] = true
			}
			knownOwners[key] = slot
		}
	}
	remember("codex", run.CodexThreadIDs)
	remember("claude", run.ClaudeSessionIDs)
	for _, event := range events {
		if event.RunEndData != nil {
			remember("codex", event.RunEndData.CodexThreadIDs)
			remember("claude", event.RunEndData.ClaudeSessionIDs)
		}
	}
	add := func(cli, slot, id string) {
		id = strings.TrimSpace(id)
		if cli == "codex-a" || cli == "codex-b" || cli == "claude-a" || cli == "claude-b" {
			if slot == "" {
				slot = cli
			}
			cli = strings.Split(cli, "-")[0]
		}
		if slot != "verifier" && (run.WorkerPair == "codex-codex" && cli == "claude" || run.WorkerPair == "claude-claude" && cli == "codex") {
			return
		}
		ownerKey := cli + "\x00" + id
		if conflictingOwners[ownerKey] {
			return
		}
		if owner, known := knownOwners[ownerKey]; known && owner != slot {
			return
		}
		key := cli + "\x00" + slot + "\x00" + id
		if id != "" && !seen[key] {
			seen[key] = true
			profile, bound := profiles[slot]
			isolated := bound && !profile.OfficialLogin
			sessions = append(sessions, protocol.OrchestrationUsageSession{CLI: cli, WorkerSlot: slot, SessionID: id, Isolated: isolated})
		}
	}
	for slot, id := range run.CodexThreadIDs {
		add("codex", slot, id)
	}
	if len(run.CodexThreadIDs) == 0 {
		add("codex", "codex", run.CodexThreadID)
	}
	for slot, id := range run.ClaudeSessionIDs {
		add("claude", slot, id)
	}
	for _, event := range events {
		if event.RunEndData == nil {
			continue
		}
		for slot, id := range event.RunEndData.CodexThreadIDs {
			add("codex", slot, id)
		}
		add("claude", "claude", event.RunEndData.ClaudeSessionID)
		for slot, id := range event.RunEndData.ClaudeSessionIDs {
			add("claude", slot, id)
		}
		for _, native := range event.RunEndData.NativeResume {
			add(native.CLI, native.WorkerSlot, native.ID)
		}
	}
	turnSlots := map[string]string{}
	for _, event := range events {
		if event.Kind == "turn.start" {
			slot := usageText(event.Data, "workerSlot")
			if event.TurnStartData != nil {
				slot = event.TurnStartData.WorkerSlot
			}
			if slot == "" && event.Task != nil {
				slot = event.Task.WorkerSlot
			}
			turnSlots[event.CLI+"\x00"+event.TurnID] = slot
		}
	}
	for _, event := range events {
		if event.Kind != "bridge.note" || usageText(event.Data, "category") != "native-identity" {
			continue
		}
		slot := usageText(event.Data, "workerSlot")
		if slot == "" {
			slot = turnSlots[event.CLI+"\x00"+event.TurnID]
		}
		if slot != "" {
			add(event.CLI, slot, usageText(event.Data, "nativeSessionId"))
		}
	}
	// Do not bill an old copied native ID twice under different participants.
	owners := map[string]string{}
	ambiguous := map[string]bool{}
	for _, session := range sessions {
		key := session.CLI + "\x00" + session.SessionID
		if owner, ok := owners[key]; ok && owner != session.WorkerSlot {
			ambiguous[key] = true
		}
		owners[key] = session.WorkerSlot
	}
	filtered := sessions[:0]
	for _, session := range sessions {
		if !ambiguous[session.CLI+"\x00"+session.SessionID] {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (s *Server) handleOrchestrationUsageSyncResult(ctx context.Context, agentID string, env protocol.Envelope) {
	result, err := protocol.Decode[protocol.OrchestrationUsageSyncResult](env)
	if err != nil || result.RunID == "" {
		return
	}
	run, err := s.store.OrchestrationRunByIDAnyUser(ctx, result.RunID)
	if err != nil || run.AgentID != agentID {
		slog.Warn("[hub] rejected usage ledger from foreign bridge", "agent_id", agentID, "run_id", result.RunID)
		return
	}
	if err := s.store.ReplaceOrchestrationUsage(ctx, result); err != nil {
		slog.Error("[hub] persist usage ledger failed", "run_id", result.RunID, "error", err)
	}
}

// Snapshots preserve native provider inputs; ledger rows already exclude
// cache reads. Normalize only snapshots, including historical records.
func snapshotUsageCallCount(usage orchestrationUsageStats) int {
	if usage.Model == "unattributed" || (usage.Estimated && !usage.Native && usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 && usage.EstimatedCostUSD == 0) {
		return 0 // Aggregate remainders are not additional model calls.
	}
	return 1
}

func orchestrationSnapshotUsage(event store.OrchestrationEvent) orchestrationUsageStats {
	usage := orchestrationUsageFromData(event.Data)
	if usage.CLI == "" {
		usage.CLI = event.CLI
	}
	// Catalog pricing expects the original provider convention.
	usage.applyOfficialCatalogFallback()
	if strings.EqualFold(usage.CLI, "codex") {
		usage.InputTokens = max(int64(0), usage.InputTokens-usage.CacheReadTokens)
	}
	return usage
}

func orchestrationUsageFromData(data map[string]any) orchestrationUsageStats {
	value := func(key string) int64 {
		switch v := data[key].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
		return 0
	}
	decimal := func(key string) float64 {
		if v, ok := data[key].(float64); ok {
			return v
		}
		return 0
	}
	text := func(key string) string { v, _ := data[key].(string); return v }
	estimated, _ := data["estimated"].(bool)
	native, _ := data["native"].(bool)
	costKnown, _ := data["costKnown"].(bool)
	return orchestrationUsageStats{WorkerSlot: text("workerSlot"), CLI: text("cli"), Model: text("model"), ModelSource: text("modelSource"), InputTokens: value("inputTokens"), OutputTokens: value("outputTokens"), CacheReadTokens: value("cacheReadTokens"), CacheWriteTokens: value("cacheWriteTokens"), EstimatedCostUSD: decimal("estimatedCostUsd"), Estimated: estimated, Native: native, CostKnown: costKnown, CostSource: text("costSource"), PricingModel: text("pricingModel")}
}

func (usage *orchestrationUsageStats) applyOfficialCatalogFallback() {
	if usage.CostKnown && usage.CostSource != "catalog" && usage.CostSource != "official-catalog" && usage.CostSource != "models.dev" || usage.CostSource == "provider-unallocated" {
		return
	}
	quote, ok := usagepricing.Estimate(usage.CLI, usage.Model, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
	if !ok {
		return
	}
	usage.EstimatedCostUSD = quote.CostUSD
	usage.CostKnown = true
	usage.CostSource = quote.Source
	usage.PricingModel = quote.PricingModel
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *Server) startOrchestration(ctx context.Context, run store.OrchestrationRun, req orchestrationStartRequest, contextParts []string, resume bool) error {
	event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:   run.ID,
		Kind:    "user.message",
		Source:  "user",
		Role:    "user",
		Content: req.Prompt,
		Status:  store.OrchestrationQueued,
		Data:    orchestrationUserMessageData(req.Files),
	})
	if err != nil {
		return err
	}
	s.pool.BroadcastToOrchestrationBrowsers(run.ID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
	if err := s.store.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		slog.Error("[hub] update orchestration status failed", "run_id", run.ID, "error", err)
	}
	payload := protocol.OrchestrationStartPayload{
		ExecutionMode: req.ExecutionMode, RunID: run.ID,
		Mode:                    req.Mode,
		WorkerPair:              req.WorkerPair,
		FirstCLI:                req.FirstCLI,
		Prompt:                  req.Prompt,
		TaskPrompt:              req.Prompt,
		Context:                 strings.Join(cleanContextParts(contextParts), "\n\n"),
		Resume:                  resume,
		PromptSeq:               event.Seq,
		MaxTurns:                req.MaxTurns,
		MaxTurnsRequested:       req.MaxTurnsRequested,
		CWD:                     req.CWD,
		Files:                   req.Files,
		CodexThreadID:           orchestrationResumeString(resume, run.CodexThreadID),
		CodexThreadIDs:          orchestrationResumeStringMap(resume, run.CodexThreadIDs),
		ClaudeStarted:           resume && run.ClaudeStarted,
		ClaudeSessionIDs:        orchestrationResumeStringMap(resume, run.ClaudeSessionIDs),
		ClaudeStartedSlots:      orchestrationResumeBoolMap(resume, run.ClaudeStartedSlots),
		RunCWD:                  orchestrationResumeString(resume, run.RunCWD),
		Profile:                 req.Profile,
		VerificationMode:        req.VerificationMode,
		NativeContextCompaction: req.NativeContextCompaction,
		PlanWorkspace:           req.PlanWorkspace,
		WorkerProfiles:          req.WorkerProfiles,
	}
	if user, err := s.store.UserByID(ctx, run.UserID); err == nil {
		payload.NativeUserInput = req.NativeUserInputClientVersion == 3 && s.featureEnabled(rollout.FeatureNativeUserInput, user) && s.nativeUserInputV3(run.AgentID, "")
		if payload.NativeUserInput {
			payload.NativeUserInputProtocolVersion = 3
		}
	}
	if s.durableTaskGraphEnabled(run.AgentID) {
		if err := s.createAndDispatchTaskGraph(ctx, run, payload); err != nil {
			_ = s.store.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationFailed, err.Error())
			return err
		}
		return nil
	}
	if err := s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationStart, "", payload)); err != nil {
		_ = s.store.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationFailed, err.Error())
		_, _ = s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
			RunID:    run.ID,
			Kind:     "run.error",
			Source:   "bridge",
			Severity: "error",
			Status:   store.OrchestrationFailed,
			Error:    err.Error(),
			RunConclusion: &protocol.RunConclusion{
				Outcome:          "errored",
				Summary:          "Orchestration could not start because the selected Bridge endpoint was unavailable: " + err.Error(),
				UnmetObligations: []string{"No CLI turn was started."},
			},
		})
		return err
	}
	return nil
}

func (s *Server) validateOrchestrationCapabilities(agentID, workerPair string) error {
	if agentID == "" {
		return errors.New("agent id is required")
	}
	connection, ok := s.pool.AgentConnectionInfo(agentID)
	if !ok || connection.Capabilities == nil {
		return errors.New("selected CLI endpoint is offline or did not advertise orchestration approval capabilities")
	}
	if bridgeVersionBefore(connection.Version, 0, 3, 1) {
		return fmt.Errorf("selected CLI endpoint is running Bridge %s, which cancels active orchestration after a transient network disconnect; update Bridge to v0.3.1 or newer before starting or continuing orchestration", connection.Version)
	}
	caps := connection.Capabilities
	required := orchestrationRequiredCLIs(workerPair)
	missingCLI := missingOrchestrationCLIs(caps, required)
	if len(missingCLI) > 0 {
		return fmt.Errorf("selected CLI endpoint cannot execute %s; reconnect the endpoint after installing the missing CLI commands or fixing its service PATH", strings.Join(missingCLI, " and "))
	}
	if strings.EqualFold(caps.ApprovalPolicy, "never") && strings.EqualFold(caps.Sandbox, "danger-full-access") {
		return nil
	}
	if strings.EqualFold(caps.Metadata["approvalMode"], permissionProfileAutoExecute) {
		return nil
	}
	if strings.EqualFold(caps.Metadata["approvalMode"], permissionProfileStrictWorkspace) {
		return nil
	}
	missing := missingOrchestrationBrowserApproval(caps, required)
	if len(missing) > 0 {
		return fmt.Errorf("review-required orchestration needs browser approval for %s; reconnect the endpoint with a review-required bridge that supports app-server orchestration", strings.Join(missing, " and "))
	}
	return nil
}

func (s *Server) validateVerificationCapability(agentID, profile, verificationMode string) error {
	if normalizeOrchestrationProfile(profile) != "formal-proof" || protocol.NormalizeVerificationMode(verificationMode) != protocol.VerificationModeTBCI {
		return nil
	}
	connection, ok := s.pool.AgentConnectionInfo(agentID)
	if !ok || connection.Capabilities == nil {
		return errors.New("selected CLI endpoint is offline and cannot report TBCI availability")
	}
	capability := connection.Capabilities.TBCI
	if capability == nil {
		return errors.New("selected CLI endpoint must update and reconnect its Bridge before TBCI verification can be selected")
	}
	if capability.Available && strings.EqualFold(strings.TrimSpace(capability.State), "ready") {
		return nil
	}
	reason := strings.TrimSpace(capability.Reason)
	if reason == "" {
		state := strings.TrimSpace(capability.State)
		if state == "" {
			state = "unavailable"
		}
		reason = "the Bridge reported TBCI as " + state
	}
	return fmt.Errorf("selected CLI endpoint cannot run TBCI: %s", reason)
}

// validateIsolatedWorkerProfileCapability rejects Bridges that predate the
// per-slot native-home contract. Accepting a profile payload on those clients
// would silently revive the retired model_catalog_json path.
func (s *Server) validateIsolatedWorkerProfileCapability(agentID string) error {
	connection, ok := s.pool.AgentConnectionInfo(agentID)
	if !ok || connection.Capabilities == nil || !connection.Capabilities.IsolatedWorkerProfiles {
		return errors.New("selected CLI endpoint must update its Bridge before using per-worker model presets; reconnect the upgraded Bridge and try again")
	}
	return nil
}

func bridgeVersionBefore(value string, wantMajor, wantMinor, wantPatch int) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if suffix := strings.IndexAny(value, "-+"); suffix >= 0 {
		value = value[:suffix]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	got := [3]int{}
	for i, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return false
		}
		got[i] = parsed
	}
	want := [3]int{wantMajor, wantMinor, wantPatch}
	for i := range got {
		if got[i] != want[i] {
			return got[i] < want[i]
		}
	}
	return false
}

func orchestrationRequiredCLIs(workerPair string) []string {
	switch protocol.NormalizeOrchestrationWorkerPair(workerPair) {
	case protocol.WorkerPairCodexCodex:
		return []string{"codex"}
	case protocol.WorkerPairClaudeClaude:
		return []string{"claude"}
	default:
		return []string{"claude", "codex"}
	}
}

func missingOrchestrationCLIs(caps *protocol.BridgeCapabilities, required []string) []string {
	if caps == nil {
		return cliDisplayNames(required)
	}
	var missing []string
	for _, cli := range required {
		capability, ok := caps.Orchestration[cli]
		if !ok || !capability.Available {
			missing = append(missing, cliDisplayName(cli))
		}
	}
	return missing
}

func missingOrchestrationBrowserApproval(caps *protocol.BridgeCapabilities, required []string) []string {
	if caps == nil {
		return cliDisplayNames(required)
	}
	var missing []string
	for _, cli := range required {
		capability, ok := caps.Orchestration[cli]
		if !ok || !capability.Available || !capability.BrowserApproval {
			missing = append(missing, cliDisplayName(cli))
		}
	}
	return missing
}

func cliDisplayNames(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, cliDisplayName(value))
	}
	return out
}

func cliDisplayName(cli string) string {
	switch strings.ToLower(cli) {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	default:
		return cli
	}
}

func normalizeOrchestrationFirstCLI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return "codex"
	default:
		return "claude"
	}
}

func normalizeOrchestrationProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "formal-proof":
		return "formal-proof"
	default:
		return "default"
	}
}

func (s *Server) handleGetOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleDeleteOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	runID := r.PathValue("runID")
	run, action, err := s.store.RequestDeleteOrchestrationRun(r.Context(), runID, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to request orchestration deletion")
		return
	}
	if action == store.DeleteOrchestrationDeleted {
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": action})
		return
	}
	s.scheduleOrchestrationCancelTimeout(run.ID, orchestrationCancelAckTimeout)
	if action == store.DeleteOrchestrationCancelStarted {
		if err := s.persistAndDispatchOrchestrationCancel(r.Context(), run); err != nil {
			serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to start orchestration cancellation")
			return
		}
	}
	serverutil.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": store.OrchestrationCanceling})
}

func (s *Server) handleOrchestrationEvents(w http.ResponseWriter, r *http.Request, uid string) {
	runID := r.PathValue("runID")
	_, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	limit := boundedQueryLimit(r, "limit", 200, 1000)
	afterSeq, hasAfterSeq, ok := int64QueryParam(w, r, "afterSeq")
	if !ok {
		return
	}
	beforeSeq, hasBeforeSeq, ok := int64QueryParam(w, r, "beforeSeq")
	if !ok {
		return
	}
	if hasAfterSeq && hasBeforeSeq {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_QUERY", "afterSeq and beforeSeq cannot be used together")
		return
	}
	// Read one extra record so the browser does not have to infer pagination
	// from page size after reconnects or event de-duplication.
	queryLimit := limit + 1
	var events []store.OrchestrationEvent
	if hasAfterSeq {
		events, err = s.store.ListOrchestrationEventsAfter(r.Context(), runID, afterSeq, queryLimit)
	} else if hasBeforeSeq {
		events, err = s.store.ListOrchestrationEventsBefore(r.Context(), runID, beforeSeq, queryLimit)
	} else {
		events, err = s.store.ListOrchestrationEvents(r.Context(), runID, queryLimit)
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list orchestration events")
		return
	}
	hasMoreBefore := false
	hasMoreAfter := false
	if hasAfterSeq && len(events) > limit {
		hasMoreAfter = true
		events = events[:limit]
	}
	if !hasAfterSeq && len(events) > limit {
		hasMoreBefore = true
		// All store methods return ascending events. Retain the newest page so
		// the next beforeSeq cursor is adjacent to the currently displayed page.
		events = events[len(events)-limit:]
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"events": events, "hasMoreBefore": hasMoreBefore, "hasMoreAfter": hasMoreAfter})
}

func (s *Server) handleCancelOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	if orchestrationTerminalStatus(run.Status) {
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": run.Status})
		return
	}
	if run.Status == store.OrchestrationCanceling {
		s.scheduleOrchestrationCancelTimeout(run.ID, orchestrationCancelAckTimeout)
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": run.Status})
		return
	}
	if !orchestrationCancelableStatus(run.Status) {
		serverutil.WriteError(w, http.StatusConflict, "NOT_CANCELABLE", "orchestration run is not cancelable")
		return
	}
	if err := s.store.UpdateOrchestrationRunStatus(r.Context(), run.ID, store.OrchestrationCanceling, ""); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration run")
		return
	}
	if err := s.persistAndDispatchOrchestrationCancel(r.Context(), run); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to persist orchestration cancellation")
		return
	}
	s.scheduleOrchestrationCancelTimeout(run.ID, orchestrationCancelAckTimeout)
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": store.OrchestrationCanceling})
}

func (s *Server) persistAndDispatchOrchestrationCancel(ctx context.Context, run store.OrchestrationRun) error {
	event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:  run.ID,
		Kind:   "run.canceling",
		Source: "bridge",
		Status: store.OrchestrationCanceling,
	})
	if err != nil {
		return err
	}
	s.pool.BroadcastToOrchestrationBrowsers(run.ID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
	_ = s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationCancel, "", protocol.OrchestrationCancelPayload{RunID: run.ID}))
	return nil
}

func (s *Server) scheduleOrchestrationCancelTimeout(runID string, delay time.Duration) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		reason := "selected CLI endpoint did not acknowledge cancellation before timeout"
		event, changed, err := s.store.CancelOrchestrationRunIfStillCanceling(ctx, runID, reason)
		if err != nil {
			slog.Error("[hub] cancel orchestration timeout failed", "run_id", runID, "error", err)
			return
		}
		if !changed {
			return
		}
		slog.Warn("[hub] marked canceling orchestration canceled after timeout", "run_id", runID)
		s.pool.BroadcastToOrchestrationBrowsers(runID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
		if deleted, deleteErr := s.store.DeleteOrchestrationRunIfRequested(ctx, runID); deleteErr != nil {
			slog.Error("[hub] delete canceled orchestration failed", "run_id", runID, "error", deleteErr)
		} else if deleted {
			slog.Info("[hub] deleted orchestration after cancellation timeout", "run_id", runID)
		}
	}()
}

func orchestrationCancelableStatus(status string) bool {
	return status == store.OrchestrationQueued || status == store.OrchestrationRunning
}

func orchestrationActiveStatus(status string) bool {
	return status == store.OrchestrationQueued || status == store.OrchestrationRunning || status == store.OrchestrationCanceling
}

func orchestrationTerminalStatus(status string) bool {
	return status == store.OrchestrationCompleted || status == store.OrchestrationFailed || status == store.OrchestrationCanceled
}

func cleanContextParts(parts []string) []string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return clean
}

func orchestrationResumeString(resume bool, value string) string {
	if !resume {
		return ""
	}
	return value
}

func orchestrationResumeStringMap(resume bool, values map[string]string) map[string]string {
	if !resume || len(values) == 0 {
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

func orchestrationResumeBoolMap(resume bool, values map[string]bool) map[string]bool {
	if !resume || len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		if key = strings.TrimSpace(key); key != "" && value {
			out[key] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boundedQueryLimit(r *http.Request, name string, defaultLimit, maxLimit int) int {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func int64QueryParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return 0, false, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_QUERY", name+" must be a non-negative integer")
		return 0, true, false
	}
	return n, true, true
}

func compactOrchestrationContext(run store.OrchestrationRun, events []store.OrchestrationEvent) string {
	filtered := make([]store.OrchestrationEvent, 0, len(events))
	for _, event := range events {
		if !isInternalOrchestrationBootstrapEvent(event) {
			filtered = append(filtered, event)
		}
	}
	events = filtered
	events = mergeOrchestrationDeltasForContext(events)
	var userMessages []string
	var turnNotes []string
	var commands []string
	var outcomes []string
	var blockers []string
	for _, event := range events {
		switch {
		case event.Kind == "user.message":
			userMessages = append(userMessages, trimForContext(event.Content, 900))
		case event.Kind == "turn.end":
			if strings.TrimSpace(event.Content) != "" {
				turnNotes = append(turnNotes, formatOrchestrationActor(event)+": "+trimForContext(event.Content, 900))
			}
			if event.Status != "" || event.Error != "" {
				outcomes = append(outcomes, formatOrchestrationActor(event)+": "+trimForContext(joinNonEmpty(event.Status, event.Error), 300))
			}
		case event.Kind == "turn.delta" && strings.TrimSpace(event.Content) != "":
			turnNotes = append(turnNotes, formatOrchestrationActor(event)+": "+trimForContext(event.Content, 900))
		case event.Kind == "command.end" || event.Kind == "command.start" || event.Kind == "command.update":
			command := stringFromMap(event.Data, "command")
			if command == "" {
				continue
			}
			status := stringFromMap(event.Data, "status")
			if status == "" {
				status = event.Status
			}
			output := trimForContext(stringFromMap(event.Data, "output"), 300)
			commands = append(commands, trimForContext(joinNonEmpty(command, status, output), 700))
		case event.Kind == "run.error" || event.Kind == "run.cancelled":
			blockers = append(blockers, trimForContext(joinNonEmpty(event.Error, event.Content, event.Status), 600))
		case event.Kind == "bridge.note" && event.BridgeNoteData != nil && event.BridgeNoteData.Category == "tbci-verification":
			blockers = append(blockers, trimForContext(joinNonEmpty(event.Error, event.Content), 600))
		}
	}

	var b strings.Builder
	b.WriteString("Compacted orchestration context from previous work.\n")
	b.WriteString("Carry this state forward for continuity. Prefer the latest user task when it conflicts with older details.\n\n")
	b.WriteString("Run state:\n")
	b.WriteString(fmt.Sprintf("- Run ID: %s\n", run.ID))
	b.WriteString(fmt.Sprintf("- Mode: %s\n", run.Mode))
	if run.CWD != "" {
		b.WriteString(fmt.Sprintf("- Working directory: %s\n", run.CWD))
	}
	if run.Status != "" {
		b.WriteString(fmt.Sprintf("- Previous status: %s\n", run.Status))
	}
	writeContextSection(&b, "User goals so far", lastN(userMessages, 8))
	writeContextSection(&b, "Recent agent outputs", lastN(turnNotes, 10))
	writeContextSection(&b, "Tool outcomes and commands", lastN(commands, 12))
	writeContextSection(&b, "Run outcomes", lastN(outcomes, 8))
	writeContextSection(&b, "Unresolved blockers or errors", lastN(blockers, 6))
	return trimForContext(b.String(), 14000)
}

func isInternalOrchestrationBootstrapEvent(event store.OrchestrationEvent) bool {
	if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "formal-proof-harness-bootstrap" {
		return true
	}
	return stringFromMap(event.Data, "category") == "formal-proof-harness-bootstrap" ||
		(event.Role == "bootstrap" && event.CLI == "bridge")
}

func mergeOrchestrationDeltasForContext(events []store.OrchestrationEvent) []store.OrchestrationEvent {
	merged := make([]store.OrchestrationEvent, 0, len(events))
	deltaIndexes := make(map[string]int)
	for _, event := range events {
		if event.Kind != "turn.delta" {
			merged = append(merged, event)
			continue
		}
		content := strings.TrimSpace(event.Content)
		if content == "" {
			continue
		}
		key := contextDeltaKey(event)
		index, ok := deltaIndexes[key]
		if !ok {
			event.Content = content
			deltaIndexes[key] = len(merged)
			merged = append(merged, event)
			continue
		}
		previous := merged[index]
		previous.Content = mergeContextDeltaContent(previous.Content, content)
		if previous.Status == "" {
			previous.Status = event.Status
		}
		if previous.Error == "" {
			previous.Error = event.Error
		}
		merged[index] = previous
	}
	return merged
}

func contextDeltaKey(event store.OrchestrationEvent) string {
	return strings.Join([]string{event.RunID, event.TurnID, event.Role, event.CLI}, "\x1f")
}

func mergeContextDeltaContent(previous, next string) string {
	if previous == "" {
		return next
	}
	if next == "" || strings.HasSuffix(previous, next) {
		return previous
	}
	if strings.HasPrefix(next, previous) {
		return next
	}
	return previous + next
}

func writeContextSection(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(strings.ReplaceAll(item, "\n", "\n  "))
		b.WriteByte('\n')
	}
}

func formatOrchestrationActor(event store.OrchestrationEvent) string {
	parts := []string{}
	if event.Role != "" {
		parts = append(parts, event.Role)
	}
	if event.CLI != "" {
		parts = append(parts, event.CLI)
	}
	if event.TurnID != "" {
		parts = append(parts, event.TurnID)
	}
	if len(parts) == 0 {
		return event.Kind
	}
	return strings.Join(parts, " via ")
}

func joinNonEmpty(values ...string) string {
	var parts []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " | ")
}

func stringFromMap(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}

func stringMapFromMap(data map[string]any, key string) map[string]string {
	if data == nil {
		return nil
	}
	raw, ok := data[key]
	if !ok || raw == nil {
		return nil
	}
	out := map[string]string{}
	switch typed := raw.(type) {
	case map[string]string:
		for key, value := range typed {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				out[key] = value
			}
		}
	case map[string]any:
		for key, value := range typed {
			key = strings.TrimSpace(key)
			valueString := strings.TrimSpace(fmt.Sprint(value))
			if key != "" && valueString != "" {
				out[key] = valueString
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boolMapFromMap(data map[string]any, key string) map[string]bool {
	if data == nil {
		return nil
	}
	raw, ok := data[key]
	if !ok || raw == nil {
		return nil
	}
	out := map[string]bool{}
	switch typed := raw.(type) {
	case map[string]bool:
		for key, value := range typed {
			if key = strings.TrimSpace(key); key != "" && value {
				out[key] = true
			}
		}
	case map[string]any:
		for key, value := range typed {
			if started, ok := value.(bool); ok && started {
				if key = strings.TrimSpace(key); key != "" {
					out[key] = true
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func lastN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func trimForContext(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= len("\n[truncated]") {
		return value[:max]
	}
	return value[:max-len("\n[truncated]")] + "\n[truncated]"
}

func (s *Server) handleOrchestrationWS(w http.ResponseWriter, r *http.Request, uid string) {
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "missing runId")
		return
	}
	run, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	if !orchestrationActiveStatus(run.Status) && !orchestrationTerminalStatus(run.Status) {
		serverutil.WriteError(w, http.StatusConflict, "RUN_NOT_ACTIVE", "orchestration event stream is unavailable")
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: s.checkOrigin}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(64 * 1024)
	conn := NewBrowserConn(runID, ws, s.cfg.Hub.MaxBrowserSendQueue)
	s.pool.AddOrchestrationBrowser(runID, conn)
	go conn.WriteLoop(s.websocketPingInterval())
	defer func() {
		s.pool.RemoveOrchestrationBrowser(runID, conn)
		conn.Close()
	}()
	_ = conn.Send(protocol.MustEnvelope(protocol.TypeStatus, "", map[string]any{"status": "connected", "runId": runID}))
	_ = ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
	})
	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
		switch env.Type {
		case protocol.TypeHeartbeat:
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeHeartbeat, "", map[string]any{"ts": time.Now().Unix()}))
		case protocol.TypeApprovalResponse:
			payload, err := protocol.Decode[protocol.ApprovalResponsePayload](env)
			decision := strings.ToLower(strings.TrimSpace(payload.Decision))
			if err != nil || payload.RequestID == "" || !validApprovalDecision(decision) {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "BAD_APPROVAL_RESPONSE", Message: "invalid approval response"}))
				continue
			}
			payload.Decision = decision
			latest, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
			if err != nil {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "RUN_NOT_FOUND", Message: "orchestration run not found"}))
				continue
			}
			if !orchestrationActiveStatus(latest.Status) {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "RUN_NOT_ACTIVE", Message: "approval is unavailable for a completed run"}))
				continue
			}
			if err := s.pool.SendToAgent(latest.AgentID, protocol.MustEnvelope(protocol.TypeApprovalResponse, "", payload)); err != nil {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "AGENT_OFFLINE", Message: err.Error()}))
			}
		case protocol.TypeUserInputResponse, protocol.TypeUserInputSync:
			s.handleUserInputFromBrowser(r.Context(), uid, runID, conn, env)
		}
	}
}

func validApprovalDecision(decision string) bool {
	return decision == "accept" || decision == "decline" || decision == "cancel"
}

func (s *Server) handleOrchestrationEvent(ctx context.Context, env protocol.Envelope) {
	s.handleOrchestrationEventFromAgent(ctx, "", env)
}

func (s *Server) handleOrchestrationEventFromAgent(ctx context.Context, agentID string, env protocol.Envelope) {
	payload, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
	if err != nil || payload.RunID == "" {
		return
	}
	existingRun, loadErr := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID)
	if loadErr != nil {
		return
	}
	if agentID != "" {
		run := existingRun
		if run.AgentID != agentID {
			slog.Warn("[hub] rejected orchestration event from foreign bridge", "agent_id", agentID, "run_id", payload.RunID)
			return
		}
	}
	// Older Bridges emitted stagnation as an error. Apply the current run
	// lifecycle policy before persistence, retaining their factual check record.
	if payload.RunEndData != nil && payload.RunEndData.TerminalReason == "stalled" && (payload.Kind == "run.error" || payload.Kind == "run.end") {
		reason := strings.TrimSpace(payload.Error)
		if reason == "" {
			reason = payload.Content
		}
		if reason == "" {
			reason = "Repeated unchanged verification result"
		}
		verificationStatus := "unknown"
		if payload.RunEndData.VerifierVerdict != nil {
			reason = payload.RunEndData.VerifierVerdict.Reason
			verificationStatus = payload.RunEndData.VerifierVerdict.Status
		}
		if payload.RunConclusion == nil {
			payload.RunConclusion = &protocol.RunConclusion{}
		}
		payload.RunConclusion.Outcome = "completed"
		payload.RunConclusion.VerificationStatus = verificationStatus
		payload.RunConclusion.Summary = "已完成：连续三轮工作区未变且检查报告相同，已自动提前结束本次运行。\n检查记录：" + reason
		payload.RunConclusion.UnmetObligations = append(payload.RunConclusion.UnmetObligations, reason)
		payload.Kind, payload.Status, payload.Error, payload.Severity = "run.end", store.OrchestrationCompleted, "", "info"
		payload.Content = payload.RunConclusion.Summary
	}
	if existingRun.DeleteRequested && (payload.Task != nil || (payload.Kind != "run.end" && payload.Kind != "run.error" && payload.Kind != "run.cancelled")) {
		slog.Info("[hub] ignored non-terminal event for orchestration pending deletion", "run_id", payload.RunID, "kind", payload.Kind)
		return
	}
	if suppressEmptyPagesReadFailure(payload) {
		return
	}
	isTaskEvent := payload.Task != nil
	if isTaskEvent {
		// Persist node evidence first, then advance the graph. This keeps event
		// sequence intuitive: the reviewer node's terminal evidence precedes the
		// single public graph conclusion it authorizes.
		payload.Status = ""
		if payload.Data == nil {
			payload.Data = map[string]any{}
		}
		payload.Data["task"] = payload.Task
	}
	status := payload.Status
	runStatus := ""
	if !isTaskEvent {
		switch payload.Kind {
		case "run.start":
			runStatus = store.OrchestrationRunning
			if status == "" {
				status = runStatus
			}
		case "run.end":
			runStatus = store.OrchestrationCompleted
			status = runStatus
		case "run.error":
			runStatus = store.OrchestrationFailed
			status = runStatus
		case "run.cancelled":
			runStatus = store.OrchestrationCanceled
			status = runStatus
		case "run.canceling":
			runStatus = store.OrchestrationCanceling
			status = runStatus
		}
	}
	if runStatus != "" {
		if existing, err := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID); err == nil && orchestrationTerminalStatus(existing.Status) {
			slog.Warn("[hub] ignored late terminal orchestration status", "run_id", payload.RunID, "kind", payload.Kind, "status", runStatus)
			return
		}
		changed, err := s.store.UpdateOrchestrationRunStatusIfAllowed(ctx, payload.RunID, runStatus, payload.Error)
		if err != nil {
			slog.Error("[hub] update orchestration status failed", "run_id", payload.RunID, "error", err)
			return
		}
		if !changed {
			slog.Info("[hub] ignored disallowed orchestration status transition", "run_id", payload.RunID, "kind", payload.Kind, "status", runStatus)
			return
		}
	}
	s.updateOrchestrationRunSessionFromEvent(ctx, payload)
	event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:          payload.RunID,
		Kind:           payload.Kind,
		Source:         payload.Source,
		Severity:       payload.Severity,
		Role:           payload.Role,
		CLI:            payload.CLI,
		TurnID:         payload.TurnID,
		Content:        payload.Content,
		Status:         status,
		Error:          payload.Error,
		CommandData:    payload.CommandData,
		RunStartData:   payload.RunStartData,
		TurnStartData:  payload.TurnStartData,
		TurnEndData:    payload.TurnEndData,
		RunEndData:     payload.RunEndData,
		BridgeNoteData: payload.BridgeNoteData,
		RunConclusion:  payload.RunConclusion,
		Task:           payload.Task,
		Data:           payload.Data,
	})
	if err != nil {
		slog.Error("[hub] persist orchestration event failed", "run_id", payload.RunID, "error", err)
		if runStatus != "" && orchestrationTerminalStatus(runStatus) {
			if _, deleteErr := s.store.DeleteOrchestrationRunIfRequested(ctx, payload.RunID); deleteErr != nil {
				slog.Error("[hub] delete settled orchestration after event failure failed", "run_id", payload.RunID, "error", deleteErr)
			}
		}
		return
	}
	s.pool.BroadcastToOrchestrationBrowsers(payload.RunID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
	if !isTaskEvent && (payload.Kind == "run.end" || payload.Kind == "run.error" || payload.Kind == "run.cancelled") {
		if run, loadErr := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID); loadErr == nil {
			if run.DeleteRequested {
				if deleted, deleteErr := s.store.DeleteOrchestrationRunIfRequested(ctx, payload.RunID); deleteErr != nil {
					slog.Error("[hub] delete settled orchestration failed", "run_id", payload.RunID, "error", deleteErr)
				} else if deleted {
					slog.Info("[hub] deleted orchestration after terminal event", "run_id", payload.RunID)
				}
			} else if events, listErr := s.store.ListOrchestrationEvents(ctx, payload.RunID, 10000); listErr == nil {
				if syncErr := s.requestOrchestrationUsageSync(ctx, run, events); syncErr != nil {
					slog.Info("[hub] deferred orchestration usage sync", "run_id", payload.RunID, "reason", syncErr)
				}
			}
		}
	}
	if isTaskEvent {
		if _, taskErr := s.handleTaskGraphEvent(ctx, payload); taskErr != nil {
			slog.Error("[hub] durable task event failed", "run_id", payload.RunID, "task_id", payload.Task.TaskID, "error", taskErr)
		}
	}
}

func (s *Server) updateOrchestrationRunSessionFromEvent(ctx context.Context, payload protocol.OrchestrationEventPayload) {
	switch payload.Kind {
	case "run.start":
		cwd := ""
		if payload.RunStartData != nil {
			cwd = payload.RunStartData.CWD
		}
		if cwd == "" {
			cwd = stringFromMap(payload.Data, "cwd")
		}
		if cwd != "" {
			if err := s.store.UpdateOrchestrationRunSession(ctx, payload.RunID, "", false, cwd); err != nil {
				slog.Error("[hub] update orchestration run cwd failed", "run_id", payload.RunID, "error", err)
			}
		}
	case "turn.end", "run.end":
		codexThreadID := stringFromMap(payload.Data, "codexThreadId")
		if codexThreadID == "" {
			codexThreadID = stringFromMap(payload.Data, "threadId")
		}
		codexThreadIDs := stringMapFromMap(payload.Data, "codexThreadIds")
		if codexThreadID == "" && payload.RunEndData != nil {
			codexThreadID = payload.RunEndData.CodexThreadID
		}
		if len(codexThreadIDs) == 0 && payload.RunEndData != nil {
			codexThreadIDs = payload.RunEndData.CodexThreadIDs
		}
		claudeSessionIDs := stringMapFromMap(payload.Data, "claudeSessionIds")
		if len(claudeSessionIDs) == 0 && payload.RunEndData != nil {
			claudeSessionIDs = payload.RunEndData.ClaudeSessionIDs
		}
		claudeStartedSlots := boolMapFromMap(payload.Data, "claudeStartedSlots")
		claudeStarted := payload.Kind == "turn.end" && strings.EqualFold(payload.CLI, "claude") &&
			!strings.EqualFold(payload.Status, "error") && !strings.EqualFold(payload.Severity, "error")
		claudeWorkerSlot := stringFromMap(payload.Data, "workerSlot")
		if claudeWorkerSlot == "" && payload.TurnStartData != nil {
			claudeWorkerSlot = payload.TurnStartData.WorkerSlot
		}
		if claudeStarted && claudeWorkerSlot != "" {
			if claudeStartedSlots == nil {
				claudeStartedSlots = map[string]bool{}
			}
			claudeStartedSlots[claudeWorkerSlot] = true
		}
		if codexThreadID != "" || len(codexThreadIDs) > 0 || len(claudeSessionIDs) > 0 || len(claudeStartedSlots) > 0 || claudeStarted {
			if err := s.store.UpdateOrchestrationRunSessionStateWithClaude(ctx, payload.RunID, codexThreadID, codexThreadIDs, claudeStarted, claudeSessionIDs, claudeStartedSlots, ""); err != nil {
				slog.Error("[hub] update orchestration run session failed", "run_id", payload.RunID, "error", err)
			}
		}
	}
}

func suppressEmptyPagesReadFailure(payload protocol.OrchestrationEventPayload) bool {
	if !strings.HasPrefix(payload.Kind, "command.") {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	if payload.CommandData != nil && payload.CommandData.Status != "" {
		status = strings.ToLower(strings.TrimSpace(payload.CommandData.Status))
	}
	if status != "failed" && status != "error" {
		return false
	}
	output := strings.TrimSpace(payload.Error + "\n" + payload.Content)
	if payload.CommandData != nil {
		output += "\n" + payload.CommandData.Output
	}
	if payload.Data != nil {
		if value, ok := payload.Data["output"].(string); ok {
			output += "\n" + value
		}
	}
	return strings.Contains(output, `Invalid pages parameter: ""`) && strings.Contains(output, "Pages are 1-indexed")
}

func (s *Server) validateOrchestrationFiles(files []protocol.AttachmentPayload) error {
	if len(files) > 12 {
		return errors.New("at most 12 files can be uploaded")
	}
	maxBytes := s.cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}
	for _, file := range files {
		if strings.TrimSpace(file.Name) == "" {
			return errors.New("file name is required")
		}
		if file.Size <= 0 || file.Size > maxBytes {
			return errors.New("file is too large")
		}
		if strings.TrimSpace(file.Data) == "" {
			return errors.New("file data is missing")
		}
	}
	return nil
}

func orchestrationFileMeta(files []protocol.AttachmentPayload) []store.OrchestrationFile {
	metas := make([]store.OrchestrationFile, 0, len(files))
	for _, file := range files {
		metas = append(metas, store.OrchestrationFile{
			Name:     file.Name,
			MimeType: file.MimeType,
			Size:     file.Size,
		})
	}
	return metas
}

func mergeOrchestrationFiles(groups ...[]store.OrchestrationFile) []store.OrchestrationFile {
	seen := make(map[string]struct{})
	out := make([]store.OrchestrationFile, 0)
	for _, group := range groups {
		for _, file := range group {
			name := strings.TrimSpace(file.Name)
			if name == "" {
				continue
			}
			mimeType := strings.TrimSpace(file.MimeType)
			size := file.Size
			if size < 0 {
				size = 0
			}
			key := fmt.Sprintf("%s\x1f%s\x1f%d", name, mimeType, size)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, store.OrchestrationFile{Name: name, MimeType: mimeType, Size: size})
		}
	}
	return out
}

func orchestrationUserMessageData(files []protocol.AttachmentPayload) map[string]any {
	metas := orchestrationFileMeta(files)
	if len(metas) == 0 {
		return nil
	}
	return map[string]any{"files": metas}
}

func (s *Server) resolveAgentID(ctx context.Context, uid, requested string) (string, error) {
	if requested != "" {
		if _, err := s.visibleAgentByID(ctx, uid, requested); err != nil {
			return "", store.ErrNotFound
		}
		return requested, nil
	}
	agents, err := s.visibleAgents(ctx, uid)
	if err != nil {
		return "", err
	}
	for _, agent := range agents {
		if s.pool.AgentOnline(agent.ID) {
			return agent.ID, nil
		}
	}
	if len(agents) == 0 {
		return "", errors.New("no bridge agent has enrolled yet")
	}
	return agents[0].ID, nil
}

func eventToPayload(event store.OrchestrationEvent) protocol.OrchestrationEventPayload {
	return protocol.OrchestrationEventPayload{
		ID:             event.ID,
		RunID:          event.RunID,
		Seq:            event.Seq,
		TurnID:         event.TurnID,
		Kind:           event.Kind,
		Source:         event.Source,
		Severity:       event.Severity,
		Role:           event.Role,
		CLI:            event.CLI,
		Content:        event.Content,
		Status:         event.Status,
		Error:          event.Error,
		CommandData:    event.CommandData,
		RunStartData:   event.RunStartData,
		TurnStartData:  event.TurnStartData,
		TurnEndData:    event.TurnEndData,
		RunEndData:     event.RunEndData,
		BridgeNoteData: event.BridgeNoteData,
		RunConclusion:  event.RunConclusion,
		Task:           event.Task,
		Data:           event.Data,
		CreatedAt:      event.CreatedAt,
	}
}
