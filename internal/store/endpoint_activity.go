package store

import "context"

// AgentHasActiveWork includes idle gaps between attempts of a durable task.
func (s *Store) AgentHasActiveWork(ctx context.Context, agentID string) (bool, error) {
	var busy bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM runs JOIN sessions ON runs.session_id=sessions.id
              WHERE sessions.agent_id=? AND runs.status IN ('queued','running'))
    OR EXISTS(SELECT 1 FROM orchestration_runs
              WHERE agent_id=? AND status IN ('queued','running','canceling'))`, agentID, agentID).Scan(&busy)
	return busy, err
}
