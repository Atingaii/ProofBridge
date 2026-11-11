package store

import (
	"context"
	"reflect"
	"testing"
)

func TestAdminContentSourcePagesKeepEmptyPagesAndTurnFragments(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "content", "owner", "agent-a", "content", 1)
	for i, body := range []string{"A", "B", "C", "hidden", "hidden", "hidden", "hidden"} {
		kind := "turn.delta"
		if i >= 3 {
			kind = "command.output"
		}
		if _, err := st.db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,role,source,turn_id,content,data_json,created_at) VALUES (?, 'content', ?, ?, 'worker','cli','same-turn',?,'{"secret":"never read"}',1)`, NewID("event"), i+1, kind, body); err != nil {
			t.Fatal(err)
		}
	}
	var cursor *int64
	var pieces []string
	for i, want := range []struct {
		count  int
		cursor int64
		more   bool
	}{{0, 6, true}, {0, 4, true}, {1, 2, true}, {1, 1, false}} {
		page, err := st.AdminConversationContent(context.Background(), "owner", "orchestration", "content", AdminConversationContentPage{Limit: 2, BeforeSeq: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != want.count || page.NextBeforeSeq != want.cursor || page.HasMoreBefore != want.more {
			t.Fatalf("page %d: %+v", i, page)
		}
		for _, item := range page.Items {
			pieces = append([]string{item.Content}, pieces...)
		}
		next := page.NextBeforeSeq
		cursor = &next
	}
	if !reflect.DeepEqual(pieces, []string{"A", "BC"}) {
		t.Fatalf("turn fragments lost or duplicated: %v", pieces)
	}
	if _, err := st.AdminConversationContent(context.Background(), "other", "orchestration", "content", AdminConversationContentPage{Limit: 2, BeforeSeq: cursor}); err != ErrNotFound {
		t.Fatalf("cross-owner page: %v", err)
	}
}

func TestAdminContentChatSameTimestampCursor(t *testing.T) {
	st := historyFixture(t)
	if _, err := st.db.Exec(`INSERT INTO sessions(id,agent_id,user_id,title,created_at,updated_at) VALUES ('chat','agent-a','owner','chat',1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, err := st.db.Exec(`INSERT INTO messages(id,session_id,role,content,usage_json,created_at) VALUES (?,'chat','user',?,'{"private":"secret"}',0)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	pageOptions := AdminConversationContentPage{Limit: 2}
	for i, want := range [][]string{{"d", "e"}, {"b", "c"}, {"a"}} {
		page, err := st.AdminConversationContent(context.Background(), "owner", "chat", "chat", pageOptions)
		if err != nil {
			t.Fatal(err)
		}
		var actual []string
		for _, item := range page.Items {
			actual = append(actual, item.Content)
		}
		if !reflect.DeepEqual(actual, want) || page.HasMoreBefore != (i < 2) || page.NextBeforeCreatedAt != 0 {
			t.Fatalf("page %d: %+v", i, page)
		}
		timestamp := page.NextBeforeCreatedAt
		pageOptions.BeforeCreatedAt = &timestamp
		pageOptions.BeforeID = page.NextBeforeID
	}
}

func TestAdminContentPageDefaultAndMaximum(t *testing.T) {
	st := historyFixture(t)
	insertHistoryRun(t, st, "many", "owner", "agent-a", "many", 1)
	if _, err := st.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1105)
 INSERT INTO orchestration_events(id,run_id,seq,kind,content,created_at) SELECT 'many-'||i,'many',i,'user.message','body',1 FROM n`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		limit, want int
		cursor      int64
	}{{0, 200, 906}, {10000, 1000, 106}} {
		page, err := st.AdminConversationContent(context.Background(), "owner", "orchestration", "many", AdminConversationContentPage{Limit: tc.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != tc.want || !page.HasMoreBefore || page.NextBeforeSeq != tc.cursor {
			t.Fatalf("limit %d: items=%d cursor=%d more=%v", tc.limit, len(page.Items), page.NextBeforeSeq, page.HasMoreBefore)
		}
	}
}
