package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// Only disposable databases created by the test are opened for fixture writes.
func newUsageReadFixture(t testing.TB) (*Server, *store.Store, *sql.DB) {
	t.Helper()
	cfg := config.Default()
	cfg.Hub.DBPath = t.TempDir() + "/usage.db"
	cfg.Auth.JWTSecret = "usage-read-fixture-secret-32-bytes"
	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`INSERT INTO users(id,username,password_hash,created_at) VALUES ('usage-user','fixture','unused',1)`,
		`INSERT INTO agents(id,user_id,name,machine_id,last_seen_at) VALUES ('usage-agent','usage-user','fixture','fixture-machine',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return NewServer(&cfg, st, BuildInfo{Version: "test"}), st, db
}

func TestUsageReadErrorsNeverReturnPartialOverview(t *testing.T) {
	for _, table := range []string{"orchestration_events", "orchestration_usage_events", "orchestration_usage_syncs", "orchestration_task_graphs", "agents"} {
		t.Run(table, func(t *testing.T) {
			s, st, db := newUsageReadFixture(t)
			run := createOrchestrationRun(t, st, "usage-user", "usage-agent")
			// The agent lookup is reached only when there is billable usage.
			if _, err := db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,cli,data_json,created_at) VALUES ('usage',?,1,'turn.usage','codex','{"inputTokens":10,"native":true}',1)`, run.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?days=0", nil), "usage-user")
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), `"overview"`) {
				t.Fatalf("failed %s read yielded %d: %s", table, w.Code, w.Body.String())
			}
		})
	}
}

func TestUsageReadDeadlineAndCancellation(t *testing.T) {
	s, _, _ := newUsageReadFixture(t)
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := http.StatusRequestTimeout
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = http.StatusGatewayTimeout
		} else {
			cancel()
		}
		for _, handler := range []func(http.ResponseWriter, *http.Request, string){s.handleUsageOverview, s.handleOrchestrationStats, s.handleOrchestrationUsageSync} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/usage/overview?days=0", nil).WithContext(ctx)
			r.SetPathValue("runID", "unused")
			handler(w, r, "usage-user")
			if w.Code != want {
				t.Fatalf("deadline=%v: %d %s", deadline, w.Code, w.Body.String())
			}
		}
		cancel()
	}
}

func TestUsageOverviewEndpointReuseStaysRequestLocal(t *testing.T) {
	s, _, db := newUsageReadFixture(t)
	if _, err := db.Exec(`INSERT INTO agents(id,user_id,name,machine_id,last_seen_at,deleted_at) VALUES ('removed-agent','usage-user','removed','removed-machine',1,2)`); err != nil {
		t.Fatal(err)
	}
	for i, agentID := range []string{"usage-agent", "usage-agent", "removed-agent"} {
		runID := fmt.Sprintf("endpoint-run-%d", i)
		if _, err := db.Exec(`INSERT INTO orchestration_runs(id,user_id,agent_id,title,mode,prompt,max_turns,status,created_at,updated_at) VALUES (?,'usage-user',?,?,'collaboration','fixture',0,'completed',1,1)`, runID, agentID, runID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,cli,data_json,created_at) VALUES (?,?,1,'turn.usage','codex','{"inputTokens":10,"native":true}',1)`, "usage-"+runID, runID); err != nil {
			t.Fatal(err)
		}
	}
	read := func(wantName string) {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?days=0", nil), "usage-user")
		if w.Code != http.StatusOK {
			t.Fatalf("overview %d: %s", w.Code, w.Body.String())
		}
		var response struct {
			Overview orchestrationUsageOverview `json:"overview"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if got := response.Overview; got.Runs != 3 || got.Machines != 2 || got.TotalTokens != 30 {
			t.Fatalf("endpoint reuse changed accounting: %+v", got)
		}
		for _, item := range response.Overview.Items {
			if item.RunID == "endpoint-run-2" {
				if item.MachineID != "removed-agent" || item.MachineName != "removed-agent" {
					t.Fatalf("removed endpoint fallback changed: %+v", item)
				}
			} else if item.MachineName != wantName || item.MachineID != "fixture-machine" {
				t.Fatalf("endpoint metadata stale across requests: %+v", item)
			}
		}
	}
	read("fixture")
	if _, err := db.Exec(`UPDATE agents SET name='renamed' WHERE id='usage-agent'`); err != nil {
		t.Fatal(err)
	}
	read("renamed")
}

