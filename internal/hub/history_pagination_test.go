package hub

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestOrchestrationEventsPageBoundsAndForwardCatchup(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	for i := 1; i <= 1005; i++ {
		kind := "turn.delta"
		if i == 1005 {
			kind = "run.end"
		}
		if _, err := st.AddOrchestrationEvent(context.Background(), store.OrchestrationEvent{RunID: run.ID, Seq: int64(i * 2), Kind: kind, Content: "event"}); err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/orchestrations/" + run.ID + "/events"
	for _, tc := range []struct {
		query         string
		count         int
		first, last   float64
		before, after bool
	}{
		{"", 200, 1612, 2010, true, false},
		{"?limit=10000", 1000, 12, 2010, true, false},
		{"?afterSeq=0&limit=1000", 1000, 2, 2000, false, true},
		{"?afterSeq=2000&limit=1000", 5, 2002, 2010, false, false},
		{"?beforeSeq=12&limit=1000", 5, 2, 10, false, false},
		{"?afterSeq=2008&limit=1", 1, 2010, 2010, false, false},
	} {
		body := getJSON(t, s, uid, base+tc.query, http.StatusOK)
		events, ok := body["events"].([]any)
		if !ok || len(events) != tc.count || events[0].(map[string]any)["seq"] != tc.first || events[len(events)-1].(map[string]any)["seq"] != tc.last || body["hasMoreBefore"] != tc.before || body["hasMoreAfter"] != tc.after {
			t.Fatalf("%s: events=%d before=%v after=%v", tc.query, len(events), body["hasMoreBefore"], body["hasMoreAfter"])
		}
		if tc.last == 2010 && events[len(events)-1].(map[string]any)["kind"] != "run.end" {
			t.Fatal("catchup lost terminal event")
		}
	}
}

func TestAdminConversationContentPaginationHTTP(t *testing.T) {
	s, st, uid, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agentID)
	for i := 0; i < 3; i++ {
		if _, err := st.AddOrchestrationEvent(context.Background(), store.OrchestrationEvent{RunID: run.ID, Kind: "command.output", Content: "must not display"}); err != nil {
			t.Fatal(err)
		}
	}
	base := fmt.Sprintf("/api/admin/users/%s/conversations/orchestration/%s", uid, run.ID)
	body := getJSON(t, s, uid, base+"?limit=2", http.StatusOK)
	content := body["conversation"].(map[string]any)
	if len(content["items"].([]any)) != 0 || content["hasMoreBefore"] != true || content["nextBeforeSeq"] != float64(2) {
		t.Fatalf("empty visible page lost source cursor: %+v", content)
	}
	body = getJSON(t, s, uid, base+"?limit=2&beforeSeq=2", http.StatusOK)
	content = body["conversation"].(map[string]any)
	if content["hasMoreBefore"] != false || content["nextBeforeSeq"] != float64(1) {
		t.Fatalf("last page: %+v", content)
	}
	for _, query := range []string{"?beforeSeq=-1", "?beforeCreatedAt=1&beforeId=a"} {
		getJSON(t, s, uid, base+query, http.StatusBadRequest)
	}
	session, err := st.CreateSession(context.Background(), uid, agentID, "chat")
	if err != nil {
		t.Fatal(err)
	}
	chat := fmt.Sprintf("/api/admin/users/%s/conversations/chat/%s", uid, session.ID)
	for _, query := range []string{"?beforeSeq=1", "?beforeCreatedAt=1", "?beforeId=a"} {
		getJSON(t, s, uid, chat+query, http.StatusBadRequest)
	}
}
