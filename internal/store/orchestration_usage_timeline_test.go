package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestUsageTimelineDecoderMatchesLegacy(t *testing.T) {
	for _, payload := range []string{
		``, ` `, `null`, `{}`, `[]`, `invalid`, `{"round":`,
		`{"round":2,"model":"legacy","inputTokens":13,"native":true}`,
		`{"extra":null,"runStartData":null}`, `{"extra":{}}`,
		`{"extra":{"round":3},"round":7,"turnStartData":{"round":4,"model":"typed","promptText":"long prompt"},"task":{"round":5,"workerSlot":"codex-a"}}`,
		`{"extra":{"round":3},"turnStartData":{"round":"invalid","model":"typed"}}`,
		`{"extra":{"round":3},"turnStartData":{"promptText":42}}`,
		`{"extra":{"round":3},"task":42}`,
		`{"extra":42,"runStartData":{"round":3}}`,
		`{"extra":{"category":"native-identity","nativeSessionId":"session","model":"model"},"bridgeNoteData":{"category":"native-identity"}}`,
		`{"runEndData":{"codexThreadIds":{"codex-a":"session"},"claudeSessionId":"claude","nativeResume":[{"cli":"codex","id":"session","workerSlot":"codex-a"}]}}`,
		`{"commandData":{"command":"echo test"},"runConclusion":{"summary":"end"},"turnEndData":{"durationMs":123}}`,
		`{"extra":{"round":2},"extra":{"round":3}}`,
		`{"extra":{"round":2},"extra":null}`,
	} {
		for _, kind := range []string{"turn.start", "turn.end", "run.start", "turn.usage", "bridge.note"} {
			event := OrchestrationEvent{Kind: kind, Status: "warning", Content: "retained content"}
			got, want := decodeOrchestrationEvent(event, payload), legacyUsageDecode(event, payload)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s / %s: got %+v, want %+v", kind, payload, got, want)
			}
		}
	}
}

