package providerprobe

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidateInferenceResponse(t *testing.T) {
	for _, tc := range []struct {
		name, cli, body string
		ok              bool
	}{
		{"html", "codex", "<!doctype html><title>New API</title>", false},
		{"empty", "codex", "", false},
		{"unrelated", "codex", `{"success":true}`, false},
		{"null", "codex", `null`, false},
		{"error", "codex", `{"error":{"message":"secret-do-not-echo"}}`, false},
		{"completed", "codex", `{"id":"r","object":"response","status":"completed","error":null,"output":[]}`, true},
		{"budget_limited", "codex", `{"id":"r","object":"response","status":"incomplete","output":[]}`, true},
		{"failed", "codex", `{"id":"r","object":"response","status":"failed","output":[]}`, false},
		{"missing_output", "codex", `{"id":"r","object":"response","status":"completed"}`, false},
		{"null_output", "codex", `{"id":"r","object":"response","status":"completed","output":null}`, false},
		{"chat_completions", "codex", `{"id":"r","object":"chat.completion","choices":[]}`, false},
		{"messages", "claude", `{"id":"m","type":"message","content":[]}`, true},
		{"wrong_protocol", "claude", `{"id":"r","object":"response","output":[]}`, false},
		{"trailing_html", "codex", `{"id":"r","object":"response","status":"completed","output":[]}<html>`, false},
		{"oversize", "codex", strings.Repeat(" ", maxResponseBytes+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}
			err := ValidateInferenceResponse(r, tc.cli)
			if (err == nil) != tc.ok {
				t.Fatalf("validation = %v, want success %v", err, tc.ok)
			}
			if err != nil && strings.Contains(err.Error(), "secret-do-not-echo") {
				t.Fatal("upstream body leaked")
			}
		})
	}
}
