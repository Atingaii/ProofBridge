package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/serverutil"
)

type workerProfileError struct {
	status  int
	code    string
	message string
	err     error
}

func (e *workerProfileError) Error() string { return e.message }
func (e *workerProfileError) Unwrap() error { return e.err }

func (s *Server) writeWorkerProfileError(w http.ResponseWriter, err error) {
	var relay *workerProfileError
	if errors.As(err, &relay) {
		serverutil.WriteError(w, relay.status, relay.code, relay.message)
		return
	}
	serverutil.WriteError(w, http.StatusBadRequest, "BAD_WORKER_PROFILE", err.Error())
}

func (s *Server) requireOfficialWorkerFeature(w http.ResponseWriter, r *http.Request, uid string) bool {
	user, err := s.store.UserByID(r.Context(), uid)
	if err != nil || !s.featureEnabled(rollout.FeatureOfficialCodexWorkerModels, user) {
		serverutil.WriteError(w, http.StatusForbidden, "FEATURE_UNAVAILABLE", "official Codex worker model selection is not enabled for this account")
		return false
	}
	return true
}

// A model map supplements the existing complete preset map. It never changes
// the established omission/replacement semantics of follow-up requests.
func (s *Server) authorizeOfficialWorkerRequest(w http.ResponseWriter, r *http.Request, uid string, req orchestrationCreateRequest) bool {
	explicit := req.WorkerProfileModels != nil
	officialSlots := make(map[string]bool)
	for slot, presetID := range req.WorkerProfilePresetIDs {
		if strings.TrimSpace(presetID) == officialLoginPresetID {
			officialSlots[strings.TrimSpace(slot)] = true
		}
	}
	for slot, effort := range req.WorkerProfileEfforts {
		if strings.TrimSpace(effort) != "" && officialSlots[strings.TrimSpace(slot)] {
			explicit = true
		}
	}
	if !explicit {
		return true
	}
	if !s.requireOfficialWorkerFeature(w, r, uid) {
		return false
	}
	if req.WorkerProfilePresetIDs == nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_WORKER_PROFILE", "official model overrides require an explicit worker profile selection")
		return false
	}
	return true
}

func normalizeOfficialWorkerModels(requested, presets map[string]string) (map[string]string, error) {
	models := make(map[string]string, len(requested))
	for rawSlot, rawModel := range requested {
		slot, model := strings.TrimSpace(rawSlot), strings.TrimSpace(rawModel)
		if !officialCodexWorkerSlot(slot) {
			return nil, fmt.Errorf("official model slot %q must belong to a Codex worker", slot)
		}
		if presets[slot] != officialLoginPresetID {
			return nil, fmt.Errorf("worker slot %q permits model overrides only with official login", slot)
		}
		if _, exists := models[slot]; exists {
			return nil, fmt.Errorf("worker slot %q has more than one model override", slot)
		}
		if model == "" || len(model) > protocol.MaxOfficialModelIDBytes {
			return nil, fmt.Errorf("worker slot %q requires an official model of at most %d bytes", slot, protocol.MaxOfficialModelIDBytes)
		}
		models[slot] = model
	}
	return models, nil
}

func officialCodexWorkerSlot(slot string) bool {
	return slot == "codex" || slot == "codex-a" || slot == "codex-b"
}

func hasExplicitOfficialWorkerParameters(profiles map[string]protocol.WorkerProfileBinding) bool {
	for _, binding := range profiles {
		if binding.OfficialLogin && (binding.Model != "" || binding.ReasoningEffort != "") {
			return true
		}
	}
	return false
}

func (s *Server) validateOfficialWorkerCapability(agentID string) error {
	connection, online := s.pool.AgentConnectionInfo(agentID)
	if !online {
		return &workerProfileError{status: http.StatusConflict, code: "AGENT_OFFLINE", message: "Bridge must be online to select official Codex models"}
	}
	if connection.Capabilities == nil || connection.Capabilities.ConfigSwitcher == nil || connection.Capabilities.ConfigSwitcher.Version < 3 || !containsString(connection.Capabilities.ConfigSwitcher.CLIs, "codex") {
		return &workerProfileError{status: http.StatusConflict, code: "BRIDGE_UPDATE_REQUIRED", message: "upgrade and reconnect Bridge before selecting official Codex worker models and reasoning effort"}
	}
	return nil
}

