package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestCompletionPlanningOnceAndFailureFeedback(t *testing.T) {
	for _, mode := range []string{"collaboration", "debate"} {
		t.Run(mode, func(t *testing.T) {
			s, st, uid, agentID := newOrchestrationTestServer(t)
			run := createOrchestrationRun(t, st, uid, agentID)
			conn := testBridgeConn(agentID, 16)
			s.pool.RegisterAgent(conn)
			defer s.pool.UnregisterAgent(agentID, conn)
			payload := protocol.OrchestrationStartPayload{RunID: run.ID, Mode: mode, WorkerPair: protocol.WorkerPairCodexCodex, Prompt: "prove sort", TaskPrompt: "prove sort", MaxTurns: 2, PlanWorkspace: true, CWD: t.TempDir(), Context: strings.Repeat("older context ", 6000)}
			if err := s.createAndDispatchTaskGraph(context.Background(), run, payload); err != nil {
				t.Fatal(err)
			}
			for pos := 0; pos < 10; pos++ {
				var env protocol.Envelope
				select {
				case env = <-conn.send:
				default:
					t.Fatalf("no dispatch at position %d", pos)
				}
				p := decodeTaskDispatchPayload(t, env)
				task := p.TaskGraph.Tasks[0]
				if pos >= 6 {
					if p.Round != 2 || task.Role == store.TaskRolePlanner || task.Role == store.TaskRolePlanReviewer {
						t.Fatalf("planning repeated: %#v", p)
					}
					if len(p.CompletionHistory) != 1 || p.CompletionHistory[0].SourceDigest != "source" || !strings.Contains(p.Context, "missing sortedness") || !strings.Contains(p.Context, "prove sortedness P1") {
						t.Fatalf("lost plan, blocker or observation: context=%s history=%#v", p.Context, p.CompletionHistory)
					}
				}
				ref := protocol.TaskAttemptRef{GraphID: p.TaskGraph.ID, TaskID: task.ID, AttemptID: task.AttemptID, Role: task.Role, Name: task.Name, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest}
				s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{RunID: run.ID, Kind: "run.start", Task: &ref}))
				end := protocol.OrchestrationEventPayload{RunID: run.ID, Kind: "run.end", Content: "node complete", Task: &ref}
				if task.Role == store.TaskRolePlanReviewer {
					end.Content = "[PLAN_ITEM id=\"P1\" status=\"pending\"] prove sortedness P1"
				}
				if task.Role == store.TaskRoleReviewer {
					end.RunConclusion = &protocol.RunConclusion{Outcome: "satisfied", Summary: "worker claims done"}
					end.RunEndData = &protocol.RunEndData{TerminalReason: "max-turns", VerifierVerdict: &protocol.VerifierVerdict{Status: "continue", Reason: "missing sortedness"}, CompletionObservation: &protocol.CompletionObservation{Round: p.Round, SourceDigest: "source"}}
					if pos == 9 {
						end.RunEndData.TerminalReason = "verified-early"
						end.RunEndData.VerifierVerdict.Status = "pass"
					}
				}
				s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", end))
			}
			loaded, err := st.OrchestrationRunByID(context.Background(), run.ID, uid)
			if err != nil || loaded.Status != store.OrchestrationCompleted || len(conn.send) != 0 {
				t.Fatalf("unexpected terminal state: %#v err=%v queued=%d", loaded, err, len(conn.send))
			}
		})
	}
}

func TestCompletionHistoryAndTerminalGuards(t *testing.T) {
	p := protocol.OrchestrationStartPayload{Round: 9, MaxRounds: 12}
	for i := 1; i < 9; i++ {
		p.CompletionHistory = append(p.CompletionHistory, protocol.CompletionObservation{Round: i})
	}
	raw, _ := json.Marshal(p)
	g := store.OrchestrationTaskGraph{PayloadJSON: string(raw)}
	for _, test := range []struct {
		reason, verdict string
		advance         bool
	}{
		{"verified-early", "pass", false}, {"verified-early", "continue", true},
		{"verified-early", "", true}, {"stalled", "continue", false}, {"max-turns", "continue", true},
	} {
		e := protocol.OrchestrationEventPayload{RunEndData: &protocol.RunEndData{TerminalReason: test.reason, CompletionObservation: &protocol.CompletionObservation{Round: 9}}}
		if test.verdict != "" {
			e.RunEndData.VerifierVerdict = &protocol.VerifierVerdict{Status: test.verdict}
		}
		next, advance, err := nextTaskGraphPayload(g, store.OrchestrationRun{}, e)
		if err != nil || advance != test.advance {
			t.Fatalf("%#v: advance=%v err=%v", test, advance, err)
		}
		if advance && (len(next.CompletionHistory) != 8 || next.CompletionHistory[0].Round != 2 || next.CompletionHistory[7].Round != 9) {
			t.Fatalf("history not bounded/persisted: %#v", next.CompletionHistory)
		}
	}
}

