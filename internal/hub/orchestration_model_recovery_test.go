package hub

import (
	"context"
	"encoding/json"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"reflect"
	"testing"
	"time"
)

func TestModelRecoveryRetriesDespiteExistingSessionSync(t *testing.T) {
	now := int64(1800000000)
	hint := protocol.OrchestrationUsageRecovery{WorkerSlot: "codex-a", CWD: "/workspace", StartedAt: 100, EndedAt: 200, InputTokens: 100, OutputTokens: 10}
	hints := []protocol.OrchestrationUsageRecovery{hint}
	syncs := []store.OrchestrationUsageSync{{SessionID: "existing-native-session", Status: "complete", ScannedAt: now}}
	if !modelRecoveryDue(hints, syncs, now) {
		t.Fatal("existing session suppressed missing planner")
	}
	syncs = append(syncs, store.OrchestrationUsageSync{SessionID: hint.RecoveryMarkerID(), Status: "unavailable", ScannedAt: now})
	if modelRecoveryDue(hints, syncs, now+1) {
		t.Fatal("retried scan before cooldown")
	}
	if !modelRecoveryDue(hints, syncs, now+modelRecoveryRetrySeconds) {
		t.Fatal("failed scan never retried")
	}
	if modelRecoveryDue(nil, syncs, now) {
		t.Fatal("recovered model still scanned")
	}
	hint.InputTokens++
	if !modelRecoveryDue([]protocol.OrchestrationUsageRecovery{hint}, syncs, now) {
		t.Fatal("changed evidence not reexamined")
	}
	s := &Server{}
	at := time.Unix(now, 0)
	if !s.reserveUsageSync("run", at) || s.reserveUsageSync("run", at.Add(time.Second)) || !s.reserveUsageSync("other", at) {
		t.Fatal("requests not coalesced per run")
	}
	s.releaseUsageSync("run")
	if !s.reserveUsageSync("run", at) || !s.reserveUsageSync("run", at.Add(2*time.Minute)) {
		t.Fatal("request cannot retry")
	}
}

func TestUsageModelUsesExactGraphDespiteEqualTimestamps(t *testing.T) {
	payload := func(model string) string {
		raw, _ := json.Marshal(protocol.OrchestrationStartPayload{WorkerProfiles: map[string]protocol.WorkerProfileBinding{"codex-a": {CLI: "codex", Model: model}}})
		return string(raw)
	}
	graphs := []store.OrchestrationTaskGraph{{ID: "original", CreatedAt: 100, PayloadJSON: payload("original-model")}, {ID: "new", CreatedAt: 100, PayloadJSON: payload("new-model")}}
	for _, tc := range []struct{ id, want string }{{"original", "original-model"}, {"missing", ""}} {
		events := []store.OrchestrationEvent{{Kind: "turn.start", CLI: "codex", TurnID: "plan", CreatedAt: 100, Task: &protocol.TaskAttemptRef{GraphID: tc.id, WorkerSlot: "codex-a"}}, {Kind: "turn.usage", CLI: "codex", TurnID: "plan", CreatedAt: 101, Data: map[string]any{"native": true, "inputTokens": 100}}}
		got := enrichOrchestrationUsageTimeline(events, graphs)
		if model := usageText(got[1].Data, "model"); model != tc.want {
			t.Fatalf("graph %s model=%q want=%q", tc.id, model, tc.want)
		}
	}
}

func TestModelRecoveryCapabilityStatus(t *testing.T) {
	s := &Server{pool: NewPool()}
	hints := []protocol.OrchestrationUsageRecovery{{WorkerSlot: "codex-a", StartedAt: 100}}
	if got := s.modelRecoveryStatus("agent", hints, nil, 200); got != "offline" {
		t.Fatal(got)
	}
	for _, version := range []int{0, 2} {
		conn := &BridgeConn{agentID: "agent", capabilities: &protocol.BridgeCapabilities{UsageLedger: true, UsageModelRecovery: version}, wsSender: wsSender{send: make(chan protocol.Envelope, 1), done: make(chan struct{})}}
		s.pool.RegisterAgent(conn)
		want := "upgrade-required"
		if version == 2 {
			want = "pending"
		}
		if got := s.modelRecoveryStatus("agent", hints, nil, 200); got != want {
			t.Fatal(got, want)
		}
		if got := s.modelRecoveryStatus("agent", nil, nil, 200); got != "" {
			t.Fatal("no missing model", got)
		}
		if version == 2 {
			syncs := []store.OrchestrationUsageSync{{SessionID: hints[0].RecoveryMarkerID(), Status: "unavailable", ScannedAt: 200}}
			if got := s.modelRecoveryStatus("agent", hints, syncs, 201); got != "unresolved" {
				t.Fatal(got)
			}
		}
		s.pool.UnregisterAgent("agent", conn)
	}
}