func (s *Server) handleOfficialCLIModels(w http.ResponseWriter, r *http.Request, uid string) {
	if !s.requireOfficialWorkerFeature(w, r, uid) {
		return
	}
	if r.URL.Query().Get("cli") != "codex" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_CLI", "official model selection supports codex only")
		return
	}
	agentID := r.PathValue("agentID")
	if _, ok := s.requireCLIConfigAgent(w, r, uid, agentID, "codex"); !ok {
		return
	}
	if !s.allowAuthAttempt(r, "cli-config:"+agentID, uid, cliConfigRateLimit, cliConfigRateWindow) {
		serverutil.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many model configuration requests; try again shortly")
		return
	}
	models, err := s.loadOfficialCLIModels(r.Context(), agentID)
	if err != nil {
		s.writeWorkerProfileError(w, err)
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) loadOfficialCLIModels(ctx context.Context, agentID string) ([]protocol.OfficialCLIModelMetadata, error) {
	if err := s.validateOfficialWorkerCapability(agentID); err != nil {
		return nil, err
	}
	result, err := s.sendCLIConfigRequest(ctx, agentID, protocol.TypeCLIConfigOfficialModels, protocol.CLIConfigRequest{CLI: "codex"})
	if err != nil {
		status, code := http.StatusBadGateway, "BRIDGE_ERROR"
		if errors.Is(err, ErrAgentOffline) {
			status, code = http.StatusConflict, "AGENT_OFFLINE"
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status, code = http.StatusGatewayTimeout, "BRIDGE_TIMEOUT"
		}
		return nil, &workerProfileError{status: status, code: code, message: "Bridge could not complete the official Codex model directory request", err: err}
	}
	if !result.OK || result.CLI != "codex" {
		return nil, &workerProfileError{status: http.StatusBadGateway, code: "OFFICIAL_MODELS_FAILED", message: "Bridge could not load the official Codex model directory"}
	}
	models, err := validateOfficialModelDirectory(result.OfficialModels)
	if err != nil {
		return nil, &workerProfileError{status: http.StatusBadGateway, code: "OFFICIAL_MODELS_FAILED", message: "Bridge returned an invalid official Codex model directory", err: err}
	}
	return models, nil
}

// The endpoint is the authority; the browser cannot provide this metadata.
// Reject ambiguous/truncated directories rather than silently accepting models.
func validateOfficialModelDirectory(raw []protocol.OfficialCLIModelMetadata) ([]protocol.OfficialCLIModelMetadata, error) {
	if len(raw) == 0 || len(raw) > protocol.MaxOfficialModels {
		return nil, errors.New("empty or oversized official model directory")
	}
	models := make([]protocol.OfficialCLIModelMetadata, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, metadata := range raw {
		metadata.Model = strings.TrimSpace(metadata.Model)
		if metadata.Model == "" || len(metadata.Model) > protocol.MaxOfficialModelIDBytes || len(metadata.DisplayName) > protocol.MaxOfficialModelDisplayNameBytes || seen[metadata.Model] || len(metadata.SupportedReasoningEfforts) > protocol.MaxOfficialReasoningEfforts {
			return nil, errors.New("invalid or repeated official model")
		}
		seen[metadata.Model] = true
		levels := make([]string, 0, len(metadata.SupportedReasoningEfforts))
		for _, rawEffort := range metadata.SupportedReasoningEfforts {
			effort := strings.TrimSpace(rawEffort)
			if effort == "" || len(effort) > protocol.MaxOfficialReasoningEffortBytes || containsString(levels, effort) {
				return nil, errors.New("invalid or repeated official reasoning effort")
			}
			levels = append(levels, effort)
		}
		metadata.SupportedReasoningEfforts = levels
		metadata.DefaultReasoningEffort = strings.TrimSpace(metadata.DefaultReasoningEffort)
		if metadata.DefaultReasoningEffort != "" && !containsString(levels, metadata.DefaultReasoningEffort) {
			return nil, errors.New("unsupported official default reasoning effort")
		}
		models = append(models, metadata)
	}
	return models, nil
}
