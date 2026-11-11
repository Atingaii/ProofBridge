package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

type automationNotificationPreferences struct {
	Email             string `json:"email"`
	CompletionEnabled bool   `json:"completionEnabled"`
	AttentionEnabled  bool   `json:"attentionEnabled"`
}

func automationNotificationPreferencesTx(ctx context.Context, tx *sql.Tx, uid string) (automationNotificationPreferences, error) {
	p := automationNotificationPreferences{CompletionEnabled: true, AttentionEnabled: true}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT data_json FROM automation_settings WHERE user_id=? AND scope='account'`, uid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return p, fmt.Errorf("decode automation preferences: %w", err)
	}
	p.Email = strings.TrimSpace(p.Email)
	return p, nil
}

type AutomationNotification struct {
	ID            string          `json:"id"`
	UserID        string          `json:"-"`
	RunID         string          `json:"runId"`
	SubmissionID  string          `json:"submissionId"`
	Kind          string          `json:"kind"`
	InteractionID string          `json:"interactionId,omitempty"`
	Recipient     string          `json:"-"`
	Snapshot      json.RawMessage `json:"-"`
	State         string          `json:"status"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt int64           `json:"nextAttemptAt,omitempty"`
	ClaimToken    string          `json:"-"`
	LeaseUntil    int64           `json:"-"`
	LastError     string          `json:"lastError,omitempty"`
	CreatedAt     int64           `json:"createdAt"`
	UpdatedAt     int64           `json:"updatedAt"`
	SentAt        int64           `json:"sentAt,omitempty"`
}

const automationNotificationColumns = `id,user_id,run_id,submission_id,kind,interaction_id,recipient,snapshot_json,state,attempts,next_attempt_at,claim_token,lease_until,last_error,created_at,updated_at,sent_at`