func TestIndependentCompletionPersistsInBothModes(t *testing.T) {
	for _, mode := range []string{"collaboration", "debate"} {
		for _, reason := range []string{"verified-early", "stalled"} {
			t.Run(mode+"/"+reason, func(t *testing.T) {
				ctx := context.Background()
				s, st, uid, agentID := newOrchestrationTestServer(t)
				run := createOrchestrationRun(t, st, uid, agentID)
				conn := testBridgeConn(agentID, 16)
				s.pool.RegisterAgent(conn)
				defer s.pool.UnregisterAgent(agentID, conn)
				p := protocol.OrchestrationStartPayload{RunID: run.ID, Mode: mode, WorkerPair: protocol.WorkerPairCodexCodex, Prompt: "prove sort_correct", TaskPrompt: "prove sort_correct", MaxTurns: 12, PlanWorkspace: true, CWD: t.TempDir()}
				if err := s.createAndDispatchTaskGraph(ctx, run, p); err != nil {
					t.Fatal(err)
				}
				for pos := 0; pos < 6; pos++ {
					var env protocol.Envelope
					select {
					case env = <-conn.send:
					default:
						t.Fatalf("no dispatch at %d", pos)
					}
					ref := decodeTaskDispatch(t, env)
					finish := reason == "verified-early" && ref.Role == store.TaskRoleWorker || reason == "stalled" && ref.Role == store.TaskRoleReviewer
					if !finish {
						sendTaskLifecycle(s, run.ID, ref, nil)
						continue
					}
					verdict, outcome := "pass", "satisfied"
					if reason == "stalled" {
						verdict, outcome = "continue", "completed"
					}
					e := protocol.OrchestrationEventPayload{RunID: run.ID, Task: &ref, Kind: "run.end", RunEndData: &protocol.RunEndData{TerminalReason: reason, VerifierVerdict: &protocol.VerifierVerdict{Status: verdict, Reason: "sort_correct unproved"}}}
					s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", e))
					// Duplicate terminal delivery must not open another generation.
					s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", e))
					loaded, err := st.OrchestrationRunByID(ctx, run.ID, uid)
					if err != nil || loaded.Status != store.OrchestrationCompleted || len(conn.send) != 0 {
						t.Fatalf("state=%#v err=%v queued=%d", loaded, err, len(conn.send))
					}
					events, err := st.ListOrchestrationEvents(ctx, run.ID, 100)
					if err != nil {
						t.Fatal(err)
					}
					count := 0
					for _, event := range events {
						if event.Kind == "run.end" && event.Task == nil {
							count++
							if event.RunConclusion == nil || event.RunConclusion.Outcome != outcome || event.RunConclusion.VerificationStatus != verdict {
								t.Fatalf("lost conclusion: %#v", event)
							}
							if reason == "stalled" && !strings.Contains(event.RunConclusion.Summary, "sort_correct unproved") {
								t.Fatal("lost actual check record")
							}
						}
					}
					if count != 1 {
						t.Fatalf("terminal events=%d", count)
					}
					return
				}
				t.Fatal("completion boundary never reached")
			})
		}
	}
}

func TestLegacyCompletionErrorIsCompletedAndDoesNotResume(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	graph := createHubTaskGraph(t, st, run)
	conn := testBridgeConn(agentID, 8)
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)
	if err := s.dispatchReadyTaskGraph(context.Background(), run, graph); err != nil {
		t.Fatal(err)
	}
	for pos := 0; pos < 4; pos++ {
		ref := decodeTaskDispatch(t, <-conn.send)
		if pos < 3 {
			sendTaskLifecycle(s, run.ID, ref, nil)
			continue
		}
		e := protocol.OrchestrationEventPayload{RunID: run.ID, Task: &ref, Kind: "run.error", Error: "stable blocker", RunEndData: &protocol.RunEndData{TerminalReason: "stalled"}, RunConclusion: &protocol.RunConclusion{Outcome: "blocked"}}
		s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", e))
		// Late delivery must not resurrect or complete a stopped task.
		e.Kind = "run.end"
		s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", e))
	}
	loaded, err := st.OrchestrationRunByID(context.Background(), run.ID, uid)
	if err != nil || loaded.Status != store.OrchestrationCompleted || len(conn.send) != 0 {
		t.Fatalf("stopped state=%#v err=%v", loaded, err)
	}
	events, err := st.ListOrchestrationEvents(context.Background(), run.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Task == nil && e.Kind == "run.end" {
			found = e.RunConclusion != nil && e.RunConclusion.Outcome == "completed" && e.RunEndData != nil && e.RunEndData.TerminalReason == "stalled"
		}
	}
	if !found {
		t.Fatal("missing public completed terminal event")
	}
}
