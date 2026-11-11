package store

import "context"

// Keep this predicate shared by the index and both readers. Besides markers,
// the first plan-review event clears the draft plan even without marker text.
// Task metadata is persisted by orchestrationEventDataJSON using these JSON keys.
// CASE also tolerates pre-existing malformed or empty event payloads.
const orchestrationProgressPredicate = `(content LIKE '%[PLAN_%'
    OR CASE WHEN json_valid(data_json) THEN json_extract(data_json, '$.task.name') END = 'plan-review')`

const orchestrationProgressQuery = `
    SELECT id, run_id, seq, kind, COALESCE(source,''), COALESCE(severity,''), COALESCE(role,''), COALESCE(cli,''), COALESCE(turn_id,''),
        CASE WHEN content LIKE '%[PLAN_%' THEN content ELSE '' END,
        COALESCE(status,''), COALESCE(error,''), COALESCE(data_json,''), created_at
    FROM orchestration_events
    WHERE run_id = ? AND ` + orchestrationProgressPredicate + `
        AND (content LIKE '%[PLAN_%' OR seq >= COALESCE((
            SELECT seq FROM orchestration_events WHERE run_id = ? ORDER BY seq DESC LIMIT 1 OFFSET 9999
        ), 0))
    ORDER BY seq ASC`

// ListOrchestrationProgressEvents preserves all historical plan markers and
// recent review boundaries, matching the former latest-10000 + marker union.
// Other transcript events cannot affect the reducer and are never loaded.
func (s *Store) ListOrchestrationProgressEvents(ctx context.Context, runID string) ([]OrchestrationEvent, error) {
	rows, err := s.db.QueryContext(ctx, orchestrationProgressQuery, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrchestrationEvent
	for rows.Next() {
		event, err := scanOrchestrationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