func TestUsageTimelineBatchesReleaseConnectionAndKeepHighWater(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "usage", "owner", "agent-a", "usage", 1)
	const count = 145
	for i := 1; i <= count; i++ {
		data := `{"extra":{"round":1},"turnStartData":{"model":"model","promptText":"small"}}`
		if i == 1 {
			data = `{"extra":{"round":1},"turnStartData":{"promptText":"` + strings.Repeat("x", orchestrationUsageBatchBytes+1) + `"}}`
		}
		if _, err := st.db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,data_json,created_at) VALUES (?, 'usage', ?, 'turn.start', ?, 1)`, fmt.Sprint(i), i*3, data); err != nil {
			t.Fatal(err)
		}
	}
	var seen []int64
	batches := 0
	err := st.readOrchestrationUsageTimeline(context.Background(), "usage", func(batch []rawOrchestrationUsageEvent) error {
		batches++
		if len(batch) > orchestrationUsageBatchRows || (batches == 1 && len(batch) != 1) {
			t.Fatalf("row/byte batch bound ignored: batch=%d rows=%d", batches, len(batch))
		}
		// This real lookup on the same one-connection Store would deadlock if
		// rows remained open during decoding/consumption.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := st.UserByID(ctx, "owner"); err != nil {
			return fmt.Errorf("batch held the single connection: %w", err)
		}
		if batches == 1 {
			if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: "usage", Kind: "run.end"}); err != nil {
				return err
			}
		}
		for _, raw := range batch {
			seen = append(seen, raw.event.Seq)
			if got, want := decodeOrchestrationEvent(raw.event, raw.dataJSON), legacyUsageDecode(raw.event, raw.dataJSON); !reflect.DeepEqual(got, want) {
				t.Fatal("batched decoding changed event")
			}
		}
		return nil
	})
	if err != nil || batches < 4 || len(seen) != count {
		t.Fatalf("incomplete/unbounded timeline: rows=%d batches=%d err=%v", len(seen), batches, err)
	}
	for i, seq := range seen {
		if seq != int64((i+1)*3) {
			t.Fatalf("lost/repeated event %d: seq=%d", i, seq)
		}
	}
	all, err := st.ListOrchestrationUsageTimeline(context.Background(), "usage")
	if err != nil || len(all) != count+1 || all[count].Kind != "run.end" {
		t.Fatalf("next read must include appended terminal: rows=%d err=%v", len(all), err)
	}
}

func TestUsageTimelineBatchFailureDoesNotPublishPartialResult(t *testing.T) {
	for _, failure := range []string{"cancel", "delete", "store"} {
		t.Run(failure, func(t *testing.T) {
			st := historyFixture(t)
			insertHistoryRun(t, st, "usage", "owner", "agent-a", "usage", 1)
			if _, err := st.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<130) INSERT INTO orchestration_events(id,run_id,seq,kind,data_json,created_at) SELECT 'e'||i,'usage',i,'turn.usage','{"inputTokens":1}',1 FROM n`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			batches := 0
			err := st.readOrchestrationUsageTimeline(ctx, "usage", func(batch []rawOrchestrationUsageEvent) error {
				batches++
				switch failure {
				case "cancel":
					cancel()
				case "delete":
					_, err := st.db.Exec(`DELETE FROM orchestration_runs WHERE id='usage'`)
					return err
				case "store":
					_, err := st.db.Exec(`DROP TABLE orchestration_events`)
					return err
				}
				return nil
			})
			if err == nil || batches != 1 || (failure == "cancel" && !errors.Is(err, context.Canceled)) || (failure == "delete" && !errors.Is(err, ErrNotFound)) {
				t.Fatalf("partial read succeeded: batches=%d err=%v", batches, err)
			}
			if failure == "cancel" || failure == "store" {
				rows, err := st.ListOrchestrationUsageTimeline(ctx, "usage")
				if err == nil || rows != nil {
					t.Fatalf("failed public read returned data: %v %v", rows, err)
				}
			}
		})
	}
}

