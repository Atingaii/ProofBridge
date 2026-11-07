package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

type automationToolDefinition struct {
	name, description, fields, required string
	write                               bool
}

var automationTools = []automationToolDefinition{
	{"get_context", "Read your account, feature gates, defaults and mail delivery mode before using ProofBridge. Preview mail stays local and never reaches an external mailbox.", "", "", false},
	{"list_endpoints", "Find your connected machines, workspaces and actual execution capabilities.", "online cli", "", false},
	{"list_model_options", "List your saved model presets, native-login options and supported worker pairs for an owned endpoint.", "endpointId cli", "endpointId", false},
	{"list_execution_presets", "List your named orchestration configurations; no credentials are returned.", "", "", false},
	{"save_execution_preset", "Create or update your named execution configuration without starting work.", "id name config expectedVersion", "name config expectedVersion", true},
	{"delete_execution_preset", "Delete your execution preset. Existing run snapshots are retained.", "id expectedVersion", "id expectedVersion", true},
	{"get_preferences", "Read account or workspace defaults and notification preferences.", "scope endpointId cwd", "", false},
	{"update_preferences", "Save defaults only when the user asks to remember them. Does not edit native CLI settings.", "scope data expectedVersion", "data expectedVersion", true},
	{"preview_run", "Resolve endpoint, model configuration and monitoring before starting. Does not execute project commands.", "endpointId agentId cwd executionPresetId prompt mode workerPair firstCli profile verificationMode executionMode maxTurns workerProfilePresetIds workerProfileEfforts workerProfileModels nativeContextCompaction monitoringMode", "prompt", false},
	{"start_run", "Start verification on your connected Bridge. Use one stable clientRequestId for retries; returns immediately.", "clientRequestId endpointId agentId cwd executionPresetId prompt title mode workerPair firstCli profile verificationMode executionMode maxTurns workerProfilePresetIds workerProfileEfforts workerProfileModels nativeContextCompaction monitoringMode", "clientRequestId prompt", true},
	{"continue_run", "Continue the SAME run: explicit follow_up after it ended, or live_instruction while active. Never answer native questions with this tool.", "runId operation clientRequestId messageId deliveryMode prompt endpointId agentId cwd executionPresetId mode workerPair firstCli profile verificationMode maxTurns workerProfilePresetIds workerProfileEfforts workerProfileModels monitoringMode", "runId operation prompt", true},
	{"list_runs", "Read your task history using bounded filters and a continuation cursor.", "endpointId status query cursor limit", "", false},
	{"get_run", "Read current state or the immutable result of a particular submission; completed is not verification pass.", "runId submissionId", "runId", false},
	{"get_run_events", "Read a bounded page of task events. Follow nextSeq and hasMore; do not infer pending native questions from logs.", "runId afterSeq limit", "runId", false},
	{"wait_run", "Observe a run for at most twenty seconds; timeout or disconnect never cancels it.", "runId submissionId afterSeq timeoutMs", "runId", false},
	{"cancel_run", "Request cancellation of your task. Cancellation is asynchronous and can already be complete.", "runId clientRequestId", "runId", true},
	{"get_run_interactions", "Read authoritative approval/question snapshots from the live Bridge. Does not reconcile or fence old decisions.", "runId", "runId", false},
	{"respond_run_interaction", "Answer or decide the SAME native request, or explicitly reconcile pending delivery. Reconcile may fence old IDs. Never guess user answers.", "runId operation kind requestId instanceId executionId decisionId decision submissionId answers pendingDecisions pendingSubmissions", "runId operation kind", true},
	{"get_run_statistics", "Read recorded task usage, duration and estimates. Estimates are not invoices; missing usage remains incomplete.", "runId submissionId", "runId", false},
	{"get_notifications", "Read your notification attempts and safe errors. Sent means SMTP acceptance, not inbox delivery; the reported mode describes the current transport, not historical attempts.", "runId submissionId limit", "", false},
}

