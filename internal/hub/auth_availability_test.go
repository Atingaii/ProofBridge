package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthUserReadFailuresDoNotInvalidateSession(t *testing.T) {
	s, st := newAuthTestServer(t)
	user, err := st.AuthenticateUser(context.Background(), "admin", "secret12345")
	if err != nil {
		t.Fatal(err)
	}
	handlers := map[string]http.HandlerFunc{
		"me": s.withAuth(s.handleMe),
		"admin": s.withAdmin(func(w http.ResponseWriter, _ *http.Request, _ string) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	request := func(t *testing.T, handler http.HandlerFunc, ctx context.Context, uid string, status int, code string) {
		t.Helper()
		token, _, err := s.signer.Sign(uid)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		r.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != status || !strings.Contains(w.Body.String(), code) {
			t.Fatalf("status/body = %d %s; want %d %s", w.Code, w.Body.String(), status, code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("read failure must not clear or replace the session cookie")
		}
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			request(t, handler, context.Background(), "deleted-user", http.StatusUnauthorized, "INVALID_TOKEN")
			expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			request(t, handler, expired, user.ID, http.StatusGatewayTimeout, "AUTH_TIMEOUT")
			canceled, cancelRequest := context.WithCancel(context.Background())
			cancelRequest()
			request(t, handler, canceled, user.ID, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE")
			// A successful retry uses exactly the same valid account and token.
			want := http.StatusOK
			if name == "admin" {
				want = http.StatusNoContent
			}
			request(t, handler, context.Background(), user.ID, want, "")
		})
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for name, handler := range handlers {
		t.Run(name+"/database-unavailable", func(t *testing.T) {
			request(t, handler, context.Background(), user.ID, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE")
		})
	}
}