func TestUsageTimelineBatchUsesExistingIndexes(t *testing.T) {
	st := historyFixture(t)
	for _, query := range []struct {
		sql, index string
		args       []any
	}{
		{orchestrationUsageBatchQuery, "idx_orchestration_events_usage", []any{"usage", 0, 1000000, orchestrationUsageBatchRows}},
		{`SELECT COALESCE(MAX(seq),0) FROM orchestration_events WHERE run_id = ?`, "COVERING INDEX idx_orchestration_events_run_seq", []any{"usage"}},
	} {
		rows, err := st.db.Query(`EXPLAIN QUERY PLAN `+query.sql, query.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plans []string
		for rows.Next() {
			var a, b, c int
			var plan string
			if err := rows.Scan(&a, &b, &c, &plan); err != nil {
				t.Fatal(err)
			}
			plans = append(plans, plan)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !strings.Contains(strings.Join(plans, " "), query.index) {
			t.Fatalf("unexpected query plan: %v %v", plans, err)
		}
		t.Log(plans)
	}
}

func BenchmarkUsageTimelineLargePayload(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "usage.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	// A disposable minimal copy of the event schema avoids benchmarking
	// unrelated migrations. The indexes and query columns match production.
	for _, q := range []string{
		`CREATE TABLE orchestration_events(id TEXT,run_id TEXT,seq INTEGER,kind TEXT,source TEXT,severity TEXT,role TEXT,cli TEXT,turn_id TEXT,content TEXT,status TEXT,error TEXT,data_json TEXT,created_at INTEGER)`,
		`CREATE INDEX idx_orchestration_events_run_seq ON orchestration_events(run_id,seq)`,
		`CREATE INDEX idx_orchestration_events_usage ON orchestration_events(run_id,seq) WHERE ` + orchestrationUsageTimelinePredicate,
	} {
		if _, err := st.db.Exec(q); err != nil {
			b.Fatal(err)
		}
	}
	payload := `{"extra":{"round":2},"turnStartData":{"round":2,"workerSlot":"codex-a","model":"model","promptText":"` + strings.Repeat("x", 9600) + `"}}`
	if _, err := st.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1516) INSERT INTO orchestration_events(id,run_id,seq,kind,content,data_json,created_at) SELECT 'e'||i,'usage',i,'turn.start','summary',?,1 FROM n`, payload); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"legacy-held-connection", "batched-release-before-decode"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "batched-release-before-decode" {
					events, err := st.ListOrchestrationUsageTimeline(context.Background(), "usage")
					if err != nil || len(events) != 1516 {
						b.Fatalf("read: %d %v", len(events), err)
					}
					continue
				}
				rows, err := st.db.Query(orchestrationUsageBatchQuery, "usage", 0, 1516, 1516)
				if err != nil {
					b.Fatal(err)
				}
				var events []OrchestrationEvent
				for rows.Next() {
					var e OrchestrationEvent
					var data string
					if err := rows.Scan(&e.ID, &e.RunID, &e.Seq, &e.Kind, &e.Source, &e.Severity, &e.Role, &e.CLI, &e.TurnID, &e.Content, &e.Status, &e.Error, &data, &e.CreatedAt); err != nil {
						b.Fatal(err)
					}
					events = append(events, legacyUsageDecode(e, data))
				}
				err = rows.Err()
				rows.Close()
				if err != nil || len(events) != 1516 {
					b.Fatalf("legacy read: %d %v", len(events), err)
				}
			}
		})
	}
}

// Frozen pre-batching decoder is the compatibility oracle, including fallback
// behavior for malformed typed fields and legacy top-level maps.
func legacyUsageDecode(event OrchestrationEvent, dataJSON string) OrchestrationEvent {
	if strings.TrimSpace(dataJSON) != "" {
		_ = json.Unmarshal([]byte(dataJSON), &event.Data)
		var typed struct {
			Extra          map[string]any           `json:"extra,omitempty"`
			CommandData    *protocol.CommandData    `json:"commandData,omitempty"`
			RunStartData   *protocol.RunStartData   `json:"runStartData,omitempty"`
			TurnStartData  *protocol.TurnStartData  `json:"turnStartData,omitempty"`
			TurnEndData    *protocol.TurnEndData    `json:"turnEndData,omitempty"`
			RunEndData     *protocol.RunEndData     `json:"runEndData,omitempty"`
			BridgeNoteData *protocol.BridgeNoteData `json:"bridgeNoteData,omitempty"`
			RunConclusion  *protocol.RunConclusion  `json:"runConclusion,omitempty"`
			Task           *protocol.TaskAttemptRef `json:"task,omitempty"`
		}
		if err := json.Unmarshal([]byte(dataJSON), &typed); err == nil && (typed.Extra != nil ||
			typed.CommandData != nil || typed.RunStartData != nil || typed.TurnStartData != nil || typed.TurnEndData != nil ||
			typed.RunEndData != nil || typed.BridgeNoteData != nil || typed.RunConclusion != nil || typed.Task != nil) {
			event.Data = typed.Extra
			event.CommandData = typed.CommandData
			event.RunStartData = typed.RunStartData
			event.TurnStartData = typed.TurnStartData
			event.TurnEndData = typed.TurnEndData
			event.RunEndData = typed.RunEndData
			event.BridgeNoteData = typed.BridgeNoteData
			event.RunConclusion = typed.RunConclusion
			event.Task = typed.Task
		}
	}
	event.Source = normalizeOrchestrationEventSource(event.Source, event.Kind)
	if event.Severity == "" {
		event.Severity = severityFromLegacyStatus(event.Status)
		// turn.end carries a semantic lifecycle status (success/error/warning),
		// not a log level; keep it so error guards and timelines stay correct.
		if event.Severity != "" && event.Kind != "turn.end" {
			event.Status = ""
		}
	}
	return event
}