func automationInputSchema(d automationToolDefinition) map[string]any {
	props := map[string]any{}
	for _, key := range strings.Fields(d.fields) {
		typ := "string"
		switch key {
		case "expectedVersion", "maxTurns", "afterSeq", "limit", "timeoutMs":
			typ = "integer"
		case "online", "completionEnabled", "attentionEnabled":
			typ = "boolean"
		case "config", "data", "workerProfilePresetIds", "workerProfileEfforts", "workerProfileModels", "answers":
			typ = "object"
		case "pendingDecisions", "pendingSubmissions":
			typ = "array"
		}
		p := map[string]any{"type": typ}
		if typ == "array" {
			p["items"] = map[string]any{"type": "object"}
		}
		switch key {
		case "config":
			p = automationInputSchema(automationToolDefinition{fields: "mode workerPair firstCli profile verificationMode executionMode maxTurns workerProfilePresetIds workerProfileEfforts workerProfileModels nativeContextCompaction monitoringMode"})
		case "data":
			p = automationInputSchema(automationToolDefinition{fields: "email completionEnabled attentionEnabled defaultEndpointId defaultPresetId monitoringMode"})
		case "workerProfilePresetIds", "workerProfileEfforts", "workerProfileModels":
			p["additionalProperties"] = map[string]any{"type": "string"}
			p["description"] = "Map by worker slot: claude/codex for claude-codex, codex-a/codex-b for codex-codex, claude-a/claude-b for claude-claude. Use only options returned by list_model_options; preset ID __official__ selects native login."
		case "answers":
			p["description"] = "Map from the authoritative question ID to the user's selected option IDs or custom text; never guess answers."
			p["additionalProperties"] = map[string]any{"type": "object", "properties": map[string]any{"optionIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "customText": map[string]any{"type": "string"}}, "additionalProperties": false}
		case "pendingDecisions", "pendingSubmissions":
			identity := "decisionId"
			if key == "pendingSubmissions" {
				identity = "submissionId"
			}
			p["items"] = automationInputSchema(automationToolDefinition{fields: "requestId executionId " + identity, required: "requestId executionId " + identity})
			p["description"] = "Explicit reconciliation only; copy original pending identities. Read-only snapshots must not send these."
		case "clientRequestId":
			p["description"] = "Stable unique key for this intended action; repeat the exact same key and arguments after a lost reply."
			p["maxLength"] = 240
		case "cwd":
			p["description"] = "Absolute project directory on the selected Bridge machine, not on Hub."
		case "scope":
			p["description"] = "account or workspace:<endpointId>:<cwd>; workspace values override account defaults."
		case "timeoutMs":
			p["minimum"], p["maximum"] = 0, 20000
		case "expectedVersion", "afterSeq":
			p["minimum"] = 0
		case "operation":
			if d.name == "continue_run" {
				p["enum"] = []string{"follow_up", "live_instruction"}
			} else {
				p["enum"] = []string{"decide", "answer", "reconcile"}
			}
		case "kind":
			p["enum"] = []string{"approval", "question"}
		case "decision":
			p["enum"] = []string{"accept", "decline", "cancel"}
		case "monitoringMode":
			p["enum"] = []string{"interactive", "background-mail"}
		case "workerPair":
			p["enum"] = []string{"claude-codex", "codex-codex", "claude-claude"}
		case "firstCli", "cli":
			p["enum"] = []string{"codex", "claude"}
		case "mode":
			p["enum"] = []string{"collaboration", "debate"}
		case "profile":
			p["enum"] = []string{"default", "formal-proof"}
		case "verificationMode":
			p["enum"] = []string{"agent"}
		case "executionMode":
			p["enum"] = []string{"bounded", "dynamic"}
		case "nativeContextCompaction":
			p["enum"] = []string{"off", "after-turn"}
		case "deliveryMode":
			p["enum"] = []string{"queue", "immediate"}
		}
		props[key] = p
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if required := strings.Fields(d.required); len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
func (s *Server) newAutomationMCPHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "proofbridge", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: "Use get_context, owned endpoints, current model options and preferences before starting. User work executes on the selected Bridge, not Hub. Preserve runId and use a stable clientRequestId for retries. background-mail requires completion and attention notifications; interactive uses bounded wait. mailDeliveryMode=preview only stores mail in a local preview inbox and never delivers to an external mailbox. Sent means SMTP acceptance, not confirmed inbox receipt. Read independent verification separately from execution status. Never recursively invoke ProofBridge from a managed worker.",
	})
	for _, def := range automationTools {
		d := def
		open := d.name == "start_run" || d.name == "continue_run"
		destructive := d.write
		server.AddTool(&mcp.Tool{Name: "proofbridge_" + d.name, Description: d.description, InputSchema: automationInputSchema(d), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !d.write, IdempotentHint: !d.write || d.name == "start_run" || d.name == "continue_run" || d.name == "cancel_run", OpenWorldHint: &open, DestructiveHint: &destructive}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if req.Extra == nil || req.Extra.TokenInfo == nil || req.Extra.TokenInfo.UserID == "" {
				return automationMCPResult(nil, &operationError{401, "UNAUTHORIZED", "MCP 身份缺失。"}), nil
			}
			value, err := s.callAutomationTool(ctx, req.Extra.TokenInfo.UserID, d.name, req.Params.Arguments)
			return automationMCPResult(value, err), nil
		})
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	auth := mcpauth.RequireBearerToken(func(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if !strings.HasPrefix(token, "pb_mcp_") {
			return nil, mcpauth.ErrInvalidToken
		}
		row, err := s.store.AutomationTokenByHash(ctx, automationTokenHash(token))
		if errors.Is(err, store.ErrNotFound) || err == nil && row.RevokedAt != 0 {
			return nil, mcpauth.ErrInvalidToken
		}
		if err != nil {
			return nil, fmt.Errorf("authentication temporarily unavailable")
		}
		if _, err = s.store.UserByID(ctx, row.UserID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, mcpauth.ErrInvalidToken
			}
			return nil, fmt.Errorf("authentication temporarily unavailable")
		}
		return &mcpauth.TokenInfo{UserID: row.UserID, Scopes: []string{"read", "execute", "preferences", "interact"}}, nil
	}, &mcpauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(transport)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.cfg.Automation.Enabled {
			serverutil.WriteError(w, 503, "AUTOMATION_DISABLED", "自动化入口未启用。")
			return
		}
		if !s.checkOrigin(r) {
			serverutil.WriteError(w, 403, "ORIGIN_DENIED", "请求来源不允许。")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		auth.ServeHTTP(w, r)
	})
}
func automationMCPResult(value any, err error) *mcp.CallToolResult {
	if err != nil {
		code, message := "OPERATION_FAILED", "操作暂时无法完成。请稍后重试查询；已提交的任务不会因此取消。"
		var op *operationError
		switch {
		case errors.As(err, &op):
			code, message = op.Code, op.Message
		case errors.Is(err, store.ErrNotFound):
			code, message = "NOT_FOUND", "找不到本人可访问的资源。"
		case errors.Is(err, store.ErrConflict):
			code, message = "REQUEST_CONFLICT", "请求键已用于其他内容，或配置版本已改变。请查询原提交或重新读取配置。"
		case errors.Is(err, store.ErrInvalidOrchestrationHistory):
			code, message = "BAD_QUERY", "历史筛选条件或游标无效，请重新读取第一页。"
		case errors.Is(err, context.Canceled):
			code, message = "OBSERVATION_CANCELED", "本次查询已结束；任务不会因此取消。"
		case errors.Is(err, context.DeadlineExceeded):
			code, message = "OBSERVATION_TIMEOUT", "本次查询超时，请使用同一任务编号继续查询。"
		}
		// Interaction adapters use the same stable error names while preserving
		// their existing native delivery semantics.
		if code == "OPERATION_FAILED" {
			prefix, detail, found := strings.Cut(err.Error(), ": ")
			switch prefix {
			case "INTERACTION_UNAVAILABLE", "DELIVERY_UNKNOWN", "REQUEST_CONFLICT", "BAD_REQUEST", "CAPABILITY_UNAVAILABLE", "RUN_ENDED", "NOT_FOUND", "STALE_INTERACTION":
				if found {
					code, message = prefix, detail
				}
			}
		}
		if len(message) > 1000 {
			message = message[:1000]
		}
		value = map[string]any{"schemaVersion": 1, "error": map[string]string{"code": code, "message": message}}
	}
	if value == nil {
		value = map[string]any{"ok": true}
	}
	raw, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Result encoding failed"}}}
	}
	return &mcp.CallToolResult{IsError: err != nil, StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}

