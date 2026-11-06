package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestAutomationStartupRejectsUnacceptedReservationsWithoutDispatchOrMail(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	var reservations []AutomationSubmission
	for _, stage := range []string{"reserved", "bound", "unconfirmed-before-prompt"} {
		sub, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "start_run", stage, "digest", "background-mail", nil)
		if err != nil {
			t.Fatal(err)
		}
		if stage != "reserved" {
			run, err := s.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: u.ID, AgentID: a.ID, Mode: "collaboration", Prompt: "not yet accepted", AutomationSubmissionID: sub.ID})
			if err != nil {
				t.Fatal(err)
			}
			sub.RunID = run.ID
		}
		if stage == "unconfirmed-before-prompt" {
			if err := s.SetAutomationSubmissionState(ctx, u.ID, sub.ID, "dispatch_unconfirmed"); err != nil {
				t.Fatal(err)
			}
		}
		reservations = append(reservations, sub)
	}
	if err := s.ReconcileAutomationSubmissions(ctx); err != nil {
		t.Fatal(err)
	}
	for _, original := range reservations {
		got, err := s.AutomationSubmissionByID(ctx, u.ID, original.ID)
		if err != nil || got.State != "rejected" || got.PromptSeq != 0 {
			t.Fatalf("unaccepted reservation was not settled: %+v %v", got, err)
		}
		var result AutomationResult
		if err = json.Unmarshal(got.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.Execution.TerminalReason != "not-accepted-before-restart" || result.Verification.Status != "unknown" {
			t.Fatalf("recovery fabricated acceptance or verification: %+v", result)
		}
		if got.RunID != "" {
			run, err := s.OrchestrationRunByID(ctx, got.RunID, u.ID)
			if err != nil || run.Status != OrchestrationFailed || run.Error == "" {
				t.Fatalf("unstarted active run was not settled: %+v %v", run, err)
			}
		}
		retry, created, err := s.ReserveAutomationSubmission(ctx, u.ID, original.Operation, original.RequestKey, original.RequestDigest, "background-mail", nil)
		if err != nil || created || retry.ID != got.ID || retry.State != "rejected" {
			t.Fatalf("retry re-reserved interrupted work: %+v %v %v", retry, created, err)
		}
		if err = s.ReconcileAutomationSubmissions(ctx); err != nil {
			t.Fatal(err)
		}
		again, _ := s.AutomationSubmissionByID(ctx, u.ID, original.ID)
		if string(again.Result) != string(got.Result) {
			t.Fatal("startup recovery changed an already settled rejection")
		}
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, "", "", 100)
	if err != nil || len(n) != 0 {
		t.Fatalf("unaccepted work generated a completion mail: %+v %v", n, err)
	}
	var messages int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM orchestration_events WHERE kind='user.message'`).Scan(&messages); err != nil || messages != 0 {
		t.Fatal("recovery fabricated a dispatched prompt")
	}
}

func TestAutomationStartupPendingFollowupCannotClaimLaterPrompt(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	first, run := startAutomationFixture(t, s, u, a, "before-interruption")
	finishAutomationFixture(t, s, run, "continue")
	firstFrozen, _ := s.AutomationSubmissionByID(ctx, u.ID, first.ID)
	pending, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "follow_up", "interrupted-followup", "digest", "background-mail", nil)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimAutomationRunForContinue(ctx, run.ID, u.ID, pending.ID); err != nil || !claimed {
		t.Fatalf("claim before interruption: %v %v", claimed, err)
	}
	if err = s.ReconcileAutomationSubmissions(ctx); err != nil {
		t.Fatal(err)
	}
	newSub, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "follow_up", "explicit-new-followup", "new-digest", "interactive", nil)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimAutomationRunForContinue(ctx, run.ID, u.ID, newSub.ID); err != nil || !claimed {
		t.Fatalf("explicit follow-up claim: %v %v", claimed, err)
	}
	event, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "explicitly different follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	pendingAgain, _ := s.AutomationSubmissionByID(ctx, u.ID, pending.ID)
	newAgain, _ := s.AutomationSubmissionByID(ctx, u.ID, newSub.ID)
	if pendingAgain.State != "rejected" || pendingAgain.PromptSeq != 0 || newAgain.State != "accepted" || newAgain.PromptSeq != event.Seq {
		t.Fatalf("new prompt was attached to interrupted submission: old=%+v new=%+v", pendingAgain, newAgain)
	}
	firstAgain, _ := s.AutomationSubmissionByID(ctx, u.ID, first.ID)
	if string(firstFrozen.Result) != string(firstAgain.Result) {
		t.Fatal("unaccepted follow-up rewrote an earlier accepted result")
	}
	n, err := s.ListAutomationNotifications(ctx, u.ID, run.ID, "", 100)
	if err != nil || len(n) != 1 || n[0].SubmissionID != first.ID {
		t.Fatalf("unaccepted follow-up sent an earlier prompt as its completion: %+v %v", n, err)
	}
}

func TestAutomationStartupPreservesEvidenceAndMarksOnlyUnconfirmedDelivery(t *testing.T) {
	for _, evidence := range []string{"none", "run-start", "current-graph", "previous-graph"} {
		t.Run(evidence, func(t *testing.T) {
			s, u, a := automationFixture(t)
			ctx := context.Background()
			sub, run := startAutomationFixture(t, s, u, a, evidence)
			if err := s.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationRunning, ""); err != nil {
				t.Fatal(err)
			}
			want := "dispatch_unconfirmed"
			switch evidence {
			case "run-start":
				if _, err := s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "run.start", Source: "bridge"}); err != nil {
					t.Fatal(err)
				}
				want = "accepted"
			case "current-graph", "previous-graph":
				seq := sub.PromptSeq
				if evidence == "previous-graph" {
					seq--
				} else {
					want = "accepted"
				}
				raw, _ := json.Marshal(map[string]any{"promptSeq": seq})
				if _, err := s.CreateOrchestrationTaskGraph(ctx, run.ID, string(raw), "digest", []CreateTaskSpec{{Name: "review", Role: TaskRoleReviewer, PayloadJSON: `{}`, PayloadDigest: "review-digest"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ReconcileAutomationSubmissions(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
			current, _ := s.OrchestrationRunByID(ctx, run.ID, u.ID)
			if got.State != want || got.PromptSeq != sub.PromptSeq || len(got.Result) != 0 || current.Status != OrchestrationRunning {
				t.Fatalf("startup replayed or falsely settled accepted work: %+v %+v", got, current)
			}
			var attempts int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM orchestration_task_attempts`).Scan(&attempts); err != nil || attempts != 0 {
				t.Fatal("startup reconciliation dispatched a task attempt")
			}
			finishAutomationFixture(t, s, run, "continue")
			finished, _ := s.AutomationSubmissionByID(ctx, u.ID, sub.ID)
			if finished.State != "terminal" {
				t.Fatal("late terminal evidence could not settle unknown delivery")
			}
		})
	}
}