func TestUsageGraphMetadataPreservesEnrichmentAndTaskStats(t *testing.T) {
	_, st, db := newUsageReadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run := createOrchestrationRun(t, st, "usage-user", "usage-agent")
	run.WorkerPair, run.CreatedAt, run.FinishedAt, run.Status = "codex-codex", 100, 200, store.OrchestrationCompleted
	fixtureEvents, fixtureGraphs := verifierUsageFixture()
	for i, prompt := range []string{"first task", "follow-up task"} {
		var payload protocol.OrchestrationStartPayload
		if err := json.Unmarshal([]byte(fixtureGraphs[0].PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		payload.Prompt, payload.PromptSeq = prompt, int64(i+1)
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		graph, err := st.CreateOrchestrationTaskGraph(ctx, run.ID, string(encoded), "digest", []store.CreateTaskSpec{
			{Name: "worker", Role: store.TaskRoleWorker, WorkerSlot: "codex-a", PayloadJSON: "{}", PayloadDigest: "d"},
			{Name: "review", Role: store.TaskRoleReviewer, PayloadJSON: "{}", PayloadDigest: "d", Dependencies: []int{0}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE orchestration_task_graphs SET created_at=?,finished_at=? WHERE id=?`, 100+i*50, 149+i*50, graph.ID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			for e := range fixtureEvents {
				fixtureEvents[e].Task = &protocol.TaskAttemptRef{GraphID: graph.ID, WorkerSlot: "codex-a"}
			}
		}
	}
	metadata, err := st.ListTaskGraphMetadataByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	full, err := st.ListTaskGraphsByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 || len(metadata[0].Tasks) != 0 || len(full[0].Tasks) != 2 || len(full[0].Tasks[1].Dependencies) != 1 {
		t.Fatal("fixture must exercise tasks and dependencies")
	}
	ledger := []protocol.OrchestrationUsageEvent{{CLI: "codex", WorkerSlot: "codex-a", Model: "model-a", InputTokens: 250, OccurredAt: 170}}
	syncs := []store.OrchestrationUsageSync{{CLI: "codex", WorkerSlot: "codex-a", Status: "complete"}}
	// ByCLI is assembled from a map; equal-CLI model order is not an API
	// contract. Normalize only that order before comparing accounting values.
	var normalizeCLIOrder func(*orchestrationRunStats)
	normalizeCLIOrder = func(stats *orchestrationRunStats) {
		sort.Slice(stats.ByCLI, func(i, j int) bool {
			a, b := stats.ByCLI[i], stats.ByCLI[j]
			if a.CLI != b.CLI {
				return a.CLI < b.CLI
			}
			if a.Model != b.Model {
				return a.Model < b.Model
			}
			return a.WorkerSlot < b.WorkerSlot
		})
		for i := range stats.Tasks {
			normalizeCLIOrder(&stats.Tasks[i])
		}
	}
	build := func(graphs []store.OrchestrationTaskGraph) (orchestrationRunStats, []store.OrchestrationEvent) {
		// Enrichment mutates map fields, so both paths get independent input.
		encoded, err := json.Marshal(fixtureEvents)
		if err != nil {
			t.Fatal(err)
		}
		var events []store.OrchestrationEvent
		if err := json.Unmarshal(encoded, &events); err != nil {
			t.Fatal(err)
		}
		events = enrichVerifierUsage(enrichSnapshotModelsFromLedger(enrichOrchestrationUsageTimeline(events, graphs), ledger), graphs)
		stats := buildOrchestrationRunStatsWithLedger(run, events, ledger, syncs)
		stats.Tasks = buildOrchestrationTaskStats(run, events, ledger, syncs, graphs)
		normalizeCLIOrder(&stats)
		return stats, events
	}
	want, wantEvents := build(full)
	got, gotEvents := build(metadata)
	if !reflect.DeepEqual(wantEvents, gotEvents) || !reflect.DeepEqual(want, got) {
		t.Fatalf("metadata changed accounting: full=%+v metadata=%+v", want, got)
	}
	if len(got.Tasks) != 2 || gotEvents[3].Data["workerSlot"] != "verifier:codex-b" || gotEvents[4].Data["model"] != "model-a" {
		t.Fatalf("lost task/verifier identity: %+v %+v", got.Tasks, gotEvents)
	}
	if !reflect.DeepEqual(planningUsageRecoveryHints(fixtureEvents, full), planningUsageRecoveryHints(fixtureEvents, metadata)) {
		t.Fatal("model recovery hints changed")
	}
}

// Benchmark the complete HTTP handler (all runs, graphs, ledger, sync, agent,
// enrichment, accounting and JSON). Both paths use metadata-only graph reads;
// the only variable is the usage-event index. This is not a production replay.
func BenchmarkUsageOverviewLargeHistory(b *testing.B) {
	s, st, db := newUsageReadFixture(b)
	const runs, deltas, usages = 32, 2000, 64
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	insert, err := tx.Prepare(`INSERT INTO orchestration_events(id,run_id,seq,kind,cli,content,data_json,created_at) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	defer insert.Close()
	content := strings.Repeat("x", 2048)
	for i := 0; i < runs; i++ {
		id := fmt.Sprintf("run-%02d", i)
		if _, err := tx.Exec(`INSERT INTO orchestration_runs(id,user_id,agent_id,title,mode,prompt,max_turns,status,created_at,updated_at) VALUES (?,'usage-user','usage-agent',?,'collaboration','fixture',0,'completed',1,1)`, id, id); err != nil {
			b.Fatal(err)
		}
		for n := 0; n < deltas+usages; n++ {
			kind, text, data := "assistant.delta", content, "{}"
			if n >= deltas {
				kind, text, data = "turn.usage", "", `{"workerSlot":"codex-a","model":"gpt-5.5","inputTokens":100,"outputTokens":10,"native":true}`
			}
			if _, err := insert.Exec(fmt.Sprintf("e-%d-%d", i, n), id, n+1, kind, "codex", text, data, n+1); err != nil {
				b.Fatal(err)
			}
		}
		for n := 0; n < 4; n++ {
			if _, err := tx.Exec(`INSERT INTO orchestration_task_graphs(id,run_id,generation,status,parallel_limit,payload_json,payload_digest,created_at,updated_at) VALUES (?,?,?,'completed',2,?,'digest',?,1)`, fmt.Sprintf("graph-%d-%d", i, n), id, n+1, fmt.Sprintf(`{"prompt":"task %d","promptSeq":%d}`, n, n+1), 1+n*500); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO orchestration_usage_syncs(run_id,cli,worker_slot,session_id,status,event_count,scanned_at) VALUES (?,'codex','codex-a','session','complete',1,1)`, id); err != nil {
			b.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO orchestration_usage_events(run_id,cli,worker_slot,session_id,event_id,occurred_at,model,input_tokens,output_tokens) VALUES (?,'codex','codex-a','session','ledger',1,'gpt-5.5',500,20)`, id); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	read := func() string {
		w := httptest.NewRecorder()
		s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?days=0", nil), "usage-user")
		if w.Code != http.StatusOK {
			b.Fatalf("overview %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	want := read()
	for _, indexed := range []bool{false, true} {
		if indexed {
			if err := st.Migrate(ctx); err != nil {
				b.Fatal(err)
			}
		} else if _, err := db.Exec(`DROP INDEX idx_orchestration_events_usage`); err != nil {
			b.Fatal(err)
		}
		if got := read(); got != want {
			b.Fatal("index changed overview JSON")
		}
		b.Run(fmt.Sprintf("sparse_index_%t", indexed), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if got := read(); got != want {
					b.Fatal("unstable overview JSON")
				}
			}
		})
	}
}
