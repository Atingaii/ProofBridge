package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOrchestrationProgressSparseIndex(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "progress", "owner", "agent-a", "progress", 1)
	insertHistoryRun(t, st, "foreign", "other", "agent-a", "foreign", 1)
	if _, err := st.db.Exec(`DROP INDEX idx_orchestration_events_progress`); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		seq           int
		content, data string
	}{
		{1, `[PLAN_ITEM id="P1" status="pending"] earliest`, `{"task":{"name":"plan","graphId":"graph"}}`},
		{2, `old review without a marker`, `{"task":{"name":"plan-review"}}`},
		{3, `[plan_item] case-insensitive legacy match`, `broken json`},
		{4, `[PLANX] underscore wildcard remains unchanged`, `{}`},
		{30001, `recent review without marker`, `{"task":{"name":"plan-review","graphId":"graph"}}`},
		{30002, `[PLAN_UPDATE id="P1" status="completed"] done`, `{"task":{"name":"review","graphId":"graph"}}`},
	}
	for _, fixture := range fixtures {
		if _, err := st.db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,content,data_json,created_at) VALUES (?, 'progress', ?, 'turn.delta', ?, ?, 1)`, fmt.Sprint(fixture.seq), fixture.seq, fixture.content, fixture.data); err != nil {
			t.Fatal(err)
		}
	}
	// A gap in sequence numbers must not change the latest-10000 *rows* boundary.
	if _, err := st.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 10000 UNION ALL SELECT i+1 FROM n WHERE i < 20000) INSERT INTO orchestration_events(id,run_id,seq,kind,content,data_json,created_at) SELECT 'noise-'||i,'progress',i,'command.output','irrelevant transcript', 'not json',1 FROM n`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,content,created_at) VALUES ('foreign','foreign',1,'turn.delta','[PLAN_ITEM] foreign',1)`); err != nil {
		t.Fatal(err)
	}
	before, err := st.ListOrchestrationPlanEvents(context.Background(), "progress")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	after, err := st.ListOrchestrationPlanEvents(context.Background(), "progress")
	if err != nil || !reflect.DeepEqual(before, after) || len(after) != 4 {
		t.Fatalf("marker semantics changed: before=%v after=%v err=%v", before, after, err)
	}
	progress, err := st.ListOrchestrationProgressEvents(context.Background(), "progress")
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, event := range progress {
		seqs = append(seqs, event.Seq)
	}
	if !reflect.DeepEqual(seqs, []int64{1, 3, 4, 30001, 30002}) {
		t.Fatalf("wrong historical markers/recent review subset: %v", seqs)
	}
	if progress[3].Task == nil || progress[3].Task.Name != "plan-review" || progress[3].Content != "" {
		t.Fatalf("review boundary not projected: %+v", progress[3])
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`SELECT id FROM orchestration_events WHERE run_id = ? AND ` + orchestrationProgressPredicate + ` AND content LIKE '%[PLAN_%' ORDER BY seq`, []any{"progress"}},
		{orchestrationProgressQuery, []any{"progress", "progress"}},
	} {
		rows, err := st.db.Query(`EXPLAIN QUERY PLAN `+query.sql, query.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plans []string
		for rows.Next() {
			var a, b, c int
			var detail string
			if err := rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			plans = append(plans, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(plans, " "), "idx_orchestration_events_progress") {
			t.Fatalf("missing sparse index: %v", plans)
		}
		t.Log(strings.Join(plans, "; "))
	}
}

func TestOrchestrationProgressCancellationReleasesConnection(t *testing.T) {
	st := historyFixture(t)
	conn, err := st.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	events, err := st.ListOrchestrationProgressEvents(ctx, "progress")
	if !errors.Is(err, context.DeadlineExceeded) || events != nil {
		t.Fatalf("cancellation returned partial success: %v %v", events, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListOrchestrationProgressEvents(context.Background(), "progress"); err != nil {
		t.Fatalf("next read failed: %v", err)
	}
}

func BenchmarkOrchestrationProgressLongHistory(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "progress.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	_, err = st.db.Exec(`INSERT INTO users(id,username,password_hash,created_at) VALUES ('u','u','unused',1);
 INSERT INTO agents(id,name,machine_id,last_seen_at) VALUES ('a','a','a',1);
 INSERT INTO orchestration_runs(id,user_id,agent_id,title,mode,prompt,max_turns,status,created_at,updated_at) VALUES ('r','u','a','r','collaboration','p',1,'running',1,1);`)
	if err != nil {
		b.Fatal(err)
	}
	_, err = st.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 270000)
 INSERT INTO orchestration_events(id,run_id,seq,kind,content,data_json,created_at)
 SELECT 'e'||i,'r',i,'turn.delta',CASE WHEN i % 10000 = 1 THEN '[PLAN_ITEM id="P1" status="pending"] plan' ELSE ? END,
 '{"task":{"graphId":"g","name":"plan"}}',1 FROM n`, strings.Repeat("transcript ", 100))
	if err != nil {
		b.Fatal(err)
	}
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := st.ListOrchestrationEvents(context.Background(), "r", 10000); err != nil {
				b.Fatal(err)
			}
			rows, err := st.db.Query(`SELECT content FROM orchestration_events INDEXED BY idx_orchestration_events_run_seq WHERE run_id='r' AND content LIKE '%[PLAN_%' ORDER BY seq`)
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
				var text string
				if err := rows.Scan(&text); err != nil {
					b.Fatal(err)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("sparse", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			events, err := st.ListOrchestrationProgressEvents(context.Background(), "r")
			if err != nil || len(events) != 27 {
				b.Fatalf("events=%d err=%v", len(events), err)
			}
		}
	})
}
