package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// ReconcileAutomationSubmissions runs once at Hub startup, before HTTP serving
// or Bridge dispatch begins. Never call it from a periodic worker: a current
// request legitimately has a short reservation-to-prompt acceptance interval.
//
// A persisted user.message is required before any dispatch. A reservation with
// no PromptSeq therefore cannot have started work, whereas an accepted prompt
// without execution evidence remains delivery-unknown and is never replayed.
func (s *Store) ReconcileAutomationSubmissions(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for {
		rows, err := tx.QueryContext(ctx, `SELECT `+automationSubmissionColumns+` FROM automation_submissions WHERE prompt_seq=0 AND state IN ('validating','dispatch_unconfirmed') LIMIT 100`)
		if err != nil {
			return err
		}
		var pending []AutomationSubmission
		for rows.Next() {
			sub, scanErr := scanAutomationSubmission(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			pending = append(pending, sub)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(pending) == 0 {
			break
		}
		for _, sub := range pending {
			if err = rejectUnacceptedAutomationSubmissionTx(ctx, tx, sub); err != nil {
				return err
			}
		}
	}
	// A graph with this exact PromptSeq owns durable dispatch responsibility.
	// A Bridge run.start after the prompt is independent execution evidence.
	// An older graph from a previous submission must not confirm a follow-up.
	_, err = tx.ExecContext(ctx, `UPDATE automation_submissions AS a SET state='dispatch_unconfirmed',updated_at=?
		WHERE a.state='accepted' AND a.prompt_seq>0
		AND EXISTS (SELECT 1 FROM orchestration_runs r WHERE r.id=a.run_id AND r.status IN ('queued','running','canceling'))
		AND NOT EXISTS (SELECT 1 FROM orchestration_events e WHERE e.run_id=a.run_id AND e.kind='run.start' AND e.seq>a.prompt_seq)
		AND NOT EXISTS (SELECT 1 FROM orchestration_task_graphs g WHERE g.run_id=a.run_id
			AND CASE WHEN json_valid(g.payload_json) THEN CAST(json_extract(g.payload_json,'$.promptSeq') AS INTEGER) ELSE 0 END=a.prompt_seq)`, time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func rejectUnacceptedAutomationSubmissionTx(ctx context.Context, tx *sql.Tx, sub AutomationSubmission) error {
	const reason = "Hub restarted before this submission was accepted; no task was dispatched. Submit a new request explicitly if you still want to run it."
	now := time.Now().Unix()
	result := AutomationResult{
		SchemaVersion: 1, RunID: sub.RunID, SubmissionID: sub.ID, Configuration: sub.Configuration, FinishedAt: now,
		Execution:    AutomationExecutionResult{Status: OrchestrationFailed, TerminalReason: "not-accepted-before-restart"},
		Verification: AutomationVerificationResult{Authority: "unavailable", Status: "unknown", Summary: reason, Checks: []protocol.VerifierCheck{}, UnmetObligations: []string{}},
	}
	if sub.RunID != "" {
		run, err := scanOrchestrationRun(tx.QueryRowContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE id=?`, sub.RunID))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			result.Title, result.EndpointID = run.Title, run.AgentID
			if _, err = tx.ExecContext(ctx, `UPDATE orchestration_runs SET status=?,error=?,finished_at=?,updated_at=? WHERE id=? AND status IN ('queued','running','canceling')`, OrchestrationFailed, reason, now, now, sub.RunID); err != nil {
				return err
			}
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE automation_submissions SET state='rejected',result_json=?,updated_at=? WHERE id=? AND prompt_seq=0`, string(raw), now, sub.ID); err != nil {
		return err
	}
	// An unaccepted reservation must never become a completion email, even if
	// its requested monitoring mode was background-mail.
	_, err = tx.ExecContext(ctx, `UPDATE automation_notifications SET state='canceled',last_error='submission was not accepted before restart',updated_at=? WHERE submission_id=? AND state IN ('queued','retry')`, now, sub.ID)
	return err
}