func TestAutomationRevocationTombstoneNeedsNoTokenOrDecryption(t *testing.T) {
	s, u, _ := automationFixture(t)
	ctx := context.Background()
	if err := s.RevokeAutomationToken(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetAutomationToken(ctx, u.ID)
	if err != nil || first.RevokedAt <= 0 || first.Ciphertext != "" || first.TokenHash != "revoked:"+u.ID {
		t.Fatalf("missing persistent first-revoke tombstone: %+v %v", first, err)
	}
	if _, err = s.AutomationTokenByHash(ctx, first.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatal("revocation tombstone authenticated")
	}
	if err = s.PutAutomationToken(ctx, AutomationToken{UserID: u.ID, TokenHash: "unreadable-token-hash", Ciphertext: "invalid-with-new-jwt-key"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeAutomationToken(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetAutomationToken(ctx, u.ID)
	if err != nil || again.RevokedAt <= 0 || again.TokenHash != "unreadable-token-hash" || again.Ciphertext != "invalid-with-new-jwt-key" {
		t.Fatalf("revocation needed decryption or rotated credential: %+v %v", again, err)
	}
	if _, err = s.AutomationTokenByHash(ctx, again.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatal("undecryptable revoked token remained valid")
	}
}

func TestAutomationEmailOnlyPreferencesUseEnabledDefaultsAtReservationAndClaim(t *testing.T) {
	s, u, a := automationFixture(t)
	ctx := context.Background()
	if _, err := s.PutAutomationSettings(ctx, u.ID, "account", json.RawMessage(`{"email":"owner@example.test"}`), 1); err != nil {
		t.Fatal(err)
	}
	sub, _, err := s.ReserveAutomationSubmission(ctx, u.ID, "start_run", "email-only", "digest", "interactive", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: u.ID, AgentID: a.ID, Mode: "collaboration", Prompt: "Verify default notification flags", AutomationSubmissionID: sub.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "user.message", Content: "Verify default notification flags"}); err != nil {
		t.Fatal(err)
	}
	if !sub.NotifyCompletion || !sub.NotifyAttention {
		t.Fatal("omitted notification flags disabled the reserved task")
	}
	finishAutomationFixture(t, s, run, "continue")
	if _, err := s.db.Exec(`UPDATE automation_notifications SET next_attempt_at=0`); err != nil {
		t.Fatal(err)
	}
	n, err := s.ClaimAutomationNotification(ctx, time.Minute)
	if err != nil || n.SubmissionID != sub.ID || n.Kind != "completion" {
		t.Fatalf("claim disagreed with reservation defaults: %+v %v", n, err)
	}
}
