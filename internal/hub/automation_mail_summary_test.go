package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestAutomationCompletionSummaryMatchesDetailsAndFreezesAcrossContinuation(t *testing.T) {
	s, st, db := newUsageReadFixture(t)
	ctx := context.Background()
	if _, err := st.PutAutomationSettings(ctx, "usage-user", "account", json.RawMessage(`{"email":"owner@example.test"}`), 0); err != nil {
		t.Fatal(err)
	}
	run := createOrchestrationRun(t, st, "usage-user", "usage-agent")
	add := func(e store.OrchestrationEvent) store.OrchestrationEvent {
		t.Helper()
		e.RunID = run.ID
		v, err := st.AddOrchestrationEvent(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	add(store.OrchestrationEvent{Kind: "user.message", Content: "First"})
	add(store.OrchestrationEvent{Kind: "turn.usage", CLI: "codex", Data: map[string]any{"inputTokens": 100, "cacheReadTokens": 80, "outputTokens": 5, "model": "private-priced-model", "costKnown": true, "costSource": "provider", "estimatedCostUsd": .125}})
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	add(store.OrchestrationEvent{Kind: "run.end", RunEndData: &protocol.RunEndData{VerifierVerdict: &protocol.VerifierVerdict{Status: "pass"}}})
	if _, err := db.Exec(`UPDATE automation_notifications SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	row, err := st.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.prepareAutomationNotification(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	var result store.AutomationResult
	_ = json.Unmarshal(prepared.Snapshot, &result)
	usage := result.UsageSummary
	detail, err := s.automationRunStatistics(ctx, "usage-user", run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	stats := detail.(map[string]any)["stats"].(orchestrationRunStats)
	if usage == nil || !usage.Available || usage.TotalTokens != 105 || usage.InputTokens != 20 || usage.CacheTokens != 80 || usage.EstimatedCostUSD != stats.EstimatedCostUSD || usage.TotalTokens != stats.TotalTokens {
		t.Fatal("mail and website use different accounting")
	}
	add(store.OrchestrationEvent{Kind: "user.message", Content: "Followup"})
	add(store.OrchestrationEvent{Kind: "turn.usage", CLI: "codex", Data: map[string]any{"inputTokens": 9999}})
	again, err := s.prepareAutomationNotification(ctx, row)
	if err != nil || string(again.Snapshot) != string(prepared.Snapshot) {
		t.Fatal("continuation changed frozen mail")
	}
	// The unfrozen old snapshot cannot borrow later usage, even in the same second.
	var original store.AutomationResult
	_ = json.Unmarshal(row.Snapshot, &original)
	late := s.automationCompletionUsage(ctx, row, original)
	if late.Available || late.Reason != "later-submission-boundary-unconfirmed" {
		t.Fatal("unfrozen old mail attributed followup statistics")
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	add(store.OrchestrationEvent{Kind: "run.end"})
	if _, err := db.Exec(`UPDATE automation_notifications SET next_attempt_at=0 WHERE state='queued'`); err != nil {
		t.Fatal(err)
	}
	second, err := st.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err = s.prepareAutomationNotification(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	var next store.AutomationResult
	_ = json.Unmarshal(second.Snapshot, &next)
	if next.UsageSummary == nil || !next.UsageSummary.Available || next.UsageSummary.TotalTokens != 10104 || next.SubmissionID == result.SubmissionID {
		t.Fatal("new submission did not freeze its own run-cumulative totals")
	}
}

func TestAutomationCompletionSummaryStatisticsFailureDoesNotChangeOutcome(t *testing.T) {
	s, st, db := newUsageReadFixture(t)
	ctx := context.Background()
	if _, err := st.PutAutomationSettings(ctx, "usage-user", "account", json.RawMessage(`{"email":"owner@example.test"}`), 0); err != nil {
		t.Fatal(err)
	}
	run := createOrchestrationRun(t, st, "usage-user", "usage-agent")
	for _, kind := range []string{"user.message", "run.end"} {
		if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: kind, RunEndData: &protocol.RunEndData{VerifierVerdict: &protocol.VerifierVerdict{Status: "pass"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE automation_notifications SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	row, err := st.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TABLE orchestration_usage_syncs`); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.prepareAutomationNotification(ctx, row)
	if err != nil {
		t.Fatal("statistics failure blocked status notification")
	}
	var result store.AutomationResult
	_ = json.Unmarshal(prepared.Snapshot, &result)
	if result.UsageSummary == nil || result.UsageSummary.Available || result.UsageSummary.Reason != "statistics-unavailable" || result.Verification.Status != "pass" {
		t.Fatal("statistics failure became a proof failure or fake zero")
	}
}

func TestAutomationCompletionSummaryLedgerRequiresObservedIdentity(t *testing.T) {
	s, st, _ := newUsageReadFixture(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, "usage-user", "usage-agent")
	prompt, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "user.message"})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "run.end", RunEndData: &protocol.RunEndData{CodexThreadIDs: map[string]string{"codex": "observed"}}})
	if err != nil {
		t.Fatal(err)
	}
	result := store.AutomationResult{RunID: run.ID, SubmissionID: "test", PromptSeq: prompt.Seq, UsageCutoffSeq: terminal.Seq, FinishedAt: terminal.CreatedAt, Execution: store.AutomationExecutionResult{Status: "completed"}}
	row := store.AutomationNotification{RunID: run.ID, SubmissionID: "test", UserID: "usage-user"}
	for _, session := range []string{"observed", "unobserved"} {
		if err := st.ReplaceOrchestrationUsage(ctx, protocol.OrchestrationUsageSyncResult{RunID: run.ID, Sessions: []protocol.OrchestrationUsageSessionResult{{CLI: "codex", WorkerSlot: "codex", SessionID: session, Status: "complete"}}, Events: []protocol.OrchestrationUsageEvent{{CLI: "codex", WorkerSlot: "codex", SessionID: session, EventID: session, OccurredAt: terminal.CreatedAt, InputTokens: 7, OutputTokens: 3, Model: "gpt-5.6-sol"}}}); err != nil {
			t.Fatal(err)
		}
		u := s.automationCompletionUsage(ctx, row, result)
		if session == "observed" && (!u.Available || u.TotalTokens != 10) {
			t.Fatal("trusted native ledger not reused")
		}
		if session == "observed" {
			detail, err := s.automationRunStatistics(ctx, "usage-user", run.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			stats := detail.(map[string]any)["stats"].(orchestrationRunStats)
			if u.TotalTokens != stats.TotalTokens || u.EstimatedCostUSD != stats.EstimatedCostUSD || u.CostKnown != stats.CostKnown {
				t.Fatal("ledger mail differs from the existing detail aggregate")
			}
		}
		if session == "unobserved" && u.Available {
			t.Fatal("unobserved native session entered summary")
		}
	}
}
