package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type usageOverviewPageResponse struct {
	Overview orchestrationUsageOverview `json:"overview"`
	Page     *usageOverviewPage         `json:"page"`
}

func readOverviewPage(t *testing.T, s *Server, uid, query string) usageOverviewPageResponse {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?"+query, nil), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("overview %s: %d %s", query, w.Code, w.Body.String())
	}
	var response usageOverviewPageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestUsageOverviewPagesEqualFullTotals(t *testing.T) {
	s, _, db := newUsageReadFixture(t)
	now := time.Now().Unix()
	old := now - 40*86400
	for _, query := range []string{
		`INSERT INTO users(id,username,password_hash,created_at) VALUES ('foreign','foreign','unused',1)`,
		`INSERT INTO agents(id,user_id,name,machine_id,last_seen_at) VALUES ('second-agent','usage-user','second','second-machine',1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	// All creation times tie, and updated times use the opposite order. The
	// empty first page and archived last page both belong to the traversal.
	for i, id := range []string{"a", "b", "c", "d", "e", "foreign"} {
		owner, agent := "usage-user", "usage-agent"
		if id == "foreign" {
			owner = "foreign"
		}
		if id == "c" {
			agent = "second-agent"
		}
		if _, err := db.Exec(`INSERT INTO orchestration_runs(id,user_id,agent_id,title,mode,prompt,max_turns,status,created_at,updated_at,archived_at) VALUES (?,?,?,?,'collaboration','prompt',2,'completed',?,?,?)`, id, owner, agent, id, old, now-int64(i), map[bool]int{true: 1}[id == "a"]); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []struct {
		run, data string
		at        int64
	}{
		{"a", `{"cli":"codex","model":"provider-model","inputTokens":12,"cacheReadTokens":2,"outputTokens":4,"native":true,"costKnown":true,"costSource":"provider","estimatedCostUsd":0.25}`, now},
		{"c", `{"cli":"claude","model":"unknown-model","inputTokens":5,"outputTokens":3,"native":true}`, now - 86400},
		{"d", `{"cli":"claude","model":"provider-model","inputTokens":7,"outputTokens":2,"native":true,"costKnown":true,"costSource":"provider","estimatedCostUsd":0.5}`, old},
		{"foreign", `{"inputTokens":999999,"outputTokens":999999}`, now},
	} {
		if _, err := db.Exec(`INSERT INTO orchestration_events(id,run_id,seq,kind,cli,data_json,created_at) VALUES (?,?,1,'turn.usage','codex',?,?)`, "event-"+event.run, event.run, event.data, event.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO orchestration_usage_syncs(run_id,cli,worker_slot,session_id,status,event_count,scanned_at) VALUES ('b','codex','codex-a','session','complete',3,?)`, now); err != nil {
		t.Fatal(err)
	}
	for i, at := range []int64{now, old, 0} {
		if _, err := db.Exec(`INSERT INTO orchestration_usage_events(run_id,cli,worker_slot,session_id,event_id,occurred_at,model,input_tokens,cache_read_tokens,cache_write_tokens,output_tokens,reasoning_tokens) VALUES ('b','codex','codex-a','session',?,?,'unknown-model',8,2,1,3,0)`, fmt.Sprint(i), at); err != nil {
			t.Fatal(err)
		}
	}
	for _, days := range []int{0, 30} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			query := fmt.Sprintf("days=%d&timezoneOffset=-480", days)
			full := readOverviewPage(t, s, "usage-user", query)
			if full.Page != nil {
				t.Fatal("legacy caller must still receive complete data without page metadata")
			}
			combined := orchestrationUsageOverview{Days: days, TimezoneOffset: -480, CostKnown: true, Items: []orchestrationUsageOverviewItem{}, Trend: []orchestrationUsageTrendPoint{}}
			trend := map[string]*orchestrationUsageTrendPoint{}
			machines, seen, cursors := map[string]bool{}, map[string]bool{}, map[string]bool{}
			cursor, scanned, pages := "", 0, 0
			for {
				response := readOverviewPage(t, s, "usage-user", query+"&limit=1&cursor="+url.QueryEscape(cursor))
				if response.Page == nil || response.Page.ScannedRuns != 1 {
					t.Fatalf("invalid source page: %+v", response.Page)
				}
				pages++
				if pages == 1 && (len(response.Overview.Items) != 0 || !response.Page.HasMore) {
					t.Fatal("empty first source page must advance")
				}
				scanned += response.Page.ScannedRuns
				part := response.Overview
				combined.InputTokens += part.InputTokens
				combined.OutputTokens += part.OutputTokens
				combined.CacheTokens += part.CacheTokens
				combined.TotalTokens += part.TotalTokens
				combined.EstimatedCostUSD += part.EstimatedCostUSD
				combined.CallCount += part.CallCount
				combined.Runs += part.Runs
				for _, item := range part.Items {
					if seen[item.RunID] {
						t.Fatal("run counted twice")
					}
					seen[item.RunID] = true
					machines[item.MachineID] = true
					combined.CostKnown = combined.CostKnown && item.CostKnown
					combined.Items = append(combined.Items, item)
				}
				for _, point := range part.Trend {
					target := overviewTrendPoint(trend, point.Date)
					target.InputTokens += point.InputTokens
					target.OutputTokens += point.OutputTokens
					target.CacheTokens += point.CacheTokens
					target.TotalTokens += point.TotalTokens
					target.EstimatedCostUSD += point.EstimatedCostUSD
					target.CallCount += point.CallCount
					target.CostKnown = target.CostKnown && point.CostKnown
				}
				if !response.Page.HasMore {
					if response.Page.NextCursor != "" {
						t.Fatal("final page has a cursor")
					}
					break
				}
				cursor = response.Page.NextCursor
				if cursor == "" || cursors[cursor] || pages > 5 {
					t.Fatal("pagination failed to progress")
				}
				cursors[cursor] = true
			}
			if scanned != 5 || !seen["a"] || !seen["c"] || seen["foreign"] {
				t.Fatalf("archive/ownership/old-run recent usage changed: scanned=%d seen=%v", scanned, seen)
			}
			combined.Machines = len(machines)
			for _, point := range trend {
				combined.Trend = append(combined.Trend, *point)
			}
			normalize := func(v *orchestrationUsageOverview) {
				sort.Slice(v.Items, func(i, j int) bool { return v.Items[i].RunID < v.Items[j].RunID })
				sort.Slice(v.Trend, func(i, j int) bool { return v.Trend[i].Date < v.Trend[j].Date })
			}
			normalize(&combined)
			normalize(&full.Overview)
			if !reflect.DeepEqual(combined, full.Overview) {
				t.Fatalf("paged sum differs from full overview:\ngot %+v\nwant %+v", combined, full.Overview)
			}
		})
	}
}

