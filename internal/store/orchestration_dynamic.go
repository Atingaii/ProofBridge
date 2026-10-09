package store

import (
	"context"
	"fmt"
)

// Run after historical table rebuilds so upgrading an old database cannot drop
// the new column. Old runs remain bounded; only explicit new requests opt in.
func (s *Store) migrateDynamicExecutionMode(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `ALTER TABLE orchestration_runs ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'bounded' CHECK(execution_mode IN ('bounded','dynamic'))`)
	if err != nil && !isDuplicateColumn(err) {
		return fmt.Errorf("migrate execution mode: %w", err)
	}
	return nil
}
