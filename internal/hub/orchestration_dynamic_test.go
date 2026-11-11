package hub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestDynamicExecutionNormalization(t *testing.T) {
	s, _, _, _ := newOrchestrationTestServer(t)
	for _, tc := range []struct {
		mode        string
		turns, want int
		valid       bool
	}{
		{"dynamic", 0, 0, true}, {"dynamic", 12, 0, true}, {"dynamic", 999, 0, true},
		{"", 0, 2, true}, {"bounded", 999, 12, true}, {"infinite-typo", 0, 0, false},
	} {
		w := httptest.NewRecorder()
		got, ok := s.normalizeOrchestrationStart(w, orchestrationStartRequest{ExecutionMode: tc.mode, MaxTurns: tc.turns, Prompt: "work"})
		if ok != tc.valid || ok && got.MaxTurns != tc.want {
			t.Fatalf("%+v => %+v valid=%v body=%s", tc, got, ok, w.Body.String())
		}
	}
}

func TestDynamicGraphAdvancesBeyondLegacyLimitAndStopsOnVerdict(t *testing.T) {
	for _, terminal := range []string{"verified-early", "stalled", "new-instruction"} {
		t.Run(terminal, func(t *testing.T) {
			s, st, userID, agentID := newOrchestrationTestServer(t)
			run, err := st.CreateOrchestrationRun(context.Background(), store.CreateOrchestrationRunParams{UserID: userID, AgentID: agentID, Mode: "collaboration", ExecutionMode: "dynamic", Prompt: "finish"})
			if err != nil {
				t.Fatal(err)
			}
			conn := testBridgeConn(agentID, 8)
			s.pool.RegisterAgent(conn)
			defer s.pool.UnregisterAgent(agentID, conn)
			payload := protocol.OrchestrationStartPayload{RunID: run.ID, ExecutionMode: "dynamic", Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex, Prompt: "finish", TaskPrompt: "finish", Round: 12}
			if err := s.createAndDispatchTaskGraph(context.Background(), run, payload); err != nil {
				t.Fatal(err)
			}
			finalRound := 13
			if terminal == "new-instruction" {
				finalRound = 14
			}
			for round := 12; round <= finalRound; round++ {
				for position := 0; position < 4; position++ {
					var env protocol.Envelope
					select {
					case env = <-conn.send:
					default:
						t.Fatalf("stopped at round %d position %d", round, position)
					}
					sent := decodeTaskDispatchPayload(t, env)
					if sent.ExecutionMode != "dynamic" || sent.TaskGraph.MaxRounds != 0 || sent.TaskGraph.Round != round || sent.MaxTurns != 1 {
						t.Fatalf("unexpected dispatch: %+v", sent)
					}
					if round == 14 && !strings.Contains(sent.TaskPrompt, "also verify new requirement") {
						t.Fatalf("new contract missing: %s", sent.TaskPrompt)
					}
					task := sent.TaskGraph.Tasks[0]
					ref := &protocol.TaskAttemptRef{GraphID: sent.TaskGraph.ID, TaskID: task.ID, AttemptID: task.AttemptID, Role: task.Role, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest}
					s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{RunID: run.ID, Kind: "run.start", Task: ref}))
					end := protocol.OrchestrationEventPayload{RunID: run.ID, Kind: "run.end", Task: ref, Content: "progress"}
					if terminal == "new-instruction" && round == 13 && position == 3 {
						m, _, err := st.CreateOrchestrationMessage(context.Background(), store.OrchestrationMessage{ID: "late-contract", RunID: run.ID, Prompt: "also verify new requirement", DeliveryMode: "immediate"})
						if err != nil {
							t.Fatal(err)
						}
						execID := run.ID + ":" + task.AttemptID
						if _, err := st.ClaimOrchestrationMessage(context.Background(), run.ID, m.ID, execID); err != nil {
							t.Fatal(err)
						}
						if _, err := st.FinishOrchestrationMessage(context.Background(), run.ID, m.ID, execID, "delivered", ""); err != nil {
							t.Fatal(err)
						}
						end.RunEndData = &protocol.RunEndData{TerminalReason: "verified-early", VerifierVerdict: &protocol.VerifierVerdict{Status: "pass", Reason: "old contract"}}
					}
					if round == finalRound && position == 3 {
						verdict := "continue"
						if terminal == "verified-early" || terminal == "new-instruction" {
							verdict = "pass"
						}
						reason := terminal
						if reason == "new-instruction" {
							reason = "verified-early"
						}
						end.RunEndData = &protocol.RunEndData{TerminalReason: reason, VerifierVerdict: &protocol.VerifierVerdict{Status: verdict, Reason: "check"}}
					}
					s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", end))
				}
			}
			loaded, err := st.OrchestrationRunByID(context.Background(), run.ID, userID)
			if err != nil || loaded.Status != store.OrchestrationCompleted || loaded.ExecutionMode != "dynamic" {
				t.Fatalf("run=%+v err=%v", loaded, err)
			}
			select {
			case unexpected := <-conn.send:
				t.Fatalf("dispatched after terminal verdict: %+v", unexpected)
			default:
			}
		})
	}
}

func TestBoundedGraphRemainsBounded(t *testing.T) {
	raw, _ := json.Marshal(protocol.OrchestrationStartPayload{Round: 2, MaxRounds: 2, MaxTurns: 2})
	_, advance, err := nextTaskGraphPayload(store.OrchestrationTaskGraph{PayloadJSON: string(raw)}, store.OrchestrationRun{}, protocol.OrchestrationEventPayload{})
	if err != nil || advance {
		t.Fatalf("legacy limit bypassed: advance=%v err=%v", advance, err)
	}
}
