package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/store"
)

// Only non-secret execution choices can enter a named preset. Task text,
// credentials, privilege policy and native context identities are excluded.
var automationExecutionFields = map[string]bool{
	"mode": true, "workerPair": true, "firstCli": true, "profile": true, "verificationMode": true,
	"executionMode": true, "maxTurns": true, "workerProfilePresetIds": true, "workerProfileEfforts": true,
	"workerProfileModels": true, "nativeContextCompaction": true, "monitoringMode": true,
}

type automationRunArguments struct {
	orchestrationCreateRequest
	EndpointID        string `json:"endpointId"`
	ExecutionPresetID string `json:"executionPresetId"`
	ClientRequestID   string `json:"clientRequestId"`
	RunID             string `json:"runId"`
	Operation         string `json:"operation"`
	MessageID         string `json:"messageId"`
	DeliveryMode      string `json:"deliveryMode"`
	MonitoringMode    string `json:"monitoringMode"`
}
type automationRunPlan struct {
	request        orchestrationCreateRequest
	MonitoringMode string          `json:"monitoringMode"`
	Configuration  json.RawMessage `json:"configuration"`
}

func automationObject(raw json.RawMessage) (map[string]any, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil || object == nil {
		return nil, &operationError{400, "BAD_REQUEST", "请提供 JSON 对象。"}
	}
	return object, nil
}
func automationExplicitDigest(raw json.RawMessage) (string, error) {
	object, err := automationObject(raw)
	if err != nil {
		return "", err
	}
	delete(object, "clientRequestId")
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
func automationMerge(target map[string]any, source map[string]any) {
	for key, value := range source {
		target[key] = value
	}
}
func automationText(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
func automationRequestObject(req orchestrationCreateRequest) map[string]any {
	raw, _ := json.Marshal(req)
	object, _ := automationObject(raw)
	return object
}

func (s *Server) saveAutomationPreset(ctx context.Context, uid string, args automationArguments) (any, error) {
	values, err := automationObject(args.Config)
	if err != nil {
		return nil, err
	}
	for key := range values {
		if !automationExecutionFields[key] {
			return nil, &operationError{400, "INVALID_PRESET", "执行方案不支持字段：" + key}
		}
	}
	if err = validateAutomationChoices(values); err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(values)
	var parsed automationRunArguments
	if json.Unmarshal(encoded, &parsed) != nil {
		return nil, &operationError{400, "INVALID_PRESET", "执行方案字段类型无效。"}
	}
	if err = s.validateAutomationPresetModels(ctx, uid, parsed.orchestrationCreateRequest); err != nil {
		return nil, err
	}

	// Preset references remain subject to endpoint credential/capability checks at
	// preview/start. Saving this object never applies a native CLI configuration.
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return s.store.SaveAutomationPreset(ctx, store.AutomationPreset{ID: args.ID, UserID: uid, Name: args.Name, Config: raw}, args.ExpectedVersion)
}
func automationWorkerSlots(pair string) map[string]string {
	switch protocol.NormalizeOrchestrationWorkerPair(pair) {
	case protocol.WorkerPairCodexCodex:
		return map[string]string{"codex-a": "codex", "codex-b": "codex"}
	case protocol.WorkerPairClaudeClaude:
		return map[string]string{"claude-a": "claude", "claude-b": "claude"}
	default:
		return map[string]string{"claude": "claude", "codex": "codex"}
	}
}
func (s *Server) validateAutomationPresetModels(ctx context.Context, uid string, req orchestrationCreateRequest) error {
	fail := func(message string) error { return &operationError{400, "INVALID_PRESET", message} }
	slots := automationWorkerSlots(req.WorkerPair)
	if len(req.WorkerProfilePresetIDs) == 0 {
		if len(req.WorkerProfileModels) > 0 || len(req.WorkerProfileEfforts) > 0 {
			return fail("指定模型或思考强度时，需要完整的槽位模型配置。")
		}
		return nil
	}
	for slot := range slots {
		if req.WorkerProfilePresetIDs[slot] == "" {
			return fail("缺少槽位 " + slot + " 的模型配置。")
		}
	}
	models, err := normalizeOfficialWorkerModels(req.WorkerProfileModels, req.WorkerProfilePresetIDs)
	if err != nil {
		return fail(err.Error())
	}
	for slot, effort := range req.WorkerProfileEfforts {
		if slots[slot] == "" || len(effort) > protocol.MaxOfficialReasoningEffortBytes {
			return fail("思考强度的槽位或长度无效。")
		}
	}
	for slot, id := range req.WorkerProfilePresetIDs {
		if slots[slot] == "" || id == "" || len(id) > 240 {
			return fail("模型配置槽位或引用无效。")
		}
		if id == officialLoginPresetID {
			if req.WorkerProfileEfforts[slot] != "" && (!officialCodexWorkerSlot(slot) || models[slot] == "") {
				return fail("选择官方思考强度时，请明确指定官方 Codex 模型。")
			}
			continue
		}
		preset, err := s.store.CLIConfigPresetByID(ctx, id, uid, "")
		if err != nil {
			return err
		}
		if preset.CLI != slots[slot] {
			return fail("槽位 " + slot + " 的模型类型不匹配。")
		}
		normalizeReviewedPreset(&preset)
		if effort := req.WorkerProfileEfforts[slot]; effort != "" && !containsString(preset.ReasoningLevels, effort) {
			return fail("模型配置不支持所选思考强度。")
		}
	}
	return nil
}
func validateAutomationChoices(values map[string]any) error {
	enums := map[string][]string{
		"workerPair": {protocol.WorkerPairClaudeCodex, protocol.WorkerPairCodexCodex, protocol.WorkerPairClaudeClaude},
		"firstCli":   {"codex", "claude"}, "mode": {"collaboration", "debate"}, "profile": {"default", "formal-proof"},
		"verificationMode": {"agent"}, "executionMode": {"bounded", "dynamic"},
		"nativeContextCompaction": {"off", "after-turn"}, "monitoringMode": {"interactive", "background-mail"},
	}
	for key, allowed := range enums {
		if raw, ok := values[key]; ok {
			value, valid := raw.(string)
			if !valid {
				return &operationError{400, "INVALID_CONFIGURATION", key + " 必须为文本。"}
			}
			if value == "" {
				continue
			}
			found := false
			for _, candidate := range allowed {
				if value == candidate {
					found = true
				}
			}
			if !found {
				if key == "verificationMode" {
					return &operationError{400, "BAD_VERIFICATION_MODE", "unsupported verification mode; choose agent verification"}
				}
				return &operationError{400, "INVALID_CONFIGURATION", "不支持的 " + key + "；请查询当前可用配置。"}
			}
		}
	}
	if raw, ok := values["maxTurns"]; ok {
		var turns int
		encoded, _ := json.Marshal(raw)
		if json.Unmarshal(encoded, &turns) != nil || (turns < 1 && !(turns == 0 && automationText(values, "executionMode") == "dynamic")) || turns > 12 {
			return &operationError{400, "INVALID_CONFIGURATION", "maxTurns 必须为 1–12 的整数。"}
		}
	}
	return nil
}
func (s *Server) resolveAutomationRun(ctx context.Context, uid string, raw json.RawMessage, previous *store.OrchestrationRun) (automationRunPlan, error) {
	explicit, err := automationObject(raw)
	if err != nil {
		return automationRunPlan{}, err
	}
	account, err := s.automationPreferences(ctx, uid, "account")
	if err != nil {
		return automationRunPlan{}, err
	}
	accountValues, _ := automationObject(account.Data)
	values := map[string]any{"mode": "collaboration", "workerPair": protocol.WorkerPairClaudeCodex, "firstCli": "claude", "profile": "default", "verificationMode": "agent", "executionMode": "bounded", "maxTurns": json.Number("2"), "nativeContextCompaction": "off", "monitoringMode": "interactive"}
	sources := map[string]any{"systemDefaults": "native CLI configuration; collaboration, claude-codex, bounded, two turns, agent verification"}
	presetID := automationText(accountValues, "defaultPresetId")
	endpoint := automationText(explicit, "endpointId")
	if endpoint == "" {
		endpoint = automationText(explicit, "agentId")
	}
	if automationText(explicit, "endpointId") != "" && automationText(explicit, "agentId") != "" && automationText(explicit, "endpointId") != automationText(explicit, "agentId") {
		return automationRunPlan{}, &operationError{400, "TARGET_CONFLICT", "endpointId 与 agentId 必须指向同一执行端点。"}
	}
	cwd := automationText(explicit, "cwd")
	if previous != nil {
		if endpoint != "" && endpoint != previous.AgentID {
			return automationRunPlan{}, &operationError{409, "TARGET_CONFLICT", "继续任务必须使用原来的执行端点。"}
		}
		previousCWD := previous.CWD
		if previousCWD == "" {
			previousCWD = previous.RunCWD
		}
		if cwd != "" && cwd != previousCWD {
			return automationRunPlan{}, &operationError{409, "WORKSPACE_CONFLICT", "继续任务必须保留原工作目录。"}
		}
		endpoint = previous.AgentID
		cwd = previous.CWD
		if cwd == "" {
			cwd = previous.RunCWD
		}
		presetID = ""
		automationMerge(values, automationRequestObject(orchestrationCreateRequest{AgentID: previous.AgentID, CWD: previous.CWD, Mode: previous.Mode, WorkerPair: previous.WorkerPair, FirstCLI: previous.FirstCLI, Profile: previous.Profile, VerificationMode: previous.VerificationMode, ExecutionMode: previous.ExecutionMode, NativeContextCompaction: previous.NativeContextCompaction, MaxTurns: previous.MaxTurns}))
		sources["continuation"] = "saved run settings and worker bindings"
	} else {
		if endpoint == "" {
			endpoint = automationText(accountValues, "defaultEndpointId")
		}
		if mode := automationText(accountValues, "monitoringMode"); mode != "" {
			values["monitoringMode"] = mode
			sources["monitoringMode"] = "account"
		}
	}
	if endpoint == "" {
		agents, err := s.visibleAgents(ctx, uid)
		if err != nil {
			return automationRunPlan{}, err
		}
		candidates := []string{}
		for _, agent := range agents {
			if s.pool.AgentOnline(agent.ID) {
				candidates = append(candidates, agent.ID)
			}
		}
		if len(candidates) != 1 {
			return automationRunPlan{}, &operationError{409, "TARGET_AMBIGUOUS", "请明确选择自己的在线执行端点，或先设置默认执行端点。"}
		}
		endpoint = candidates[0]
		sources["endpointId"] = "only online owned endpoint"
	}
	agent, err := s.visibleAgentByID(ctx, uid, endpoint)
	if err != nil {
		return automationRunPlan{}, err
	}
	caps, online := s.pool.AgentCapabilities(endpoint)
	if !online || caps == nil {
		return automationRunPlan{}, &operationError{409, "ENDPOINT_OFFLINE", "执行端点离线。请先连接自己的机器，再提交任务。"}
	}
	if caps.AutomationInteractions < 1 {
		return automationRunPlan{}, &operationError{409, "UPGRADE_BRIDGE_REQUIRED", "请升级并重连 Bridge，以启用自动化交互及内部工作器隔离。"}
	}
	if cwd == "" {
		if len(agent.WorkingDirs) != 1 {
			return automationRunPlan{}, &operationError{409, "WORKSPACE_AMBIGUOUS", "请提供执行机器上的工作目录 cwd。"}
		}
		cwd = agent.WorkingDirs[0]
		sources["cwd"] = "only advertised working directory"
	}
	if previous == nil {
		workspace, err := s.automationPreferences(ctx, uid, "workspace:"+endpoint+":"+cwd)
		if err != nil {
			return automationRunPlan{}, err
		}
		workspaceValues, _ := automationObject(workspace.Data)
		if id := automationText(workspaceValues, "defaultPresetId"); id != "" {
			presetID = id
			sources["preset"] = "workspace"
		}
		if mode := automationText(workspaceValues, "monitoringMode"); mode != "" {
			values["monitoringMode"] = mode
			sources["monitoringMode"] = "workspace"
		}
	}
	if id := automationText(explicit, "executionPresetId"); id != "" {
		presetID = id
		sources["preset"] = "explicit"
	}
	if presetID != "" {
		preset, err := s.store.GetAutomationPreset(ctx, uid, presetID)
		if err != nil {
			return automationRunPlan{}, err
		}
		configValues, err := automationObject(preset.Config)
		if err != nil {
			return automationRunPlan{}, err
		}
		automationMerge(values, configValues)
		sources["executionPresetId"] = preset.ID
		sources["executionPresetVersion"] = preset.Version
	}
	if automationText(explicit, "executionPresetId") == "" && previous == nil {
		monitoring := automationText(accountValues, "monitoringMode")
		workspace, workspaceErr := s.automationPreferences(ctx, uid, "workspace:"+endpoint+":"+cwd)
		if workspaceErr != nil {
			return automationRunPlan{}, workspaceErr
		}
		workspaceValues, _ := automationObject(workspace.Data)
		if preferred := automationText(workspaceValues, "monitoringMode"); preferred != "" {
			monitoring = preferred
		}
		if monitoring != "" {
			values["monitoringMode"] = monitoring
		}
	}
	automationMerge(values, explicit)
	for field := range explicit {
		if automationExecutionFields[field] {
			sources[field] = "explicit"
		}
	}
	values["agentId"] = endpoint
	values["cwd"] = cwd
	if err = validateAutomationChoices(values); err != nil {
		return automationRunPlan{}, err
	}
	encoded, _ := json.Marshal(values)
	var args automationRunArguments
	if json.Unmarshal(encoded, &args) != nil {
		return automationRunPlan{}, &operationError{400, "INVALID_CONFIGURATION", "配置字段类型无效。"}
	}
	args.NativeUserInputClientVersion = 3
	if previous != nil && args.ExecutionMode != previous.ExecutionMode {
		return automationRunPlan{}, &operationError{409, "EXECUTION_MODE_CONFLICT", "继续任务不能改变原执行模式。"}
	}
	response := &operationValidation{}
	normalized, ok := s.normalizeOrchestrationStart(response, orchestrationStartRequest{ExecutionMode: args.ExecutionMode, NativeUserInputClientVersion: 3, AgentID: endpoint, Title: args.Title, Mode: args.Mode, WorkerPair: args.WorkerPair, FirstCLI: args.FirstCLI, Profile: args.Profile, VerificationMode: args.VerificationMode, NativeContextCompaction: args.NativeContextCompaction, Prompt: args.Prompt, CWD: cwd, MaxTurns: args.MaxTurns})
	if !ok {
		return automationRunPlan{}, response.failure()
	}
	req := args.orchestrationCreateRequest
	req.AgentID = endpoint
	req.CWD = cwd
	req.Mode = normalized.Mode
	req.WorkerPair = normalized.WorkerPair
	req.FirstCLI = normalized.FirstCLI
	req.Profile = normalized.Profile
	req.VerificationMode = normalized.VerificationMode
	req.ExecutionMode = normalized.ExecutionMode
	req.NativeContextCompaction = normalized.NativeContextCompaction
	req.MaxTurns = normalized.MaxTurns
	req.Prompt = normalized.Prompt
	req.NativeUserInputClientVersion = 3
	if normalized.ExecutionMode == "dynamic" && !s.durableTaskGraphEnabled(endpoint) {
		return automationRunPlan{}, &operationError{409, "CAPABILITY_UNAVAILABLE", "动态执行需要支持 durable task graph 的 Bridge。"}
	}
	if err = s.validateOrchestrationCapabilities(endpoint, req.WorkerPair); err != nil {
		return automationRunPlan{}, &operationError{409, "CAPABILITY_UNAVAILABLE", err.Error()}
	}
	if err = s.validateVerificationCapability(endpoint, req.Profile, req.VerificationMode); err != nil {
		return automationRunPlan{}, &operationError{409, "CAPABILITY_UNAVAILABLE", err.Error()}
	}
	if !s.authorizeOfficialWorkerRequestContext(response, ctx, uid, req) {
		return automationRunPlan{}, response.failure()
	}
	var bindings map[string]protocol.WorkerProfileBinding
	if previous != nil && req.WorkerProfilePresetIDs == nil {
		bindings, err = s.store.OrchestrationWorkerProfiles(ctx, previous.ID)
		if err == nil && bindings == nil {
			if graph, graphErr := s.store.TaskGraphByRun(ctx, previous.ID); graphErr == nil {
				var priorPayload protocol.OrchestrationStartPayload
				if json.Unmarshal([]byte(graph.PayloadJSON), &priorPayload) == nil {
					bindings = priorPayload.WorkerProfiles
				}
			}
		}
		for slot, binding := range bindings {
			if automationWorkerSlots(req.WorkerPair)[slot] != binding.CLI {
				return automationRunPlan{}, &operationError{409, "MODEL_CONFIGURATION_REQUIRED", "更换工作器组合时，请明确选择新的完整模型配置，或显式清空为原生默认配置。"}
			}
		}
	} else {
		bindings, err = s.resolveWorkerProfiles(ctx, uid, endpoint, req.WorkerPair, req.WorkerProfilePresetIDs, req.WorkerProfileEfforts, req.WorkerProfileModels)
	}
	if len(req.WorkerProfilePresetIDs) == 0 && len(req.WorkerProfileEfforts) > 0 {
		return automationRunPlan{}, &operationError{400, "MODEL_REQUIRED", "指定思考强度时需同时选择该槽位的模型配置。"}
	}
	if err != nil {
		s.writeWorkerProfileError(response, err)
		return automationRunPlan{}, response.failure()
	}
	if len(bindings) > 0 {
		if err = s.validateIsolatedWorkerProfileCapability(endpoint); err != nil {
			return automationRunPlan{}, &operationError{409, "CAPABILITY_UNAVAILABLE", err.Error()}
		}
	}
	user, err := s.store.UserByID(ctx, uid)
	if err != nil {
		return automationRunPlan{}, err
	}
	reviewRequired := !strings.EqualFold(caps.Metadata["approvalMode"], permissionProfileAutoExecute) && !strings.EqualFold(caps.Metadata["approvalMode"], permissionProfileStrictWorkspace) && !(strings.EqualFold(caps.ApprovalPolicy, "never") && strings.EqualFold(caps.Sandbox, "danger-full-access"))
	if reviewRequired {
		if caps.AutomationInteractions < 1 {
			return automationRunPlan{}, &operationError{409, "CAPABILITY_UNAVAILABLE", "此审批模式需要支持自动化审批快照的新版 Bridge。"}
		}
		for _, cli := range orchestrationRequiredCLIs(req.WorkerPair) {
			capability := caps.NativeUserInput[cli]
			if !s.featureEnabled(rollout.FeatureNativeUserInput, user) || !capability.Available || capability.Version < 3 {
				return automationRunPlan{}, &operationError{409, "CAPABILITY_UNAVAILABLE", "当前审批模式的 " + cli + " 需要已验证的原生问答 v3；请使用网站处理或升级执行端。"}
			}
		}
	}
	if args.MonitoringMode == "background-mail" {
		completion, attention := true, true
		if value, ok := accountValues["completionEnabled"].(bool); ok {
			completion = value
		}
		if value, ok := accountValues["attentionEnabled"].(bool); ok {
			attention = value
		}
		if !s.automationMailReady() || automationText(accountValues, "email") == "" || !completion || !attention {
			return automationRunPlan{}, &operationError{409, "NOTIFICATION_NOT_READY", "后台邮件模式需要管理员配置邮件服务，并在个人中心填写邮箱、开启完成和人工处理提醒。"}
		}
	}
	workers := map[string]any{}
	slots := automationWorkerSlots(req.WorkerPair)
	for slot, cli := range slots {
		binding, exists := bindings[slot]
		if !exists {
			workers[slot] = map[string]any{"cli": cli, "nativeDefault": true}
		} else {
			workers[slot] = map[string]any{"cli": cli, "nativeDefault": false, "presetId": binding.PresetID, "name": binding.Name, "officialLogin": binding.OfficialLogin, "model": binding.Model, "reasoningEffort": binding.ReasoningEffort}
		}
	}
	snapshot := automationRequestObject(req)
	delete(snapshot, "prompt")
	delete(snapshot, "files")
	delete(snapshot, "nativeUserInputClientVersion")
	snapshot["schemaVersion"] = 1
	snapshot["endpointId"] = endpoint
	snapshot["monitoringMode"] = args.MonitoringMode
	snapshot["workers"] = workers
	snapshot["sources"] = sources
	configuration, _ := json.Marshal(snapshot)
	return automationRunPlan{request: req, MonitoringMode: args.MonitoringMode, Configuration: configuration}, nil
}
func (s *Server) previewAutomationRun(ctx context.Context, uid string, raw json.RawMessage) (any, error) {
	plan, err := s.resolveAutomationRun(ctx, uid, raw, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schemaVersion": 1, "configuration": plan.Configuration, "monitoringMode": plan.MonitoringMode, "startsWork": false, "workspaceValidation": "Bridge validates the actual directory when work starts"}, nil
}
func (s *Server) automationSubmissionResponse(ctx context.Context, uid string, sub store.AutomationSubmission, replayed bool) (any, error) {
	if latest, err := s.store.AutomationSubmissionByID(ctx, uid, sub.ID); err == nil {
		sub = latest
	}
	result := map[string]any{"schemaVersion": 1, "submissionId": sub.ID, "runId": sub.RunID, "state": sub.State, "promptSeq": sub.PromptSeq, "configuration": sub.Configuration, "monitoringMode": sub.MonitoringMode, "replayed": replayed}
	if base, err := s.automationPublicURL(); err == nil && sub.RunID != "" {
		result["resultUrl"] = base + "/orchestrate/runs/" + sub.RunID
	}
	if sub.Result != nil {
		result["result"] = sub.Result
	}
	return result, nil
}
func (s *Server) startAutomationRun(ctx context.Context, uid string, raw json.RawMessage, runID string) (any, error) {
	var args automationRunArguments
	if json.Unmarshal(raw, &args) != nil || args.ClientRequestID == "" || len(args.ClientRequestID) > 240 {
		return nil, &operationError{400, "REQUEST_KEY_REQUIRED", "请提供稳定的 clientRequestId；重试时保留此值。"}
	}
	digest, err := automationExplicitDigest(raw)
	if err != nil {
		return nil, err
	}
	operation := "start_run"
	if runID != "" {
		operation = "follow_up"
	}
	// This precedes default/model/endpoint resolution. Changing a default after
	// acceptance cannot turn a retry into a new task or a false conflict.
	if prior, err := s.store.AutomationSubmissionByRequest(ctx, uid, operation, args.ClientRequestID); err == nil {
		if prior.RequestDigest != digest {
			return nil, &operationError{409, "REQUEST_CONFLICT", "此请求键已用于不同的任务内容。请保留原键查询原任务，或为新任务使用新键。"}
		}
		return s.automationSubmissionResponse(ctx, uid, prior, true)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var previous *store.OrchestrationRun
	if runID != "" {
		run, err := s.store.OrchestrationRunByID(ctx, runID, uid)
		if err != nil {
			return nil, err
		}
		if !orchestrationTerminalStatus(run.Status) {
			return nil, &operationError{409, "RUN_ACTIVE", "任务仍在执行；请使用 live_instruction 发送实时指令。"}
		}
		previous = &run
	}
	plan, err := s.resolveAutomationRun(ctx, uid, raw, previous)
	if err != nil {
		return nil, err
	}
	sub, created, err := s.store.ReserveAutomationSubmission(ctx, uid, operation, args.ClientRequestID, digest, plan.MonitoringMode, plan.Configuration)
	if errors.Is(err, store.ErrConflict) {
		return nil, &operationError{409, "REQUEST_CONFLICT", "此请求键已用于不同的任务内容。"}
	}
	if err != nil {
		return nil, err
	}
	if !created {
		return s.automationSubmissionResponse(ctx, uid, sub, true)
	}
	// After durable reservation, request cancellation is only cancellation of
	// observation. It must not tear down an accepted task or force a second run.
	dispatchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if previous == nil {
		_, err = s.createOrchestrationOperation(dispatchCtx, uid, plan.request, sub.ID)
	} else {
		s.orchestrationInputMu.Lock()
		_, err = s.continueOrchestrationOperation(dispatchCtx, uid, *previous, plan.request, sub.ID)
		s.orchestrationInputMu.Unlock()
	}
	if err != nil {
		current, readErr := s.store.AutomationSubmissionByID(dispatchCtx, uid, sub.ID)
		if readErr == nil && current.RunID != "" {
			_ = s.store.SetAutomationSubmissionState(dispatchCtx, uid, sub.ID, "dispatch_unconfirmed")
			return s.automationSubmissionResponse(dispatchCtx, uid, current, false)
		}
		_ = s.store.SetAutomationSubmissionState(dispatchCtx, uid, sub.ID, "rejected")
		return nil, err
	}
	return s.automationSubmissionResponse(dispatchCtx, uid, sub, false)
}
func (s *Server) continueAutomationRun(ctx context.Context, uid string, raw json.RawMessage) (any, error) {
	var args automationRunArguments
	if json.Unmarshal(raw, &args) != nil || args.RunID == "" {
		return nil, &operationError{400, "BAD_REQUEST", "请提供 runId 和明确的继续操作。"}
	}
	switch args.Operation {
	case "follow_up":
		return s.startAutomationRun(ctx, uid, raw, args.RunID)
	case "live_instruction":
		object, err := automationObject(raw)
		if err != nil {
			return nil, err
		}
		allowed := map[string]bool{"runId": true, "operation": true, "messageId": true, "clientRequestId": true, "prompt": true, "deliveryMode": true}
		for field := range object {
			if !allowed[field] {
				return nil, &operationError{400, "INVALID_LIVE_INSTRUCTION", "实时指令不能改变执行配置；请只提供原任务、messageId、prompt 和 deliveryMode。"}
			}
		}
		s.orchestrationInputMu.Lock()
		defer s.orchestrationInputMu.Unlock()
		run, err := s.store.OrchestrationRunByID(ctx, args.RunID, uid)
		if err != nil {
			return nil, err
		}
		if _, lookupErr := s.store.OrchestrationMessageByID(ctx, run.ID, args.MessageID); errors.Is(lookupErr, store.ErrNotFound) {
			caps, online := s.pool.AgentCapabilities(run.AgentID)
			if !online || caps == nil {
				return nil, &operationError{409, "ENDPOINT_OFFLINE", "执行端点离线。"}
			}
			if caps.AutomationInteractions < 1 {
				return nil, &operationError{409, "UPGRADE_BRIDGE_REQUIRED", "请升级 Bridge，以启用自动化交互及内部工作器隔离。"}
			}
		} else if lookupErr != nil {
			return nil, lookupErr
		}
		message, _, err := s.orchestrationMessageOperation(ctx, run, args.MessageID, args.Prompt, args.DeliveryMode)
		if err != nil {
			return nil, err
		}
		return map[string]any{"schemaVersion": 1, "runId": run.ID, "message": message}, nil
	default:
		return nil, &operationError{http.StatusBadRequest, "OPERATION_REQUIRED", "operation 必须为 follow_up 或 live_instruction；回答原生问题请使用交互工具。"}
	}
}
