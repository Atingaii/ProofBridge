package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// OrchestrationMessage is an idempotent user instruction. Delivered means the
// native transport accepted it, not that the model has executed the instruction.
type OrchestrationMessage struct {
	ID           string `json:"id"`
	RunID        string `json:"runId"`
	Prompt       string `json:"prompt"`
	DeliveryMode string `json:"deliveryMode"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	ExecutionID  string `json:"-"`
}

func (s *Store) migrateOrchestrationMessages(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS orchestration_messages (
 id TEXT NOT NULL, run_id TEXT NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
 prompt TEXT NOT NULL, delivery_mode TEXT NOT NULL CHECK(delivery_mode IN ('queue','immediate')),
 status TEXT NOT NULL CHECK(status IN ('queued','sending','delivered','failed')),
 error TEXT NOT NULL DEFAULT '', execution_id TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(run_id,id));
 CREATE INDEX IF NOT EXISTS idx_orchestration_messages_run ON orchestration_messages(run_id,created_at);`)
	return err
}

const messageColumns = `id,run_id,prompt,delivery_mode,status,error,created_at,updated_at,execution_id`

func scanOrchestrationMessage(row interface{ Scan(...any) error }) (OrchestrationMessage, error) {
	var m OrchestrationMessage
	err := row.Scan(&m.ID, &m.RunID, &m.Prompt, &m.DeliveryMode, &m.Status, &m.Error, &m.CreatedAt, &m.UpdatedAt, &m.ExecutionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return m, err
}
func (s *Store) OrchestrationMessageByID(ctx context.Context, runID, id string) (OrchestrationMessage, error) {
	return scanOrchestrationMessage(s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM orchestration_messages WHERE run_id=? AND id=?`, runID, id))
}
func (s *Store) CreateOrchestrationMessage(ctx context.Context, m OrchestrationMessage) (OrchestrationMessage, bool, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `INSERT INTO orchestration_messages (`+messageColumns+`) VALUES(?,?,?,?,'queued','',?,?,'') ON CONFLICT(run_id,id) DO NOTHING`, m.ID, m.RunID, m.Prompt, m.DeliveryMode, now, now)
	if err != nil {
		return m, false, err
	}
	n, _ := res.RowsAffected()
	got, err := s.OrchestrationMessageByID(ctx, m.RunID, m.ID)
	if err == nil && (got.Prompt != m.Prompt || got.DeliveryMode != m.DeliveryMode) {
		err = ErrConflict
	}
	return got, n == 1, err
}
func (s *Store) ListOrchestrationMessages(ctx context.Context, runID string) ([]OrchestrationMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM orchestration_messages WHERE run_id=? ORDER BY created_at,rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []OrchestrationMessage{}
	for rows.Next() {
		m, err := scanOrchestrationMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// Claim fences duplicate HTTP requests and stale receipts before any wire I/O.
func (s *Store) ClaimOrchestrationMessage(ctx context.Context, runID, id, execution string) (bool, error) {
	r, e := s.db.ExecContext(ctx, `UPDATE orchestration_messages SET status='sending',execution_id=?,updated_at=? WHERE run_id=? AND id=? AND status='queued'`, execution, time.Now().UnixMilli(), runID, id)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}
func (s *Store) FinishOrchestrationMessage(ctx context.Context, runID, id, execution, status, reason string) (bool, error) {
	if status != "delivered" && status != "failed" {
		return false, ErrConflict
	}
	r, e := s.db.ExecContext(ctx, `UPDATE orchestration_messages SET status=?,error=?,updated_at=? WHERE run_id=? AND id=? AND execution_id=? AND status='sending'`, status, reason, time.Now().UnixMilli(), runID, id, execution)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

// Finalize leaves uncertain writes failed and never replays them automatically.
func (s *Store) FailOrchestrationMessages(ctx context.Context, runID, execution, reason string) error {
	query := `UPDATE orchestration_messages SET status='failed',error=?,updated_at=? WHERE run_id=? AND status='sending'`
	args := []any{reason, time.Now().UnixMilli(), runID}
	if execution != "" {
		query += ` AND execution_id=?`
		args = append(args, execution)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}
func (s *Store) FailOrchestrationMessagesForAgent(ctx context.Context, agentID, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE orchestration_messages SET status='failed',error=?,updated_at=? WHERE status IN ('queued','sending') AND run_id IN (SELECT id FROM orchestration_runs WHERE agent_id=?)`, reason, time.Now().UnixMilli(), agentID)
	return err
}