func scanAutomationNotification(row automationScanner) (AutomationNotification, error) {
	var n AutomationNotification
	var raw string
	err := row.Scan(&n.ID, &n.UserID, &n.RunID, &n.SubmissionID, &n.Kind, &n.InteractionID, &n.Recipient, &raw, &n.State, &n.Attempts, &n.NextAttemptAt, &n.ClaimToken, &n.LeaseUntil, &n.LastError, &n.CreatedAt, &n.UpdatedAt, &n.SentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	n.Snapshot = json.RawMessage(raw)
	return n, err
}

func automationTerminal(status string) bool {
	return status == OrchestrationCompleted || status == OrchestrationFailed || status == OrchestrationCanceled
}
func automationEventTx(ctx context.Context, tx *sql.Tx, event OrchestrationEvent) error {
	// Native task-node completions are not top-level task completion.
	if event.Task != nil {
		return nil
	}
	switch event.Kind {
	case "user.message":
		return automationSubmissionPromptTx(ctx, tx, event)
	case "run.end", "run.error", "run.cancelled":
		return automationFinalizeEventTx(ctx, tx, event, false)
	case "run.conclusion":
		// A public conclusion can follow other diagnostic events. The newest
		// prompt boundary must still be terminal before it can be enriched.
		return automationFinalizeEventTx(ctx, tx, event, true)
	}
	return nil
}
func automationSubmissionPromptTx(ctx context.Context, tx *sql.Tx, event OrchestrationEvent) error {
	var uid string
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM orchestration_runs WHERE id=?`, event.RunID).Scan(&uid); err != nil {
		return err
	}
	sub, err := scanAutomationSubmission(tx.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE user_id=? AND run_id=? AND prompt_seq=0 AND state='validating' ORDER BY created_at,id LIMIT 1`, uid, event.RunID))
	if errors.Is(err, ErrNotFound) {
		prefs, err := automationNotificationPreferencesTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		now := event.CreatedAt
		_, err = tx.ExecContext(ctx, `INSERT INTO automation_submissions(id,user_id,operation,request_key,request_digest,run_id,prompt_seq,state,monitoring_mode,configuration_json,recipient,notify_completion,notify_attention,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,'accepted','interactive','{}',?,?,?,?,?)`, NewID("asub"), uid, "website", event.ID, event.ID, event.RunID, event.Seq, prefs.Email, boolInt(prefs.CompletionEnabled), boolInt(prefs.AttentionEnabled), now, now)
		return err
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE automation_submissions SET prompt_seq=?,state='accepted',updated_at=? WHERE id=? AND prompt_seq=0`, event.Seq, event.CreatedAt, sub.ID)
	return err
}
func automationFinalizeEventTx(ctx context.Context, tx *sql.Tx, event OrchestrationEvent, enrich bool) error {
	var sub AutomationSubmission
	var err error
	sub, err = scanAutomationSubmission(tx.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE run_id=? AND prompt_seq>0 AND prompt_seq<? ORDER BY prompt_seq DESC LIMIT 1`, event.RunID, event.Seq))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if enrich && sub.State != "terminal" {
		return nil
	}
	if !enrich && sub.State == "terminal" {
		var previous AutomationResult
		if err = json.Unmarshal(sub.Result, &previous); err != nil {
			return err
		}
		// Recovery freezes a conservative result if only the run status was
		// durable. A later real terminal event can supply the missing evidence.
		if previous.Execution.TerminalReason != "recovered-unconfirmed" {
			return nil
		}
	}
	run, err := scanOrchestrationRun(tx.QueryRowContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE id=?`, event.RunID))
	if err != nil {
		return err
	}
	result := AutomationResult{SchemaVersion: 1, RunID: run.ID, SubmissionID: sub.ID, PromptSeq: sub.PromptSeq, Title: run.Title, EndpointID: run.AgentID, Configuration: sub.Configuration, FinishedAt: event.CreatedAt,
		Execution: AutomationExecutionResult{Status: run.Status}, Verification: AutomationVerificationResult{Authority: run.VerificationMode, Status: "unknown", Summary: event.Content, Checks: []protocol.VerifierCheck{}, UnmetObligations: []string{}}}
	if enrich && len(sub.Result) > 0 {
		if err = json.Unmarshal(sub.Result, &result); err != nil {
			return err
		}
	} else {
		switch event.Kind {
		case "run.end":
			result.Execution.Status = OrchestrationCompleted
		case "run.error":
			result.Execution.Status = OrchestrationFailed
		case "run.cancelled":
			result.Execution.Status = OrchestrationCanceled
		}
		// Legacy relay emits its public conclusion immediately before run.end.
		if event.RunConclusion == nil {
			var data string
			err = tx.QueryRowContext(ctx, `SELECT COALESCE(data_json,'') FROM orchestration_events WHERE run_id=? AND kind='run.conclusion' AND seq>? AND seq<? AND (data_json IS NULL OR json_extract(data_json,'$.task') IS NULL) ORDER BY seq DESC LIMIT 1`, event.RunID, sub.PromptSeq, event.Seq).Scan(&data)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				var typed struct {
					Conclusion *protocol.RunConclusion `json:"runConclusion"`
				}
				if json.Unmarshal([]byte(data), &typed) == nil {
					event.RunConclusion = typed.Conclusion
				}
			}
		}
	}
	if event.RunEndData != nil {
		result.Execution.TerminalReason = event.RunEndData.TerminalReason
		if verdict := event.RunEndData.VerifierVerdict; verdict != nil {
			result.Verification.Verdict = verdict
			result.Verification.Status = verdict.Status
			result.Verification.Summary = verdict.Reason
			result.Verification.Checks = append([]protocol.VerifierCheck{}, verdict.Checkers...)
		}
	}
	if c := event.RunConclusion; c != nil {
		result.Verification.Conclusion = c
		if c.VerificationStatus != "" {
			result.Verification.Status = c.VerificationStatus
		}
		if c.Summary != "" {
			result.Verification.Summary = c.Summary
		}
		result.Verification.UnmetObligations = append([]string{}, c.UnmetObligations...)
	}
	if result.Verification.Summary == "" {
		result.Verification.Summary = event.Error
	}
	if result.Verification.Summary == "" {
		result.Verification.Summary = "Execution ended without an independent verification result."
	}
	if !enrich && event.ID != "" && result.Execution.TerminalReason != "recovered-unconfirmed" {
		result.UsageCutoffSeq = event.Seq
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	terminalSeq := event.Seq
	if enrich {
		terminalSeq = sub.TerminalSeq
	}
	if _, err = tx.ExecContext(ctx, `UPDATE automation_submissions SET state='terminal',terminal_seq=?,result_json=?,updated_at=? WHERE id=?`, terminalSeq, string(raw), event.CreatedAt, sub.ID); err != nil {
		return err
	}
	if !enrich {
		if _, err = tx.ExecContext(ctx, `UPDATE automation_notifications SET state='canceled',last_error='task finished before attention notification was sent',updated_at=? WHERE submission_id=? AND kind='attention' AND state IN ('queued','retry')`, event.CreatedAt, sub.ID); err != nil {
			return err
		}
	}
	if sub.NotifyCompletion && sub.Recipient != "" {
		if err = queueAutomationNotificationTx(ctx, tx, sub, "completion", "", raw, event.CreatedAt); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE automation_notifications SET snapshot_json=CASE WHEN json_type(snapshot_json,'$.usageSummary')='object' THEN json_set(?, '$.usageSummary',json_extract(snapshot_json,'$.usageSummary')) ELSE ? END,updated_at=? WHERE submission_id=? AND kind='completion' AND state IN ('queued','retry')`, string(raw), string(raw), event.CreatedAt, sub.ID)
	return err
}
func queueAutomationNotificationTx(ctx context.Context, tx *sql.Tx, sub AutomationSubmission, kind, interactionID string, snapshot json.RawMessage, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO automation_notifications(id,user_id,run_id,submission_id,kind,interaction_id,recipient,snapshot_json,state,next_attempt_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,'queued',?,?,?) ON CONFLICT(user_id,run_id,submission_id,kind,interaction_id) DO NOTHING`,
		NewID("ant"), sub.UserID, sub.RunID, sub.ID, kind, interactionID, sub.Recipient, string(snapshot), now+1, now, now)
	return err
}
func (s *Store) QueueAutomationAttention(ctx context.Context, runID, interactionID string) error {
	if runID == "" || interactionID == "" || len(interactionID) > 2048 {
		return errors.New("run and interaction identity are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sub, err := scanAutomationSubmission(tx.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE run_id=? AND state IN ('accepted','dispatch_unconfirmed') AND prompt_seq>0 ORDER BY prompt_seq DESC LIMIT 1`, runID))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sub.NotifyAttention || sub.Recipient == "" {
		return nil
	}
	var title, status, endpoint string
	if err = tx.QueryRowContext(ctx, `SELECT title,status,agent_id FROM orchestration_runs WHERE id=?`, runID).Scan(&title, &status, &endpoint); err != nil {
		return err
	}
	if automationTerminal(status) {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "runId": runID, "submissionId": sub.ID, "title": title, "endpointId": endpoint, "needsAttention": true, "summary": "The task needs your approval or answer. Reconnect to the task to inspect and respond."})
	if err = queueAutomationNotificationTx(ctx, tx, sub, "attention", interactionID, raw, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CancelAutomationAttention(ctx context.Context, runID, interactionID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE automation_notifications SET state='canceled',last_error='interaction closed',updated_at=? WHERE run_id=? AND interaction_id=? AND kind='attention' AND state IN ('queued','retry')`, time.Now().Unix(), runID, interactionID)
	return err
}
func (s *Store) ListAutomationNotifications(ctx context.Context, uid, runID, submissionID string, limit int) ([]AutomationNotification, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+automationNotificationColumns+` FROM automation_notifications WHERE user_id=? AND (?='' OR run_id=?) AND (?='' OR submission_id=?) ORDER BY created_at DESC,id DESC LIMIT ?`, uid, runID, runID, submissionID, submissionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationNotification{}
	for rows.Next() {
		n, err := scanAutomationNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) ClaimAutomationNotification(ctx context.Context, leaseDuration time.Duration) (AutomationNotification, error) {
	if leaseDuration < time.Second {
		leaseDuration = time.Minute
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AutomationNotification{}, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	// Check current settings before claiming. A changed recipient is not a
	// grant to redirect an already frozen private report to a different address.
	rows, err := tx.QueryContext(ctx, `SELECT `+automationNotificationColumns+` FROM automation_notifications WHERE (state IN ('queued','retry') AND next_attempt_at<=?) OR (state='sending' AND lease_until<=?) ORDER BY created_at,id LIMIT 100`, now, now)
	if err != nil {
		return AutomationNotification{}, err
	}
	var due []AutomationNotification
	for rows.Next() {
		n, err := scanAutomationNotification(rows)
		if err != nil {
			rows.Close()
			return n, err
		}
		due = append(due, n)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return AutomationNotification{}, err
	}
	rows.Close()
	for _, n := range due {
		prefs, err := automationNotificationPreferencesTx(ctx, tx, n.UserID)
		if err != nil {
			return n, err
		}
		sub, err := scanAutomationSubmission(tx.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE id=?`, n.SubmissionID))
		if err != nil {
			return n, err
		}
		enabled := prefs.CompletionEnabled
		if n.Kind == "attention" {
			enabled = prefs.AttentionEnabled
		}
		// background-mail accepted both notifications as part of this submission;
		// a later explicit account disable is still respected.
		if prefs.Email == "" || prefs.Email != n.Recipient || !enabled || (n.Kind == "attention" && sub.State == "terminal") {
			if _, err = tx.ExecContext(ctx, `UPDATE automation_notifications SET state='canceled',last_error='notification preferences or interaction changed',updated_at=? WHERE id=?`, now, n.ID); err != nil {
				return n, err
			}
			continue
		}
		n.ClaimToken = NewID("acl")
		n.LeaseUntil = now + int64(leaseDuration/time.Second)
		n.State = "sending"
		n.Attempts++
		n.UpdatedAt = now
		if _, err = tx.ExecContext(ctx, `UPDATE automation_notifications SET state='sending',claim_token=?,lease_until=?,attempts=attempts+1,updated_at=? WHERE id=?`, n.ClaimToken, n.LeaseUntil, now, n.ID); err != nil {
			return n, err
		}
		if err = tx.Commit(); err != nil {
			return n, err
		}
		return n, nil
	}
	if err = tx.Commit(); err != nil {
		return AutomationNotification{}, err
	}
	return AutomationNotification{}, ErrNotFound
}
func (s *Store) FinishAutomationNotification(ctx context.Context, id, claimToken string, sendError error, retryAt time.Time) error {
	state := "sent"
	errText := ""
	var sentAt int64
	var nextAttemptAt int64
	now := time.Now().Unix()
	if sendError != nil {
		state = "failed"
		errText = sendError.Error()
		if len(errText) > 500 {
			errText = errText[:500]
		}
		if !retryAt.IsZero() {
			state = "retry"
			nextAttemptAt = retryAt.Unix()
		}
	} else {
		sentAt = now
	}
	res, err := s.db.ExecContext(ctx, `UPDATE automation_notifications SET state=?,last_error=?,next_attempt_at=?,lease_until=0,claim_token='',updated_at=?,sent_at=? WHERE id=? AND state='sending' AND claim_token=?`, state, errText, nextAttemptAt, now, sentAt, id, claimToken)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// ReconcileAutomationNotifications repairs a status/event transaction gap after
// a Hub restart without inferring a verification pass from a completed status.
func (s *Store) ReconcileAutomationNotifications(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT a.run_id FROM automation_submissions a JOIN orchestration_runs r ON r.id=a.run_id WHERE a.state<>'terminal' AND a.prompt_seq>0 AND r.status IN ('completed','failed','canceled') AND r.updated_at<=? AND NOT EXISTS (SELECT 1 FROM orchestration_task_graphs g WHERE g.run_id=r.id AND g.status='running') LIMIT 100`, time.Now().Unix()-2)
	if err != nil {
		return err
	}
	var runIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		runIDs = append(runIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range runIDs {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		run, err := scanOrchestrationRun(tx.QueryRowContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE id=?`, id))
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = automationReconcileRunTx(ctx, tx, run); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
func automationReconcileRunTx(ctx context.Context, tx *sql.Tx, run OrchestrationRun) error {
	if !automationTerminal(run.Status) {
		return nil
	}
	sub, err := scanAutomationSubmission(tx.QueryRowContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE run_id=? AND prompt_seq>0 ORDER BY prompt_seq DESC LIMIT 1`, run.ID))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sub.State == "terminal" {
		return nil
	}
	final, err := scanOrchestrationEvent(tx.QueryRowContext(ctx, `SELECT id,run_id,seq,kind,COALESCE(source,''),COALESCE(severity,''),COALESCE(role,''),COALESCE(cli,''),COALESCE(turn_id,''),COALESCE(content,''),COALESCE(status,''),COALESCE(error,''),COALESCE(data_json,''),created_at FROM orchestration_events WHERE run_id=? AND seq>? AND kind IN ('run.end','run.error','run.cancelled') AND (data_json IS NULL OR json_extract(data_json,'$.task') IS NULL) ORDER BY seq DESC LIMIT 1`, run.ID, sub.PromptSeq))
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		kind := "run.end"
		if run.Status == OrchestrationFailed {
			kind = "run.error"
		}
		if run.Status == OrchestrationCanceled {
			kind = "run.cancelled"
		}
		final = OrchestrationEvent{RunID: run.ID, Kind: kind, Status: run.Status, Error: run.Error, CreatedAt: time.Now().Unix(), RunEndData: &protocol.RunEndData{TerminalReason: "recovered-unconfirmed"}, RunConclusion: &protocol.RunConclusion{Outcome: "blocked", VerificationStatus: "unknown", Summary: "Execution ended, but its verification conclusion was not recorded before recovery."}}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM orchestration_events WHERE run_id=?`, run.ID).Scan(&final.Seq); err != nil {
			return err
		}
	}
	return automationFinalizeEventTx(ctx, tx, final, false)
}