func TestRecoveryMarkersCannotChangeAccounting(t *testing.T) {
	run := store.OrchestrationRun{ID: "old", CreatedAt: 100, FinishedAt: 200}
	timeline := []store.OrchestrationEvent{{Kind: "turn.usage", CLI: "codex", Data: map[string]any{"inputTokens": 100, "native": true}}}
	ledger := []protocol.OrchestrationUsageEvent{{CLI: "codex", Model: "gpt-5.5", InputTokens: 120, OccurredAt: 150}}
	syncs := []store.OrchestrationUsageSync{{SessionID: "native-session", Status: "complete", ScannedAt: 200}}
	before := buildOrchestrationRunStatsWithLedger(run, timeline, ledger, syncs)
	marker := protocol.OrchestrationUsageRecovery{StartedAt: 100}.RecoveryMarkerID()
	syncs = append(syncs, store.OrchestrationUsageSync{SessionID: marker, Status: "unavailable", ScannedAt: 300})
	after := buildOrchestrationRunStatsWithLedger(run, timeline, ledger, syncs)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("bookkeeping changed accounting: before=%+v after=%+v", before, after)
	}
}

func TestReconnectBackfillRecoversPlannerAfterUnrelatedSync(t *testing.T) {
	s, st, uid, agent := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agent)
	ctx := context.Background()
	payload := protocol.OrchestrationStartPayload{RunID: run.ID, CWD: "/workspace", WorkerProfiles: map[string]protocol.WorkerProfileBinding{"codex-a": {CLI: "codex", OfficialLogin: true}}}
	raw, _ := json.Marshal(payload)
	digest := taskPayloadDigest(string(raw))
	graph, err := st.CreateOrchestrationTaskGraph(ctx, run.ID, string(raw), digest, orchestrationTaskSpecs(digest, "codex-codex", "collaboration"))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []store.OrchestrationEvent{
		{RunID: run.ID, Kind: "turn.start", CLI: "codex", TurnID: "plan", Task: &protocol.TaskAttemptRef{GraphID: graph.ID, Role: "planner", WorkerSlot: "codex-a"}},
		{RunID: run.ID, Kind: "turn.usage", CLI: "codex", TurnID: "plan", Data: map[string]any{"native": true, "inputTokens": 198150, "cacheReadTokens": 145920, "outputTokens": 2709}},
	} {
		if _, err := st.AddOrchestrationEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceOrchestrationUsage(ctx, protocol.OrchestrationUsageSyncResult{RunID: run.ID, ScannedAt: time.Now().Unix(), Sessions: []protocol.OrchestrationUsageSessionResult{{CLI: "codex", WorkerSlot: "codex-b", SessionID: "unrelated", Status: "complete"}}}); err != nil {
		t.Fatal(err)
	}
	conn := testBridgeConn(agent, 4)
	conn.capabilities.UsageLedger = true
	conn.capabilities.UsageModelRecovery = 2
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agent, conn)
	s.scheduleHistoricalUsageBackfill(agent)
	select {
	case env := <-conn.send:
		request, err := protocol.Decode[protocol.OrchestrationUsageSyncRequest](env)
		if err != nil || len(request.Recovery) != 1 || request.Recovery[0].InputTokens != 198150 {
			t.Fatalf("request=%+v err=%v", request, err)
		}
		marker := request.Recovery[0].RecoveryMarkerID()
		if err := st.ReplaceOrchestrationUsage(ctx, protocol.OrchestrationUsageSyncResult{RunID: run.ID, ScannedAt: time.Now().Unix(), Sessions: []protocol.OrchestrationUsageSessionResult{{CLI: "codex", WorkerSlot: "codex-a", SessionID: marker, Status: "unavailable"}}}); err != nil {
			t.Fatal(err)
		}
		syncs, err := st.ListOrchestrationUsageSyncs(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if modelRecoveryDue(request.Recovery, syncs, time.Now().Unix()) {
			t.Fatal("retry marker not persisted")
		}
		events, err := st.ListOrchestrationUsageEvents(ctx, run.ID)
		if err != nil || len(events) != 0 {
			t.Fatal("marker created tokens", events, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("existing sync skipped missing planner")
	}
}
