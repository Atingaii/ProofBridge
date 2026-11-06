package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

func (s *Server) handleListOrchestrationMessages(w http.ResponseWriter, r *http.Request, uid string) {
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		serverutil.WriteError(w, 404, "NOT_FOUND", "orchestration run not found")
		return
	}
	messages, err := s.store.ListOrchestrationMessages(r.Context(), run.ID)
	if err != nil {
		serverutil.WriteError(w, 500, "STORE_ERROR", "failed to load messages")
		return
	}
	serverutil.WriteJSON(w, 200, map[string]any{"messages": messages})
}
func (s *Server) handleActiveOrchestrationMessage(w http.ResponseWriter, r *http.Request, run store.OrchestrationRun) {
	var req struct {
		MessageID    string `json:"messageId"`
		Prompt       string `json:"prompt"`
		DeliveryMode string `json:"deliveryMode"`
	}
	limit := s.cfg.Hub.MaxPromptBytes
	if limit <= 0 {
		limit = 64 * 1024
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit+4096)).Decode(&req); err != nil {
		serverutil.WriteError(w, 400, "BAD_REQUEST", "invalid instruction")
		return
	}
	message, replayed, err := s.orchestrationMessageOperation(r.Context(), run, req.MessageID, req.Prompt, req.DeliveryMode)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	serverutil.WriteJSON(w, status, map[string]any{"message": message})
}

// The browser and MCP share native capability checks and the same idempotent
// message identity. The caller holds orchestrationInputMu.
func (s *Server) orchestrationMessageOperation(ctx context.Context, run store.OrchestrationRun, messageID, prompt, deliveryMode string) (store.OrchestrationMessage, bool, error) {
	fail := func(status int, code, message string) (store.OrchestrationMessage, bool, error) {
		return store.OrchestrationMessage{}, false, &operationError{status, code, message}
	}
	limit := s.cfg.Hub.MaxPromptBytes
	if limit <= 0 {
		limit = 64 * 1024
	}
	prompt = strings.TrimSpace(prompt)
	if deliveryMode == "" {
		deliveryMode = "queue"
	}
	if messageID == "" {
		return fail(409, "RUN_ACTIVE", "active run requires an idempotent runtime messageId")
	}
	if len(messageID) > 128 || prompt == "" || int64(len(prompt)) > limit || (deliveryMode != "queue" && deliveryMode != "immediate") {
		return fail(400, "BAD_REQUEST", "messageId, prompt and valid deliveryMode are required")
	}
	if old, err := s.store.OrchestrationMessageByID(ctx, run.ID, messageID); err == nil {
		if old.Prompt != prompt || old.DeliveryMode != deliveryMode {
			return fail(409, "MESSAGE_CONFLICT", "messageId already belongs to another instruction")
		}
		return old, true, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.OrchestrationMessage{}, false, err
	}
	if orchestrationTerminalStatus(run.Status) {
		return fail(409, "RUN_ENDED", "runtime instruction target has ended; send an explicit continuation")
	}
	if run.Status == store.OrchestrationCanceling {
		return fail(409, "RUN_STOPPING", "run is stopping")
	}
	caps, online := s.pool.AgentCapabilities(run.AgentID)
	if !online || caps == nil || caps.OrchestrationLiveInput < 1 {
		return fail(409, "LIVE_INPUT_UNAVAILABLE", "connected Bridge does not support runtime instructions")
	}
	if caps.OrchestrationLiveInputCLIs != nil {
		for _, cli := range orchestrationRequiredCLIs(run.WorkerPair) {
			if !caps.OrchestrationLiveInputCLIs[cli] {
				return fail(409, "LIVE_INPUT_UNAVAILABLE", cli+" runtime does not support the live-input adapter protocol")
			}
		}
	}
	message, created, err := s.store.CreateOrchestrationMessage(ctx, store.OrchestrationMessage{ID: messageID, RunID: run.ID, Prompt: prompt, DeliveryMode: deliveryMode})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fail(409, "MESSAGE_CONFLICT", "messageId already used")
		}
		return store.OrchestrationMessage{}, false, err
	}
	if created {
		s.dispatchOrchestrationMessages(ctx, run)
	}
	if latest, err := s.store.OrchestrationMessageByID(ctx, run.ID, messageID); err == nil {
		message = latest
	}
	return message, !created, nil
}

