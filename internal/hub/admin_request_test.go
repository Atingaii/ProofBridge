package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdminReadsHonorCancellationAndDeadline(t *testing.T) {
	s, _, _ := newUsageReadFixture(t)
	for name, handler := range map[string]func(http.ResponseWriter, *http.Request, string){
		"overview": s.handleAdminUsage,
		"detail":   s.handleAdminUserUsage,
		"content":  s.handleAdminConversationContent,
	} {
		for _, deadline := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/canceled", true: "/deadline"}[deadline], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				status, code := http.StatusRequestTimeout, "USAGE_CANCELED"
				if deadline {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					status, code = http.StatusGatewayTimeout, "USAGE_TIMEOUT"
				} else {
					cancel()
				}
				defer cancel()
				r := httptest.NewRequest(http.MethodGet, "/api/admin/usage?days=0", nil).WithContext(ctx)
				r.SetPathValue("userID", "usage-user")
				r.SetPathValue("kind", "orchestration")
				r.SetPathValue("conversationID", "not-found")
				w := httptest.NewRecorder()
				handler(w, r, "usage-user")
				if w.Code != status || !strings.Contains(w.Body.String(), code) {
					t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), status, code)
				}
				for _, key := range []string{`"overview"`, `"detail"`, `"conversation"`} {
					if strings.Contains(w.Body.String(), key) {
						t.Fatalf("interrupted read returned successful payload: %s", w.Body.String())
					}
				}
			})
		}
	}
}
