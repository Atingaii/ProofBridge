package store

import "context"

const (
	orchestrationUsageBatchRows  = 64
	orchestrationUsageBatchBytes = 512 << 10
)

const orchestrationUsageBatchQuery = `
	SELECT id, run_id, seq, kind, COALESCE(source,''), COALESCE(severity,''), COALESCE(role,''), COALESCE(cli,''), COALESCE(turn_id,''),
		COALESCE(content,''), COALESCE(status,''), COALESCE(error,''), COALESCE(data_json,''), created_at
	FROM orchestration_events
	WHERE run_id = ? AND seq > ? AND seq <= ? AND ` + orchestrationUsageTimelinePredicate + `
	ORDER BY seq ASC LIMIT ?`

type rawOrchestrationUsageEvent struct {
	event    OrchestrationEvent
	dataJSON string
}

// ListOrchestrationUsageTimeline returns every usage/session event through the
// initial sequence high-water mark. Events are append-only. Short batches let
// other requests use the single connection, including during JSON decoding.
func (s *Store) ListOrchestrationUsageTimeline(ctx context.Context, runID string) ([]OrchestrationEvent, error) {
	var out []OrchestrationEvent
	err := s.readOrchestrationUsageTimeline(ctx, runID, func(batch []rawOrchestrationUsageEvent) error {
		for _, raw := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			out = append(out, decodeOrchestrationEvent(raw.event, raw.dataJSON))
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// The consumer runs only after rows are closed, never while holding a database
// connection. The byte bound is soft: one event may itself exceed the bound.
func (s *Store) readOrchestrationUsageTimeline(ctx context.Context, runID string, consume func([]rawOrchestrationUsageEvent) error) error {
	var throughSeq int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM orchestration_events WHERE run_id = ?`, runID).Scan(&throughSeq); err != nil {
		return err
	}
	var afterSeq int64
	for afterSeq < throughSeq {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := s.readOrchestrationUsageBatch(ctx, runID, afterSeq, throughSeq)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		afterSeq = batch[len(batch)-1].event.Seq
		if err := consume(batch); err != nil {
			return err
		}
	}
	// Deleting a run cascades to its events. Never publish a partial prefix if
	// that happened between batches. This is an exact existing-index lookup.
	if throughSeq > 0 {
		var present bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orchestration_events WHERE run_id = ? AND seq = ?)`, runID, throughSeq).Scan(&present); err != nil {
			return err
		}
		if !present {
			return ErrNotFound
		}
	}
	return ctx.Err()
}

func (s *Store) readOrchestrationUsageBatch(ctx context.Context, runID string, afterSeq, throughSeq int64) ([]rawOrchestrationUsageEvent, error) {
	rows, err := s.db.QueryContext(ctx, orchestrationUsageBatchQuery, runID, afterSeq, throughSeq, orchestrationUsageBatchRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batch := make([]rawOrchestrationUsageEvent, 0, orchestrationUsageBatchRows)
	bytes := 0
	for rows.Next() {
		var raw rawOrchestrationUsageEvent
		e := &raw.event
		if err := rows.Scan(&e.ID, &e.RunID, &e.Seq, &e.Kind, &e.Source, &e.Severity, &e.Role, &e.CLI, &e.TurnID,
			&e.Content, &e.Status, &e.Error, &raw.dataJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		batch = append(batch, raw)
		bytes += len(raw.dataJSON) + len(e.Content) + len(e.Error)
		if bytes >= orchestrationUsageBatchBytes {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return batch, nil
}
