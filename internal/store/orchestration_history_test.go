package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func historyFixture(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	for _, user := range []string{"owner", "other"} {
		if _, err := st.db.Exec(`INSERT INTO users(id, username, password_hash, created_at) VALUES (?, ?, 'unused', 1)`, user, user); err != nil {
			t.Fatal(err)
		}
	}
	for _, agent := range []string{"agent-a", "agent-b"} {
		if _, err := st.db.Exec(`INSERT INTO agents(id, user_id, name, machine_id, last_seen_at) VALUES (?, 'owner', ?, ?, 1)`, agent, agent, agent); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func insertHistoryRun(t *testing.T, st *Store, id, user, agent, title string, at int64) {
	t.Helper()
	_, err := st.db.Exec(`INSERT INTO orchestration_runs(id,user_id,agent_id,title,mode,prompt,max_turns,status,created_at,updated_at) VALUES (?,?,?,?, 'collaboration', 'original prompt', 2, 'completed', ?, ?)`, id, user, agent, title, at, at)
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrchestrationHistoryPaginationFiltersAndOwnership(t *testing.T) {
	ctx := context.Background()
	st := historyFixture(t)
	for i := 0; i < 267; i++ {
		insertHistoryRun(t, st, fmt.Sprintf("run-%03d", i), "owner", "agent-a", fmt.Sprintf("Title %d", i), int64(100+i/3))
	}
	insertHistoryRun(t, st, "older-endpoint", "owner", "agent-b", `100%_\ literal`, 1)
	insertHistoryRun(t, st, "foreign", "other", "agent-a", "Foreign title", 9999)
	first, err := st.ListOrchestrationHistory(ctx, "owner", OrchestrationHistoryFilter{Limit: 37})
	if err != nil || len(first.Runs) != 37 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	repeat, err := st.ListOrchestrationHistory(ctx, "owner", OrchestrationHistoryFilter{Limit: 37})
	if err != nil || repeat.NextCursor != first.NextCursor {
		t.Fatalf("repeated page changed: %v", err)
	}
	// New activity/renames cannot change this traversal's creation-time order.
	title := "renamed while paging"
	if _, err := st.UpdateOrchestrationMetadata(ctx, first.Runs[0].ID, "owner", &title, nil); err != nil {
		t.Fatal(err)
	}
	insertHistoryRun(t, st, "new-after-first-page", "owner", "agent-a", "new", 20000)
	seen := map[string]bool{}
	page := first
	var previous OrchestrationRun
	for {
		for _, run := range page.Runs {
			if seen[run.ID] || run.UserID != "owner" || run.ID == "new-after-first-page" || (previous.ID != "" && (run.CreatedAt > previous.CreatedAt || (run.CreatedAt == previous.CreatedAt && run.ID >= previous.ID))) {
				t.Fatalf("unstable or foreign page entry: %+v", run)
			}
			seen[run.ID], previous = true, run
		}
		if page.NextCursor == "" {
			break
		}
		page, err = st.ListOrchestrationHistory(ctx, "owner", OrchestrationHistoryFilter{Limit: 37, Cursor: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 268 || !seen["older-endpoint"] {
		t.Fatalf("paged %d unique runs, want 268 including older endpoint", len(seen))
	}
	for _, filter := range []OrchestrationHistoryFilter{{AgentID: "agent-b"}, {Query: `%_\`}, {Query: "100%_"}} {
		page, err := st.ListOrchestrationHistory(ctx, "owner", filter)
		if err != nil || len(page.Runs) != 1 || page.Runs[0].ID != "older-endpoint" {
			t.Fatalf("filter %+v: %#v, %v", filter, page, err)
		}
	}
	for _, filter := range []OrchestrationHistoryFilter{{AgentID: "missing"}, {Status: OrchestrationRunning}, {Query: "' OR 1=1 --"}, {Archived: "true"}} {
		page, err := st.ListOrchestrationHistory(ctx, "owner", filter)
		if err != nil || len(page.Runs) != 0 || page.Runs == nil {
			t.Fatalf("empty filter %+v: %#v, %v", filter, page, err)
		}
	}
	for _, filter := range []OrchestrationHistoryFilter{{Archived: "bad"}, {Status: "bad"}, {Limit: 201}, {Cursor: "bad"}, {Cursor: first.NextCursor, AgentID: "agent-a"}} {
		if _, err := st.ListOrchestrationHistory(ctx, "owner", filter); !errors.Is(err, ErrInvalidOrchestrationHistory) {
			t.Fatalf("filter %+v should fail: %v", filter, err)
		}
	}
	if _, err := st.ListOrchestrationHistory(ctx, "other", OrchestrationHistoryFilter{Cursor: first.NextCursor}); !errors.Is(err, ErrInvalidOrchestrationHistory) {
		t.Fatalf("foreign cursor accepted: %v", err)
	}
}

func TestOrchestrationHistoryMetadataContinuityAndConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	st := historyFixture(t)
	insertHistoryRun(t, st, "run", "owner", "agent-a", "before", 100)
	if _, err := st.db.Exec(`UPDATE orchestration_runs SET codex_thread_id='native-thread', finished_at=200 WHERE id='run'`); err != nil {
		t.Fatal(err)
	}
	yes, no, title := true, false, "  "+strings.Repeat("改", 200)+"  "
	run, err := st.UpdateOrchestrationMetadata(ctx, "run", "owner", &title, &yes)
	if err != nil || run.Title != strings.TrimSpace(title) || run.ArchivedAt < time.Now().Add(-time.Minute).UnixMilli() || run.CodexThreadID != "native-thread" {
		t.Fatalf("metadata = %+v, %v", run, err)
	}
	for _, archive := range []string{"false", "true", "all"} {
		page, err := st.ListOrchestrationHistory(ctx, "owner", OrchestrationHistoryFilter{Archived: archive})
		want := 1
		if archive == "false" {
			want = 0
		}
		if err != nil || len(page.Runs) != want {
			t.Fatalf("archive %s: %+v, %v", archive, page, err)
		}
	}
	all, err := st.ListAllOrchestrationRuns(ctx, "owner")
	if err != nil || len(all) != 1 {
		t.Fatalf("archive must remain in usage: %v", err)
	}
	run, err = st.UpdateOrchestrationMetadata(ctx, "run", "owner", nil, &no)
	if err != nil || run.ID != "run" || run.ArchivedAt != 0 || run.CodexThreadID != "native-thread" || run.Prompt != "original prompt" {
		t.Fatalf("restore lost continuity: %+v, %v", run, err)
	}
	if _, err := st.UpdateOrchestrationMetadata(ctx, "run", "other", nil, &yes); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign mutation: %v", err)
	}
	for _, invalid := range []string{"   ", strings.Repeat("改", 201)} {
		if _, err := st.UpdateOrchestrationMetadata(ctx, "run", "owner", &invalid, nil); !errors.Is(err, ErrInvalidOrchestrationHistory) {
			t.Fatalf("invalid title accepted: %v", err)
		}
	}
	for i := 0; i < 20; i++ {
		if err := st.UpdateOrchestrationRunStatus(ctx, "run", OrchestrationCompleted, ""); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var archiveErr, claimErr error
		var claimed bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, archiveErr = st.UpdateOrchestrationMetadata(ctx, "run", "owner", nil, &yes)
		}()
		go func() { defer wg.Done(); <-start; claimed, claimErr = st.ClaimOrchestrationRunForContinue(ctx, "run") }()
		close(start)
		wg.Wait()
		if (archiveErr != nil && !errors.Is(archiveErr, ErrConflict)) || claimErr != nil || !claimed {
			t.Fatalf("concurrent archive=%v claim=%v/%v", archiveErr, claimed, claimErr)
		}
		run, err := st.OrchestrationRunByID(ctx, "run", "owner")
		if err != nil || run.Status != OrchestrationRunning || run.ArchivedAt != 0 || run.CodexThreadID != "native-thread" {
			t.Fatalf("active work hidden or continuity lost: %+v, %v", run, err)
		}
	}
}

func TestOrchestrationHistoryMigrationIsAdditiveAndRepeatable(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "legacy", "owner", "agent-a", "legacy", 100)
	// Model a pre-history database, including its existing rows.
	for _, query := range []string{`DROP INDEX idx_orchestration_runs_history`, `DROP INDEX idx_orchestration_runs_history_agent`, `DROP INDEX idx_orchestration_runs_history_archive`, `ALTER TABLE orchestration_runs DROP COLUMN archived_at`} {
		if _, err := st.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		page, err := st.ListOrchestrationHistory(context.Background(), "owner", OrchestrationHistoryFilter{})
		if err != nil || len(page.Runs) != 1 || page.Runs[0].ID != "legacy" || page.Runs[0].ArchivedAt != 0 {
			t.Fatalf("migration changed old run: %+v, %v", page, err)
		}
	}
}

func TestOrchestrationHistoryMigrationAfterLegacyTableRebuild(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "legacy", "owner", "agent-a", "legacy", 100)
	if _, err := st.db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,created_at) VALUES ('legacy-event','legacy',1,'run.start',100)`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DROP INDEX idx_orchestration_runs_history`, `DROP INDEX idx_orchestration_runs_history_agent`, `DROP INDEX idx_orchestration_runs_history_archive`, `ALTER TABLE orchestration_runs DROP COLUMN archived_at`} {
		if _, err := st.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	var schema string
	if err := st.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='orchestration_runs'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	legacySchema := strings.Replace(schema, "CREATE TABLE orchestration_runs", "CREATE TABLE legacy_runs", 1)
	legacySchema = strings.ReplaceAll(legacySchema, ",'claude-claude'", "")
	if strings.Contains(legacySchema, "'claude-claude'") || !strings.Contains(legacySchema, "CREATE TABLE legacy_runs") {
		t.Fatalf("unexpected fixture schema: %s", schema)
	}
	// Recreate the historical two-worker-pair CHECK, preserving its child rows.
	if _, err := st.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{legacySchema, `INSERT INTO legacy_runs SELECT * FROM orchestration_runs`, `DROP TABLE orchestration_runs`, `ALTER TABLE legacy_runs RENAME TO orchestration_runs`, `PRAGMA foreign_keys=ON`} {
		if _, err := st.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		run, err := st.OrchestrationRunByID(context.Background(), "legacy", "owner")
		if err != nil || run.ArchivedAt != 0 || run.Title != "legacy" || run.WorkerPair != "claude-codex" {
			t.Fatalf("legacy rebuild lost data: %+v %v", run, err)
		}
		events, err := st.ListOrchestrationUsageTimeline(context.Background(), run.ID)
		if err != nil || len(events) != 1 || events[0].ID != "legacy-event" {
			t.Fatalf("legacy rebuild lost events: %+v %v", events, err)
		}
	}
}
