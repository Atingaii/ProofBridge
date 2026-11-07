package hub

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// Rendering preparation is outside the terminal transaction and SMTP send. A
// failed accounting read becomes an explicit missing metric, not a failed proof
// or a reason to delay the status notification indefinitely.
func (s *Server) prepareAutomationNotification(ctx context.Context, claimed store.AutomationNotification) (store.AutomationNotification, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		row, err := s.store.ClaimedAutomationNotification(ctx, claimed.ID, claimed.ClaimToken)
		if err != nil {
			return row, err
		}
		if row.Kind != "completion" {
			return row, nil
		}
		var result store.AutomationResult
		if err = json.Unmarshal(row.Snapshot, &result); err != nil {
			return row, err
		}
		if result.UsageSummary != nil {
			return row, nil
		}
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		usage := s.automationCompletionUsage(readCtx, row, result)
		cancel()
		raw, err := s.store.FreezeAutomationNotificationUsage(ctx, row.ID, row.ClaimToken, row.Snapshot, usage)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return row, err
		}
		row.Snapshot = raw
		return row, nil
	}
	return claimed, store.ErrConflict
}

func (s *Server) automationCompletionUsage(ctx context.Context, row store.AutomationNotification, result store.AutomationResult) store.AutomationUsageSummary {
	u := store.AutomationUsageSummary{Scope: "run-cumulative-through-submission", ThroughSeq: result.UsageCutoffSeq, Reason: "cutoff-unavailable"}
	if result.RunID != row.RunID || result.SubmissionID != row.SubmissionID || result.UsageCutoffSeq <= result.PromptSeq || result.FinishedAt <= 0 {
		return u
	}
	input, err := s.store.ReadAutomationNotificationUsage(ctx, row.UserID, row.RunID, result.UsageCutoffSeq)
	if err != nil {
		u.Reason = "statistics-unavailable"
		return u
	}
	if !input.CutoffExists {
		return u
	}
	if input.HasLaterPrompt {
		u.Reason = "later-submission-boundary-unconfirmed"
		return u
	}
	// Never use today's mutable native identities to attribute old usage.
	identity := input.Run
	identity.CodexThreadID = ""
	identity.CodexThreadIDs = nil
	identity.ClaudeSessionIDs = nil
	allowed := map[string]bool{}
	key := func(cli, slot, session string) string { return cli + "\x00" + slot + "\x00" + session }
	for _, session := range orchestrationUsageSessions(identity, input.Timeline, nil) {
		allowed[key(session.CLI, session.WorkerSlot, session.SessionID)] = true
	}
	ledger := make([]protocol.OrchestrationUsageEvent, 0, len(input.Ledger))
	for _, event := range input.Ledger {
		if !allowed[key(event.CLI, event.WorkerSlot, event.SessionID)] || event.OccurredAt <= 0 || event.OccurredAt > result.FinishedAt {
			u.Reason = "ledger-boundary-unconfirmed"
			return u
		}
		ledger = append(ledger, event)
	}
	syncs := make([]store.OrchestrationUsageSync, 0, len(input.Syncs))
	for _, sync := range input.Syncs {
		if allowed[key(sync.CLI, sync.WorkerSlot, sync.SessionID)] {
			syncs = append(syncs, sync)
		}
	}
	graphs := make([]store.OrchestrationTaskGraph, 0, len(input.Graphs))
	for _, graph := range input.Graphs {
		var payload protocol.OrchestrationStartPayload
		if json.Unmarshal([]byte(graph.PayloadJSON), &payload) == nil && payload.PromptSeq > 0 && payload.PromptSeq <= result.PromptSeq && graph.CreatedAt <= result.FinishedAt {
			graphs = append(graphs, graph)
		}
	}
	run := input.Run
	run.Status = result.Execution.Status
	run.FinishedAt = result.FinishedAt
	timeline := enrichVerifierUsage(enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(input.Timeline, graphs), ledger), graphs)
	stats := buildOrchestrationRunStatsWithLedger(run, timeline, ledger, syncs)
	if stats.CallCount == 0 && stats.TotalTokens == 0 && stats.EstimatedCostUSD == 0 {
		u.Reason = "usage-not-recorded"
		return u
	}
	u.Available = true
	u.Reason = ""
	u.InputTokens = stats.InputTokens
	u.OutputTokens = stats.OutputTokens
	u.CacheTokens = stats.CacheTokens
	u.TotalTokens = stats.TotalTokens
	u.EstimatedCostUSD = stats.EstimatedCostUSD
	u.CostKnown = stats.CostKnown
	u.AccountingStatus = stats.AccountingStatus
	u.AccountingSource = stats.AccountingSource
	return u
}
