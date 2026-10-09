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
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.DeliveryMode == "" {
		req.DeliveryMode = "queue"
	}
	if req.MessageID == "" {
		serverutil.WriteError(w, 409, "RUN_ACTIVE", "active run requires an idempotent runtime messageId")
		return
	}
	if len(req.MessageID) > 128 || req.Prompt == "" || int64(len(req.Prompt)) > limit || (req.DeliveryMode != "queue" && req.DeliveryMode != "immediate") {
		serverutil.WriteError(w, 400, "BAD_REQUEST", "messageId, prompt and valid deliveryMode are required")
		return
	}
	// Retries return their original receipt even after the Bridge disconnects.
	if old, err := s.store.OrchestrationMessageByID(r.Context(), run.ID, req.MessageID); err == nil {
		if old.Prompt != req.Prompt || old.DeliveryMode != req.DeliveryMode {
			serverutil.WriteError(w, 409, "MESSAGE_CONFLICT", "messageId already belongs to another instruction")
			return
		}
		serverutil.WriteJSON(w, 200, map[string]any{"message": old})
		return
	}
	if run.Status == store.OrchestrationCanceling {
		serverutil.WriteError(w, 409, "RUN_STOPPING", "run is stopping")
		return
	}
	caps, online := s.pool.AgentCapabilities(run.AgentID)
	if !online || caps == nil || caps.OrchestrationLiveInput < 1 {
		serverutil.WriteError(w, 409, "LIVE_INPUT_UNAVAILABLE", "connected Bridge does not support runtime instructions")
		return
	}
	if caps.OrchestrationLiveInputCLIs != nil {
		for _, cli := range orchestrationRequiredCLIs(run.WorkerPair) {
			if !caps.OrchestrationLiveInputCLIs[cli] {
				serverutil.WriteError(w, 409, "LIVE_INPUT_UNAVAILABLE", cli+" runtime does not support the live-input adapter protocol")
				return
			}
		}
	}
	message, created, err := s.store.CreateOrchestrationMessage(r.Context(), store.OrchestrationMessage{ID: req.MessageID, RunID: run.ID, Prompt: req.Prompt, DeliveryMode: req.DeliveryMode})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			serverutil.WriteError(w, 409, "MESSAGE_CONFLICT", "messageId already used")
		} else {
			serverutil.WriteError(w, 500, "STORE_ERROR", "failed to save instruction")
		}
		return
	}
	if created {
		s.dispatchOrchestrationMessages(r.Context(), run)
	}
	if latest, err := s.store.OrchestrationMessageByID(r.Context(), run.ID, req.MessageID); err == nil {
		message = latest
	}
	serverutil.WriteJSON(w, http.StatusAccepted, map[string]any{"message": message})
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
