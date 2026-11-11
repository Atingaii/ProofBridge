package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func automationFixture(t *testing.T) (*Store, User, Agent) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "automation-owner", "testing-password")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.UpsertAgentForUser(ctx, u.ID, "workstation", "automation-machine", "localhost", "instance", []string{"/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutAutomationSettings(ctx, u.ID, "account", json.RawMessage(`{"email":"owner@example.test","completionEnabled":true,"attentionEnabled":true}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	return s, u, a
}
func startAutomationFixture(t *testing.T, s *Store, u User, a Agent, key string) (AutomationSubmission, OrchestrationRun) {
	t.Helper()
	ctx := context.Background()
	sub, created, err := s.ReserveAutomationSubmission(ctx, u.ID, "start", key, "digest-"+key, "background-mail", json.RawMessage(`{"workerPair":"codex-codex","revision":1}`))
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", sub, created, err)
	}
	run, err := s.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: u.ID, AgentID: a.ID, Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex, Profile: "formal-proof", Prompt: "Prove the requested target", AutomationSubmissionID: sub.ID})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Source: "user", Content: "Prove the requested target"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err = s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.RunID != run.ID || sub.PromptSeq != e.Seq || sub.State != "accepted" {
		t.Fatalf("unbound accepted receipt: %+v", sub)
	}
	return sub, run
}
func finishAutomationFixture(t *testing.T, s *Store, run OrchestrationRun, status string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.end", Status: OrchestrationCompleted, RunEndData: &protocol.RunEndData{TerminalReason: "max-turns", VerifierVerdict: &protocol.VerifierVerdict{Status: status, Reason: "Independent evidence"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomationCredentialsRevocationAndSerialization(t *testing.T) {
	s, u, _ := automationFixture(t)
	ctx := context.Background()
	token := AutomationToken{UserID: u.ID, TokenHash: "unique-hash", Ciphertext: "encrypted-value"}
	if err := s.PutAutomationToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	got, err := s.AutomationTokenByHash(ctx, token.TokenHash)
	if err != nil || got.Ciphertext != token.Ciphertext {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "unique-hash") || strings.Contains(string(raw), "encrypted-value") {
		t.Fatal("credential material serialized")
	}
	if err = s.RevokeAutomationToken(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AutomationTokenByHash(ctx, token.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token accepted: %v", err)
	}
	token.TokenHash = "rotated"
	token.Ciphertext = "new-encrypted"
	if err = s.PutAutomationToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AutomationTokenByHash(ctx, "rotated"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AutomationTokenByHash(ctx, "rotated"); err != nil {
		t.Fatal("idempotent migration lost token:", err)
	}
}

func TestAutomationPreferencesAndPresetsVersionAndOwnership(t *testing.T) {
	s, u, _ := automationFixture(t)
	ctx := context.Background()
	p, err := s.SaveAutomationPreset(ctx, AutomationPreset{UserID: u.ID, Name: "My pair", Config: json.RawMessage(`{"workerPair":"codex-codex"}`)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAutomationPreset(ctx, "other-user", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user lookup: %v", err)
	}
	p.Name = "Revised"
	if _, err = s.SaveAutomationPreset(ctx, p, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("stale version overwrote preset:", err)
	}
	p, err = s.SaveAutomationPreset(ctx, p, 1)
	if err != nil || p.Version != 2 {
		t.Fatalf("update: %+v %v", p, err)
	}
	raw, _ := json.Marshal(map[string]any{"defaultPresetId": p.ID, "email": "owner@example.test"})
	settings, err := s.PutAutomationSettings(ctx, u.ID, "account", raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutAutomationSettings(ctx, u.ID, "account", json.RawMessage(`{}`), 1); !errors.Is(err, ErrConflict) {
		t.Fatal("lost update:", err)
	}
	if err = s.DeleteAutomationPreset(ctx, u.ID, p.ID, 2); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetAutomationSettings(ctx, u.ID, "account")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	_ = json.Unmarshal(after.Data, &obj)
	if _, exists := obj["defaultPresetId"]; exists || after.Version != settings.Version+1 {
		t.Fatalf("deleted default retained: %+v", after)
	}
	if obj["email"] != "owner@example.test" {
		t.Fatal("unrelated preferences lost")
	}
}

func TestAutomationSubmissionReplayFreezesInputAndRunBinding(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	sub, run := startAutomationFixture(t, s, u, a, "same")
	replay, created, err := s.ReserveAutomationSubmission(ctx, u.ID, "start", "same", "digest-same", "interactive", json.RawMessage(`{"changedDefault":true}`))
	if err != nil || created || replay.ID != sub.ID || string(replay.Configuration) != string(sub.Configuration) {
		t.Fatalf("retry changed accepted config: %+v %v %v", replay, created, err)
	}
	if _, _, err = s.ReserveAutomationSubmission(ctx, u.ID, "start", "same", "different", "interactive", nil); !errors.Is(err, ErrConflict) {
		t.Fatal("key reused for different request:", err)
	}
	if err = s.SetAutomationSubmissionConfiguration(ctx, sub.ID, u.ID, json.RawMessage(`{"changed":true}`)); !errors.Is(err, ErrConflict) {
		t.Fatal("accepted snapshot changed:", err)
	}
	if _, err = s.AutomationSubmissionByID(ctx, "other", sub.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-user receipt lookup:", err)
	}
	unbound, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "start", "invalid-run", "digest", "interactive", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: u.ID, AgentID: "missing-agent", Mode: "collaboration", Prompt: "fail", AutomationSubmissionID: unbound.ID})
	if err == nil {
		t.Fatal("expected invalid endpoint failure")
	}
	again, err := s.AutomationSubmissionByID(ctx, u.ID, unbound.ID)
	if err != nil || again.RunID != "" {
		t.Fatalf("partial create bound reservation: %+v %v", again, err)
	}
	_ = run
}

func TestAutomationNodeEventsDoNotSendCompletionAndConclusionEnriches(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	sub, run := startAutomationFixture(t, s, u, a, "node")
	if _, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.end", Task: &protocol.TaskAttemptRef{TaskID: "node", AttemptID: "attempt"}}); err != nil {
		t.Fatal(err)
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, "", 100)
	if err != nil || len(n) != 0 {
		t.Fatalf("node sent completion: %+v %v", n, err)
	}
	finishAutomationFixture(t, s, run, "continue")
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "diagnostic", Content: "final usage accounting"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.conclusion", RunConclusion: &protocol.RunConclusion{Outcome: "blocked", VerificationStatus: "continue", Summary: "Target still has a proof hole", UnmetObligations: []string{"TheoremA"}}})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result AutomationResult
	if err = json.Unmarshal(receipt.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Execution.Status != "completed" || result.Verification.Status != "continue" || result.Verification.Summary != "Target still has a proof hole" || result.Verification.Conclusion == nil || result.Verification.Verdict == nil {
		t.Fatalf("incorrect frozen result: %+v", result)
	}
	n, err = s.ListAutomationNotifications(ctx, u.ID, run.ID, sub.ID, 100)
	if err != nil || len(n) != 1 {
		t.Fatalf("duplicate completion: %+v %v", n, err)
	}
	if !strings.Contains(string(n[0].Snapshot), "Target still has a proof hole") {
		t.Fatal("queued mail did not receive authoritative conclusion")
	}
	raw, _ := json.Marshal(n[0])
	if strings.Contains(string(raw), "owner@example.test") || strings.Contains(string(raw), "Target still") {
		t.Fatal("notification list leaks private snapshot")
	}
}

func TestAutomationSameRunFollowupHasIndependentNotificationAndFrozenResult(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	first, run := startAutomationFixture(t, s, u, a, "first")
	finishAutomationFixture(t, s, run, "continue")
	old, _ := s.AutomationSubmissionByID(ctx, u.ID, first.ID)
	next, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "follow_up", "second", "second-digest", "background-mail", json.RawMessage(`{"revision":2}`))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAutomationRunForContinue(ctx, run.ID, u.ID, next.ID)
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "Finish remaining target"}); err != nil {
		t.Fatal(err)
	}
	finishAutomationFixture(t, s, run, "pass")
	firstAgain, err := s.AutomationSubmissionByID(ctx, u.ID, first.ID)
	if err != nil || string(firstAgain.Result) != string(old.Result) {
		t.Fatal("followup changed prior result:", err)
	}
	second, err := s.AutomationSubmissionByID(ctx, u.ID, next.ID)
	if err != nil || second.PromptSeq <= first.PromptSeq {
		t.Fatalf("follow-up lost boundary: %+v %v", second, err)
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, "", 100)
	if err != nil || len(n) != 2 {
		t.Fatalf("expected two submission reports: %+v %v", n, err)
	}
}

