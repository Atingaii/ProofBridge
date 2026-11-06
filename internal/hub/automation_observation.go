package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/modelcatalog"
	"github.com/tencent/codex-bridge/internal/store"
)

// Public task metadata deliberately excludes native session/resume identities.
func automationPublicRun(run store.OrchestrationRun) map[string]any {
	return map[string]any{"id": run.ID, "endpointId": run.AgentID, "title": run.Title, "status": run.Status, "error": run.Error, "cwd": run.CWD, "executionMode": run.ExecutionMode, "mode": run.Mode, "workerPair": run.WorkerPair, "profile": run.Profile, "verificationMode": run.VerificationMode, "createdAt": run.CreatedAt, "updatedAt": run.UpdatedAt, "finishedAt": run.FinishedAt}
}
func (s *Server) automationRunResult(ctx context.Context, uid, runID, submissionID string) (any, error) {
	run, err := s.store.OrchestrationRunByID(ctx, runID, uid)
	if err != nil {
		return nil, err
	}
	var sub store.AutomationSubmission
	if submissionID != "" {
		sub, err = s.store.AutomationSubmissionByID(ctx, uid, submissionID)
		if err == nil && sub.RunID != runID {
			return nil, store.ErrNotFound
		}
	} else {
		sub, err = s.store.LatestAutomationSubmissionForRun(ctx, uid, runID)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if submissionID != "" && err != nil {
		return nil, err
	}
	response := map[string]any{"schemaVersion": 1, "run": automationPublicRun(run), "verification": map[string]any{"status": "unknown", "authority": "unavailable", "summary": "尚无此提交的独立验证结论。"}}
	if err == nil {
		response["submission"] = sub
		if len(sub.Result) > 0 {
			response["result"] = sub.Result
			var frozen store.AutomationResult
			if json.Unmarshal(sub.Result, &frozen) == nil {
				response["verification"] = frozen.Verification
			}
		}
	}
	if base, e := s.automationPublicURL(); e == nil {
		response["resultUrl"] = base + "/orchestrate/runs/" + url.PathEscape(runID)
	}
	return response, nil
}

type automationEventPage struct {
	Events        []store.OrchestrationEvent `json:"events"`
	NextSeq       int64                      `json:"nextSeq"`
	HasMore       bool                       `json:"hasMore"`
	TruncatedSeqs []int64                    `json:"truncatedSeqs"`
}

func (s *Server) automationRunEvents(ctx context.Context, uid, runID string, after int64, limit int) (any, error) {
	if _, err := s.store.OrchestrationRunByID(ctx, runID, uid); err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, &operationError{400, "INVALID_CURSOR", "afterSeq 不能为负数。"}
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	events, err := s.store.ListOrchestrationEventsAfter(ctx, runID, after, limit+1)
	if err != nil {
		return nil, err
	}
	page := automationEventPage{Events: []store.OrchestrationEvent{}, NextSeq: after, TruncatedSeqs: []int64{}}
	budget := 128 << 10
	for i, event := range events {
		if i >= limit {
			page.HasMore = true
			break
		}
		// Reuse the established safe projections for native resume handles and
		// unstructured event metadata. This applies even to very small events.
		event.RunEndData = publicRunEndData(event.RunEndData)
		event.Data = publicOrchestrationEventData(event.Data)
		raw, e := json.Marshal(event)
		if e != nil {
			return nil, e
		}
		truncated := false
		if len(raw) > 32<<10 {
			event.Content = automationClip(event.Content, 4000)
			event.Error = automationClip(event.Error, 1000)
			event.Data = map[string]any{"projectionTruncated": true, "originalBytes": len(raw)}
			event.CommandData = nil
			event.RunStartData = nil
			event.TurnStartData = nil
			event.TurnEndData = nil
			event.RunEndData = nil
			event.BridgeNoteData = nil
			event.RunConclusion = nil
			raw, _ = json.Marshal(event)
			truncated = true
		}
		if len(raw) > budget && len(page.Events) > 0 {
			page.HasMore = true
			break
		}
		budget -= len(raw)
		page.Events = append(page.Events, event)
		if truncated {
			page.TruncatedSeqs = append(page.TruncatedSeqs, event.Seq)
		}
		page.NextSeq = event.Seq
	}
	return page, nil
}
func automationClip(value string, limit int) string {
	r := []rune(value)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return value
}
func (s *Server) waitAutomationRun(ctx context.Context, uid string, a automationArguments) (any, error) {
	if a.TimeoutMS < 0 || a.TimeoutMS > 20000 {
		return nil, &operationError{400, "INVALID_TIMEOUT", "timeoutMs 必须在 0 到 20000 之间。"}
	}
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := s.automationRunResult(ctx, uid, a.RunID, a.SubmissionID)
		if err != nil {
			return nil, err
		}
		events, err := s.automationRunEvents(ctx, uid, a.RunID, a.AfterSeq, 50)
		if err != nil {
			return nil, err
		}
		out := result.(map[string]any)
		page := events.(automationEventPage)
		out["events"] = page
		status, _ := out["run"].(map[string]any)["status"].(string)
		sub, _ := out["submission"].(store.AutomationSubmission)
		if len(page.Events) > 0 || orchestrationTerminalStatus(status) || len(sub.Result) > 0 {
			out["timedOut"] = false
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			out["timedOut"] = true
			return out, nil
		case <-ticker.C:
		}
	}
}
func (s *Server) cancelAutomationRun(ctx context.Context, uid, runID string) (any, error) {
	s.orchestrationInputMu.Lock()
	defer s.orchestrationInputMu.Unlock()
	run, err := s.store.OrchestrationRunByID(ctx, runID, uid)
	if err != nil {
		return nil, err
	}
	if orchestrationTerminalStatus(run.Status) {
		return map[string]any{"ok": true, "status": run.Status}, nil
	}
	if run.Status == store.OrchestrationCanceling {
		s.scheduleOrchestrationCancelTimeout(run.ID, orchestrationCancelAckTimeout)
		return map[string]any{"ok": true, "status": run.Status}, nil
	}
	if !orchestrationCancelableStatus(run.Status) {
		return nil, &operationError{409, "NOT_CANCELABLE", "该任务当前无法取消。"}
	}
	// Once accepted, the durable cancellation survives an observer disconnect.
	accepted, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err = s.store.UpdateOrchestrationRunStatus(accepted, run.ID, store.OrchestrationCanceling, ""); err != nil {
		return nil, err
	}
	if err = s.persistAndDispatchOrchestrationCancel(accepted, run); err != nil {
		return nil, err
	}
	s.scheduleOrchestrationCancelTimeout(run.ID, orchestrationCancelAckTimeout)
	return map[string]any{"ok": true, "status": store.OrchestrationCanceling}, nil
}
func (s *Server) automationRunStatistics(ctx context.Context, uid, runID, submissionID string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, orchestrationStatsTimeout)
	defer cancel()
	run, err := s.store.OrchestrationRunByID(ctx, runID, uid)
	if err != nil {
		return nil, err
	}
	events, err := s.store.ListOrchestrationUsageTimeline(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	ledger, err := s.store.ListOrchestrationUsageEvents(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	syncs, err := s.store.ListOrchestrationUsageSyncs(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	graphs, err := s.store.ListTaskGraphMetadataByRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	events = enrichVerifierUsage(enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(events, graphs), ledger), graphs)
	stats := buildOrchestrationRunStatsWithLedger(run, events, ledger, syncs)
	stats.Tasks = buildOrchestrationTaskStats(run, events, ledger, syncs, graphs)
	stats.ModelRecoveryStatus = s.modelRecoveryStatus(run.AgentID, planningUsageRecoveryHints(events, graphs), syncs, time.Now().Unix())
	stats.CatalogUpdatedAt = modelcatalog.Default.UpdatedAt()
	if submissionID != "" {
		sub, err := s.store.AutomationSubmissionByID(ctx, uid, submissionID)
		if err != nil {
			return nil, err
		}
		if sub.RunID != runID {
			return nil, store.ErrNotFound
		}
		for _, task := range stats.Tasks {
			if task.PromptSeq == sub.PromptSeq && sub.PromptSeq > 0 {
				return map[string]any{"stats": task, "submissionId": sub.ID, "source": "recorded-task-boundary", "billing": "estimates are not invoices"}, nil
			}
		}
		return map[string]any{"submissionId": sub.ID, "source": "unavailable", "incomplete": true, "reason": "No trustworthy usage boundary is recorded for this submission; run totals have not been attributed to it."}, nil
	}
	return map[string]any{"stats": stats, "source": "recorded", "billing": "estimates are not invoices"}, nil
}

func validateAutomationToolArguments(name string, raw json.RawMessage) error {
	var values map[string]json.RawMessage
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return &operationError{400, "BAD_REQUEST", "工具参数必须为 JSON 对象。"}
	}
	var definition *automationToolDefinition
	for i := range automationTools {
		if automationTools[i].name == name {
			definition = &automationTools[i]
			break
		}
	}
	if definition == nil {
		return &operationError{400, "UNKNOWN_TOOL", "未知的 ProofBridge 工具。"}
	}
	schema := automationInputSchema(*definition)
	properties := schema["properties"].(map[string]any)
	for key, value := range values {
		property, ok := properties[key]
		if !ok {
			return &operationError{400, "BAD_REQUEST", "不支持的工具参数：" + key}
		}
		kind := property.(map[string]any)["type"].(string)
		valid := false
		switch kind {
		case "string":
			var v string
			valid = string(value) != "null" && json.Unmarshal(value, &v) == nil
		case "integer":
			var v int64
			valid = string(value) != "null" && json.Unmarshal(value, &v) == nil
		case "boolean":
			var v bool
			valid = string(value) != "null" && json.Unmarshal(value, &v) == nil
		case "object":
			var v map[string]json.RawMessage
			valid = json.Unmarshal(value, &v) == nil && v != nil
		case "array":
			var v []map[string]json.RawMessage
			valid = json.Unmarshal(value, &v) == nil && v != nil
			for _, item := range v {
				valid = valid && item != nil
			}
		}
		if !valid {
			return &operationError{400, "BAD_REQUEST", key + " 的类型必须为 " + kind + "。"}
		}
	}
	for _, key := range strings.Fields(definition.required) {
		value, ok := values[key]
		if !ok || string(value) == "\"\"" {
			return &operationError{400, "BAD_REQUEST", "缺少必填参数：" + key}
		}
	}
	return nil
}
