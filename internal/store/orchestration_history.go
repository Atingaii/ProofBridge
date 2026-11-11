package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidOrchestrationHistory = errors.New("invalid orchestration history request")

// Run after historical table rebuilds, which must not discard the new column.
func (s *Store) migrateOrchestrationHistory(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE orchestration_runs ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0`); err != nil && !isDuplicateColumn(err) {
		return fmt.Errorf("migrate orchestration history: %w", err)
	}
	for _, query := range []string{
		`CREATE INDEX IF NOT EXISTS idx_orchestration_runs_history ON orchestration_runs(user_id, created_at DESC, id DESC) WHERE delete_requested = 0`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_runs_history_agent ON orchestration_runs(user_id, agent_id, created_at DESC, id DESC) WHERE delete_requested = 0`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_runs_history_archive ON orchestration_runs(user_id, (archived_at > 0), created_at DESC, id DESC) WHERE delete_requested = 0`,
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("index orchestration history: %w", err)
		}
	}
	return nil
}

type OrchestrationHistoryFilter struct {
	AgentID  string
	Query    string
	Status   string
	Archived string
	Limit    int
	Cursor   string
}

type OrchestrationHistoryPage struct {
	Runs       []OrchestrationRun `json:"runs"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

type orchestrationHistoryCursor struct {
	Version   int    `json:"v"`
	CreatedAt int64  `json:"at"`
	ID        string `json:"id"`
	Scope     string `json:"scope"`
}

func (s *Store) ListOrchestrationHistory(ctx context.Context, userID string, filter OrchestrationHistoryFilter) (OrchestrationHistoryPage, error) {
	page := OrchestrationHistoryPage{Runs: []OrchestrationRun{}}
	filter.AgentID, filter.Query = strings.TrimSpace(filter.AgentID), strings.TrimSpace(filter.Query)
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 1 || filter.Limit > 200 {
		return page, fmt.Errorf("%w: limit must be between 1 and 200", ErrInvalidOrchestrationHistory)
	}
	if filter.Archived == "" {
		filter.Archived = "false"
	}
	switch filter.Archived {
	case "false", "true", "all":
	default:
		return page, fmt.Errorf("%w: archived must be false, true, or all", ErrInvalidOrchestrationHistory)
	}
	switch filter.Status {
	case "", OrchestrationQueued, OrchestrationRunning, OrchestrationCanceling, OrchestrationCompleted, OrchestrationFailed, OrchestrationCanceled:
	default:
		return page, fmt.Errorf("%w: unknown status", ErrInvalidOrchestrationHistory)
	}
	// Bind cursors to both ownership and filters, without exposing account IDs.
	scopeJSON, _ := json.Marshal([]string{userID, filter.AgentID, filter.Query, filter.Status, filter.Archived})
	scopeHash := sha256.Sum256(scopeJSON)
	scope := base64.RawURLEncoding.EncodeToString(scopeHash[:])
	where, args := `user_id = ? AND delete_requested = 0`, []any{userID}
	if filter.AgentID != "" {
		where += ` AND agent_id = ?`
		args = append(args, filter.AgentID)
	}
	if filter.Archived != "all" {
		where += ` AND (archived_at > 0) = ?`
		args = append(args, filter.Archived == "true")
	}
	if filter.Status != "" {
		where += ` AND status = ?`
		args = append(args, filter.Status)
	}
	if filter.Query != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filter.Query) + "%"
		where += ` AND (title LIKE ? ESCAPE '\' OR prompt LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}
	if filter.Cursor != "" {
		if len(filter.Cursor) > 2048 {
			return page, fmt.Errorf("%w: cursor too long", ErrInvalidOrchestrationHistory)
		}
		var cursor orchestrationHistoryCursor
		raw, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.CreatedAt <= 0 || cursor.ID == "" || len(cursor.ID) > 200 || cursor.Scope != scope {
			return page, fmt.Errorf("%w: invalid cursor for these filters", ErrInvalidOrchestrationHistory)
		}
		where += ` AND (created_at, id) < (?, ?)`
		args = append(args, cursor.CreatedAt, cursor.ID)
	}
	args = append(args, filter.Limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		run, err := scanOrchestrationRun(rows)
		if err != nil {
			return page, err
		}
		page.Runs = append(page.Runs, run)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Runs) > filter.Limit {
		page.Runs = page.Runs[:filter.Limit]
		last := page.Runs[len(page.Runs)-1]
		raw, _ := json.Marshal(orchestrationHistoryCursor{Version: 1, CreatedAt: last.CreatedAt, ID: last.ID, Scope: scope})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

// The transaction serializes archive eligibility with continuation's status
// claim. A claim always clears archived_at; a later archive sees an active run.
func (s *Store) UpdateOrchestrationMetadata(ctx context.Context, id, userID string, title *string, archived *bool) (OrchestrationRun, error) {
	if title == nil && archived == nil {
		return OrchestrationRun{}, fmt.Errorf("%w: title or archived is required", ErrInvalidOrchestrationHistory)
	}
	var normalizedTitle string
	if title != nil {
		normalizedTitle = strings.TrimSpace(*title)
		if !utf8.ValidString(normalizedTitle) || utf8.RuneCountInString(normalizedTitle) < 1 || utf8.RuneCountInString(normalizedTitle) > 200 {
			return OrchestrationRun{}, fmt.Errorf("%w: title must contain 1 to 200 characters", ErrInvalidOrchestrationHistory)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationRun{}, err
	}
	defer tx.Rollback()
	run, err := scanOrchestrationRun(tx.QueryRowContext(ctx, `SELECT `+orchestrationRunColumns+` FROM orchestration_runs WHERE id = ? AND user_id = ? AND delete_requested = 0`, id, userID))
	if err != nil {
		return OrchestrationRun{}, err
	}
	if archived != nil && *archived {
		switch run.Status {
		case OrchestrationCompleted, OrchestrationFailed, OrchestrationCanceled:
		default:
			return OrchestrationRun{}, ErrConflict
		}
		if run.ArchivedAt == 0 {
			run.ArchivedAt = time.Now().UnixMilli()
		}
	} else if archived != nil {
		run.ArchivedAt = 0
	}
	if title != nil {
		run.Title = normalizedTitle
	}
	run.UpdatedAt = time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE orchestration_runs SET title = ?, archived_at = ?, updated_at = ? WHERE id = ? AND user_id = ?`, run.Title, run.ArchivedAt, run.UpdatedAt, id, userID); err != nil {
		return OrchestrationRun{}, err
	}
	return run, tx.Commit()
}