func TestAutomationAttentionLeaseRetryAndCancellation(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	_, run := startAutomationFixture(t, s, u, a, "attention")
	for i := 0; i < 2; i++ {
		if err := s.QueueAutomationAttention(ctx, run.ID, "instance/request"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, "", 10)
	if err != nil || len(n) != 1 {
		t.Fatalf("attention dedupe: %+v %v", n, err)
	}
	if _, err = s.db.Exec(`UPDATE automation_notifications SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil || claimed.State != "sending" {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err = s.ClaimAutomationNotification(ctx, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatal("leased work double claimed:", err)
	}
	if err = s.FinishAutomationNotification(ctx, claimed.ID, "stale", nil, time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale lease accepted:", err)
	}
	if err = s.FinishAutomationNotification(ctx, claimed.ID, claimed.ClaimToken, errors.New("temporary SMTP failure"), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	retry, err := s.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil || retry.Attempts != 2 {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if err = s.FinishAutomationNotification(ctx, retry.ID, retry.ClaimToken, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err = s.QueueAutomationAttention(ctx, run.ID, "another-request"); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelAutomationAttention(ctx, run.ID, "another-request"); err != nil {
		t.Fatal(err)
	}
	n, _ = s.ListAutomationNotifications(ctx, u.ID, run.ID, "", 10)
	states := map[string]string{}
	for _, item := range n {
		states[item.InteractionID] = item.State
	}
	if states["instance/request"] != "sent" || states["another-request"] != "canceled" {
		t.Fatalf("bad lifecycle: %+v", states)
	}
	if other, err := s.ListAutomationNotifications(ctx, "other", run.ID, "", 10); err != nil || len(other) != 0 {
		t.Fatal("notification ownership violated")
	}
}

func TestAutomationFinalEventAndOutboxAreAtomic(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	sub, run := startAutomationFixture(t, s, u, a, "atomic")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_notification_insert BEFORE INSERT ON automation_notifications BEGIN SELECT RAISE(ABORT,'fixture outbox unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.error", Error: "command failed"})
	if err == nil {
		t.Fatal("expected outbox failure")
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM orchestration_events WHERE run_id=? AND kind='run.error'`, run.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("terminal event committed without outbox: %d %v", count, err)
	}
	got, _ := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	if got.State != "accepted" || len(got.Result) != 0 {
		t.Fatal("failed transaction froze incomplete result")
	}
}

func TestAutomationRecoveryDoesNotInferPassAndIsSettledBeforeContinue(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	first, run := startAutomationFixture(t, s, u, a, "recovery")
	if err := s.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "follow_up", "after-crash", "digest", "interactive", nil)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAutomationRunForContinue(ctx, run.ID, u.ID, next.ID)
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	frozen, _ := s.AutomationSubmissionByID(ctx, u.ID, first.ID)
	var result AutomationResult
	if err = json.Unmarshal(frozen.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Verification.Status != "unknown" || result.Execution.TerminalReason != "recovered-unconfirmed" {
		t.Fatalf("recovery fabricated pass: %+v", result)
	}
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "new attempt"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.conclusion", RunConclusion: &protocol.RunConclusion{VerificationStatus: "pass", Summary: "late earlier conclusion"}}); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.AutomationSubmissionByID(ctx, u.ID, next.ID)
	if pending.State != "accepted" || len(pending.Result) != 0 {
		t.Fatal("late conclusion prematurely completed the next submission")
	}
	finishAutomationFixture(t, s, run, "pass")
	stillFrozen, _ := s.AutomationSubmissionByID(ctx, u.ID, first.ID)
	if string(stillFrozen.Result) != string(frozen.Result) {
		t.Fatal("recovery report mutated after follow-up")
	}
}

func TestAutomationRecoveredSnapshotAcceptsLateAuthoritativeTerminal(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	sub, run := startAutomationFixture(t, s, u, a, "late-terminal")
	if err := s.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE orchestration_runs SET updated_at=0 WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileAutomationNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	if !strings.Contains(string(before.Result), "recovered-unconfirmed") {
		t.Fatal("missing conservative recovery snapshot")
	}
	finishAutomationFixture(t, s, run, "pass")
	after, _ := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
	var result AutomationResult
	if err := json.Unmarshal(after.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Verification.Status != "pass" || result.Execution.TerminalReason == "recovered-unconfirmed" {
		t.Fatalf("late independent evidence was lost: %+v", result)
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, sub.ID, 100)
	if err != nil || len(n) != 1 || !strings.Contains(string(n[0].Snapshot), `"status":"pass"`) {
		t.Fatalf("late terminal duplicated or failed to refresh outbox: %+v %v", n, err)
	}
}

func TestAutomationLegacyWebsiteSubmissionAndNotificationOptOut(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	run, err := s.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: u.ID, AgentID: a.ID, Mode: "collaboration", Prompt: "Website task"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "Website task"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.LatestAutomationSubmissionForRun(ctx, u.ID, run.ID)
	if err != nil || sub.Operation != "website" || sub.PromptSeq != e.Seq {
		t.Fatalf("website request was not tracked: %+v %v", sub, err)
	}
	finishAutomationFixture(t, s, run, "pass")
	if _, err = s.PutAutomationSettings(ctx, u.ID, "account", json.RawMessage(`{"email":"owner@example.test","completionEnabled":false,"attentionEnabled":false}`), 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE automation_notifications SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAutomationNotification(ctx, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatal("account opt-out did not cancel unsent private report:", err)
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, sub.ID, 100)
	if err != nil || len(n) != 1 || n[0].State != "canceled" {
		t.Fatalf("opt-out state not observable: %+v %v", n, err)
	}
}

func TestAutomationFailureAndCancellationRecoveryCreateFinalReport(t *testing.T) {
	for _, path := range []string{"run.error", "run.cancelled", "cancel-timeout", "disconnect-recovery"} {
		t.Run(path, func(t *testing.T) {
			s, u, a := automationFixture(t)
			ctx := context.Background()
			sub, run := startAutomationFixture(t, s, u, a, path)
			want := OrchestrationFailed
			var err error
			switch path {
			case "run.error", "run.cancelled":
				if path == "run.cancelled" {
					want = OrchestrationCanceled
				}
				_, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: path, Status: want, Error: "task did not complete"})
			case "cancel-timeout":
				want = OrchestrationCanceled
				if err = s.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCanceling, ""); err == nil {
					_, _, err = s.CancelOrchestrationRunIfStillCanceling(ctx, run.ID, "cancel timeout")
				}
			case "disconnect-recovery":
				_, err = s.MarkActiveOrchestrationRunsForAgentFailed(ctx, a.ID, "Bridge disconnected")
			}
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
			if err != nil {
				t.Fatal(err)
			}
			var result AutomationResult
			if err = json.Unmarshal(frozen.Result, &result); err != nil {
				t.Fatal(err)
			}
			if frozen.State != "terminal" || result.Execution.Status != want || result.Verification.Status != "unknown" {
				t.Fatalf("wrong recovered outcome: %+v %+v", frozen, result)
			}
			n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, sub.ID, 100)
			if err != nil || len(n) != 1 {
				t.Fatalf("final notification missing: %+v %v", n, err)
			}
		})
	}
}
