package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestOrchestrationHistoryHTTPMetadataAndFilters(t *testing.T) {
	s, st, uid, agent := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, uid, agent)
	patch := func(user, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/orchestrations/"+run.ID, strings.NewReader(body))
		req.SetPathValue("runID", run.ID)
		w := httptest.NewRecorder()
		s.handleUpdateOrchestration(w, req, user)
		return w
	}
	for _, body := range []string{`{}`, `{"title":" "}`, `{"archived":"true"}`, `{"title":3}`, `{"unexpected":true}`, `{"title":"ok"} {}`, `{"title":null}`, `null`} {
		if w := patch(uid, body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid patch %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := patch("other", `{"title":"no"}`); w.Code != http.StatusNotFound {
		t.Fatalf("foreign update: %d", w.Code)
	}
	if w := patch(uid, `{"title":" no partial rename ","archived":true}`); w.Code != http.StatusConflict {
		t.Fatalf("active archive: %d %s", w.Code, w.Body.String())
	}
	loaded, _ := st.OrchestrationRunByID(context.Background(), run.ID, uid)
	if loaded.Title != run.Title {
		t.Fatal("failed archive partially renamed the run")
	}
	if err := st.UpdateOrchestrationRunStatus(context.Background(), run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	w := patch(uid, `{"title":" renamed ✓ ","archived":true}`)
	var result struct {
		Run store.OrchestrationRun `json:"run"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Run.Title != "renamed ✓" || result.Run.ArchivedAt == 0 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"limit=0", "limit=201", "limit=no", "archived=maybe", "status=no", "cursor=garbage"} {
		w := httptest.NewRecorder()
		s.handleListOrchestrations(w, httptest.NewRequest(http.MethodGet, "/api/orchestrations?"+query, nil), uid)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid filter %s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"", "archived=true", "archived=all&status=completed&agentId=" + agent} {
		w := httptest.NewRecorder()
		s.handleListOrchestrations(w, httptest.NewRequest(http.MethodGet, "/api/orchestrations?"+query, nil), uid)
		var page store.OrchestrationHistoryPage
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Runs == nil {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		want := 1
		if query == "" {
			want = 0
		}
		if len(page.Runs) != want {
			t.Fatalf("query %s returned %d", query, len(page.Runs))
		}
	}
	if w := patch(uid, `{"archived":false}`); w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
}