// Dispatch only queued rows. Sending rows must never be replayed: a disconnected
// transport may have accepted the instruction before losing its receipt.
func (s *Server) dispatchOrchestrationMessages(ctx context.Context, run store.OrchestrationRun) {
	execution := run.ID
	graph, err := s.store.TaskGraphByRun(ctx, run.ID)
	if err == nil {
		execution = ""
		for _, task := range graph.Tasks {
			if task.Status == "running" || task.Status == "dispatching" {
				execution = run.ID + ":" + task.CurrentAttemptID
				break
			}
		}
		if execution == "" {
			return
		}
	}
	messages, err := s.store.ListOrchestrationMessages(ctx, run.ID)
	if err != nil {
		return
	}
	for _, m := range messages {
		if m.Status != "queued" {
			continue
		}
		claimed, err := s.store.ClaimOrchestrationMessage(ctx, run.ID, m.ID, execution)
		if err != nil || !claimed {
			continue
		}
		p := protocol.OrchestrationInputPayload{RunID: run.ID, MessageID: m.ID, ExecutionID: execution, Prompt: m.Prompt, DeliveryMode: m.DeliveryMode}
		if err := s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationInput, "", p)); err != nil {
			_, _ = s.store.FinishOrchestrationMessage(ctx, run.ID, m.ID, execution, "failed", "Bridge transport unavailable; instruction was not sent")
		}
	}
}
func (s *Server) handleOrchestrationInputStatus(ctx context.Context, agentID string, env protocol.Envelope) {
	p, err := protocol.Decode[protocol.OrchestrationInputStatusPayload](env)
	if err != nil {
		return
	}
	run, err := s.store.OrchestrationRunByIDAnyUser(ctx, p.RunID)
	if err != nil || run.AgentID != agentID {
		return
	}
	changed, err := s.store.FinishOrchestrationMessage(ctx, p.RunID, p.MessageID, p.ExecutionID, p.Status, p.Error)
	if err == nil && changed {
		s.pool.BroadcastToOrchestrationBrowsers(p.RunID, env)
	}
}

// Append accepted instructions to every subsequent worker's context. Failed
// deliveries remain visible to the user but are not silently executed later.
func (s *Server) appendOrchestrationInputContext(ctx context.Context, p *protocol.OrchestrationStartPayload) error {
	messages, err := s.store.ListOrchestrationMessages(ctx, p.RunID)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, m := range messages {
		if m.Status != "delivered" {
			continue
		}
		b.WriteString("\n\nUser instruction [" + m.ID + "]:\n" + m.Prompt)
	}
	if b.Len() > 0 {
		p.Context += "\n\nAdditional user instructions (apply in chronological order):" + b.String()
		p.TaskPrompt += "\n\nAdditional user acceptance requirements:" + b.String()
	}
	return nil
}
func (s *Server) orchestrationInputsPending(ctx context.Context, runID string) (bool, error) {
	messages, err := s.store.ListOrchestrationMessages(ctx, runID)
	if err != nil {
		return false, err
	}
	for _, m := range messages {
		if m.Status == "queued" || m.Status == "sending" {
			return true, nil
		}
	}
	return false, nil
}

// Count is a graph acceptance watermark, not a numeric execution budget.
func (s *Server) orchestrationInputVersion(ctx context.Context, runID string) (int, error) {
	messages, err := s.store.ListOrchestrationMessages(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(messages), nil
}

// Called before settling the attempt's terminal event. An absent native
// acknowledgement is uncertain, never permission to replay the instruction.
func (s *Server) finishOrchestrationMessages(ctx context.Context, runID, execution string) error {
	return s.store.FailOrchestrationMessages(ctx, runID, execution, "Execution ended without a native receipt; delivery is uncertain and will not be retried automatically")
}

// Runtime submissions are never converted to an ordinary terminal-run resume.
func (s *Server) handleTerminalOrchestrationMessage(w http.ResponseWriter, r *http.Request, run store.OrchestrationRun) bool {
	limit := s.cfg.Hub.MaxPromptBytes + s.cfg.Hub.MaxAttachmentBytes*16 + 128*1024
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		serverutil.WriteError(w, 400, "BAD_REQUEST", "invalid orchestration payload")
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		MessageID    string `json:"messageId"`
		Prompt       string `json:"prompt"`
		DeliveryMode string `json:"deliveryMode"`
	}
	if json.Unmarshal(body, &req) != nil || req.MessageID == "" {
		return false
	}
	if req.DeliveryMode == "" {
		req.DeliveryMode = "queue"
	}
	old, err := s.store.OrchestrationMessageByID(r.Context(), run.ID, req.MessageID)
	if err != nil {
		serverutil.WriteError(w, 409, "RUN_ENDED", "runtime instruction target has ended; send an explicit continuation")
		return true
	}
	if old.Prompt != strings.TrimSpace(req.Prompt) || old.DeliveryMode != req.DeliveryMode {
		serverutil.WriteError(w, 409, "MESSAGE_CONFLICT", "messageId already used")
		return true
	}
	serverutil.WriteJSON(w, 200, map[string]any{"message": old})
	return true
}
