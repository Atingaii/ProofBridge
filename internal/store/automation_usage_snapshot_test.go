package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestAutomationUsageCutoffReadAndFreezeCAS(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	sub, run := startAutomationFixture(t, s, u, a, "usage-cutoff")
	if _, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "turn.usage", CLI: "codex", Data: map[string]any{"inputTokens": 100, "model": "private-model"}}); err != nil {
		t.Fatal(err)
	}
	finishAutomationFixture(t, s, run, "pass")
	sub, err := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result AutomationResult
	if err = json.Unmarshal(sub.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.UsageCutoffSeq != sub.TerminalSeq || result.UsageCutoffSeq <= sub.PromptSeq || result.UsageSummary != nil {
		t.Fatal("terminal cutoff not recorded independently of mail projection")
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	input, err := s.ReadAutomationNotificationUsage(readCtx, u.ID, run.ID, result.UsageCutoffSeq)
	if err != nil || !input.CutoffExists || input.HasLaterPrompt || len(input.Timeline) != 2 {
		t.Fatalf("bounded single-connection read: %v", err)
	}
	if _, err = s.ReadAutomationNotificationUsage(ctx, "other", run.ID, result.UsageCutoffSeq); !errors.Is(err, ErrNotFound) {
		t.Fatal("read crossed ownership")
	}
	invalid, err := s.ReadAutomationNotificationUsage(ctx, u.ID, run.ID, sub.PromptSeq+1)
	if err != nil || invalid.CutoffExists || len(invalid.Timeline) > 0 {
		t.Fatal("nonterminal sequence was accepted as an accounting cutoff")
	}
	if _, err = s.db.Exec(`UPDATE automation_notifications SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	row, err := s.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	usage := AutomationUsageSummary{Available: true, Scope: "run-cumulative-through-submission", ThroughSeq: result.UsageCutoffSeq, TotalTokens: 100}
	if _, err = s.FreezeAutomationNotificationUsage(ctx, row.ID, "stale", row.Snapshot, usage); !errors.Is(err, ErrConflict) {
		t.Fatal("stale claim changed snapshot")
	}
	frozen, err := s.FreezeAutomationNotificationUsage(ctx, row.ID, row.ClaimToken, row.Snapshot, usage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FreezeAutomationNotificationUsage(ctx, row.ID, row.ClaimToken, row.Snapshot, AutomationUsageSummary{TotalTokens: 999}); !errors.Is(err, ErrConflict) {
		t.Fatal("CAS replaced frozen usage")
	}
	if err = s.FinishAutomationNotification(ctx, row.ID, row.ClaimToken, errors.New("temporary failure"), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	// A late conclusion updates outcome text while retaining the frozen metric.
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.conclusion", RunConclusion: &protocol.RunConclusion{Summary: "Later evidence", VerificationStatus: "pass"}}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var retried AutomationResult
	if err = json.Unmarshal(retry.Snapshot, &retried); err != nil {
		t.Fatal(err)
	}
	if retried.UsageSummary == nil || retried.UsageSummary.TotalTokens != 100 {
		t.Fatal("late evidence or retry erased frozen metrics")
	}
	unchanged, err := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	var accepted AutomationResult
	_ = json.Unmarshal(unchanged.Result, &accepted)
	if accepted.UsageSummary != nil {
		t.Fatal("mail projection mutated accepted submission")
	}
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "Continue"}); err != nil {
		t.Fatal(err)
	}
	input, err = s.ReadAutomationNotificationUsage(ctx, u.ID, run.ID, result.UsageCutoffSeq)
	if err != nil || !input.HasLaterPrompt || len(input.Ledger) > 0 || len(input.Timeline) > 0 {
		t.Fatal("later prompt leaked into old usage read")
	}
	if _, err = s.db.Exec(`UPDATE automation_notifications SET lease_until=0 WHERE id=?`, retry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimedAutomationNotification(ctx, retry.ID, retry.ClaimToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired owner can still send")
	}
	if _, err = s.FreezeAutomationNotificationUsage(ctx, retry.ID, retry.ClaimToken, frozen, usage); !errors.Is(err, ErrConflict) {
		t.Fatal("expired owner can write")
	}
}
