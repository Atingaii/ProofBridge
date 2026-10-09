package store

import (
	"context"
	"slices"
	"strings"
)

// AdminConversationContentPage limits source rows before content projection.
// An empty visible page can still have a cursor when it contains only native
// or command events; callers must use HasMoreBefore rather than item count.
type AdminConversationContentPage struct {
	Limit           int
	BeforeSeq       *int64
	BeforeCreatedAt *int64
	BeforeID        string
}

// AdminConversationContent exposes only administrator-visible text. It never
// reads event data_json, native metadata, or a whole conversation's delta body.
func (s *Store) AdminConversationContent(ctx context.Context, userID, kind, conversationID string, options ...AdminConversationContentPage) (AdminConversationContent, error) {
	page := AdminConversationContentPage{Limit: 200}
	if len(options) > 0 {
		page = options[0]
	}
	if page.Limit <= 0 {
		page.Limit = 200
	}
	if page.Limit > 1000 {
		page.Limit = 1000
	}
	content := AdminConversationContent{ID: conversationID, Kind: kind, Items: []AdminConversationContentItem{}}
	switch kind {
	case "chat":
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(title,'') FROM sessions WHERE id=? AND user_id=?`, conversationID, userID).Scan(&content.Title); err != nil {
			return AdminConversationContent{}, storeError(err)
		}
		where := `session_id = ?`
		args := []any{conversationID}
		if page.BeforeCreatedAt != nil {
			where += ` AND created_at <= ? AND (created_at < ? OR id < ?)`
			args = append(args, *page.BeforeCreatedAt, *page.BeforeCreatedAt, page.BeforeID)
		}
		args = append(args, page.Limit+1)
		rows, err := s.db.QueryContext(ctx, `SELECT id,role,content,created_at FROM messages WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT ?`, args...)
		if err != nil {
			return AdminConversationContent{}, err
		}
		defer rows.Close()
		type message struct {
			id   string
			item AdminConversationContentItem
		}
		var records []message
		for rows.Next() {
			var record message
			if err := rows.Scan(&record.id, &record.item.Role, &record.item.Content, &record.item.CreatedAt); err != nil {
				return AdminConversationContent{}, err
			}
			record.item.Kind = "message"
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return AdminConversationContent{}, err
		}
		content.HasMoreBefore = len(records) > page.Limit
		if content.HasMoreBefore {
			records = records[:page.Limit]
		}
		if len(records) > 0 {
			oldest := records[len(records)-1]
			content.NextBeforeCreatedAt = oldest.item.CreatedAt
			content.NextBeforeID = oldest.id
		}
		for i := len(records) - 1; i >= 0; i-- {
			content.Items = append(content.Items, records[i].item)
		}
		return content, nil
	case "orchestration":
		if err := s.db.QueryRowContext(ctx, `SELECT title,prompt FROM orchestration_runs WHERE id=? AND user_id=?`, conversationID, userID).Scan(&content.Title, &content.Prompt); err != nil {
			return AdminConversationContent{}, storeError(err)
		}
		where := `run_id = ?`
		args := []any{conversationID}
		if page.BeforeSeq != nil {
			where += ` AND seq < ?`
			args = append(args, *page.BeforeSeq)
		}
		args = append(args, page.Limit+1)
		// Limit the indexed source stream, including non-display events. Filtering
		// before LIMIT could still scan every row of a long command-only history.
		rows, err := s.db.QueryContext(ctx, `SELECT id,seq,COALESCE(role,''),COALESCE(source,''),kind,COALESCE(turn_id,''),
   CASE WHEN kind IN ('user.message','turn.delta','run.error','run.conclusion') THEN COALESCE(content,'') ELSE '' END,created_at
   FROM orchestration_events WHERE `+where+` ORDER BY seq DESC LIMIT ?`, args...)
		if err != nil {
			return AdminConversationContent{}, err
		}
		defer rows.Close()
		type event struct {
			id, turnID string
			seq        int64
			item       AdminConversationContentItem
		}
		var records []event
		for rows.Next() {
			var record event
			if err := rows.Scan(&record.id, &record.seq, &record.item.Role, &record.item.Source, &record.item.Kind, &record.turnID, &record.item.Content, &record.item.CreatedAt); err != nil {
				return AdminConversationContent{}, err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return AdminConversationContent{}, err
		}
		content.HasMoreBefore = len(records) > page.Limit
		if content.HasMoreBefore {
			records = records[:page.Limit]
		}
		if len(records) > 0 {
			content.NextBeforeSeq = records[len(records)-1].seq
		}
		slices.Reverse(records)
		// Preserve the former turn/role/source grouping within this page. A turn
		// crossing a page boundary is deliberately returned as separate fragments.
		type groupKey struct{ id, turnID, role, source string }
		groups := map[groupKey]int{}
		var fragments []*strings.Builder
		for _, record := range records {
			if record.item.Content == "" {
				continue
			}
			key := groupKey{id: record.id}
			if record.item.Kind == "turn.delta" && record.turnID != "" {
				key = groupKey{turnID: record.turnID, role: record.item.Role, source: record.item.Source}
			}
			if i, ok := groups[key]; ok {
				fragments[i].WriteString(record.item.Content)
				if record.item.CreatedAt < content.Items[i].CreatedAt {
					content.Items[i].CreatedAt = record.item.CreatedAt
				}
			} else {
				groups[key] = len(content.Items)
				text := new(strings.Builder)
				text.WriteString(record.item.Content)
				fragments = append(fragments, text)
				record.item.Content = ""
				content.Items = append(content.Items, record.item)
			}
		}
		for i, text := range fragments {
			content.Items[i].Content = text.String()
		}
		return content, nil
	default:
		return AdminConversationContent{}, ErrNotFound
	}
}