type automationArguments struct {
	EndpointID      string          `json:"endpointId"`
	CWD             string          `json:"cwd"`
	CLI             string          `json:"cli"`
	Online          *bool           `json:"online"`
	Scope           string          `json:"scope"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	ExpectedVersion int64           `json:"expectedVersion"`
	Config          json.RawMessage `json:"config"`
	Data            json.RawMessage `json:"data"`
	RunID           string          `json:"runId"`
	SubmissionID    string          `json:"submissionId"`
	Status          string          `json:"status"`
	Query           string          `json:"query"`
	Cursor          string          `json:"cursor"`
	Limit           int             `json:"limit"`
	AfterSeq        int64           `json:"afterSeq"`
	TimeoutMS       int             `json:"timeoutMs"`
}

func (s *Server) callAutomationTool(ctx context.Context, uid, name string, raw json.RawMessage) (any, error) {
	if err := validateAutomationToolArguments(name, raw); err != nil {
		return nil, err
	}
	var a automationArguments
	if len(raw) > 0 && json.Unmarshal(raw, &a) != nil {
		return nil, &operationError{400, "BAD_REQUEST", "工具参数必须为 JSON 对象。"}
	}
	switch name {
	case "get_context":
		user, err := s.store.UserByID(ctx, uid)
		if err != nil {
			return nil, err
		}
		user = s.decorateUser(user)
		prefs, err := s.automationPreferences(ctx, uid, "account")
		if err != nil {
			return nil, err
		}
		var data map[string]any
		_ = json.Unmarshal(prefs.Data, &data)
		email, _ := data["email"].(string)
		return map[string]any{"schemaVersion": 1, "user": user, "preferences": prefs, "mailReady": s.automationMailReady(), "mailDeliveryMode": s.automationMailDeliveryMode(), "emailConfigured": email != "", "nativeUserInputProtocolVersion": 3, "maximumWaitMs": 20000}, nil
	case "list_endpoints":
		agents, err := s.visibleAgents(ctx, uid)
		if err != nil {
			return nil, err
		}
		out := []agentResponse{}
		for _, agent := range agents {
			info, online := s.pool.AgentConnectionInfo(agent.ID)
			if a.Online != nil && *a.Online != online {
				continue
			}
			if a.CLI != "" && (!online || info.Capabilities == nil || !info.Capabilities.Orchestration[a.CLI].Available) {
				continue
			}
			item := agentResponse{Agent: agent}
			if online {
				item.Online = true
				item.Capabilities = info.Capabilities
				item.Version = info.Version
				item.ConnectedAt = info.ConnectedAt
			}
			out = append(out, item)
		}
		return map[string]any{"endpoints": out}, nil
	case "list_model_options":
		return s.automationModelOptions(ctx, uid, a.EndpointID, a.CLI)
	case "list_execution_presets":
		rows, err := s.store.ListAutomationPresets(ctx, uid)
		return map[string]any{"presets": rows}, err
	case "save_execution_preset":
		return s.saveAutomationPreset(ctx, uid, a)
	case "delete_execution_preset":
		return map[string]bool{"ok": true}, s.store.DeleteAutomationPreset(ctx, uid, a.ID, a.ExpectedVersion)
	case "get_preferences":
		if a.Scope == "" && (a.EndpointID == "") != (a.CWD == "") {
			return nil, &operationError{400, "INVALID_SCOPE", "读取项目偏好时请同时提供 endpointId 和 cwd。"}
		}
		if a.Scope == "" && a.EndpointID != "" && a.CWD != "" {
			a.Scope = "workspace:" + a.EndpointID + ":" + a.CWD
		}
		if err := s.validateAutomationScope(ctx, uid, a.Scope); err != nil {
			return nil, err
		}
		return s.automationPreferences(ctx, uid, a.Scope)
	case "update_preferences":
		return s.putAutomationPreferences(ctx, uid, a.Scope, a.Data, a.ExpectedVersion)
	case "preview_run":
		return s.previewAutomationRun(ctx, uid, raw)
	case "start_run":
		return s.startAutomationRun(ctx, uid, raw, "")
	case "continue_run":
		return s.continueAutomationRun(ctx, uid, raw)
	case "list_runs":
		page, err := s.store.ListOrchestrationHistory(ctx, uid, store.OrchestrationHistoryFilter{AgentID: a.EndpointID, Query: a.Query, Status: a.Status, Archived: "all", Limit: a.Limit, Cursor: a.Cursor})
		if err != nil {
			return nil, err
		}
		runs := make([]map[string]any, 0, len(page.Runs))
		for _, run := range page.Runs {
			runs = append(runs, automationPublicRun(run))
		}
		return map[string]any{"runs": runs, "nextCursor": page.NextCursor}, nil
	case "get_run":
		return s.automationRunResult(ctx, uid, a.RunID, a.SubmissionID)
	case "get_run_events":
		return s.automationRunEvents(ctx, uid, a.RunID, a.AfterSeq, a.Limit)
	case "wait_run":
		return s.waitAutomationRun(ctx, uid, a)
	case "cancel_run":
		return s.cancelAutomationRun(ctx, uid, a.RunID)
	case "get_run_interactions":
		return s.automationGetInteractions(ctx, uid, a.RunID)
	case "respond_run_interaction":
		return s.automationRespondInteraction(ctx, uid, raw)
	case "get_run_statistics":
		return s.automationRunStatistics(ctx, uid, a.RunID, a.SubmissionID)
	case "get_notifications":
		if a.RunID != "" {
			if _, err := s.store.OrchestrationRunByID(ctx, a.RunID, uid); err != nil {
				return nil, err
			}
		}
		rows, err := s.store.ListAutomationNotifications(ctx, uid, a.RunID, a.SubmissionID, a.Limit)
		return s.automationNotificationStatus(rows), err
	default:
		return nil, &operationError{400, "UNKNOWN_TOOL", "未知的 ProofBridge 工具。"}
	}
}

func (s *Server) automationModelOptions(ctx context.Context, uid, endpoint, cli string) (any, error) {
	if endpoint == "" {
		return nil, &operationError{400, "TARGET_REQUIRED", "请先选择自己的执行端点。"}
	}
	agent, err := s.visibleAgentByID(ctx, uid, endpoint)
	if err != nil {
		return nil, err
	}
	info, online := s.pool.AgentConnectionInfo(endpoint)
	presets, err := s.store.ListCLIConfigPresets(ctx, uid, endpoint)
	if err != nil {
		return nil, err
	}
	filtered := []store.CLIConfigPreset{}
	for _, p := range presets {
		if cli == "" || p.CLI == cli {
			normalizeReviewedPreset(&p)
			filtered = append(filtered, p)
		}
	}
	pairs := []string{}
	if online && info.Capabilities != nil {
		for _, pair := range []string{protocol.WorkerPairClaudeCodex, protocol.WorkerPairCodexCodex, protocol.WorkerPairClaudeClaude} {
			if len(missingOrchestrationCLIs(info.Capabilities, orchestrationRequiredCLIs(pair))) == 0 {
				pairs = append(pairs, pair)
			}
		}
	}
	result := map[string]any{"endpoint": agent, "online": online, "presets": filtered, "workerPairs": pairs, "nativeDefaultAvailable": online, "officialPresetId": officialLoginPresetID}
	if online {
		result["capabilities"] = info.Capabilities
	}
	user, err := s.store.UserByID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if online && s.featureEnabled(rollout.FeatureOfficialCodexWorkerModels, user) && s.validateOfficialWorkerCapability(endpoint) == nil {
		models, err := s.loadOfficialCLIModels(ctx, endpoint)
		if err != nil {
			result["officialModelError"] = "官方模型目录暂不可查询；不能据公共目录推断可用性。"
		} else {
			result["officialCodexModels"] = models
		}
	}
	return result, nil
}
