package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

type automationUsageQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// All reads share one database snapshot; no nested Store call acquires the
// single connection. Callers aggregate after this transaction has closed.
type AutomationNotificationUsageInput struct {
	Run            OrchestrationRun
	Timeline       []OrchestrationEvent
	Ledger         []protocol.OrchestrationUsageEvent
	Syncs          []OrchestrationUsageSync
	Graphs         []OrchestrationTaskGraph
	HasLaterPrompt bool
	CutoffExists   bool
}

func (s *Store) ReadAutomationNotificationUsage(ctx context.Context, uid, runID string, throughSeq int64) (AutomationNotificationUsageInput, error) {
	var input AutomationNotificationUsageInput
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return input, err
	}
	defer tx.Rollback()
	input.Run, err = scanOrchestrationRun(tx.QueryRowContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE id=? AND user_id=? AND delete_requested=0`, runID, uid))
	if err != nil {
		return input, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orchestration_events WHERE run_id=? AND seq=? AND kind IN ('run.end','run.error','run.cancelled') AND (data_json IS NULL OR json_extract(data_json,'$.task') IS NULL))`, runID, throughSeq).Scan(&input.CutoffExists); err != nil {
		return input, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orchestration_events WHERE run_id=? AND kind='user.message' AND seq>?)`, runID, throughSeq).Scan(&input.HasLaterPrompt); err != nil {
		return input, err
	}
	if !input.CutoffExists || input.HasLaterPrompt {
		return input, tx.Commit()
	}
	var after int64
	for after < throughSeq {
		batch, err := readOrchestrationUsageBatchWith(ctx, tx, runID, after, throughSeq)
		if err != nil {
			return input, err
		}
		if len(batch) == 0 {
			break
		}
		for _, raw := range batch {
			input.Timeline = append(input.Timeline, decodeOrchestrationEvent(raw.event, raw.dataJSON))
		}
		after = batch[len(batch)-1].event.Seq
	}
	if input.Ledger, err = listOrchestrationUsageEvents(ctx, tx, runID); err != nil {
		return input, err
	}
	if input.Syncs, err = listOrchestrationUsageSyncs(ctx, tx, runID); err != nil {
		return input, err
	}
	if input.Graphs, err = listTaskGraphMetadataByRun(ctx, tx, runID); err != nil {
		return input, err
	}
	return input, tx.Commit()
}

func (s *Store) ClaimedAutomationNotification(ctx context.Context, id, claimToken string) (AutomationNotification, error) {
	return scanAutomationNotification(s.db.QueryRowContext(ctx, `SELECT `+automationNotificationColumns+` FROM automation_notifications WHERE id=? AND state='sending' AND claim_token=? AND lease_until>?`, id, claimToken, time.Now().Unix()))
}

// Freeze only this outbox snapshot; the accepted submission result stays intact.
func (s *Store) FreezeAutomationNotificationUsage(ctx context.Context, id, claimToken string, original json.RawMessage, usage AutomationUsageSummary) (json.RawMessage, error) {
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(original, &snapshot); err != nil {
		return nil, err
	}
	if raw := snapshot["usageSummary"]; len(raw) > 0 && string(raw) != "null" {
		return nil, ErrConflict
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return nil, err
	}
	snapshot["usageSummary"] = raw
	frozen, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE automation_notifications SET snapshot_json=?,updated_at=? WHERE id=? AND state='sending' AND claim_token=? AND lease_until>? AND snapshot_json=?`, string(frozen), time.Now().Unix(), id, claimToken, time.Now().Unix(), string(original))
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrConflict
	}
	return frozen, nil
}