func TestUsageOverviewPageCursorAndFailureContract(t *testing.T) {
	s, st, db := newUsageReadFixture(t)
	for i := 0; i < 4; i++ {
		createOrchestrationRun(t, st, "usage-user", "usage-agent")
	}
	first := readOverviewPage(t, s, "usage-user", "days=30&limit=1")
	cursor := url.QueryEscape(first.Page.NextCursor)
	for _, test := range []struct{ owner, query string }{
		{"usage-user", "limit=0"}, {"usage-user", "limit=-1"}, {"usage-user", "limit=abc"},
		{"usage-user", "cursor=" + cursor},
		{"usage-user", "limit=1&cursor=malformed"},
		{"usage-user", "days=7&limit=1&cursor=" + cursor},
		{"usage-user", "days=30&timezoneOffset=1&limit=1&cursor=" + cursor},
		{"foreign", "days=30&limit=1&cursor=" + cursor},
	} {
		w := httptest.NewRecorder()
		s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?"+test.query, nil), test.owner)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), `"overview"`) || strings.Contains(w.Body.String(), `"page"`) {
			t.Fatalf("invalid query returned page: %s -> %d %s", test.query, w.Code, w.Body.String())
		}
	}
	clamped := readOverviewPage(t, s, "usage-user", "days=0&limit=100")
	if clamped.Page.ScannedRuns != 3 || !clamped.Page.HasMore {
		t.Fatalf("unbounded page: %+v", clamped.Page)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?limit=1", nil).WithContext(ctx), "usage-user")
	if w.Code != http.StatusRequestTimeout || strings.Contains(w.Body.String(), `"page"`) {
		t.Fatalf("canceled page succeeded: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Exec(`DROP TABLE orchestration_events`); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handleUsageOverview(w, httptest.NewRequest(http.MethodGet, "/api/usage/overview?limit=1", nil), "usage-user")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), `"page"`) || strings.Contains(w.Body.String(), `"overview"`) {
		t.Fatalf("failed page succeeded: %d %s", w.Code, w.Body.String())
	}
}

func TestUsageOverviewCursorKeepsInitialCutoff(t *testing.T) {
	first, err := parseUsageOverviewPage(httptest.NewRequest(http.MethodGet, "/?limit=1", nil), 30, -480, 100)
	if err != nil {
		t.Fatal(err)
	}
	page := first.response("owner-bound-store-cursor", 1)
	next, err := parseUsageOverviewPage(httptest.NewRequest(http.MethodGet, "/?limit=1&cursor="+url.QueryEscape(page.NextCursor), nil), 30, -480, 100+86400)
	if err != nil || next.Cutoff != 100 {
		t.Fatalf("cutoff changed across midnight: %+v %v", next, err)
	}
	for _, saved := range []usageOverviewCursor{
		{Version: 2, History: "cursor", Days: 30, Cutoff: 100},
		{Version: 1, Days: 30, Cutoff: 100},
		{Version: 1, History: "cursor", Days: 30, Cutoff: 0},
		{Version: 1, History: "cursor", Days: 30, Cutoff: -1},
	} {
		raw, _ := json.Marshal(saved)
		cursor := base64.RawURLEncoding.EncodeToString(raw)
		if _, err := parseUsageOverviewPage(httptest.NewRequest(http.MethodGet, "/?limit=1&cursor="+cursor, nil), 30, 0, 100); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
}
