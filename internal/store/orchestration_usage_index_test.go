package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOrchestrationUsageSparseIndexKeepsTimelineSemantics(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "usage", "owner", "agent-a", "usage", 1)
	if _, err := st.db.Exec(`DROP INDEX idx_orchestration_events_usage`); err != nil {
		t.Fatal(err)
	}
	var expectedSeqs []int64
	for i, event := range []struct {
		kind, data string
		include    bool
	}{
		{"turn.delta", `{"category":"native-identity"}`, false},
		{"command.start", `{}`, false},
		{"bridge.note", `not json`, false},
		{"bridge.note", `{"category":"other"}`, false},
		{"bridge.note", `{"category":"native-identity"}`, true},
		{"bridge.note", `{"extra":{"category":"native-identity"}}`, true},
		{"bridge.note", `{"extra":{"category":"other"},"category":"native-identity"}`, false},
		{"run.start", `{}`, true},
		{"turn.start", `{}`, true},
		{"turn.usage", `{}`, true},
		{"verifier.verdict", `{}`, true},
		{"run.end", `{}`, true},
	} {
		_, err := st.db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,data_json,created_at) VALUES (?, 'usage', ?, ?, ?, 1)`, NewID("event"), i+1, event.kind, event.data)
		if err != nil {
			t.Fatal(err)
		}
		if event.include {
			expectedSeqs = append(expectedSeqs, int64(i+1))
		}
	}
	before, err := st.ListOrchestrationUsageTimeline(context.Background(), "usage")
	if err != nil || len(before) != 7 {
		t.Fatalf("legacy timeline: %v %v", before, err)
	}
	// Migration must tolerate malformed old JSON and remain re-runnable.
	for i := 0; i < 2; i++ {
		if err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	after, err := st.ListOrchestrationUsageTimeline(context.Background(), "usage")
	if err != nil || len(after) != len(before) {
		t.Fatalf("indexed timeline: %v %v", after, err)
	}
	for i := range before {
		if before[i].ID != after[i].ID || after[i].Seq != expectedSeqs[i] {
			t.Fatal("index changed chronological subset")
		}
	}
	rows, err := st.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM orchestration_events WHERE run_id = ? AND `+orchestrationUsageTimelinePredicate+` ORDER BY seq`, "usage")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plans []string
	for rows.Next() {
		var a, b, c int
		var plan string
		if err := rows.Scan(&a, &b, &c, &plan); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plans, " "), "idx_orchestration_events_usage") {
		t.Fatalf("sparse index not selected: %v", plans)
	}
}

func TestOrchestrationUsageDeadlineWhileConnectionBusy(t *testing.T) {
	st := historyFixture(t)
	conn, err := st.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = st.ListOrchestrationUsageTimeline(ctx, "unused")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("busy single connection ignored deadline: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := st.ListOrchestrationUsageTimeline(ctx, "unused"); err != nil {
		t.Fatalf("canceled wait poisoned the next read: %v", err)
	}
}
