package bridge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderProbeRejectsWebsiteFallback(t *testing.T) {
	for _, inferenceOK := range []bool{true, false} {
		t.Run(fmt.Sprint(inferenceOK), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
				case "/v1/responses":
					if !inferenceOK {
						w.WriteHeader(400)
						fmt.Fprint(w, `{"error":{"message":"unsupported model"}}`)
						return
					}
					fmt.Fprint(w, `{"id":"r","object":"response","status":"completed","output":[]}`)
				default:
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, "<!doctype html><title>New API</title>")
				}
			}))
			defer s.Close()
			for _, suffix := range []string{"", "/v1"} {
				base, _, _, _, err := probeProvider(context.Background(), "codex", s.URL+suffix, "test-model", "test-key")
				if inferenceOK {
					if err != nil || base != s.URL+"/v1" {
						t.Fatalf("base=%q err=%v", base, err)
					}
				} else if err == nil || base != "" || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "HTML") {
					t.Fatalf("failed inference must not select website: base=%q err=%v", base, err)
				}
			}
		})
	}
}
